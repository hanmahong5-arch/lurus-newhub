package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// logView is the field-whitelisted projection returned by the v2 log endpoints.
// It excludes tenant_id (implicit from route), the caller IP (PII), and the
// governance-internal fingerprint/upstream-model/channel-type/relay-mode
// columns. Mirrors redemptionView. `other` is tier-filtered, not dropped:
// the user route ships the same TierInternal-stripped projection as the v1
// self-log route (cache token counts, request_path — what makes a bill
// explainable), the tenant-admin route ships the full payload including
// admin_info.route_attempts, which feeds the console's routing-trace panel.
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
	// What this request took from the customer's platform wallet, in
	// 0.0001 yuan, as recorded at settlement (logs.charged_cny4); 0 when
	// nothing was. The console's log detail shows it as "charged". It was
	// missing from this view until 2026-10-05, so that line never rendered:
	// the console's own test fed the field in by hand, and the real-chain
	// acceptance scenario TC-M1 found it absent.
	ChargedCNY4 int64 `json:"charged_cny4"`
	// Omitted (not null) when the row carries no payload or the stored JSON is
	// corrupt — embedding an invalid RawMessage would break marshalling of the
	// entire response.
	Other json.RawMessage `json:"other,omitempty"`
}

// toLogViews projects log rows for the API. includeInternalOther=true is the
// tenant-admin view (full `other`); false strips TierInternal keys via the
// same shared list the v1 self-log route uses.
func toLogViews(logs []*repo.Log, includeInternalOther bool) []logView {
	items := make([]logView, 0, len(logs))
	for _, l := range logs {
		other := l.Other
		// channel_name is classified TierInternal (governance/classification.go)
		// and the v1 self-log route has always blanked it (formatUserLogs). The
		// v2 user route shipped it anyway, so an ordinary customer could read
		// our upstream vendor accounts straight off their own log rows — the
		// operator's channel naming ("openai-key3", "azure-backup") describes
		// our supply chain, not their request.
		//
		// The console already renders channel_name → #<channel id> → n/a, so
		// blanking it here needs no frontend change: users fall back to the
		// opaque id. The tenant-admin route (includeInternalOther=true) is
		// unaffected.
		channelName := l.ChannelName
		if !includeInternalOther {
			other = repo.SanitizeOtherForUser(other)
			channelName = ""
		}
		var rawOther json.RawMessage
		if other != "" && other != "null" && json.Valid([]byte(other)) {
			rawOther = json.RawMessage(other)
		}
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
			ChannelName:      channelName,
			TokenId:          l.TokenId,
			Group:            l.Group,
			TotalLatencyMs:   l.TotalLatencyMs,
			ChargedCNY4:      l.ChargedCNY4,
			Other:            rawOther,
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
	// Cross-product attribution filter (Workstream 0); "" = no filter.
	sourceProduct := c.Query("source_product")
	// L2-REQUEST-IDENTITY correlation-id filters; "" = no filter.
	requestID := c.Query("request_id")
	sessionID := c.Query("session_id")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	offset := (page - 1) * pageSize

	// A department lead reads their projects' rows instead of their own; a
	// project_id outside that set is a 404.
	scope := resolveLogReadScope(c, tenantCtx)
	if scope.rejectProject(c, projectID) {
		return
	}

	// Build log query params (tenant isolation via the explicit scope arg)
	params := &repo.LogQueryParams{
		UserID:        scope.userID(tenantCtx.UserID),
		ProjectIDs:    scope.projects(),
		LogType:       logType,
		ModelName:     modelName,
		StartTime:     startTime,
		EndTime:       endTime,
		TokenName:     tokenName,
		AfterID:       afterID,
		ProjectID:     projectID,
		SourceProduct: sourceProduct,
		RequestID:     requestID,
		SessionID:     sessionID,
		Offset:        offset,
		Limit:         pageSize,
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
			"logs":      toLogViews(logs, false),
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
	// Cross-product attribution filter (Workstream 0); "" = no filter.
	sourceProduct := c.Query("source_product")
	// L2-REQUEST-IDENTITY correlation-id filters; "" = no filter.
	requestID := c.Query("request_id")
	sessionID := c.Query("session_id")
	// The vendor's own request/trace id (TierInternal) — admin-only, so it
	// is bound HERE (the tenant-admin route) and nowhere in GetLogsV2 above.
	upstreamRequestID := c.Query("upstream_request_id")
	if !isPlatformStaff(c, tenantCtx) {
		// Customer tenant admins must not probe vendor ids (TierInternal).
		upstreamRequestID = ""
	}

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
		LogType:           logType,
		ModelName:         modelName,
		StartTime:         startTime,
		EndTime:           endTime,
		TokenName:         tokenName,
		Username:          username,
		ProjectID:         projectID,
		SourceProduct:     sourceProduct,
		RequestID:         requestID,
		SessionID:         sessionID,
		UpstreamRequestID: upstreamRequestID,
		Offset:            offset,
		Limit:             pageSize,
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
			// Platform staff get the full `other` (admin_info incl. route_attempts);
			// a tenant admin is a CUSTOMER and gets the user view (TierInternal keys
			// stripped, channel_name blank) — the route only gates on tenant-admin.
			"logs":      toLogViews(logs, isPlatformStaff(c, tenantCtx)),
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}
