package limiter

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestAllow_SurvivesScriptCacheFlush reproduces a Redis restart (or SCRIPT
// FLUSH): the SHA loaded at startup is gone. The limiter used to EvalSha that
// SHA, get NOSCRIPT on every call, and the caller failed open until the pod
// restarted. It must reload and keep limiting.
func TestAllow_SurvivesScriptCacheFlush(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	resetSingleton(t)
	rl := New(context.Background(), client)
	ctx := context.Background()

	if _, err := rl.Allow(ctx, "k", WithCapacity(1), WithRate(1)); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := client.ScriptFlush(ctx).Err(); err != nil {
		t.Fatalf("script flush: %v", err)
	}
	// Capacity 1 was spent above: after the flush the limiter must still
	// answer (no error) and still refuse, i.e. actually run the script.
	allowed, err := rl.Allow(ctx, "k", WithCapacity(1), WithRate(1))
	if err != nil {
		t.Fatalf("after SCRIPT FLUSH: %v — the limiter no longer works (callers fail open)", err)
	}
	if allowed {
		t.Fatal("after SCRIPT FLUSH the second request within capacity 1 was allowed")
	}
}
