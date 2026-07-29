package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"encoding/json"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// tokenView is the field-whitelisted projection returned by ListTokensV2. It
// excludes the raw Key (the bearer secret — must never appear in a list, only
// once on create), tenant_id (implicit from route) and the platform/provisioning
// linkage IDs (identity_account_id, creator_user_id). Mirrors redemptionView.
type tokenView struct {
	Id                 int      `json:"id"`
	Name               string   `json:"name"`
	Key                string   `json:"key"` // masked (first4****last4); full key returned only on create/rotate
	Status             int      `json:"status"`
	CreatedTime        int64    `json:"created_time"`
	AccessedTime       int64    `json:"accessed_time"`
	ExpiredTime        int64    `json:"expired_time"`
	RemainQuota        int      `json:"remain_quota"`
	UsedQuota          int      `json:"used_quota"`
	UnlimitedQuota     bool     `json:"unlimited_quota"`
	ModelLimitsEnabled bool     `json:"model_limits_enabled"`
	ModelLimits        string   `json:"model_limits"`
	AllowIps           *string  `json:"allow_ips"`
	Group              string   `json:"group"`
	Scopes             []string `json:"scopes"` // empty array = no scope restriction (backward compat)
	// ProjectId is the cost-attribution label (migration 029); 0 = unassigned.
	// Safe to expose to the token owner: it is not a privilege, and the
	// project list itself is readable by every user in the tenant.
	ProjectId int `json:"project_id"`
}

func toTokenViews(tokens []*repo.Token) []tokenView {
	items := make([]tokenView, 0, len(tokens))
	for _, t := range tokens {
		scopes := t.GetScopes()
		if scopes == nil {
			scopes = []string{}
		}
		items = append(items, tokenView{
			Id:                 t.Id,
			Name:               t.Name,
			Key:                maskKey(t.Key),
			Status:             t.Status,
			CreatedTime:        t.CreatedTime,
			AccessedTime:       t.AccessedTime,
			ExpiredTime:        t.ExpiredTime,
			RemainQuota:        t.RemainQuota,
			UsedQuota:          t.UsedQuota,
			UnlimitedQuota:     t.UnlimitedQuota,
			ModelLimitsEnabled: t.ModelLimitsEnabled,
			ModelLimits:        t.ModelLimits,
			AllowIps:           t.AllowIps,
			Group:              t.Group,
			Scopes:             scopes,
			ProjectId:          t.ProjectId,
		})
	}
	return items
}

// ListTokensV2 retrieves the current user's tokens (v2 API with tenant context)
// Route: GET /api/v2/:tenant_slug/tokens
func ListTokensV2(c *gin.Context) {
	// Get tenant context from middleware
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Parse pagination parameters (match frontend: p=&size=)
	page, _ := strconv.Atoi(c.DefaultQuery("p", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	offset := (page - 1) * pageSize

	// Get tokens for the user scoped to the current tenant. Explicit
	// tenant_id filter is defence-in-depth — prevents cross-tenant leakage
	// even when TenantPlugin is not registered (e.g. hermetic tests).
	tokens, err := repo.GetUserTokensByTenant(tenantCtx.UserID, tenantCtx.TenantID, offset, pageSize)
	if err != nil {
		common.SysError("Failed to get tokens: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve tokens",
		})
		return
	}

	// Get total count (same tenant scope as the list query)
	total, _ := repo.CountUserTokensByTenant(tenantCtx.UserID, tenantCtx.TenantID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":     toTokenViews(tokens),
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// CreateTokenV2 creates a new token for the current user (v2 API with tenant context)
// Route: POST /api/v2/:tenant_slug/tokens
func CreateTokenV2(c *gin.Context) {
	// Get tenant context from middleware
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Parse request body
	var req struct {
		Name               string   `json:"name" binding:"required"`
		ExpiredTime        int64    `json:"expired_time"`         // -1 for never expires
		RemainQuota        int      `json:"remain_quota"`         // Initial quota
		UnlimitedQuota     bool     `json:"unlimited_quota"`      // Unlimited quota flag
		ModelLimitsEnabled bool     `json:"model_limits_enabled"` // Enable model limits
		ModelLimits        string   `json:"model_limits"`         // JSON string of model limits
		AllowIps           string   `json:"allow_ips"`            // Comma-separated allowed IPs
		Group              string   `json:"group"`                // Token group
		Scopes             []string `json:"scopes"`               // Relay scope allowlist (empty = unrestricted)
		RateLimitRPM       int      `json:"rate_limit_rpm"`       // Requests/min ceiling (0 = unlimited)
		RateLimitTPM       int      `json:"rate_limit_tpm"`       // Tokens/min ceiling (0 = unlimited)
		ProjectId          int      `json:"project_id"`           // Cost-attribution project (0 = unassigned)
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
			"error":   err.Error(),
		})
		return
	}

	// Validate token name
	if err := app.ValidateTokenName(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// Validate quota
	if err := app.ValidateTokenQuota(req.RemainQuota, req.UnlimitedQuota); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// Validate expiry upper bound (unit mistakes would mean "never expires")
	if err := app.ValidateTokenExpiredTime(req.ExpiredTime); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// Validate per-minute rate limits (0 = unlimited)
	if err := app.ValidateRateLimits(req.RateLimitRPM, req.RateLimitTPM); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// Normalize and validate scopes — invalid values reject with 400.
	scopesNormalized, err := app.NormalizeTokenScopes(req.Scopes)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// Cost attribution (migration 029): the project must belong to the
	// CALLER'S OWN tenant. Without this check a member could stamp their
	// spend onto another tenant's project id — there is no foreign key, so
	// the database would happily store it and the other tenant's report would
	// silently gain rows it cannot explain.
	if !validateTokenProject(c, tenantCtx.TenantID, req.ProjectId) {
		return
	}

	// Generate token key
	key, err := app.GenerateTokenKey()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// Set default values
	if req.ExpiredTime == 0 {
		req.ExpiredTime = -1 // Never expires
	}
	if req.Group == "" {
		req.Group = "default"
	}

	// Handle optional AllowIps field
	var allowIps *string
	if req.AllowIps != "" {
		allowIps = &req.AllowIps
	}

	// Create token with tenant context
	token := repo.Token{
		UserId:             tenantCtx.UserID,
		TenantId:           tenantCtx.TenantID,
		Name:               req.Name,
		Key:                key,
		CreatedTime:        common.GetTimestamp(),
		AccessedTime:       common.GetTimestamp(),
		ExpiredTime:        req.ExpiredTime,
		RemainQuota:        req.RemainQuota,
		UnlimitedQuota:     req.UnlimitedQuota,
		ModelLimitsEnabled: req.ModelLimitsEnabled,
		ModelLimits:        req.ModelLimits,
		AllowIps:           allowIps,
		Group:              req.Group,
		Scopes:             scopesNormalized,
		RateLimitRPM:       req.RateLimitRPM,
		RateLimitTPM:       req.RateLimitTPM,
		ProjectId:          req.ProjectId,
	}

	err = token.Insert()
	if err != nil {
		common.SysError("Failed to create token: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create token",
		})
		return
	}
	detailBytes, _ := json.Marshal(map[string]string{"name": token.Name})
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, tenantCtx.UserID,
		governance.ActionTokenCreated, governance.ResourceToken, token.Id, string(detailBytes)))

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "Token created successfully",
		"data": gin.H{
			"id":   token.Id,
			"name": token.Name,
			"key":  "sk-" + token.Key, // Return full key only on creation
		},
	})
}

// UpdateTokenV2 updates a token (v2 API with tenant context)
// Route: PUT /api/v2/:tenant_slug/tokens/:id
func UpdateTokenV2(c *gin.Context) {
	// Get tenant context from middleware
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Get token ID from URL
	tokenID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid token ID",
		})
		return
	}

	// Get existing token (ensures ownership)
	token, err := repo.GetTokenByIds(tokenID, tenantCtx.UserID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Token not found",
		})
		return
	}

	// Verify tenant ownership
	if token.TenantId != tenantCtx.TenantID {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Access denied",
		})
		return
	}

	// Parse request body
	var req struct {
		Name               string    `json:"name"`
		ExpiredTime        int64     `json:"expired_time"`
		RemainQuota        int       `json:"remain_quota"`
		UnlimitedQuota     *bool     `json:"unlimited_quota"`
		ModelLimitsEnabled *bool     `json:"model_limits_enabled"`
		ModelLimits        string    `json:"model_limits"`
		AllowIps           string    `json:"allow_ips"`
		Group              string    `json:"group"`
		Status             *int      `json:"status"`
		Scopes             *[]string `json:"scopes"`         // nil = no change; non-nil (incl. []) = replace allowlist
		RateLimitRPM       *int      `json:"rate_limit_rpm"` // nil = no change; 0 resets to unlimited
		RateLimitTPM       *int      `json:"rate_limit_tpm"` // nil = no change; 0 resets to unlimited
		// ProjectId: nil = no change; 0 unassigns. A pointer, not a plain int,
		// because 0 is a meaningful value here (unassign) rather than "absent".
		ProjectId *int `json:"project_id"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
			"error":   err.Error(),
		})
		return
	}

	// Update fields if provided
	if req.Name != "" {
		if err := app.ValidateTokenName(req.Name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		token.Name = req.Name
	}

	if req.ExpiredTime != 0 {
		if err := app.ValidateTokenExpiredTime(req.ExpiredTime); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		token.ExpiredTime = req.ExpiredTime
	}

	if req.UnlimitedQuota != nil {
		token.UnlimitedQuota = *req.UnlimitedQuota
	}

	if !token.UnlimitedQuota && req.RemainQuota != 0 {
		if req.RemainQuota < 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "Quota value cannot be negative",
			})
			return
		}
		token.RemainQuota = req.RemainQuota
	}

	if req.ModelLimitsEnabled != nil {
		token.ModelLimitsEnabled = *req.ModelLimitsEnabled
	}

	if req.ModelLimits != "" {
		token.ModelLimits = req.ModelLimits
	}

	if req.AllowIps != "" {
		token.AllowIps = &req.AllowIps
	}

	if req.Group != "" {
		token.Group = req.Group
	}

	if req.Status != nil {
		if *req.Status == common.TokenStatusEnabled {
			if err := app.CanEnableToken(token); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{
					"success": false,
					"message": err.Error(),
				})
				return
			}
		}
		token.Status = *req.Status
	}

	// Rate limits: nil = unchanged, explicit 0 resets to unlimited.
	if req.RateLimitRPM != nil || req.RateLimitTPM != nil {
		rpm, tpm := token.RateLimitRPM, token.RateLimitTPM
		if req.RateLimitRPM != nil {
			rpm = *req.RateLimitRPM
		}
		if req.RateLimitTPM != nil {
			tpm = *req.RateLimitTPM
		}
		if err := app.ValidateRateLimits(rpm, tpm); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		token.RateLimitRPM = rpm
		token.RateLimitTPM = tpm
	}

	// Cost attribution (migration 029): nil = unchanged, 0 = unassign, and any
	// other value must resolve inside the CALLER'S OWN tenant.
	if req.ProjectId != nil {
		if !validateTokenProject(c, tenantCtx.TenantID, *req.ProjectId) {
			return
		}
		token.ProjectId = *req.ProjectId
	}

	// Scopes replacement is opt-in: nil = unchanged, non-nil = full replace.
	// Sending [] clears the allowlist (= no restriction). Record both before/
	// after in the audit event so admins can reconstruct privilege changes.
	scopeAuditPrev := token.Scopes
	scopeAuditChanged := false
	if req.Scopes != nil {
		scopesNormalized, err := app.NormalizeTokenScopes(*req.Scopes)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		if scopesNormalized != token.Scopes {
			scopeAuditChanged = true
		}
		token.Scopes = scopesNormalized
	}

	// Save changes
	err = token.Update()
	if err != nil {
		common.SysError("Failed to update token: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to update token",
		})
		return
	}

	auditDetails := ""
	if scopeAuditChanged {
		detailBytes, _ := json.Marshal(map[string]string{
			"action":     "scope_update",
			"old_scopes": scopeAuditPrev,
			"new_scopes": token.Scopes,
		})
		auditDetails = string(detailBytes)
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, tenantCtx.UserID,
		governance.ActionTokenUpdated, governance.ResourceToken, tokenID, auditDetails))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Token updated successfully",
		"data":    token,
	})
}

// DeleteTokenV2 deletes a token (v2 API with tenant context)
// Route: DELETE /api/v2/:tenant_slug/tokens/:id
func DeleteTokenV2(c *gin.Context) {
	// Get tenant context from middleware
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Get token ID from URL
	tokenID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid token ID",
		})
		return
	}

	// Verify ownership before deletion
	token, err := repo.GetTokenByIds(tokenID, tenantCtx.UserID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Token not found",
		})
		return
	}

	// Verify tenant ownership
	if token.TenantId != tenantCtx.TenantID {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Access denied",
		})
		return
	}

	// Delete token
	err = repo.DeleteTokenById(tokenID, tenantCtx.UserID)
	if err != nil {
		common.SysError("Failed to delete token: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to delete token",
		})
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, tenantCtx.UserID,
		governance.ActionTokenDeleted, governance.ResourceToken, tokenID, ""))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Token deleted successfully",
	})
}

// maxBatchDeleteTokens bounds a single batch-delete request. Above this the
// request is rejected rather than sweeping an unbounded set in one transaction.
const maxBatchDeleteTokens = 100

// DeleteTokensV2 batch-deletes the caller's own tokens (v2 API with tenant context).
// Route: POST /api/v2/:tenant_slug/tokens/batch-delete  Body: { "ids": [int] }
//
// Ownership is enforced inside repo.BatchDeleteTokens (the delete is scoped to
// tenantCtx.UserID), so ids belonging to other users are silently ignored and
// never deleted. An empty list is a no-op (the UI may submit an empty selection).
func DeleteTokensV2(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	var req struct {
		Ids []int `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
			"error":   err.Error(),
		})
		return
	}

	// Empty selection deletes nothing — succeed with deleted:0 rather than error.
	if len(req.Ids) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true, "deleted": 0})
		return
	}

	if len(req.Ids) > maxBatchDeleteTokens {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": fmt.Sprintf("Too many ids: %d (max %d per batch)", len(req.Ids), maxBatchDeleteTokens),
		})
		return
	}

	deleted, err := repo.BatchDeleteTokens(req.Ids, tenantCtx.UserID)
	if err != nil {
		common.SysError("Failed to batch-delete tokens: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to delete tokens",
		})
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, tenantCtx.UserID,
		governance.ActionTokenDeleted, governance.ResourceToken, 0,
		fmt.Sprintf(`{"action":"batch_delete","count":%d}`, deleted)))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"deleted": deleted,
	})
}

// RotateTokenV2 generates a new key for an existing token, invalidating the old one.
// Route: POST /api/v2/:tenant_slug/tokens/:id/rotate
func RotateTokenV2(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Tenant context not found"})
		return
	}

	tokenID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid token ID"})
		return
	}

	token, err := repo.GetTokenByIds(tokenID, tenantCtx.UserID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Token not found"})
		return
	}

	if token.TenantId != tenantCtx.TenantID {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Access denied"})
		return
	}

	newKey, err := app.GenerateTokenKey()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to generate new key"})
		return
	}

	if err = token.RotateKey(newKey); err != nil {
		common.SysError("Failed to rotate token key: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to rotate token key"})
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, tenantCtx.UserID,
		governance.ActionAuthTokenRotated, governance.ResourceToken, tokenID, ""))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Token key rotated successfully",
		"data": gin.H{
			"id":  token.Id,
			"key": "sk-" + newKey,
		},
	})
}
