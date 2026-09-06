package app

// quota_playground_pool_test.go — PostConsumeQuota's tenant-pool debit
// (debitTenantPool, gated by `poolDebit > 0 && relayInfo.TokenId > 0`) fires
// for a playground-shaped relayInfo — IsPlayground=true skips the per-token
// quota decrement but must NOT exempt the pool — and debits only the owning
// tenant's pool.
//
// Scope, stated so this file is not read as more than it is: the relayInfo
// here is built by hand, so this passes on any revision, including one where
// the /pg handler throws the real token id away. It documents the downstream
// contract the handler has to satisfy; the lock on the handler actually
// satisfying it is TestPlayground_PreservesResolvedTokenIdentity in
// internal/adapter/handler — that one goes red when /pg rebuilds its context
// from a zero-value token, which is how playground spend used to move no
// credit pool at all no matter how much a caller consumed.

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

func TestPostConsumeQuota_PlaygroundDebitsRealTenantPool(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)

	userId := seedTestUser(t, db, 1_000_000)
	tokenId := seedTenantToken(t, db, userId, "t-pg")

	pool, err := repo.CreateTenantCreditPool("t-pg", 1, 1000, repo.PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if _, err := repo.TopupPool(pool.ID, "t-pg", 1000, 1, "seed"); err != nil {
		t.Fatalf("topup t-pg: %v", err)
	}

	// A second tenant's pool must stay untouched by the debit below — proves
	// the debit is scoped to the RIGHT token/tenant, not some global effect.
	otherUserId := seedTestUser(t, db, 1_000_000)
	_ = seedTenantToken(t, db, otherUserId, "t-other")
	otherPool, err := repo.CreateTenantCreditPool("t-other", 1, 1000, repo.PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool t-other: %v", err)
	}
	if _, err := repo.TopupPool(otherPool.ID, "t-other", 1000, 1, "seed"); err != nil {
		t.Fatalf("topup t-other: %v", err)
	}

	// Build relayInfo the way the /pg path does after this lane's fix:
	// IsPlayground=true (skips the per-token quota decrement, quota.go:885),
	// but a REAL TokenId (not zero) so the pool debit at quota.go:866 fires.
	relayInfo := &relaycommon.RelayInfo{
		UserId:       userId,
		TokenId:      tokenId,
		IsPlayground: true,
	}

	if err := PostConsumeQuota(relayInfo, 100, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}

	got, err := repo.GetTenantCreditPool("t-pg")
	if err != nil {
		t.Fatalf("readback t-pg: %v", err)
	}
	if got.CurrentBalance != 900 {
		t.Errorf("t-pg balance = %d, want 900 (1000 - 100 playground debit) — the playground identity's real token id must reach debitTenantPool", got.CurrentBalance)
	}

	otherGot, err := repo.GetTenantCreditPool("t-other")
	if err != nil {
		t.Fatalf("readback t-other: %v", err)
	}
	if otherGot.CurrentBalance != 1000 {
		t.Errorf("t-other balance = %d, want untouched 1000", otherGot.CurrentBalance)
	}
}
