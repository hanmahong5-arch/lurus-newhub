package governance

import (
	"sync"
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

// AuditedContextKey is the gin context key RecordAuditEvent sets to true the
// moment it is called for a request-scoped event — see middleware.AuditWriteGuard,
// which checks this after an admin write handler runs to catch writes that
// forgot to call governance.RecordAuditEvent.
const AuditedContextKey = "governance_audited"

// pendingAuditContexts associates an event built by NewAuditEvent with the
// *gin.Context it came from, keyed by the event's pointer identity — so
// RecordAuditEvent (the persisting call) can mark that request audited
// without entity.AuditEvent itself needing a gin.Context field (the entity
// package has no framework dependency today; this keeps it that way).
// LoadAndDelete means an entry only lives between NewAuditEvent(c, …) and the
// matching RecordAuditEvent call — the decoupled construct/record shape at
// internal_privacy_erase.go:153-156 is why marking happens here and not
// inside NewAuditEvent: a caller that builds an event and never records it
// must not mark the request as covered.
var pendingAuditContexts sync.Map // map[*entity.AuditEvent]*gin.Context

// SetAuditWriter sets the global audit event writer (called once during startup).
// It also wires the entity-level audit-chain fallback logger so fail-open
// chain degradations (entity.AuditEvent.BeforeCreate) surface in the system log.
func SetAuditWriter(w AuditWriter) {
	auditWriterRef.Store(&w)
	entity.SetAuditChainLogger(common.SysError)
}

// RecordAuditEvent asynchronously persists an audit event.
// Safe to call even if no writer is configured (no-op). If event was built by
// NewAuditEvent(c, …), this call also marks c with AuditedContextKey — see
// pendingAuditContexts — regardless of whether a writer is configured, since
// what AuditWriteGuard cares about is "did the handler attempt to audit",
// not "did the write succeed".
func RecordAuditEvent(event *entity.AuditEvent) {
	if event == nil {
		return
	}
	if v, ok := pendingAuditContexts.LoadAndDelete(event); ok {
		if c, ok := v.(*gin.Context); ok {
			c.Set(AuditedContextKey, true)
		}
	}
	wp := auditWriterRef.Load()
	if wp == nil {
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
		TenantID:   c.GetString("tenant_id"),
		Timestamp:  ts,
		ActorType:  actorType,
		ActorID:    actorID,
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		Details:    details,
		IP:         c.ClientIP(),
		// RequestID is a correlation convenience, not a trust anchor: since
		// middleware.RequestId() honours a well-formed caller-supplied
		// X-Request-Id (charset-guarded and length-guarded to <= 36 chars —
		// this column's width, entity.AuditEvent.RequestID varchar(36) — so
		// this INSERT cannot fail on an oversized id; but the id is still
		// caller-chosen and therefore reusable/non-unique — a hostile caller
		// can send the same id on every request), this field can collide
		// across rows. Tamper evidence and ordering come from ID
		// (autoincrement) and the
		// PrevHash/RowHash chain, not from RequestID; do not use RequestID
		// as a uniqueness or ordering key when auditing the audit log.
		RequestID:      c.GetString(common.RequestIdKey),
		RetentionUntil: ts + DefaultAuditRetentionSeconds,
	}
	if event.TenantID == "" {
		event.TenantID = "default"
	}
	pendingAuditContexts.Store(event, c)
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
