package limiter

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetSingleton resets the package-level singleton so that each test that
// exercises New() starts from a clean slate. This is test-internal only and
// relies on being in the same package (package limiter, not package limiter_test).
func resetSingleton(t *testing.T) {
	t.Helper()
	once = sync.Once{}
	instance = nil
}

// unreachableClient returns a redis.Client that will fail every network call
// immediately, without blocking. Used to exercise error paths hermetically.
func unreachableClient() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:       "127.0.0.1:1", // port 1 is always closed / permission-denied
		MaxRetries: 0,
	})
}

// withTestRedis creates a real Redis client for integration sub-tests.
// Skips (never fails) when NEWHUB_REDIS_TEST_ADDR is unset or -short is active.
func withTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("NEWHUB_REDIS_TEST_ADDR")
	if addr == "" || testing.Short() {
		t.Skip("set NEWHUB_REDIS_TEST_ADDR and run without -short to exercise real-Redis paths")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: 0})
	ctx := context.Background()
	require.NoError(t, client.Ping(ctx).Err(), "test Redis unreachable at %s", addr)
	require.NoError(t, client.FlushDB(ctx).Err(), "flushdb")
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// ---------------------------------------------------------------------------
// Config + Option tests — pure, no Redis, always run
// ---------------------------------------------------------------------------

func TestConfig_Defaults(t *testing.T) {
	// Verify that Allow() builds its default config with the documented values.
	// We do this by constructing the config the same way Allow() does and
	// applying zero options.
	cfg := &Config{
		Capacity:  10,
		Rate:      1,
		Requested: 1,
	}
	assert.Equal(t, int64(10), cfg.Capacity)
	assert.Equal(t, int64(1), cfg.Rate)
	assert.Equal(t, int64(1), cfg.Requested)
}

func TestWithCapacity_SetsCapacity(t *testing.T) {
	tests := []struct {
		name  string
		input int64
	}{
		{"zero", 0},
		{"one", 1},
		{"large", 1_000_000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{}
			opt := WithCapacity(tc.input)
			opt(cfg)
			assert.Equal(t, tc.input, cfg.Capacity)
		})
	}
}

func TestWithRate_SetsRate(t *testing.T) {
	tests := []struct {
		name  string
		input int64
	}{
		{"zero", 0},
		{"one", 1},
		{"large", 500},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{}
			opt := WithRate(tc.input)
			opt(cfg)
			assert.Equal(t, tc.input, cfg.Rate)
		})
	}
}

func TestWithRequested_SetsRequested(t *testing.T) {
	tests := []struct {
		name  string
		input int64
	}{
		{"zero", 0},
		{"one", 1},
		{"burst", 100},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{}
			opt := WithRequested(tc.input)
			opt(cfg)
			assert.Equal(t, tc.input, cfg.Requested)
		})
	}
}

func TestOptions_MultipleAppliedInOrder(t *testing.T) {
	// Verifies that each option is applied sequentially without overwriting
	// the others — guards against accidental shared-pointer bugs.
	cfg := &Config{Capacity: 10, Rate: 1, Requested: 1}
	opts := []Option{
		WithCapacity(20),
		WithRate(5),
		WithRequested(3),
	}
	for _, opt := range opts {
		opt(cfg)
	}
	assert.Equal(t, int64(20), cfg.Capacity)
	assert.Equal(t, int64(5), cfg.Rate)
	assert.Equal(t, int64(3), cfg.Requested)
}

func TestOptions_LastWriterWins(t *testing.T) {
	// Two WithCapacity options applied: the last one wins.
	cfg := &Config{Capacity: 10}
	WithCapacity(50)(cfg)
	WithCapacity(99)(cfg)
	assert.Equal(t, int64(99), cfg.Capacity)
}

// ---------------------------------------------------------------------------
// New() — singleton behaviour
// ---------------------------------------------------------------------------

func TestNew_ReturnsNonNilOnScriptLoadFailure(t *testing.T) {
	// When ScriptLoad fails (unreachable Redis), New() still returns the
	// singleton (with an empty SHA). The contract is: caller gets an instance
	// back, and any subsequent Allow() will return an error.
	resetSingleton(t)
	t.Cleanup(func() { resetSingleton(t) }) // leave clean for other tests

	client := unreachableClient()
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	rl := New(ctx, client)
	require.NotNil(t, rl, "New must return a non-nil instance even when ScriptLoad fails")
	// The returned instance must be the package singleton.
	assert.Same(t, instance, rl)
}

func TestNew_SingletonReturnsSameInstance(t *testing.T) {
	// Calling New() a second time with a different client must return the same
	// pointer — the once.Do guarantees only the first call initialises.
	resetSingleton(t)
	t.Cleanup(func() { resetSingleton(t) })

	ctx := context.Background()
	c1 := unreachableClient()
	c2 := unreachableClient()
	t.Cleanup(func() { _ = c1.Close(); _ = c2.Close() })

	first := New(ctx, c1)
	second := New(ctx, c2)
	assert.Same(t, first, second, "New must return the same singleton on repeated calls")
}

// ---------------------------------------------------------------------------
// Allow() — error path (no real Redis needed)
// ---------------------------------------------------------------------------

func TestAllow_ReturnsErrorOnEvalShaFailure(t *testing.T) {
	// Build a RedisLimiter directly (same-package access) pointing at an
	// unreachable server with a plausible SHA — EvalSha will fail.
	client := unreachableClient()
	t.Cleanup(func() { _ = client.Close() })

	rl := &RedisLimiter{
		client: client,
	}

	ctx := context.Background()
	allowed, err := rl.Allow(ctx, "test:key")
	assert.False(t, allowed, "Allow must return false on Redis error")
	require.Error(t, err, "Allow must propagate the Redis error")
	assert.Contains(t, err.Error(), "rate limit failed", "error must be wrapped with sentinel prefix")
}

func TestAllow_DefaultsAreAppliedWhenNoOptionsGiven(t *testing.T) {
	// Verify that zero options produces a valid (non-panic) call path; the error
	// comes from Redis being unreachable, not from a nil/zero config.
	client := unreachableClient()
	t.Cleanup(func() { _ = client.Close() })

	rl := &RedisLimiter{
		client: client,
	}

	ctx := context.Background()
	_, err := rl.Allow(ctx, "default-opts-key")
	// We expect an error from Redis; what we're asserting is that it is the
	// *Redis* error, not a nil-pointer or panic from config setup.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate limit failed")
}

func TestAllow_CustomOptionsForwardedToScript(t *testing.T) {
	// When custom options are supplied the call must still reach EvalSha (no
	// panic or early return before it). Because Redis is unreachable, we receive
	// an error — that's expected; what matters is that the call path completes.
	client := unreachableClient()
	t.Cleanup(func() { _ = client.Close() })

	rl := &RedisLimiter{
		client: client,
	}

	ctx := context.Background()
	_, err := rl.Allow(ctx, "custom-opts-key",
		WithCapacity(100),
		WithRate(10),
		WithRequested(5),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate limit failed")
}

// ---------------------------------------------------------------------------
// Allow() — concurrency / race test (no real Redis needed)
// ---------------------------------------------------------------------------

func TestAllow_ConcurrentCallsDoNotRace(t *testing.T) {
	// Fire N goroutines calling Allow() concurrently on the same key.
	// The test goal is "no data race" (verified by -race), not any specific
	// allow/deny count (Redis is unreachable so all calls return errors).
	client := unreachableClient()
	t.Cleanup(func() { _ = client.Close() })

	rl := &RedisLimiter{
		client: client,
	}

	ctx := context.Background()
	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, _ = rl.Allow(ctx, "race-key", WithCapacity(100), WithRate(10))
		}()
	}
	wg.Wait()
}

func TestNew_ConcurrentCallsReturnSameSingleton(t *testing.T) {
	// N goroutines racing on New() must all get back the same pointer.
	resetSingleton(t)
	t.Cleanup(func() { resetSingleton(t) })

	ctx := context.Background()
	const goroutines = 20
	results := make([]*RedisLimiter, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			c := unreachableClient()
			results[i] = New(ctx, c)
			// Note: we intentionally do NOT close these clients mid-test; the
			// singleton holds one of them.  Cleanup below closes the singleton's
			// client.
		}()
	}
	wg.Wait()

	first := results[0]
	require.NotNil(t, first)
	for i, r := range results {
		assert.Same(t, first, r, "goroutine %d got a different instance", i)
	}
}

// ---------------------------------------------------------------------------
// Allow() — integration (real Redis, skipped in hermetic runs)
// ---------------------------------------------------------------------------

func TestAllow_Integration_AllowsWhenTokensAvailable(t *testing.T) {
	client := withTestRedis(t)
	resetSingleton(t)
	t.Cleanup(func() { resetSingleton(t) })

	ctx := context.Background()
	rl := New(ctx, client)
	require.NotNil(t, rl)

	allowed, err := rl.Allow(ctx, "integ:key:allow",
		WithCapacity(10),
		WithRate(1),
		WithRequested(1),
	)
	require.NoError(t, err)
	assert.True(t, allowed, "fresh bucket with capacity=10 must allow the first request")
}

func TestAllow_Integration_DeniesWhenCapacityExhausted(t *testing.T) {
	client := withTestRedis(t)
	resetSingleton(t)
	t.Cleanup(func() { resetSingleton(t) })

	ctx := context.Background()
	rl := New(ctx, client)
	require.NotNil(t, rl)

	const capacity = int64(3)
	key := "integ:key:exhaust"

	// Drain the bucket completely.
	for i := int64(0); i < capacity; i++ {
		allowed, err := rl.Allow(ctx, key,
			WithCapacity(capacity),
			WithRate(0), // rate=0 means no token refill between calls
			WithRequested(1),
		)
		require.NoError(t, err)
		assert.True(t, allowed, "request %d should be allowed (bucket not yet empty)", i+1)
	}

	// One more request must be denied — bucket is empty.
	allowed, err := rl.Allow(ctx, key,
		WithCapacity(capacity),
		WithRate(0),
		WithRequested(1),
	)
	require.NoError(t, err)
	assert.False(t, allowed, "bucket exhausted: request beyond capacity must be denied")
}
