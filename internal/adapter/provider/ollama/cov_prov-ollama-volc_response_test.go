package ollama

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func prov_ollama_volc_newTestCtx(method, path, body string) (*httptest.ResponseRecorder, *gin.Context) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	c.Request = httptest.NewRequest(method, path, r)
	return w, c
}

func prov_ollama_volc_mkResp(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// ---------------------------------------------------------------------------
// ollamaChatHandler (non-stream): this computes billing usage — the field
// most likely to silently resell tokens for free if it regresses.
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_OllamaChatHandler_SingleJSONObject_UsageAndContent(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	body := `{"model":"llama3","created_at":"2024-01-01T00:00:00Z","message":{"role":"assistant","content":"hello there"},"done":true,"done_reason":"stop","prompt_eval_count":10,"eval_count":5}`
	resp := prov_ollama_volc_mkResp(body)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	usage, apiErr := ollamaChatHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", usage.PromptTokens)
	}
	if usage.CompletionTokens != 5 {
		t.Errorf("CompletionTokens = %d, want 5", usage.CompletionTokens)
	}
	if usage.TotalTokens != 15 {
		t.Errorf("TotalTokens = %d, want 15 (prompt+completion, the billing quantity)", usage.TotalTokens)
	}
	respBody := w.Body.String()
	if !strings.Contains(respBody, `"hello there"`) {
		t.Errorf("body should contain converted content, got %q", respBody)
	}
	if !strings.Contains(respBody, `"chat.completion"`) {
		t.Errorf("body should be object=chat.completion, got %q", respBody)
	}
	if !strings.Contains(respBody, `"finish_reason":"stop"`) {
		t.Errorf("body should carry through finish_reason=stop, got %q", respBody)
	}
}

func TestProvOllamaVolc_OllamaChatHandler_GenerateEndpoint_UsesResponseField(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/completions", "")
	body := `{"model":"llama3","response":"the answer is 4","done":true,"prompt_eval_count":3,"eval_count":2}`
	resp := prov_ollama_volc_mkResp(body)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	usage, apiErr := ollamaChatHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.TotalTokens != 5 {
		t.Errorf("TotalTokens = %d, want 5", usage.TotalTokens)
	}
	if !strings.Contains(w.Body.String(), "the answer is 4") {
		t.Errorf("body should contain the generate-mode response text, got %q", w.Body.String())
	}
}

func TestProvOllamaVolc_OllamaChatHandler_MissingUsageFields_DegradesGracefullyToZero(t *testing.T) {
	// A response with no prompt_eval_count/eval_count must not panic; usage
	// must degrade to 0, not some garbage/negative value.
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	body := `{"model":"llama3","message":{"role":"assistant","content":"hi"},"done":true}`
	resp := prov_ollama_volc_mkResp(body)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	usage, apiErr := ollamaChatHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.PromptTokens != 0 || usage.CompletionTokens != 0 || usage.TotalTokens != 0 {
		t.Errorf("usage = %+v, want all-zero when upstream omits token counts", usage)
	}
	_ = w
}

func TestProvOllamaVolc_OllamaChatHandler_MultiLineNDJSON_AggregatesContentAndUsesLastChunkMeta(t *testing.T) {
	// Ollama's non-stream endpoint can still emit newline-delimited partial
	// objects; the handler must aggregate content across all lines but use
	// the LAST line's metadata (done_reason, token counts, model, created_at).
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	body := strings.Join([]string{
		`{"model":"llama3","message":{"role":"assistant","content":"Hel"},"done":false}`,
		`{"model":"llama3","message":{"role":"assistant","content":"lo!"},"done":true,"done_reason":"stop","prompt_eval_count":7,"eval_count":3}`,
	}, "\n")
	resp := prov_ollama_volc_mkResp(body)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	usage, apiErr := ollamaChatHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.PromptTokens != 7 || usage.CompletionTokens != 3 {
		t.Errorf("usage = %+v, want the LAST line's counts (7,3), not the first (0,0)", usage)
	}
	if !strings.Contains(w.Body.String(), `"Hello!"`) {
		t.Errorf("body should contain aggregated content across both lines, got %q", w.Body.String())
	}
}

func TestProvOllamaVolc_OllamaChatHandler_ReasoningThinkingField_Aggregated(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	body := `{"model":"llama3","message":{"role":"assistant","content":"answer","thinking":"\"step by step\""},"done":true}`
	resp := prov_ollama_volc_mkResp(body)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	_, apiErr := ollamaChatHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if !strings.Contains(w.Body.String(), "step by step") {
		t.Errorf("body should surface the reasoning/thinking content, got %q", w.Body.String())
	}
}

func TestProvOllamaVolc_OllamaChatHandler_MalformedJSON_ReturnsError(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	resp := prov_ollama_volc_mkResp(`{not-json`)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := ollamaChatHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected an error for malformed JSON body")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
	if usage != nil {
		t.Errorf("usage = %+v, want nil on parse error", usage)
	}
	_ = w
}

func TestProvOllamaVolc_OllamaChatHandler_EmptyBody_ReturnsError(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	resp := prov_ollama_volc_mkResp("")
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	usage, apiErr := ollamaChatHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected an error for a completely empty response body")
	}
	if usage != nil {
		t.Errorf("usage = %+v, want nil", usage)
	}
	_ = w
}

func TestProvOllamaVolc_OllamaChatHandler_ModelFallsBackToUpstreamModelName(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	body := `{"message":{"role":"assistant","content":"hi"},"done":true}`
	resp := prov_ollama_volc_mkResp(body)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3-fallback"}}

	_, apiErr := ollamaChatHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if !strings.Contains(w.Body.String(), `"model":"llama3-fallback"`) {
		t.Errorf("body should fall back to info.UpstreamModelName when upstream omits model, got %q", w.Body.String())
	}
}

func TestProvOllamaVolc_OllamaChatHandler_BodyReadFailure(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(errReaderOllama{})}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := ollamaChatHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error when the response body fails to read")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeReadResponseBodyFailed {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeReadResponseBodyFailed)
	}
	if usage != nil {
		t.Errorf("usage = %+v, want nil", usage)
	}
	_ = w
}

type errReaderOllama struct{}

func (errReaderOllama) Read(p []byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// ---------------------------------------------------------------------------
// toUnix
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_ToUnix(t *testing.T) {
	if got := toUnix("2024-01-01T00:00:00Z"); got != 1704067200 {
		t.Errorf("toUnix(RFC3339) = %d, want 1704067200", got)
	}
	if got := toUnix("2024-01-01T00:00:00.123456789Z"); got != 1704067200 {
		t.Errorf("toUnix(RFC3339Nano) = %d, want 1704067200", got)
	}
	// empty and garbage timestamps must fall back to "now" (a positive unix
	// time), not panic or return zero.
	if got := toUnix(""); got <= 0 {
		t.Errorf("toUnix(\"\") = %d, want a positive fallback-to-now value", got)
	}
	if got := toUnix("not-a-timestamp"); got <= 0 {
		t.Errorf("toUnix(garbage) = %d, want a positive fallback-to-now value", got)
	}
}

// ---------------------------------------------------------------------------
// ollamaStreamHandler: SSE streaming translation + final usage accounting.
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_OllamaStreamHandler_EmptyResponse_ReturnsError(t *testing.T) {
	_, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := ollamaStreamHandler(c, info, nil)
	if apiErr == nil {
		t.Fatal("expected error for nil response")
	}
	if usage != nil {
		t.Errorf("usage = %+v, want nil", usage)
	}
}

func TestProvOllamaVolc_OllamaStreamHandler_ChatDeltasAndFinalUsage(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	body := strings.Join([]string{
		`{"model":"llama3","created_at":"2024-01-01T00:00:00Z","message":{"role":"assistant","content":"Hel"},"done":false}`,
		`{"model":"llama3","created_at":"2024-01-01T00:00:01Z","message":{"role":"assistant","content":"lo"},"done":false}`,
		`{"model":"llama3","created_at":"2024-01-01T00:00:02Z","done":true,"done_reason":"stop","prompt_eval_count":11,"eval_count":4}`,
	}, "\n")
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	usage, apiErr := ollamaStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.PromptTokens != 11 || usage.CompletionTokens != 4 {
		t.Errorf("usage = %+v, want PromptTokens=11 CompletionTokens=4", usage)
	}
	if usage.TotalTokens != 15 {
		t.Errorf("TotalTokens = %d, want 15", usage.TotalTokens)
	}
	out := w.Body.String()
	if !strings.Contains(out, `"content":"Hel"`) {
		t.Errorf("stream should emit a delta chunk for 'Hel', got %q", out)
	}
	if !strings.Contains(out, `"content":"lo"`) {
		t.Errorf("stream should emit a delta chunk for 'lo', got %q", out)
	}
	if !strings.Contains(out, `"finish_reason":"stop"`) {
		t.Errorf("stream should emit the stop finish_reason, got %q", out)
	}
	if !strings.Contains(out, "[DONE]") {
		t.Errorf("stream should terminate with [DONE], got %q", out)
	}
}

func TestProvOllamaVolc_OllamaStreamHandler_GenerateEndpoint_UsesResponseField(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/completions", "")
	body := strings.Join([]string{
		`{"model":"llama3","response":"partial","done":false}`,
		`{"model":"llama3","done":true,"prompt_eval_count":2,"eval_count":1}`,
	}, "\n")
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	usage, apiErr := ollamaStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.TotalTokens != 3 {
		t.Errorf("TotalTokens = %d, want 3", usage.TotalTokens)
	}
	if !strings.Contains(w.Body.String(), `"content":"partial"`) {
		t.Errorf("stream should surface the generate-mode 'response' field as delta content, got %q", w.Body.String())
	}
}

func TestProvOllamaVolc_OllamaStreamHandler_DoneReasonDefaultsToStop(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	body := `{"model":"llama3","done":true,"prompt_eval_count":1,"eval_count":1}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	_, apiErr := ollamaStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if !strings.Contains(w.Body.String(), `"finish_reason":"stop"`) {
		t.Errorf("empty done_reason must default to 'stop', got %q", w.Body.String())
	}
}

func TestProvOllamaVolc_OllamaStreamHandler_ToolCallDeltas(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	body := strings.Join([]string{
		`{"model":"llama3","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"SF"}}}]},"done":false}`,
		`{"model":"llama3","done":true,"done_reason":"tool_calls","prompt_eval_count":1,"eval_count":1}`,
	}, "\n")
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	_, apiErr := ollamaStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	out := w.Body.String()
	if !strings.Contains(out, `"get_weather"`) {
		t.Errorf("stream should include the tool call function name, got %q", out)
	}
	if !strings.Contains(out, `"call_0"`) {
		t.Errorf("stream should assign a synthesized tool call id starting at index 0, got %q", out)
	}
	if !strings.Contains(out, `"finish_reason":"tool_calls"`) {
		t.Errorf("stream should carry through the tool_calls finish reason, got %q", out)
	}
}

func TestProvOllamaVolc_OllamaStreamHandler_ThinkingDelta_JSONStringUnwrapped(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	body := strings.Join([]string{
		`{"model":"llama3","message":{"role":"assistant","content":"","thinking":"\"reasoning here\""},"done":false}`,
		`{"model":"llama3","done":true,"prompt_eval_count":1,"eval_count":1}`,
	}, "\n")
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	_, apiErr := ollamaStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if !strings.Contains(w.Body.String(), "reasoning here") {
		t.Errorf("stream should surface unwrapped thinking content, got %q", w.Body.String())
	}
}

func TestProvOllamaVolc_OllamaStreamHandler_ThinkingNullIgnored(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	body := strings.Join([]string{
		`{"model":"llama3","message":{"role":"assistant","content":"hi","thinking":null},"done":false}`,
		`{"model":"llama3","done":true,"prompt_eval_count":1,"eval_count":1}`,
	}, "\n")
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	_, apiErr := ollamaStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if strings.Contains(w.Body.String(), `"reasoning_content"`) {
		t.Errorf("a null thinking field should not synthesize a reasoning_content delta, got %q", w.Body.String())
	}
}

func TestProvOllamaVolc_OllamaStreamHandler_MalformedLine_ReturnsErrorMidStream(t *testing.T) {
	// A non-JSON line mid-stream (half-written chunk / corrupted upstream)
	// must surface as a NewAPIError, not panic, and must not silently be
	// skipped (which would drop tokens from the billed usage count).
	_, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	body := strings.Join([]string{
		`{"model":"llama3","message":{"role":"assistant","content":"ok"},"done":false}`,
		`{not-json-at-all`,
	}, "\n")
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	usage, apiErr := ollamaStreamHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected an error for a malformed mid-stream line")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
	// Usage accumulated so far (zero, since no done frame was reached) must
	// still be returned, not nil, matching the function's signature contract.
	if usage == nil {
		t.Error("usage should be a non-nil zero-value Usage, not nil, on a parse error mid-stream")
	}
}

func TestProvOllamaVolc_OllamaStreamHandler_BlankLinesSkipped(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	body := "\n\n" + `{"model":"llama3","message":{"role":"assistant","content":"hi"},"done":false}` + "\n\n" +
		`{"model":"llama3","done":true,"prompt_eval_count":1,"eval_count":1}` + "\n\n"
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	usage, apiErr := ollamaStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.TotalTokens != 2 {
		t.Errorf("TotalTokens = %d, want 2 (blank lines must not corrupt parsing)", usage.TotalTokens)
	}
	if !strings.Contains(w.Body.String(), `"content":"hi"`) {
		t.Errorf("expected the delta content despite surrounding blank lines, got %q", w.Body.String())
	}
}

func TestProvOllamaVolc_OllamaStreamHandler_MissingDoneFrame_NoFinalUsageEmitted(t *testing.T) {
	// If the upstream stream is truncated before a done:true frame arrives,
	// the handler must not fabricate a final usage/DONE chunk — it simply
	// stops scanning. Usage stays at zero (nothing was billed).
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	body := `{"model":"llama3","message":{"role":"assistant","content":"only partial"},"done":false}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	usage, apiErr := ollamaStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0 when no done frame ever arrived", usage.TotalTokens)
	}
	if strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("stream should NOT emit [DONE] when the upstream never sent a done:true frame, got %q", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// ollamaEmbeddingHandler
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_OllamaEmbeddingHandler_Success(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/embeddings", "")
	body := `{"model":"nomic-embed-text","embeddings":[[0.1,0.2],[0.3,0.4]],"prompt_eval_count":6}`
	resp := prov_ollama_volc_mkResp(body)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "nomic-embed-text"}}

	usage, apiErr := ollamaEmbeddingHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.PromptTokens != 6 || usage.TotalTokens != 6 {
		t.Errorf("usage = %+v, want PromptTokens=TotalTokens=6", usage)
	}
	if usage.CompletionTokens != 0 {
		t.Errorf("CompletionTokens = %d, want 0 (embeddings have no completion tokens)", usage.CompletionTokens)
	}
	out := w.Body.String()
	if !strings.Contains(out, `"embedding":[0.1,0.2]`) {
		t.Errorf("body should contain the first embedding vector, got %q", out)
	}
	if !strings.Contains(out, `"index":1`) {
		t.Errorf("body should index the second embedding item as 1, got %q", out)
	}
}

func TestProvOllamaVolc_OllamaEmbeddingHandler_UpstreamErrorField(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/embeddings", "")
	body := `{"error":"model not found"}`
	resp := prov_ollama_volc_mkResp(body)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := ollamaEmbeddingHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected an error when upstream returns an 'error' field")
	}
	if !strings.Contains(apiErr.Error(), "model not found") {
		t.Errorf("error message = %q, want to contain upstream error text", apiErr.Error())
	}
	if usage != nil {
		t.Errorf("usage = %+v, want nil", usage)
	}
	_ = w
}

func TestProvOllamaVolc_OllamaEmbeddingHandler_EmptyEmbeddingsArray(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/embeddings", "")
	body := `{"model":"m","embeddings":[],"prompt_eval_count":0}`
	resp := prov_ollama_volc_mkResp(body)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := ollamaEmbeddingHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0", usage.TotalTokens)
	}
	if !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Errorf("body should have an empty data array (not null), got %q", w.Body.String())
	}
}

func TestProvOllamaVolc_OllamaEmbeddingHandler_MalformedJSON(t *testing.T) {
	_, c := prov_ollama_volc_newTestCtx("POST", "/v1/embeddings", "")
	resp := prov_ollama_volc_mkResp(`{not-json`)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	_, apiErr := ollamaEmbeddingHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse: dispatch to the correct handler by RelayMode/IsStream.
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_Adaptor_DoResponse_DispatchesEmbeddings(t *testing.T) {
	a := &Adaptor{}
	_, c := prov_ollama_volc_newTestCtx("POST", "/v1/embeddings", "")
	body := `{"model":"m","embeddings":[[1.0]],"prompt_eval_count":1}`
	resp := prov_ollama_volc_mkResp(body)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{}}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage for embeddings dispatch")
	}
}

func TestProvOllamaVolc_Adaptor_DoResponse_DispatchesNonStreamChat(t *testing.T) {
	a := &Adaptor{}
	_, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	body := `{"model":"llama3","message":{"role":"assistant","content":"hi"},"done":true,"prompt_eval_count":1,"eval_count":1}`
	resp := prov_ollama_volc_mkResp(body)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, IsStream: false, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage for non-stream chat dispatch")
	}
}

func TestProvOllamaVolc_Adaptor_DoResponse_DispatchesStreamChat(t *testing.T) {
	a := &Adaptor{}
	_, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	body := `{"model":"llama3","done":true,"prompt_eval_count":2,"eval_count":2}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage for stream chat dispatch")
	}
}

// ---------------------------------------------------------------------------
// Ollama management API client functions: FetchOllamaModels /
// PullOllamaModel / PullOllamaModelStream / DeleteOllamaModel /
// FetchOllamaVersion. These hit an httptest.Server standing in for a local
// Ollama daemon — never real external network.
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_FetchOllamaModels_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-key")
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3","size":123,"modified_at":"2024-01-01T00:00:00Z"}]}`))
	}))
	defer srv.Close()

	models, err := FetchOllamaModels(srv.URL, "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 1 || models[0].Name != "llama3" {
		t.Errorf("models = %+v, want one model named llama3", models)
	}
	if models[0].Size != 123 {
		t.Errorf("Size = %d, want 123", models[0].Size)
	}
}

func TestProvOllamaVolc_FetchOllamaModels_NoApiKey_NoAuthHeaderSent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization header should be absent when apiKey is empty, got %q", got)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	models, err := FetchOllamaModels(srv.URL, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("models = %+v, want empty", models)
	}
}

func TestProvOllamaVolc_FetchOllamaModels_NonOKStatus_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	_, err := FetchOllamaModels(srv.URL, "")
	if err == nil {
		t.Fatal("expected error for a non-200 upstream status")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v, want it to mention status 500", err)
	}
}

func TestProvOllamaVolc_FetchOllamaModels_MalformedJSON_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	_, err := FetchOllamaModels(srv.URL, "")
	if err == nil {
		t.Fatal("expected error for malformed JSON body")
	}
}

func TestProvOllamaVolc_PullOllamaModel_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("method = %q, want POST", r.Method)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	if err := PullOllamaModel(srv.URL, "key", "llama3"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProvOllamaVolc_PullOllamaModel_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte("model not found"))
	}))
	defer srv.Close()

	err := PullOllamaModel(srv.URL, "", "missing-model")
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %v, want it to mention status 404", err)
	}
}

func TestProvOllamaVolc_PullOllamaModelStream_SuccessTerminatesOnSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fw := w.(http.Flusher)
		_, _ = w.Write([]byte(`{"status":"downloading","completed":10,"total":100}` + "\n"))
		fw.Flush()
		_, _ = w.Write([]byte(`{"status":"success"}` + "\n"))
		fw.Flush()
	}))
	defer srv.Close()

	var progress []OllamaPullResponse
	err := PullOllamaModelStream(srv.URL, "", "llama3", func(p OllamaPullResponse) {
		progress = append(progress, p)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(progress) != 2 {
		t.Fatalf("len(progress) = %d, want 2 callback invocations", len(progress))
	}
	if progress[0].Status != "downloading" || progress[0].Completed != 10 {
		t.Errorf("progress[0] = %+v, want status=downloading completed=10", progress[0])
	}
	if progress[1].Status != "success" {
		t.Errorf("progress[1] = %+v, want status=success", progress[1])
	}
}

func TestProvOllamaVolc_PullOllamaModelStream_ErrorStatusReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"error","digest":"disk full"}` + "\n"))
	}))
	defer srv.Close()

	err := PullOllamaModelStream(srv.URL, "", "llama3", nil)
	if err == nil {
		t.Fatal("expected error when the stream reports status=error")
	}
}

func TestProvOllamaVolc_PullOllamaModelStream_NeverSucceeds_ReturnsIncompleteError(t *testing.T) {
	// Stream ends (upstream closes the connection) without ever emitting a
	// "success" status line -> the pull must be reported as failed, not
	// silently treated as done.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"downloading"}` + "\n"))
	}))
	defer srv.Close()

	err := PullOllamaModelStream(srv.URL, "", "llama3", nil)
	if err == nil {
		t.Fatal("expected an error when the stream ends without a success status")
	}
	if !strings.Contains(err.Error(), "未完成") && !strings.Contains(err.Error(), "success") {
		t.Errorf("error = %v, want it to indicate the pull never completed", err)
	}
}

func TestProvOllamaVolc_PullOllamaModelStream_MalformedLinesAreSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("not-json-garbage\n"))
		_, _ = w.Write([]byte(`{"status":"success"}` + "\n"))
	}))
	defer srv.Close()

	called := 0
	err := PullOllamaModelStream(srv.URL, "", "llama3", func(OllamaPullResponse) { called++ })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called != 1 {
		t.Errorf("callback called %d times, want 1 (malformed line must be silently skipped, not invoke the callback)", called)
	}
}

func TestProvOllamaVolc_DeleteOllamaModel_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/delete" {
			t.Errorf("path = %q, want /api/delete", r.URL.Path)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	if err := DeleteOllamaModel(srv.URL, "key", "llama3"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProvOllamaVolc_DeleteOllamaModel_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte("forbidden"))
	}))
	defer srv.Close()

	err := DeleteOllamaModel(srv.URL, "", "llama3")
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
}

func TestProvOllamaVolc_FetchOllamaVersion_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			t.Errorf("path = %q, want /api/version", r.URL.Path)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"version":"0.5.1"}`))
	}))
	defer srv.Close()

	v, err := FetchOllamaVersion(srv.URL, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "0.5.1" {
		t.Errorf("version = %q, want %q", v, "0.5.1")
	}
}

func TestProvOllamaVolc_FetchOllamaVersion_TrailingSlashNormalized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			t.Errorf("path = %q, want /api/version (trailing slash on base URL must be trimmed)", r.URL.Path)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"version":"0.5.1"}`))
	}))
	defer srv.Close()

	_, err := FetchOllamaVersion(srv.URL+"/", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProvOllamaVolc_FetchOllamaVersion_EmptyBaseURL_ReturnsError(t *testing.T) {
	_, err := FetchOllamaVersion("", "")
	if err == nil {
		t.Fatal("expected error for empty baseURL")
	}
}

func TestProvOllamaVolc_FetchOllamaVersion_EmptyVersionField_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"version":""}`))
	}))
	defer srv.Close()

	_, err := FetchOllamaVersion(srv.URL, "")
	if err == nil {
		t.Fatal("expected error when upstream returns an empty version string")
	}
}

func TestProvOllamaVolc_FetchOllamaVersion_NonOKStatus_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte("unavailable"))
	}))
	defer srv.Close()

	_, err := FetchOllamaVersion(srv.URL, "")
	if err == nil {
		t.Fatal("expected error for 503 response")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error = %v, want it to mention status 503", err)
	}
}
