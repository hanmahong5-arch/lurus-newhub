package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// logClusterRow is the view model returned per aggregated row.
type logClusterRow struct {
	ModelName string `json:"model_name"`
	ErrorCode string `json:"error_code"`
	Bucket    string `json:"bucket"`
	Count     int64  `json:"count"`
}

// GetLogClusterV2 aggregates log rows by model_name + error_code + time bucket.
// Route: GET /api/v2/:tenant_slug/logs/cluster?bucket=hour|day&from=<unix>&to=<unix>
func GetLogClusterV2(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	bucket := c.DefaultQuery("bucket", "hour")
	if bucket != "hour" && bucket != "day" {
		bucket = "hour"
	}

	now := time.Now().Unix()
	var defaultFrom int64
	switch bucket {
	case "day":
		defaultFrom = now - 7*24*3600
	default:
		defaultFrom = now - 24*3600
	}

	from := queryInt64(c, "from", -1)
	if from < 0 {
		from = defaultFrom
	}
	to := queryInt64(c, "to", -1)
	if to < 0 {
		to = now
	}

	// Build time-bucket expression compatible with SQLite and PostgreSQL.
	// The SQLite arm exists only for the hermetic glebarez unit-test tier;
	// runtime is always PostgreSQL.
	var bucketExpr string
	if common.UsingPostgreSQL {
		if bucket == "day" {
			bucketExpr = "TO_CHAR(TO_TIMESTAMP(created_at), 'YYYY-MM-DD')"
		} else {
			bucketExpr = "TO_CHAR(DATE_TRUNC('hour', TO_TIMESTAMP(created_at)), 'YYYY-MM-DD HH24:00')"
		}
	} else {
		// SQLite
		if bucket == "day" {
			bucketExpr = "strftime('%Y-%m-%d', datetime(created_at, 'unixepoch'))"
		} else {
			bucketExpr = "strftime('%Y-%m-%d %H:00', datetime(created_at, 'unixepoch'))"
		}
	}

	selectSQL := "model_name, COALESCE(content, '') as error_code, " + bucketExpr + " as bucket, COUNT(*) as count"
	groupSQL := "model_name, COALESCE(content, ''), " + bucketExpr

	type rawRow struct {
		ModelName string `gorm:"column:model_name"`
		ErrorCode string `gorm:"column:error_code"`
		Bucket    string `gorm:"column:bucket"`
		Count     int64  `gorm:"column:count"`
	}

	// This endpoint reports ERROR clusters: without the `type = ?` predicate
	// below, the query previously aggregated every log row regardless of
	// type — including LogTypeConsume (successful, billed calls) — and
	// labeled their Content as an "error_code". repo.LogTypeError (5) is the
	// only type whose Content is actually an error string; see
	// internal/adapter/repo/log.go:299 (the sole writer that sets Type:
	// LogTypeError).
	var rows []rawRow
	err = repo.LOG_DB.Model(&repo.Log{}).
		Select(selectSQL).
		Where("tenant_id = ? AND user_id = ? AND type = ? AND created_at >= ? AND created_at <= ?",
			tenantCtx.TenantID, tenantCtx.UserID, repo.LogTypeError, from, to).
		Group(groupSQL).
		Order("count DESC").
		Find(&rows).Error
	if err != nil {
		common.SysError("GetLogClusterV2: query failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to aggregate log clusters",
		})
		return
	}

	items := make([]logClusterRow, len(rows))
	for i, r := range rows {
		items[i] = logClusterRow{
			ModelName: r.ModelName,
			ErrorCode: r.ErrorCode,
			Bucket:    r.Bucket,
			Count:     r.Count,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":  items,
			"total":  len(items),
			"bucket": bucket,
		},
	})
}

// queryInt64 parses a query param as int64, returning defaultVal on missing / parse error.
func queryInt64(c *gin.Context, key string, defaultVal int64) int64 {
	raw := c.Query(key)
	if raw == "" {
		return defaultVal
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return defaultVal
	}
	return v
}
