package handler

import (
	"net/http"
	"testing"
)

// Lane E / item E2 regression: ListProvisionedKeys scopes its query to the
// caller's creator identity (repo.GetProvisionedTokensByTenant filters on
// creator_user_id = apiKey.CreatedBy), but RevokeProvisionedKey previously
// checked only tenant + whitelist — never CreatorUserId. That let a narrow
// Reseller key whitelisted for a tenant revoke a *different* Reseller's live
// key under the same tenant, even though the same key could never have
// listed it. Revoke must mirror List's creator scoping (ScopeAll keys keep
// the same cross-creator bypass they already get on the tenant whitelist).

// Same tenant, same whitelist, but the token was issued by a DIFFERENT
// Reseller (creator_user_id mismatch) — must now be denied and the token
// must survive, matching what that narrow key would see (nothing) via LIST.
func TestRevokeProvisionedKey_DifferentCreator_SameTenant_Denied(t *testing.T) {
	ctx := setupProvTestRouter(t)
	// narrowKeyRaw is whitelisted for tenant-alpha and has CreatedBy =
	// resellerUserID; seed the victim token under tenant-alpha but with
	// CreatorUserId = otherResellerID so only tenant/whitelist line up.
	victim := seedProvisionedToken(t, ctx, ctx.tenantAlpha.Id, "alpha-foreign-creator", ctx.otherResellerID)

	w := fixRevokeRequest(ctx, ctx.tenantAlpha.Slug, victim.Id, ctx.narrowKeyRaw)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for same-tenant, different-creator revoke; body: %s", w.Code, w.Body.String())
	}
	resp := parseList(t, w)
	if resp["error_code"] != "KEY_NOT_FOUND" {
		t.Errorf("error_code = %v, want KEY_NOT_FOUND", resp["error_code"])
	}
	if !fixRevokeTokenExists(t, ctx, victim.Id) {
		t.Error("token was revoked by a key that did not create it")
	}
}

// The other half of the symmetry: a ScopeAll key's LIST reach must match its
// revoke reach. Without the ProvisionedAllCreators bypass on the list query, a
// platform admin key could revoke keys it cannot see — the original asymmetry
// inverted rather than closed. The narrow key on the same tenant keeps seeing
// only its own creator's keys.
func TestListProvisionedKeys_ScopeAll_SeesAllCreators(t *testing.T) {
	ctx := setupProvTestRouter(t)
	seedProvisionedToken(t, ctx, ctx.tenantAlpha.Id, "alpha-own-creator", ctx.resellerUserID)
	seedProvisionedToken(t, ctx, ctx.tenantAlpha.Id, "alpha-foreign-creator", ctx.otherResellerID)

	w := doListRequest(ctx, ctx.tenantAlpha.Slug, ctx.scopeAllRaw, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for ScopeAll list; body: %s", w.Code, w.Body.String())
	}
	data := parseList(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 2 {
		t.Errorf("ScopeAll total = %v, want 2 (both creators' keys)", got)
	}

	w = doListRequest(ctx, ctx.tenantAlpha.Slug, ctx.narrowKeyRaw, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for narrow list; body: %s", w.Code, w.Body.String())
	}
	data = parseList(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 1 {
		t.Errorf("narrow-key total = %v, want 1 (own creator only)", got)
	}
}

// ScopeAll keys keep the cross-creator bypass they already have for the
// tenant whitelist: a platform admin key can revoke a token it did not
// itself create.
func TestRevokeProvisionedKey_ScopeAll_CrossCreator_Allowed(t *testing.T) {
	ctx := setupProvTestRouter(t)
	// scopeAllRaw's CreatedBy is resellerUserID (see setupProvTestRouter);
	// seed the token under a DIFFERENT creator so the bypass is exercised,
	// not an incidental CreatedBy match.
	victim := seedProvisionedToken(t, ctx, ctx.tenantBeta.Id, "beta-foreign-creator", ctx.otherResellerID)

	w := fixRevokeRequest(ctx, ctx.tenantBeta.Slug, victim.Id, ctx.scopeAllRaw)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for ScopeAll cross-creator revoke; body: %s", w.Code, w.Body.String())
	}
	if fixRevokeTokenExists(t, ctx, victim.Id) {
		t.Error("token still present after an authorized ScopeAll revoke")
	}
}
