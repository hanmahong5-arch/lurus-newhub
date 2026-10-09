package handler

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/app/tenantpolicy"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// Tenant-admin self-service over the tenant's model allow-list. The platform
// list (tenantpolicy.ModelAllowlistConfigKey, root-written) is a ceiling; the
// tenant's own selection (tenantpolicy.SelectionConfigKey) may only narrow it.

func tenantSelectionScope(c *gin.Context) (string, bool) {
	tc, err := middleware.GetTenantContext(c)
	if err != nil || tc == nil || tc.TenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Not authenticated", "error_code": "UNAUTHENTICATED"})
		return "", false
	}
	if !requireTenantAdmin(c, tc) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Tenant admin required", "error_code": "PERMISSION_DENIED"})
		return "", false
	}
	return tc.TenantID, true
}

func tenantSelectionView(tenantID string) (gin.H, error) {
	platform, pc, err := tenantpolicy.LoadModelAllowlistUncached(tenantID)
	if err != nil {
		return nil, err
	}
	selected, sc, err := tenantpolicy.LoadSelectionUncached(tenantID)
	if err != nil {
		return nil, err
	}
	nz := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	return gin.H{
		"platform_allowed":      nz(platform),
		"platform_unrestricted": !pc,
		"tenant_selected":       nz(selected),
		"tenant_configured":     sc,
		"effective":             tenantpolicy.Effective(platform, pc, selected, sc),
	}, nil
}

// GetTenantModelAllowlistV2: GET /api/v2/:tenant_slug/models/allowlist
func GetTenantModelAllowlistV2(c *gin.Context) {
	tenantID, ok := tenantSelectionScope(c)
	if !ok {
		return
	}
	data, err := tenantSelectionView(tenantID)
	if err != nil {
		common.SysError("tenant model selection read failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to retrieve model allow-list"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// PutTenantModelAllowlistV2: PUT /api/v2/:tenant_slug/models/allowlist
// body {"selected_models":[...]}. Every entry must be covered by the platform
// ceiling (when one is configured); [] is an explicit deny-all.
func PutTenantModelAllowlistV2(c *gin.Context) {
	tenantID, ok := tenantSelectionScope(c)
	if !ok {
		return
	}
	var req struct {
		SelectedModels *[]string `json:"selected_models"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.SelectedModels == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "selected_models is required; send [] for an explicit deny-all"})
		return
	}
	normalized, verr := validateAllowlistEntries(*req.SelectedModels)
	if verr != "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": verr})
		return
	}
	platform, pc, err := tenantpolicy.LoadModelAllowlistUncached(tenantID)
	if err != nil {
		common.SysError("tenant model selection: platform list read failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to save model allow-list"})
		return
	}
	if pc {
		for _, e := range normalized {
			if !tenantpolicy.Covered(platform, e) {
				c.JSON(http.StatusForbidden, gin.H{
					"success": false, "error_code": "MODEL_NOT_GRANTED",
					"message": "model not granted to this tenant by the platform: " + e,
				})
				return
			}
		}
	}
	if err := repo.SetTenantConfigJSON(tenantID, tenantpolicy.SelectionConfigKey, normalized, "tenant-admin model selection (narrows platform allow-list)"); err != nil {
		common.SysError("tenant model selection save failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to save model allow-list"})
		return
	}
	tenantpolicy.InvalidateSelection(tenantID)
	actorID, _ := repo.GetUserID(c)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, actorID,
		governance.ActionTenantUpdated, governance.ResourceTenant, 0, "model-selection set by tenant admin: "+tenantID))
	data, err := tenantSelectionView(tenantID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "Model allow-list saved"})
		return
	}
	data["propagation_seconds"] = 30
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Model allow-list saved", "data": data})
}
