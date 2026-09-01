package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
)

// CreateProvisionedKey issues a Token row tied to a Reseller-owned tenant.
// Route: POST /internal/v1/provisioning/tenants/:slug/keys
// Auth:  X-API-Key + scope "provisioning".
//
// Returns 201 with the plaintext token key (one-time view, per existing
// /internal/token convention). Sets tokens.creator_user_id to the API key's
// CreatedBy (Reseller's user.id) so the Token list "Created by" column can
// render it. last_used_at = 0 (never used yet).
//
// Canonical: ADR 2026-05-18 (tenant-credit-pool) §4.2.
func CreateProvisionedKey(c *gin.Context) {
	slug := c.Param("slug")
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "tenant slug required",
		})
		return
	}

	tenant, err := repo.GetTenantBySlug(slug)
	if err != nil || tenant == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Tenant not found",
			"error_code": "TENANT_NOT_FOUND",
		})
		return
	}

	var req struct {
		Name           string `json:"name" binding:"required"`
		UnlimitedQuota bool   `json:"unlimited_quota"`
		RemainQuota    int    `json:"remain_quota"`
		Group          string `json:"group"`
		ExpiresAt      int64  `json:"expires_at"` // unix seconds; 0 or negative = no expiry
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	keyInterface, _ := c.Get("internal_api_key")
	apiKey, _ := keyInterface.(*repo.InternalApiKey)
	creatorUserID := 0
	if apiKey != nil {
		creatorUserID = apiKey.CreatedBy
	}

	// SECURITY (Phase 2 self-audit 2026-05-19): prevent cross-tenant key
	// issuance. ScopeProvisioning by itself did not bound which tenants the
	// key could write to; a leaked or misissued Reseller key could mint
	// tokens for any tenant slug. Platform admin keys (ScopeAll = "*")
	// bypass the check — they're already trusted cross-tenant. Narrower
	// keys must have an (api_key_id, tenant_id) row in
	// internal_api_key_tenants; missing row → 403 (fail-closed).
	if !repo.InternalKeyAllowedForTenant(apiKey, tenant.Id) {
		keyID := 0
		if apiKey != nil {
			keyID = apiKey.Id
		}
		common.SysLog(fmt.Sprintf("Provisioning: cross-tenant denied key=%d tenant=%s creator=%d",
			keyID, tenant.Id, creatorUserID))
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "API key not authorized for this tenant",
			"error_code": "TENANT_NOT_AUTHORIZED",
		})
		return
	}

	tokenKey, err := app.GenerateTokenKey()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to generate token key: " + err.Error(),
		})
		return
	}

	expired := int64(-1)
	if req.ExpiresAt > 0 {
		expired = req.ExpiresAt
	}
	group := req.Group
	if group == "" {
		group = "default"
	}

	token := &repo.Token{
		UserId:         0, // Provisioned keys are tenant-scoped, not user-scoped
		TenantId:       tenant.Id,
		Name:           req.Name,
		Key:            tokenKey,
		UnlimitedQuota: req.UnlimitedQuota,
		RemainQuota:    req.RemainQuota,
		CreatedTime:    time.Now().Unix(),
		AccessedTime:   time.Now().Unix(),
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    expired,
		Group:          group,
		CreatorUserId:  creatorUserID,
		LastUsedAt:     0,
	}

	if err := token.Insert(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create provisioned token: " + err.Error(),
		})
		return
	}

	metrics.ProvisioningKeysCreatedTotal.WithLabelValues(tenant.Id).Inc()
	keyName := c.GetString("internal_api_key_name")
	common.SysLog("Provisioning: created token id=" + strconv.Itoa(token.Id) +
		" tenant=" + tenant.Id + " creator=" + strconv.Itoa(creatorUserID) +
		" via key: " + keyName)

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data": gin.H{
			"id":         token.Id,
			"key":        tokenKey,
			"name":       token.Name,
			"tenant_id":  tenant.Id,
			"expires_at": expired,
			"warning":    "Please save this key - it will not be shown again.",
		},
	})
}

// ListProvisionedKeys returns Provisioning-issued tokens for a tenant, scoped
// to the actor key's creator user. Route:
//
//	GET /internal/v1/provisioning/tenants/:slug/keys
//	    ?limit=20&offset=0&include_revoked=false
//
// Tier 1.1 (2026-05-19): customers using the Provisioning API had no way to
// recover the list of keys they had previously issued — only the one-time
// plaintext view from Create. This endpoint closes that gap.
//
// Response fields are an explicit whitelist (id / name / status / quota /
// expires_at / created_at / last_used_at / created_by_user_id). The raw
// key, key_hash, and model_limits payload are never serialised so the
// LIST output is safe to log and forward.
func ListProvisionedKeys(c *gin.Context) {
	slug := c.Param("slug")
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "tenant slug required",
			"error_code": "INVALID_TENANT_SLUG",
		})
		return
	}

	tenant, err := repo.GetTenantBySlug(slug)
	if err != nil || tenant == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Tenant not found",
			"error_code": "TENANT_NOT_FOUND",
		})
		return
	}

	keyInterface, _ := c.Get("internal_api_key")
	apiKey, _ := keyInterface.(*repo.InternalApiKey)

	// SECURITY: same tenant guard as CreateProvisionedKey — narrow keys must
	// be whitelisted for the tenant, ScopeAll bypasses. Without this check a
	// leaked Reseller key could enumerate sibling tenants' provisioned keys.
	if !repo.InternalKeyAllowedForTenant(apiKey, tenant.Id) {
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "API key not authorized for this tenant",
			"error_code": "TENANT_NOT_AUTHORIZED",
		})
		return
	}

	// Pagination bounds: reject limit < 1 outright (catches ?limit=0 which
	// would silently return nothing); clamp values > 100 to 100.
	limitRaw := c.DefaultQuery("limit", "20")
	limit, err := strconv.Atoi(limitRaw)
	if err != nil || limit < 1 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "limit must be an integer between 1 and 100",
			"error_code": "INVALID_PAGINATION",
		})
		return
	}
	if limit > 100 {
		limit = 100
	}

	offsetRaw := c.DefaultQuery("offset", "0")
	offset, err := strconv.Atoi(offsetRaw)
	if err != nil || offset < 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "offset must be a non-negative integer",
			"error_code": "INVALID_PAGINATION",
		})
		return
	}

	includeRevoked := c.Query("include_revoked") == "true"

	// LIST reach must stay symmetric with revoke reach: RevokeProvisionedKey
	// skips the creator check for ScopeAll, so a ScopeAll (platform-admin)
	// key lists every creator's provisioned keys here — otherwise it could
	// revoke keys it cannot see. Narrow Reseller keys stay scoped to their
	// own creator identity.
	creatorUserID := 0
	if apiKey != nil {
		if apiKey.HasScope(repo.ScopeAll) {
			creatorUserID = repo.ProvisionedAllCreators
		} else {
			creatorUserID = apiKey.CreatedBy
		}
	}

	tokens, err := repo.GetProvisionedTokensByTenant(creatorUserID, tenant.Id, includeRevoked, offset, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to list provisioned tokens: " + err.Error(),
		})
		return
	}

	total, err := repo.CountProvisionedTokensByTenant(creatorUserID, tenant.Id, includeRevoked)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to count provisioned tokens: " + err.Error(),
		})
		return
	}

	items := make([]gin.H, 0, len(tokens))
	for _, t := range tokens {
		items = append(items, gin.H{
			"id":                 t.Id,
			"name":               t.Name,
			"status":             t.Status,
			"unlimited_quota":    t.UnlimitedQuota,
			"remain_quota":       t.RemainQuota,
			"used_quota":         t.UsedQuota,
			"group":              t.Group,
			"expires_at":         t.ExpiredTime,
			"created_at":         t.CreatedTime,
			"last_used_at":       t.LastUsedAt,
			"created_by_user_id": t.CreatorUserId,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":  items,
			"total":  total,
			"limit":  limit,
			"offset": offset,
		},
	})
}

// RevokeProvisionedKey soft-deletes a Token row issued via the Provisioning
// API. Route: DELETE /internal/v1/provisioning/tenants/:slug/keys/:key_id.
//
// 404 when the token does not exist or belongs to a different tenant. This
// prevents one Reseller from revoking another Reseller's keys via spoofed slugs.
// 403 when the calling API key is not whitelisted for the addressed tenant.
func RevokeProvisionedKey(c *gin.Context) {
	slug := c.Param("slug")
	keyIDStr := c.Param("key_id")
	keyID, err := strconv.Atoi(keyIDStr)
	if err != nil || keyID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid key_id",
		})
		return
	}

	tenant, err := repo.GetTenantBySlug(slug)
	if err != nil || tenant == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Tenant not found",
			"error_code": "TENANT_NOT_FOUND",
		})
		return
	}

	keyInterface, _ := c.Get("internal_api_key")
	apiKey, _ := keyInterface.(*repo.InternalApiKey)

	// SECURITY: same tenant guard as CreateProvisionedKey / ListProvisionedKeys.
	// The token.TenantId check below only proves the key_id belongs to the slug
	// in the URL — it does not bound which tenants the calling key may act on.
	// Without this gate a narrow Reseller key whitelisted for tenant A could
	// enumerate key_ids and revoke live tokens under tenant B. ScopeAll ("*")
	// bypasses; missing whitelist row → 403 (fail-closed).
	if !repo.InternalKeyAllowedForTenant(apiKey, tenant.Id) {
		apiKeyID := 0
		if apiKey != nil {
			apiKeyID = apiKey.Id
		}
		common.SysLog(fmt.Sprintf("Provisioning: cross-tenant revoke denied key=%d tenant=%s",
			apiKeyID, tenant.Id))
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "API key not authorized for this tenant",
			"error_code": "TENANT_NOT_AUTHORIZED",
		})
		return
	}

	token, err := repo.GetTokenById(keyID)
	if err != nil || token == nil || token.TenantId != tenant.Id {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Provisioned key not found in this tenant",
			"error_code": "KEY_NOT_FOUND",
		})
		return
	}

	// SECURITY (revoke/list scoping asymmetry): ListProvisionedKeys scopes
	// its query to the caller's creator identity (creator_user_id =
	// apiKey.CreatedBy — see GetProvisionedTokensByTenant), so a narrow
	// Reseller key only ever sees the keys it issued. Without the same check
	// here, that same narrow key could revoke ANY key in a whitelisted
	// tenant, including ones issued by a different Reseller sharing that
	// tenant. ScopeAll keys keep the same bypass they get on the tenant
	// whitelist above — a platform admin key is already trusted to act on
	// any creator's keys. 404 (not 403) to match the tenant-mismatch branch
	// above and avoid confirming the key_id exists under a foreign creator.
	if apiKey == nil || !apiKey.HasScope(repo.ScopeAll) {
		creatorUserID := 0
		if apiKey != nil {
			creatorUserID = apiKey.CreatedBy
		}
		if token.CreatorUserId != creatorUserID {
			c.JSON(http.StatusNotFound, gin.H{
				"success":    false,
				"message":    "Provisioned key not found in this tenant",
				"error_code": "KEY_NOT_FOUND",
			})
			return
		}
	}

	if err := token.Delete(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to revoke key: " + err.Error(),
		})
		return
	}

	keyName := c.GetString("internal_api_key_name")
	common.SysLog("Provisioning: revoked token id=" + strconv.Itoa(keyID) +
		" tenant=" + tenant.Id + " via key: " + keyName)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Key revoked",
	})
}
