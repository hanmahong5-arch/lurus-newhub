package handler

import (
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

func parseStatusFilter(statusParam string) int {
	switch strings.ToLower(statusParam) {
	case "enabled", "1":
		return common.ChannelStatusEnabled
	case "disabled", "0":
		return 0
	default:
		return -1
	}
}

func clearChannelInfo(channel *repo.Channel) {
	if channel.ChannelInfo.IsMultiKey {
		channel.ChannelInfo.MultiKeyDisabledReason = nil
		channel.ChannelInfo.MultiKeyDisabledTime = nil
	}
}

// enforceTenantScope blocks non-root administrators from reading or mutating another tenant's channel.
func enforceTenantScope(c *gin.Context, resourceTenantID string) bool {
	if c.GetInt("role") >= common.RoleRootUser {
		return false
	}
	if resourceTenantID != c.GetString("tenant_id") {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "无权访问其它租户的资源",
		})
		return true
	}
	return false
}

// tenantScopeForDiscovery resolves the effective tenant filter for v1 model-discovery endpoints.
func tenantScopeForDiscovery(c *gin.Context) (tenantID string, isRoot bool) {
	if c.GetInt("role") >= common.RoleRootUser {
		return "", true
	}
	tenantID = c.GetString("tenant_id")
	if tenantID == "" {
		tenantID = "default"
	}
	return tenantID, false
}

// filterChannelIdsByTenant restricts bulk channel ID slices to rows owned by tenantID, failing closed on lookup errors.
func filterChannelIdsByTenant(ids []int, tenantID string) []int {
	if len(ids) == 0 {
		return ids
	}
	channels, err := repo.GetChannelsByIds(ids)
	if err != nil {
		return nil
	}
	scoped := make([]int, 0, len(channels))
	for _, ch := range channels {
		if ch.TenantId == tenantID {
			scoped = append(scoped, ch.Id)
		}
	}
	return scoped
}
