package app

import (
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func StreamResponseOpenAI2Claude(openAIResponse *dto.ChatCompletionsStreamResponse, info *relaycommon.RelayInfo) []*dto.ClaudeResponse {
	if info.ClaudeConvertInfo.Done {
		return nil
	}

	if info.SendResponseCount == 1 {
		return streamClaudeFirstChunk(openAIResponse, info)
	}

	if len(openAIResponse.Choices) == 0 {
		return nil
	}

	return streamClaudeDeltaChunk(openAIResponse, info)
}

func streamClaudeFirstChunk(openAIResponse *dto.ChatCompletionsStreamResponse, info *relaycommon.RelayInfo) []*dto.ClaudeResponse {
	var claudeResponses []*dto.ClaudeResponse
	msg := &dto.ClaudeMediaMessage{
		Id:    openAIResponse.Id,
		Model: openAIResponse.Model,
		Type:  "message",
		Role:  "assistant",
		Usage: &dto.ClaudeUsage{
			InputTokens:  info.GetEstimatePromptTokens(),
			OutputTokens: 0,
		},
	}
	msg.SetContent(make([]any, 0))
	claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
		Type:    "message_start",
		Message: msg,
	})

	if openAIResponse.IsToolCall() {
		info.ClaudeConvertInfo.LastMessagesType = relaycommon.LastMessageTypeTools
		var toolCall dto.ToolCallResponse
		if len(openAIResponse.Choices) > 0 && len(openAIResponse.Choices[0].Delta.ToolCalls) > 0 {
			toolCall = openAIResponse.Choices[0].Delta.ToolCalls[0]
		} else if first := openAIResponse.GetFirstToolCall(); first != nil {
			toolCall = *first
		}
		resp := &dto.ClaudeResponse{
			Type: "content_block_start",
			ContentBlock: &dto.ClaudeMediaMessage{
				Id:    toolCall.ID,
				Type:  "tool_use",
				Name:  toolCall.Function.Name,
				Input: map[string]interface{}{},
			},
		}
		resp.SetIndex(0)
		claudeResponses = append(claudeResponses, resp)
		if toolCall.Function.Arguments != "" {
			claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
				Index: &info.ClaudeConvertInfo.Index,
				Type:  "content_block_delta",
				Delta: &dto.ClaudeMediaMessage{
					Type:        "input_json_delta",
					PartialJson: &toolCall.Function.Arguments,
				},
			})
		}
	}

	if len(openAIResponse.Choices) > 0 {
		reasoning := openAIResponse.Choices[0].Delta.GetReasoningContent()
		content := openAIResponse.Choices[0].Delta.GetContentString()

		if reasoning != "" {
			claudeResponses = append(claudeResponses,
				&dto.ClaudeResponse{
					Index: &info.ClaudeConvertInfo.Index,
					Type:  "content_block_start",
					ContentBlock: &dto.ClaudeMediaMessage{
						Type:     "thinking",
						Thinking: common.GetPointer[string](""),
					},
				},
				&dto.ClaudeResponse{
					Index: &info.ClaudeConvertInfo.Index,
					Type:  "content_block_delta",
					Delta: &dto.ClaudeMediaMessage{
						Type:     "thinking_delta",
						Thinking: &reasoning,
					},
				},
			)
			info.ClaudeConvertInfo.LastMessagesType = relaycommon.LastMessageTypeThinking
		} else if content != "" {
			claudeResponses = append(claudeResponses,
				&dto.ClaudeResponse{
					Index: &info.ClaudeConvertInfo.Index,
					Type:  "content_block_start",
					ContentBlock: &dto.ClaudeMediaMessage{
						Type: "text",
						Text: common.GetPointer[string](""),
					},
				},
				&dto.ClaudeResponse{
					Index: &info.ClaudeConvertInfo.Index,
					Type:  "content_block_delta",
					Delta: &dto.ClaudeMediaMessage{
						Type: "text_delta",
						Text: common.GetPointer[string](content),
					},
				},
			)
			info.ClaudeConvertInfo.LastMessagesType = relaycommon.LastMessageTypeText
		}
	}

	if len(openAIResponse.Choices) > 0 && openAIResponse.Choices[0].FinishReason != nil && *openAIResponse.Choices[0].FinishReason != "" {
		info.FinishReason = *openAIResponse.Choices[0].FinishReason
		claudeResponses = append(claudeResponses, generateStopBlock(info.ClaudeConvertInfo.Index))
		claudeResponses = append(claudeResponses, claudeTerminalEvents(openAIResponse, info)...)
	}
	return claudeResponses
}

func streamClaudeDeltaChunk(openAIResponse *dto.ChatCompletionsStreamResponse, info *relaycommon.RelayInfo) []*dto.ClaudeResponse {
	var claudeResponses []*dto.ClaudeResponse
	chosenChoice := openAIResponse.Choices[0]
	doneChunk := chosenChoice.FinishReason != nil && *chosenChoice.FinishReason != ""
	if doneChunk {
		info.FinishReason = *chosenChoice.FinishReason
	}

	var claudeResponse dto.ClaudeResponse
	var isEmpty bool
	claudeResponse.Type = "content_block_delta"
	if len(chosenChoice.Delta.ToolCalls) > 0 {
		toolCalls := chosenChoice.Delta.ToolCalls
		if info.ClaudeConvertInfo.LastMessagesType != relaycommon.LastMessageTypeTools {
			claudeResponses = append(claudeResponses, generateStopBlock(info.ClaudeConvertInfo.Index))
			info.ClaudeConvertInfo.Index++
		}
		info.ClaudeConvertInfo.LastMessagesType = relaycommon.LastMessageTypeTools

		for i, toolCall := range toolCalls {
			blockIndex := info.ClaudeConvertInfo.Index
			if toolCall.Index != nil {
				blockIndex = *toolCall.Index
			} else if len(toolCalls) > 1 {
				blockIndex = info.ClaudeConvertInfo.Index + i
			}

			idx := blockIndex
			if toolCall.Function.Name != "" {
				claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
					Index: &idx,
					Type:  "content_block_start",
					ContentBlock: &dto.ClaudeMediaMessage{
						Id:    toolCall.ID,
						Type:  "tool_use",
						Name:  toolCall.Function.Name,
						Input: map[string]interface{}{},
					},
				})
			}

			if len(toolCall.Function.Arguments) > 0 {
				claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
					Index: &idx,
					Type:  "content_block_delta",
					Delta: &dto.ClaudeMediaMessage{
						Type:        "input_json_delta",
						PartialJson: &toolCall.Function.Arguments,
					},
				})
			}

			info.ClaudeConvertInfo.Index = blockIndex
		}
	} else {
		reasoning := chosenChoice.Delta.GetReasoningContent()
		textContent := chosenChoice.Delta.GetContentString()
		if reasoning != "" {
			if info.ClaudeConvertInfo.LastMessagesType != relaycommon.LastMessageTypeThinking {
				claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
					Index: &info.ClaudeConvertInfo.Index,
					Type:  "content_block_start",
					ContentBlock: &dto.ClaudeMediaMessage{
						Type:     "thinking",
						Thinking: common.GetPointer[string](""),
					},
				})
			}
			info.ClaudeConvertInfo.LastMessagesType = relaycommon.LastMessageTypeThinking
			claudeResponse.Delta = &dto.ClaudeMediaMessage{
				Type:     "thinking_delta",
				Thinking: &reasoning,
			}
		} else if textContent != "" {
			if info.ClaudeConvertInfo.LastMessagesType != relaycommon.LastMessageTypeText {
				if info.ClaudeConvertInfo.LastMessagesType == relaycommon.LastMessageTypeThinking || info.ClaudeConvertInfo.LastMessagesType == relaycommon.LastMessageTypeTools {
					claudeResponses = append(claudeResponses, generateStopBlock(info.ClaudeConvertInfo.Index))
					info.ClaudeConvertInfo.Index++
				}
				claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
					Index: &info.ClaudeConvertInfo.Index,
					Type:  "content_block_start",
					ContentBlock: &dto.ClaudeMediaMessage{
						Type: "text",
						Text: common.GetPointer[string](""),
					},
				})
			}
			info.ClaudeConvertInfo.LastMessagesType = relaycommon.LastMessageTypeText
			claudeResponse.Delta = &dto.ClaudeMediaMessage{
				Type: "text_delta",
				Text: common.GetPointer[string](textContent),
			}
		} else {
			isEmpty = true
		}
	}

	claudeResponse.Index = &info.ClaudeConvertInfo.Index
	if !isEmpty && claudeResponse.Delta != nil {
		claudeResponses = append(claudeResponses, &claudeResponse)
	}

	if doneChunk || info.ClaudeConvertInfo.Done {
		claudeResponses = append(claudeResponses, generateStopBlock(info.ClaudeConvertInfo.Index))
		return append(claudeResponses, claudeTerminalEvents(openAIResponse, info)...)
	}

	return claudeResponses
}
