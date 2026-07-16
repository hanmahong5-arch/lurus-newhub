package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// provisionedRedemptionMaxCount bounds one distributor batch. 500 is the
// contract agreed with the platform business side (larger drops are split
// into multiple events on their side).
const provisionedRedemptionMaxCount = 500

// defaultRedemptionNamePrefix is used when the caller omits name_prefix; the
// per-code name becomes "dist-1", "dist-2", ...
const defaultRedemptionNamePrefix = "dist"

// InternalProvisionRedemptions is the platform distributor batch-issuance
// endpoint: it mints `count` redemption codes for the tenant, atomically and
// idempotently keyed by event_id.
//
// Route:  POST /internal/v1/provisioning/tenants/:slug/redemptions
// Scope:  provisioning (same group + auth middleware as the key-issuance
//
//	endpoints; narrow keys must additionally be whitelisted for the
//	tenant in internal_api_key_tenants — same fail-closed guard as
//	CreateProvisionedKey).
//
// Idempotency: event_id is stored with a UNIQUE constraint in
// provisioned_redemption_batches (migration 027). A replayed call returns the
// ORIGINAL codes with HTTP 200 + data.replayed=true — no second batch is
// minted. The whole batch (ledger row + all redemption rows) commits or rolls
// back as one transaction.
//
// Request body (JSON):
//
//	{
//	  "event_id":       "<platform event UUID>",   // required, idempotency key
//	  "count":          50,                        // required, 1..500
//	  "quota_per_code": 500000,                    // required, > 0 (quota units per code)
//	  "name_prefix":    "campaign-x",              // optional, default "dist"; codes named "<prefix>-<n>"
//	  "expires_at":     "2026-12-31T23:59:59Z"     // optional RFC3339; omitted/empty = never expires
//	}
//
// Response 200 (first write or replay), same shell as credit-pool/fund:
//
//	{
//	  "success": true,
//	  "data": {
//	    "tenant_id": "...",
//	    "event_id":  "...",
//	    "replayed":  false,        // true on idempotent replay
//	    "batch_id":  12,
//	    "codes":     ["<32-char code>", ...]
//	  }
//	}
func InternalProvisionRedemptions(c *gin.Context) {
	slug := c.Param("slug")
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "tenant slug is required: provide slug in the URL path",
			"error_code": "MISSING_TENANT_SLUG",
		})
		return
	}

	tenant, err := repo.GetTenantBySlug(slug)
	if err != nil || tenant == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "tenant not found: verify the slug matches an active tenant in newhub",
			"error_code": "TENANT_NOT_FOUND",
		})
		return
	}

	var req struct {
		EventID      string `json:"event_id"`
		Count        int    `json:"count"`
		QuotaPerCode int64  `json:"quota_per_code"`
		NamePrefix   string `json:"name_prefix"`
		ExpiresAt    string `json:"expires_at"` // RFC3339; empty = never expires
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "invalid request body: " + err.Error(),
			"error_code": "INVALID_REQUEST",
		})
		return
	}

	if req.EventID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "event_id is required: pass the platform event UUID for idempotency",
			"error_code": "MISSING_EVENT_ID",
		})
		return
	}
	if req.Count < 1 || req.Count > provisionedRedemptionMaxCount {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "count must be between 1 and " +
				strconv.Itoa(provisionedRedemptionMaxCount) +
				"; received: " + strconv.Itoa(req.Count),
			"error_code": "INVALID_COUNT",
		})
		return
	}
	if req.QuotaPerCode <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "quota_per_code must be a positive integer (quota units); received: " + formatInt64(req.QuotaPerCode),
			"error_code": "INVALID_QUOTA_PER_CODE",
		})
		return
	}

	expiredTime := int64(0) // 0 = never expires (redemptions.expired_time convention)
	if req.ExpiresAt != "" {
		parsed, parseErr := time.Parse(time.RFC3339, req.ExpiresAt)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success":    false,
				"message":    "expires_at must be RFC3339 (e.g. 2026-12-31T23:59:59Z): " + parseErr.Error(),
				"error_code": "INVALID_EXPIRES_AT",
			})
			return
		}
		expiredTime = parsed.Unix()
		if expiredTime < common.GetTimestamp() {
			c.JSON(http.StatusBadRequest, gin.H{
				"success":    false,
				"message":    "expires_at must be in the future",
				"error_code": "INVALID_EXPIRES_AT",
			})
			return
		}
	}

	namePrefix := req.NamePrefix
	if namePrefix == "" {
		namePrefix = defaultRedemptionNamePrefix
	}

	// SECURITY: same tenant guard as CreateProvisionedKey — narrow provisioning
	// keys must be whitelisted for the tenant, ScopeAll bypasses (fail-closed).
	keyInterface, _ := c.Get("internal_api_key")
	apiKey, _ := keyInterface.(*repo.InternalApiKey)
	if !repo.InternalKeyAllowedForTenant(apiKey, tenant.Id) {
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "API key not authorized for this tenant",
			"error_code": "TENANT_NOT_AUTHORIZED",
		})
		return
	}
	creatorUserID := 0
	if apiKey != nil {
		creatorUserID = apiKey.CreatedBy
	}

	batch, codes, provErr := repo.ProvisionRedemptionBatchIdempotent(
		c.Request.Context(),
		tenant.Id, req.EventID,
		req.Count, req.QuotaPerCode,
		namePrefix, expiredTime,
		"platform-distributor", creatorUserID,
	)

	replayed := errors.Is(provErr, repo.ErrRedemptionBatchExists)
	if provErr != nil && !replayed {
		common.SysError("InternalProvisionRedemptions: batch failed" +
			" tenant=" + tenant.Id +
			" event_id=" + req.EventID +
			" count=" + strconv.Itoa(req.Count) +
			" err=" + provErr.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "redemption batch issuance failed: " + provErr.Error(),
			"error_code": "PROVISION_FAILED",
		})
		return
	}

	if replayed {
		common.SysLog("InternalProvisionRedemptions: replayed" +
			" tenant=" + tenant.Id +
			" event_id=" + req.EventID +
			" batch_id=" + formatInt64(batch.ID))
	} else {
		common.SysLog("InternalProvisionRedemptions: issued" +
			" tenant=" + tenant.Id +
			" event_id=" + req.EventID +
			" batch_id=" + formatInt64(batch.ID) +
			" count=" + strconv.Itoa(req.Count) +
			" quota_per_code=" + formatInt64(req.QuotaPerCode))
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"tenant_id": tenant.Id,
			"event_id":  batch.EventID,
			"replayed":  replayed,
			"batch_id":  batch.ID,
			"codes":     codes,
		},
	})
}

// InternalRevokeProvisionedRedemptions disables every still-unused code of a
// previously issued distributor batch. Used codes are untouched; revoking an
// already-revoked batch is a no-op returning revoked_count=0 (naturally
// idempotent).
//
// Route:  POST /internal/v1/provisioning/tenants/:slug/redemptions/revoke
// Scope:  provisioning (same group + tenant allowlist guard as issuance).
//
// Request body (JSON):
//
//	{
//	  "event_id": "<event UUID of the batch to revoke>",  // required
//	  "reason":   "distributor contract terminated"       // optional, logged only
//	}
//
// Response 200:
//
//	{ "success": true, "data": { "tenant_id": "...", "event_id": "...", "revoked_count": 42 } }
func InternalRevokeProvisionedRedemptions(c *gin.Context) {
	slug := c.Param("slug")
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "tenant slug is required: provide slug in the URL path",
			"error_code": "MISSING_TENANT_SLUG",
		})
		return
	}

	tenant, err := repo.GetTenantBySlug(slug)
	if err != nil || tenant == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "tenant not found: verify the slug matches an active tenant in newhub",
			"error_code": "TENANT_NOT_FOUND",
		})
		return
	}

	var req struct {
		EventID string `json:"event_id"`
		Reason  string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "invalid request body: " + err.Error(),
			"error_code": "INVALID_REQUEST",
		})
		return
	}
	if req.EventID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "event_id is required: identify the batch to revoke",
			"error_code": "MISSING_EVENT_ID",
		})
		return
	}

	// SECURITY: same tenant guard as issuance (fail-closed for narrow keys).
	keyInterface, _ := c.Get("internal_api_key")
	apiKey, _ := keyInterface.(*repo.InternalApiKey)
	if !repo.InternalKeyAllowedForTenant(apiKey, tenant.Id) {
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "API key not authorized for this tenant",
			"error_code": "TENANT_NOT_AUTHORIZED",
		})
		return
	}

	revoked, revokeErr := repo.RevokeProvisionedRedemptionBatch(c.Request.Context(), tenant.Id, req.EventID)
	if revokeErr != nil {
		if errors.Is(revokeErr, repo.ErrRedemptionBatchNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"success":    false,
				"message":    "no provisioned redemption batch with this event_id for tenant '" + slug + "'",
				"error_code": "BATCH_NOT_FOUND",
			})
			return
		}
		common.SysError("InternalRevokeProvisionedRedemptions: revoke failed" +
			" tenant=" + tenant.Id +
			" event_id=" + req.EventID +
			" err=" + revokeErr.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "redemption batch revoke failed: " + revokeErr.Error(),
			"error_code": "REVOKE_FAILED",
		})
		return
	}

	common.SysLog("InternalRevokeProvisionedRedemptions: revoked" +
		" tenant=" + tenant.Id +
		" event_id=" + req.EventID +
		" revoked_count=" + formatInt64(revoked) +
		" reason=" + req.Reason)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"tenant_id":     tenant.Id,
			"event_id":      req.EventID,
			"revoked_count": revoked,
		},
	})
}
