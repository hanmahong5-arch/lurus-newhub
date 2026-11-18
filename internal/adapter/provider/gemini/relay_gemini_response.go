package gemini

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/gin-gonic/gin"
)

func unescapeString(s string) (string, error) {
	var result []rune
	escaped := false
	i := 0

	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError {
			return "", fmt.Errorf("invalid UTF-8 encoding")
		}

		if escaped {
			switch r {
			case '"':
				result = append(result, '"')
			case '\\':
				result = append(result, '\\')
			case '/':
				result = append(result, '/')
			case 'b':
				result = append(result, '\b')
			case 'f':
				result = append(result, '\f')
			case 'n':
				result = append(result, '\n')
			case 'r':
				result = append(result, '\r')
			case 't':
				result = append(result, '\t')
			case '\'':
				result = append(result, '\'')
			default:
				result = append(result, '\\', r)
			}
			escaped = false
		} else {
			if r == '\\' {
				escaped = true
			} else {
				result = append(result, r)
			}
		}
		i += size
	}

	return string(result), nil
}

func unescapeMapOrSlice(data interface{}) interface{} {
	switch v := data.(type) {
	case map[string]interface{}:
		for k, val := range v {
			v[k] = unescapeMapOrSlice(val)
		}
	case []interface{}:
		for i, val := range v {
			v[i] = unescapeMapOrSlice(val)
		}
	case string:
		if unescaped, err := unescapeString(v); err != nil {
			return v
		} else {
			return unescaped
		}
	}
	return data
}

func getResponseToolCall(item *dto.GeminiPart) *dto.ToolCallResponse {
	var argsBytes []byte
	var err error
	if result, ok := item.FunctionCall.Arguments.(map[string]interface{}); ok {
		argsBytes, err = json.Marshal(unescapeMapOrSlice(result))
	} else {
		argsBytes, err = json.Marshal(item.FunctionCall.Arguments)
	}

	if err != nil {
		return nil
	}
	return &dto.ToolCallResponse{
		ID:   fmt.Sprintf("call_%s", common.GetUUID()),
		Type: "function",
		Function: dto.FunctionResponse{
			Arguments: string(argsBytes),
			Name:      item.FunctionCall.FunctionName,
		},
	}
}

func responseGeminiChat2OpenAI(c *gin.Context, response *dto.GeminiChatResponse) *dto.OpenAITextResponse {
	fullTextResponse := dto.OpenAITextResponse{
		Id:      helper.GetResponseID(c),
		Object:  "chat.completion",
		Created: common.GetTimestamp(),
		Choices: make([]dto.OpenAITextResponseChoice, 0, len(response.Candidates)),
	}
	isToolCall := false
	for _, candidate := range response.Candidates {
		choice := dto.OpenAITextResponseChoice{
			Index: int(candidate.Index),
			Message: dto.Message{
				Role:    "assistant",
				Content: "",
			},
			FinishReason: constant.FinishReasonStop,
		}
		if len(candidate.Content.Parts) > 0 {
			var texts []string
			var toolCalls []dto.ToolCallResponse
			for _, part := range candidate.Content.Parts {
				if part.InlineData != nil {
					if strings.HasPrefix(part.InlineData.MimeType, "image") {
						imgText := "![image](data:" + part.InlineData.MimeType + ";base64," + part.InlineData.Data + ")"
						texts = append(texts, imgText)
					} else {
						texts = append(texts, fmt.Sprintf("[media](data:%s;base64,%s)", part.InlineData.MimeType, part.InlineData.Data))
					}
				} else if part.FunctionCall != nil {
					choice.FinishReason = constant.FinishReasonToolCalls
					if call := getResponseToolCall(&part); call != nil {
						toolCalls = append(toolCalls, *call)
					}
				} else if part.Thought {
					choice.Message.ReasoningContent = part.Text
				} else if part.ExecutableCode != nil {
					texts = append(texts, "```"+part.ExecutableCode.Language+"\n"+part.ExecutableCode.Code+"\n```")
				} else if part.CodeExecutionResult != nil {
					texts = append(texts, "```output\n"+part.CodeExecutionResult.Output+"\n```")
				} else if part.Text != "\n" {
					texts = append(texts, part.Text)
				}
			}
			if len(toolCalls) > 0 {
				choice.Message.SetToolCalls(toolCalls)
				isToolCall = true
			}
			choice.Message.SetStringContent(strings.Join(texts, "\n"))
		}
		if candidate.FinishReason != nil {
			switch *candidate.FinishReason {
			case "STOP":
				choice.FinishReason = constant.FinishReasonStop
			case "MAX_TOKENS":
				choice.FinishReason = constant.FinishReasonLength
			default:
				choice.FinishReason = constant.FinishReasonContentFilter
			}
		}
		if isToolCall {
			choice.FinishReason = constant.FinishReasonToolCalls
		}

		fullTextResponse.Choices = append(fullTextResponse.Choices, choice)
	}
	return &fullTextResponse
}

func streamResponseGeminiChat2OpenAI(geminiResponse *dto.GeminiChatResponse) (*dto.ChatCompletionsStreamResponse, bool) {
	choices := make([]dto.ChatCompletionsStreamResponseChoice, 0, len(geminiResponse.Candidates))
	isStop := false
	for _, candidate := range geminiResponse.Candidates {
		if candidate.FinishReason != nil && *candidate.FinishReason == "STOP" {
			isStop = true
			candidate.FinishReason = nil
		}
		choice := dto.ChatCompletionsStreamResponseChoice{
			Index: int(candidate.Index),
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{},
		}
		var texts []string
		isTools := false
		isThought := false
		if candidate.FinishReason != nil {
			switch *candidate.FinishReason {
			case "STOP":
				choice.FinishReason = &constant.FinishReasonStop
			case "MAX_TOKENS":
				choice.FinishReason = &constant.FinishReasonLength
			default:
				choice.FinishReason = &constant.FinishReasonContentFilter
			}
		}
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil {
				if strings.HasPrefix(part.InlineData.MimeType, "image") {
					imgText := "![image](data:" + part.InlineData.MimeType + ";base64," + part.InlineData.Data + ")"
					texts = append(texts, imgText)
				}
			} else if part.FunctionCall != nil {
				isTools = true
				if call := getResponseToolCall(&part); call != nil {
					call.SetIndex(len(choice.Delta.ToolCalls))
					choice.Delta.ToolCalls = append(choice.Delta.ToolCalls, *call)
				}
			} else if part.Thought {
				isThought = true
				texts = append(texts, part.Text)
			} else if part.ExecutableCode != nil {
				texts = append(texts, "```"+part.ExecutableCode.Language+"\n"+part.ExecutableCode.Code+"\n```\n")
			} else if part.CodeExecutionResult != nil {
				texts = append(texts, "```output\n"+part.CodeExecutionResult.Output+"\n```\n")
			} else if part.Text != "\n" {
				texts = append(texts, part.Text)
			}
		}
		if isThought {
			choice.Delta.SetReasoningContent(strings.Join(texts, "\n"))
		} else {
			choice.Delta.SetContentString(strings.Join(texts, "\n"))
		}
		if isTools {
			choice.FinishReason = &constant.FinishReasonToolCalls
		}
		choices = append(choices, choice)
	}

	var response dto.ChatCompletionsStreamResponse
	response.Object = "chat.completion.chunk"
	response.Choices = choices
	return &response, isStop
}
