package handler

import (
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// GetAllChannels lists paginated channels scoped to the caller's tenant (or globally for root).
func GetAllChannels(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	channelData := make([]*repo.Channel, 0)
	idSort, _ := strconv.ParseBool(c.Query("id_sort"))
	enableTagMode, _ := strconv.ParseBool(c.Query("tag_mode"))
	statusFilter := parseStatusFilter(c.Query("status"))
	typeFilter := parseOptionalTypeFilter(c.Query("type"))
	isRoot := c.GetInt("role") >= common.RoleRootUser
	callerTenant := c.GetString("tenant_id")

	var total int64
	if enableTagMode {
		items, tagTotal, err := queryTagModeChannels(pageInfo, isRoot, callerTenant, idSort, statusFilter, typeFilter)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		channelData = items
		total = tagTotal
	} else {
		items, rowTotal, err := queryPagedChannels(pageInfo, isRoot, callerTenant, idSort, statusFilter, typeFilter)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		channelData = items
		total = rowTotal
	}

	for _, datum := range channelData {
		clearChannelInfo(datum)
	}

	typeCounts := queryChannelTypeCounts(isRoot, callerTenant, statusFilter)
	common.ApiSuccess(c, gin.H{
		"items":       channelData,
		"total":       total,
		"page":        pageInfo.GetPage(),
		"page_size":   pageInfo.GetPageSize(),
		"type_counts": typeCounts,
	})
}

func parseOptionalTypeFilter(raw string) int {
	if raw == "" {
		return -1
	}
	if t, err := strconv.Atoi(raw); err == nil {
		return t
	}
	return -1
}

func queryTagModeChannels(
	pageInfo *common.PageInfo,
	isRoot bool,
	callerTenant string,
	idSort bool,
	statusFilter int,
	typeFilter int,
) ([]*repo.Channel, int64, error) {
	var tags []*string
	var err error
	if isRoot {
		tags, err = repo.GetPaginatedTags(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	} else {
		tags, err = repo.GetPaginatedTagsByTenant(callerTenant, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	}
	if err != nil {
		return nil, 0, err
	}
	channelData := make([]*repo.Channel, 0)
	for _, tag := range tags {
		if tag == nil || *tag == "" {
			continue
		}
		tagChannels, qErr := repo.GetChannelsByTag(*tag, idSort, false)
		if qErr != nil {
			continue
		}
		for _, ch := range tagChannels {
			if matchesChannelListFilter(ch, isRoot, callerTenant, statusFilter, typeFilter) {
				channelData = append(channelData, ch)
			}
		}
	}
	var total int64
	if isRoot {
		total, _ = repo.CountAllTags()
	} else {
		total, _ = repo.CountAllTagsByTenant(callerTenant)
	}
	return channelData, total, nil
}

func matchesChannelListFilter(ch *repo.Channel, isRoot bool, callerTenant string, statusFilter, typeFilter int) bool {
	if !isRoot && ch.TenantId != callerTenant {
		return false
	}
	if statusFilter == common.ChannelStatusEnabled && ch.Status != common.ChannelStatusEnabled {
		return false
	}
	if statusFilter == 0 && ch.Status == common.ChannelStatusEnabled {
		return false
	}
	if typeFilter >= 0 && ch.Type != typeFilter {
		return false
	}
	return true
}

func queryPagedChannels(
	pageInfo *common.PageInfo,
	isRoot bool,
	callerTenant string,
	idSort bool,
	statusFilter int,
	typeFilter int,
) ([]*repo.Channel, int64, error) {
	baseQuery := repo.DB.Model(&repo.Channel{})
	if !isRoot {
		baseQuery = baseQuery.Where("tenant_id = ?", callerTenant)
	}
	if typeFilter >= 0 {
		baseQuery = baseQuery.Where("type = ?", typeFilter)
	}
	if statusFilter == common.ChannelStatusEnabled {
		baseQuery = baseQuery.Where("status = ?", common.ChannelStatusEnabled)
	} else if statusFilter == 0 {
		baseQuery = baseQuery.Where("status != ?", common.ChannelStatusEnabled)
	}

	var total int64
	baseQuery.Count(&total)

	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	channelData := make([]*repo.Channel, 0)
	err := baseQuery.Order(order).Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Omit("key").Find(&channelData).Error
	return channelData, total, err
}

func queryChannelTypeCounts(isRoot bool, callerTenant string, statusFilter int) map[int64]int64 {
	countQuery := repo.DB.Model(&repo.Channel{})
	if !isRoot {
		countQuery = countQuery.Where("tenant_id = ?", callerTenant)
	}
	if statusFilter == common.ChannelStatusEnabled {
		countQuery = countQuery.Where("status = ?", common.ChannelStatusEnabled)
	} else if statusFilter == 0 {
		countQuery = countQuery.Where("status != ?", common.ChannelStatusEnabled)
	}
	var results []struct {
		Type  int64
		Count int64
	}
	_ = countQuery.Select("type, count(*) as count").Group("type").Find(&results).Error
	typeCounts := make(map[int64]int64, len(results))
	for _, r := range results {
		typeCounts[r.Type] = r.Count
	}
	return typeCounts
}

// GetChannel returns metadata for a single channel within the caller's tenant scope.
func GetChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channel, err := repo.GetChannelById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if enforceTenantScope(c, channel.TenantId) {
		return
	}
	if channel != nil {
		clearChannelInfo(channel)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channel,
	})
}

// FixChannelsAbilities rebuilds the channel routing ability table.
func FixChannelsAbilities(c *gin.Context) {
	success, fails, err := repo.FixAbility()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"success": success,
			"fails":   fails,
		},
	})
}
