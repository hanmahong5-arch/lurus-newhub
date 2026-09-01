package handler

// Pins the CACHE side of the welcome-quota grant. Second live probe
// 2026-08-31: DB quota said 10000 but relay still 402'd "available
// $0.000000" — Insert()'s sidebar-config step calls user.Update(), which
// caches the user hash (Quota=0) in Redis, and the first grant
// implementation used a raw DB UPDATE that left that stale 0 in the cache.
// The grant must go through IncreaseUserQuota so the cache-preferring read
// the relay uses (GetUserQuota(id, false)) sees the granted amount.

import (
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestAutoCreateBridgedUser_WelcomeQuotaVisibleThroughCache(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prevRDB, prevEnabled, prevSync := common.RDB, common.RedisEnabled, common.SyncFrequency
	common.RDB = client
	common.RedisEnabled = true
	common.SyncFrequency = 60 // keys get a TTL → HINCRBY field helpers act
	defer func() {
		common.RDB, common.RedisEnabled, common.SyncFrequency = prevRDB, prevEnabled, prevSync
		_ = client.Close()
	}()

	const accountID = int64(939393)
	got, err := autoCreateBridgedUser(accountID, "default")
	if err != nil {
		t.Fatalf("autoCreateBridgedUser: %v", err)
	}

	// The exact read the relay's pre-consume gate performs: cache-preferring.
	// The cache increment is async (gopool) — poll with a bound instead of a
	// fixed sleep. A stale cached 0 (the raw-UPDATE bug) never converges, so
	// the deadline is the failure detector.
	deadline := time.Now().Add(2 * time.Second)
	for {
		q, qerr := repo.GetUserQuota(got.Id, false)
		if qerr == nil && q == 10000 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("cache-preferring GetUserQuota = %d (err %v), want 10000 — welcome grant invisible through the cache", q, qerr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
