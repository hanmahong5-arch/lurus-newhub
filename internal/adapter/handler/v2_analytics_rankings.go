package handler

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// rankingsHourPresets bounds the cache-key cardinality: an arbitrary `hours`
// query value is snapped to the nearest of these before it becomes part of
// the cache key AND the window actually served, so a caller sweeping
// hours=1..720 cannot mint a fresh cache entry (and a fresh two-window
// aggregate) on every request (cycle-7 plan §8/L4 "throttle and cache").
var rankingsHourPresets = []int{1, 6, 24, 168, 720}

const (
	rankingsDefaultHours = 24
	rankingsCacheTTL     = 5 * time.Minute
	rankingsDefaultLimit = 20
)

// snapRankingHours rounds an already-clamped [1,720] hours value to the
// nearest preset (ties resolve to the smaller preset).
func snapRankingHours(hours int) int {
	best := rankingsHourPresets[0]
	bestDiff := rankingsAbs(hours - best)
	for _, p := range rankingsHourPresets[1:] {
		if d := rankingsAbs(hours - p); d < bestDiff {
			best, bestDiff = p, d
		}
	}
	return best
}

func rankingsAbs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// rankingsCacheEntry is one cached response snapshot: the whole payload
// (window + rows) is cached, not just the rows, so a cache hit is byte-for-
// byte identical (including cached_at) until it expires.
type rankingsCacheEntry struct {
	cachedAt               int64
	windowStart, windowEnd int64
	rows                   []repo.RankingRow
}

// rankingsCache is per-pod (in-process sync.Map, no shared backing store):
// the 3 replicas can each report a different cached_at, which is documented
// in the plan's Risk paragraph as expected, not a bug.
var rankingsCache sync.Map // key: tenantID+"|"+by+"|"+hours -> *rankingsCacheEntry

func rankingsCacheKey(tenantID, by string, hours int) string {
	return tenantID + "|" + by + "|" + strconv.Itoa(hours)
}

// getCachedRankings serves repo.GetRankings through a 5-minute in-process
// cache keyed by (tenantID, by, hours). tenantID is "" for the root route's
// unfiltered (cross-tenant) view.
func getCachedRankings(tenantID, by string, hours int) (*rankingsCacheEntry, error) {
	key := rankingsCacheKey(tenantID, by, hours)
	if v, ok := rankingsCache.Load(key); ok {
		entry := v.(*rankingsCacheEntry)
		if time.Since(time.Unix(entry.cachedAt, 0)) < rankingsCacheTTL {
			return entry, nil
		}
	}

	end := time.Now().Unix()
	start := end - int64(hours)*3600
	rows, err := repo.GetRankings(start, end, tenantID, by, rankingsDefaultLimit)
	if err != nil {
		return nil, err
	}
	entry := &rankingsCacheEntry{
		cachedAt:    time.Now().Unix(),
		windowStart: start,
		windowEnd:   end,
		rows:        rows,
	}
	rankingsCache.Store(key, entry)
	return entry, nil
}

// parseRankingsParams reads and validates the `by`/`hours` query params
// shared by the root and tenant rankings routes. `by` defaults to (and
// falls back to, on any other value) "model"; `hours` is clamped to [1,720]
// then snapped to a cache-friendly preset.
func parseRankingsParams(c *gin.Context) (by string, hours int) {
	by = c.DefaultQuery("by", "model")
	if by != "model" && by != "vendor" {
		by = "model"
	}
	h, _ := strconv.Atoi(c.DefaultQuery("hours", strconv.Itoa(rankingsDefaultHours)))
	if h < 1 {
		h = 1
	}
	if h > 720 {
		h = 720
	}
	hours = snapRankingHours(h)
	return
}

func writeRankingsResponse(c *gin.Context, by string, entry *rankingsCacheEntry) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"by": by,
			"window": gin.H{
				"start": entry.windowStart,
				"end":   entry.windowEnd,
			},
			"cached_at": entry.cachedAt,
			"rows":      entry.rows,
		},
	})
}

// GetTenantRankingsV2 is the tenant-admin model/vendor leaderboard: rank,
// trend (rank_delta/is_new) and share (token_share_pct/quota_share_pct) per
// model or per channel-type vendor, scoped to the caller's own tenant.
//
// GET /api/v2/:tenant_slug/analytics/rankings?by=model|vendor&hours=1..720
//
// Tenant id comes from the resolved tenant context, never from the query —
// cross-tenant market share is impossible by construction (mirrors
// GetAllLogStatV2's requireTenantAdmin gate, v2_log_stat.go:87-113).
func GetTenantRankingsV2(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}
	if !requireTenantAdmin(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Admin role required",
		})
		return
	}

	by, hours := parseRankingsParams(c)
	entry, err := getCachedRankings(tenantCtx.TenantID, by, hours)
	if err != nil {
		common.SysError("GetTenantRankingsV2: aggregate failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to aggregate rankings",
		})
		return
	}
	writeRankingsResponse(c, by, entry)
}
