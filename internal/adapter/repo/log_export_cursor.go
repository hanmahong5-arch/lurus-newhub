package repo

import (
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// log_export_cursor.go - read paths for the JSONL export and the usage-event
// publisher. They live in their own file because GetUserLogsWithParams orders
// by created_at DESC with an OFFSET: neither is stable under concurrent
// inserts, which a resumable export cannot tolerate. Here the cursor is the
// primary key (strictly ascending, indexed), so a page boundary never skips or
// repeats a row.

// ExportUserLogsAfterID returns up to limit rows with id > afterID, oldest id
// first, under the same filters GetUserLogsWithParams applies. scope is the
// caller's tenant decision exactly as for the list endpoint.
func ExportUserLogsAfterID(scope TenantScope, params *LogQueryParams, afterID, limit int) ([]*Log, error) {
	tx := scope.apply(LOG_DB.Model(&Log{}))
	if params.UserID > 0 {
		tx = tx.Where("user_id = ?", params.UserID)
	}
	if params.LogType > 0 {
		tx = tx.Where("type = ?", params.LogType)
	}
	if params.ModelName != "" {
		tx = tx.Where("model_name = ?", params.ModelName)
	}
	if params.StartTime > 0 {
		tx = tx.Where("created_at >= ?", params.StartTime)
	}
	if params.EndTime > 0 {
		tx = tx.Where("created_at <= ?", params.EndTime)
	}
	if params.TokenName != "" {
		tx = tx.Where("token_name = ?", params.TokenName)
	}
	if params.ProjectID > 0 {
		tx = tx.Where("project_id = ?", params.ProjectID)
	}
	tx = ApplyLogAttributionFilters(tx, params.ProjectIDs, params.EmployeeRef)
	var logs []*Log
	err := tx.Where("id > ?", afterID).Order("id ASC").Limit(limit).Find(&logs).Error
	return logs, err
}

// LogBodyKey is the lookup key of GetLogBodiesByRequestIDs. Tenant is part of
// it so a request id that somehow collides across tenants never attaches the
// wrong tenant's prompt to a row.
func LogBodyKey(tenantID, requestID string) string { return tenantID + "\x00" + requestID }

// GetLogBodiesByRequestIDs batch-loads live (unexpired) archived bodies. With
// a non-empty tenantID the query is confined to that tenant; with "" it spans
// tenants (platform-admin export), still keyed per tenant in the result.
func GetLogBodiesByRequestIDs(tenantID string, requestIDs []string) (map[string]*LogBody, error) {
	out := map[string]*LogBody{}
	if len(requestIDs) == 0 {
		return out, nil
	}
	q := DB.Where("request_id IN ? AND expires_at > ?", requestIDs, common.GetTimestamp())
	if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}
	var rows []*LogBody
	if err := q.Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows { // ascending id: the newest row wins, as in GetLogBody
		out[LogBodyKey(r.TenantId, r.RequestId)] = r
	}
	return out, nil
}

// LogBodyExists reports whether a live archived body exists for the request.
func LogBodyExists(tenantID, requestID string) (bool, error) {
	if tenantID == "" || requestID == "" {
		return false, nil
	}
	var n int64
	err := DB.Model(&LogBody{}).
		Where("tenant_id = ? AND request_id = ? AND expires_at > ?", tenantID, requestID, common.GetTimestamp()).
		Limit(1).Count(&n).Error
	return n > 0, err
}
