package app

// wire_log_parity_test.go — the consume log must carry the RAW upstream
// prompt_tokens on the Claude-native settlement path, while the MONEY keeps
// using the cache-reduced base.
//
// Until 2026-09-01 PostClaudeConsumeQuota decremented `promptTokens` in place
// and then handed that same decremented variable to RecordConsumeLogParams, so
// one physical request produced two irreconcilable log rows depending on which
// wire format the client happened to use: measured on UAT the same prompt
// logged prompt_tokens=3127 through /v1/chat/completions
// (relay/compatible_handler.go keeps a separate `baseTokens`) and
// prompt_tokens=55 through /v1/messages, with identical quota on both rows.
// Any per-token capacity or cost analysis under-counted input by exactly the
// cached amount.
//
// Mutation oracle: change `billablePromptTokens := promptTokens` back to an
// in-place `promptTokens -= cacheTokens` and the log assertion goes red while
// the quota assertion stays green — the exact shape that hid this for months
// (the charge was always right).

import (
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestPostClaudeConsumeQuota_LogsRawPromptTokensButBillsReducedBase(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 10_000_000)
	key, tokenId := seedTestToken(t, db, userId, 10_000_000, false)

	c := createTestGinContext()
	const (
		rawPromptTokens = 1000 // as reported by the upstream, INCLUDES the cached 200
		cachedTokens    = 200
		completionTok   = 100
	)
	usage := &dto.Usage{
		PromptTokens:              rawPromptTokens,
		CompletionTokens:          completionTok,
		TotalTokens:               rawPromptTokens + completionTok,
		PromptTokensIncludeCached: true,
		PromptTokensDetails:       dto.InputTokenDetails{CachedTokens: cachedTokens},
	}
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: "wire-log-parity-model",
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		PriceData: types.PriceData{
			ModelRatio:      2.0,
			CompletionRatio: 3.0,
			CacheRatio:      0.5,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	before := userQuota(t, db, userId)
	PostClaudeConsumeQuota(c, relayInfo, usage)

	// Money leg — unchanged by this fix. Billing base is 1000-200 = 800:
	// (800 + 200*0.5 + 100*3) * 2 = 2400.
	const wantQuota = 2400
	if got := before - userQuota(t, db, userId); got != wantQuota {
		t.Errorf("debited %d, want %d — the reporting fix must not move any money", got, wantQuota)
	}

	// Reporting leg — the whole point of this test.
	var row repo.Log
	if err := repo.LOG_DB.Where("user_id = ? AND model_name = ?", userId, "wire-log-parity-model").
		Order("id desc").First(&row).Error; err != nil {
		t.Fatalf("no consume log row recorded: %v", err)
	}
	if row.PromptTokens != rawPromptTokens {
		t.Errorf("log prompt_tokens = %d, want %d (the raw upstream figure). Got %d = the cache-reduced "+
			"billing base leaking into the log, which makes /v1/messages rows impossible to reconcile "+
			"with /v1/chat/completions rows for the same request",
			row.PromptTokens, rawPromptTokens, row.PromptTokens)
	}
	if row.Quota != wantQuota {
		t.Errorf("log quota = %d, want %d", row.Quota, wantQuota)
	}
}

// TestPostClaudeConsumeQuota_AnthropicWireLogsUnchanged is the negative control:
// on Anthropic wire (input_tokens already exclude cache) nothing is deducted,
// so raw and billable coincide and the log must be identical to before.
func TestPostClaudeConsumeQuota_AnthropicWireLogsUnchanged(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 10_000_000)
	key, tokenId := seedTestToken(t, db, userId, 10_000_000, false)

	c := createTestGinContext()
	usage := &dto.Usage{
		PromptTokens:              800, // EXCLUDES the 200 cached (Anthropic wire)
		CompletionTokens:          100,
		TotalTokens:               900,
		PromptTokensIncludeCached: false,
		PromptTokensDetails:       dto.InputTokenDetails{CachedTokens: 200},
	}
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: "wire-log-parity-anthropic",
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAnthropic},
		PriceData: types.PriceData{
			ModelRatio:      2.0,
			CompletionRatio: 3.0,
			CacheRatio:      0.5,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	PostClaudeConsumeQuota(c, relayInfo, usage)

	var row repo.Log
	if err := repo.LOG_DB.Where("user_id = ? AND model_name = ?", userId, "wire-log-parity-anthropic").
		Order("id desc").First(&row).Error; err != nil {
		t.Fatalf("no consume log row recorded: %v", err)
	}
	if row.PromptTokens != 800 {
		t.Errorf("log prompt_tokens = %d, want 800 — Anthropic-wire usage is never reduced, so the "+
			"logged value must equal the reported one", row.PromptTokens)
	}
}
