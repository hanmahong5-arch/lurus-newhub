package handler

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

const (
	// adminExportHardMaxRows is the absolute row cap for one admin export
	// response; requests above it are clamped and flagged via X-Truncated.
	adminExportHardMaxRows = 100_000
	// adminExportBatchSize is the number of rows fetched per DB round-trip.
	adminExportBatchSize = 1_000
)

// adminLogCSVHeader lists the whitelisted columns, in declaration order.
// Deliberately excluded: content (may carry prompt previews under governance
// log_detail_level=full) and other (raw internal JSON blob) — the admin
// export is a usage report, not a data dump. Token *names* are safe; token
// keys never live in the logs table.
var adminLogCSVHeader = []string{
	"id",
	"created_at",
	"tenant_id",
	"user_id",
	"username",
	"log_type",
	"model_name",
	"upstream_model",
	"token_name",
	"prompt_tokens",
	"completion_tokens",
	"quota",
	"use_time",
	"total_latency_ms",
	"is_stream",
	"channel_id",
	"group",
	"ip",
}

// ExportAdminLogsV2 streams platform-wide usage logs as CSV for reporting.
//
// GET /api/v2/admin/logs/export?format=csv&tenant_id=&type=&model_name=&start_time=&end_time=&max_rows=
//
//	format      string — only "csv" is supported (default csv)
//	tenant_id   string — optional tenant filter (empty = all tenants)
//	type        int    — log type filter (0 = all)
//	model_name  string — exact model name filter
//	start_time  int64  — unix seconds lower bound (inclusive)
//	end_time    int64  — unix seconds upper bound (inclusive)
//	max_rows    int    — row cap; clamped to 100000 if higher
//
// When more rows match than the cap, the response carries X-Truncated: true
// (plus X-Total-Matched with the full match count). Headers are computed via
// a COUNT before streaming because they cannot change once the body starts.
//
// Root-only (router applies RootJWTAuth + CriticalRateLimit on the route).
func ExportAdminLogsV2(c *gin.Context) {
	format := c.DefaultQuery("format", "csv")
	if format != "csv" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "format must be csv",
		})
		return
	}

	tenantID := c.Query("tenant_id")
	logType, _ := strconv.Atoi(c.DefaultQuery("type", "0"))
	modelName := c.Query("model_name")
	startTime, _ := strconv.ParseInt(c.DefaultQuery("start_time", "0"), 10, 64)
	endTime, _ := strconv.ParseInt(c.DefaultQuery("end_time", "0"), 10, 64)

	maxRows, _ := strconv.Atoi(c.DefaultQuery("max_rows", strconv.Itoa(adminExportHardMaxRows)))
	if maxRows <= 0 || maxRows > adminExportHardMaxRows {
		maxRows = adminExportHardMaxRows
	}

	totalMatched, err := repo.CountAdminExportLogs(tenantID, logType, modelName, startTime, endTime)
	if err != nil {
		common.SysError("ExportAdminLogsV2: count failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to query logs",
		})
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition",
		fmt.Sprintf(`attachment; filename="admin-logs-%d.csv"`, time.Now().Unix()))
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Total-Matched", strconv.FormatInt(totalMatched, 10))
	if totalMatched > int64(maxRows) {
		c.Header("X-Truncated", "true")
	}
	c.Status(http.StatusOK)

	w := csv.NewWriter(c.Writer)
	defer w.Flush()

	if err := w.Write(adminLogCSVHeader); err != nil {
		common.SysError("ExportAdminLogsV2: write header: " + err.Error())
		return
	}

	written := 0
	afterID := 0
	for written < maxRows {
		batchLimit := adminExportBatchSize
		if remaining := maxRows - written; remaining < batchLimit {
			batchLimit = remaining
		}

		logs, err := repo.ExportAdminLogsBatch(afterID, tenantID, logType, modelName, startTime, endTime, batchLimit)
		if err != nil {
			common.SysError("ExportAdminLogsV2: query failed: " + err.Error())
			return
		}
		if len(logs) == 0 {
			break
		}

		for _, l := range logs {
			row := []string{
				strconv.Itoa(l.Id),
				time.Unix(l.CreatedAt, 0).UTC().Format(time.RFC3339),
				l.TenantId,
				strconv.Itoa(l.UserId),
				l.Username,
				strconv.Itoa(l.Type),
				l.ModelName,
				l.UpstreamModel,
				l.TokenName,
				strconv.Itoa(l.PromptTokens),
				strconv.Itoa(l.CompletionTokens),
				strconv.Itoa(l.Quota),
				strconv.Itoa(l.UseTime),
				strconv.Itoa(l.TotalLatencyMs),
				strconv.FormatBool(l.IsStream),
				strconv.Itoa(l.ChannelId),
				l.Group,
				l.Ip,
			}
			if err := w.Write(row); err != nil {
				common.SysError("ExportAdminLogsV2: write row: " + err.Error())
				return
			}
		}
		w.Flush()

		afterID = logs[len(logs)-1].Id
		written += len(logs)
		if len(logs) < batchLimit {
			break
		}
	}
}
