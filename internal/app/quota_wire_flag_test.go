package app

// quota_wire_flag_test.go — pins the wire-flag prompt-base deduction on the
// Claude settlement path (PostClaudeConsumeQuota). Before 2026-08-29 the
// deduction was gated on ChannelType==OpenRouter, so every OTHER OpenAI-wire
// channel serving /v1/messages (OpenAI, Azure, Ollama, …) had its cached
// tokens billed at full input price PLUS CacheRatio — this test computes the
// old wrong number in its failure message and is red against that code.

import (
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// TestPostClaudeConsumeQuota_OpenAIWireSubtractsCache: Claude-format request
// served by a plain OpenAI channel — the usage came from
// provider/openai/relay-openai.go, whose prompt_tokens INCLUDES cached_tokens
// (flag stamped there). The cached tokens must be priced ONCE at CacheRatio,
// not once at full price plus once at CacheRatio.
func TestPostClaudeConsumeQuota_OpenAIWireSubtractsCache(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 10_000_000)
	key, tokenId := seedTestToken(t, db, userId, 10_000_000, false)

	c := createTestGinContext()
	usage := &dto.Usage{
		PromptTokens:              1000, // includes the 200 cached
		CompletionTokens:          100,
		TotalTokens:               1100,
		PromptTokensIncludeCached: true,
		PromptTokensDetails:       dto.InputTokenDetails{CachedTokens: 200},
	}
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: "oai-claude-format-model",
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		PriceData: types.PriceData{
			ModelRatio:      2.0,
			CompletionRatio: 3.0,
			CacheRatio:      0.5,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	// promptTokens = 1000 - 200(cache) = 800
	// quota = (800 + 200*0.5 + 100*3) * 2 = (800 + 100 + 300) * 2 = 2400
	// The pre-fix ChannelType-gated code did NOT subtract on ChannelTypeOpenAI
	// and produced (1000 + 100 + 300) * 2 = 2800 — cached tokens billed at
	// full input price on top of the cache price.
	const want = 2400
	before := userQuota(t, db, userId)
	PostClaudeConsumeQuota(c, relayInfo, usage)
	if got := before - userQuota(t, db, userId); got != want {
		t.Errorf("OpenAI-wire Claude-format debited %d, want %d (2800 = the old double-charge)", got, want)
	}
}

// TestPostClaudeConsumeQuota_AnthropicWireNoSubtraction: the flag left false
// (Anthropic wire — input_tokens already exclude cache reads) must keep the
// pre-existing no-subtraction behaviour on a NON-Anthropic channel type
// (ali/zhipu_4v/deepseek/moonshot Claude passthroughs produce exactly this
// shape). Guards the other direction: keying on the flag must not
// accidentally start subtracting for Anthropic-wire usage.
func TestPostClaudeConsumeQuota_AnthropicWireNoSubtraction(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 10_000_000)
	key, tokenId := seedTestToken(t, db, userId, 10_000_000, false)

	c := createTestGinContext()
	usage := &dto.Usage{
		PromptTokens:     800, // EXCLUDES the 200 cached (Anthropic wire)
		CompletionTokens: 100,
		TotalTokens:      900,
		// PromptTokensIncludeCached left false — Anthropic-wire default.
		PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 200},
	}
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: "ali-claude-passthrough-model",
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAli},
		PriceData: types.PriceData{
			ModelRatio:      2.0,
			CompletionRatio: 3.0,
			CacheRatio:      0.5,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	// quota = (800 + 200*0.5 + 100*3) * 2 = 2400 — same money as the
	// OpenAI-wire case above for the same real usage, which is the whole
	// point: the wire encoding must not change the price.
	const want = 2400
	before := userQuota(t, db, userId)
	PostClaudeConsumeQuota(c, relayInfo, usage)
	if got := before - userQuota(t, db, userId); got != want {
		t.Errorf("Anthropic-wire on Ali channel debited %d, want %d", got, want)
	}
}
