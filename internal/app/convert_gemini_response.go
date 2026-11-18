package app

import (
	"encoding/json"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// geminiUsageMetadata normalizes an OpenAI-wire usage into Gemini usageMetadata.
func geminiUsageMetadata(u *dto.Usage) dto.GeminiUsageMetadata {
	if u == nil {
		return dto.GeminiUsageMetadata{}
	}
	reasoning := u.CompletionTokenDetails.ReasoningTokens
	total := u.TotalTokens
	if total == 0 {
		total = u.PromptTokens + u.CompletionTokens
	}
	return dto.GeminiUsageMetadata{
		PromptTokenCount:        u.PromptTokens,
		CandidatesTokenCount:    u.CompletionTokens - reasoning,
		ThoughtsTokenCount:      reasoning,
		TotalTokenCount:         total,
		CachedContentTokenCount: u.PromptTokensDetails.CachedTokens,
	}
}

func ResponseOpenAI2Gemini(openAIResponse *dto.OpenAITextResponse, info *relaycommon.RelayInfo) *dto.GeminiChatResponse {
	geminiResponse := &dto.GeminiChatResponse{
		Candidates:    make([]dto.GeminiChatCandidate, 0, len(openAIResponse.Choices)),
		UsageMetadata: geminiUsageMetadata(&openAIResponse.Usage),
	}

	for _, choice := range openAIResponse.Choices {
		candidate := dto.GeminiChatCandidate{
			Index:         int64(choice.Index),
			SafetyRatings: []dto.GeminiChatSafetyRating{},
		}

		var finishReason string
		switch choice.FinishReason {
		case "stop":
			finishReason = "STOP"
		case "length":
			finishReason = "MAX_TOKENS"
		case "content_filter":
			finishReason = "SAFETY"
		case "tool_calls":
			finishReason = "STOP"
		default:
			finishReason = "STOP"
		}
		candidate.FinishReason = &finishReason

		content := dto.GeminiChatContent{
			Role:  "model",
			Parts: make([]dto.GeminiPart, 0),
		}

		toolCalls := choice.Message.ParseToolCalls()
		if len(toolCalls) > 0 {
			for _, toolCall := range toolCalls {
				var args map[string]interface{}
				if toolCall.Function.Arguments != "" {
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
						args = map[string]interface{}{"arguments": toolCall.Function.Arguments}
					}
				} else {
					args = make(map[string]interface{})
				}

				content.Parts = append(content.Parts, dto.GeminiPart{
					FunctionCall: &dto.FunctionCall{
						FunctionName: toolCall.Function.Name,
						Arguments:    args,
					},
				})
			}
		} else {
			textContent := choice.Message.StringContent()
			if textContent != "" {
				content.Parts = append(content.Parts, dto.GeminiPart{
					Text: textContent,
				})
			}
		}

		candidate.Content = content
		geminiResponse.Candidates = append(geminiResponse.Candidates, candidate)
	}

	return geminiResponse
}

func geminiFinishReason(openAIFinishReason string) string {
	switch openAIFinishReason {
	case "length":
		return "MAX_TOKENS"
	case "content_filter":
		return "SAFETY"
	default:
		return "STOP"
	}
}

func StreamResponseOpenAI2Gemini(openAIResponse *dto.ChatCompletionsStreamResponse, info *relaycommon.RelayInfo) *dto.GeminiChatResponse {
	hasContent := false
	hasFinishReason := false
	for _, choice := range openAIResponse.Choices {
		if len(choice.Delta.GetContentString()) > 0 || (choice.Delta.ToolCalls != nil && len(choice.Delta.ToolCalls) > 0) {
			hasContent = true
		}
		if choice.FinishReason != nil {
			hasFinishReason = true
			info.StreamFinishReason = *choice.FinishReason
		}
	}
	hasUsage := openAIResponse.Usage != nil

	if !hasContent && !hasFinishReason && !hasUsage {
		return nil
	}

	geminiResponse := &dto.GeminiChatResponse{
		Candidates: make([]dto.GeminiChatCandidate, 0, len(openAIResponse.Choices)),
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:     info.GetEstimatePromptTokens(),
			CandidatesTokenCount: 0,
			TotalTokenCount:      info.GetEstimatePromptTokens(),
		},
	}

	if openAIResponse.Usage != nil {
		geminiResponse.UsageMetadata = geminiUsageMetadata(openAIResponse.Usage)
	}

	for _, choice := range openAIResponse.Choices {
		candidate := dto.GeminiChatCandidate{
			Index:         int64(choice.Index),
			SafetyRatings: []dto.GeminiChatSafetyRating{},
		}

		if choice.FinishReason != nil {
			finishReason := geminiFinishReason(*choice.FinishReason)
			candidate.FinishReason = &finishReason
		}

		content := dto.GeminiChatContent{
			Role:  "model",
			Parts: make([]dto.GeminiPart, 0),
		}

		if choice.Delta.ToolCalls != nil {
			for _, toolCall := range choice.Delta.ToolCalls {
				var args map[string]interface{}
				if toolCall.Function.Arguments != "" {
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
						args = map[string]interface{}{"arguments": toolCall.Function.Arguments}
					}
				} else {
					args = make(map[string]interface{})
				}

				content.Parts = append(content.Parts, dto.GeminiPart{
					FunctionCall: &dto.FunctionCall{
						FunctionName: toolCall.Function.Name,
						Arguments:    args,
					},
				})
			}
		} else {
			textContent := choice.Delta.GetContentString()
			if textContent != "" {
				content.Parts = append(content.Parts, dto.GeminiPart{
					Text: textContent,
				})
			}
		}

		candidate.Content = content
		geminiResponse.Candidates = append(geminiResponse.Candidates, candidate)
	}

	if len(openAIResponse.Choices) == 0 && hasUsage {
		candidate := dto.GeminiChatCandidate{
			SafetyRatings: []dto.GeminiChatSafetyRating{},
			Content:       dto.GeminiChatContent{Role: "model", Parts: make([]dto.GeminiPart, 0)},
		}
		if info.StreamFinishReason != "" {
			fr := geminiFinishReason(info.StreamFinishReason)
			candidate.FinishReason = &fr
		}
		geminiResponse.Candidates = append(geminiResponse.Candidates, candidate)
	}

	return geminiResponse
}
