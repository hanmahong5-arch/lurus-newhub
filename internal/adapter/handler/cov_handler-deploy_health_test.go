package handler

// cov_handler-deploy_health_test.go — business-acceptance tests for
// health.go: GET /api/health. This is the K8s readiness/liveness probe body,
// so its status-code contract (200 vs 503) and per-dependency degrade
// semantics are operationally load-bearing.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func handlerDeployHealthRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/health", GetHealthDetailed)
	return r
}

func handlerDeployHealthDo(r *gin.Engine) (*httptest.ResponseRecorder, map[string]interface{}) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	r.ServeHTTP(w, req)
	var out map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

// handlerDeployHealthSnapshot saves/restores every package-level global
// GetHealthDetailed reads, and resets the shared billing circuit breaker to
// closed on both entry and exit so this file's failure-injection tests never
// leak state into other tests running in the same binary.
func handlerDeployHealthSnapshot(t *testing.T) {
	t.Helper()
	prevDB, prevLogDB := repo.DB, repo.LOG_DB
	prevRedisEnabled, prevRDB := common.RedisEnabled, common.RDB
	prevBillingUnified := common.BillingUnifiedEnabled()
	common.BillingBreakerSuccess() // force closed before the test runs
	t.Cleanup(func() {
		repo.DB, repo.LOG_DB = prevDB, prevLogDB
		common.RedisEnabled, common.RDB = prevRedisEnabled, prevRDB
		common.SetBillingUnifiedEnabled(prevBillingUnified)
		common.BillingBreakerSuccess() // force closed after the test too
	})
}

// ─── Database branch drives the overall status code ───────────────────────

func TestGetHealthDetailed_DBNotConfigured_StillHealthy(t *testing.T) {
	handlerDeployHealthSnapshot(t)
	repo.DB = nil
	common.RedisEnabled = false

	w, body := handlerDeployHealthDo(handlerDeployHealthRouter())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if body["status"] != "healthy" {
		t.Errorf("status field = %v, want healthy", body["status"])
	}
	checks, _ := body["checks"].(map[string]interface{})
	if checks["database"] != "not_configured" {
		t.Errorf("checks.database = %v, want not_configured", checks["database"])
	}
}

func TestGetHealthDetailed_DBUp_Returns200Healthy(t *testing.T) {
	handlerDeployHealthSnapshot(t)
	db, err := gorm.Open(sqlite.Open("file:covhealthup?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	repo.DB = db
	common.RedisEnabled = false

	w, body := handlerDeployHealthDo(handlerDeployHealthRouter())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%v", w.Code, body)
	}
	checks, _ := body["checks"].(map[string]interface{})
	if checks["database"] != "ok" {
		t.Errorf("checks.database = %v, want ok", checks["database"])
	}
}

// TestGetHealthDetailed_DBDown_Returns503Degraded is the headline readiness
// contract: a DB that cannot be pinged must flip both the HTTP status AND the
// "status" field, so K8s pulls the pod out of the Service before it drains
// live traffic against a dead database.
func TestGetHealthDetailed_DBDown_Returns503Degraded(t *testing.T) {
	handlerDeployHealthSnapshot(t)
	db, err := gorm.Open(sqlite.Open("file:covhealthdown?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db(): %v", err)
	}
	_ = sqlDB.Close() // simulate a dead connection: PingContext now fails
	repo.DB = db
	common.RedisEnabled = false

	w, body := handlerDeployHealthDo(handlerDeployHealthRouter())
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body=%v", w.Code, body)
	}
	if body["status"] != "degraded" {
		t.Errorf("status field = %v, want degraded", body["status"])
	}
	checks, _ := body["checks"].(map[string]interface{})
	if checks["database"] != "unreachable" {
		t.Errorf("checks.database = %v, want unreachable", checks["database"])
	}
}

// ─── Redis branch: soft dependency, must never itself flip the HTTP status ─

func TestGetHealthDetailed_RedisDisabled(t *testing.T) {
	handlerDeployHealthSnapshot(t)
	repo.DB = nil
	common.RedisEnabled = false
	common.RDB = nil

	_, body := handlerDeployHealthDo(handlerDeployHealthRouter())
	checks, _ := body["checks"].(map[string]interface{})
	if checks["redis"] != "disabled" {
		t.Errorf("checks.redis = %v, want disabled", checks["redis"])
	}
}

// The mis-wired case (RedisEnabled=true, RDB=nil) used to collapse into the
// same "disabled" label as a deliberate opt-out, hiding a broken client from
// whoever reads /api/health; it now reports "not_configured". This drives the
// full router round trip (fix_health_redis_label_test.go calls
// GetHealthDetailed directly) and additionally pins that the redis check never
// flips the HTTP status — a soft dependency must not turn the endpoint 503.
func TestGetHealthDetailed_RedisEnabledButClientNil_ReportsNotConfigured(t *testing.T) {
	handlerDeployHealthSnapshot(t)
	repo.DB = nil
	common.RedisEnabled = true
	common.RDB = nil

	w, body := handlerDeployHealthDo(handlerDeployHealthRouter())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (redis check never flips HTTP status)", w.Code)
	}
	checks, _ := body["checks"].(map[string]interface{})
	if checks["redis"] != "not_configured" {
		t.Errorf("checks.redis = %v, want not_configured (RedisEnabled=true + nil RDB is a wiring fault, not an opt-out)", checks["redis"])
	}
}

func TestGetHealthDetailed_RedisUp_ReportsOK(t *testing.T) {
	handlerDeployHealthSnapshot(t)
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	t.Cleanup(mr.Close)

	repo.DB = nil
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = common.RDB.Close() })

	w, body := handlerDeployHealthDo(handlerDeployHealthRouter())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	checks, _ := body["checks"].(map[string]interface{})
	if checks["redis"] != "ok" {
		t.Errorf("checks.redis = %v, want ok", checks["redis"])
	}
}

func TestGetHealthDetailed_RedisDown_ReportsUnreachableButStays200(t *testing.T) {
	handlerDeployHealthSnapshot(t)
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	addr := mr.Addr()
	mr.Close() // server gone; client still points at the now-dead address

	repo.DB = nil
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = common.RDB.Close() })

	w, body := handlerDeployHealthDo(handlerDeployHealthRouter())
	// Redis is a soft dependency: down Redis must NOT flip readiness — only
	// the DB check drives the HTTP status code (documents current behaviour).
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (redis failure alone must not fail readiness)", w.Code)
	}
	checks, _ := body["checks"].(map[string]interface{})
	if checks["redis"] != "unreachable" {
		t.Errorf("checks.redis = %v, want unreachable, body=%v", checks["redis"], body)
	}
}

// ─── Billing branch: legacy / ok / circuit_open, also a soft dependency ───

func TestGetHealthDetailed_BillingLegacyMode(t *testing.T) {
	handlerDeployHealthSnapshot(t)
	repo.DB = nil
	common.RedisEnabled = false
	common.SetBillingUnifiedEnabled(false)

	_, body := handlerDeployHealthDo(handlerDeployHealthRouter())
	checks, _ := body["checks"].(map[string]interface{})
	if checks["billing"] != "legacy_mode" {
		t.Errorf("checks.billing = %v, want legacy_mode", checks["billing"])
	}
}

func TestGetHealthDetailed_BillingUnifiedClosed_ReportsOK(t *testing.T) {
	handlerDeployHealthSnapshot(t)
	repo.DB = nil
	common.RedisEnabled = false
	common.SetBillingUnifiedEnabled(true)

	_, body := handlerDeployHealthDo(handlerDeployHealthRouter())
	checks, _ := body["checks"].(map[string]interface{})
	if checks["billing"] != "ok" {
		t.Errorf("checks.billing = %v, want ok", checks["billing"])
	}
}

func TestGetHealthDetailed_BillingCircuitOpen_ReportsCircuitOpenButStays200(t *testing.T) {
	handlerDeployHealthSnapshot(t)
	repo.DB = nil
	common.RedisEnabled = false
	common.SetBillingUnifiedEnabled(true)

	// Trip the breaker: threshold is 3 consecutive failures (billing_breaker.go).
	common.BillingBreakerFailure()
	common.BillingBreakerFailure()
	common.BillingBreakerFailure()
	t.Cleanup(common.BillingBreakerSuccess)

	w, body := handlerDeployHealthDo(handlerDeployHealthRouter())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (billing circuit-open must not fail readiness either)", w.Code)
	}
	checks, _ := body["checks"].(map[string]interface{})
	if checks["billing"] != "circuit_open" {
		t.Errorf("checks.billing = %v, want circuit_open", checks["billing"])
	}
}

// ─── Combined worst-case: DB down AND every soft dependency degraded ─────

func TestGetHealthDetailed_AllDependenciesDown_Only503FromDB(t *testing.T) {
	handlerDeployHealthSnapshot(t)
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:covhealthall%d?mode=memory&cache=shared", 1)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	_ = sqlDB.Close()
	repo.DB = db

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	addr := mr.Addr()
	mr.Close()
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = common.RDB.Close() })

	common.SetBillingUnifiedEnabled(true)
	common.BillingBreakerFailure()
	common.BillingBreakerFailure()
	common.BillingBreakerFailure()
	t.Cleanup(common.BillingBreakerSuccess)

	w, body := handlerDeployHealthDo(handlerDeployHealthRouter())
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	checks, _ := body["checks"].(map[string]interface{})
	if checks["database"] != "unreachable" || checks["redis"] != "unreachable" || checks["billing"] != "circuit_open" {
		t.Errorf("checks = %v, want all three degraded", checks)
	}
}
