package dto

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestGetOpenAIError(t *testing.T) {
	t.Run("nil error returns nil", func(t *testing.T) {
		if got := GetOpenAIError(nil); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})

	t.Run("value type passthrough", func(t *testing.T) {
		in := types.OpenAIError{Message: "boom", Type: "invalid_request_error"}
		got := GetOpenAIError(in)
		if got == nil || got.Message != "boom" || got.Type != "invalid_request_error" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("pointer type passthrough", func(t *testing.T) {
		in := &types.OpenAIError{Message: "boom2"}
		got := GetOpenAIError(in)
		if got != in {
			t.Fatalf("expected same pointer identity")
		}
	})

	t.Run("map is converted field by field", func(t *testing.T) {
		in := map[string]interface{}{
			"type": "server_error", "message": "oops", "param": "model", "code": "500",
		}
		got := GetOpenAIError(in)
		if got == nil || got.Type != "server_error" || got.Message != "oops" || got.Param != "model" || got.Code != "500" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("map missing fields leaves zero values", func(t *testing.T) {
		in := map[string]interface{}{"message": "only message"}
		got := GetOpenAIError(in)
		if got == nil || got.Message != "only message" || got.Type != "" || got.Param != "" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("string becomes generic error message", func(t *testing.T) {
		got := GetOpenAIError("plain string error")
		if got == nil || got.Type != "error" || got.Message != "plain string error" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("unknown type falls back to formatted message", func(t *testing.T) {
		got := GetOpenAIError(12345)
		if got == nil || got.Type != "unknown_error" || got.Message != "12345" {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestSimpleResponse_GetOpenAIError(t *testing.T) {
	s := &SimpleResponse{Error: map[string]interface{}{"message": "bad request", "type": "invalid_request_error"}}
	got := s.GetOpenAIError()
	if got == nil || got.Message != "bad request" {
		t.Fatalf("got %+v", got)
	}

	s2 := &SimpleResponse{}
	if got := s2.GetOpenAIError(); got != nil {
		t.Fatalf("got %+v, want nil for no error", got)
	}
}

func TestOpenAITextResponse_GetOpenAIError(t *testing.T) {
	o := &OpenAITextResponse{Error: "upstream failed"}
	got := o.GetOpenAIError()
	if got == nil || got.Message != "upstream failed" {
		t.Fatalf("got %+v", got)
	}
}

func TestToolCallResponse_SetIndex(t *testing.T) {
	tc := &ToolCallResponse{}
	if tc.Index != nil {
		t.Fatal("Index should start nil")
	}
	tc.SetIndex(3)
	if tc.Index == nil || *tc.Index != 3 {
		t.Fatalf("Index = %v, want pointer to 3", tc.Index)
	}
}

func TestChatCompletionsStreamResponseChoiceDelta_ContentHelpers(t *testing.T) {
	d := &ChatCompletionsStreamResponseChoiceDelta{}
	if got := d.GetContentString(); got != "" {
		t.Fatalf("got %q, want empty for nil Content", got)
	}
	d.SetContentString("hello")
	if got := d.GetContentString(); got != "hello" {
		t.Fatalf("got %q, want hello", got)
	}
}

func TestChatCompletionsStreamResponseChoiceDelta_ReasoningContent(t *testing.T) {
	t.Run("both nil returns empty", func(t *testing.T) {
		d := &ChatCompletionsStreamResponseChoiceDelta{}
		if got := d.GetReasoningContent(); got != "" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("ReasoningContent takes priority over Reasoning", func(t *testing.T) {
		rc := "primary"
		rs := "secondary"
		d := &ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: &rc, Reasoning: &rs}
		if got := d.GetReasoningContent(); got != "primary" {
			t.Fatalf("got %q, want primary", got)
		}
	})

	t.Run("falls back to Reasoning when ReasoningContent is nil", func(t *testing.T) {
		rs := "fallback"
		d := &ChatCompletionsStreamResponseChoiceDelta{Reasoning: &rs}
		if got := d.GetReasoningContent(); got != "fallback" {
			t.Fatalf("got %q, want fallback", got)
		}
	})

	t.Run("SetReasoningContent populates ReasoningContent field only", func(t *testing.T) {
		d := &ChatCompletionsStreamResponseChoiceDelta{}
		d.SetReasoningContent("set-value")
		if d.ReasoningContent == nil || *d.ReasoningContent != "set-value" {
			t.Fatalf("ReasoningContent = %v", d.ReasoningContent)
		}
		if d.Reasoning != nil {
			t.Fatalf("Reasoning should remain nil, got %v", *d.Reasoning)
		}
	})
}

func strPtr(s string) *string { return &s }

func TestChatCompletionsStreamResponse_IsFinished(t *testing.T) {
	cases := []struct {
		name     string
		resp     *ChatCompletionsStreamResponse
		wantDone bool
	}{
		{"no choices", &ChatCompletionsStreamResponse{}, false},
		{"nil finish reason", &ChatCompletionsStreamResponse{Choices: []ChatCompletionsStreamResponseChoice{{FinishReason: nil}}}, false},
		{"empty string finish reason", &ChatCompletionsStreamResponse{Choices: []ChatCompletionsStreamResponseChoice{{FinishReason: strPtr("")}}}, false},
		{"stop finish reason", &ChatCompletionsStreamResponse{Choices: []ChatCompletionsStreamResponseChoice{{FinishReason: strPtr("stop")}}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.resp.IsFinished(); got != c.wantDone {
				t.Errorf("IsFinished() = %v, want %v", got, c.wantDone)
			}
		})
	}
}

func TestChatCompletionsStreamResponse_ToolCallHelpers(t *testing.T) {
	t.Run("no choices means no tool call", func(t *testing.T) {
		r := &ChatCompletionsStreamResponse{}
		if r.IsToolCall() {
			t.Fatal("expected false")
		}
		if got := r.GetFirstToolCall(); got != nil {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("empty tool calls slice means no tool call", func(t *testing.T) {
		r := &ChatCompletionsStreamResponse{Choices: []ChatCompletionsStreamResponseChoice{{}}}
		if r.IsToolCall() {
			t.Fatal("expected false")
		}
	})

	t.Run("present tool calls detected and first returned", func(t *testing.T) {
		r := &ChatCompletionsStreamResponse{Choices: []ChatCompletionsStreamResponseChoice{{
			Delta: ChatCompletionsStreamResponseChoiceDelta{
				ToolCalls: []ToolCallResponse{
					{ID: "call_1", Type: "function", Function: FunctionResponse{Name: "fn1"}},
					{ID: "call_2"},
				},
			},
		}}}
		if !r.IsToolCall() {
			t.Fatal("expected true")
		}
		first := r.GetFirstToolCall()
		if first == nil || first.ID != "call_1" {
			t.Fatalf("got %+v", first)
		}
	})

	t.Run("ClearToolCalls no-op when no tool calls present", func(t *testing.T) {
		r := &ChatCompletionsStreamResponse{Choices: []ChatCompletionsStreamResponseChoice{{}}}
		r.ClearToolCalls() // must not panic
	})

	t.Run("ClearToolCalls clears ID/Type/Name but preserves Arguments", func(t *testing.T) {
		r := &ChatCompletionsStreamResponse{Choices: []ChatCompletionsStreamResponseChoice{{
			Delta: ChatCompletionsStreamResponseChoiceDelta{
				ToolCalls: []ToolCallResponse{
					{ID: "call_1", Type: "function", Function: FunctionResponse{Name: "fn1", Arguments: `{"x":1}`}},
				},
			},
		}}}
		r.ClearToolCalls()
		tc := r.Choices[0].Delta.ToolCalls[0]
		if tc.ID != "" {
			t.Errorf("ID = %q, want cleared", tc.ID)
		}
		if tc.Type != nil {
			t.Errorf("Type = %v, want cleared", tc.Type)
		}
		if tc.Function.Name != "" {
			t.Errorf("Function.Name = %q, want cleared", tc.Function.Name)
		}
		if tc.Function.Arguments != `{"x":1}` {
			t.Errorf("Function.Arguments = %q, want preserved", tc.Function.Arguments)
		}
	})
}

func TestChatCompletionsStreamResponse_Copy(t *testing.T) {
	sf := "fp-1"
	original := &ChatCompletionsStreamResponse{
		Id: "chatcmpl-1", Model: "gpt-4o", SystemFingerprint: &sf,
		Choices: []ChatCompletionsStreamResponseChoice{{Index: 0, FinishReason: strPtr("stop")}},
	}
	cpy := original.Copy()

	if cpy == original {
		t.Fatal("Copy() must return a distinct pointer")
	}
	if cpy.Id != original.Id || cpy.Model != original.Model {
		t.Fatalf("scalar fields not copied correctly: %+v", cpy)
	}

	// Mutating the copy's Choices slice must not affect the original slice
	// (Copy() allocates a new backing array via `copy`).
	cpy.Choices[0].Index = 99
	if original.Choices[0].Index == 99 {
		t.Fatal("mutating copy's Choices leaked into original: Copy() is not slice-independent")
	}
}

func TestChatCompletionsStreamResponse_SystemFingerprint(t *testing.T) {
	r := &ChatCompletionsStreamResponse{}
	if got := r.GetSystemFingerprint(); got != "" {
		t.Fatalf("got %q, want empty for nil pointer", got)
	}
	r.SetSystemFingerprint("fp-123")
	if got := r.GetSystemFingerprint(); got != "fp-123" {
		t.Fatalf("got %q, want fp-123", got)
	}
}

func TestOpenAIResponsesResponse_ImageGenerationHelpers(t *testing.T) {
	t.Run("empty output", func(t *testing.T) {
		r := &OpenAIResponsesResponse{}
		if r.HasImageGenerationCall() {
			t.Fatal("expected false")
		}
		if got := r.GetQuality(); got != "" {
			t.Fatalf("got %q", got)
		}
		if got := r.GetSize(); got != "" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("output present but no image generation call", func(t *testing.T) {
		r := &OpenAIResponsesResponse{Output: []ResponsesOutput{{Type: "message"}}}
		if r.HasImageGenerationCall() {
			t.Fatal("expected false")
		}
		if got := r.GetQuality(); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
		if got := r.GetSize(); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})

	t.Run("image generation call present", func(t *testing.T) {
		r := &OpenAIResponsesResponse{Output: []ResponsesOutput{
			{Type: "message"},
			{Type: ResponsesOutputTypeImageGenerationCall, Quality: "hd", Size: "1024x1024"},
		}}
		if !r.HasImageGenerationCall() {
			t.Fatal("expected true")
		}
		if got := r.GetQuality(); got != "hd" {
			t.Fatalf("got %q, want hd", got)
		}
		if got := r.GetSize(); got != "1024x1024" {
			t.Fatalf("got %q, want 1024x1024", got)
		}
	})

	t.Run("GetOpenAIError delegates correctly", func(t *testing.T) {
		r := &OpenAIResponsesResponse{Error: "responses api failed"}
		got := r.GetOpenAIError()
		if got == nil || got.Message != "responses api failed" {
			t.Fatalf("got %+v", got)
		}
	})
}
