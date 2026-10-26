package bifrosteng

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/maximhq/bifrost/core/schemas"
)

func TestToBifrostMessages(t *testing.T) {
	msgs, err := toBifrostMessages([]dto.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len=%d", len(msgs))
	}
	if msgs[0].Role != schemas.ChatMessageRoleUser {
		t.Fatalf("role=%s", msgs[0].Role)
	}
	if msgs[0].Content == nil || msgs[0].Content.ContentStr == nil || *msgs[0].Content.ContentStr != "hello" {
		t.Fatalf("content=%v", msgs[0].Content)
	}
}

func TestToBifrostMessages_MultimodalAndTools(t *testing.T) {
	assistant := dto.Message{Role: "assistant", Content: ""}
	assistant.SetToolCalls([]dto.ToolCallRequest{{
		ID:   "call_1",
		Type: "function",
		Function: dto.FunctionRequest{
			Name:      "lookup",
			Arguments: `{"q":"x"}`,
		},
	}})
	tool := dto.Message{Role: "tool", Content: `{"ok":true}`, ToolCallId: "call_1"}
	user := dto.Message{Role: "user"}
	user.SetMediaContent([]dto.MediaContent{
		{Type: "text", Text: "see"},
		{Type: "image_url", ImageUrl: &dto.MessageImageUrl{Url: "https://example.com/a.png", Detail: "low"}},
	})

	msgs, err := toBifrostMessages([]dto.Message{user, assistant, tool})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("len=%d", len(msgs))
	}
	if msgs[0].Content == nil || len(msgs[0].Content.ContentBlocks) != 2 {
		t.Fatalf("user blocks=%v", msgs[0].Content)
	}
	if msgs[0].Content.ContentBlocks[1].ImageURLStruct == nil || msgs[0].Content.ContentBlocks[1].ImageURLStruct.URL == "" {
		t.Fatalf("image block=%+v", msgs[0].Content.ContentBlocks[1])
	}
	if msgs[1].ChatAssistantMessage == nil || len(msgs[1].ToolCalls) != 1 {
		t.Fatalf("assistant tool_calls=%v", msgs[1].ChatAssistantMessage)
	}
	if msgs[2].ChatToolMessage == nil || msgs[2].ToolCallID == nil || *msgs[2].ToolCallID != "call_1" {
		t.Fatalf("tool msg=%v", msgs[2].ChatToolMessage)
	}
}

func TestChatConvertible_RejectsVideo(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user"}},
	}
	req.Messages[0].SetMediaContent([]dto.MediaContent{
		{Type: "video_url", VideoUrl: map[string]any{"url": "https://example.com/v.mp4"}},
	})
	if ChatConvertible(req) {
		t.Fatal("video_url must force legacy fallback")
	}
}

func TestChatConvertible_PlainOK(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
		Tools: []dto.ToolCallRequest{{
			Type: "function",
			Function: dto.FunctionRequest{
				Name:       "lookup",
				Parameters: map[string]any{"type": "object"},
			},
		}},
	}
	if !ChatConvertible(req) {
		t.Fatal("plain text + function tools should be convertible")
	}
	params := toChatParams(req)
	if params == nil || len(params.Tools) != 1 {
		t.Fatalf("tools params=%v", params)
	}
}

func TestFromBifrostUsage(t *testing.T) {
	u := fromBifrostUsage(&schemas.BifrostLLMUsage{
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
		PromptTokensDetails: &schemas.ChatPromptTokensDetails{
			CachedReadTokens:  2,
			CachedWriteTokens: 1,
		},
	})
	if u.PromptTokens != 10 || u.CompletionTokens != 5 || u.TotalTokens != 15 {
		t.Fatalf("usage=%+v", u)
	}
	if u.PromptTokensDetails.CachedTokens != 3 {
		t.Fatalf("cached=%d", u.PromptTokensDetails.CachedTokens)
	}
	if !u.PromptTokensIncludeCached {
		t.Fatal("expected OpenAI-wire cached semantics")
	}
}

func TestToOpenAITextResponse_WithToolCalls(t *testing.T) {
	content := "pong"
	name := "lookup"
	id := "call_1"
	typ := "function"
	resp := &schemas.BifrostChatResponse{
		ID:      "chatcmpl-1",
		Created: 1,
		Model:   "gpt-4o-mini",
		Choices: []schemas.BifrostResponseChoice{{
			Index:        0,
			FinishReason: schemas.Ptr("tool_calls"),
			ChatNonStreamResponseChoice: &schemas.ChatNonStreamResponseChoice{
				Message: &schemas.ChatMessage{
					Role: schemas.ChatMessageRoleAssistant,
					Content: &schemas.ChatMessageContent{
						ContentStr: &content,
					},
					ChatAssistantMessage: &schemas.ChatAssistantMessage{
						ToolCalls: []schemas.ChatAssistantMessageToolCall{{
							Index: 0,
							ID:    &id,
							Type:  &typ,
							Function: schemas.ChatAssistantMessageToolCallFunction{
								Name:      &name,
								Arguments: `{"q":"x"}`,
							},
						}},
					},
				},
			},
		}},
		Usage: &schemas.BifrostLLMUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}
	out := toOpenAITextResponse(resp, "gpt-4o-mini")
	if out.Id != "chatcmpl-1" || len(out.Choices) != 1 {
		t.Fatalf("out=%+v", out)
	}
	calls := out.Choices[0].ParseToolCalls()
	if len(calls) != 1 || calls[0].Function.Name != "lookup" {
		t.Fatalf("tool_calls=%+v", calls)
	}
}
