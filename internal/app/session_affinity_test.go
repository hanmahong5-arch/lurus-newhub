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
	"github.com/gin-gonic/gin"
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
