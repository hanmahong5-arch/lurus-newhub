package gemini

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	pkgconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/model_setting"

	"github.com/gin-gonic/gin"
)

// newTestContext builds a gin context with a real (non-nil) Request, which
// several downstream helpers (context.Get/Set, request-scoped caching) need
// in order not to panic.
func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, w
}

// withGeminiVisionMax temporarily overrides the vision image-count limit for
// the duration of a test and restores it afterward. The zero value of the
// package-level var is 0 (not -1), which would make every test with >=1
// image fail the "too many images" gate unless explicitly set.
func withGeminiVisionMax(t *testing.T, n int) {
	t.Helper()
	prev := pkgconstant.GeminiVisionMaxImageNum
	pkgconstant.GeminiVisionMaxImageNum = n
	t.Cleanup(func() { pkgconstant.GeminiVisionMaxImageNum = prev })
}

// ---------------------------------------------------------------------------
// CovertOpenAI2Gemini: role handling, system instructions
// ---------------------------------------------------------------------------

func TestCovertOpenAI2Gemini_SystemAndRoles(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	req := dto.GeneralOpenAIRequest{
		Model: "gemini-2.0-flash",
		Messages: []dto.Message{
			{Role: "system", Content: "You are helpful."},
			{Role: "developer", Content: "Be terse."},
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
		},
	}

	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// system + developer messages are joined with "\n" into SystemInstructions
	if got.SystemInstructions == nil {
		t.Fatal("SystemInstructions is nil, want joined system/developer text")
	}
	wantSys := "You are helpful.\nBe terse."
	if len(got.SystemInstructions.Parts) != 1 || got.SystemInstructions.Parts[0].Text != wantSys {
		t.Errorf("SystemInstructions.Parts = %+v, want single part with text %q", got.SystemInstructions.Parts, wantSys)
	}

	// system/developer messages must NOT appear in Contents
	if len(got.Contents) != 2 {
		t.Fatalf("len(Contents) = %d, want 2 (user + assistant only)", len(got.Contents))
	}
	if got.Contents[0].Role != "user" {
		t.Errorf("Contents[0].Role = %q, want %q", got.Contents[0].Role, "user")
	}
	// "assistant" role must be rewritten to "model" -- Gemini has no assistant role
	if got.Contents[1].Role != "model" {
		t.Errorf("Contents[1].Role = %q, want %q (assistant must map to model)", got.Contents[1].Role, "model")
	}
	if got.Contents[1].Parts[0].Text != "Hi there" {
		t.Errorf("Contents[1].Parts[0].Text = %q, want %q", got.Contents[1].Parts[0].Text, "Hi there")
	}
}

func TestCovertOpenAI2Gemini_NoSystemMessage_NilInstructions(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SystemInstructions != nil {
		t.Errorf("SystemInstructions = %+v, want nil when no system messages present", got.SystemInstructions)
	}
}

func TestCovertOpenAI2Gemini_EmptyTextPartDropped(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: ""}},
	}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// an empty text part is skipped, so the message produces zero parts and is
	// therefore dropped from Contents entirely (see: len(content.Parts) > 0 gate)
	if len(got.Contents) != 0 {
		t.Errorf("len(Contents) = %d, want 0 for message with only empty text", len(got.Contents))
	}
}

func TestCovertOpenAI2Gemini_EmptyMessages(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	got, err := CovertOpenAI2Gemini(c, dto.GeneralOpenAIRequest{}, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Contents) != 0 {
		t.Errorf("len(Contents) = %d, want 0 for empty request", len(got.Contents))
	}
}

// ---------------------------------------------------------------------------
// CovertOpenAI2Gemini: tool/function role (function response) handling
// ---------------------------------------------------------------------------

func TestCovertOpenAI2Gemini_ToolResponse_AppendsToExistingUserTurn(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	name := "get_weather"
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{Role: "user", Content: "what's the weather"},
			{Role: "tool", Content: `{"temp":72}`, Name: &name, ToolCallId: "call_1"},
		},
	}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// tool response is appended into the last content turn since it's role "user"
	if len(got.Contents) != 1 {
		t.Fatalf("len(Contents) = %d, want 1 (tool response folded into user turn)", len(got.Contents))
	}
	parts := got.Contents[0].Parts
	if len(parts) != 2 {
		t.Fatalf("len(Parts) = %d, want 2 (text + function response)", len(parts))
	}
	fr := parts[1].FunctionResponse
	if fr == nil {
		t.Fatal("FunctionResponse is nil")
	}
	if fr.Name != name {
		t.Errorf("FunctionResponse.Name = %q, want %q", fr.Name, name)
	}
	if fr.Response["temp"] != float64(72) {
		t.Errorf("FunctionResponse.Response[temp] = %v, want 72", fr.Response["temp"])
	}
}

func TestCovertOpenAI2Gemini_ToolResponse_AfterModelTurn_CreatesNewUserTurn(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{Role: "assistant", Content: "calling tool"},
			{Role: "function", Content: `{"result":"ok"}`, ToolCallId: "call_2"},
		},
	}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Contents) != 2 {
		t.Fatalf("len(Contents) = %d, want 2 (model turn + synthesized user turn for function response)", len(got.Contents))
	}
	if got.Contents[1].Role != "user" {
		t.Errorf("Contents[1].Role = %q, want %q", got.Contents[1].Role, "user")
	}
	if len(got.Contents[1].Parts) != 1 || got.Contents[1].Parts[0].FunctionResponse == nil {
		t.Fatalf("Contents[1].Parts = %+v, want single function-response part", got.Contents[1].Parts)
	}
}

func TestCovertOpenAI2Gemini_ToolResponse_NameFromCallIdMap(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	toolCalls := []dto.ToolCallRequest{
		{ID: "call_9", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: "{}"}},
	}
	assistantMsg := dto.Message{Role: "assistant"}
	assistantMsg.SetToolCalls(toolCalls)

	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			assistantMsg,
			// no Name set on the tool message -- must be resolved via tool_call_ids map
			{Role: "tool", Content: "42", ToolCallId: "call_9"},
		},
	}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// last content is "model" (assistant with tool call, no text parts -> dropped?), so
	// a new user turn is created. Locate the function response part.
	var fr *dto.GeminiFunctionResponse
	for _, content := range got.Contents {
		for _, part := range content.Parts {
			if part.FunctionResponse != nil {
				fr = part.FunctionResponse
			}
		}
	}
	if fr == nil {
		t.Fatal("no FunctionResponse part found")
	}
	if fr.Name != "lookup" {
		t.Errorf("FunctionResponse.Name = %q, want %q (resolved via tool_call_ids)", fr.Name, "lookup")
	}
}

func TestCovertOpenAI2Gemini_ToolResponse_ContentVariants(t *testing.T) {
	withGeminiVisionMax(t, 16)
	tests := []struct {
		name    string
		content string
		check   func(t *testing.T, resp map[string]interface{})
	}{
		{
			name:    "json object",
			content: `{"a":1,"b":"two"}`,
			check: func(t *testing.T, resp map[string]interface{}) {
				if resp["a"] != float64(1) || resp["b"] != "two" {
					t.Errorf("resp = %+v, want a=1 b=two", resp)
				}
			},
		},
		{
			name:    "json array wrapped in result",
			content: `[1,2,3]`,
			check: func(t *testing.T, resp map[string]interface{}) {
				arr, ok := resp["result"].([]interface{})
				if !ok || len(arr) != 3 {
					t.Errorf("resp[result] = %+v, want array of len 3", resp["result"])
				}
			},
		},
		{
			name:    "plain text wrapped in content",
			content: `not json at all`,
			check: func(t *testing.T, resp map[string]interface{}) {
				if resp["content"] != "not json at all" {
					t.Errorf("resp[content] = %v, want raw text", resp["content"])
				}
			},
		},
		{
			name:    "empty string wrapped in content",
			content: "",
			check: func(t *testing.T, resp map[string]interface{}) {
				if resp["content"] != "" {
					t.Errorf("resp[content] = %v, want empty string", resp["content"])
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestContext()
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
			req := dto.GeneralOpenAIRequest{
				Messages: []dto.Message{
					{Role: "user", Content: "context"},
					{Role: "tool", Content: tt.content, ToolCallId: "x"},
				},
			}
			got, err := CovertOpenAI2Gemini(c, req, info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var fr *dto.GeminiFunctionResponse
			for _, content := range got.Contents {
				for _, part := range content.Parts {
					if part.FunctionResponse != nil {
						fr = part.FunctionResponse
					}
				}
			}
			if fr == nil {
				t.Fatal("no FunctionResponse found")
			}
			tt.check(t, fr.Response)
		})
	}
}

// ---------------------------------------------------------------------------
// CovertOpenAI2Gemini: function/tool calls (assistant -> model tool_calls)
// ---------------------------------------------------------------------------

func TestCovertOpenAI2Gemini_ToolCalls_ValidArguments(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	toolCalls := []dto.ToolCallRequest{
		{ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "get_time", Arguments: `{"tz":"UTC"}`}},
	}
	msg := dto.Message{Role: "assistant"}
	msg.SetToolCalls(toolCalls)

	req := dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Contents) != 1 {
		t.Fatalf("len(Contents) = %d, want 1", len(got.Contents))
	}
	parts := got.Contents[0].Parts
	if len(parts) != 1 || parts[0].FunctionCall == nil {
		t.Fatalf("Parts = %+v, want single FunctionCall part", parts)
	}
	if parts[0].FunctionCall.FunctionName != "get_time" {
		t.Errorf("FunctionName = %q, want %q", parts[0].FunctionCall.FunctionName, "get_time")
	}
	if parts[0].FunctionCall.Arguments.(map[string]interface{})["tz"] != "UTC" {
		t.Errorf("Arguments = %+v, want tz=UTC", parts[0].FunctionCall.Arguments)
	}
}

func TestCovertOpenAI2Gemini_ToolCalls_InvalidArgumentsJSON_ReturnsError(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	toolCalls := []dto.ToolCallRequest{
		{ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "broken", Arguments: `{not-json`}},
	}
	msg := dto.Message{Role: "assistant"}
	msg.SetToolCalls(toolCalls)

	req := dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}}
	_, err := CovertOpenAI2Gemini(c, req, info)
	if err == nil {
		t.Fatal("expected error for malformed function-call arguments JSON")
	}
	if !strings.Contains(err.Error(), "invalid arguments") {
		t.Errorf("error = %q, want contains 'invalid arguments'", err.Error())
	}
}

func TestCovertOpenAI2Gemini_ToolCalls_EmptyArguments(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	toolCalls := []dto.ToolCallRequest{
		{ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "noargs", Arguments: ""}},
	}
	msg := dto.Message{Role: "assistant"}
	msg.SetToolCalls(toolCalls)
	req := dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fc := got.Contents[0].Parts[0].FunctionCall
	if fc == nil || fc.FunctionName != "noargs" {
		t.Fatalf("FunctionCall = %+v, want name noargs", fc)
	}
	if len(fc.Arguments.(map[string]interface{})) != 0 {
		t.Errorf("Arguments = %+v, want empty map", fc.Arguments)
	}
}

// thoughtSignature is only attached when the channel is Gemini/VertexAI AND
// FunctionCallThoughtSignatureEnabled is true (true by default).
func TestCovertOpenAI2Gemini_ToolCalls_ThoughtSignatureAttachedOnGeminiChannel(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: pkgconstant.ChannelTypeGemini},
	}
	toolCalls := []dto.ToolCallRequest{
		{ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "f", Arguments: `{"x":1}`}},
	}
	msg := dto.Message{Role: "assistant"}
	msg.SetToolCalls(toolCalls)
	req := dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sig := got.Contents[0].Parts[0].ThoughtSignature
	if len(sig) == 0 {
		t.Error("ThoughtSignature not attached on Gemini channel with a real function call")
	}
}

func TestCovertOpenAI2Gemini_ToolCalls_ThoughtSignatureNotAttachedOnOtherChannel(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 1 /* not gemini/vertex */},
	}
	toolCalls := []dto.ToolCallRequest{
		{ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "f", Arguments: `{"x":1}`}},
	}
	msg := dto.Message{Role: "assistant"}
	msg.SetToolCalls(toolCalls)
	req := dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sig := got.Contents[0].Parts[0].ThoughtSignature
	if len(sig) != 0 {
		t.Errorf("ThoughtSignature = %q, want empty on non-Gemini channel", string(sig))
	}
}

// ---------------------------------------------------------------------------
// CovertOpenAI2Gemini: markdown-embedded images inside text parts
// ---------------------------------------------------------------------------

// NOTE: the split of "before ![pic](...) after" into text/image/trailing-text
// is covered by TestCovertOpenAI2Gemini_MarkdownImage_KeepsTrailingText in
// fix_markdown_image_tail_test.go — the trailing text used to be dropped by the
// scanning loop, and that file also covers the early-break and no-regression
// variants of the same loop.

// The markdown-image branch's base64 decode error is only reachable when the
// data URL has NO ";" between the mime type and the comma (see
// app.DecodeBase64FileData: a ";base64," data URL is passed through as a raw
// substring split with no base64 validation at all -- decode validation only
// happens via the image-header-sniffing fallback path).
func TestCovertOpenAI2Gemini_MarkdownImage_MalformedBase64_Errors(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	text := "look ![pic](data:image/png,not-valid-base64!!!) end"
	req := dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: text}}}
	_, err := CovertOpenAI2Gemini(c, req, info)
	if err == nil {
		t.Fatal("expected decode error for malformed markdown image base64")
	}
	if !strings.Contains(err.Error(), "decode markdown base64 image data failed") {
		t.Errorf("error = %q, want decode error", err.Error())
	}
}

func TestCovertOpenAI2Gemini_MarkdownImage_TooMany_Errors(t *testing.T) {
	withGeminiVisionMax(t, 1)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	imgB64 := base64.StdEncoding.EncodeToString([]byte("x"))
	imgTag := "![p](data:image/png;base64," + imgB64 + ")"
	text := imgTag + imgTag // two images, limit is 1
	req := dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: text}}}
	_, err := CovertOpenAI2Gemini(c, req, info)
	if err == nil {
		t.Fatal("expected 'too many images' error")
	}
	if !strings.Contains(err.Error(), "too many images") {
		t.Errorf("error = %q, want too-many-images error", err.Error())
	}
}

func TestCovertOpenAI2Gemini_NoMarkdownImage_PlainTextUnchanged(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "plain text, no markdown ![] here"}},
	}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Contents[0].Parts) != 1 || got.Contents[0].Parts[0].Text != "plain text, no markdown ![] here" {
		t.Errorf("Parts = %+v, want single unchanged text part", got.Contents[0].Parts)
	}
}

// ---------------------------------------------------------------------------
// CovertOpenAI2Gemini: image_url content parts (http URL vs base64)
// ---------------------------------------------------------------------------

func TestCovertOpenAI2Gemini_ImageURL_Base64Direct(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	imgB64 := base64.StdEncoding.EncodeToString([]byte("fake-jpeg-bytes"))
	msg := dto.Message{Role: "user"}
	msg.SetMediaContent([]dto.MediaContent{
		{Type: dto.ContentTypeImageURL, ImageUrl: &dto.MessageImageUrl{Url: "data:image/jpeg;base64," + imgB64}},
	})
	req := dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	part := got.Contents[0].Parts[0]
	if part.InlineData == nil || part.InlineData.MimeType != "image/jpeg" || part.InlineData.Data != imgB64 {
		t.Errorf("InlineData = %+v, want mimeType image/jpeg data %q", part.InlineData, imgB64)
	}
}

// SSRF protection is enabled by default and rejects loopback URLs, so a
// remote (http-prefixed) image URL must surface as a wrapped error rather
// than silently succeeding or panicking. This exercises the real
// GetFileBase64FromUrl error path without any live network access.
func TestCovertOpenAI2Gemini_ImageURL_RemoteFetchRejectedBySSRFGuard(t *testing.T) {
	withGeminiVisionMax(t, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("fake"))
	}))
	defer srv.Close()

	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	msg := dto.Message{Role: "user"}
	msg.SetMediaContent([]dto.MediaContent{
		{Type: dto.ContentTypeImageURL, ImageUrl: &dto.MessageImageUrl{Url: srv.URL + "/img.png"}},
	})
	req := dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}}
	_, err := CovertOpenAI2Gemini(c, req, info)
	if err == nil {
		t.Fatal("expected error: loopback URLs must be rejected by SSRF protection")
	}
	if !strings.Contains(err.Error(), "get file base64 from url") {
		t.Errorf("error = %q, want wrapped 'get file base64 from url' error", err.Error())
	}
}

func TestCovertOpenAI2Gemini_ImageURL_MalformedBase64_Errors(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	msg := dto.Message{Role: "user"}
	// No "data:...;base64," prefix at all (and no comma), so DecodeBase64FileData
	// falls through to the image-header-sniffing path, which actually validates
	// the base64 payload and rejects invalid characters like "!".
	msg.SetMediaContent([]dto.MediaContent{
		{Type: dto.ContentTypeImageURL, ImageUrl: &dto.MessageImageUrl{Url: "not-valid-base64!!!"}},
	})
	req := dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}}
	_, err := CovertOpenAI2Gemini(c, req, info)
	if err == nil {
		t.Fatal("expected decode error")
	}
	if !strings.Contains(err.Error(), "decode base64 image data failed") {
		t.Errorf("error = %q, want decode error", err.Error())
	}
}

// ---------------------------------------------------------------------------
// CovertOpenAI2Gemini: file and audio content parts
// ---------------------------------------------------------------------------

func TestCovertOpenAI2Gemini_FilePart_FileIdUnsupported(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	msg := dto.Message{Role: "user"}
	msg.SetMediaContent([]dto.MediaContent{
		{Type: dto.ContentTypeFile, File: &dto.MessageFile{FileId: "file-abc"}},
	})
	req := dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}}
	_, err := CovertOpenAI2Gemini(c, req, info)
	if err == nil {
		t.Fatal("expected error: FileId (remote-reference file) is not supported by Gemini")
	}
	if !strings.Contains(err.Error(), "only base64 file is supported") {
		t.Errorf("error = %q, want 'only base64 file is supported'", err.Error())
	}
}

func TestCovertOpenAI2Gemini_FilePart_Base64Decoded(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	pdfB64 := base64.StdEncoding.EncodeToString([]byte("%PDF-fake"))
	msg := dto.Message{Role: "user"}
	msg.SetMediaContent([]dto.MediaContent{
		{Type: dto.ContentTypeFile, File: &dto.MessageFile{FileData: "data:application/pdf;base64," + pdfB64}},
	})
	req := dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	part := got.Contents[0].Parts[0]
	if part.InlineData == nil || part.InlineData.MimeType != "application/pdf" || part.InlineData.Data != pdfB64 {
		t.Errorf("InlineData = %+v, want application/pdf %q", part.InlineData, pdfB64)
	}
}

func TestCovertOpenAI2Gemini_AudioPart_EmptyData_Errors(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	msg := dto.Message{Role: "user"}
	msg.SetMediaContent([]dto.MediaContent{
		{Type: dto.ContentTypeInputAudio, InputAudio: &dto.MessageInputAudio{Data: "", Format: "mp3"}},
	})
	req := dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}}
	_, err := CovertOpenAI2Gemini(c, req, info)
	if err == nil {
		t.Fatal("expected error for empty audio data")
	}
	if !strings.Contains(err.Error(), "only base64 audio is supported") {
		t.Errorf("error = %q, want audio-required error", err.Error())
	}
}

func TestCovertOpenAI2Gemini_AudioPart_Decoded(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	audioB64 := base64.StdEncoding.EncodeToString([]byte("raw-audio"))
	msg := dto.Message{Role: "user"}
	msg.SetMediaContent([]dto.MediaContent{
		{Type: dto.ContentTypeInputAudio, InputAudio: &dto.MessageInputAudio{Data: audioB64, Format: "mp3"}},
	})
	req := dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	part := got.Contents[0].Parts[0]
	if part.InlineData == nil || part.InlineData.MimeType != "audio/mp3" || part.InlineData.Data != audioB64 {
		t.Errorf("InlineData = %+v, want audio/mp3 %q", part.InlineData, audioB64)
	}
}

// ---------------------------------------------------------------------------
// CovertOpenAI2Gemini: tools (googleSearch/codeExecution/urlContext/functions)
// ---------------------------------------------------------------------------

func TestCovertOpenAI2Gemini_BuiltinTools(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
		Tools: []dto.ToolCallRequest{
			{Type: "function", Function: dto.FunctionRequest{Name: "googleSearch"}},
			{Type: "function", Function: dto.FunctionRequest{Name: "codeExecution"}},
			{Type: "function", Function: dto.FunctionRequest{Name: "urlContext"}},
		},
	}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools := got.GetTools()
	if len(tools) != 3 {
		t.Fatalf("len(tools) = %d, want 3 (googleSearch, codeExecution, urlContext); got %+v", len(tools), tools)
	}
	var hasSearch, hasExec, hasURL bool
	for _, tool := range tools {
		if tool.GoogleSearch != nil {
			hasSearch = true
		}
		if tool.CodeExecution != nil {
			hasExec = true
		}
		if tool.URLContext != nil {
			hasURL = true
		}
	}
	if !hasSearch || !hasExec || !hasURL {
		t.Errorf("tools = %+v, want googleSearch+codeExecution+urlContext all present", tools)
	}
}

func TestCovertOpenAI2Gemini_FunctionTool_EmptyPropertiesStripped(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
		Tools: []dto.ToolCallRequest{
			{Type: "function", Function: dto.FunctionRequest{
				Name: "no_args_fn",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			}},
		},
	}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools := got.GetTools()
	if len(tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(tools))
	}
	funcs, ok := tools[0].FunctionDeclarations.([]interface{})
	if !ok {
		t.Fatalf("FunctionDeclarations type = %T, want []interface{} after JSON round-trip", tools[0].FunctionDeclarations)
	}
	fn := funcs[0].(map[string]interface{})
	if fn["parameters"] != nil {
		t.Errorf("parameters = %+v, want nil after stripping empty properties object", fn["parameters"])
	}
}

func TestCovertOpenAI2Gemini_ResponseFormat_JSONSchema(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
		ResponseFormat: &dto.ResponseFormat{
			Type: "json_schema",
			JsonSchema: []byte(`{"name":"result","schema":{"type":"object","additionalProperties":false,"title":"drop-me","properties":{"a":{"type":"string"}}}}`),
		},
	}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.GenerationConfig.ResponseMimeType != "application/json" {
		t.Errorf("ResponseMimeType = %q, want application/json", got.GenerationConfig.ResponseMimeType)
	}
	schema, ok := got.GenerationConfig.ResponseSchema.(map[string]interface{})
	if !ok {
		t.Fatalf("ResponseSchema type = %T, want map[string]interface{}", got.GenerationConfig.ResponseSchema)
	}
	if _, has := schema["additionalProperties"]; has {
		t.Errorf("schema = %+v, want additionalProperties stripped", schema)
	}
	if _, has := schema["title"]; has {
		t.Errorf("schema = %+v, want title stripped", schema)
	}
}

func TestCovertOpenAI2Gemini_ResponseFormat_JSONObject_NoSchema(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{
		Messages:       []dto.Message{{Role: "user", Content: "hi"}},
		ResponseFormat: &dto.ResponseFormat{Type: "json_object"},
	}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.GenerationConfig.ResponseMimeType != "application/json" {
		t.Errorf("ResponseMimeType = %q, want application/json", got.GenerationConfig.ResponseMimeType)
	}
	if got.GenerationConfig.ResponseSchema != nil {
		t.Errorf("ResponseSchema = %+v, want nil (json_object has no schema)", got.GenerationConfig.ResponseSchema)
	}
}

func TestCovertOpenAI2Gemini_ResponseFormat_Text_Ignored(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{
		Messages:       []dto.Message{{Role: "user", Content: "hi"}},
		ResponseFormat: &dto.ResponseFormat{Type: "text"},
	}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.GenerationConfig.ResponseMimeType != "" {
		t.Errorf("ResponseMimeType = %q, want empty for type=text", got.GenerationConfig.ResponseMimeType)
	}
}

// ---------------------------------------------------------------------------
// CovertOpenAI2Gemini: extra_body (Gemini-specific thinking/image config)
// ---------------------------------------------------------------------------

func TestCovertOpenAI2Gemini_ExtraBody_ThinkingBudget(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{
		Model:     "gemini-2.5-pro",
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
		ExtraBody: []byte(`{"google":{"thinking_config":{"thinking_budget":1234,"include_thoughts":true}}}`),
	}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := got.GenerationConfig.ThinkingConfig
	if tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != 1234 {
		t.Fatalf("ThinkingConfig = %+v, want ThinkingBudget=1234", tc)
	}
	if !tc.IncludeThoughts {
		t.Error("IncludeThoughts = false, want true")
	}
}

func TestCovertOpenAI2Gemini_ExtraBody_ThinkingConfig_NoBudget(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
		ExtraBody: []byte(`{"google":{"thinking_config":{"include_thoughts":true}}}`),
	}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := got.GenerationConfig.ThinkingConfig
	if tc == nil || tc.ThinkingBudget != nil {
		t.Fatalf("ThinkingConfig = %+v, want IncludeThoughts-only (nil budget)", tc)
	}
}

func TestCovertOpenAI2Gemini_ExtraBody_WrongCamelCaseThinkingConfig_Errors(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
		ExtraBody: []byte(`{"google":{"thinkingConfig":{"thinking_budget":1}}}`),
	}
	_, err := CovertOpenAI2Gemini(c, req, info)
	if err == nil {
		t.Fatal("expected error for camelCase 'thinkingConfig' key")
	}
	if !strings.Contains(err.Error(), "thinking_config") {
		t.Errorf("error = %q, want hint about thinking_config", err.Error())
	}
}

func TestCovertOpenAI2Gemini_ExtraBody_WrongCamelCaseThinkingBudget_Errors(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
		ExtraBody: []byte(`{"google":{"thinking_config":{"thinkingBudget":1}}}`),
	}
	_, err := CovertOpenAI2Gemini(c, req, info)
	if err == nil {
		t.Fatal("expected error for camelCase 'thinkingBudget' key")
	}
	if !strings.Contains(err.Error(), "thinking_budget") {
		t.Errorf("error = %q, want hint about thinking_budget", err.Error())
	}
}

func TestCovertOpenAI2Gemini_ExtraBody_ImageConfig(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
		ExtraBody: []byte(`{"google":{"image_config":{"aspect_ratio":"16:9","image_size":"2K"}}}`),
	}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.GenerationConfig.ImageConfig) == 0 {
		t.Fatal("ImageConfig is empty, want marshaled camelCase config")
	}
	body := string(got.GenerationConfig.ImageConfig)
	if !strings.Contains(body, `"aspectRatio":"16:9"`) {
		t.Errorf("ImageConfig = %s, want aspectRatio converted to camelCase", body)
	}
	if !strings.Contains(body, `"imageSize":"2K"`) {
		t.Errorf("ImageConfig = %s, want imageSize converted to camelCase", body)
	}
}

func TestCovertOpenAI2Gemini_ExtraBody_WrongCamelCaseImageConfig_Errors(t *testing.T) {
	withGeminiVisionMax(t, 16)
	tests := []struct {
		name string
		body string
		want string
	}{
		{"imageConfig", `{"google":{"imageConfig":{}}}`, "image_config"},
		{"aspectRatio", `{"google":{"image_config":{"aspectRatio":"1:1"}}}`, "aspect_ratio"},
		{"imageSize", `{"google":{"image_config":{"imageSize":"1K"}}}`, "image_size"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestContext()
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
			req := dto.GeneralOpenAIRequest{
				Messages:  []dto.Message{{Role: "user", Content: "hi"}},
				ExtraBody: []byte(tt.body),
			}
			_, err := CovertOpenAI2Gemini(c, req, info)
			if err == nil {
				t.Fatalf("expected error for camelCase %q", tt.name)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want contains %q", err.Error(), tt.want)
			}
		})
	}
}

func TestCovertOpenAI2Gemini_ExtraBody_MalformedJSON_Errors(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
		ExtraBody: []byte(`{not valid json`),
	}
	_, err := CovertOpenAI2Gemini(c, req, info)
	if err == nil {
		t.Fatal("expected error for malformed extra_body JSON")
	}
	if !strings.Contains(err.Error(), "invalid extra body") {
		t.Errorf("error = %q, want 'invalid extra body'", err.Error())
	}
}

func TestCovertOpenAI2Gemini_ExtraBody_SkippedForNothinkingSuffix(t *testing.T) {
	withGeminiVisionMax(t, 16)
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{
		Model:    "gemini-2.5-flash-nothinking",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
		// even malformed JSON should be silently skipped for -nothinking models
		ExtraBody: []byte(`{not valid json`),
	}
	// info.UpstreamModelName is what gates the "-nothinking" check, not textRequest.Model
	info.ChannelMeta = &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-flash-nothinking"}
	got, err := CovertOpenAI2Gemini(c, req, info)
	if err != nil {
		t.Fatalf("unexpected error: extra_body should be skipped entirely for -nothinking models: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil result")
	}
}

// ---------------------------------------------------------------------------
// ThinkingAdaptor
// ---------------------------------------------------------------------------

func TestThinkingAdaptor_DisabledBySettingIsNoOp(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	prevEnabled := settings.ThinkingAdapterEnabled
	settings.ThinkingAdapterEnabled = false
	t.Cleanup(func() { settings.ThinkingAdapterEnabled = prevEnabled })

	req := &dto.GeminiChatRequest{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-pro-thinking-2000"}}
	ThinkingAdaptor(req, info)
	if req.GenerationConfig.ThinkingConfig != nil {
		t.Errorf("ThinkingConfig = %+v, want nil when adapter disabled", req.GenerationConfig.ThinkingConfig)
	}
}

func TestThinkingAdaptor_ExplicitBudgetSuffix(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	prevEnabled := settings.ThinkingAdapterEnabled
	settings.ThinkingAdapterEnabled = true
	t.Cleanup(func() { settings.ThinkingAdapterEnabled = prevEnabled })

	req := &dto.GeminiChatRequest{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash-thinking-500"}}
	ThinkingAdaptor(req, info)
	tc := req.GenerationConfig.ThinkingConfig
	if tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != 500 {
		t.Fatalf("ThinkingConfig = %+v, want budget 500 (within [0,24576] range for non-pro/lite model)", tc)
	}
	if !tc.IncludeThoughts {
		t.Error("IncludeThoughts = false, want true")
	}
}

func TestThinkingAdaptor_ExplicitBudgetSuffix_ClampedToModelRange(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	prevEnabled := settings.ThinkingAdapterEnabled
	settings.ThinkingAdapterEnabled = true
	t.Cleanup(func() { settings.ThinkingAdapterEnabled = prevEnabled })

	req := &dto.GeminiChatRequest{}
	// pro25 model with an out-of-range explicit budget must clamp to pro25MaxBudget
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-pro-latest-thinking-999999"}}
	ThinkingAdaptor(req, info)
	tc := req.GenerationConfig.ThinkingConfig
	if tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != pro25MaxBudget {
		t.Fatalf("ThinkingConfig = %+v, want clamped budget %d", tc, pro25MaxBudget)
	}
}

func TestThinkingAdaptor_ThinkingSuffix_UnsupportedModel_NoBudgetSet(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	prevEnabled := settings.ThinkingAdapterEnabled
	settings.ThinkingAdapterEnabled = true
	t.Cleanup(func() { settings.ThinkingAdapterEnabled = prevEnabled })

	req := &dto.GeminiChatRequest{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-pro-preview-05-06-thinking"}}
	ThinkingAdaptor(req, info)
	tc := req.GenerationConfig.ThinkingConfig
	if tc == nil {
		t.Fatal("ThinkingConfig is nil, want IncludeThoughts-only config for unsupported model")
	}
	if tc.ThinkingBudget != nil {
		t.Errorf("ThinkingBudget = %v, want nil for unsupported preview model", *tc.ThinkingBudget)
	}
	if !tc.IncludeThoughts {
		t.Error("IncludeThoughts = false, want true")
	}
}

func TestThinkingAdaptor_ThinkingSuffix_MaxOutputTokensPercentage(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	prevEnabled := settings.ThinkingAdapterEnabled
	prevPct := settings.ThinkingAdapterBudgetTokensPercentage
	settings.ThinkingAdapterEnabled = true
	settings.ThinkingAdapterBudgetTokensPercentage = 0.5
	t.Cleanup(func() {
		settings.ThinkingAdapterEnabled = prevEnabled
		settings.ThinkingAdapterBudgetTokensPercentage = prevPct
	})

	req := &dto.GeminiChatRequest{GenerationConfig: dto.GeminiChatGenerationConfig{MaxOutputTokens: 1000}}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash-thinking"}}
	ThinkingAdaptor(req, info)
	tc := req.GenerationConfig.ThinkingConfig
	if tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != 500 {
		t.Fatalf("ThinkingConfig = %+v, want budget = 50%% of 1000 = 500", tc)
	}
}

func TestThinkingAdaptor_ThinkingSuffix_ReasoningEffortEmpty_NoMaxTokens(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	prevEnabled := settings.ThinkingAdapterEnabled
	settings.ThinkingAdapterEnabled = true
	t.Cleanup(func() { settings.ThinkingAdapterEnabled = prevEnabled })

	req := &dto.GeminiChatRequest{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash-thinking"}}
	oai := dto.GeneralOpenAIRequest{ReasoningEffort: "high"}
	ThinkingAdaptor(req, info, oai)
	tc := req.GenerationConfig.ThinkingConfig
	// other-model maxBudget = flash25MaxBudget=24576, high=80% => 19660 (clamped within [0,24576])
	want := flash25MaxBudget * 80 / 100
	if tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != want {
		t.Fatalf("ThinkingConfig = %+v, want budget=%d (from reasoning effort 'high')", tc, want)
	}
}

func TestThinkingAdaptor_NothinkingSuffix_ZeroBudget(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	prevEnabled := settings.ThinkingAdapterEnabled
	settings.ThinkingAdapterEnabled = true
	t.Cleanup(func() { settings.ThinkingAdapterEnabled = prevEnabled })

	req := &dto.GeminiChatRequest{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash-nothinking"}}
	ThinkingAdaptor(req, info)
	tc := req.GenerationConfig.ThinkingConfig
	if tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != 0 {
		t.Fatalf("ThinkingConfig = %+v, want ThinkingBudget=0 for -nothinking suffix", tc)
	}
}

func TestThinkingAdaptor_NothinkingSuffix_New25ProSkipped(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	prevEnabled := settings.ThinkingAdapterEnabled
	settings.ThinkingAdapterEnabled = true
	t.Cleanup(func() { settings.ThinkingAdapterEnabled = prevEnabled })

	req := &dto.GeminiChatRequest{}
	// new-25-pro models don't support disabling thinking, so -nothinking must be a no-op
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-pro-latest-nothinking"}}
	ThinkingAdaptor(req, info)
	if req.GenerationConfig.ThinkingConfig != nil {
		t.Errorf("ThinkingConfig = %+v, want nil (new-25-pro cannot disable thinking)", req.GenerationConfig.ThinkingConfig)
	}
}

func TestThinkingAdaptor_EffortSuffix_SetsThinkingLevel(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	prevEnabled := settings.ThinkingAdapterEnabled
	settings.ThinkingAdapterEnabled = true
	t.Cleanup(func() { settings.ThinkingAdapterEnabled = prevEnabled })

	req := &dto.GeminiChatRequest{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-3-pro-preview-high"}}
	ThinkingAdaptor(req, info)
	tc := req.GenerationConfig.ThinkingConfig
	if tc == nil || tc.ThinkingLevel != "high" {
		t.Fatalf("ThinkingConfig = %+v, want ThinkingLevel=high", tc)
	}
	if info.ReasoningEffort != "high" {
		t.Errorf("info.ReasoningEffort = %q, want %q", info.ReasoningEffort, "high")
	}
}

func TestThinkingAdaptor_NoMatchingSuffix_NoOp(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	prevEnabled := settings.ThinkingAdapterEnabled
	settings.ThinkingAdapterEnabled = true
	t.Cleanup(func() { settings.ThinkingAdapterEnabled = prevEnabled })

	req := &dto.GeminiChatRequest{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"}}
	ThinkingAdaptor(req, info)
	if req.GenerationConfig.ThinkingConfig != nil {
		t.Errorf("ThinkingConfig = %+v, want nil for plain model name", req.GenerationConfig.ThinkingConfig)
	}
}
