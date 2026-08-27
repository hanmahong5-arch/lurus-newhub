package repo

// cov_repo-deep_redemption_redeem_test.go — business-acceptance coverage for
// redemption.go Redeem(), the money-crediting path for gift/promo codes.
// Redeem() is the one function in this file with a real concurrency hazard
// (SELECT ... FOR UPDATE row lock) and a real cross-tenant boundary (only
// "default"-tenant codes are redeemable across tenants), so this file focuses
// on exactly those two properties plus the ordinary validation branches.

import (
	"strings"
	"sync"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// repoDeepFitRedemptionKey clamps a seed key to the width the schema actually
// allows. `Redemption.Key` is `char(32)`; a longer value is silently accepted by
// the SQLite tier but rejected by PostgreSQL with SQLSTATE 22001, so seeds built
// as `"<label>-" + GetUUID()` (8-14 chars of label plus 32 hex) must be trimmed.
// Truncating keeps the label prefix readable in failure output while leaving
// enough of the UUID tail for uniqueness within a test database.
func repoDeepFitRedemptionKey(key string) string {
	if len(key) > 32 {
		return key[:32]
	}
	return key
}

func repoDeepSeedRedemptionCode(t *testing.T, tenantID, key string, quota int, status int, expiredTime int64) *Redemption {
	t.Helper()
	key = repoDeepFitRedemptionKey(key)
	r := &Redemption{
		TenantId:    tenantID,
		Key:         key,
		Status:      status,
		Name:        "deep-" + key,
		Quota:       quota,
		CreatedTime: common.GetTimestamp(),
		ExpiredTime: expiredTime,
	}
	if err := DB.Create(r).Error; err != nil {
		t.Fatalf("seed redemption code: %v", err)
	}
	return r
}

func repoDeepSeedRedeemUser(t *testing.T, tenantID string, quota int) *User {
	t.Helper()
	u := &User{
		Username: "deep-redeem-" + common.GetUUID(), DisplayName: "d", Email: common.GetUUID() + "@test.local",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		TenantId: tenantID, Group: "default", Quota: quota,
	}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("seed redeem user: %v", err)
	}
	return u
}

func TestRedeem_ValidationErrors(t *testing.T) {
	SetupTestDB(t)
	u := repoDeepSeedRedeemUser(t, "default", 0)

	if _, err := Redeem("", u.Id); err == nil || !strings.Contains(err.Error(), "未提供兑换码") {
		t.Fatalf("empty key must be rejected before any DB access, got %v", err)
	}
	if _, err := Redeem("some-key", 0); err == nil || !strings.Contains(err.Error(), "无效的 user id") {
		t.Fatalf("zero userId must be rejected, got %v", err)
	}
}

func TestRedeem_UnknownCodeRejected(t *testing.T) {
	SetupTestDB(t)
	u := repoDeepSeedRedeemUser(t, "default", 0)

	if _, err := Redeem("does-not-exist", u.Id); err == nil {
		t.Fatal("unknown code must be rejected")
	}
	var reloaded User
	DB.First(&reloaded, u.Id)
	if reloaded.Quota != 0 {
		t.Fatalf("unknown-code redeem must not touch quota, got %d", reloaded.Quota)
	}
}

func TestRedeem_UnknownUserRejected(t *testing.T) {
	SetupTestDB(t)
	code := repoDeepSeedRedemptionCode(t, "default", "code-no-user", 500, common.RedemptionCodeStatusEnabled, 0)

	if _, err := Redeem(code.Key, 999999); err == nil {
		t.Fatal("redeem by a non-existent user id must be rejected")
	}
	var reloaded Redemption
	DB.First(&reloaded, code.Id)
	if reloaded.Status != common.RedemptionCodeStatusEnabled {
		t.Fatalf("code must stay enabled when the crediting user doesn't exist, got status=%d", reloaded.Status)
	}
}

func TestRedeem_SuccessCreditsQuotaAndFlipsCode(t *testing.T) {
	SetupTestDB(t)
	u := repoDeepSeedRedeemUser(t, "default", 1000)
	code := repoDeepSeedRedemptionCode(t, "default", "code-ok-"+common.GetUUID(), 750, common.RedemptionCodeStatusEnabled, 0)

	gotQuota, err := Redeem(code.Key, u.Id)
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if gotQuota != 750 {
		t.Fatalf("Redeem must return the code's face quota, got %d", gotQuota)
	}

	var reloadedUser User
	DB.First(&reloadedUser, u.Id)
	if reloadedUser.Quota != 1750 {
		t.Fatalf("user quota must be credited by exactly the code's quota: want 1750, got %d", reloadedUser.Quota)
	}

	var reloadedCode Redemption
	DB.First(&reloadedCode, code.Id)
	if reloadedCode.Status != common.RedemptionCodeStatusUsed {
		t.Fatalf("code must flip to Used, got status=%d", reloadedCode.Status)
	}
	if reloadedCode.UsedUserId != u.Id {
		t.Fatalf("code must record the redeeming user, got used_user_id=%d want %d", reloadedCode.UsedUserId, u.Id)
	}
	if reloadedCode.RedeemedTime == 0 {
		t.Fatal("code must stamp redeemed_time on success")
	}
}

func TestRedeem_AlreadyUsedCodeCannotBeRedeemedTwice(t *testing.T) {
	SetupTestDB(t)
	u1 := repoDeepSeedRedeemUser(t, "default", 0)
	u2 := repoDeepSeedRedeemUser(t, "default", 0)
	code := repoDeepSeedRedemptionCode(t, "default", "code-once-"+common.GetUUID(), 200, common.RedemptionCodeStatusEnabled, 0)

	if _, err := Redeem(code.Key, u1.Id); err != nil {
		t.Fatalf("first redeem must succeed: %v", err)
	}
	// A second redeem attempt (even by a different user) must be rejected —
	// the code is now Used, not Enabled.
	if _, err := Redeem(code.Key, u2.Id); err == nil || !strings.Contains(err.Error(), "已被使用") {
		t.Fatalf("second redeem of an already-used code must be rejected, got %v", err)
	}
	var reloadedU2 User
	DB.First(&reloadedU2, u2.Id)
	if reloadedU2.Quota != 0 {
		t.Fatalf("the rejected second redeemer must not be credited, got quota=%d", reloadedU2.Quota)
	}
}

func TestRedeem_DisabledCodeRejected(t *testing.T) {
	SetupTestDB(t)
	u := repoDeepSeedRedeemUser(t, "default", 0)
	code := repoDeepSeedRedemptionCode(t, "default", "code-disabled-"+common.GetUUID(), 200, common.RedemptionCodeStatusDisabled, 0)

	if _, err := Redeem(code.Key, u.Id); err == nil {
		t.Fatal("a disabled (non-Enabled status) code must be rejected")
	}
}

func TestRedeem_ExpiredCodeRejected(t *testing.T) {
	SetupTestDB(t)
	u := repoDeepSeedRedeemUser(t, "default", 0)
	past := common.GetTimestamp() - 3600
	code := repoDeepSeedRedemptionCode(t, "default", "code-expired-"+common.GetUUID(), 200, common.RedemptionCodeStatusEnabled, past)

	if _, err := Redeem(code.Key, u.Id); err == nil || !strings.Contains(err.Error(), "已过期") {
		t.Fatalf("an expired code must be rejected with an expiry error, got %v", err)
	}
	var reloadedU User
	DB.First(&reloadedU, u.Id)
	if reloadedU.Quota != 0 {
		t.Fatalf("expired-code redeem must not credit quota, got %d", reloadedU.Quota)
	}
}

// ExpiredTime == 0 means "never expires" — must NOT be treated as expired
// (the zero value must not collide with the sentinel).
func TestRedeem_ZeroExpiredTimeMeansNeverExpires(t *testing.T) {
	SetupTestDB(t)
	u := repoDeepSeedRedeemUser(t, "default", 0)
	code := repoDeepSeedRedemptionCode(t, "default", "code-noexpiry-"+common.GetUUID(), 300, common.RedemptionCodeStatusEnabled, 0)

	if _, err := Redeem(code.Key, u.Id); err != nil {
		t.Fatalf("expired_time=0 must mean never-expires, got error: %v", err)
	}
}

func TestRedeem_TenantMismatchRejected(t *testing.T) {
	SetupTestDB(t)
	// A code minted for a specific named tenant ("tenant-a") must not be
	// redeemable by a user belonging to a different named tenant.
	u := repoDeepSeedRedeemUser(t, "tenant-b", 0)
	code := repoDeepSeedRedemptionCode(t, "tenant-a", "code-mismatch-"+common.GetUUID(), 200, common.RedemptionCodeStatusEnabled, 0)

	_, err := Redeem(code.Key, u.Id)
	if err == nil || !strings.Contains(err.Error(), "不属于当前租户") {
		t.Fatalf("cross-tenant redeem must be rejected, got %v", err)
	}
	var reloadedU User
	DB.First(&reloadedU, u.Id)
	if reloadedU.Quota != 0 {
		t.Fatalf("rejected cross-tenant redeem must not credit quota, got %d", reloadedU.Quota)
	}
}

// G5a: a code minted under the "default" tenant used to be treated as a
// backward-compatible v1 global code — redeemable by ANY tenant's user
// regardless of a TenantId mismatch. Because "default" is also the
// platform's own tenant (and the column's GORM default for any code
// inserted without an explicit tenant), that wildcard let a cross-tenant
// user redeem a code that was never minted for them: a genuine
// authorization bypass, not a compatibility shim. This test used to assert
// the bypass succeeded; it now pins the fixed, tenant-scoped rejection.
func TestRedeem_DefaultTenantCodeRejectedForOtherTenant(t *testing.T) {
	SetupTestDB(t)
	u := repoDeepSeedRedeemUser(t, "some-other-tenant", 0)
	code := repoDeepSeedRedemptionCode(t, "default", "code-global-"+common.GetUUID(), 400, common.RedemptionCodeStatusEnabled, 0)

	_, err := Redeem(code.Key, u.Id)
	if err == nil || !strings.Contains(err.Error(), "不属于当前租户") {
		t.Fatalf("a default-tenant code must NOT be redeemable by a different tenant's user, got %v", err)
	}
	var reloadedU User
	DB.First(&reloadedU, u.Id)
	if reloadedU.Quota != 0 {
		t.Fatalf("rejected cross-tenant redeem must not credit quota, got %d", reloadedU.Quota)
	}
	var reloadedCode Redemption
	DB.First(&reloadedCode, code.Id)
	if reloadedCode.Status != common.RedemptionCodeStatusEnabled {
		t.Fatalf("an unusable (rejected) code must not be burned — expected it to stay Enabled, got status=%d", reloadedCode.Status)
	}
}

// TestRedeem_ConcurrentSameCodeOnlyOneWinner is the money-path concurrency
// property: Redeem() takes the row via SELECT ... FOR UPDATE inside a
// transaction. N goroutines racing to redeem the SAME code must yield exactly
// one winner (credited once) — the row lock, not a Go-level mutex, is what
// prevents double-crediting.
func TestRedeem_ConcurrentSameCodeOnlyOneWinner(t *testing.T) {
	SetupTestDB(t)
	const n = 8
	code := repoDeepSeedRedemptionCode(t, "default", "code-race-"+common.GetUUID(), 1000, common.RedemptionCodeStatusEnabled, 0)

	users := make([]*User, n)
	for i := 0; i < n; i++ {
		users[i] = repoDeepSeedRedeemUser(t, "default", 0)
	}

	var wg sync.WaitGroup
	successes := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := Redeem(code.Key, users[idx].Id)
			successes[idx] = err == nil
		}(i)
	}
	wg.Wait()

	winners := 0
	var winnerUserId int
	for i, ok := range successes {
		if ok {
			winners++
			winnerUserId = users[i].Id
		}
	}
	if winners != 1 {
		t.Fatalf("exactly one goroutine must win the race on a single-use code, got %d winners", winners)
	}

	// Total quota credited across ALL n users must equal exactly one code
	// redemption (1000), not n*1000 — the definitive "final consistency"
	// check against double-crediting.
	var totalCredited int64
	for _, u := range users {
		var reloaded User
		DB.First(&reloaded, u.Id)
		totalCredited += int64(reloaded.Quota)
	}
	if totalCredited != 1000 {
		t.Fatalf("total credited quota across the race must be exactly 1000 (one redemption), got %d", totalCredited)
	}

	var reloadedCode Redemption
	DB.First(&reloadedCode, code.Id)
	if reloadedCode.Status != common.RedemptionCodeStatusUsed {
		t.Fatalf("code must end Used after the race, got status=%d", reloadedCode.Status)
	}
	if reloadedCode.UsedUserId != winnerUserId {
		t.Fatalf("used_user_id must match the actual winner: want %d got %d", winnerUserId, reloadedCode.UsedUserId)
	}
}
