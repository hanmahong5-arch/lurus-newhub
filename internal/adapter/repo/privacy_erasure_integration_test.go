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
	"encoding/json"
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

// TestPrivacyErasure_PG_ContentDisposition (cycle-13 L5) proves the
// content-disposition primitives — chat/Midjourney hard delete, task and
// quota_data scrub, playground preset hard delete — against real
// PostgreSQL, where the batched IN-clause deletes, the session-ownership
// subquery and the JSON-column NULLing behave differently than on SQLite
// (the lifecycle package's TestExecuteErasure_FullCascade proves the
// orchestration; this proves the SQL). Calls the repo primitives directly,
// same convention as TestPrivacyErasure_PG_CascadePrimitives above —
// executeErasure lives in package lifecycle, which imports this package,
// so it cannot be called from here without an import cycle.
func TestPrivacyErasure_PG_ContentDisposition(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	if err := DB.AutoMigrate(&entity.ChatSession{}, &entity.ChatMessage{}, &PlaygroundPreset{}); err != nil {
		t.Fatalf("migrate content tables: %v", err)
	}

	accountID := int64(987655)
	user := User{
		Username: "pg-content-victim", Email: "pg-content-victim@example.com",
		Status: common.UserStatusEnabled, Group: "default", LurusAccountID: &accountID,
	}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// chat session + 2 messages
	session := entity.ChatSession{TenantId: "default", UserId: user.Id, Title: "pg erase chat", Model: "gpt-x"}
	if err := DB.Create(&session).Error; err != nil {
		t.Fatalf("seed chat session: %v", err)
	}
	for i, role := range []string{"user", "assistant"} {
		if err := DB.Create(&entity.ChatMessage{
			SessionId: session.Id, TenantId: "default", UserId: user.Id,
			Seq: i, Role: role, Content: fmt.Sprintf("pg erase message %d", i),
		}).Error; err != nil {
			t.Fatalf("seed chat message %d: %v", i, err)
		}
	}

	// 1 midjourney row
	if err := DB.Create(&Midjourney{
		UserId: user.Id, Action: "IMAGINE", MjId: "pg-mj-1",
		Prompt: "pg erase prompt", PromptEn: "pg erase prompt en", Quota: 5,
	}).Error; err != nil {
		t.Fatalf("seed midjourney: %v", err)
	}

	// 1 task with properties.input + fail_reason + data
	task := &Task{
		TaskID: "pg-task-erase-1", UserId: user.Id, Quota: 9,
		FailReason: "pg erase failure detail",
		Properties: Properties{Input: "pg erase prompt input"},
		Data:       json.RawMessage(`{"k":"pg erase payload"}`),
	}
	if err := DB.Create(task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	// 1 quota_data row
	if err := DB.Table("quota_data").Create(&QuotaData{
		UserID: user.Id, Username: "pg-content-victim", ModelName: "gpt-x",
		CreatedAt: time.Now().Unix(), Count: 3, Quota: 21, TokenUsed: 100,
	}).Error; err != nil {
		t.Fatalf("seed quota_data: %v", err)
	}

	// 1 playground preset
	if err := DB.Create(&PlaygroundPreset{
		TenantID: "default", UserID: user.Id, Name: "pg erase preset",
		Prompt: "pg erase preset prompt", Models: `["gpt-4o"]`, Params: `{}`,
	}).Error; err != nil {
		t.Fatalf("seed playground preset: %v", err)
	}

	// --- Dispositions: drain the batch loops exactly as executeErasure does ---
	for {
		ids, err := HardDeleteChatMessagesBatch(context.Background(), user.Id, 500)
		if err != nil {
			t.Fatalf("HardDeleteChatMessagesBatch: %v", err)
		}
		if len(ids) == 0 {
			break
		}
	}
	for {
		ids, err := HardDeleteChatSessionsBatch(context.Background(), user.Id, 500)
		if err != nil {
			t.Fatalf("HardDeleteChatSessionsBatch: %v", err)
		}
		if len(ids) == 0 {
			break
		}
	}
	for {
		ids, err := HardDeleteMidjourneyBatch(context.Background(), user.Id, 500)
		if err != nil {
			t.Fatalf("HardDeleteMidjourneyBatch: %v", err)
		}
		if len(ids) == 0 {
			break
		}
	}
	if _, err := ScrubTasksForUser(context.Background(), user.Id); err != nil {
		t.Fatalf("ScrubTasksForUser: %v", err)
	}
	if _, err := ScrubQuotaDataUsernameForUser(context.Background(), user.Id); err != nil {
		t.Fatalf("ScrubQuotaDataUsernameForUser: %v", err)
	}
	if _, err := HardDeletePlaygroundPresetsForUser(context.Background(), user.Id); err != nil {
		t.Fatalf("HardDeletePlaygroundPresetsForUser: %v", err)
	}

	// --- Assertions per the cycle-13 L5 oracle ---
	var chatCount int64
	DB.Model(&entity.ChatSession{}).Where("user_id = ?", user.Id).Count(&chatCount)
	if chatCount != 0 {
		t.Errorf("chat sessions remaining = %d, want 0", chatCount)
	}
	var msgCount int64
	DB.Model(&entity.ChatMessage{}).Where("user_id = ?", user.Id).Count(&msgCount)
	if msgCount != 0 {
		t.Errorf("chat messages remaining = %d, want 0", msgCount)
	}
	var mjCount int64
	DB.Model(&Midjourney{}).Where("user_id = ?", user.Id).Count(&mjCount)
	if mjCount != 0 {
		t.Errorf("midjourney rows remaining = %d, want 0", mjCount)
	}
	var gotTask Task
	if err := DB.Where("user_id = ?", user.Id).First(&gotTask).Error; err != nil {
		t.Fatalf("task row must survive (scrubbed, not deleted): %v", err)
	}
	if gotTask.Properties.Input != "" || gotTask.FailReason != "" || len(gotTask.Data) != 0 {
		t.Errorf("task not scrubbed: properties.input=%q fail_reason=%q data=%q", gotTask.Properties.Input, gotTask.FailReason, string(gotTask.Data))
	}
	if gotTask.Quota != 9 {
		t.Errorf("task quota must survive: got %d, want 9", gotTask.Quota)
	}
	var gotQD QuotaData
	if err := DB.Table("quota_data").Where("user_id = ?", user.Id).First(&gotQD).Error; err != nil {
		t.Fatalf("quota_data row must survive: %v", err)
	}
	if gotQD.Username != ErasedMarker {
		t.Errorf("quota_data.username = %q, want %q", gotQD.Username, ErasedMarker)
	}
	if gotQD.Quota != 21 {
		t.Errorf("quota_data.quota must survive: got %d, want 21", gotQD.Quota)
	}
	var presetCount int64
	DB.Model(&PlaygroundPreset{}).Where("user_id = ?", user.Id).Count(&presetCount)
	if presetCount != 0 {
		t.Errorf("playground preset rows remaining = %d, want 0", presetCount)
	}

	// --- Re-run: every primitive must be a no-op now ---
	if ids, _ := HardDeleteChatMessagesBatch(context.Background(), user.Id, 500); len(ids) != 0 {
		t.Errorf("re-run chat message batch returned %d ids, want 0", len(ids))
	}
	if ids, _ := HardDeleteMidjourneyBatch(context.Background(), user.Id, 500); len(ids) != 0 {
		t.Errorf("re-run midjourney batch returned %d ids, want 0", len(ids))
	}
	if n, _ := ScrubQuotaDataUsernameForUser(context.Background(), user.Id); n != 0 {
		t.Errorf("re-run quota_data scrub affected %d rows, want 0", n)
	}
}
