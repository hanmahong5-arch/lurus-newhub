package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// affinityCtx builds a request context with the caller scope that
// DeriveSessionAffinityKey hashes into every binding.
func affinityCtx(t *testing.T, tokenID, userID int, group, model string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("token_id", tokenID)
	c.Set("id", userID)
	c.Set("group", group)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, model)
	return c
}

func TestDeriveSessionAffinityKey_Sources(t *testing.T) {
	t.Setenv("SESSION_AFFINITY_ENABLED", "true")

	t.Run("no_identifier_yields_no_affinity", func(t *testing.T) {
		c := affinityCtx(t, 1, 1, "default", "gpt-4o")
		if got := DeriveSessionAffinityKey(c, &dto.GeneralOpenAIRequest{}); got != "" {
			t.Errorf("one-shot request must not be pinned, got %q", got)
		}
	})

	t.Run("header_wins_over_body", func(t *testing.T) {
		c := affinityCtx(t, 1, 1, "default", "gpt-4o")
		c.Request.Header.Set("X-Session-Id", "from-header")
		fromHeader := DeriveSessionAffinityKey(c, &dto.GeneralOpenAIRequest{PromptCacheKey: "from-body"})

		c2 := affinityCtx(t, 1, 1, "default", "gpt-4o")
		c2.Request.Header.Set("X-Session-Id", "from-header")
		headerOnly := DeriveSessionAffinityKey(c2, &dto.GeneralOpenAIRequest{})

		if fromHeader == "" || fromHeader != headerOnly {
			t.Errorf("header must take precedence: %q vs %q", fromHeader, headerOnly)
		}
	})

	t.Run("openai_prompt_cache_key", func(t *testing.T) {
		c := affinityCtx(t, 1, 1, "default", "gpt-4o")
		if got := DeriveSessionAffinityKey(c, &dto.GeneralOpenAIRequest{PromptCacheKey: "conv-7"}); got == "" {
			t.Error("prompt_cache_key must produce a binding")
		}
	})

	t.Run("responses_prompt_cache_key_json_string", func(t *testing.T) {
		c := affinityCtx(t, 1, 1, "default", "gpt-4o")
		raw, err := json.Marshal("conv-8")
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if got := DeriveSessionAffinityKey(c, &dto.OpenAIResponsesRequest{PromptCacheKey: raw}); got == "" {
			t.Error("responses prompt_cache_key must produce a binding")
		}
	})

	t.Run("responses_non_string_cache_key_ignored", func(t *testing.T) {
		c := affinityCtx(t, 1, 1, "default", "gpt-4o")
		// A client sending {"prompt_cache_key": {...}} must not crash or pin.
		if got := DeriveSessionAffinityKey(c, &dto.OpenAIResponsesRequest{PromptCacheKey: json.RawMessage(`{"a":1}`)}); got != "" {
			t.Errorf("non-string cache key must be ignored, got %q", got)
		}
	})

	t.Run("claude_metadata_user_id", func(t *testing.T) {
		c := affinityCtx(t, 1, 1, "default", "claude-sonnet")
		meta, err := json.Marshal(dto.ClaudeMetadata{UserId: "u-42"})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if got := DeriveSessionAffinityKey(c, &dto.ClaudeRequest{Metadata: meta}); got == "" {
			t.Error("claude metadata.user_id must produce a binding")
		}
	})

	t.Run("disabled_yields_no_affinity", func(t *testing.T) {
		t.Setenv("SESSION_AFFINITY_ENABLED", "false")
		c := affinityCtx(t, 1, 1, "default", "gpt-4o")
		c.Request.Header.Set("X-Session-Id", "s1")
		if got := DeriveSessionAffinityKey(c, &dto.GeneralOpenAIRequest{}); got != "" {
			t.Errorf("feature flag off must disable pinning, got %q", got)
		}
	})
}

// TestDeriveSessionAffinityKey_ScopeIsolation is the security-relevant case:
// the same session id presented by a different token, user, group or model must
// never resolve to the same binding.
func TestDeriveSessionAffinityKey_ScopeIsolation(t *testing.T) {
	t.Setenv("SESSION_AFFINITY_ENABLED", "true")
	const sessionID = "shared-session-id"

	keyFor := func(tokenID, userID int, group, model string) string {
		c := affinityCtx(t, tokenID, userID, group, model)
		c.Request.Header.Set("X-Session-Id", sessionID)
		return DeriveSessionAffinityKey(c, &dto.GeneralOpenAIRequest{})
	}

	base := keyFor(1, 1, "default", "gpt-4o")
	if base == "" {
		t.Fatal("baseline key must be non-empty")
	}
	if base == sessionID {
		t.Fatal("raw session id must never be used as the storage key")
	}

	for _, tc := range []struct {
		name string
		got  string
	}{
		{"different_token", keyFor(2, 1, "default", "gpt-4o")},
		{"different_user", keyFor(1, 2, "default", "gpt-4o")},
		{"different_group", keyFor(1, 1, "vip", "gpt-4o")},
		{"different_model", keyFor(1, 1, "default", "claude-sonnet")},
	} {
		if tc.got == base {
			t.Errorf("%s: binding leaked across scope boundary", tc.name)
		}
	}

	if again := keyFor(1, 1, "default", "gpt-4o"); again != base {
		t.Error("same scope + same session id must be stable across turns")
	}
}

func TestAffinityRecord_EncodeDecode(t *testing.T) {
	rec := affinityRecord{ChannelID: 12, Group: "vip"}
	got, ok := decodeAffinity(encodeAffinity(rec))
	if !ok || got != rec {
		t.Fatalf("round-trip failed: %+v ok=%v", got, ok)
	}

	for _, bad := range []string{"", "12", "abc|vip", "0|default", "-3|default"} {
		if _, ok := decodeAffinity(bad); ok {
			t.Errorf("decodeAffinity(%q) must reject malformed record", bad)
		}
	}

	// A group containing the separator must not corrupt the channel id.
	weird := affinityRecord{ChannelID: 5, Group: "a|b"}
	if got, ok := decodeAffinity(encodeAffinity(weird)); !ok || got.ChannelID != 5 {
		t.Errorf("separator in group broke decode: %+v ok=%v", got, ok)
	}
}

func TestAffinityStoreLoad_MemoryFallback(t *testing.T) {
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prevRedis })
	resetAffinityMemForTest()

	c := affinityCtx(t, 1, 1, "default", "gpt-4o")
	rec := affinityRecord{ChannelID: 9, Group: "default"}

	if _, ok := affinityLoad(c, "unknown-key"); ok {
		t.Error("unknown key must miss")
	}

	affinityStore(c, "k1", rec)
	got, ok := affinityLoad(c, "k1")
	if !ok || got != rec {
		t.Fatalf("store/load round-trip failed: %+v ok=%v", got, ok)
	}

	t.Run("expired_entry_is_dropped", func(t *testing.T) {
		affinityMemMu.Lock()
		affinityMem["k-expired"] = affinityMemEntry{value: encodeAffinity(rec), expires: time.Now().Add(-time.Minute)}
		affinityMemMu.Unlock()

		if _, ok := affinityLoad(c, "k-expired"); ok {
			t.Error("expired binding must not be served")
		}
		affinityMemMu.Lock()
		_, still := affinityMem["k-expired"]
		affinityMemMu.Unlock()
		if still {
			t.Error("expired binding must be evicted on read")
		}
	})

	t.Run("malformed_entry_is_a_miss", func(t *testing.T) {
		affinityMemMu.Lock()
		affinityMem["k-bad"] = affinityMemEntry{value: "not-a-record", expires: time.Now().Add(time.Hour)}
		affinityMemMu.Unlock()

		if _, ok := affinityLoad(c, "k-bad"); ok {
			t.Error("malformed binding must fail open, not pin")
		}
	})

	t.Run("prune_reclaims_expired", func(t *testing.T) {
		resetAffinityMemForTest()
		affinityMemMu.Lock()
		affinityMem["dead"] = affinityMemEntry{value: encodeAffinity(rec), expires: time.Now().Add(-time.Hour)}
		affinityMem["live"] = affinityMemEntry{value: encodeAffinity(rec), expires: time.Now().Add(time.Hour)}
		pruneAffinityMemLocked()
		remaining := len(affinityMem)
		_, liveKept := affinityMem["live"]
		affinityMemMu.Unlock()

		if remaining != 1 || !liveKept {
			t.Errorf("prune must drop only expired entries, remaining=%d liveKept=%v", remaining, liveKept)
		}
	})
}

func TestAffinityTTL_Defaults(t *testing.T) {
	t.Setenv("SESSION_AFFINITY_TTL", "0")
	if got := affinityTTL(); got != time.Duration(affinityDefaultTTLSeconds)*time.Second {
		t.Errorf("non-positive TTL must fall back to default, got %v", got)
	}
	t.Setenv("SESSION_AFFINITY_TTL", "120")
	if got := affinityTTL(); got != 2*time.Minute {
		t.Errorf("TTL override ignored, got %v", got)
	}
}

// TestAffinityKeyHeader_OnlyWhenSourcePresent covers L5's console-ux-30
// header: present with the derived key when the request carries an
// affinity source, absent entirely otherwise — never present-but-empty.
func TestAffinityKeyHeader_OnlyWhenSourcePresent(t *testing.T) {
	t.Setenv("SESSION_AFFINITY_ENABLED", "true")

	t.Run("no_source_no_header", func(t *testing.T) {
		c := affinityCtx(t, 1, 1, "default", "gpt-4o")
		key := DeriveSessionAffinityKey(c, &dto.GeneralOpenAIRequest{})
		if key != "" {
			t.Fatalf("expected no affinity key, got %q", key)
		}
		if got := c.Writer.Header().Get(AffinityKeyResponseHeader); got != "" {
			t.Errorf("header must be absent when there is no affinity source, got %q", got)
		}
	})

	t.Run("source_present_header_matches_key", func(t *testing.T) {
		c := affinityCtx(t, 1, 1, "default", "gpt-4o")
		c.Request.Header.Set("X-Session-Id", "conv-123")
		key := DeriveSessionAffinityKey(c, &dto.GeneralOpenAIRequest{})
		if key == "" {
			t.Fatal("expected a non-empty affinity key")
		}
		if got := c.Writer.Header().Get(AffinityKeyResponseHeader); got != key {
			t.Errorf("header = %q, want %q (the derived affinity key)", got, key)
		}
	})
}

// TestAffinityStats_ReflectsMemoryEntries proves AffinityStatsSnapshot reads
// LIVE state — both the atomic hit/miss/stale counters recordAffinityOutcome
// updates and the fallback map's actual current size — not a stale or
// hand-built copy.
func TestAffinityStats_ReflectsMemoryEntries(t *testing.T) {
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prevRedis })
	resetAffinityMemForTest()
	resetAffinityCountersForTest()

	c := affinityCtx(t, 1, 1, "default", "gpt-4o")
	affinityStore(c, "stat-key-1", affinityRecord{ChannelID: 1, Group: "default"})
	affinityStore(c, "stat-key-2", affinityRecord{ChannelID: 2, Group: "default"})

	recordAffinityOutcome("hit")
	recordAffinityOutcome("hit")
	recordAffinityOutcome("miss")
	recordAffinityOutcome("stale")

	stats := AffinityStatsSnapshot()
	if stats.Hit != 2 || stats.Miss != 1 || stats.Stale != 1 {
		t.Errorf("counters = hit=%d miss=%d stale=%d, want 2/1/1", stats.Hit, stats.Miss, stats.Stale)
	}
	if stats.Backend != "memory" {
		t.Errorf("Backend = %q, want memory (RedisEnabled=false)", stats.Backend)
	}
	if stats.MemEntries != 2 {
		t.Errorf("MemEntries = %d, want 2 (both stored keys still live)", stats.MemEntries)
	}
	if stats.Enabled != SessionAffinityEnabled() {
		t.Errorf("Enabled = %v, want %v (must read the live SessionAffinityEnabled())", stats.Enabled, SessionAffinityEnabled())
	}
	if stats.TTLSeconds != int(affinityTTL().Seconds()) {
		t.Errorf("TTLSeconds = %d, want %d (must read the live affinityTTL())", stats.TTLSeconds, int(affinityTTL().Seconds()))
	}

	// Purging one entry must be reflected on the very next snapshot — proves
	// this isn't a cached/point-in-time value.
	if found, err := PurgeAffinityKey(c, "stat-key-1"); err != nil || !found {
		t.Fatalf("precondition: purge of a live key must report true, got found=%v err=%v", found, err)
	}
	if got := AffinityStatsSnapshot().MemEntries; got != 1 {
		t.Errorf("MemEntries after purge = %d, want 1", got)
	}
}

// TestAffinityPurge_KeyRemovedThenMiss drives the real memory-fallback path:
// PurgeAffinityKey removes a live binding and reports it existed; the
// binding is then genuinely gone (affinityLoad misses, not merely "the
// caller believes it's gone").
func TestAffinityPurge_KeyRemovedThenMiss(t *testing.T) {
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prevRedis })
	resetAffinityMemForTest()

	c := affinityCtx(t, 1, 1, "default", "gpt-4o")
	affinityStore(c, "purge-key", affinityRecord{ChannelID: 5, Group: "default"})

	if _, ok := affinityLoad(c, "purge-key"); !ok {
		t.Fatal("precondition: binding must exist before purge")
	}

	if found, err := PurgeAffinityKey(c, "purge-key"); err != nil || !found {
		t.Fatalf("expected PurgeAffinityKey to report the key existed, got found=%v err=%v", found, err)
	}

	if _, ok := affinityLoad(c, "purge-key"); ok {
		t.Error("binding must be genuinely gone after purge, not just reported gone")
	}
}

// TestAffinityPurge_Unknown404 (naming mirrors the admin handler's HTTP
// status for this case) proves purging a key that never existed reports
// false rather than silently "succeeding" — the handler needs this to
// distinguish "purged" from "nothing there" and return 404 accordingly.
func TestAffinityPurge_Unknown404(t *testing.T) {
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prevRedis })
	resetAffinityMemForTest()

	c := affinityCtx(t, 1, 1, "default", "gpt-4o")
	if found, err := PurgeAffinityKey(c, "never-existed"); err != nil || found {
		t.Errorf("purging an unknown key must report found=false, err=nil; got found=%v err=%v", found, err)
	}
}

// TestAffinityPurge_RedisError_ReturnsError is the lock for finding
// routing-resilience-limits-11#6/#20/#47: a genuine Redis failure must come
// back as a non-nil error, never silently folded into found=false — the
// admin handler relies on this distinction to 500 instead of 404ing a
// backend outage as "nothing to purge".
func TestAffinityPurge_RedisError_ReturnsError(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()

	prevRDB := common.RDB
	prevEnabled := common.RedisEnabled
	common.RDB = rdb
	common.RedisEnabled = true
	t.Cleanup(func() {
		common.RDB = prevRDB
		common.RedisEnabled = prevEnabled
	})

	mr.Close() // simulate an outage: rdb can no longer reach the server

	c := affinityCtx(t, 1, 1, "default", "gpt-4o")
	found, err := PurgeAffinityKey(c, "irrelevant-key")
	if err == nil {
		t.Fatal("expected a non-nil error when Redis is unreachable, got nil")
	}
	if found {
		t.Error("found must be false on error, not true")
	}
}

// TestAffinityPurgeAll_Redis proves the Redis backend's purge-all path uses
// bounded SCAN+UNLINK against a real (miniredis) server: every affinity key
// is gone afterward, an unrelated key with a different prefix survives (SCAN
// is pattern-matched, not a blind FLUSHALL), and the reported count matches.
func TestAffinityPurgeAll_Redis(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()

	prevRDB := common.RDB
	prevEnabled := common.RedisEnabled
	common.RDB = rdb
	common.RedisEnabled = true
	t.Cleanup(func() {
		common.RDB = prevRDB
		common.RedisEnabled = prevEnabled
	})

	c := affinityCtx(t, 1, 1, "default", "gpt-4o")
	affinityStore(c, "redis-purge-1", affinityRecord{ChannelID: 1, Group: "default"})
	affinityStore(c, "redis-purge-2", affinityRecord{ChannelID: 2, Group: "default"})
	affinityStore(c, "redis-purge-3", affinityRecord{ChannelID: 3, Group: "default"})
	if err := rdb.Set(c.Request.Context(), "unrelated:other-feature-key", "keep-me", 0).Err(); err != nil {
		t.Fatalf("seed unrelated key: %v", err)
	}

	purged, complete, err := PurgeAllAffinity(c)
	if err != nil {
		t.Fatalf("PurgeAllAffinity: %v", err)
	}
	if purged != 3 {
		t.Errorf("purged = %d, want 3", purged)
	}
	if !complete {
		t.Error("complete = false, want true: the scan finished within the round cap")
	}

	for _, key := range []string{"redis-purge-1", "redis-purge-2", "redis-purge-3"} {
		if _, ok := affinityLoad(c, key); ok {
			t.Errorf("affinity key %q must be gone after purge-all", key)
		}
	}
	if v, err := rdb.Get(c.Request.Context(), "unrelated:other-feature-key").Result(); err != nil || v != "keep-me" {
		t.Errorf("unrelated key must survive purge-all (SCAN must be pattern-scoped, not KEYS/FLUSHALL): got %q, err=%v", v, err)
	}
}

// TestPurgeAllAffinity_RoundCapHit_ReportsIncomplete is the lock for L5
// repair round 3, finding routing-resilience-limits-13#6: the pre-repair
// PurgeAllAffinity had no way to tell a caller "the round cap cut this off
// before the cursor reached 0" apart from "the scan actually finished" — it
// just returned whatever count it had and a nil error either way, so an
// operator reading a 200 with a purged count had no signal that bindings
// might remain.
//
// The finding's suggested repro (shrink affinityPurgeAllScanCount, seed more
// keys than one SCAN page, cap rounds at 1) does not reproduce against
// miniredis: miniredis's SCAN returns every matching key with cursor=0 on
// the first call regardless of the COUNT hint (tried with
// affinityPurgeAllScanCount=1 and 20 seeded keys — complete came back true).
// COUNT is documented as advisory in the real Redis protocol too, so this is
// not a miniredis-only quirk this test can route around by seeding more
// keys. Instead this locks the round-cap branch directly:
// affinityPurgeAllMaxRounds=0 makes the `for round := 0; round <
// affinityPurgeAllMaxRounds` loop body never run, so PurgeAllAffinity must
// fall through to its final `return purged, false, nil` — the exact branch
// the pre-repair code lacked. Revert that fall-through to `return purged,
// true, nil` to see this test go red.
func TestPurgeAllAffinity_RoundCapHit_ReportsIncomplete(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()

	prevRDB := common.RDB
	prevEnabled := common.RedisEnabled
	common.RDB = rdb
	common.RedisEnabled = true
	t.Cleanup(func() {
		common.RDB = prevRDB
		common.RedisEnabled = prevEnabled
	})

	prevMaxRounds := affinityPurgeAllMaxRounds
	affinityPurgeAllMaxRounds = 0
	t.Cleanup(func() { affinityPurgeAllMaxRounds = prevMaxRounds })

	c := affinityCtx(t, 1, 1, "default", "gpt-4o")
	affinityStore(c, "cap-purge-1", affinityRecord{ChannelID: 1, Group: "default"})

	purged, complete, err := PurgeAllAffinity(c)
	if err != nil {
		t.Fatalf("PurgeAllAffinity: %v", err)
	}
	if complete {
		t.Error("complete = true, want false: affinityPurgeAllMaxRounds=0 must not let a single SCAN round run")
	}
	if purged != 0 {
		t.Errorf("purged = %d, want 0: the round cap fired before any SCAN round could remove anything", purged)
	}
	if _, ok := affinityLoad(c, "cap-purge-1"); !ok {
		t.Error("binding must survive: the round cap fired before it could be removed")
	}
}
