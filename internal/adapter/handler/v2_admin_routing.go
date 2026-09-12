package handler

import (
	"encoding/json"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"

	"github.com/gin-gonic/gin"
)

// v2_admin_routing.go — L5 (routing-resilience-limits-11, console-ux-30):
// operator visibility into and control over session-affinity bindings
// (app/session_affinity.go). Read-only stats plus two purge shapes (one
// binding by key, every binding via ?all=true). Root only (RootJWTAuth on
// the adminRoute group in api-v2-router.go).

// routingAuditDetails builds the JSON Details blob for both purge routes,
// mirroring middleware.AuditWriteGuard's convention: RootJWTAuth's
// Bearer-JWT branch never sets the "id" context key (admin_jwt_auth.go), so
// c.GetInt("id") is 0 for that path and the audit row would otherwise record
// an unattributable actor. AdminSub is only populated in that case, exactly
// like audit_write_guard.go's auditFallbackDetails.
type routingAuditDetails struct {
	Scope    string `json:"scope"`
	Key      string `json:"key,omitempty"`
	Purged   *int   `json:"purged,omitempty"`
	AdminSub string `json:"admin_sub,omitempty"`
}

func routingAuditDetailsJSON(c *gin.Context, d routingAuditDetails) string {
	if c.GetInt("id") == 0 {
		d.AdminSub = c.GetString("admin_sub")
	}
	b, _ := json.Marshal(d)
	return string(b)
}

// GetAffinityStatsV2 reports session-affinity hit/miss/stale counters and
// which storage backend is currently live (Redis, or the bounded in-process
// fallback with its current entry count) — the same counters
// recordAffinityOutcome updates beside the existing Prometheus increment, so
// this works identically whether or not a scrape pipeline is wired up.
//
// The counters and mem_entries are per-replica in-process state (see
// session_affinity.go's recordAffinityOutcome doc): production runs 3
// replicas behind one NodePort, so this GET only sees whichever replica
// answered it. The "scope":"replica" field names that explicitly rather than
// leaving an operator to discover it by comparing two GETs that disagree.
// Cluster-wide totals require reading the Prometheus counter
// lurus_gateway_session_affinity_total instead.
//
// GET /api/v2/admin/routing/affinity — root only.
func GetAffinityStatsV2(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"scope":   "replica",
		"data":    app.AffinityStatsSnapshot(),
	})
}

// PurgeAffinityBindingV2 removes one session-affinity binding by its
// HMAC-derived key — the value the relay call returned via the
// X-Lurus-Affinity-Key response header when it created the binding.
//
// Three outcomes: 204 when a binding existed and was removed; 404 when
// app.PurgeAffinityKey positively confirmed no such binding exists (an
// operator retyping a stale key gets a clear signal); 500 when the backend
// itself failed (e.g. Redis is down) — that case must never be folded into
// 404, or an operator would read a backend outage as "the pin is already
// gone" (L5 repair, finding routing-resilience-limits-11#6/#20/#47).
//
// DELETE /api/v2/admin/routing/affinity/:key — root only.
func PurgeAffinityBindingV2(c *gin.Context) {
	// gin's :key route parameter never matches an empty path segment, so
	// key=="" cannot reach this handler; no bad-request branch is needed
	// here (L5 repair, finding routing-resilience-limits-11#9/#12).
	key := c.Param("key")

	found, err := app.PurgeAffinityKey(c, key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "affinity purge failed: " + err.Error()})
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "affinity binding not found"})
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionRoutingAffinityPurged, governance.ResourceSessionAffinity, 0,
		routingAuditDetailsJSON(c, routingAuditDetails{Scope: "key", Key: key})))
	c.Status(http.StatusNoContent)
}

// PurgeAllAffinityBindingsV2 drops every session-affinity binding on
// whichever backend is currently live (bounded SCAN+UNLINK on Redis, a map
// reset on the in-process fallback). Requires the explicit ?all=true query
// so a bare DELETE against the collection path can never wipe every binding
// by accident. Responds 200 with {"purged":n} so an operator sees how many
// pins were actually dropped without having to go find the audit row (L5
// repair, finding routing-resilience-limits-11#5/#21).
//
// DELETE /api/v2/admin/routing/affinity?all=true — root only.
func PurgeAllAffinityBindingsV2(c *gin.Context) {
	if c.Query("all") != "true" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "purging every binding requires ?all=true; to purge one binding use DELETE /api/v2/admin/routing/affinity/:key",
		})
		return
	}

	purged, err := app.PurgeAllAffinity(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionRoutingAffinityPurged, governance.ResourceSessionAffinity, 0,
		routingAuditDetailsJSON(c, routingAuditDetails{Scope: "all", Purged: &purged})))
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"purged": purged}})
}
