package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// logStatWindowSeconds bounds the rolling RPM/TPM sample window. RPM/TPM are
// inherently "right now" rates, so they ignore the caller's start/end filter
// and always look at the last 60 seconds — matching repo.SumUsedQuota.
const logStatWindowSeconds = 60

// logStatView is the aggregate returned by GetLogStatV2. Totals reflect the
// caller's active filters (same shape as GetLogsV2); rpm/tpm are the last-60s
// rolling rates for the same tenant/user/model/token scope.
type logStatView struct {
	TotalRequests    int64 `json:"total_requests"`
	TotalQuota       int64 `json:"total_quota"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	// CacheReadTokens / CacheWriteTokens report prompt-cache savings
	// (C08-TTFT-CACHE-SERIES): the same other.cache_tokens /
	// other.cache_creation_tokens keys log_info_generate.go writes onto
	// text/claude consume rows, summed over the window. Zero for a window
	// with no cache hits. Upstream-reported except on OpenRouter channels,
	// where cache_creation_tokens may instead be derived from the
	// upstream-reported cost (quota.go CalcOpenRouterCacheCreateTokens) when
	// the wire itself did not report a cache-creation count.
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	Rpm              int64 `json:"rpm"`
	Tpm              int64 `json:"tpm"`
	StartTime        int64 `json:"start_time"`
	EndTime          int64 `json:"end_time"`
	// ByProduct is the cross-product attribution breakdown (Workstream 0):
	// the same window filters as the totals above, MINUS the source_product
	// filter itself, so a caller filtering to one product still sees where
	// the rest of the window's spend went.
	ByProduct []productSpendView `json:"by_product"`
}

// productSpendView is one row of the by_product breakdown.
type productSpendView struct {
	SourceProduct    string `json:"source_product"`
	TotalRequests    int64  `json:"total_requests"`
	TotalQuota       int64  `json:"total_quota"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
}

// GetLogStatV2 returns aggregate usage stats for the current user over the
// active filters (RPM / TPM / total requests / total quota / token totals).
// Route: GET /api/v2/:tenant_slug/logs/stat
//
// The filter shape mirrors GetLogsV2 (type, model_name, token_name, start_time,
// end_time) so the header summarises exactly the rows the trace table lists.
// Two bounded aggregate queries run regardless of window size — there is no row
// scan returned to the caller, so a wide window stays a single GROUP-free SUM.
func GetLogStatV2(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	serveLogStatV2(c, tenantCtx.TenantID, tenantCtx.UserID, "")
}

// GetAllLogStatV2 is the tenant-wide sibling of GetLogStatV2 — same aggregates
// with no user_id filter, so the stat header can summarise the same rows
// GetAllLogsV2 lists. Mirrors that route's shape: admin gate in the handler
// (mounted under UserAuth()), plus an optional `username` filter.
// Route: GET /api/v2/:tenant_slug/logs/stat/all
func GetAllLogStatV2(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Tenant-wide aggregates cover every member's usage, so this must stay
	// restricted to tenant admins — same gate as GetAllLogsV2.
	if !requireTenantAdmin(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Admin role required",
		})
		return
	}

	serveLogStatV2(c, tenantCtx.TenantID, 0, c.Query("username"))
}

// serveLogStatV2 runs the two aggregate queries and writes the response.
// userID > 0 scopes to that member's rows; userID == 0 is tenant-wide (callers
// must admin-gate before passing 0). username is the tenant-wide route's
// optional member filter.
func serveLogStatV2(c *gin.Context, tenantID string, userID int, username string) {
	logType, _ := strconv.Atoi(c.DefaultQuery("type", "0"))
	modelName := c.Query("model_name")
	tokenName := c.Query("token_name")
	startTime, _ := strconv.ParseInt(c.DefaultQuery("start_time", "0"), 10, 64)
	endTime, _ := strconv.ParseInt(c.DefaultQuery("end_time", "0"), 10, 64)
	// Cost-attribution filter (migration 029); 0 = no filter. This handler
	// hand-writes its aggregates instead of going through
	// repo.GetUserLogsWithParams, so every filter GetLogsV2 accepts has to be
	// repeated here or the header totals stop matching the rows below them.
	projectID, _ := strconv.Atoi(c.DefaultQuery("project_id", "0"))
	// Cross-product attribution filter (Workstream 0); "" = no filter.
	sourceProduct := c.Query("source_product")

	// Window totals — apply exactly the GetLogsV2 filters, tenant scoped
	// (+ user scoped unless tenant-wide).
	windowQuery := repo.LOG_DB.Model(&repo.Log{}).
		Where("tenant_id = ?", tenantID)
	if userID > 0 {
		windowQuery = windowQuery.Where("user_id = ?", userID)
	}
	if username != "" {
		windowQuery = windowQuery.Where("username = ?", username)
	}
	if logType > 0 {
		windowQuery = windowQuery.Where("type = ?", logType)
	}
	if modelName != "" {
		windowQuery = windowQuery.Where("model_name = ?", modelName)
	}
	if tokenName != "" {
		windowQuery = windowQuery.Where("token_name = ?", tokenName)
	}
	if startTime > 0 {
		windowQuery = windowQuery.Where("created_at >= ?", startTime)
	}
	if endTime > 0 {
		windowQuery = windowQuery.Where("created_at <= ?", endTime)
	}
	if projectID > 0 {
		windowQuery = windowQuery.Where("project_id = ?", projectID)
	}
	if sourceProduct != "" {
		windowQuery = windowQuery.Where(repo.SourceProductExpr()+" = ?", sourceProduct)
	}

	var window struct {
		TotalRequests    int64 `gorm:"column:total_requests"`
		TotalQuota       int64 `gorm:"column:total_quota"`
		PromptTokens     int64 `gorm:"column:prompt_tokens"`
		CompletionTokens int64 `gorm:"column:completion_tokens"`
		CacheReadTokens  int64 `gorm:"column:cache_read_tokens"`
		CacheWriteTokens int64 `gorm:"column:cache_write_tokens"`
	}
	if err := windowQuery.
		Select("COUNT(*) AS total_requests, " +
			"COALESCE(SUM(quota), 0) AS total_quota, " +
			"COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, " +
			"COALESCE(SUM(completion_tokens), 0) AS completion_tokens, " +
			"COALESCE(SUM(CAST(" + repo.OtherTextExpr("cache_tokens") + " AS BIGINT)), 0) AS cache_read_tokens, " +
			"COALESCE(SUM(CAST(" + repo.OtherTextExpr("cache_creation_tokens") + " AS BIGINT)), 0) AS cache_write_tokens").
		Scan(&window).Error; err != nil {
		common.SysError("serveLogStatV2: window aggregate failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to aggregate log stats",
		})
		return
	}

	// Rolling RPM/TPM — last 60s of consume traffic for the same scope,
	// independent of the caller's window. now is the cutoff anchor.
	since := time.Now().Unix() - logStatWindowSeconds
	rateQuery := repo.LOG_DB.Model(&repo.Log{}).
		Where("tenant_id = ?", tenantID).
		Where("type = ?", repo.LogTypeConsume).
		Where("created_at >= ?", since)
	if userID > 0 {
		rateQuery = rateQuery.Where("user_id = ?", userID)
	}
	if username != "" {
		rateQuery = rateQuery.Where("username = ?", username)
	}
	if modelName != "" {
		rateQuery = rateQuery.Where("model_name = ?", modelName)
	}
	if tokenName != "" {
		rateQuery = rateQuery.Where("token_name = ?", tokenName)
	}
	if projectID > 0 {
		rateQuery = rateQuery.Where("project_id = ?", projectID)
	}
	if sourceProduct != "" {
		rateQuery = rateQuery.Where(repo.SourceProductExpr()+" = ?", sourceProduct)
	}

	var rate struct {
		Rpm int64 `gorm:"column:rpm"`
		Tpm int64 `gorm:"column:tpm"`
	}
	if err := rateQuery.
		Select("COUNT(*) AS rpm, " +
			"COALESCE(SUM(prompt_tokens), 0) + COALESCE(SUM(completion_tokens), 0) AS tpm").
		Scan(&rate).Error; err != nil {
		common.SysError("serveLogStatV2: rate aggregate failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to aggregate log stats",
		})
		return
	}

	// by_product breakdown — same window filters as the totals above, MINUS
	// the source_product filter, so it always shows where a caller's window
	// spend went even when they filtered the totals down to one product.
	// Unattributed rows (no tag, or written before attribution shipped) fold
	// into the default product id rather than a blank group, mirroring
	// repo.GetSpendByProduct.
	breakdownQuery := repo.LOG_DB.Model(&repo.Log{}).
		Where("tenant_id = ?", tenantID)
	if userID > 0 {
		breakdownQuery = breakdownQuery.Where("user_id = ?", userID)
	}
	if username != "" {
		breakdownQuery = breakdownQuery.Where("username = ?", username)
	}
	if logType > 0 {
		breakdownQuery = breakdownQuery.Where("type = ?", logType)
	}
	if modelName != "" {
		breakdownQuery = breakdownQuery.Where("model_name = ?", modelName)
	}
	if tokenName != "" {
		breakdownQuery = breakdownQuery.Where("token_name = ?", tokenName)
	}
	if startTime > 0 {
		breakdownQuery = breakdownQuery.Where("created_at >= ?", startTime)
	}
	if endTime > 0 {
		breakdownQuery = breakdownQuery.Where("created_at <= ?", endTime)
	}
	if projectID > 0 {
		breakdownQuery = breakdownQuery.Where("project_id = ?", projectID)
	}

	var byProduct []productSpendView
	if err := breakdownQuery.
		Select("COALESCE(" + repo.SourceProductExpr() + ", '" + ratio_setting.DefaultSourceProduct + "') AS source_product, " +
			"COUNT(*) AS total_requests, " +
			"COALESCE(SUM(quota), 0) AS total_quota, " +
			"COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, " +
			"COALESCE(SUM(completion_tokens), 0) AS completion_tokens, " +
			"COALESCE(SUM(CAST(" + repo.OtherTextExpr("cache_tokens") + " AS BIGINT)), 0) AS cache_read_tokens, " +
			"COALESCE(SUM(CAST(" + repo.OtherTextExpr("cache_creation_tokens") + " AS BIGINT)), 0) AS cache_write_tokens").
		Group("source_product").
		Scan(&byProduct).Error; err != nil {
		common.SysError("serveLogStatV2: by_product aggregate failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to aggregate log stats",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": logStatView{
			TotalRequests:    window.TotalRequests,
			TotalQuota:       window.TotalQuota,
			PromptTokens:     window.PromptTokens,
			CompletionTokens: window.CompletionTokens,
			CacheReadTokens:  window.CacheReadTokens,
			CacheWriteTokens: window.CacheWriteTokens,
			Rpm:              rate.Rpm,
			Tpm:              rate.Tpm,
			StartTime:        startTime,
			EndTime:          endTime,
			ByProduct:        byProduct,
		},
	})
}
