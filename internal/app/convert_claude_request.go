package app

import (
	"encoding/json"
	"fmt"
	"strings"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/openrouter"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func ClaudeToOpenAIRequest(claudeRequest dto.ClaudeRequest, info *relaycommon.RelayInfo) (*dto.GeneralOpenAIRequest, error) {
	openAIRequest := dto.GeneralOpenAIRequest{
		Model:       claudeRequest.Model,
		MaxTokens:   claudeRequest.MaxTokens,
		Temperature: claudeRequest.Temperature,
		TopP:        claudeRequest.TopP,
		Stream:      claudeRequest.Stream,
	}

	isOpenRouter := info.ChannelType == constant.ChannelTypeOpenRouter

	if claudeRequest.Thinking != nil && claudeRequest.Thinking.Type == "enabled" {
		if isOpenRouter {
			reasoning := openrouter.RequestReasoning{
				MaxTokens: claudeRequest.Thinking.GetBudgetTokens(),
			}
			reasoningJSON, err := json.Marshal(reasoning)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal reasoning: %w", err)
			}
			openAIRequest.Reasoning = reasoningJSON
		} else {
			thinkingSuffix := "-thinking"
			if strings.HasSuffix(info.OriginModelName, thinkingSuffix) &&
				!strings.HasSuffix(openAIRequest.Model, thinkingSuffix) {
				openAIRequest.Model = openAIRequest.Model + thinkingSuffix
			}
		}
	}

	thinkingDropped := claudeRequest.Thinking != nil &&
		openAIRequest.Reasoning == nil &&
		openAIRequest.Model == claudeRequest.Model

	if len(claudeRequest.StopSequences) == 1 {
		openAIRequest.Stop = claudeRequest.StopSequences[0]
	} else if len(claudeRequest.StopSequences) > 1 {
		openAIRequest.Stop = claudeRequest.StopSequences
	}

	tools, _ := common.Any2Type[[]dto.Tool](claudeRequest.Tools)
	openAITools := make([]dto.ToolCallRequest, 0)
	for _, claudeTool := range tools {
		openAITool := dto.ToolCallRequest{
			Type: "function",
			Function: dto.FunctionRequest{
				Name:        claudeTool.Name,
				Description: claudeTool.Description,
				Parameters:  claudeTool.InputSchema,
			},
		}
		openAITools = append(openAITools, openAITool)
	}
	openAIRequest.Tools = openAITools

	openAIMessages := appendClaudeSystemMessages(nil, claudeRequest, isOpenRouter, info.UpstreamModelName)

	for _, claudeMessage := range claudeRequest.Messages {
		var err error
		openAIMessages, err = appendClaudeConversationMessage(openAIMessages, claudeRequest, claudeMessage)
		if err != nil {
			return nil, err
		}
	}

	openAIRequest.Messages = openAIMessages
	info.ConversionDropped = claudeDroppedFields(claudeRequest, thinkingDropped)

	return &openAIRequest, nil
}

func appendClaudeSystemMessages(openAIMessages []dto.Message, claudeRequest dto.ClaudeRequest, isOpenRouter bool, upstreamModelName string) []dto.Message {
	if claudeRequest.System == nil {
		return openAIMessages
	}
	if claudeRequest.IsStringSystem() && claudeRequest.GetStringSystem() != "" {
		openAIMessage := dto.Message{Role: "system"}
		openAIMessage.SetStringContent(claudeRequest.GetStringSystem())
		return append(openAIMessages, openAIMessage)
	}
	systems := claudeRequest.ParseSystem()
	if len(systems) == 0 {
		return openAIMessages
	}
	openAIMessage := dto.Message{Role: "system"}
	isOpenRouterClaude := isOpenRouter && strings.HasPrefix(upstreamModelName, "anthropic/claude")
	if isOpenRouterClaude {
		systemMediaMessages := make([]dto.MediaContent, 0, len(systems))
		for _, system := range systems {
			systemMediaMessages = append(systemMediaMessages, dto.MediaContent{
				Type:         "text",
				Text:         system.GetText(),
				CacheControl: system.CacheControl,
			})
		}
		openAIMessage.SetMediaContent(systemMediaMessages)
	} else {
		systemStr := ""
		for _, system := range systems {
			if system.Text != nil {
				systemStr += *system.Text
			}
		}
		openAIMessage.SetStringContent(systemStr)
	}
	return append(openAIMessages, openAIMessage)
}

func appendClaudeConversationMessage(openAIMessages []dto.Message, claudeRequest dto.ClaudeRequest, claudeMessage dto.ClaudeMessage) ([]dto.Message, error) {
	openAIMessage := dto.Message{
		Role: claudeMessage.Role,
	}

	if claudeMessage.IsStringContent() {
		openAIMessage.SetStringContent(claudeMessage.GetStringContent())
	} else {
		contents, err := claudeMessage.ParseContent()
		if err != nil {
			return nil, err
		}
		var toolCalls []dto.ToolCallRequest
		mediaMessages := make([]dto.MediaContent, 0, len(contents))

		for _, mediaMsg := range contents {
			switch mediaMsg.Type {
			case "text":
				mediaMessages = append(mediaMessages, dto.MediaContent{
					Type:         "text",
					Text:         mediaMsg.GetText(),
					CacheControl: mediaMsg.CacheControl,
				})
			case "image":
				imageData := fmt.Sprintf("data:%s;base64,%s", mediaMsg.Source.MediaType, mediaMsg.Source.Data)
				mediaMessages = append(mediaMessages, dto.MediaContent{
					Type:     "image_url",
					ImageUrl: &dto.MessageImageUrl{Url: imageData},
				})
			case "tool_use":
				toolCalls = append(toolCalls, dto.ToolCallRequest{
					ID:   mediaMsg.Id,
					Type: "function",
					Function: dto.FunctionRequest{
						Name:      mediaMsg.Name,
						Arguments: toJSONString(mediaMsg.Input),
					},
				})
			case "tool_result":
				toolName := mediaMsg.Name
				if toolName == "" {
					toolName = claudeRequest.SearchToolNameByToolCallId(mediaMsg.ToolUseId)
				}
				oaiToolMessage := dto.Message{
					Role:       "tool",
					Name:       &toolName,
					ToolCallId: mediaMsg.ToolUseId,
				}
				if mediaMsg.IsStringContent() {
					oaiToolMessage.SetStringContent(mediaMsg.GetStringContent())
				} else {
					mediaContents := mediaMsg.ParseMediaContent()
					encodeJson, _ := common.Marshal(mediaContents)
					oaiToolMessage.SetStringContent(string(encodeJson))
				}
				openAIMessages = append(openAIMessages, oaiToolMessage)
			}
		}

		if len(toolCalls) > 0 {
			openAIMessage.SetToolCalls(toolCalls)
		}
		if len(mediaMessages) > 0 && len(toolCalls) == 0 {
			openAIMessage.SetMediaContent(mediaMessages)
		}
	}

	if len(openAIMessage.ParseContent()) > 0 || len(openAIMessage.ToolCalls) > 0 {
		openAIMessages = append(openAIMessages, openAIMessage)
	}
	return openAIMessages, nil
}
