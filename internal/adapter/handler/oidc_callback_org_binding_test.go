package handler

// oidc_callback_org_binding_test.go — L9 (cycle 12): the OIDC callback must
// refuse a login whose ID token names a DIFFERENT IdP organization than the
// tenant the login URL selected.
//
// tenants.zitadel_org_id is a unique, not-null 1:1 projection of an upstream
// IdP organization (entity/tenant.go:18), but the callback resolved the tenant
// purely from stateData.TenantSlug — a value that comes from the URL the
// browser was sent to. With OIDC_AUTO_CREATE_USER=true (the live setting), an
// account belonging to organization A that follows tenant B's login URL was
// auto-provisioned INTO tenant B: a user row, a default token, and from then
// on tenant B's channels, pool and logs.
//
// The guard compares only when both sides name a real organization — an empty
// claim (an IdP that does not advertise the organization, the pre-cycle-12
// situation on this deployment) and a tenant still carrying a *_PLACEHOLDER
// stand-in (migrations/021:172, migrations/030:71) both keep the old
// behaviour, which is what the "keeps logging in" cases below pin.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// seedOrgBindingTenant inserts a tenant with an explicit org id into whatever
// DB the harness installed as repo.DB.
func seedOrgBindingTenant(t *testing.T, id, slug, orgID string) *repo.Tenant {
	t.Helper()
	tenant := &repo.Tenant{
		Id:        id,
		Slug:      slug,
		Name:      "Org Binding " + slug,
		IDPOrgID:  orgID,
		Status:    repo.TenantStatusEnabled,
		MaxUsers:  100,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := repo.DB.Create(tenant).Error; err != nil {
		t.Fatalf("seed tenant %s: %v", id, err)
	}
	return tenant
}

// doCallbackForSlug drives OIDCCallback with a state naming an explicit tenant
// slug (h.doCallback always uses the harness's own tenant).
func (h *oidcCallbackTestHarness) doCallbackForSlug(t *testing.T, idToken, slug string) *httptest.ResponseRecorder {
	t.Helper()
	state, _, err := generateOAuthState(slug, "/dashboard")
	if err != nil {
		t.Fatalf("generateOAuthState: %v", err)
	}
	q := url.Values{}
	q.Set("code", idToken)
	q.Set("state", state)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/oauth/callback?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	return w
}

// TestOIDCCallback_OrgMismatch_Rejected is the headline lock: organization A's
// account on tenant B's login URL is refused, and no user row is left behind.
func TestOIDCCallback_OrgMismatch_Rejected(t *testing.T) {
	h := setupOIDCCallbackHarness(t, 6001)
	defer h.cleanup()

	tenantB := seedOrgBindingTenant(t, "org-binding-b", "orgb", "org-B")

	const subject = "org-binding-subject-1"
	signed := h.signMapClaimsIDToken(t, map[string]interface{}{
		"sub":    subject,
		"email":  "org-binding-1@example.com",
		"org_id": "org-A",
	})

	w := h.doCallbackForSlug(t, signed, tenantB.Slug)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — an org-A account must not be admitted into tenant %q (org-B); body=%s",
			w.Code, tenantB.Id, w.Body.String())
	}
	resp := handlerDeployParseBody(t, w)
	if resp["error_code"] != "TENANT_ORG_MISMATCH" {
		t.Errorf("error_code = %v, want TENANT_ORG_MISMATCH; body=%s", resp["error_code"], w.Body.String())
	}

	user, _, err := repo.GetUserByIDPSubject(subject, tenantB.Id)
	if err == nil && user != nil {
		t.Errorf("user %d was provisioned into tenant %q despite the 403", user.Id, tenantB.Id)
	}
}

// TestOIDCCallback_OrgMatch_Admitted is the other half: the account that DOES
// belong to the tenant's organization still logs in. Without it the guard
// could be satisfied by rejecting every org-carrying token.
func TestOIDCCallback_OrgMatch_Admitted(t *testing.T) {
	h := setupOIDCCallbackHarness(t, 6002)
	defer h.cleanup()

	tenantB := seedOrgBindingTenant(t, "org-binding-b2", "orgb2", "org-B2")

	signed := h.signMapClaimsIDToken(t, map[string]interface{}{
		"sub":    "org-binding-subject-2",
		"email":  "org-binding-2@example.com",
		"org_id": "org-B2",
	})

	w := h.doCallbackForSlug(t, signed, tenantB.Slug)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 — a matching org claim must still log in; body=%s", w.Code, w.Body.String())
	}
}

// TestOIDCCallback_NoOrgClaim_Admitted pins the do-not-regress case: an IdP
// that advertises no organization keeps logging users in exactly as before.
func TestOIDCCallback_NoOrgClaim_Admitted(t *testing.T) {
	h := setupOIDCCallbackHarness(t, 6003)
	defer h.cleanup()

	tenantB := seedOrgBindingTenant(t, "org-binding-b3", "orgb3", "org-B3")

	signed := h.signMapClaimsIDToken(t, map[string]interface{}{
		"sub":   "org-binding-subject-3",
		"email": "org-binding-3@example.com",
	})

	w := h.doCallbackForSlug(t, signed, tenantB.Slug)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 — a token with no org claim must keep the pre-guard behaviour; body=%s",
			w.Code, w.Body.String())
	}
}

// TestOIDCCallback_PlaceholderTenantOrgID_Admitted covers the two rows the SQL
// baseline seeds: a tenant whose org id is a stand-in is not bound to any
// organization, so it cannot mismatch one. Enforcing on those rows would lock
// every login out of the "default" and "switch" tenants until an owner
// replaces the seeded value.
func TestOIDCCallback_PlaceholderTenantOrgID_Admitted(t *testing.T) {
	h := setupOIDCCallbackHarness(t, 6004)
	defer h.cleanup()

	tenantB := seedOrgBindingTenant(t, "org-binding-b4", "orgb4", "ORGB4_ORG_ID_PLACEHOLDER")

	signed := h.signMapClaimsIDToken(t, map[string]interface{}{
		"sub":    "org-binding-subject-4",
		"email":  "org-binding-4@example.com",
		"org_id": "org-A",
	})

	w := h.doCallbackForSlug(t, signed, tenantB.Slug)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 — a placeholder tenant org id must not be compared; body=%s",
			w.Code, w.Body.String())
	}
}
