package coze

// Business-acceptance tests for the Coze adaptor: OpenAI-request-to-Coze
// translation, the create-chat/poll/fetch-detail non-stream orchestration
// (Coze's chat API is asynchronous, unlike OpenAI's synchronous
// chat-completions), the SSE stream event state machine, and usage/billing
// extraction from both paths. Upstream calls are faked with httptest.Server.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"github.com/gin-gonic/gin"
)

func httpNopCloserForTestCoze(r io.Reader) io.ReadCloser { return io.NopCloser(r) }

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

func prov_aws_coze_dify_coze_newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, w
}

// prov_aws_coze_dify_coze_allowLocalFetch relaxes SSRF protection so
// httptest.Server (127.0.0.1) upstreams are reachable, mirroring the pattern
// used by sibling provider packages for the same real-network-transport tests.
func prov_aws_coze_dify_coze_allowLocalFetch(t *testing.T) {
	t.Helper()
	if app.GetHttpClient() == nil {
		app.InitHttpClient()
	}
	fs := system_setting.GetFetchSetting()
	prevSSRF, prevAllow := fs.EnableSSRFProtection, fs.AllowPrivateIp
	fs.EnableSSRFProtection = false
	fs.AllowPrivateIp = true
	t.Cleanup(func() {
		fs := system_setting.GetFetchSetting()
		fs.EnableSSRFProtection, fs.AllowPrivateIp = prevSSRF, prevAllow
	})
}

// ---------------------------------------------------------------------------
// convertCozeChatRequest
// ---------------------------------------------------------------------------

func TestConvertCozeChatRequest_FiltersToUserMessagesOnly(t *testing.T) {
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{Role: "system", Content: "be nice"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "how are you"},
		},
	}
	got := convertCozeChatRequest(c, req)
	if len(got.AdditionalMessages) != 2 {
		t.Fatalf("AdditionalMessages = %+v, want 2 (only user-role messages; system/assistant dropped -- Coze bot owns its own persona/history)", got.AdditionalMessages)
	}
	if got.AdditionalMessages[0].Content != "hello" || got.AdditionalMessages[1].Content != "how are you" {
		t.Errorf("AdditionalMessages = %+v, want user contents in order", got.AdditionalMessages)
	}
	for _, m := range got.AdditionalMessages {
		if m.Role != "user" || m.ContentType != "text" {
			t.Errorf("message = %+v, want role=user contentType=text", m)
		}
	}
}

func TestConvertCozeChatRequest_UserFallsBackToResponseID(t *testing.T) {
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	req := dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	got := convertCozeChatRequest(c, req)
	if got.UserId == "" {
		t.Fatal("UserId must not be empty when caller sent no User field")
	}
	if !strings.HasPrefix(got.UserId, "chatcmpl-") {
		t.Errorf("UserId = %q, want fallback to helper.GetResponseID (chatcmpl- prefixed)", got.UserId)
	}
}

func TestConvertCozeChatRequest_ExplicitUserPreserved(t *testing.T) {
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	req := dto.GeneralOpenAIRequest{User: "user-42", Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	got := convertCozeChatRequest(c, req)
	if got.UserId != "user-42" {
		t.Errorf("UserId = %q, want caller-supplied %q, not the fallback", got.UserId, "user-42")
	}
}

func TestConvertCozeChatRequest_StreamFlagPassthrough(t *testing.T) {
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	got := convertCozeChatRequest(c, dto.GeneralOpenAIRequest{Stream: true, Messages: []dto.Message{{Role: "user", Content: "hi"}}})
	if !got.Stream {
		t.Error("Stream = false, want true (must propagate to the Coze request)")
	}
	got2 := convertCozeChatRequest(c, dto.GeneralOpenAIRequest{Stream: false, Messages: []dto.Message{{Role: "user", Content: "hi"}}})
	if got2.Stream {
		t.Error("Stream = true, want false when caller did not request streaming")
	}
}

func TestConvertCozeChatRequest_BotIdFromContext(t *testing.T) {
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	c.Set("bot_id", "bot-123")
	got := convertCozeChatRequest(c, dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hi"}}})
	if got.BotId != "bot-123" {
		t.Errorf("BotId = %q, want %q from gin context", got.BotId, "bot-123")
	}
}

// ---------------------------------------------------------------------------
// cozeChatHandler (non-stream response translation + usage extraction)
// ---------------------------------------------------------------------------

func TestCozeChatHandler_HappyPath_ExtractsAnswerAndUsage(t *testing.T) {
	c, w := prov_aws_coze_dify_coze_newTestContext()
	c.Set("coze_input_count", 10)
	c.Set("coze_output_count", 5)
	c.Set("coze_token_count", 15)
	body := `{"code":0,"msg":"","data":[{"id":"msg1","type":"answer","content":"the final answer","created_at":1000},{"id":"msg2","type":"follow_up","content":"ignored"}]}`
	resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestCoze(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4-coze"}}
	usage, apiErr := cozeChatHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.PromptTokens != 10 || usage.CompletionTokens != 5 || usage.TotalTokens != 15 {
		t.Errorf("usage = %+v, want {10 5 15} pulled from gin context set by the polling loop", usage)
	}
	var out dto.TextResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("response body not valid JSON: %v (%s)", err, w.Body.String())
	}
	if len(out.Choices) != 1 {
		t.Fatalf("Choices = %+v, want 1", out.Choices)
	}
	if s, ok := out.Choices[0].Content.(string); !ok || s != "the final answer" {
		t.Errorf("Content = %v (%T), want the 'answer'-typed message %q, not follow_up", out.Choices[0].Content, out.Choices[0].Content, "the final answer")
	}
	if out.Model != "gpt-4-coze" {
		t.Errorf("Model = %q, want propagated upstream model name %q", out.Model, "gpt-4-coze")
	}
}

func TestCozeChatHandler_UpstreamErrorCodeReturnsError(t *testing.T) {
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	body := `{"code":4001,"msg":"invalid bot_id","data":[]}`
	resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestCoze(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, apiErr := cozeChatHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for a non-zero Coze response code")
	}
}

func TestCozeChatHandler_MalformedJSONErrors(t *testing.T) {
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestCoze(strings.NewReader(`not json`))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, apiErr := cozeChatHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for malformed upstream JSON")
	}
}

func TestCozeChatHandler_NoAnswerMessage_EmptyContentNoPanic(t *testing.T) {
	// Edge case: only non-"answer" typed messages present (e.g. only
	// "verbose"/"follow_up"). responseContent stays its zero value; must
	// degrade to an empty content field, not panic.
	c, w := prov_aws_coze_dify_coze_newTestContext()
	body := `{"code":0,"msg":"","data":[{"id":"msg1","type":"follow_up","content":"x"}]}`
	resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestCoze(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := cozeChatHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil (zero) usage, not a panic/nil")
	}
	if w.Code == 0 {
		t.Error("expected a response to have been written")
	}
}

// ---------------------------------------------------------------------------
// cozeChatStreamHandler / handleCozeEvent (SSE state machine)
// ---------------------------------------------------------------------------

func sseEvent(event, data string) string {
	return "event: " + event + "\ndata: " + data + "\n\n"
}

func TestCozeChatStreamHandler_FullConversation_AccumulatesUsageAndText(t *testing.T) {
	c, w := prov_aws_coze_dify_coze_newTestContext()
	body := sseEvent("conversation.message.delta", `{"content":"hello "}`) +
		sseEvent("conversation.message.delta", `{"content":"world"}`) +
		sseEvent("conversation.chat.completed", `{"usage":{"token_count":30,"input_count":20,"output_count":10}}`)
	resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestCoze(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4-coze"}}
	usage, apiErr := cozeChatStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.PromptTokens != 20 || usage.CompletionTokens != 10 || usage.TotalTokens != 30 {
		t.Errorf("usage = %+v, want {20 10 30} from conversation.chat.completed", usage)
	}
	if !strings.Contains(w.Body.String(), "hello") || !strings.Contains(w.Body.String(), "world") {
		t.Errorf("streamed body = %q, want to contain both delta fragments", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("streamed body = %q, want terminated with [DONE]", w.Body.String())
	}
}

func TestCozeChatStreamHandler_NoUsageEvent_FallsBackToEstimate(t *testing.T) {
	// When the upstream stream ends without a conversation.chat.completed
	// usage event (usage.TotalTokens stays 0), the handler must fall back to
	// app.ResponseText2Usage from the accumulated text rather than billing a
	// hard zero for a real, billable response.
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	body := sseEvent("conversation.message.delta", `{"content":"some real content that was streamed"}`)
	resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestCoze(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4-coze"}}
	c.Set("coze_input_count", 3)
	usage, apiErr := cozeChatStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.CompletionTokens == 0 {
		t.Error("CompletionTokens = 0, want a non-zero estimate derived from the streamed text (must not silently under-bill)")
	}
	if usage.PromptTokens != 3 {
		t.Errorf("PromptTokens = %d, want 3 (from coze_input_count context value used as the estimate baseline)", usage.PromptTokens)
	}
}

func TestCozeChatStreamHandler_LastEventWithoutTrailingBlankLine(t *testing.T) {
	// Edge case: the upstream connection closes right after the final data
	// line with no trailing blank line to flush it. The handler has an
	// explicit post-loop flush for exactly this ("Last event" comment).
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	body := "event: conversation.chat.completed\ndata: {\"usage\":{\"token_count\":5,\"input_count\":3,\"output_count\":2}}"
	resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestCoze(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := cozeChatStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.TotalTokens != 5 {
		t.Errorf("TotalTokens = %d, want 5: the trailing (unblanked) event must still be flushed and processed", usage.TotalTokens)
	}
}

func TestCozeChatStreamHandler_MalformedEventDataDoesNotAbortStream(t *testing.T) {
	// A single malformed event ({not valid at all) must be logged and
	// skipped, not abort the whole stream -- otherwise one bad chunk from an
	// otherwise-healthy upstream would drop a whole response.
	c, w := prov_aws_coze_dify_coze_newTestContext()
	body := sseEvent("conversation.message.delta", `{not-json`) +
		sseEvent("conversation.message.delta", `{"content":"still works"}`) +
		sseEvent("conversation.chat.completed", `{"usage":{"token_count":2,"input_count":1,"output_count":1}}`)
	resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestCoze(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := cozeChatStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.TotalTokens != 2 {
		t.Errorf("TotalTokens = %d, want 2 (proves the stream continued past the malformed event)", usage.TotalTokens)
	}
	if !strings.Contains(w.Body.String(), "still works") {
		t.Errorf("body = %q, want the later valid delta to still have been forwarded", w.Body.String())
	}
}

func TestCozeChatStreamHandler_ErrorEventDoesNotSetUsage(t *testing.T) {
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	body := sseEvent("error", `{"code":500,"message":"internal error"}`)
	resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestCoze(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4-coze"}}
	usage, apiErr := cozeChatStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	// No message/completed events were seen; usage must not spuriously
	// report tokens for a stream that only ever emitted an error event.
	if usage.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0 for a stream that only emitted an error event with no content", usage.TotalTokens)
	}
}

func TestCozeChatStreamHandler_EmptyBodyProducesZeroUsage(t *testing.T) {
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestCoze(strings.NewReader(""))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := cozeChatStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil zero usage for an empty stream body, not a nil/panic")
	}
}

// ---------------------------------------------------------------------------
// checkIfChatComplete / getChatDetail / doRequest -- real HTTP round trips
// against a fake Coze upstream.
// ---------------------------------------------------------------------------

func TestCheckIfChatComplete_StatusTransitions(t *testing.T) {
	prov_aws_coze_dify_coze_allowLocalFetch(t)

	cases := []struct {
		name       string
		status     string
		wantErr    bool
		wantDone   bool
	}{
		{"completed", "completed", false, true},
		{"still in progress", "in_progress", false, false},
		{"failed", "failed", true, false},
		{"canceled", "canceled", true, false},
		{"requires_action", "requires_action", true, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(200)
				_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"status":"` + tt.status + `","usage":{"token_count":9,"input_count":6,"output_count":3}}}`))
			}))
			defer upstream.Close()

			a := &Adaptor{}
			c, _ := prov_aws_coze_dify_coze_newTestContext()
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL, ApiKey: "sk-test"}}
			err, done := checkIfChatComplete(a, c, info)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if done != tt.wantDone {
				t.Errorf("done = %v, want %v", done, tt.wantDone)
			}
			if tt.status == "completed" {
				if c.GetInt("coze_token_count") != 9 || c.GetInt("coze_input_count") != 6 || c.GetInt("coze_output_count") != 3 {
					t.Errorf("context usage values not set correctly on completion: token=%d input=%d output=%d",
						c.GetInt("coze_token_count"), c.GetInt("coze_input_count"), c.GetInt("coze_output_count"))
				}
			}
		})
	}
}

func TestCheckIfChatComplete_InvalidProxyErrors(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "http://127.0.0.1:1",
			ChannelSetting: dto.ChannelSettings{Proxy: "://not-a-valid-proxy-url"},
		},
	}
	err, done := checkIfChatComplete(a, c, info)
	if err == nil {
		t.Fatal("expected error for a malformed proxy URL")
	}
	if done {
		t.Error("done should be false on error")
	}
}

func TestGetChatDetail_ReturnsUpstreamResponse(t *testing.T) {
	prov_aws_coze_dify_coze_allowLocalFetch(t)

	var gotAuth, gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"code":0,"msg":"","data":[]}`))
	}))
	defer upstream.Close()

	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	c.Set("coze_conversation_id", "conv-1")
	c.Set("coze_chat_id", "chat-1")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL, ApiKey: "sk-detail-key"}}
	resp, err := getChatDetail(a, c, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if gotAuth != "Bearer sk-detail-key" {
		t.Errorf("upstream saw Authorization = %q, want Bearer-prefixed api key", gotAuth)
	}
	if !strings.Contains(gotQuery, "conversation_id=conv-1") || !strings.Contains(gotQuery, "chat_id=chat-1") {
		t.Errorf("query = %q, want conversation_id/chat_id from gin context", gotQuery)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoRequest -- end-to-end non-stream orchestration (create -> poll ->
// fetch detail) and the stream short-circuit.
// ---------------------------------------------------------------------------

func TestAdaptor_DoRequest_NonStream_PollsUntilCompleteThenFetchesDetail(t *testing.T) {
	prov_aws_coze_dify_coze_allowLocalFetch(t)

	var sawDetailFetch bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v3/chat":
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"id":"chat-1","conversation_id":"conv-1","status":"in_progress"}}`))
		case r.URL.Path == "/v3/chat/retrieve":
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"status":"completed","usage":{"token_count":7,"input_count":4,"output_count":3}}}`))
		case r.URL.Path == "/v3/chat/message/list":
			sawDetailFetch = true
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"code":0,"msg":"","data":[{"id":"m1","type":"answer","content":"done"}]}`))
		default:
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()

	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL, ApiKey: "sk-test"}}
	result, err := a.DoRequest(c, info, strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp, ok := result.(*http.Response)
	if !ok {
		t.Fatalf("result type = %T, want *http.Response (from getChatDetail)", result)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if !sawDetailFetch {
		t.Error("expected DoRequest to poll to completion then fetch the message detail")
	}
	if c.GetInt("coze_token_count") != 7 {
		t.Errorf("coze_token_count = %d, want 7 (set by the completed poll)", c.GetInt("coze_token_count"))
	}
}

func TestAdaptor_DoRequest_NonStream_CreateChatErrorCodePropagates(t *testing.T) {
	prov_aws_coze_dify_coze_allowLocalFetch(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"code":4000,"msg":"bad bot id","data":{}}`))
	}))
	defer upstream.Close()

	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL, ApiKey: "sk-test"}}
	_, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	if err == nil {
		t.Fatal("expected error for a non-zero create-chat response code")
	}
	if !strings.Contains(err.Error(), "bad bot id") {
		t.Errorf("error = %q, want to surface the upstream msg", err.Error())
	}
}

func TestAdaptor_DoRequest_NonStream_FailedChatStatusAbortsPolling(t *testing.T) {
	prov_aws_coze_dify_coze_allowLocalFetch(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v3/chat":
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"id":"chat-1","conversation_id":"conv-1","status":"in_progress"}}`))
		case r.URL.Path == "/v3/chat/retrieve":
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"status":"failed"}}`))
		default:
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()

	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL, ApiKey: "sk-test"}}
	_, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	if err == nil {
		t.Fatal("expected error when the upstream chat status becomes 'failed'")
	}
}

func TestAdaptor_DoRequest_StreamModeShortCircuitsToDoApiRequest(t *testing.T) {
	prov_aws_coze_dify_coze_allowLocalFetch(t)

	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(200)
		_, _ = w.Write([]byte("event: conversation.chat.completed\ndata: {}\n\n"))
	}))
	defer upstream.Close()

	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL, ApiKey: "sk-test"}, IsStream: true}
	result, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp, ok := result.(*http.Response)
	if !ok {
		t.Fatalf("result type = %T, want *http.Response", result)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if gotPath != "/v3/chat" {
		t.Errorf("upstream path = %q, want /v3/chat (a single direct call, not the create/poll/fetch dance)", gotPath)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse dispatch
// ---------------------------------------------------------------------------

func TestAdaptor_DoResponse_DispatchesStreamVsNonStream(t *testing.T) {
	a := &Adaptor{}
	t.Run("stream", func(t *testing.T) {
		c, w := prov_aws_coze_dify_coze_newTestContext()
		body := sseEvent("conversation.chat.completed", `{"usage":{"token_count":1,"input_count":1,"output_count":0}}`)
		resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestCoze(strings.NewReader(body))}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, IsStream: true}
		_, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %+v", apiErr)
		}
		if !strings.Contains(w.Body.String(), "[DONE]") {
			t.Error("stream dispatch should terminate with [DONE], proving cozeChatStreamHandler ran")
		}
	})
	t.Run("non-stream", func(t *testing.T) {
		c, w := prov_aws_coze_dify_coze_newTestContext()
		body := `{"code":0,"msg":"","data":[{"id":"m1","type":"answer","content":"hi"}]}`
		resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestCoze(strings.NewReader(body))}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, IsStream: false}
		_, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %+v", apiErr)
		}
		if !strings.Contains(w.Body.String(), `"choices"`) {
			t.Error("non-stream dispatch should write an OpenAI-shaped JSON response, proving cozeChatHandler ran")
		}
	})
}

// ---------------------------------------------------------------------------
// GetRequestURL / SetupRequestHeader / GetModelList / GetChannelName / Init /
// unimplemented stubs
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.coze.cn"}}
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://api.coze.cn/v3/chat" {
		t.Errorf("url = %q, want %q", url, "https://api.coze.cn/v3/chat")
	}
}

func TestAdaptor_SetupRequestHeader(t *testing.T) {
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-coze-secret"}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if header.Get("Authorization") != "Bearer sk-coze-secret" {
		t.Errorf("Authorization = %q, want Bearer-prefixed api key", header.Get("Authorization"))
	}
}

func TestAdaptor_GetModelList_And_GetChannelName(t *testing.T) {
	a := &Adaptor{}
	if len(a.GetModelList()) == 0 {
		t.Error("model list must not be empty")
	}
	if got := a.GetChannelName(); got != "coze" {
		t.Errorf("GetChannelName() = %q, want %q", got, "coze")
	}
}

func TestAdaptor_Init_NoPanic(t *testing.T) {
	a := &Adaptor{}
	a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
}

func TestAdaptor_UnimplementedStubsReturnErrors(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	if _, err := a.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{}); err == nil {
		t.Error("ConvertGeminiRequest: expected not-implemented error")
	}
	if _, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{}); err == nil {
		t.Error("ConvertAudioRequest: expected not-implemented error")
	}
	if _, err := a.ConvertClaudeRequest(c, info, &dto.ClaudeRequest{}); err == nil {
		t.Error("ConvertClaudeRequest: expected not-implemented error")
	}
	if _, err := a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{}); err == nil {
		t.Error("ConvertEmbeddingRequest: expected not-implemented error")
	}
	if _, err := a.ConvertImageRequest(c, info, dto.ImageRequest{}); err == nil {
		t.Error("ConvertImageRequest: expected not-implemented error")
	}
	if _, err := a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{}); err == nil {
		t.Error("ConvertOpenAIResponsesRequest: expected not-implemented error")
	}
	if _, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{}); err == nil {
		t.Error("ConvertRerankRequest: expected not-implemented error")
	}
	if _, err := a.ConvertOpenAIRequest(c, info, nil); err == nil {
		t.Error("ConvertOpenAIRequest: expected error for nil request")
	}
}

func TestAdaptor_ConvertOpenAIRequest_SuccessDelegatesToConversion(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "convert me"}}}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cozeReq, ok := got.(*CozeChatRequest)
	if !ok {
		t.Fatalf("result type = %T, want *CozeChatRequest", got)
	}
	if len(cozeReq.AdditionalMessages) != 1 || cozeReq.AdditionalMessages[0].Content != "convert me" {
		t.Errorf("AdditionalMessages = %+v, want single converted user message", cozeReq.AdditionalMessages)
	}
}

// ---------------------------------------------------------------------------
// handleCozeEvent edge cases (malformed payloads per event type must not
// abort stream processing -- covered end-to-end above; these drill into the
// individual unmarshal-error branches directly).
// ---------------------------------------------------------------------------

func TestHandleCozeEvent_MalformedChatCompletedData_DoesNotSetUsage(t *testing.T) {
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	var responseText string
	usage := &dto.Usage{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	handleCozeEvent(c, "conversation.chat.completed", `{not-json`, &responseText, usage, "id1", info)
	if usage.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0: malformed usage payload must not silently produce a bogus bill", usage.TotalTokens)
	}
}

func TestHandleCozeEvent_MalformedMessageDeltaEnvelope_DoesNotAppendText(t *testing.T) {
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	responseText := "existing"
	usage := &dto.Usage{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	handleCozeEvent(c, "conversation.message.delta", `{not-json`, &responseText, usage, "id1", info)
	if responseText != "existing" {
		t.Errorf("responseText = %q, want unchanged when the delta envelope itself is malformed", responseText)
	}
}

func TestHandleCozeEvent_MessageDeltaWithNonStringContent_DoesNotAppendText(t *testing.T) {
	// messageData.Content is valid JSON (a raw message envelope) but its
	// inner Content payload is not itself a JSON string -- the second
	// json.Unmarshal (into `content string`) must fail gracefully.
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	responseText := "existing"
	usage := &dto.Usage{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	handleCozeEvent(c, "conversation.message.delta", `{"content":42}`, &responseText, usage, "id1", info)
	if responseText != "existing" {
		t.Errorf("responseText = %q, want unchanged when inner content is not a JSON string", responseText)
	}
}

func TestHandleCozeEvent_MalformedErrorEventDoesNotPanic(t *testing.T) {
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	var responseText string
	usage := &dto.Usage{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	// Must simply return without panicking; nothing to assert on responseText/usage.
	handleCozeEvent(c, "error", `{not-json`, &responseText, usage, "id1", info)
}

func TestHandleCozeEvent_UnknownEventTypeIgnored(t *testing.T) {
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	responseText := "before"
	usage := &dto.Usage{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	handleCozeEvent(c, "some.unknown.event", `{"whatever":true}`, &responseText, usage, "id1", info)
	if responseText != "before" || usage.TotalTokens != 0 {
		t.Errorf("unknown event types must be no-ops: responseText=%q usage=%+v", responseText, usage)
	}
}

// ---------------------------------------------------------------------------
// checkIfChatComplete / getChatDetail additional edge cases
// ---------------------------------------------------------------------------

func TestCheckIfChatComplete_MalformedUpstreamJSONErrors(t *testing.T) {
	prov_aws_coze_dify_coze_allowLocalFetch(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`not json at all`))
	}))
	defer upstream.Close()

	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL, ApiKey: "sk-test"}}
	err, done := checkIfChatComplete(a, c, info)
	if err == nil {
		t.Fatal("expected error for malformed retrieve response JSON")
	}
	if done {
		t.Error("done should be false on a parse error")
	}
}

func TestGetChatDetail_InvalidProxyErrors(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "http://127.0.0.1:1",
			ChannelSetting: dto.ChannelSettings{Proxy: "://not-a-valid-proxy-url"},
		},
	}
	bcResp0, err := getChatDetail(a, c, info)
	defer func() {
		if bcResp0 != nil {
			_ = bcResp0.Body.Close()
		}
	}()
	if err == nil {
		t.Fatal("expected error for a malformed proxy URL")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoRequest: polling loop actually iterates (in_progress once, then
// completed) -- exercises the time.Sleep-then-retry branch, not just the
// immediate-completion fast path.
// ---------------------------------------------------------------------------

func TestAdaptor_DoRequest_NonStream_PollingLoopIteratesOnceBeforeCompleting(t *testing.T) {
	prov_aws_coze_dify_coze_allowLocalFetch(t)

	var retrieveCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v3/chat":
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"id":"chat-1","conversation_id":"conv-1","status":"in_progress"}}`))
		case r.URL.Path == "/v3/chat/retrieve":
			retrieveCalls++
			status := "in_progress"
			if retrieveCalls >= 2 {
				status = "completed"
			}
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"status":"` + status + `","usage":{"token_count":1,"input_count":1,"output_count":0}}}`))
		case r.URL.Path == "/v3/chat/message/list":
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"code":0,"msg":"","data":[]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()

	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL, ApiKey: "sk-test"}}
	result, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := result.(*http.Response)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if retrieveCalls < 2 {
		t.Errorf("retrieveCalls = %d, want >= 2 (proves the poll loop actually retried after an in_progress status)", retrieveCalls)
	}
}
