package repo

// cov_repo-deep_user_totp_test.go — coverage for user_totp.go, the step-up
// TOTP factor store. The table is NOT part of SetupTestDB's AutoMigrate list
// (see user_totp.go comment) — it is lazily created on first call via
// ensureUserTOTPTable(), so every test here also exercises that lazy-create
// path from a genuinely fresh database.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestGetUserTOTP_NoEnrollmentReturnsNilNil(t *testing.T) {
	SetupTestDB(t)

	rec, err := GetUserTOTP(4242)
	if err != nil {
		t.Fatalf("GetUserTOTP on a fresh DB must not error (lazy table create): %v", err)
	}
	if rec != nil {
		t.Fatalf("no enrollment must return nil record, got %+v", rec)
	}
}

func TestUpsertUserTOTP_InsertThenOnConflictReplace(t *testing.T) {
	SetupTestDB(t)

	rec := &entity.UserTOTP{
		UserId:          77,
		SecretEncrypted: "enc-v1",
		Enabled:         false,
		CreatedAt:       common.GetTimestamp(),
	}
	if err := UpsertUserTOTP(rec); err != nil {
		t.Fatalf("UpsertUserTOTP insert: %v", err)
	}

	got, err := GetUserTOTP(77)
	if err != nil {
		t.Fatalf("GetUserTOTP after insert: %v", err)
	}
	if got == nil || got.SecretEncrypted != "enc-v1" || got.Enabled {
		t.Fatalf("first upsert not persisted correctly: %+v", got)
	}

	// Second upsert for the SAME user must replace the row (OnConflict
	// UpdateAll on the user_id primary key), not create a duplicate.
	confirmedAt := common.GetTimestamp()
	rec2 := &entity.UserTOTP{
		UserId:          77,
		SecretEncrypted: "enc-v2-confirmed",
		Enabled:         true,
		CreatedAt:       rec.CreatedAt,
		ConfirmedAt:     confirmedAt,
	}
	if err := UpsertUserTOTP(rec2); err != nil {
		t.Fatalf("UpsertUserTOTP replace: %v", err)
	}

	got2, err := GetUserTOTP(77)
	if err != nil {
		t.Fatalf("GetUserTOTP after replace: %v", err)
	}
	if got2 == nil {
		t.Fatal("record must still exist after replace")
	}
	if got2.SecretEncrypted != "enc-v2-confirmed" || !got2.Enabled || got2.ConfirmedAt != confirmedAt {
		t.Fatalf("on-conflict upsert must replace all fields, got %+v", got2)
	}

	var count int64
	DB.Model(&entity.UserTOTP{}).Where("user_id = ?", 77).Count(&count)
	if count != 1 {
		t.Fatalf("upsert-on-conflict must not create a duplicate row, got %d rows", count)
	}
}

func TestUpsertUserTOTP_IsolatesByUser(t *testing.T) {
	SetupTestDB(t)

	if err := UpsertUserTOTP(&entity.UserTOTP{UserId: 1, SecretEncrypted: "a", CreatedAt: common.GetTimestamp()}); err != nil {
		t.Fatalf("upsert user1: %v", err)
	}
	if err := UpsertUserTOTP(&entity.UserTOTP{UserId: 2, SecretEncrypted: "b", CreatedAt: common.GetTimestamp()}); err != nil {
		t.Fatalf("upsert user2: %v", err)
	}

	got1, _ := GetUserTOTP(1)
	got2, _ := GetUserTOTP(2)
	if got1 == nil || got1.SecretEncrypted != "a" {
		t.Fatalf("user1 record wrong: %+v", got1)
	}
	if got2 == nil || got2.SecretEncrypted != "b" {
		t.Fatalf("user2 record wrong: %+v", got2)
	}
}

func TestDeleteUserTOTP_RemovesEnrollment(t *testing.T) {
	SetupTestDB(t)

	if err := UpsertUserTOTP(&entity.UserTOTP{UserId: 99, SecretEncrypted: "s", CreatedAt: common.GetTimestamp()}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got, _ := GetUserTOTP(99); got == nil {
		t.Fatal("precondition: record must exist before delete")
	}

	if err := DeleteUserTOTP(99); err != nil {
		t.Fatalf("DeleteUserTOTP: %v", err)
	}

	got, err := GetUserTOTP(99)
	if err != nil {
		t.Fatalf("GetUserTOTP after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("record must be gone after delete, got %+v", got)
	}
}

// Deleting a user with no enrollment (and on a fresh, table-less DB) must be
// a harmless no-op, not an error.
func TestDeleteUserTOTP_NoEnrollmentIsNoop(t *testing.T) {
	SetupTestDB(t)

	if err := DeleteUserTOTP(123456); err != nil {
		t.Fatalf("deleting a non-existent enrollment must not error: %v", err)
	}
}
