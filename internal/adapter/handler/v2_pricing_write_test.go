/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Hermetic SQLite setup mirrors the pattern used in v2_pricing_test.go.

// pinnedAuditWriter implements governance.AuditWriter against a fixed *gorm.DB
// captured at construction, instead of the mutable package-global repo.DB —
// see the comment at its call site in setupPricingWriteRouter for why.
type pinnedAuditWriter struct{ db *gorm.DB }

func (w *pinnedAuditWriter) CreateAuditEvent(event *entity.AuditEvent) error {
	return w.db.Create(event).Error
}

var pricingWriteTestDBCounter atomic.Int64

type pricingWriteCtx struct {
	router     *gin.Engine
	db         *gorm.DB
	tenantSlug string
	cleanup    func()
}

func setupPricingWriteRouter(t *testing.T) *pricingWriteCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:pricingwrite%d?mode=memory&cache=shared", pricingWriteTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{
		&repo.User{}, &repo.Token{}, &repo.Tenant{}, &repo.Option{},
		&entity.AuditEvent{}, &entity.AuditChainHead{},
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
	// Bootstrap OptionMap so repo.UpdateOption does not panic on nil map.
	repo.InitOptionMap()
	// Real audit-writer wiring (mirrors cmd/server/main.go's
	// governance.SetAuditWriter(&repo.AuditEventRepo{})), but pinned to THIS
	// test's db via closure rather than the mutable package-global repo.DB:
	// RecordAuditEvent runs on a gopool goroutine that can still be in flight
	// after this test returns and t.Cleanup swaps repo.DB back and closes
	// this connection. A plain &repo.AuditEventRepo{} would then dereference
	// whatever repo.DB has become (nil, or another test's db) and panic
	// inside the pool; writing to this closed *gorm.DB instead just returns
	// an ordinary "database is closed" error that RecordAuditEvent logs.
	governance.SetAuditWriter(&pinnedAuditWriter{db: db})

	slug := "acme-write"
	tenant := &repo.Tenant{
		Id:       "acme-write-id",
		Slug:     slug,
		Name:     "Acme Write Test",
		IDPOrgID: fmt.Sprintf("zitadel-write-%d", pricingWriteTestDBCounter.Load()),
		Status:   1,
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	router := gin.New()
	// mockAuth defaults to an authenticated platform root — UpdatePricingV2
	// replaces the process-wide ratio maps and the global option row, so root
	// is the level it requires. The context shape mirrors the real
	// UserAuth()+TenantSlugGuard() chain, which always populates
	// tenant_context before the handler runs. The gate tests below downgrade
	// the caller via the X-Test-Role header to exercise the 403 path.
	mockAuth := func(c *gin.Context) {
		role := common.RoleRootUser
		switch c.GetHeader("X-Test-Role") {
		case "user":
			role = common.RoleCommonUser
		case "admin":
			role = common.RoleAdminUser
		}
		c.Set("role", role)
		c.Set("tenant_context", &middleware.TenantContext{
			TenantID: tenant.Id,
			UserID:   1,
		})
		c.Next()
	}
	router.POST("/api/v2/:tenant_slug/pricing", mockAuth, UpdatePricingV2)
	router.POST("/api/v2/:tenant_slug/pricing/preview", mockAuth, PreviewPricingV2)

	ctx := &pricingWriteCtx{
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

func postPricing(ctx *pricingWriteCtx, slug string, body interface{}, headers ...map[string]string) *httptest.ResponseRecorder {
	return postPricingPath(ctx, "/api/v2/"+slug+"/pricing", body, headers...)
}

func postPricingPreview(ctx *pricingWriteCtx, slug string, body interface{}, headers ...map[string]string) *httptest.ResponseRecorder {
	return postPricingPath(ctx, "/api/v2/"+slug+"/pricing/preview", body, headers...)
}

func postPricingPath(ctx *pricingWriteCtx, path string, body interface{}, headers ...map[string]string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for _, h := range headers {
		for k, v := range h {
			req.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

// pollAuditRow polls repo.GetAuditEvents (bounded) for the first row with the
// given action — RecordAuditEvent persists via gopool.Go, so the row is not
// guaranteed to exist yet when ServeHTTP returns.
func pollAuditRow(t *testing.T, action string, timeout time.Duration) *entity.AuditEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		events, _, err := repo.GetAuditEvents("", action, 0, "", 0, 0, 0, 10)
		if err == nil && len(events) > 0 {
			return events[0]
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func parsePricingWrite(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}
	return out
}

// 1. HappyPath — valid batch → 200, updated_count == len(batch).
func TestV2PricingWrite_HappyPath(t *testing.T) {
	ctx := setupPricingWriteRouter(t)

	ratio := 1.5
	batch := []map[string]interface{}{
		{"model_name": "gpt-4o", "model_ratio": ratio},
		{"model_name": "claude-3.5-sonnet", "model_price": 3.0},
	}

	w := postPricing(ctx, ctx.tenantSlug, batch)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	resp := parsePricingWrite(t, w)
	if resp["success"] != true {
		t.Errorf("success = %v, want true", resp["success"])
	}

	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data missing, body: %s", w.Body.String())
	}
	count, ok := data["updated_count"].(float64)
	if !ok || int(count) != 2 {
		t.Errorf("updated_count = %v, want 2", data["updated_count"])
	}
}

// 2. TenantNotFound — unknown slug → 404 TENANT_NOT_FOUND.
func TestV2PricingWrite_TenantNotFound(t *testing.T) {
	ctx := setupPricingWriteRouter(t)

	batch := []map[string]interface{}{
		{"model_name": "gpt-4o", "model_ratio": 1.0},
	}

	w := postPricing(ctx, "no-such-tenant-xyz", batch)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body: %s", w.Code, w.Body.String())
	}

	resp := parsePricingWrite(t, w)
	if resp["error_code"] != "TENANT_NOT_FOUND" {
		t.Errorf("error_code = %v, want TENANT_NOT_FOUND", resp["error_code"])
	}
}

// 3. EmptyBatch — empty array body → 400 EMPTY_BATCH.
func TestV2PricingWrite_EmptyBatch(t *testing.T) {
	ctx := setupPricingWriteRouter(t)

	w := postPricing(ctx, ctx.tenantSlug, []interface{}{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}

	resp := parsePricingWrite(t, w)
	if resp["error_code"] != "EMPTY_BATCH" {
		t.Errorf("error_code = %v, want EMPTY_BATCH", resp["error_code"])
	}
}

// 4. MissingModelName — item without model_name → 400 MISSING_MODEL_NAME.
func TestV2PricingWrite_MissingModelName(t *testing.T) {
	ctx := setupPricingWriteRouter(t)

	// model_name is deliberately absent.
	batch := []map[string]interface{}{
		{"model_ratio": 1.5},
	}

	w := postPricing(ctx, ctx.tenantSlug, batch)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}

	resp := parsePricingWrite(t, w)
	if resp["error_code"] != "MISSING_MODEL_NAME" {
		t.Errorf("error_code = %v, want MISSING_MODEL_NAME", resp["error_code"])
	}
}

// 5. RootGate — CRITICAL: UpdatePricingV2 mutates the process-wide ratio maps and
// the single global option row, so it must reject anything below platform root
// with 403 before any write occurs; a root caller for the same batch must still
// succeed (200), guarding against over-tightening.
//
// This used to assert that a tenant admin (role 10) was allowed through. That
// assertion was wrong: the route is mounted under /:tenant_slug but the write has
// no tenant in it, so one tenant's admin repriced every tenant. v1 keeps the
// equivalent write behind middleware.RootAuth().
func TestV2PricingWrite_RootGate(t *testing.T) {
	ctx := setupPricingWriteRouter(t)

	batch := []map[string]interface{}{
		{"model_name": "gpt-4o", "model_ratio": 1.0},
	}

	t.Run("non_admin_forbidden", func(t *testing.T) {
		w := postPricing(ctx, ctx.tenantSlug, batch, map[string]string{"X-Test-Role": "user"})
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403, body: %s", w.Code, w.Body.String())
		}
		resp := parsePricingWrite(t, w)
		if msg, _ := resp["message"].(string); msg != "Root role required" {
			t.Errorf("message = %q, want %q", msg, "Root role required")
		}
	})

	t.Run("tenant_admin_forbidden", func(t *testing.T) {
		w := postPricing(ctx, ctx.tenantSlug, batch, map[string]string{"X-Test-Role": "admin"})
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 for a role-10 tenant admin, body: %s", w.Code, w.Body.String())
		}
	})

	t.Run("root_allowed", func(t *testing.T) {
		w := postPricing(ctx, ctx.tenantSlug, batch)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 for root caller, body: %s", w.Code, w.Body.String())
		}
	})

	// PreviewPricingV2 shares UpdatePricingV2's rationale (the maps it reads
	// are process-global) so it must reject the same non-root caller with
	// 403 and return no diff. This sub-case is the oracle for
	// PreviewPricingV2's requirePlatformRoot check: deleting that check turns
	// it red.
	t.Run("preview_non_admin_forbidden", func(t *testing.T) {
		w := postPricingPreview(ctx, ctx.tenantSlug, batch, map[string]string{"X-Test-Role": "user"})
		if w.Code != http.StatusForbidden {
			t.Fatalf("preview status = %d, want 403, body: %s", w.Code, w.Body.String())
		}
		resp := parsePricingWrite(t, w)
		if _, present := resp["data"]; present {
			t.Errorf("preview returned data for a forbidden caller: %v", resp["data"])
		}
	})

	t.Run("preview_root_allowed", func(t *testing.T) {
		w := postPricingPreview(ctx, ctx.tenantSlug, batch)
		if w.Code != http.StatusOK {
			t.Fatalf("preview status = %d, want 200 for root caller, body: %s", w.Code, w.Body.String())
		}
	})
}

// seedPricingVersion writes PricingVersion directly through repo.UpdateOption
// (package-global repo.DB, which setupPricingWriteRouter has already swapped
// to this test's db) so a test can start from a known baseline version.
func seedPricingVersion(t *testing.T, v int64) {
	t.Helper()
	if err := repo.UpdateOption("PricingVersion", strconv.FormatInt(v, 10)); err != nil {
		t.Fatalf("seed PricingVersion: %v", err)
	}
}

// 6. VersionConflict_NoWrite — a stale If-Match-Pricing-Version header must
// be rejected with 409 before any write, reporting the DB's real version.
func TestV2PricingWrite_VersionConflict_NoWrite(t *testing.T) {
	ctx := setupPricingWriteRouter(t)
	seedPricingVersion(t, 3)

	model := "version-conflict-probe-model"
	baseline := ratio_setting.GetModelRatioCopy()[model]

	batch := []map[string]interface{}{
		{"model_name": model, "model_ratio": 9.9},
	}
	w := postPricing(ctx, ctx.tenantSlug, batch, map[string]string{"If-Match-Pricing-Version": "2"})
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body: %s", w.Code, w.Body.String())
	}

	resp := parsePricingWrite(t, w)
	if resp["error_code"] != "PRICING_VERSION_CONFLICT" {
		t.Errorf("error_code = %v, want PRICING_VERSION_CONFLICT", resp["error_code"])
	}
	if cv, _ := resp["current_version"].(float64); int64(cv) != 3 {
		t.Errorf("current_version = %v, want 3", resp["current_version"])
	}

	if got := ratio_setting.GetModelRatioCopy()[model]; got != baseline {
		t.Errorf("model_ratio map mutated despite version conflict: got %v want %v", got, baseline)
	}

	var opt repo.Option
	err := ctx.db.Where("key = ?", "ModelRatio").First(&opt).Error
	if err == nil {
		t.Errorf("ModelRatio option row was written despite version conflict: %s", opt.Value)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("unexpected error querying ModelRatio row: %v", err)
	}
}

// 7. VersionRace_SecondWriterLoses — two sequential POSTs pinned to the same
// baseline version: the first wins and bumps the version, the second (still
// holding the now-stale header) loses with 409, and the final map holds only
// the first writer's value.
func TestV2PricingWrite_VersionRace_SecondWriterLoses(t *testing.T) {
	ctx := setupPricingWriteRouter(t)
	seedPricingVersion(t, 3)

	model := "version-race-probe-model"

	w1 := postPricing(ctx, ctx.tenantSlug,
		[]map[string]interface{}{{"model_name": model, "model_ratio": 1.11}},
		map[string]string{"If-Match-Pricing-Version": "3"})
	if w1.Code != http.StatusOK {
		t.Fatalf("first writer status = %d, want 200, body: %s", w1.Code, w1.Body.String())
	}
	resp1 := parsePricingWrite(t, w1)
	data1, _ := resp1["data"].(map[string]interface{})
	if nv, _ := data1["new_version"].(float64); int64(nv) != 4 {
		t.Fatalf("first writer new_version = %v, want 4", data1["new_version"])
	}

	w2 := postPricing(ctx, ctx.tenantSlug,
		[]map[string]interface{}{{"model_name": model, "model_ratio": 2.22}},
		map[string]string{"If-Match-Pricing-Version": "3"})
	if w2.Code != http.StatusConflict {
		t.Fatalf("second writer status = %d, want 409, body: %s", w2.Code, w2.Body.String())
	}
	resp2 := parsePricingWrite(t, w2)
	if cv, _ := resp2["current_version"].(float64); int64(cv) != 4 {
		t.Errorf("second writer current_version = %v, want 4", resp2["current_version"])
	}

	if got := ratio_setting.GetModelRatioCopy()[model]; got != 1.11 {
		t.Errorf("final map[%q] = %v, want the first writer's value 1.11", model, got)
	}
}

// 8. NoHeader_SkipsGuard — rollout compatibility (O1): a legacy caller that
// never sends the header must still succeed. Deleted the release O1 flips
// the header to mandatory.
func TestV2PricingWrite_NoHeader_SkipsGuard(t *testing.T) {
	ctx := setupPricingWriteRouter(t)
	model := "no-header-probe-model"

	w := postPricing(ctx, ctx.tenantSlug,
		[]map[string]interface{}{{"model_name": model, "model_ratio": 4.4}})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
}

// 8b. MalformedVersionHeader — a non-numeric If-Match-Pricing-Version header
// must be rejected with 400 INVALID_VERSION_HEADER before any write, not
// silently treated as absent (guard-skipped) or as 0.
func TestV2PricingWrite_MalformedVersionHeader(t *testing.T) {
	ctx := setupPricingWriteRouter(t)
	model := "malformed-header-probe-model"

	w := postPricing(ctx, ctx.tenantSlug,
		[]map[string]interface{}{{"model_name": model, "model_ratio": 1.0}},
		map[string]string{"If-Match-Pricing-Version": "abc"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	resp := parsePricingWrite(t, w)
	if resp["error_code"] != "INVALID_VERSION_HEADER" {
		t.Errorf("error_code = %v, want INVALID_VERSION_HEADER", resp["error_code"])
	}

	var opt repo.Option
	err := ctx.db.Where("key = ?", "ModelRatio").First(&opt).Error
	if err == nil {
		t.Errorf("ModelRatio option row was written despite a malformed header: %s", opt.Value)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("unexpected error querying ModelRatio row: %v", err)
	}
	if got := ratio_setting.GetModelRatioCopy()[model]; got != 0 {
		t.Errorf("in-memory model_ratio changed despite a malformed header: got %v want 0", got)
	}
}

// 9. DBFailure_LeavesMemoryUntouched — a DB write failure must surface as 500
// and never leave the in-memory ratio ahead of the (now unwritten) database.
// Mutation: restoring the old memory-first order turns this red.
func TestV2PricingWrite_DBFailure_LeavesMemoryUntouched(t *testing.T) {
	ctx := setupPricingWriteRouter(t)
	model := "db-failure-probe-model"
	baseline := ratio_setting.GetModelRatioCopy()[model]

	if err := ctx.db.Exec("DROP TABLE options").Error; err != nil {
		t.Fatalf("drop options table: %v", err)
	}

	w := postPricing(ctx, ctx.tenantSlug,
		[]map[string]interface{}{{"model_name": model, "model_ratio": 7.7}})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body: %s", w.Code, w.Body.String())
	}
	resp := parsePricingWrite(t, w)
	if resp["error_code"] != "PERSIST_FAILED" {
		t.Errorf("error_code = %v, want PERSIST_FAILED", resp["error_code"])
	}

	if got := ratio_setting.GetModelRatioCopy()[model]; got != baseline {
		t.Errorf("in-memory model_ratio changed despite a DB failure: got %v want %v", got, baseline)
	}
}

// 10. CacheRatioRoundTrip — cache_ratio can be edited from the console, not
// only imported from the upstream sync whitelist.
func TestV2PricingWrite_CacheRatioRoundTrip(t *testing.T) {
	ctx := setupPricingWriteRouter(t)
	model := "cache-ratio-roundtrip-probe"

	w := postPricing(ctx, ctx.tenantSlug,
		[]map[string]interface{}{{"model_name": model, "cache_ratio": 0.35}})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	if got := ratio_setting.GetCacheRatioCopy()[model]; got != 0.35 {
		t.Errorf("GetCacheRatioCopy()[%q] = %v, want 0.35", model, got)
	}

	var opt repo.Option
	if err := ctx.db.Where("key = ?", "CacheRatio").First(&opt).Error; err != nil {
		t.Fatalf("CacheRatio option row not persisted: %v", err)
	}
	if !strings.Contains(opt.Value, model) {
		t.Errorf("CacheRatio option row = %s, want it to contain %q", opt.Value, model)
	}
}

// 11. AuditRow — a successful commit produces one pricing.updated audit row
// whose Details carry the real from_version/to_version pair and the
// changed model's diff, not merely the substrings "from_version"/
// "to_version" — swapping or zeroing those values in the handler must go
// red here even though a substring-only check would stay green.
// RecordAuditEvent persists via gopool.Go (asynchronously), so this polls
// rather than reading once. Mutation: dropping the RecordAuditEvent call
// turns this red.
func TestV2PricingWrite_AuditRow(t *testing.T) {
	ctx := setupPricingWriteRouter(t)
	seedPricingVersion(t, 3)
	model := "audit-row-probe-model"

	w := postPricing(ctx, ctx.tenantSlug,
		[]map[string]interface{}{{"model_name": model, "model_ratio": 1.23}},
		map[string]string{"If-Match-Pricing-Version": "3"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	ev := pollAuditRow(t, governance.ActionPricingUpdated, 2*time.Second)
	if ev == nil {
		t.Fatal("no pricing.updated audit row appeared within the poll window")
	}

	var details struct {
		FromVersion int64                    `json:"from_version"`
		ToVersion   int64                    `json:"to_version"`
		Diffs       []map[string]interface{} `json:"diffs"`
	}
	if err := json.Unmarshal([]byte(ev.Details), &details); err != nil {
		t.Fatalf("unmarshal audit details: %v — raw: %s", err, ev.Details)
	}
	if details.FromVersion != 3 {
		t.Errorf("from_version = %d, want 3", details.FromVersion)
	}
	if details.ToVersion != 4 {
		t.Errorf("to_version = %d, want 4", details.ToVersion)
	}
	if len(details.Diffs) == 0 {
		t.Fatal("audit details.diffs is empty")
	}
	if details.Diffs[0]["model_name"] != model {
		t.Errorf("diffs[0].model_name = %v, want %q", details.Diffs[0]["model_name"], model)
	}
}

// 12. PricingPreview_NeverPersists — preview returns diffs/updated_count and
// leaves the options table, PricingVersion, and the live ratio maps
// untouched.
func TestV2PricingPreview_NeverPersists(t *testing.T) {
	ctx := setupPricingWriteRouter(t)
	model := "preview-never-persists-probe"

	var before int64
	ctx.db.Model(&repo.Option{}).Count(&before)
	versionBefore := currentPricingVersion()

	w := postPricingPreview(ctx, ctx.tenantSlug,
		[]map[string]interface{}{{"model_name": model, "model_ratio": 5.5}})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	resp := parsePricingWrite(t, w)
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data missing, body: %s", w.Body.String())
	}
	diffs, ok := data["diffs"].([]interface{})
	if !ok || len(diffs) == 0 {
		t.Fatalf("diffs missing/empty, body: %s", w.Body.String())
	}
	if uc, _ := data["updated_count"].(float64); int(uc) != 1 {
		t.Errorf("updated_count = %v, want 1", data["updated_count"])
	}

	var after int64
	ctx.db.Model(&repo.Option{}).Count(&after)
	if after != before {
		t.Errorf("options row count changed: before=%d after=%d — preview must never persist", before, after)
	}
	if got := currentPricingVersion(); got != versionBefore {
		t.Errorf("PricingVersion changed by preview: before=%d after=%d", versionBefore, got)
	}
	if got := ratio_setting.GetModelRatioCopy()[model]; got != 0 {
		t.Errorf("preview mutated the live ratio map: got %v, want untouched (0)", got)
	}
}

// 12b. PricingPreview_InvalidBatch_Rejected — the preview route runs the same
// validatePricingBatch UpdatePricingV2 does, before computing any diff.
// This is the oracle for that validation branch in PreviewPricingV2 — the
// invalid batch must be rejected before any diff is computed. Mutation: short-circuiting that
// branch (e.g. `if false && !ok`) turns this red.
func TestV2PricingPreview_InvalidBatch_Rejected(t *testing.T) {
	ctx := setupPricingWriteRouter(t)

	t.Run("empty_batch", func(t *testing.T) {
		w := postPricingPreview(ctx, ctx.tenantSlug, []map[string]interface{}{})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
		}
		resp := parsePricingWrite(t, w)
		if resp["error_code"] != "EMPTY_BATCH" {
			t.Errorf("error_code = %v, want EMPTY_BATCH", resp["error_code"])
		}
		if _, present := resp["data"]; present {
			t.Errorf("preview returned data for a rejected batch: %v", resp["data"])
		}
	})

	t.Run("missing_model_name", func(t *testing.T) {
		w := postPricingPreview(ctx, ctx.tenantSlug,
			[]map[string]interface{}{{"model_ratio": 1.0}})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
		}
		resp := parsePricingWrite(t, w)
		if resp["error_code"] != "MISSING_MODEL_NAME" {
			t.Errorf("error_code = %v, want MISSING_MODEL_NAME", resp["error_code"])
		}
	})

	t.Run("non_positive_ratio", func(t *testing.T) {
		w := postPricingPreview(ctx, ctx.tenantSlug,
			[]map[string]interface{}{{"model_name": "preview-invalid-ratio-model", "model_ratio": 0}})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
		}
		resp := parsePricingWrite(t, w)
		if resp["error_code"] != "INVALID_RATIO" {
			t.Errorf("error_code = %v, want INVALID_RATIO", resp["error_code"])
		}
	})
}

// 13. PartialBatchFailure_RollsBackEarlierFields — a failure on the third
// field in a batch that touches three fields must roll back the first two as
// well: the four ratio-map persists plus the version CAS are one
// transaction, not four independent repo.UpdateOption calls.
func TestV2PricingWrite_PartialBatchFailure_RollsBackEarlierFields(t *testing.T) {
	ctx := setupPricingWriteRouter(t)

	modelA := "partial-fail-ratio-model"
	modelB := "partial-fail-completion-model"
	modelFail := "partial-fail-FAILPROBE-model"

	baselineRatio := ratio_setting.GetModelRatioCopy()[modelA]
	baselineCompletion := ratio_setting.GetCompletionRatioCopy()[modelB]
	baselinePrice := ratio_setting.GetModelPriceCopy()[modelFail]

	// Surgical failure on ONLY the ModelPrice persist: a SQLite trigger keyed
	// to a sentinel substring in the marshaled JSON value. DROP TABLE (used by
	// the DBFailure test above) would fail every persist and could not prove
	// that the first two, having genuinely succeeded inside the transaction,
	// were rolled back rather than never attempted.
	for _, stmt := range []string{
		`CREATE TRIGGER pricing_partial_fail_insert BEFORE INSERT ON options
		 WHEN NEW.key='ModelPrice' AND NEW.value LIKE '%FAILPROBE%'
		 BEGIN SELECT RAISE(ABORT,'injected failure'); END;`,
		`CREATE TRIGGER pricing_partial_fail_update BEFORE UPDATE ON options
		 WHEN NEW.key='ModelPrice' AND NEW.value LIKE '%FAILPROBE%'
		 BEGIN SELECT RAISE(ABORT,'injected failure'); END;`,
	} {
		if err := ctx.db.Exec(stmt).Error; err != nil {
			t.Fatalf("install fail-probe trigger: %v", err)
		}
	}

	batch := []map[string]interface{}{
		{"model_name": modelA, "model_ratio": 8.1},
		{"model_name": modelB, "completion_ratio": 8.2},
		{"model_name": modelFail, "model_price": 8.3},
	}
	w := postPricing(ctx, ctx.tenantSlug, batch)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body: %s", w.Code, w.Body.String())
	}

	// (a) DB: this is a fresh per-test database, so any of these three rows
	// existing at all proves a persist that should have rolled back did not.
	var count int64
	ctx.db.Model(&repo.Option{}).
		Where("key IN ?", []string{"ModelRatio", "CompletionRatio", "ModelPrice"}).
		Count(&count)
	if count != 0 {
		t.Errorf("options rows survived a rolled-back transaction: count=%d, want 0", count)
	}

	// (b) memory: none of the three in-memory maps were mutated either.
	if got := ratio_setting.GetModelRatioCopy()[modelA]; got != baselineRatio {
		t.Errorf("ModelRatio mutated despite rollback: got %v want %v", got, baselineRatio)
	}
	if got := ratio_setting.GetCompletionRatioCopy()[modelB]; got != baselineCompletion {
		t.Errorf("CompletionRatio mutated despite rollback: got %v want %v", got, baselineCompletion)
	}
	if got := ratio_setting.GetModelPriceCopy()[modelFail]; got != baselinePrice {
		t.Errorf("ModelPrice mutated despite rollback: got %v want %v", got, baselinePrice)
	}
}

// 14. BatchAppliesOnDBBaseline_NotStaleMemory — the batch must be applied on
// top of the database's committed ModelRatio row, not this process's
// (possibly stale) in-memory copy: a model another replica already priced
// and committed, which this process's memory has never seen, must survive a
// write that touches a different model. Mutation: reading the base map from
// ratio_setting.GetModelRatioCopy() instead of the database row (the old,
// memory-first behaviour) turns this red — the seeded model disappears from
// the persisted JSON.
func TestV2PricingWrite_BatchAppliesOnDBBaseline_NotStaleMemory(t *testing.T) {
	ctx := setupPricingWriteRouter(t)

	dbOnlyModel := "db-only-committed-by-another-replica"
	newModel := "db-baseline-probe-new-model"

	// Simulate "another replica already committed this" by writing the
	// ModelRatio option row directly through the test's *gorm.DB, bypassing
	// ratio_setting entirely — this process's in-memory ModelRatio map never
	// learns about dbOnlyModel.
	seedJSON, err := json.Marshal(map[string]float64{dbOnlyModel: 42.0})
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	if err := ctx.db.Create(&repo.Option{Key: "ModelRatio", Value: string(seedJSON)}).Error; err != nil {
		t.Fatalf("seed ModelRatio row: %v", err)
	}
	if got := ratio_setting.GetModelRatioCopy()[dbOnlyModel]; got != 0 {
		t.Fatalf("test setup invariant broken: in-memory map already knows dbOnlyModel (%v)", got)
	}

	w := postPricing(ctx, ctx.tenantSlug,
		[]map[string]interface{}{{"model_name": newModel, "model_ratio": 9.9}})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var opt repo.Option
	if err := ctx.db.Where("key = ?", "ModelRatio").First(&opt).Error; err != nil {
		t.Fatalf("read ModelRatio row: %v", err)
	}
	var persisted map[string]float64
	if err := json.Unmarshal([]byte(opt.Value), &persisted); err != nil {
		t.Fatalf("unmarshal persisted ModelRatio: %v — raw: %s", err, opt.Value)
	}
	if persisted[dbOnlyModel] != 42.0 {
		t.Errorf("persisted ModelRatio lost the other replica's committed entry: got %v, want 42.0 (full row: %v)", persisted[dbOnlyModel], persisted)
	}
	if persisted[newModel] != 9.9 {
		t.Errorf("persisted ModelRatio missing this write's own entry: got %v, want 9.9", persisted[newModel])
	}

	// The post-commit in-memory apply replaces the whole map with the
	// persisted JSON, so memory should now also know about dbOnlyModel.
	if got := ratio_setting.GetModelRatioCopy()[dbOnlyModel]; got != 42.0 {
		t.Errorf("in-memory ModelRatio did not heal to the DB baseline: got %v, want 42.0", got)
	}
}

// 15. PreviewDiff_OldReflectsEffectiveDefault — a model with no explicit
// cache_ratio entry must show the effective default (1, ratio_setting's
// GetCacheRatio fallback) as "old", not a misleading 0, and old_explicit
// must be false; a model with an explicit entry must show that entry with
// old_explicit true.
func TestV2PricingPreview_OldReflectsEffectiveDefault(t *testing.T) {
	ctx := setupPricingWriteRouter(t)

	noEntryModel := "preview-default-no-cache-ratio-entry"
	explicitModel := "preview-default-explicit-cache-ratio-entry"

	seedJSON, err := json.Marshal(map[string]float64{explicitModel: 0.42})
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	if err := ratio_setting.UpdateCacheRatioByJSONString(string(seedJSON)); err != nil {
		t.Fatalf("seed live CacheRatio map: %v", err)
	}

	w := postPricingPreview(ctx, ctx.tenantSlug, []map[string]interface{}{
		{"model_name": noEntryModel, "cache_ratio": 0.9},
		{"model_name": explicitModel, "cache_ratio": 0.9},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	resp := parsePricingWrite(t, w)
	data, _ := resp["data"].(map[string]interface{})
	diffs, _ := data["diffs"].([]interface{})
	byModel := make(map[string]map[string]interface{}, len(diffs))
	for _, d := range diffs {
		entry, _ := d.(map[string]interface{})
		if entry != nil {
			byModel[fmt.Sprint(entry["model_name"])] = entry
		}
	}

	noEntry, ok := byModel[noEntryModel]
	if !ok {
		t.Fatalf("no diff entry for %q, diffs: %v", noEntryModel, diffs)
	}
	if old, _ := noEntry["old"].(float64); old != 1 {
		t.Errorf("%s: old = %v, want the GetCacheRatio default 1", noEntryModel, noEntry["old"])
	}
	if explicit, _ := noEntry["old_explicit"].(bool); explicit {
		t.Errorf("%s: old_explicit = true, want false (no configured entry)", noEntryModel)
	}

	explicitEntry, ok := byModel[explicitModel]
	if !ok {
		t.Fatalf("no diff entry for %q, diffs: %v", explicitModel, diffs)
	}
	if old, _ := explicitEntry["old"].(float64); old != 0.42 {
		t.Errorf("%s: old = %v, want the configured 0.42", explicitModel, explicitEntry["old"])
	}
	if explicit, _ := explicitEntry["old_explicit"].(bool); !explicit {
		t.Errorf("%s: old_explicit = false, want true (configured entry)", explicitModel)
	}
}

// 16. ContextTiers_PersistedInSameTx — a batch that includes a context_tiers
// patch persists the ContextLengthTiers option row and updates the live
// ratio_setting map, riding the same transaction/CAS/audit path as the other
// four pricing maps (cycle-8 L5, extending cycle-7 L1's write).
func TestV2PricingWrite_ContextTiers_PersistedInSameTx(t *testing.T) {
	ctx := setupPricingWriteRouter(t)
	prevTiers := ratio_setting.ContextLengthTiers2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateContextLengthTiersByJSONString(prevTiers) })
	model := "context-tiers-persist-probe"

	batch := []map[string]interface{}{
		{
			"model_name": model,
			"context_tiers": []map[string]interface{}{
				{"threshold_tokens": 0, "model_ratio": 1.0},
				{"threshold_tokens": 3000, "model_ratio": 2.0},
			},
		},
	}
	w := postPricing(ctx, ctx.tenantSlug, batch)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	tier := ratio_setting.GetContextLengthTier(model, 3000)
	if tier == nil || tier.ModelRatio == nil || *tier.ModelRatio != 2.0 {
		t.Errorf("GetContextLengthTier(%q,3000) after write = %+v, want tier with ModelRatio=2.0", model, tier)
	}

	var opt repo.Option
	if err := ctx.db.Where("key = ?", "ContextLengthTiers").First(&opt).Error; err != nil {
		t.Fatalf("ContextLengthTiers option row not persisted: %v", err)
	}
	if !strings.Contains(opt.Value, model) {
		t.Errorf("ContextLengthTiers option row = %s, want it to contain %q", opt.Value, model)
	}
}

// 17. ContextTiers_RollsBackWithBatch — extends
// TestV2PricingWrite_PartialBatchFailure_RollsBackEarlierFields: a batch
// carrying both a context_tiers patch and a FAILPROBE-triggered ModelPrice
// failure must roll back the ContextLengthTiers persist and the in-memory
// tier map too, not just the four pre-existing maps.
func TestV2PricingWrite_ContextTiers_RollsBackWithBatch(t *testing.T) {
	ctx := setupPricingWriteRouter(t)
	prevTiers := ratio_setting.ContextLengthTiers2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateContextLengthTiersByJSONString(prevTiers) })
	model := "context-tiers-rollback-probe"
	modelFail := "context-tiers-rollback-FAILPROBE-model"

	for _, stmt := range []string{
		`CREATE TRIGGER pricing_ctx_tier_fail_insert BEFORE INSERT ON options
		 WHEN NEW.key='ModelPrice' AND NEW.value LIKE '%FAILPROBE%'
		 BEGIN SELECT RAISE(ABORT,'injected failure'); END;`,
		`CREATE TRIGGER pricing_ctx_tier_fail_update BEFORE UPDATE ON options
		 WHEN NEW.key='ModelPrice' AND NEW.value LIKE '%FAILPROBE%'
		 BEGIN SELECT RAISE(ABORT,'injected failure'); END;`,
	} {
		if err := ctx.db.Exec(stmt).Error; err != nil {
			t.Fatalf("install fail-probe trigger: %v", err)
		}
	}

	batch := []map[string]interface{}{
		{
			"model_name": model,
			"context_tiers": []map[string]interface{}{
				{"threshold_tokens": 0, "model_ratio": 1.0},
			},
		},
		{"model_name": modelFail, "model_price": 8.3},
	}
	w := postPricing(ctx, ctx.tenantSlug, batch)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body: %s", w.Code, w.Body.String())
	}

	var count int64
	ctx.db.Model(&repo.Option{}).Where("key = ?", "ContextLengthTiers").Count(&count)
	if count != 0 {
		t.Errorf("ContextLengthTiers option row survived a rolled-back transaction: count=%d, want 0", count)
	}
	if got := ratio_setting.GetContextLengthTier(model, 0); got != nil {
		t.Errorf("in-memory context tiers mutated despite rollback: GetContextLengthTier(%q,0) = %+v, want nil", model, got)
	}
}

// 18. ContextTiers_AuditDiff — the pricing.updated audit row's details carry
// a context_tiers diff entry for a context_tiers-only patch (no ratio/price
// fields touched), mirroring TestV2PricingWrite_AuditRow for the new field.
func TestV2PricingWrite_ContextTiers_AuditDiff(t *testing.T) {
	ctx := setupPricingWriteRouter(t)
	prevTiers := ratio_setting.ContextLengthTiers2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateContextLengthTiersByJSONString(prevTiers) })
	seedPricingVersion(t, 5)
	model := "context-tiers-audit-probe"

	batch := []map[string]interface{}{
		{
			"model_name": model,
			"context_tiers": []map[string]interface{}{
				{"threshold_tokens": 0, "model_ratio": 1.5},
			},
		},
	}
	w := postPricing(ctx, ctx.tenantSlug, batch, map[string]string{"If-Match-Pricing-Version": "5"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	ev := pollAuditRow(t, governance.ActionPricingUpdated, 2*time.Second)
	if ev == nil {
		t.Fatal("no pricing.updated audit row appeared within the poll window")
	}

	var details struct {
		Diffs []map[string]interface{} `json:"diffs"`
	}
	if err := json.Unmarshal([]byte(ev.Details), &details); err != nil {
		t.Fatalf("unmarshal audit details: %v — raw: %s", err, ev.Details)
	}
	var found bool
	for _, d := range details.Diffs {
		if d["model_name"] == model && d["field"] == "context_tiers" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("audit details.diffs has no context_tiers entry for %q: %v", model, details.Diffs)
	}
}

// 19. ContextTiers_InvalidRejected — cycle-8 plan §8 L5 A-F2: a batch
// carrying non-ascending thresholds must be rejected with 400
// INVALID_CONTEXT_TIERS at the route, before validatePricingBatch's
// business-rule check is bypassed. Without this check the invalid list would
// be persisted by the fifth UpdateOptionTx inside the transaction while the
// post-commit apply (ratio_setting.UpdateContextLengthTiersByJSONString)
// silently rejects it — the database row and process memory would diverge.
func TestV2PricingWrite_ContextTiers_InvalidRejected(t *testing.T) {
	ctx := setupPricingWriteRouter(t)
	prevTiers := ratio_setting.ContextLengthTiers2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateContextLengthTiersByJSONString(prevTiers) })
	seedPricingVersion(t, 5)
	model := "context-tiers-invalid-route-probe"

	batch := []map[string]interface{}{
		{
			"model_name": model,
			"context_tiers": []map[string]interface{}{
				{"threshold_tokens": 3000, "model_ratio": 1.0},
				{"threshold_tokens": 1000, "model_ratio": 2.0},
			},
		},
	}
	w := postPricing(ctx, ctx.tenantSlug, batch, map[string]string{"If-Match-Pricing-Version": "5"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if out["error_code"] != "INVALID_CONTEXT_TIERS" {
		t.Errorf("error_code = %v, want INVALID_CONTEXT_TIERS", out["error_code"])
	}

	var count int64
	ctx.db.Model(&repo.Option{}).Where("key = ?", "ContextLengthTiers").Count(&count)
	if count != 0 {
		t.Errorf("ContextLengthTiers option row = %d rows, want 0 (rejected before the transaction opens)", count)
	}
	if got := ratio_setting.GetContextLengthTier(model, 5000); got != nil {
		t.Errorf("GetContextLengthTier(%q,5000) = %+v, want nil (invalid list must never reach memory)", model, got)
	}
	if got := readPricingVersionRow(t); got != 5 {
		t.Errorf("PricingVersion row = %d, want 5 (rejected write must not bump the version)", got)
	}
}

// 20. ContextTiers_RejectedForPerCallModel — cycle-8 plan §8 L5 B-F6: a tier
// list on a per-call priced model can never take effect
// (helper.ModelPriceHelper's tier branch only runs under !UsePrice), so the
// write must reject it with a typed error instead of storing and echoing it
// back. Covers both ways a batch item can be "per-call": the model already
// has a price entry today, and this same item sets model_price.
func TestV2PricingWrite_ContextTiers_RejectedForPerCallModel(t *testing.T) {
	ctx := setupPricingWriteRouter(t)
	prevTiers := ratio_setting.ContextLengthTiers2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateContextLengthTiersByJSONString(prevTiers) })
	seedPricingVersion(t, 5)

	t.Run("already per-call priced", func(t *testing.T) {
		model := "context-tiers-percall-existing-probe"
		prevPrice := ratio_setting.ModelPrice2JSONString()
		t.Cleanup(func() { _ = ratio_setting.UpdateModelPriceByJSONString(prevPrice) })
		if err := ratio_setting.UpdateModelPriceByJSONString(`{"` + model + `":0.02}`); err != nil {
			t.Fatalf("seed model price: %v", err)
		}

		batch := []map[string]interface{}{
			{
				"model_name": model,
				"context_tiers": []map[string]interface{}{
					{"threshold_tokens": 0, "model_ratio": 1.0},
				},
			},
		}
		w := postPricing(ctx, ctx.tenantSlug, batch, map[string]string{"If-Match-Pricing-Version": "5"})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
		}
		var out map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out["error_code"] != "INVALID_CONTEXT_TIERS" {
			t.Errorf("error_code = %v, want INVALID_CONTEXT_TIERS", out["error_code"])
		}
		if got := ratio_setting.GetContextLengthTier(model, 0); got != nil {
			t.Errorf("GetContextLengthTier(%q,0) = %+v, want nil", model, got)
		}
	})

	t.Run("made per-call by this same batch item", func(t *testing.T) {
		model := "context-tiers-percall-newprice-probe"
		batch := []map[string]interface{}{
			{
				"model_name":  model,
				"model_price": 0.05,
				"context_tiers": []map[string]interface{}{
					{"threshold_tokens": 0, "model_ratio": 1.0},
				},
			},
		}
		w := postPricing(ctx, ctx.tenantSlug, batch, map[string]string{"If-Match-Pricing-Version": "5"})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
		}
		var out map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out["error_code"] != "INVALID_CONTEXT_TIERS" {
			t.Errorf("error_code = %v, want INVALID_CONTEXT_TIERS", out["error_code"])
		}
	})

	t.Run("explicit empty list still clears on a per-call model", func(t *testing.T) {
		model := "context-tiers-percall-clear-probe"
		prevPrice := ratio_setting.ModelPrice2JSONString()
		t.Cleanup(func() { _ = ratio_setting.UpdateModelPriceByJSONString(prevPrice) })
		if err := ratio_setting.UpdateModelPriceByJSONString(`{"` + model + `":0.02}`); err != nil {
			t.Fatalf("seed model price: %v", err)
		}
		batch := []map[string]interface{}{
			{"model_name": model, "context_tiers": []map[string]interface{}{}},
		}
		w := postPricing(ctx, ctx.tenantSlug, batch, map[string]string{"If-Match-Pricing-Version": "5"})
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 (clearing an empty tier list is always allowed), body: %s", w.Code, w.Body.String())
		}
	})
}

// 21. ContextTiers_PreviewIncludesDiff — cycle-8 plan §8 L5 A-F3: the
// preview route (used by neither TestV2PricingWrite_ContextTiers_* test
// above, which all post to the live save route) must surface a context_tiers
// diff entry and must not write anything, matching the four ratio/price
// fields' existing preview coverage.
func TestV2PricingWrite_ContextTiers_PreviewIncludesDiff(t *testing.T) {
	ctx := setupPricingWriteRouter(t)
	prevTiers := ratio_setting.ContextLengthTiers2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateContextLengthTiersByJSONString(prevTiers) })
	model := "context-tiers-preview-probe"

	batch := []map[string]interface{}{
		{
			"model_name": model,
			"context_tiers": []map[string]interface{}{
				{"threshold_tokens": 0, "model_ratio": 1.0},
				{"threshold_tokens": 4000, "model_ratio": 3.0},
			},
		},
	}
	w := postPricingPreview(ctx, ctx.tenantSlug, batch)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			Diffs []map[string]interface{} `json:"diffs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v — body: %s", err, w.Body.String())
	}
	var found bool
	for _, d := range resp.Data.Diffs {
		if d["model_name"] == model && d["field"] == "context_tiers" {
			found = true
			if _, ok := d["new"]; !ok {
				t.Errorf("context_tiers diff entry has no \"new\" key: %v", d)
			}
		}
	}
	if !found {
		t.Errorf("preview response diffs has no context_tiers entry for %q: %v", model, resp.Data.Diffs)
	}

	// The preview route must never persist.
	var count int64
	ctx.db.Model(&repo.Option{}).Where("key = ?", "ContextLengthTiers").Count(&count)
	if count != 0 {
		t.Errorf("ContextLengthTiers option row = %d rows, want 0 (preview must never write)", count)
	}
	if got := ratio_setting.GetContextLengthTier(model, 4000); got != nil {
		t.Errorf("GetContextLengthTier(%q,4000) = %+v, want nil (preview must never mutate the live map)", model, got)
	}
}
