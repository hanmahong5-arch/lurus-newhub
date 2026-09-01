package handler

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	minTransferCNY = 1.0
	maxTransferCNY = 10000.0
)

// getIdentityAccountID extracts the platform account ID set by OIDCAuth middleware.
// Returns 0 if not available (platform was unreachable during auth).
func getIdentityAccountID(c *gin.Context) int64 {
	v, ok := c.Get("identity_account_id")
	if !ok {
		return 0
	}
	id, ok := v.(int64)
	if !ok {
		return 0
	}
	return id
}

// GetBillingSummary returns the authenticated user's aggregated billing info
// from lurus-platform (wallet balance, frozen, pre-auths, pending orders).
// GET /api/v2/user/billing/summary
func GetBillingSummary(c *gin.Context) {
	accountID := getIdentityAccountID(c)
	if accountID == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "platform account not linked",
		})
		return
	}

	bs, err := common.GetBillingSummary(c.Request.Context(), accountID)
	if err != nil || bs == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "billing service unavailable",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    bs,
	})
}

// GetBillingPaymentMethods returns available payment methods from lurus-platform.
// GET /api/v2/user/billing/payment-methods
func GetBillingPaymentMethods(c *gin.Context) {
	methods, _ := common.GetPaymentMethods(c.Request.Context())
	if methods == nil {
		methods = []common.PaymentMethod{}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    methods,
	})
}

// createCheckoutRequest is the input for creating a checkout session.
type createCheckoutRequest struct {
	AmountCNY     float64 `json:"amount_cny" binding:"required,gt=0"`
	PaymentMethod string  `json:"payment_method" binding:"required"`
	ReturnURL     string  `json:"return_url"`
}

// CreateBillingCheckout creates a wallet topup checkout session via lurus-platform.
// POST /api/v2/user/billing/checkout
func CreateBillingCheckout(c *gin.Context) {
	accountID := getIdentityAccountID(c)
	if accountID == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "platform account not linked",
		})
		return
	}

	var req createCheckoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": fmt.Sprintf("invalid request: %v", err),
		})
		return
	}

	// Generate idempotency key from request context to prevent duplicate orders
	idempotencyKey := fmt.Sprintf("api-%d-%s", accountID, uuid.New().String()[:8])

	result, err := common.CreateCheckout(
		c.Request.Context(),
		accountID,
		req.AmountCNY,
		req.PaymentMethod,
		"lurus-api",
		idempotencyKey,
		req.ReturnURL,
	)
	if err != nil {
		// A CheckoutError means the platform actively answered the request
		// (not a network/5xx outage). Its Status is the platform's real HTTP
		// status — pass a 400 through honestly with the real message so the
		// frontend can distinguish "bad amount / no payment method configured"
		// from "platform is down". Anything else (network error, 5xx, or no
		// typed error at all) stays the generic 503.
		var checkoutErr *common.CheckoutError
		if errors.As(err, &checkoutErr) && checkoutErr.Status == http.StatusBadRequest {
			msg := checkoutErr.Message
			if msg == "" {
				msg = "invalid checkout request"
			}
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": msg,
			})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "checkout service unavailable",
		})
		return
	}

	// Record local ownership (order_no -> account_id) so the status poll can
	// prove the caller owns the order: the platform's status endpoint is
	// keyed only by order_no and takes no account parameter, so this is the
	// only place the binding can be captured. A failure here must not fail
	// the checkout the user already paid to start — log and continue; the
	// status poll fails closed on a missing record, so the worst case is a
	// re-poll after the row lands, not a lost order.
	if result != nil && result.OrderNo != "" {
		if err := repo.RecordCheckoutOrder(result.OrderNo, accountID, req.AmountCNY, common.GetTimestamp()); err != nil {
			logger.LogError(c, fmt.Sprintf("record checkout order ownership failed (order_no=%s account=%d): %v",
				result.OrderNo, accountID, err))
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    result,
	})
}

// GetBillingCheckoutStatus polls the status of a checkout order.
// GET /api/v2/user/billing/checkout/:order_no/status
func GetBillingCheckoutStatus(c *gin.Context) {
	orderNo := c.Param("order_no")
	if orderNo == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "order_no is required",
		})
		return
	}

	// Ownership gate: the platform status endpoint is keyed only by order_no,
	// so without this check any authenticated caller could poll another
	// account's order (amount_cny/status/paid_at) by knowing or enumerating
	// its order_no. Resolve the caller's linked platform account, then require
	// a local ownership record that matches. Fail closed when the account is
	// unlinked or no record exists.
	accountID := getIdentityAccountID(c)
	if accountID == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "platform account not linked",
		})
		return
	}
	owner, found, err := repo.CheckoutOrderAccount(orderNo)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "checkout status unavailable",
		})
		return
	}
	if !found || owner != accountID {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "order not found for this account",
		})
		return
	}

	status, err := common.GetCheckoutStatus(c.Request.Context(), orderNo)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "checkout status unavailable",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    status,
	})
}

// topUpV2Request is the input for wallet-to-quota transfer.
type topUpV2Request struct {
	AmountCNY float64 `json:"amount_cny" binding:"required,gt=0"`
}

// TopUpV2 transfers wallet balance to product quota (synchronous, one-way).
// POST /api/v2/:tenant_slug/billing/topup
func TopUpV2(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "tenant context not found",
		})
		return
	}

	accountID := getIdentityAccountID(c)
	if accountID == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "platform account not linked",
		})
		return
	}

	// Require idempotency key header
	idempotencyKey := c.GetHeader("X-Idempotency-Key")
	if idempotencyKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "X-Idempotency-Key header is required",
		})
		return
	}

	var req topUpV2Request
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": fmt.Sprintf("invalid request: %v", err),
		})
		return
	}

	if req.AmountCNY < minTransferCNY || req.AmountCNY > maxTransferCNY {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": fmt.Sprintf("amount must be between %.0f and %.0f CNY", minTransferCNY, maxTransferCNY),
		})
		return
	}

	userID := tenantCtx.UserID
	tenantID := tenantCtx.TenantID

	// Idempotency check via log content tag
	ikTag := "[IK:" + idempotencyKey + "]"
	var count int64
	repo.LOG_DB.Model(&repo.Log{}).
		Where("user_id = ? AND type = ? AND content LIKE ?", userID, repo.LogTypeTopup, "%"+ikTag+"%").
		Count(&count)
	if count > 0 {
		currentQuota, _ := repo.GetUserQuota(userID, true)
		c.JSON(http.StatusOK, gin.H{
			"success":    true,
			"message":    "Already processed",
			"idempotent": true,
			"data": gin.H{
				"quota_balance": currentQuota,
			},
		})
		return
	}

	// Debit platform wallet (synchronous)
	debitResult, err := common.DebitWalletGRPC(
		c.Request.Context(), accountID, req.AmountCNY,
		"product_purchase",
		fmt.Sprintf("Wallet to lurus-api quota transfer (%.2f CNY)", req.AmountCNY),
		"lurus-api", idempotencyKey,
	)
	if err != nil {
		status := http.StatusServiceUnavailable
		msg := "wallet service unavailable"
		if strings.Contains(err.Error(), "insufficient") {
			status = http.StatusBadRequest
			msg = "insufficient wallet balance"
		}
		c.JSON(status, gin.H{
			"success": false,
			"message": msg,
		})
		return
	}

	// Credit product quota
	quotaAmount := int(req.AmountCNY * common.QuotaPerUnit)
	if err := repo.IncreaseUserQuota(userID, quotaAmount, true); err != nil {
		// Rollback: credit wallet back
		rollbackErr := common.CreditWalletGRPC(
			c.Request.Context(), accountID, req.AmountCNY,
			"refund",
			fmt.Sprintf("Rollback: quota credit failed for transfer %.2f CNY", req.AmountCNY),
			"lurus-api", idempotencyKey+":rollback",
		)
		if rollbackErr != nil {
			slog.Error("CRITICAL: wallet debited but quota credit AND rollback both failed",
				"user_id", userID,
				"account_id", accountID,
				"amount_cny", req.AmountCNY,
				"quota_error", err.Error(),
				"rollback_error", rollbackErr.Error(),
			)
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to credit quota, wallet has been refunded",
		})
		return
	}

	// Audit log with idempotency tag
	newQuota, _ := repo.GetUserQuota(userID, true)
	logContent := fmt.Sprintf("Wallet transfer: %.2f CNY -> %s quota. Balance after: %s. %s",
		req.AmountCNY, logger.LogQuota(quotaAmount), logger.LogQuota(newQuota), ikTag)
	repo.RecordLogWithTenant(userID, tenantID, repo.LogTypeTopup, logContent)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Transfer successful",
		"data": gin.H{
			"amount_cny":           req.AmountCNY,
			"quota_added":          quotaAmount,
			"new_quota":            newQuota,
			"wallet_balance_after": debitResult.BalanceAfter,
		},
	})
}

// GetTopUpsV2 returns paginated topup history for the current user.
// GET /api/v2/:tenant_slug/billing/topups
func GetTopUpsV2(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "tenant context not found",
		})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("p", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	var logs []*repo.Log
	var total int64

	tx := repo.LOG_DB.Model(&repo.Log{}).
		Where("user_id = ? AND type = ? AND tenant_id = ?",
			tenantCtx.UserID, repo.LogTypeTopup, tenantCtx.TenantID)

	if err := tx.Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to query topup history",
		})
		return
	}

	if err := tx.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&logs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to query topup history",
		})
		return
	}

	items := make([]gin.H, 0, len(logs))
	for _, l := range logs {
		items = append(items, gin.H{
			"id":         l.Id,
			"content":    l.Content,
			"quota":      l.Quota,
			"created_at": l.CreatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":     items,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}
