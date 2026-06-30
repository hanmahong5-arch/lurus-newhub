package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Self-contained router setup for admin audit/governance endpoints.
// Kept separate from SetupV2TestRouter to avoid blast-radius on other tests.

var adminGovDBCounter atomic.Int64

type adminGovCtx struct {
	router  *gin.Engine
	db      *gorm.DB
	cleanup func()
}

func setupAdminGovRouter(t *testing.T) *adminGovCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	n := adminGovDBCounter.Add(1)
	dsn := fmt.Sprintf("file:adminGov%d?mode=memory&cache=shared", n)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	tables := []interface{}{
		&entity.Log{},
		&entity.AuditEvent{},
	}
	for _, tbl := range tables {
		if err := db.AutoMigrate(tbl); err != nil &&
			!strings.Contains(err.Error(), "already exists") {
			t.Fatalf("auto-migrate %T: %v", tbl, err)
		}
	}

	prevDB, prevLog := repo.DB, repo.LOG_DB
	prevSQLite, prevPG := common.UsingSQLite, common.UsingPostgreSQL
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false

	router := gin.New()
	mockAuth := func(c *gin.Context) {
		c.Set("tenant_context", &middleware.TenantContext{
			TenantID: "test-tenant", UserID: 1,
			IDPSubject: "zita_admin", Email: "admin@test.local",
			Username: "admin", Roles: []string{"admin"},
		})
		c.Set("role", common.RoleAdminUser)
		c.Set("id", 1)
		c.Next()
	}

	admin := router.Group("/api/v2/admin")
	admin.Use(mockAuth)
	{
		admin.GET("/audit/events", GetAuditEvents)
		gov := admin.Group("/governance")
		gov.GET("/channels", GetGovernanceChannelDistribution)
		gov.GET("/fingerprints", GetGovernanceFingerprintStats)
		gov.GET("/latency", GetGovernanceLatencyStats)
		gov.GET("/efficiency", GetGovernanceEfficiencyStats)
		gov.GET("/savings", GetGovernanceSavings)
	}

	return &adminGovCtx{
		router: router, db: db,
		cleanup: func() {
			repo.DB, repo.LOG_DB = prevDB, prevLog
			common.UsingSQLite, common.UsingPostgreSQL = prevSQLite, prevPG
			if sqlDB, err := db.DB(); err == nil {
				_ = sqlDB.Close()
			}
		},
	}
}

func doGET(router *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func parseJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("parse json: %v, body: %s", err, w.Body.String())
	}
	return m
}

func seedAuditEvent(t *testing.T, db *gorm.DB, action, resource string, actorID int, tsOffset time.Duration) {
	t.Helper()
	ev := &entity.AuditEvent{
		TenantID:  "test-tenant",
		Timestamp: time.Now().Add(tsOffset).Unix(),
		ActorType: "admin", ActorID: actorID,
		Action: action, Resource: resource,
	}
	if err := db.Create(ev).Error; err != nil {
		t.Fatalf("seed audit: %v", err)
	}
}

func seedConsumeLog(t *testing.T, db *gorm.DB, model string, channelType int, quota int, latencyMs int, fingerprint string, tokenID int) {
	t.Helper()
	l := &entity.Log{
		UserId: 1, TenantId: "test-tenant",
		CreatedAt: time.Now().Add(-time.Hour).Unix(),
		Type:      entity.LogTypeConsume,
		ModelName: model,
		Quota:     quota, PromptTokens: 100, CompletionTokens: 200,
		ChannelType: channelType, RequestFingerprint: fingerprint,
		TotalLatencyMs: latencyMs, TokenId: tokenID,
	}
	if err := db.Create(l).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// audit
// ─────────────────────────────────────────────────────────────────────────────

func TestGetAuditEvents_EmptyList(t *testing.T) {
	ctx := setupAdminGovRouter(t)
	defer ctx.cleanup()

	w := doGET(ctx.router, "/api/v2/admin/audit/events")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	resp := parseJSON(t, w)
	if resp["success"] != true {
		t.Errorf("expected success=true, got %v", resp["success"])
	}
	data := resp["data"].(map[string]interface{})
	events := data["events"].([]interface{})
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
	if int(data["total"].(float64)) != 0 {
		t.Errorf("expected total=0, got %v", data["total"])
	}
}

func TestGetAuditEvents_FilterByAction(t *testing.T) {
	ctx := setupAdminGovRouter(t)
	defer ctx.cleanup()

	seedAuditEvent(t, ctx.db, "token.created", "token", 1, -10*time.Minute)
	seedAuditEvent(t, ctx.db, "token.deleted", "token", 1, -5*time.Minute)
	seedAuditEvent(t, ctx.db, "channel.created", "channel", 1, -3*time.Minute)

	w := doGET(ctx.router, "/api/v2/admin/audit/events?action=token.created")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	data := parseJSON(t, w)["data"].(map[string]interface{})
	events := data["events"].([]interface{})
	if len(events) != 1 {
		t.Fatalf("filter action=token.created: expected 1 event, got %d", len(events))
	}
	ev := events[0].(map[string]interface{})
	if ev["action"] != "token.created" {
		t.Errorf("expected action=token.created, got %v", ev["action"])
	}
}

func TestGetAuditEvents_FilterByActorAndResource(t *testing.T) {
	ctx := setupAdminGovRouter(t)
	defer ctx.cleanup()

	seedAuditEvent(t, ctx.db, "login", "session", 7, -1*time.Hour)
	seedAuditEvent(t, ctx.db, "login", "session", 8, -30*time.Minute)
	seedAuditEvent(t, ctx.db, "token.created", "token", 7, -10*time.Minute)

	w := doGET(ctx.router, "/api/v2/admin/audit/events?actor_id=7&resource=token")
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	data := parseJSON(t, w)["data"].(map[string]interface{})
	events := data["events"].([]interface{})
	if len(events) != 1 {
		t.Fatalf("filter actor=7+resource=token: expected 1, got %d", len(events))
	}
	if int(data["total"].(float64)) != 1 {
		t.Errorf("expected total=1, got %v", data["total"])
	}
}

func TestGetAuditEvents_Pagination(t *testing.T) {
	ctx := setupAdminGovRouter(t)
	defer ctx.cleanup()

	for i := 0; i < 7; i++ {
		seedAuditEvent(t, ctx.db, "ping", "system", 1, -time.Duration(i)*time.Minute)
	}

	w := doGET(ctx.router, "/api/v2/admin/audit/events?per_page=3&page=1")
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	data := parseJSON(t, w)["data"].(map[string]interface{})
	if len(data["events"].([]interface{})) != 3 {
		t.Errorf("page=1 per_page=3: expected 3 events, got %d", len(data["events"].([]interface{})))
	}
	if int(data["total"].(float64)) != 7 {
		t.Errorf("expected total=7, got %v", data["total"])
	}
	if int(data["page"].(float64)) != 1 {
		t.Errorf("expected page=1, got %v", data["page"])
	}

	w = doGET(ctx.router, "/api/v2/admin/audit/events?per_page=3&page=3")
	data = parseJSON(t, w)["data"].(map[string]interface{})
	if got := len(data["events"].([]interface{})); got != 1 {
		t.Errorf("page=3 per_page=3 of 7: expected 1, got %d", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// governance
// ─────────────────────────────────────────────────────────────────────────────

func TestGetGovernanceChannelDistribution_GroupsByChannelType(t *testing.T) {
	ctx := setupAdminGovRouter(t)
	defer ctx.cleanup()

	seedConsumeLog(t, ctx.db, "gpt-4", 1, 1000, 0, "", 1)
	seedConsumeLog(t, ctx.db, "gpt-4", 1, 2000, 0, "", 1)
	seedConsumeLog(t, ctx.db, "claude-3", 14, 500, 0, "", 2)

	w := doGET(ctx.router, "/api/v2/admin/governance/channels?hours=24")
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	stats := parseJSON(t, w)["data"].([]interface{})
	if len(stats) != 2 {
		t.Fatalf("expected 2 channel groups, got %d", len(stats))
	}
	// Higher total_quota (gpt-4 = 3000) sorts first.
	first := stats[0].(map[string]interface{})
	if int(first["total_quota"].(float64)) != 3000 {
		t.Errorf("expected first group total_quota=3000, got %v", first["total_quota"])
	}
}

func TestGetGovernanceLatencyStats_AvgAndMaxPerModel(t *testing.T) {
	ctx := setupAdminGovRouter(t)
	defer ctx.cleanup()

	seedConsumeLog(t, ctx.db, "gpt-4", 1, 100, 200, "", 1)
	seedConsumeLog(t, ctx.db, "gpt-4", 1, 100, 600, "", 1)
	seedConsumeLog(t, ctx.db, "gpt-3.5", 1, 50, 100, "", 1)
	// Single-record model gets filtered out by HAVING COUNT > 1.
	seedConsumeLog(t, ctx.db, "claude-3", 14, 100, 50, "", 2)

	w := doGET(ctx.router, "/api/v2/admin/governance/latency?hours=24")
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	stats := parseJSON(t, w)["data"].([]interface{})

	var found bool
	for _, s := range stats {
		m := s.(map[string]interface{})
		if m["model_name"] == "gpt-4" {
			found = true
			if int(m["max_ms"].(float64)) != 600 {
				t.Errorf("gpt-4 max_ms expected 600, got %v", m["max_ms"])
			}
			if int(m["count"].(float64)) != 2 {
				t.Errorf("gpt-4 count expected 2, got %v", m["count"])
			}
		}
		if m["model_name"] == "claude-3" {
			t.Errorf("claude-3 should be filtered out (only 1 record)")
		}
	}
	if !found {
		t.Error("expected gpt-4 row in latency stats")
	}
}

func TestGetGovernanceEfficiencyStats_DerivedCostMetrics(t *testing.T) {
	ctx := setupAdminGovRouter(t)
	defer ctx.cleanup()

	// 2 requests, total quota 300, total tokens (100+200)*2 = 600
	seedConsumeLog(t, ctx.db, "gpt-4", 1, 100, 0, "", 1)
	seedConsumeLog(t, ctx.db, "gpt-4", 1, 200, 0, "", 1)

	w := doGET(ctx.router, "/api/v2/admin/governance/efficiency?hours=24")
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	stats := parseJSON(t, w)["data"].([]interface{})
	if len(stats) != 1 {
		t.Fatalf("expected 1 efficiency row, got %d", len(stats))
	}
	row := stats[0].(map[string]interface{})
	if row["model_name"] != "gpt-4" {
		t.Errorf("expected model_name=gpt-4, got %v", row["model_name"])
	}
	if got := row["avg_quota_per_request"].(float64); got != 150.0 {
		t.Errorf("avg_quota_per_request expected 150, got %v", got)
	}
	if got := row["quota_per_token"].(float64); got != 0.5 {
		t.Errorf("quota_per_token expected 0.5, got %v", got)
	}
}

func TestGetGovernanceFingerprintStats_DuplicateRate(t *testing.T) {
	ctx := setupAdminGovRouter(t)
	defer ctx.cleanup()

	// Token 1: 12 requests, 4 distinct fingerprints → duplicate_rate = 1 - 4/12 ≈ 0.667
	for i := 0; i < 12; i++ {
		fp := fmt.Sprintf("fp-%d", i%4)
		seedConsumeLog(t, ctx.db, "gpt-4", 1, 100, 0, fp, 1)
	}
	// Token 2: 5 requests (below min_count=10 default) — should be excluded
	for i := 0; i < 5; i++ {
		seedConsumeLog(t, ctx.db, "gpt-4", 1, 100, 0, "fp-a", 2)
	}

	w := doGET(ctx.router, "/api/v2/admin/governance/fingerprints?hours=24&min_count=10")
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	stats := parseJSON(t, w)["data"].([]interface{})
	if len(stats) != 1 {
		t.Fatalf("expected 1 token (above min_count=10), got %d", len(stats))
	}
	row := stats[0].(map[string]interface{})
	if int(row["token_id"].(float64)) != 1 {
		t.Errorf("expected token_id=1, got %v", row["token_id"])
	}
	dr := row["duplicate_rate"].(float64)
	if dr < 0.65 || dr > 0.68 {
		t.Errorf("duplicate_rate expected ~0.667, got %v", dr)
	}
}

func TestGetGovernance_HoursClamping(t *testing.T) {
	ctx := setupAdminGovRouter(t)
	defer ctx.cleanup()

	// hours=0 and hours>720 both clamp to 24 (24h window).
	// Behavior check: endpoint accepts these without 4xx.
	for _, q := range []string{"hours=0", "hours=9999", "hours=-5"} {
		w := doGET(ctx.router, "/api/v2/admin/governance/channels?"+q)
		if w.Code != http.StatusOK {
			t.Errorf("query %q: expected 200 (clamped), got %d", q, w.Code)
		}
	}
}
