package handler

// v2_models_performance.go — GET /api/v2/:tenant_slug/models/performance
// (cycle 16, P7): per-model latency and error rate over a recent window,
// scoped to the caller's tenant. The console's model marketplace shows it on
// each model the way openrouter.ai shows per-model latency/uptime.
//
// Until now the same aggregate (repo.GetModelPerformance) was reachable only
// from /api/v2/admin/analytics/model-performance (RootJWTAuth), so nobody
// choosing a model could see how it had been performing for their own
// tenant.
//
// Visibility: every member of the tenant sees QUALITY — p50/p95 latency,
// error rate, and whether the sample is large enough to mean anything. The
// VOLUME fields (request/error counts, tokens, spend, sample size) stay with
// tenant admins, for the same reason the tenant rankings are admin-only
// (GetTenantRankingsV2): they describe what everybody else in the tenant is
// doing.

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

const (
	modelPerfCacheTTL = 5 * time.Minute
	// Below this many latency samples a percentile is one or two requests'
	// worth of noise; the item says so instead of the console presenting it
	// as a measurement.
	modelPerfMinSamples = 20
)

// The only windows served. A fixed set bounds the cache-key cardinality the
// same way rankingsHourPresets does: every percentile is two extra queries
// per model, so an arbitrary `hours` would let a caller mint a fresh
// expensive aggregate per request.
var modelPerfHourPresets = map[int]bool{1: true, 24: true, 168: true}

type modelPerfCacheEntry struct {
	cachedAt int64
	stats    []repo.ModelPerformanceStat
}

// Per-pod, like rankingsCache: replicas may report different cached_at.
var modelPerfCache sync.Map // tenantID+"|"+hours -> *modelPerfCacheEntry

// modelPerfMemberView is what every tenant member gets.
type modelPerfMemberView struct {
	ModelName     string  `json:"model_name"`
	P50LatencyMs  int     `json:"p50_latency_ms"`
	P95LatencyMs  int     `json:"p95_latency_ms"`
	ErrorRate     float64 `json:"error_rate"`
	EnoughSamples bool    `json:"enough_samples"`
}

// modelPerfAdminView adds the volume fields.
type modelPerfAdminView struct {
	modelPerfMemberView
	Requests       int64 `json:"requests"`
	Errors         int64 `json:"errors"`
	TotalTokens    int64 `json:"total_tokens"`
	Quota          int64 `json:"quota"`
	LatencySamples int64 `json:"latency_samples"`
}

func getCachedModelPerf(tenantID string, hours int, now time.Time) (*modelPerfCacheEntry, error) {
	key := tenantID + "|" + strconv.Itoa(hours)
	if v, ok := modelPerfCache.Load(key); ok {
		e := v.(*modelPerfCacheEntry)
		if now.Unix()-e.cachedAt < int64(modelPerfCacheTTL/time.Second) {
			return e, nil
		}
	}
	end := now.Unix()
	start := end - int64(hours)*3600
	stats, err := repo.GetModelPerformance(start, end, tenantID, "")
	if err != nil {
		return nil, err
	}
	e := &modelPerfCacheEntry{cachedAt: end, stats: stats}
	modelPerfCache.Store(key, e)
	return e, nil
}

// ListModelPerformanceV2 handles GET /api/v2/:tenant_slug/models/performance
// ?hours=1|24|168 (default 24).
func ListModelPerformanceV2(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil || tenantCtx == nil || tenantCtx.TenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":    false,
			"message":    "Not authenticated",
			"error_code": "UNAUTHENTICATED",
		})
		return
	}
	hours := 24
	if raw := c.Query("hours"); raw != "" {
		h, perr := strconv.Atoi(raw)
		if perr != nil || !modelPerfHourPresets[h] {
			c.JSON(http.StatusBadRequest, gin.H{
				"success":    false,
				"message":    "hours must be one of 1, 24, 168",
				"error_code": "INVALID_WINDOW",
			})
			return
		}
		hours = h
	}

	entry, err := getCachedModelPerf(tenantCtx.TenantID, hours, time.Now())
	if err != nil {
		common.SysError("ListModelPerformanceV2: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to aggregate model performance",
		})
		return
	}

	admin := requireTenantAdmin(c, tenantCtx)
	items := make([]any, 0, len(entry.stats))
	for _, s := range entry.stats {
		m := modelPerfMemberView{
			ModelName:     s.ModelName,
			P50LatencyMs:  s.P50LatencyMs,
			P95LatencyMs:  s.P95LatencyMs,
			ErrorRate:     s.ErrorRate,
			EnoughSamples: s.LatencySamples >= modelPerfMinSamples,
		}
		if !admin {
			items = append(items, m)
			continue
		}
		items = append(items, modelPerfAdminView{
			modelPerfMemberView: m,
			Requests:            s.Requests,
			Errors:              s.Errors,
			TotalTokens:         s.TotalTokens,
			Quota:               s.Quota,
			LatencySamples:      s.LatencySamples,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"hours":       hours,
			"cached_at":   entry.cachedAt,
			"min_samples": modelPerfMinSamples,
			"items":       items,
		},
	})
}
