package handler

// cov_handler-deep-b_token_test.go — business-acceptance coverage for the
// remaining gaps in v2_token.go: middleware.GetTenantContext defence-in-depth
// branches (route misconfiguration), pagination-bound clamping, the
// per-field update branches on UpdateTokenV2, the DeleteTokenV2 cross-tenant
// IDOR guard (untested before this file — DeleteTokenV2 has the same
// TenantId check as UpdateTokenV2/RotateTokenV2 but no regression test
// existed), and genuine DB-write-failure paths (Insert/Update) using a
// SQLite BEFORE-trigger / dropped-table fault injection — never a fabricated
// nil/zero-value input.
//
// Reuses SetupV2TestRouter / V2RequestAsUser / SeedV2Token from
// v2_testutil_test.go per "先读既有测试...复用脚手架".

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// bytesReaderDeepB is a tiny io.Reader adapter shared by this file's raw
// httptest.NewRequest calls (used where malformed-JSON bodies need to bypass
// V2Request's json.Marshal).
func bytesReaderDeepB(b []byte) *bytes.Reader {
	return bytes.NewReader(b)
}

// handlerDeepBBareTokenRouter wires the five v2 token routes with NO auth
// middleware at all — simulating a route-registration bug where the tenant
// context never gets set on the gin.Context. Every handler must fail closed
// with 401 "Tenant context not found" via middleware.GetTenantContext,
// exactly like RequireRole does. This is the only way to reach that
// defence-in-depth branch: SetupV2TestRouter's mockAuth always populates
// tenant_context.
func handlerDeepBBareTokenRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/v2/:tenant_slug")
	g.GET("/tokens", ListTokensV2)
	g.POST("/tokens", CreateTokenV2)
	g.PUT("/tokens/:id", UpdateTokenV2)
	g.DELETE("/tokens/:id", DeleteTokenV2)
	g.POST("/tokens/batch-delete", DeleteTokensV2)
	return r
}

func handlerDeepBBareReq(r *gin.Engine, method, path string, body []byte) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytesReaderDeepB(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestListTokensV2_NoTenantContext_Unauthorized(t *testing.T) {
	r := handlerDeepBBareTokenRouter()
	w := handlerDeepBBareReq(r, http.MethodGet, "/api/v2/acme/tokens", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	if resp["message"] != "Tenant context not found" {
		t.Errorf("message = %v, want 'Tenant context not found'", resp["message"])
	}
}

func TestCreateTokenV2_NoTenantContext_Unauthorized(t *testing.T) {
	r := handlerDeepBBareTokenRouter()
	w := handlerDeepBBareReq(r, http.MethodPost, "/api/v2/acme/tokens", []byte(`{"name":"x"}`))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateTokenV2_NoTenantContext_Unauthorized(t *testing.T) {
	r := handlerDeepBBareTokenRouter()
	w := handlerDeepBBareReq(r, http.MethodPut, "/api/v2/acme/tokens/1", []byte(`{"name":"x"}`))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteTokenV2_NoTenantContext_Unauthorized(t *testing.T) {
	r := handlerDeepBBareTokenRouter()
	w := handlerDeepBBareReq(r, http.MethodDelete, "/api/v2/acme/tokens/1", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteTokensV2_NoTenantContext_Unauthorized(t *testing.T) {
	r := handlerDeepBBareTokenRouter()
	w := handlerDeepBBareReq(r, http.MethodPost, "/api/v2/acme/tokens/batch-delete", []byte(`{"ids":[1]}`))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
}

// ─── ListTokensV2: pagination clamp bounds ─────────────────────────────────

func TestListTokensV2_PaginationBoundsClamped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	SeedV2Token(t, ctx, ctx.NormalUser.Id, "clamp-token")

	cases := []struct {
		name         string
		query        string
		wantPage     float64
		wantPageSize float64
	}{
		{"page_zero_clamps_to_1", "p=0&size=20", 1, 20},
		{"page_negative_clamps_to_1", "p=-5&size=20", 1, 20},
		{"size_zero_clamps_to_20", "p=1&size=0", 1, 20},
		{"size_over_100_clamps_to_20", "p=1&size=500", 1, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/tokens?"+tc.query, nil, nil)
			resp := AssertV2Success(t, w)
			data := resp["data"].(map[string]interface{})
			if data["page"].(float64) != tc.wantPage {
				t.Errorf("page = %v, want %v", data["page"], tc.wantPage)
			}
			if data["page_size"].(float64) != tc.wantPageSize {
				t.Errorf("page_size = %v, want %v", data["page_size"], tc.wantPageSize)
			}
		})
	}
}

// ─── CreateTokenV2: AllowIps round-trip + DB-write failure ────────────────

func TestCreateTokenV2_AllowIpsPersisted(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":            "Allow-IP Token",
		"unlimited_quota": true,
		"allow_ips":       "10.0.0.1,10.0.0.2",
	}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)
	AssertV2Status(t, w, http.StatusCreated)
	AssertV2Success(t, w)

	var persisted repo.Token
	if err := ctx.DB.Where("name = ?", "Allow-IP Token").First(&persisted).Error; err != nil {
		t.Fatalf("token not persisted: %v", err)
	}
	if persisted.AllowIps == nil || *persisted.AllowIps != "10.0.0.1,10.0.0.2" {
		t.Errorf("allow_ips = %v, want '10.0.0.1,10.0.0.2'", persisted.AllowIps)
	}
}

// TestCreateTokenV2_InsertDBError forces a genuine DB write failure (the
// tokens table is dropped from under a live connection — the same failure
// shape as a mid-deploy schema drift or a replica falling behind) and
// confirms the handler surfaces 500 rather than panicking or silently
// succeeding.
func TestCreateTokenV2_InsertDBError(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if err := ctx.DB.Migrator().DropTable(&repo.Token{}); err != nil {
		t.Fatalf("drop tokens table: %v", err)
	}

	body := map[string]interface{}{
		"name":            "Will Fail",
		"unlimited_quota": true,
	}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	if resp["message"] != "Failed to create token" {
		t.Errorf("message = %v, want 'Failed to create token'", resp["message"])
	}
}

// ─── UpdateTokenV2: malformed body, per-field branches, DB write failure ──

func TestUpdateTokenV2_MalformedJSON(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "malformed-target")

	path := "/api/v2/test-tenant/tokens/" + strconv.Itoa(token.Id)
	req := httptest.NewRequest(http.MethodPut, path, bytesReaderDeepB([]byte(`{"name": not-json}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Tenant-ID", ctx.TenantID)
	req.Header.Set("X-Test-User-ID", strconv.Itoa(ctx.NormalUser.Id))
	w := httptest.NewRecorder()
	ctx.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateTokenV2_ExpiredTimeSetSuccessfully(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "expiry-target")

	future := common.GetTimestamp() + 86400
	body := map[string]interface{}{"expired_time": future}
	path := "/api/v2/test-tenant/tokens/" + strconv.Itoa(token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, body, nil)
	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)

	var reloaded repo.Token
	if err := ctx.DB.First(&reloaded, token.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if reloaded.ExpiredTime != future {
		t.Errorf("expired_time = %d, want %d", reloaded.ExpiredTime, future)
	}
}

// TestUpdateTokenV2_QuotaFieldsTogether covers both the UnlimitedQuota
// pointer-set branch and the positive-RemainQuota branch in one request:
// flipping unlimited off and setting a positive remaining quota in the same
// call is the real-world "convert an unlimited token to a metered one" flow.
func TestUpdateTokenV2_QuotaFieldsTogether(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "quota-target") // seeded unlimited=true

	body := map[string]interface{}{
		"unlimited_quota": false,
		"remain_quota":    7500,
	}
	path := "/api/v2/test-tenant/tokens/" + strconv.Itoa(token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, body, nil)
	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)

	var reloaded repo.Token
	if err := ctx.DB.First(&reloaded, token.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if reloaded.UnlimitedQuota {
		t.Errorf("unlimited_quota still true after explicit false update")
	}
	if reloaded.RemainQuota != 7500 {
		t.Errorf("remain_quota = %d, want 7500", reloaded.RemainQuota)
	}
}

// TestUpdateTokenV2_NegativeRemainQuotaOnMeteredToken covers the negative
// quota rejection branch specifically on the update path (the create-path
// equivalent is TestCreateTokenV2_NegativeQuota).
func TestUpdateTokenV2_NegativeRemainQuotaOnMeteredToken(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "negative-quota-target")

	body := map[string]interface{}{
		"unlimited_quota": false,
		"remain_quota":    -500,
	}
	path := "/api/v2/test-tenant/tokens/" + strconv.Itoa(token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, body, nil)
	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if resp["message"] != "Quota value cannot be negative" {
		t.Errorf("message = %v, want 'Quota value cannot be negative'", resp["message"])
	}

	// The token must remain unmutated by the rejected write.
	var reloaded repo.Token
	if err := ctx.DB.First(&reloaded, token.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if reloaded.RemainQuota < 0 {
		t.Errorf("remain_quota persisted as negative: %d", reloaded.RemainQuota)
	}
}

func TestUpdateTokenV2_ModelLimitsFields(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "model-limits-target")

	enabled := true
	body := map[string]interface{}{
		"model_limits_enabled": enabled,
		"model_limits":         `{"gpt-4":100}`,
	}
	path := "/api/v2/test-tenant/tokens/" + strconv.Itoa(token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, body, nil)
	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)

	var reloaded repo.Token
	if err := ctx.DB.First(&reloaded, token.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if !reloaded.ModelLimitsEnabled {
		t.Errorf("model_limits_enabled = false, want true")
	}
	if reloaded.ModelLimits != `{"gpt-4":100}` {
		t.Errorf("model_limits = %q, want '{\"gpt-4\":100}'", reloaded.ModelLimits)
	}
}

func TestUpdateTokenV2_AllowIpsAndGroupFields(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "allowips-group-target")

	body := map[string]interface{}{
		"allow_ips": "203.0.113.5",
		"group":     "enterprise",
	}
	path := "/api/v2/test-tenant/tokens/" + strconv.Itoa(token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, body, nil)
	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)

	var reloaded repo.Token
	if err := ctx.DB.First(&reloaded, token.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if reloaded.AllowIps == nil || *reloaded.AllowIps != "203.0.113.5" {
		t.Errorf("allow_ips = %v, want '203.0.113.5'", reloaded.AllowIps)
	}
	if reloaded.Group != "enterprise" {
		t.Errorf("group = %q, want 'enterprise'", reloaded.Group)
	}
}

// TestUpdateTokenV2_DisableStatus covers the Status-set branch on the
// non-enable path (disabling never calls app.CanEnableToken).
func TestUpdateTokenV2_DisableStatus(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "disable-target")

	body := map[string]interface{}{"status": common.TokenStatusDisabled}
	path := "/api/v2/test-tenant/tokens/" + strconv.Itoa(token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, body, nil)
	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)

	var reloaded repo.Token
	if err := ctx.DB.First(&reloaded, token.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if reloaded.Status != common.TokenStatusDisabled {
		t.Errorf("status = %d, want disabled(%d)", reloaded.Status, common.TokenStatusDisabled)
	}
}

func TestUpdateTokenV2_InvalidScopeRejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "bad-scope-target")

	body := map[string]interface{}{"scopes": []string{"chat", "definitely-not-a-scope"}}
	path := "/api/v2/test-tenant/tokens/" + strconv.Itoa(token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, body, nil)
	AssertV2Status(t, w, http.StatusBadRequest)

	// Scopes must remain untouched by the rejected update.
	var reloaded repo.Token
	if err := ctx.DB.First(&reloaded, token.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if reloaded.Scopes != "" {
		t.Errorf("scopes mutated despite validation failure: %q", reloaded.Scopes)
	}
}

// TestUpdateTokenV2_DBWriteFailure installs a BEFORE UPDATE trigger that
// rejects every write to the tokens table (models a read-replica-only /
// disk-full failure: reads for the ownership check still succeed, the write
// does not) and confirms the handler surfaces 500 without corrupting state.
func TestUpdateTokenV2_DBWriteFailure(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "write-fail-target")

	if err := ctx.DB.Exec(`CREATE TRIGGER handler_deep_b_block_token_update
		BEFORE UPDATE ON tokens
		BEGIN SELECT RAISE(ABORT, 'simulated write failure'); END;`).Error; err != nil {
		t.Fatalf("install trigger: %v", err)
	}

	body := map[string]interface{}{"name": "should-not-persist"}
	path := "/api/v2/test-tenant/tokens/" + strconv.Itoa(token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, body, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}

	// Drop the trigger before reading back so the assertion query itself
	// isn't blocked (SELECT is unaffected, but be explicit/defensive).
	ctx.DB.Exec(`DROP TRIGGER handler_deep_b_block_token_update`)
	var reloaded repo.Token
	if err := ctx.DB.First(&reloaded, token.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if reloaded.Name == "should-not-persist" {
		t.Errorf("name was persisted despite the write failure")
	}
}

// ─── DeleteTokenV2: cross-tenant IDOR guard (previously untested) ─────────

// TestDeleteTokenV2_TenantMismatch proves DeleteTokenV2 enforces the same
// TenantId check as UpdateTokenV2/RotateTokenV2. Before this test, this
// specific handler's Forbidden branch had zero coverage even though the
// sibling endpoints were already regression-tested.
func TestDeleteTokenV2_TenantMismatch(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "cross-tenant-delete-target")
	token.TenantId = "other-tenant-999"
	if err := ctx.DB.Save(token).Error; err != nil {
		t.Fatalf("reassign tenant: %v", err)
	}

	path := "/api/v2/test-tenant/tokens/" + strconv.Itoa(token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodDelete, path, nil, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	if resp["message"] != "Access denied" {
		t.Errorf("message = %v, want 'Access denied'", resp["message"])
	}

	// The token must survive the denied cross-tenant delete attempt.
	var count int64
	ctx.DB.Model(&repo.Token{}).Where("id = ?", token.Id).Count(&count)
	if count != 1 {
		t.Errorf("token was deleted despite a 403 tenant-mismatch response: count=%d", count)
	}
}

// ─── DeleteTokensV2: malformed body ────────────────────────────────────────

func TestDeleteTokensV2_MalformedJSON(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/v2/test-tenant/tokens/batch-delete", bytesReaderDeepB([]byte(`{"ids": not-an-array}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Tenant-ID", ctx.TenantID)
	req.Header.Set("X-Test-User-ID", strconv.Itoa(ctx.NormalUser.Id))
	w := httptest.NewRecorder()
	ctx.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}
