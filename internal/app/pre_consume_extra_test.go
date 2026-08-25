package app

// pre_consume_extra_test.go — covers the pre-consume deduction + refund paths
// that the trust-skip fixtures in pre_consume_quota_test.go don't reach:
// the actual local pre-deduction (userQuota below the trust threshold, so
// preConsumedQuota survives), and ReturnPreConsumedQuota's refund.

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// TestPreConsumeQuota_UnifiedBillingSkipsPreAuthOnHighBalance drives the
// platformPreAuthorize fast path: with unified billing enabled, a platform
// account, and a high cached wallet balance, ShouldSkipPreAuth returns true so
// no synchronous pre-auth gRPC call is made and PreConsumeQuota proceeds on the
// local quota checks alone.
func TestPreConsumeQuota_UnifiedBillingSkipsPreAuthOnHighBalance(t *testing.T) {
	db := setupServiceTestDB(t)
	repo.InitCol()
	seedPoolTables(t, db)

	// ShouldSkipPreAuth reads the cached balance from Redis, so wire a real one
	// (withRedis flips RedisEnabled back on for this test). The trust switch is
	// off by default — this test is about the fast path itself, so it opts in.
	_ = withRedis(t)
	t.Setenv("BILLING_WALLET_TRUST_SKIP_PREAUTH", "true")

	const accountID int64 = 9911
	prevUnified := common.BillingUnifiedEnabled()
	common.SetBillingUnifiedEnabled(true)
	common.SetCachedWalletBalance(accountID, 1000.0) // >> trust threshold + 3× estimate
	t.Cleanup(func() {
		common.InvalidateCachedWalletBalance(accountID)
		common.SetBillingUnifiedEnabled(prevUnified)
	})

	userId := seedTestUser(t, db, 50_000)
	key, tokenId := seedTestToken(t, db, userId, 50_000, false)

	c := createTestGinContext()
	c.Set("token_quota", 50_000)

	relayInfo := &relaycommon.RelayInfo{
		UserId:            userId,
		TokenId:           tokenId,
		TokenKey:          key,
		IdentityAccountID: accountID,
	}

	if apiErr := PreConsumeQuota(c, 1_000, relayInfo); apiErr != nil {
		t.Fatalf("PreConsumeQuota: %v", apiErr.Error())
	}
	// No pre-auth was created (skip path) => PlatformPreAuthID stays 0.
	if relayInfo.PlatformPreAuthID != 0 {
		t.Errorf("PlatformPreAuthID = %d, want 0 (pre-auth skipped)", relayInfo.PlatformPreAuthID)
	}
	// Local pre-deduction still ran.
	if got := userQuota(t, db, userId); got != 50_000-1_000 {
		t.Errorf("user quota = %d, want %d", got, 50_000-1_000)
	}
}

// TestPreConsumeQuota_ActuallyPreDeducts drives the non-trust path: a user
// whose balance is at/below the trust quota must have the estimate deducted
// from BOTH user quota and token remain quota up front.
func TestPreConsumeQuota_ActuallyPreDeducts(t *testing.T) {
	db := setupServiceTestDB(t)
	repo.InitCol()
	seedPoolTables(t, db) // token defaults tenant_id="default"; pool skip is clean

	// Balance below trust quota => pre-deduction is NOT skipped.
	const userStart = 50_000
	const tokenStart = 50_000
	userId := seedTestUser(t, db, userStart)
	key, tokenId := seedTestToken(t, db, userId, tokenStart, false)

	c := createTestGinContext()
	c.Set("token_quota", tokenStart)

	relayInfo := &relaycommon.RelayInfo{
		UserId:         userId,
		TokenId:        tokenId,
		TokenKey:       key,
		TokenUnlimited: false,
	}

	const estimate = 1_000
	if apiErr := PreConsumeQuota(c, estimate, relayInfo); apiErr != nil {
		t.Fatalf("PreConsumeQuota: %v", apiErr.Error())
	}

	if relayInfo.FinalPreConsumedQuota != estimate {
		t.Errorf("FinalPreConsumedQuota = %d, want %d (deducted, not trust-skipped)",
			relayInfo.FinalPreConsumedQuota, estimate)
	}
	if got := userQuota(t, db, userId); got != userStart-estimate {
		t.Errorf("user quota = %d, want %d", got, userStart-estimate)
	}
	if got := tokenRemain(t, db, tokenId); got != tokenStart-estimate {
		t.Errorf("token remain = %d, want %d", got, tokenStart-estimate)
	}
}

// TestPreConsumeQuota_TokenInsufficientReleasesAndErrors proves the token-quota
// rejection branch: user has enough but the token doesn't, so the pre-consume
// fails and no user quota is left deducted (PreConsumeTokenQuota runs before the
// user decrement).
func TestPreConsumeQuota_TokenInsufficientReleasesAndErrors(t *testing.T) {
	db := setupServiceTestDB(t)
	repo.InitCol()
	seedPoolTables(t, db)

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
	// User quota must be untouched (token check fails first).
	if got := userQuota(t, db, userId); got != 50_000 {
		t.Errorf("user quota = %d, want 50000 (untouched on token failure)", got)
	}
	if got := tokenRemain(t, db, tokenId); got != 100 {
		t.Errorf("token remain = %d, want 100 (untouched)", got)
	}
}

// TestReturnPreConsumedQuota_RefundsLocalQuota proves the refund path restores
// both user and token quota by the pre-consumed amount.
func TestReturnPreConsumedQuota_RefundsLocalQuota(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)

	userId := seedTestUser(t, db, 9_000) // already had 1000 deducted at pre-consume
	key, tokenId := seedTestToken(t, db, userId, 9_000, false)

	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{
		UserId:                userId,
		TokenId:               tokenId,
		TokenKey:              key,
		FinalPreConsumedQuota: 1_000,
		PlatformPreAuthID:     0, // no platform pre-auth => release is a no-op
	}

	ReturnPreConsumedQuota(c, relayInfo)

	if got := userQuota(t, db, userId); got != 9_000+1_000 {
		t.Errorf("user quota after refund = %d, want %d", got, 9_000+1_000)
	}
	if got := tokenRemain(t, db, tokenId); got != 9_000+1_000 {
		t.Errorf("token remain after refund = %d, want %d", got, 9_000+1_000)
	}
}

// TestPreConsumeQuota_TenantQuotaExceededRejects drives the tenant-monthly-quota
// enforcement branch of PreConsumeQuota: the per-user check passes, but the
// tenant is over its monthly cap, so PreConsumeQuota returns a 402 and does not
// pre-deduct the user quota.
func TestPreConsumeQuota_TenantQuotaExceededRejects(t *testing.T) {
	db := setupServiceTestDB(t)
	repo.InitCol()

	tenantID := seedTestTenant(t, 1_000_000)
	seedTenantLog(t, tenantID, 950_000) // near the cap this month

	prevEnabled := tenantQuotaEnforcementEnabled
	tenantQuotaEnforcementEnabled = true
	t.Cleanup(func() { tenantQuotaEnforcementEnabled = prevEnabled })

	const start = 5_000_000
	userId := seedTestUser(t, db, start)
	key, tokenId := seedTestToken(t, db, userId, start, false)

	c := createTestGinContext()
	c.Set("tenant_id", tenantID)

	relayInfo := &relaycommon.RelayInfo{
		UserId:   userId,
		TokenId:  tokenId,
		TokenKey: key,
	}

	// 950_000 + 200_000 > 1_000_000 => tenant quota denies the request.
	apiErr := PreConsumeQuota(c, 200_000, relayInfo)
	if apiErr == nil {
		t.Fatal("expected tenant-quota-exceeded rejection")
	}
	// User quota must not have been pre-deducted on the tenant rejection.
	if got := userQuota(t, db, userId); got != start {
		t.Errorf("user quota = %d, want %d (untouched on tenant reject)", got, start)
	}
}

// TestPreConsumeQuota_UserQuotaLookupError exercises the GetUserQuota error
// branch: a non-existent user id causes the local quota lookup to fail and
// PreConsumeQuota returns a query-error without pre-deducting anything.
func TestPreConsumeQuota_UserQuotaLookupError(t *testing.T) {
	_ = setupServiceTestDB(t)
	repo.InitCol()

	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{
		UserId:         987654321, // no such user
		TokenUnlimited: true,
	}
	if apiErr := PreConsumeQuota(c, 100, relayInfo); apiErr == nil {
		t.Fatal("expected error for non-existent user quota lookup")
	}
}

// TestReleasePlatformPreAuth_FailoverEnqueuesOutbox drives releasePlatformPreAuth
// with a live pre-auth id and no reachable billing backend: ReleaseWithBreaker
// fails, so the release is enqueued to the outbox rather than dropped.
func TestReleasePlatformPreAuth_FailoverEnqueuesOutbox(t *testing.T) {
	db := setupServiceTestDB(t)
	restore := setupOutbox(t, db)
	defer restore()

	const preAuthID int64 = 424242
	relayInfo := &relaycommon.RelayInfo{
		IdentityAccountID: 7,
		PlatformPreAuthID: preAuthID,
	}
	// Refund with FinalPreConsumedQuota=0 so only the release path runs.
	c := createTestGinContext()
	ReturnPreConsumedQuota(c, relayInfo)

	var count int64
	if err := db.Model(&entity.BillingOutbox{}).Where("pre_auth_id = ? AND action = ?", preAuthID, "release").Count(&count).Error; err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if count == 0 {
		t.Error("expected a release entry enqueued to the outbox after failed release")
	}
}

// TestReturnPreConsumedQuota_ZeroPreConsumeNoop proves the no-refund branch: a
// request that never pre-consumed anything leaves quota unchanged.
func TestReturnPreConsumedQuota_ZeroPreConsumeNoop(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 5_000)

	c := createTestGinContext()
	relayInfo := &relaycommon.RelayInfo{
		UserId:                userId,
		FinalPreConsumedQuota: 0,
		PlatformPreAuthID:     0,
	}

	ReturnPreConsumedQuota(c, relayInfo)

	if got := userQuota(t, db, userId); got != 5_000 {
		t.Errorf("user quota = %d, want 5000 (no refund on zero pre-consume)", got)
	}
}
