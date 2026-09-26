package app

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/currency"
)

// TestPostConsumeQuota_RecordsTheWalletCharge: whatever PostConsumeQuota
// decides to take from the platform wallet is recorded on RelayInfo (and from
// there on the consume log), in the wallet's 0.0001 CNY unit. Before this the
// charge existed only as a transient float passed to the RPC; every later
// reader re-derived it from quota at whatever rate was current then.
func TestPostConsumeQuota_RecordsTheWalletCharge(t *testing.T) {
	const quota = 700
	want := currency.CNYToUnits4(currency.QuotaToCNY(quota))
	if want <= 0 {
		t.Fatalf("test quota %d rounds to a zero wallet charge; pick a larger one", quota)
	}

	cases := []struct {
		name       string
		preAuthID  int64
		breakLocal bool
		want       int64
	}{
		{"pre-auth settle", 42, false, want},
		{"legacy direct debit", 0, false, want},
		// Local write failed, not advisory: the wallet is deliberately not
		// charged, so nothing may be recorded as charged.
		{"legacy, local write failed", 0, true, 0},
		{"pre-auth released, local write failed", 42, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := setupServiceTestDB(t)
			seedPoolTables(t, db)
			isolateBizTPMWindow(t)
			legacyDebitEnv(t)
			hookSettleSeam(t)
			hookLegacyDebit(t) // also forces AsyncGo inline

			userId := seedTestUser(t, db, 100_000)
			key, tokenId := seedTestToken(t, db, userId, 100_000, false)
			if tc.breakLocal {
				breakTokenTable(t, db)
			}
			relayInfo := &relaycommon.RelayInfo{
				UserId: userId, TokenId: tokenId, TokenKey: key,
				IdentityAccountID: 77, PlatformPreAuthID: tc.preAuthID,
			}
			_ = PostConsumeQuota(relayInfo, quota, 0, false)
			if relayInfo.WalletChargeCNY4 != tc.want {
				t.Fatalf("WalletChargeCNY4 = %d, want %d", relayInfo.WalletChargeCNY4, tc.want)
			}
		})
	}
}
