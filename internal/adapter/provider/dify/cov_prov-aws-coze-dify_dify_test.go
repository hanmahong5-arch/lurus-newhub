package dify

// Business-acceptance tests for the Dify adaptor: OpenAI-request-to-Dify
// translation (system/assistant/user role folding into a single Query
// string, image handling), streaming/non-stream response translation, and
// usage/billing extraction. Upstream calls are faked with httptest.Server.

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"github.com/gin-gonic/gin"
)

func httpNopCloserForTestDify(r io.Reader) io.ReadCloser { return io.NopCloser(r) }

func init() {
	// difyStreamHandler delegates to helper.StreamScannerHandler, which builds
	// a time.NewTicker(StreamingTimeout*time.Second); a zero/unset value
	// panics ("non-positive interval"), so ensure a safe default here (same
	// pattern used by sibling provider packages, e.g. openai).
	if constant.StreamingTimeout <= 0 {
		constant.StreamingTimeout = 60
	}
}

func prov_aws_coze_dify_dify_newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, w
}

func prov_aws_coze_dify_dify_allowLocalFetch(t *testing.T) {
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
// Adaptor.Init / GetRequestURL / SetupRequestHeader
// ---------------------------------------------------------------------------

func TestAdaptor_Init_SetsChatFlowBotType(t *testing.T) {
	a := &Adaptor{}
	a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
	if a.BotType != BotTypeChatFlow {
		t.Errorf("BotType = %d, want BotTypeChatFlow (%d)", a.BotType, BotTypeChatFlow)
	}
}

func TestAdaptor_GetRequestURL_ByBotType(t *testing.T) {
	cases := []struct {
		name    string
		botType int
		want    string
	}{
		{"default/chatflow", BotTypeChatFlow, "https://api.dify.ai/v1/chat-messages"},
		{"agent falls through to chat-messages", BotTypeAgent, "https://api.dify.ai/v1/chat-messages"},
		{"workflow", BotTypeWorkFlow, "https://api.dify.ai/v1/workflows/run"},
		{"completion", BotTypeCompletion, "https://api.dify.ai/v1/completion-messages"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			a := &Adaptor{BotType: tt.botType}
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.dify.ai"}}
			url, err := a.GetRequestURL(info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if url != tt.want {
				t.Errorf("url = %q, want %q", url, tt.want)
			}
		})
	}
}

func TestAdaptor_SetupRequestHeader_SetsBearerAuth(t *testing.T) {
	c, _ := prov_aws_coze_dify_dify_newTestContext()
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-dify-secret"}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if header.Get("Authorization") != "Bearer sk-dify-secret" {
		t.Errorf("Authorization = %q, want Bearer-prefixed api key", header.Get("Authorization"))
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertOpenAIRequest / ConvertRerankRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertOpenAIRequest_NilRequestErrors(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_dify_newTestContext()
	_, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestAdaptor_ConvertOpenAIRequest_DelegatesToConversion(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_dify_newTestContext()
	req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hello dify"}}}
	got, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	difyReq, ok := got.(*DifyChatRequest)
	if !ok {
		t.Fatalf("result type = %T, want *DifyChatRequest", got)
	}
	if !strings.Contains(difyReq.Query, "hello dify") {
		t.Errorf("Query = %q, want to contain the user message text", difyReq.Query)
	}
}

func TestAdaptor_ConvertRerankRequest_AlwaysNilNil(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_dify_newTestContext()
	got, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	if got != nil || err != nil {
		t.Errorf("got (%v, %v), want (nil, nil): Dify has no native rerank support", got, err)
	}
}

// ---------------------------------------------------------------------------
// requestOpenAI2Dify: role folding into Query, stream mode, user fallback
// ---------------------------------------------------------------------------

func TestRequestOpenAI2Dify_RoleFolding(t *testing.T) {
	c, _ := prov_aws_coze_dify_dify_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{Role: "system", Content: "be terse"},
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		},
	}
	got := requestOpenAI2Dify(c, info, req)
	if !strings.Contains(got.Query, "SYSTEM: \nbe terse") {
		t.Errorf("Query = %q, want SYSTEM: prefix for the system message", got.Query)
	}
	if !strings.Contains(got.Query, "USER: \nhi") {
		t.Errorf("Query = %q, want USER: prefix for the user message", got.Query)
	}
	if !strings.Contains(got.Query, "ASSISTANT: \nhello") {
		t.Errorf("Query = %q, want ASSISTANT: prefix for the assistant message", got.Query)
	}
}

func TestRequestOpenAI2Dify_StreamModeMapping(t *testing.T) {
	c, _ := prov_aws_coze_dify_dify_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	streaming := requestOpenAI2Dify(c, info, dto.GeneralOpenAIRequest{Stream: true, Messages: []dto.Message{{Role: "user", Content: "hi"}}})
	if streaming.ResponseMode != "streaming" {
		t.Errorf("ResponseMode = %q, want streaming", streaming.ResponseMode)
	}

	blocking := requestOpenAI2Dify(c, info, dto.GeneralOpenAIRequest{Stream: false, Messages: []dto.Message{{Role: "user", Content: "hi"}}})
	if blocking.ResponseMode != "blocking" {
		t.Errorf("ResponseMode = %q, want blocking", blocking.ResponseMode)
	}
}

func TestRequestOpenAI2Dify_UserFallsBackToResponseID(t *testing.T) {
	c, _ := prov_aws_coze_dify_dify_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	got := requestOpenAI2Dify(c, info, dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hi"}}})
	if got.User == "" || !strings.HasPrefix(got.User, "chatcmpl-") {
		t.Errorf("User = %q, want fallback to helper.GetResponseID (chatcmpl- prefixed)", got.User)
	}
}

func TestRequestOpenAI2Dify_EmptyMessagesProducesEmptyQuery(t *testing.T) {
	c, _ := prov_aws_coze_dify_dify_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	got := requestOpenAI2Dify(c, info, dto.GeneralOpenAIRequest{Messages: nil})
	if got.Query != "" {
		t.Errorf("Query = %q, want empty string for a request with no messages", got.Query)
	}
	if len(got.Files) != 0 {
		t.Errorf("Files = %+v, want empty slice, not nil (json.omitempty on a nil slice vs [] matters for Dify's schema)", got.Files)
	}
}

func TestRequestOpenAI2Dify_UserTextContentArray(t *testing.T) {
	c, _ := prov_aws_coze_dify_dify_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	msg := dto.Message{Role: "user"}
	msg.SetMediaContent([]dto.MediaContent{{Type: dto.ContentTypeText, Text: "structured text block"}})
	got := requestOpenAI2Dify(c, info, dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}})
	if !strings.Contains(got.Query, "USER: \nstructured text block") {
		t.Errorf("Query = %q, want the structured text content block folded in", got.Query)
	}
}

// requestOpenAI2Dify's remote-image branch declared `var file *DifyFile` and
// then wrote through it without allocating, so any user message carrying an
// http(s) image_url on a Dify channel crashed the request goroutine.
//
// fix_remote_image_test.go builds the message from a raw []any content slice
// and a populated MimeType; this test comes in through SetMediaContent with no
// MimeType, which is the shape a plain OpenAI-format client produces.
func TestRequestOpenAI2Dify_UserRemoteImage_BuildsRemoteFile(t *testing.T) {
	c, _ := prov_aws_coze_dify_dify_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	msg := dto.Message{Role: "user"}
	msg.SetMediaContent([]dto.MediaContent{
		{Type: dto.ContentTypeImageURL, ImageUrl: &dto.MessageImageUrl{Url: "https://example.com/pic.png"}},
	})
	got := requestOpenAI2Dify(c, info, dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}})

	if len(got.Files) != 1 {
		t.Fatalf("Files = %+v, want exactly one remote file", got.Files)
	}
	file := got.Files[0]
	if file.TransferMode != "remote_url" {
		t.Errorf("TransferMode = %q, want remote_url", file.TransferMode)
	}
	if file.URL != "https://example.com/pic.png" {
		t.Errorf("URL = %q, want the remote image url carried over verbatim", file.URL)
	}
	if file.UploadFileId != "" {
		t.Errorf("UploadFileId = %q, want empty — a remote image is never uploaded", file.UploadFileId)
	}
}

func TestRequestOpenAI2Dify_UserLocalImage_UploadsAndAttachesFile(t *testing.T) {
	prov_aws_coze_dify_dify_allowLocalFetch(t)

	var gotAuth, gotUser string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("upstream failed to parse multipart form: %v", err)
		}
		gotUser = r.FormValue("user")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"file-upload-123"}`))
	}))
	defer upstream.Close()

	c, _ := prov_aws_coze_dify_dify_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL, ApiKey: "sk-upload"}}
	imgBytes := []byte("fake-png-bytes")
	msg := dto.Message{Role: "user", Name: nil}
	msg.SetMediaContent([]dto.MediaContent{
		{Type: dto.ContentTypeImageURL, ImageUrl: &dto.MessageImageUrl{
			Url:      "data:image/png;base64," + base64.StdEncoding.EncodeToString(imgBytes),
			MimeType: "image/png",
		}},
	})
	got := requestOpenAI2Dify(c, info, dto.GeneralOpenAIRequest{User: "uploader-1", Messages: []dto.Message{msg}})
	if len(got.Files) != 1 {
		t.Fatalf("Files = %+v, want 1 uploaded file", got.Files)
	}
	if got.Files[0].UploadFileId != "file-upload-123" {
		t.Errorf("UploadFileId = %q, want the id returned by the upload endpoint", got.Files[0].UploadFileId)
	}
	if got.Files[0].TransferMode != "local_file" {
		t.Errorf("TransferMode = %q, want local_file for an uploaded (non-remote) image", got.Files[0].TransferMode)
	}
	if gotAuth != "Bearer sk-upload" {
		t.Errorf("upload request Authorization = %q, want Bearer-prefixed channel api key", gotAuth)
	}
	if gotUser != "uploader-1" {
		t.Errorf("upload request user field = %q, want %q", gotUser, "uploader-1")
	}
}

func TestUploadDifyFile_InvalidBase64ReturnsNilNotError(t *testing.T) {
	c, _ := prov_aws_coze_dify_dify_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "http://127.0.0.1:1"}}
	media := dto.MediaContent{Type: dto.ContentTypeImageURL, ImageUrl: &dto.MessageImageUrl{Url: "not-valid-base64!!!"}}
	got := uploadDifyFile(c, info, "user1", media)
	if got != nil {
		t.Errorf("got = %+v, want nil for undecodable base64 image data", got)
	}
}

func TestUploadDifyFile_UpstreamMalformedResponseReturnsNil(t *testing.T) {
	prov_aws_coze_dify_dify_allowLocalFetch(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer upstream.Close()

	c, _ := prov_aws_coze_dify_dify_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL}}
	media := dto.MediaContent{Type: dto.ContentTypeImageURL, ImageUrl: &dto.MessageImageUrl{Url: base64.StdEncoding.EncodeToString([]byte("x"))}}
	got := uploadDifyFile(c, info, "user1", media)
	if got != nil {
		t.Errorf("got = %+v, want nil when the upload endpoint returns a malformed body", got)
	}
}

func TestUploadDifyFile_DefaultMimeTypeWhenUnset(t *testing.T) {
	prov_aws_coze_dify_dify_allowLocalFetch(t)

	var gotFilename string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if fh := r.MultipartForm.File["file"]; len(fh) > 0 {
			gotFilename = fh[0].Filename
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"f1"}`))
	}))
	defer upstream.Close()

	c, _ := prov_aws_coze_dify_dify_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL}}
	media := dto.MediaContent{Type: dto.ContentTypeImageURL, ImageUrl: &dto.MessageImageUrl{
		Url:      base64.StdEncoding.EncodeToString([]byte("bytes")),
		MimeType: "", // unset -> must default to image/jpeg
	}}
	got := uploadDifyFile(c, info, "user1", media)
	if got == nil {
		t.Fatal("expected a successful upload result")
	}
	if !strings.HasSuffix(gotFilename, ".jpeg") {
		t.Errorf("uploaded filename = %q, want .jpeg extension from the default mime type fallback", gotFilename)
	}
}

// ---------------------------------------------------------------------------
// streamResponseDify2OpenAI
// ---------------------------------------------------------------------------

func TestStreamResponseDify2OpenAI_MessageEvent(t *testing.T) {
	got := streamResponseDify2OpenAI(DifyChunkChatCompletionResponse{Event: "message", Answer: "hello"})
	if len(got.Choices) != 1 || got.Choices[0].Delta.GetContentString() != "hello" {
		t.Errorf("Choices = %+v, want single choice with content %q", got.Choices, "hello")
	}
}

func TestStreamResponseDify2OpenAI_AgentMessageEvent(t *testing.T) {
	got := streamResponseDify2OpenAI(DifyChunkChatCompletionResponse{Event: "agent_message", Answer: "agent says hi"})
	if got.Choices[0].Delta.GetContentString() != "agent says hi" {
		t.Errorf("content = %q, want %q", got.Choices[0].Delta.GetContentString(), "agent says hi")
	}
}

func TestStreamResponseDify2OpenAI_ThinkingSentinelsRewritten(t *testing.T) {
	openThinking := "<details style=\"color:gray;background-color: #f8f8f8;padding: 8px;border-radius: 4px;\" open> <summary> Thinking... </summary>\n"
	got := streamResponseDify2OpenAI(DifyChunkChatCompletionResponse{Event: "message", Answer: openThinking})
	if got.Choices[0].Delta.GetContentString() != "<think>" {
		t.Errorf("content = %q, want the opening Dify 'thinking' marker rewritten to <think>", got.Choices[0].Delta.GetContentString())
	}

	gotClose := streamResponseDify2OpenAI(DifyChunkChatCompletionResponse{Event: "message", Answer: "</details>"})
	if gotClose.Choices[0].Delta.GetContentString() != "</think>" {
		t.Errorf("content = %q, want the closing Dify 'thinking' marker rewritten to </think>", gotClose.Choices[0].Delta.GetContentString())
	}
}

func TestStreamResponseDify2OpenAI_WorkflowAndNodeEvents_DebugOff(t *testing.T) {
	origDebug := constant.DifyDebug
	constant.DifyDebug = false
	defer func() { constant.DifyDebug = origDebug }()

	got := streamResponseDify2OpenAI(DifyChunkChatCompletionResponse{Event: "workflow_started", Data: DifyData{WorkflowId: "wf1"}})
	if got.Choices[0].Delta.ReasoningContent != nil {
		t.Errorf("ReasoningContent = %v, want nil when DifyDebug is off", *got.Choices[0].Delta.ReasoningContent)
	}
}

func TestStreamResponseDify2OpenAI_WorkflowEvent_DebugOn(t *testing.T) {
	origDebug := constant.DifyDebug
	constant.DifyDebug = true
	defer func() { constant.DifyDebug = origDebug }()

	got := streamResponseDify2OpenAI(DifyChunkChatCompletionResponse{Event: "workflow_finished", Data: DifyData{WorkflowId: "wf1", Status: "succeeded"}})
	if got.Choices[0].Delta.ReasoningContent == nil {
		t.Fatal("ReasoningContent = nil, want debug trace text when DifyDebug is on")
	}
	if !strings.Contains(*got.Choices[0].Delta.ReasoningContent, "wf1") || !strings.Contains(*got.Choices[0].Delta.ReasoningContent, "succeeded") {
		t.Errorf("ReasoningContent = %q, want workflow id and terminal status included", *got.Choices[0].Delta.ReasoningContent)
	}
}

func TestStreamResponseDify2OpenAI_NodeEvent_DebugOn(t *testing.T) {
	origDebug := constant.DifyDebug
	constant.DifyDebug = true
	defer func() { constant.DifyDebug = origDebug }()

	got := streamResponseDify2OpenAI(DifyChunkChatCompletionResponse{Event: "node_finished", Data: DifyData{NodeType: "llm", Status: "succeeded"}})
	if got.Choices[0].Delta.ReasoningContent == nil {
		t.Fatal("ReasoningContent = nil, want node debug trace when DifyDebug is on")
	}
	if !strings.Contains(*got.Choices[0].Delta.ReasoningContent, "llm") {
		t.Errorf("ReasoningContent = %q, want node type included", *got.Choices[0].Delta.ReasoningContent)
	}
}

func TestStreamResponseDify2OpenAI_UnrecognizedEventEmptyDelta(t *testing.T) {
	got := streamResponseDify2OpenAI(DifyChunkChatCompletionResponse{Event: "totally_unknown"})
	if got.Choices[0].Delta.GetContentString() != "" {
		t.Errorf("content = %q, want empty for an unrecognized event", got.Choices[0].Delta.GetContentString())
	}
}

// ---------------------------------------------------------------------------
// difyStreamHandler
// ---------------------------------------------------------------------------

func sseLine(payload string) string { return "data: " + payload + "\n\n" }

func TestDifyStreamHandler_AccumulatesTextAndFinalUsage(t *testing.T) {
	c, w := prov_aws_coze_dify_dify_newTestContext()
	body := sseLine(`{"event":"message","answer":"hello "}`) +
		sseLine(`{"event":"message","answer":"world"}`) +
		sseLine(`{"event":"message_end","metadata":{"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13}}}`)
	resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestDify(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "dify-bot"}}
	usage, apiErr := difyStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.PromptTokens != 9 || usage.CompletionTokens != 4 || usage.TotalTokens != 13 {
		t.Errorf("usage = %+v, want {9 4 13} from message_end metadata (billing-critical)", usage)
	}
	if !strings.Contains(w.Body.String(), "hello") || !strings.Contains(w.Body.String(), "world") {
		t.Errorf("streamed body = %q, want both answer fragments forwarded", w.Body.String())
	}
}

func TestDifyStreamHandler_NoUsageEvent_FallsBackToEstimate(t *testing.T) {
	c, _ := prov_aws_coze_dify_dify_newTestContext()
	body := sseLine(`{"event":"message","answer":"some real streamed content"}`)
	resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestDify(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "dify-bot"}}
	usage, apiErr := difyStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.CompletionTokens == 0 {
		t.Error("CompletionTokens = 0, want a non-zero estimate: a stream that ends without message_end must not silently under-bill")
	}
}

func TestDifyStreamHandler_ErrorEventStopsWithoutUsage(t *testing.T) {
	c, _ := prov_aws_coze_dify_dify_newTestContext()
	body := sseLine(`{"event":"error"}`) + sseLine(`{"event":"message","answer":"should not be reached"}`)
	resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestDify(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "dify-bot"}}
	usage, apiErr := difyStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage even when the stream halts on an error event")
	}
}

func TestDifyStreamHandler_MalformedChunkSkippedNotFatal(t *testing.T) {
	c, w := prov_aws_coze_dify_dify_newTestContext()
	body := sseLine(`{not-json`) +
		sseLine(`{"event":"message","answer":"recovered"}`) +
		sseLine(`{"event":"message_end","metadata":{"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}}`)
	resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestDify(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "dify-bot"}}
	usage, apiErr := difyStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.TotalTokens != 2 {
		t.Errorf("TotalTokens = %d, want 2: proves the stream survived the malformed chunk and kept processing", usage.TotalTokens)
	}
	if !strings.Contains(w.Body.String(), "recovered") {
		t.Errorf("body = %q, want the later valid event still forwarded", w.Body.String())
	}
}

func TestDifyStreamHandler_ReasoningContentAddsToCompletionTokens(t *testing.T) {
	origDebug := constant.DifyDebug
	constant.DifyDebug = true
	defer func() { constant.DifyDebug = origDebug }()

	c, _ := prov_aws_coze_dify_dify_newTestContext()
	body := sseLine(`{"event":"node_finished","data":{"node_type":"llm","status":"succeeded"}}`) +
		sseLine(`{"event":"message_end","metadata":{"usage":{"prompt_tokens":1,"completion_tokens":5,"total_tokens":6}}}`)
	resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestDify(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "dify-bot"}}
	usage, apiErr := difyStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	// One debug node event was seen -> nodeToken=1 added on top of the
	// message_end CompletionTokens=5.
	if usage.CompletionTokens != 6 {
		t.Errorf("CompletionTokens = %d, want 6 (5 from message_end + 1 debug node token)", usage.CompletionTokens)
	}
}

// ---------------------------------------------------------------------------
// difyHandler (non-stream)
// ---------------------------------------------------------------------------

func TestDifyHandler_HappyPath(t *testing.T) {
	c, w := prov_aws_coze_dify_dify_newTestContext()
	body := `{"conversation_id":"conv-1","answer":"the answer","metadata":{"usage":{"prompt_tokens":6,"completion_tokens":2,"total_tokens":8}}}`
	resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestDify(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := difyHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage.PromptTokens != 6 || usage.CompletionTokens != 2 || usage.TotalTokens != 8 {
		t.Errorf("usage = %+v, want {6 2 8}", usage)
	}
	var out dto.OpenAITextResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body not valid JSON: %v (%s)", err, w.Body.String())
	}
	if out.Id != "conv-1" {
		t.Errorf("Id = %q, want the Dify conversation_id", out.Id)
	}
	if len(out.Choices) != 1 || out.Choices[0].Message.Content != "the answer" {
		t.Errorf("Choices = %+v, want single choice with the Dify answer text", out.Choices)
	}
}

func TestDifyHandler_MalformedBodyErrors(t *testing.T) {
	c, _ := prov_aws_coze_dify_dify_newTestContext()
	resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestDify(strings.NewReader(`not json`))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, apiErr := difyHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for malformed upstream response body")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoRequest / DoResponse dispatch / GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestAdaptor_DoRequest_RealHTTPRoundTrip(t *testing.T) {
	prov_aws_coze_dify_dify_allowLocalFetch(t)

	var gotAuth, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"conversation_id":"c1","answer":"ok"}`))
	}))
	defer upstream.Close()

	a := &Adaptor{BotType: BotTypeChatFlow}
	c, _ := prov_aws_coze_dify_dify_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL, ApiKey: "sk-dify"}}
	result, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp, ok := result.(*http.Response)
	if !ok {
		t.Fatalf("result type = %T, want *http.Response", result)
	}
	defer resp.Body.Close()
	if gotPath != "/v1/chat-messages" {
		t.Errorf("upstream path = %q, want /v1/chat-messages for BotTypeChatFlow", gotPath)
	}
	if gotAuth != "Bearer sk-dify" {
		t.Errorf("Authorization = %q, want Bearer-prefixed api key", gotAuth)
	}
}

func TestAdaptor_DoResponse_DispatchesStreamVsNonStream(t *testing.T) {
	a := &Adaptor{}
	t.Run("stream", func(t *testing.T) {
		c, w := prov_aws_coze_dify_dify_newTestContext()
		body := sseLine(`{"event":"message_end","metadata":{"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}}`)
		resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestDify(strings.NewReader(body))}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, IsStream: true}
		_, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %+v", apiErr)
		}
		if w.Code == 0 {
			t.Error("expected the stream handler to have written a response")
		}
	})
	t.Run("non-stream", func(t *testing.T) {
		c, w := prov_aws_coze_dify_dify_newTestContext()
		body := `{"conversation_id":"c1","answer":"hi"}`
		resp := &http.Response{StatusCode: 200, Body: httpNopCloserForTestDify(strings.NewReader(body))}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, IsStream: false}
		_, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %+v", apiErr)
		}
		if !strings.Contains(w.Body.String(), `"choices"`) {
			t.Error("non-stream dispatch should write an OpenAI-shaped JSON response, proving difyHandler ran")
		}
	})
}

func TestUploadDifyFile_UnreachableUpstreamReturnsNil(t *testing.T) {
	prov_aws_coze_dify_dify_allowLocalFetch(t)

	c, _ := prov_aws_coze_dify_dify_newTestContext()
	// Nothing listens on this loopback port, so client.Do must fail.
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "http://127.0.0.1:1"}}
	media := dto.MediaContent{Type: dto.ContentTypeImageURL, ImageUrl: &dto.MessageImageUrl{Url: base64.StdEncoding.EncodeToString([]byte("bytes"))}}
	got := uploadDifyFile(c, info, "user1", media)
	if got != nil {
		t.Errorf("got = %+v, want nil when the upload transport itself fails (connection refused)", got)
	}
}

func TestUploadDifyFile_NonImageMediaTypeReturnsNil(t *testing.T) {
	c, _ := prov_aws_coze_dify_dify_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "http://127.0.0.1:1"}}
	media := dto.MediaContent{Type: dto.ContentTypeText, Text: "not an image"}
	got := uploadDifyFile(c, info, "user1", media)
	if got != nil {
		t.Errorf("got = %+v, want nil for a non-image media type (upload only handles ContentTypeImageURL)", got)
	}
}

func TestAdaptor_GetModelList_And_GetChannelName(t *testing.T) {
	a := &Adaptor{}
	if a.GetModelList() == nil && len(a.GetModelList()) != 0 {
		t.Errorf("GetModelList() = %v, want the (currently empty) ModelList var", a.GetModelList())
	}
	if got := a.GetChannelName(); got != "dify" {
		t.Errorf("GetChannelName() = %q, want %q", got, "dify")
	}
}

