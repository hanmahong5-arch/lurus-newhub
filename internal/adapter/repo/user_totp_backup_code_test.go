package repo

// user_totp_backup_code_test.go — hermetic coverage for
// user_totp_backup_code.go (entity.UserTOTPBackupCode persistence). The
// tables are NOT part of SetupTestDB's AutoMigrate list — both user_totps
// and user_totp_backup_codes are created lazily on first call (same pattern
// as cov_repo-deep_user_totp_test.go), so every test here also exercises
// that lazy-create path from a genuinely fresh database.
//
// L6 repair round: the plan's Files list named this file but it did not
// exist on disk (repo functions were only exercised indirectly through
// handler-package tests on SQLite). This file closes that gap with direct,
// handler-independent coverage of the replace-not-append, atomic-consume,
// and adoption-stats-bucketing behaviour repo/user_totp_backup_code.go's
// doc comments describe.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// TestReplaceUserTOTPBackupCodes_ReplacesNotAppends proves the second call
// for a user removes the first set entirely rather than accumulating rows —
// the core guarantee the regenerate endpoint depends on.
func TestReplaceUserTOTPBackupCodes_ReplacesNotAppends(t *testing.T) {
	SetupTestDB(t)

	first := []entity.UserTOTPBackupCode{
		{UserId: 1, CodeHash: "hash-a", CreatedAt: common.GetTimestamp()},
		{UserId: 1, CodeHash: "hash-b", CreatedAt: common.GetTimestamp()},
	}
	if err := ReplaceUserTOTPBackupCodes(1, first); err != nil {
		t.Fatalf("first replace: %v", err)
	}
	n, err := CountUnusedUserTOTPBackupCodes(1)
	if err != nil || n != 2 {
		t.Fatalf("after first replace: n=%d err=%v, want 2", n, err)
	}

	second := []entity.UserTOTPBackupCode{
		{UserId: 1, CodeHash: "hash-c", CreatedAt: common.GetTimestamp()},
	}
	if err := ReplaceUserTOTPBackupCodes(1, second); err != nil {
		t.Fatalf("second replace: %v", err)
	}
	n, err = CountUnusedUserTOTPBackupCodes(1)
	if err != nil || n != 1 {
		t.Fatalf("after second replace: n=%d err=%v, want 1 (old set must be gone, not appended)", n, err)
	}
	var total int64
	DB.Model(&entity.UserTOTPBackupCode{}).Where("user_id = ?", 1).Count(&total)
	if total != 1 {
		t.Fatalf("total rows for user 1 = %d, want 1 (hash-a/hash-b must be deleted)", total)
	}

	// A different user's codes must be untouched by user 1's replace.
	if err := ReplaceUserTOTPBackupCodes(2, []entity.UserTOTPBackupCode{
		{UserId: 2, CodeHash: "hash-d", CreatedAt: common.GetTimestamp()},
	}); err != nil {
		t.Fatalf("seed user 2: %v", err)
	}
	if err := ReplaceUserTOTPBackupCodes(1, nil); err != nil {
		t.Fatalf("replace user 1 with empty set: %v", err)
	}
	n2, err := CountUnusedUserTOTPBackupCodes(2)
	if err != nil || n2 != 1 {
		t.Fatalf("user 2 codes disturbed by user 1's replace: n=%d err=%v, want 1", n2, err)
	}
}

// TestConsumeUserTOTPBackupCode_OnceOnly proves a code can be consumed
// exactly once: the first call succeeds and flips used_at, the second call
// (same hash, same user) fails — no read-then-write window.
func TestConsumeUserTOTPBackupCode_OnceOnly(t *testing.T) {
	SetupTestDB(t)

	if err := ReplaceUserTOTPBackupCodes(10, []entity.UserTOTPBackupCode{
		{UserId: 10, CodeHash: "solo-hash", CreatedAt: common.GetTimestamp()},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ok, err := ConsumeUserTOTPBackupCode(10, "solo-hash", common.GetTimestamp())
	if err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if !ok {
		t.Fatal("first consume of an unused code must succeed")
	}

	ok2, err := ConsumeUserTOTPBackupCode(10, "solo-hash", common.GetTimestamp())
	if err != nil {
		t.Fatalf("second consume: %v", err)
	}
	if ok2 {
		t.Fatal("second consume of the same code must fail (already used)")
	}

	remaining, err := CountUnusedUserTOTPBackupCodes(10)
	if err != nil || remaining != 0 {
		t.Fatalf("remaining unused = %d err=%v, want 0", remaining, err)
	}
}

// TestConsumeUserTOTPBackupCode_WrongUserRejected proves the UPDATE's
// WHERE clause is scoped by user_id: a hash that belongs to user A cannot
// be consumed by presenting user B's id, even though HashBackupCode's
// domain-separator design means the two would never collide in practice —
// this locks the query itself, independent of the hash construction.
func TestConsumeUserTOTPBackupCode_WrongUserRejected(t *testing.T) {
	SetupTestDB(t)

	if err := ReplaceUserTOTPBackupCodes(20, []entity.UserTOTPBackupCode{
		{UserId: 20, CodeHash: "owned-by-20", CreatedAt: common.GetTimestamp()},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ok, err := ConsumeUserTOTPBackupCode(21, "owned-by-20", common.GetTimestamp())
	if err != nil {
		t.Fatalf("consume as wrong user: %v", err)
	}
	if ok {
		t.Fatal("consuming another user's code hash must fail")
	}

	// The real owner can still consume it afterward — the wrong-user
	// attempt above must not have marked it used.
	ok2, err := ConsumeUserTOTPBackupCode(20, "owned-by-20", common.GetTimestamp())
	if err != nil {
		t.Fatalf("consume as real owner: %v", err)
	}
	if !ok2 {
		t.Fatal("real owner must still be able to consume the code after a wrong-user attempt")
	}
}

// TestDeleteUserTOTPBackupCodes_RemovesUsedAndUnused proves the delete used
// by TotpDisable/ForceDisableTotpV2 purges both consumed and unconsumed
// rows, not just the unused ones.
func TestDeleteUserTOTPBackupCodes_RemovesUsedAndUnused(t *testing.T) {
	SetupTestDB(t)

	if err := ReplaceUserTOTPBackupCodes(30, []entity.UserTOTPBackupCode{
		{UserId: 30, CodeHash: "will-be-used", CreatedAt: common.GetTimestamp()},
		{UserId: 30, CodeHash: "will-stay-unused", CreatedAt: common.GetTimestamp()},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if ok, err := ConsumeUserTOTPBackupCode(30, "will-be-used", common.GetTimestamp()); err != nil || !ok {
		t.Fatalf("consume seed code: ok=%v err=%v", ok, err)
	}

	if err := DeleteUserTOTPBackupCodes(30); err != nil {
		t.Fatalf("DeleteUserTOTPBackupCodes: %v", err)
	}

	var total int64
	DB.Model(&entity.UserTOTPBackupCode{}).Where("user_id = ?", 30).Count(&total)
	if total != 0 {
		t.Fatalf("total rows after delete = %d, want 0 (both used and unused must be gone)", total)
	}
}

// TestGetTOTPAdoptionStats_BucketsNoCodesIssuedVsExhausted proves the two
// enrolled-population buckets are computed correctly and are mutually
// exclusive: a user with unused codes is in neither bucket, a user with
// zero code rows ever issued is no_codes_issued, and a user whose every
// issued code has been consumed is exhausted.
func TestGetTOTPAdoptionStats_BucketsNoCodesIssuedVsExhausted(t *testing.T) {
	SetupTestDB(t)

	seed := func(id int, tenant, username string) {
		if err := DB.Create(&User{
			Id: id, TenantId: tenant, Username: username,
			Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
			Email: username + "@test.local",
		}).Error; err != nil {
			t.Fatalf("seed user %d: %v", id, err)
		}
	}
	seedTotp := func(id int, enabled bool) {
		if err := UpsertUserTOTP(&entity.UserTOTP{
			UserId: id, SecretEncrypted: "enc", Enabled: enabled,
			CreatedAt: common.GetTimestamp(),
		}); err != nil {
			t.Fatalf("seed totp %d: %v", id, err)
		}
	}

	// user 41: enrolled, one unused code — neither bucket.
	seed(41, "default", "has-unused")
	seedTotp(41, true)
	if err := ReplaceUserTOTPBackupCodes(41, []entity.UserTOTPBackupCode{
		{UserId: 41, CodeHash: "u41-a", CreatedAt: common.GetTimestamp()},
	}); err != nil {
		t.Fatalf("seed codes 41: %v", err)
	}

	// user 42: enrolled, zero code rows ever issued — no_codes_issued.
	seed(42, "default", "no-codes")
	seedTotp(42, true)

	// user 43: enrolled, one code, fully consumed — exhausted.
	seed(43, "default", "exhausted")
	seedTotp(43, true)
	if err := ReplaceUserTOTPBackupCodes(43, []entity.UserTOTPBackupCode{
		{UserId: 43, CodeHash: "u43-a", CreatedAt: common.GetTimestamp()},
	}); err != nil {
		t.Fatalf("seed codes 43: %v", err)
	}
	if ok, err := ConsumeUserTOTPBackupCode(43, "u43-a", common.GetTimestamp()); err != nil || !ok {
		t.Fatalf("consume 43: ok=%v err=%v", ok, err)
	}

	// user 44: pending (enabled=false) — excluded from Enrolled and from
	// both backup-code buckets.
	seed(44, "default", "pending")
	seedTotp(44, false)

	stats, err := GetTOTPAdoptionStats("")
	if err != nil {
		t.Fatalf("GetTOTPAdoptionStats: %v", err)
	}
	if stats.Enrolled != 3 {
		t.Errorf("enrolled = %d, want 3 (users 41/42/43)", stats.Enrolled)
	}
	if stats.Pending != 1 {
		t.Errorf("pending = %d, want 1 (user 44)", stats.Pending)
	}
	if stats.NoCodesIssued != 1 {
		t.Errorf("no_codes_issued = %d, want 1 (user 42 only)", stats.NoCodesIssued)
	}
	if stats.Exhausted != 1 {
		t.Errorf("exhausted = %d, want 1 (user 43 only)", stats.Exhausted)
	}
}
