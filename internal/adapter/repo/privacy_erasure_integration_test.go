package repo

// privacy_erasure_integration_test.go — PIPL §47 disposition primitives
// against real PostgreSQL (CI pg-integration gate; skips without
// TEST_POSTGRES_DSN). The lifecycle package covers the orchestration on
// SQLite; this file proves the SQL itself (Unscoped deletes, batched
// pseudonymization with the ErasedMarker cursor, audit scrub, user
// anonymization incl. NULLing unique columns) behaves on the production
// engine, where NULL-vs-'' and unique-index semantics differ from SQLite.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestPrivacyErasure_PG_CascadePrimitives(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	// SetupTestDB migrates the core tables; add the erasure surface.
	if err := DB.AutoMigrate(&entity.AuditEvent{}, &entity.PrivacyErasureRequest{}); err != nil {
		t.Fatalf("migrate erasure tables: %v", err)
	}

	// --- Seed: user + 3 tokens (one soft-deleted) + mapping + 1200 logs + audit ---
	accountID := int64(987654)
	user := User{
		Username: "pg-victim", Email: "pg-victim@example.com",
		Status: common.UserStatusEnabled, Group: "default", LurusAccountID: &accountID,
	}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	for i := 0; i < 3; i++ {
		tok := Token{
			UserId: user.Id, Key: fmt.Sprintf("pgerase%025d", i),
			Status: common.TokenStatusEnabled, Name: fmt.Sprintf("pg-tok-%d", i),
		}
		if err := DB.Create(&tok).Error; err != nil {
			t.Fatalf("seed token %d: %v", i, err)
		}
		if i == 2 {
			if err := DB.Delete(&tok).Error; err != nil {
				t.Fatalf("soft-delete token: %v", err)
			}
		}
	}
	if err := DB.Create(&UserIdentityMapping{
		LurusUserID: user.Id, IDPSubject: "zit-pg-1", TenantID: "default",
		Email: "pg-victim@example.com", DisplayName: "PG Victim",
	}).Error; err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	const logCount = 1200 // > 2 full batches of 500 — exercises the batch cursor
	logs := make([]Log, 0, logCount)
	now := time.Now().Unix()
	for i := 0; i < logCount; i++ {
		logs = append(logs, Log{
			UserId: user.Id, Username: "pg-victim", TokenName: "pg-tok-0",
			Ip: "10.1.1.1", Content: "prompt", Other: `{"k":1}`,
			ModelName: "gpt-pg", Quota: 11, CreatedAt: now,
		})
	}
	if err := LOG_DB.CreateInBatches(logs, 200).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}
	if err := DB.Create(&entity.AuditEvent{
		ActorType: "user", ActorID: user.Id, Action: "token.created",
		IP: "10.1.1.1", Details: `{"d":1}`, Timestamp: 1, TenantID: "default",
	}).Error; err != nil {
		t.Fatalf("seed audit: %v", err)
	}

	// --- Intent row: idempotent create + replay ---
	row, err := CreateErasureRequestIdempotent(context.Background(),
		"evt-pg-1", accountID, "req-1", "user_requested",
		user.Id, "default", ErasureStatusPending)
	if err != nil {
		t.Fatalf("create intent: %v", err)
	}
	if _, replayErr := CreateErasureRequestIdempotent(context.Background(),
		"evt-pg-1", accountID, "", "", user.Id, "default", ErasureStatusPending); !errors.Is(replayErr, ErrErasureEventExists) {
		t.Errorf("replay err = %v, want ErrErasureEventExists", replayErr)
	}

	// --- Dispositions ---
	if n, err := HardDeleteUserTokens(context.Background(), user.Id); err != nil || n != 3 {
		t.Fatalf("HardDeleteUserTokens = (%d, %v), want (3, nil) — soft-deleted row must count", n, err)
	}
	if n, err := HardDeleteUserIdentityMappings(context.Background(), user.Id); err != nil || n != 1 {
		t.Fatalf("HardDeleteUserIdentityMappings = (%d, %v), want (1, nil)", n, err)
	}

	var scrubbed int
	for {
		ids, err := AnonymizeLogsBatch(context.Background(), user.Id, 500)
		if err != nil {
			t.Fatalf("AnonymizeLogsBatch: %v", err)
		}
		if len(ids) == 0 {
			break
		}
		scrubbed += len(ids)
	}
	if scrubbed != logCount {
		t.Errorf("anonymized logs = %d, want %d", scrubbed, logCount)
	}

	for {
		n, err := ScrubAuditEventsBatch(context.Background(), user.Id, 500)
		if err != nil {
			t.Fatalf("ScrubAuditEventsBatch: %v", err)
		}
		if n == 0 {
			break
		}
	}

	if err := AnonymizeUserRow(context.Background(), user.Id); err != nil {
		t.Fatalf("AnonymizeUserRow: %v", err)
	}
	if err := MarkErasureCompleted(context.Background(), row.ID); err != nil {
		t.Fatalf("MarkErasureCompleted: %v", err)
	}

	// --- Assertions per the contracts disposition table ---
	var tokenCount int64
	DB.Unscoped().Model(&Token{}).Where("user_id = ?", user.Id).Count(&tokenCount)
	if tokenCount != 0 {
		t.Errorf("tokens remaining = %d, want 0", tokenCount)
	}
	var mapCount int64
	DB.Unscoped().Model(&UserIdentityMapping{}).Where("lurus_user_id = ?", user.Id).Count(&mapCount)
	if mapCount != 0 {
		t.Errorf("mappings remaining = %d, want 0", mapCount)
	}

	var dirtyLogs, keptLogs int64
	LOG_DB.Model(&Log{}).Where("user_id = ? AND username <> ?", user.Id, ErasedMarker).Count(&dirtyLogs)
	LOG_DB.Model(&Log{}).Where("user_id = ?", user.Id).Count(&keptLogs)
	if dirtyLogs != 0 || keptLogs != logCount {
		t.Errorf("logs dirty=%d kept=%d, want 0/%d (pseudonymize, not delete)", dirtyLogs, keptLogs, logCount)
	}
	var sample Log
	LOG_DB.Where("user_id = ?", user.Id).First(&sample)
	if sample.Ip != "" || sample.Content != "" || sample.Quota != 11 {
		t.Errorf("log scrub wrong: ip=%q content=%q quota=%d", sample.Ip, sample.Content, sample.Quota)
	}

	var audit entity.AuditEvent
	if err := DB.Where("actor_id = ?", user.Id).First(&audit).Error; err != nil {
		t.Fatalf("audit row must survive: %v", err)
	}
	if audit.IP != "" || audit.Details != ErasedMarker {
		t.Errorf("audit scrub wrong: ip=%q details=%q", audit.IP, audit.Details)
	}

	var anon User
	if err := DB.Unscoped().Where("id = ?", user.Id).First(&anon).Error; err != nil {
		t.Fatalf("user row must survive: %v", err)
	}
	if anon.Username != fmt.Sprintf("erased_%d", user.Id) || anon.Email != "" ||
		anon.LurusAccountID != nil || anon.AccessToken != nil || !anon.DeletedAt.Valid {
		t.Errorf("user anonymization wrong: %+v", anon)
	}

	// Account can re-bind a fresh user after erasure (unique index freed).
	if _, err := GetUserByLurusAccountID(accountID); err == nil {
		t.Errorf("erased account must no longer resolve to a user")
	}

	// --- Re-run: every primitive must be a no-op now ---
	if n, _ := HardDeleteUserTokens(context.Background(), user.Id); n != 0 {
		t.Errorf("re-run token delete affected %d rows, want 0", n)
	}
	if ids, _ := AnonymizeLogsBatch(context.Background(), user.Id, 500); len(ids) != 0 {
		t.Errorf("re-run log batch returned %d ids, want 0", len(ids))
	}
	if n, _ := ScrubAuditEventsBatch(context.Background(), user.Id, 500); n != 0 {
		t.Errorf("re-run audit scrub affected %d rows, want 0", n)
	}
}
