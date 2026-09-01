package handler

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// setAnalyticsCtx returns a middleware that seeds the gin context keys these
// v1-style handlers read directly (id/role/tenant_id/username), mirroring
// what authHelper would set on a real request.
func setAnalyticsCtx(vals map[string]interface{}) gin.HandlerFunc {
	return func(c *gin.Context) {
		for k, v := range vals {
			c.Set(k, v)
		}
		c.Next()
	}
}

func doAnalyticsGet(r *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// --- SearchAllLogs ---

func TestCoverUpliftAnalytics_SearchAllLogs_RootCrossTenant(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	otherTenant := "other-tenant-xyz"
	if err := ctx.DB.Create(&repo.Log{TenantId: ctx.TenantID, UserId: ctx.NormalUser.Id, Type: repo.LogTypeConsume, Content: "findme-cross fizz", ModelName: "gpt-4o", CreatedAt: common.GetTimestamp()}).Error; err != nil {
		t.Fatalf("seed log1: %v", err)
	}
	if err := ctx.DB.Create(&repo.Log{TenantId: otherTenant, UserId: 999999, Type: repo.LogTypeConsume, Content: "findme-cross buzz", ModelName: "claude-3", CreatedAt: common.GetTimestamp()}).Error; err != nil {
		t.Fatalf("seed log2: %v", err)
	}

	r := gin.New()
	r.Use(setAnalyticsCtx(map[string]interface{}{
		"id":        ctx.RootUser.Id,
		"role":      common.RoleRootUser,
		"tenant_id": ctx.TenantID,
	}))
	r.GET("/api/log/search", SearchAllLogs)

	w := doAnalyticsGet(r, "/api/log/search?keyword=findme-cross")
	resp := AssertV2Success(t, w)
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, body: %s", w.Body.String())
	}
	items, ok := data["items"].([]interface{})
	if !ok {
		t.Fatalf("expected items array, got %T (body %s)", data["items"], w.Body.String())
	}
	if len(items) != 2 {
		t.Fatalf("root search should see both tenants' logs, got %d items: %v", len(items), items)
	}
	seenModels := map[string]bool{}
	for _, it := range items {
		m := it.(map[string]interface{})
		seenModels[m["model_name"].(string)] = true
	}
	if !seenModels["gpt-4o"] || !seenModels["claude-3"] {
		t.Fatalf("expected both tenants' models present, got %v", seenModels)
	}
}

func TestCoverUpliftAnalytics_SearchAllLogs_TenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	otherTenant := "other-tenant-abc"
	if err := ctx.DB.Create(&repo.Log{TenantId: ctx.TenantID, UserId: ctx.NormalUser.Id, Type: repo.LogTypeConsume, Content: "findme-scope apple", ModelName: "gpt-4o", CreatedAt: common.GetTimestamp()}).Error; err != nil {
		t.Fatalf("seed log1: %v", err)
	}
	if err := ctx.DB.Create(&repo.Log{TenantId: otherTenant, UserId: 999999, Type: repo.LogTypeConsume, Content: "findme-scope banana", ModelName: "claude-3", CreatedAt: common.GetTimestamp()}).Error; err != nil {
		t.Fatalf("seed log2 (other tenant): %v", err)
	}

	r := gin.New()
	r.Use(setAnalyticsCtx(map[string]interface{}{
		"id":        ctx.AdminUser.Id,
		"role":      common.RoleAdminUser, // tenant admin, NOT platform root
		"tenant_id": ctx.TenantID,
	}))
	r.GET("/api/log/search", SearchAllLogs)

	w := doAnalyticsGet(r, "/api/log/search?keyword=findme-scope")
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	items, ok := data["items"].([]interface{})
	if !ok {
		t.Fatalf("expected items array, got %T (body %s)", data["items"], w.Body.String())
	}
	if len(items) != 1 {
		t.Fatalf("tenant admin must not see other tenant's logs, got %d items: %v", len(items), items)
	}
	got := items[0].(map[string]interface{})
	if got["tenant_id"].(string) != ctx.TenantID {
		t.Fatalf("expected tenant_id %q, got %v", ctx.TenantID, got["tenant_id"])
	}
	if got["model_name"].(string) != "gpt-4o" {
		t.Fatalf("expected the in-tenant log (gpt-4o), got %v", got["model_name"])
	}
}

// --- SearchUserLogs ---

func TestCoverUpliftAnalytics_SearchUserLogs(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	target := ctx.NormalUser
	if err := ctx.DB.Create(&repo.Log{TenantId: ctx.TenantID, UserId: target.Id, Type: repo.LogTypeConsume, Content: "findme-user hello", ModelName: "gpt-4o-mini", CreatedAt: common.GetTimestamp()}).Error; err != nil {
		t.Fatalf("seed log1: %v", err)
	}
	if err := ctx.DB.Create(&repo.Log{TenantId: ctx.TenantID, UserId: target.Id, Type: repo.LogTypeConsume, Content: "findme-user world", ModelName: "gpt-4o", CreatedAt: common.GetTimestamp()}).Error; err != nil {
		t.Fatalf("seed log2: %v", err)
	}
	if err := ctx.DB.Create(&repo.Log{TenantId: ctx.TenantID, UserId: ctx.AdminUser.Id, Type: repo.LogTypeConsume, Content: "findme-user other-user-log", ModelName: "claude-3", CreatedAt: common.GetTimestamp()}).Error; err != nil {
		t.Fatalf("seed log3 (other user): %v", err)
	}

	r := gin.New()
	r.Use(setAnalyticsCtx(map[string]interface{}{
		"id":        target.Id,
		"role":      common.RoleCommonUser,
		"tenant_id": ctx.TenantID,
		"username":  target.Username,
	}))
	r.GET("/api/log/self/search", SearchUserLogs)

	w := doAnalyticsGet(r, "/api/log/self/search?keyword=findme-user")
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	items, ok := data["items"].([]interface{})
	if !ok {
		t.Fatalf("expected items array, got %T (body %s)", data["items"], w.Body.String())
	}
	if len(items) != 2 {
		t.Fatalf("expected only the calling user's own 2 logs, got %d: %v", len(items), items)
	}
	seenModels := map[string]bool{}
	for _, it := range items {
		m := it.(map[string]interface{})
		seenModels[m["model_name"].(string)] = true
	}
	if !seenModels["gpt-4o-mini"] || !seenModels["gpt-4o"] {
		t.Fatalf("expected target user's own models, got %v", seenModels)
	}
	if seenModels["claude-3"] {
		t.Fatalf("must not leak another user's log, got %v", seenModels)
	}
}

func TestCoverUpliftAnalytics_SearchUserLogs_Empty(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	r := gin.New()
	r.Use(setAnalyticsCtx(map[string]interface{}{
		"id":        ctx.NormalUser.Id,
		"role":      common.RoleCommonUser,
		"tenant_id": ctx.TenantID,
		"username":  ctx.NormalUser.Username,
	}))
	r.GET("/api/log/self/search", SearchUserLogs)

	w := doAnalyticsGet(r, "/api/log/self/search?keyword=nothing-matches-this")
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	items, ok := data["items"].([]interface{})
	if !ok {
		t.Fatalf("expected items array (possibly empty), got %T (body %s)", data["items"], w.Body.String())
	}
	if len(items) != 0 {
		t.Fatalf("expected empty result set, got %d items", len(items))
	}
}

// --- GetLogByKey ---

func TestCoverUpliftAnalytics_GetLogByKey_Found(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "key-lookup-token")
	if err := ctx.DB.Create(&repo.Log{TenantId: ctx.TenantID, UserId: ctx.NormalUser.Id, TokenId: tok.Id, Type: repo.LogTypeConsume, Content: "by-key hit", ModelName: "gpt-4o-mini", CreatedAt: common.GetTimestamp()}).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}

	r := gin.New()
	// GetLogByKey is ownership-checked against the authenticated principal
	// (TokenAuth sets id + tenant_id in production), so the caller identity
	// has to be present for the happy path to resolve anything.
	r.GET("/api/log/token", asCaller(ctx.NormalUser.Id, ctx.TenantID), GetLogByKey)

	w := doAnalyticsGet(r, "/api/log/token?key=sk-"+tok.Key)
	resp := AssertV2Success(t, w)
	items, ok := resp["data"].([]interface{})
	if !ok || len(items) != 1 {
		t.Fatalf("expected exactly 1 matching log, got %v (body %s)", resp["data"], w.Body.String())
	}
	got := items[0].(map[string]interface{})
	if got["model_name"].(string) != "gpt-4o-mini" {
		t.Fatalf("expected model_name gpt-4o-mini, got %v", got["model_name"])
	}
}

func TestCoverUpliftAnalytics_GetLogByKey_NoMatch(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	r := gin.New()
	r.GET("/api/log/token", asCaller(ctx.NormalUser.Id, ctx.TenantID), GetLogByKey)

	w := doAnalyticsGet(r, "/api/log/token?key=sk-does-not-exist-anywhere")
	resp := AssertV2Success(t, w)
	items, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("expected data array (empty), got %T (body %s)", resp["data"], w.Body.String())
	}
	if len(items) != 0 {
		t.Fatalf("expected empty result for unknown key, got %d items", len(items))
	}
}

// --- GetAllQuotaDates ---

func TestCoverUpliftAnalytics_GetAllQuotaDates_GroupedByModel(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	bucket := int64(1000)
	rows := []*repo.QuotaData{
		{UserID: 1, Username: "alice", ModelName: "gpt-4", CreatedAt: bucket, Count: 2, Quota: 100, TokenUsed: 50},
		{UserID: 2, Username: "bob", ModelName: "gpt-4", CreatedAt: bucket, Count: 3, Quota: 200, TokenUsed: 80},
		{UserID: 3, Username: "carol", ModelName: "claude-3", CreatedAt: 9_999_999, Count: 1, Quota: 10, TokenUsed: 5},
	}
	for _, row := range rows {
		if err := ctx.DB.Table("quota_data").Create(row).Error; err != nil {
			t.Fatalf("seed quota_data: %v", err)
		}
	}

	// These cases pin the grouping/range logic, which is the cross-tenant
	// (root) view; tenant scoping itself is covered in
	// usedata_tenant_scope_test.go.
	r := gin.New()
	r.Use(setAnalyticsCtx(map[string]interface{}{
		"id":        ctx.RootUser.Id,
		"role":      common.RoleRootUser,
		"tenant_id": ctx.TenantID,
	}))
	r.GET("/api/data", GetAllQuotaDates)

	w := doAnalyticsGet(r, "/api/data?start_timestamp=0&end_timestamp=2000")
	resp := AssertV2Success(t, w)
	items, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("expected data array, got %T (body %s)", resp["data"], w.Body.String())
	}
	if len(items) != 1 {
		t.Fatalf("expected exactly 1 grouped (model_name, created_at) bucket in range, got %d: %v", len(items), items)
	}
	got := items[0].(map[string]interface{})
	if got["model_name"].(string) != "gpt-4" {
		t.Fatalf("expected model_name gpt-4, got %v", got["model_name"])
	}
	if int(got["count"].(float64)) != 5 {
		t.Fatalf("expected summed count=5 (2+3), got %v", got["count"])
	}
	if int(got["quota"].(float64)) != 300 {
		t.Fatalf("expected summed quota=300 (100+200), got %v", got["quota"])
	}
	if int(got["token_used"].(float64)) != 130 {
		t.Fatalf("expected summed token_used=130 (50+80), got %v", got["token_used"])
	}
}

func TestCoverUpliftAnalytics_GetAllQuotaDates_ByUsername(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	rows := []*repo.QuotaData{
		{UserID: 1, Username: "alice", ModelName: "gpt-4", CreatedAt: 100, Count: 1, Quota: 11, TokenUsed: 1},
		{UserID: 1, Username: "alice", ModelName: "gpt-3.5", CreatedAt: 200, Count: 2, Quota: 22, TokenUsed: 2},
		{UserID: 1, Username: "alice", ModelName: "gpt-4", CreatedAt: 9_999_999, Count: 9, Quota: 999, TokenUsed: 9}, // out of range
		{UserID: 2, Username: "bob", ModelName: "gpt-4", CreatedAt: 100, Count: 5, Quota: 55, TokenUsed: 5},          // different username
	}
	for _, row := range rows {
		if err := ctx.DB.Table("quota_data").Create(row).Error; err != nil {
			t.Fatalf("seed quota_data: %v", err)
		}
	}

	r := gin.New()
	r.Use(setAnalyticsCtx(map[string]interface{}{
		"id":        ctx.RootUser.Id,
		"role":      common.RoleRootUser,
		"tenant_id": ctx.TenantID,
	}))
	r.GET("/api/data", GetAllQuotaDates)

	w := doAnalyticsGet(r, "/api/data?start_timestamp=0&end_timestamp=2000&username=alice")
	resp := AssertV2Success(t, w)
	items, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("expected data array, got %T (body %s)", resp["data"], w.Body.String())
	}
	if len(items) != 2 {
		t.Fatalf("expected alice's 2 in-range rows (ungrouped), got %d: %v", len(items), items)
	}
	for _, it := range items {
		m := it.(map[string]interface{})
		if m["username"].(string) != "alice" {
			t.Fatalf("username filter leaked another user's row: %v", m)
		}
	}
}

// --- GetUserQuotaDates ---

func TestCoverUpliftAnalytics_GetUserQuotaDates_Happy(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	target := ctx.NormalUser
	rows := []*repo.QuotaData{
		{UserID: target.Id, Username: target.Username, ModelName: "gpt-4", CreatedAt: 500, Count: 1, Quota: 42, TokenUsed: 7},
		{UserID: target.Id, Username: target.Username, ModelName: "gpt-4", CreatedAt: 9_999_999, Count: 1, Quota: 1, TokenUsed: 1},         // out of range
		{UserID: ctx.AdminUser.Id, Username: ctx.AdminUser.Username, ModelName: "gpt-4", CreatedAt: 500, Count: 1, Quota: 9, TokenUsed: 1}, // different user
	}
	for _, row := range rows {
		if err := ctx.DB.Table("quota_data").Create(row).Error; err != nil {
			t.Fatalf("seed quota_data: %v", err)
		}
	}

	r := gin.New()
	r.Use(setAnalyticsCtx(map[string]interface{}{"id": target.Id}))
	r.GET("/api/data/self", GetUserQuotaDates)

	w := doAnalyticsGet(r, "/api/data/self?start_timestamp=0&end_timestamp=1000")
	resp := AssertV2Success(t, w)
	items, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("expected data array, got %T (body %s)", resp["data"], w.Body.String())
	}
	if len(items) != 1 {
		t.Fatalf("expected exactly the caller's 1 in-range row, got %d: %v", len(items), items)
	}
	got := items[0].(map[string]interface{})
	if int(got["quota"].(float64)) != 42 {
		t.Fatalf("expected quota=42, got %v", got["quota"])
	}
}

func TestCoverUpliftAnalytics_GetUserQuotaDates_SpanTooLarge(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	r := gin.New()
	r.Use(setAnalyticsCtx(map[string]interface{}{"id": ctx.NormalUser.Id}))
	r.GET("/api/data/self", GetUserQuotaDates)

	// span = 3,000,000s > 2,592,000s (1 month) cap enforced by the handler.
	w := doAnalyticsGet(r, "/api/data/self?start_timestamp=0&end_timestamp=3000000")
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Fatalf("expected success=false for over-long span, got true (body %s)", w.Body.String())
	}
	if resp["message"].(string) != "时间跨度不能超过 1 个月" {
		t.Fatalf("unexpected message: %v", resp["message"])
	}
}

// --- GetPricing ---

func TestCoverUpliftAnalytics_GetPricing_WithUser(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	r := gin.New()
	r.Use(setAnalyticsCtx(map[string]interface{}{"id": ctx.RootUser.Id}))
	r.GET("/api/pricing", GetPricing)

	w := doAnalyticsGet(r, "/api/pricing")
	resp := AssertV2Success(t, w)
	if _, ok := resp["data"]; !ok {
		t.Fatalf("expected data field, body: %s", w.Body.String())
	}
	if _, ok := resp["vendors"].([]interface{}); !ok {
		if resp["vendors"] == nil {
			t.Fatalf("expected vendors field present, body: %s", w.Body.String())
		}
	}
	groupRatio, ok := resp["group_ratio"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected group_ratio object, got %T (body %s)", resp["group_ratio"], w.Body.String())
	}
	usableGroup, ok := resp["usable_group"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected usable_group object, got %T (body %s)", resp["usable_group"], w.Body.String())
	}
	// GetPricing deletes any group_ratio key absent from usable_group, so
	// group_ratio must never contain a key usable_group doesn't have.
	for g := range groupRatio {
		if _, present := usableGroup[g]; !present {
			t.Fatalf("group_ratio contains %q not present in usable_group %v", g, usableGroup)
		}
	}
	if _, ok := resp["supported_endpoint"].(map[string]interface{}); !ok {
		t.Fatalf("expected supported_endpoint object, got %T", resp["supported_endpoint"])
	}
}

func TestCoverUpliftAnalytics_GetPricing_Anonymous(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	_ = ctx

	// No "id" set in context at all: exercises the `exists == false` branch
	// (c.Get("id") returns ok=false), skipping the per-user group-ratio lookup.
	r := gin.New()
	r.GET("/api/pricing", GetPricing)

	w := doAnalyticsGet(r, "/api/pricing")
	resp := AssertV2Success(t, w)
	if _, ok := resp["group_ratio"].(map[string]interface{}); !ok {
		t.Fatalf("expected group_ratio object even without a caller identity, body: %s", w.Body.String())
	}
}

// --- ResetModelRatio ---

func TestCoverUpliftAnalytics_ResetModelRatio(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	defaultRatios := ratio_setting.GetDefaultModelRatioMap()
	if len(defaultRatios) == 0 {
		t.Fatalf("test precondition broken: default model ratio map is empty")
	}

	// Corrupt live state with a bogus model ratio the default map does not
	// contain, so a successful reset is observable.
	const bogusModel = "zzz-cover-uplift-bogus-model"
	if err := ratio_setting.UpdateModelRatioByJSONString(fmt.Sprintf(`{"%s":123.456}`, bogusModel)); err != nil {
		t.Fatalf("failed to corrupt model ratio state: %v", err)
	}
	if _, ok := ratio_setting.GetModelRatioCopy()[bogusModel]; !ok {
		t.Fatalf("test precondition broken: corruption didn't take")
	}

	r := gin.New()
	r.POST("/api/pricing/reset", ResetModelRatio)

	req := httptest.NewRequest("POST", "/api/pricing/reset", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	AssertV2Success(t, w)

	after := ratio_setting.GetModelRatioCopy()
	if _, ok := after[bogusModel]; ok {
		t.Fatalf("expected bogus model ratio to be gone after reset, still present: %v", after[bogusModel])
	}
	for model, want := range defaultRatios {
		got, ok := after[model]
		if !ok || got != want {
			t.Fatalf("expected reset ratio for %q = %v, got %v (present=%v)", model, want, got, ok)
		}
	}

	// Verify the DB-persisted option actually changed, not just the in-memory map.
	var opt repo.Option
	if err := ctx.DB.Where("key = ?", "ModelRatio").First(&opt).Error; err != nil {
		t.Fatalf("expected ModelRatio option row to exist: %v", err)
	}
	if opt.Value != ratio_setting.DefaultModelRatio2JSONString() {
		t.Fatalf("persisted ModelRatio option does not match default JSON")
	}
}
