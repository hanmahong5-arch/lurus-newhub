package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var healthTestDBCounter atomic.Int64

// callHealth invokes GetHealthDetailed against a fresh test context and returns
// the recorder plus the wall-clock the handler took (for the zombie-pod guard).
func callHealth(t *testing.T) (*httptest.ResponseRecorder, time.Duration) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/health", nil)
	start := time.Now()
	GetHealthDetailed(c)
	return w, time.Since(start)
}

func healthDBCheck(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse health body: %v, raw=%s", err, w.Body.String())
	}
	return body.Checks["database"]
}

func TestGetHealthDetailed_DBOK(t *testing.T) {
	dbName := fmt.Sprintf("file:healthok%d?mode=memory&cache=shared", healthTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	prevDB, prevRedis := repo.DB, common.RedisEnabled
	repo.DB, common.RedisEnabled = db, false
	t.Cleanup(func() {
		repo.DB, common.RedisEnabled = prevDB, prevRedis
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})

	w, _ := callHealth(t)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := healthDBCheck(t, w); got != "ok" {
		t.Fatalf("expected database=ok, got %q", got)
	}
}

func TestGetHealthDetailed_ClosedDBReturns503Fast(t *testing.T) {
	dbName := fmt.Sprintf("file:healthdown%d?mode=memory&cache=shared", healthTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// Close the underlying pool so PingContext fails fast — stands in for an
	// unreachable DB host without a real network hang. The elapsed<1s assertion
	// is the zombie-pod regression guard: the handler must return within its
	// bounded ping deadline (HealthDBPingTimeout default 1.5s), never wedge.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	_ = sqlDB.Close()

	prevDB, prevRedis := repo.DB, common.RedisEnabled
	repo.DB, common.RedisEnabled = db, false
	t.Cleanup(func() { repo.DB, common.RedisEnabled = prevDB, prevRedis })

	w, elapsed := callHealth(t)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := healthDBCheck(t, w); got != "unreachable" {
		t.Fatalf("expected database=unreachable, got %q", got)
	}
	if elapsed >= time.Second {
		t.Fatalf("zombie-pod guard: /api/health must return well under 1s, took %v", elapsed)
	}
}

func TestGetHealthDetailed_NilDBNotConfigured(t *testing.T) {
	prevDB, prevRedis := repo.DB, common.RedisEnabled
	repo.DB, common.RedisEnabled = nil, false
	t.Cleanup(func() { repo.DB, common.RedisEnabled = prevDB, prevRedis })

	w, _ := callHealth(t)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for nil DB, got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := healthDBCheck(t, w); got != "not_configured" {
		t.Fatalf("expected database=not_configured, got %q", got)
	}
}

// ─── Body "status" word: must reflect degradation, readiness must not move ─
//
// The HTTP status code contract (200 only flips to 503 on a failed DB ping)
// is untouched below. What changes is the JSON "status" word: it used to be
// derived from the same single `healthy` flag that drives the status code,
// so a replica whose Redis/billing was down still answered {"status":
// "healthy"} at exactly the moment its rate limiters were failing open.

// healthFullBody parses both the top-level status word and the per-check map.
func healthFullBody(t *testing.T, w *httptest.ResponseRecorder) (string, map[string]string) {
	t.Helper()
	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse health body: %v, raw=%s", err, w.Body.String())
	}
	return body.Status, body.Checks
}

// healthSnapshotAll saves/restores every global GetHealthDetailed reads and
// resets the shared billing circuit breaker to closed on entry and exit, so
// these body-status tests never leak state into other tests in this binary.
func healthSnapshotAll(t *testing.T) {
	t.Helper()
	prevDB := repo.DB
	prevRedisEnabled, prevRDB := common.RedisEnabled, common.RDB
	prevBillingUnified := common.BillingUnifiedEnabled()
	prevLeader := common.IsLeader()
	common.BillingBreakerSuccess() // force closed before the test runs
	t.Cleanup(func() {
		repo.DB = prevDB
		common.RedisEnabled, common.RDB = prevRedisEnabled, prevRDB
		common.SetBillingUnifiedEnabled(prevBillingUnified)
		common.SetLeader(prevLeader)
		common.BillingBreakerSuccess() // force closed after the test too
	})
}

func TestGetHealthDetailed_BodyStatus_RedisUnreachable_DegradedBut200(t *testing.T) {
	healthSnapshotAll(t)
	repo.DB = nil
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	addr := mr.Addr()
	mr.Close() // server gone; client still points at the now-dead address
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = common.RDB.Close() })
	common.SetBillingUnifiedEnabled(false)

	w, _ := callHealth(t)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (redis alone must not fail readiness), got %d (body=%s)", w.Code, w.Body.String())
	}
	status, checks := healthFullBody(t, w)
	if status != "degraded" {
		t.Errorf("status = %q, want degraded (redis unreachable while HTTP stays 200)", status)
	}
	if checks["redis"] != "unreachable" {
		t.Errorf("checks.redis = %q, want unreachable", checks["redis"])
	}
}

func TestGetHealthDetailed_BodyStatus_BillingCircuitOpen_DegradedBut200(t *testing.T) {
	healthSnapshotAll(t)
	repo.DB = nil
	common.RedisEnabled = false
	common.SetBillingUnifiedEnabled(true)
	// Trip the breaker: threshold is 3 consecutive failures (billing_breaker.go).
	common.BillingBreakerFailure()
	common.BillingBreakerFailure()
	common.BillingBreakerFailure()

	w, _ := callHealth(t)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (billing alone must not fail readiness), got %d (body=%s)", w.Code, w.Body.String())
	}
	status, checks := healthFullBody(t, w)
	if status != "degraded" {
		t.Errorf("status = %q, want degraded (billing circuit open while HTTP stays 200)", status)
	}
	if checks["billing"] != "circuit_open" {
		t.Errorf("checks.billing = %q, want circuit_open", checks["billing"])
	}
}

func TestGetHealthDetailed_BodyStatus_PendingMigrations_DegradedBut200(t *testing.T) {
	healthSnapshotAll(t)
	repo.DB = nil
	common.RedisEnabled = false
	common.SetBillingUnifiedEnabled(false)
	metrics.SetSchemaMigrations(2, 27)
	t.Cleanup(func() { metrics.SetSchemaMigrations(0, 0) })

	w, _ := callHealth(t)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (pending migrations must not fail readiness), got %d (body=%s)", w.Code, w.Body.String())
	}
	status, checks := healthFullBody(t, w)
	if status != "degraded" {
		t.Errorf("status = %q, want degraded (pending migrations reported honestly)", status)
	}
	if checks["schema_migrations"] != "pending:2" {
		t.Errorf("checks.schema_migrations = %q, want pending:2", checks["schema_migrations"])
	}
}

func TestGetHealthDetailed_BodyStatus_AllOK_ReportsHealthy(t *testing.T) {
	healthSnapshotAll(t)
	dbName := fmt.Sprintf("file:healthbodyok%d?mode=memory&cache=shared", healthTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	repo.DB = db
	common.RedisEnabled = false
	common.SetBillingUnifiedEnabled(false)
	metrics.SetSchemaMigrations(0, 29)
	t.Cleanup(func() { metrics.SetSchemaMigrations(0, 0) })

	w, _ := callHealth(t)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	status, checks := healthFullBody(t, w)
	if status != "healthy" {
		t.Errorf("status = %q, want healthy, checks=%v", status, checks)
	}
}

func TestGetHealthDetailed_BodyStatus_ClosedDBStaysDegradedAnd503(t *testing.T) {
	healthSnapshotAll(t)
	dbName := fmt.Sprintf("file:healthbodydown%d?mode=memory&cache=shared", healthTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db(): %v", err)
	}
	_ = sqlDB.Close()
	repo.DB = db
	common.RedisEnabled = false
	common.SetBillingUnifiedEnabled(false)

	w, _ := callHealth(t)
	// The readiness contract must not move: only a failed DB check may 503.
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (body=%s)", w.Code, w.Body.String())
	}
	status, _ := healthFullBody(t, w)
	if status != "degraded" {
		t.Errorf("status = %q, want degraded", status)
	}
}

func TestGetHealthDetailed_BodyStatus_IntentionalOffStatesStayHealthy(t *testing.T) {
	healthSnapshotAll(t)
	repo.DB = nil                          // database -> not_configured (intentional off-state)
	common.RedisEnabled = false            // redis -> disabled (intentional off-state)
	common.SetBillingUnifiedEnabled(false) // billing -> legacy_mode (intentional off-state)
	common.SetLeader(false)                // leader -> standby (intentional off-state)

	w, _ := callHealth(t)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	status, checks := healthFullBody(t, w)
	if status != "healthy" {
		t.Errorf("status = %q, want healthy — intentional off-states must not be misreported as degraded, checks=%v", status, checks)
	}
	if checks["leader"] != "standby" {
		t.Errorf("checks.leader = %q, want standby", checks["leader"])
	}
}

// TestGetHealthDetailed_Leader_HeldAndStandbyBothHealthy is the direct
// oracle for the HA drill: a replica that holds the lease and one that
// doesn't must both answer 200/"healthy" — only checks.leader tells them
// apart. Before this, checks.leader did not exist and there was no way to
// tell a leader from a follower by hitting /api/health.
func TestGetHealthDetailed_Leader_HeldAndStandbyBothHealthy(t *testing.T) {
	healthSnapshotAll(t)
	repo.DB = nil
	common.RedisEnabled = false
	common.SetBillingUnifiedEnabled(false)

	common.SetLeader(true)
	w, _ := callHealth(t)
	if w.Code != http.StatusOK {
		t.Fatalf("held: expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	status, checks := healthFullBody(t, w)
	if checks["leader"] != "held" {
		t.Errorf("held: checks.leader = %q, want held", checks["leader"])
	}
	if status != "healthy" {
		t.Errorf("held: status = %q, want healthy", status)
	}

	common.SetLeader(false)
	w2, _ := callHealth(t)
	if w2.Code != http.StatusOK {
		t.Fatalf("standby: expected 200, got %d (body=%s)", w2.Code, w2.Body.String())
	}
	status2, checks2 := healthFullBody(t, w2)
	if checks2["leader"] != "standby" {
		t.Errorf("standby: checks.leader = %q, want standby", checks2["leader"])
	}
	if status2 != "healthy" {
		t.Errorf("standby: status = %q, want healthy — a follower must never be reported unhealthy", status2)
	}
}

// TestGetHealthDetailed_NoInstanceLeak locks the fix for L3 finding #1:
// GET /api/health is registered under the public (no-authentication) route
// group (router/api-router.go) and proxied by the host vhost with no
// forwarding-header gate, unlike /metrics (metricsAuthMiddleware). Publishing
// this pod's identity there would make it readable by anyone on the public
// internet, so the body must carry no "instance" key and no X-Lurus-Instance
// header — only the coarse checks.leader role.
func TestGetHealthDetailed_NoInstanceLeak(t *testing.T) {
	healthSnapshotAll(t)
	repo.DB = nil
	common.RedisEnabled = false
	common.SetBillingUnifiedEnabled(false)
	t.Setenv("POD_NAME", "health-test-pod")

	w, _ := callHealth(t)
	if got := w.Header().Get("X-Lurus-Instance"); got != "" {
		t.Errorf("X-Lurus-Instance header = %q, want absent", got)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse health body: %v, raw=%s", err, w.Body.String())
	}
	if _, present := body["instance"]; present {
		t.Errorf("body has an \"instance\" key: %s", w.Body.String())
	}
}
