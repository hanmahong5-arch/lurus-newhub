package dto

import (
	"encoding/json"
	"strings"
	"testing"
)

// OpenAI's wire reports cache writes as prompt_tokens_details.cache_write_tokens
// (chat) / input_tokens_details.cache_write_tokens (Responses): "the unadjusted
// number of prompt tokens written to cache", disjoint from cached_tokens and
// inside prompt_tokens. Until 2026-09-02 the Go field was json:"-", so the
// figure was neither parsed (GPT-5.6+ writes, priced 1.25x by the vendor, were
// billed at the plain input rate) nor ever shown to a caller on another wire.
func TestUsage_CacheWriteTokensRoundTripOnTheOpenAIWire(t *testing.T) {
	var u Usage
	body := `{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,` +
		`"prompt_tokens_details":{"cached_tokens":50,"cache_write_tokens":20}}`
	if err := json.Unmarshal([]byte(body), &u); err != nil {
		t.Fatal(err)
	}
	if u.PromptTokensDetails.CachedTokens != 50 || u.PromptTokensDetails.CachedCreationTokens != 20 {
		t.Errorf("parsed cached/write = %d/%d, want 50/20", u.PromptTokensDetails.CachedTokens, u.PromptTokensDetails.CachedCreationTokens)
	}

	out, _ := json.Marshal(u)
	if !strings.Contains(string(out), `"cache_write_tokens":20`) {
		t.Errorf("cache_write_tokens not re-emitted: %s", out)
	}

	// Zero stays off the wire: callers on models without cache writes must not
	// suddenly see a new key.
	none, _ := json.Marshal(Usage{PromptTokens: 1, PromptTokensDetails: InputTokenDetails{CachedTokens: 1}})
	if strings.Contains(string(none), "cache_write_tokens") {
		t.Errorf("cache_write_tokens emitted at zero: %s", none)
	}
}
