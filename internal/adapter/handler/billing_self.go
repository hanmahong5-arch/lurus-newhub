package handler

import (
	"net/http"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/currency"

	"github.com/gin-gonic/gin"
)

// SelfBillingBalance returns the caller's remaining balance (LB = Lubell units).
// Authenticated via TokenAuth (sk-xxx key).
// GET /v1/billing/balance
func SelfBillingBalance(c *gin.Context) {
	userId := c.GetInt("id")
	tokenId := c.GetInt("token_id")

	info, err := app.GetSubscriptionQuotaInfo(userId, tokenId, common.DisplayTokenStatEnabled)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query balance"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"balance_lb":      info.RemainingAmount,
		"used_lb":         info.UsedAmount,
		"total_lb":        info.TotalAmount,
		"unlimited_quota": info.UnlimitedQuota,
	})
}

// SelfBillingUsage returns usage statistics grouped by model for a given time period.
// Authenticated via TokenAuth (sk-xxx key).
// GET /v1/billing/usage?period=today|7d|30d
func SelfBillingUsage(c *gin.Context) {
	userId := c.GetInt("id")

	period := c.DefaultQuery("period", "today")
	var since time.Time
	now := time.Now()
	switch period {
	case "7d":
		since = now.AddDate(0, 0, -7)
	case "30d":
		since = now.AddDate(0, 0, -30)
	default:
		// today
		since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	}

	stats, err := repo.GetUserLogStatByPeriod(userId, since)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query usage"})
		return
	}

	var totalCostLB float64
	byModel := make([]gin.H, 0, len(stats))
	for _, s := range stats {
		costLB := currency.QuotaToCNY(int(s.TotalQuota))
		totalCostLB += costLB
		byModel = append(byModel, gin.H{
			"model":   s.Key,
			"count":   s.Count,
			"cost_lb": costLB,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"period":        period,
		"total_cost_lb": totalCostLB,
		"by_model":      byModel,
	})
}
