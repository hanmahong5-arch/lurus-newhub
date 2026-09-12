package handler

// audit_coverage_gen.go — production-side artefacts for the L2
// audit-completeness coverage endpoint (GET /api/v2/admin/audit/coverage,
// v2_admin_audit.go).
//
// Two pieces live here:
//
//  1. AuditExplicitRoutes is a static "route → explicitly audited" map. It is
//     the production-fast-path mirror of the live go/ast walk
//     router/audit_coverage_test.go (TestAdminWriteRoutesAreAudited) performs
//     at every CI run — that test parses source and follows one level of
//     same-package calls looking for governance.NewAuditEvent /
//     governance.RecordAuditEvent, which needs the repo's source tree and is
//     not something a deployed binary can do to itself. This map is the
//     checked-in snapshot of that walk's result; the CI test is the thing
//     that keeps it honest — any route added here without an actual audit
//     call fails TestAdminWriteRoutesAreAudited (it independently re-derives
//     "audited" from source), and any admin write route NOT audited and NOT
//     in router/audit_coverage_test.go's knownFallbackRoutes fails the same
//     test. A route absent from this map is NOT assumed unaudited by
//     anything except the coverage endpoint's own classification (see
//     GetAdminWriteRoutes below) — that's exactly "relying on fallback".
//
//  2. adminWriteRoutes is the actual mutating admin-route table, captured
//     once at router-build time. *gin.Context has no Engine() accessor in
//     gin v1.12.0, so GetAuditCoverageV2 (which only has a *gin.Context)
//     cannot enumerate router.Routes() itself; router.SetApiV2Router /
//     SetInternalApiRouter DO hold the *gin.Engine and call SetAdminWriteRoutes
//     once, after every route is registered, with the real table.

import (
	"net/http"
	"strings"
	"sync"
)

// AuditExplicitRoutes lists "METHOD PATH" (gin's registered-path format,
// e.g. "POST /api/v2/admin/tenants") admin/internal-admin write routes whose
// handler calls governance.NewAuditEvent / governance.RecordAuditEvent
// itself (directly, or via one level of same-package delegation — e.g.
// UpdateAdminOptionV2 → UpdateOption). Absent = "relies on
// middleware.AuditWriteGuard's fallback" as far as the coverage endpoint is
// concerned.
var AuditExplicitRoutes = map[string]bool{
	// api-v2-router.go: adminRoute (RootJWTAuth) — tenant.go
	"POST /api/v2/admin/tenants":             true,
	"PUT /api/v2/admin/tenants/:id":          true,
	"DELETE /api/v2/admin/tenants/:id":       true,
	"POST /api/v2/admin/tenants/:id/enable":  true,
	"POST /api/v2/admin/tenants/:id/disable": true,
	"POST /api/v2/admin/tenants/:id/suspend": true,
	// tenant_model_limits.go
	"PUT /api/v2/admin/tenants/:id/model-limits":    true,
	"DELETE /api/v2/admin/tenants/:id/model-limits": true,
	// tenant_model_allowlist.go
	"PUT /api/v2/admin/tenants/:id/model-allowlist":    true,
	"DELETE /api/v2/admin/tenants/:id/model-allowlist": true,
	// tenant_credit_pool.go — money routes, explicit as of this lane
	"POST /api/v2/admin/tenants/:id/credit-pool":       true,
	"POST /api/v2/admin/tenants/:id/credit-pool/topup": true,
	"DELETE /api/v2/admin/tenants/:id/credit-pool":     true,
	// tenant_invite.go
	"POST /api/v2/admin/tenants/:id/invites":              true,
	"DELETE /api/v2/admin/tenants/:id/invites/:invite_id": true,
	// v2_admin.go
	"DELETE /api/v2/admin/mappings/:id": true,
	// internal_api_key_admin_v2.go
	"POST /api/v2/admin/internal-keys/:id/tenants":              true,
	"DELETE /api/v2/admin/internal-keys/:id/tenants/:tenant_id": true,
	// v2_admin_users.go
	"PUT /api/v2/admin/users/:id":             true,
	"DELETE /api/v2/admin/users/:id":          true,
	"DELETE /api/v2/admin/users/:id/sessions": true, // L7 — explicit as of this lane
	// v2_admin_options.go → option.go (one level of delegation)
	"PUT /api/v2/admin/options": true,
	// switch_preset.go — explicit as of this lane
	"POST /api/v2/admin/switch/presets": true,
	// v2_admin_routing.go — L5, explicit as of this lane
	"DELETE /api/v2/admin/routing/affinity":      true,
	"DELETE /api/v2/admin/routing/affinity/:key": true,
	// v2_admin_security.go — L6, explicit as of this lane
	"POST /api/v2/admin/security/users/:id/totp/force-disable": true,

	// internal-api-router.go: adminGroup (ScopeAdmin) — explicit as of this lane
	"POST /internal/admin/backfill-token-accounts": true,
	"POST /internal/admin/rotate-due-tokens":       true,
	"POST /internal/admin/reset-due-pools":         true,

	// The one root-gated write outside /admin (L1, v2_pricing_write.go).
	"POST /api/v2/:tenant_slug/pricing": true,
}

// isMutatingWriteMethod reports whether method is one AuditWriteGuard treats
// as a write (matches middleware.auditGuardedMethods — duplicated here rather
// than imported to avoid a handler→middleware→handler import risk; both
// lists are two lines and covered by the CI test either way).
func isMutatingWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// IsAdminWriteRoute reports whether (method, path) is in scope for the L2
// audit-completeness guard: a mutating request under /api/v2/admin or
// /internal/admin, or the one root-gated write outside /admin. Shared by the
// router setup (to build the captured route list) and by
// router/audit_coverage_test.go (so both sides of "what counts" cannot
// drift apart).
func IsAdminWriteRoute(method, path string) bool {
	if !isMutatingWriteMethod(method) {
		return false
	}
	if path == "/api/v2/:tenant_slug/pricing" {
		return true
	}
	return strings.HasPrefix(path, "/api/v2/admin/") || strings.HasPrefix(path, "/internal/admin/")
}

var (
	adminWriteRoutesMu sync.RWMutex
	adminWriteRoutes   []string
)

// SetAdminWriteRoutes replaces the captured admin-write route list. Called
// once by router.SetApiV2Router / SetInternalApiRouter after all routes are
// registered on the real *gin.Engine (see package doc above for why this
// can't happen inside the request handler itself). Callers pass raw
// "METHOD PATH" entries already filtered by IsAdminWriteRoute — this setter
// does not re-filter, so tests can also use it to inject a synthetic route.
func SetAdminWriteRoutes(routes []string) {
	adminWriteRoutesMu.Lock()
	defer adminWriteRoutesMu.Unlock()
	adminWriteRoutes = append([]string(nil), routes...)
}

// GetAdminWriteRoutes returns a snapshot of the captured admin-write route
// list. Used by GetAuditCoverageV2 to classify each route as explicit or
// fallback-covered.
func GetAdminWriteRoutes() []string {
	adminWriteRoutesMu.RLock()
	defer adminWriteRoutesMu.RUnlock()
	return append([]string(nil), adminWriteRoutes...)
}
