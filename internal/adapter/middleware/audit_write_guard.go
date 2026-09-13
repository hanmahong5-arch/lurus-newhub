package middleware

// audit_write_guard.go — L2 audit-completeness fail-closed backstop.
//
// governance.RecordAuditEvent is called by hand from handler code (and from
// a handful of middleware rejection paths, e.g. middleware/auth.go); nothing
// enforced that an admin write route actually reaches one of those calls, so
// a forgotten call was invisible until a compliance export went looking for
// it — L1's pricing route was the live proof (it shipped with zero audit
// calls for months). AuditWriteGuard closes that gap structurally: mounted
// on an admin write route group, it runs the handler and then checks whether
// governance.RecordAuditEvent actually fired during this request
// (governance.AuditedContextKey, set by the persisting call itself — see
// governance/audit.go). If a mutating method completed without that flag, it
// records a typed admin.write_unaudited fallback event and increments
// metrics.AdminWriteUnauditedTotal.
//
// It fires on every response status this test suite exercises, not only
// 2xx: a write the handler rejected (403/…) is still a write attempt, and
// dropping it from the audit trail just because it failed would hide
// exactly the events a security reviewer cares most about
// (TestAuditWriteGuard_FallbackRowWhenHandlerSilent covers 201,
// _FiresOnRejectedWrite covers 403).

import (
	"encoding/json"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
)

// auditGuardedMethods is the set of HTTP methods AuditWriteGuard treats as
// mutating; any other method (GET is the one TestAuditWriteGuard_ReadRoutesIgnored
// exercises; HEAD/OPTIONS are not tested but match the same map lookup) returns
// before reaching the fallback path — a read that forgets to audit is not a
// governance gap.
var auditGuardedMethods = map[string]bool{
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

// auditFallbackDetails is the JSON shape recorded on governance.ActionAdminWriteUnaudited
// fallback rows. admin_sub is only populated when the caller is attributed
// solely by Zitadel subject (RootJWTAuth's Bearer-JWT branch never sets the
// "id" context key — admin_jwt_auth.go:101 — a pre-existing gap this guard
// documents rather than closes).
type auditFallbackDetails struct {
	Route    string            `json:"route"`
	Method   string            `json:"method"`
	Status   int               `json:"status"`
	Params   map[string]string `json:"params,omitempty"`
	AdminSub string            `json:"admin_sub,omitempty"`
}

// AuditWriteGuard is mounted on an admin write route group — adminRoute in
// router/api-v2-router.go and the internal adminGroup in
// router/internal-api-router.go. One constructor serves both mount points:
// it tells them apart by the presence of "internal_api_key_id" in context
// (set by middleware.InternalApiAuth for the internal group; nothing mounted
// ahead of adminRoute sets that key today, so its callers do not carry it).
func AuditWriteGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Registered before c.Next() (not after) so it still runs while a
		// panic from the handler unwinds through this frame — the
		// engine-level gin.CustomRecovery (cmd/server/main.go) recovers
		// above this middleware, and a defer registered after c.Next()
		// returns would not run on that path, leaking the pending entry
		// (and pinning this *gin.Context — see governance.ForgetPending's
		// doc). The fallback row and the unaudited counter are still not
		// emitted when a guarded handler panics: the code after c.Next()
		// below is skipped during the unwind, so that write leaves no audit
		// trail beyond the recovery log. The sweep is idempotent and safe to
		// run whether or not the handler's
		// own RecordAuditEvent already fired: it LoadAndDeletes, so a
		// prior successful record leaves nothing here to forget.
		defer governance.ForgetPending(c)

		c.Next()

		if !auditGuardedMethods[c.Request.Method] {
			return
		}
		if c.GetBool(governance.AuditedContextKey) {
			return
		}

		// FullPath() is set by gin's router before any group middleware on a
		// matched route runs, so this is always the registered pattern (e.g.
		// "/api/v2/admin/tenants/:id"), never the raw request path.
		route := c.FullPath()

		actorType := governance.ActorAdmin
		actorID := c.GetInt("id")
		if keyID, ok := c.Get("internal_api_key_id"); ok {
			actorType = governance.ActorSystem
			if id, ok := keyID.(int); ok {
				actorID = id
			}
		}

		details := auditFallbackDetails{
			Route:  route,
			Method: c.Request.Method,
			Status: c.Writer.Status(),
		}
		if len(c.Params) > 0 {
			params := make(map[string]string, len(c.Params))
			for _, p := range c.Params {
				params[p.Key] = p.Value
			}
			details.Params = params
		}
		if actorType == governance.ActorAdmin && actorID == 0 {
			details.AdminSub = c.GetString("admin_sub")
		}
		detailsJSON, _ := json.Marshal(details)

		governance.RecordAuditEvent(governance.NewAuditEvent(
			c, actorType, actorID,
			governance.ActionAdminWriteUnaudited, governance.ResourceRoute, 0,
			string(detailsJSON),
		))
		metrics.AdminWriteUnauditedTotal.WithLabelValues(route).Inc()
	}
}
