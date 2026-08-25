package relay

import (
	"net/http"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// runPostConsumeQuota drives postConsumeQuota against the hermetic SQLite store
// and returns the exact quota it billed. UpdateUserUsedQuotaAndRequestCount adds
// the computed quota verbatim to used_quota, so that column is the charge itself
// (unlike Quota/balance, which the settlement path also touches).
func runPostConsumeQuota(t *testing.T, username string, mutate func(*relaycommon.RelayInfo, *gin.Context), usage *dto.Usage) int {
	t.Helper()

	const startQuota = 100_000_000
	u := &repo.User{Username: username, Quota: startQuota}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	info := newRelayInfo(u.Id, 0, constant.APITypeOpenAI)
	info.IsPlayground = true // no token row in this harness; user quota still debited
	info.UserQuota = startQuota

	c, _ := newJSONContext(http.MethodPost, "/", nil)
	c.Set("token_name", "tkn")

	mutate(info, c)
	postConsumeQuota(c, info, usage)

	var refreshed repo.User
	if err := repo.DB.First(&refreshed, u.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	return refreshed.UsedQuota
}

// TestPostConsumeQuota_ClaudeWebSearchFeeIsCharged locks the Claude web-search
// tool fee into the billed quota. It used to be computed and written to the
// consume log's "other" map while never entering the Add chain, so the log said
// "charged" and the wallet never saw it.
//
// Numbers: ModelRatio 2 × GroupRatio 1 → (1000 prompt + 500×1 completion) × 2 =
// 3000. Claude web search = $10/1000 calls × 2 calls × QuotaPerUnit (500000) ×
// GroupRatio 1 = 10000. OtherRatios{surcharge: 2} additionally pins the fee's
// position BEFORE the OtherRatios multiply: (3000 + 10000) × 2 = 26000. Adding
// it after the multiply would give 16000.
func TestPostConsumeQuota_ClaudeWebSearchFeeIsCharged(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()

	usage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500}

	got := runPostConsumeQuota(t, "claude-websearch", func(info *relaycommon.RelayInfo, c *gin.Context) {
		info.OriginModelName = "claude-sonnet-4"
		info.PriceData = types.PriceData{
			ModelRatio:      2,
			CompletionRatio: 1,
			OtherRatios:     map[string]float64{"surcharge": 2},
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
		}
		c.Set("claude_web_search_requests", 2)
	}, usage)

	if got != 26000 {
		t.Errorf("billed quota = %d, want 26000 (3000 tokens + 10000 claude web search, ×2 other ratio)", got)
	}
}

// TestPostConsumeQuota_NoClaudeWebSearch_Unchanged proves the fee only lands
// when the upstream actually reported web-search calls.
func TestPostConsumeQuota_NoClaudeWebSearch_Unchanged(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()

	usage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500}

	got := runPostConsumeQuota(t, "no-websearch", func(info *relaycommon.RelayInfo, c *gin.Context) {
		info.OriginModelName = "gpt-4o"
		info.PriceData = types.PriceData{
			ModelRatio:      2,
			CompletionRatio: 1,
			OtherRatios:     map[string]float64{"surcharge": 2},
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
		}
		// no claude_web_search_requests key
	}, usage)

	if got != 6000 {
		t.Errorf("billed quota = %d, want 6000 (3000 tokens ×2 other ratio, no tool fee)", got)
	}
}

// TestPostConsumeQuota_CacheCreationBucketSplit locks the 5m/1h cache-write
// split onto the OpenAI-compatible path. cache_control passes through verbatim
// from an OpenAI-format client and the Claude adaptor fills both bucket fields,
// so charging every cache-creation token at the 5-minute ratio priced 1h writes
// below what the native /v1/messages path (app.PostClaudeConsumeQuota) charges
// for the identical upstream payload.
//
// Fixture: Anthropic channel (prompt_tokens excludes cache tokens, so no base
// subtraction), ModelRatio 1, GroupRatio 1, CacheCreationRatio == 5m ratio 1.25,
// 1h ratio 2.0 (= 1.25 × the 6/3.75 Anthropic multiplier).
func TestPostConsumeQuota_CacheCreationBucketSplit(t *testing.T) {
	cases := []struct {
		name      string
		total     int
		tokens5m  int
		tokens1h  int
		wantQuota int
		explain   string
	}{
		{
			name:  "1h bucket charged at the 1h ratio",
			total: 1000, tokens5m: 400, tokens1h: 600,
			wantQuota: 1800, explain: "100 base + 400×1.25 + 600×2.0",
		},
		{
			name:  "partial split leaves a remainder at the flat ratio",
			total: 1000, tokens5m: 400, tokens1h: 100,
			wantQuota: 1425, explain: "100 base + 400×1.25 + 100×2.0 + 500×1.25",
		},
		{
			// Providers that report only the total (i.e. every non-Claude
			// upstream) must bill exactly as before this split existed.
			name:  "total only, no buckets reported",
			total: 1000, tokens5m: 0, tokens1h: 0,
			wantQuota: 1350, explain: "100 base + 1000×1.25",
		},
		{
			name:  "no cache creation at all",
			total: 0, tokens5m: 0, tokens1h: 0,
			wantQuota: 100, explain: "100 base only",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cleanup := setupRelayDB(t)
			defer cleanup()

			usage := &dto.Usage{PromptTokens: 100, CompletionTokens: 0, TotalTokens: 100}
			usage.PromptTokensDetails.CachedCreationTokens = tc.total
			usage.ClaudeCacheCreation5mTokens = tc.tokens5m
			usage.ClaudeCacheCreation1hTokens = tc.tokens1h

			got := runPostConsumeQuota(t, "cache-"+tc.name, func(info *relaycommon.RelayInfo, c *gin.Context) {
				info.OriginModelName = "claude-sonnet-4"
				info.ChannelType = constant.ChannelTypeAnthropic
				info.PriceData = types.PriceData{
					ModelRatio:           1,
					CompletionRatio:      1,
					CacheCreationRatio:   1.25,
					CacheCreation5mRatio: 1.25,
					CacheCreation1hRatio: 2.0,
					GroupRatioInfo:       types.GroupRatioInfo{GroupRatio: 1},
				}
			}, usage)

			if got != tc.wantQuota {
				t.Errorf("billed quota = %d, want %d (%s)", got, tc.wantQuota, tc.explain)
			}
		})
	}
}
