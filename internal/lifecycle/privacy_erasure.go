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
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/search"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
)

// privacyErasureTaskName is the "task" label this job stamps on
// metrics.LeaderTaskLastSuccess and registers under in taskreg.
const privacyErasureTaskName = "privacy-erasure"

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

	metrics.LeaderTaskLastSuccess.WithLabelValues(privacyErasureTaskName).Set(0)
	taskreg.Register(privacyErasureTaskName, func() time.Duration { return interval }, true, nil)

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
	// anyFailed tracks whether any request in this pass errored — the
	// per-request error itself is swallowed below (crash-resume design: one
	// stuck request must not halt the others), but the heartbeat must not
	// advance on a pass that left work broken.
	anyFailed := false
	for _, req := range pending {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := executeErasure(ctx, req); err != nil {
			anyFailed = true
			common.SysError(fmt.Sprintf(
				`{"event":"privacy_erase_step_failed","who":"account:%d/user:%d","what":"erasure %s at step %q","result":"failed, will retry: %s"}`,
				req.AccountID, req.UserID, req.EventID, req.Step, err.Error()))
			if recErr := repo.RecordErasureError(ctx, req.ID, err.Error()); recErr != nil {
				common.SysError(fmt.Sprintf("privacy erasure: record error on %s: %v", req.EventID, recErr))
			}
		}
	}
	if !anyFailed {
		metrics.RecordLeaderTaskSuccess(privacyErasureTaskName)
	}
}

// executeErasure advances one request through the disposition steps in order,
// persisting the cursor after each completed step (crash-resume). Steps:
//
//  1. tokens               hard delete (incl. soft-deleted) + user_totps + user_totp_backup_codes + user_sessions + response_registry hard delete
//  2. user_identity_mapping hard delete
//  3. content              (cycle-13 L5) chat_messages/chat_sessions/midjourneys hard
//     delete in batches (midjourneys terminal-ised first), open tasks terminal-ised
//     then scrubbed (properties/data/fail_reason), quota_data username scrubbed
//     (incl. this replica's write-behind cache), playground_presets hard delete
//
// A cursor value none of the steps below recognises is an error, not a
// silent no-op — see the final return.
//  4. logs                 pseudonymize in batches + best-effort Meili purge
//  5. audit_events         scrub ip/details in batches
//  6. users                anonymize in place + soft delete → completed
func executeErasure(ctx context.Context, req *repo.PrivacyErasureRequest) error {
	step := req.Step

	if step == repo.ErasureStepNone {
		if _, err := repo.HardDeleteUserTokens(ctx, req.UserID); err != nil {
			return err
		}
		// TOTP secret + its recovery codes ride the same step (no new cursor
		// value) — same class of security-adjacent personal data as the
		// tokens they step up alongside (SEC-C).
		if _, err := repo.HardDeleteUserTOTP(ctx, req.UserID); err != nil {
			return err
		}
		if _, err := repo.HardDeleteUserTOTPBackupCodes(ctx, req.UserID); err != nil {
			return err
		}
		// user_sessions rides the same step — same personal-adjacent-data
		// class as the tokens/TOTP rows above (L7 repair round 3, finding
		// routing-resilience-limits-13#11).
		if _, err := repo.HardDeleteUserSessions(ctx, req.UserID); err != nil {
			return err
		}
		// response_registry rides the same step — same personal-adjacent-data
		// class as user_sessions above: it holds user_id/token_id/vendor
		// response ids for up to RESPONSE_REGISTRY_TTL_DAYS (default 30),
		// which is longer than this cascade's own crash-resume latency
		// (cycle-8 L7 repair round, finding B-F9).
		if _, err := repo.HardDeleteUserResponseRegistry(ctx, req.UserID); err != nil {
			return err
		}
		// Delegated permission grants (L4, admin_permission_grants) ride
		// the same step: a grant is security-adjacent access, not billing
		// data, so it is revoked alongside the tokens/sessions it sits next
		// to rather than surviving into the anonymized user row (cycle-8 L4
		// repair round, B-F5).
		if _, err := repo.RevokePermissionGrantsForUser(req.UserID); err != nil {
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
		// chat_messages before chat_sessions: messages have no DB-level ON
		// DELETE CASCADE to their session (entity.ChatSession's doc comment),
		// so draining the child batch first keeps every partial pass
		// consistent even if a crash lands between the two loops.
		for {
			ids, err := repo.HardDeleteChatMessagesBatch(ctx, req.UserID, erasureBatchSize)
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				break
			}
		}
		for {
			ids, err := repo.HardDeleteChatSessionsBatch(ctx, req.UserID, erasureBatchSize)
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				break
			}
		}
		// Midjourney rows are taken out of the poller's selection before
		// they are deleted: repo.MjUpdate is DB.Save, which re-Creates a
		// row whose UPDATE matched nothing, so a poller pass running
		// against rows it loaded earlier can resurrect a deleted row
		// (residue documented on TerminaliseOpenMidjourneyForUser).
		if _, err := repo.TerminaliseOpenMidjourneyForUser(ctx, req.UserID); err != nil {
			return err
		}
		for {
			ids, err := repo.HardDeleteMidjourneyBatch(ctx, req.UserID, erasureBatchSize)
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				break
			}
		}
		// Async tasks are scrubbed in place, not deleted, so the poller has
		// to be told to stop first — otherwise its next 15s sync writes
		// data/fail_reason back from the upstream response (cycle-13 L5
		// repair, D-L5-3).
		if _, err := repo.TerminaliseOpenTasksForUser(ctx, req.UserID); err != nil {
			return err
		}
		if _, err := repo.ScrubTasksForUser(ctx, req.UserID); err != nil {
			return err
		}
		if _, err := repo.ScrubQuotaDataUsernameForUser(ctx, req.UserID); err != nil {
			return err
		}
		// Playground presets: found via the model-coverage forcing-function
		// test (erasure_model_coverage_test.go), not in the cycle-13 plan's
		// own table enumeration — Prompt carries the user's authored text
		// verbatim the same way Midjourney's does.
		if _, err := repo.HardDeletePlaygroundPresetsForUser(ctx, req.UserID); err != nil {
			return err
		}
		if err := repo.AdvanceErasureStep(ctx, req.ID, repo.ErasureStepContentDeleted, 0); err != nil {
			return err
		}
		step = repo.ErasureStepContentDeleted
	}

	if step == repo.ErasureStepContentDeleted {
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
		return nil
	}

	// Reached only when the entry cursor matched none of the branches
	// above — each branch advances into the next one and the last returns.
	// That happens when the row carries a step value a NEWER build wrote —
	// a later cycle adds a step, then the deployment rolls back to this
	// binary. Returning nil here would leave the request pending forever
	// while runErasurePass kept stamping the leader-task heartbeat, so the
	// pass records it as a per-request error (last_error) instead. The
	// mirror case cannot be fixed from here: a build older than cycle-13
	// has no arm like this one and still no-ops on content_deleted.
	return fmt.Errorf("unknown erasure step %q: this build has no cascade branch for it (cursor written by a newer build?)", step)
}
