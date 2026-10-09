package handler

// Operations read models over the channel health function:
//
//   - channelOps: the extra list columns of GET /:tenant_slug/channels
//   - GET /api/v2/:tenant_slug/channels/health-summary
//
// Both derive "routable / reasons" from buildChannelHealthWin, the very
// function behind GET /:id/health, so the three views cannot disagree. Windows
// come from one batched snapshot read instead of one lookup per channel.

import (
	"net/http"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/planquota"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func init() {
	// Single-channel health reads the snapshot straight from the store.
	ChannelWindowProvider = planquotaWindow
}

// planquotaWindow is the production ChannelWindowProvider: the last plan-quota
// probe of the channel, in the same shape as other_info.plan_quota. The
// snapshot is account-level, so every key of a channel shares it.
func planquotaWindow(channelID, _ int) map[string]interface{} {
	snap, ok := planquota.LatestSnapshot(channelID)
	if !ok {
		return nil
	}
	return planquota.WindowView(snap)
}

// batchWindows prefetches the snapshots of ids with one Redis round trip and
// returns a windowFn that serves from that batch.
func batchWindows(ids []int) windowFn {
	snaps := planquota.LatestSnapshots(ids)
	return func(channelID, _ int) map[string]interface{} {
		return planquota.WindowView(snaps[channelID])
	}
}

// windowMaxUsedPct is the highest used_pct among a window description's
// windows; ok=false when there is none.
func windowMaxUsedPct(w map[string]interface{}) (float64, bool) {
	if w == nil {
		return 0, false
	}
	best, found := 0.0, false
	take := func(m map[string]interface{}) {
		if v, ok := m["used_pct"].(float64); ok && (!found || v > best) {
			best, found = v, true
		}
	}
	switch ws := w["windows"].(type) {
	case []map[string]interface{}:
		for _, m := range ws {
			take(m)
		}
	case []interface{}:
		for _, e := range ws {
			if m, ok := e.(map[string]interface{}); ok {
				take(m)
			}
		}
	}
	return best, found
}

// channelOps are the operations columns added to every channelView.
type channelOps struct {
	PlanKind          string   `json:"plan_kind"`
	ExpiresAt         int64    `json:"expires_at"`
	KeyCount          int      `json:"key_count"`
	EnabledKeyCount   int      `json:"enabled_key_count"`
	Routable          bool     `json:"routable"`
	UnroutableReasons []string `json:"unroutable_reasons"`
}

func enabledKeyCount(ch *repo.Channel) int {
	n := 0
	for i := 0; i < channelKeyCount(ch); i++ {
		if snapshotKeyState(ch, i).KeyStatus == common.ChannelStatusEnabled {
			n++
		}
	}
	return n
}

func buildChannelOps(ch *repo.Channel, h channelHealth) channelOps {
	set := ch.GetSetting()
	reasons := h.Reasons
	if reasons == nil || h.Routable {
		reasons = []string{}
	}
	return channelOps{
		PlanKind:          planquota.NormalizeKind(set.PlanKind),
		ExpiresAt:         channelEarliestExpiry(ch),
		KeyCount:          channelKeyCount(ch),
		EnabledKeyCount:   enabledKeyCount(ch),
		Routable:          h.Routable,
		UnroutableReasons: reasons,
	}
}

// channelOpsFor evaluates the ops columns for a page of channels (rows loaded
// WITHOUT keys). It re-reads the same ids with keys in one query, refreshes
// the cooldown snapshot once and batches the window snapshots.
func channelOpsFor(tenantID string, page []*repo.Channel) map[int]channelOps {
	out := make(map[int]channelOps, len(page))
	if len(page) == 0 {
		return out
	}
	ids := make([]int, len(page))
	for i, ch := range page {
		ids[i] = ch.Id
	}
	full, err := repo.ListTenantChannelsWithKeys(tenantID, ids, 0)
	if err != nil {
		common.SysError("channel ops columns: " + err.Error())
		return out
	}
	app.RefreshChannelCooldownSnapshot()
	win := batchWindows(ids)
	now := time.Now().Unix()
	for _, ch := range full {
		out[ch.Id] = buildChannelOps(ch, buildChannelHealthWin(ch, now, win))
	}
	return out
}

// maxHealthSummaryChannels bounds one summary response.
const maxHealthSummaryChannels = 5000

type channelHealthSummaryItem struct {
	ID               int      `json:"id"`
	Name             string   `json:"name"`
	Routable         bool     `json:"routable"`
	Reasons          []string `json:"reasons"`
	ExpiresAt        int64    `json:"expires_at"`
	WindowMaxUsedPct *float64 `json:"window_max_used_pct"`
	CooldownUntil    int64    `json:"cooldown_until,omitempty"`
	LastError        string   `json:"last_error,omitempty"`
}

// GetChannelHealthSummaryV2 returns the health roll-up of every channel of the
// tenant in one call (platform staff only).
//
// Route: GET /api/v2/:tenant_slug/channels/health-summary
func GetChannelHealthSummaryV2(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Tenant context not found"})
		return
	}
	if !isPlatformStaff(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Admin role required"})
		return
	}
	chans, err := repo.ListTenantChannelsWithKeys(tenantCtx.TenantID, nil, maxHealthSummaryChannels+1)
	if err != nil {
		common.SysError("channel health summary: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to get channels"})
		return
	}
	truncated := len(chans) > maxHealthSummaryChannels
	if truncated {
		chans = chans[:maxHealthSummaryChannels]
	}
	ids := make([]int, len(chans))
	for i, ch := range chans {
		ids[i] = ch.Id
	}
	app.RefreshChannelCooldownSnapshot()
	win := batchWindows(ids)
	now := time.Now().Unix()
	items := make([]channelHealthSummaryItem, 0, len(chans))
	for _, ch := range chans {
		h := buildChannelHealthWin(ch, now, win)
		it := channelHealthSummaryItem{
			ID: ch.Id, Name: ch.Name, Routable: h.Routable,
			Reasons: h.Reasons, ExpiresAt: channelEarliestExpiry(ch),
			CooldownUntil: h.CooldownUntil, LastError: h.LastError,
		}
		if it.Reasons == nil {
			it.Reasons = []string{}
		}
		if v, ok := windowMaxUsedPct(h.Window); ok {
			it.WindowMaxUsedPct = &v
		}
		items = append(items, it)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"channels": items, "total": len(items), "truncated": truncated,
	}})
}

// opsOrDefault returns the evaluated ops columns, or a fail-safe "not routable,
// unknown" row when the evaluation could not run (never a false routable).
func opsOrDefault(m map[int]channelOps, ch *repo.Channel) channelOps {
	if o, ok := m[ch.Id]; ok {
		return o
	}
	return channelOps{UnroutableReasons: []string{}, PlanKind: planquota.NormalizeKind(ch.GetSetting().PlanKind), ExpiresAt: channelEarliestExpiry(ch)}
}

// ListChannelTemplateApplicationsV2 handles
// GET /admin/channel-templates/:id/applications?page=&page_size= : the
// append-only log of which channels this template was written into, newest
// first (platform staff, RootJWTAuth like the sibling template routes).
func ListChannelTemplateApplicationsV2(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		dpFail(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid template id")
		return
	}
	if _, err := repo.GetChannelOverrideTemplate(id); err != nil {
		templateStatus(c, err, "load channel template")
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	rows, total, err := repo.ListChannelTemplateApplications(id, (page-1)*pageSize, pageSize)
	if err != nil {
		templateStatus(c, err, "list template applications")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"applications": rows, "total": total, "page": page, "page_size": pageSize,
	}})
}

// channelEarliestExpiry is the soonest declared end of a channel: the plan end
// in its setting or any per-key end (0 = none declared).
func channelEarliestExpiry(ch *repo.Channel) int64 {
	best := ch.GetSetting().ExpiresAt
	for _, m := range ch.ChannelInfo.MultiKeyMeta {
		if m.ExpiresAt > 0 && (best <= 0 || m.ExpiresAt < best) {
			best = m.ExpiresAt
		}
	}
	if best < 0 {
		return 0
	}
	return best
}
