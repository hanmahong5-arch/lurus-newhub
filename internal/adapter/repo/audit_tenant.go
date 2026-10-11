package repo

import (
	"errors"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// ErrAuditTenantRequired is returned by the tenant-scoped audit readers when
// tenantID is empty: GetAuditEvents treats "" as "all tenants", which must
// never be reachable from a tenant-facing endpoint.
var ErrAuditTenantRequired = errors.New("audit: tenant id required")

// ListTenantAuditEvents is GetAuditEvents hard-pinned to one tenant.
func ListTenantAuditEvents(tenantID, action string, actorID int, startTime, endTime int64, offset, limit int) ([]*entity.AuditEvent, int64, error) {
	if tenantID == "" {
		return nil, 0, ErrAuditTenantRequired
	}
	return GetAuditEvents(tenantID, action, actorID, "", startTime, endTime, offset, limit)
}

// ExportTenantAuditEvents is the cursor-paged (id ASC) export pinned to one
// tenant. Returns the next cursor (0 when drained).
func ExportTenantAuditEvents(tenantID string, cursorID int64, action string, actorID int, startTime, endTime int64, limit int) ([]*entity.AuditEvent, int64, error) {
	if tenantID == "" {
		return nil, 0, ErrAuditTenantRequired
	}
	tx := DB.Model(&entity.AuditEvent{}).Where("tenant_id = ?", tenantID).Where("id >= ?", cursorID)
	if action != "" {
		tx = tx.Where("action = ?", action)
	}
	if actorID > 0 {
		tx = tx.Where("actor_id = ?", actorID)
	}
	if startTime > 0 {
		tx = tx.Where("timestamp >= ?", startTime)
	}
	if endTime > 0 {
		tx = tx.Where("timestamp <= ?", endTime)
	}
	if limit <= 0 {
		limit = 1000
	}
	if limit > 10000 {
		limit = 10000
	}
	var events []*entity.AuditEvent
	if err := tx.Order("id ASC").Limit(limit + 1).Find(&events).Error; err != nil {
		return nil, 0, err
	}
	var next int64
	if len(events) > limit {
		next = events[limit].ID
		events = events[:limit]
	}
	return events, next, nil
}
