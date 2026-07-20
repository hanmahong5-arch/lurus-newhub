package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	entity "github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// ─── PrefillGroup: GetPrefillGroups / CreatePrefillGroup / UpdatePrefillGroup / DeletePrefillGroup ───
//
// These v1-style handlers read no gin-context identity keys at all — they are
// plain global CRUD over repo.PrefillGroup — so the router under test needs no
// auth middleware. The PrefillGroup table isn't in SetupV2TestRouter's default
// migration list, so each test AutoMigrates it onto ctx.DB itself.

func TestCoverUpliftPresets_GetPrefillGroups_FilterByType(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.PrefillGroup{}); err != nil {
		t.Fatalf("automigrate PrefillGroup: %v", err)
	}

	seed := []repo.PrefillGroup{
		{Name: "Quick A", Type: "quick_action", UpdatedTime: 100},
		{Name: "Quick B", Type: "quick_action", UpdatedTime: 200},
		{Name: "Other C", Type: "other_type", UpdatedTime: 300},
	}
	for i := range seed {
		if err := ctx.DB.Create(&seed[i]).Error; err != nil {
			t.Fatalf("seed prefill group %d: %v", i, err)
		}
	}

	r := gin.New()
	r.GET("/prefill-groups", GetPrefillGroups)

	w := V2Request(r, "GET", "/prefill-groups?type=quick_action", nil, nil)
	resp := AssertV2Success(t, w)
	items, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("expected data array, got %T (body %s)", resp["data"], w.Body.String())
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 quick_action groups, got %d: %v", len(items), items)
	}
	first := items[0].(map[string]interface{})
	if first["name"].(string) != "Quick B" {
		t.Fatalf("expected the most-recently-updated group first (Quick B), got %v", first["name"])
	}
	for _, it := range items {
		m := it.(map[string]interface{})
		if m["type"].(string) != "quick_action" {
			t.Fatalf("type filter leaked a non-matching row: %v", m)
		}
	}
}

func TestCoverUpliftPresets_GetPrefillGroups_NoTypeFilterMatch(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.PrefillGroup{}); err != nil {
		t.Fatalf("automigrate PrefillGroup: %v", err)
	}
	if err := ctx.DB.Create(&repo.PrefillGroup{Name: "Solo", Type: "solo_type", UpdatedTime: 1}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	r := gin.New()
	r.GET("/prefill-groups", GetPrefillGroups)

	w := V2Request(r, "GET", "/prefill-groups?type=does-not-exist", nil, nil)
	resp := AssertV2Success(t, w)
	items, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("expected data array (possibly empty), got %T (body %s)", resp["data"], w.Body.String())
	}
	if len(items) != 0 {
		t.Fatalf("expected zero groups for a non-matching type filter, got %d", len(items))
	}
}

func TestCoverUpliftPresets_CreatePrefillGroup_Happy(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.PrefillGroup{}); err != nil {
		t.Fatalf("automigrate PrefillGroup: %v", err)
	}

	r := gin.New()
	r.POST("/prefill-groups", CreatePrefillGroup)

	body := map[string]interface{}{
		"name": "Fresh Group",
		"type": "quick_action",
	}
	w := V2Request(r, "POST", "/prefill-groups", body, nil)
	resp := AssertV2Success(t, w)
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, body: %s", w.Body.String())
	}
	if data["name"].(string) != "Fresh Group" {
		t.Fatalf("expected created name echoed back, got %v", data["name"])
	}
	newID := int(data["id"].(float64))
	if newID == 0 {
		t.Fatalf("expected a nonzero assigned id, got %v", data["id"])
	}

	var stored repo.PrefillGroup
	if err := ctx.DB.First(&stored, newID).Error; err != nil {
		t.Fatalf("expected created group to exist in DB: %v", err)
	}
	if stored.Type != "quick_action" {
		t.Fatalf("expected stored type quick_action, got %q", stored.Type)
	}
	if stored.CreatedTime == 0 || stored.UpdatedTime == 0 {
		t.Fatalf("expected CreatedTime/UpdatedTime to be stamped, got created=%d updated=%d", stored.CreatedTime, stored.UpdatedTime)
	}
}

func TestCoverUpliftPresets_CreatePrefillGroup_MissingFields(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.PrefillGroup{}); err != nil {
		t.Fatalf("automigrate PrefillGroup: %v", err)
	}

	r := gin.New()
	r.POST("/prefill-groups", CreatePrefillGroup)

	w := V2Request(r, "POST", "/prefill-groups", map[string]interface{}{"name": "", "type": ""}, nil)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Fatalf("expected success=false for empty name/type, body: %s", w.Body.String())
	}
	if resp["message"].(string) != "组名称和类型不能为空" {
		t.Fatalf("unexpected message: %v", resp["message"])
	}

	var count int64
	ctx.DB.Model(&repo.PrefillGroup{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected no row created on validation failure, got %d", count)
	}
}

func TestCoverUpliftPresets_CreatePrefillGroup_DuplicateName(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.PrefillGroup{}); err != nil {
		t.Fatalf("automigrate PrefillGroup: %v", err)
	}
	if err := ctx.DB.Create(&repo.PrefillGroup{Name: "Dup", Type: "quick_action", UpdatedTime: 1}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	r := gin.New()
	r.POST("/prefill-groups", CreatePrefillGroup)

	w := V2Request(r, "POST", "/prefill-groups", map[string]interface{}{"name": "Dup", "type": "other"}, nil)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Fatalf("expected success=false for a duplicate name, body: %s", w.Body.String())
	}
	if resp["message"].(string) != "组名称已存在" {
		t.Fatalf("unexpected message: %v", resp["message"])
	}

	var count int64
	ctx.DB.Model(&repo.PrefillGroup{}).Where("name = ?", "Dup").Count(&count)
	if count != 1 {
		t.Fatalf("expected still exactly 1 row named Dup, got %d", count)
	}
}

func TestCoverUpliftPresets_UpdatePrefillGroup_Happy(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.PrefillGroup{}); err != nil {
		t.Fatalf("automigrate PrefillGroup: %v", err)
	}
	seed := repo.PrefillGroup{Name: "Orig", Type: "quick_action", Description: "before", UpdatedTime: 1}
	if err := ctx.DB.Create(&seed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	r := gin.New()
	r.PUT("/prefill-groups", UpdatePrefillGroup)

	body := map[string]interface{}{
		"id":          seed.Id,
		"name":        "Orig",
		"type":        "quick_action",
		"description": "after",
	}
	w := V2Request(r, "PUT", "/prefill-groups", body, nil)
	AssertV2Success(t, w)

	var stored repo.PrefillGroup
	if err := ctx.DB.First(&stored, seed.Id).Error; err != nil {
		t.Fatalf("reload updated group: %v", err)
	}
	if stored.Description != "after" {
		t.Fatalf("expected description updated to 'after', got %q", stored.Description)
	}
	if stored.UpdatedTime <= 1 {
		t.Fatalf("expected UpdatedTime to be bumped past the seed value, got %d", stored.UpdatedTime)
	}
}

func TestCoverUpliftPresets_UpdatePrefillGroup_MissingID(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.PrefillGroup{}); err != nil {
		t.Fatalf("automigrate PrefillGroup: %v", err)
	}

	r := gin.New()
	r.PUT("/prefill-groups", UpdatePrefillGroup)

	w := V2Request(r, "PUT", "/prefill-groups", map[string]interface{}{"name": "X", "type": "Y"}, nil)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Fatalf("expected success=false when id is missing, body: %s", w.Body.String())
	}
	if resp["message"].(string) != "缺少组 ID" {
		t.Fatalf("unexpected message: %v", resp["message"])
	}
}

func TestCoverUpliftPresets_UpdatePrefillGroup_DuplicateName(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.PrefillGroup{}); err != nil {
		t.Fatalf("automigrate PrefillGroup: %v", err)
	}
	a := repo.PrefillGroup{Name: "Alpha", Type: "t", UpdatedTime: 1}
	b := repo.PrefillGroup{Name: "Beta", Type: "t", UpdatedTime: 2}
	if err := ctx.DB.Create(&a).Error; err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if err := ctx.DB.Create(&b).Error; err != nil {
		t.Fatalf("seed b: %v", err)
	}

	r := gin.New()
	r.PUT("/prefill-groups", UpdatePrefillGroup)

	// Rename Beta to Alpha's name -> must conflict with the other row Alpha.
	body := map[string]interface{}{"id": b.Id, "name": "Alpha", "type": "t"}
	w := V2Request(r, "PUT", "/prefill-groups", body, nil)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Fatalf("expected success=false renaming into a duplicate name, body: %s", w.Body.String())
	}
	if resp["message"].(string) != "组名称已存在" {
		t.Fatalf("unexpected message: %v", resp["message"])
	}

	var stored repo.PrefillGroup
	if err := ctx.DB.First(&stored, b.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.Name != "Beta" {
		t.Fatalf("expected Beta's name unchanged after the rejected rename, got %q", stored.Name)
	}
}

func TestCoverUpliftPresets_DeletePrefillGroup_Happy(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.PrefillGroup{}); err != nil {
		t.Fatalf("automigrate PrefillGroup: %v", err)
	}
	seed := repo.PrefillGroup{Name: "ToDelete", Type: "t", UpdatedTime: 1}
	if err := ctx.DB.Create(&seed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	r := gin.New()
	r.DELETE("/prefill-groups/:id", DeletePrefillGroup)

	w := V2Request(r, "DELETE", fmt.Sprintf("/prefill-groups/%d", seed.Id), nil, nil)
	AssertV2Success(t, w)

	var stillVisible int64
	ctx.DB.Model(&repo.PrefillGroup{}).Where("id = ?", seed.Id).Count(&stillVisible)
	if stillVisible != 0 {
		t.Fatalf("expected the soft-deleted row hidden from the default scope, still visible count=%d", stillVisible)
	}

	var stored repo.PrefillGroup
	if err := ctx.DB.Unscoped().First(&stored, seed.Id).Error; err != nil {
		t.Fatalf("expected the soft-deleted row to still exist unscoped: %v", err)
	}
	if !stored.DeletedAt.Valid {
		t.Fatalf("expected DeletedAt to be set after delete")
	}
}

func TestCoverUpliftPresets_DeletePrefillGroup_InvalidID(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.PrefillGroup{}); err != nil {
		t.Fatalf("automigrate PrefillGroup: %v", err)
	}

	r := gin.New()
	r.DELETE("/prefill-groups/:id", DeletePrefillGroup)

	w := V2Request(r, "DELETE", "/prefill-groups/not-a-number", nil, nil)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Fatalf("expected success=false for a non-numeric id, body: %s", w.Body.String())
	}
}

// ─── SwitchPreset: ListSwitchPresets / CreateSwitchPreset ───

func TestCoverUpliftPresets_ListSwitchPresets_FilterOfficialOnly(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.SwitchConfigPresetRow{}); err != nil {
		t.Skipf("SwitchConfigPresetRow migration unsupported on this SQLite build: %v", err)
	}

	bg := context.Background()
	if _, err := repo.CreateSwitchConfigPreset(bg, "lumen", "Official One", "d1", "creative", map[string]interface{}{"model": "gpt-4o"}, true); err != nil {
		t.Fatalf("seed official preset: %v", err)
	}
	if _, err := repo.CreateSwitchConfigPreset(bg, "lumen", "Unofficial One", "d2", "creative", map[string]interface{}{"model": "gpt-3.5"}, false); err != nil {
		t.Fatalf("seed unofficial preset: %v", err)
	}
	if _, err := repo.CreateSwitchConfigPreset(bg, "otherTool", "Official Other Tool", "d3", "creative", map[string]interface{}{"model": "x"}, true); err != nil {
		t.Fatalf("seed other-tool preset: %v", err)
	}

	r := gin.New()
	r.GET("/switch/presets", ListSwitchPresets)

	w := V2Request(r, "GET", "/switch/presets?tool=lumen", nil, nil)
	resp := AssertV2Success(t, w)
	items, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("expected data array, got %T (body %s)", resp["data"], w.Body.String())
	}
	if len(items) != 1 {
		t.Fatalf("expected only the 1 official lumen preset (is_official filter + tool filter), got %d: %v", len(items), items)
	}
	got := items[0].(map[string]interface{})
	if got["name"].(string) != "Official One" {
		t.Fatalf("expected Official One, got %v", got["name"])
	}
	if got["is_official"].(bool) != true {
		t.Fatalf("expected is_official=true, got %v", got["is_official"])
	}
	cfg, ok := got["config_json"].(map[string]interface{})
	if !ok || cfg["model"] != "gpt-4o" {
		t.Fatalf("expected config_json round-trip with model gpt-4o, got %v", got["config_json"])
	}
	if int(resp["page"].(float64)) != 1 {
		t.Fatalf("expected default page=1, got %v", resp["page"])
	}
	if int(resp["limit"].(float64)) != 20 {
		t.Fatalf("expected default page_size=20 as limit, got %v", resp["limit"])
	}
}

func TestCoverUpliftPresets_ListSwitchPresets_NoMatch(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.SwitchConfigPresetRow{}); err != nil {
		t.Skipf("SwitchConfigPresetRow migration unsupported on this SQLite build: %v", err)
	}

	bg := context.Background()
	if _, err := repo.CreateSwitchConfigPreset(bg, "lumen", "Some Preset", "d", "creative", map[string]interface{}{"a": 1}, true); err != nil {
		t.Fatalf("seed: %v", err)
	}

	r := gin.New()
	r.GET("/switch/presets", ListSwitchPresets)

	w := V2Request(r, "GET", "/switch/presets?tool=does-not-exist", nil, nil)
	resp := AssertV2Success(t, w)
	items, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("expected data array (possibly empty), got %T (body %s)", resp["data"], w.Body.String())
	}
	if len(items) != 0 {
		t.Fatalf("expected zero presets for a non-matching tool, got %d", len(items))
	}
}

func TestCoverUpliftPresets_CreateSwitchPreset_Happy(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.SwitchConfigPresetRow{}); err != nil {
		t.Skipf("SwitchConfigPresetRow migration unsupported on this SQLite build: %v", err)
	}

	r := gin.New()
	r.POST("/switch/presets", CreateSwitchPreset)

	body := map[string]interface{}{
		"tool":        "codex",
		"name":        "Codex Preset",
		"description": "test preset",
		"category":    "coding",
		"config_json": map[string]interface{}{"temperature": 0.2},
		"is_official": true,
	}
	w := V2Request(r, "POST", "/switch/presets", body, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d (body %s)", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); !success {
		t.Fatalf("expected success=true, body: %s", w.Body.String())
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, body: %s", w.Body.String())
	}
	if data["name"].(string) != "Codex Preset" {
		t.Fatalf("expected name Codex Preset, got %v", data["name"])
	}

	var count int64
	ctx.DB.Model(&repo.SwitchConfigPresetRow{}).Where("name = ? AND tool = ?", "Codex Preset", "codex").Count(&count)
	if count != 1 {
		t.Fatalf("expected exactly 1 persisted row for the created preset, got %d", count)
	}
}

func TestCoverUpliftPresets_CreateSwitchPreset_MissingRequiredField(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.SwitchConfigPresetRow{}); err != nil {
		t.Skipf("SwitchConfigPresetRow migration unsupported on this SQLite build: %v", err)
	}

	r := gin.New()
	r.POST("/switch/presets", CreateSwitchPreset)

	// config_json carries `binding:"required"` and is omitted here.
	body := map[string]interface{}{
		"tool": "codex",
		"name": "Incomplete Preset",
	}
	w := V2Request(r, "POST", "/switch/presets", body, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for missing config_json, got %d (body %s)", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Fatalf("expected success=false, body: %s", w.Body.String())
	}

	var count int64
	ctx.DB.Model(&repo.SwitchConfigPresetRow{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected no row persisted on validation failure, got %d", count)
	}
}

// ─── SecureVerification: GetVerificationStatus / CheckSecureVerification ───
//
// Both handlers only read gin context id + gin-contrib/sessions state, so the
// router under test is a minimal engine with a real cookie session store and
// a stub middleware fixing the caller's id. A test-only helper route stamps
// the session directly (bypassing UniversalVerify, which is exercised by its
// own existing tests) so these two functions can be driven in isolation.

func buildPresetsSecureVerifyRouter(userId int, setID bool) *gin.Engine {
	r := gin.New()
	store := cookie.NewStore([]byte("cover-uplift-presets-secret"))
	r.Use(sessions.Sessions("session", store))
	r.Use(func(c *gin.Context) {
		if setID {
			c.Set("id", userId)
		}
		c.Next()
	})
	r.GET("/api/verify/status", GetVerificationStatus)
	r.POST("/api/test/set-session", func(c *gin.Context) {
		var reqBody struct {
			Value int64 `json:"value"`
			AsInt bool  `json:"as_int"`
		}
		_ = c.ShouldBindJSON(&reqBody)
		session := sessions.Default(c)
		if reqBody.AsInt {
			session.Set(SecureVerificationSessionKey, int(reqBody.Value))
		} else {
			session.Set(SecureVerificationSessionKey, reqBody.Value)
		}
		if err := session.Save(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	r.GET("/api/test/check", func(c *gin.Context) {
		verified := CheckSecureVerification(c)
		c.JSON(http.StatusOK, gin.H{"verified": verified})
	})
	return r
}

func presetsDo(r *gin.Engine, method, path string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func presetsSetSession(t *testing.T, r *gin.Engine, value int64, asInt bool) []*http.Cookie {
	t.Helper()
	payload, _ := json.Marshal(map[string]interface{}{"value": value, "as_int": asInt})
	req := httptest.NewRequest(http.MethodPost, "/api/test/set-session", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set-session test helper failed: status=%d body=%s", w.Code, w.Body.String())
	}
	return w.Result().Cookies()
}

func TestCoverUpliftPresets_GetVerificationStatus_Unauthenticated(t *testing.T) {
	r := buildPresetsSecureVerifyRouter(0, false)
	w := presetsDo(r, http.MethodGet, "/api/verify/status", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when the context carries no id, got %d (body %s)", w.Code, w.Body.String())
	}
}

func TestCoverUpliftPresets_GetVerificationStatus_NotVerified(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	r := buildPresetsSecureVerifyRouter(ctx.NormalUser.Id, true)
	w := presetsDo(r, http.MethodGet, "/api/verify/status", nil)
	resp := AssertV2Success(t, w)
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, body: %s", w.Body.String())
	}
	if data["verified"].(bool) {
		t.Fatalf("expected verified=false with no session stamp, got %v", data["verified"])
	}
	if data["totp_enrolled"].(bool) {
		t.Fatalf("expected totp_enrolled=false with no TOTP enrollment, got %v", data["totp_enrolled"])
	}
	if _, present := data["expires_at"]; present {
		t.Fatalf("expected expires_at omitted (omitempty) when not verified, got %v", data["expires_at"])
	}
}

func TestCoverUpliftPresets_GetVerificationStatus_Verified(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	r := buildPresetsSecureVerifyRouter(ctx.NormalUser.Id, true)
	now := time.Now().Unix()
	cookies := presetsSetSession(t, r, now, false)

	w := presetsDo(r, http.MethodGet, "/api/verify/status", cookies)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	if !data["verified"].(bool) {
		t.Fatalf("expected verified=true right after stamping the session, body: %s", w.Body.String())
	}
	wantExpiry := float64(now + SecureVerificationTimeout)
	if data["expires_at"].(float64) != wantExpiry {
		t.Fatalf("expected expires_at=%v, got %v", wantExpiry, data["expires_at"])
	}
}

func TestCoverUpliftPresets_GetVerificationStatus_Expired(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	r := buildPresetsSecureVerifyRouter(ctx.NormalUser.Id, true)
	stale := time.Now().Unix() - SecureVerificationTimeout - 10
	cookies := presetsSetSession(t, r, stale, false)

	w := presetsDo(r, http.MethodGet, "/api/verify/status", cookies)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	if data["verified"].(bool) {
		t.Fatalf("expected verified=false for a stamp older than the timeout, body: %s", w.Body.String())
	}
	if _, present := data["expires_at"]; present {
		t.Fatalf("expected expires_at omitted for expired verification, got %v", data["expires_at"])
	}

	// The handler must have cleared the stale key from the session store; a
	// follow-up read carrying the refreshed cookie must still report false.
	freshCookies := w.Result().Cookies()
	if len(freshCookies) > 0 {
		w2 := presetsDo(r, http.MethodGet, "/api/verify/status", freshCookies)
		resp2 := AssertV2Success(t, w2)
		data2 := resp2["data"].(map[string]interface{})
		if data2["verified"].(bool) {
			t.Fatalf("expected verified to stay false after the stale session key was cleared")
		}
	}
}

func TestCoverUpliftPresets_GetVerificationStatus_TotpEnrolled(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if err := repo.UpsertUserTOTP(&entity.UserTOTP{
		UserId:          ctx.NormalUser.Id,
		SecretEncrypted: "enc-secret",
		Enabled:         true,
		CreatedAt:       common.GetTimestamp(),
		ConfirmedAt:     common.GetTimestamp(),
	}); err != nil {
		t.Fatalf("seed TOTP enrollment: %v", err)
	}

	r := buildPresetsSecureVerifyRouter(ctx.NormalUser.Id, true)
	w := presetsDo(r, http.MethodGet, "/api/verify/status", nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	if !data["totp_enrolled"].(bool) {
		t.Fatalf("expected totp_enrolled=true after enrollment, got %v", data["totp_enrolled"])
	}
	if data["verified"].(bool) {
		t.Fatalf("enrollment alone must not imply a verified session, got %v", data["verified"])
	}
}

func TestCoverUpliftPresets_GetVerificationStatus_NonInt64SessionValue(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	r := buildPresetsSecureVerifyRouter(ctx.NormalUser.Id, true)
	// Stored as a plain int (not int64): exercises the failed type-assertion branch.
	cookies := presetsSetSession(t, r, time.Now().Unix(), true)

	w := presetsDo(r, http.MethodGet, "/api/verify/status", cookies)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	if data["verified"].(bool) {
		t.Fatalf("expected verified=false when the stored session value isn't int64, got %v", data["verified"])
	}
}

func TestCoverUpliftPresets_CheckSecureVerification_NoSession(t *testing.T) {
	r := buildPresetsSecureVerifyRouter(1, true)
	w := presetsDo(r, http.MethodGet, "/api/test/check", nil)
	var body struct {
		Verified bool `json:"verified"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Verified {
		t.Fatalf("expected verified=false with no session at all, got true")
	}
}

func TestCoverUpliftPresets_CheckSecureVerification_Valid(t *testing.T) {
	r := buildPresetsSecureVerifyRouter(1, true)
	cookies := presetsSetSession(t, r, time.Now().Unix(), false)

	w := presetsDo(r, http.MethodGet, "/api/test/check", cookies)
	var body struct {
		Verified bool `json:"verified"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !body.Verified {
		t.Fatalf("expected verified=true right after stamping the session, body: %s", w.Body.String())
	}
}

func TestCoverUpliftPresets_CheckSecureVerification_Expired(t *testing.T) {
	r := buildPresetsSecureVerifyRouter(1, true)
	stale := time.Now().Unix() - SecureVerificationTimeout - 10
	cookies := presetsSetSession(t, r, stale, false)

	w := presetsDo(r, http.MethodGet, "/api/test/check", cookies)
	var body struct {
		Verified bool `json:"verified"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Verified {
		t.Fatalf("expected verified=false for an expired stamp, got true")
	}
}
