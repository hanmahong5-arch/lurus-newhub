package handler

import (
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"

	"github.com/gin-gonic/gin"
)

// v2 admin surface for internal API keys — Console-not-scripts for the
// internal_api_key_tenants whitelist (migration 013/021 §1), which previously
// had zero management routes: whitelisting a narrow key for a tenant meant
// hand-writing SQL. Key CREATION already exists at POST /api/api-keys
// (handler.AdminCreateApiKey, v1 session-auth) — this file does not duplicate
// it, only lists key metadata and manages the per-tenant whitelist.
//
// Routes registered under /api/v2/admin/internal-keys in api-v2-router.go.
// Auth: middleware.RootJWTAuth (adminRoute group) — same operator-only
// rationale as the v1 /api/api-keys group: even LISTING internal keys
// exposes the platform's cross-tenant trust boundary.

// internalKeySummary is a strict whitelist projection of repo.InternalApiKey.
// KeyHash is deliberately never referenced here — this struct cannot leak it
// even if a future field is added to InternalApiKey, unlike relying solely on
// json:"-" on the source struct (endUserPoolView / internal_credit_pool_fund.go
// establish this "explicit response shape" convention for admin-adjacent APIs).
type internalKeySummary struct {
	ID        int      `json:"id"`
	Name      string   `json:"name"`
	KeyPrefix string   `json:"key_prefix"`
	Scopes    []string `json:"scopes"`
	ScopeAll  bool     `json:"scope_all"`
	Enabled   bool     `json:"enabled"`
	CreatedAt int64    `json:"created_at"`
}

func toInternalKeySummary(k *repo.InternalApiKey) internalKeySummary {
	return internalKeySummary{
		ID:        k.Id,
		Name:      k.Name,
		KeyPrefix: k.KeyPrefix,
		Scopes:    k.GetScopes(),
		ScopeAll:  k.HasScope(repo.ScopeAll),
		Enabled:   k.Enabled,
		CreatedAt: k.CreatedAt,
	}
}

// ListInternalApiKeysV2 lists internal API key metadata (never hash or any
// key-reconstructing material).
// Route: GET /api/v2/admin/internal-keys
func ListInternalApiKeysV2(c *gin.Context) {
	keys, err := repo.GetAllInternalApiKeys()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to list internal API keys: " + err.Error(),
		})
		return
	}

	out := make([]internalKeySummary, 0, len(keys))
	for _, k := range keys {
		out = append(out, toInternalKeySummary(k))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}

// lookupInternalApiKeyV2 resolves the :id path param to an InternalApiKey,
// writing the 400/404 response itself and returning ok=false when the
// caller should stop. Shared by all three tenant-whitelist endpoints below.
func lookupInternalApiKeyV2(c *gin.Context) (*repo.InternalApiKey, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "invalid internal API key id",
		})
		return nil, false
	}

	key, err := repo.GetInternalApiKeyById(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "internal API key not found",
			"error_code": "KEY_NOT_FOUND",
		})
		return nil, false
	}
	return key, true
}

// ListInternalApiKeyTenantsV2 lists the tenant whitelist for one internal API
// key. Route: GET /api/v2/admin/internal-keys/:id/tenants
func ListInternalApiKeyTenantsV2(c *gin.Context) {
	key, ok := lookupInternalApiKeyV2(c)
	if !ok {
		return
	}

	grants, err := repo.ListInternalKeyTenants(key.Id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to list tenant whitelist: " + err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": grants})
}

// GrantInternalApiKeyTenantV2 whitelists a tenant for an internal API key —
// the console alternative to hand-writing an INSERT into
// internal_api_key_tenants.
// Route: POST /api/v2/admin/internal-keys/:id/tenants
// Body:  { "tenant_id": "..." }
// Idempotent: granting an already-whitelisted tenant returns 200, not 409.
func GrantInternalApiKeyTenantV2(c *gin.Context) {
	key, ok := lookupInternalApiKeyV2(c)
	if !ok {
		return
	}

	var req struct {
		TenantID string `json:"tenant_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "tenant_id is required: " + err.Error(),
		})
		return
	}

	if _, err := repo.GetTenantByID(req.TenantID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "tenant not found: " + req.TenantID,
			"error_code": "TENANT_NOT_FOUND",
		})
		return
	}

	if err := repo.GrantInternalKeyTenant(key.Id, req.TenantID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to grant tenant: " + err.Error(),
		})
		return
	}

	actorID := c.GetInt("id")
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionInternalKeyTenantGranted, governance.ResourceInternalKey, key.Id, req.TenantID))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"api_key_id": key.Id, "tenant_id": req.TenantID},
	})
}

// RevokeInternalApiKeyTenantV2 removes a tenant from an internal API key's
// whitelist.
// Route: DELETE /api/v2/admin/internal-keys/:id/tenants/:tenant_id
// Idempotent: revoking an absent grant still returns 200 — 404 is reserved
// for the key itself being unknown (checked by lookupInternalApiKeyV2).
func RevokeInternalApiKeyTenantV2(c *gin.Context) {
	key, ok := lookupInternalApiKeyV2(c)
	if !ok {
		return
	}
	tenantID := c.Param("tenant_id")

	if err := repo.RevokeInternalKeyTenant(key.Id, tenantID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to revoke tenant: " + err.Error(),
		})
		return
	}

	actorID := c.GetInt("id")
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionInternalKeyTenantRevoked, governance.ResourceInternalKey, key.Id, tenantID))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"api_key_id": key.Id, "tenant_id": tenantID},
	})
}
