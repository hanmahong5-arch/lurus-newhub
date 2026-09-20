package entity

import "time"

// Erasure request lifecycle states.
//
// pending   — intent row landed, background executor has work to do
// completed — every disposition step ran; the row stays as compliance evidence
// no_data   — the platform account had no newhub user; recorded so the caller
//
//	gets a durable, queryable answer instead of a transient 404
const (
	ErasureStatusPending   = "pending"
	ErasureStatusCompleted = "completed"
	ErasureStatusNoData    = "no_data"
)

// Erasure executor steps, advanced in order so a crash resumes where it
// stopped instead of redoing (or worse, half-redoing) earlier steps. Every
// step is idempotent on its own, but step tracking keeps a resumed run from
// re-scanning tables that are already clean.
const (
	ErasureStepNone            = ""                 // not started
	ErasureStepTokensDeleted   = "tokens_deleted"   // tokens hard-deleted (incl. soft-deleted)
	ErasureStepMappingsDeleted = "mappings_deleted" // user_identity_mapping hard-deleted
	// ErasureStepContentDeleted (cycle-13 L5) sits between mappings and logs:
	// chat_messages/chat_sessions/midjourneys hard-deleted (midjourneys
	// first taken out of the poller's selection), open async tasks
	// terminal-ised then scrubbed (properties/data/fail_reason blanked,
	// quota/ids retained), quota_data username scrubbed, playground_presets
	// hard-deleted. See lifecycle.executeErasure for the exact call
	// sequence and doc/runbook/privacy-erasure.md for the operator view.
	ErasureStepContentDeleted = "content_deleted"
	ErasureStepLogsAnonymized = "logs_anonymized" // logs pseudonymized + Meili purge
	ErasureStepAuditScrubbed  = "audit_scrubbed"  // audit_events ip/details scrubbed
)

// ErasedMarker replaces personal-data string fields during anonymization.
// A non-empty sentinel (vs "") lets the batched scans distinguish
// "already anonymized" from "legitimately blank" and stay resumable.
const ErasedMarker = "[erased]"

// PrivacyErasureRequest is the intent + progress + evidence row for a PIPL
// §47 account-erasure request from lurus-platform. EventID is UNIQUE so a
// replayed POST is answered from this row instead of re-running the cascade.
//
// Dispositions (see doc/decisions ADR + contracts.md):
//
//	tokens                  hard delete (Unscoped, incl. soft-deleted)
//	user_identity_mapping   hard delete
//	chat_messages/sessions  hard delete (cycle-13 L5; messages first, via session ownership)
//	midjourneys             terminal-ise, then hard delete (cycle-13 L5; Prompt/PromptEn
//	                        carry the request verbatim)
//	playground_presets      hard delete (cycle-13 L5)
//	tasks                   terminal-ise the still-polled rows, then scrub
//	                        properties/data/fail_reason in place (cycle-13 L5);
//	                        quota/ids retained (cost attribution + support history)
//	quota_data              scrub username to ErasedMarker in place, incl. the replica's
//	                        write-behind cache (cycle-13 L5);
//	                        quota/count/token_used retained (statutory-retention carve-out)
//	users                   anonymize in place + soft delete, lurus_account_id → NULL
//	logs                    pseudonymize username/token_name/ip/content/other;
//	                        billing fields retained (PIPL statutory-retention carve-out)
//	Meilisearch logs        best-effort DeleteLogsByIDs per anonymized batch
//	audit_events            scrub ip/details; action + timestamps retained (existing TTL applies)
//	redemptions/pool draws/this table — retained (financial ledger / compliance evidence)
type PrivacyErasureRequest struct {
	ID        int64  `json:"id"         gorm:"primaryKey;autoIncrement"`
	EventID   string `json:"event_id"   gorm:"type:varchar(128);not null;uniqueIndex"`
	AccountID int64  `json:"account_id" gorm:"type:bigint;not null;index"`
	RequestID string `json:"request_id" gorm:"type:varchar(64);default:''"`
	Reason    string `json:"reason"     gorm:"type:varchar(255);default:''"`
	// UserID/TenantID are resolved at intake; UserID = 0 for no_data rows.
	UserID   int    `json:"user_id"   gorm:"default:0;index"`
	TenantID string `json:"tenant_id" gorm:"type:varchar(36);default:''"`
	Status   string `json:"status"    gorm:"type:varchar(16);not null;index"`
	// Step is the last COMPLETED executor step (crash-resume cursor).
	Step         string     `json:"step"           gorm:"type:varchar(32);default:''"`
	LogsScrubbed int64      `json:"logs_scrubbed"  gorm:"type:bigint;default:0"`
	LastError    string     `json:"last_error"     gorm:"type:varchar(512);default:''"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

// TableName overrides the default GORM table name.
func (PrivacyErasureRequest) TableName() string {
	return "privacy_erasure_requests"
}
