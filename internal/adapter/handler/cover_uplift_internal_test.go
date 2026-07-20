package handler

import (
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func doInternalGet(r *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// --- InternalGetUserLogs ---
// GET /internal/log/user/:id?page=&per_page=

func TestCoverUpliftInternal_GetUserLogs_Happy(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	target := ctx.NormalUser
	for i, model := range []string{"gpt-4o", "gpt-4o-mini", "claude-3"} {
		if err := ctx.DB.Create(&repo.Log{TenantId: ctx.TenantID, UserId: target.Id, Type: repo.LogTypeConsume, Content: "log", ModelName: model, Quota: 10 * (i + 1), CreatedAt: common.GetTimestamp() + int64(i)}).Error; err != nil {
			t.Fatalf("seed log %d: %v", i, err)
		}
	}
	// Log belonging to a different user must not leak into the result.
	if err := ctx.DB.Create(&repo.Log{TenantId: ctx.TenantID, UserId: ctx.AdminUser.Id, Type: repo.LogTypeConsume, Content: "other", ModelName: "other-model", CreatedAt: common.GetTimestamp()}).Error; err != nil {
		t.Fatalf("seed other-user log: %v", err)
	}

	r := gin.New()
	r.GET("/internal/log/user/:id", InternalGetUserLogs)

	w := doInternalGet(r, "/internal/log/user/"+idStr(target.Id))
	resp := ParseV2Response(t, w)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	if int(resp["total"].(float64)) != 3 {
		t.Fatalf("expected total=3, got %v", resp["total"])
	}
	if int(resp["page"].(float64)) != 1 || int(resp["per_page"].(float64)) != 20 {
		t.Fatalf("expected default page=1 per_page=20, got page=%v per_page=%v", resp["page"], resp["per_page"])
	}
	data, ok := resp["data"].([]interface{})
	if !ok || len(data) != 3 {
		t.Fatalf("expected 3 logs in data, got %v (body %s)", resp["data"], w.Body.String())
	}
	seenModels := map[string]bool{}
	for _, it := range data {
		m := it.(map[string]interface{})
		seenModels[m["model_name"].(string)] = true
	}
	for _, want := range []string{"gpt-4o", "gpt-4o-mini", "claude-3"} {
		if !seenModels[want] {
			t.Fatalf("expected model %q present, got %v", want, seenModels)
		}
	}
	if seenModels["other-model"] {
		t.Fatalf("must not leak another user's log, got %v", seenModels)
	}
}

func TestCoverUpliftInternal_GetUserLogs_Pagination(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	target := ctx.NormalUser
	for i := 0; i < 3; i++ {
		if err := ctx.DB.Create(&repo.Log{TenantId: ctx.TenantID, UserId: target.Id, Type: repo.LogTypeConsume, Content: "log", ModelName: "gpt-4o", CreatedAt: common.GetTimestamp() + int64(i)}).Error; err != nil {
			t.Fatalf("seed log %d: %v", i, err)
		}
	}

	r := gin.New()
	r.GET("/internal/log/user/:id", InternalGetUserLogs)

	// 3 rows, per_page=2 -> page 2 must hold exactly the remaining 1 row.
	w := doInternalGet(r, "/internal/log/user/"+idStr(target.Id)+"?page=2&per_page=2")
	resp := ParseV2Response(t, w)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	if int(resp["total"].(float64)) != 3 {
		t.Fatalf("expected total=3 regardless of page, got %v", resp["total"])
	}
	data, ok := resp["data"].([]interface{})
	if !ok || len(data) != 1 {
		t.Fatalf("expected exactly 1 row on page 2 of 2-per-page, got %v (body %s)", resp["data"], w.Body.String())
	}
}

func TestCoverUpliftInternal_GetUserLogs_InvalidID(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	_ = ctx

	r := gin.New()
	r.GET("/internal/log/user/:id", InternalGetUserLogs)

	w := doInternalGet(r, "/internal/log/user/not-a-number")
	if w.Code != 400 {
		t.Fatalf("expected 400 for non-numeric id, got %d body %s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	if resp["error"] == nil {
		t.Fatalf("expected error field, body %s", w.Body.String())
	}
}

func TestCoverUpliftInternal_GetUserLogs_UnknownUser(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	_ = ctx

	r := gin.New()
	r.GET("/internal/log/user/:id", InternalGetUserLogs)

	w := doInternalGet(r, "/internal/log/user/999999")
	if w.Code != 200 {
		t.Fatalf("expected 200 for a user with no logs, got %d body %s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	if int(resp["total"].(float64)) != 0 {
		t.Fatalf("expected total=0, got %v", resp["total"])
	}
	data, ok := resp["data"].([]interface{})
	if !ok || len(data) != 0 {
		t.Fatalf("expected empty data array, got %v (body %s)", resp["data"], w.Body.String())
	}
}

// --- InternalGetUserLogStat ---
// GET /internal/log/user/:id/stat?group_by=model|day

func TestCoverUpliftInternal_GetUserLogStat_Happy(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	target := ctx.NormalUser
	rows := []*repo.Log{
		{TenantId: ctx.TenantID, UserId: target.Id, Type: repo.LogTypeConsume, ModelName: "gpt-4", Quota: 10, CreatedAt: common.GetTimestamp()},
		{TenantId: ctx.TenantID, UserId: target.Id, Type: repo.LogTypeConsume, ModelName: "gpt-4", Quota: 20, CreatedAt: common.GetTimestamp() + 1},
		{TenantId: ctx.TenantID, UserId: target.Id, Type: repo.LogTypeConsume, ModelName: "claude-3", Quota: 5, CreatedAt: common.GetTimestamp() + 2},
		// Different user: must not pollute the stat.
		{TenantId: ctx.TenantID, UserId: ctx.AdminUser.Id, Type: repo.LogTypeConsume, ModelName: "gpt-4", Quota: 1000, CreatedAt: common.GetTimestamp()},
	}
	for i, row := range rows {
		if err := ctx.DB.Create(row).Error; err != nil {
			t.Fatalf("seed log %d: %v", i, err)
		}
	}

	r := gin.New()
	r.GET("/internal/log/user/:id/stat", InternalGetUserLogStat)

	w := doInternalGet(r, "/internal/log/user/"+idStr(target.Id)+"/stat?group_by=model")
	resp := ParseV2Response(t, w)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	data, ok := resp["data"].([]interface{})
	if !ok || len(data) != 2 {
		t.Fatalf("expected 2 grouped entries (gpt-4, claude-3), got %v (body %s)", resp["data"], w.Body.String())
	}
	byKey := map[string]map[string]interface{}{}
	for _, it := range data {
		m := it.(map[string]interface{})
		byKey[m["key"].(string)] = m
	}
	gpt4, ok := byKey["gpt-4"]
	if !ok {
		t.Fatalf("expected gpt-4 group, got keys %v", byKey)
	}
	if int(gpt4["count"].(float64)) != 2 {
		t.Fatalf("expected gpt-4 count=2 (hand-derived: 2 seeded rows for target user), got %v", gpt4["count"])
	}
	if int(gpt4["total_quota"].(float64)) != 30 {
		t.Fatalf("expected gpt-4 total_quota=30 (10+20), got %v", gpt4["total_quota"])
	}
	claude, ok := byKey["claude-3"]
	if !ok {
		t.Fatalf("expected claude-3 group, got keys %v", byKey)
	}
	if int(claude["count"].(float64)) != 1 {
		t.Fatalf("expected claude-3 count=1, got %v", claude["count"])
	}
	if int(claude["total_quota"].(float64)) != 5 {
		t.Fatalf("expected claude-3 total_quota=5, got %v", claude["total_quota"])
	}
}

func TestCoverUpliftInternal_GetUserLogStat_InvalidID(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	_ = ctx

	r := gin.New()
	r.GET("/internal/log/user/:id/stat", InternalGetUserLogStat)

	w := doInternalGet(r, "/internal/log/user/0/stat")
	if w.Code != 400 {
		t.Fatalf("expected 400 for id=0 (handler requires userID > 0), got %d body %s", w.Code, w.Body.String())
	}
}

func TestCoverUpliftInternal_GetUserLogStat_BadGroupByFallsBackToModel(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	target := ctx.NormalUser
	if err := ctx.DB.Create(&repo.Log{TenantId: ctx.TenantID, UserId: target.Id, Type: repo.LogTypeConsume, ModelName: "gpt-4", Quota: 7, CreatedAt: common.GetTimestamp()}).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}

	r := gin.New()
	r.GET("/internal/log/user/:id/stat", InternalGetUserLogStat)

	// group_by=bogus is neither "model" nor "day" -> handler falls back to "model".
	w := doInternalGet(r, "/internal/log/user/"+idStr(target.Id)+"/stat?group_by=bogus")
	resp := ParseV2Response(t, w)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	data, ok := resp["data"].([]interface{})
	if !ok || len(data) != 1 {
		t.Fatalf("expected 1 model-grouped entry, got %v (body %s)", resp["data"], w.Body.String())
	}
	m := data[0].(map[string]interface{})
	if m["key"].(string) != "gpt-4" {
		t.Fatalf("expected fallback grouping by model_name (key=gpt-4), got %v", m["key"])
	}
}

// --- InternalGetTokenLogs ---
// GET /internal/log/token/:token_id?page=&per_page=

func TestCoverUpliftInternal_GetTokenLogs_Happy(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "cover-uplift-token")
	otherTok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "other-token")

	if err := ctx.DB.Create(&repo.Log{TenantId: ctx.TenantID, UserId: ctx.NormalUser.Id, TokenId: tok.Id, Type: repo.LogTypeConsume, ModelName: "gpt-4o", CreatedAt: common.GetTimestamp()}).Error; err != nil {
		t.Fatalf("seed log1: %v", err)
	}
	if err := ctx.DB.Create(&repo.Log{TenantId: ctx.TenantID, UserId: ctx.NormalUser.Id, TokenId: tok.Id, Type: repo.LogTypeConsume, ModelName: "gpt-4o-mini", CreatedAt: common.GetTimestamp() + 1}).Error; err != nil {
		t.Fatalf("seed log2: %v", err)
	}
	// Different token must not leak into the result.
	if err := ctx.DB.Create(&repo.Log{TenantId: ctx.TenantID, UserId: ctx.NormalUser.Id, TokenId: otherTok.Id, Type: repo.LogTypeConsume, ModelName: "claude-3", CreatedAt: common.GetTimestamp()}).Error; err != nil {
		t.Fatalf("seed log3 (other token): %v", err)
	}

	r := gin.New()
	r.GET("/internal/log/token/:token_id", InternalGetTokenLogs)

	w := doInternalGet(r, "/internal/log/token/"+idStr(tok.Id))
	resp := ParseV2Response(t, w)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	if int(resp["total"].(float64)) != 2 {
		t.Fatalf("expected total=2, got %v", resp["total"])
	}
	data, ok := resp["data"].([]interface{})
	if !ok || len(data) != 2 {
		t.Fatalf("expected 2 logs for the token, got %v (body %s)", resp["data"], w.Body.String())
	}
	seenModels := map[string]bool{}
	for _, it := range data {
		m := it.(map[string]interface{})
		seenModels[m["model_name"].(string)] = true
	}
	if !seenModels["gpt-4o"] || !seenModels["gpt-4o-mini"] {
		t.Fatalf("expected both of this token's models, got %v", seenModels)
	}
	if seenModels["claude-3"] {
		t.Fatalf("must not leak another token's log, got %v", seenModels)
	}
}

func TestCoverUpliftInternal_GetTokenLogs_InvalidID(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	_ = ctx

	r := gin.New()
	r.GET("/internal/log/token/:token_id", InternalGetTokenLogs)

	w := doInternalGet(r, "/internal/log/token/-1")
	if w.Code != 400 {
		t.Fatalf("expected 400 for negative token id, got %d body %s", w.Code, w.Body.String())
	}
}

func TestCoverUpliftInternal_GetTokenLogs_UnknownToken(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	_ = ctx

	r := gin.New()
	r.GET("/internal/log/token/:token_id", InternalGetTokenLogs)

	w := doInternalGet(r, "/internal/log/token/999999")
	if w.Code != 200 {
		t.Fatalf("expected 200 for a token with no logs, got %d body %s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	if int(resp["total"].(float64)) != 0 {
		t.Fatalf("expected total=0, got %v", resp["total"])
	}
	data, ok := resp["data"].([]interface{})
	if !ok || len(data) != 0 {
		t.Fatalf("expected empty data array, got %v (body %s)", resp["data"], w.Body.String())
	}
}

// --- InternalGetModelCatalog ---
// GET /internal/models/catalog?group=default
//
// This handler reads only the in-process ratio_setting globals (no repo.DB
// access), so it is driven directly without SetupV2TestRouter.

func TestCoverUpliftInternal_GetModelCatalog_DefaultGroup(t *testing.T) {
	wantModels := ratio_setting.GetDefaultModelRatioMap()
	if len(wantModels) == 0 {
		t.Fatalf("test precondition broken: default model ratio map is empty")
	}
	wantGroupRatio := ratio_setting.GetGroupRatio("default")

	r := gin.New()
	r.GET("/internal/models/catalog", InternalGetModelCatalog)

	w := doInternalGet(r, "/internal/models/catalog")
	resp := ParseV2Response(t, w)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	if resp["group"].(string) != "default" {
		t.Fatalf("expected default group when unspecified, got %v", resp["group"])
	}
	if resp["group_ratio"].(float64) != wantGroupRatio {
		t.Fatalf("expected group_ratio=%v, got %v", wantGroupRatio, resp["group_ratio"])
	}
	data, ok := resp["data"].([]interface{})
	if !ok || len(data) != len(wantModels) {
		t.Fatalf("expected %d catalog entries (one per default model ratio), got %v (body %s)", len(wantModels), resp["data"], w.Body.String())
	}
	byID := map[string]map[string]interface{}{}
	for _, it := range data {
		m := it.(map[string]interface{})
		byID[m["id"].(string)] = m
	}
	entry, ok := byID["gpt-4-all"]
	if !ok {
		t.Fatalf("expected known model 'gpt-4-all' present in catalog, got ids %v", func() []string {
			ids := make([]string, 0, len(byID))
			for k := range byID {
				ids = append(ids, k)
			}
			return ids
		}())
	}
	if entry["model_ratio"].(float64) != wantModels["gpt-4-all"] {
		t.Fatalf("expected gpt-4-all model_ratio=%v, got %v", wantModels["gpt-4-all"], entry["model_ratio"])
	}
	if entry["group_ratio"].(float64) != wantGroupRatio {
		t.Fatalf("expected gpt-4-all group_ratio=%v, got %v", wantGroupRatio, entry["group_ratio"])
	}
	if entry["available"].(bool) != true {
		t.Fatalf("expected available=true, got %v", entry["available"])
	}
}

func TestCoverUpliftInternal_GetModelCatalog_CustomGroup(t *testing.T) {
	wantGroupRatio := ratio_setting.GetGroupRatio("vip")

	r := gin.New()
	r.GET("/internal/models/catalog", InternalGetModelCatalog)

	w := doInternalGet(r, "/internal/models/catalog?group=vip")
	resp := ParseV2Response(t, w)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	if resp["group"].(string) != "vip" {
		t.Fatalf("expected echoed group=vip, got %v", resp["group"])
	}
	if resp["group_ratio"].(float64) != wantGroupRatio {
		t.Fatalf("expected group_ratio=%v for vip group, got %v", wantGroupRatio, resp["group_ratio"])
	}
}

func idStr(n int) string {
	return strconv.Itoa(n)
}
