package relay

// postconsume_xai_cache_test.go — metadata to money for xAI, both transports.
// The same upstream numbers (prompt 120 including 50 cached, 30 output) must
// settle at the same figure whether or not the client streamed. Before
// 2026-09-01 non-stream settled at 175 (cached parsed, flag missing: full
// prompt plus cache price) and stream at 150 (cached dropped: full price),
// against a correct 105.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/xai"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

const xaiCachedUsage = `"usage":{"prompt_tokens":120,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":50}}`

func settleXai(t *testing.T, username string, stream bool) int {
	t.Helper()
	hc, _ := newJSONContext(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeChatCompletions,
		IsStream:    stream,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-4"},
	}
	var body string
	if stream {
		body = `data: {"id":"c1","model":"grok-4","choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
			`data: {"id":"c1","model":"grok-4","choices":[],` + xaiCachedUsage + `}` + "\n\n" + "data: [DONE]\n\n"
	} else {
		body = `{"id":"c1","model":"grok-4","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],` + xaiCachedUsage + `}`
	}
	resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
	if stream {
		resp.Header.Set("Content-Type", "text/event-stream")
	}
	raw, apiErr := (&xai.Adaptor{}).DoResponse(hc, resp, info)
	if apiErr != nil {
		t.Fatalf("xai DoResponse(stream=%v): %v", stream, apiErr.Error())
	}
	usage := raw.(*dto.Usage)

	return runPostConsumeQuota(t, username, func(info *relaycommon.RelayInfo, c *gin.Context) {
		info.OriginModelName = "grok-4"
		info.ChannelType = constant.ChannelTypeXai
		info.PriceData = types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			CacheRatio:      0.1,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
		}
	}, usage)
}

func TestPostConsumeQuota_XaiCachedTokens_SameChargeOnBothTransports(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()

	// StreamScannerHandler builds a time.NewTicker(StreamingTimeout*s); zero panics.
	prevTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 60
	defer func() { constant.StreamingTimeout = prevTimeout }()

	// quota = (120 - 50) base + 50*0.1 cache read + 30 completion = 70 + 5 + 30 = 105
	const want = 105
	if got := settleXai(t, "xai-cache-nonstream", false); got != want {
		t.Errorf("non-stream settled at %d, want %d (175 = flag missing: full prompt plus cache price)", got, want)
	}
	if got := settleXai(t, "xai-cache-stream", true); got != want {
		t.Errorf("stream settled at %d, want %d (150 = cached dropped: full input price)", got, want)
	}
}
