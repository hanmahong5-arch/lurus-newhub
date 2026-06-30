package handler

import (
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

// GetIdentityOverview returns the authenticated user's aggregated identity overview
// from lurus-platform (VIP level, Lubell balance, subscription status).
// Degrades gracefully when lurus-platform is unavailable.
// GET /api/v2/user/identity-overview?product_id=<pid>
func GetIdentityOverview(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil || tenantCtx.IDPSubject == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	im, err := common.GetAccountByZitadelSub(c.Request.Context(), tenantCtx.IDPSubject)
	if err != nil || im == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "identity account not found"})
		return
	}

	productID := c.DefaultQuery("product_id", "lurus-api")
	ov, _ := common.GetAccountOverview(c.Request.Context(), im.ID, productID)
	if ov == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "identity service unavailable"})
		return
	}

	c.JSON(http.StatusOK, ov)
}

// calculateDisplayAmount converts a raw quota value to the display amount
// based on the configured display type (USD, CNY, or Tokens).
func calculateDisplayAmount(quota int) float64 {
	amount := float64(quota)
	switch operation_setting.GetQuotaDisplayType() {
	case operation_setting.QuotaDisplayTypeCNY:
		amount = amount / common.QuotaPerUnit * operation_setting.USDExchangeRate
	case operation_setting.QuotaDisplayTypeTokens:
		// Keep raw token count
	default:
		// USD
		amount = amount / common.QuotaPerUnit
	}
	return amount
}

// getSessionAccountID reads the platform account ID from the session.
// Returns 0 if not available (user didn't login via OAuth or platform was unreachable).
func getSessionAccountID(c *gin.Context) int64 {
	session := sessions.Default(c)
	v := session.Get("identity_account_id")
	if v == nil {
		return 0
	}
	id, ok := v.(int64)
	if !ok {
		return 0
	}
	return id
}

// GetWalletInfo returns platform wallet balance for the current session user.
// GET /api/wallet/info
func GetWalletInfo(c *gin.Context) {
	accountID := getSessionAccountID(c)
	if accountID == 0 {
		// Fallback: return internal quota as "balance" for non-platform users.
		userId := c.GetInt("id")
		user, err := repo.GetUserById(userId, false)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to load user"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"source":         "internal",
				"balance":        float64(user.Quota) / common.QuotaPerUnit,
				"frozen":         0,
				"available":      float64(user.Quota) / common.QuotaPerUnit,
				"lifetime_topup": 0,
				"lifetime_spend": float64(user.UsedQuota) / common.QuotaPerUnit,
			},
		})
		return
	}

	bs, err := common.GetBillingSummary(c.Request.Context(), accountID)
	if err != nil || bs == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "platform billing unavailable",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"source":          "platform",
			"balance":         bs.Balance,
			"frozen":          bs.Frozen,
			"available":       bs.Available,
			"lifetime_topup":  bs.LifetimeTopup,
			"lifetime_spend":  bs.LifetimeSpend,
			"active_preauths": bs.ActivePreAuths,
			"pending_orders":  bs.PendingOrders,
			"topup_url":       common.IdentityPublicURL + "/wallet/topup",
		},
	})
}

// GetWalletTransactions returns paginated wallet transaction history for the
// current session's platform account. Reseller-mode Switch uses this to render
// the Wallet page transactions table.
//
// GET /api/wallet/transactions?p=1&page_size=20
//
// 503 when the caller has no platform-linked session — Switch surfaces this
// as "钱包功能仅对绑定 lurus-platform 的账号可用".
func GetWalletTransactions(c *gin.Context) {
	accountID := getSessionAccountID(c)
	if accountID == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "platform wallet not linked",
		})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("p", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	resp, err := common.ListWalletTransactionsHTTP(c.Request.Context(), accountID, page, pageSize)
	if err != nil || resp == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "platform wallet service unavailable",
		})
		return
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":     resp.Data,
			"total":     resp.Total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

func GetSubscription(c *gin.Context) {
	userId := c.GetInt("id")
	tokenId := c.GetInt("token_id")

	var totalAmount, expiredTime float64
	var expired int64

	if common.DisplayTokenStatEnabled {
		token, err := repo.GetTokenById(tokenId)
		if err != nil {
			c.JSON(200, gin.H{"error": types.OpenAIError{Message: err.Error(), Type: "upstream_error"}})
			return
		}
		totalAmount = calculateDisplayAmount(token.RemainQuota + token.UsedQuota)
		expired = token.ExpiredTime
		if expired <= 0 {
			expired = 0
		}
		if token.UnlimitedQuota {
			totalAmount = 100000000
		}
		_ = expiredTime
	} else {
		remainQuota, err := repo.GetUserQuota(userId, false)
		if err != nil {
			c.JSON(200, gin.H{"error": types.OpenAIError{Message: err.Error(), Type: "upstream_error"}})
			return
		}
		usedQuota, err := repo.GetUserUsedQuota(userId)
		if err != nil {
			c.JSON(200, gin.H{"error": types.OpenAIError{Message: err.Error(), Type: "upstream_error"}})
			return
		}
		totalAmount = calculateDisplayAmount(remainQuota + usedQuota)
	}

	subscription := OpenAISubscriptionResponse{
		Object:             "billing_subscription",
		HasPaymentMethod:   true,
		SoftLimitUSD:       totalAmount,
		HardLimitUSD:       totalAmount,
		SystemHardLimitUSD: totalAmount,
		AccessUntil:        expired,
	}
	c.JSON(200, subscription)
}

func GetUsage(c *gin.Context) {
	userId := c.GetInt("id")
	tokenId := c.GetInt("token_id")

	var quota int
	if common.DisplayTokenStatEnabled {
		token, err := repo.GetTokenById(tokenId)
		if err != nil {
			c.JSON(200, gin.H{"error": types.OpenAIError{Message: err.Error(), Type: "new_api_error"}})
			return
		}
		quota = token.UsedQuota
	} else {
		var err error
		quota, err = repo.GetUserUsedQuota(userId)
		if err != nil {
			c.JSON(200, gin.H{"error": types.OpenAIError{Message: err.Error(), Type: "new_api_error"}})
			return
		}
	}

	usage := OpenAIUsageResponse{
		Object:     "list",
		TotalUsage: calculateDisplayAmount(quota) * 100,
	}
	c.JSON(200, usage)
}
