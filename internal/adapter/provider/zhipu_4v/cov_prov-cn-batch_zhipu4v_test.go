package zhipu_4v

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	pkgconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func init() {
	if pkgconstant.StreamingTimeout <= 0 {
		pkgconstant.StreamingTimeout = 60
	}
}

func prov_cn_batch_zhipu4vGinContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return c, w
}

func prov_cn_batch_zhipu4vRespFromBody(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func prov_cn_batch_zhipu4vSSEBody(frames ...string) string {
	var b strings.Builder
	for _, f := range frames {
		b.WriteString("data: ")
		b.WriteString(f)
		b.WriteString("\n\n")
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Unimplemented conversion paths must fail loudly.
// ---------------------------------------------------------------------------

func TestAdaptor_UnimplementedConversions(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_zhipu4vGinContext()

	t.Run("ConvertGeminiRequest", func(t *testing.T) {
		out, err := a.ConvertGeminiRequest(c, &relaycommon.RelayInfo{}, &dto.GeminiChatRequest{})
		if err == nil || out != nil {
			t.Errorf("expected (nil, error), got (%v, %v)", out, err)
		}
	})

	t.Run("ConvertAudioRequest", func(t *testing.T) {
		out, err := a.ConvertAudioRequest(c, &relaycommon.RelayInfo{}, dto.AudioRequest{})
		if err == nil || out != nil {
			t.Errorf("expected (nil, error), got (%v, %v)", out, err)
		}
	})

	t.Run("ConvertOpenAIResponsesRequest", func(t *testing.T) {
		out, err := a.ConvertOpenAIResponsesRequest(c, &relaycommon.RelayInfo{}, dto.OpenAIResponsesRequest{})
		if err == nil || out != nil {
			t.Errorf("expected (nil, error), got (%v, %v)", out, err)
		}
	})
}

func TestAdaptor_ConvertClaudeRequest_Passthrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_zhipu4vGinContext()
	req := &dto.ClaudeRequest{Model: "glm-4v"}
	out, err := a.ConvertClaudeRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.(*dto.ClaudeRequest) != req {
		t.Errorf("expected the same *dto.ClaudeRequest pointer to pass through unmodified")
	}
}

func TestAdaptor_ConvertImageRequest_Passthrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_zhipu4vGinContext()
	req := dto.ImageRequest{Prompt: "a cat", Model: "cogview-3"}
	out, err := a.ConvertImageRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.(dto.ImageRequest)
	if got.Prompt != "a cat" {
		t.Errorf("Prompt = %q, want passthrough of 'a cat'", got.Prompt)
	}
}

func TestAdaptor_Init_NoOp(t *testing.T) {
	a := &Adaptor{}
	a.Init(&relaycommon.RelayInfo{}) // must not panic
}

// ---------------------------------------------------------------------------
// Adaptor.GetRequestURL — default base, special coding-plan overrides, modes
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL(t *testing.T) {
	a := &Adaptor{}

	t.Run("empty ChannelBaseUrl falls back to the Zhipu v4 default host", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://open.bigmodel.cn/api/paas/v4/chat/completions" {
			t.Errorf("url = %q, want default-host chat completions endpoint", url)
		}
	})

	t.Run("embeddings relay mode", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://custom.zhipu.example"}}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://custom.zhipu.example/api/paas/v4/embeddings" {
			t.Errorf("url = %q, want embeddings endpoint", url)
		}
	})

	t.Run("image generation relay mode", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://custom.zhipu.example"}}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://custom.zhipu.example/api/paas/v4/images/generations" {
			t.Errorf("url = %q, want images/generations endpoint", url)
		}
	})

	t.Run("Claude relay format without special plan uses anthropic-compat path", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://custom.zhipu.example"}}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://custom.zhipu.example/api/anthropic/v1/messages" {
			t.Errorf("url = %q, want anthropic-compat messages endpoint", url)
		}
	})

	t.Run("known coding-plan base URL redirects chat completions to the special OpenAI base", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "glm-coding-plan"}}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://open.bigmodel.cn/api/coding/paas/v4/chat/completions" {
			t.Errorf("url = %q, want coding-plan OpenAI base redirected chat endpoint", url)
		}
	})

	t.Run("known coding-plan base URL redirects Claude format to the special Claude base", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "glm-coding-plan"}}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://open.bigmodel.cn/api/anthropic/v1/messages" {
			t.Errorf("url = %q, want coding-plan Claude base redirected messages endpoint", url)
		}
	})

	t.Run("coding-plan base with embeddings mode also redirects to the special OpenAI base", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "glm-coding-plan"}}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://open.bigmodel.cn/api/coding/paas/v4/embeddings" {
			t.Errorf("url = %q, want coding-plan OpenAI base redirected embeddings endpoint", url)
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.SetupRequestHeader
// ---------------------------------------------------------------------------

func TestAdaptor_SetupRequestHeader(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_zhipu4vGinContext()
	req := &http.Header{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-zhipu4v-key"}}
	if err := a.SetupRequestHeader(c, req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Get("Authorization") != "Bearer sk-zhipu4v-key" {
		t.Errorf("Authorization = %q, want Bearer sk-zhipu4v-key", req.Get("Authorization"))
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertOpenAIRequest — TopP clamp + nil check
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertOpenAIRequest_NilRejectedAndTopPClamped(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_zhipu4vGinContext()

	t.Run("nil request rejected", func(t *testing.T) {
		_, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, nil)
		if err == nil {
			t.Fatal("expected error for nil request")
		}
	})

	t.Run("TopP >= 1 clamped to 0.99", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "glm-4v", TopP: 1.0}
		out, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := out.(*dto.GeneralOpenAIRequest)
		if got.TopP != 0.99 {
			t.Errorf("TopP = %v, want 0.99", got.TopP)
		}
	})
}

func TestAdaptor_ConvertRerankRequest_NotSupported(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_zhipu4vGinContext()
	out, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	if out != nil || err != nil {
		t.Errorf("expected (nil, nil), got (%v, %v)", out, err)
	}
}

func TestAdaptor_ConvertEmbeddingRequest_Passthrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_zhipu4vGinContext()
	req := dto.EmbeddingRequest{Model: "embedding-2"}
	out, err := a.ConvertEmbeddingRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.(dto.EmbeddingRequest).Model != "embedding-2" {
		t.Errorf("expected embedding request passthrough, got %+v", out)
	}
}

// ---------------------------------------------------------------------------
// requestOpenAI2Zhipu — multimodal image base64 stripping, Stop mapping
// ---------------------------------------------------------------------------

func TestRequestOpenAI2Zhipu(t *testing.T) {
	t.Run("string content passes through unaltered", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Messages: []dto.Message{{Role: "user", Content: "hello"}},
		}
		out := requestOpenAI2Zhipu(req)
		if out.Messages[0].Content != "hello" {
			t.Errorf("Content = %v, want hello", out.Messages[0].Content)
		}
	})

	t.Run("base64 data-URI image content has the media-type prefix stripped", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Messages: []dto.Message{
				{Role: "user", Content: []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,QUFBQQ=="}},
				}},
			},
		}
		out := requestOpenAI2Zhipu(req)
		media, ok := out.Messages[0].Content.([]dto.MediaContent)
		if !ok {
			t.Fatalf("expected []dto.MediaContent, got %T", out.Messages[0].Content)
		}
		img := media[0].GetImageMedia()
		if img == nil {
			t.Fatal("expected non-nil image media")
		}
		if img.Url != "QUFBQQ==" {
			t.Errorf("Url = %q, want base64 payload with data-URI prefix stripped", img.Url)
		}
	})

	t.Run("remote (non-data-URI) image URL left untouched", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Messages: []dto.Message{
				{Role: "user", Content: []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/cat.png"}},
				}},
			},
		}
		out := requestOpenAI2Zhipu(req)
		media := out.Messages[0].Content.([]dto.MediaContent)
		img := media[0].GetImageMedia()
		if img.Url != "https://example.com/cat.png" {
			t.Errorf("Url = %q, want remote URL untouched", img.Url)
		}
	})

	t.Run("Stop as string wrapped into single-element slice", func(t *testing.T) {
		out := requestOpenAI2Zhipu(dto.GeneralOpenAIRequest{Stop: "STOP_WORD"})
		if len(out.Stop.([]string)) != 1 || out.Stop.([]string)[0] != "STOP_WORD" {
			t.Errorf("Stop = %v, want [\"STOP_WORD\"]", out.Stop)
		}
	})

	t.Run("Stop as []string passed through", func(t *testing.T) {
		out := requestOpenAI2Zhipu(dto.GeneralOpenAIRequest{Stop: []string{"a", "b"}})
		stops, ok := out.Stop.([]string)
		if !ok || len(stops) != 2 {
			t.Errorf("Stop = %v, want [\"a\",\"b\"]", out.Stop)
		}
	})

	t.Run("Stop nil (unset) yields an empty (nil) []string, no panic", func(t *testing.T) {
		out := requestOpenAI2Zhipu(dto.GeneralOpenAIRequest{})
		stops, ok := out.Stop.([]string)
		if !ok {
			t.Fatalf("Stop = %#v (%T), want a []string-typed value even when unset", out.Stop, out.Stop)
		}
		if len(stops) != 0 {
			t.Errorf("Stop = %v, want empty for unset stop", stops)
		}
	})

	t.Run("MaxTokens / model / stream propagated via GetMaxTokens", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{Model: "glm-4v", Stream: true, MaxCompletionTokens: 99}
		out := requestOpenAI2Zhipu(req)
		if out.Model != "glm-4v" {
			t.Errorf("Model = %q, want glm-4v", out.Model)
		}
		if !out.Stream {
			t.Error("expected Stream=true propagated")
		}
		if out.MaxTokens != 99 {
			t.Errorf("MaxTokens = %d, want 99 (from MaxCompletionTokens via GetMaxTokens)", out.MaxTokens)
		}
	})

	t.Run("empty messages produce empty slice, no panic", func(t *testing.T) {
		out := requestOpenAI2Zhipu(dto.GeneralOpenAIRequest{Messages: []dto.Message{}})
		if len(out.Messages) != 0 {
			t.Errorf("expected zero messages, got %d", len(out.Messages))
		}
	})
}

// ---------------------------------------------------------------------------
// zhipu4vImageHandler — image generation response translation + billing
// ---------------------------------------------------------------------------

func TestZhipu4vImageHandler(t *testing.T) {
	t.Run("b64_json data forwarded directly, no download needed", func(t *testing.T) {
		c, w := prov_cn_batch_zhipu4vGinContext()
		created := int64(1700000000)
		info := &relaycommon.RelayInfo{}
		body := `{"created":1700000000,"data":[{"url":"https://cdn.example/img.png","b64_json":"QUJD"}]}`
		usage, apiErr := zhipu4vImageHandler(c, prov_cn_batch_zhipu4vRespFromBody(body), info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage == nil {
			t.Fatal("expected non-nil usage")
		}
		out := w.Body.String()
		if !strings.Contains(out, "QUJD") {
			t.Errorf("expected b64_json forwarded verbatim: %s", out)
		}
		if !strings.Contains(out, `"created":`+intToStr(created)) {
			t.Errorf("expected upstream created timestamp preserved: %s", out)
		}
	})

	t.Run("b64_image alternate field name also honored", func(t *testing.T) {
		c, w := prov_cn_batch_zhipu4vGinContext()
		body := `{"created":1,"data":[{"url":"https://cdn.example/img.png","b64_image":"WFla"}]}`
		_, apiErr := zhipu4vImageHandler(c, prov_cn_batch_zhipu4vRespFromBody(body), &relaycommon.RelayInfo{})
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if !strings.Contains(w.Body.String(), "WFla") {
			t.Errorf("expected b64_image forwarded via b64_json output field, got %s", w.Body.String())
		}
	})

	t.Run("upstream error surfaces classified, not silent success", func(t *testing.T) {
		c, _ := prov_cn_batch_zhipu4vGinContext()
		body := `{"error":{"code":"1234","message":"content moderation rejected"}}`
		usage, apiErr := zhipu4vImageHandler(c, prov_cn_batch_zhipu4vRespFromBody(body), &relaycommon.RelayInfo{})
		if apiErr == nil {
			t.Fatal("expected error for upstream error payload")
		}
		if usage != nil {
			t.Errorf("expected nil usage on error, got %v", usage)
		}
	})

	t.Run("missing url on a data item is skipped, not fatal", func(t *testing.T) {
		c, w := prov_cn_batch_zhipu4vGinContext()
		body := `{"created":1,"data":[{"b64_json":"AAA"},{}]}`
		// FINDING: the first item has no url/image_url at all, so the handler
		// takes the `url == ""` branch and `continue`s -- it silently drops a
		// valid b64_json payload just because the (unused) url field was
		// empty. Documenting the current (surprising) drop behavior.
		_, apiErr := zhipu4vImageHandler(c, prov_cn_batch_zhipu4vRespFromBody(body), &relaycommon.RelayInfo{})
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if strings.Contains(w.Body.String(), "AAA") {
			t.Errorf("expected the url-less entry's b64_json to be dropped per current handler logic, but found it in output: %s", w.Body.String())
		}
	})

	t.Run("created==0 falls back to info.StartTime", func(t *testing.T) {
		c, w := prov_cn_batch_zhipu4vGinContext()
		start := time.Unix(1650000000, 0)
		info := &relaycommon.RelayInfo{StartTime: start}
		body := `{"data":[{"url":"https://cdn.example/img.png","b64_json":"ZZZ"}]}`
		_, apiErr := zhipu4vImageHandler(c, prov_cn_batch_zhipu4vRespFromBody(body), info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if !strings.Contains(w.Body.String(), `"created":1650000000`) {
			t.Errorf("expected created to fall back to info.StartTime.Unix()=1650000000, got %s", w.Body.String())
		}
	})

	t.Run("malformed JSON classified as bad response body", func(t *testing.T) {
		c, _ := prov_cn_batch_zhipu4vGinContext()
		_, apiErr := zhipu4vImageHandler(c, prov_cn_batch_zhipu4vRespFromBody("{not json"), &relaycommon.RelayInfo{})
		if apiErr == nil {
			t.Fatal("expected error for malformed JSON")
		}
	})

	t.Run("download failure for a url-only entry (no b64) drops the entry gracefully", func(t *testing.T) {
		c, w := prov_cn_batch_zhipu4vGinContext()
		// Port 1 is not listening; the download attempt fails fast (connection
		// refused / SSRF-blocked), and the handler must skip the entry rather
		// than propagate the download error as a hard failure.
		body := `{"created":1,"data":[{"url":"http://127.0.0.1:1/no-such-image.png"}]}`
		usage, apiErr := zhipu4vImageHandler(c, prov_cn_batch_zhipu4vRespFromBody(body), &relaycommon.RelayInfo{})
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage == nil {
			t.Fatal("expected a non-nil (possibly zero) usage even when every image entry fails to download")
		}
		if strings.Contains(w.Body.String(), "b64_json") && !strings.Contains(w.Body.String(), `"data":[]`) && !strings.Contains(w.Body.String(), `"data":null`) {
			// Only fail if an actual populated entry leaked through; a
			// download failure must not fabricate image bytes.
			t.Errorf("did not expect a fabricated data entry from a failed download: %s", w.Body.String())
		}
	})
}

func intToStr(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse — routing between Claude/image/openai-default handlers
// ---------------------------------------------------------------------------

func TestAdaptor_DoResponse_Routing(t *testing.T) {
	a := &Adaptor{}

	t.Run("Claude format non-stream routes to claude.ClaudeHandler", func(t *testing.T) {
		c, w := prov_cn_batch_zhipu4vGinContext()
		body := `{"id":"msg_1","type":"message","content":[{"type":"text","text":"hi from claude route"}],"stop_reason":"end_turn","model":"glm-4v","usage":{"input_tokens":4,"output_tokens":2}}`
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "glm-4v"},
			RelayFormat: types.RelayFormatClaude,
		}
		usage, apiErr := a.DoResponse(c, prov_cn_batch_zhipu4vRespFromBody(body), info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		u, ok := usage.(*dto.Usage)
		if !ok || u.PromptTokens != 4 || u.CompletionTokens != 2 {
			t.Errorf("usage = %+v (ok=%v), want PromptTokens=4 CompletionTokens=2 from claude-format response", usage, ok)
		}
		if !strings.Contains(w.Body.String(), "hi from claude route") {
			t.Errorf("expected claude-format response body forwarded, got %s", w.Body.String())
		}
	})

	t.Run("Claude format stream routes to claude.ClaudeStreamHandler", func(t *testing.T) {
		c, w := prov_cn_batch_zhipu4vGinContext()
		body := prov_cn_batch_zhipu4vSSEBody(
			`{"type":"message_start","message":{"id":"m1","model":"glm-4v","usage":{"input_tokens":2,"output_tokens":0}}}`,
			`{"type":"message_stop"}`,
		)
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "glm-4v"},
			RelayFormat: types.RelayFormatClaude,
			IsStream:    true,
		}
		_, apiErr := a.DoResponse(c, prov_cn_batch_zhipu4vRespFromBody(body), info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if w.Body.Len() == 0 {
			t.Error("expected the claude-format stream route to forward at least one chunk")
		}
	})

	t.Run("image generation relay mode routes to zhipu4vImageHandler", func(t *testing.T) {
		c, w := prov_cn_batch_zhipu4vGinContext()
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations}
		body := `{"created":1,"data":[{"url":"https://cdn.example/img.png","b64_json":"routed-image-bytes"}]}`
		_, apiErr := a.DoResponse(c, prov_cn_batch_zhipu4vRespFromBody(body), info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if !strings.Contains(w.Body.String(), "routed-image-bytes") {
			t.Errorf("expected image-generation response routed to image handler, got %s", w.Body.String())
		}
	})

	t.Run("default (non-Claude, non-image) routes to openai.Adaptor.DoResponse", func(t *testing.T) {
		c, w := prov_cn_batch_zhipu4vGinContext()
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "glm-4v"},
			RelayFormat: "openai",
		}
		body := `{"id":"c1","model":"glm-4v","choices":[{"index":0,"message":{"role":"assistant","content":"default route reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
		usage, apiErr := a.DoResponse(c, prov_cn_batch_zhipu4vRespFromBody(body), info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		u := usage.(*dto.Usage)
		if u.TotalTokens != 5 {
			t.Errorf("TotalTokens = %d, want 5 (delegated to openai handler for billing)", u.TotalTokens)
		}
		if !strings.Contains(w.Body.String(), "default route reply") {
			t.Errorf("expected default-route response forwarded, got %s", w.Body.String())
		}
	})
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestAdaptor_GetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	if len(a.GetModelList()) == 0 {
		t.Fatal("expected non-empty model list")
	}
	if a.GetChannelName() != ChannelName {
		t.Errorf("channel name = %q, want %q", a.GetChannelName(), ChannelName)
	}
}
