package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// Regression: RevokeProvisionedKey used to skip the (api_key, tenant) whitelist
// gate that CreateProvisionedKey / ListProvisionedKeys both enforce. A narrow
// Reseller key whitelisted for tenant-alpha could therefore revoke live tokens
// under tenant-beta simply by addressing beta's slug and guessing the key_id.

func fixRevokeRequest(ctx *provTestCtx, slug string, keyID int, apiKey string) *httptest.ResponseRecorder {
	url := fmt.Sprintf("/internal/v1/provisioning/tenants/%s/keys/%d", slug, keyID)
	req := httptest.NewRequest(http.MethodDelete, url, nil)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

func fixRevokeTokenExists(t *testing.T, ctx *provTestCtx, id int) bool {
	t.Helper()
	var count int64
	if err := ctx.db.Model(&repo.Token{}).Where("id = ?", id).Count(&count).Error; err != nil {
		t.Fatalf("count token %d: %v", id, err)
	}
	return count > 0
}

// A key whitelisted for tenant-alpha only must get 403 when revoking a
// tenant-beta token, and the token must survive.
func TestFixRevokeProvisionedKey_CrossTenantDenied(t *testing.T) {
	ctx := setupProvTestRouter(t)
	victim := seedProvisionedToken(t, ctx, ctx.tenantBeta.Id, "beta-live-key", ctx.otherResellerID)

	w := fixRevokeRequest(ctx, ctx.tenantBeta.Slug, victim.Id, ctx.narrowKeyRaw)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body: %s", w.Code, w.Body.String())
	}
	resp := parseList(t, w)
	if resp["error_code"] != "TENANT_NOT_AUTHORIZED" {
		t.Errorf("error_code = %v, want TENANT_NOT_AUTHORIZED", resp["error_code"])
	}
	if !fixRevokeTokenExists(t, ctx, victim.Id) {
		t.Error("tenant-beta token was revoked by a key not authorized for that tenant")
	}
}

// A narrow key with no whitelist row at all is denied too (fail-closed).
func TestFixRevokeProvisionedKey_NoWhitelistDenied(t *testing.T) {
	ctx := setupProvTestRouter(t)
	victim := seedProvisionedToken(t, ctx, ctx.tenantAlpha.Id, "alpha-live-key", ctx.resellerUserID)

	w := fixRevokeRequest(ctx, ctx.tenantAlpha.Slug, victim.Id, ctx.narrowKeyOther)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body: %s", w.Code, w.Body.String())
	}
	if !fixRevokeTokenExists(t, ctx, victim.Id) {
		t.Error("tenant-alpha token was revoked by a key with no whitelist row")
	}
}

// The guard must not break the legitimate paths: the whitelisted narrow key
// still revokes its own tenant's token, and a ScopeAll key revokes anywhere.
func TestFixRevokeProvisionedKey_AuthorizedStillWorks(t *testing.T) {
	ctx := setupProvTestRouter(t)

	own := seedProvisionedToken(t, ctx, ctx.tenantAlpha.Id, "alpha-own-key", ctx.resellerUserID)
	w := fixRevokeRequest(ctx, ctx.tenantAlpha.Slug, own.Id, ctx.narrowKeyRaw)
	if w.Code != http.StatusOK {
		t.Fatalf("whitelisted revoke status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	if fixRevokeTokenExists(t, ctx, own.Id) {
		t.Error("token still present after an authorized revoke")
	}

	cross := seedProvisionedToken(t, ctx, ctx.tenantBeta.Id, "beta-admin-key", ctx.resellerUserID)
	w = fixRevokeRequest(ctx, ctx.tenantBeta.Slug, cross.Id, ctx.scopeAllRaw)
	if w.Code != http.StatusOK {
		t.Fatalf("scope-all revoke status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	if fixRevokeTokenExists(t, ctx, cross.Id) {
		t.Error("token still present after a scope-all revoke")
	}
}
