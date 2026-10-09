package openrouter_pool

import (
	"net/http"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// testORType is the channel type used by pre-existing keyword/metadata tests.
const testORType = constant.ChannelTypeOpenRouter

func requestTooLarge429() *types.NewAPIError {
	e := types.WithOpenAIError(types.OpenAIError{
		Message: "Request too large for model `x` in organization `org` on tokens per minute (TPM): Limit 6000, Requested 9000. The input or output tokens must be reduced in order to run successfully.",
		Type:    "tokens",
		Code:    "rate_limit_exceeded",
	}, 429)
	e.UpstreamHeader = http.Header{"Retry-After": []string{"60"}}
	return e
}

func TestIsRequestCaused429(t *testing.T) {
	cases := []struct {
		name string
		err  *types.NewAPIError
		want bool
	}{
		{"nil", nil, false},
		{"too large", requestTooLarge429(), true},
		{"must be reduced upper", types.WithOpenAIError(types.OpenAIError{Message: "Tokens MUST BE REDUCED"}, 429), true},
		{"reduce the length", types.WithOpenAIError(types.OpenAIError{Message: "Please Reduce The Length of the messages"}, 429), true},
		{"maximum context", types.WithOpenAIError(types.OpenAIError{Message: "exceeds Maximum Context window"}, 429), true},
		{"tokens requested over limit", types.WithOpenAIError(types.OpenAIError{Message: "Limit 100, Requested 200", Type: "tokens"}, 429), true},
		{"tokens requested under limit", types.WithOpenAIError(types.OpenAIError{Message: "Limit 200, Requested 100", Type: "tokens"}, 429), false},
		{"plain rate limit", types.WithOpenAIError(types.OpenAIError{Message: "Rate limit reached", Type: "rate_limit_error"}, 429), false},
		{"not 429", types.WithOpenAIError(types.OpenAIError{Message: "request too large"}, 413), false},
	}
	for _, tc := range cases {
		if got := app.IsRequestCaused429(tc.err); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMaybeMarkCooldown_RequestCaused429_NeverCools(t *testing.T) {
	withAutoDisable(t)
	for _, typ := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeOpenRouter, constant.ChannelTypeGemini} {
		calls := markSeam(t, true)
		single := baseChannelErr()
		single.ChannelType, single.IsMultiKey, single.ChannelId = typ, false, 4100+typ
		MaybeMarkCooldown(single, requestTooLarge429())
		if app.ChannelCoolingUntil(single.ChannelId, 0) != 0 {
			t.Fatalf("type %d: single-key channel cooled on a request-caused 429", typ)
		}
		multi := baseChannelErr()
		multi.ChannelType, multi.AutoBan = typ, true
		MaybeMarkCooldown(multi, requestTooLarge429())
		if len(*calls) != 0 {
			t.Fatalf("type %d: multi-key write on a request-caused 429: %v", typ, *calls)
		}
	}
}

func TestMaybeMarkCooldown_PlainRateLimit429_StillCools(t *testing.T) {
	withAutoDisable(t)
	markSeam(t, true)
	ce := baseChannelErr()
	ce.ChannelType, ce.IsMultiKey, ce.ChannelId = constant.ChannelTypeOpenAI, false, 4200
	e := types.WithOpenAIError(types.OpenAIError{Message: "Rate limit reached for requests", Type: "rate_limit_error"}, 429)
	e.UpstreamHeader = http.Header{"Retry-After": []string{"60"}}
	MaybeMarkCooldown(ce, e)
	if app.ChannelCoolingUntil(4200, 0) == 0 {
		t.Fatal("ordinary rate-limit 429 must still cool the channel")
	}
}

func TestParseCooldownUntil_DailyKeywordOnlyForOpenRouter(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"error":{"message":"free-models-per-day limit reached"}}`)

	if got := ParseCooldownUntil(nil, body, now, constant.ChannelTypeOpenRouter); got != now.Add(24*time.Hour).Unix() {
		t.Fatalf("openrouter keyword must park 24h, got +%ds", got-now.Unix())
	}
	// Non-OpenRouter without Retry-After: 60s fallback.
	if got := ParseCooldownUntil(nil, body, now, constant.ChannelTypeOpenAI); got != now.Unix()+60 {
		t.Fatalf("non-openrouter without header must use 60s fallback, got +%ds", got-now.Unix())
	}
	// Non-OpenRouter with Retry-After: never longer than the header.
	h := http.Header{"Retry-After": []string{"45"}}
	if got := ParseCooldownUntil(h, body, now, constant.ChannelTypeOpenAI); got > now.Unix()+45 {
		t.Fatalf("non-openrouter must not exceed Retry-After, got +%ds", got-now.Unix())
	}
}
