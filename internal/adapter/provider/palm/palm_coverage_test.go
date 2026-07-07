package palm

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// closeNotifyRecorder wraps httptest.ResponseRecorder to satisfy
// http.CloseNotifier, which gin's Context.Stream requires of the
// underlying ResponseWriter (used by palmStreamHandler).
type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
}

func (c *closeNotifyRecorder) CloseNotify() <-chan bool {
	return make(chan bool)
}

func newTestContext() (*httptest.ResponseRecorder, *gin.Context) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(&closeNotifyRecorder{w})
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return w, c
}

// errReader always fails on Read, to exercise the io.ReadAll error branch.
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (errReader) Close() error               { return nil }

// ---------------------------------------------------------------------------
// GetRequestURL
// ---------------------------------------------------------------------------

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://generativelanguage.googleapis.com"}}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://generativelanguage.googleapis.com/v1beta2/models/chat-bison-001:generateMessage"
	if got != want {
		t.Errorf("GetRequestURL() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestSetupRequestHeader(t *testing.T) {
	a := &Adaptor{}
	_, c := newTestContext()
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Accept", "application/json")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "palm-secret-key"}}
	header := http.Header{}

	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("x-goog-api-key"); got != "palm-secret-key" {
		t.Errorf("x-goog-api-key = %q, want %q", got, "palm-secret-key")
	}
	if got := header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q (delegated to provider.SetupApiRequestHeader)", got, "application/json")
	}
}

func TestSetupRequestHeader_StreamDefaultAccept(t *testing.T) {
	a := &Adaptor{}
	_, c := newTestContext()
	// No Accept header set on the incoming request, and IsStream=true should
	// make the shared SetupApiRequestHeader default it to text/event-stream.
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "k"},
		IsStream:    true,
	}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Accept"); got != "text/event-stream" {
		t.Errorf("Accept = %q, want %q", got, "text/event-stream")
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest
// ---------------------------------------------------------------------------

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil request returns error", func(t *testing.T) {
		got, err := a.ConvertOpenAIRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "request is nil" {
			t.Errorf("error = %v, want %q", err, "request is nil")
		}
	})

	t.Run("non-nil request is passed through unchanged", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "PaLM-2"}
		got, err := a.ConvertOpenAIRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq, ok := got.(*dto.GeneralOpenAIRequest)
		if !ok {
			t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", got)
		}
		if gotReq != req {
			t.Error("expected the same pointer to be returned")
		}
		if gotReq.Model != "PaLM-2" {
			t.Errorf("Model = %q, want %q", gotReq.Model, "PaLM-2")
		}
	})
}

func TestConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}

	models := a.GetModelList()
	if len(models) != 1 || models[0] != "PaLM-2" {
		t.Errorf("GetModelList() = %v, want [\"PaLM-2\"]", models)
	}

	if got := a.GetChannelName(); got != "google palm" {
		t.Errorf("GetChannelName() = %q, want %q", got, "google palm")
	}
}

// ---------------------------------------------------------------------------
// responsePaLM2OpenAI
// ---------------------------------------------------------------------------

func TestResponsePaLM2OpenAI(t *testing.T) {
	t.Run("multiple candidates map to indexed choices", func(t *testing.T) {
		palmResp := &PaLMChatResponse{
			Candidates: []PaLMChatMessage{
				{Author: "1", Content: "first"},
				{Author: "1", Content: "second"},
			},
		}
		got := responsePaLM2OpenAI(palmResp)
		if len(got.Choices) != 2 {
			t.Fatalf("len(Choices) = %d, want 2", len(got.Choices))
		}
		for i, want := range []string{"first", "second"} {
			if got.Choices[i].Index != i {
				t.Errorf("Choices[%d].Index = %d, want %d", i, got.Choices[i].Index, i)
			}
			if got.Choices[i].Role != "assistant" {
				t.Errorf("Choices[%d].Message.Role = %q, want %q", i, got.Choices[i].Role, "assistant")
			}
			if got.Choices[i].Content != want {
				t.Errorf("Choices[%d].Message.Content = %v, want %q", i, got.Choices[i].Content, want)
			}
			if got.Choices[i].FinishReason != "stop" {
				t.Errorf("Choices[%d].FinishReason = %q, want %q", i, got.Choices[i].FinishReason, "stop")
			}
		}
	})

	t.Run("no candidates yields empty choices slice, not nil", func(t *testing.T) {
		got := responsePaLM2OpenAI(&PaLMChatResponse{})
		if got.Choices == nil {
			t.Error("Choices = nil, want an initialized empty slice")
		}
		if len(got.Choices) != 0 {
			t.Errorf("len(Choices) = %d, want 0", len(got.Choices))
		}
	})
}

// ---------------------------------------------------------------------------
// streamResponsePaLM2OpenAI
// ---------------------------------------------------------------------------

func TestStreamResponsePaLM2OpenAI(t *testing.T) {
	t.Run("with candidates sets delta content", func(t *testing.T) {
		got := streamResponsePaLM2OpenAI(&PaLMChatResponse{
			Candidates: []PaLMChatMessage{{Content: "hello stream"}},
		})
		if got.Object != "chat.completion.chunk" {
			t.Errorf("Object = %q, want %q", got.Object, "chat.completion.chunk")
		}
		if got.Model != "palm2" {
			t.Errorf("Model = %q, want %q", got.Model, "palm2")
		}
		if len(got.Choices) != 1 {
			t.Fatalf("len(Choices) = %d, want 1", len(got.Choices))
		}
		content := got.Choices[0].Delta.GetContentString()
		if content != "hello stream" {
			t.Errorf("Delta content = %q, want %q", content, "hello stream")
		}
		if got.Choices[0].FinishReason == nil || *got.Choices[0].FinishReason != "stop" {
			t.Errorf("FinishReason = %v, want %q", got.Choices[0].FinishReason, "stop")
		}
	})

	t.Run("no candidates leaves delta content empty", func(t *testing.T) {
		got := streamResponsePaLM2OpenAI(&PaLMChatResponse{})
		if len(got.Choices) != 1 {
			t.Fatalf("len(Choices) = %d, want 1", len(got.Choices))
		}
		if content := got.Choices[0].Delta.GetContentString(); content != "" {
			t.Errorf("Delta content = %q, want empty", content)
		}
	})
}

// ---------------------------------------------------------------------------
// palmHandler
// ---------------------------------------------------------------------------

func TestPalmHandler_Success(t *testing.T) {
	w, c := newTestContext()
	body := `{"candidates":[{"author":"1","content":"the answer"}]}`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := palmHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("usage is nil")
	}
	if usage.CompletionTokens == 0 {
		t.Error("CompletionTokens = 0, want > 0 for non-empty response text")
	}
	if w.Code != http.StatusOK {
		t.Errorf("http status written = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "the answer") {
		t.Errorf("response body = %q, want it to contain %q", w.Body.String(), "the answer")
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want %q", w.Header().Get("Content-Type"), "application/json")
	}
}

func TestPalmHandler_BodyReadError(t *testing.T) {
	_, c := newTestContext()
	resp := &http.Response{StatusCode: 200, Body: errReader{}}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := palmHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error when body read fails, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

func TestPalmHandler_MalformedJSON(t *testing.T) {
	_, c := newTestContext()
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{not-json"))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := palmHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON body, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

func TestPalmHandler_UpstreamErrorCode(t *testing.T) {
	_, c := newTestContext()
	body := `{"error":{"code":400,"message":"bad request","status":"INVALID_ARGUMENT"}}`
	resp := &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := palmHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for upstream error-code response, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

func TestPalmHandler_NoCandidatesNoErrorCode(t *testing.T) {
	_, c := newTestContext()
	// Neither an error code nor any candidates: still routes through the
	// "treat as error" branch since len(Candidates) == 0.
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := palmHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error when no candidates are returned, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

// ---------------------------------------------------------------------------
// palmStreamHandler — exercises the goroutine + c.Stream loop against an
// in-memory reader (no network); hermetic because it only needs an
// io.ReadCloser body, not a live connection.
// ---------------------------------------------------------------------------

func TestPalmStreamHandler_Success(t *testing.T) {
	w, c := newTestContext()
	body := `{"candidates":[{"author":"1","content":"stream answer"}]}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}

	apiErr, responseText := palmStreamHandler(c, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if responseText != "stream answer" {
		t.Errorf("responseText = %q, want %q", responseText, "stream answer")
	}
	if !strings.Contains(w.Body.String(), "data: [DONE]") {
		t.Errorf("streamed body = %q, want it to contain the terminal DONE event", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "stream answer") {
		t.Errorf("streamed body = %q, want it to contain the candidate content", w.Body.String())
	}
}

func TestPalmStreamHandler_BodyReadError(t *testing.T) {
	_, c := newTestContext()
	resp := &http.Response{StatusCode: 200, Body: errReader{}}

	apiErr, responseText := palmStreamHandler(c, resp)
	if apiErr != nil {
		t.Errorf("expected nil error (read failures are swallowed into a bare DONE event), got %v", apiErr)
	}
	if responseText != "" {
		t.Errorf("responseText = %q, want empty on body-read failure", responseText)
	}
}

func TestPalmStreamHandler_MalformedJSON(t *testing.T) {
	_, c := newTestContext()
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{not-json"))}

	apiErr, responseText := palmStreamHandler(c, resp)
	if apiErr != nil {
		t.Errorf("expected nil error (unmarshal failures are swallowed into a bare DONE event), got %v", apiErr)
	}
	if responseText != "" {
		t.Errorf("responseText = %q, want empty on unmarshal failure", responseText)
	}
}

// ---------------------------------------------------------------------------
// DoResponse — dispatches to palmHandler/palmStreamHandler, both hermetic.
// DoRequest itself is not exercised: it always performs a live HTTP
// round-trip via provider.DoApiRequest, which needs a real upstream.
// ---------------------------------------------------------------------------

func TestDoResponse_NonStreaming(t *testing.T) {
	a := &Adaptor{}
	_, c := newTestContext()
	body := `{"candidates":[{"author":"1","content":"blocking answer"}]}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, IsStream: false}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("usage type = %T, want *dto.Usage", usage)
	}
	if u.CompletionTokens == 0 {
		t.Error("CompletionTokens = 0, want > 0")
	}
}

func TestDoResponse_Streaming(t *testing.T) {
	a := &Adaptor{}
	_, c := newTestContext()
	body := `{"candidates":[{"author":"1","content":"streaming answer"}]}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, IsStream: true}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("usage type = %T, want *dto.Usage", usage)
	}
	if u.CompletionTokens == 0 {
		t.Error("CompletionTokens = 0, want > 0 for non-empty streamed response text")
	}
}
