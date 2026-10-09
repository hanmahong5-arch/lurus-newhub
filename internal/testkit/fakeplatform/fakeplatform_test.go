package fakeplatform_test

// The fake is only worth something if newhub's real platform client can talk
// to it, so these tests drive it through internal/pkg/common — the same
// functions the relay settlement calls — rather than through hand-built
// requests that could drift from the client.

import (
	"context"
	"errors"
	"math"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/testkit/fakeplatform"
)

func start(t *testing.T) *fakeplatform.Server {
	t.Helper()
	s := fakeplatform.New("ik-test")
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	origURL, origKey := common.IdentityServiceURL, common.IdentityServiceInternalKey
	common.IdentityServiceURL, common.IdentityServiceInternalKey = ts.URL, "ik-test"
	t.Cleanup(func() { common.IdentityServiceURL, common.IdentityServiceInternalKey = origURL, origKey })
	return s
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestPreAuthSettleThroughTheRealClient(t *testing.T) {
	s := start(t)
	s.AddAccount(fakeplatform.Account{ID: 7, IDPSubject: "sub-7", Balance: 10})
	ctx := context.Background()

	pa, err := common.PreAuthorize(ctx, 7, 2.5, "llm-api", "ref-1", "relay", 600)
	if err != nil || pa.PreAuthID == 0 {
		t.Fatalf("PreAuthorize = %+v, %v", pa, err)
	}
	if bal, _ := common.GetWalletBalance(ctx, 7); bal == nil || !near(bal.Frozen, 2.5) || !near(bal.Balance, 10) {
		t.Fatalf("balance after hold = %+v", bal)
	}
	if _, err := common.SettlePreAuth(ctx, pa.PreAuthID, 0.75); err != nil {
		t.Fatalf("SettlePreAuth: %v", err)
	}
	// A retried settle (same Idempotency-Key) must not charge twice.
	if _, err := common.SettlePreAuth(ctx, pa.PreAuthID, 0.75); err != nil {
		t.Fatalf("retried SettlePreAuth: %v", err)
	}
	if got := s.Charged(7); !near(got, 0.75) {
		t.Errorf("Charged = %v, want 0.75 (settle counted once)", got)
	}
	if bal, _ := common.GetWalletBalance(ctx, 7); !near(bal.Frozen, 0) || !near(bal.Balance, 9.25) {
		t.Errorf("balance after settle = %+v, want 9.25 / 0 frozen", bal)
	}
}

func TestReleaseChargesNothing(t *testing.T) {
	s := start(t)
	s.AddAccount(fakeplatform.Account{ID: 8, Balance: 5})
	ctx := context.Background()
	pa, err := common.PreAuthorize(ctx, 8, 1, "llm-api", "ref-2", "relay", 600)
	if err != nil {
		t.Fatal(err)
	}
	if err := common.ReleasePreAuth(ctx, pa.PreAuthID); err != nil {
		t.Fatalf("ReleasePreAuth: %v", err)
	}
	if got := s.Charged(8); got != 0 {
		t.Errorf("Charged after release = %v, want 0", got)
	}
}

func TestInsufficientBalanceIsTheTypedSentinel(t *testing.T) {
	s := start(t)
	s.AddAccount(fakeplatform.Account{ID: 9, Balance: 0.1})
	ctx := context.Background()
	if _, err := common.PreAuthorize(ctx, 9, 1, "llm-api", "ref-3", "relay", 600); !errors.Is(err, common.ErrInsufficientBalance) {
		t.Errorf("PreAuthorize err = %v, want ErrInsufficientBalance", err)
	}
	if _, err := common.DebitWallet(ctx, 9, 1, "llm_usage", "d", "llm-api", "k-1"); !errors.Is(err, common.ErrInsufficientBalance) {
		t.Errorf("DebitWallet err = %v, want ErrInsufficientBalance", err)
	}
}

func TestDebitIsIdempotentAndCounted(t *testing.T) {
	s := start(t)
	s.AddAccount(fakeplatform.Account{ID: 10, Balance: 3})
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := common.DebitWallet(ctx, 10, 0.4, "llm_usage", "d", "llm-api", "same-key"); err != nil {
			t.Fatalf("DebitWallet #%d: %v", i, err)
		}
	}
	if got := s.Charged(10); !near(got, 0.4) {
		t.Errorf("Charged = %v, want 0.4", got)
	}
}

func TestAccountLookupBySubject(t *testing.T) {
	s := start(t)
	s.AddAccount(fakeplatform.Account{ID: 11, IDPSubject: "acc-sub", Email: "a@example.test"})
	got, err := common.GetAccountByZitadelSub(context.Background(), "acc-sub")
	if err != nil || got == nil || got.ID != 11 {
		t.Fatalf("lookup = %+v, %v", got, err)
	}
	if miss, _ := common.GetAccountByZitadelSub(context.Background(), "nobody"); miss != nil {
		t.Errorf("unknown subject resolved to %+v", miss)
	}
}

func TestUnimplementedCallsAreListed(t *testing.T) {
	s := start(t)
	_, _ = common.GetAccountOverview(context.Background(), 1, "llm-api")
	if u := s.Unhandled(); len(u) != 1 {
		t.Errorf("Unhandled = %v, want the overview call", u)
	}
}

// The suite must run the money path production runs. newhub takes that from
// billing-config, not from its env, so the fake has to state production's
// values — checked here through the real poller.
func TestBillingConfigStatesProductionThroughTheRealPoller(t *testing.T) {
	start(t)
	origUnified, origAdvisory := common.BillingUnifiedEnabled(), common.LocalLedgerAdvisory()
	t.Cleanup(func() {
		common.SetBillingUnifiedEnabled(origUnified)
		common.SetLocalLedgerAdvisory(origAdvisory)
	})
	common.SetBillingUnifiedEnabled(false)
	common.SetLocalLedgerAdvisory(true)

	ctx, cancel := context.WithCancel(context.Background())
	done := common.StartBillingConfigPoller(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for common.BillingConfigEffective() != "unified=true(platform) advisory=false(platform)" {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("billing-config effective = %q, want production's unified=true advisory=false, both stated by the platform",
				common.BillingConfigEffective())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

// 450 quota of deepseek-chat is 0.00657 yuan; the platform's decimal(14,4)
// columns book it as 0.0066, and so must the fake.
func TestSettleIsBookedAtThePlatformsPrecision(t *testing.T) {
	s := start(t)
	s.AddAccount(fakeplatform.Account{ID: 12, Balance: 1})
	ctx := context.Background()
	pa, err := common.PreAuthorize(ctx, 12, 0.001095, "llm-api", "ref-r", "relay", 600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := common.SettlePreAuth(ctx, pa.PreAuthID, 0.00657); err != nil {
		t.Fatalf("over-settle within the balance must succeed, as on the platform: %v", err)
	}
	if got := s.Charged(12); got != 0.0066 {
		t.Errorf("Charged = %v, want 0.0066", got)
	}
	if bal, _ := common.GetWalletBalance(ctx, 12); bal.Balance != 0.9934 || bal.Frozen != 0 {
		t.Errorf("balance = %+v, want 0.9934 / 0 frozen", bal)
	}
}
