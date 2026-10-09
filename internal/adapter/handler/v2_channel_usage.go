package handler

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/channelusage"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// maxSummaryChannels bounds the usage-summary channel scan; a tenant with more
// channels than this gets the first page by id and truncated=true.
const maxSummaryChannels = 2000

// usageStaffGate resolves the tenant context and refuses non-platform-staff
// callers. It writes the error response itself and returns ok=false.
func usageStaffGate(c *gin.Context) (*middleware.TenantContext, bool) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Tenant context not found"})
		return nil, false
	}
	if !isPlatformStaff(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Admin role required"})
		return nil, false
	}
	return tenantCtx, true
}

func utilizationOrNil(cost, fee int64, w channelusage.Window) *float64 {
	if r, ok := channelusage.Utilization(cost, fee, w); ok {
		return &r
	}
	return nil
}

// GetChannelUsageV2 serves GET /api/v2/:tenant_slug/channels/:id/usage?window=.
// Usage per upstream key index plus the channel total; utilization (window
// cost / prorated plan fee) only when the channel carries a plan fee.
func GetChannelUsageV2(c *gin.Context) {
	tenantCtx, ok := usageStaffGate(c)
	if !ok {
		return
	}
	w, ok := channelusage.ParseWindow(c.Query("window"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "window must be one of 5h, 24h, 7d, 30d"})
		return
	}
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid channel ID"})
		return
	}
	channel, err := repo.GetChannelById(channelID, false)
	if err != nil || channel == nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Channel not found"})
		return
	}
	if channel.TenantId != tenantCtx.TenantID {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Access denied"})
		return
	}

	since := time.Now().Add(-w.Duration).Unix()
	rows, err := repo.AggregateChannelUsage([]int{channel.Id}, since, true)
	if err != nil {
		common.SysError("GetChannelUsageV2: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to aggregate usage"})
		return
	}

	byKey := make(map[int64]channelusage.KeyRow, len(rows))
	var total channelusage.Totals
	for _, r := range rows {
		t := channelusage.Totals{
			Requests: r.Requests, Errors: r.Errors,
			PromptTokens: r.PromptTokens, CompletionTokens: r.CompletionTokens,
			Quota: r.Quota, CostCNY4: r.CostCNY4,
		}
		byKey[r.KeyIdx] = channelusage.KeyRow{KeyIdx: r.KeyIdx, Totals: t}
		total.Add(t)
	}
	// A multi-key channel lists every key it has, used or not: an idle key is
	// exactly what the operator is looking for.
	if channel.ChannelInfo.IsMultiKey {
		for i := 0; i < channel.ChannelInfo.MultiKeySize; i++ {
			if _, seen := byKey[int64(i)]; !seen {
				byKey[int64(i)] = channelusage.KeyRow{KeyIdx: int64(i)}
			}
		}
	} else if _, seen := byKey[repo.NoChannelKeyIdx]; !seen {
		byKey[repo.NoChannelKeyIdx] = channelusage.KeyRow{KeyIdx: repo.NoChannelKeyIdx}
	}
	keys := make([]channelusage.KeyRow, 0, len(byKey))
	for _, k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].KeyIdx < keys[j].KeyIdx })

	fee := channel.GetSetting().PlanMonthlyFeeCNY4
	data := gin.H{
		"channel_id":            channel.Id,
		"window":                w.Name,
		"since":                 since,
		"keys":                  keys,
		"total":                 total,
		"plan_monthly_fee_cny4": fee,
	}
	if u := utilizationOrNil(total.CostCNY4, fee, w); u != nil {
		data["utilization"] = *u
		data["prorated_fee_cny4"] = channelusage.ProratedFeeCNY4(fee, w)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// GetChannelUsageSummaryV2 serves GET /api/v2/:tenant_slug/channels/usage-summary:
// every channel of the tenant over 30 days, plan-fee channels first by
// utilization (overused on top), each tagged idle / overuse.
func GetChannelUsageSummaryV2(c *gin.Context) {
	tenantCtx, ok := usageStaffGate(c)
	if !ok {
		return
	}
	w, _ := channelusage.ParseWindow(channelusage.SummaryWindow)

	channels, err := repo.GetChannelsByTenant(tenantCtx.TenantID, 0, maxSummaryChannels+1, true)
	if err != nil {
		common.SysError("GetChannelUsageSummaryV2: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to list channels"})
		return
	}
	truncated := len(channels) > maxSummaryChannels
	if truncated {
		channels = channels[:maxSummaryChannels]
	}
	ids := make([]int, 0, len(channels))
	for _, ch := range channels {
		ids = append(ids, ch.Id)
	}
	since := time.Now().Add(-w.Duration).Unix()
	usage, err := repo.AggregateChannelUsage(ids, since, false)
	if err != nil {
		common.SysError("GetChannelUsageSummaryV2: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to aggregate usage"})
		return
	}
	byChannel := make(map[int]repo.ChannelUsageRow, len(usage))
	for _, r := range usage {
		byChannel[r.ChannelId] = r
	}

	rows := make([]channelusage.SummaryRow, 0, len(channels))
	for _, ch := range channels {
		r := byChannel[ch.Id]
		fee := ch.GetSetting().PlanMonthlyFeeCNY4
		row := channelusage.SummaryRow{
			ChannelId: ch.Id, Name: ch.Name, Status: ch.Status,
			Totals: channelusage.Totals{
				Requests: r.Requests, Errors: r.Errors,
				PromptTokens: r.PromptTokens, CompletionTokens: r.CompletionTokens,
				Quota: r.Quota, CostCNY4: r.CostCNY4,
			},
			PlanMonthlyFeeCNY4: fee,
			Utilization:        utilizationOrNil(r.CostCNY4, fee, w),
		}
		if row.Utilization != nil {
			row.Verdict = channelusage.Classify(*row.Utilization, true)
		}
		rows = append(rows, row)
	}
	channelusage.SortSummary(rows)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"window": w.Name, "since": since, "channels": rows, "truncated": truncated,
	}})
}
