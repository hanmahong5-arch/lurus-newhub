package handler

import (
	"fmt"
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

// GetAffinityStatsV2 reports session-affinity hit/miss/stale counters and
// which storage backend is currently live (Redis, or the bounded in-process
// fallback with its current entry count) — the same counters
// recordAffinityOutcome updates beside the existing Prometheus increment, so
// this works identically whether or not a scrape pipeline is wired up.
//
// GET /api/v2/admin/routing/affinity — root only.
func GetAffinityStatsV2(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    app.AffinityStatsSnapshot(),
	})
}

// PurgeAffinityBindingV2 removes one session-affinity binding by its
// HMAC-derived key — the value the relay call returned via the
// X-Lurus-Affinity-Key response header when it created the binding. An
// unknown key 404s rather than reporting a false "purged", so retyping a key
// from an old log line gets a clear signal.
//
// DELETE /api/v2/admin/routing/affinity/:key — root only.
func PurgeAffinityBindingV2(c *gin.Context) {
	key := c.Param("key")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "key is required"})
		return
	}

	if !app.PurgeAffinityKey(c, key) {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "affinity binding not found"})
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionRoutingAffinityPurged, governance.ResourceSessionAffinity, 0,
		fmt.Sprintf(`{"scope":"key","key":%q}`, key)))
	c.Status(http.StatusNoContent)
}

// PurgeAllAffinityBindingsV2 drops every session-affinity binding on
// whichever backend is currently live (bounded SCAN+UNLINK on Redis, a map
// reset on the in-process fallback). Requires the explicit ?all=true query
// so a bare DELETE against the collection path can never wipe every binding
// by accident.
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
		fmt.Sprintf(`{"scope":"all","purged":%d}`, purged)))
	c.Status(http.StatusNoContent)
}
