package relay

// billing_cache_write_test.go — what a cache write costs, by wire.
//
// One event: 120 prompt tokens of which 50 were read from cache and 20 written
// to it, 30 output. Ratios: model 1, completion 1, cache read 0.1, group 1.
//
//	base 50 + read 50×0.1 + write 20×r + output 30 = 85 + 20r
//
// r is the cache-creation ratio. The map default (1.25) is Anthropic's
// universal write surcharge; on the OpenAI wire (cache_write_tokens, parsed
// since 2026-09-02) it applies only to a model the operator listed (GPT-5.6 and
// later), otherwise the write is plain input. So:
//
//	OpenAI wire, model unlisted (ratio defaulted) → r = 1    → 105
//	OpenAI wire, model listed at 1.25             → r = 1.25 → 110
//	Anthropic wire, unlisted (default applies)    → r = 1.25 → 110
//
// Both settlement paths and the pre-settlement estimate must agree on every
// row: the estimate is what the caller sees in x_lurus before the charge.

import (
	"testing"

	"github.com/gin-gonic/gin"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestBilling_CacheWriteRatioIsKeyedOnWireAndListing(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()

	openAIWire := dto.Usage{
		PromptTokens: 120, CompletionTokens: 30, TotalTokens: 150,
		PromptTokensIncludeCached: true,
		PromptTokensDetails:       dto.InputTokenDetails{CachedTokens: 50, CachedCreationTokens: 20},
	}
	anthropicWire := dto.Usage{
		PromptTokens: 50, CompletionTokens: 30, TotalTokens: 80,
		PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 50, CachedCreationTokens: 20},
	}

	cases := []struct {
		name        string
		channelType int
		usage       dto.Usage
		defaulted   bool
		want        int
	}{
		{"openai wire, unlisted model: write at plain input rate", constant.ChannelTypeOpenAI, openAIWire, true, 105},
		{"openai wire, listed model (gpt-5.6 class): write at 1.25", constant.ChannelTypeOpenAI, openAIWire, false, 110},
		{"anthropic wire, unlisted model: vendor default 1.25 still applies", constant.ChannelTypeAnthropic, anthropicWire, true, 110},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			price := func(info *relaycommon.RelayInfo, c *gin.Context) {
				info.OriginModelName = "m"
				info.ChannelType = tc.channelType
				info.PriceData = types.PriceData{
					ModelRatio:                  1,
					CompletionRatio:             1,
					CacheRatio:                  0.1,
					CacheCreationRatio:          1.25,
					CacheCreationRatioDefaulted: tc.defaulted,
					GroupRatioInfo:              types.GroupRatioInfo{GroupRatio: 1},
				}
			}
			tag := "cw" + string(rune('a'+i))

			u1 := tc.usage
			if got := runPostConsumeQuota(t, tag+"-compat", price, &u1); got != tc.want {
				t.Errorf("compatible settlement charged %d, want %d", got, tc.want)
			}
			u2 := tc.usage
			if got := runSettlement(t, tag+"-claude", price, &u2, app.PostClaudeConsumeQuota); got != tc.want {
				t.Errorf("claude-path settlement charged %d, want %d", got, tc.want)
			}

			info := newRelayInfo(0, 0, constant.APITypeOpenAI)
			c, _ := newJSONContext("POST", "/", nil)
			price(info, c)
			u3 := tc.usage
			if got := helper.EstimateQuotaFromUsage(info, &u3); got != tc.want {
				t.Errorf("pre-settlement estimate = %d, want %d (must match the charge)", got, tc.want)
			}
		})
	}
}
