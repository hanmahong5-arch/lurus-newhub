package handler

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// ListAuditActionsV2 returns the canonical audit action taxonomy. Useful for
// admin UI dropdowns and for clients that want to validate action filters
// before calling /audit/events or /audit/export.
//
// GET /api/v2/admin/audit/actions
func ListAuditActionsV2(c *gin.Context) {
	actions := governance.AllAuditActions()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"actions": actions,
		},
	})
}

// ExportAuditEventsV2 streams audit events using cursor pagination, supporting
// either JSON (page-by-page) or CSV (one HTTP response = one page).
//
// GET /api/v2/admin/audit/export?format=json|csv&cursor=&limit=&action=&resource=&actor_id=&start_time=&end_time=
//
// JSON envelope: {"success":true,"data":{"events":[...],"next_cursor":N}}.
// next_cursor=0 indicates the last page; pass the returned next_cursor on
// the following request to continue.
//
// CSV emits a header row plus one data row per event. The HTTP response
// represents a single page; the Link header carries the next page URL
// (rel="next") so curl-style consumers can drive pagination shell-side.
//
// Root-only (router applies RootAuth on the /api/v2/admin/* group).
func ExportAuditEventsV2(c *gin.Context) {
	format := c.DefaultQuery("format", "json")
	if format != "json" && format != "csv" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "format must be json or csv",
		})
		return
	}

	cursor, _ := strconv.ParseInt(c.Query("cursor"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "1000"))
	action := c.Query("action")
	resource := c.Query("resource")
	actorID, _ := strconv.Atoi(c.Query("actor_id"))
	startTime, _ := strconv.ParseInt(c.Query("start_time"), 10, 64)
	endTime, _ := strconv.ParseInt(c.Query("end_time"), 10, 64)

	// Reject unknown action filters early to avoid an empty CSV that
	// signals "no events" when really the caller misspelled the action.
	if action != "" && !governance.IsValidAuditAction(action) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": fmt.Sprintf("unknown audit action: %q", action),
		})
		return
	}

	events, nextCursor, err := repo.ExportAuditEvents(cursor, action, resource, actorID, startTime, endTime, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to query audit events",
		})
		return
	}
	if events == nil {
		events = make([]*entity.AuditEvent, 0)
	}

	if format == "csv" {
		writeAuditEventsCSV(c, events, nextCursor)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"events":      events,
			"next_cursor": nextCursor,
		},
	})
}

// VerifyAuditChainV2 recomputes the audit tamper-evidence hash chain
// (migration 024) over an id-ordered window and reports breaks.
//
// GET /api/v2/admin/audit/chain-verify?tenant_id=&after_id=&limit=&start_time=&end_time=
//
// Response data: {checked, legacy_rows, hash_breaks, first_break:{id,expected,
// actual}|null, link_breaks, first_link_break|null, link_checks_skipped,
// next_cursor}. legacy_rows are pre-chain (or fail-open) empty-hash rows —
// reported, never failed. Pass next_cursor back as after_id to page; limit is
// capped server-side so one call can never full-table-scan.
//
// Root-only (router applies RootJWTAuth on the /api/v2/admin/* group).
func VerifyAuditChainV2(c *gin.Context) {
	afterID, _ := strconv.ParseInt(c.Query("after_id"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "1000"))
	startTime, _ := strconv.ParseInt(c.Query("start_time"), 10, 64)
	endTime, _ := strconv.ParseInt(c.Query("end_time"), 10, 64)

	result, err := governance.VerifyAuditChain(repo.DB, governance.ChainVerifyParams{
		TenantID:  c.Query("tenant_id"),
		AfterID:   afterID,
		Limit:     limit,
		StartTime: startTime,
		EndTime:   endTime,
	})
	if err != nil {
		common.SysError(fmt.Sprintf("audit chain verify failed: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to verify audit chain",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// writeAuditEventsCSV serializes events as CSV. Header order matches the
// SQL column order. We never embed raw newlines in `Details` (they get
// CSV-quoted by encoding/csv), so consumers can split by line safely on
// any field that does not span a quoted multi-line value.
func writeAuditEventsCSV(c *gin.Context, events []*entity.AuditEvent, nextCursor int64) {
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="audit-events.csv"`)
	if nextCursor > 0 {
		c.Header("X-Next-Cursor", strconv.FormatInt(nextCursor, 10))
	}

	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{
		"id", "tenant_id", "timestamp", "actor_type", "actor_id",
		"action", "resource", "resource_id", "ip", "request_id",
		"retention_until", "details",
	})
	rowsWritten := 0
	for _, e := range events {
		_ = w.Write([]string{
			strconv.FormatInt(e.ID, 10),
			e.TenantID,
			strconv.FormatInt(e.Timestamp, 10),
			e.ActorType,
			strconv.Itoa(e.ActorID),
			e.Action,
			e.Resource,
			strconv.Itoa(e.ResourceID),
			e.IP,
			e.RequestID,
			strconv.FormatInt(e.RetentionUntil, 10),
			e.Details,
		})
		rowsWritten++
		// csv.Writer buffers the first write error; bail out early instead of
		// spinning through the remaining rows once the underlying stream is broken.
		if w.Error() != nil {
			break
		}
	}
	w.Flush()
	// The response status (200) is already committed by the time we get here,
	// so a broken pipe / disk-full mid-export can only be surfaced via logs —
	// this keeps a truncated compliance export from failing silently.
	if err := w.Error(); err != nil {
		common.SysError(fmt.Sprintf("audit CSV export truncated: wrote %d/%d rows, error=%v", rowsWritten, len(events), err))
	}
}
