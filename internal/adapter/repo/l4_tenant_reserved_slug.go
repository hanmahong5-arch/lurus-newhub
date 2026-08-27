package repo

import (
	"errors"
	"strings"
)

// ErrReservedTenantSlug is returned when a tenant is about to be created (or
// would be created) with a slug that collides with a static top-level path
// segment registered under /api/v2/:tenant_slug/* — see
// internal/adapter/handler/router/api-v2-router.go.
//
// gin's route tree matches static children before it falls back to a :param
// wildcard sibling, so a tenant whose slug equals one of these segments has
// exactly the entries whose method AND path collide exactly shadowed by the
// public/differently-authed static route of the same name — gin backtracks
// to the :tenant_slug sibling for every other method+path shape under that
// segment (verified live on gin v1.12.0, go.mod pinned: e.g.
// GET/POST /api/v2/switch/tokens still resolves to
// :tenant_slug/tokens; only GET /api/v2/switch/pricing and
// POST /api/v2/switch/redeem, which have an exact static counterpart, are
// swallowed).
//
// The whole top-level segment is reserved anyway, as a deliberate
// conservative superset: it is cheap (these words are not realistic org
// slugs) and it means a future static sub-route added under an
// already-reserved segment can never silently widen the collision surface —
// see internal/adapter/handler/router/l4_tenant_slug_shadowing_test.go, which
// derives the reserved set of top-level segments from the live route table
// rather than trusting this list to stay in sync by hand.
var ErrReservedTenantSlug = errors.New("tenant slug collides with a reserved top-level API route segment")

// ReservedTenantSlugs enumerates every static first-path-segment registered
// directly under /api/v2/ (i.e. at the same tree depth as :tenant_slug) as of
// api-v2-router.go. Each entry below is annotated with the route(s) that
// register it as a static segment.
var ReservedTenantSlugs = map[string]struct{}{
	// GET /api/v2/oauth/callback, POST /api/v2/oauth/logout, POST /api/v2/oauth/refresh
	"oauth": {},
	// GET /api/v2/auth/session-info, /api/v2/auth/zita-login, /api/v2/auth/zita-logout,
	// POST /api/v2/auth/zita-bootstrap
	"auth": {},
	// GET /api/v2/me/zita — registered only when common.ZitaClient != nil
	// (api-v2-router.go), i.e. IDENTITY_PUBLIC_URL + IDENTITY_SESSION_SECRET
	// are set. This entry IS derived: the router oracle
	// (l4_tenant_slug_shadowing_test.go) builds the route table a second time
	// with the seam forced on (t.Setenv + common.InitZitaClient), takes the
	// union with the unconditional table, and asserts "me" is present in
	// that derived set by name — deleting this entry fails that test with a
	// name-specific error, it is not left unchecked. The path stays reserved
	// regardless of whether this particular process registers it.
	"me": {},
	// POST /api/v2/bridge/exchange — registered only when
	// handler.BridgeEnabled() (E2E_BRIDGE_TOKEN set), but the path is
	// reserved regardless of whether it is registered in this process.
	// Derived the same way as "me" above (l4_tenant_slug_shadowing_test.go
	// pass 2, t.Setenv(E2E_BRIDGE_TOKEN)).
	"bridge": {},
	// GET/POST /api/v2/switch/* (Switch desktop client — public routes group).
	// The live slug="switch" tenant (created before this guard existed, kept
	// resolving via the idempotent short-circuit — see
	// TestL4CreateTenantFromIDP_IdempotentForExisting below) was queried
	// directly against the production DB on 2026-08-26: 0 users / 0 tokens /
	// 0 redemption codes / 0 logs — all 4 real users and both redemption
	// codes live under the "default" tenant. Today's actual blast radius for
	// this specific tenant is therefore zero and a future rename needs no
	// data migration. This is a point-in-time fact about this one tenant, not
	// a claim about any future tenant.
	"switch": {},
	// GET /api/v2/relays/recommended
	"relays": {},
	// GET /api/v2/tools/download-manifest
	"tools": {},
	// GET/POST /api/v2/user/* (platform user routes, OIDCAuth)
	"user": {},
	// /api/v2/admin/* (RootJWTAuth-gated platform admin surface)
	"admin": {},
	// POST /api/v2/lutu/search
	"lutu": {},
}

// ValidateTenantSlug rejects a candidate tenant slug if it collides
// (case-insensitively) with a reserved top-level API route segment. It is
// intentionally NOT a format/charset validator: on the OIDC auto-create path
// (handler/oauth.go, middleware/oidc_auth.go) Tenant.Slug is populated
// directly from the upstream OIDC organization domain (e.g.
// "acme.example.com"), which legitimately contains dots and is not a
// URL-safe identifier by any narrower convention; the admin API
// (POST /api/v2/admin/tenants, handler/tenant.go) also accepts an explicit,
// human-typed slug through the same validator. Adding a format regex here
// would reject real OIDC org domains at signup time — see
// internal/adapter/repo/l4_tenant_reserved_slug_test.go
// TestL4CreateTenantFromIDP_AcceptsDottedOrgDomain for the regression lock.
func ValidateTenantSlug(slug string) error {
	if _, reserved := ReservedTenantSlugs[strings.ToLower(slug)]; reserved {
		return ErrReservedTenantSlug
	}
	return nil
}
