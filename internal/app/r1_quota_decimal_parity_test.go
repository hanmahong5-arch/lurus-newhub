package app

// r1_quota_decimal_parity_test.go — R1 lane. A2: proves the Claude-native
// settlement accumulation (PostClaudeConsumeQuota / claudeCalculateQuota,
// quota.go) is decimal end-to-end and no longer loses precision the way the
// old float64 accumulator did. Verified counterexample: modelRatio=0.125,
// groupRatio=2.5, completionRatio=0.7, prompt=4, completion=24 sums to
// exactly 6.5 in decimal (Round(0) => 7) but to 6.49999999999999911182 in
// float64 (Round(0) => 6) — an undercharge that is silent because it never
// throws, just quietly rounds down.
//
// Also carries A5: the per-account log throttle on warnZeroWalletAmount does
// not affect the (unconditional, per D-A5) metrics counter.

import (
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/shopspring/decimal"
)

// TestR1ClaudeQuota_DecimalParity_Exemplar drives the real settlement path
// (not the pure helper) with the operator-verified counterexample and asserts
// the debited quota and the consume-log row both land on 7 — the decimal
// answer — not 6, the float64 answer.
func TestR1ClaudeQuota_DecimalParity_Exemplar(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 1_000_000)
	key, tokenId := seedTestToken(t, db, userId, 1_000_000, false)

	c := createTestGinContext()
	c.Set("token_name", "tkn")

	usage := &dto.Usage{
		PromptTokens:     4,
		CompletionTokens: 24,
		TotalTokens:      28,
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: "claude-3-5-sonnet",
		StartTime:       time.Now().Add(-time.Second),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio:      0.125,
			CompletionRatio: 0.7,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 2.5},
		},
	}

	const wantQuota = 7 // exact decimal 6.5, round-half-up => 7 (float64 path gave 6)

	before := userQuota(t, db, userId)
	PostClaudeConsumeQuota(c, relayInfo, usage)
	after := userQuota(t, db, userId)

	if before-after != wantQuota {
		t.Errorf("Claude debited %d, want %d (decimal exact 6.5 rounds to 7)", before-after, wantQuota)
	}

	var logRow repo.Log
	if err := db.Where("user_id = ? AND type = ?", userId, repo.LogTypeConsume).First(&logRow).Error; err != nil {
		t.Fatalf("no consume log written: %v", err)
	}
	if logRow.Quota != wantQuota {
		t.Errorf("consume log quota = %d, want %d", logRow.Quota, wantQuota)
	}
}

// TestR1ClaudeQuota_MatchesCompatibleFormula_Grid diffs claudeCalculateQuota
// (production, quota.go) against an INDEPENDENTLY hand-typed decimal
// reference expression below (not a call into any production helper — the
// measurement and the thing measured must not share code) over a fixed
// deterministic grid. No cache/cache-creation dimensions are exercised here
// (all zero) — this isolates the exact term the old float64 accumulator lost
// precision on: promptTokens + completionTokens*completionRatio, scaled by
// modelRatio*groupRatio.
func TestR1ClaudeQuota_MatchesCompatibleFormula_Grid(t *testing.T) {
	modelRatios := []float64{0.02, 0.125, 0.5, 1, 3}
	groupRatios := []float64{1, 2.5}
	completionRatios := []float64{0.7, 1, 5}

	checked := 0
	for _, mr := range modelRatios {
		for _, gr := range groupRatios {
			for _, cr := range completionRatios {
				for prompt := 0; prompt <= 12; prompt++ {
					for completion := 0; completion <= 24; completion += 4 {
						checked++

						got := claudeCalculateQuota(false,
							prompt, 0, 0, 0, 0, completion,
							0, 0, 0, 0, cr, gr, mr, 0,
						)

						// Independently hand-typed reference — same math, not
						// the same code: refQuota = (prompt + completion*cr) * mr * gr.
						ref := decimal.NewFromInt(int64(prompt)).
							Add(decimal.NewFromInt(int64(completion)).Mul(decimal.NewFromFloat(cr))).
							Mul(decimal.NewFromFloat(mr)).
							Mul(decimal.NewFromFloat(gr))

						gotRounded := got.Round(0).IntPart()
						refRounded := ref.Round(0).IntPart()
						if gotRounded != refRounded {
							t.Fatalf("mismatch at modelRatio=%v groupRatio=%v completionRatio=%v prompt=%d completion=%d: claudeCalculateQuota=%s (round %d), reference=%s (round %d)",
								mr, gr, cr, prompt, completion, got.String(), gotRounded, ref.String(), refRounded)
						}
					}
				}
			}
		}
	}
	if checked < 1000 {
		t.Fatalf("grid too small to be meaningful: only %d combinations checked", checked)
	}
	t.Logf("checked %d combinations", checked)
}

// TestR1ZeroWalletWarn_ThrottlesLogNotMetric proves warnZeroWalletAmount's
// per-account throttle gates the LOG LINE, not the metric (D-A5: the counter
// must stay unconditional so operators can see how many charges were lost,
// even while the log noise is suppressed). This is the A5 throttle-body
// coverage itself: swapping quota.go's zeroWalletWarnLogf seam (over
// common.SysError) for a counting closure is what makes the suppression
// `return` in warnZeroWalletAmount's throttle block observable at all —
// without the seam, no test in this package can tell "the throttle
// suppressed this call" from "the throttle never fired" (common.SysError
// only writes to the process log). Adversarial round found this: deleting
// the suppression `return` left `go test ./internal/app/` fully green
// because nothing here previously asserted a log-call COUNT, only the
// metric and the limiter map's key presence.
//
// Also proves: the throttle key is the account id, not a global flag (two
// distinct accounts both log on their own first call, independently); and
// the window expires rather than suppressing forever.
func TestR1ZeroWalletWarn_ThrottlesLogNotMetric(t *testing.T) {
	resetZeroWalletWarnThrottle()
	t.Cleanup(resetZeroWalletWarnThrottle)

	prevLogf := zeroWalletWarnLogf
	logCount := 0
	zeroWalletWarnLogf = func(string) { logCount++ }
	t.Cleanup(func() { zeroWalletWarnLogf = prevLogf })

	const acct1 int64 = 4001
	before := testutil.ToFloat64(metrics.BillingZeroAmountChargeTotal)
	for i := 0; i < 3; i++ {
		warnZeroWalletAmount(acct1, 1, 2e-6) // totalQuota=1, amountLB=2e-6 < 5e-5 threshold
	}
	after := testutil.ToFloat64(metrics.BillingZeroAmountChargeTotal)
	if got := after - before; got != 3 {
		t.Errorf("counter advanced by %v across 3 calls for the same account, want 3 (counter must be unconditional)", got)
	}
	if logCount != 1 {
		t.Errorf("log line count = %d across 3 calls within the same minute for one account, want 1 (D-A5: throttle must gate the log, not just the metric)", logCount)
	}

	// Per-account, not global: a second, distinct account must log on its
	// OWN first call within the same window — i.e. it is not gated by
	// account 1's throttle.
	const acct2 int64 = 4002
	warnZeroWalletAmount(acct2, 1, 2e-6)
	if logCount != 2 {
		t.Errorf("log line count = %d after a second, distinct account's first call, want 2 (per-account throttle, not global)", logCount)
	}

	// The throttle window must expire, not suppress the account forever:
	// backdate acct1's last-logged timestamp past the 1-minute window and
	// confirm the next call logs again.
	zeroWalletWarnLast.Store(acct1, time.Now().Add(-2*time.Minute))
	warnZeroWalletAmount(acct1, 1, 2e-6)
	if logCount != 3 {
		t.Errorf("log line count = %d after acct1's throttle window elapsed, want 3 (window must expire, not suppress permanently)", logCount)
	}

	seen := map[int64]bool{}
	zeroWalletWarnLast.Range(func(key, _ any) bool {
		seen[key.(int64)] = true
		return true
	})
	if !seen[acct1] || !seen[acct2] {
		t.Errorf("limiter map = %v, want entries for both account %d and %d (per-account, not global)", seen, acct1, acct2)
	}
}
