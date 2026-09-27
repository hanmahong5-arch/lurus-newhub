package app

// l3_pre_consume_topup_url_test.go — D4 (lane L3) originally locked the
// token-insufficient-quota 402 returned by PreConsumeQuota
// (pre_consume_quota.go, the ErrTokenQuotaInsufficient branch) as carrying a
// topup_url in its error metadata, matching the OTHER three 402s in this
// file (user-balance fast pre-check, atomic-debit race, platform pre-auth
// failure).
//
// R2 correction (B2): that assertion locked a DEFECT. ErrTokenQuotaInsufficient
// (quota.go:645-649, explicitly documented "not ledger state") is a per-TOKEN
// spending cap, not the user's wallet balance — a wallet top-up cannot raise
// it. TestL3PreConsumeQuota_TokenInsufficient_HasTokenQuotaHint below is the
// INVERTED assertion: reason/token_remain_quota_units present, topup_url absent.
//
// The companion tests further down (TestR2...) prove the inversion did not
// over-reach: the three genuine USER-BALANCE 402s this file is named after
// still carry topup_url — untouched by the B2 fix, which only added a branch
// inside the ErrTokenQuotaInsufficient case.
//   - fast pre-check (pre_consume_quota.go ~:111-113): covered by the
//     existing TestPreConsumeQuota_InsufficientLocalQuota_402_CarriesTopupURL
//     in pre_consume_quota_test.go (unmodified by this lane).
//   - atomic-debit race (~:206-210): TestR2PreConsumeQuota_ConcurrentDebitRace_LosersHaveTopupURL.
//   - platform pre-auth OUTAGE (~:264-268): TestR2PreConsumeQuota_PlatformPreAuthOutage_Is503NotTopup
//     — no longer a topup 402 since cycle 18 L2; only the platform's own
//     insufficient_balance verdict keeps that shape (see
//     pre_consume_platform_failure_test.go).
//
// Reuses the same harness as TestPreConsumeQuota_TokenInsufficientReleasesAndErrors
// (pre_consume_extra_test.go) — that test proves the rejection itself; this one
// proves the error's Metadata shape.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestL3PreConsumeQuota_TokenInsufficient_HasTokenQuotaHint(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	repo.InitCol() // the token-cap branch re-reads the token by key (commonKeyCol) for the hint's remain-quota figure

	userId := seedTestUser(t, db, 50_000)
	key, tokenId := seedTestToken(t, db, userId, 100, false) // token can't cover estimate

	c := createTestGinContext()
	c.Set("token_quota", 100)

	relayInfo := &relaycommon.RelayInfo{
		UserId:         userId,
		TokenId:        tokenId,
		TokenKey:       key,
		TokenUnlimited: false,
	}

	apiErr := PreConsumeQuota(c, 5_000, relayInfo)
	if apiErr == nil {
		t.Fatal("expected error when token quota is insufficient")
	}
	if apiErr.StatusCode != 402 {
		t.Fatalf("StatusCode = %d, want 402", apiErr.StatusCode)
	}

	oaErr := apiErr.ToOpenAIError()
	if len(oaErr.Metadata) == 0 {
		t.Fatalf("expected non-empty error metadata (token-cap hint), got none; error=%q", oaErr.Message)
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(oaErr.Metadata, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v; metadata=%s", err, oaErr.Metadata)
	}
	if meta["reason"] != "token_quota_exhausted" {
		t.Errorf(`metadata["reason"] = %v, want "token_quota_exhausted"`, meta["reason"])
	}
	if remain, present := meta["token_remain_quota_units"]; !present {
		t.Errorf("metadata missing token_remain_quota_units; metadata=%s", oaErr.Metadata)
	} else if remain != float64(100) {
		// The rejected debit never touches remain_quota, so it must still
		// read back the seeded value (100), proving the hint re-reads the
		// real row rather than defaulting to 0 on lookup failure.
		t.Errorf(`metadata["token_remain_quota_units"] = %v, want 100`, remain)
	}
	if _, present := meta["token_remain_quota"]; present {
		t.Errorf("metadata must use the unambiguous token_remain_quota_units key, not the old token_remain_quota name, got: %s", oaErr.Metadata)
	}
	if _, present := meta["topup_url"]; present {
		t.Errorf("token's OWN spending cap must NOT surface a wallet topup_url (wrong remedy), got: %s", oaErr.Metadata)
	}
}

// TestR2PreConsumeQuota_PlatformPreAuthOutage_Is503NotTopup pins the
// platform-pre-auth OUTAGE shape (pre_consume_quota.go, platformPreAuthorize's
// error return). Until cycle 18 L2 this test locked the opposite — a 402 with
// topup_url, on the premise that "a platform outage is a USER-BALANCE
// failure". It is not: the customer may be fully funded, and telling them to
// top up (with a status their SDK will not retry) hides our outage behind
// their wallet. Only the platform's own insufficient_balance verdict keeps the
// 402 + topup_url shape — pre_consume_platform_failure_test.go has the full
// table; this test keeps the outage arm pinned from the file that used to
// lock it wrong.
func TestR2PreConsumeQuota_PlatformPreAuthOutage_Is503NotTopup(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)

	prevUnified := common.BillingUnifiedEnabled()
	common.SetBillingUnifiedEnabled(true)
	t.Cleanup(func() { common.SetBillingUnifiedEnabled(prevUnified) })

	// Reset the breaker to a known-closed state so degradeAdmissible's
	// breaker-open guard cannot accidentally let this test degrade instead
	// of 402ing; TryDegradedPreAuth denies anyway (no cache warmed for this
	// account) but a closed breaker makes the denial reason unambiguous.
	common.BillingBreakerSuccess()
	t.Cleanup(common.BillingBreakerSuccess)

	prevPreAuth := preAuthorizeWithBreaker
	preAuthorizeWithBreaker = func(ctx context.Context, accountID int64, amount float64,
		productID, referenceID, description string, ttlSeconds int) (*common.PreAuthResult, error) {
		return nil, errors.New("simulated platform billing outage")
	}
	t.Cleanup(func() { preAuthorizeWithBreaker = prevPreAuth })

	userId := seedTestUser(t, db, 50_000)

	c := createTestGinContext()
	c.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)
	c.Set("tenant_id", "r2-preauth-fail")

	relayInfo := &relaycommon.RelayInfo{
		UserId:            userId,
		IdentityAccountID: 909090,
		TokenUnlimited:    true, // isolate the platform-preauth branch from the token-cap branch
		OriginModelName:   "gpt-4",
	}

	apiErr := PreConsumeQuota(c, 1_000, relayInfo)
	if apiErr == nil {
		t.Fatal("expected an error when platform pre-auth fails and degrade denies")
	}
	if apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("StatusCode = %d, want 503 (a platform outage is OUR failure, not the customer's balance)", apiErr.StatusCode)
	}
	if apiErr.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
		t.Errorf("errorCode = %q — an outage must not wear the out-of-money code", apiErr.GetErrorCode())
	}
	if !types.IsRecordErrorLog(apiErr) {
		t.Errorf("an outage must leave an error-log row; only a customer's empty wallet is exempt")
	}
	if apiErr.RetryAfterUnix <= 0 {
		t.Errorf("RetryAfterUnix = %d, want > 0 (503 without Retry-After leaves the SDK guessing)", apiErr.RetryAfterUnix)
	}
	if strings.Contains(string(apiErr.ToOpenAIError().Metadata), "topup_url") {
		t.Errorf("an outage must not offer a top-up link, got metadata %s", apiErr.ToOpenAIError().Metadata)
	}
}

// TestR2PreConsumeQuota_ConcurrentDebitRace_LosersHaveTopupURL proves the
// atomic-conditional-debit race branch (pre_consume_quota.go, the
// DecreaseUserQuotaIfEnough ok==false path) still carries topup_url. The DB
// helper pins sql.DB to a single connection (setupServiceTestDB), so the two
// concurrent calls cannot execute SQL simultaneously and cannot hit
// SQLITE_BUSY — but the Go-level fast pre-check and the later atomic UPDATE
// are two separate statements with logic in between, so the scheduler can
// still interleave two callers between them, reproducing the exact TOCTOU
// the atomic debit exists to close.
//
// The invariant asserted does not depend on WHICH of the two request-side
// branches (fast pre-check vs. atomic race) catches each loser — both use
// the identical ErrorCodeInsufficientUserQuota + ErrOptionWithTopupURL()
// construction, so "exactly one winner, every loser 402s with topup_url" is
// deterministic regardless of scheduling.
func TestR2PreConsumeQuota_ConcurrentDebitRace_LosersHaveTopupURL(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)

	const startQuota = 100
	const cost = 100
	const n = 8
	userId := seedTestUser(t, db, startQuota)

	var wg sync.WaitGroup
	errs := make([]*types.NewAPIError, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			c := createTestGinContext()
			relayInfo := &relaycommon.RelayInfo{
				UserId:         userId,
				TokenUnlimited: true, // isolate the user-balance branch from the token-cap branch
			}
			errs[idx] = PreConsumeQuota(c, cost, relayInfo)
		}(i)
	}
	wg.Wait()

	wins, losses := 0, 0
	for _, apiErr := range errs {
		if apiErr == nil {
			wins++
			continue
		}
		losses++
		if apiErr.StatusCode != http.StatusPaymentRequired {
			t.Errorf("loser StatusCode = %d, want 402", apiErr.StatusCode)
		}
		oaErr := apiErr.ToOpenAIError()
		var meta struct {
			TopupURL string `json:"topup_url"`
		}
		if err := json.Unmarshal(oaErr.Metadata, &meta); err != nil {
			t.Fatalf("unmarshal metadata: %v; metadata=%s", err, oaErr.Metadata)
		}
		if meta.TopupURL == "" {
			t.Errorf("loser must carry topup_url, got: %s", oaErr.Metadata)
		}
	}
	if wins != 1 {
		t.Errorf("wins = %d, want exactly 1 (startQuota=%d only covers one %d-cost request)", wins, startQuota, cost)
	}
	if losses != n-1 {
		t.Errorf("losses = %d, want %d", losses, n-1)
	}
}

// TestL3ErrOptionWithTopupURL_SetsMetadata is a narrow construction check on
// the option itself (types.ErrOptionWithTopupURL), independent of DB/gin
// wiring, so the option's own contract stays pinned regardless of how the
// pre-consume call site above evolves.
func TestL3ErrOptionWithTopupURL_SetsMetadata(t *testing.T) {
	apiErr := types.NewErrorWithStatusCode(
		errNoQuota, types.ErrorCodePreConsumeTokenQuotaFailed, 402,
		types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog(),
		types.ErrOptionWithTopupURL(),
	)
	oaErr := apiErr.ToOpenAIError()
	if len(oaErr.Metadata) == 0 {
		t.Fatal("expected non-empty metadata from ErrOptionWithTopupURL")
	}
}

var errNoQuota = topupURLTestErr{}

type topupURLTestErr struct{}

func (topupURLTestErr) Error() string { return "insufficient token quota" }
