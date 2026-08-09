package ollama

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Adaptor.ConvertClaudeRequest: Claude -> OpenAI -> Ollama chat, forcing
// stream usage inclusion so downstream billing always gets a usage frame.
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_Adaptor_ConvertClaudeRequest_MapsThroughOpenAIToOllamaChat(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	req := &dto.ClaudeRequest{
		Model:     "claude-3-opus",
		MaxTokens: 100,
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello from claude"},
		},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	out, err := a.ConvertClaudeRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	chatReq, ok := out.(*OllamaChatRequest)
	if !ok {
		t.Fatalf("expected *OllamaChatRequest, got %T", out)
	}
	if len(chatReq.Messages) != 1 || chatReq.Messages[0].Content != "hello from claude" {
		t.Errorf("Messages = %+v, want the Claude user message translated through", chatReq.Messages)
	}
	// num_predict must reflect Claude's required max_tokens field, not be
	// dropped in the Claude->OpenAI->Ollama double hop.
	if chatReq.Options["num_predict"] != 100 {
		t.Errorf("Options[num_predict] = %v, want 100 (from Claude MaxTokens)", chatReq.Options["num_predict"])
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoRequest / GetRequestURL / SetupRequestHeader wired end-to-end
// against a local httptest upstream (never real external network).
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_Adaptor_DoRequest_SendsAuthHeaderAndHitsChatEndpoint(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"model":"llama3","done":true}`))
	}))
	defer srv.Close()

	app.InitHttpClient()
	// The stub upstream above listens on loopback, which the relay SSRF dial
	// guard blocks by design (internal/app/relay_dial_guard.go). Allow
	// private destinations for the duration of this test only so it exercises
	// the DoApiRequest transport wiring itself rather than the
	// (separately-tested elsewhere) dial guard.
	fs := system_setting.GetFetchSetting()
	prevAllow := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	defer func() { system_setting.GetFetchSetting().AllowPrivateIp = prevAllow }()

	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "route-key"},
	}
	resp, err := a.DoRequest(c, info, strings.NewReader(`{"model":"llama3"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	httpResp, ok := resp.(*http.Response)
	if !ok {
		t.Fatalf("expected *http.Response, got %T", resp)
	}
	defer func() {
		if httpResp != nil {
			_ = httpResp.Body.Close()
		}
	}()
	if httpResp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", httpResp.StatusCode)
	}
	if gotAuth != "Bearer route-key" {
		t.Errorf("upstream received Authorization = %q, want %q", gotAuth, "Bearer route-key")
	}
	if gotPath != "/api/chat" {
		t.Errorf("upstream received path = %q, want %q (chat mode routes to /api/chat)", gotPath, "/api/chat")
	}
}

// ---------------------------------------------------------------------------
// openAIToGenerate: remaining parameter-mapping branches not exercised by
// the primary request test (TopK/FrequencyPenalty/PresencePenalty/Seed, plus
// the []string and []any stop-sequence variants for the /generate path).
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_OpenAIToGenerate_RemainingOptionParams(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/completions", nil)

	r := &dto.GeneralOpenAIRequest{
		Model:            "llama3",
		TopK:             33,
		FrequencyPenalty: 0.6,
		PresencePenalty:  0.7,
		Seed:             99,
	}
	out, err := openAIToGenerate(c, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Options["top_k"] != 33 {
		t.Errorf("Options[top_k] = %v, want 33", out.Options["top_k"])
	}
	if out.Options["frequency_penalty"] != 0.6 {
		t.Errorf("Options[frequency_penalty] = %v, want 0.6", out.Options["frequency_penalty"])
	}
	if out.Options["presence_penalty"] != 0.7 {
		t.Errorf("Options[presence_penalty] = %v, want 0.7", out.Options["presence_penalty"])
	}
	if out.Options["seed"] != 99 {
		t.Errorf("Options[seed] = %v, want 99", out.Options["seed"])
	}
}

func TestProvOllamaVolc_OpenAIToGenerate_StopSequenceSliceVariants(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/completions", nil)

	r1 := &dto.GeneralOpenAIRequest{Model: "llama3", Stop: []string{"A", "B"}}
	out1, err := openAIToGenerate(c, r1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stop1, ok := out1.Options["stop"].([]string)
	if !ok || len(stop1) != 2 {
		t.Errorf("Options[stop] = %v, want [A B]", out1.Options["stop"])
	}

	r2 := &dto.GeneralOpenAIRequest{Model: "llama3", Stop: []any{"X", 1, "Y"}}
	out2, err := openAIToGenerate(c, r2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stop2, ok := out2.Options["stop"].([]string)
	if !ok || len(stop2) != 2 || stop2[0] != "X" || stop2[1] != "Y" {
		t.Errorf("Options[stop] = %v, want [X Y] with non-string entries filtered", out2.Options["stop"])
	}

	r3 := &dto.GeneralOpenAIRequest{Model: "llama3", Stop: []any{1, 2}}
	out3, err := openAIToGenerate(c, r3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := out3.Options["stop"]; ok {
		t.Errorf("Options[stop] should be absent when the []any slice has no string entries, got %v", out3.Options["stop"])
	}
}

// ---------------------------------------------------------------------------
// ollamaChatHandler: debug-log branch must not alter observable success
// behavior (matches the pattern already used for the Claude handler's debug
// branch).
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_OllamaChatHandler_DebugEnabled_DoesNotBreakSuccess(t *testing.T) {
	prevDebug := common.DebugEnabled
	common.DebugEnabled = true
	defer func() { common.DebugEnabled = prevDebug }()

	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	body := `{"model":"llama3","message":{"role":"assistant","content":"debug ok"},"done":true,"prompt_eval_count":1,"eval_count":1}`
	resp := prov_ollama_volc_mkResp(body)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3"}}

	usage, apiErr := ollamaChatHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error with DebugEnabled=true: %+v", apiErr)
	}
	if usage.TotalTokens != 2 {
		t.Errorf("TotalTokens = %d, want 2", usage.TotalTokens)
	}
	if !strings.Contains(w.Body.String(), "debug ok") {
		t.Errorf("body should still contain the response content with debug logging enabled, got %q", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// contentPtr: empty completion text must serialize as a nil (omitted)
// content field, not an empty-string content field — this affects how
// clients render "no text response" (e.g. tool-call-only turns).
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_OllamaChatHandler_EmptyCompletionContent_OmitsContentField(t *testing.T) {
	w, c := prov_ollama_volc_newTestCtx("POST", "/v1/chat/completions", "")
	body := `{"model":"llama3","message":{"role":"assistant","content":""},"done":true,"done_reason":"tool_calls","prompt_eval_count":1,"eval_count":0}`
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
	if strings.Contains(w.Body.String(), `"content":""`) {
		t.Errorf("empty completion content should be omitted (nil), not serialized as an empty string, got %q", w.Body.String())
	}
}
