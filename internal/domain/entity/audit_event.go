package entity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AuditEvent records security-relevant actions for governance audit trails.
type AuditEvent struct {
	ID         int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID   string `json:"tenant_id" gorm:"type:varchar(36);index;default:'default'"`
	Timestamp  int64  `json:"timestamp" gorm:"bigint;index"`
	ActorType  string `json:"actor_type" gorm:"type:varchar(16)"` // user, admin, system, token
	ActorID    int    `json:"actor_id" gorm:"index"`
	Action     string `json:"action" gorm:"type:varchar(64);index"` // token.created, auth.failed, ...
	Resource   string `json:"resource" gorm:"type:varchar(64)"`     // token, channel, user, setting
	ResourceID int    `json:"resource_id"`
	Details    string `json:"details" gorm:"type:text"` // JSON
	IP         string `json:"ip" gorm:"type:varchar(45);default:''"`
	RequestID  string `json:"request_id" gorm:"type:varchar(36);default:''"`
	// RetentionUntil is the unix-seconds deadline after which the
	// audit_cleanup lifecycle task will delete this row. Zero means
	// "no expiry" — legacy rows backfilled without a policy stay forever
	// until an explicit backfill assigns a value. See migration 016 and
	// internal/lifecycle/audit_cleanup.go.
	RetentionUntil int64 `json:"retention_until" gorm:"bigint;default:0;index:idx_audit_events_retention_until,where:retention_until > 0"`

	// PrevHash / RowHash implement the per-tenant tamper-evidence hash chain
	// (migration 024_audit_events_tamper_evidence). RowHash is the SHA-256 of
	// this row's immutable fields plus PrevHash; PrevHash is the RowHash of
	// the previous chained row of the same tenant. Both are set by
	// BeforeCreate; empty values mark "legacy" rows written before the chain
	// was enabled (never backfilled) or rows persisted through the fail-open
	// fallback. See ComputeAuditRowHash for the exact field coverage.
	PrevHash string `json:"prev_hash" gorm:"type:varchar(64);not null;default:''"`
	RowHash  string `json:"row_hash" gorm:"type:varchar(64);not null;default:''"`
}

// AuditChainHead is the per-tenant serialization point of the audit hash
// chain: one row per tenant holding the RowHash of the tenant's most recent
// chained audit event. Writers lock this row (SELECT ... FOR UPDATE on
// PostgreSQL) inside the insert transaction, so concurrent audit writes for
// the same tenant cannot interleave and fork the chain. Created by migration
// 024 on PostgreSQL; hermetic SQLite tests AutoMigrate it explicitly.
type AuditChainHead struct {
	TenantID  string `json:"tenant_id" gorm:"primaryKey;type:varchar(36)"`
	LastHash  string `json:"last_hash" gorm:"type:varchar(64);not null;default:''"`
	UpdatedAt int64  `json:"updated_at" gorm:"bigint;default:0"`
}

// TableName pins the chain-head table name to match migration 024.
func (AuditChainHead) TableName() string { return "audit_chain_heads" }

// AuditChainFallbacks counts audit inserts that could not be chained and fell
// back to a plain (legacy, empty-hash) insert. Fail-open by design: audit
// availability beats chain perfection, so a chain failure must never drop the
// event or block the (already async) write path. Observable via logs — see
// SetAuditChainLogger.
var AuditChainFallbacks atomic.Int64

// auditChainLogger receives fail-open diagnostics. Wired to the process
// logger by governance.SetAuditWriter at startup; nil-safe default keeps the
// entity package free of logging dependencies.
var auditChainLogger atomic.Pointer[func(string)]

// SetAuditChainLogger installs the logger used when a chain link falls back
// to an unchained insert.
func SetAuditChainLogger(f func(string)) {
	if f != nil {
		auditChainLogger.Store(&f)
	}
}

func auditChainFallback(msg string) {
	AuditChainFallbacks.Add(1)
	if lp := auditChainLogger.Load(); lp != nil {
		(*lp)("audit chain fallback (event stored unchained): " + msg)
	}
}

// auditChainSavepoint fences the chain bookkeeping inside the insert
// transaction so a chain failure can be rolled back without poisoning the
// transaction (PostgreSQL aborts the whole tx on any statement error), letting
// the event itself still be inserted unchained.
const auditChainSavepoint = "audit_chain_link"

// ComputeAuditRowHash returns the full 64-hex-char SHA-256 of the row's
// chain-covered fields. Canonical preimage (length-prefixed, LF-terminated
// fields, in this exact order):
//
//	"lurus-audit-chain-v1\n"
//	<len>:<tenant_id>\n
//	<len>:<timestamp base10>\n
//	<len>:<actor_type>\n
//	<len>:<actor_id base10>\n
//	<len>:<action>\n
//	<len>:<resource>\n
//	<len>:<resource_id base10>\n
//	<len>:<request_id>\n
//	<len>:<retention_until base10>\n
//	<len>:<prev_hash>\n
//
// COVERED: tenant_id, timestamp, actor_type, actor_id, action, resource,
// resource_id, request_id, retention_until, prev_hash.
// EXCLUDED — and why:
//   - ip, details: the PIPL §47 erasure path (repo.ScrubAuditEventsBatch)
//     legitimately rewrites them in place; hashing them would make lawful
//     redaction indistinguishable from tampering and break the chain.
//   - id: assigned by the database during INSERT, after the hash must already
//     be final (the append-only trigger blocks post-insert UPDATE). Chain
//     position is fixed by prev_hash linkage instead.
//   - row_hash: is the digest itself.
//
// Migration 024's trigger enforces the same split: under the redaction GUC
// only ip/details may change; every hashed column is immutable at the DB level.
func ComputeAuditRowHash(e *AuditEvent) string {
	h := sha256.New()
	_, _ = io.WriteString(h, "lurus-audit-chain-v1\n")
	field := func(s string) {
		_, _ = fmt.Fprintf(h, "%d:%s\n", len(s), s)
	}
	field(e.TenantID)
	field(strconv.FormatInt(e.Timestamp, 10))
	field(e.ActorType)
	field(strconv.Itoa(e.ActorID))
	field(e.Action)
	field(e.Resource)
	field(strconv.Itoa(e.ResourceID))
	field(e.RequestID)
	field(strconv.FormatInt(e.RetentionUntil, 10))
	field(e.PrevHash)
	return hex.EncodeToString(h.Sum(nil))
}

// BeforeCreate links the event into the per-tenant hash chain inside the same
// transaction as the INSERT (GORM's default transaction wraps Create; the
// hook's tx is that transaction).
//
// Serialization: the chain head row is locked with SELECT ... FOR UPDATE on
// PostgreSQL, so two concurrent inserts for one tenant serialize on the head
// row — the second blocks until the first commits, then reads its RowHash as
// PrevHash. No interleaving can produce two rows with the same PrevHash
// (a fork). On SQLite (hermetic test tier only) there is no FOR UPDATE;
// SQLite's single-writer transaction model provides the equivalent guarantee.
//
// Fail-open: any chain failure (missing chain table, lock error, savepoint
// unsupported) is rolled back to the savepoint and the event is inserted
// unchained (empty hashes = legacy row), with a log + counter. Audit
// availability wins over chain completeness; chain-verify reports such rows
// as legacy, not as breaks.
func (e *AuditEvent) BeforeCreate(tx *gorm.DB) error {
	if e.RowHash != "" {
		// Pre-hashed row (restore tooling / tests seeding explicit chains):
		// trust the caller, do not re-link.
		return nil
	}
	if err := tx.SavePoint(auditChainSavepoint).Error; err != nil {
		// No savepoint support/transaction — skip chaining rather than risk
		// poisoning the insert.
		e.PrevHash, e.RowHash = "", ""
		auditChainFallback("savepoint unavailable: " + err.Error())
		return nil //nolint:nilerr // fail-open by design: audit availability beats chain completeness
	}
	if err := e.chainLink(tx); err != nil {
		if rbErr := tx.RollbackTo(auditChainSavepoint).Error; rbErr != nil {
			// Transaction is unusable; surface the original failure so the
			// async writer logs it instead of committing garbage.
			return fmt.Errorf("audit chain rollback failed: %w (chain error: %w)", rbErr, err)
		}
		e.PrevHash, e.RowHash = "", ""
		auditChainFallback(err.Error())
	}
	return nil
}

// chainLink performs the head-locked link step. Must run inside tx.
func (e *AuditEvent) chainLink(tx *gorm.DB) error {
	// Ensure the head row exists. ON CONFLICT DO NOTHING is race-safe: a
	// concurrent creator blocks on the PK until its tx resolves.
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&AuditChainHead{TenantID: e.TenantID}).Error; err != nil {
		return fmt.Errorf("ensure chain head: %w", err)
	}
	var head AuditChainHead
	q := tx.Where("tenant_id = ?", e.TenantID)
	if tx.Name() == "postgres" {
		// Row lock serializes concurrent chain writers per tenant.
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := q.Take(&head).Error; err != nil {
		return fmt.Errorf("lock chain head: %w", err)
	}
	e.PrevHash = head.LastHash
	e.RowHash = ComputeAuditRowHash(e)
	res := tx.Model(&AuditChainHead{}).Where("tenant_id = ?", e.TenantID).
		Updates(map[string]interface{}{
			"last_hash":  e.RowHash,
			"updated_at": time.Now().Unix(),
		})
	if res.Error != nil {
		return fmt.Errorf("advance chain head: %w", res.Error)
	}
	return nil
}
