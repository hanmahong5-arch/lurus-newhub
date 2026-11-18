package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/search"
	"github.com/gin-gonic/gin"
)

// SearchChannels searches channels across tag mode, Meilisearch (root-only), or tenant-scoped SQL.
func SearchChannels(c *gin.Context) {
	keyword := c.Query("keyword")
	group := c.Query("group")
	modelKeyword := c.Query("model")
	statusFilter := parseStatusFilter(c.Query("status"))
	idSort, _ := strconv.ParseBool(c.Query("id_sort"))
	enableTagMode, _ := strconv.ParseBool(c.Query("tag_mode"))
	isRoot := c.GetInt("role") >= common.RoleRootUser
	callerTenant := c.GetString("tenant_id")

	channelData, err := loadSearchedChannels(enableTagMode, isRoot, callerTenant, keyword, group, modelKeyword, statusFilter, idSort)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	if !isRoot {
		scoped := make([]*repo.Channel, 0, len(channelData))
		for _, ch := range channelData {
			if ch.TenantId == callerTenant {
				scoped = append(scoped, ch)
			}
		}
		channelData = scoped
	}

	channelData = applyStatusSliceFilter(channelData, statusFilter)
	typeCounts := make(map[int64]int64)
	for _, channel := range channelData {
		typeCounts[int64(channel.Type)]++
	}

	typeFilter := parseOptionalTypeFilter(c.Query("type"))
	if typeFilter >= 0 {
		filtered := make([]*repo.Channel, 0, len(channelData))
		for _, ch := range channelData {
			if ch.Type == typeFilter {
				filtered = append(filtered, ch)
			}
		}
		channelData = filtered
	}

	pagedData, total := paginateChannelSlice(channelData, c.DefaultQuery("p", "1"), c.DefaultQuery("page_size", "20"))
	for _, datum := range pagedData {
		clearChannelInfo(datum)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items":       pagedData,
			"total":       total,
			"type_counts": typeCounts,
		},
	})
}

func loadSearchedChannels(
	enableTagMode bool,
	isRoot bool,
	callerTenant, keyword, group, modelKeyword string,
	statusFilter int,
	idSort bool,
) ([]*repo.Channel, error) {
	if enableTagMode {
		tags, err := repo.SearchTags(keyword, group, modelKeyword, idSort)
		if err != nil {
			return nil, err
		}
		out := make([]*repo.Channel, 0)
		for _, tag := range tags {
			if tag != nil && *tag != "" {
				if tagChannel, tErr := repo.GetChannelsByTag(*tag, idSort, false); tErr == nil {
					out = append(out, tagChannel...)
				}
			}
		}
		return out, nil
	}

	if isRoot && search.IsEnabled() && statusFilter != 0 {
		status := 0
		if statusFilter == common.ChannelStatusEnabled {
			status = common.ChannelStatusEnabled
		}
		results, _, err := search.SearchChannels(keyword, group, status, 1, 10000)
		if err == nil {
			return decodeMeilisearchChannels(results), nil
		}
		common.SysLog(fmt.Sprintf("Meilisearch search failed, falling back to database: %v", err))
		return repo.SearchChannels(keyword, group, modelKeyword, idSort)
	}

	if isRoot {
		return repo.SearchChannels(keyword, group, modelKeyword, idSort)
	}
	return repo.SearchChannelsByTenant(callerTenant, keyword, group, modelKeyword, idSort)
}

func decodeMeilisearchChannels(results []map[string]any) []*repo.Channel {
	channels := make([]*repo.Channel, 0, len(results))
	for _, result := range results {
		channel := &repo.Channel{}
		if id, ok := result["id"].(float64); ok {
			channel.Id = int(id)
		}
		if typeVal, ok := result["type"].(float64); ok {
			channel.Type = int(typeVal)
		}
		if name, ok := result["name"].(string); ok {
			channel.Name = name
		}
		if baseURL, ok := result["base_url"].(string); ok {
			channel.BaseURL = &baseURL
		}
		if models, ok := result["models"].(string); ok {
			channel.Models = models
		}
		if groupVal, ok := result["group"].(string); ok {
			channel.Group = groupVal
		}
		if tag, ok := result["tag"].(string); ok {
			channel.Tag = &tag
		}
		if statusVal, ok := result["status"].(float64); ok {
			channel.Status = int(statusVal)
		}
		if priority, ok := result["priority"].(float64); ok {
			priorityVal := int64(priority)
			channel.Priority = &priorityVal
		}
		if balance, ok := result["balance"].(float64); ok {
			channel.Balance = balance
		}
		if testTime, ok := result["test_time"].(float64); ok {
			channel.TestTime = int64(testTime)
		}
		channels = append(channels, channel)
	}
	return channels
}

func applyStatusSliceFilter(channels []*repo.Channel, statusFilter int) []*repo.Channel {
	if statusFilter != common.ChannelStatusEnabled && statusFilter != 0 {
		return channels
	}
	filtered := make([]*repo.Channel, 0, len(channels))
	for _, ch := range channels {
		if statusFilter == common.ChannelStatusEnabled && ch.Status != common.ChannelStatusEnabled {
			continue
		}
		if statusFilter == 0 && ch.Status == common.ChannelStatusEnabled {
			continue
		}
		filtered = append(filtered, ch)
	}
	return filtered
}

func paginateChannelSlice(items []*repo.Channel, pageRaw, pageSizeRaw string) ([]*repo.Channel, int) {
	page, _ := strconv.Atoi(pageRaw)
	pageSize, _ := strconv.Atoi(pageSizeRaw)
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	total := len(items)
	startIdx := (page - 1) * pageSize
	if startIdx > total {
		startIdx = total
	}
	endIdx := startIdx + pageSize
	if endIdx > total {
		endIdx = total
	}
	return items[startIdx:endIdx], total
}
