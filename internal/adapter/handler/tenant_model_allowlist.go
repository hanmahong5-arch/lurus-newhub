package handler

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/app/tenantpolicy"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// Root-only admin surface for the per-tenant model allow-list
// (internal/app/tenantpolicy). This is the SOLE writer of the
// tenant_configs "models.allowlist" row — the earlier free-form
// GetTenantConfigs/UpdateTenantConfig handlers were never routed and have
// been deleted, so a tenant's allow-list can only be set through the
// validating endpoints below.
//
// maxAllowlistEntries / maxAllowlistEntryBytes bound the PUT body: an
// unbounded list would let an admin request grow the tenant_configs row (and
// the per-request ModelAllowed scan) without limit.
const (
	maxAllowlistEntries    = 256
	maxAllowlistEntryBytes = 128
)

// ListTenantModelAllowlist reads the tenant's current allow-list.
// Route: GET /api/v2/admin/tenants/:id/model-allowlist
func ListTenantModelAllowlist(c *gin.Context) {
	tenantID := c.Param("id")
	if !requireModelLimitTenant(c, tenantID) {
		return
	}

	// Uncached: on the 3-replica deployment a GET served by a different
	// replica than the most recent PUT must not echo a stale pre-write list
	// back to the operator for up to the cache TTL.
	list, configured, err := tenantpolicy.LoadModelAllowlistUncached(tenantID)
	if err != nil {
		common.SysError("Failed to load tenant model allow-list: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve model allow-list",
		})
		return
	}
	if list == nil {
		list = []string{}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"configured":     configured,
			"allowed_models": list,
			"mode":           tenantpolicy.Mode(),
		},
	})
}

// UpsertTenantModelAllowlist replaces the tenant's allow-list wholesale.
// An empty list ("allowed_models":[]) is a valid, explicit deny-all — it is
// NOT the same as never calling this endpoint (which leaves the tenant
// unrestricted).
// Route: PUT /api/v2/admin/tenants/:id/model-allowlist
func UpsertTenantModelAllowlist(c *gin.Context) {
	tenantID := c.Param("id")

	// AllowedModels is a pointer so a missing/null field is distinguishable
	// from an explicit []: {} and {"allowed_models":null} bind to a nil
	// pointer and are rejected with 400 below, instead of silently writing
	// an explicit deny-all row (an admin typo must not become a tenant-wide
	// outage under enforce).
	var req struct {
		AllowedModels *[]string `json:"allowed_models"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
			"error":   err.Error(),
		})
		return
	}
	if req.AllowedModels == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "allowed_models is required; send [] for an explicit deny-all",
		})
		return
	}

	normalized, verr := validateAllowlistEntries(*req.AllowedModels)
	if verr != "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": verr,
		})
		return
	}

	if !requireModelLimitTenant(c, tenantID) {
		return
	}

	if err := repo.SetTenantConfigJSON(tenantID, tenantpolicy.ModelAllowlistConfigKey, normalized, "per-tenant relay model allow-list"); err != nil {
		common.SysError("Failed to save tenant model allow-list: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to save model allow-list",
		})
		return
	}
	tenantpolicy.Invalidate(tenantID)

	actorID, _ := repo.GetUserID(c)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionTenantUpdated, governance.ResourceTenant, 0, "model-allowlist set: "+tenantID))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Model allow-list saved",
		"data": gin.H{
			"configured":          true,
			"allowed_models":      normalized,
			"mode":                tenantpolicy.Mode(),
			"propagation_seconds": 30,
		},
	})
}

// DeleteTenantModelAllowlist removes the allow-list row entirely, returning
// the tenant to unrestricted.
// Route: DELETE /api/v2/admin/tenants/:id/model-allowlist
func DeleteTenantModelAllowlist(c *gin.Context) {
	tenantID := c.Param("id")
	if !requireModelLimitTenant(c, tenantID) {
		return
	}

	if err := repo.DeleteTenantConfig(tenantID, tenantpolicy.ModelAllowlistConfigKey); err != nil {
		common.SysError("Failed to delete tenant model allow-list: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to delete model allow-list",
		})
		return
	}
	tenantpolicy.Invalidate(tenantID)

	actorID, _ := repo.GetUserID(c)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionTenantUpdated, governance.ResourceTenant, 0, "model-allowlist deleted: "+tenantID))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Model allow-list deleted",
		"data": gin.H{
			"configured":     false,
			"allowed_models": []string{},
		},
	})
}

// validateAllowlistEntries trims, rejects empties/duplicates/oversize
// entries and the oversize list itself, and returns the normalised (trimmed)
// list, or a non-empty message on the first violation. "*" is honoured only
// as a trailing wildcard (matching tenantpolicy.ModelAllowed, which only
// implements a trailing "*"); an entry with "*" anywhere else would silently
// never match and is rejected here instead.
func validateAllowlistEntries(entries []string) ([]string, string) {
	if len(entries) > maxAllowlistEntries {
		return nil, "allowed_models exceeds the maximum of 256 entries"
	}
	seen := make(map[string]bool, len(entries))
	out := make([]string, 0, len(entries))
	for i, raw := range entries {
		e := strings.TrimSpace(raw)
		if e == "" {
			return nil, "allowed_models entries must not be blank"
		}
		if len(e) > maxAllowlistEntryBytes {
			// The offending entry is not echoed back: the length bound
			// exists precisely because the entry may be too large to put
			// in an error body.
			return nil, fmt.Sprintf("allowed_models entry #%d exceeds the maximum of 128 bytes", i+1)
		}
		if idx := strings.IndexByte(e, '*'); idx >= 0 && idx != len(e)-1 {
			return nil, "allowed_models entry may use * only as a trailing wildcard: " + e
		}
		if seen[e] {
			return nil, "allowed_models contains a duplicate entry: " + e
		}
		seen[e] = true
		out = append(out, e)
	}
	return out, ""
}
