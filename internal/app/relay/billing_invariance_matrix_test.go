package relay

// billing_invariance_matrix_test.go — one semantic event, every upstream wire,
// every transport, both settlement paths: the price must not depend on which
// vendor served the request or how the bytes were framed.
//
// The event: a prompt whose effective size is 120 tokens, 50 of them served
// from the vendor's prompt cache, and a 30-token answer. Each vendor reports
// that event in its own dialect (DeepSeek prompt_cache_hit_tokens, Moonshot
// cached_tokens, Gemini cachedContentTokenCount, Anthropic input_tokens that
// EXCLUDE the cache read, …). The relay parses each dialect at exactly one
// site, and settlement keys its prompt-base deduction on the wire flag stamped
// there (dto.Usage.PromptTokensIncludeCached). At ModelRatio 1, CompletionRatio
// 1, CacheRatio 0.1 the event costs
//
//	(120 - 50) + 50 * 0.1 + 30 = 105
//
// on every row of the table below. Every earlier cache-token defect in this
// repo (#115, #122, #125, #126) was a single cell of this matrix being wrong
// while its neighbours were right; the per-vendor locks each pin one row, this
// file pins the claim across rows.
//
// The bodies are fed through the vendor's real Adaptor.DoResponse so the parse
// site under test is the production one, not a re-implementation.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/claude"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/deepseek"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/gemini"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/moonshot"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/openai"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/xai"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/zhipu_4v"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// The semantic event and its price at the matrix ratios.
const (
	matrixPromptTotal = 120 // effective prompt size, cached slice included
	matrixCached      = 50
	matrixOutput      = 30
	matrixWantQuota   = (matrixPromptTotal - matrixCached) + matrixCached/10 + matrixOutput // 105
)

type matrixRow struct {
	name        string
	channelType int
	relayMode   int
	adaptor     func() provider.Adaptor
	nonStream   string // upstream body, non-stream
	stream      string // upstream body, SSE
	// Client wire formats this handler serves. The caller-visible body is
	// checked for the cached figure in each format's own field.
	formats []types.RelayFormat
	// geminiNative: a Gemini-wire client on this upstream is served by the
	// native passthrough (RelayModeGemini), the only production path for it.
	geminiNative bool
}

func sseFrames(frames ...string) string {
	var b strings.Builder
	for _, f := range frames {
		b.WriteString("data: ")
		b.WriteString(f)
		b.WriteString("\n\n")
	}
	return b.String()
}

// OpenAI chat wire: prompt_tokens includes cached_tokens.
func oaiChatNonStream(usage string) string {
	return `{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":` + usage + `}`
}

func oaiChatStream(lastFrame string) string {
	return sseFrames(
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`,
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		lastFrame,
		"[DONE]",
	)
}

const oaiStandardUsage = `{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":50}}`

func matrixRows() []matrixRow {
	openaiChat := func() provider.Adaptor { return &openai.Adaptor{} }
	return []matrixRow{
		{
			name: "openai chat wire (prompt_tokens_details.cached_tokens)", channelType: constant.ChannelTypeOpenAI,
			relayMode: relayconstant.RelayModeChatCompletions, adaptor: openaiChat,
			nonStream: oaiChatNonStream(oaiStandardUsage),
			stream:    oaiChatStream(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":` + oaiStandardUsage + `}`),
			formats:   []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini},
		},
		{
			// DeepSeek documents prompt_tokens = prompt_cache_hit_tokens + prompt_cache_miss_tokens.
			name: "deepseek (prompt_cache_hit_tokens)", channelType: constant.ChannelTypeDeepSeek,
			relayMode: relayconstant.RelayModeChatCompletions, adaptor: func() provider.Adaptor { return &deepseek.Adaptor{} },
			nonStream: oaiChatNonStream(`{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,"prompt_cache_hit_tokens":50,"prompt_cache_miss_tokens":70}`),
			stream:    oaiChatStream(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,"prompt_cache_hit_tokens":50,"prompt_cache_miss_tokens":70}}`),
			formats:   []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatGemini},
		},
		{
			// The two Moonshot shapes the handler itself documents: usage.cached_tokens
			// on the non-stream body, choices[].usage.cached_tokens on the last chunk.
			name: "moonshot (cached_tokens off the standard slot)", channelType: constant.ChannelTypeMoonshot,
			relayMode: relayconstant.RelayModeChatCompletions, adaptor: func() provider.Adaptor { return &moonshot.Adaptor{} },
			nonStream: oaiChatNonStream(`{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,"cached_tokens":50}`),
			stream:    oaiChatStream(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop","usage":{"cached_tokens":50}}],"usage":{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150}}`),
			formats:   []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatGemini},
		},
		{
			name: "zhipu v4 (standard slot through its own adaptor)", channelType: constant.ChannelTypeZhipu_v4,
			relayMode: relayconstant.RelayModeChatCompletions, adaptor: func() provider.Adaptor { return &zhipu_4v.Adaptor{} },
			nonStream: oaiChatNonStream(oaiStandardUsage),
			stream:    oaiChatStream(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":` + oaiStandardUsage + `}`),
			formats:   []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatGemini},
		},
		{
			name: "xai (own handler, OpenAI wire)", channelType: constant.ChannelTypeXai,
			relayMode: relayconstant.RelayModeChatCompletions, adaptor: func() provider.Adaptor { return &xai.Adaptor{} },
			nonStream: oaiChatNonStream(`{"prompt_tokens":120,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":50}}`),
			stream:    oaiChatStream(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":120,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":50}}}`),
			formats:   []types.RelayFormat{types.RelayFormatOpenAI},
		},
		{
			name: "openai responses wire (input_tokens_details.cached_tokens)", channelType: constant.ChannelTypeOpenAI,
			relayMode: relayconstant.RelayModeResponses, adaptor: openaiChat,
			nonStream: `{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"m","output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":50},"output_tokens":30,"total_tokens":150}}`,
			stream: sseFrames(
				`{"type":"response.output_text.delta","delta":"hi"}`,
				`{"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"m","output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":50},"output_tokens":30,"total_tokens":150}}}`,
			),
			formats: []types.RelayFormat{types.RelayFormatOpenAIResponses},
		},
		{
			// Gemini: promptTokenCount includes the cached content.
			name: "gemini (cachedContentTokenCount)", channelType: constant.ChannelTypeGemini,
			relayMode: relayconstant.RelayModeChatCompletions, adaptor: func() provider.Adaptor { return &gemini.Adaptor{} },
			nonStream: `{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":30,"totalTokenCount":150,"cachedContentTokenCount":50}}`,
			stream: sseFrames(
				`{"candidates":[{"content":{"parts":[{"text":"h"}]},"index":0}]}`,
				`{"candidates":[{"content":{"parts":[{"text":"i"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":30,"totalTokenCount":150,"cachedContentTokenCount":50}}`,
			),
			formats:      []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini},
			geminiNative: true,
		},
		{
			// Anthropic: input_tokens EXCLUDES cache reads, so the same event is 70 + 50.
			name: "anthropic wire (input_tokens excludes cache_read_input_tokens)", channelType: constant.ChannelTypeAnthropic,
			relayMode: relayconstant.RelayModeChatCompletions,
			adaptor:   func() provider.Adaptor { return &claude.Adaptor{RequestMode: claude.RequestModeMessage} },
			nonStream: `{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":70,"cache_read_input_tokens":50,"output_tokens":30}}`,
			stream: sseFrames(
				`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","usage":{"input_tokens":70,"cache_read_input_tokens":50,"output_tokens":0}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":30}}`,
				`{"type":"message_stop"}`,
			),
			formats: []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude},
		},
	}
}

// parseThroughAdaptor runs the vendor's production DoResponse over the body and
// returns the usage it hands to settlement plus the bytes it forwarded.
func parseThroughAdaptor(t *testing.T, row matrixRow, stream bool, format types.RelayFormat) (*dto.Usage, string) {
	t.Helper()
	c, rec := newJSONContext(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		RelayMode:          row.relayMode,
		RelayFormat:        format,
		IsStream:           stream,
		ShouldIncludeUsage: true,                             // stream_options.include_usage: the only way an OpenAI-wire stream carries usage
		ClaudeConvertInfo:  &relaycommon.ClaudeConvertInfo{}, // request conversion sets this for Claude-wire clients
		ChannelMeta:        &relaycommon.ChannelMeta{ChannelType: row.channelType, UpstreamModelName: "m"},
	}
	if format == types.RelayFormatGemini && row.geminiNative {
		info.RelayMode = relayconstant.RelayModeGemini
	}
	body := row.nonStream
	if stream {
		body = row.stream
	}
	resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
	if stream {
		resp.Header.Set("Content-Type", "text/event-stream")
	}
	raw, apiErr := row.adaptor().DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("%s stream=%v format=%s: DoResponse: %v", row.name, stream, format, apiErr.Error())
	}
	usage, ok := raw.(*dto.Usage)
	if !ok || usage == nil {
		t.Fatalf("%s stream=%v: DoResponse returned %T, want *dto.Usage", row.name, stream, raw)
	}
	return usage, rec.Body.String()
}

// settleBoth returns the quota charged by each settlement path for one usage.
// compatible_handler.postConsumeQuota serves OpenAI/Gemini/Responses clients,
// app.PostClaudeConsumeQuota serves /v1/messages clients; the same upstream
// event reaches one or the other depending only on what the CLIENT spoke.
func settleBoth(t *testing.T, tag string, channelType int, usage *dto.Usage) (viaCompatible, viaClaude int) {
	t.Helper()
	price := func(info *relaycommon.RelayInfo, c *gin.Context) {
		info.OriginModelName = "m"
		info.ChannelType = channelType
		info.PriceData = types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			CacheRatio:      0.1,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
		}
	}
	u1 := *usage
	viaCompatible = runPostConsumeQuota(t, tag+"-compat", price, &u1)

	u2 := *usage
	viaClaude = runSettlement(t, tag+"-claude", price, &u2, app.PostClaudeConsumeQuota)
	return viaCompatible, viaClaude
}

// runSettlement is runPostConsumeQuota generalised over the settlement function.
func runSettlement(t *testing.T, username string, mutate func(*relaycommon.RelayInfo, *gin.Context), usage *dto.Usage,
	settle func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage)) int {
	t.Helper()
	const startQuota = 100_000_000
	u := &repo.User{Username: username, Quota: startQuota}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	info := newRelayInfo(u.Id, 0, constant.APITypeOpenAI)
	info.IsPlayground = true
	info.UserQuota = startQuota
	c, _ := newJSONContext(http.MethodPost, "/", nil)
	c.Set("token_name", "tkn")
	mutate(info, c)
	settle(c, info, usage)
	var refreshed repo.User
	if err := repo.DB.First(&refreshed, u.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	return refreshed.UsedQuota
}

func TestBillingInvariance_SameCacheEventCostsTheSameOnEveryWire(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()
	prevTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 60 // stream handlers build a ticker from it; zero panics
	defer func() { constant.StreamingTimeout = prevTimeout }()

	for i, row := range matrixRows() {
		for _, stream := range []bool{false, true} {
			name := row.name + "/non-stream"
			if stream {
				name = row.name + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				usage, _ := parseThroughAdaptor(t, row, stream, row.formats[0])
				tag := "mx" + string(rune('a'+i)) + map[bool]string{false: "n", true: "s"}[stream]
				viaCompat, viaClaude := settleBoth(t, tag, row.channelType, usage)
				if viaCompat != matrixWantQuota {
					t.Errorf("compatible settlement charged %d, want %d (usage %+v)", viaCompat, matrixWantQuota, *usage)
				}
				if viaClaude != matrixWantQuota {
					t.Errorf("claude-path settlement charged %d, want %d (usage %+v)", viaClaude, matrixWantQuota, *usage)
				}
			})
		}
	}
}

// The caller must be told about the cache hit in its own dialect, whichever
// vendor served it: the same figure, in the field that wire defines for it.
func TestBillingInvariance_CallerSeesTheCacheHitOnEveryWire(t *testing.T) {
	prevTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 60
	defer func() { constant.StreamingTimeout = prevTimeout }()

	cachedField := map[types.RelayFormat]string{
		types.RelayFormatOpenAI:          `"cached_tokens":50`,
		types.RelayFormatOpenAIResponses: `"cached_tokens":50`,
		types.RelayFormatClaude:          `"cache_read_input_tokens":50`,
		types.RelayFormatGemini:          `"cachedContentTokenCount":50`,
	}

	for _, row := range matrixRows() {
		for _, format := range row.formats {
			for _, stream := range []bool{false, true} {
				name := row.name + "/" + string(format) + map[bool]string{false: "/non-stream", true: "/stream"}[stream]
				t.Run(name, func(t *testing.T) {
					_, forwarded := parseThroughAdaptor(t, row, stream, format)
					if !strings.Contains(forwarded, cachedField[format]) {
						t.Errorf("caller body lacks %s:\n%s", cachedField[format], forwarded)
					}
				})
			}
		}
	}
}
