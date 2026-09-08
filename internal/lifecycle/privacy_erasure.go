package lifecycle

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/search"
)

// AuditActionErasureCompleted marks the cascade's terminal audit event.
const AuditActionErasureCompleted = "privacy.erasure.completed"

// erasureBatchSize bounds each anonymization / scrub statement so a user
// with years of logs doesn't lock the table in one transaction. Matches
// auditCleanupBatchSize.
const erasureBatchSize = 500

// erasureDefaultInterval is the pause between executor passes. Minutes, not
// hours: PIPL erasure is user-facing (platform polls progress) so daily
// cadence would look like the request was dropped.
const erasureDefaultInterval = time.Minute

// StartPrivacyErasureWithContext launches the PIPL §47 erasure executor.
// Leader-only (same HA rule as audit cleanup): the cascade is idempotent and
// crash-resumable via the per-request step cursor, but duplicate concurrent
// runs would double-scan the log table for nothing.
func StartPrivacyErasureWithContext(ctx context.Context) {
	interval := erasureDefaultInterval
	if raw := os.Getenv("PRIVACY_ERASURE_INTERVAL_SECONDS"); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			interval = time.Duration(secs) * time.Second
		}
	}
	common.SysLog(fmt.Sprintf("privacy erasure executor started, interval=%s", interval))

	ticker := time.NewTicker(interval)
	common.SafeGoWithContext(ctx, func(c context.Context) {
		defer ticker.Stop()
		if common.IsLeader() {
			runErasurePass(c)
		}
		for {
			select {
			case <-c.Done():
				common.SysLog("privacy erasure executor stopped")
				return
			case <-ticker.C:
				if !common.IsLeader() {
					continue
				}
				runErasurePass(c)
			}
		}
	})
}

// runErasurePass drains the pending queue. Per-request errors are recorded on
// the row and retried next tick from the persisted step cursor — one stuck
// request must not halt the others.
func runErasurePass(ctx context.Context) {
	pending, err := repo.ListPendingErasureRequests(ctx, 10)
	if err != nil {
		common.SysError(fmt.Sprintf("privacy erasure: list pending: %v", err))
		return
	}
	for _, req := range pending {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := executeErasure(ctx, req); err != nil {
			common.SysError(fmt.Sprintf(
				`{"event":"privacy_erase_step_failed","who":"account:%d/user:%d","what":"erasure %s at step %q","result":"failed, will retry: %s"}`,
				req.AccountID, req.UserID, req.EventID, req.Step, err.Error()))
			if recErr := repo.RecordErasureError(ctx, req.ID, err.Error()); recErr != nil {
				common.SysError(fmt.Sprintf("privacy erasure: record error on %s: %v", req.EventID, recErr))
			}
		}
	}
}

// executeErasure advances one request through the disposition steps in order,
// persisting the cursor after each completed step (crash-resume). Steps:
//
//  1. tokens               hard delete (incl. soft-deleted) + user_totps hard delete
//  2. user_identity_mapping hard delete
//  3. logs                 pseudonymize in batches + best-effort Meili purge
//  4. audit_events         scrub ip/details in batches
//  5. users                anonymize in place + soft delete → completed
func executeErasure(ctx context.Context, req *repo.PrivacyErasureRequest) error {
	step := req.Step

	if step == repo.ErasureStepNone {
		if _, err := repo.HardDeleteUserTokens(ctx, req.UserID); err != nil {
			return err
		}
		// TOTP secret rides the same step (no new cursor value) — it is the
		// same class of security-adjacent personal data as the tokens it
		// steps up alongside (SEC-C).
		if _, err := repo.HardDeleteUserTOTP(ctx, req.UserID); err != nil {
			return err
		}
		if err := repo.AdvanceErasureStep(ctx, req.ID, repo.ErasureStepTokensDeleted, 0); err != nil {
			return err
		}
		step = repo.ErasureStepTokensDeleted
	}

	if step == repo.ErasureStepTokensDeleted {
		if _, err := repo.HardDeleteUserIdentityMappings(ctx, req.UserID); err != nil {
			return err
		}
		if err := repo.AdvanceErasureStep(ctx, req.ID, repo.ErasureStepMappingsDeleted, 0); err != nil {
			return err
		}
		step = repo.ErasureStepMappingsDeleted
	}

	if step == repo.ErasureStepMappingsDeleted {
		var scrubbed int64
		for {
			ids, err := repo.AnonymizeLogsBatch(ctx, req.UserID, erasureBatchSize)
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				break
			}
			scrubbed += int64(len(ids))
			// Best-effort: Meilisearch is a rebuildable secondary index; a
			// failed purge must not wedge the legally-required PG cascade.
			if err := search.DeleteLogsByIDs(ids); err != nil {
				common.SysError(fmt.Sprintf("privacy erasure: meilisearch purge (%d docs) non-fatal: %v", len(ids), err))
			}
		}
		if err := repo.AdvanceErasureStep(ctx, req.ID, repo.ErasureStepLogsAnonymized, scrubbed); err != nil {
			return err
		}
		step = repo.ErasureStepLogsAnonymized
	}

	if step == repo.ErasureStepLogsAnonymized {
		for {
			n, err := repo.ScrubAuditEventsBatch(ctx, req.UserID, erasureBatchSize)
			if err != nil {
				return err
			}
			if n == 0 {
				break
			}
		}
		if err := repo.AdvanceErasureStep(ctx, req.ID, repo.ErasureStepAuditScrubbed, 0); err != nil {
			return err
		}
		step = repo.ErasureStepAuditScrubbed
	}

	if step == repo.ErasureStepAuditScrubbed {
		if err := repo.AnonymizeUserRow(ctx, req.UserID); err != nil {
			return err
		}
		if err := repo.MarkErasureCompleted(ctx, req.ID); err != nil {
			return err
		}
		common.SysLog(fmt.Sprintf(
			`{"event":"privacy_erase_completed","who":"account:%d/user:%d","what":"erasure %s cascade","result":"completed"}`,
			req.AccountID, req.UserID, req.EventID))
		governance.RecordAuditEvent(&entity.AuditEvent{
			TenantID:   req.TenantID,
			Timestamp:  common.GetTimestamp(),
			ActorType:  "system",
			Action:     AuditActionErasureCompleted,
			Resource:   "user",
			ResourceID: req.UserID,
			Details:    fmt.Sprintf(`{"event_id":%q,"account_id":%d}`, req.EventID, req.AccountID),
		})
	}

	return nil
}
