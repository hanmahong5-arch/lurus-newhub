package router

// tenant_slug_guard_completeness_test.go — cycle-13 L9 structural forcing
// function for tenant isolation on the v2 console surface.
//
// Every route mounted under /api/v2/:tenant_slug/ must either carry
// middleware.TenantSlugGuard (slug resolves → must equal the authenticated
// tenant → tenant must be enabled) or appear in the exemption table below
// naming the tenant check it performs instead. A new tenant-scoped route
// added without either fails this test: you cannot ship one whose URL tenant
// never reaches a comparison.
//
// It reads the REAL chain rather than a hand-kept list: a middleware
// registered before SetApiV2Router sees c.HandlerNames(), which gin populates
// with the whole matched chain (group middleware included — RouterGroup's
// handlers are not otherwise reachable from engine.Routes()). The probe
// aborts, so no handler, database or downstream middleware ever runs.
//
// It lives in the router package (not handler) for the same import-cycle
// reason as its siblings: it needs the real, fully-wired route table.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// tenantSlugConcretePath turns a registered path into one a request can
// actually match by replacing every :param and *wildcard segment with a
// literal.
func tenantSlugConcretePath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, ":") || strings.HasPrefix(p, "*") {
			parts[i] = "probe"
		}
	}
	return strings.Join(parts, "/")
}

func TestTenantSlugRoutesCarryTheGuard(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)

	engine := gin.New()
	var chain []string
	engine.Use(func(c *gin.Context) {
		chain = c.HandlerNames()
		c.AbortWithStatus(http.StatusTeapot)
	})
	SetApiV2Router(engine)

	// The routes that resolve the tenant themselves because they cannot use
	// the shared guard: it compares the slug against an already-authenticated
	// tenant context, and these authenticate with something else (or, for the
	// login redirect, not at all). Each entry names the check that stands in
	// for the guard — an entry without one is not a reason, it is a hole.
	exempt := map[string]string{
		"GET /api/v2/:tenant_slug/auth/login":      "pre-authentication OIDC redirect: there is no identity yet to compare the slug against; the handler resolves the slug only to build the IdP URL",
		"GET /api/v2/:tenant_slug/credit-pool/me":  "OIDCAuth (JWT arm rejects a disabled tenant in mapOIDCUserToLurus, cookie arm runs repo.TenantGate) + GetCreditPoolForEndUser's own slug-vs-context comparison, 403 TENANT_MISMATCH",
		"POST /api/v2/:tenant_slug/provision":      "entitlement-token auth: ProvisionV2 resolves the slug, runs repo.TenantGate on it, creates the bridged user in THAT tenant and refuses an account pinned elsewhere with 403 TENANT_MISMATCH",
		"POST /api/v2/:tenant_slug/user/heartbeat": "raw relay-token auth: UserHeartbeat compares token.TenantId with the slug's tenant and then runs repo.TenantGate (403 TENANT_DISABLED)",
	}

	const guardName = "TenantSlugGuard"
	present := map[string]bool{}
	seen := 0
	for _, rt := range engine.Routes() {
		if !strings.HasPrefix(rt.Path, "/api/v2/:tenant_slug/") {
			continue
		}
		seen++
		key := rt.Method + " " + rt.Path
		present[key] = true

		chain = nil
		engine.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(rt.Method, tenantSlugConcretePath(rt.Path), nil))
		if len(chain) == 0 {
			t.Fatalf("%s: the probe middleware never ran, so the request did not reach this route — the walk would prove nothing", key)
		}

		guarded := false
		for _, name := range chain {
			// The closure is created inside SetApiV2Router, so its runtime
			// name is "…/router.SetApiV2Router.TenantSlugGuard.funcN", not
			// "…/middleware.TenantSlugGuard.funcN" — match on the bare
			// function name.
			if strings.Contains(name, guardName) {
				guarded = true
				break
			}
		}
		reason, exempted := exempt[key]
		switch {
		case guarded && exempted:
			t.Errorf("%s carries %s and is ALSO in the exemption table — delete the entry (%q)", key, guardName, reason)
		case !guarded && !exempted:
			t.Errorf("%s carries no %s and is not in the exemption table: mount the guard, or add an entry naming the tenant check it does instead", key, guardName)
		}
	}

	// A stale exemption is as bad as a missing guard: it would silently cover
	// a route that was renamed or removed.
	for key := range exempt {
		if !present[key] {
			t.Errorf("exemption table lists %q, which is not a registered route — remove the stale entry", key)
		}
	}

	// Fail fast rather than pass vacuously if the route table did not build:
	// 60 routes matched on 2026-09-20.
	if seen < 40 {
		t.Fatalf("walked only %d routes under /api/v2/:tenant_slug/ — the v2 route table did not build, so a green run here means nothing", seen)
	}
}
