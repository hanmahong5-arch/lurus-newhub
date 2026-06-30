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
	"github.com/glebarez/sqlite"
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
		&entity.PrivacyErasureRequest{},
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
	if testing.Short() {
		t.Skip("seeds 1200 logs; covered by the CI run")
	}
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
