package app

// post_consume_zero_usage_release_test.go — a stream that settles to zero
// usage (abnormal end / provider returned no billable tokens) must still
// release its platform pre-auth. quota.go's platform-wallet gate used to be
// keyed on totalQuota > 0 only, so a zero-usage relay skipped the gate
// entirely and left the wallet freeze stuck until the platform-side TTL
// (300s) auto-expired it.

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// TestPostConsumeQuota_ZeroUsageReleasesPlatformPreAuth drives the zero-usage
// settlement shape used by the abnormal-stream-end callers (quotaDelta =
// -preConsumed, so totalQuota lands on exactly 0): the platform pre-auth must
// be released (not left frozen) and PlatformPreAuthID must be cleared so a
// later ReturnPreConsumedQuota can't release it a second time.
func TestPostConsumeQuota_ZeroUsageReleasesPlatformPreAuth(t *testing.T) {
	db := setupServiceTestDB(t)
	restore := setupOutbox(t, db)
	defer restore()

	const pre = 1_000
	userId := seedTestUser(t, db, 9_000) // already had `pre` deducted at pre-consume
	key, tokenId := seedTestToken(t, db, userId, 9_000, false)

	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{
		UserId:                userId,
		TokenId:               tokenId,
		TokenKey:              key,
		IdentityAccountID:     42,
		PlatformPreAuthID:     991500,
		FinalPreConsumedQuota: pre,
		PlatformGoverned:      true,
	}

	if err := PostConsumeQuota(relayInfo, -pre, pre, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}

	var releaseCount int64
	if err := db.Model(&entity.BillingOutbox{}).
		Where("pre_auth_id = ? AND action = ?", 991500, "release").
		Count(&releaseCount).Error; err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if releaseCount != 1 {
		t.Errorf("release outbox rows for pre-auth 991500 = %d, want 1 (zero-usage settlement must release the frozen pre-auth)", releaseCount)
	}
	if relayInfo.PlatformPreAuthID != 0 {
		t.Errorf("PlatformPreAuthID = %d, want 0 (must be cleared so a later release can't double-enqueue)", relayInfo.PlatformPreAuthID)
	}

	// Guard: a later refund call (the normal failure-path cleanup) must be a
	// no-op now that PlatformPreAuthID is cleared — it must not enqueue a
	// second release row for the same pre-auth id.
	ReturnPreConsumedQuota(c, relayInfo)
	if err := db.Model(&entity.BillingOutbox{}).
		Where("pre_auth_id = ? AND action = ?", 991500, "release").
		Count(&releaseCount).Error; err != nil {
		t.Fatalf("count outbox after refund: %v", err)
	}
	if releaseCount != 1 {
		t.Errorf("release outbox rows for pre-auth 991500 after refund = %d, want 1 (no double enqueue)", releaseCount)
	}
}

// TestPostConsumeQuota_PositiveUsageSettlesPlatformPreAuth is the positive
// control: identical setup but with positive settled usage takes the existing
// settle branch — one "settle" row for the pre-auth id, and no "release" row.
func TestPostConsumeQuota_PositiveUsageSettlesPlatformPreAuth(t *testing.T) {
	db := setupServiceTestDB(t)
	restore := setupOutbox(t, db)
	defer restore()

	const pre = 1_000
	userId := seedTestUser(t, db, 9_000)
	key, tokenId := seedTestToken(t, db, userId, 9_000, false)

	relayInfo := &relaycommon.RelayInfo{
		UserId:                userId,
		TokenId:               tokenId,
		TokenKey:              key,
		IdentityAccountID:     42,
		PlatformPreAuthID:     991501,
		FinalPreConsumedQuota: pre,
		PlatformGoverned:      true,
	}

	// Actual cost came in 10 above the pre-consumed estimate: quota=10,
	// totalQuota = 10+pre = 1010 > 0 => the existing settle branch runs.
	if err := PostConsumeQuota(relayInfo, 10, pre, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}

	var settleCount, releaseCount int64
	if err := db.Model(&entity.BillingOutbox{}).
		Where("pre_auth_id = ? AND action = ?", 991501, "settle").
		Count(&settleCount).Error; err != nil {
		t.Fatalf("count settle outbox: %v", err)
	}
	if settleCount != 1 {
		t.Errorf("settle outbox rows for pre-auth 991501 = %d, want 1", settleCount)
	}
	if err := db.Model(&entity.BillingOutbox{}).
		Where("pre_auth_id = ? AND action = ?", 991501, "release").
		Count(&releaseCount).Error; err != nil {
		t.Fatalf("count release outbox: %v", err)
	}
	if releaseCount != 0 {
		t.Errorf("release outbox rows for pre-auth 991501 = %d, want 0 (positive usage must settle, not release)", releaseCount)
	}
	if relayInfo.PlatformPreAuthID != 0 {
		t.Errorf("PlatformPreAuthID = %d, want 0 (marked handled after settle)", relayInfo.PlatformPreAuthID)
	}
}

// TestPostConsumeQuota_ZeroPerEventUsageKeepsPlatformPreAuth pins the other
// side of the arm: the realtime WSS path (PreWssConsumeQuota) calls
// PostConsumeQuota once per usage event with preConsumedQuota = 0. A
// zero-token event mid-session must NOT release the freeze — the next
// positive event is meant to settle it, and releasing early would push that
// event onto the no-pre-auth debit path, which has no outbox. Same shape as
// the zero-usage case above except for the caller signature, which is
// exactly the distinction the arm keys on.
func TestPostConsumeQuota_ZeroPerEventUsageKeepsPlatformPreAuth(t *testing.T) {
	db := setupServiceTestDB(t)
	restore := setupOutbox(t, db)
	defer restore()

	userId := seedTestUser(t, db, 9_000)
	key, tokenId := seedTestToken(t, db, userId, 9_000, false)

	relayInfo := &relaycommon.RelayInfo{
		UserId:                userId,
		TokenId:               tokenId,
		TokenKey:              key,
		IdentityAccountID:     42,
		PlatformPreAuthID:     991502,
		FinalPreConsumedQuota: 1_000,
		PlatformGoverned:      true,
	}

	// quota=0, preConsumedQuota=0: the per-event shape (quota.go
	// PreWssConsumeQuota), not a final settlement.
	if err := PostConsumeQuota(relayInfo, 0, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}

	var releaseCount int64
	if err := db.Model(&entity.BillingOutbox{}).
		Where("pre_auth_id = ? AND action = ?", 991502, "release").
		Count(&releaseCount).Error; err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if releaseCount != 0 {
		t.Errorf("release outbox rows for pre-auth 991502 = %d, want 0 (a zero per-event usage must leave the freeze for the next event to settle)", releaseCount)
	}
	if relayInfo.PlatformPreAuthID != 991502 {
		t.Errorf("PlatformPreAuthID = %d, want 991502 (must stay live until settled)", relayInfo.PlatformPreAuthID)
	}
}
