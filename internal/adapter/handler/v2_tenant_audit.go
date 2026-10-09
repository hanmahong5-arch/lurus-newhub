package handler

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// Tenant-facing audit trail. Tenant-admin gated; every query is pinned to the
// caller's own tenant id (taken from the authenticated TenantContext, never
// from a query parameter) and rows are projected through tenantAuditView so
// platform-side detail never leaves the platform.

// tenantAuditView is the tenant-safe projection of entity.AuditEvent: no hash
// chain, no retention bookkeeping, details redacted.
type tenantAuditView struct {
	ID         int64  `json:"id"`
	Timestamp  int64  `json:"timestamp"`
	ActorType  string `json:"actor_type"`
	ActorID    int    `json:"actor_id"`
	Action     string `json:"action"`
	Resource   string `json:"resource"`
	ResourceID int    `json:"resource_id"`
	IP         string `json:"ip"`
	RequestID  string `json:"request_id"`
	Details    string `json:"details"`
}

// sensitiveAuditKeys: a detail-JSON key whose lower-cased form contains any of
// these carries supply-chain or platform-internal data and is masked.
var sensitiveAuditKeys = []string{
	"channel", "upstream", "key", "secret", "internal", "note", "remark", "base_url", "proxy", "password",
}

var (
	auditSecretRe  = regexp.MustCompile(`(?i)\b(sk|ak|lurus_ik|key)[-_][A-Za-z0-9_\-]{6,}`)
	auditChannelRe = regexp.MustCompile(`(?i)\bchannel[ _#:=-]*\d+`)
)

const auditRedacted = "[redacted]"

func redactAuditText(s string) string {
	s = auditSecretRe.ReplaceAllString(s, auditRedacted)
	return auditChannelRe.ReplaceAllString(s, "channel "+auditRedacted)
}

func redactAuditJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			lk := strings.ToLower(k)
			hit := false
			for _, s := range sensitiveAuditKeys {
				if strings.Contains(lk, s) {
					hit = true
					break
				}
			}
			if hit {
				out[k] = auditRedacted
				continue
			}
			out[k] = redactAuditJSON(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = redactAuditJSON(val)
		}
		return out
	case string:
		return redactAuditText(t)
	default:
		return v
	}
}

// redactAuditDetails scrubs a details blob: JSON objects/arrays are redacted
// structurally (sensitive keys masked, string values pattern-scrubbed),
// anything else is pattern-scrubbed as free text.
func redactAuditDetails(details string) string {
	if details == "" {
		return ""
	}
	var parsed any
	if err := json.Unmarshal([]byte(details), &parsed); err == nil {
		switch parsed.(type) {
		case map[string]any, []any:
			if b, merr := json.Marshal(redactAuditJSON(parsed)); merr == nil {
				return string(b)
			}
		}
	}
	return redactAuditText(details)
}

func toTenantAuditView(e *entity.AuditEvent) tenantAuditView {
	return tenantAuditView{
		ID: e.ID, Timestamp: e.Timestamp, ActorType: e.ActorType, ActorID: e.ActorID,
		Action: e.Action, Resource: e.Resource, ResourceID: e.ResourceID,
		IP: e.IP, RequestID: e.RequestID, Details: redactAuditDetails(e.Details),
	}
}

// tenantAuditScope authorises the caller and returns the tenant to read.
func tenantAuditScope(c *gin.Context) (string, bool) {
	tc, err := middleware.GetTenantContext(c)
	if err != nil || tc == nil || tc.TenantID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Not authenticated", "error_code": "UNAUTHENTICATED"})
		return "", false
	}
	if !requireTenantAdmin(c, tc) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Tenant admin required", "error_code": "PERMISSION_DENIED"})
		return "", false
	}
	return tc.TenantID, true
}

type tenantAuditFilter struct {
	action  string
	actorID int
	start   int64
	end     int64
}

func parseTenantAuditFilter(c *gin.Context) (tenantAuditFilter, bool) {
	f := tenantAuditFilter{action: c.Query("action")}
	f.actorID, _ = strconv.Atoi(c.Query("actor_id"))
	f.start, _ = strconv.ParseInt(c.Query("start_time"), 10, 64)
	f.end, _ = strconv.ParseInt(c.Query("end_time"), 10, 64)
	if f.action != "" && !governance.IsValidAuditAction(f.action) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": fmt.Sprintf("unknown audit action: %q", f.action)})
		return f, false
	}
	return f, true
}

// ListTenantAuditV2: GET /api/v2/:tenant_slug/audit?action=&actor_id=&start_time=&end_time=&page=&page_size=
func ListTenantAuditV2(c *gin.Context) {
	tenantID, ok := tenantAuditScope(c)
	if !ok {
		return
	}
	f, ok := parseTenantAuditFilter(c)
	if !ok {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	events, total, err := repo.ListTenantAuditEvents(tenantID, f.action, f.actorID, f.start, f.end, (page-1)*size, size)
	if err != nil {
		common.SysError("tenant audit list failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to query audit events"})
		return
	}
	items := make([]tenantAuditView, 0, len(events))
	for _, e := range events {
		items = append(items, toTenantAuditView(e))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"items": items, "total": total, "page": page, "page_size": size,
	}})
}

// ExportTenantAuditCSVV2: GET /api/v2/:tenant_slug/audit/export.csv (one page per
// call; X-Next-Cursor carries the continuation, pass it back as ?cursor=).
func ExportTenantAuditCSVV2(c *gin.Context) {
	tenantID, ok := tenantAuditScope(c)
	if !ok {
		return
	}
	f, ok := parseTenantAuditFilter(c)
	if !ok {
		return
	}
	cursor, _ := strconv.ParseInt(c.Query("cursor"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "1000"))
	events, next, err := repo.ExportTenantAuditEvents(tenantID, cursor, f.action, f.actorID, f.start, f.end, limit)
	if err != nil {
		common.SysError("tenant audit export failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to query audit events"})
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="audit-events.csv"`)
	if next > 0 {
		c.Header("X-Next-Cursor", strconv.FormatInt(next, 10))
	}
	w := csv.NewWriter(c.Writer)
	_ = w.Write(csvRow("id", "timestamp", "actor_type", "actor_id", "action", "resource", "resource_id", "ip", "request_id", "details"))
	for _, e := range events {
		v := toTenantAuditView(e)
		_ = w.Write(csvRow(
			strconv.FormatInt(v.ID, 10), strconv.FormatInt(v.Timestamp, 10), v.ActorType,
			strconv.Itoa(v.ActorID), v.Action, v.Resource, strconv.Itoa(v.ResourceID),
			v.IP, v.RequestID, v.Details,
		))
		if w.Error() != nil {
			break
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		common.SysError("tenant audit CSV export truncated: " + err.Error())
	}
}
