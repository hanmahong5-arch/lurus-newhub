package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

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
		&repo.Channel{}, &repo.Ability{},
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
	// Bootstrap OptionMap so repo.UpdateOption (used by tests that seed
	// PricingVersion) does not panic writing into a nil map.
	repo.InitOptionMap()

	slug := "acme-pricing"
	tenant := &repo.Tenant{
		Id:   "acme-pricing-id",
		Slug: slug,
		Name: "Acme Pricing Test",
		// ZitadelOrgID must be unique; use a stable value per test DB
		IDPOrgID: fmt.Sprintf("zitadel-pricing-%d", pricingTestDBCounter.Load()),
		Status:   1,
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

// 4. Version — data.version reflects the database's PricingVersion row (via
// repo.UpdateOption, the same write path the pricing CAS uses), not a value
// baked in at zero. Mutation: deleting the "version" key from GetPricingV2's
// response (or reverting currentPricingVersion to a hard-coded 0) turns
// this red.
func TestGetPricingV2_Version(t *testing.T) {
	ctx := setupPricingRouter(t)

	if err := repo.UpdateOption("PricingVersion", strconv.FormatInt(7, 10)); err != nil {
		t.Fatalf("seed PricingVersion: %v", err)
	}

	w := getPricing(ctx, ctx.tenantSlug)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	resp := parsePricing(t, w)
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data missing, body: %s", w.Body.String())
	}
	if v, _ := data["version"].(float64); int64(v) != 7 {
		t.Errorf("data.version = %v, want 7", data["version"])
	}
}

// 4b. VersionIgnoresStaleProcessCache — data.version must come from the
// database, not the per-process option cache. TestGetPricingV2_Version alone
// cannot prove this: it seeds through repo.UpdateOption, which writes both
// the DB row and common.OptionMap, so a handler that reads either source
// would pass it. This test seeds the DB row directly through ctx.db
// (bypassing repo.UpdateOption/OptionMap entirely) while leaving
// common.OptionMap holding a different, stale value, so only a DB read can
// return the right answer. Mutation: reverting currentPricingVersion to read
// common.OptionMap["PricingVersion"] turns this red (it would return the
// stale 999 instead of 7).
func TestGetPricingV2_Version_DBOnly_NotProcessCache(t *testing.T) {
	ctx := setupPricingRouter(t)

	common.OptionMapRWMutex.Lock()
	common.OptionMap["PricingVersion"] = "999"
	common.OptionMapRWMutex.Unlock()

	if err := ctx.db.Create(&repo.Option{Key: "PricingVersion", Value: "7"}).Error; err != nil {
		t.Fatalf("seed PricingVersion row directly: %v", err)
	}

	w := getPricing(ctx, ctx.tenantSlug)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	resp := parsePricing(t, w)
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data missing, body: %s", w.Body.String())
	}
	if v, _ := data["version"].(float64); int64(v) != 7 {
		t.Errorf("data.version = %v, want 7 (the DB row) — common.OptionMap held a stale 999", data["version"])
	}
}

// 5. CacheRatioPrefill — a model with an explicit cache_ratio entry gets it
// projected into the pricing row so the console can prefill its input
// instead of editing blind; a model with no entry (same catalogue, seeded
// alongside it) omits the field rather than fabricating a value. Mutation:
// deleting the CacheRatio field/population in GetPricingV2 turns this red.
func TestGetPricingV2_CacheRatioPrefill(t *testing.T) {
	ctx := setupPricingRouter(t)

	withRatio := "cache-ratio-prefill-with-entry"
	withoutRatio := "cache-ratio-prefill-without-entry"

	// A minimal enabled ability + channel is enough for repo.GetPricing to
	// list a model — no repo.Model metadata row is required (model_tenant_
	// scope_test.go's seeding pattern).
	ch := &repo.Channel{Type: 1, Status: common.ChannelStatusEnabled, Name: "cache-ratio-prefill-channel", Models: withRatio + "," + withoutRatio, Group: "default"}
	if err := ctx.db.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	for _, model := range []string{withRatio, withoutRatio} {
		if err := ctx.db.Create(&repo.Ability{Group: "default", Model: model, ChannelId: ch.Id, Enabled: true}).Error; err != nil {
			t.Fatalf("seed ability for %q: %v", model, err)
		}
	}

	if err := ratio_setting.UpdateCacheRatioByJSONString(`{"` + withRatio + `":0.42}`); err != nil {
		t.Fatalf("seed live CacheRatio map: %v", err)
	}

	w := getPricing(ctx, ctx.tenantSlug)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	resp := parsePricing(t, w)
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data missing, body: %s", w.Body.String())
	}
	rows, ok := data["pricing"].([]interface{})
	if !ok {
		t.Fatalf("data.pricing missing/wrong type, body: %s", w.Body.String())
	}
	byModel := make(map[string]map[string]interface{}, len(rows))
	for _, r := range rows {
		row, _ := r.(map[string]interface{})
		if row != nil {
			byModel[fmt.Sprint(row["model_name"])] = row
		}
	}

	withRow, ok := byModel[withRatio]
	if !ok {
		t.Fatalf("no pricing row for %q, rows: %v", withRatio, rows)
	}
	if cr, _ := withRow["cache_ratio"].(float64); cr != 0.42 {
		t.Errorf("%s: cache_ratio = %v, want 0.42", withRatio, withRow["cache_ratio"])
	}

	withoutRow, ok := byModel[withoutRatio]
	if !ok {
		t.Fatalf("no pricing row for %q, rows: %v", withoutRatio, rows)
	}
	if _, present := withoutRow["cache_ratio"]; present {
		t.Errorf("%s: cache_ratio = %v, want omitted (no configured entry)", withoutRatio, withoutRow["cache_ratio"])
	}
}
