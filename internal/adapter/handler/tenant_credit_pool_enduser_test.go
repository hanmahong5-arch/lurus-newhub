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

// Test infrastructure for the EndUser pool readback (Tier 1.2). Stands alone
// from the existing V2TestRouter because the credit-pool tables are not in
// that fixture's AutoMigrate list, and the slug-vs-JWT mismatch path needs
// fine control over the test tenant context.

var endUserPoolTestDBCounter atomic.Int64

type endUserPoolCtx struct {
	router       *gin.Engine
	db           *gorm.DB
	tenantID     string
	tenantSlug   string
	otherTenant  *repo.Tenant
	cleanup      func()
	withTenantID func(string) // override the next request's mock tenant id
}

func setupEndUserPoolRouter(t *testing.T) *endUserPoolCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:eupool%d?mode=memory&cache=shared", endUserPoolTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	tables := []interface{}{
		&repo.User{},
		&repo.Tenant{},
		&entity.TenantCreditPool{},
		&entity.TenantCreditPoolDraw{},
	}
	for _, tbl := range tables {
		if err := db.AutoMigrate(tbl); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("auto migrate %T: %v", tbl, err)
		}
	}

	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	tenantID := "tenant-acme"
	tenantSlug := "acme"
	now := time.Now()
	if err := db.Create(&repo.Tenant{
		Id: tenantID, Name: "Acme", Slug: tenantSlug,
		Status: repo.TenantStatusEnabled, IDPOrgID: "org_acme",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed acme: %v", err)
	}
	other := &repo.Tenant{
		Id: "tenant-other", Name: "Other", Slug: "other",
		Status: repo.TenantStatusEnabled, IDPOrgID: "org_other",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("seed other: %v", err)
	}

	// Mock OIDCAuth that injects a TenantContext keyed on a per-request
	// X-Mock-Tenant-ID header (default: tenantID).
	mockAuth := func(c *gin.Context) {
		mockTID := c.GetHeader("X-Mock-Tenant-ID")
		if mockTID == "" {
			mockTID = tenantID
		}
		c.Set("tenant_context", &middleware.TenantContext{
			TenantID:      mockTID,
			UserID:        42,
			IDPSubject: "zitadel_eu_42",
			Email:         "eu@test.local",
			Username:      "enduser",
		})
		c.Next()
	}

	router := gin.New()
	router.GET("/api/v2/:tenant_slug/credit-pool/me", mockAuth, GetCreditPoolForEndUser)

	cleanup := func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	t.Cleanup(cleanup)

	return &endUserPoolCtx{
		router:      router,
		db:          db,
		tenantID:    tenantID,
		tenantSlug:  tenantSlug,
		otherTenant: other,
		cleanup:     cleanup,
	}
}

func doEndUserPoolGet(ctx *endUserPoolCtx, slug string, mockTenantID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v2/"+slug+"/credit-pool/me", nil)
	if mockTenantID != "" {
		req.Header.Set("X-Mock-Tenant-ID", mockTenantID)
	}
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

func parsePoolView(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}
	return out
}

// ─── Tests ──────────────────────────────────────────────────────────────────

// 1. Unknown slug → 404 TENANT_NOT_FOUND (slug validation happens before
//    the mismatch check, so a typo gets the more specific error).
func TestGetCreditPoolForEndUser_UnknownSlug(t *testing.T) {
	ctx := setupEndUserPoolRouter(t)
	w := doEndUserPoolGet(ctx, "does-not-exist", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body: %s", w.Code, w.Body.String())
	}
	resp := parsePoolView(t, w)
	if resp["error_code"] != "TENANT_NOT_FOUND" {
		t.Errorf("error_code = %v, want TENANT_NOT_FOUND", resp["error_code"])
	}
}

// 2. JWT tenant ≠ URL slug → 403 TENANT_MISMATCH. EndUser authenticated
//    for tenant-other but typed /api/v2/acme/credit-pool/me.
func TestGetCreditPoolForEndUser_TenantMismatch(t *testing.T) {
	ctx := setupEndUserPoolRouter(t)
	w := doEndUserPoolGet(ctx, ctx.tenantSlug, ctx.otherTenant.Id)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body: %s", w.Code, w.Body.String())
	}
	resp := parsePoolView(t, w)
	if resp["error_code"] != "TENANT_MISMATCH" {
		t.Errorf("error_code = %v, want TENANT_MISMATCH", resp["error_code"])
	}
}

// 3. Pool not configured → 200 + unlimited projection. The relay gate
//    treats absence as unlimited; the EndUser view MUST agree.
func TestGetCreditPoolForEndUser_PoolNotConfigured(t *testing.T) {
	ctx := setupEndUserPoolRouter(t)
	w := doEndUserPoolGet(ctx, ctx.tenantSlug, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	resp := parsePoolView(t, w)
	data := resp["data"].(map[string]interface{})
	if data["health"] != "unlimited" {
		t.Errorf("health = %v, want unlimited", data["health"])
	}
	if data["current_balance"] != nil {
		t.Errorf("current_balance = %v, want null", data["current_balance"])
	}
	if data["max_balance"] != nil {
		t.Errorf("max_balance = %v, want null", data["max_balance"])
	}
}

// 4. Balance = 0 → health "red". The relay gate is now blocking.
func TestGetCreditPoolForEndUser_BalanceZero(t *testing.T) {
	ctx := setupEndUserPoolRouter(t)
	seedPool(t, ctx, 0, 1000)
	w := doEndUserPoolGet(ctx, ctx.tenantSlug, "")
	resp := parsePoolView(t, w)
	data := resp["data"].(map[string]interface{})
	if data["health"] != "red" {
		t.Errorf("health = %v, want red", data["health"])
	}
}

// 5. Balance < 20% max → "yellow". Boundary check at 19% to confirm the
//    threshold is strictly below 20%.
func TestGetCreditPoolForEndUser_BalanceYellow(t *testing.T) {
	ctx := setupEndUserPoolRouter(t)
	seedPool(t, ctx, 190, 1000) // 19% — under the 20% threshold
	w := doEndUserPoolGet(ctx, ctx.tenantSlug, "")
	resp := parsePoolView(t, w)
	data := resp["data"].(map[string]interface{})
	if data["health"] != "yellow" {
		t.Errorf("health = %v, want yellow at balance=190/1000", data["health"])
	}
}

// 6. Balance >= 20% max → "green". 200/1000 = 20%, the inclusive lower
//    bound of the green band.
func TestGetCreditPoolForEndUser_BalanceGreen(t *testing.T) {
	ctx := setupEndUserPoolRouter(t)
	seedPool(t, ctx, 200, 1000)
	w := doEndUserPoolGet(ctx, ctx.tenantSlug, "")
	resp := parsePoolView(t, w)
	data := resp["data"].(map[string]interface{})
	if data["health"] != "green" {
		t.Errorf("health = %v, want green at balance=200/1000", data["health"])
	}
	if data["current_balance"].(float64) != 200 {
		t.Errorf("current_balance = %v, want 200", data["current_balance"])
	}
	if data["max_balance"].(float64) != 1000 {
		t.Errorf("max_balance = %v, want 1000", data["max_balance"])
	}
}

// 7. Response field whitelist — admin-only fields (ceiling policy,
//    alert thresholds, owner, reset schedule, draws) MUST never appear,
//    even though they exist on the underlying TenantCreditPool entity.
func TestGetCreditPoolForEndUser_ResponseFieldWhitelist(t *testing.T) {
	ctx := setupEndUserPoolRouter(t)
	seedPool(t, ctx, 800, 1000)
	w := doEndUserPoolGet(ctx, ctx.tenantSlug, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, banned := range []string{
		"alert_threshold_pct", "reset_period", "next_reset_at",
		"alert_fired_at", "created_by_user_id", "owner_user_id",
		"last_topup_at", "parent_tenant_id", "draws", "ceiling",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("response leaks %q — got body: %s", banned, body)
		}
	}
	// Confirm allowed fields are present.
	for _, expected := range []string{`"current_balance"`, `"max_balance"`, `"health"`} {
		if !strings.Contains(body, expected) {
			t.Errorf("response missing %q — got body: %s", expected, body)
		}
	}
}

// seedPool inserts a TenantCreditPool with the given balance + ceiling
// directly. Uses entity types so we don't drag in CreateTenantCreditPool's
// monthly-reset default schedule, which would muddy the "what's leaking"
// assertion in test 7.
func seedPool(t *testing.T, ctx *endUserPoolCtx, current, max int64) {
	t.Helper()
	now := time.Now()
	p := &entity.TenantCreditPool{
		TenantID: ctx.tenantID, CreatedByUserID: 1,
		CurrentBalance: current, MaxBalance: max,
		ResetPeriod:  repo.PoolResetMonthly,
		LastResetAt:  now,
		AlertThresholdPct: 80,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := ctx.db.Create(p).Error; err != nil {
		t.Fatalf("seed pool: %v", err)
	}
}
