package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// JSONL export (format=jsonl) for the tenant and admin log exports. One JSON
// object per line, rows in ascending id order, resumable by cursor: the client
// passes the id of the last line it received as ?cursor= and gets the next
// page. A page shorter than the requested limit is the last one. Rows are
// streamed batch by batch, never held as a whole table in memory.
//
// Unlike the CSV the values are written raw: JSON has no formula-evaluating
// consumer, so the spreadsheet-injection prefixing of csv_cell.go would only
// corrupt the data here.

const (
	exportFormatCSV   = "csv"
	exportFormatJSONL = "jsonl"
)

// exportBodyJSON is the optional archived body attached to a row.
type exportBodyJSON struct {
	RequestBody      string `json:"request_body"`
	ResponseText     string `json:"response_text"`
	ResponseCaptured bool   `json:"response_captured"`
	Truncated        bool   `json:"truncated"`
}

func exportBodyOf(b *repo.LogBody) *exportBodyJSON {
	return &exportBodyJSON{
		RequestBody: b.RequestBody, ResponseText: b.ResponseText,
		ResponseCaptured: b.ResponseCaptured, Truncated: b.Truncated,
	}
}

// exportKeyIdx maps the stored sentinel (-1 = not a multi-key channel) to null.
func exportKeyIdx(p *int64) *int64 {
	if p == nil || *p < 0 {
		return nil
	}
	return p
}

// tenantExportJSONLRow carries every CSV column plus id and channel_key_idx.
type tenantExportJSONLRow struct {
	ID               int             `json:"id"`
	CreatedAt        string          `json:"created_at"`
	LogType          int             `json:"log_type"`
	ModelName        string          `json:"model_name"`
	TokenName        string          `json:"token_name"`
	ProjectID        int             `json:"project_id"`
	ProjectName      string          `json:"project_name"`
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	Quota            int             `json:"quota"`
	ChargedCNY       string          `json:"charged_cny"`
	IP               string          `json:"ip"`
	Content          string          `json:"content"`
	EmployeeRef      string          `json:"employee_ref"`
	TokenID          int             `json:"token_id"`
	RequestID        string          `json:"request_id"`
	ChannelKeyIdx    *int64          `json:"channel_key_idx"`
	Body             *exportBodyJSON `json:"body,omitempty"`
}

// adminExportJSONLRow carries the admin CSV columns plus request_id,
// channel_key_idx and the charged amount. content/other stay excluded: the
// admin export is a usage report, not a data dump (see adminLogCSVHeader).
type adminExportJSONLRow struct {
	ID               int             `json:"id"`
	CreatedAt        string          `json:"created_at"`
	TenantID         string          `json:"tenant_id"`
	UserID           int             `json:"user_id"`
	Username         string          `json:"username"`
	LogType          int             `json:"log_type"`
	ModelName        string          `json:"model_name"`
	UpstreamModel    string          `json:"upstream_model"`
	TokenName        string          `json:"token_name"`
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	Quota            int             `json:"quota"`
	ChargedCNY       string          `json:"charged_cny"`
	UseTime          int             `json:"use_time"`
	TotalLatencyMs   int             `json:"total_latency_ms"`
	IsStream         bool            `json:"is_stream"`
	ChannelID        int             `json:"channel_id"`
	Group            string          `json:"group"`
	IP               string          `json:"ip"`
	RequestID        string          `json:"request_id"`
	ChannelKeyIdx    *int64          `json:"channel_key_idx"`
	Body             *exportBodyJSON `json:"body,omitempty"`
}

// parseExportFormat validates ?format; an unknown value is a 400.
func parseExportFormat(c *gin.Context) (string, bool) {
	f := c.DefaultQuery("format", exportFormatCSV)
	if f != exportFormatCSV && f != exportFormatJSONL {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "format must be csv or jsonl", "error_code": "INVALID_FORMAT"})
		return "", false
	}
	if c.Query("include_body") != "" && f != exportFormatJSONL {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "include_body requires format=jsonl", "error_code": "INVALID_FORMAT"})
		return "", false
	}
	return f, true
}

// parseExportCursor reads ?cursor (0 = from the start).
func parseExportCursor(c *gin.Context) (int, bool) {
	raw := c.Query("cursor")
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "cursor must be a non-negative id", "error_code": "INVALID_CURSOR"})
		return 0, false
	}
	return n, true
}

// jsonlRowCap is the page size: ?limit wins, then ?max_rows, clamped to hard.
func jsonlRowCap(c *gin.Context, def, hard int) int {
	raw := c.Query("limit")
	if raw == "" {
		raw = c.Query("max_rows")
	}
	n, _ := strconv.Atoi(raw)
	if n <= 0 {
		n = def
	}
	if n > hard {
		n = hard
	}
	return n
}

func startJSONL(c *gin.Context, filename string) {
	c.Header("Content-Type", "application/x-ndjson; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Cache-Control", "no-cache")
	// Declared so a client that reads trailers can resume without parsing the
	// last line; every row also carries its own id, which is the cursor.
	c.Header("Trailer", "X-Next-Cursor")
	c.Status(http.StatusOK)
}

// loadExportBodies fetches the archived bodies of one batch. tenantID ""
// means a platform (cross-tenant) export.
func loadExportBodies(tenantID string, ids []string) map[string]*repo.LogBody {
	m, err := repo.GetLogBodiesByRequestIDs(tenantID, ids)
	if err != nil {
		// Non-fatal: the usage columns are still complete; bodies are the extra.
		common.SysError("log export: load bodies: " + err.Error())
		return map[string]*repo.LogBody{}
	}
	return m
}

// auditExportBody records one log_body.read per attached body, so exporting
// cannot be a quieter way to read prompts than the direct GET.
func auditExportBody(c *gin.Context, b *repo.LogBody) {
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, dpActor(c),
		governance.ActionLogBodyRead, governance.ResourceLogBody, 0,
		fmt.Sprintf(`{"tenant_id":%q,"request_id":%q,"via":"export"}`, b.TenantId, b.RequestId)))
}

// exportTenantLogsJSONL streams the tenant export. includeBody must already
// have passed the tenant-admin check.
func exportTenantLogsJSONL(c *gin.Context, tenantID, tenantSlug string, params *repo.LogQueryParams, limit, cursor int, includeBody bool) {
	startJSONL(c, fmt.Sprintf("logs-%s-%d.jsonl", tenantSlug, time.Now().Unix()))
	enc := json.NewEncoder(c.Writer)
	written, afterID := 0, cursor
	for written < limit {
		batch := exportBatchSize
		if rem := limit - written; rem < batch {
			batch = rem
		}
		logs, err := repo.ExportUserLogsAfterID(repo.ForTenant(tenantID), params, afterID, batch)
		if err != nil {
			common.SysError("ExportLogsV2 jsonl: query failed: " + err.Error())
			return
		}
		if len(logs) == 0 {
			break
		}
		pids := make([]int, 0, len(logs))
		rids := make([]string, 0, len(logs))
		for _, l := range logs {
			if l.ProjectId > 0 {
				pids = append(pids, l.ProjectId)
			}
			if rid := logOtherString(l.Other, "request_id"); rid != "" {
				rids = append(rids, rid)
			}
		}
		names, nameErr := repo.ResolveProjectNames(tenantID, pids)
		if nameErr != nil {
			common.SysError("ExportLogsV2 jsonl: resolve project names: " + nameErr.Error())
			names = map[int]string{}
		}
		var bodies map[string]*repo.LogBody
		if includeBody {
			bodies = loadExportBodies(tenantID, rids)
		}
		for _, l := range logs {
			rid := logOtherString(l.Other, "request_id")
			row := tenantExportJSONLRow{
				ID: l.Id, CreatedAt: time.Unix(l.CreatedAt, 0).UTC().Format(time.RFC3339),
				LogType: l.Type, ModelName: l.ModelName, TokenName: l.TokenName,
				ProjectID: l.ProjectId, ProjectName: names[l.ProjectId],
				PromptTokens: l.PromptTokens, CompletionTokens: l.CompletionTokens, Quota: l.Quota,
				ChargedCNY: chargedCNYCell(l.ChargedCNY4), IP: l.Ip, Content: l.Content,
				EmployeeRef: l.EmployeeRef, TokenID: l.TokenId, RequestID: rid,
				ChannelKeyIdx: exportKeyIdx(l.ChannelKeyIdx),
			}
			if b := bodies[repo.LogBodyKey(tenantID, rid)]; b != nil && rid != "" {
				row.Body = exportBodyOf(b)
				auditExportBody(c, b)
			}
			if err := enc.Encode(row); err != nil {
				common.SysError("ExportLogsV2 jsonl: write row: " + err.Error())
				return
			}
		}
		c.Writer.Flush()
		afterID = logs[len(logs)-1].Id
		written += len(logs)
		if len(logs) < batch {
			break
		}
	}
	c.Writer.Header().Set("X-Next-Cursor", strconv.Itoa(afterID))
}

// exportAdminLogsJSONL streams the platform export (root only). The caller has
// already counted and set X-Total-Matched / X-Truncated and the JSONL headers.
func exportAdminLogsJSONL(c *gin.Context, tenantID string, logType int, modelName string, startTime, endTime int64, upstreamRequestID string, limit, cursor int, includeBody bool) {
	enc := json.NewEncoder(c.Writer)
	written, afterID := 0, cursor
	for written < limit {
		batch := adminExportBatchSize
		if rem := limit - written; rem < batch {
			batch = rem
		}
		logs, err := repo.ExportAdminLogsBatch(afterID, tenantID, logType, modelName, startTime, endTime, upstreamRequestID, batch)
		if err != nil {
			common.SysError("ExportAdminLogsV2 jsonl: query failed: " + err.Error())
			return
		}
		if len(logs) == 0 {
			break
		}
		var bodies map[string]*repo.LogBody
		if includeBody {
			rids := make([]string, 0, len(logs))
			for _, l := range logs {
				if rid := logOtherString(l.Other, "request_id"); rid != "" {
					rids = append(rids, rid)
				}
			}
			bodies = loadExportBodies(tenantID, rids)
		}
		for _, l := range logs {
			rid := logOtherString(l.Other, "request_id")
			row := adminExportJSONLRow{
				ID: l.Id, CreatedAt: time.Unix(l.CreatedAt, 0).UTC().Format(time.RFC3339),
				TenantID: l.TenantId, UserID: l.UserId, Username: l.Username, LogType: l.Type,
				ModelName: l.ModelName, UpstreamModel: l.UpstreamModel, TokenName: l.TokenName,
				PromptTokens: l.PromptTokens, CompletionTokens: l.CompletionTokens, Quota: l.Quota,
				ChargedCNY: chargedCNYCell(l.ChargedCNY4), UseTime: l.UseTime,
				TotalLatencyMs: l.TotalLatencyMs, IsStream: l.IsStream, ChannelID: l.ChannelId,
				Group: l.Group, IP: l.Ip, RequestID: rid, ChannelKeyIdx: exportKeyIdx(l.ChannelKeyIdx),
			}
			if b := bodies[repo.LogBodyKey(l.TenantId, rid)]; b != nil && rid != "" {
				row.Body = exportBodyOf(b)
				auditExportBody(c, b)
			}
			if err := enc.Encode(row); err != nil {
				common.SysError("ExportAdminLogsV2 jsonl: write row: " + err.Error())
				return
			}
		}
		c.Writer.Flush()
		afterID = logs[len(logs)-1].Id
		written += len(logs)
		if len(logs) < batch {
			break
		}
	}
	c.Writer.Header().Set("X-Next-Cursor", strconv.Itoa(afterID))
}
