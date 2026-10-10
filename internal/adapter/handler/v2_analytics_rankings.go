package handler

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
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
// (window + rows + totals) is cached, not just the rows, so a cache hit is
// byte-for-byte identical (including cached_at) until it expires.
type rankingsCacheEntry struct {
	cachedAt                int64
	windowStart, windowEnd  int64
	rows                    []repo.RankingRow
	totalTokens, totalQuota int64
	// series is hourly usage for exactly the names in rows (the chart above
	// the leaderboard); fetched with the rows, cached with them.
	series []repo.RankingSeriesPoint
}

// rankingsCache is per-pod (in-process sync.Map, no shared backing store):
// replicas can each report a different cached_at, which is documented in
// the plan's Risk paragraph as expected, not a bug.
var rankingsCache sync.Map // key: tenantID+"|"+by+"|"+hours -> *rankingsCacheEntry

// resetRankingsCacheForTest wipes rankingsCache so a test's first call is a
// proven miss regardless of test order or -count (mirrors
// resetWebSearchClientForTesting's pattern in web_search.go).
func resetRankingsCacheForTest() {
	rankingsCache.Range(func(k, _ any) bool {
		rankingsCache.Delete(k)
		return true
	})
}

func rankingsCacheKey(tenantID, by string, hours int, projectIDs []int) string {
	key := tenantID + "|" + by + "|" + strconv.Itoa(hours)
	if projectIDs != nil {
		// A department lead's board must never be served from (or into) the
		// tenant-wide entry: the project set is part of the key.
		ids := slices.Clone(projectIDs)
		slices.Sort(ids)
		parts := make([]string, len(ids))
		for i, id := range ids {
			parts[i] = strconv.Itoa(id)
		}
		key += "|p:" + strings.Join(parts, ",")
	}
	return key
}

// getCachedRankings serves repo.GetRankings through a 5-minute in-process
// cache keyed by (tenantID, by, hours). tenantID is "" for the root route's
// unfiltered (cross-tenant) view.
func getCachedRankings(tenantID, by string, hours int) (*rankingsCacheEntry, error) {
	return getCachedRankingsForProjects(tenantID, by, hours, nil)
}

// getCachedRankingsForProjects is getCachedRankings restricted to a project
// set (nil = unrestricted; empty non-nil = nothing).
func getCachedRankingsForProjects(tenantID, by string, hours int, projectIDs []int) (*rankingsCacheEntry, error) {
	key := rankingsCacheKey(tenantID, by, hours, projectIDs)
	if v, ok := rankingsCache.Load(key); ok {
		entry := v.(*rankingsCacheEntry)
		if time.Since(time.Unix(entry.cachedAt, 0)) < rankingsCacheTTL {
			return entry, nil
		}
	}

	end := time.Now().Unix()
	start := end - int64(hours)*3600
	rows, totalTokens, totalQuota, err := repo.GetRankingsForProjects(start, end, tenantID, by, rankingsDefaultLimit, projectIDs)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Name
	}
	series, err := repo.GetRankingSeriesForProjects(start, end, tenantID, by, names, projectIDs)
	if err != nil {
		return nil, err
	}
	entry := &rankingsCacheEntry{
		cachedAt:    time.Now().Unix(),
		windowStart: start,
		windowEnd:   end,
		rows:        rows,
		totalTokens: totalTokens,
		totalQuota:  totalQuota,
		series:      series,
	}
	rankingsCache.Store(key, entry)
	return entry, nil
}

// parseRankingsParams reads and validates the `by`/`hours` query params
// shared by the root and tenant rankings routes. `by` must be one of
// rankingDimensions — any other value is rejected with a non-empty
// errMsg (unknown `by` used to fall back to "model" silently, which let a
// typo mint an unbounded number of cache-key dimensions; now the caller
// must ask for one of the dimensions the leaderboard actually supports).
// `hours` is clamped to [1,720] then snapped to a cache-friendly preset; an
// unparsable `hours` (empty string, non-integer) falls back to
// rankingsDefaultHours rather than silently clamping to the 1-hour preset.
// rankingDimensions is the closed set of leaderboard dimensions (see
// repo.GetRankings). key/user/product answer "who and what is spending":
// per API key, per member, per calling product (X-Lurus-Product).
var rankingDimensions = map[string]bool{
	"model": true, "vendor": true, "group": true,
	"key": true, "user": true, "product": true,
}

func parseRankingsParams(c *gin.Context) (by string, hours int, errMsg string) {
	by = c.DefaultQuery("by", "model")
	if !rankingDimensions[by] {
		return "", 0, "by must be model, vendor, group, key, user or product"
	}
	h, atoiErr := strconv.Atoi(c.DefaultQuery("hours", strconv.Itoa(rankingsDefaultHours)))
	if atoiErr != nil {
		h = rankingsDefaultHours
	}
	if h < 1 {
		h = 1
	}
	if h > 720 {
		h = 720
	}
	hours = snapRankingHours(h)
	return by, hours, ""
}

func writeRankingsResponse(c *gin.Context, by string, hours int, entry *rankingsCacheEntry) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"by":    by,
			"hours": hours,
			"window": gin.H{
				"start": entry.windowStart,
				"end":   entry.windowEnd,
			},
			"cached_at":    entry.cachedAt,
			"rows":         entry.rows,
			"total_tokens": entry.totalTokens,
			"total_quota":  entry.totalQuota,
			"series":       entry.series,
		},
	})
}

// GetTenantRankingsV2 is the tenant-admin model/vendor/group leaderboard:
// rank, trend (rank_delta/is_new) and share (token_share_pct/quota_share_pct)
// per model, per channel-type vendor, or per logs.group, scoped to the
// caller's own tenant.
//
// GET /api/v2/:tenant_slug/analytics/rankings?by=model|vendor|group|key|user|product&hours=1..720
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
	// Tenant admins see the whole tenant; a department lead sees the same board
	// restricted to their projects; everyone else is refused.
	scope := resolveLogReadScope(c, tenantCtx)
	if !requireTenantAdmin(c, tenantCtx) && !scope.deptLead {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Admin role required",
		})
		return
	}

	by, hours, errMsg := parseRankingsParams(c)
	if errMsg != "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": errMsg,
		})
		return
	}
	// The vendor dimension maps channel_type to upstream vendor names, i.e. it
	// reveals our supply chain; customer tenant admins never get it.
	if by == "vendor" && !isPlatformStaff(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Platform staff role required for by=vendor",
		})
		return
	}
	entry, err := getCachedRankingsForProjects(tenantCtx.TenantID, by, hours, scope.projects())
	if err != nil {
		common.SysError("GetTenantRankingsV2: aggregate failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to aggregate rankings",
		})
		return
	}
	writeRankingsResponse(c, by, hours, entry)
}
