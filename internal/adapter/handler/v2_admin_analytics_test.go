package handler

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func setupAnalyticsRouter(t *testing.T) *adminGovCtx {
	t.Helper()
	ctx := setupAdminGovRouter(t)
	admin := ctx.router.Group("/api/v2/admin")
	admin.Use(func(c *gin.Context) {
		c.Set("id", 1)
		c.Next()
	})
	admin.GET("/analytics/model-performance", GetModelPerformanceV2)
	return ctx
}

// seedPerfLog inserts a log row with full field control so aggregate
// expectations can be hand-computed.
func seedPerfLog(t *testing.T, db *gorm.DB, tenant, model string, logType, latencyMs, promptTokens, completionTokens, quota int, createdAt int64) {
	t.Helper()
	l := &entity.Log{
		UserId:           1,
		TenantId:         tenant,
		CreatedAt:        createdAt,
		Type:             logType,
		ModelName:        model,
		Quota:            quota,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalLatencyMs:   latencyMs,
	}
	if err := db.Create(l).Error; err != nil {
		t.Fatalf("seed perf log: %v", err)
	}
}

// seedModelPerfFixture seeds the canonical two-model dataset used by the
// aggregate tests. All in-window rows sit at now-3600.
//
// alpha (tenant test-tenant): 4 consume rows with latencies 100/200/300/400,
// prompt 10/20/30/40, completion 1/2/3/4, quota 5/6/7/8; one legacy consume
// row (latency 0, zero tokens/quota); one error row.
// beta (tenant test-tenant): 1 consume row latency 50, prompt 5, completion 7,
// quota 3.
// Plus: one out-of-window alpha row and one other-tenant alpha row.
func seedModelPerfFixture(t *testing.T, db *gorm.DB, now int64) {
	t.Helper()
	in := now - 3600
	lat := []int{100, 200, 300, 400}
	for i, ms := range lat {
		seedPerfLog(t, db, "test-tenant", "alpha", entity.LogTypeConsume,
			ms, 10*(i+1), i+1, 5+i, in)
	}
	// Legacy consume row recorded before the latency column existed.
	seedPerfLog(t, db, "test-tenant", "alpha", entity.LogTypeConsume, 0, 0, 0, 0, in)
	// Upstream error.
	seedPerfLog(t, db, "test-tenant", "alpha", entity.LogTypeError, 0, 0, 0, 0, in)
	// beta: single healthy request.
	seedPerfLog(t, db, "test-tenant", "beta", entity.LogTypeConsume, 50, 5, 7, 3, in)
	// Outside the [now-7200, now] request window used by the tests.
	seedPerfLog(t, db, "test-tenant", "alpha", entity.LogTypeConsume, 999, 99, 99, 99, now-10000)
	// Another tenant, in-window.
	seedPerfLog(t, db, "other-tenant", "alpha", entity.LogTypeConsume, 700, 11, 13, 17, in)
}

func getPerfModels(t *testing.T, w *httptest.ResponseRecorder) []interface{} {
	t.Helper()
	body := parseJSON(t, w)
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing data object: %s", w.Body.String())
	}
	models, ok := data["models"].([]interface{})
	if !ok {
		t.Fatalf("missing models array: %s", w.Body.String())
	}
	return models
}

func TestGetModelPerformanceV2_ExactAggregates(t *testing.T) {
	ctx := setupAnalyticsRouter(t)
	defer ctx.cleanup()

	now := time.Now().Unix()
	seedModelPerfFixture(t, ctx.db, now)

	w := doGET(ctx.router, fmt.Sprintf(
		"/api/v2/admin/analytics/model-performance?start=%d&end=%d&tenant_id=test-tenant",
		now-7200, now))
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	models := getPerfModels(t, w)
	if len(models) != 2 {
		t.Fatalf("models=%d, want 2 (alpha, beta)", len(models))
	}

	// Sorted by requests DESC: alpha (6) then beta (1).
	alpha := models[0].(map[string]interface{})
	beta := models[1].(map[string]interface{})
	if alpha["model_name"] != "alpha" || beta["model_name"] != "beta" {
		t.Fatalf("order: got [%v, %v], want [alpha, beta]", alpha["model_name"], beta["model_name"])
	}

	// alpha: 4 latency rows + 1 legacy consume + 1 error.
	wantAlpha := map[string]float64{
		"requests":          6,
		"errors":            1,
		"prompt_tokens":     100, // 10+20+30+40
		"completion_tokens": 10,  // 1+2+3+4
		"total_tokens":      110,
		"quota":             26, // 5+6+7+8
		"latency_samples":   4,
		"avg_latency_ms":    250, // (100+200+300+400)/4
		"p50_latency_ms":    200, // nearest-rank ceil(0.5*4)=2nd of [100,200,300,400]
		"p95_latency_ms":    400, // nearest-rank ceil(0.95*4)=4th
	}
	for k, want := range wantAlpha {
		got, ok := alpha[k].(float64)
		if !ok || got != want {
			t.Errorf("alpha.%s = %v, want %v", k, alpha[k], want)
		}
	}
	if got := alpha["error_rate"].(float64); math.Abs(got-1.0/6.0) > 1e-9 {
		t.Errorf("alpha.error_rate = %v, want %v", got, 1.0/6.0)
	}

	wantBeta := map[string]float64{
		"requests":          1,
		"errors":            0,
		"error_rate":        0,
		"prompt_tokens":     5,
		"completion_tokens": 7,
		"total_tokens":      12,
		"quota":             3,
		"latency_samples":   1,
		"avg_latency_ms":    50,
		"p50_latency_ms":    50,
		"p95_latency_ms":    50,
	}
	for k, want := range wantBeta {
		got, ok := beta[k].(float64)
		if !ok || got != want {
			t.Errorf("beta.%s = %v, want %v", k, beta[k], want)
		}
	}
}

func TestGetModelPerformanceV2_NoTenantFilterSpansTenants(t *testing.T) {
	ctx := setupAnalyticsRouter(t)
	defer ctx.cleanup()

	now := time.Now().Unix()
	seedModelPerfFixture(t, ctx.db, now)

	w := doGET(ctx.router, fmt.Sprintf(
		"/api/v2/admin/analytics/model-performance?start=%d&end=%d", now-7200, now))
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	models := getPerfModels(t, w)
	if len(models) != 2 {
		t.Fatalf("models=%d, want 2", len(models))
	}
	alpha := models[0].(map[string]interface{})
	// 6 test-tenant rows + 1 other-tenant row.
	if got := alpha["requests"].(float64); got != 7 {
		t.Errorf("alpha.requests without tenant filter = %v, want 7", got)
	}
	// Latency pool gains the 700ms row: [100,200,300,400,700],
	// p50 = ceil(0.5*5)=3rd = 300, p95 = ceil(0.95*5)=5th = 700, avg = 340.
	if got := alpha["p50_latency_ms"].(float64); got != 300 {
		t.Errorf("alpha.p50 = %v, want 300", got)
	}
	if got := alpha["p95_latency_ms"].(float64); got != 700 {
		t.Errorf("alpha.p95 = %v, want 700", got)
	}
	if got := alpha["avg_latency_ms"].(float64); got != 340 {
		t.Errorf("alpha.avg = %v, want 340", got)
	}
}

func TestGetModelPerformanceV2_ModelFilter(t *testing.T) {
	ctx := setupAnalyticsRouter(t)
	defer ctx.cleanup()

	now := time.Now().Unix()
	seedModelPerfFixture(t, ctx.db, now)

	w := doGET(ctx.router, fmt.Sprintf(
		"/api/v2/admin/analytics/model-performance?start=%d&end=%d&tenant_id=test-tenant&model=beta",
		now-7200, now))
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	models := getPerfModels(t, w)
	if len(models) != 1 {
		t.Fatalf("models=%d, want 1", len(models))
	}
	if got := models[0].(map[string]interface{})["model_name"]; got != "beta" {
		t.Errorf("model_name = %v, want beta", got)
	}
}

func TestGetModelPerformanceV2_EmptyWindow(t *testing.T) {
	ctx := setupAnalyticsRouter(t)
	defer ctx.cleanup()

	// No rows seeded — default 24h window must return an empty array, not null.
	w := doGET(ctx.router, "/api/v2/admin/analytics/model-performance")
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	if models := getPerfModels(t, w); len(models) != 0 {
		t.Errorf("models=%d, want 0", len(models))
	}
}

func TestGetModelPerformanceV2_RejectsInvertedRange(t *testing.T) {
	ctx := setupAnalyticsRouter(t)
	defer ctx.cleanup()

	now := time.Now().Unix()
	w := doGET(ctx.router, fmt.Sprintf(
		"/api/v2/admin/analytics/model-performance?start=%d&end=%d", now, now-100))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for start>end, got %d", w.Code)
	}
}

// TestGetModelPerformanceV2_UnauthenticatedRejected mounts the production
// RootJWTAuth middleware (same wiring as api-v2-router.go's adminRoute) and
// verifies an anonymous request never reaches the handler.
func TestGetModelPerformanceV2_UnauthenticatedRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	store := cookie.NewStore([]byte("analytics-auth-test-secret"))
	router.Use(sessions.Sessions("session", store))
	admin := router.Group("/api/v2/admin")
	admin.Use(middleware.RootJWTAuth())
	admin.GET("/analytics/model-performance", GetModelPerformanceV2)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/analytics/model-performance", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for anonymous request, got %d body=%s", w.Code, w.Body.String())
	}
}
