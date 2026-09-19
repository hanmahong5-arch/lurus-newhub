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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus/testutil"
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

// enableOrgBindingAudit makes the audit trail readable from the harness's own
// DB (setupOIDCCallbackHarness installs it as repo.DB): the two tables the
// writer needs, plus a writer pinned to that DB for the duration of the test
// (pinAuditWriter, audit_writer_pin_test.go). governance.AsyncGo is
// synchronous for this package (TestMain, async_seam_test.go), so the row is
// durable by the time the callback returns.
func enableOrgBindingAudit(t *testing.T) {
	t.Helper()
	if err := repo.DB.AutoMigrate(&entity.AuditEvent{}, &entity.AuditChainHead{}); err != nil {
		t.Fatalf("migrate audit tables: %v", err)
	}
	pinAuditWriter(t, repo.DB)
}

// orgBindingAuditDetails returns the parsed `details` JSON of the single
// auth.failed/tenant audit row, failing the test when the count is not one.
func orgBindingAuditDetails(t *testing.T) map[string]interface{} {
	t.Helper()
	var events []entity.AuditEvent
	if err := repo.DB.Where("action = ? AND resource = ?",
		governance.ActionAuthFailed, governance.ResourceTenant).Find(&events).Error; err != nil {
		t.Fatalf("read audit events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit rows with action=%s resource=%s = %d, want exactly 1 — the refusal's only durable record",
			governance.ActionAuthFailed, governance.ResourceTenant, len(events))
	}
	var details map[string]interface{}
	if err := json.Unmarshal([]byte(events[0].Details), &details); err != nil {
		t.Fatalf("parse audit details %q: %v", events[0].Details, err)
	}
	return details
}

// orgOutcomeCount reads one outcome series of the tenant-gate counter. The
// counter is process-global, so every assertion here is on a delta.
func orgOutcomeCount(t *testing.T, outcome string) float64 {
	t.Helper()
	return testutil.ToFloat64(metrics.TenantGateTotal.WithLabelValues(outcome))
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
	enableOrgBindingAudit(t)

	tenantB := seedOrgBindingTenant(t, "org-binding-b", "orgb", "org-B")
	beforeDenied := orgOutcomeCount(t, metrics.TenantGateOutcomeOrgMismatchDenied)

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

	// The audit row is the only durable record an operator has of this
	// refusal: the browser got JSON, nothing was written to users, and the
	// system log is not queryable after rotation.
	details := orgBindingAuditDetails(t)
	if details["reason"] != "tenant_org_mismatch" {
		t.Errorf("audit reason = %v, want tenant_org_mismatch (details=%v)", details["reason"], details)
	}
	if details["tenant_id"] != tenantB.Id {
		t.Errorf("audit tenant_id = %v, want %q (details=%v)", details["tenant_id"], tenantB.Id, details)
	}
	if details["tenant_org_id"] != "org-B" || details["claim_org_id"] != "org-A" {
		t.Errorf("audit org ids = (%v, %v), want (org-B, org-A) (details=%v)",
			details["tenant_org_id"], details["claim_org_id"], details)
	}

	if after := orgOutcomeCount(t, metrics.TenantGateOutcomeOrgMismatchDenied); after != beforeDenied+1 {
		t.Errorf("tenant_gate_total{outcome=%q} = %v, want %v — a refusal an operator cannot count is invisible",
			metrics.TenantGateOutcomeOrgMismatchDenied, after, beforeDenied+1)
	}
}

// TestOIDCCallback_OrgMatch_Admitted is the other half: the account that DOES
// belong to the tenant's organization still logs in. Without it the guard
// could be satisfied by rejecting every org-carrying token.
func TestOIDCCallback_OrgMatch_Admitted(t *testing.T) {
	h := setupOIDCCallbackHarness(t, 6002)
	defer h.cleanup()

	tenantB := seedOrgBindingTenant(t, "org-binding-b2", "orgb2", "org-B2")
	beforeMatched := orgOutcomeCount(t, metrics.TenantGateOutcomeOrgMatched)

	signed := h.signMapClaimsIDToken(t, map[string]interface{}{
		"sub":    "org-binding-subject-2",
		"email":  "org-binding-2@example.com",
		"org_id": "org-B2",
	})

	w := h.doCallbackForSlug(t, signed, tenantB.Slug)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 — a matching org claim must still log in; body=%s", w.Code, w.Body.String())
	}
	// Admissions are counted too: without the denominator an operator cannot
	// tell "everything matched" from "nothing was comparable" (owner O-org).
	if after := orgOutcomeCount(t, metrics.TenantGateOutcomeOrgMatched); after != beforeMatched+1 {
		t.Errorf("tenant_gate_total{outcome=%q} = %v, want %v",
			metrics.TenantGateOutcomeOrgMatched, after, beforeMatched+1)
	}
}

// TestOIDCCallback_NoOrgClaim_Admitted pins the do-not-regress case: an IdP
// that advertises no organization keeps logging users in exactly as before.
func TestOIDCCallback_NoOrgClaim_Admitted(t *testing.T) {
	h := setupOIDCCallbackHarness(t, 6003)
	defer h.cleanup()

	tenantB := seedOrgBindingTenant(t, "org-binding-b3", "orgb3", "org-B3")
	beforeAbsent := orgOutcomeCount(t, metrics.TenantGateOutcomeOrgClaimAbsent)

	signed := h.signMapClaimsIDToken(t, map[string]interface{}{
		"sub":   "org-binding-subject-3",
		"email": "org-binding-3@example.com",
	})

	w := h.doCallbackForSlug(t, signed, tenantB.Slug)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 — a token with no org claim must keep the pre-guard behaviour; body=%s",
			w.Code, w.Body.String())
	}
	// This is the series that answers the first half of owner item O-org
	// ("does the IdP emit an org_id claim at all?") from production traffic.
	if after := orgOutcomeCount(t, metrics.TenantGateOutcomeOrgClaimAbsent); after != beforeAbsent+1 {
		t.Errorf("tenant_gate_total{outcome=%q} = %v, want %v",
			metrics.TenantGateOutcomeOrgClaimAbsent, after, beforeAbsent+1)
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
	beforeUnbound := orgOutcomeCount(t, metrics.TenantGateOutcomeOrgTenantUnbound)

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
	// Counted separately from a match, because this IS the production
	// situation today: both seeded tenants carry a placeholder, so a
	// cross-organization login lands here and the guard refuses nothing until
	// owner item O-org replaces those two values.
	if after := orgOutcomeCount(t, metrics.TenantGateOutcomeOrgTenantUnbound); after != beforeUnbound+1 {
		t.Errorf("tenant_gate_total{outcome=%q} = %v, want %v — this series is how an operator sees that the guard is dormant",
			metrics.TenantGateOutcomeOrgTenantUnbound, after, beforeUnbound+1)
	}
}
