package repo

// privacy_erasure_test.go — SQLite unit coverage for the PIPL §47 disposition
// primitives (the PG integration twin runs the same SQL against the real
// engine in the CI pg-integration gate; this file keeps the -short coverage
// gate honest about the new repo surface).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

func setupErasureSQLite(t *testing.T) func() {
	t.Helper()
	cleanup := setupSQLiteDB(t)
	if err := DB.AutoMigrate(&entity.PrivacyErasureRequest{}); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		cleanup()
		t.Fatalf("migrate erasure table: %v", err)
	}
	return cleanup
}

func TestCreateErasureRequestIdempotent_ValidationAndReplay(t *testing.T) {
	cleanup := setupErasureSQLite(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := CreateErasureRequestIdempotent(ctx, "", 1, "", "", 0, "", ErasureStatusPending); err == nil {
		t.Error("empty event_id must be rejected")
	}
	if _, err := CreateErasureRequestIdempotent(ctx, "evt-v", 0, "", "", 0, "", ErasureStatusPending); err == nil {
		t.Error("account_id 0 must be rejected")
	}

	first, err := CreateErasureRequestIdempotent(ctx, "evt-a", 11, "req-1", "user_requested", 7, "default", ErasureStatusPending)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if first.Status != ErasureStatusPending || first.CompletedAt != nil {
		t.Errorf("pending row wrong: status=%q completed=%v", first.Status, first.CompletedAt)
	}

	replay, err := CreateErasureRequestIdempotent(ctx, "evt-a", 11, "", "", 7, "default", ErasureStatusPending)
	if !errors.Is(err, ErrErasureEventExists) {
		t.Fatalf("replay err = %v, want ErrErasureEventExists", err)
	}
	if replay.ID != first.ID {
		t.Errorf("replay returned a different row: %d vs %d", replay.ID, first.ID)
	}

	// no_data rows are terminal at insert: completed_at stamped immediately.
	nodata, err := CreateErasureRequestIdempotent(ctx, "evt-nd", 12, "", "", 0, "", ErasureStatusNoData)
	if err != nil {
		t.Fatalf("no_data create: %v", err)
	}
	if nodata.CompletedAt == nil {
		t.Error("no_data row must carry completed_at")
	}
}

func TestErasureRequestLifecycle_StepStatusError(t *testing.T) {
	cleanup := setupErasureSQLite(t)
	defer cleanup()
	ctx := context.Background()

	row, err := CreateErasureRequestIdempotent(ctx, "evt-life", 21, "", "", 5, "default", ErasureStatusPending)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	pending, err := ListPendingErasureRequests(ctx, 0) // 0 → default limit
	if err != nil || len(pending) != 1 || pending[0].EventID != "evt-life" {
		t.Fatalf("ListPending = (%d rows, %v), want the seeded row", len(pending), err)
	}

	if err := RecordErasureError(ctx, row.ID, strings.Repeat("x", 600)); err != nil {
		t.Fatalf("record error: %v", err)
	}
	got, err := GetErasureRequestByEventID(ctx, "evt-life")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.LastError) != 512 {
		t.Errorf("last_error length = %d, want truncated to 512", len(got.LastError))
	}

	if err := AdvanceErasureStep(ctx, row.ID, ErasureStepLogsAnonymized, 42); err != nil {
		t.Fatalf("advance: %v", err)
	}
	got, _ = GetErasureRequestByEventID(ctx, "evt-life")
	if got.Step != ErasureStepLogsAnonymized || got.LogsScrubbed != 42 || got.LastError != "" {
		t.Errorf("after advance: step=%q scrubbed=%d err=%q", got.Step, got.LogsScrubbed, got.LastError)
	}

	if err := MarkErasureCompleted(ctx, row.ID); err != nil {
		t.Fatalf("complete: %v", err)
	}
	got, _ = GetErasureRequestByEventID(ctx, "evt-life")
	if got.Status != ErasureStatusCompleted || got.CompletedAt == nil {
		t.Errorf("after complete: status=%q completed=%v", got.Status, got.CompletedAt)
	}
	if pending, _ := ListPendingErasureRequests(ctx, 10); len(pending) != 0 {
		t.Errorf("completed row still listed as pending")
	}

	if _, err := GetErasureRequestByEventID(ctx, "evt-ghost"); err == nil {
		t.Error("unknown event_id must error")
	}
}

func TestErasureDispositions_SQLite(t *testing.T) {
	cleanup := setupErasureSQLite(t)
	defer cleanup()
	ctx := context.Background()

	user := seedUser(t, "erase-victim", "victim@x.io", 1, 1, "default")
	for i := 0; i < 3; i++ {
		tok := Token{UserId: user.Id, Key: fmt.Sprintf("ek%030d", i), Status: 1, Name: fmt.Sprintf("t%d", i)}
		if err := DB.Create(&tok).Error; err != nil {
			t.Fatalf("seed token: %v", err)
		}
		if i == 2 {
			if err := DB.Delete(&tok).Error; err != nil {
				t.Fatalf("soft delete: %v", err)
			}
		}
	}
	if err := DB.Create(&UserIdentityMapping{LurusUserID: user.Id, IDPSubject: "z1", TenantID: "default"}).Error; err != nil {
		t.Fatalf("seed mapping: %v", err)
	}
	for i := 0; i < 7; i++ {
		if err := LOG_DB.Create(&Log{
			UserId: user.Id, Username: "erase-victim", TokenName: "t0",
			Ip: "1.2.3.4", Content: "c", Other: "o", Quota: 3, CreatedAt: time.Now().Unix(),
		}).Error; err != nil {
			t.Fatalf("seed log: %v", err)
		}
	}
	if err := DB.Create(&entity.AuditEvent{ActorType: "user", ActorID: user.Id, Action: "a", IP: "1.2.3.4", Details: "d", Timestamp: 1}).Error; err != nil {
		t.Fatalf("seed audit: %v", err)
	}

	// Containment first (intake), then the cascade primitives.
	if n, err := DisableTokensByUserID(ctx, user.Id); err != nil || n != 2 {
		t.Errorf("DisableTokens = (%d, %v), want 2 live tokens disabled", n, err)
	}
	if n, err := HardDeleteUserTokens(ctx, user.Id); err != nil || n != 3 {
		t.Errorf("HardDeleteUserTokens = (%d, %v), want 3 (incl. soft-deleted)", n, err)
	}
	if n, err := HardDeleteUserIdentityMappings(ctx, user.Id); err != nil || n != 1 {
		t.Errorf("HardDeleteUserIdentityMappings = (%d, %v), want 1", n, err)
	}

	// Logs: batch 5 then 2, then empty (cursor via ErasedMarker).
	ids1, err := AnonymizeLogsBatch(ctx, user.Id, 5)
	if err != nil || len(ids1) != 5 {
		t.Fatalf("batch1 = (%d, %v), want 5", len(ids1), err)
	}
	ids2, err := AnonymizeLogsBatch(ctx, user.Id, 5)
	if err != nil || len(ids2) != 2 {
		t.Fatalf("batch2 = (%d, %v), want 2", len(ids2), err)
	}
	if ids3, _ := AnonymizeLogsBatch(ctx, user.Id, 5); len(ids3) != 0 {
		t.Errorf("batch3 = %d ids, want 0 (done)", len(ids3))
	}

	if n, err := ScrubAuditEventsBatch(ctx, user.Id, 10); err != nil || n != 1 {
		t.Errorf("ScrubAudit = (%d, %v), want 1", n, err)
	}
	if n, _ := ScrubAuditEventsBatch(ctx, user.Id, 10); n != 0 {
		t.Errorf("ScrubAudit re-run = %d, want 0", n)
	}

	if err := AnonymizeUserRow(ctx, user.Id); err != nil {
		t.Fatalf("AnonymizeUserRow: %v", err)
	}
	if err := AnonymizeUserRow(ctx, 0); err == nil {
		t.Error("AnonymizeUserRow(0) must be rejected")
	}

	var anon User
	if err := DB.Unscoped().Where("id = ?", user.Id).First(&anon).Error; err != nil {
		t.Fatalf("readback: %v", err)
	}
	if anon.Username != fmt.Sprintf("erased_%d", user.Id) || anon.Email != "" || !anon.DeletedAt.Valid {
		t.Errorf("user not anonymized: %+v", anon)
	}
}
