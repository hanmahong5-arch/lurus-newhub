package middleware

// audit_write_guard.go — L2 audit-completeness fail-closed backstop.
//
// governance.RecordAuditEvent is called by hand at 28 sites across the
// handler package (grep -rl on internal/adapter/handler/*.go); nothing
// enforced that an admin write route actually reaches one of them, so a
// forgotten call was invisible until a compliance export went looking for
// it — L1's pricing route was the live proof (it shipped with zero audit
// calls for months). AuditWriteGuard closes that gap structurally: mounted
// on an admin write route group, it runs the handler and then checks whether
// governance.RecordAuditEvent actually fired during this request
// (governance.AuditedContextKey, set by the persisting call itself — see
// governance/audit.go). If a mutating method completed without that flag, it
// records a typed admin.write_unaudited fallback event and increments
// metrics.AdminWriteUnauditedTotal.
//
// It fires on every response status, not only 2xx: a write the handler
// rejected (403/409/…) is still a write attempt, and dropping it from the
// audit trail just because it failed would hide exactly the events a
// security reviewer cares most about.

import (
	"encoding/json"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
)

// auditGuardedMethods is the set of HTTP methods AuditWriteGuard treats as
// mutating. GET/HEAD/OPTIONS never reach the fallback path — a read that
// forgets to audit is not a governance gap.
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
	Route    string `json:"route"`
	Method   string `json:"method"`
	Status   int    `json:"status"`
	AdminSub string `json:"admin_sub,omitempty"`
}

// AuditWriteGuard is mounted on an admin write route group — adminRoute in
// router/api-v2-router.go and the internal adminGroup in
// router/internal-api-router.go. One constructor serves both mount points:
// it tells them apart by the presence of "internal_api_key_id" in context
// (set by middleware.InternalApiAuth for the internal group; adminRoute
// callers never carry that key).
func AuditWriteGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if !auditGuardedMethods[c.Request.Method] {
			return
		}
		if c.GetBool(governance.AuditedContextKey) {
			return
		}

		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}

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
