package handler

// v2_analytics_rankings_test.go — GetTenantRankingsV2: the tenant-admin
// gate and per-tenant scoping of the model/vendor leaderboard. Self-
// contained hermetic SQLite router, mirroring v2_pricing_write_test.go's
// pattern (mock tenant_context + role, no TenantSlugGuard/UserAuth — those
// are proven elsewhere).

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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

var tenantRankingsTestDBCounter atomic.Int64

type tenantRankingsCtx struct {
	router   *gin.Engine
	db       *gorm.DB
	tenantID string
	cleanup  func()
}

func setupTenantRankingsRouter(t *testing.T) *tenantRankingsCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	n := tenantRankingsTestDBCounter.Add(1)
	dsn := fmt.Sprintf("file:tenantrankings%d?mode=memory&cache=shared", n)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.Log{}); err != nil {
		t.Fatalf("auto migrate entity.Log: %v", err)
	}

	prevDB, prevLog := repo.DB, repo.LOG_DB
	prevSQLite, prevPG := common.UsingSQLite, common.UsingPostgreSQL
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false

	tenantID := fmt.Sprintf("tenant-rankings-%d", n)

	router := gin.New()
	// mockAuth's context shape mirrors the real UserAuth()+TenantSlugGuard()
	// chain, which always populates tenant_context before the handler runs
	// (same rationale as setupPricingWriteRouter's mockAuth). X-Test-Role
	// downgrades the caller to exercise the 403 gate.
	mockAuth := func(c *gin.Context) {
		role := common.RoleCommonUser
		switch c.GetHeader("X-Test-Role") {
		case "admin":
			role = common.RoleAdminUser
		case "root":
			role = common.RoleRootUser
		}
		c.Set("role", role)
		c.Set("tenant_context", &middleware.TenantContext{
			TenantID: tenantID,
			UserID:   1,
		})
		c.Next()
	}
	router.GET("/api/v2/:tenant_slug/analytics/rankings", mockAuth, GetTenantRankingsV2)

	// Each test uses a fresh, unique tenantID (tenant-rankings-N), so it
	// cannot collide with another test's cache entries, but reset anyway
	// for symmetry with setupAnalyticsRouter and to keep the seam obvious.
	t.Cleanup(resetRankingsCacheForTest)
	resetRankingsCacheForTest()

	return &tenantRankingsCtx{
		router:   router,
		db:       db,
		tenantID: tenantID,
		cleanup: func() {
			repo.DB, repo.LOG_DB = prevDB, prevLog
			common.UsingSQLite, common.UsingPostgreSQL = prevSQLite, prevPG
			if sqlDB, err := db.DB(); err == nil {
				_ = sqlDB.Close()
			}
		},
	}
}

func doGETWithHeaders(router *gin.Engine, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func seedTenantRankingsLog(t *testing.T, db *gorm.DB, tenantID, model string, prompt, completion, quota int, createdAt int64) {
	t.Helper()
	l := &entity.Log{
		UserId:           1,
		TenantId:         tenantID,
		Type:             entity.LogTypeConsume,
		ModelName:        model,
		Quota:            quota,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		CreatedAt:        createdAt,
	}
	if err := db.Create(l).Error; err != nil {
		t.Fatalf("seed tenant rankings log: %v", err)
	}
}

// TestTenantRankingsV2_ForbiddenForNormalUser: tenant-wide rankings cover
// every member's usage, so the route carries the same admin gate as
// GetAllLogStatV2 (v2_log_stat.go:87-113).
func TestTenantRankingsV2_ForbiddenForNormalUser(t *testing.T) {
	ctx := setupTenantRankingsRouter(t)
	defer ctx.cleanup()

	w := doGET(ctx.router, "/api/v2/acme/analytics/rankings")
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 for a normal (non-admin) caller, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestTenantRankingsV2_AdminSeesOwnTenantOnly: a tenant admin's rankings
// must reflect only their own tenant's usage, even when another tenant logs
// the same model name at a much larger volume, AND even when the request
// itself tries to ask for the other tenant via a `tenant_id` query param —
// the handler must resolve tenant id from the tenant context only (never
// from the query; see GetTenantRankingsV2's doc comment). The mutation
// named in the plan (drop tenant_id where clause) is exercised at the repo
// layer by TestGetRankings_TenantIsolation; this test proves the same
// guarantee holds end-to-end through the HTTP handler, including against a
// handler that reads `tenant_id` from the query as a fallback.
func TestTenantRankingsV2_AdminSeesOwnTenantOnly(t *testing.T) {
	ctx := setupTenantRankingsRouter(t)
	defer ctx.cleanup()

	now := time.Now().Unix()
	seedTenantRankingsLog(t, ctx.db, ctx.tenantID, "shared-model", 100, 50, 30, now-60)
	seedTenantRankingsLog(t, ctx.db, "other-tenant", "shared-model", 100_000, 50_000, 30_000, now-60)

	w := doGETWithHeaders(ctx.router, "/api/v2/acme/analytics/rankings?by=model&hours=1&tenant_id=other-tenant",
		map[string]string{"X-Test-Role": "admin"})
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	body := parseJSON(t, w)
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing data object: %s", w.Body.String())
	}
	if got := data["by"]; got != "model" {
		t.Errorf("data.by = %v, want model", got)
	}
	rows, ok := data["rows"].([]interface{})
	if !ok {
		t.Fatalf("missing rows array: %s", w.Body.String())
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row scoped to the caller's own tenant, got %d: %v", len(rows), rows)
	}
	row := rows[0].(map[string]interface{})
	if row["name"] != "shared-model" {
		t.Errorf("name = %v, want shared-model", row["name"])
	}
	if got := row["total_tokens"].(float64); got != 150 {
		t.Errorf("total_tokens = %v, want 150 (other tenant's 150000 leaked in)", got)
	}
	if got := row["quota"].(float64); got != 30 {
		t.Errorf("quota = %v, want 30 (other tenant's 30000 leaked in)", got)
	}
}

// TestTenantRankingsV2_RootRoleAlsoAdmitted: requireTenantAdmin's gate is
// >= RoleAdminUser, so a platform-root caller (who outranks tenant-admin)
// must also pass — mirrors GetAllLogStatV2's gate semantics.
func TestTenantRankingsV2_RootRoleAlsoAdmitted(t *testing.T) {
	ctx := setupTenantRankingsRouter(t)
	defer ctx.cleanup()

	now := time.Now().Unix()
	seedTenantRankingsLog(t, ctx.db, ctx.tenantID, "alpha", 10, 5, 3, now-60)

	w := doGETWithHeaders(ctx.router, "/api/v2/acme/analytics/rankings",
		map[string]string{"X-Test-Role": "root"})
	if w.Code != http.StatusOK {
		t.Fatalf("root caller should be admitted, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestSnapRankingHours locks the preset-snap table: an arbitrary `hours`
// must land on one of {1,6,24,168,720}, ties resolving to the smaller
// preset. Mutation target: `hours = h` (skip snapping) — this test alone
// pins the function's output for values on both sides of every tie.
func TestSnapRankingHours(t *testing.T) {
	cases := []struct{ in, want int }{
		{1, 1},
		{3, 1},
		{4, 6},
		{15, 6},  // tie between 6 (diff 9) and 24 (diff 9) -> smaller
		{96, 24}, // tie between 24 (diff 72) and 168 (diff 72) -> smaller
		{100, 168},
		{500, 720},
	}
	for _, c := range cases {
		if got := snapRankingHours(c.in); got != c.want {
			t.Errorf("snapRankingHours(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// newRankingsParamsContext builds a *gin.Context around a GET request to
// the given query string, for exercising parseRankingsParams directly
// without a full router.
func newRankingsParamsContext(query string) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x?"+query, nil)
	return c
}

// TestParseRankingsParams_SnapsToPresets: hours clamps to [1,720] then
// snaps to a preset; an unparsable/empty hours falls back to the 24h
// default rather than silently clamping to 1h; an unknown `by` is rejected
// (errMsg non-empty) rather than silently falling back to "model".
func TestParseRankingsParams_SnapsToPresets(t *testing.T) {
	cases := []struct {
		name      string
		query     string
		wantHours int
		wantErr   bool
	}{
		{"snaps up to nearest preset", "hours=100", 168, false},
		{"clamps below 1 then snaps", "hours=0", 1, false},
		{"clamps above 720 then snaps", "hours=9999", 720, false},
		{"omitted hours falls back to the 24h default", "", 24, false},
		{"empty hours value falls back to the 24h default", "hours=", 24, false},
		{"unparsable hours falls back to the 24h default", "hours=abc", 24, false},
		{"unknown by is rejected", "by=x", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newRankingsParamsContext(tc.query)
			_, hours, errMsg := parseRankingsParams(c)
			if tc.wantErr {
				if errMsg == "" {
					t.Fatalf("query=%q: want a non-empty errMsg, got none (hours=%d)", tc.query, hours)
				}
				return
			}
			if errMsg != "" {
				t.Fatalf("query=%q: unexpected errMsg %q", tc.query, errMsg)
			}
			if hours != tc.wantHours {
				t.Errorf("query=%q: hours = %d, want %d", tc.query, hours, tc.wantHours)
			}
		})
	}
}

// TestRankingsV2_UnknownByRejected: the root route returns 400 for an
// unrecognized `by` value instead of silently answering as if `by=model`
// had been requested.
func TestRankingsV2_UnknownByRejected(t *testing.T) {
	ctx := setupAnalyticsRouter(t)
	defer ctx.cleanup()

	w := doGET(ctx.router, "/api/v2/admin/analytics/rankings?by=foo")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("by=foo: want 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestRankingsV2_HoursSnapSharesCacheEntry: two different `hours` query
// values that snap to the same preset (168) must land on the same cache
// entry — identical cached_at across both calls, and a window spanning
// exactly 168 hours. Mutation target: `hours = h` (skip snapping) would
// give each of these two calls its own cache key and a differently sized
// window.
func TestRankingsV2_HoursSnapSharesCacheEntry(t *testing.T) {
	ctx := setupAnalyticsRouter(t)
	defer ctx.cleanup()

	seedPerfLog(t, ctx.db, "test-tenant", "alpha", entity.LogTypeConsume, 0, 10, 5, 100, time.Now().Unix()-60)

	w1 := doGET(ctx.router, "/api/v2/admin/analytics/rankings?by=model&hours=100")
	if w1.Code != http.StatusOK {
		t.Fatalf("hours=100: status %d body=%s", w1.Code, w1.Body.String())
	}
	body1 := parseJSON(t, w1)
	data1 := body1["data"].(map[string]interface{})

	w2 := doGET(ctx.router, "/api/v2/admin/analytics/rankings?by=model&hours=168")
	if w2.Code != http.StatusOK {
		t.Fatalf("hours=168: status %d body=%s", w2.Code, w2.Body.String())
	}
	body2 := parseJSON(t, w2)
	data2 := body2["data"].(map[string]interface{})

	if data1["cached_at"] != data2["cached_at"] {
		t.Errorf("cached_at differs across hours=100 and hours=168 (both snap to 168): %v vs %v", data1["cached_at"], data2["cached_at"])
	}
	// The reported `hours` (the snapped value) must be 168 for BOTH calls —
	// this is the assertion the mutation actually breaks: with snapping
	// skipped, the hours=100 call reports hours=100.
	if got := data1["hours"]; got != float64(168) {
		t.Errorf("hours=100 request: data.hours = %v, want 168 (snapped)", got)
	}
	if got := data2["hours"]; got != float64(168) {
		t.Errorf("hours=168 request: data.hours = %v, want 168", got)
	}
	// Same for the window span of the call that actually exercises snapping
	// (hours=100 -> 168h window, not a 100h one).
	window1 := data1["window"].(map[string]interface{})
	if got := window1["end"].(float64) - window1["start"].(float64); got != 168*3600 {
		t.Errorf("hours=100 request: window span = %v seconds, want %v (168h, snapped)", got, 168*3600)
	}
	window2 := data2["window"].(map[string]interface{})
	if got := window2["end"].(float64) - window2["start"].(float64); got != 168*3600 {
		t.Errorf("hours=168 request: window span = %v seconds, want %v (168h)", got, 168*3600)
	}
}

// TestResetRankingsCacheForTest_ClearsEntries: a direct unit test of the
// reset seam itself (rather than relying on cross-test -count=N ordering to
// observe pollution) — mutation target: a no-op resetRankingsCacheForTest
// body leaves the probe key in place.
func TestResetRankingsCacheForTest_ClearsEntries(t *testing.T) {
	rankingsCache.Store("probe-key", &rankingsCacheEntry{cachedAt: 1})
	resetRankingsCacheForTest()
	if _, ok := rankingsCache.Load("probe-key"); ok {
		t.Fatal("resetRankingsCacheForTest must clear every entry, found a leftover probe key")
	}
}

// TestParseRankingsParams_AcceptsGroup: `by=group` is a valid dimension
// alongside model/vendor (cycle-9 plan L7) — the logs table's `group`
// column already exists (internal/domain/entity/log.go); this was
// previously rejected with "by must be model or vendor".
func TestParseRankingsParams_AcceptsGroup(t *testing.T) {
	c := newRankingsParamsContext("by=group")
	by, _, errMsg := parseRankingsParams(c)
	if errMsg != "" {
		t.Fatalf("by=group: unexpected errMsg %q", errMsg)
	}
	if by != "group" {
		t.Errorf("by = %q, want group", by)
	}
}

// TestParseRankingsParams_AcceptsKeyUserProduct: the cycle-17 "who and
// what is spending" dimensions are accepted; near-misses are not.
func TestParseRankingsParams_AcceptsKeyUserProduct(t *testing.T) {
	for _, dim := range []string{"key", "user", "product"} {
		by, _, errMsg := parseRankingsParams(newRankingsParamsContext("by=" + dim))
		if errMsg != "" || by != dim {
			t.Errorf("by=%s: got by=%q errMsg=%q, want accepted", dim, by, errMsg)
		}
	}
	for _, bad := range []string{"token", "users", "Product"} {
		if _, _, errMsg := parseRankingsParams(newRankingsParamsContext("by=" + bad)); errMsg == "" {
			t.Errorf("by=%s: want rejected", bad)
		}
	}
}

// TestTenantRankingsV2_ByGroup_ScopedToOwnTenant mounts GetTenantRankingsV2
// directly on a bare gin.New() (setupTenantRankingsRouter) with
// tenant_context hand-seeded by mockAuth's c.Set — not the production
// UserAuth()+TenantSlugGuard() chain (router/api-v2-router.go:213-215),
// which is exercised instead by
// TestRankingsByGroupRealChain_ScopedToOwnTenant
// (internal/adapter/handler/router/l7_rankings_by_group_real_chain_test.go).
// What this test proves directly: with a tenant_context populated the same
// shape TenantSlugGuard produces, by=group never returns another tenant's
// group, mirroring TestTenantRankingsV2_AdminSeesOwnTenantOnly for the new
// dimension.
func TestTenantRankingsV2_ByGroup_ScopedToOwnTenant(t *testing.T) {
	ctx := setupTenantRankingsRouter(t)
	defer ctx.cleanup()

	now := time.Now().Unix()
	seedTenantRankingsGroupLog(t, ctx.db, ctx.tenantID, "premium", 100, 50, 30, now-60)
	seedTenantRankingsGroupLog(t, ctx.db, "other-tenant", "premium", 100_000, 50_000, 30_000, now-60)

	w := doGETWithHeaders(ctx.router, "/api/v2/acme/analytics/rankings?by=group&hours=1",
		map[string]string{"X-Test-Role": "admin"})
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.Code, w.Body.String())
	}
	body := parseJSON(t, w)
	data := body["data"].(map[string]interface{})
	if got := data["by"]; got != "group" {
		t.Errorf("data.by = %v, want group", got)
	}
	rows, ok := data["rows"].([]interface{})
	if !ok {
		t.Fatalf("missing rows array: %s", w.Body.String())
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row scoped to the caller's own tenant, got %d: %v", len(rows), rows)
	}
	row := rows[0].(map[string]interface{})
	if row["name"] != "premium" {
		t.Errorf("name = %v, want premium", row["name"])
	}
	if got := row["total_tokens"].(float64); got != 150 {
		t.Errorf("total_tokens = %v, want 150 (other tenant's 150000 leaked in)", got)
	}
}

// seedTenantRankingsGroupLog is seedTenantRankingsLog's sibling that also
// sets the `group` field, for the by=group dimension tests.
func seedTenantRankingsGroupLog(t *testing.T, db *gorm.DB, tenantID, group string, prompt, completion, quota int, createdAt int64) {
	t.Helper()
	l := &entity.Log{
		UserId:           1,
		TenantId:         tenantID,
		Type:             entity.LogTypeConsume,
		ModelName:        "irrelevant-model",
		Group:            group,
		Quota:            quota,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		CreatedAt:        createdAt,
	}
	if err := db.Create(l).Error; err != nil {
		t.Fatalf("seed tenant rankings group log: %v", err)
	}
}

// TestRankingsV2_UnknownByStillRejected_AfterGroupAdded: adding `group` as
// a third accepted dimension must not widen the validator into accepting
// anything — `by=whatever` still 400s.
func TestRankingsV2_UnknownByStillRejected_AfterGroupAdded(t *testing.T) {
	ctx := setupAnalyticsRouter(t)
	defer ctx.cleanup()

	w := doGET(ctx.router, "/api/v2/admin/analytics/rankings?by=whatever")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("by=whatever: want 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// seedAdminRankingsGroupLog is seedPerfLog's sibling that also sets the
// `group` field, for the admin-route by=group tests below.
func seedAdminRankingsGroupLog(t *testing.T, db *gorm.DB, tenant, group string, prompt, completion, quota int, createdAt int64) {
	t.Helper()
	l := &entity.Log{
		UserId:           1,
		TenantId:         tenant,
		Type:             entity.LogTypeConsume,
		ModelName:        "irrelevant-model",
		Group:            group,
		Quota:            quota,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		CreatedAt:        createdAt,
	}
	if err := db.Create(l).Error; err != nil {
		t.Fatalf("seed admin rankings group log: %v", err)
	}
}

// TestGetRankingsV2_ByGroup_MergesAcrossTenantsThenFiltersByOne exercises
// the ADMIN route's by=group dimension — the half of the lane's "accept
// by=group on BOTH routes" spec that was previously carried entirely by
// the shared parseRankingsParams with no request actually reaching
// GetRankingsV2 on the happy path. The admin path has behaviour the
// tenant-scoped path does not: an optional tenant_id query filter
// (v2_admin_analytics.go) and a tenantID=="" cross-tenant cache key
// (v2_analytics_rankings.go), so this also pins that the admin and
// tenant-scoped routes do not collide on that shared cache key.
func TestGetRankingsV2_ByGroup_MergesAcrossTenantsThenFiltersByOne(t *testing.T) {
	ctx := setupAnalyticsRouter(t)
	defer ctx.cleanup()

	now := time.Now().Unix()
	seedAdminRankingsGroupLog(t, ctx.db, "tenant-a", "premium", 100, 50, 30, now-60)
	seedAdminRankingsGroupLog(t, ctx.db, "tenant-b", "default", 200, 100, 60, now-60)

	// Unfiltered: both tenants' groups are visible, merged into one leaderboard.
	wAll := doGET(ctx.router, "/api/v2/admin/analytics/rankings?by=group&hours=1")
	if wAll.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", wAll.Code, wAll.Body.String())
	}
	bodyAll := parseJSON(t, wAll)
	dataAll, ok := bodyAll["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing data object: %s", wAll.Body.String())
	}
	if got := dataAll["by"]; got != "group" {
		t.Errorf("data.by = %v, want group", got)
	}
	rowsAll, ok := dataAll["rows"].([]interface{})
	if !ok || len(rowsAll) != 2 {
		t.Fatalf("want 2 merged rows (both tenants unfiltered), got %d: %s", len(rowsAll), wAll.Body.String())
	}
	namesAll := map[string]bool{}
	for _, r := range rowsAll {
		namesAll[r.(map[string]interface{})["name"].(string)] = true
	}
	if !namesAll["premium"] || !namesAll["default"] {
		t.Fatalf("unfiltered admin by=group must merge both tenants' groups, got %v", namesAll)
	}

	// Filtered: only tenant-a's group, via the admin-only tenant_id param.
	wA := doGET(ctx.router, "/api/v2/admin/analytics/rankings?by=group&hours=1&tenant_id=tenant-a")
	if wA.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", wA.Code, wA.Body.String())
	}
	bodyA := parseJSON(t, wA)
	dataA, ok := bodyA["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing data object: %s", wA.Body.String())
	}
	rowsA, ok := dataA["rows"].([]interface{})
	if !ok || len(rowsA) != 1 {
		t.Fatalf("want 1 row scoped to tenant_id=tenant-a, got %d: %s", len(rowsA), wA.Body.String())
	}
	rowA := rowsA[0].(map[string]interface{})
	if rowA["name"] != "premium" {
		t.Errorf("name = %v, want premium (tenant-b's default group must not leak in)", rowA["name"])
	}
}
