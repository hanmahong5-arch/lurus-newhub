package repo

// redemption_quota_cache_test.go — Redeem writes the new quota straight to the
// user row inside its transaction. Every other top-up path goes through
// IncreaseUserQuota, which also increments the cached copy; Redeem did not, so
// GetUserQuota(id, false) kept serving the pre-topup balance until the cache
// key expired. The user redeemed a code and was still refused for insufficient
// quota. This reads the value back out of a real in-process Redis rather than
// trusting a nil error.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// redeemCacheMiniRedis points common.RDB at an in-process Redis for the test and
// restores every global it touches afterwards.
func redeemCacheMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prevRDB, prevEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() {
		common.RDB, common.RedisEnabled = prevRDB, prevEnabled
		_ = client.Close()
		mr.Close()
	})
	return mr
}

func TestRedeem_KeepsCachedQuotaInStepWithTheRow(t *testing.T) {
	setupSQLiteDB(t)
	redeemCacheMiniRedis(t)

	user := &User{
		Username: "redeemer",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		TenantId: "default",
		Group:    "default",
		Quota:    100,
	}
	if err := DB.Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	const code = "redeem-cache-lock-001"
	rc := &Redemption{
		UserId:      user.Id,
		Key:         code,
		Status:      common.RedemptionCodeStatusEnabled,
		Name:        "cache lock",
		Quota:       500,
		TenantId:    "default",
		CreatedTime: common.GetTimestamp(),
	}
	if err := DB.Create(rc).Error; err != nil {
		t.Fatalf("seed redemption: %v", err)
	}

	// Warm the cache the way a normal request would, so the stale entry exists.
	if err := updateUserCache(*user); err != nil {
		t.Fatalf("warm user cache: %v", err)
	}
	if cached, err := getUserQuotaCache(user.Id); err != nil || cached != 100 {
		t.Fatalf("precondition: cached quota = %d, err = %v; want 100, nil", cached, err)
	}

	credited, err := Redeem(code, user.Id)
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if credited != 500 {
		t.Fatalf("Redeem credited %d, want 500", credited)
	}

	// The row is the source of truth...
	fromDB, err := GetUserQuota(user.Id, true)
	if err != nil {
		t.Fatalf("GetUserQuota(fromDB): %v", err)
	}
	if fromDB != 600 {
		t.Fatalf("row quota = %d, want 600", fromDB)
	}

	// ...and the cached read — which is what the relay path actually uses —
	// must agree with it.
	cached, err := getUserQuotaCache(user.Id)
	if err != nil {
		t.Fatalf("getUserQuotaCache: %v", err)
	}
	if cached != 600 {
		t.Errorf("cached quota = %d after redeeming 500 on top of 100, want 600 "+
			"(the cache was left holding the pre-topup balance)", cached)
	}
}
