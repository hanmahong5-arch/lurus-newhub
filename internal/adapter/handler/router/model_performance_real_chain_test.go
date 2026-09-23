package router

// model_performance_real_chain_test.go — GET /api/v2/~/models/performance
// through the production UserAuth()+TenantSlugGuard() chain (cycle 16, P7).
//
// Properties:
//   - the aggregate is the caller's tenant only (another tenant's slow rows
//     do not move the percentiles);
//   - a member sees quality (p50/p95, error rate, enough_samples) and no
//     volume fields; a tenant admin also sees the volume fields;
//   - a model with too few latency samples is flagged, not presented as a
//     measurement;
//   - only the preset windows are served.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var modelPerfDBCounter atomic.Int64

type modelPerfFixture struct {
	serve func(role int, path string) *httptest.ResponseRecorder
}

func setupModelPerf(t *testing.T) *modelPerfFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	n := modelPerfDBCounter.Add(1)
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:modelperf%d?mode=memory&cache=shared", n)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &entity.Tenant{}, &entity.Log{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}
	prevDB, prevLogDB, prevRedis := repo.DB, repo.LOG_DB, common.RedisEnabled
	repo.DB, repo.LOG_DB = db, db
	repo.InitCol()
	common.RedisEnabled = false
	t.Cleanup(func() {
		repo.DB, repo.LOG_DB, common.RedisEnabled = prevDB, prevLogDB, prevRedis
		if sqlDB, cerr := db.DB(); cerr == nil {
			_ = sqlDB.Close()
		}
	})

	own := fmt.Sprintf("perf-own-%d", n)
	other := fmt.Sprintf("perf-other-%d", n)
	for _, id := range []string{own, other} {
		if err := db.Create(&entity.Tenant{Id: id, IDPOrgID: "org-" + id, Slug: id + "-slug", Name: id, Status: entity.TenantStatusEnabled}).Error; err != nil {
			t.Fatalf("create tenant: %v", err)
		}
	}
	users := map[int]*repo.User{}
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser} {
		u := &repo.User{Username: fmt.Sprintf("perf_%d_%d", n, role), Role: role, Status: common.UserStatusEnabled, Email: fmt.Sprintf("perf-%d-%d@local", n, role), TenantId: own}
		if err := db.Create(u).Error; err != nil {
			t.Fatalf("create user: %v", err)
		}
		users[role] = u
	}

	now := time.Now().Unix()
	var rows []entity.Log
	// own tenant, "fast": 25 successes at 100..124 ms and 5 errors.
	for i := 0; i < 25; i++ {
		rows = append(rows, entity.Log{TenantId: own, Type: entity.LogTypeConsume, ModelName: "fast", CreatedAt: now - 60, TotalLatencyMs: 100 + i, PromptTokens: 10, Quota: 7})
	}
	for i := 0; i < 5; i++ {
		rows = append(rows, entity.Log{TenantId: own, Type: entity.LogTypeError, ModelName: "fast", CreatedAt: now - 60})
	}
	// own tenant, "rare": 3 samples only.
	for i := 0; i < 3; i++ {
		rows = append(rows, entity.Log{TenantId: own, Type: entity.LogTypeConsume, ModelName: "rare", CreatedAt: now - 60, TotalLatencyMs: 900})
	}
	// another tenant, "fast", very slow: must not reach this tenant's numbers.
	for i := 0; i < 40; i++ {
		rows = append(rows, entity.Log{TenantId: other, Type: entity.LogTypeConsume, ModelName: "fast", CreatedAt: now - 60, TotalLatencyMs: 99999})
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}

	serve := func(role int, path string) *httptest.ResponseRecorder {
		u := users[role]
		engine := gin.New()
		engine.Use(gin.Recovery())
		engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("model-perf-secret"))))
		engine.Use(func(c *gin.Context) {
			s := sessions.Default(c)
			s.Set("username", u.Username)
			s.Set("role", u.Role)
			s.Set("id", u.Id)
			s.Set("status", common.UserStatusEnabled)
			_ = s.Save()
			c.Next()
		})
		SetApiV2Router(engine)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	return &modelPerfFixture{serve: serve}
}

func decodePerfItems(t *testing.T, w *httptest.ResponseRecorder) map[string]map[string]any {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := map[string]map[string]any{}
	for _, it := range body.Data.Items {
		out[fmt.Sprint(it["model_name"])] = it
	}
	return out
}

func TestModelPerformance_MemberSeesOwnTenantQualityOnly(t *testing.T) {
	fx := setupModelPerf(t)
	items := decodePerfItems(t, fx.serve(common.RoleCommonUser, "/api/v2/~/models/performance"))

	fast, ok := items["fast"]
	if !ok {
		t.Fatalf("no item for fast: %v", items)
	}
	// p50 of 100..124 (nearest rank, n=25 → 13th) = 112; the other tenant's
	// 99999 ms rows would drag it far up if they leaked in.
	if p50 := fast["p50_latency_ms"].(float64); p50 != 112 {
		t.Errorf("fast p50 = %v, want 112 (own tenant only)", p50)
	}
	if er := fast["error_rate"].(float64); er < 0.166 || er > 0.167 {
		t.Errorf("fast error_rate = %v, want 5/30", er)
	}
	if fast["enough_samples"] != true {
		t.Errorf("fast enough_samples = %v, want true (25 samples)", fast["enough_samples"])
	}
	if items["rare"]["enough_samples"] != false {
		t.Errorf("rare enough_samples = %v, want false (3 samples)", items["rare"]["enough_samples"])
	}
	for _, f := range []string{"requests", "errors", "total_tokens", "quota", "latency_samples"} {
		if _, leaked := fast[f]; leaked {
			t.Errorf("member response carries volume field %q", f)
		}
	}
}

func TestModelPerformance_TenantAdminAlsoSeesVolume(t *testing.T) {
	fx := setupModelPerf(t)
	items := decodePerfItems(t, fx.serve(common.RoleAdminUser, "/api/v2/~/models/performance?hours=1"))
	fast := items["fast"]
	if fast["requests"].(float64) != 30 || fast["latency_samples"].(float64) != 25 {
		t.Fatalf("admin volume = requests %v samples %v, want 30 / 25", fast["requests"], fast["latency_samples"])
	}
}

func TestModelPerformance_OnlyPresetWindows(t *testing.T) {
	fx := setupModelPerf(t)
	for _, h := range []string{"2", "720", "abc"} {
		if w := fx.serve(common.RoleCommonUser, "/api/v2/~/models/performance?hours="+h); w.Code != http.StatusBadRequest {
			t.Errorf("hours=%s → %d, want 400", h, w.Code)
		}
	}
}
