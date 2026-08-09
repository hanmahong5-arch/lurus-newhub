package cohere

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	pkgconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func init() {
	if pkgconstant.StreamingTimeout <= 0 {
		pkgconstant.StreamingTimeout = 60
	}
}

// prov_cn_batch_cohereCloseNotifyRecorder adds http.CloseNotifier to
// httptest.ResponseRecorder, which gin.Context.Stream() requires (cohere's
// streaming handler uses c.Stream internally).
type prov_cn_batch_cohereCloseNotifyRecorder struct {
	*httptest.ResponseRecorder
}

func (r *prov_cn_batch_cohereCloseNotifyRecorder) CloseNotify() <-chan bool {
	return make(chan bool)
}

func prov_cn_batch_cohereGinContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	w := &prov_cn_batch_cohereCloseNotifyRecorder{rec}
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return c, rec
}

func prov_cn_batch_cohereRespFromBody(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// ---------------------------------------------------------------------------
// Adaptor.GetRequestURL
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL(t *testing.T) {
	a := &Adaptor{}
	t.Run("rerank mode uses /v1/rerank", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeRerank, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.cohere.com"}}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://api.cohere.com/v1/rerank" {
			t.Errorf("url = %q, want .../v1/rerank", url)
		}
	})

	t.Run("chat mode uses /v1/chat", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.cohere.com"}}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "https://api.cohere.com/v1/chat" {
			t.Errorf("url = %q, want .../v1/chat", url)
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.SetupRequestHeader
// ---------------------------------------------------------------------------

func TestAdaptor_SetupRequestHeader(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_cohereGinContext()
	req := &http.Header{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "co-secret-key"}}
	if err := a.SetupRequestHeader(c, req, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Get("Authorization") != "Bearer co-secret-key" {
		t.Errorf("Authorization = %q, want Bearer co-secret-key", req.Get("Authorization"))
	}
}

// NOTE: cohere's ConvertOpenAIRequest used to dereference a nil request without
// the guard its sibling adapters all have. Covered by
// TestFixCohereConvertOpenAIRequest_NilRequestReturnsError in
// fix_nil_request_test.go, which asserts both the returned error and that the
// non-nil path still converts.

func TestAdaptor_ConvertOpenAIRequest_ValidRequest(t *testing.T) {
	old := common.CohereSafetySetting
	defer func() { common.CohereSafetySetting = old }()

	t.Run("role mapping: user -> Message, assistant -> CHATBOT, system -> SYSTEM, other -> USER", func(t *testing.T) {
		common.CohereSafetySetting = "NONE"
		a := &Adaptor{}
		c, _ := prov_cn_batch_cohereGinContext()
		req := &dto.GeneralOpenAIRequest{
			Model: "command-r",
			Messages: []dto.Message{
				{Role: "system", Content: "be terse"},
				{Role: "assistant", Content: "prior reply"},
				{Role: "tool", Content: "tool output"},
				{Role: "user", Content: "final question"},
			},
		}
		out, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cr := out.(*CohereRequest)
		if cr.Message != "final question" {
			t.Errorf("Message = %q, want the user turn's content (final question)", cr.Message)
		}
		if len(cr.ChatHistory) != 3 {
			t.Fatalf("expected 3 non-user history entries, got %d: %+v", len(cr.ChatHistory), cr.ChatHistory)
		}
		if cr.ChatHistory[0].Role != "SYSTEM" {
			t.Errorf("history[0].Role = %q, want SYSTEM", cr.ChatHistory[0].Role)
		}
		if cr.ChatHistory[1].Role != "CHATBOT" {
			t.Errorf("history[1].Role = %q, want CHATBOT", cr.ChatHistory[1].Role)
		}
		if cr.ChatHistory[2].Role != "USER" {
			t.Errorf("history[2].Role (unrecognized 'tool' role) = %q, want USER fallback", cr.ChatHistory[2].Role)
		}
	})

	t.Run("MaxTokens 0 defaults to 4000", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_cohereGinContext()
		req := &dto.GeneralOpenAIRequest{Model: "command-r"}
		out, _ := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		cr := out.(*CohereRequest)
		if cr.MaxTokens != 4000 {
			t.Errorf("MaxTokens = %d, want 4000 default", cr.MaxTokens)
		}
	})

	t.Run("MaxTokens explicit value passed through", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_cohereGinContext()
		req := &dto.GeneralOpenAIRequest{Model: "command-r", MaxTokens: 777}
		out, _ := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		cr := out.(*CohereRequest)
		if cr.MaxTokens != 777 {
			t.Errorf("MaxTokens = %d, want 777", cr.MaxTokens)
		}
	})

	t.Run("SafetyMode set when CohereSafetySetting is not NONE", func(t *testing.T) {
		common.CohereSafetySetting = "STRICT"
		a := &Adaptor{}
		c, _ := prov_cn_batch_cohereGinContext()
		req := &dto.GeneralOpenAIRequest{Model: "command-r"}
		out, _ := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		cr := out.(*CohereRequest)
		if cr.SafetyMode != "STRICT" {
			t.Errorf("SafetyMode = %q, want STRICT to be forwarded", cr.SafetyMode)
		}
	})

	t.Run("SafetyMode left empty when CohereSafetySetting is NONE", func(t *testing.T) {
		common.CohereSafetySetting = "NONE"
		a := &Adaptor{}
		c, _ := prov_cn_batch_cohereGinContext()
		req := &dto.GeneralOpenAIRequest{Model: "command-r"}
		out, _ := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		cr := out.(*CohereRequest)
		if cr.SafetyMode != "" {
			t.Errorf("SafetyMode = %q, want empty when disabled", cr.SafetyMode)
		}
	})

	t.Run("empty messages leaves empty Message and history, no panic", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_cohereGinContext()
		req := &dto.GeneralOpenAIRequest{Model: "command-r", Messages: []dto.Message{}}
		out, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cr := out.(*CohereRequest)
		if cr.Message != "" || len(cr.ChatHistory) != 0 {
			t.Errorf("expected empty Message/history for empty messages, got Message=%q History=%v", cr.Message, cr.ChatHistory)
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertRerankRequest / requestConvertRerank2Cohere
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_cohereGinContext()

	t.Run("TopN 0 defaults to 1", func(t *testing.T) {
		req := dto.RerankRequest{Query: "q", Model: "rerank-v3", Documents: []any{"doc1"}}
		out, err := a.ConvertRerankRequest(c, 0, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cr := out.(*CohereRerankRequest)
		if cr.TopN != 1 {
			t.Errorf("TopN = %d, want 1 default", cr.TopN)
		}
		if !cr.ReturnDocuments {
			t.Error("expected ReturnDocuments=true always")
		}
	})

	t.Run("explicit TopN preserved", func(t *testing.T) {
		req := dto.RerankRequest{Query: "q", Model: "rerank-v3", TopN: 5}
		out, _ := a.ConvertRerankRequest(c, 0, req)
		cr := out.(*CohereRerankRequest)
		if cr.TopN != 5 {
			t.Errorf("TopN = %d, want 5", cr.TopN)
		}
	})
}

// ---------------------------------------------------------------------------
// stopReasonCohere2OpenAI
// ---------------------------------------------------------------------------

func TestStopReasonCohere2OpenAI(t *testing.T) {
	tests := []struct{ in, want string }{
		{"COMPLETE", "stop"},
		{"MAX_TOKENS", "max_tokens"},
		{"ERROR_TOXIC", "ERROR_TOXIC"}, // unmapped reason passed through verbatim
		{"", ""},
	}
	for _, tt := range tests {
		if got := stopReasonCohere2OpenAI(tt.in); got != tt.want {
			t.Errorf("stopReasonCohere2OpenAI(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// cohereHandler / cohereRerankHandler — response translation + billing
// ---------------------------------------------------------------------------

func TestCohereHandler(t *testing.T) {
	t.Run("valid response extracts usage and finish reason", func(t *testing.T) {
		c, w := prov_cn_batch_cohereGinContext()
		body := `{"response_id":"r1","text":"the answer","finish_reason":"COMPLETE","meta":{"billed_units":{"input_tokens":5,"output_tokens":3}}}`
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command-r"}}
		bcResp9 := prov_cn_batch_cohereRespFromBody(body)
		defer func() {
			if bcResp9 != nil {
				_ = bcResp9.Body.Close()
			}
		}()
		usage, apiErr := cohereHandler(c, info, bcResp9)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage.PromptTokens != 5 || usage.CompletionTokens != 3 || usage.TotalTokens != 8 {
			t.Errorf("usage = %+v, want prompt=5 completion=3 total=8 (billing extraction)", usage)
		}
		if !strings.Contains(w.Body.String(), "the answer") {
			t.Errorf("response body missing content: %s", w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"finish_reason":"stop"`) {
			t.Errorf("expected mapped finish_reason=stop in output, got %s", w.Body.String())
		}
	})

	t.Run("malformed JSON classified as bad response body", func(t *testing.T) {
		c, _ := prov_cn_batch_cohereGinContext()
		bcResp8 := prov_cn_batch_cohereRespFromBody("{not json")
		defer func() {
			if bcResp8 != nil {
				_ = bcResp8.Body.Close()
			}
		}()
		_, apiErr := cohereHandler(c, &relaycommon.RelayInfo{}, bcResp8)
		if apiErr == nil {
			t.Fatal("expected error for malformed JSON")
		}
	})
}

func TestCohereRerankHandler(t *testing.T) {
	t.Run("billed units present used directly for usage", func(t *testing.T) {
		c, w := prov_cn_batch_cohereGinContext()
		body := `{"results":[{"index":0,"relevance_score":0.9}],"meta":{"billed_units":{"input_tokens":9,"output_tokens":0}}}`
		bcResp7 := prov_cn_batch_cohereRespFromBody(body)
		defer func() {
			if bcResp7 != nil {
				_ = bcResp7.Body.Close()
			}
		}()
		usage, apiErr := cohereRerankHandler(c, bcResp7, &relaycommon.RelayInfo{})
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage.PromptTokens != 9 || usage.TotalTokens != 9 {
			t.Errorf("usage = %+v, want PromptTokens=9 TotalTokens=9 from billed_units", usage)
		}
		if !strings.Contains(w.Body.String(), "relevance_score") {
			t.Errorf("response body missing rerank results: %s", w.Body.String())
		}
	})

	t.Run("zero billed units falls back to estimated prompt tokens", func(t *testing.T) {
		c, _ := prov_cn_batch_cohereGinContext()
		body := `{"results":[],"meta":{"billed_units":{"input_tokens":0,"output_tokens":0}}}`
		info := &relaycommon.RelayInfo{}
		info.SetEstimatePromptTokens(42)
		bcResp6 := prov_cn_batch_cohereRespFromBody(body)
		defer func() {
			if bcResp6 != nil {
				_ = bcResp6.Body.Close()
			}
		}()
		usage, apiErr := cohereRerankHandler(c, bcResp6, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if usage.PromptTokens != 42 || usage.TotalTokens != 42 {
			t.Errorf("usage = %+v, want PromptTokens=TotalTokens=42 (estimate fallback since upstream billed_units are zero)", usage)
		}
	})

	t.Run("malformed JSON classified as bad response body", func(t *testing.T) {
		c, _ := prov_cn_batch_cohereGinContext()
		bcResp5 := prov_cn_batch_cohereRespFromBody("{not json")
		defer func() {
			if bcResp5 != nil {
				_ = bcResp5.Body.Close()
			}
		}()
		_, apiErr := cohereRerankHandler(c, bcResp5, &relaycommon.RelayInfo{})
		if apiErr == nil {
			t.Fatal("expected error for malformed JSON")
		}
	})
}

// ---------------------------------------------------------------------------
// cohereStreamHandler
// ---------------------------------------------------------------------------

func TestCohereStreamHandler(t *testing.T) {
	c, w := prov_cn_batch_cohereGinContext()
	body := `{"event_type":"text-generation","text":"He","is_finished":false}` + "\n" +
		`{"event_type":"text-generation","text":"llo","is_finished":false}` + "\n" +
		`{"event_type":"stream-end","is_finished":true,"finish_reason":"COMPLETE","response":{"meta":{"billed_units":{"input_tokens":2,"output_tokens":4}}}}` + "\n"
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command-r"}}
	bcResp4 := prov_cn_batch_cohereRespFromBody(body)
	defer func() {
		if bcResp4 != nil {
			_ = bcResp4.Body.Close()
		}
	}()
	usage, apiErr := cohereStreamHandler(c, info, bcResp4)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.PromptTokens != 2 || usage.CompletionTokens != 4 {
		t.Errorf("usage = %+v, want PromptTokens=2 CompletionTokens=4 from terminal billed_units", usage)
	}
	out := w.Body.String()
	if !strings.Contains(out, "He") || !strings.Contains(out, "llo") {
		t.Errorf("stream output missing forwarded text deltas: %s", out)
	}
	if !strings.Contains(out, `"finish_reason":"stop"`) {
		t.Errorf("expected mapped finish_reason=stop on terminal chunk: %s", out)
	}
	if !strings.Contains(out, "[DONE]") {
		t.Errorf("expected terminal [DONE] marker: %s", out)
	}
}

func TestCohereStreamHandler_NoBilledUnits_FallsBackToEstimate(t *testing.T) {
	c, _ := prov_cn_batch_cohereGinContext()
	body := `{"event_type":"text-generation","text":"hi","is_finished":false}` + "\n" +
		`{"event_type":"stream-end","is_finished":true,"finish_reason":"COMPLETE"}` + "\n"
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command-r"}}
	bcResp3 := prov_cn_batch_cohereRespFromBody(body)
	defer func() {
		if bcResp3 != nil {
			_ = bcResp3.Body.Close()
		}
	}()
	usage, apiErr := cohereStreamHandler(c, info, bcResp3)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.CompletionTokens <= 0 {
		t.Errorf("expected estimated positive CompletionTokens when upstream never reported billed_units, got %d", usage.CompletionTokens)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse — routing between rerank/stream/chat handlers
// ---------------------------------------------------------------------------

func TestAdaptor_DoResponse_Routing(t *testing.T) {
	a := &Adaptor{}

	t.Run("rerank relay mode routes to rerank handler", func(t *testing.T) {
		c, w := prov_cn_batch_cohereGinContext()
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeRerank}
		body := `{"results":[{"index":0,"relevance_score":0.5}],"meta":{"billed_units":{"input_tokens":1}}}`
		bcResp2 := prov_cn_batch_cohereRespFromBody(body)
		defer func() {
			if bcResp2 != nil {
				_ = bcResp2.Body.Close()
			}
		}()
		_, apiErr := a.DoResponse(c, bcResp2, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if !strings.Contains(w.Body.String(), "relevance_score") {
			t.Errorf("expected rerank-shaped response, got %s", w.Body.String())
		}
	})

	t.Run("non-stream default routes to chat handler", func(t *testing.T) {
		c, w := prov_cn_batch_cohereGinContext()
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"response_id":"r1","text":"chat reply","finish_reason":"COMPLETE"}`
		bcResp1 := prov_cn_batch_cohereRespFromBody(body)
		defer func() {
			if bcResp1 != nil {
				_ = bcResp1.Body.Close()
			}
		}()
		_, apiErr := a.DoResponse(c, bcResp1, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if !strings.Contains(w.Body.String(), "chat reply") {
			t.Errorf("expected chat-shaped response, got %s", w.Body.String())
		}
	})

	t.Run("stream routes to stream handler", func(t *testing.T) {
		c, w := prov_cn_batch_cohereGinContext()
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"event_type":"stream-end","is_finished":true,"finish_reason":"COMPLETE"}` + "\n"
		bcResp0 := prov_cn_batch_cohereRespFromBody(body)
		defer func() {
			if bcResp0 != nil {
				_ = bcResp0.Body.Close()
			}
		}()
		_, apiErr := a.DoResponse(c, bcResp0, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if !strings.Contains(w.Body.String(), "[DONE]") {
			t.Errorf("expected streamed terminal marker, got %s", w.Body.String())
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
