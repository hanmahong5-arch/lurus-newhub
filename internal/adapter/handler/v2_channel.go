package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// ============================================================================
// V2 Channel Controllers
// Channels are tenant-level resources managed by tenant admins
// ============================================================================

// channelView is the field-whitelisted projection returned by ListChannelsV2.
// It excludes the raw key and per-channel secrets (other, setting,
// param_override, header_override, other_info) plus tenant_id (implicit from
// route), so a tenant admin only sees the display surface — never another
// channel's credentials. Mirrors redemptionView.
type channelView struct {
	Id           int     `json:"id"`
	Name         string  `json:"name"`
	Type         int     `json:"type"`
	Key          string  `json:"key"` // always masked; repo omits the key column
	Status       int     `json:"status"`
	Group        string  `json:"group"`
	Models       string  `json:"models"`
	ModelMapping *string `json:"model_mapping"`
	Priority     *int64  `json:"priority"`
	Weight       *uint   `json:"weight"`
	Tag          *string `json:"tag"`
	Remark       *string `json:"remark"`
	BaseURL      *string `json:"base_url"`
	Balance      float64 `json:"balance"`
	UsedQuota    int64   `json:"used_quota"`
	ResponseTime int     `json:"response_time"`
	TestTime     int64   `json:"test_time"`
	CreatedTime  int64   `json:"created_time"`
}

// ListChannelsV2 retrieves channels for the tenant (admin only)
// Route: GET /api/v2/:tenant_slug/channels
func ListChannelsV2(c *gin.Context) {
	// Get tenant context from middleware
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Check admin role
	if !requireTenantAdmin(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Admin role required",
		})
		return
	}

	// Parse pagination parameters
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	startIdx := (page - 1) * pageSize

	// Parse filter parameters
	keyword := c.Query("keyword")
	group := c.Query("group")
	modelFilter := c.Query("model")
	tag := c.Query("tag")
	idSort := c.DefaultQuery("id_sort", "false") == "true"

	var channels []*repo.Channel
	var total int64

	// Build query based on filters
	if keyword != "" || group != "" || modelFilter != "" {
		// Use search function (tenant-scoped: never matches other tenants' channels)
		allChannels, searchErr := repo.SearchChannelsByTenant(tenantCtx.TenantID, keyword, group, modelFilter, idSort)
		if searchErr != nil {
			common.SysError("Failed to search channels: " + searchErr.Error())
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "Failed to search channels",
			})
			return
		}
		total = int64(len(allChannels))
		// Manual pagination
		end := startIdx + pageSize
		if end > len(allChannels) {
			end = len(allChannels)
		}
		if startIdx < len(allChannels) {
			channels = allChannels[startIdx:end]
		}
	} else if tag != "" {
		// Filter by tag (tenant-scoped)
		allChannels, tagErr := repo.GetChannelsByTagAndTenant(tenantCtx.TenantID, tag, idSort)
		if tagErr != nil {
			common.SysError("Failed to get channels by tag: " + tagErr.Error())
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "Failed to get channels",
			})
			return
		}
		total = int64(len(allChannels))
		// Manual pagination
		end := startIdx + pageSize
		if end > len(allChannels) {
			end = len(allChannels)
		}
		if startIdx < len(allChannels) {
			channels = allChannels[startIdx:end]
		}
	} else {
		// Get this tenant's channels with pagination
		var getAllErr error
		channels, getAllErr = repo.GetChannelsByTenant(tenantCtx.TenantID, startIdx, pageSize, idSort)
		if getAllErr != nil {
			common.SysError("Failed to get channels: " + getAllErr.Error())
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "Failed to get channels",
			})
			return
		}
		total, _ = repo.GetTenantChannelCount(tenantCtx.TenantID)
	}

	// Project to the field-whitelisted view (masked/empty key, no secrets).
	items := make([]channelView, 0, len(channels))
	for _, ch := range channels {
		items = append(items, channelView{
			Id:           ch.Id,
			Name:         ch.Name,
			Type:         ch.Type,
			Key:          maskKey(ch.Key),
			Status:       ch.Status,
			Group:        ch.Group,
			Models:       ch.Models,
			ModelMapping: ch.ModelMapping,
			Priority:     ch.Priority,
			Weight:       ch.Weight,
			Tag:          ch.Tag,
			Remark:       ch.Remark,
			BaseURL:      ch.BaseURL,
			Balance:      ch.Balance,
			UsedQuota:    ch.UsedQuota,
			ResponseTime: ch.ResponseTime,
			TestTime:     ch.TestTime,
			CreatedTime:  ch.CreatedTime,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"channels":  items,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// GetChannelV2 retrieves a specific channel (admin only)
// Route: GET /api/v2/:tenant_slug/channels/:id
func GetChannelV2(c *gin.Context) {
	// Get tenant context from middleware
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Check admin role
	if !requireTenantAdmin(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Admin role required",
		})
		return
	}

	// Get channel ID from URL
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid channel ID",
		})
		return
	}

	// Get channel
	channel, err := repo.GetChannelById(channelID, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Channel not found",
		})
		return
	}

	// Verify tenant ownership
	if channel.TenantId != tenantCtx.TenantID {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Access denied"})
		return
	}

	// Mask the upstream provider key — this is the single-channel read path
	// and must never return the plaintext credential (see channelView in
	// ListChannelsV2, which already masks it for the list path).
	channel.Key = maskKey(channel.Key)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    channel,
	})
}

// CreateChannelV2 creates a new channel (admin only)
// Route: POST /api/v2/:tenant_slug/channels
func CreateChannelV2(c *gin.Context) {
	// Get tenant context from middleware
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Check admin role
	if !requireTenantAdmin(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Admin role required",
		})
		return
	}

	// Parse request body
	var channel repo.Channel
	if err := c.ShouldBindJSON(&channel); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
			"error":   err.Error(),
		})
		return
	}

	// Validate required fields
	if channel.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Channel name is required",
		})
		return
	}
	if channel.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Channel key is required",
		})
		return
	}
	if channel.Models == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "At least one model is required",
		})
		return
	}

	// Validate name length
	if len(channel.Name) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Channel name too long (max 100 characters)",
		})
		return
	}

	// Set defaults
	channel.CreatedTime = common.GetTimestamp()
	if channel.Status == 0 {
		channel.Status = common.ChannelStatusEnabled
	}
	if channel.Group == "" {
		channel.Group = "default"
	}

	// Set tenant ID from context
	if tenantId, err := repo.GetTenantID(c); err == nil {
		channel.TenantId = tenantId
	} else {
		channel.TenantId = "default"
	}

	// Shared vendor/config validation (settings format, model-name length,
	// VertexAI deployment region) — the same rules the v1 write path enforces via
	// validateChannel, so the v2 create path no longer diverges (previously it
	// only checked settings and would accept an over-long model name or a
	// VertexAI channel with no/invalid region). Egress (SSRF) is validated
	// separately below; see validateChannelContent for why it is kept out.
	if err := validateChannelContent(&channel, true); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// SSRF guard: a tenant admin must not point the channel egress at an
	// internal address to make the gateway relay/reflect internal responses.
	if err := validateChannelEgress(&channel); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// Insert channel
	if err := channel.Insert(); err != nil {
		common.SysError("Failed to create channel: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create channel",
		})
		return
	}

	// Refresh channel cache
	go repo.InitChannelCache()
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, tenantCtx.UserID,
		governance.ActionChannelUpdated, governance.ResourceChannel, channel.Id, ""))

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "Channel created successfully",
		"data": gin.H{
			"id":   channel.Id,
			"name": channel.Name,
		},
	})
}

// UpdateChannelV2 updates a channel (admin only)
// Route: PUT /api/v2/:tenant_slug/channels/:id
func UpdateChannelV2(c *gin.Context) {
	// Get tenant context from middleware
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Check admin role
	if !requireTenantAdmin(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Admin role required",
		})
		return
	}

	// Get channel ID from URL
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid channel ID",
		})
		return
	}

	// Get existing channel
	existingChannel, err := repo.GetChannelById(channelID, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Channel not found",
		})
		return
	}

	// Verify tenant ownership
	if existingChannel.TenantId != tenantCtx.TenantID {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Access denied"})
		return
	}

	// Parse request body
	var updateReq repo.Channel
	if err := c.ShouldBindJSON(&updateReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
			"error":   err.Error(),
		})
		return
	}

	// Update fields if provided
	if updateReq.Name != "" {
		if len(updateReq.Name) > 100 {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "Channel name too long (max 100 characters)",
			})
			return
		}
		existingChannel.Name = updateReq.Name
	}
	if updateReq.Key != "" {
		existingChannel.Key = updateReq.Key
	}
	if updateReq.Models != "" {
		existingChannel.Models = updateReq.Models
	}
	if updateReq.Group != "" {
		existingChannel.Group = updateReq.Group
	}
	if updateReq.BaseURL != nil {
		existingChannel.BaseURL = updateReq.BaseURL
	}
	if updateReq.Status != 0 {
		existingChannel.Status = updateReq.Status
	}
	if updateReq.Type != 0 {
		existingChannel.Type = updateReq.Type
	}
	if updateReq.Weight != nil {
		existingChannel.Weight = updateReq.Weight
	}
	if updateReq.Priority != nil {
		existingChannel.Priority = updateReq.Priority
	}
	if updateReq.ModelMapping != nil {
		existingChannel.ModelMapping = updateReq.ModelMapping
	}
	if updateReq.Tag != nil {
		existingChannel.Tag = updateReq.Tag
	}
	if updateReq.Remark != nil {
		existingChannel.Remark = updateReq.Remark
	}
	if updateReq.Setting != nil {
		existingChannel.Setting = updateReq.Setting
	}
	if updateReq.ParamOverride != nil {
		existingChannel.ParamOverride = updateReq.ParamOverride
	}
	if updateReq.HeaderOverride != nil {
		existingChannel.HeaderOverride = updateReq.HeaderOverride
	}

	// Validate settings
	if err := existingChannel.ValidateSettings(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// SSRF guard: re-validate egress only when this update actually touches the
	// egress fields. Validating unconditionally would trap a channel that became
	// non-compliant after a policy tightening — the admin could no longer disable
	// or rename it (every mutation would 400), leaving delete as the only escape.
	if updateReq.BaseURL != nil || updateReq.Setting != nil {
		if err := validateChannelEgress(existingChannel); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	}

	// Save channel
	if err := existingChannel.Update(); err != nil {
		common.SysError("Failed to update channel: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to update channel",
		})
		return
	}

	// Refresh channel cache
	go repo.InitChannelCache()
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, tenantCtx.UserID,
		governance.ActionChannelUpdated, governance.ResourceChannel, existingChannel.Id, ""))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Channel updated successfully",
		"data": gin.H{
			"id":   existingChannel.Id,
			"name": existingChannel.Name,
		},
	})
}

// DeleteChannelV2 deletes a channel (admin only)
// Route: DELETE /api/v2/:tenant_slug/channels/:id
func DeleteChannelV2(c *gin.Context) {
	// Get tenant context from middleware
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	// Check admin role
	if !requireTenantAdmin(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Admin role required",
		})
		return
	}

	// Get channel ID from URL
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid channel ID",
		})
		return
	}

	// Get existing channel to verify it exists
	channel, err := repo.GetChannelById(channelID, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Channel not found",
		})
		return
	}

	// Verify tenant ownership
	if channel.TenantId != tenantCtx.TenantID {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Access denied"})
		return
	}

	// Delete channel
	if err := channel.Delete(); err != nil {
		common.SysError("Failed to delete channel: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to delete channel",
		})
		return
	}

	// Refresh channel cache
	go repo.InitChannelCache()
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, tenantCtx.UserID,
		governance.ActionChannelDeleted, governance.ResourceChannel, channelID, ""))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Channel deleted successfully",
	})
}

// hasRole checks if the user has a specific role
func hasRole(roles []string, role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

// maskKey masks sensitive API keys for display
func maskKey(key string) string {
	if key == "" {
		return ""
	}
	// If key contains newlines (multi-key), mask each
	if strings.Contains(key, "\n") {
		lines := strings.Split(key, "\n")
		masked := make([]string, len(lines))
		for i, line := range lines {
			masked[i] = maskSingleKey(line)
		}
		return strings.Join(masked, "\n")
	}
	return maskSingleKey(key)
}

// maskSingleKey masks a single API key
func maskSingleKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}
