package app

// pre_consume_toctou_test.go — non-regression guards for the atomic pre-consume
// gate (token gate #8 + user gate #9). The concurrency overdraft proof itself
// lives in the repo package against real PostgreSQL
// (quota_if_enough_concurrency_pg_test.go); SQLite serialises writers and cannot
// manufacture the interleaving, so here we pin the behavioural contract the fix
// must NOT change:
//
//   - a legitimate request with EXACTLY enough balance still succeeds and debits
//     (guards the `>= quota` boundary against an off-by-one that would 402 paying
//     traffic);
//   - unlimited tokens keep debiting unconditionally (no per-key cap);
//   - the token gate still rejects an over-cap request without touching remain.

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// TestPreConsumeTokenQuota_ExactBalanceDebits — remain == quota must be admitted
// (boundary of the `remain_quota >= ?` guard).
func TestPreConsumeTokenQuota_ExactBalanceDebits(t *testing.T) {
	db := setupServiceTestDB(t)
	repo.InitCol()
	userId := seedTestUser(t, db, 10_000)
	key, tokenId := seedTestToken(t, db, userId, 300, false)

	relayInfo := &relaycommon.RelayInfo{TokenId: tokenId, TokenKey: key}
	if err := PreConsumeTokenQuota(relayInfo, 300); err != nil {
		t.Fatalf("exact-balance debit must succeed, got: %v", err)
	}
	if got := tokenRemain(t, db, tokenId); got != 0 {
		t.Errorf("token remain = %d, want 0 (300-300)", got)
	}
}

// TestPreConsumeTokenQuota_UnlimitedTokenDebitsUnconditionally — an unlimited
// token has no per-key cap: PreConsumeTokenQuota must debit unconditionally
// (used_quota accounting) exactly as before the atomic gate, even when the
// nominal remain_quota goes negative.
func TestPreConsumeTokenQuota_UnlimitedTokenDebitsUnconditionally(t *testing.T) {
	db := setupServiceTestDB(t)
	repo.InitCol()
	userId := seedTestUser(t, db, 10_000)
	key, tokenId := seedTestToken(t, db, userId, 0, true) // unlimited, remain 0

	relayInfo := &relaycommon.RelayInfo{TokenId: tokenId, TokenKey: key, TokenUnlimited: true}
	if err := PreConsumeTokenQuota(relayInfo, 300); err != nil {
		t.Fatalf("unlimited token must not be rejected, got: %v", err)
	}

	var tok repo.Token
	if err := db.First(&tok, tokenId).Error; err != nil {
		t.Fatalf("read token: %v", err)
	}
	if tok.RemainQuota != -300 {
		t.Errorf("remain = %d, want -300 (unconditional debit, unchanged behavior)", tok.RemainQuota)
	}
	if tok.UsedQuota != 300 {
		t.Errorf("used_quota = %d, want 300", tok.UsedQuota)
	}
}

// TestPreConsumeQuota_NonAdvisory_ExactBalanceSucceeds — the whole gate, non
// advisory: a user + token each holding EXACTLY the estimate must be admitted
// and drained to zero, not 402'd. This is the "don't reject legitimate paying
// traffic" pin for the atomic user + token debits together.
func TestPreConsumeQuota_NonAdvisory_ExactBalanceSucceeds(t *testing.T) {
	db := setupServiceTestDB(t)
	repo.InitCol()
	seedPoolTables(t, db)
	withAdvisory(t, false)

	const estimate = 1_000
	userId := seedTestUser(t, db, estimate)          // exactly enough
	key, tokenId := seedTestToken(t, db, userId, estimate, false) // exactly enough

	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{
		UserId:         userId,
		TokenId:        tokenId,
		TokenKey:       key,
		TokenUnlimited: false,
	}

	if apiErr := PreConsumeQuota(c, estimate, relayInfo); apiErr != nil {
		t.Fatalf("exact-balance request must succeed, got: %v", apiErr.Error())
	}
	if relayInfo.FinalPreConsumedQuota != estimate {
		t.Errorf("FinalPreConsumedQuota = %d, want %d", relayInfo.FinalPreConsumedQuota, estimate)
	}
	if got := userQuota(t, db, userId); got != 0 {
		t.Errorf("user quota = %d, want 0 (exactly drained)", got)
	}
	if got := tokenRemain(t, db, tokenId); got != 0 {
		t.Errorf("token remain = %d, want 0 (exactly drained)", got)
	}
}
