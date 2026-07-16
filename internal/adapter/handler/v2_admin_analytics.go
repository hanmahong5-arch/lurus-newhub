package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// modelPerformanceDefaultWindow is the lookback applied when the caller
// omits the start parameter.
const modelPerformanceDefaultWindow = 24 * time.Hour

// GetModelPerformanceV2 returns per-model performance aggregates from the
// logs table: request/error counts, error rate, token usage, quota, and
// latency avg/p50/p95 (total_latency_ms, nearest-rank percentiles).
//
// GET /api/v2/admin/analytics/model-performance?start=&end=&tenant_id=&model=
//
//	start     int64  — unix seconds lower bound (inclusive); default end-24h
//	end       int64  — unix seconds upper bound (inclusive); default now
//	tenant_id string — optional tenant filter (empty = all tenants)
//	model     string — optional exact model name filter
//
// Response: {"success":true,"data":{"start_time":N,"end_time":N,"models":[...]}}
// with models sorted by request count descending, capped at 200 entries.
//
// Root-only (router applies RootJWTAuth + CriticalRateLimit on the route).
func GetModelPerformanceV2(c *gin.Context) {
	now := time.Now().Unix()
	endTime, _ := strconv.ParseInt(c.DefaultQuery("end", "0"), 10, 64)
	if endTime <= 0 {
		endTime = now
	}
	startTime, _ := strconv.ParseInt(c.DefaultQuery("start", "0"), 10, 64)
	if startTime <= 0 {
		startTime = endTime - int64(modelPerformanceDefaultWindow.Seconds())
	}
	if startTime > endTime {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "start must not be after end",
		})
		return
	}

	tenantID := c.Query("tenant_id")
	modelName := c.Query("model")

	stats, err := repo.GetModelPerformance(startTime, endTime, tenantID, modelName)
	if err != nil {
		common.SysError("GetModelPerformanceV2: aggregate failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to aggregate model performance",
		})
		return
	}
	if stats == nil {
		stats = []repo.ModelPerformanceStat{}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"start_time": startTime,
			"end_time":   endTime,
			"models":     stats,
		},
	})
}
