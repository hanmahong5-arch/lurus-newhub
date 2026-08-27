package repo

// l4_tenant_reserved_slug_test.go — reserved-slug enforcement on the tenant
// auto-provisioning path (CreateTenantFromIDP), covering:
//   - new tenants cannot be created with a slug that shadows a reserved
//     top-level /api/v2/* route segment (D5's paydown, lane L4);
//   - the guard sits AFTER the idempotent short-circuit, so an org that
//     already exists (even with a slug that would be rejected today, e.g.
//     the live slug="switch" tenant) keeps resolving on repeat calls instead
//     of suddenly failing;
//   - ValidateTenantSlug is a reserved-word check only, NOT a format/charset
//     validator — real OIDC org domains contain dots and must keep working.
//
// Uses the hermetic SQLite tier (setupSQLiteDB), no TEST_POSTGRES_DSN
// required.

import (
	"errors"
	"testing"
)

func TestL4CreateTenantFromIDP_RejectsReservedSlug(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	for _, slug := range []string{"switch", "admin", "user", "Switch", "ADMIN"} {
		t.Run(slug, func(t *testing.T) {
			orgID := "org-reserved-" + slug
			tenant, err := CreateTenantFromIDP(orgID, slug, "Reserved Slug Org")
			if err == nil {
				t.Fatalf("expected error for reserved slug %q, got nil (tenant=%+v)", slug, tenant)
			}
			if !errors.Is(err, ErrReservedTenantSlug) {
				t.Fatalf("expected ErrReservedTenantSlug for slug %q, got: %v", slug, err)
			}

			// No row must have been persisted for this org id.
			if _, lookupErr := GetTenantByIDPOrgID(orgID); lookupErr == nil {
				t.Fatalf("expected no tenant row to exist for rejected org %q, but lookup succeeded", orgID)
			}
			var count int64
			if dbErr := DB.Model(&Tenant{}).Where("zitadel_org_id = ?", orgID).Count(&count).Error; dbErr != nil {
				t.Fatalf("count query failed: %v", dbErr)
			}
			if count != 0 {
				t.Fatalf("expected 0 rows for rejected org %q, got %d", orgID, count)
			}
		})
	}
}

// TestL4CreateTenantFromIDP_AcceptsDottedOrgDomain locks in that
// ValidateTenantSlug must never become a format/charset regex: Tenant.Slug is
// populated directly from the upstream OIDC organization domain, and real
// values look like "acme.example.com" — dots and all. If a future change
// adds a `^[a-z0-9][a-z0-9-]{1,62}$`-style pattern here, this test goes red.
func TestL4CreateTenantFromIDP_AcceptsDottedOrgDomain(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	tenant, err := CreateTenantFromIDP("org-dotted-domain", "acme.example.com", "Acme Corp")
	if err != nil {
		t.Fatalf("CreateTenantFromIDP() with dotted org domain returned error: %v", err)
	}
	if tenant.Slug != "acme.example.com" {
		t.Errorf("Slug mismatch: got %q", tenant.Slug)
	}
}

// TestL4CreateTenantFromIDP_IdempotentForExisting proves the reserved-slug
// guard is inserted AFTER the GetTenantByIDPOrgID idempotent short-circuit:
// a tenant that already exists in the DB with a now-reserved slug (mirrors
// the live slug="switch" tenant, which predates this guard and is explicitly
// out of scope for this lane per X3) must keep resolving on repeat
// CreateTenantFromIDP calls for its org id, not start failing.
func TestL4CreateTenantFromIDP_IdempotentForExisting(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	pre := seedTenant(t, "tenant-legacy-switch", "switch", "Legacy Switch Org")
	// seedTenant's IDPOrgID is "org_"+id; CreateTenantFromIDP looks up by that.
	orgID := "org_" + pre.Id

	got, err := CreateTenantFromIDP(orgID, "switch", "Legacy Switch Org")
	if err != nil {
		t.Fatalf("expected idempotent CreateTenantFromIDP to succeed for pre-existing reserved-slug tenant, got error: %v", err)
	}
	if got.Id != pre.Id {
		t.Errorf("expected same tenant id on idempotent call: got %q, want %q", got.Id, pre.Id)
	}
}
