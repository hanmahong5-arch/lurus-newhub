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
	ErasureStepContentDeleted  = entity.ErasureStepContentDeleted
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

// erasureStepsPastContent are the cursor values that mean the content step
// (which scrubs quota_data.username) has already run for that request.
// Step holds the last COMPLETED step, so anything at or beyond
// ErasureStepContentDeleted qualifies.
var erasureStepsPastContent = []string{
	ErasureStepContentDeleted,
	ErasureStepLogsAnonymized,
	ErasureStepAuditScrubbed,
}

// HasErasureScrubbedUserContent reports whether an erasure request for this
// user has already passed the content step — either because the whole
// cascade completed, or because it is still running but the cursor is past
// the quota_data scrub. Used on the rare create path of the quota_data
// flush (writeQuotaDataSnapshot, usedata.go) so a bucket another replica
// buffered before the cascade ran cannot re-introduce the plaintext
// username after this one scrubbed it (cycle-13 L5 repair, D-L5-1, the
// cross-replica half).
//
// The in-flight arm is wider than D-L5-1's literal "completed request"
// wording on purpose: the cascade's remaining steps (logs batches, audit
// batches, the user row) can take minutes on a heavy account, and a flush
// landing in that window would put the name straight back. A query error —
// including "relation privacy_erasure_requests does not exist" on a
// deployment that has never migrated the table — answers false, leaving
// the caller's pre-existing behaviour unchanged rather than dropping usage
// data over a lookup.
func HasErasureScrubbedUserContent(ctx context.Context, userID int) bool {
	if userID <= 0 {
		return false
	}
	var count int64
	err := DB.WithContext(ctx).Model(&PrivacyErasureRequest{}).
		Where("user_id = ? AND (status = ? OR step IN ?)", userID, ErasureStatusCompleted, erasureStepsPastContent).
		Limit(1).
		Count(&count).Error
	if err != nil {
		return false
	}
	return count > 0
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

// HardDeleteUserTOTP removes the user's TOTP (RFC 6238) enrollment, if any —
// the shared secret is security-adjacent personal data and was previously
// left behind by the erasure cascade (SEC-C: a purged account could still
// have a live step-up factor in user_totps). No-op (0, nil) when the user
// never enrolled — and, just as importantly, when the user_totps table
// itself does not exist yet. The table is created lazily (ensureUserTOTPTable
// in user_totp.go), and not only on enroll: GetUserTOTP calls it too, and
// GetTotpStatus (handler/totp.go) calls GetUserTOTP for each authenticated
// caller of the Settings TOTP card (unconditional after the auth check),
// enrolled or not. Without this guard, a deployment where
// the table has never been created would fail this DELETE at this exact step
// ("relation user_totps does not exist"); runErasurePass (lifecycle/
// privacy_erasure.go) records the error and retries the request on the next
// tick, per its own comment there.
func HardDeleteUserTOTP(ctx context.Context, userID int) (int64, error) {
	if !DB.Migrator().HasTable(&entity.UserTOTP{}) {
		return 0, nil
	}
	result := WithoutTenantIsolationCtx(ctx, DB).Unscoped().
		Where("user_id = ?", userID).
		Delete(&entity.UserTOTP{})
	if result.Error != nil {
		return 0, fmt.Errorf("hard delete user totp: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// HardDeleteUserTOTPBackupCodes removes the user's TOTP recovery codes (used
// and unused), if any — same security-adjacent-personal-data class as the
// TOTP secret above, so it rides the same erasure step (SEC-C). Same lazy-
// table guard as HardDeleteUserTOTP above and for the same reason: the
// user_totp_backup_codes table is created lazily by
// ensureUserTOTPBackupCodeTable, reached from more than confirm/regenerate/
// force-disable — CountUnusedUserTOTPBackupCodes (GetTotpStatus, for any
// already-enrolled user), ConsumeUserTOTPBackupCode (UniversalVerify method
// totp_backup), DeleteUserTOTPBackupCodes (self-service disable) and
// GetTOTPAdoptionStats (the admin stats endpoint, unconditionally) all call
// it too.
func HardDeleteUserTOTPBackupCodes(ctx context.Context, userID int) (int64, error) {
	if !DB.Migrator().HasTable(&entity.UserTOTPBackupCode{}) {
		return 0, nil
	}
	result := WithoutTenantIsolationCtx(ctx, DB).Unscoped().
		Where("user_id = ?", userID).
		Delete(&entity.UserTOTPBackupCode{})
	if result.Error != nil {
		return 0, fmt.Errorf("hard delete user totp backup codes: %w", result.Error)
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

// HardDeleteUserSessions removes the user's per-device session-registry rows
// (entity.UserSession — L7, auth-security-08/26/29): IP,
// user-agent and session-key are personal-adjacent data, same class as the
// tokens/TOTP rows the erasure cascade already hard-deletes at this step.
// entity.UserSession has no soft-delete column (see its own doc comment) and
// is registered in the boot-time AutoMigrate list (repo/main.go), unlike the
// lazily-created TOTP tables above, so this does not need their HasTable
// guard. Called from the privacy-erasure cascade (lifecycle/privacy_erasure.go
// executeErasure, same step as HardDeleteUserTokens/HardDeleteUserTOTP*) and
// from DeleteUserById (the live user-delete path InternalDeleteUser's
// platform-core user:delete scope calls) — cycle7 L7 repair round 3, finding
// routing-resilience-limits-13#11. (repo.HardDeleteUserById /
// (*User).HardDelete are a separate, currently uncalled hard-delete path;
// this function is not wired into those.)
func HardDeleteUserSessions(ctx context.Context, userID int) (int64, error) {
	result := WithoutTenantIsolationCtx(ctx, DB).Unscoped().
		Where("user_id = ?", userID).
		Delete(&entity.UserSession{})
	if result.Error != nil {
		return 0, fmt.Errorf("hard delete user sessions: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// HardDeleteChatMessagesBatch hard-deletes one batch of the user's saved
// console-Chat messages (cycle-13 L5). The predicate is the UNION of the
// two keys a message can be reached by — session ownership (session_id IN
// chat_sessions owned by the user) OR the message's own user_id column —
// because either one alone leaves a row behind under a divergence the
// other covers: a message whose session row is already gone (orphan) has
// no ownership path, and a message written against another user's session
// is not matched by user_id. The two columns are written together today
// (CreateChatSession / UpdateChatSessionOwned in chat_session.go both set
// SessionId and UserId from the same caller), so the union costs nothing
// while they agree. Same batch shape as AnonymizeLogsBatch
// below: a heavy Chat user does not lock the table in one transaction.
// Callers must drain this before HardDeleteChatSessionsBatch — there is no
// DB-level ON DELETE CASCADE from chat_messages.session_id to
// chat_sessions.id (see entity.ChatSession's doc comment). Returns the
// deleted ids; empty means done.
func HardDeleteChatMessagesBatch(ctx context.Context, userID int, batchSize int) ([]int, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	var ids []int
	err := WithoutTenantIsolationCtx(ctx, DB).Model(&entity.ChatMessage{}).
		Where("(session_id IN (SELECT id FROM chat_sessions WHERE user_id = ?) OR user_id = ?)", userID, userID).
		Order("id ASC").
		Limit(batchSize).
		Pluck("id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("hard delete chat messages batch select: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if err := WithoutTenantIsolationCtx(ctx, DB).Where("id IN ?", ids).Delete(&entity.ChatMessage{}).Error; err != nil {
		return nil, fmt.Errorf("hard delete chat messages batch delete: %w", err)
	}
	return ids, nil
}

// HardDeleteChatSessionsBatch hard-deletes one batch of the user's saved
// console-Chat sessions (cycle-13 L5). Same batch shape as
// AnonymizeLogsBatch; sessions are additionally capped at
// MaxChatSessionsPerUser (chat_session.go) so in practice this drains in
// one call, but the loop shape keeps the caller identical to the other
// batched steps. Call only after HardDeleteChatMessagesBatch has drained —
// see that function's doc comment.
func HardDeleteChatSessionsBatch(ctx context.Context, userID int, batchSize int) ([]int, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	var ids []int
	err := WithoutTenantIsolationCtx(ctx, DB).Model(&entity.ChatSession{}).
		Where("user_id = ?", userID).
		Order("id ASC").
		Limit(batchSize).
		Pluck("id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("hard delete chat sessions batch select: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if err := WithoutTenantIsolationCtx(ctx, DB).Where("id IN ?", ids).Delete(&entity.ChatSession{}).Error; err != nil {
		return nil, fmt.Errorf("hard delete chat sessions batch delete: %w", err)
	}
	return ids, nil
}

// erasedTaskFailReason is the reason the cascade stamps on a task row it
// terminal-ises: a fixed marker rather than request-derived text, so the
// row that stops the poller carries nothing personal even in the window
// before ScrubTasksForUser blanks the column again.
const erasedTaskFailReason = "erased"

// pollerDoneProgress is the progress value both pollers read as "stop
// polling this row": repo.GetAllUnFinishTasks (midjourney.go) and
// repo.GetAllUnFinishSyncTasks (task.go) each filter on `progress !=
// '100%'`, and it is the value handler.UpdateMidjourneyTaskBulk /
// handler.UpdateTaskBulk write when they give up on a row.
const pollerDoneProgress = "100%"

// TerminaliseOpenMidjourneyForUser marks the user's still-polled Midjourney
// rows finished BEFORE HardDeleteMidjourneyBatch removes them (cycle-13 L5
// repair, D-L5-3). Without it, the rows keep matching
// GetAllUnFinishTasks's `progress != '100%'` predicate right up to the
// delete, so the next poller pass can load one, fetch upstream, and call
// repo.MjUpdate — which is DB.Save, and GORM's Save re-Creates a row whose
// UPDATE affected 0 rows (gorm@v1.25.12 finisher_api.go), resurrecting the
// deleted row together with its Prompt/PromptEn.
//
// Residue this does NOT close: a poller pass that had already loaded the
// row before this UPDATE committed still resurrects it (the poller holds
// its own copy for the length of one upstream fetch). The cascade does not
// re-run after MarkErasureCompleted, so such a row survives until an
// operator re-drives the request — see doc/runbook/privacy-erasure.md. A
// full fix needs MjUpdate to stop falling back to Create, which is the
// relay/poller owner's file, not this one's.
func TerminaliseOpenMidjourneyForUser(ctx context.Context, userID int) (int64, error) {
	result := WithoutTenantIsolationCtx(ctx, DB).Model(&Midjourney{}).
		Where("user_id = ? AND progress <> ?", userID, pollerDoneProgress).
		Updates(map[string]interface{}{
			"progress": pollerDoneProgress,
			"status":   TaskStatusFailure,
		})
	if result.Error != nil {
		return 0, fmt.Errorf("terminalise midjourney rows: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// TerminaliseOpenTasksForUser closes the user's async task rows (MJ/Suno/
// video) that the task poller is still tracking, BEFORE ScrubTasksForUser
// blanks them (cycle-13 L5 repair, D-L5-3). repo.GetAllUnFinishSyncTasks
// (task.go) hands the poller the rows whose status is neither FAILURE nor
// SUCCESS and whose progress is not "100%"; handler.UpdateTaskBulk then
// writes `data` and `fail_reason`
// back from the upstream response on its next 15s sync, which would
// re-populate exactly the columns the scrub just emptied. Setting the
// poller's own terminal status takes the rows out of that query. Quota,
// ids, channel and timestamps are untouched — same retention carve-out
// ScrubTasksForUser documents.
//
// fail_reason gets a fixed marker rather than being left as-is; the scrub
// that runs immediately after blanks it again, so the marker is what an
// operator sees only if the cascade stops between the two statements (the
// step then re-runs from the persisted cursor).
//
// Residue, stated rather than implied (cycle-13 acceptance):
//   - A poller pass that had already loaded a row before this UPDATE
//     committed still writes `data`/`fail_reason` back once — the same
//     in-flight window TerminaliseOpenMidjourneyForUser documents; a re-run
//     of the step from the persisted cursor scrubs it again.
//   - Terminal-ising an open task here BYPASSES the refund path. The
//     poller's FAILURE arm (handler.refundTaskQuota via UpdateTaskBulk) is
//     what hands a failed submission's money back, and a row that is already
//     FAILURE never enters that arm — so the key's allowance and the tenant
//     pool stay debited for a task that never completed. The user's balance
//     leg is moot (the account is being erased); the pool and key legs are
//     not, since a reseller's pool paid for them. Owner item
//     O-erasure-inflight-refund (doc/runbook/privacy-erasure.md).
func TerminaliseOpenTasksForUser(ctx context.Context, userID int) (int64, error) {
	result := WithoutTenantIsolationCtx(ctx, DB).Model(&Task{}).
		Where("user_id = ? AND status NOT IN ?", userID, []string{TaskStatusFailure, TaskStatusSuccess}).
		Updates(map[string]interface{}{
			"status":      TaskStatusFailure,
			"progress":    pollerDoneProgress,
			"fail_reason": erasedTaskFailReason,
		})
	if result.Error != nil {
		return 0, fmt.Errorf("terminalise open tasks: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// HardDeleteMidjourneyBatch hard-deletes one batch of the user's Midjourney
// task rows (cycle-13 L5) — Prompt/PromptEn carry the user's original
// generation request verbatim. Same batch shape as AnonymizeLogsBatch.
// Call TerminaliseOpenMidjourneyForUser first: see the residue paragraph
// in that function's doc comment for what a concurrent poller pass can
// otherwise do to a deleted row.
func HardDeleteMidjourneyBatch(ctx context.Context, userID int, batchSize int) ([]int, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	var ids []int
	err := WithoutTenantIsolationCtx(ctx, DB).Model(&Midjourney{}).
		Where("user_id = ?", userID).
		Order("id ASC").
		Limit(batchSize).
		Pluck("id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("hard delete midjourney batch select: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if err := WithoutTenantIsolationCtx(ctx, DB).Where("id IN ?", ids).Delete(&Midjourney{}).Error; err != nil {
		return nil, fmt.Errorf("hard delete midjourney batch delete: %w", err)
	}
	return ids, nil
}

// HardDeletePlaygroundPresetsForUser hard-deletes every Playground preset
// the user saved (cycle-13 L5) — Prompt carries the user's own authored
// prompt text verbatim. Found via
// TestErasureCascadeCoversEveryPersonalDataShapedModel
// (erasure_model_coverage_test.go), not named in the cycle-13 plan's own
// table enumeration. Unlike the batch functions above, presets are created
// one at a time through a console form rather than accumulating
// automatically per request, so this is a single statement rather than a
// batch loop.
func HardDeletePlaygroundPresetsForUser(ctx context.Context, userID int) (int64, error) {
	result := WithoutTenantIsolationCtx(ctx, DB).Where("user_id = ?", userID).Delete(&PlaygroundPreset{})
	if result.Error != nil {
		return 0, fmt.Errorf("hard delete playground presets: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// ScrubTasksForUser blanks the personal-data-bearing columns of every async
// task (MJ/Suno/video) row the user owns (cycle-13 L5): properties (Input
// carries the user's original prompt verbatim; also upstream/origin model
// names), data (raw upstream/vendor payload, can echo request content) and
// fail_reason (can echo request content in the error text). Quota, ids,
// status, channel and every other billing/audit column are left untouched
// so cost attribution and support history survive the erasure — this is a
// scrub, not a delete, matching AnonymizeLogsBatch's treatment of logs. Not
// batched (contrast the chat/Midjourney batch functions above): one user's
// task rows are bounded by how many async jobs they submitted, not by
// years of accumulated request history the way logs are.
func ScrubTasksForUser(ctx context.Context, userID int) (int64, error) {
	result := WithoutTenantIsolationCtx(ctx, DB).Model(&Task{}).
		Where("user_id = ?", userID).
		Updates(map[string]interface{}{
			"properties":  nil,
			"data":        nil,
			"fail_reason": "",
		})
	if result.Error != nil {
		return 0, fmt.Errorf("scrub tasks: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// ScrubQuotaDataUsernameForUser overwrites quota_data.username with
// ErasedMarker for every row the user owns (cycle-13 L5). Queries against
// this table go through .Table("quota_data") rather than
// .Model(&QuotaData{}) throughout usedata.go, so this function matches
// that convention rather than relying on GORM's pluralization inferring
// the same table name from the bare struct. quota_data has no soft-delete
// column; the billing-relevant columns (quota, count, token_used) are
// retained under the same statutory-retention carve-out AnonymizeLogsBatch
// documents below — only the display name is personal data here.
// Resumable: rows already carrying ErasedMarker are excluded, matching
// AnonymizeLogsBatch's own resume convention.
//
// The in-process write-behind cache is purged first (cycle-13 L5 repair,
// D-L5-1). usedata.go buffers each relay call's bucket in CacheQuotaData
// and flushes it DataExportInterval minutes later; the flush looks the row
// up by (user_id, username, model_name, created_at), so after this UPDATE
// it misses and Creates a fresh row carrying the plaintext username again.
// Dropping the user's buckets under CacheQuotaDataLock before the UPDATE
// closes that window on THIS replica; the other replicas' caches are
// handled at flush time by the completed-erasure check in
// writeQuotaDataSnapshot (usedata.go). Buckets dropped here are usage
// aggregates for an account being erased — losing them is the point.
func ScrubQuotaDataUsernameForUser(ctx context.Context, userID int) (int64, error) {
	CacheQuotaDataLock.Lock()
	for key, bucket := range CacheQuotaData {
		if bucket != nil && bucket.UserID == userID {
			delete(CacheQuotaData, key)
		}
	}
	CacheQuotaDataLock.Unlock()

	result := WithoutTenantIsolationCtx(ctx, DB).Table("quota_data").
		Where("user_id = ? AND username <> ?", userID, ErasedMarker).
		Update("username", ErasedMarker)
	if result.Error != nil {
		return 0, fmt.Errorf("scrub quota data username: %w", result.Error)
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
