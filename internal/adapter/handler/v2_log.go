package handler

import (
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"

	"github.com/gin-gonic/gin"
)

// logView is the field-whitelisted projection returned by the v2 log endpoints.
// It excludes tenant_id (implicit from route), the caller IP (PII), the raw
// `other` payload, and the governance-internal fingerprint/upstream-model/
// channel-type/relay-mode columns. Mirrors redemptionView.
type logView struct {
	Id               int    `json:"id"`
	UserId           int    `json:"user_id"`
	CreatedAt        int64  `json:"created_at"`
	Type             int    `json:"type"`
	Content          string `json:"content"`
	Username         string `json:"username"`
	TokenName        string `json:"token_name"`
	ModelName        string `json:"model_name"`
	Quota            int    `json:"quota"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	UseTime          int    `json:"use_time"`
	IsStream         bool   `json:"is_stream"`
	ChannelId        int    `json:"channel"`
	ChannelName      string `json:"channel_name"`
	TokenId          int    `json:"token_id"`
	Group            string `json:"group"`
	TotalLatencyMs   int    `json:"total_latency_ms"`
}

func toLogViews(logs []*repo.Log) []logView {
	items := make([]logView, 0, len(logs))
	for _, l := range logs {
		items = append(items, logView{
			Id:               l.Id,
			UserId:           l.UserId,
			CreatedAt:        l.CreatedAt,
			Type:             l.Type,
			Content:          l.Content,
			Username:         l.Username,
			TokenName:        l.TokenName,
			ModelName:        l.ModelName,
			Quota:            l.Quota,
			PromptTokens:     l.PromptTokens,
			CompletionTokens: l.CompletionTokens,
			UseTime:          l.UseTime,
			IsStream:         l.IsStream,
			ChannelId:        l.ChannelId,
			ChannelName:      l.ChannelName,
			TokenId:          l.TokenId,
			Group:            l.Group,
			TotalLatencyMs:   l.TotalLatencyMs,
		})
	}
	return items
}

// GetLogsV2 retrieves the current user's logs (v2 API with tenant context)
// Route: GET /api/v2/:tenant_slug/logs
func GetLogsV2(c *gin.Context) {
	// Get tenant context from middleware
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Parse pagination and filter parameters
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	logType, _ := strconv.Atoi(c.DefaultQuery("type", "0"))
	modelName := c.Query("model_name")
	startTime, _ := strconv.ParseInt(c.DefaultQuery("start_time", "0"), 10, 64)
	endTime, _ := strconv.ParseInt(c.DefaultQuery("end_time", "0"), 10, 64)
	tokenName := c.Query("token_name")
	// Live-tail cursor: when present, return only rows newer than this id.
	afterID, _ := strconv.Atoi(c.DefaultQuery("after_id", "0"))
	// Cost-attribution filter (migration 029); 0 = no filter.
	projectID, _ := strconv.Atoi(c.DefaultQuery("project_id", "0"))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	offset := (page - 1) * pageSize

	// Build log query params (tenant isolation via the explicit scope arg)
	params := &repo.LogQueryParams{
		UserID:     tenantCtx.UserID,
		LogType:    logType,
		ModelName:  modelName,
		StartTime:  startTime,
		EndTime:    endTime,
		TokenName:  tokenName,
		AfterID:    afterID,
		ProjectID:  projectID,
		Offset:     offset,
		Limit:      pageSize,
	}

	// Get logs
	logs, total, err := repo.GetUserLogsWithParams(repo.ForTenant(tenantCtx.TenantID), params)
	if err != nil {
		common.SysError("Failed to get logs: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve logs",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"logs":      toLogViews(logs),
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// GetAllLogsV2 retrieves all logs for the tenant (admin only, v2 API)
// Route: GET /api/v2/:tenant_slug/logs/all
func GetAllLogsV2(c *gin.Context) {
	// Get tenant context from middleware
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// This endpoint returns every tenant member's logs (no user_id filter below),
	// so it must be restricted to tenant admins. The route is mounted under
	// UserAuth(), so the role check lives here in the handler.
	if !requireTenantAdmin(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Admin role required",
		})
		return
	}

	// Parse pagination and filter parameters
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	logType, _ := strconv.Atoi(c.DefaultQuery("type", "0"))
	modelName := c.Query("model_name")
	startTime, _ := strconv.ParseInt(c.DefaultQuery("start_time", "0"), 10, 64)
	endTime, _ := strconv.ParseInt(c.DefaultQuery("end_time", "0"), 10, 64)
	tokenName := c.Query("token_name")
	username := c.Query("username")
	// Cost-attribution filter (migration 029); 0 = no filter.
	projectID, _ := strconv.Atoi(c.DefaultQuery("project_id", "0"))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	offset := (page - 1) * pageSize

	// Build log query params (no user filter for all logs; tenant isolation
	// via the explicit scope arg)
	params := &repo.LogQueryParams{
		LogType:    logType,
		ModelName:  modelName,
		StartTime:  startTime,
		EndTime:    endTime,
		TokenName:  tokenName,
		Username:   username,
		ProjectID:  projectID,
		Offset:     offset,
		Limit:      pageSize,
	}

	// Get all logs for tenant
	logs, total, err := repo.GetTenantLogsWithParams(repo.ForTenant(tenantCtx.TenantID), params)
	if err != nil {
		common.SysError("Failed to get logs: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to retrieve logs",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"logs":      toLogViews(logs),
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}
