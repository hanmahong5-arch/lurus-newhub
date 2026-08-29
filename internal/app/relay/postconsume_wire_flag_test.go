package relay

// postconsume_wire_flag_test.go — pins the wire-flag prompt-base deduction on
// the OpenAI-compatible settlement path (postConsumeQuota). Before 2026-08-29
// the deduction was gated on ChannelType != Anthropic, which subtracted cache
// tokens the wire never included for OpenAI-format requests served by
// aws/vertex-claude (Anthropic-wire usage) — driving the prompt base negative
// on cache-heavy calls and flooring the whole charge to 1 (a free call).

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// TestPostConsumeQuota_AnthropicWireOnAwsNoSubtraction: an OpenAI-format
// request served by a Bedrock channel — the usage came from
// provider/claude/relay-claude.go (AWS's DoResponse never branches on relay
// format), so input_tokens EXCLUDE the cache reads and creations. Nothing may
// be subtracted from the base.
func TestPostConsumeQuota_AnthropicWireOnAwsNoSubtraction(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()

	usage := &dto.Usage{
		PromptTokens:     100, // EXCLUDES cache reads and creations (Anthropic wire)
		CompletionTokens: 10,
		TotalTokens:      110,
		// PromptTokensIncludeCached left false — the claude parse site never
		// sets it.
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         50,
			CachedCreationTokens: 40,
		},
	}

	got := runPostConsumeQuota(t, "aws-anthropic-wire", func(info *relaycommon.RelayInfo, c *gin.Context) {
		info.OriginModelName = "claude-sonnet-4"
		info.ChannelType = constant.ChannelTypeAws
		info.PriceData = types.PriceData{
			ModelRatio:         1,
			CompletionRatio:    1,
			CacheRatio:         0.5,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: 1},
		}
	}, usage)

	// quota = 100 base + 50*0.5(cache read) + 40*1.25(cache write) + 10*1
	//       = 100 + 25 + 50 + 10 = 185
	// The pre-fix ChannelType-gated code subtracted BOTH cache dimensions the
	// wire never included: (100-50-40) + 25 + 50 + 10 = 95 — and on a
	// cache-heavy call (C+K > P) the base went negative and the whole charge
	// floored to 1.
	const want = 185
	if got != want {
		t.Errorf("Aws Anthropic-wire billed %d, want %d (95 = the old under-charge)", got, want)
	}
}

// TestPostConsumeQuota_AnthropicWireCacheHeavyNotFree: the worst case of the
// old bug — cache dimensions larger than the reported prompt base. Old code:
// base = 100-90-80 < 0, total floored to 1 ⇒ free call. Must now bill in full.
func TestPostConsumeQuota_AnthropicWireCacheHeavyNotFree(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 0,
		TotalTokens:      100,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         90,
			CachedCreationTokens: 80,
		},
	}

	got := runPostConsumeQuota(t, "aws-cache-heavy", func(info *relaycommon.RelayInfo, c *gin.Context) {
		info.OriginModelName = "claude-sonnet-4"
		info.ChannelType = constant.ChannelTypeAws
		info.PriceData = types.PriceData{
			ModelRatio:         1,
			CompletionRatio:    1,
			CacheRatio:         0.5,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: 1},
		}
	}, usage)

	// quota = 100 + 90*0.5 + 80*1.25 = 100 + 45 + 100 = 245
	const want = 245
	if got != want {
		t.Errorf("cache-heavy Aws call billed %d, want %d (1 = the old floored free call)", got, want)
	}
}

// TestPostConsumeQuota_OpenAIWireStillSubtracts: the flag=true arm must keep
// the pre-existing OpenAI-wire behaviour byte-for-byte (prompt_tokens include
// cached ⇒ subtract before pricing at CacheRatio). Guards against the fix
// regressing the correct combinations.
func TestPostConsumeQuota_OpenAIWireStillSubtracts(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()

	usage := &dto.Usage{
		PromptTokens:              100, // includes the 50 cached
		CompletionTokens:          10,
		TotalTokens:               110,
		PromptTokensIncludeCached: true,
		PromptTokensDetails:       dto.InputTokenDetails{CachedTokens: 50},
	}

	got := runPostConsumeQuota(t, "openai-wire-subtract", func(info *relaycommon.RelayInfo, c *gin.Context) {
		info.OriginModelName = "gpt-4o"
		info.ChannelType = constant.ChannelTypeOpenAI
		info.PriceData = types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			CacheRatio:      0.5,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
		}
	}, usage)

	// quota = (100-50) + 50*0.5 + 10 = 50 + 25 + 10 = 85
	const want = 85
	if got != want {
		t.Errorf("OpenAI-wire billed %d, want %d", got, want)
	}
}
