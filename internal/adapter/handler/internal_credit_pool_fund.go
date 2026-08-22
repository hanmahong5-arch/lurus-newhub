package handler

import (
	"errors"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
)

// InternalFundCreditPool is the platform BillingOutbox supply endpoint for
// newhub tenant credit pools (SEAM S1, model (b), ADR 2026-05-30 D3).
//
// Platform calls this after a subscription.activated event so that the tenant
// can spend LLM quota via the relay gate. Without this call the pool stays
// empty and the gateway returns 402 even though the customer paid.
//
// Route:  POST /internal/v1/provisioning/tenants/:slug/credit-pool/fund
// Scope:  balance:write  (reuse — platform's internal key already carries this
//         scope for wallet operations; adding a bespoke scope would require a
//         key rotation with no security benefit because the caller set is the
//         same: lurus-platform internal API key)
// Auth:   X-API-Key + middleware.RequireScope(repo.ScopeBalanceWrite)
//         (applied in internal-api-router.go), AND a tenant-scope check:
//         the key must carry repo.ScopeAll, OR have a row in
//         internal_api_key_tenants for (api_key_id, tenant.Id). A key
//         missing that row gets 403 TENANT_NOT_AUTHORIZED — see the
//         Response 403 case below for how to unlock it.
//
// Idempotency: event_id in the request body is stored with a UNIQUE constraint
// in credit_pool_fund_events (migration 019). A replayed call returns the first
// result with HTTP 200 — not an error. The caller (platform BillingOutbox) can
// safely retry on network failure.
//
// Request body (JSON):
//
//	{
//	  "event_id":  "<BillingOutbox event UUID>",  // idempotency key
//	  "amount":    12000,                          // integer, same unit as pool.current_balance (quota units)
//	  "source":    "platform-billing-outbox",      // caller label, stored verbatim
//	  "account_id": 100                            // platform account_id, informational for reconciliation
//	}
//
// Response 200 (first write or replay):
//
//	{
//	  "success": true,
//	  "data": {
//	    "tenant_id":   "...",
//	    "new_balance": 12000,
//	    "event_id":    "...",
//	    "replayed":    false   // true on idempotent replay
//	  }
//	}
//
// Response 403 (TENANT_NOT_AUTHORIZED): the key is not ScopeAll and has no
// internal_api_key_tenants row for this tenant — see Auth above for how to
// unlock it (whitelist the key for the tenant, or use a ScopeAll key).
//
//	{
//	  "success": false,
//	  "message": "API key not authorized for this tenant: ...",
//	  "error_code": "TENANT_NOT_AUTHORIZED"
//	}
func InternalFundCreditPool(c *gin.Context) {
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

	// SECURITY: same tenant guard as CreateProvisionedKey / InternalProvisionRedemptions —
	// narrow provisioning keys must be whitelisted for the tenant, ScopeAll bypasses (fail-closed).
	keyInterface, _ := c.Get("internal_api_key")
	apiKey, _ := keyInterface.(*repo.InternalApiKey)
	if !repo.InternalKeyAllowedForTenant(apiKey, tenant.Id) {
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "API key not authorized for this tenant: insert (api_key_id, tenant_id) into internal_api_key_tenants, or use a ScopeAll key",
			"error_code": "TENANT_NOT_AUTHORIZED",
		})
		return
	}

	var req struct {
		EventID   string `json:"event_id"`
		Amount    int64  `json:"amount"`
		Source    string `json:"source"`
		AccountID int64  `json:"account_id"` // informational; stored for reconciliation
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
			"message":    "event_id is required: pass the BillingOutbox event UUID for idempotency",
			"error_code": "MISSING_EVENT_ID",
		})
		return
	}
	if req.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "amount must be a positive integer (quota units); received: " + formatInt64(req.Amount),
			"error_code": "INVALID_AMOUNT",
		})
		return
	}
	if req.Source == "" {
		req.Source = "platform-billing-outbox"
	}

	pool, poolErr := repo.GetTenantCreditPool(tenant.Id)
	if poolErr != nil {
		if errors.Is(poolErr, repo.ErrPoolNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"success": false,
				"message": "credit pool not configured for tenant '" + slug +
					"': create the pool via POST /api/v2/admin/tenants/:id/credit-pool before funding",
				"error_code": "POOL_NOT_FOUND",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "failed to read credit pool: " + poolErr.Error(),
			"error_code": "POOL_READ_FAILED",
		})
		return
	}

	// Actor ID 0 signals a system/platform operation in the draws ledger.
	const systemActorID = 0

	event, fundErr := repo.FundPoolIdempotent(
		c.Request.Context(),
		pool.ID, tenant.Id, req.Amount,
		req.EventID, req.Source, systemActorID,
	)

	replayed := errors.Is(fundErr, repo.ErrFundEventExists)
	if fundErr != nil && !replayed {
		// Real error — ceiling exceeded or DB problem.
		if errors.Is(fundErr, repo.ErrPoolWouldExceedCeiling) {
			c.JSON(http.StatusConflict, gin.H{
				"success": false,
				"message": "fund amount would exceed pool max_balance ceiling: " +
					"reduce amount or raise max_balance on the pool before retrying",
				"error_code": "POOL_CEILING_EXCEEDED",
			})
			return
		}
		common.SysError("InternalFundCreditPool: fund failed" +
			" tenant=" + tenant.Id +
			" event_id=" + req.EventID +
			" amount=" + formatInt64(req.Amount) +
			" err=" + fundErr.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "fund operation failed: " + fundErr.Error(),
			"error_code": "FUND_FAILED",
		})
		return
	}

	// Structured log: who (platform source + tenant) / what (amount) / result (new_balance).
	if replayed {
		common.SysLog("InternalFundCreditPool: replayed" +
			" source=" + req.Source +
			" tenant=" + tenant.Id +
			" event_id=" + req.EventID +
			" new_balance=" + formatInt64(event.NewBalance))
	} else {
		common.SysLog("InternalFundCreditPool: funded" +
			" source=" + req.Source +
			" tenant=" + tenant.Id +
			" event_id=" + req.EventID +
			" amount=" + formatInt64(req.Amount) +
			" new_balance=" + formatInt64(event.NewBalance))
		metrics.CreditPoolBalance.WithLabelValues(tenant.Id).Set(float64(event.NewBalance))
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"tenant_id":   tenant.Id,
			"new_balance": event.NewBalance,
			"event_id":    event.EventID,
			"replayed":    replayed,
		},
	})
}

// formatInt64 converts int64 to string without importing strconv at call sites.
func formatInt64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
