package app

// l1_quota_claude_round_test.go — D1 money-path regression coverage for the
// Claude-native settlement path (PostClaudeConsumeQuota, quota.go). Before
// this lane's fix, quota.go:368 truncated `calculateQuota` with a bare
// `int(...)` cast (floor, not round) and had no post-hoc floor for the
// non-UsePrice branch, unlike the OpenAI-compatible sibling path
// (compatible_handler.go:392 uses decimal .Round(0), and :406-408 floors
// sub-unit-but-nonzero results to 1). Two concrete production symptoms this
// locks in:
//   - same token cost computed to a *.5 quota rounded DOWN instead of to the
//     nearest unit (half-up), undercharging every such call by exactly 1
//     quota unit versus the compatible-handler sibling formula;
//   - a 0 < calc < 1 result (e.g. a tiny max_tokens=1 call) truncated to 0,
//     so the caller gets a real upstream response for zero recorded quota —
//     the exact "immortally free" defect reproduced live on
//     /v1/messages with a 10/2-token call.
//
// Both hand-computed expectations below use the SAME formula as
// TestPostClaudeConsumeQuota_Arithmetic in quota_consume_test.go:
//
//	calculateQuota = (promptTokens + cacheTokens*cacheRatio
//	                   + completionTokens*completionRatio) * groupRatio * modelRatio

import (
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestR1ClaudeQuota_SubHalfUnitFloorsToOne — A1 (R1 lane). Adversarial round
// found the modelRatio!=0 && quota==0 floor (quota.go, currently :407-409)
// UNCOVERED:
// deleting it left internal/app and internal/app/relay fully green. The
// existing TestL1ClaudeQuota_SubUnitStillCharges above only exercises
// calc=0.6 (rounds to 1 on its own via Round(0), so it does NOT need the
// floor to pass). This test picks calc strictly below the 0.5 rounding
// boundary so the floor is the ONLY thing standing between a real answer and
// a free call: modelRatio=0.02, groupRatio=1, completionRatio=1, prompt=10,
// completion=2 -> calc = (10+2)*1*0.02 = 0.24 -> Round(0) = 0 without the
// floor, 1 with it.
func TestR1ClaudeQuota_SubHalfUnitFloorsToOne(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 1_000_000)
	key, tokenId := seedTestToken(t, db, userId, 1_000_000, false)

	c := createTestGinContext()
	c.Set("token_name", "tkn")

	usage := &dto.Usage{
		PromptTokens:     10,
		CompletionTokens: 2,
		TotalTokens:      12,
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: "claude-3-5-sonnet",
		StartTime:       time.Now().Add(-time.Second),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio:      0.02,
			CompletionRatio: 1.0,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	const wantQuota = 1 // calc=0.24 rounds to 0; the post-hoc floor must lift it to 1

	before := userQuota(t, db, userId)
	PostClaudeConsumeQuota(c, relayInfo, usage)
	after := userQuota(t, db, userId)

	if before-after != wantQuota {
		t.Errorf("Claude debited %d, want %d (sub-half-unit calc=0.24 must still floor to 1)", before-after, wantQuota)
	}

	var logRow repo.Log
	if err := db.Where("user_id = ? AND type = ?", userId, repo.LogTypeConsume).First(&logRow).Error; err != nil {
		t.Fatalf("no consume log written: %v", err)
	}
	if logRow.Quota != wantQuota {
		t.Errorf("consume log quota = %d, want %d", logRow.Quota, wantQuota)
	}
}

// TestR1WarnZeroWalletAmount_WiredIntoSettlement — A3 (R1 lane). Adversarial
// round found warnZeroWalletAmount's only production call site
// (quota.go:902, inside PostConsumeQuota) UNWIRED-untested: replacing the
// call with a no-op left `go test -short ./internal/app/` fully green. This
// drives the REAL call site (PostConsumeQuota, the legacy fire-and-forget
// debit arm — PlatformPreAuthID==0) instead of calling warnZeroWalletAmount
// directly, so it dies if quota.go:902 is ever unwired again.
func TestR1WarnZeroWalletAmount_WiredIntoSettlement(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 1_000_000)
	key, tokenId := seedTestToken(t, db, userId, 1_000_000, false)

	prevAsync := AsyncGo
	AsyncGo = func(f func()) { f() }
	t.Cleanup(func() { AsyncGo = prevAsync })

	resetZeroWalletWarnThrottle()
	t.Cleanup(resetZeroWalletWarnThrottle)

	relayInfo := &relaycommon.RelayInfo{
		UserId:            userId,
		TokenId:           tokenId,
		TokenKey:          key,
		IdentityAccountID: 9001, // >0 => enters the platform-wallet branch (quota.go:899)
		// PlatformPreAuthID left at 0 => legacy fire-and-forget debit arm.
	}

	// totalQuota = quota(1) + preConsumedQuota(0) = 1.
	// amountLB = 1 / common.QuotaPerUnit(500000) = 2e-6, well under the
	// 0.00005 LB threshold => warnZeroWalletAmount must fire and increment
	// the counter.
	before := testutil.ToFloat64(metrics.BillingZeroAmountChargeTotal)
	if err := PostConsumeQuota(relayInfo, 1, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}
	after := testutil.ToFloat64(metrics.BillingZeroAmountChargeTotal)

	if after <= before {
		t.Errorf("BillingZeroAmountChargeTotal did not increment (before=%v after=%v) — warnZeroWalletAmount call site at quota.go:902 may be unwired", before, after)
	}
}

// TestL1ClaudeQuota_RoundsHalfUp: prompt=1, completion=1, completionRatio=1,
// groupRatio=1, modelRatio=0.75 -> calc = (1 + 1*1) * 1 * 0.75 = 1.5.
// Half-up rounding must debit 2 quota units. The pre-fix bare `int()` cast
// floors 1.5 to 1 — a 1-unit undercharge on every *.5 call.
func TestL1ClaudeQuota_RoundsHalfUp(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 1_000_000)
	key, tokenId := seedTestToken(t, db, userId, 1_000_000, false)

	c := createTestGinContext()
	c.Set("token_name", "tkn")

	usage := &dto.Usage{
		PromptTokens:     1,
		CompletionTokens: 1,
		TotalTokens:      2,
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: "claude-3-5-sonnet",
		StartTime:       time.Now().Add(-time.Second),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio:      0.75,
			CompletionRatio: 1.0,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	const wantQuota = 2 // round-half-up(1.5), NOT floor(1.5) == 1

	before := userQuota(t, db, userId)
	PostClaudeConsumeQuota(c, relayInfo, usage)
	after := userQuota(t, db, userId)

	if before-after != wantQuota {
		t.Errorf("Claude debited %d, want %d (half-up round of 1.5)", before-after, wantQuota)
	}

	var logRow repo.Log
	if err := db.Where("user_id = ? AND type = ?", userId, repo.LogTypeConsume).First(&logRow).Error; err != nil {
		t.Fatalf("no consume log written: %v", err)
	}
	if logRow.Quota != wantQuota {
		t.Errorf("consume log quota = %d, want %d", logRow.Quota, wantQuota)
	}
}

// TestL1ClaudeQuota_SubUnitStillCharges: prompt=10, completion=2,
// completionRatio=1, groupRatio=1, modelRatio=0.05 -> calc = (10 + 2*1) * 1 *
// 0.05 = 0.6. A nonzero modelRatio with a nonzero calc must never settle to a
// free (quota==0) call. This reproduces the live max_tokens=1 /v1/messages
// call that returned a real upstream response for quota=0.
func TestL1ClaudeQuota_SubUnitStillCharges(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 1_000_000)
	key, tokenId := seedTestToken(t, db, userId, 1_000_000, false)

	c := createTestGinContext()
	c.Set("token_name", "tkn")

	usage := &dto.Usage{
		PromptTokens:     10,
		CompletionTokens: 2,
		TotalTokens:      12,
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: "claude-3-5-sonnet",
		StartTime:       time.Now().Add(-time.Second),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio:      0.05,
			CompletionRatio: 1.0,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	const wantQuota = 1 // sub-unit-but-nonzero (0.6) must floor to 1, not 0

	before := userQuota(t, db, userId)
	PostClaudeConsumeQuota(c, relayInfo, usage)
	after := userQuota(t, db, userId)

	if before-after != wantQuota {
		t.Errorf("Claude debited %d, want %d (sub-unit call must still charge 1)", before-after, wantQuota)
	}

	var logRow repo.Log
	if err := db.Where("user_id = ? AND type = ?", userId, repo.LogTypeConsume).First(&logRow).Error; err != nil {
		t.Fatalf("no consume log written: %v", err)
	}
	if logRow.Quota != wantQuota {
		t.Errorf("consume log quota = %d, want %d", logRow.Quota, wantQuota)
	}
}

// TestL1WarnZeroWalletAmount — X1's leak DETECTOR (quota.go's
// warnZeroWalletAmount, called right before the platform-wallet settle/debit
// call). A strictly-positive local quota that converts to < 0.00005 LB will
// round to 0.0000 under the platform wallet's numeric(14,4) column: the local
// ledger recorded real usage but the wallet-side charge is silently lost.
// This does NOT fix the wallet truncation (out of scope, see X1) — it only
// makes the leak observable via metrics.BillingZeroAmountChargeTotal.
func TestL1WarnZeroWalletAmount(t *testing.T) {
	cases := []struct {
		name          string
		totalQuota    int
		amountLB      float64
		wantIncrement bool
	}{
		{"positive-quota-sub-threshold-amount-warns", 1, 0.000002, true},
		{"zero-quota-never-warns-even-if-amount-looks-small", 0, 0.001, false},
		{"amount-at-or-above-threshold-does-not-warn", 30, 0.00006, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := testutil.ToFloat64(metrics.BillingZeroAmountChargeTotal)
			warnZeroWalletAmount(1, tc.totalQuota, tc.amountLB)
			after := testutil.ToFloat64(metrics.BillingZeroAmountChargeTotal)

			gotIncrement := after > before
			if gotIncrement != tc.wantIncrement {
				t.Errorf("warnZeroWalletAmount(1, %d, %v): counter incremented=%v, want %v",
					tc.totalQuota, tc.amountLB, gotIncrement, tc.wantIncrement)
			}
		})
	}
}
