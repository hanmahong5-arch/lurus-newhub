package relay

// postconsume_gemini_cache_test.go — metadata to money, end to end: a Gemini
// upstream reply carrying cachedContentTokenCount must settle at the cache
// discount, and the cached slice must be subtracted from the prompt base
// exactly once.
//
// This is the chain the unit locks in provider/gemini cannot see: the parse
// site stamps CachedTokens and the wire flag, and postConsumeQuota keys its
// prompt-base deduction on that flag. Before 2026-09-01 the DTO had no field
// for the figure at all, so the same reply billed 1005 here instead of 375.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/gemini"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestPostConsumeQuota_GeminiCachedContentSettlesAtCacheRatio(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()

	// Parse a Gemini reply the way the relay does, through the real handler.
	w := httptest.NewRecorder()
	hc, _ := gin.CreateTestContext(w)
	hc.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	upstream := &http.Response{
		StatusCode: 200,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP","index":0}],
			"usageMetadata":{"promptTokenCount":1000,"candidatesTokenCount":5,"totalTokenCount":1005,"cachedContentTokenCount":700}
		}`)),
	}
	usage, apiErr := gemini.GeminiChatHandler(hc, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-flash"},
		RelayFormat: types.RelayFormatOpenAI,
	}, upstream)
	if apiErr != nil {
		t.Fatalf("gemini handler: %v", apiErr)
	}

	got := runPostConsumeQuota(t, "gemini-cache-user", func(info *relaycommon.RelayInfo, c *gin.Context) {
		info.OriginModelName = "gemini-2.5-flash"
		info.ChannelType = constant.ChannelTypeGemini
		info.PriceData = types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			CacheRatio:      0.1,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
		}
	}, usage)

	// quota = (1000 - 700) base + 700*0.1 cache read + 5*1 completion
	//       = 300 + 70 + 5 = 375
	// Pre-fix: cachedContentTokenCount was not parsed -> 1000 + 5 = 1005, the
	// cache hit billed at full input price. A flag-less parse would give
	// 1000 + 70 + 5 = 1075 — full price PLUS the cache price.
	const want = 375
	if got != want {
		t.Errorf("Gemini cached reply settled at %d, want %d (1005 = cache ignored, 1075 = flag missing)", got, want)
	}
}
