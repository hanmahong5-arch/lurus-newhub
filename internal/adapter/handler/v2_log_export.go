/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package handler

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

const (
	// exportDefaultMaxRows is the number of rows returned when max_rows is not specified.
	exportDefaultMaxRows = 10_000
	// exportHardMaxRows is the absolute upper bound; requests above this are clamped silently.
	exportHardMaxRows = 50_000
	// exportBatchSize is the number of rows fetched per DB round-trip.
	exportBatchSize = 1_000
)

// csvHeader lists the whitelisted columns written to the CSV in declaration order.
// These are explicit strings — not derived from entity field reflection.
var csvHeader = []string{
	"created_at",
	"log_type",
	"model_name",
	"token_name",
	// Cost attribution (migration 029). Both the id AND the resolved name are
	// exported: the id is the stable join key for a spreadsheet pivot, while
	// the name is what a finance reader actually reads — and a project that
	// gets renamed or retired later would otherwise make an archived export
	// unreadable. Empty name = the "unassigned" bucket (project_id 0).
	"project_id",
	"project_name",
	"prompt_tokens",
	"completion_tokens",
	"quota",
	"ip",
	"content",
}

// ExportLogsV2 streams the current user's logs as a CSV file.
// Route: GET /api/v2/:tenant_slug/logs/export
// Auth:  UserAuth (session) — inherited from the tenantLogs group.
// Query params (all optional):
//
//	type        int    — log type filter (0 = all)
//	model_name  string — exact model name filter
//	token_name  string — exact token name filter
//	start_time  int64  — unix seconds lower bound (inclusive)
//	end_time    int64  — unix seconds upper bound (inclusive)
//	max_rows    int    — row cap; clamped to 50000 if higher
func ExportLogsV2(c *gin.Context) {
	// ── auth & tenant context ──────────────────────────────────────────────────
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	tenantSlug := c.Param("tenant_slug")
	if tenantSlug == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "tenant slug required",
			"error_code": "INVALID_TENANT_SLUG",
		})
		return
	}

	// Verify the URL slug maps to a real tenant and matches the JWT context.
	// Exception: "default" is the virtual single-tenant (tenant id == slug ==
	// "default", no tenants row exists). Sibling v2 log endpoints (GetLogsV2 /
	// GetLogStatV2 / cluster) accept it because they scope purely via the
	// middleware tenant context, so the export must not 404 on the missing row.
	if tenantSlug != "default" || tenantCtx.TenantID != "default" {
		tenant, err := repo.GetTenantBySlug(tenantSlug)
		if err != nil || tenant == nil {
			c.JSON(http.StatusNotFound, gin.H{
				"success":    false,
				"message":    "Tenant not found",
				"error_code": "TENANT_NOT_FOUND",
			})
			return
		}
		if tenant.Id != tenantCtx.TenantID {
			c.JSON(http.StatusForbidden, gin.H{
				"success":    false,
				"message":    "Tenant mismatch",
				"error_code": "TENANT_MISMATCH",
			})
			return
		}
	}

	// ── query params ───────────────────────────────────────────────────────────
	logType, _ := strconv.Atoi(c.DefaultQuery("type", "0"))
	modelName := c.Query("model_name")
	tokenName := c.Query("token_name")
	startTime, _ := strconv.ParseInt(c.DefaultQuery("start_time", "0"), 10, 64)
	endTime, _ := strconv.ParseInt(c.DefaultQuery("end_time", "0"), 10, 64)
	// Cost-attribution filter (migration 029); 0 = no filter.
	projectID, _ := strconv.Atoi(c.DefaultQuery("project_id", "0"))

	maxRows, _ := strconv.Atoi(c.DefaultQuery("max_rows", strconv.Itoa(exportDefaultMaxRows)))
	if maxRows <= 0 {
		maxRows = exportDefaultMaxRows
	}
	if maxRows > exportHardMaxRows {
		maxRows = exportHardMaxRows
	}

	// ── response headers ───────────────────────────────────────────────────────
	filename := fmt.Sprintf("logs-%s-%d.csv", tenantSlug, time.Now().Unix())
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Cache-Control", "no-cache")
	c.Status(http.StatusOK)

	w := csv.NewWriter(c.Writer)
	defer w.Flush()

	// Write the whitelisted header row.
	if err := w.Write(csvHeader); err != nil {
		common.SysError("ExportLogsV2: write header: " + err.Error())
		return
	}
	w.Flush()

	// ── paginated streaming ────────────────────────────────────────────────────
	fetched := 0
	offset := 0
	for fetched < maxRows {
		batchLimit := exportBatchSize
		remaining := maxRows - fetched
		if remaining < batchLimit {
			batchLimit = remaining
		}

		params := &repo.LogQueryParams{
			UserID:    tenantCtx.UserID,
			LogType:   logType,
			ModelName: modelName,
			StartTime: startTime,
			EndTime:   endTime,
			TokenName: tokenName,
			ProjectID: projectID,
			Offset:    offset,
			Limit:     batchLimit,
		}

		logs, _, err := repo.GetUserLogsWithParams(repo.ForTenant(tenantCtx.TenantID), params)
		if err != nil {
			common.SysError("ExportLogsV2: query failed: " + err.Error())
			return
		}
		if len(logs) == 0 {
			break
		}

		// Resolve this batch's project names in one tenant-scoped query.
		// ResolveProjectNames reads through Unscoped(), so rows attributed to
		// a since-retired project still export with a readable name instead of
		// silently degrading to a bare integer.
		projectIDs := make([]int, 0, len(logs))
		for _, l := range logs {
			if l.ProjectId > 0 {
				projectIDs = append(projectIDs, l.ProjectId)
			}
		}
		projectNames, nameErr := repo.ResolveProjectNames(tenantCtx.TenantID, projectIDs)
		if nameErr != nil {
			// Non-fatal: the export is still correct and complete without the
			// human-readable column, and project_id keeps every row joinable.
			common.SysError("ExportLogsV2: resolve project names: " + nameErr.Error())
			projectNames = map[int]string{}
		}

		for _, l := range logs {
			createdAt := time.Unix(l.CreatedAt, 0).UTC().Format(time.RFC3339)
			row := []string{
				createdAt,
				strconv.Itoa(l.Type),
				l.ModelName,
				l.TokenName,
				strconv.Itoa(l.ProjectId),
				projectNames[l.ProjectId],
				strconv.Itoa(l.PromptTokens),
				strconv.Itoa(l.CompletionTokens),
				strconv.Itoa(l.Quota),
				l.Ip,
				l.Content,
			}
			if err := w.Write(row); err != nil {
				common.SysError("ExportLogsV2: write row: " + err.Error())
				return
			}
		}
		w.Flush()

		fetched += len(logs)
		offset += len(logs)

		// If we got fewer rows than we asked for, we've reached the end.
		if len(logs) < batchLimit {
			break
		}
	}
}
