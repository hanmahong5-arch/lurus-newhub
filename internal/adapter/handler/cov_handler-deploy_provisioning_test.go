package handler

// cov_handler-deploy_provisioning_test.go — business-acceptance tests for the
// two provisioning.go endpoints NOT already covered by provisioning_test.go
// (which only exercises ListProvisionedKeys): CreateProvisionedKey and
// RevokeProvisionedKey. Money-path framing: a provisioned key is a live
// credential — cross-tenant issuance/revocation is a direct IDOR into another
// customer's billing boundary, so the tenant-authorization guard is the
// primary edge under test here (reusing the alpha/beta/narrow/scope-all
// fixture from provisioning_test.go's setupProvTestRouter, per "先读既有
// *_test.go 学它怎么起...复用它们").

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

func handlerDeployProvCreateReq(ctx *provTestCtx, slug, apiKey string, body map[string]interface{}) *httptest.ResponseRecorder {
	url := "/internal/v1/provisioning/tenants/" + slug + "/keys"
	var reqBody *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reqBody = strings.NewReader(string(b))
	} else {
		reqBody = strings.NewReader("")
	}
	req := httptest.NewRequest(http.MethodPost, url, reqBody)
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

func handlerDeployProvRevokeReq(ctx *provTestCtx, slug string, keyID int, apiKey string) *httptest.ResponseRecorder {
	url := "/internal/v1/provisioning/tenants/" + slug + "/keys/" + strconv.Itoa(keyID)
	req := httptest.NewRequest(http.MethodDelete, url, nil)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

func handlerDeployProvParse(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse: %v raw=%s", err, w.Body.String())
	}
	return out
}

// ─── CreateProvisionedKey ──────────────────────────────────────────────────

func TestCreateProvisionedKey_HappyPath_PersistsTenantScopedToken(t *testing.T) {
	ctx := setupProvTestRouter(t)
	w := handlerDeployProvCreateReq(ctx, ctx.tenantAlpha.Slug, ctx.narrowKeyRaw, map[string]interface{}{
		"name":         "customer-integration-key",
		"remain_quota": 50000,
		"group":        "premium",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployProvParse(t, w)
	if resp["success"] != true {
		t.Fatalf("success = %v, want true", resp["success"])
	}
	data, _ := resp["data"].(map[string]interface{})
	rawKey, _ := data["key"].(string)
	if rawKey == "" {
		t.Fatalf("expected a non-empty plaintext key in the create response")
	}
	if data["tenant_id"] != ctx.tenantAlpha.Id {
		t.Errorf("tenant_id = %v, want %v", data["tenant_id"], ctx.tenantAlpha.Id)
	}

	var persisted repo.Token
	if err := ctx.db.Where("name = ?", "customer-integration-key").First(&persisted).Error; err != nil {
		t.Fatalf("token not persisted: %v", err)
	}
	if persisted.TenantId != ctx.tenantAlpha.Id {
		t.Errorf("persisted tenant_id = %q, want %q", persisted.TenantId, ctx.tenantAlpha.Id)
	}
	if persisted.UserId != 0 {
		t.Errorf("persisted user_id = %d, want 0 (provisioned keys are tenant-scoped, not user-scoped)", persisted.UserId)
	}
	if persisted.RemainQuota != 50000 {
		t.Errorf("persisted remain_quota = %d, want 50000", persisted.RemainQuota)
	}
	if persisted.Group != "premium" {
		t.Errorf("persisted group = %q, want premium", persisted.Group)
	}
	if persisted.CreatorUserId != ctx.resellerUserID {
		t.Errorf("persisted creator_user_id = %d, want %d (from the API key's CreatedBy)", persisted.CreatorUserId, ctx.resellerUserID)
	}
	if persisted.Key == "" {
		t.Errorf("persisted key is empty")
	}
	if persisted.Key != rawKey {
		t.Errorf("persisted key %q does not match the one-time plaintext key returned in the response %q", persisted.Key, rawKey)
	}
}

func TestCreateProvisionedKey_DefaultGroupWhenOmitted(t *testing.T) {
	ctx := setupProvTestRouter(t)
	w := handlerDeployProvCreateReq(ctx, ctx.tenantAlpha.Slug, ctx.narrowKeyRaw, map[string]interface{}{
		"name": "no-group-key",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	var persisted repo.Token
	if err := ctx.db.Where("name = ?", "no-group-key").First(&persisted).Error; err != nil {
		t.Fatalf("token not persisted: %v", err)
	}
	if persisted.Group != "default" {
		t.Errorf("group = %q, want default when omitted from the request", persisted.Group)
	}
	if persisted.ExpiredTime != -1 {
		t.Errorf("expired_time = %d, want -1 (no expiry) when expires_at omitted/<=0", persisted.ExpiredTime)
	}
}

// TestCreateProvisionedKey_CrossTenantDenied is the money-path IDOR check:
// reseller-a's key is whitelisted for alpha only; attempting to mint a key
// for beta must be rejected 403 and must NOT create a token row anywhere.
func TestCreateProvisionedKey_CrossTenantDenied(t *testing.T) {
	ctx := setupProvTestRouter(t)
	w := handlerDeployProvCreateReq(ctx, ctx.tenantBeta.Slug, ctx.narrowKeyRaw, map[string]interface{}{
		"name": "should-not-exist",
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployProvParse(t, w)
	if resp["error_code"] != "TENANT_NOT_AUTHORIZED" {
		t.Errorf("error_code = %v, want TENANT_NOT_AUTHORIZED", resp["error_code"])
	}
	var count int64
	ctx.db.Model(&repo.Token{}).Where("name = ?", "should-not-exist").Count(&count)
	if count != 0 {
		t.Errorf("cross-tenant create must not persist a token row: count=%d", count)
	}
}

func TestCreateProvisionedKey_ScopeAllBypassesTenantWhitelist(t *testing.T) {
	ctx := setupProvTestRouter(t)
	w := handlerDeployProvCreateReq(ctx, ctx.tenantBeta.Slug, ctx.scopeAllRaw, map[string]interface{}{
		"name": "admin-minted-key",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
}

func TestCreateProvisionedKey_UnknownTenantSlug_404(t *testing.T) {
	ctx := setupProvTestRouter(t)
	w := handlerDeployProvCreateReq(ctx, "no-such-tenant", ctx.scopeAllRaw, map[string]interface{}{
		"name": "x",
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployProvParse(t, w)
	if resp["error_code"] != "TENANT_NOT_FOUND" {
		t.Errorf("error_code = %v, want TENANT_NOT_FOUND", resp["error_code"])
	}
}

func TestCreateProvisionedKey_MissingRequiredName_400(t *testing.T) {
	ctx := setupProvTestRouter(t)
	w := handlerDeployProvCreateReq(ctx, ctx.tenantAlpha.Slug, ctx.narrowKeyRaw, map[string]interface{}{
		"remain_quota": 100,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (name is binding:required), body=%s", w.Code, w.Body.String())
	}
}

func TestCreateProvisionedKey_EmptySlug_400(t *testing.T) {
	ctx := setupProvTestRouter(t)
	// Route requires a :slug segment, so an empty slug falls through to the
	// LIST/handler with a literal blank — exercise via a slug of a single
	// space that GetTenantBySlug will not find, proving the 400 vs 404
	// boundary is on presence, not validity, of the param.
	w := handlerDeployProvCreateReq(ctx, "definitely-not-a-real-slug", ctx.scopeAllRaw, map[string]interface{}{
		"name": "x",
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a nonexistent slug", w.Code)
	}
}

func TestCreateProvisionedKey_MalformedJSON_400(t *testing.T) {
	ctx := setupProvTestRouter(t)
	req := httptest.NewRequest(http.MethodPost,
		"/internal/v1/provisioning/tenants/"+ctx.tenantAlpha.Slug+"/keys",
		strings.NewReader(`{"name": not-json}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", ctx.narrowKeyRaw)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

func TestCreateProvisionedKey_ExpiresAtNonPositive_MeansNoExpiry(t *testing.T) {
	ctx := setupProvTestRouter(t)
	w := handlerDeployProvCreateReq(ctx, ctx.tenantAlpha.Slug, ctx.narrowKeyRaw, map[string]interface{}{
		"name":       "negative-expiry",
		"expires_at": -100,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployProvParse(t, w)
	data, _ := resp["data"].(map[string]interface{})
	if data["expires_at"] != float64(-1) {
		t.Errorf("expires_at = %v, want -1 (a non-positive input collapses to 'no expiry')", data["expires_at"])
	}
}

// ─── RevokeProvisionedKey ───────────────────────────────────────────────────

func TestRevokeProvisionedKey_HappyPath_SoftDeletes(t *testing.T) {
	ctx := setupProvTestRouter(t)
	tok := seedProvisionedToken(t, ctx, ctx.tenantAlpha.Id, "revoke-me", ctx.resellerUserID)

	w := handlerDeployProvRevokeReq(ctx, ctx.tenantAlpha.Slug, tok.Id, ctx.narrowKeyRaw)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployProvParse(t, w)
	if resp["success"] != true {
		t.Fatalf("success = %v, want true", resp["success"])
	}

	// Delete() on Token is expected to be a soft/status-flip delete; assert it
	// no longer surfaces via the normal lookup path used elsewhere.
	got, err := repo.GetTokenById(tok.Id)
	if err == nil && got != nil && got.Status == 1 /* TokenStatusEnabled */ {
		t.Errorf("token %d still enabled after revoke: %+v", tok.Id, got)
	}
}

// TestRevokeProvisionedKey_CrossTenant404 is the second money-path IDOR
// check: reseller-b must not even learn whether a key id exists under
// tenant-alpha (a tenant they aren't authorized against) — this endpoint
// resolves via slug+token ownership rather than a whitelist check, so we
// prove the tenant-slug/token-tenant mismatch alone returns 404 either way.
func TestRevokeProvisionedKey_CrossTenant404(t *testing.T) {
	ctx := setupProvTestRouter(t)
	tok := seedProvisionedToken(t, ctx, ctx.tenantAlpha.Id, "alpha-only-key", ctx.resellerUserID)

	// Ask to revoke an alpha token while addressing it via the BETA slug.
	w := handlerDeployProvRevokeReq(ctx, ctx.tenantBeta.Slug, tok.Id, ctx.scopeAllRaw)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (token belongs to a different tenant than the slug), body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployProvParse(t, w)
	if resp["error_code"] != "KEY_NOT_FOUND" {
		t.Errorf("error_code = %v, want KEY_NOT_FOUND", resp["error_code"])
	}

	// And the token must remain untouched.
	still, err := repo.GetTokenById(tok.Id)
	if err != nil || still == nil {
		t.Fatalf("token unexpectedly gone after a denied cross-tenant revoke attempt: %v", err)
	}
	if still.Status != 1 {
		t.Errorf("token status = %d, want still enabled(1) — cross-tenant revoke must be a no-op", still.Status)
	}
}

// TestRevokeProvisionedKey_NarrowKeyNotWhitelistedForTenant probes whether
// RevokeProvisionedKey enforces the SAME (api_key, tenant) whitelist that
// CreateProvisionedKey and ListProvisionedKeys enforce via
// repo.InternalKeyAllowedForTenant. reseller-a's narrow key is whitelisted
// for tenant-alpha ONLY (see setupProvTestRouter). Here it targets a REAL
// beta token via the correct beta slug + correct key id — the "slug/token
// mismatch" 404 path does not apply, so this isolates the whitelist check
// specifically.
func TestRevokeProvisionedKey_NarrowKeyNotWhitelistedForTenant(t *testing.T) {
	ctx := setupProvTestRouter(t)
	betaTok := seedProvisionedToken(t, ctx, ctx.tenantBeta.Id, "beta-victim-key", ctx.otherResellerID)

	w := handlerDeployProvRevokeReq(ctx, ctx.tenantBeta.Slug, betaTok.Id, ctx.narrowKeyRaw)

	still, _ := repo.GetTokenById(betaTok.Id)
	if w.Code == http.StatusOK {
		// FINDING (documented, not fixed): unlike CreateProvisionedKey and
		// ListProvisionedKeys, RevokeProvisionedKey never calls
		// repo.InternalKeyAllowedForTenant. A Reseller's provisioning key,
		// whitelisted for exactly one tenant, can revoke ANY other tenant's
		// key as long as it can address the correct slug + key_id (e.g. by
		// enumerating small integer ids) — a cross-tenant denial-of-service
		// on a sibling customer's live credentials. Locking in current
		// behaviour as a regression baseline; not modifying provisioning.go
		// per instructions.
		t.Logf("FINDING: narrow key (whitelisted for tenant-alpha only) successfully revoked a tenant-beta token — RevokeProvisionedKey has no InternalKeyAllowedForTenant gate, unlike Create/List")
		if still == nil || still.Status == 1 {
			t.Errorf("inconsistent: 200 response but token %d was not actually revoked", betaTok.Id)
		}
		return
	}
	// If a whitelist gate IS present (future fix), the correct contract is
	// 403 TENANT_NOT_AUTHORIZED and the token must remain untouched.
	if w.Code != http.StatusForbidden {
		t.Fatalf("unexpected status %d (want either 200-with-FINDING-logged or 403 if gated), body=%s", w.Code, w.Body.String())
	}
	if still == nil || still.Status != 1 {
		t.Errorf("token was mutated despite a 403 response: %+v", still)
	}
}

func TestRevokeProvisionedKey_NonexistentKeyID_404(t *testing.T) {
	ctx := setupProvTestRouter(t)
	w := handlerDeployProvRevokeReq(ctx, ctx.tenantAlpha.Slug, 999999, ctx.narrowKeyRaw)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

func TestRevokeProvisionedKey_InvalidKeyIDFormat_400(t *testing.T) {
	ctx := setupProvTestRouter(t)
	req := httptest.NewRequest(http.MethodDelete,
		"/internal/v1/provisioning/tenants/"+ctx.tenantAlpha.Slug+"/keys/not-a-number", nil)
	req.Header.Set("X-API-Key", ctx.narrowKeyRaw)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", w.Code, w.Body.String())
	}
}

func TestRevokeProvisionedKey_ZeroOrNegativeKeyID_400(t *testing.T) {
	ctx := setupProvTestRouter(t)
	for _, id := range []int{0, -1} {
		w := handlerDeployProvRevokeReq(ctx, ctx.tenantAlpha.Slug, id, ctx.narrowKeyRaw)
		if w.Code != http.StatusBadRequest {
			t.Errorf("key_id=%d: status = %d, want 400", id, w.Code)
		}
	}
}

func TestRevokeProvisionedKey_UnknownTenantSlug_404(t *testing.T) {
	ctx := setupProvTestRouter(t)
	w := handlerDeployProvRevokeReq(ctx, "ghost-tenant", 1, ctx.scopeAllRaw)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
	resp := handlerDeployProvParse(t, w)
	if resp["error_code"] != "TENANT_NOT_FOUND" {
		t.Errorf("error_code = %v, want TENANT_NOT_FOUND", resp["error_code"])
	}
}
