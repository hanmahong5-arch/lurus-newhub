package cohere

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// closeNotifyRecorder wraps httptest.ResponseRecorder to satisfy
// http.CloseNotifier, which gin's Context.Stream requires of the
// underlying ResponseWriter.
type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
}

func (c *closeNotifyRecorder) CloseNotify() <-chan bool {
	return make(chan bool)
}

func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(&closeNotifyRecorder{rec})
	return c, rec
}

// ---------------------------------------------------------------------------
// Adaptor.GetChannelName / GetModelList
// ---------------------------------------------------------------------------

func TestAdaptor_GetChannelName(t *testing.T) {
	a := &Adaptor{}
	if got := a.GetChannelName(); got != "cohere" {
		t.Errorf("GetChannelName() = %q, want %q", got, "cohere")
	}
}

func TestAdaptor_GetModelList(t *testing.T) {
	a := &Adaptor{}
	got := a.GetModelList()
	want := []string{
		"command-a-03-2025",
		"command-r", "command-r-plus",
		"command-r-08-2024", "command-r-plus-08-2024",
		"c4ai-aya-23-35b", "c4ai-aya-23-8b",
		"command-light", "command-light-nightly", "command", "command-nightly",
		"rerank-english-v3.0", "rerank-multilingual-v3.0", "rerank-english-v2.0", "rerank-multilingual-v2.0",
	}
	if len(got) != len(want) {
		t.Fatalf("len(GetModelList()) = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestChannelName(t *testing.T) {
	if ChannelName != "cohere" {
		t.Errorf("ChannelName = %q, want %q", ChannelName, "cohere")
	}
}

func TestModelListNoDuplicates(t *testing.T) {
	seen := make(map[string]int, len(ModelList))
	for _, m := range ModelList {
		seen[m]++
	}
	for m, count := range seen {
		if count != 1 {
			t.Errorf("model %q appears %d times in ModelList, want 1", m, count)
		}
	}
}

// ---------------------------------------------------------------------------
// Adaptor.GetRequestURL
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL(t *testing.T) {
	a := &Adaptor{}
	tests := []struct {
		name    string
		baseUrl string
		mode    int
		want    string
	}{
		{"rerank mode", "https://api.cohere.ai", constant.RelayModeRerank, "https://api.cohere.ai/v1/rerank"},
		{"chat completions mode", "https://api.cohere.ai", constant.RelayModeChatCompletions, "https://api.cohere.ai/v1/chat"},
		{"unrelated mode falls to chat", "https://api.cohere.ai", constant.RelayModeEmbeddings, "https://api.cohere.ai/v1/chat"},
		{"trailing slash base url preserved as-is", "https://api.cohere.ai/", constant.RelayModeChatCompletions, "https://api.cohere.ai//v1/chat"},
		{"empty base url", "", constant.RelayModeRerank, "/v1/rerank"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				RelayMode:   tt.mode,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: tt.baseUrl},
			}
			got, err := a.GetRequestURL(info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("GetRequestURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Adaptor.SetupRequestHeader
// ---------------------------------------------------------------------------

func TestAdaptor_SetupRequestHeader(t *testing.T) {
	a := &Adaptor{}

	t.Run("sets bearer auth and forwards content-type/accept", func(t *testing.T) {
		c, _ := newTestContext()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat", nil)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		c.Request = req

		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "test-key-123"},
		}
		header := &http.Header{}
		err := a.SetupRequestHeader(c, header, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Authorization"); got != "Bearer test-key-123" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-key-123")
		}
		if got := header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want %q", got, "application/json")
		}
		if got := header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want %q", got, "application/json")
		}
	})

	t.Run("stream defaults Accept to text/event-stream when absent", func(t *testing.T) {
		c, _ := newTestContext()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat", nil)
		c.Request = req

		info := &relaycommon.RelayInfo{
			IsStream:    true,
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "streaming-key"},
		}
		header := &http.Header{}
		err := a.SetupRequestHeader(c, header, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("Accept = %q, want %q", got, "text/event-stream")
		}
		if got := header.Get("Authorization"); got != "Bearer streaming-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer streaming-key")
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertOpenAIRequest / ConvertRerankRequest (delegation)
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	req := &dto.GeneralOpenAIRequest{
		Model:  "command-r",
		Stream: true,
		Messages: []dto.Message{
			{Role: "user", Content: "hi"},
		},
	}
	result, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cr, ok := result.(*CohereRequest)
	if !ok {
		t.Fatalf("result type = %T, want *CohereRequest", result)
	}
	if cr.Model != "command-r" || cr.Message != "hi" || !cr.Stream {
		t.Errorf("unexpected converted request: %+v", cr)
	}
}

func TestAdaptor_ConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	req := dto.RerankRequest{Query: "q", Model: "rerank-english-v3.0", Documents: []any{"a", "b"}}
	result, err := a.ConvertRerankRequest(c, constant.RelayModeRerank, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rr, ok := result.(*CohereRerankRequest)
	if !ok {
		t.Fatalf("result type = %T, want *CohereRerankRequest", result)
	}
	if rr.TopN != 1 {
		t.Errorf("TopN = %d, want 1 (default)", rr.TopN)
	}
	if !rr.ReturnDocuments {
		t.Error("ReturnDocuments = false, want true")
	}
}

// ---------------------------------------------------------------------------
// requestOpenAI2Cohere
// ---------------------------------------------------------------------------

func TestRequestOpenAI2Cohere(t *testing.T) {
	origSafety := common.CohereSafetySetting
	defer func() { common.CohereSafetySetting = origSafety }()

	t.Run("role mapping and message extraction", func(t *testing.T) {
		common.CohereSafetySetting = "NONE"
		req := dto.GeneralOpenAIRequest{
			Model:     "command-r",
			MaxTokens: 512,
			Messages: []dto.Message{
				{Role: "system", Content: "be nice"},
				{Role: "assistant", Content: "prior answer"},
				{Role: "other", Content: "weird role"},
				{Role: "user", Content: "final question"},
			},
		}
		got := requestOpenAI2Cohere(req)
		if got.Model != "command-r" {
			t.Errorf("Model = %q, want %q", got.Model, "command-r")
		}
		if got.Message != "final question" {
			t.Errorf("Message = %q, want %q", got.Message, "final question")
		}
		if got.MaxTokens != 512 {
			t.Errorf("MaxTokens = %d, want 512", got.MaxTokens)
		}
		if got.SafetyMode != "" {
			t.Errorf("SafetyMode = %q, want empty when setting is NONE", got.SafetyMode)
		}
		wantHistory := []ChatHistory{
			{Role: "SYSTEM", Message: "be nice"},
			{Role: "CHATBOT", Message: "prior answer"},
			{Role: "USER", Message: "weird role"},
		}
		if len(got.ChatHistory) != len(wantHistory) {
			t.Fatalf("len(ChatHistory) = %d, want %d", len(got.ChatHistory), len(wantHistory))
		}
		for i, w := range wantHistory {
			if got.ChatHistory[i] != w {
				t.Errorf("ChatHistory[%d] = %+v, want %+v", i, got.ChatHistory[i], w)
			}
		}
	})

	t.Run("non-NONE safety setting propagated", func(t *testing.T) {
		common.CohereSafetySetting = "STRICT"
		req := dto.GeneralOpenAIRequest{
			Model:    "command-r",
			Messages: []dto.Message{{Role: "user", Content: "hi"}},
		}
		got := requestOpenAI2Cohere(req)
		if got.SafetyMode != "STRICT" {
			t.Errorf("SafetyMode = %q, want %q", got.SafetyMode, "STRICT")
		}
	})

	t.Run("zero max tokens defaults to 4000", func(t *testing.T) {
		common.CohereSafetySetting = "NONE"
		req := dto.GeneralOpenAIRequest{
			Model:    "command-r",
			Messages: []dto.Message{{Role: "user", Content: "hi"}},
		}
		got := requestOpenAI2Cohere(req)
		if got.MaxTokens != 4000 {
			t.Errorf("MaxTokens = %d, want 4000", got.MaxTokens)
		}
	})

	t.Run("MaxCompletionTokens takes precedence via GetMaxTokens", func(t *testing.T) {
		common.CohereSafetySetting = "NONE"
		req := dto.GeneralOpenAIRequest{
			Model:               "command-r",
			MaxTokens:           100,
			MaxCompletionTokens: 250,
			Messages:            []dto.Message{{Role: "user", Content: "hi"}},
		}
		got := requestOpenAI2Cohere(req)
		if got.MaxTokens != 250 {
			t.Errorf("MaxTokens = %d, want 250", got.MaxTokens)
		}
	})

	t.Run("no messages leaves empty message and history", func(t *testing.T) {
		common.CohereSafetySetting = "NONE"
		req := dto.GeneralOpenAIRequest{Model: "command-r"}
		got := requestOpenAI2Cohere(req)
		if got.Message != "" {
			t.Errorf("Message = %q, want empty", got.Message)
		}
		if len(got.ChatHistory) != 0 {
			t.Errorf("len(ChatHistory) = %d, want 0", len(got.ChatHistory))
		}
	})
}

// ---------------------------------------------------------------------------
// requestConvertRerank2Cohere
// ---------------------------------------------------------------------------

func TestRequestConvertRerank2Cohere(t *testing.T) {
	t.Run("zero TopN defaults to 1", func(t *testing.T) {
		req := dto.RerankRequest{Query: "q", Model: "m", Documents: []any{"d1"}}
		got := requestConvertRerank2Cohere(req)
		if got.TopN != 1 {
			t.Errorf("TopN = %d, want 1", got.TopN)
		}
		if !got.ReturnDocuments {
			t.Error("ReturnDocuments = false, want true")
		}
		if got.Query != "q" || got.Model != "m" {
			t.Errorf("unexpected conversion: %+v", got)
		}
	})

	t.Run("explicit TopN preserved", func(t *testing.T) {
		req := dto.RerankRequest{Query: "q", Model: "m", Documents: []any{"d1", "d2"}, TopN: 5}
		got := requestConvertRerank2Cohere(req)
		if got.TopN != 5 {
			t.Errorf("TopN = %d, want 5", got.TopN)
		}
		if len(got.Documents) != 2 {
			t.Errorf("len(Documents) = %d, want 2", len(got.Documents))
		}
	})
}

// ---------------------------------------------------------------------------
// stopReasonCohere2OpenAI
// ---------------------------------------------------------------------------

func TestStopReasonCohere2OpenAI(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"COMPLETE", "stop"},
		{"MAX_TOKENS", "max_tokens"},
		{"ERROR_LIMIT", "ERROR_LIMIT"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stopReasonCohere2OpenAI(tt.input)
			if got != tt.want {
				t.Errorf("stopReasonCohere2OpenAI(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// cohereHandler
// ---------------------------------------------------------------------------

func TestCohereHandler(t *testing.T) {
	t.Run("success path converts usage and body", func(t *testing.T) {
		c, rec := newTestContext()
		body := `{"response_id":"resp-1","finish_reason":"COMPLETE","text":"hello world","meta":{"billed_units":{"input_tokens":10,"output_tokens":20}}}`
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       httpNopCloser(body),
			Header:     make(http.Header),
		}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command-r"}}

		usage, apiErr := cohereHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		if usage.PromptTokens != 10 || usage.CompletionTokens != 20 || usage.TotalTokens != 30 {
			t.Errorf("usage = %+v, want prompt=10 completion=20 total=30", usage)
		}
		if rec.Code != http.StatusOK {
			t.Errorf("recorded status = %d, want %d", rec.Code, http.StatusOK)
		}
		var got dto.TextResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("failed to unmarshal written body: %v", err)
		}
		if got.Id != "resp-1" || got.Model != "command-r" || got.Object != "chat.completion" {
			t.Errorf("unexpected written response: %+v", got)
		}
		if len(got.Choices) != 1 || got.Choices[0].FinishReason != "stop" || got.Choices[0].Message.Content != "hello world" {
			t.Errorf("unexpected choices: %+v", got.Choices)
		}
	})

	t.Run("malformed json body returns bad_response_body error", func(t *testing.T) {
		c, _ := newTestContext()
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       httpNopCloser("not json"),
			Header:     make(http.Header),
		}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, apiErr := cohereHandler(c, info, resp)
		if apiErr == nil {
			t.Fatal("expected error for malformed body")
		}
		if usage != nil {
			t.Errorf("usage = %+v, want nil on error", usage)
		}
	})
}

// ---------------------------------------------------------------------------
// cohereRerankHandler
// ---------------------------------------------------------------------------

func TestCohereRerankHandler(t *testing.T) {
	t.Run("billed units present used directly", func(t *testing.T) {
		c, rec := newTestContext()
		body := `{"results":[{"index":0,"relevance_score":0.9}],"meta":{"billed_units":{"input_tokens":7,"output_tokens":0}}}`
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       httpNopCloser(body),
			Header:     make(http.Header),
		}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, apiErr := cohereRerankHandler(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		if usage.PromptTokens != 7 || usage.TotalTokens != 7 {
			t.Errorf("usage = %+v, want prompt=7 total=7", usage)
		}
		if rec.Code != http.StatusOK {
			t.Errorf("recorded status = %d, want %d", rec.Code, http.StatusOK)
		}
	})

	t.Run("zero billed units falls back to estimated prompt tokens", func(t *testing.T) {
		c, _ := newTestContext()
		body := `{"results":[{"index":0,"relevance_score":0.5}],"meta":{"billed_units":{"input_tokens":0,"output_tokens":0}}}`
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       httpNopCloser(body),
			Header:     make(http.Header),
		}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		info.SetEstimatePromptTokens(42)
		usage, apiErr := cohereRerankHandler(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		if usage.PromptTokens != 42 || usage.TotalTokens != 42 || usage.CompletionTokens != 0 {
			t.Errorf("usage = %+v, want prompt=42 total=42 completion=0", usage)
		}
	})

	t.Run("malformed json body returns bad_response_body error", func(t *testing.T) {
		c, _ := newTestContext()
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       httpNopCloser("not json"),
			Header:     make(http.Header),
		}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, apiErr := cohereRerankHandler(c, resp, info)
		if apiErr == nil {
			t.Fatal("expected error for malformed body")
		}
		if usage != nil {
			t.Errorf("usage = %+v, want nil on error", usage)
		}
	})
}

// ---------------------------------------------------------------------------
// cohereStreamHandler
// ---------------------------------------------------------------------------

func TestCohereStreamHandler(t *testing.T) {
	t.Run("streams deltas then finish event and computes usage from billed units", func(t *testing.T) {
		c, rec := newTestContext()
		lines := []string{
			`{"is_finished":false,"event_type":"text-generation","text":"Hel"}`,
			`{"is_finished":false,"event_type":"text-generation","text":"lo"}`,
			`{"is_finished":true,"event_type":"stream-end","finish_reason":"COMPLETE","response":{"response_id":"r1","text":"Hello","meta":{"billed_units":{"input_tokens":3,"output_tokens":4}}}}`,
		}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       httpNopCloser(strings.Join(lines, "\n")),
			Header:     make(http.Header),
		}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command-r"}}

		usage, apiErr := cohereStreamHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		if usage.PromptTokens != 3 || usage.CompletionTokens != 4 {
			t.Errorf("usage = %+v, want prompt=3 completion=4", usage)
		}
		out := rec.Body.String()
		if !strings.Contains(out, `"content":"Hel"`) {
			t.Errorf("expected first delta chunk in output, got: %s", out)
		}
		if !strings.Contains(out, `"finish_reason":"stop"`) {
			t.Errorf("expected mapped finish_reason=stop in output, got: %s", out)
		}
		if !strings.Contains(out, "data: [DONE]") {
			t.Errorf("expected terminal [DONE] event, got: %s", out)
		}
		if info.FirstResponseTime.IsZero() {
			t.Error("FirstResponseTime should be set after first streamed chunk")
		}
	})

	t.Run("no billed units falls back to local token estimation", func(t *testing.T) {
		c, rec := newTestContext()
		lines := []string{
			`{"is_finished":false,"event_type":"text-generation","text":"hi"}`,
			`{"is_finished":true,"event_type":"stream-end","finish_reason":"MAX_TOKENS"}`,
		}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       httpNopCloser(strings.Join(lines, "\n")),
			Header:     make(http.Header),
		}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command-r"}}

		usage, apiErr := cohereStreamHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		// No response.meta present -> PromptTokens stays 0 -> falls back to
		// app.ResponseText2Usage, which sets PromptTokens from the estimate
		// (0 here, since none was configured) but always computes CompletionTokens
		// via EstimateTokenByModel for the accumulated response text.
		if usage.PromptTokens != 0 {
			t.Errorf("PromptTokens = %d, want 0 (no estimate configured)", usage.PromptTokens)
		}
		if usage.CompletionTokens <= 0 {
			t.Errorf("CompletionTokens = %d, want > 0 (estimated from streamed text)", usage.CompletionTokens)
		}
		out := rec.Body.String()
		if !strings.Contains(out, `"finish_reason":"max_tokens"`) {
			t.Errorf("expected mapped finish_reason=max_tokens in output, got: %s", out)
		}
	})

	t.Run("malformed json line is skipped without terminating the stream", func(t *testing.T) {
		c, rec := newTestContext()
		lines := []string{
			`not-json-at-all`,
			`{"is_finished":true,"event_type":"stream-end","finish_reason":"COMPLETE"}`,
		}
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       httpNopCloser(strings.Join(lines, "\n")),
			Header:     make(http.Header),
		}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command-r"}}

		usage, apiErr := cohereStreamHandler(c, info, resp)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		if usage == nil {
			t.Fatal("expected non-nil usage even when a line fails to unmarshal")
		}
		out := rec.Body.String()
		if !strings.Contains(out, "data: [DONE]") {
			t.Errorf("expected terminal [DONE] event despite malformed line, got: %s", out)
		}
	})
}

// httpNopCloser builds a ReadCloser from a string body, mimicking an
// upstream HTTP response body without any real network I/O.
func httpNopCloser(body string) *nopReadCloser {
	return &nopReadCloser{r: strings.NewReader(body)}
}

type nopReadCloser struct {
	r *strings.Reader
}

func (n *nopReadCloser) Read(p []byte) (int, error) { return n.r.Read(p) }
func (n *nopReadCloser) Close() error                { return nil }
