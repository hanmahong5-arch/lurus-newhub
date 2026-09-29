package handler

// v2_admin_model_pools_test.go — hermetic sqlite coverage for
// GET /api/v2/admin/model-pools (GetModelPoolsV2): group fan-out (a channel
// in "default,free" appears under both), multi-key key-status counts,
// model-health join, 24h usage counts, and — the one that matters most for
// this endpoint — that the raw channel key material never reaches the
// response body.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var modelPoolsDBCounter atomic.Int64

func setupModelPoolsRouter(t *testing.T) (*gin.Engine, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:modelPools%d?mode=memory&cache=shared", modelPoolsDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.Channel{}, &entity.ModelHealth{}, &entity.Log{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	prevDB, prevLog := repo.DB, repo.LOG_DB
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()

	router := gin.New()
	admin := router.Group("/api/v2/admin")
	admin.Use(func(c *gin.Context) {
		c.Set("id", 1)
		c.Set("role", common.RoleRootUser)
		c.Next()
	})
	admin.GET("/model-pools", GetModelPoolsV2)

	cleanup := func() {
		repo.DB, repo.LOG_DB = prevDB, prevLog
		if sqlDB, sErr := db.DB(); sErr == nil {
			_ = sqlDB.Close()
		}
	}
	return router, cleanup
}

func ptrInt64(v int64) *int64 { return &v }
func ptrUint(v uint) *uint    { return &v }

func TestGetModelPoolsV2_FullFixture(t *testing.T) {
	router, cleanup := setupModelPoolsRouter(t)
	defer cleanup()

	now := time.Now().Unix()

	// Channel A: multi-key, in groups "default" and "free". Key0 enabled
	// (no status entry), key1 permanently disabled (status entry, no
	// cooldown), key2 cooling (status entry + future cooldown).
	chanA := &repo.Channel{
		Name:         "chan-a",
		Type:         1,
		Status:       common.ChannelStatusEnabled,
		Group:        "default,free",
		Models:       "m1,m2,m2", // deliberate duplicate
		Priority:     ptrInt64(10),
		Weight:       ptrUint(5),
		TenantId:     "default",
		TestTime:     1700000000,
		ResponseTime: 120,
		Balance:      3.5,
		Key:          `["sk-key-0-SECRET","sk-key-1-SECRET","sk-key-2-SECRET"]`,
		ChannelInfo: repo.ChannelInfo{
			IsMultiKey:         true,
			MultiKeySize:       3,
			MultiKeyStatusList: map[int]int{1: common.ChannelStatusAutoDisabled, 2: common.ChannelStatusAutoDisabled},
			MultiKeyCooldownUntil: map[int]int64{
				2: now + 3600, // still cooling
			},
		},
	}
	if err := repo.DB.Create(chanA).Error; err != nil {
		t.Fatalf("seed chan-a: %v", err)
	}

	// Channel B: single key, higher priority, group "free" only.
	chanB := &repo.Channel{
		Name:         "chan-b",
		Type:         2,
		Status:       common.ChannelStatusEnabled,
		Group:        "free",
		Models:       "m3",
		Priority:     ptrInt64(20),
		Weight:       ptrUint(1),
		TenantId:     "default",
		TestTime:     1700001000,
		ResponseTime: 80,
		Balance:      1.0,
		Key:          "sk-single-SECRET",
	}
	if err := repo.DB.Create(chanB).Error; err != nil {
		t.Fatalf("seed chan-b: %v", err)
	}

	// Model health: m1 healthy, m2 auto-disabled. m3 never probed.
	if err := repo.SaveModelHealth(&entity.ModelHealth{
		ChannelId: chanA.Id, Model: "m1", Ok: true, LastProbeAt: now - 60, LatencyMs: 50,
	}); err != nil {
		t.Fatalf("seed health m1: %v", err)
	}
	if err := repo.SaveModelHealth(&entity.ModelHealth{
		ChannelId: chanA.Id, Model: "m2", Ok: false, AutoDisabled: true, LastError: "boom",
		ConsecutiveFailures: 5, LastProbeAt: now - 30,
	}); err != nil {
		t.Fatalf("seed health m2: %v", err)
	}

	// Usage: 2 consume + 1 error for chan-a/m1 inside the 24h window, plus
	// one consume row outside the window (must be excluded).
	in := now - 3600
	out := now - 100000
	seedLog := func(channelID int, model string, logType int, createdAt int64) {
		l := &entity.Log{ChannelId: channelID, ModelName: model, Type: logType, CreatedAt: createdAt}
		if err := repo.LOG_DB.Create(l).Error; err != nil {
			t.Fatalf("seed log: %v", err)
		}
	}
	seedLog(chanA.Id, "m1", entity.LogTypeConsume, in)
	seedLog(chanA.Id, "m1", entity.LogTypeConsume, in)
	seedLog(chanA.Id, "m1", entity.LogTypeError, in)
	seedLog(chanA.Id, "m1", entity.LogTypeConsume, out)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/v2/admin/model-pools", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()

	// No key leakage, anywhere in the response.
	for _, secret := range []string{"sk-key-0-SECRET", "sk-key-1-SECRET", "sk-key-2-SECRET", "sk-single-SECRET", "SECRET"} {
		if strings.Contains(body, secret) {
			t.Fatalf("response leaked key material %q: %s", secret, body)
		}
	}

	data := parseJSON(t, w)["data"].(map[string]interface{})
	if _, ok := data["generated_at"]; !ok {
		t.Fatalf("missing generated_at: %s", body)
	}
	pools, ok := data["pools"].([]interface{})
	if !ok || len(pools) != 2 {
		t.Fatalf("pools = %v, want 2 groups: %s", data["pools"], body)
	}

	poolByGroup := map[string]map[string]interface{}{}
	for _, p := range pools {
		pm := p.(map[string]interface{})
		poolByGroup[pm["group"].(string)] = pm
	}

	// "default" pool: only chan-a.
	defaultPool, ok := poolByGroup["default"]
	if !ok {
		t.Fatalf("missing default pool: %s", body)
	}
	defaultChannels := defaultPool["channels"].([]interface{})
	if len(defaultChannels) != 1 {
		t.Fatalf("default pool channels = %d, want 1: %s", len(defaultChannels), body)
	}

	// "free" pool: chan-b (priority 20) before chan-a (priority 10).
	freePool, ok := poolByGroup["free"]
	if !ok {
		t.Fatalf("missing free pool: %s", body)
	}
	freeChannels := freePool["channels"].([]interface{})
	if len(freeChannels) != 2 {
		t.Fatalf("free pool channels = %d, want 2: %s", len(freeChannels), body)
	}
	first := freeChannels[0].(map[string]interface{})
	second := freeChannels[1].(map[string]interface{})
	if first["name"] != "chan-b" || second["name"] != "chan-a" {
		t.Fatalf("free pool order = [%v, %v], want [chan-b, chan-a]: %s", first["name"], second["name"], body)
	}

	// chan-a key counts: 1 enabled, 1 cooling, 1 disabled, total 3.
	chanAView := defaultChannels[0].(map[string]interface{})
	keys := chanAView["keys"].(map[string]interface{})
	if keys["total"] != float64(3) || keys["enabled"] != float64(1) ||
		keys["cooling"] != float64(1) || keys["disabled"] != float64(1) {
		t.Fatalf("chan-a keys = %v, want total 3 / enabled 1 / cooling 1 / disabled 1: %s", keys, body)
	}

	// chan-a models: m1 and m2 (deduped from "m1,m2,m2"), with health + usage joined.
	models := chanAView["models"].([]interface{})
	if len(models) != 2 {
		t.Fatalf("chan-a models = %d, want 2 (deduped): %s", len(models), body)
	}
	modelByName := map[string]map[string]interface{}{}
	for _, m := range models {
		mm := m.(map[string]interface{})
		modelByName[mm["model"].(string)] = mm
	}
	m1 := modelByName["m1"]
	if m1 == nil {
		t.Fatalf("missing m1: %s", body)
	}
	if m1["requests_24h"] != float64(2) || m1["errors_24h"] != float64(1) {
		t.Fatalf("m1 usage = requests=%v errors=%v, want 2/1: %s", m1["requests_24h"], m1["errors_24h"], body)
	}
	m1Health := m1["health"].(map[string]interface{})
	if m1Health["ok"] != true || m1Health["auto_disabled"] != false {
		t.Fatalf("m1 health = %v, want ok=true auto_disabled=false: %s", m1Health, body)
	}

	m2 := modelByName["m2"]
	if m2 == nil {
		t.Fatalf("missing m2: %s", body)
	}
	if m2["requests_24h"] != float64(0) || m2["errors_24h"] != float64(0) {
		t.Fatalf("m2 usage = %v/%v, want 0/0: %s", m2["requests_24h"], m2["errors_24h"], body)
	}
	m2Health := m2["health"].(map[string]interface{})
	if m2Health["auto_disabled"] != true || m2Health["last_error"] != "boom" {
		t.Fatalf("m2 health = %v, want auto_disabled=true last_error=boom: %s", m2Health, body)
	}

	// chan-b: single key counts as 1 enabled, m3 has never been probed
	// (health null) and carries zero usage.
	chanBView := freeChannels[0].(map[string]interface{})
	bKeys := chanBView["keys"].(map[string]interface{})
	if bKeys["total"] != float64(1) || bKeys["enabled"] != float64(1) ||
		bKeys["cooling"] != float64(0) || bKeys["disabled"] != float64(0) {
		t.Fatalf("chan-b keys = %v, want total 1 / enabled 1: %s", bKeys, body)
	}
	bModels := chanBView["models"].([]interface{})
	if len(bModels) != 1 {
		t.Fatalf("chan-b models = %d, want 1: %s", len(bModels), body)
	}
	m3 := bModels[0].(map[string]interface{})
	if m3["model"] != "m3" || m3["health"] != nil {
		t.Fatalf("m3 = %v, want health nil (never probed): %s", m3, body)
	}
}

func TestGetModelPoolsV2_EmptyChannelSet(t *testing.T) {
	router, cleanup := setupModelPoolsRouter(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/v2/admin/model-pools", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	data := parseJSON(t, w)["data"].(map[string]interface{})
	pools, ok := data["pools"].([]interface{})
	if !ok || len(pools) != 0 {
		t.Fatalf("pools = %v, want empty slice: %s", data["pools"], w.Body.String())
	}
}

// A key whose cooldown deadline has passed but which the pool reaper has not
// re-enabled yet is still "cooling" (the reaper fixes it on its own), never
// "disabled"; a key with a non-enabled status and no cooldown is disabled.
func TestCountChannelKeys_ExpiredCooldownIsCoolingNotDisabled(t *testing.T) {
	now := int64(1_000_000)
	ch := &repo.Channel{
		Key: "k0\nk1\nk2\nk3",
		ChannelInfo: repo.ChannelInfo{
			IsMultiKey: true,
			MultiKeyStatusList: map[int]int{
				1: common.ChannelStatusAutoDisabled,
				2: common.ChannelStatusAutoDisabled,
				3: common.ChannelStatusAutoDisabled,
			},
			MultiKeyCooldownUntil: map[int]int64{
				1: now + 600, // cooling
				2: now - 5,   // cooldown expired, awaiting reaper
			},
		},
	}
	got := countChannelKeys(ch, now)
	want := modelPoolKeyCounts{Total: 4, Enabled: 1, Cooling: 2, Disabled: 1}
	if got != want {
		t.Fatalf("countChannelKeys = %+v, want %+v", got, want)
	}
}
