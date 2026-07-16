package governance

import (
	"sync/atomic"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// AuditWriter is the interface for persisting audit events.
// Set via SetAuditWriter during initialization to avoid circular imports.
type AuditWriter interface {
	CreateAuditEvent(event *entity.AuditEvent) error
}

// auditWriterRef stores the global audit writer atomically for safe concurrent access.
var auditWriterRef atomic.Pointer[AuditWriter]

// SetAuditWriter sets the global audit event writer (called once during startup).
// It also wires the entity-level audit-chain fallback logger so fail-open
// chain degradations (entity.AuditEvent.BeforeCreate) surface in the system log.
func SetAuditWriter(w AuditWriter) {
	auditWriterRef.Store(&w)
	entity.SetAuditChainLogger(common.SysError)
}

// RecordAuditEvent asynchronously persists an audit event.
// Safe to call even if no writer is configured (no-op).
func RecordAuditEvent(event *entity.AuditEvent) {
	wp := auditWriterRef.Load()
	if wp == nil || event == nil {
		return
	}
	writer := *wp
	gopool.Go(func() {
		if err := writer.CreateAuditEvent(event); err != nil {
			common.SysLog("failed to record audit event: " + err.Error())
		}
	})
}

// DefaultAuditRetentionSeconds is the default per-event retention window
// applied by NewAuditEvent — seven years, matching the SOC 2 / industry-norm
// retention for security-relevant access logs. Override per-event by setting
// the RetentionUntil field directly after construction (e.g. shorter for
// high-volume relay events, indefinite by setting 0).
const DefaultAuditRetentionSeconds int64 = 7 * 365 * 24 * 60 * 60

// NewAuditEvent creates an AuditEvent from gin context with common fields pre-filled.
// The default RetentionUntil is Timestamp + 7 years; callers may override after
// construction for shorter or indefinite retention.
func NewAuditEvent(c *gin.Context, actorType string, actorID int, action, resource string, resourceID int, details string) *entity.AuditEvent {
	ts := common.GetTimestamp()
	event := &entity.AuditEvent{
		TenantID:       c.GetString("tenant_id"),
		Timestamp:      ts,
		ActorType:      actorType,
		ActorID:        actorID,
		Action:         action,
		Resource:       resource,
		ResourceID:     resourceID,
		Details:        details,
		IP:             c.ClientIP(),
		RequestID:      c.GetString(common.RequestIdKey),
		RetentionUntil: ts + DefaultAuditRetentionSeconds,
	}
	if event.TenantID == "" {
		event.TenantID = "default"
	}
	return event
}

// NewDetachedAuditEvent builds an AuditEvent for background tasks that lack a
// gin.Context — lifecycle jobs, async post-debit hooks, and similar paths.
// Tenant must be supplied by the caller (empty defaults to "default"); IP and
// RequestID are unset because there is no inbound request to attribute them to.
// RetentionUntil defaults to ts + 7y, same as NewAuditEvent.
func NewDetachedAuditEvent(tenantID, actorType string, actorID int, action, resource string, resourceID int, details string) *entity.AuditEvent {
	ts := common.GetTimestamp()
	if tenantID == "" {
		tenantID = "default"
	}
	return &entity.AuditEvent{
		TenantID:       tenantID,
		Timestamp:      ts,
		ActorType:      actorType,
		ActorID:        actorID,
		Action:         action,
		Resource:       resource,
		ResourceID:     resourceID,
		Details:        details,
		RetentionUntil: ts + DefaultAuditRetentionSeconds,
	}
}
