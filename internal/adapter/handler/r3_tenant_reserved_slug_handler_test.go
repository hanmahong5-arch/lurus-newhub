package handler

// r3_tenant_reserved_slug_handler_test.go — C1 (lane R3): the 400 mapping in
// tenant.go CreateTenant() for repo.ErrReservedTenantSlug had zero handler-level
// coverage. Adversarial proof it was unlocked: rewriting the branch at
// tenant.go:132 to `if false && errors.Is(err, repo.ErrReservedTenantSlug)`
// still left the PG-backed internal/adapter/handler package green
// (ok 58.049s) — see this file's own mutation-proof run in the R3 handoff.
//
// Reuses SetupV2TestRouter (v2_testutil_test.go) rather than defining a new
// setup/seed helper in package handler, per lane instructions; it only adds
// the one admin route this test needs.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// r3TenantAdminSetup mounts POST /api/v2/admin/tenants -> CreateTenant on a
// fresh SetupV2TestRouter instance. No additional auth wiring is needed:
// CreateTenant itself does not gate on role (that happens one layer up, via
// middleware.RootJWTAuth in the real router — see api-v2-router.go:334); the
// handler-level behavior under test here is the repo.ErrReservedTenantSlug ->
// HTTP 400 mapping, which is reachable regardless of caller identity.
func r3TenantAdminSetup(t *testing.T) *V2TestContext {
	t.Helper()
	ctx := SetupV2TestRouter(t)
	ctx.Router.POST("/api/v2/admin/tenants", CreateTenant)
	return ctx
}

// TestR3CreateTenant_ReservedSlugReturns400 is the regression lock for C1.
// The org id must be fresh (not one that already resolves to an existing
// tenant) so the idempotent short-circuit in CreateTenantFromIDP
// (repo/tenant.go:80-86) is NOT taken -- an existing-org id would return 200
// and make this test hollow (it would pass whether or not the 400 mapping at
// tenant.go:132 exists at all).
func TestR3CreateTenant_ReservedSlugReturns400(t *testing.T) {
	ctx := r3TenantAdminSetup(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"zitadel_org_id": "org-r3-reserved-switch",
		"slug":           "switch",
		"name":           "R3 Reserved Slug Org",
	}
	w := V2Request(ctx.Router, "POST", "/api/v2/admin/tenants", body, nil)

	resp := AssertV2Error(t, w, http.StatusBadRequest)
	msg, _ := resp["message"].(string)
	if !strings.Contains(msg, "switch") {
		t.Errorf("expected 400 message to name the offending slug %q, got: %q", "switch", msg)
	}

	// The rejected create must not have persisted a tenant row.
	if tenant, err := repo.GetTenantByIDPOrgID("org-r3-reserved-switch"); err == nil {
		t.Errorf("expected no tenant to exist for rejected org, but lookup succeeded: %+v", tenant)
	}
}

// TestR3CreateTenant_OrdinarySlugSucceeds is a companion positive: the 400
// mapping must not swallow ordinary, non-reserved slugs.
func TestR3CreateTenant_OrdinarySlugSucceeds(t *testing.T) {
	ctx := r3TenantAdminSetup(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"zitadel_org_id": "org-r3-ordinary",
		"slug":           "acme-corp",
		"name":           "R3 Ordinary Org",
	}
	w := V2Request(ctx.Router, "POST", "/api/v2/admin/tenants", body, nil)
	AssertV2Status(t, w, http.StatusCreated)
	AssertV2Success(t, w)
}

// TestR3CreateTenant_DottedOrgDomainAccepted is the handler-level companion
// to repo's TestL4CreateTenantFromIDP_AcceptsDottedOrgDomain: guards against
// anyone "fixing" the reserved-slug gate with a charset/format regex. On the
// OIDC auto-create path Tenant.Slug is populated directly from the upstream
// organization domain (e.g. "acme.example.com" — dots and all); this test
// exercises the admin API instead, which accepts the same kind of raw,
// dotted value through the identical ValidateTenantSlug call, so a format
// regex added here would break both callers.
func TestR3CreateTenant_DottedOrgDomainAccepted(t *testing.T) {
	ctx := r3TenantAdminSetup(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"zitadel_org_id": "org-r3-dotted-domain",
		"slug":           "acme.example.com",
		"name":           "R3 Dotted Domain Org",
	}
	w := V2Request(ctx.Router, "POST", "/api/v2/admin/tenants", body, nil)
	AssertV2Status(t, w, http.StatusCreated)
	AssertV2Success(t, w)
}
