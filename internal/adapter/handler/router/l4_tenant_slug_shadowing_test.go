package router

// l4_tenant_slug_shadowing_test.go — structural forcing function for D5
// (gin's static-segment-before-:param route resolution shadowing an entry of
// the UserAuth+TenantSlugGuard-gated /api/v2/:tenant_slug/* group whenever a
// tenant's slug equals a static top-level segment AND the method+path of a
// request collides exactly with a route registered under that segment — gin
// backtracks to the :tenant_slug sibling for every other method+path shape,
// verified live on gin v1.12.0; see the correspondingly-updated blast-radius
// comment in internal/adapter/repo/l4_tenant_reserved_slug.go).
//
// This is an oracle, not a checklist: instead of hand-maintaining a list of
// "routes that shadow :tenant_slug", it DERIVES the set of static top-level
// segments from the live route table (via SetApiV2Router/engine.Routes(),
// same technique as v2_completeness_test.go / idor_completeness_test.go) and
// asserts that set is a subset of repo.ReservedTenantSlugs.
//
// Coverage note (R3/C2): two of the ten reserved segments — "me" and
// "bridge" — are registered CONDITIONALLY (api-v2-router.go: `if
// common.ZitaClient != nil` / `if handler.BridgeEnabled()`), neither true in
// a bare `gin.New()` + SetApiV2Router(engine) test process. Deleting both
// entries from repo.ReservedTenantSlugs previously left this test green
// (verified: the unconditional-only walk derives zero segments that
// reference them, so the subset check never has anything to complain about
// for those two words) — i.e. the walk covered only the UNCONDITIONAL
// segments. This test now builds the engine TWICE — once with the
// conditional registrations left off, once with both forced on via
// t.Setenv/InitZitaClient — and takes the UNION of both route tables before
// checking the subset, so the derived set now includes "me" and "bridge"
// too and a regression in either is caught by name.
//
// It lives in the router package (not handler/repo) for the same import-cycle
// reason as its siblings: it needs the real, fully-wired route table.

import (
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// r3TenantSlugSegments splits a gin-registered path
// ("/api/v2/:tenant_slug/tokens") into its "/"-delimited parts, dropping the
// leading empty element from the initial "/".
func r3TenantSlugSegments(path string) []string {
	parts := strings.Split(path, "/")
	if len(parts) > 0 && parts[0] == "" {
		parts = parts[1:]
	}
	return parts
}

// r3DeriveTenantSlugIndex finds the segment index :tenant_slug sits at in
// the given route table — do not hard-code it, so a future restructure
// (e.g. an extra "/api/v2/tenants/:tenant_slug/..." nesting level) is still
// measured correctly instead of silently comparing the wrong segment.
func r3DeriveTenantSlugIndex(t *testing.T, routes gin.RoutesInfo) int {
	t.Helper()
	tenantSlugIndex := -1
	for _, rt := range routes {
		segs := r3TenantSlugSegments(rt.Path)
		for i, seg := range segs {
			if seg == ":tenant_slug" {
				if tenantSlugIndex == -1 {
					tenantSlugIndex = i
				} else if tenantSlugIndex != i {
					t.Fatalf(":tenant_slug appears at inconsistent segment index across routes: %d vs %d (route %s %s)",
						tenantSlugIndex, i, rt.Method, rt.Path)
				}
				break
			}
		}
	}
	if tenantSlugIndex == -1 {
		t.Fatal("no route containing :tenant_slug was found in the v2 router — cannot derive shadowing depth")
	}
	return tenantSlugIndex
}

// r3DeriveStaticSegments collects every static (non-param) literal that ANY
// route in the given table places at tenantSlugIndex — these are exactly the
// literals gin's route tree will match BEFORE ever falling back to the
// :tenant_slug wildcard sibling for a request with the same method+path
// shape (gin tree.go: static children are tried before the param child).
// Entries already present in dst (from a prior pass) are left alone, so
// callers can accumulate a union across multiple route-table snapshots.
func r3DeriveStaticSegments(routes gin.RoutesInfo, tenantSlugIndex int, dst map[string]string) {
	for _, rt := range routes {
		segs := r3TenantSlugSegments(rt.Path)
		if len(segs) <= tenantSlugIndex {
			continue
		}
		seg := segs[tenantSlugIndex]
		if seg == "" || strings.HasPrefix(seg, ":") || strings.HasPrefix(seg, "*") {
			continue // param/catch-all, not a literal collision candidate
		}
		if _, ok := dst[seg]; !ok {
			dst[seg] = rt.Method + " " + rt.Path
		}
	}
}

func TestL4TenantSlugShadowing_StaticSegmentsAreReserved(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Pass 1: unconditional registrations only (common.ZitaClient nil,
	// E2E_BRIDGE_TOKEN unset — the default test-process state).
	baseEngine := gin.New()
	SetApiV2Router(baseEngine)
	baseRoutes := baseEngine.Routes()
	tenantSlugIndex := r3DeriveTenantSlugIndex(t, baseRoutes)

	derived := map[string]string{} // slug -> one example "METHOD PATH" for the error message
	r3DeriveStaticSegments(baseRoutes, tenantSlugIndex, derived)
	baseCount := len(derived)

	if baseCount == 0 {
		t.Fatal("derived zero static top-level segments from the unconditional route table — the route table walk is broken, this test would pass vacuously")
	}

	// Pass 2: force both env-conditional registration seams on and re-derive
	// against a FRESH engine, then union the result into derived. Process
	// globals (common.ZitaClient) are reset via t.Cleanup so a leak cannot
	// change sibling tests' route tables in this package.
	t.Setenv("E2E_BRIDGE_TOKEN", "r3-test-bridge-token")
	if !handler.BridgeEnabled() {
		t.Fatal("t.Setenv(E2E_BRIDGE_TOKEN) did not take effect — handler.BridgeEnabled() still false")
	}

	t.Setenv("IDENTITY_PUBLIC_URL", "http://identity.invalid.test")
	t.Setenv("IDENTITY_SESSION_SECRET", "r3-test-session-secret-not-a-real-secret")
	prevZitaClient := common.ZitaClient
	t.Cleanup(func() { common.ZitaClient = prevZitaClient })
	common.InitZitaClient()
	if common.ZitaClient == nil {
		t.Fatal("common.InitZitaClient() with synthetic IDENTITY_PUBLIC_URL/IDENTITY_SESSION_SECRET left ZitaClient nil — cannot exercise the conditional /me/zita registration")
	}

	condEngine := gin.New()
	SetApiV2Router(condEngine)
	condRoutes := condEngine.Routes()
	r3DeriveStaticSegments(condRoutes, tenantSlugIndex, derived)

	if len(derived) <= baseCount {
		t.Fatalf("enabling the env-conditional route registrations (ZitaClient + E2E_BRIDGE_TOKEN) did not grow the derived static-segment set (%d -> %d); InitZitaClient or BridgeEnabled likely failed silently, which would make this test's union pass vacuously", baseCount, len(derived))
	}
	for _, want := range []string{"me", "bridge"} {
		if _, ok := derived[want]; !ok {
			t.Errorf("expected the env-conditional pass to derive static segment %q, but it did not — the conditional registration for it may be broken", want)
		}
	}

	for slug, example := range derived {
		if _, ok := repo.ReservedTenantSlugs[slug]; !ok {
			t.Errorf("static route segment %q (e.g. %s) sits at the same route-tree depth as :tenant_slug "+
				"but is NOT registered in repo.ReservedTenantSlugs — a request whose method+path collides "+
				"exactly with this static route would be shadowed instead of resolving to a tenant whose slug "+
				"is %q (gin matches static segments before the :param sibling for an exact method+path match). "+
				"Add %q to repo.ReservedTenantSlugs (internal/adapter/repo/l4_tenant_reserved_slug.go).", slug, example, slug, slug)
		}
	}
}
