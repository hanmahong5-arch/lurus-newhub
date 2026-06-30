package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// ============================================================================
// Client API — Platform-wide endpoints for other Lurus products.
// Auth: FlexAuth (OIDC JWT or API Token sk-xxx)
// Base path: /api/v2/client/*
// ============================================================================

// ClientGetProfile returns the authenticated user's profile, quota, and daily quota.
// GET /api/v2/client/profile
func ClientGetProfile(c *gin.Context) {
	userID := c.GetInt("id")
	user, err := repo.GetUserById(userID, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "user not found",
		})
		return
	}

	tokenCount, _ := repo.CountUserTokens(userID)
	dailyQuotaInfo, _ := repo.GetUserDailyQuotaInfo(userID)

	var dailyQuota interface{}
	if dailyQuotaInfo != nil {
		dailyQuota = gin.H{
			"limit":             dailyQuotaInfo.DailyQuota,
			"used":              dailyQuotaInfo.DailyUsed,
			"remaining":         dailyQuotaInfo.DailyRemaining,
			"last_reset":        dailyQuotaInfo.LastDailyReset,
			"is_using_fallback": dailyQuotaInfo.IsUsingFallback,
		}
	}

	displayType := "usd"
	switch operation_setting.GetQuotaDisplayType() {
	case operation_setting.QuotaDisplayTypeCNY:
		displayType = "cny"
	case operation_setting.QuotaDisplayTypeTokens:
		displayType = "tokens"
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"id":               user.Id,
			"username":         user.Username,
			"display_name":     user.DisplayName,
			"email":            user.Email,
			"role":             user.Role,
			"status":           user.Status,
			"group":            user.Group,
			"quota":            user.Quota,
			"used_quota":       user.UsedQuota,
			"remaining_quota":  user.Quota - user.UsedQuota,
			"request_count":    user.RequestCount,
			"token_count":      tokenCount,
			"daily_quota":      dailyQuota,
			"display_currency": displayType,
			"display_amount":   calculateDisplayAmount(user.Quota - user.UsedQuota),
		},
	})
}

// ClientGetUsageSummary returns aggregated usage statistics.
// GET /api/v2/client/usage/summary
// Query: start_timestamp, end_timestamp (optional, default last 30 days)
func ClientGetUsageSummary(c *gin.Context) {
	userID := c.GetInt("id")
	user, err := repo.GetUserById(userID, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "user not found",
		})
		return
	}

	startTS, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTS, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if startTS == 0 {
		startTS = time.Now().AddDate(0, 0, -30).Unix()
	}
	if endTS == 0 {
		endTS = time.Now().Unix()
	}

	stat := repo.SumUsedQuota(
		repo.LogTypeConsume, startTS, endTS,
		"", user.Username, "", 0, "",
	)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"quota":           user.Quota,
			"used_quota":      user.UsedQuota,
			"remaining_quota": user.Quota - user.UsedQuota,
			"period_used":     stat.Quota,
			"rpm":             stat.Rpm,
			"tpm":             stat.Tpm,
			"start_timestamp": startTS,
			"end_timestamp":   endTS,
			"display_amount":  calculateDisplayAmount(user.Quota - user.UsedQuota),
		},
	})
}

// ClientGetUsageByModel returns usage breakdown by model.
// GET /api/v2/client/usage/models
// Query: start_timestamp, end_timestamp (optional, default last 30 days)
func ClientGetUsageByModel(c *gin.Context) {
	userID := c.GetInt("id")

	startTS, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTS, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if startTS == 0 {
		startTS = time.Now().AddDate(0, 0, -30).Unix()
	}
	if endTS == 0 {
		endTS = time.Now().Unix()
	}

	quotaData, err := repo.GetQuotaDataByUserId(userID, startTS, endTS)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to query usage data",
		})
		return
	}

	// Aggregate by model_name
	type modelStats struct {
		Quota     int `json:"quota"`
		TokenUsed int `json:"token_used"`
		Count     int `json:"count"`
	}
	modelMap := make(map[string]*modelStats)
	for _, d := range quotaData {
		ms, ok := modelMap[d.ModelName]
		if !ok {
			ms = &modelStats{}
			modelMap[d.ModelName] = ms
		}
		ms.Quota += d.Quota
		ms.TokenUsed += d.TokenUsed
		ms.Count += d.Count
	}

	models := make([]gin.H, 0, len(modelMap))
	for name, ms := range modelMap {
		models = append(models, gin.H{
			"model_name": name,
			"quota":      ms.Quota,
			"token_used": ms.TokenUsed,
			"count":      ms.Count,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"models":          models,
			"start_timestamp": startTS,
			"end_timestamp":   endTS,
		},
	})
}

// ClientGetUsageDaily returns daily usage trend.
// GET /api/v2/client/usage/daily
// Query: days (default 30, max 90)
func ClientGetUsageDaily(c *gin.Context) {
	userID := c.GetInt("id")

	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))
	if days < 1 {
		days = 30
	}
	if days > 90 {
		days = 90
	}

	now := time.Now()
	startTS := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).
		AddDate(0, 0, -days+1).Unix()
	endTS := now.Unix()

	quotaData, err := repo.GetQuotaDataByUserId(userID, startTS, endTS)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to query usage data",
		})
		return
	}

	// Aggregate by date (created_at is a unix timestamp stored as start-of-day by LogQuotaData)
	type dayStats struct {
		Date      string `json:"date"`
		Quota     int    `json:"quota"`
		TokenUsed int    `json:"token_used"`
		Count     int    `json:"count"`
	}
	dayMap := make(map[string]*dayStats)
	for _, d := range quotaData {
		t := time.Unix(d.CreatedAt, 0)
		dateStr := t.Format("2006-01-02")
		ds, ok := dayMap[dateStr]
		if !ok {
			ds = &dayStats{Date: dateStr}
			dayMap[dateStr] = ds
		}
		ds.Quota += d.Quota
		ds.TokenUsed += d.TokenUsed
		ds.Count += d.Count
	}

	// Build ordered list for the requested date range
	dailyList := make([]gin.H, 0, days)
	for i := 0; i < days; i++ {
		d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).
			AddDate(0, 0, -days+1+i)
		dateStr := d.Format("2006-01-02")
		ds := dayMap[dateStr]
		entry := gin.H{
			"date":       dateStr,
			"quota":      0,
			"token_used": 0,
			"count":      0,
		}
		if ds != nil {
			entry["quota"] = ds.Quota
			entry["token_used"] = ds.TokenUsed
			entry["count"] = ds.Count
		}
		dailyList = append(dailyList, entry)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"days":  days,
			"daily": dailyList,
		},
	})
}

// ClientGetTokens returns the authenticated user's API tokens.
// GET /api/v2/client/tokens
// Query: p (page, default 1), size (page_size, default 20, max 100)
func ClientGetTokens(c *gin.Context) {
	userID := c.GetInt("id")

	page, _ := strconv.Atoi(c.DefaultQuery("p", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	tokens, err := repo.GetAllUserTokens(userID, offset, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to query tokens",
		})
		return
	}

	total, _ := repo.CountUserTokens(userID)

	items := make([]gin.H, 0, len(tokens))
	for _, t := range tokens {
		expiredAt := t.ExpiredTime
		if expiredAt == -1 {
			expiredAt = 0 // -1 means never expires
		}
		items = append(items, gin.H{
			"id":                   t.Id,
			"name":                 t.Name,
			"status":               t.Status,
			"created_time":         t.CreatedTime,
			"accessed_time":        t.AccessedTime,
			"expired_time":         expiredAt,
			"remain_quota":         t.RemainQuota,
			"used_quota":           t.UsedQuota,
			"unlimited_quota":      t.UnlimitedQuota,
			"model_limits_enabled": t.ModelLimitsEnabled,
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

// ClientGetSessions returns the current user's active session information.
// This is lurus-api's own session state, not the OIDC provider's.
// GET /api/v2/client/sessions
func ClientGetSessions(c *gin.Context) {
	userID := c.GetInt("id")
	user, err := repo.GetUserById(userID, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "user not found",
		})
		return
	}

	authMethod, _ := c.Get("auth_method")

	// Collect active token count as a proxy for "sessions"
	activeTokens, _ := repo.CountUserTokens(userID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"user_id":       user.Id,
			"username":      user.Username,
			"status":        user.Status,
			"auth_method":   authMethod,
			"active_tokens": activeTokens,
			"request_count": user.RequestCount,
		},
	})
}
