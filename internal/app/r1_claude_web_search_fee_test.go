package app

// r1_claude_web_search_fee_test.go — R1 lane, F1. claude_web_search_requests
// is written by the Claude provider adapter
// (internal/adapter/provider/claude/relay-claude.go:817, inside
// HandleClaudeResponseData) but before this fix was read by exactly one
// non-test site: relay/compatible_handler.go:280. That means a Claude
// web-search call made through the native /v1/messages path
// (app.PostClaudeConsumeQuota) was never charged for the tool call — it only
// ever got logged on the OpenAI-compatible path. This locks in that
// PostClaudeConsumeQuota now charges the same formula
// (relay/compatible_handler.go:280-286) and writes the same three `other`
// blob keys the frontend already reads
// (web/src/hooks/usage-logs/useUsageLogsData.jsx:382-383,459-461).
//
// Scope note (updated 2026-08-27, lane L-A / G2): HandleClaudeResponseData is
// only reached by the NON-streaming callers (provider/claude/relay-claude.go
// ClaudeHandler, and provider/aws/relay-aws.go's awsHandler). At the time this
// file was first written the streaming completion path (ClaudeStreamHandler
// -> HandleStreamFinalResponse) never set claude_web_search_requests, so a
// streamed /v1/messages web-search call went unbilled. That gap is now closed
// by a second, symmetric c.Set in HandleStreamFinalResponse
// (provider/claude/relay-claude.go) — see
// provider/claude/r5a_stream_web_search_test.go for the streaming-path lock.
// This file only ever drove PostClaudeConsumeQuota directly with the context
// key pre-set, so it never exercised either write site; it stays as the
// "does the fee formula debit and log correctly once the key is set"
// coverage, independent of which path set that key.

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/shopspring/decimal"
)

// TestR1ClaudeNativeWebSearchIsCharged drives PostClaudeConsumeQuota with the
// claude_web_search_requests context key set (mirrors what
// relay-claude.go:817 sets) and asserts the fee is added to the debited quota
// AND recorded in the log's `other` blob.
func TestR1ClaudeNativeWebSearchIsCharged(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 1_000_000)
	key, tokenId := seedTestToken(t, db, userId, 1_000_000, false)

	c := createTestGinContext()
	c.Set("token_name", "tkn")
	c.Set("claude_web_search_requests", 2)

	usage := &dto.Usage{
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: "claude-3-5-sonnet",
		StartTime:       time.Now().Add(-time.Second),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio:      1.0,
			CompletionRatio: 1.0,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	// base = (10 + 5*1) * 1 * 1 = 15
	// fee (hand-computed from the same formula the production code uses, per
	// compatible_handler.go:280-286): price/1000 * groupRatio * QuotaPerUnit * callCount
	price := operation_setting.GetClaudeWebSearchPricePerThousand()
	fee := decimal.NewFromFloat(price).
		Div(decimal.NewFromInt(1000)).
		Mul(decimal.NewFromFloat(1.0)).
		Mul(decimal.NewFromFloat(500000)). // common.QuotaPerUnit
		Mul(decimal.NewFromInt(2))
	wantQuota := 15 + int(fee.Round(0).IntPart())

	before := userQuota(t, db, userId)
	PostClaudeConsumeQuota(c, relayInfo, usage)
	after := userQuota(t, db, userId)

	if got := before - after; got != wantQuota {
		t.Errorf("Claude debited %d, want %d (base 15 + web search fee %s)", got, wantQuota, fee.String())
	}

	var logRow repo.Log
	if err := db.Where("user_id = ? AND type = ?", userId, repo.LogTypeConsume).First(&logRow).Error; err != nil {
		t.Fatalf("no consume log written: %v", err)
	}

	// logContent (quota.go:377) is the only human-readable record of the call
	// count and fee for admins reading the raw consume log Content column
	// (the structured other[] blob asserted below is a separate column, not a
	// substitute) — must name both the call count and the computed fee.
	wantContent := fmt.Sprintf("Claude Web Search 调用 %d 次，调用花费 %s", 2, fee.String())
	if logRow.Content != wantContent {
		t.Errorf("consume log Content = %q, want %q", logRow.Content, wantContent)
	}

	var other map[string]interface{}
	if err := json.Unmarshal([]byte(logRow.Other), &other); err != nil {
		t.Fatalf("consume log Other blob is not valid JSON: %v (raw=%q)", err, logRow.Other)
	}
	if v, ok := other["web_search"].(bool); !ok || !v {
		t.Errorf("other[web_search] = %v, want true", other["web_search"])
	}
	if v, ok := other["web_search_call_count"].(float64); !ok || int(v) != 2 {
		t.Errorf("other[web_search_call_count] = %v, want 2", other["web_search_call_count"])
	}
	if v, ok := other["web_search_price"].(float64); !ok || v != price {
		t.Errorf("other[web_search_price] = %v, want %v", other["web_search_price"], price)
	}
}

// TestR1ClaudeNativeWebSearchNotChargedWithoutContextKey is the companion
// negative: no claude_web_search_requests context key set => quota is only
// the base amount, and the `other` blob carries NO web_search key at all
// (locks that we don't start emitting a spurious tag on every Claude call).
func TestR1ClaudeNativeWebSearchNotChargedWithoutContextKey(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 1_000_000)
	key, tokenId := seedTestToken(t, db, userId, 1_000_000, false)

	c := createTestGinContext()
	c.Set("token_name", "tkn")
	// deliberately NOT setting claude_web_search_requests

	usage := &dto.Usage{
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
	}

	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: "claude-3-5-sonnet",
		StartTime:       time.Now().Add(-time.Second),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio:      1.0,
			CompletionRatio: 1.0,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	const wantQuota = 15 // base only, no fee

	before := userQuota(t, db, userId)
	PostClaudeConsumeQuota(c, relayInfo, usage)
	after := userQuota(t, db, userId)

	if got := before - after; got != wantQuota {
		t.Errorf("Claude debited %d, want %d (no web search call, no fee)", got, wantQuota)
	}

	var logRow repo.Log
	if err := db.Where("user_id = ? AND type = ?", userId, repo.LogTypeConsume).First(&logRow).Error; err != nil {
		t.Fatalf("no consume log written: %v", err)
	}
	var other map[string]interface{}
	if err := json.Unmarshal([]byte(logRow.Other), &other); err != nil {
		t.Fatalf("consume log Other blob is not valid JSON: %v (raw=%q)", err, logRow.Other)
	}
	if _, present := other["web_search"]; present {
		t.Errorf("other[web_search] present = %v, want key absent entirely when no web search calls happened", other["web_search"])
	}
}
