package repo

import (
	"context"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// AuditEventRepo implements governance.AuditWriter.
type AuditEventRepo struct{}

// CreateAuditEvent persists an audit event to the database.
func (r *AuditEventRepo) CreateAuditEvent(event *entity.AuditEvent) error {
	return DB.Create(event).Error
}

// GetAuditEvents queries audit events with filters and pagination.
func GetAuditEvents(tenantID string, action string, actorID int, resource string, startTime, endTime int64, offset, limit int) (events []*entity.AuditEvent, total int64, err error) {
	tx := DB.Model(&entity.AuditEvent{})

	if tenantID != "" {
		tx = tx.Where("tenant_id = ?", tenantID)
	}
	if action != "" {
		tx = tx.Where("action = ?", action)
	}
	if actorID > 0 {
		tx = tx.Where("actor_id = ?", actorID)
	}
	if resource != "" {
		tx = tx.Where("resource = ?", resource)
	}
	if startTime > 0 {
		tx = tx.Where("timestamp >= ?", startTime)
	}
	if endTime > 0 {
		tx = tx.Where("timestamp <= ?", endTime)
	}

	if err = tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	err = tx.Order("timestamp DESC").Offset(offset).Limit(limit).Find(&events).Error
	return events, total, err
}

// DeleteOldAuditEvents deletes audit events older than targetTimestamp in
// batches. Mirrors DeleteOldLog's pattern (repo/log.go): `id IN (SELECT id …
// ORDER BY id LIMIT ?)` rather than `.Limit(n).Delete(...)` directly on the
// table — gorm's DeleteClauses (both the postgres and glebarez/sqlite
// drivers) render DELETE/FROM/WHERE only, no LIMIT, so the plain form is
// silently unbounded and one call removes every matching row in a single
// transaction.
func DeleteOldAuditEvents(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	var total int64
	for {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		idQuery := DB.Model(&entity.AuditEvent{}).
			Select("id").
			Where("timestamp < ?", targetTimestamp).
			Order("id").
			Limit(limit)
		result := DB.Where("id IN (?)", idQuery).Delete(&entity.AuditEvent{})
		if result.Error != nil {
			return total, result.Error
		}
		total += result.RowsAffected
		if result.RowsAffected < int64(limit) {
			break
		}
	}
	return total, nil
}

// DeleteExpiredAuditEvents deletes audit events whose retention_until has
// passed. Rows with retention_until = 0 are treated as "no expiry" and
// preserved indefinitely. Phase E3 audit-retention enforcement.
//
// Same id-subquery batching as DeleteOldLog (repo/log.go): gorm's
// DeleteClauses render no LIMIT on DELETE for either the postgres or
// glebarez/sqlite driver, so `.Limit(n).Delete(...)` alone is silently
// unbounded — one call would lock and remove every expired row in a single
// transaction.
func DeleteExpiredAuditEvents(ctx context.Context, now int64, limit int) (int64, error) {
	var total int64
	for {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		idQuery := DB.Model(&entity.AuditEvent{}).
			Select("id").
			Where("retention_until > 0 AND retention_until <= ?", now).
			Order("id").
			Limit(limit)
		result := DB.Where("id IN (?)", idQuery).Delete(&entity.AuditEvent{})
		if result.Error != nil {
			return total, result.Error
		}
		total += result.RowsAffected
		if result.RowsAffected < int64(limit) {
			break
		}
	}
	return total, nil
}

// ExportAuditEvents returns audit events ordered by id ASC for cursor-based
// export. Pass cursor=0 to start. Returns up to limit rows plus the next
// cursor (last id + 1, or 0 if fully drained). Filters are AND-combined
// alongside the cursor predicate; pass empty/zero values to skip a filter.
func ExportAuditEvents(cursorID int64, action, resource string, actorID int, startTime, endTime int64, limit int) (events []*entity.AuditEvent, nextCursor int64, err error) {
	tx := DB.Model(&entity.AuditEvent{}).Where("id >= ?", cursorID)
	if action != "" {
		tx = tx.Where("action = ?", action)
	}
	if resource != "" {
		tx = tx.Where("resource = ?", resource)
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
	// Fetch one extra row to detect whether more pages remain.
	if err = tx.Order("id ASC").Limit(limit + 1).Find(&events).Error; err != nil {
		return nil, 0, err
	}
	if len(events) > limit {
		nextCursor = events[limit].ID
		events = events[:limit]
	}
	return events, nextCursor, nil
}
