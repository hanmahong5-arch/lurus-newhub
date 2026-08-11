package openai

// Business-acceptance tests for the Gemini/Claude -> OpenAI request bridge
// methods on Adaptor. These are the entry points hit whenever a client talks
// to an OpenAI-format upstream channel using the Gemini or Claude wire
// protocol (RelayFormatGemini / RelayFormatClaude); a broken bridge here
// means "Claude/Gemini clients silently get garbage sent to OpenAI-shaped
// upstreams."

import (
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func TestAdaptor_ConvertGeminiRequest(t *testing.T) {
	a := &Adaptor{ChannelType: constant.ChannelTypeOpenAI}
	c := newCovTestContext()

	t.Run("converts contents into OpenAI messages and forwards to ConvertOpenAIRequest", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeOpenAI,
				UpstreamModelName: "gpt-4o",
			},
			IsStream: false,
		}
		req := &dto.GeminiChatRequest{
			Contents: []dto.GeminiChatContent{
				{Role: "user", Parts: []dto.GeminiPart{{Text: "hello from gemini"}}},
			},
		}
		result, err := a.ConvertGeminiRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r, ok := result.(*dto.GeneralOpenAIRequest)
		if !ok {
			t.Fatalf("result type = %T, want *dto.GeneralOpenAIRequest", result)
		}
		if r.Model != "gpt-4o" {
			t.Errorf("Model = %q, want gpt-4o (from info.UpstreamModelName)", r.Model)
		}
		if len(r.Messages) != 1 {
			t.Fatalf("Messages = %d, want 1", len(r.Messages))
		}
		if r.Messages[0].Role != "user" {
			t.Errorf("Messages[0].Role = %q, want user", r.Messages[0].Role)
		}
	})

	t.Run("empty contents still produces a valid (empty-message) request, not an error", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeOpenAI,
				UpstreamModelName: "gpt-4o",
			},
		}
		req := &dto.GeminiChatRequest{Contents: nil}
		result, err := a.ConvertGeminiRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r := result.(*dto.GeneralOpenAIRequest)
		if len(r.Messages) != 0 {
			t.Errorf("Messages = %v, want empty for nil contents", r.Messages)
		}
	})
}

func TestAdaptor_ConvertClaudeRequest(t *testing.T) {
	a := &Adaptor{ChannelType: constant.ChannelTypeOpenAI}
	c := newCovTestContext()

	t.Run("converts basic claude request fields", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeOpenAI,
				UpstreamModelName: "gpt-4o",
			},
		}
		req := dto.ClaudeRequest{
			Model:     "claude-3-5-sonnet",
			MaxTokens: 128,
			Messages: []dto.ClaudeMessage{
				{Role: "user", Content: "hi from claude"},
			},
		}
		result, err := a.ConvertClaudeRequest(c, info, &req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r, ok := result.(*dto.GeneralOpenAIRequest)
		if !ok {
			t.Fatalf("result type = %T, want *dto.GeneralOpenAIRequest", result)
		}
		if r.MaxTokens != 128 {
			t.Errorf("MaxTokens = %d, want 128", r.MaxTokens)
		}
		if len(r.Messages) != 1 || r.Messages[0].Role != "user" {
			t.Errorf("Messages = %+v, want single user message", r.Messages)
		}
	})

	t.Run("stream options injected when SupportStreamOptions and IsStream both true", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:          constant.ChannelTypeOpenAI,
				UpstreamModelName:    "gpt-4o",
				SupportStreamOptions: true,
			},
			IsStream: true,
		}
		req := dto.ClaudeRequest{
			Model:     "claude-3-5-sonnet",
			MaxTokens: 64,
			Stream:    true,
			Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
		}
		result, err := a.ConvertClaudeRequest(c, info, &req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r := result.(*dto.GeneralOpenAIRequest)
		if r.StreamOptions == nil || !r.StreamOptions.IncludeUsage {
			t.Errorf("StreamOptions = %+v, want IncludeUsage=true", r.StreamOptions)
		}
	})

	t.Run("stream options NOT injected when SupportStreamOptions false", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:          constant.ChannelTypeOpenAI,
				UpstreamModelName:    "gpt-4o",
				SupportStreamOptions: false,
			},
			IsStream: true,
		}
		req := dto.ClaudeRequest{
			Model:     "claude-3-5-sonnet",
			MaxTokens: 64,
			Stream:    true,
			Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
		}
		result, err := a.ConvertClaudeRequest(c, info, &req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r := result.(*dto.GeneralOpenAIRequest)
		if r.StreamOptions != nil {
			t.Errorf("StreamOptions = %+v, want nil when SupportStreamOptions=false", r.StreamOptions)
		}
	})

	t.Run("openrouter thinking budget maps into Reasoning field", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeOpenRouter,
				UpstreamModelName: "anthropic/claude-3-5-sonnet",
			},
		}
		budget := 1024
		req := dto.ClaudeRequest{
			Model:     "claude-3-5-sonnet",
			MaxTokens: 64,
			Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
			Thinking:  &dto.Thinking{Type: "enabled", BudgetTokens: &budget},
		}
		aOR := &Adaptor{ChannelType: constant.ChannelTypeOpenRouter}
		result, err := aOR.ConvertClaudeRequest(c, info, &req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r := result.(*dto.GeneralOpenAIRequest)
		if len(r.Reasoning) == 0 {
			t.Error("Reasoning should be populated for openrouter + enabled thinking")
		}
	})
}
