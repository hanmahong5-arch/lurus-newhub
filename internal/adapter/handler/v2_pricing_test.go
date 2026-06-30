package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Test infrastructure mirrors playground_fanout_test.go: hermetic SQLite DB,
// no external dependencies, swap global repo.DB before each test.

var pricingTestDBCounter atomic.Int64

type pricingCtx struct {
	router     *gin.Engine
	db         *gorm.DB
	tenantSlug string
	cleanup    func()
}

func setupPricingRouter(t *testing.T) *pricingCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:pricing%d?mode=memory&cache=shared", pricingTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{
		&repo.User{}, &repo.Token{}, &repo.Tenant{}, &repo.Option{},
	} {
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

	slug := "acme-pricing"
	tenant := &repo.Tenant{
		Id:   "acme-pricing-id",
		Slug: slug,
		Name: "Acme Pricing Test",
		// ZitadelOrgID must be unique; use a stable value per test DB
		IDPOrgID: fmt.Sprintf("zitadel-pricing-%d", pricingTestDBCounter.Load()),
		Status:       1,
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	router := gin.New()
	mockAuth := func(c *gin.Context) { c.Next() }
	router.GET("/api/v2/:tenant_slug/pricing", mockAuth, GetPricingV2)

	ctx := &pricingCtx{
		router:     router,
		db:         db,
		tenantSlug: slug,
	}
	ctx.cleanup = func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	t.Cleanup(ctx.cleanup)
	return ctx
}

func getPricing(ctx *pricingCtx, slug string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v2/"+slug+"/pricing", nil)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

func parsePricing(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}
	return out
}

// 1. HappyPath — known tenant slug → 200 with pricing / vendors / group_ratio keys present.
func TestV2Pricing_HappyPath(t *testing.T) {
	ctx := setupPricingRouter(t)

	w := getPricing(ctx, ctx.tenantSlug)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	resp := parsePricing(t, w)
	if resp["success"] != true {
		t.Errorf("success = %v, want true", resp["success"])
	}

	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data field missing or wrong type, body: %s", w.Body.String())
	}
	for _, key := range []string{"pricing", "vendors", "group_ratio"} {
		if _, present := data[key]; !present {
			t.Errorf("data.%s missing from response", key)
		}
	}
}

// 2. TenantNotFound — unknown slug → 404 TENANT_NOT_FOUND.
func TestV2Pricing_TenantNotFound(t *testing.T) {
	ctx := setupPricingRouter(t)

	w := getPricing(ctx, "no-such-tenant-xyz")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body: %s", w.Code, w.Body.String())
	}

	resp := parsePricing(t, w)
	if resp["error_code"] != "TENANT_NOT_FOUND" {
		t.Errorf("error_code = %v, want TENANT_NOT_FOUND", resp["error_code"])
	}
}

// 3. WhitelistEnforced — admin-only fields must not appear in the response body.
func TestV2Pricing_WhitelistEnforced(t *testing.T) {
	ctx := setupPricingRouter(t)

	w := getPricing(ctx, ctx.tenantSlug)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	for _, forbidden := range []string{"usable_group", "auto_groups"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("response body must not contain %q (admin-only field leaked)", forbidden)
		}
	}
}
