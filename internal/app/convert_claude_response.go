package app

import (
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// claudeTerminalUsage converts an OpenAI-wire usage into the terminal Anthropic-wire usage.
func claudeTerminalUsage(oaiUsage *dto.Usage) *dto.ClaudeUsage {
	if oaiUsage == nil {
		return nil
	}
	return &dto.ClaudeUsage{
		InputTokens:              oaiUsage.AnthropicInputTokens(),
		OutputTokens:             oaiUsage.CompletionTokens,
		CacheCreationInputTokens: oaiUsage.PromptTokensDetails.CachedCreationTokens,
		CacheReadInputTokens:     oaiUsage.PromptTokensDetails.CachedTokens,
	}
}

// claudeTerminalEvents emits message_delta and message_stop when terminal usage is available.
func claudeTerminalEvents(openAIResponse *dto.ChatCompletionsStreamResponse, info *relaycommon.RelayInfo) []*dto.ClaudeResponse {
	oaiUsage := openAIResponse.Usage
	if oaiUsage == nil {
		oaiUsage = info.Usage
	}
	if oaiUsage == nil {
		return nil
	}
	info.Done = true
	return []*dto.ClaudeResponse{
		{
			Type:  "message_delta",
			Usage: claudeTerminalUsage(oaiUsage),
			Delta: &dto.ClaudeMediaMessage{
				StopReason: common.GetPointer[string](stopReasonOpenAI2Claude(info.FinishReason)),
			},
		},
		{Type: "message_stop"},
	}
}

// CloseClaudeStream closes any open content block and emits terminal events once stream conversion finishes.
func CloseClaudeStream(info *relaycommon.RelayInfo) []*dto.ClaudeResponse {
	if info.ClaudeConvertInfo == nil || info.Done {
		return nil
	}
	var out []*dto.ClaudeResponse
	if info.FinishReason == "" {
		if info.LastMessagesType != "" {
			out = append(out, generateStopBlock(info.Index))
		}
		info.FinishReason = "stop"
	}
	if info.Usage == nil {
		info.Usage = &dto.Usage{}
	}
	return append(out, claudeTerminalEvents(&dto.ChatCompletionsStreamResponse{}, info)...)
}

func generateStopBlock(index int) *dto.ClaudeResponse {
	return &dto.ClaudeResponse{
		Type:  "content_block_stop",
		Index: common.GetPointer[int](index),
	}
}

func ResponseOpenAI2Claude(openAIResponse *dto.OpenAITextResponse, info *relaycommon.RelayInfo) *dto.ClaudeResponse {
	var stopReason string
	contents := make([]dto.ClaudeMediaMessage, 0)
	claudeResponse := &dto.ClaudeResponse{
		Id:    openAIResponse.Id,
		Type:  "message",
		Role:  "assistant",
		Model: openAIResponse.Model,
	}
	for _, choice := range openAIResponse.Choices {
		stopReason = stopReasonOpenAI2Claude(choice.FinishReason)
		if choice.FinishReason == "tool_calls" {
			for _, toolUse := range choice.Message.ParseToolCalls() {
				claudeContent := dto.ClaudeMediaMessage{}
				claudeContent.Type = "tool_use"
				claudeContent.Id = toolUse.ID
				claudeContent.Name = toolUse.Function.Name
				var mapParams map[string]interface{}
				if err := common.Unmarshal([]byte(toolUse.Function.Arguments), &mapParams); err == nil {
					claudeContent.Input = mapParams
				} else {
					claudeContent.Input = toolUse.Function.Arguments
				}
				contents = append(contents, claudeContent)
			}
		} else {
			claudeContent := dto.ClaudeMediaMessage{}
			claudeContent.Type = "text"
			claudeContent.SetText(choice.Message.StringContent())
			contents = append(contents, claudeContent)
		}
	}
	claudeResponse.Content = contents
	claudeResponse.StopReason = stopReason
	claudeResponse.Usage = claudeTerminalUsage(&openAIResponse.Usage)

	return claudeResponse
}

func stopReasonOpenAI2Claude(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "stop_sequence":
		return "stop_sequence"
	case "length":
		fallthrough
	case "max_tokens":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		return reason
	}
}
