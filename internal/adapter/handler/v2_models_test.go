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

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var modelsTestDBCounter atomic.Int64

type modelsTestCtx struct {
	router    *gin.Engine
	db        *gorm.DB
	tenantID  string
	tenantSlug string
	cleanup   func()
}

func setupModelsRouter(t *testing.T) *modelsTestCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:v2models%d?mode=memory&cache=shared", modelsTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	tables := []interface{}{
		&repo.User{}, &repo.Tenant{}, &repo.Model{}, &repo.Vendor{}, &repo.Option{},
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

	dbNum := modelsTestDBCounter.Load()

	// Seed a tenant.
	tenant := &repo.Tenant{
		Id:           fmt.Sprintf("tenant-models-%d", dbNum),
		Slug:         "acme",
		Name:         "Acme Corp",
		Status:       repo.TenantStatusEnabled,
		IDPOrgID: fmt.Sprintf("org_models_%d", dbNum),
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	// Seed a user.
	user := &repo.User{
		Username: "tester", DisplayName: "Tester",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "tester@test.local",
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	ctx := &modelsTestCtx{
		db:         db,
		tenantID:   tenant.Id,
		tenantSlug: "acme",
	}

	router := gin.New()
	// Mock UserAuth: inject user id only — the handler doesn't need it for listing.
	mockAuth := func(c *gin.Context) {
		c.Set("id", user.Id)
		c.Next()
	}
	router.GET("/api/v2/:tenant_slug/models", mockAuth, ListModelsV2)
	ctx.router = router

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

func getModels(ctx *modelsTestCtx, query string) *httptest.ResponseRecorder {
	url := "/api/v2/" + ctx.tenantSlug + "/models"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

func parseModelsResp(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}
	return out
}

func TestV2Models_ListHappyPath(t *testing.T) {
	ctx := setupModelsRouter(t)

	// Insert 3 models.
	models := []*repo.Model{
		{ModelName: "gpt-4o", Status: 1},
		{ModelName: "claude-3.5-sonnet", Status: 1},
		{ModelName: "gemini-1.5-pro", Status: 1},
	}
	for _, m := range models {
		if err := ctx.db.Create(m).Error; err != nil {
			t.Fatalf("seed model %s: %v", m.ModelName, err)
		}
	}

	w := getModels(ctx, "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}

	out := parseModelsResp(t, w)
	if out["success"] != true {
		t.Fatalf("expected success=true, got %v", out["success"])
	}

	data := out["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	// Assert field whitelist: BoundChannels must NOT appear in raw body.
	body := w.Body.String()
	if strings.Contains(body, "BoundChannels") || strings.Contains(body, "bound_channels") {
		t.Error("response body must not contain BoundChannels / bound_channels")
	}
	if strings.Contains(body, "matched_models") || strings.Contains(body, "matched_count") {
		t.Error("response body must not contain matched_models / matched_count")
	}

	// Assert whitelisted fields present on first item.
	first := items[0].(map[string]interface{})
	for _, key := range []string{"id", "model_name", "vendor", "name_rule", "status", "created_time"} {
		if _, ok := first[key]; !ok {
			t.Errorf("expected field %q in item, not present", key)
		}
	}
}

func TestV2Models_VendorFilter(t *testing.T) {
	ctx := setupModelsRouter(t)

	// Create two vendors.
	openai := &repo.Vendor{Name: "OpenAI", Status: 1}
	anthropic := &repo.Vendor{Name: "Anthropic", Status: 1}
	if err := ctx.db.Create(openai).Error; err != nil {
		t.Fatalf("seed vendor OpenAI: %v", err)
	}
	if err := ctx.db.Create(anthropic).Error; err != nil {
		t.Fatalf("seed vendor Anthropic: %v", err)
	}

	// Insert 2 OpenAI + 1 Anthropic models.
	models := []*repo.Model{
		{ModelName: "gpt-4o", VendorID: openai.Id, Status: 1},
		{ModelName: "gpt-4o-mini", VendorID: openai.Id, Status: 1},
		{ModelName: "claude-3.5-sonnet", VendorID: anthropic.Id, Status: 1},
	}
	for _, m := range models {
		if err := ctx.db.Create(m).Error; err != nil {
			t.Fatalf("seed model %s: %v", m.ModelName, err)
		}
	}

	w := getModels(ctx, "vendor=OpenAI")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}

	out := parseModelsResp(t, w)
	data := out["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("expected 2 OpenAI items, got %d", len(items))
	}
	total := int(data["total"].(float64))
	if total != 2 {
		t.Fatalf("expected total=2, got %d", total)
	}
}

func TestV2Models_PaginationClamp(t *testing.T) {
	ctx := setupModelsRouter(t)

	t.Run("limit_zero_returns_400", func(t *testing.T) {
		w := getModels(ctx, "limit=0")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for limit=0, got %d", w.Code)
		}
		out := parseModelsResp(t, w)
		if out["error_code"] != "INVALID_PAGINATION" {
			t.Errorf("expected error_code INVALID_PAGINATION, got %v", out["error_code"])
		}
	})

	t.Run("limit_500_clamped_to_100", func(t *testing.T) {
		w := getModels(ctx, "limit=500")
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for limit=500 (clamped), got %d — body: %s", w.Code, w.Body.String())
		}
		out := parseModelsResp(t, w)
		data := out["data"].(map[string]interface{})
		if int(data["limit"].(float64)) != 100 {
			t.Errorf("expected clamped limit=100, got %v", data["limit"])
		}
	})
}

// TestV2Models_CrossTenantIsolation verifies two security properties:
//  1. A request using an unknown tenant slug is rejected with 404 (tenant
//     validation is the first guard — no model data is served without a valid tenant).
//  2. A request for a valid tenant slug does NOT leak internal-only fields
//     (BoundChannels, MatchedModels, MatchedCount, VendorID) through the
//     modelV2View whitelist.
func TestV2Models_CrossTenantIsolation(t *testing.T) {
	ctx := setupModelsRouter(t)

	// Seed a model so the happy-path assertion has a row to check.
	m := &repo.Model{ModelName: "gpt-4o-cross-test", Status: 1}
	if err := ctx.db.Create(m).Error; err != nil {
		t.Fatalf("seed model: %v", err)
	}

	t.Run("unknown_tenant_returns_404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/no-such-tenant/models", nil)
		w := httptest.NewRecorder()
		ctx.router.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for unknown tenant slug, got %d — body: %s", w.Code, w.Body.String())
		}
		var out map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out["error_code"] != "TENANT_NOT_FOUND" {
			t.Errorf("expected error_code TENANT_NOT_FOUND, got %v", out["error_code"])
		}
	})

	t.Run("modelV2View_forbids_internal_fields", func(t *testing.T) {
		w := getModels(ctx, "")
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
		}
		out := parseModelsResp(t, w)
		data := out["data"].(map[string]interface{})
		items := data["items"].([]interface{})
		if len(items) == 0 {
			t.Fatal("expected at least 1 item in model list")
		}
		first := items[0].(map[string]interface{})
		// Internal enrichment fields must not appear in the serialised response.
		for _, forbidden := range []string{
			"BoundChannels", "bound_channels",
			"MatchedModels", "matched_models",
			"MatchedCount", "matched_count",
			"vendor_id",
		} {
			if _, ok := first[forbidden]; ok {
				t.Errorf("forbidden field %q leaked through modelV2View", forbidden)
			}
		}
		// Whitelisted fields must be present.
		for _, required := range []string{"id", "model_name", "status", "created_time"} {
			if _, ok := first[required]; !ok {
				t.Errorf("expected field %q in modelV2View, not found", required)
			}
		}
	})
}
