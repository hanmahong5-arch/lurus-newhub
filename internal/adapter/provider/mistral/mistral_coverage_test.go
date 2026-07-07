package mistral

import (
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func TestAdaptor_GetRequestURL(t *testing.T) {
	a := &Adaptor{}
	cases := []struct {
		name     string
		info     *relaycommon.RelayInfo
		expected string
	}{
		{
			name: "plain base url",
			info: &relaycommon.RelayInfo{
				RequestURLPath: "/v1/chat/completions",
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl: "https://api.mistral.ai",
					ChannelType:    1,
				},
			},
			expected: "https://api.mistral.ai/v1/chat/completions",
		},
		{
			name: "cloudflare gateway openai channel trims /v1 prefix",
			info: &relaycommon.RelayInfo{
				RequestURLPath: "/v1/chat/completions",
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl: "https://gateway.ai.cloudflare.com/account/gw/openai",
					ChannelType:    1, // constant.ChannelTypeOpenAI
				},
			},
			expected: "https://gateway.ai.cloudflare.com/account/gw/openai/chat/completions",
		},
		{
			name: "empty base and path",
			info: &relaycommon.RelayInfo{
				RequestURLPath: "",
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelBaseUrl: "",
					ChannelType:    0,
				},
			},
			expected: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a.GetRequestURL(tc.info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestAdaptor_SetupRequestHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &Adaptor{}

	t.Run("sets authorization and content-type from request", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		c.Request = req

		header := http.Header{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-test-key"}}
		err := a.SetupRequestHeader(c, &header, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Authorization"); got != "Bearer sk-test-key" {
			t.Fatalf("expected Authorization header 'Bearer sk-test-key', got %q", got)
		}
		if got := header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("expected Content-Type application/json, got %q", got)
		}
		if got := header.Get("Accept"); got != "application/json" {
			t.Fatalf("expected Accept application/json, got %q", got)
		}
	})

	t.Run("stream without explicit accept defaults to text/event-stream", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		c.Request = req

		header := http.Header{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-stream"}, IsStream: true}
		err := a.SetupRequestHeader(c, &header, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Accept"); got != "text/event-stream" {
			t.Fatalf("expected Accept text/event-stream, got %q", got)
		}
		if got := header.Get("Authorization"); got != "Bearer sk-stream" {
			t.Fatalf("expected Authorization header 'Bearer sk-stream', got %q", got)
		}
	})
}

func TestAdaptor_ConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil request returns error", func(t *testing.T) {
		got, err := a.ConvertOpenAIRequest(nil, nil, nil)
		if got != nil {
			t.Fatalf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "request is nil" {
			t.Fatalf("expected 'request is nil' error, got %v", err)
		}
	})

	t.Run("valid request is converted via requestOpenAI2Mistral", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Model: "mistral-large-latest",
			Messages: []dto.Message{
				{Role: "user", Content: "hello"},
			},
		}
		got, err := a.ConvertOpenAIRequest(nil, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		converted, ok := got.(*dto.GeneralOpenAIRequest)
		if !ok {
			t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", got)
		}
		if converted.Model != "mistral-large-latest" {
			t.Fatalf("expected model preserved, got %q", converted.Model)
		}
		if len(converted.Messages) != 1 || converted.Messages[0].Role != "user" {
			t.Fatalf("expected 1 user message, got %+v", converted.Messages)
		}
	})
}

func TestAdaptor_ConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	if got != nil {
		t.Fatalf("expected nil result, got %v", got)
	}
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestAdaptor_GetModelList(t *testing.T) {
	a := &Adaptor{}
	got := a.GetModelList()
	expected := []string{
		"open-mistral-7b",
		"open-mixtral-8x7b",
		"mistral-small-latest",
		"mistral-medium-latest",
		"mistral-large-latest",
		"mistral-embed",
	}
	if len(got) != len(expected) {
		t.Fatalf("expected %d models, got %d: %v", len(expected), len(got), got)
	}
	for i, m := range expected {
		if got[i] != m {
			t.Fatalf("expected model[%d]=%q, got %q", i, m, got[i])
		}
	}
}

func TestAdaptor_GetChannelName(t *testing.T) {
	a := &Adaptor{}
	if got := a.GetChannelName(); got != "mistral" {
		t.Fatalf("expected channel name 'mistral', got %q", got)
	}
}

func TestAdaptor_DoRequest_URLBuildFailurePropagates(t *testing.T) {
	// Exercise the hermetically-reachable error path of DoRequest without any
	// network I/O: an unparsable request URL (control character in the path)
	// makes http.NewRequestWithContext fail inside provider.DoApiRequest
	// before any dial is attempted.
	gin.SetMode(gin.TestMode)
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request = req

	info := &relaycommon.RelayInfo{
		RequestURLPath: "/v1/chat\x7f/completions",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://api.mistral.ai",
			ApiKey:         "sk-test",
		},
	}
	_, err := a.DoRequest(c, info, nil)
	if err == nil {
		t.Fatalf("expected error building request with unparsable URL, got nil")
	}
}

func TestRequestOpenAI2Mistral(t *testing.T) {
	t.Run("valid tool call id is left unchanged", func(t *testing.T) {
		msg := dto.Message{Role: "assistant", Content: "ok"}
		msg.SetToolCalls([]dto.ToolCallRequest{{ID: "abcdefghi", Type: "function"}})
		req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}}

		got := requestOpenAI2Mistral(req)
		toolCalls := got.Messages[0].ParseToolCalls()
		if len(toolCalls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
		}
		if toolCalls[0].ID != "abcdefghi" {
			t.Fatalf("expected valid tool call id preserved, got %q", toolCalls[0].ID)
		}
	})

	t.Run("invalid tool call id is rewritten to a 9-char alnum id", func(t *testing.T) {
		msg := dto.Message{Role: "assistant", Content: "ok"}
		msg.SetToolCalls([]dto.ToolCallRequest{{ID: "not-valid-id!", Type: "function"}})
		req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}}

		got := requestOpenAI2Mistral(req)
		toolCalls := got.Messages[0].ParseToolCalls()
		if len(toolCalls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
		}
		if toolCalls[0].ID == "not-valid-id!" {
			t.Fatalf("expected tool call id to be rewritten")
		}
		if !mistralToolCallIdRegexp.MatchString(toolCalls[0].ID) {
			t.Fatalf("expected rewritten id to match %v, got %q", mistralToolCallIdRegexp, toolCalls[0].ID)
		}
	})

	t.Run("invalid tool_call_id reuses id already remapped for matching tool call", func(t *testing.T) {
		// First message: assistant emits a tool call with an invalid id.
		assistantMsg := dto.Message{Role: "assistant", Content: ""}
		assistantMsg.SetToolCalls([]dto.ToolCallRequest{{ID: "bad-id!!", Type: "function"}})
		// Second message: tool result referencing the same invalid id via ToolCallId.
		toolMsg := dto.Message{Role: "tool", Content: "result", ToolCallId: "bad-id!!"}

		req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{assistantMsg, toolMsg}}
		got := requestOpenAI2Mistral(req)

		assistantToolCalls := got.Messages[0].ParseToolCalls()
		if len(assistantToolCalls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(assistantToolCalls))
		}
		remappedID := assistantToolCalls[0].ID
		if !mistralToolCallIdRegexp.MatchString(remappedID) {
			t.Fatalf("expected remapped id to be valid, got %q", remappedID)
		}
		if got.Messages[1].ToolCallId != remappedID {
			t.Fatalf("expected tool_call_id to reuse remapped id %q, got %q", remappedID, got.Messages[1].ToolCallId)
		}
	})

	t.Run("valid tool_call_id left unchanged", func(t *testing.T) {
		toolMsg := dto.Message{Role: "tool", Content: "result", ToolCallId: "abcdefghi"}
		req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{toolMsg}}
		got := requestOpenAI2Mistral(req)
		if got.Messages[0].ToolCallId != "abcdefghi" {
			t.Fatalf("expected valid tool_call_id preserved, got %q", got.Messages[0].ToolCallId)
		}
	})

	t.Run("assistant message with tool calls and empty content clears media content", func(t *testing.T) {
		msg := dto.Message{Role: "assistant", Content: ""}
		msg.SetToolCalls([]dto.ToolCallRequest{{ID: "abcdefghi", Type: "function"}})
		req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}}

		got := requestOpenAI2Mistral(req)
		// The assistant+toolcalls+empty-content branch forces mediaMessages to
		// an empty (non-nil) []dto.MediaContent, which SetMediaContent then
		// writes back onto Content.
		mediaContent, ok := got.Messages[0].Content.([]dto.MediaContent)
		if !ok {
			t.Fatalf("expected Content to be []dto.MediaContent, got %T (%v)", got.Messages[0].Content, got.Messages[0].Content)
		}
		if len(mediaContent) != 0 {
			t.Fatalf("expected empty media content slice, got %d items", len(mediaContent))
		}
	})

	t.Run("image_url media content is normalized to plain string ImageUrl", func(t *testing.T) {
		msg := dto.Message{
			Role: "user",
			Content: []any{
				map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url":    "https://example.com/pic.png",
						"detail": "high",
					},
				},
			},
		}
		req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{msg}}

		got := requestOpenAI2Mistral(req)
		// requestOpenAI2Mistral parses the content, rewrites image_url items'
		// ImageUrl field from a *MessageImageUrl to a plain string (the
		// wrapped image URL), then writes the parsed slice back onto Content.
		mediaContent, ok := got.Messages[0].Content.([]dto.MediaContent)
		if !ok {
			t.Fatalf("expected Content to be []dto.MediaContent, got %T (%v)", got.Messages[0].Content, got.Messages[0].Content)
		}
		if len(mediaContent) != 1 {
			t.Fatalf("expected 1 media content item, got %d", len(mediaContent))
		}
		if mediaContent[0].Type != dto.ContentTypeImageURL {
			t.Fatalf("expected image_url type, got %q", mediaContent[0].Type)
		}
		url, ok := mediaContent[0].ImageUrl.(string)
		if !ok {
			t.Fatalf("expected ImageUrl normalized to string, got %T (%v)", mediaContent[0].ImageUrl, mediaContent[0].ImageUrl)
		}
		if url != "https://example.com/pic.png" {
			t.Fatalf("expected image url preserved, got %q", url)
		}
	})

	t.Run("preserves temperature top_p max_tokens tools and tool_choice", func(t *testing.T) {
		temp := 0.5
		req := &dto.GeneralOpenAIRequest{
			Model:               "mistral-large-latest",
			Temperature:         &temp,
			TopP:                0.9,
			MaxTokens:           100,
			MaxCompletionTokens: 200,
			Tools:               []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "f"}}},
			ToolChoice:          "auto",
			Stream:              true,
			Messages:            []dto.Message{{Role: "user", Content: "hi"}},
		}
		got := requestOpenAI2Mistral(req)
		if got.Temperature == nil || *got.Temperature != 0.5 {
			t.Fatalf("expected temperature 0.5 preserved, got %v", got.Temperature)
		}
		if got.TopP != 0.9 {
			t.Fatalf("expected top_p 0.9 preserved, got %v", got.TopP)
		}
		// GetMaxTokens prefers MaxCompletionTokens over MaxTokens.
		if got.MaxTokens != 200 {
			t.Fatalf("expected max_tokens resolved to MaxCompletionTokens=200, got %d", got.MaxTokens)
		}
		if len(got.Tools) != 1 || got.Tools[0].Function.Name != "f" {
			t.Fatalf("expected tools preserved, got %+v", got.Tools)
		}
		if got.ToolChoice != "auto" {
			t.Fatalf("expected tool_choice preserved, got %v", got.ToolChoice)
		}
		if !got.Stream {
			t.Fatalf("expected stream=true preserved")
		}
		if got.Model != "mistral-large-latest" {
			t.Fatalf("expected model preserved, got %q", got.Model)
		}
	})

	t.Run("empty messages produces empty (non-nil) messages slice", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{}}
		got := requestOpenAI2Mistral(req)
		if got.Messages == nil {
			t.Fatalf("expected non-nil empty messages slice")
		}
		if len(got.Messages) != 0 {
			t.Fatalf("expected 0 messages, got %d", len(got.Messages))
		}
	})
}
