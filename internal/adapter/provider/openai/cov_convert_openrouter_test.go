package openai

// Business-acceptance tests for the OpenRouter-specific branches of
// ConvertOpenAIRequest: "-thinking" suffix stripping into a reasoning
// object, plain ReasoningEffort mapping, and Claude-style Thinking-block
// translation into OpenRouter's reasoning.max_tokens. Getting any of these
// wrong means reasoning/thinking requests either silently lose their
// configured budget or get sent to OpenRouter in a shape it rejects.

import (
	"encoding/json"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func TestConvertOpenAIRequest_OpenRouter_ThinkingSuffixStripped(t *testing.T) {
	a := &Adaptor{ChannelType: constant.ChannelTypeOpenRouter}
	c := newCovTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenRouter,
			UpstreamModelName: "deepseek/deepseek-r1-thinking",
		},
		OriginModelName: "deepseek/deepseek-r1-thinking",
	}
	req := &dto.GeneralOpenAIRequest{
		Model:    "deepseek/deepseek-r1-thinking",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := result.(*dto.GeneralOpenAIRequest)
	if r.Model != "deepseek/deepseek-r1" {
		t.Errorf("Model = %q, want suffix stripped to deepseek/deepseek-r1", r.Model)
	}
	if info.UpstreamModelName != "deepseek/deepseek-r1" {
		t.Errorf("info.UpstreamModelName = %q, want stripped", info.UpstreamModelName)
	}
	if len(r.Reasoning) == 0 {
		t.Fatal("Reasoning should be populated from -thinking suffix")
	}
	var reasoning map[string]any
	if err := json.Unmarshal(r.Reasoning, &reasoning); err != nil {
		t.Fatalf("Reasoning is not valid JSON: %v", err)
	}
	if enabled, _ := reasoning["enabled"].(bool); !enabled {
		t.Errorf("reasoning.enabled = %v, want true", reasoning["enabled"])
	}
	if r.ReasoningEffort != "" {
		t.Errorf("ReasoningEffort = %q, want cleared to empty", r.ReasoningEffort)
	}
}

func TestConvertOpenAIRequest_OpenRouter_ThinkingSuffixWithEffort(t *testing.T) {
	a := &Adaptor{ChannelType: constant.ChannelTypeOpenRouter}
	c := newCovTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenRouter,
			UpstreamModelName: "some/model-thinking",
		},
	}
	req := &dto.GeneralOpenAIRequest{
		Model:           "some/model-thinking",
		ReasoningEffort: "high",
		Messages:        []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := result.(*dto.GeneralOpenAIRequest)
	if !strings.Contains(string(r.Reasoning), `"effort":"high"`) {
		t.Errorf("Reasoning = %s, want effort=high embedded", r.Reasoning)
	}
}

func TestConvertOpenAIRequest_OpenRouter_PlainReasoningEffortMapped(t *testing.T) {
	a := &Adaptor{ChannelType: constant.ChannelTypeOpenRouter}
	c := newCovTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenRouter,
			UpstreamModelName: "openai/gpt-4o",
		},
	}
	req := &dto.GeneralOpenAIRequest{
		Model:           "openai/gpt-4o",
		ReasoningEffort: "medium",
		Messages:        []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := result.(*dto.GeneralOpenAIRequest)
	if !strings.Contains(string(r.Reasoning), `"effort":"medium"`) {
		t.Errorf("Reasoning = %s, want effort=medium", r.Reasoning)
	}
	if r.ReasoningEffort != "" {
		t.Errorf("ReasoningEffort = %q, want cleared", r.ReasoningEffort)
	}
}

func TestConvertOpenAIRequest_OpenRouter_ReasoningEffortNone_NoReasoningObject(t *testing.T) {
	a := &Adaptor{ChannelType: constant.ChannelTypeOpenRouter}
	c := newCovTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenRouter,
			UpstreamModelName: "openai/gpt-4o",
		},
	}
	req := &dto.GeneralOpenAIRequest{
		Model:           "openai/gpt-4o",
		ReasoningEffort: "none",
		Messages:        []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := result.(*dto.GeneralOpenAIRequest)
	if len(r.Reasoning) != 0 {
		t.Errorf("Reasoning = %s, want empty when effort=none", r.Reasoning)
	}
	if r.ReasoningEffort != "" {
		t.Errorf("ReasoningEffort = %q, want cleared even for none", r.ReasoningEffort)
	}
}

func TestConvertOpenAIRequest_OpenRouter_ClaudeThinkingBlockMapsToMaxTokens(t *testing.T) {
	a := &Adaptor{ChannelType: constant.ChannelTypeOpenRouter}
	c := newCovTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenRouter,
			UpstreamModelName: "anthropic/claude-3-5-sonnet",
		},
	}
	req := &dto.GeneralOpenAIRequest{
		Model:    "anthropic/claude-3-5-sonnet",
		THINKING: json.RawMessage(`{"type":"enabled","budget_tokens":2048}`),
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := result.(*dto.GeneralOpenAIRequest)
	if r.THINKING != nil {
		t.Errorf("THINKING = %s, want cleared after translation", r.THINKING)
	}
	if !strings.Contains(string(r.Reasoning), `"max_tokens":2048`) {
		t.Errorf("Reasoning = %s, want max_tokens=2048", r.Reasoning)
	}
}

func TestConvertOpenAIRequest_OpenRouter_ClaudeThinkingDisabled_NoReasoningButThinkingCleared(t *testing.T) {
	a := &Adaptor{ChannelType: constant.ChannelTypeOpenRouter}
	c := newCovTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenRouter,
			UpstreamModelName: "anthropic/claude-3-5-sonnet",
		},
	}
	req := &dto.GeneralOpenAIRequest{
		Model:    "anthropic/claude-3-5-sonnet",
		THINKING: json.RawMessage(`{"type":"disabled"}`),
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := result.(*dto.GeneralOpenAIRequest)
	if r.THINKING != nil {
		t.Errorf("THINKING = %s, want cleared regardless of enabled state", r.THINKING)
	}
	if len(r.Reasoning) != 0 {
		t.Errorf("Reasoning = %s, want empty since thinking was not enabled", r.Reasoning)
	}
}

func TestConvertOpenAIRequest_OpenRouter_ClaudeThinkingEnabledMissingBudget_Errors(t *testing.T) {
	a := &Adaptor{ChannelType: constant.ChannelTypeOpenRouter}
	c := newCovTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenRouter,
			UpstreamModelName: "anthropic/claude-3-5-sonnet",
		},
	}
	req := &dto.GeneralOpenAIRequest{
		Model:    "anthropic/claude-3-5-sonnet",
		THINKING: json.RawMessage(`{"type":"enabled"}`), // no budget_tokens
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	_, err := a.ConvertOpenAIRequest(c, info, req)
	if err == nil {
		t.Fatal("expected error when thinking enabled without budget_tokens")
	}
	if !strings.Contains(err.Error(), "BudgetTokens is nil") {
		t.Errorf("error = %q, want mention of BudgetTokens", err.Error())
	}
}

func TestConvertOpenAIRequest_OpenRouter_MalformedThinkingJSON_Errors(t *testing.T) {
	a := &Adaptor{ChannelType: constant.ChannelTypeOpenRouter}
	c := newCovTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenRouter,
			UpstreamModelName: "anthropic/claude-3-5-sonnet",
		},
	}
	req := &dto.GeneralOpenAIRequest{
		Model:    "anthropic/claude-3-5-sonnet",
		THINKING: json.RawMessage(`{not-json`),
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	_, err := a.ConvertOpenAIRequest(c, info, req)
	if err == nil {
		t.Fatal("expected error for malformed THINKING JSON")
	}
}

func TestConvertOpenAIRequest_OpenRouter_DefaultUsageInjected(t *testing.T) {
	a := &Adaptor{ChannelType: constant.ChannelTypeOpenRouter}
	c := newCovTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenRouter,
			UpstreamModelName: "openai/gpt-4o",
		},
	}
	req := &dto.GeneralOpenAIRequest{Model: "openai/gpt-4o", Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	result, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := result.(*dto.GeneralOpenAIRequest)
	if !strings.Contains(string(r.Usage), `"include":true`) {
		t.Errorf("Usage = %s, want default include:true injected", r.Usage)
	}
}

func TestConvertOpenAIRequest_OpenRouter_ExistingUsagePreserved(t *testing.T) {
	a := &Adaptor{ChannelType: constant.ChannelTypeOpenRouter}
	c := newCovTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenRouter,
			UpstreamModelName: "openai/gpt-4o",
		},
	}
	req := &dto.GeneralOpenAIRequest{
		Model:    "openai/gpt-4o",
		Usage:    json.RawMessage(`{"include":false}`),
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	result, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := result.(*dto.GeneralOpenAIRequest)
	if !strings.Contains(string(r.Usage), `"include":false`) {
		t.Errorf("Usage = %s, want caller's explicit include:false preserved", r.Usage)
	}
}
