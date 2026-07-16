package app

// post_consume_actual_cost_test.go — conservation guards for two post-consume
// settlement defects (money path):
//
//	#3 The tenant credit pool is NEVER debited during pre-consume, so Phase 2.5
//	   must debit the ACTUAL COST (totalQuota = quota + preConsumedQuota), not
//	   just the post-consume delta. The old code debited `quota` (the delta),
//	   under-charging every pooled request by the pre-consumed amount, and
//	   dropping the pool debit entirely when the delta was <= 0.
//
//	#4 When the estimate is exact (quotaDelta == 0, e.g. fixed-price models) the
//	   three relay completion sites used to skip PostConsumeQuota entirely via an
//	   `if quotaDelta != 0` guard. That skipped BOTH the pool debit AND the
//	   platform pre-auth settlement — the 300s pre-auth TTL then expired and the
//	   revenue was released instead of captured. The guard is now removed so the
//	   settlement always runs; PostConsumeQuota itself already handles quota == 0
//	   (the +0 quota writes are no-ops; pool + settle key on totalQuota).
//
// Every assertion asserts the exact conserved amount (actual cost), not merely
// non-zero — a wrong pool/settle amount is a revenue leak.

import (
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// TestPostConsumeQuota_PoolDebitsActualCostNotDelta pins defect #3: the pool
// debit must equal the request's ACTUAL COST (delta + preConsumed), for every
// sign of the delta — positive (post-consume top-up), negative (over-estimate
// refund), and zero (exact estimate). Before the fix the pool moved by `quota`
// (the delta) only, so all three sub-cases mis-charge.
func TestPostConsumeQuota_PoolDebitsActualCostNotDelta(t *testing.T) {
	const startBal = 1_000_000

	cases := []struct {
		name  string
		delta int
		pre   int
	}{
		{"delta_positive", 300, 200}, // actual cost 500
		{"delta_negative", -150, 400}, // actual cost 250 (old code debited 0: quota<=0)
		{"delta_zero", 0, 350},        // actual cost 350 (old code debited 0: quota==0)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := setupServiceTestDB(t)
			seedPoolTables(t, db)

			userId := seedTestUser(t, db, 5_000_000)
			key, tokenId := seedTestToken(t, db, userId, 5_000_000, false)
			tenantID := "t-actual-" + tc.name
			if err := db.Table("tokens").Where("id = ?", tokenId).Update("tenant_id", tenantID).Error; err != nil {
				t.Fatalf("bind tenant: %v", err)
			}

			pool, err := repo.CreateTenantCreditPool(tenantID, 1, 10_000_000, repo.PoolResetMonthly, 80)
			if err != nil {
				t.Fatalf("create pool: %v", err)
			}
			if _, err := repo.TopupPool(pool.ID, tenantID, startBal, 1, "seed"); err != nil {
				t.Fatalf("topup: %v", err)
			}

			relayInfo := &relaycommon.RelayInfo{
				UserId:   userId,
				TokenId:  tokenId,
				TokenKey: key,
			}
			if err := PostConsumeQuota(relayInfo, tc.delta, tc.pre, false); err != nil {
				t.Fatalf("PostConsumeQuota: %v", err)
			}

			actualCost := tc.delta + tc.pre
			wantBal := int64(startBal - actualCost)

			got, err := repo.GetTenantCreditPool(tenantID)
			if err != nil {
				t.Fatalf("readback pool: %v", err)
			}
			if got.CurrentBalance != wantBal {
				t.Errorf("pool balance = %d, want %d (start %d - actual cost %d = delta %d + preConsumed %d); a delta-only debit would leave %d",
					got.CurrentBalance, wantBal, startBal, actualCost, tc.delta, tc.pre, int64(startBal-tc.delta))
			}
		})
	}
}

// TestPostClaudeConsumeQuota_ZeroDeltaStillDebitsPoolAndSettles pins defect #4
// at the real relay completion site: when the estimate is exact (FinalPreConsumedQuota
// == computed quota => quotaDelta == 0), PostClaudeConsumeQuota must STILL invoke
// PostConsumeQuota so that (a) the tenant pool is debited the full actual cost and
// (b) the platform pre-auth is settled (PlatformPreAuthID cleared, settle durably
// enqueued when the backend is unreachable). Before removing the `quotaDelta != 0`
// guard the call was skipped: pool untouched + pre-auth left to expire.
//
// The gRPC settle itself is exercised with the billing breaker tripped OPEN so
// SettleWithBreaker fast-fails into the durable outbox — the same seam the other
// settle tests use. A live successful gRPC settle is not hermetically covered
// (no platform backend); what is proven here is that the settle BRANCH runs at
// quotaDelta == 0 and the charge is durably captured rather than dropped.
func TestPostClaudeConsumeQuota_ZeroDeltaStillDebitsPoolAndSettles(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	restore := setupOutbox(t, db)
	defer restore()

	// Trip the breaker OPEN so SettleWithBreaker fast-fails (no 5s network wait)
	// and the settle is durably enqueued instead.
	common.BillingBreakerFailure()
	common.BillingBreakerFailure()
	common.BillingBreakerFailure()
	if !common.BillingBreakerIsOpen() {
		t.Fatal("precondition: breaker should be OPEN")
	}
	t.Cleanup(common.BillingBreakerSuccess)

	userId := seedTestUser(t, db, 5_000_000)
	key, tokenId := seedTestToken(t, db, userId, 5_000_000, false)
	tenantID := "t-zero-delta"
	if err := db.Table("tokens").Where("id = ?", tokenId).Update("tenant_id", tenantID).Error; err != nil {
		t.Fatalf("bind tenant: %v", err)
	}

	const startBal = 1_000_000
	pool, err := repo.CreateTenantCreditPool(tenantID, 1, 10_000_000, repo.PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if _, err := repo.TopupPool(pool.ID, tenantID, startBal, 1, "seed"); err != nil {
		t.Fatalf("topup: %v", err)
	}

	c := createTestGinContext()
	c.Set("token_name", "tkn")

	// Same fixture as TestPostClaudeConsumeQuota_Arithmetic:
	// quota = (1000 + 200*0.5 + 500*5) * 2 * 3 = 3600 * 6 = 21600.
	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 200,
		},
	}
	const actualCost = 21600

	const preAuthID int64 = 777_000
	relayInfo := &relaycommon.RelayInfo{
		UserId:                userId,
		TokenId:               tokenId,
		TokenKey:              key,
		OriginModelName:       "claude-3-5-sonnet",
		StartTime:             time.Now().Add(-2 * time.Second),
		ChannelMeta:           &relaycommon.ChannelMeta{},
		IdentityAccountID:     42,        // platform account => settlement path
		PlatformPreAuthID:     preAuthID, // pre-auth exists => settle (not legacy debit)
		FinalPreConsumedQuota: actualCost, // makes quotaDelta == quota - pre == 0
		PriceData: types.PriceData{
			ModelRatio:      3.0,
			CompletionRatio: 5.0,
			CacheRatio:      0.5,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 2.0},
		},
	}

	PostClaudeConsumeQuota(c, relayInfo, usage)

	// (a) Pool debited the FULL actual cost even though the delta was zero.
	got, err := repo.GetTenantCreditPool(tenantID)
	if err != nil {
		t.Fatalf("readback pool: %v", err)
	}
	if got.CurrentBalance != int64(startBal-actualCost) {
		t.Errorf("pool balance = %d, want %d (quotaDelta==0 must still debit actual cost %d); a skipped/zero debit leaves %d",
			got.CurrentBalance, int64(startBal-actualCost), actualCost, int64(startBal))
	}

	// (b) Settle branch ran: pre-auth marked handled (cleared)...
	if relayInfo.PlatformPreAuthID != 0 {
		t.Errorf("PlatformPreAuthID = %d, want 0 (settle must run at quotaDelta==0)", relayInfo.PlatformPreAuthID)
	}
	// ...and the charge was durably captured to the outbox (breaker OPEN).
	var row entity.BillingOutbox
	if err := db.Where("pre_auth_id = ? AND action = ?", preAuthID, outboxActionSettle).First(&row).Error; err != nil {
		t.Fatalf("no settle outbox row for quotaDelta==0 request (settle was skipped): %v", err)
	}

	// Drain the async report goroutine (network-only) before teardown.
	time.Sleep(50 * time.Millisecond)
}
