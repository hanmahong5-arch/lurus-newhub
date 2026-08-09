package common

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// withMiniRedis points common.RDB at an in-process miniredis for the duration of
// a test and restores the prior client on cleanup. Unlike withTestRedis (which
// needs a real server via NEWHUB_REDIS_TEST_ADDR) this runs under `-short`, so
// the Redis happy-paths — the branches the dead-client tests can only fail — are
// exercised hermetically.
func withMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prevRDB, prevEnabled := RDB, RedisEnabled
	RDB, RedisEnabled = client, true
	t.Cleanup(func() {
		RDB, RedisEnabled = prevRDB, prevEnabled
		_ = client.Close()
		mr.Close()
	})
	return mr
}

// hashRow is a small struct used to round-trip through RedisHSetObj/RedisHGetObj.
type hashRow struct {
	Name    string
	Count   int
	Enabled bool
}

// TestRedisHash_RoundTrip_MiniRedis writes a struct with RedisHSetObj and reads
// it back with RedisHGetObj against a real (in-process) Redis, asserting the
// reflect-based encode→decode preserves string/int/bool fields exactly.
func TestRedisHash_RoundTrip_MiniRedis(t *testing.T) {
	withMiniRedis(t)
	ctx := context.Background()

	in := &hashRow{Name: "alpha", Count: 42, Enabled: true}
	if err := RedisHSetObj(ctx, "row:1", in, time.Minute); err != nil {
		t.Fatalf("RedisHSetObj: %v", err)
	}

	var out hashRow
	if err := RedisHGetObj(ctx, "row:1", &out); err != nil {
		t.Fatalf("RedisHGetObj: %v", err)
	}
	if out.Name != "alpha" || out.Count != 42 || !out.Enabled {
		t.Errorf("round-trip mismatch: %+v", out)
	}

	// Missing key surfaces a not-found error (not a silent zero value).
	if err := RedisHGetObj(ctx, "row:absent", &out); err == nil {
		t.Error("RedisHGetObj on a missing key must error")
	}
	// A non-pointer target is rejected.
	if err := RedisHGetObj(ctx, "row:1", out); err == nil {
		t.Error("RedisHGetObj must reject a non-pointer target")
	}
}

// TestRedisIncr_PreservesTTL asserts RedisIncr increments a keyed counter and
// keeps its TTL (the whole reason the helper exists rather than a plain INCRBY).
func TestRedisIncr_PreservesTTL(t *testing.T) {
	withMiniRedis(t)
	ctx := context.Background()

	if err := RDB.Set(ctx, "ctr", 10, 5*time.Minute).Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := RedisIncr(ctx, "ctr", 7); err != nil {
		t.Fatalf("RedisIncr: %v", err)
	}
	if v, _ := RDB.Get(ctx, "ctr").Int64(); v != 17 {
		t.Errorf("counter = %d, want 17", v)
	}
	if ttl := RDB.TTL(ctx, "ctr").Val(); ttl <= 0 || ttl > 5*time.Minute {
		t.Errorf("TTL not preserved: %v", ttl)
	}

	// A key with no TTL (persistent) is still incremented — it just keeps having
	// no expiry (see fix_redis_incr_ttl_test.go: skipping it silently dropped
	// the write).
	if err := RDB.Set(ctx, "ctr2", 3, 0).Err(); err != nil {
		t.Fatalf("seed2: %v", err)
	}
	if err := RedisIncr(ctx, "ctr2", 5); err != nil {
		t.Fatalf("RedisIncr no-ttl: %v", err)
	}
	if v, _ := RDB.Get(ctx, "ctr2").Int64(); v != 8 {
		t.Errorf("no-ttl key should be incremented, got %d", v)
	}
}

// TestRedisHIncrBy_And_HSetField exercises the hash-field mutators' happy paths
// (key present with a TTL) and asserts both the value change and TTL retention.
func TestRedisHIncrBy_And_HSetField(t *testing.T) {
	withMiniRedis(t)
	ctx := context.Background()

	if err := RDB.HSet(ctx, "h", "n", 4).Err(); err != nil {
		t.Fatalf("seed hash: %v", err)
	}
	if err := RDB.Expire(ctx, "h", 5*time.Minute).Err(); err != nil {
		t.Fatalf("expire: %v", err)
	}

	if err := RedisHIncrBy(ctx, "h", "n", 6); err != nil {
		t.Fatalf("RedisHIncrBy: %v", err)
	}
	if v, _ := RDB.HGet(ctx, "h", "n").Int64(); v != 10 {
		t.Errorf("hash field = %d, want 10", v)
	}

	if err := RedisHSetField(ctx, "h", "label", "live"); err != nil {
		t.Fatalf("RedisHSetField: %v", err)
	}
	if v := RDB.HGet(ctx, "h", "label").Val(); v != "live" {
		t.Errorf("hash field label = %q, want live", v)
	}
	if ttl := RDB.TTL(ctx, "h").Val(); ttl <= 0 {
		t.Errorf("hash TTL not preserved: %v", ttl)
	}
}

// TestCachedWalletBalance_MiniRedis covers GetCachedWalletBalance's success and
// parse-error branches plus Set/Invalidate against a real Redis.
func TestCachedWalletBalance_MiniRedis(t *testing.T) {
	withMiniRedis(t)

	const acct int64 = 555
	SetCachedWalletBalance(acct, 123.45)
	bal, ok := GetCachedWalletBalance(acct)
	if !ok || bal < 123.44 || bal > 123.46 {
		t.Fatalf("GetCachedWalletBalance = %v ok=%v, want ~123.45", bal, ok)
	}

	InvalidateCachedWalletBalance(acct)
	if _, ok := GetCachedWalletBalance(acct); ok {
		t.Error("balance should be gone after invalidate")
	}

	// A corrupt (non-numeric) cached value degrades to (0,false), not a panic.
	ctx := context.Background()
	if err := RDB.Set(ctx, "wallet:avail:999", "not-a-number", time.Minute).Err(); err != nil {
		t.Fatalf("seed corrupt: %v", err)
	}
	if v, ok := GetCachedWalletBalance(999); ok || v != 0 {
		t.Errorf("corrupt cache should give (0,false), got (%v,%v)", v, ok)
	}
}

// TestReserveDegradedSpend_MiniRedis exercises the spend-cap reservation against
// a real Redis: it must increment the rolling counter, stamp a window TTL on the
// first write, and fail closed once the cap is reached — the safety net that
// bounds unsecured spend while billing is degraded.
func TestReserveDegradedSpend_MiniRedis(t *testing.T) {
	withMiniRedis(t)
	ctx := context.Background()

	prevCap, prevWin := BillingDegradedSpendCapLB, BillingDegradedWindowSec
	BillingDegradedSpendCapLB, BillingDegradedWindowSec = 2.0, 3600
	t.Cleanup(func() { BillingDegradedSpendCapLB, BillingDegradedWindowSec = prevCap, prevWin })

	tenant := "t-1"
	admitted := 0
	for i := 0; i < 10; i++ {
		if reserveDegradedSpend(tenant, 0.5) {
			admitted++
		}
	}
	// cap 2.0 / 0.5 = 4 admits, then fail-closed.
	if admitted != 4 {
		t.Fatalf("reserveDegradedSpend admitted %d, want 4", admitted)
	}

	// The window TTL was stamped on the first write.
	if ttl := RDB.TTL(ctx, "billing:degraded:"+tenant).Val(); ttl <= 0 || ttl > time.Hour {
		t.Errorf("degraded-spend TTL = %v, want (0, 1h]", ttl)
	}

	// A different tenant has an independent budget.
	if !reserveDegradedSpend("t-2", 0.5) {
		t.Error("a fresh tenant must have its own budget")
	}
}

// TestTryDegradedPreAuth_Admit_MiniRedis drives the full P1-2 ADMIT decision
// under `-short`: breaker OPEN + fresh ample cached balance + under-cap ⇒ admit,
// with the reservation landing in Redis. This is the safety-critical "charge
// from cache while billing is down" path the hermetic (Redis-less) unit tests
// cannot reach, and which otherwise only runs behind the skipped real-Redis tier.
func TestTryDegradedPreAuth_Admit_MiniRedis(t *testing.T) {
	withMiniRedis(t)
	resetBillingBreaker(t)
	openBillingBreaker(t)

	const (
		acct   int64 = 4242
		tenant       = "t-admit"
		est          = 0.5
	)
	prevCap, prevWin := BillingDegradedSpendCapLB, BillingDegradedWindowSec
	BillingDegradedSpendCapLB, BillingDegradedWindowSec = 100.0, 3600
	t.Cleanup(func() { BillingDegradedSpendCapLB, BillingDegradedWindowSec = prevCap, prevWin })

	SetCachedWalletBalance(acct, 1000.0)
	if !TryDegradedPreAuth(tenant, acct, est, errBreakerOpen()) {
		t.Fatal("expected ADMIT: breaker open + ample fresh cache + under cap")
	}
	reserved, err := RDB.Get(context.Background(), "billing:degraded:"+tenant).Float64()
	if err != nil || reserved != est {
		t.Errorf("reservation = %v (err=%v), want %v", reserved, err, est)
	}

	// Denied when cache is absent (no fresh balance to trust).
	if TryDegradedPreAuth(tenant, 9999, est, errBreakerOpen()) {
		t.Error("must deny when there is no cached balance for the account")
	}
}

// TestRedis_DebugBranches toggles DebugEnabled so the SysLog debug branches of
// the thin Redis wrappers execute against a real (in-process) server, and
// asserts each still returns the correct result/round-trip.
func TestRedis_DebugBranches(t *testing.T) {
	withMiniRedis(t)
	ctx := context.Background()

	prev := DebugEnabled
	DebugEnabled = true
	t.Cleanup(func() { DebugEnabled = prev })

	if err := RedisSet(ctx, "dk", "dv", time.Minute); err != nil {
		t.Fatalf("RedisSet: %v", err)
	}
	if v, err := RedisGet(ctx, "dk"); err != nil || v != "dv" {
		t.Fatalf("RedisGet = %q err=%v", v, err)
	}
	if err := RedisDelKey(ctx, "dk"); err != nil {
		t.Fatalf("RedisDelKey: %v", err)
	}
	if err := RedisSet(ctx, "dk2", "x", time.Minute); err != nil {
		t.Fatalf("RedisSet2: %v", err)
	}
	if err := RedisDel(ctx, "dk2"); err != nil {
		t.Fatalf("RedisDel: %v", err)
	}
	if _, err := RedisGet(ctx, "dk2"); err == nil {
		t.Error("RedisGet on deleted key should error (redis.Nil)")
	}
}

// richRow exercises RedisHGetObj's per-kind decode branches: string, int64,
// bool, a non-nil pointer, and the gorm.DeletedAt struct special-case.
type richRow struct {
	Name    string
	Count   int64
	Flag    bool
	Note    *string
	Deleted gorm.DeletedAt
}

// TestRedisHGetObj_TypeBranches manually seeds a hash with every supported field
// kind (including a gorm.DeletedAt in RFC3339) and asserts RedisHGetObj decodes
// each into the right Go type — the reflect branches the plain round-trip test
// (which skips DeletedAt and uses no pointer) cannot reach.
func TestRedisHGetObj_TypeBranches(t *testing.T) {
	withMiniRedis(t)
	ctx := context.Background()

	deletedAt := "2026-01-02T03:04:05Z"
	if err := RDB.HSet(ctx, "rich", map[string]any{
		"Name":    "beta",
		"Count":   "9",
		"Flag":    "true",
		"Note":    "hello",
		"Deleted": deletedAt,
	}).Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var out richRow
	if err := RedisHGetObj(ctx, "rich", &out); err != nil {
		t.Fatalf("RedisHGetObj: %v", err)
	}
	if out.Name != "beta" || out.Count != 9 || !out.Flag {
		t.Errorf("scalar decode mismatch: %+v", out)
	}
	if out.Note == nil || *out.Note != "hello" {
		t.Errorf("pointer field decode mismatch: %v", out.Note)
	}
	if !out.Deleted.Valid || out.Deleted.Time.Year() != 2026 {
		t.Errorf("gorm.DeletedAt decode mismatch: %+v", out.Deleted)
	}

	// A malformed int value surfaces a parse error (never a silent zero).
	if err := RDB.HSet(ctx, "badint", "Count", "not-a-number").Err(); err != nil {
		t.Fatalf("seed badint: %v", err)
	}
	if err := RedisHGetObj(ctx, "badint", &richRow{}); err == nil {
		t.Error("non-numeric int field must produce a parse error")
	}
}

// unsupportedRow has a float64 field that RedisHGetObj cannot decode.
type unsupportedRow struct {
	Ratio float64
}

// TestRedisHGetObj_UnsupportedType asserts an unsupported field kind is reported
// as an error rather than silently ignored.
func TestRedisHGetObj_UnsupportedType(t *testing.T) {
	withMiniRedis(t)
	ctx := context.Background()
	if err := RDB.HSet(ctx, "u", "Ratio", "1.5").Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := RedisHGetObj(ctx, "u", &unsupportedRow{}); err == nil {
		t.Error("unsupported field type must error")
	}
}

// TestInitRedisClient_Success points REDIS_CONN_STRING at an in-process Redis
// and asserts the boot-time initializer connects, flips RedisEnabled on, and
// leaves a usable client — the happy path of the bounded connect-retry logic.
func TestInitRedisClient_Success(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)

	prevRDB, prevEnabled := RDB, RedisEnabled
	t.Cleanup(func() {
		if RDB != nil {
			_ = RDB.Close()
		}
		RDB, RedisEnabled = prevRDB, prevEnabled
	})
	// InitRedisClient only ever DISABLES Redis (never sets RedisEnabled=true); it
	// relies on the boot default of true. Establish that precondition explicitly so
	// this test is independent of order (other tests leave RedisEnabled=false).
	RedisEnabled = true

	t.Setenv("REDIS_CONN_STRING", "redis://"+mr.Addr())
	t.Setenv("SYNC_FREQUENCY", "30")

	if err := InitRedisClient(); err != nil {
		t.Fatalf("InitRedisClient: %v", err)
	}
	if !RedisEnabled || RDB == nil {
		t.Fatal("Redis should stay enabled with a live client after init")
	}
	if err := RDB.Ping(context.Background()).Err(); err != nil {
		t.Errorf("initialized client should ping: %v", err)
	}
}

// TestInitRedisClient_Disabled: an empty REDIS_CONN_STRING disables Redis and
// returns nil (no fatal), the intended "run without Redis" mode.
func TestInitRedisClient_Disabled(t *testing.T) {
	prevEnabled := RedisEnabled
	t.Cleanup(func() { RedisEnabled = prevEnabled })

	t.Setenv("REDIS_CONN_STRING", "")
	if err := InitRedisClient(); err != nil {
		t.Fatalf("InitRedisClient disabled path should not error: %v", err)
	}
	if RedisEnabled {
		t.Error("empty REDIS_CONN_STRING must leave Redis disabled")
	}
}

// TestShouldSkipPreAuth_MiniRedis covers both outcomes of the fast-path skip
// decision against a real cache: a high balance skips pre-auth, a thin balance
// does not.
func TestShouldSkipPreAuth_MiniRedis(t *testing.T) {
	withMiniRedis(t)

	SetCachedWalletBalance(100, 500.0)
	if !ShouldSkipPreAuth(100, 1.0) {
		t.Error("ample cached balance should skip pre-auth")
	}
	// Balance clears the floor but not the 3× margin over the estimate.
	SetCachedWalletBalance(101, 12.0)
	if ShouldSkipPreAuth(101, 5.0) {
		t.Error("balance too close to estimate must not skip pre-auth")
	}
	// No cached balance ⇒ never skip.
	if ShouldSkipPreAuth(102, 1.0) {
		t.Error("missing cache must not skip pre-auth")
	}
}
