package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"gorm.io/gorm"
)

// Re-export so handler / lifecycle layers stay decoupled from entity.
type PrivacyErasureRequest = entity.PrivacyErasureRequest

const (
	ErasureStatusPending   = entity.ErasureStatusPending
	ErasureStatusCompleted = entity.ErasureStatusCompleted
	ErasureStatusNoData    = entity.ErasureStatusNoData

	ErasureStepNone            = entity.ErasureStepNone
	ErasureStepTokensDeleted   = entity.ErasureStepTokensDeleted
	ErasureStepMappingsDeleted = entity.ErasureStepMappingsDeleted
	ErasureStepLogsAnonymized  = entity.ErasureStepLogsAnonymized
	ErasureStepAuditScrubbed   = entity.ErasureStepAuditScrubbed

	ErasedMarker = entity.ErasedMarker
)

// ErrErasureEventExists is returned by CreateErasureRequestIdempotent when the
// event_id was already recorded. Callers treat this as a successful replay.
var ErrErasureEventExists = errors.New("erasure event already recorded (idempotent replay)")

// CreateErasureRequestIdempotent inserts the PIPL §47 erasure intent row.
// Idempotency mirrors FundPoolIdempotent: UNIQUE(event_id) is the
// authoritative guard; on conflict the existing row is returned with
// ErrErasureEventExists so the endpoint answers 200 replayed:true.
func CreateErasureRequestIdempotent(
	ctx context.Context,
	eventID string, accountID int64, requestID, reason string,
	userID int, tenantID string, status string,
) (*PrivacyErasureRequest, error) {
	if eventID == "" {
		return nil, fmt.Errorf("event_id is required for idempotent erasure: provide the platform erasure event ID")
	}
	if accountID <= 0 {
		return nil, fmt.Errorf("account_id must be > 0, got %d", accountID)
	}

	// Fast-path replay check; the unique constraint below remains authoritative.
	var existing PrivacyErasureRequest
	if err := DB.WithContext(ctx).Where("event_id = ?", eventID).First(&existing).Error; err == nil {
		return &existing, ErrErasureEventExists
	}

	now := time.Now()
	row := &PrivacyErasureRequest{
		EventID:   eventID,
		AccountID: accountID,
		RequestID: requestID,
		Reason:    reason,
		UserID:    userID,
		TenantID:  tenantID,
		Status:    status,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if status == ErasureStatusNoData || status == ErasureStatusCompleted {
		row.CompletedAt = &now
	}

	if err := DB.WithContext(ctx).Create(row).Error; err != nil {
		if isUniqueViolation(err) {
			var race PrivacyErasureRequest
			if fetchErr := DB.WithContext(ctx).Where("event_id = ?", eventID).First(&race).Error; fetchErr != nil {
				return nil, fmt.Errorf("erasure insert conflict and re-fetch failed: %w", fetchErr)
			}
			return &race, ErrErasureEventExists
		}
		return nil, fmt.Errorf("erasure request insert: %w", err)
	}
	return row, nil
}

// GetErasureRequestByEventID fetches one request for the platform poll /
// compliance-evidence endpoint. Returns gorm.ErrRecordNotFound when absent.
func GetErasureRequestByEventID(ctx context.Context, eventID string) (*PrivacyErasureRequest, error) {
	var row PrivacyErasureRequest
	err := DB.WithContext(ctx).Where("event_id = ?", eventID).First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ListPendingErasureRequests returns the executor's work queue, oldest first
// so a stuck request cannot starve newer ones of their place in line forever
// (each pass retries from the front).
func ListPendingErasureRequests(ctx context.Context, limit int) ([]*PrivacyErasureRequest, error) {
	if limit <= 0 {
		limit = 10
	}
	var rows []*PrivacyErasureRequest
	err := DB.WithContext(ctx).Where("status = ?", ErasureStatusPending).
		Order("created_at ASC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list pending erasure requests: %w", err)
	}
	return rows, nil
}

// AdvanceErasureStep persists the crash-resume cursor after a step completes.
func AdvanceErasureStep(ctx context.Context, id int64, step string, logsScrubbedDelta int64) error {
	updates := map[string]interface{}{
		"step":       step,
		"last_error": "",
		"updated_at": time.Now(),
	}
	if logsScrubbedDelta > 0 {
		updates["logs_scrubbed"] = gorm.Expr("logs_scrubbed + ?", logsScrubbedDelta)
	}
	return DB.WithContext(ctx).Model(&PrivacyErasureRequest{}).Where("id = ?", id).Updates(updates).Error
}

// MarkErasureCompleted finalizes the request row (kept as compliance evidence).
func MarkErasureCompleted(ctx context.Context, id int64) error {
	now := time.Now()
	return DB.WithContext(ctx).Model(&PrivacyErasureRequest{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":       ErasureStatusCompleted,
		"last_error":   "",
		"completed_at": now,
		"updated_at":   now,
	}).Error
}

// RecordErasureError stores the latest executor failure without changing
// status — the next tick retries from the persisted step cursor.
func RecordErasureError(ctx context.Context, id int64, msg string) error {
	if len(msg) > 512 {
		msg = msg[:512]
	}
	return DB.WithContext(ctx).Model(&PrivacyErasureRequest{}).Where("id = ?", id).Updates(map[string]interface{}{
		"last_error": msg,
		"updated_at": time.Now(),
	}).Error
}

// DisableTokensByUserID flips every enabled token of the user to disabled —
// the synchronous "stop the bleeding" action at erasure intake, before the
// background cascade hard-deletes them.
func DisableTokensByUserID(ctx context.Context, userID int) (int64, error) {
	result := WithoutTenantIsolationCtx(ctx, DB).Model(&Token{}).
		Where("user_id = ?", userID).
		Update("status", common.TokenStatusDisabled)
	return result.RowsAffected, result.Error
}

// HardDeleteUserTokens removes ALL tokens of the user, including soft-deleted
// rows (Unscoped) — token keys and names are personal-adjacent data.
func HardDeleteUserTokens(ctx context.Context, userID int) (int64, error) {
	result := WithoutTenantIsolationCtx(ctx, DB).Unscoped().
		Where("user_id = ?", userID).
		Delete(&Token{})
	if result.Error != nil {
		return 0, fmt.Errorf("hard delete user tokens: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// HardDeleteUserIdentityMappings removes the OIDC identity binding rows
// (email / display name / preferred username), including soft-deleted rows.
func HardDeleteUserIdentityMappings(ctx context.Context, userID int) (int64, error) {
	result := WithoutTenantIsolationCtx(ctx, DB).Unscoped().
		Where("lurus_user_id = ?", userID).
		Delete(&UserIdentityMapping{})
	if result.Error != nil {
		return 0, fmt.Errorf("hard delete identity mappings: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// AnonymizeLogsBatch pseudonymizes one batch of the user's logs and returns
// the affected log IDs so the caller can purge the same documents from
// Meilisearch. Billing fields (quota, token counts, model, timestamps) are
// retained under the PIPL statutory-retention carve-out; this is
// pseudonymization, not deletion.
//
// Resumable: rows already carrying ErasedMarker in username are skipped, so
// re-running after a crash continues where the previous pass stopped.
// Returns an empty slice when the user has no rows left to scrub.
func AnonymizeLogsBatch(ctx context.Context, userID int, batchSize int) ([]int, error) {
	if batchSize <= 0 {
		batchSize = 500
	}

	var ids []int
	err := WithoutTenantIsolationCtx(ctx, LOG_DB).Model(&Log{}).
		Where("user_id = ? AND username <> ?", userID, ErasedMarker).
		Order("id ASC").
		Limit(batchSize).
		Pluck("id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("anonymize logs batch select: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	err = WithoutTenantIsolationCtx(ctx, LOG_DB).Model(&Log{}).
		Where("id IN ?", ids).
		Updates(map[string]interface{}{
			"username":   ErasedMarker,
			"token_name": ErasedMarker,
			"ip":         "",
			"content":    "",
			"other":      "",
		}).Error
	if err != nil {
		return nil, fmt.Errorf("anonymize logs batch update: %w", err)
	}
	return ids, nil
}

// ScrubAuditEventsBatch removes personal data (ip, details) from one batch of
// the user's audit events while keeping action/resource/timestamps — the
// security trail stays intact under its existing retention TTL. Returns the
// number of rows scrubbed; 0 means done.
func ScrubAuditEventsBatch(ctx context.Context, userID int, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 500
	}

	var ids []int64
	err := DB.WithContext(ctx).Model(&entity.AuditEvent{}).
		Where("actor_id = ? AND actor_type = ? AND (ip <> '' OR details <> ?)",
			userID, "user", ErasedMarker).
		Order("id ASC").
		Limit(batchSize).
		Pluck("id", &ids).Error
	if err != nil {
		return 0, fmt.Errorf("scrub audit events select: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}

	// Migration 024 makes audit_events append-only at the DB level; this PIPL
	// scrub is the ONE sanctioned UPDATE path and must announce itself via the
	// transaction-scoped GUC app.audit_redaction (set_config(..., true) is
	// transaction-local, so the gate closes again at COMMIT/ROLLBACK). The
	// trigger additionally verifies only ip/details change — the hash-chain
	// columns stay immutable, so redaction does not break chain verification.
	var affected int64
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if common.UsingPostgreSQL {
			if gucErr := tx.Exec(`SELECT set_config('app.audit_redaction', 'on', true)`).Error; gucErr != nil {
				return fmt.Errorf("enable audit redaction gate: %w", gucErr)
			}
		}
		result := tx.Model(&entity.AuditEvent{}).
			Where("id IN ?", ids).
			Updates(map[string]interface{}{
				"ip":      "",
				"details": ErasedMarker,
			})
		if result.Error != nil {
			return result.Error
		}
		affected = result.RowsAffected
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("scrub audit events update: %w", err)
	}
	return affected, nil
}

// AnonymizeUserRow rewrites the user's personal fields in place, severs the
// platform binding (lurus_account_id → NULL so the account can never re-bind),
// disables the account, and soft-deletes the row. The integer id survives so
// financial ledgers (redemptions, pool draws, logs) keep a valid pseudonymous
// reference.
func AnonymizeUserRow(ctx context.Context, userID int) error {
	if userID == 0 {
		return errors.New("user id is required")
	}
	now := time.Now()
	err := WithoutTenantIsolationCtx(ctx, DB).Model(&User{}).
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"username":         fmt.Sprintf("erased_%d", userID), // unique index needs a distinct value
			"display_name":     ErasedMarker,
			"email":            "",
			"remark":           "",
			"setting":          "",
			"access_token":     nil,
			"lurus_account_id": nil,
			"status":           common.UserStatusDisabled,
			"deleted_at":       now,
		}).Error
	if err != nil {
		return fmt.Errorf("anonymize user row: %w", err)
	}
	return nil
}
