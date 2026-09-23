package app

// wallet_currency_test.go — the amounts that leave for the platform wallet
// are in CNY, and quota is priced in USD. Until 2026-09-23 both of these
// passed quota/QuotaPerUnit, i.e. charged CNY 1 for $1 of usage — 1/7.3 of
// the real cost — and no test looked at the amount, only at whether a
// debit happened. These two pin the amount itself.

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
)

// 70,000 quota is $0.14 (QuotaPerUnit = 500,000 per $1), i.e. CNY 1.022 at
// 7.3 CNY/USD. The old conversion sent 0.14.
const (
	walletTestQuota = 70_000
	walletTestCNY   = 1.022
)

func pinUSDRate(t *testing.T, rate float64) {
	t.Helper()
	prev := operation_setting.USDExchangeRate
	operation_setting.USDExchangeRate = rate
	t.Cleanup(func() { operation_setting.USDExchangeRate = prev })
}

func TestPostConsumeQuota_WalletDebitIsInCNY(t *testing.T) {
	pinUSDRate(t, 7.3)
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	isolateBizTPMWindow(t)
	legacyDebitEnv(t)

	userId := seedTestUser(t, db, 1_000_000)
	key, tokenId := seedTestToken(t, db, userId, 1_000_000, false)

	prevAsync := AsyncGo
	AsyncGo = func(f func()) { f() }
	t.Cleanup(func() { AsyncGo = prevAsync })

	var calls int32
	var gotAmount float64
	prevDebit := debitWalletGRPC
	debitWalletGRPC = func(_ context.Context, _ int64, amount float64, _, _, _, _ string) (*common.DebitWalletResult, error) {
		atomic.AddInt32(&calls, 1)
		gotAmount = amount
		return &common.DebitWalletResult{}, nil
	}
	t.Cleanup(func() { debitWalletGRPC = prevDebit })

	relayInfo := &relaycommon.RelayInfo{
		UserId:            userId,
		TokenId:           tokenId,
		TokenKey:          key,
		IdentityAccountID: 57,
	}
	if err := PostConsumeQuota(relayInfo, walletTestQuota, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("debitWalletGRPC calls = %d, want 1", calls)
	}
	if math.Abs(gotAmount-walletTestCNY) > 1e-9 {
		t.Errorf("wallet debit = %v CNY for %d quota, want %v ($0.14 at 7.3 CNY/USD)",
			gotAmount, walletTestQuota, walletTestCNY)
	}
}

func TestPlatformPreAuthorize_AmountIsInCNY(t *testing.T) {
	pinUSDRate(t, 7.3)
	withMiniRedisTPM(t)
	wait := syncAsyncGo(t)

	var gotAmount float64
	prev := preAuthorizeWithBreaker
	preAuthorizeWithBreaker = func(_ context.Context, _ int64, amount float64,
		_, _, _ string, _ int) (*common.PreAuthResult, error) {
		gotAmount = amount
		return &common.PreAuthResult{PreAuthID: 1, Amount: amount, Status: "held"}, nil
	}
	t.Cleanup(func() { preAuthorizeWithBreaker = prev })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"balance": 1.0, "frozen": 0.0})
	}))
	defer srv.Close()
	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = srv.URL
	t.Cleanup(func() { common.IdentityServiceURL = prevURL })

	c := createTestGinContext()
	c.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)
	relayInfo := &relaycommon.RelayInfo{UserId: 1, IdentityAccountID: 66031, OriginModelName: "m"}
	if apiErr := platformPreAuthorize(c, walletTestQuota, relayInfo); apiErr != nil {
		t.Fatalf("platformPreAuthorize: %v", apiErr.Error())
	}
	wait()
	if math.Abs(gotAmount-walletTestCNY) > 1e-9 {
		t.Errorf("pre-auth hold = %v CNY for %d quota, want %v", gotAmount, walletTestQuota, walletTestCNY)
	}
}
