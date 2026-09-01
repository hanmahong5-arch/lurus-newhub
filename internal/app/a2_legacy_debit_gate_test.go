package app

// a2_legacy_debit_gate_test.go — the legacy (no-pre-auth) wallet debit must obey
// the SAME consistency principle as the pre-auth branch.
//
// PostConsumeQuota's pre-auth branch deliberately RELEASES instead of settling
// when the local token-quota write failed and we are not in advisory mode
// ("avoid charging the wallet for a request that wasn't properly recorded
// locally"). The legacy branch used to fire DebitWalletGRPC unconditionally, so
// the exact case the pre-auth branch refuses to charge WAS charged for any
// account that happened to have no pre-auth (flag-off window, high-balance
// pre-auth skip, degraded admit).
//
// Oracle: metrics.BillingUsageMirrorTotal{status="error"}. The mirror is fired
// from inside the same goroutine, immediately after the debit, and is keyed on
// the same `charge` decision — with IdentityServiceURL empty,
// common.ReportUsageEvent fails instantly (no network) and bumps that counter.
// So counter delta 1 == "the debit path ran", delta 0 == "it was gated off".

import (
	"context"
	"sync/atomic"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"gorm.io/gorm"
)

// legacyDebitEnv pins the globals this branch reads: no Redis (async legs are
// DB-free), no platform URL (the HTTP twin of every billing call fails
// instantly instead of dialing).
func legacyDebitEnv(t *testing.T) {
	t.Helper()
	prevRedis := common.RedisEnabled
	prevURL := common.IdentityServiceURL
	common.RedisEnabled = false
	common.IdentityServiceURL = ""
	t.Cleanup(func() {
		common.RedisEnabled = prevRedis
		common.IdentityServiceURL = prevURL
	})
}

func usageMirrorErrors() float64 {
	return testutil.ToFloat64(metrics.BillingUsageMirrorTotal.WithLabelValues("error"))
}

// hookLegacyDebit forces the async settlement leg inline and stubs the
// money-moving call itself, returning a call counter. The mirror counter alone
// is a PROXY oracle: both it and the debit read the same `charge` flag, so a
// mutation that ungated only the debit (leaving the mirror gated) would slip
// past a mirror-only assertion. Counting debitWalletGRPC directly closes that.
func hookLegacyDebit(t *testing.T) *int32 {
	t.Helper()
	prevAsync := AsyncGo
	AsyncGo = func(f func()) { f() }
	t.Cleanup(func() { AsyncGo = prevAsync })

	var calls int32
	prevDebit := debitWalletGRPC
	debitWalletGRPC = func(_ context.Context, _ int64, _ float64, _, _, _, _ string) (*common.DebitWalletResult, error) {
		atomic.AddInt32(&calls, 1)
		return &common.DebitWalletResult{}, nil
	}
	t.Cleanup(func() { debitWalletGRPC = prevDebit })
	return &calls
}

// breakTokenTable drops the tokens table AFTER seeding, which is the only way to
// make PostConsumeQuota's Phase 3 token-quota write fail on demand (every other
// input to repo.DecreaseTokenQuota is validated upstream). That failure is
// exactly the `localQuotaConsistent = false` state both settlement branches key
// their charge decision on.
func breakTokenTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Migrator().DropTable(&repo.Token{}); err != nil {
		t.Fatalf("drop tokens table: %v", err)
	}
}

// TestPostConsumeQuota_LegacyDebit_SkippedWhenLocalQuotaInconsistent is the A2
// lock. Mutation oracle: dropping the `charge` gate (debiting unconditionally,
// as before) makes the mirror fire and this delta becomes 1.
func TestPostConsumeQuota_LegacyDebit_SkippedWhenLocalQuotaInconsistent(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	isolateBizTPMWindow(t)
	legacyDebitEnv(t)

	userId := seedTestUser(t, db, 100_000)
	key, tokenId := seedTestToken(t, db, userId, 100_000, false)
	breakTokenTable(t, db)
	debits := hookLegacyDebit(t)

	relayInfo := &relaycommon.RelayInfo{
		UserId:            userId,
		TokenId:           tokenId,
		TokenKey:          key,
		IdentityAccountID: 55, // platform account
		PlatformPreAuthID: 0,  // no pre-auth => legacy branch
		PlatformGoverned:  false,
	}

	before := usageMirrorErrors()
	err := PostConsumeQuota(relayInfo, 700, 0, false)
	after := usageMirrorErrors()

	if got := atomic.LoadInt32(debits); got != 0 {
		t.Errorf("debitWalletGRPC calls = %d, want 0 — the wallet was charged for a locally-inconsistent settlement", got)
	}

	// The token-quota failure is still surfaced to the caller, unchanged.
	if err == nil {
		t.Fatal("expected the token-quota write error to be returned (local state inconsistent, non-advisory)")
	}
	if delta := after - before; delta != 0 {
		t.Errorf("usage-mirror errors delta = %v, want 0 — the legacy wallet debit fired for a request whose "+
			"local settlement failed, which is exactly what the pre-auth branch refuses to do", delta)
	}
}

// TestPostConsumeQuota_LegacyDebit_FiresWhenLocalQuotaConsistent is the positive
// control: the gate must not have turned the legacy debit off altogether. Same
// setup, token table intact.
func TestPostConsumeQuota_LegacyDebit_FiresWhenLocalQuotaConsistent(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	isolateBizTPMWindow(t)
	legacyDebitEnv(t)

	userId := seedTestUser(t, db, 100_000)
	key, tokenId := seedTestToken(t, db, userId, 100_000, false)
	debits := hookLegacyDebit(t)

	relayInfo := &relaycommon.RelayInfo{
		UserId:            userId,
		TokenId:           tokenId,
		TokenKey:          key,
		IdentityAccountID: 56,
		PlatformPreAuthID: 0,
		PlatformGoverned:  false,
	}

	before := usageMirrorErrors()
	if err := PostConsumeQuota(relayInfo, 700, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}
	after := usageMirrorErrors()

	if got := atomic.LoadInt32(debits); got != 1 {
		t.Errorf("debitWalletGRPC calls = %d, want 1 — the gate must not turn the legacy debit off altogether", got)
	}

	if delta := after - before; delta != 1 {
		t.Errorf("usage-mirror errors delta = %v, want 1 — a consistent local settlement must still charge "+
			"the wallet and mirror the charge", delta)
	}
	if got := userQuota(t, db, userId); got != 100_000-700 {
		t.Errorf("user quota = %d, want %d", got, 100_000-700)
	}
}
