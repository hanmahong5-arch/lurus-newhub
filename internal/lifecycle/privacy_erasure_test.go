package lifecycle

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
	"github.com/glebarez/sqlite"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"gorm.io/gorm"
)

var erasureDBCounter atomic.Int64

func openErasureTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:erasure%d?mode=memory&cache=shared", erasureDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, m := range []interface{}{
		&repo.User{}, &repo.Token{}, &repo.Log{},
		&entity.UserIdentityMapping{}, &entity.AuditEvent{},
		&entity.PrivacyErasureRequest{}, &entity.UserTOTP{}, &entity.UserTOTPBackupCode{},
		&entity.UserSession{}, &entity.AdminPermissionGrant{}, &entity.ResponseRegistry{},
	} {
		if err := db.AutoMigrate(m); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("migrate %T: %v", m, err)
		}
	}

	prevDB, prevLogDB := repo.DB, repo.LOG_DB
	repo.DB, repo.LOG_DB = db, db
	t.Cleanup(func() {
		repo.DB, repo.LOG_DB = prevDB, prevLogDB
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// seedErasureFixture creates one user with tokens (incl. one soft-deleted),
// identity mapping, logs and audit events — the full disposition surface.
func seedErasureFixture(t *testing.T, db *gorm.DB, logCount int) (userID int, reqRow *entity.PrivacyErasureRequest) {
	t.Helper()

	accountID := int64(4242)
	user := repo.User{
		Username:       "victim",
		DisplayName:    "Victim User",
		Email:          "victim@example.com",
		Status:         common.UserStatusEnabled,
		Group:          "default",
		LurusAccountID: &accountID,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	for i := 0; i < 3; i++ {
		tok := repo.Token{
			UserId: user.Id, Key: fmt.Sprintf("erasekey%027d", i),
			Status: common.TokenStatusEnabled, Name: fmt.Sprintf("tok-%d", i),
		}
		if err := db.Create(&tok).Error; err != nil {
			t.Fatalf("seed token: %v", err)
		}
		if i == 2 { // one soft-deleted token must still be hard-deleted
			if err := db.Delete(&tok).Error; err != nil {
				t.Fatalf("soft delete token: %v", err)
			}
		}
	}

	if err := db.Create(&entity.UserIdentityMapping{
		LurusUserID: user.Id, IDPSubject: "zit-123", TenantID: "default",
		Email: "victim@example.com", DisplayName: "Victim User",
	}).Error; err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	if err := db.Create(&entity.UserTOTP{
		UserId: user.Id, SecretEncrypted: "ct-fixture", Enabled: true,
		CreatedAt: time.Now().Unix(), ConfirmedAt: time.Now().Unix(),
	}).Error; err != nil {
		t.Fatalf("seed totp: %v", err)
	}
	// One unused + one already-consumed backup code — both must be gone
	// after erasure, not just the unused one.
	if err := db.Create(&entity.UserTOTPBackupCode{
		UserId: user.Id, CodeHash: "erase-fixture-hash-unused", CreatedAt: time.Now().Unix(),
	}).Error; err != nil {
		t.Fatalf("seed unused backup code: %v", err)
	}
	if err := db.Create(&entity.UserTOTPBackupCode{
		UserId: user.Id, CodeHash: "erase-fixture-hash-used",
		CreatedAt: time.Now().Unix(), UsedAt: time.Now().Unix(),
	}).Error; err != nil {
		t.Fatalf("seed used backup code: %v", err)
	}
	// A per-device session-registry row (L7) — must not survive erasure
	// either (cycle7 L7 repair round 3, finding routing-resilience-limits-13#11).
	if err := db.Create(&entity.UserSession{
		SessionKey: "erase-fixture-session-key", UserId: user.Id, TenantId: "default",
		CreatedAt: time.Now().Unix(), LastSeenAt: time.Now().Unix(),
	}).Error; err != nil {
		t.Fatalf("seed user session: %v", err)
	}
	// A delegated permission grant (L4) — must not survive erasure either
	// (cycle-8 L4 repair round, B-F5): security-adjacent access, same class
	// as the tokens/sessions/TOTP rows above.
	if _, _, err := repo.CreatePermissionGrant(user.Id, "audit", "read", 1); err != nil {
		t.Fatalf("seed permission grant: %v", err)
	}
	// A response_registry row (L7) — must not survive erasure either
	// (cycle-8 L7 repair round, B-F9): personal-adjacent data (user_id,
	// token_id, vendor response id), same class as the user_sessions row
	// above, retained for up to RESPONSE_REGISTRY_TTL_DAYS otherwise.
	now := time.Now().Unix()
	if err := db.Create(&entity.ResponseRegistry{
		ResponseId: "erase-fixture-resp-1", TenantId: "default", UserId: user.Id,
		TokenId: 1, ChannelId: 1, UpstreamModel: "gpt-4o-mini",
		CreatedAt: now, ExpiresAt: now + 86400,
	}).Error; err != nil {
		t.Fatalf("seed response registry row: %v", err)
	}

	for i := 0; i < logCount; i++ {
		if err := db.Create(&entity.Log{
			UserId: user.Id, Username: "victim", TokenName: "tok-0",
			Ip: "10.0.0.9", Content: "prompt detail", Other: `{"x":1}`,
			ModelName: "gpt-x", Quota: 7, CreatedAt: time.Now().Unix(),
		}).Error; err != nil {
			t.Fatalf("seed log %d: %v", i, err)
		}
	}

	if err := db.Create(&entity.AuditEvent{
		ActorType: "user", ActorID: user.Id, Action: "token.created",
		IP: "10.0.0.9", Details: `{"name":"tok-0"}`, Timestamp: 1, TenantID: "default",
	}).Error; err != nil {
		t.Fatalf("seed audit: %v", err)
	}

	row, err := repo.CreateErasureRequestIdempotent(context.Background(),
		"evt-erase-1", accountID, "", "user_requested",
		user.Id, "default", repo.ErasureStatusPending)
	if err != nil {
		t.Fatalf("seed erasure request: %v", err)
	}
	return user.Id, row
}

// TestExecuteErasure_FullCascade seeds 1200 logs (>2 batches of 500), 3 tokens
// (one soft-deleted), a mapping and an audit event, runs the cascade, and
// asserts every disposition from the contracts table. Then re-runs — must be
// a no-op (idempotent / crash-safe).
func TestExecuteErasure_FullCascade(t *testing.T) {
	db := openErasureTestDB(t)
	userID, row := seedErasureFixture(t, db, 1200)

	if err := executeErasure(context.Background(), row); err != nil {
		t.Fatalf("executeErasure: %v", err)
	}

	// tokens: hard-deleted including the soft-deleted one
	var tokenCount int64
	db.Unscoped().Model(&repo.Token{}).Where("user_id = ?", userID).Count(&tokenCount)
	if tokenCount != 0 {
		t.Errorf("tokens remaining = %d, want 0 (hard delete incl. soft-deleted)", tokenCount)
	}

	// mapping: hard-deleted
	var mapCount int64
	db.Unscoped().Model(&entity.UserIdentityMapping{}).Where("lurus_user_id = ?", userID).Count(&mapCount)
	if mapCount != 0 {
		t.Errorf("identity mappings remaining = %d, want 0", mapCount)
	}

	// totp: hard-deleted (SEC-C — security-adjacent, same step as tokens)
	var totpCount int64
	db.Unscoped().Model(&entity.UserTOTP{}).Where("user_id = ?", userID).Count(&totpCount)
	if totpCount != 0 {
		t.Errorf("totp rows remaining = %d, want 0", totpCount)
	}

	// totp backup codes: hard-deleted too — both the unused and the
	// already-consumed one (SEC-C rides the same step).
	var backupCodeCount int64
	db.Unscoped().Model(&entity.UserTOTPBackupCode{}).Where("user_id = ?", userID).Count(&backupCodeCount)
	if backupCodeCount != 0 {
		t.Errorf("totp backup code rows remaining = %d, want 0", backupCodeCount)
	}

	// user_sessions: hard-deleted too, same step (L7 repair round 3, finding
	// routing-resilience-limits-13#11).
	var sessionCount int64
	db.Unscoped().Model(&entity.UserSession{}).Where("user_id = ?", userID).Count(&sessionCount)
	if sessionCount != 0 {
		t.Errorf("user_sessions rows remaining = %d, want 0", sessionCount)
	}

	// response_registry: hard-deleted too, same step (cycle-8 L7 repair
	// round, finding B-F9).
	var responseRegistryCount int64
	db.Unscoped().Model(&entity.ResponseRegistry{}).Where("user_id = ?", userID).Count(&responseRegistryCount)
	if responseRegistryCount != 0 {
		t.Errorf("response_registry rows remaining = %d, want 0", responseRegistryCount)
	}

	// permission grants: revoked (not hard-deleted — same "flip revoked_at"
	// shape RevokeGrantV2 uses), so a re-promoted/re-created user id does
	// not silently inherit the erased user's old grant.
	granted, err := repo.HasActivePermissionGrant(userID, "audit", "read")
	if err != nil {
		t.Fatalf("HasActivePermissionGrant after erasure: %v", err)
	}
	if granted {
		t.Errorf("permission grant still active after erasure — must be revoked")
	}

	// logs: pseudonymized, billing fields retained
	var dirty int64
	db.Model(&entity.Log{}).Where("user_id = ? AND username <> ?", userID, repo.ErasedMarker).Count(&dirty)
	if dirty != 0 {
		t.Errorf("non-anonymized logs remaining = %d, want 0", dirty)
	}
	var sample entity.Log
	if err := db.Where("user_id = ?", userID).First(&sample).Error; err != nil {
		t.Fatalf("logs must be retained (pseudonymized, not deleted): %v", err)
	}
	if sample.Ip != "" || sample.Content != "" || sample.Other != "" || sample.TokenName != repo.ErasedMarker {
		t.Errorf("log not scrubbed: ip=%q content=%q other=%q token_name=%q", sample.Ip, sample.Content, sample.Other, sample.TokenName)
	}
	if sample.Quota != 7 || sample.ModelName != "gpt-x" {
		t.Errorf("billing fields must survive: quota=%d model=%q", sample.Quota, sample.ModelName)
	}

	// audit: scrubbed but retained
	var audit entity.AuditEvent
	if err := db.Where("actor_id = ?", userID).First(&audit).Error; err != nil {
		t.Fatalf("audit event must be retained: %v", err)
	}
	if audit.IP != "" || audit.Details != repo.ErasedMarker || audit.Action != "token.created" {
		t.Errorf("audit scrub wrong: ip=%q details=%q action=%q", audit.IP, audit.Details, audit.Action)
	}

	// user: anonymized + soft-deleted + unbound
	var user repo.User
	if err := db.Unscoped().Where("id = ?", userID).First(&user).Error; err != nil {
		t.Fatalf("user row must survive (anonymized): %v", err)
	}
	if user.Username != fmt.Sprintf("erased_%d", userID) || user.Email != "" || user.LurusAccountID != nil {
		t.Errorf("user not anonymized: username=%q email=%q account=%v", user.Username, user.Email, user.LurusAccountID)
	}
	if !user.DeletedAt.Valid {
		t.Errorf("user must be soft-deleted")
	}
	if user.Status != common.UserStatusDisabled {
		t.Errorf("user status = %d, want disabled (%d)", user.Status, common.UserStatusDisabled)
	}

	// request row: completed with accurate evidence counter
	final, err := repo.GetErasureRequestByEventID(context.Background(), "evt-erase-1")
	if err != nil {
		t.Fatalf("get request: %v", err)
	}
	if final.Status != repo.ErasureStatusCompleted || final.CompletedAt == nil {
		t.Errorf("request status = %q completed_at=%v, want completed", final.Status, final.CompletedAt)
	}
	if final.LogsScrubbed != 1200 {
		t.Errorf("logs_scrubbed = %d, want 1200", final.LogsScrubbed)
	}

	// Re-run on the completed snapshot: every step is keyed off the persisted
	// cursor, so a second pass must change nothing.
	if err := executeErasure(context.Background(), final); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	again, _ := repo.GetErasureRequestByEventID(context.Background(), "evt-erase-1")
	if again.LogsScrubbed != 1200 {
		t.Errorf("re-run mutated logs_scrubbed: %d", again.LogsScrubbed)
	}
}

// openErasureTestDBNoTOTPTables mirrors openErasureTestDB but deliberately
// omits entity.UserTOTP / entity.UserTOTPBackupCode from the migrated set —
// reproducing the default post-deploy state where nobody has ever hit
// either table's lazy AutoMigrate path. That path is any repo function that
// calls ensureUserTOTPTable (user_totp.go, for user_totps) or
// ensureUserTOTPBackupCodeTable (user_totp_backup_code.go, for
// user_totp_backup_codes) — grep those two names in internal/adapter/repo
// rather than trusting an enumerated list here, since GetTOTPAdoptionStats
// alone calls both and a caller list drifts as callers are added (this
// comment's own previous version omitted ReplaceUserTOTPBackupCodes). So
// neither table exists when the erasure cascade runs.
func openErasureTestDBNoTOTPTables(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:erasure_no_totp%d?mode=memory&cache=shared", erasureDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, m := range []interface{}{
		&repo.User{}, &repo.Token{}, &repo.Log{},
		&entity.UserIdentityMapping{}, &entity.AuditEvent{},
		&entity.PrivacyErasureRequest{}, &entity.UserSession{}, &entity.AdminPermissionGrant{}, &entity.ResponseRegistry{},
	} {
		if err := db.AutoMigrate(m); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("migrate %T: %v", m, err)
		}
	}

	prevDB, prevLogDB := repo.DB, repo.LOG_DB
	repo.DB, repo.LOG_DB = db, db
	t.Cleanup(func() {
		repo.DB, repo.LOG_DB = prevDB, prevLogDB
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// TestExecuteErasure_TOTPTablesNeverCreated is the lock for the L6 repair
// finding: before repo.HardDeleteUserTOTP/HardDeleteUserTOTPBackupCodes
// guarded on DB.Migrator().HasTable, this step of the cascade errored with
// "relation user_totps does not exist" on a deployment where the lazily-
// created table had never been touched — the erasure request would fail at
// step 1 and be retried on the next lifecycle tick (runErasurePass records
// the error and moves on; it does not halt), stuck retrying that same step
// instead of completing.
// Mutation: removing either HasTable guard makes this executeErasure call
// error.
func TestExecuteErasure_TOTPTablesNeverCreated(t *testing.T) {
	db := openErasureTestDBNoTOTPTables(t)

	accountID := int64(9999)
	user := repo.User{
		Username: "no-totp-victim", Email: "no-totp@example.com",
		Status: common.UserStatusEnabled, Group: "default", LurusAccountID: &accountID,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	row, err := repo.CreateErasureRequestIdempotent(context.Background(),
		"evt-erase-no-totp", accountID, "", "user_requested",
		user.Id, "default", repo.ErasureStatusPending)
	if err != nil {
		t.Fatalf("seed erasure request: %v", err)
	}

	if err := executeErasure(context.Background(), row); err != nil {
		t.Fatalf("executeErasure must complete even when user_totp(s) tables were never created: %v", err)
	}

	final, err := repo.GetErasureRequestByEventID(context.Background(), "evt-erase-no-totp")
	if err != nil {
		t.Fatalf("get request: %v", err)
	}
	if final.Status != repo.ErasureStatusCompleted || final.CompletedAt == nil {
		t.Errorf("request status = %q completed_at=%v, want completed", final.Status, final.CompletedAt)
	}
}

// TestExecuteErasure_ResumesFromStepCursor verifies crash-resume: a request
// persisted at step logs_anonymized must NOT re-run the earlier steps —
// tokens seeded after the (simulated) crash survive untouched.
func TestExecuteErasure_ResumesFromStepCursor(t *testing.T) {
	db := openErasureTestDB(t)
	userID, row := seedErasureFixture(t, db, 5)

	// Simulate a crash after the logs step completed.
	if err := repo.AdvanceErasureStep(context.Background(), row.ID, repo.ErasureStepLogsAnonymized, 5); err != nil {
		t.Fatalf("advance: %v", err)
	}
	resumed, err := repo.GetErasureRequestByEventID(context.Background(), row.EventID)
	if err != nil {
		t.Fatalf("refetch: %v", err)
	}

	if err := executeErasure(context.Background(), resumed); err != nil {
		t.Fatalf("resume run: %v", err)
	}

	// Earlier steps (token hard-delete) must not have re-run.
	var tokenCount int64
	db.Unscoped().Model(&repo.Token{}).Where("user_id = ?", userID).Count(&tokenCount)
	if tokenCount != 3 {
		t.Errorf("tokens = %d, want 3 (resume must skip completed steps)", tokenCount)
	}

	final, _ := repo.GetErasureRequestByEventID(context.Background(), row.EventID)
	if final.Status != repo.ErasureStatusCompleted {
		t.Errorf("status = %q, want completed", final.Status)
	}
}

// TestRunErasurePass_LeaderGateAndErrorIsolation: a failing request (user row
// gone mid-flight is fine — exercise a DB-level failure via closed table) must
// not prevent the pass from finishing, and the error lands on the row.
func TestRunErasurePass_RecordsErrorAndContinues(t *testing.T) {
	db := openErasureTestDB(t)
	_, row := seedErasureFixture(t, db, 2)

	// Sabotage: drop the tokens table so step 1 fails for this request.
	if err := db.Migrator().DropTable(&repo.Token{}); err != nil {
		t.Fatalf("drop tokens: %v", err)
	}

	runErasurePass(context.Background())

	failed, err := repo.GetErasureRequestByEventID(context.Background(), row.EventID)
	if err != nil {
		t.Fatalf("refetch: %v", err)
	}
	if failed.Status != repo.ErasureStatusPending {
		t.Errorf("status = %q, want still pending (retry next tick)", failed.Status)
	}
	if failed.LastError == "" {
		t.Errorf("last_error must record the failure for ops visibility")
	}
}

// TestPrivacyErasure_SuccessfulTickStampsHeartbeat is the L3 heartbeat
// oracle: a pass with zero pending requests (or all requests succeeding) is
// "successful" and must advance
// metrics.LeaderTaskLastSuccess{task="privacy-erasure"} to "now".
func TestPrivacyErasure_SuccessfulTickStampsHeartbeat(t *testing.T) {
	openErasureTestDB(t) // empty pending queue — still a successful pass

	before := time.Now().Unix()
	runErasurePass(context.Background())
	after := time.Now().Unix()

	got := testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues("privacy-erasure"))
	if got < float64(before) || got > float64(after) {
		t.Errorf("LeaderTaskLastSuccess{task=privacy-erasure} = %v, want within [%d, %d]", got, before, after)
	}
}

// TestPrivacyErasure_FailedRequestDoesNotStamp: per runAuditCleanup's
// sibling contract, a pass where at least one request in the batch errored
// must NOT advance the heartbeat — even though runErasurePass itself
// swallows the per-request error and keeps going (crash-resume design).
func TestPrivacyErasure_FailedRequestDoesNotStamp(t *testing.T) {
	db := openErasureTestDB(t)
	seedErasureFixture(t, db, 2)

	// Sabotage: drop the tokens table so step 1 fails for this request —
	// same fault injection as TestRunErasurePass_RecordsErrorAndContinues.
	if err := db.Migrator().DropTable(&repo.Token{}); err != nil {
		t.Fatalf("drop tokens: %v", err)
	}

	baseline := testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues("privacy-erasure"))
	runErasurePass(context.Background())
	got := testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues("privacy-erasure"))

	if got != baseline {
		t.Errorf("LeaderTaskLastSuccess{task=privacy-erasure} moved from %v to %v after a pass with a failed request, want unchanged", baseline, got)
	}
}

// TestPrivacyErasure_StartRegistersHeartbeat is the A-F1 oracle for
// privacy-erasure, mirroring TestAuditCleanup_StartRegistersHeartbeat: the
// boot-time Set(0) and taskreg.Register calls inside
// StartPrivacyErasureWithContext are otherwise deletable with every test in
// this package staying green. Pre-stamps a distinctive non-zero value so
// the zero-assertion below cannot pass merely from a GaugeVec's
// first-access default; forces common.IsLeader() false so the "run once on
// startup" branch cannot race the assertions with an async pass.
func TestPrivacyErasure_StartRegistersHeartbeat(t *testing.T) {
	openErasureTestDB(t)

	prevLeader := common.IsLeader()
	common.SetLeader(false)
	t.Cleanup(func() { common.SetLeader(prevLeader) })

	metrics.LeaderTaskLastSuccess.WithLabelValues(privacyErasureTaskName).Set(999999999)
	before := len(taskreg.Snapshot())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	StartPrivacyErasureWithContext(ctx)

	if got := testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues(privacyErasureTaskName)); got != 0 {
		t.Errorf("LeaderTaskLastSuccess{task=privacy-erasure} = %v immediately after StartPrivacyErasureWithContext, want 0 (boot-time Set(0) resetting a pre-stamped series)", got)
	}

	snap := taskreg.Snapshot()
	if len(snap) <= before {
		t.Fatalf("taskreg.Snapshot() length did not grow: before=%d after=%d", before, len(snap))
	}
	found := false
	for _, task := range snap {
		if task.Name == privacyErasureTaskName {
			found = true
			if !task.LeaderOnly {
				t.Errorf("%s task.LeaderOnly = false, want true", privacyErasureTaskName)
			}
		}
	}
	if !found {
		t.Errorf("taskreg.Snapshot() does not contain %q after StartPrivacyErasureWithContext", privacyErasureTaskName)
	}
}
