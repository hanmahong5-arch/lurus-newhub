package handler

// health_schema_migrations_test.go — /api/health's schema_migrations check.
// Drift used to be invisible outside a single boot log line; the endpoint now
// reports it. Crucially it must REPORT without failing: during a rolling update
// the outgoing pods legitimately see pending migrations, and turning that into a
// 503 would make readiness stall the very deploy that applies them.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
)

// healthMigrationsCall drives GetHealthDetailed with the database deliberately
// unconfigured (that branch is covered elsewhere) and returns status + checks.
func healthMigrationsCall(t *testing.T) (int, map[string]string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	prevDB, prevRDB, prevEnabled := repo.DB, common.RDB, common.RedisEnabled
	repo.DB, common.RDB, common.RedisEnabled = nil, nil, false
	t.Cleanup(func() {
		repo.DB, common.RDB, common.RedisEnabled = prevDB, prevRDB, prevEnabled
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/health", nil)
	GetHealthDetailed(c)

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v raw=%s", err, w.Body.String())
	}
	return w.Code, body.Checks
}

func TestHealth_SchemaMigrations_OkWhenNothingPending(t *testing.T) {
	metrics.SetSchemaMigrations(0, 29)

	code, checks := healthMigrationsCall(t)
	if got := checks["schema_migrations"]; got != "ok" {
		t.Errorf("checks.schema_migrations = %q, want \"ok\"", got)
	}
	if code != http.StatusOK {
		t.Errorf("status = %d, want 200", code)
	}
}

func TestHealth_SchemaMigrations_PendingIsReportedButNotFatal(t *testing.T) {
	metrics.SetSchemaMigrations(2, 27)
	t.Cleanup(func() { metrics.SetSchemaMigrations(0, 0) })

	code, checks := healthMigrationsCall(t)
	if got := checks["schema_migrations"]; got != "pending:2" {
		t.Errorf("checks.schema_migrations = %q, want \"pending:2\"", got)
	}
	// The whole point: pending migrations must not take the pod out of service.
	if code != http.StatusOK {
		t.Errorf("status = %d, want 200 — pending migrations must not fail readiness", code)
	}
	if checks["database"] == "" || checks["redis"] == "" || checks["billing"] == "" {
		t.Errorf("the new key must not displace the existing checks: %v", checks)
	}
}
