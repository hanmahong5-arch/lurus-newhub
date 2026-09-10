package handler

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// The login handlers (ZitaBootstrap, BridgeExchange) hand the console a
// tenant_slug that the SPA stores and then puts in the path of every
// /api/v2/:tenant_slug/* call. middleware.TenantSlugGuard resolves that path
// segment with repo.GetTenantBySlug and answers 404 TENANT_NOT_FOUND when no
// row carries it. So the only useful contract for resolveTenantSlug is a
// round trip: whatever it returns must find a tenant again by slug.
//
// A tenants row may have an id and a slug that differ — both live
// deployments seed id "default" with slug "lurus" — and resolveTenantSlug
// used to answer the literal "default" for that id without reading the row.
// The SPA then called /api/v2/default/... and got 404 on every panel.
// SetupV2TestRouter seeds slug == id for its own tenant, which is why the
// existing tests could not see it.
// seedFallbackTenant creates the row both live deployments carry for the
// tenant a login lands in when nothing else claims it: id "default", routing
// slug "lurus". Tests that exercise that fallback assert on the slug the
// response carries, so the row has to exist for the answer to be routable.
func seedFallbackTenant(t *testing.T, ctx *V2TestContext) *repo.Tenant {
	t.Helper()
	tenant := &repo.Tenant{
		Id: "default", IDPOrgID: "org_default_fallback",
		Slug: "lurus", Name: "Lurus", Status: repo.TenantStatusEnabled,
	}
	if err := ctx.DB.Create(tenant).Error; err != nil {
		t.Fatalf("seed fallback tenant: %v", err)
	}
	return tenant
}

func TestResolveTenantSlugRoundTripsThroughGetTenantBySlug(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// The shape both live deployments actually have: id != slug.
	deployed := seedFallbackTenant(t, ctx)

	for _, tc := range []struct {
		name     string
		tenantID string
	}{
		{"default tenant whose slug differs from its id", deployed.Id},
		{"harness tenant whose slug equals its id", ctx.TenantID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slug := resolveTenantSlug(tc.tenantID)
			if slug == "" {
				t.Fatalf("resolveTenantSlug(%q) = \"\", want a slug the guard can resolve", tc.tenantID)
			}
			got, err := repo.GetTenantBySlug(slug)
			if err != nil || got == nil {
				t.Fatalf("GetTenantBySlug(%q) (from tenant id %q) = %v, %v — the console would get 404 TENANT_NOT_FOUND on every /api/v2/%s/* call",
					slug, tc.tenantID, got, err, slug)
			}
			if got.Id != tc.tenantID {
				t.Errorf("slug %q resolves to tenant %q, want %q — the guard would answer 403 TENANT_MISMATCH", slug, got.Id, tc.tenantID)
			}
		})
	}
}
