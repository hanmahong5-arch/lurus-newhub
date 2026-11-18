package gemini

import (
	"fmt"
	"net/http"
	"strings"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/openai"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-gonic/gin"
)

func handleStream(c *gin.Context, info *relaycommon.RelayInfo, resp *dto.ChatCompletionsStreamResponse) error {
	streamData, err := common.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to marshal stream response: %w", err)
	}
	err = openai.HandleStreamFormat(c, info, string(streamData), info.ChannelSetting.ForceFormat, info.ChannelSetting.ThinkingToContent)
	if err != nil {
		return fmt.Errorf("failed to handle stream format: %w", err)
	}
	return nil
}

func handleFinalStream(c *gin.Context, info *relaycommon.RelayInfo, resp *dto.ChatCompletionsStreamResponse) error {
	streamData, err := common.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to marshal stream response: %w", err)
	}
	openai.HandleFinalResponse(c, info, string(streamData), resp.Id, resp.Created, resp.Model, resp.GetSystemFingerprint(), resp.Usage, false)
	return nil
}

// geminiStreamHandler drives the stream scanner and returns billed usage plus
// whether the upstream finished with a terminal finishReason.
func geminiStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response, callback func(data string, geminiResponse *dto.GeminiChatResponse) bool) (*dto.Usage, bool, *types.NewAPIError) {
	var usage = &dto.Usage{}
	var imageCount int
	var streamErr *types.NewAPIError
	var sawFinish bool
	responseText := strings.Builder{}

	helper.StreamScannerHandler(c, resp, info, func(data string) bool {
		var geminiResponse dto.GeminiChatResponse
		err := common.UnmarshalJsonStr(data, &geminiResponse)
		if err != nil {
			logger.LogError(c, "error unmarshalling stream response: "+err.Error())
			streamErr = types.NewError(err, types.ErrorCodeBadResponseBody)
			return false
		}

		for _, candidate := range geminiResponse.Candidates {
			if candidate.FinishReason != nil && *candidate.FinishReason != "" {
				sawFinish = true
			}
			for _, part := range candidate.Content.Parts {
				if part.InlineData != nil && part.InlineData.MimeType != "" {
					imageCount++
				}
				if part.Text != "" {
					responseText.WriteString(part.Text)
				}
			}
		}

		if geminiResponse.UsageMetadata.TotalTokenCount != 0 {
			*usage = buildUsageFromGeminiMetadata(geminiResponse.UsageMetadata)
		}

		return callback(data, &geminiResponse)
	})

	if streamErr != nil {
		return nil, false, streamErr
	}

	if imageCount != 0 && usage.CompletionTokens == 0 {
		usage.CompletionTokens = imageCount * 1400
	}

	usage.PromptTokensDetails.TextTokens = usage.PromptTokens
	usage.CompletionTokens = geminiCompletionTokens(*usage)

	if usage.CompletionTokens <= 0 {
		str := responseText.String()
		if len(str) > 0 {
			usage = app.ResponseText2Usage(c, responseText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		} else {
			usage = &dto.Usage{}
		}
	}

	return usage, sawFinish, nil
}

func GeminiChatStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	id := helper.GetResponseID(c)
	createAt := common.GetTimestamp()
	finishReason := constant.FinishReasonStop

	usage, complete, err := geminiStreamHandler(c, info, resp, func(data string, geminiResponse *dto.GeminiChatResponse) bool {
		response, isStop := streamResponseGeminiChat2OpenAI(geminiResponse)

		response.Id = id
		response.Created = createAt
		response.Model = info.UpstreamModelName

		logger.LogDebug(c, fmt.Sprintf("info.SendResponseCount = %d", info.SendResponseCount))
		if info.SendResponseCount == 0 {
			emptyResponse := helper.GenerateStartEmptyResponse(id, createAt, info.UpstreamModelName, nil)
			if response.IsToolCall() {
				if len(emptyResponse.Choices) > 0 && len(response.Choices) > 0 {
					toolCalls := response.Choices[0].Delta.ToolCalls
					copiedToolCalls := make([]dto.ToolCallResponse, len(toolCalls))
					for idx := range toolCalls {
						copiedToolCalls[idx] = toolCalls[idx]
						copiedToolCalls[idx].Function.Arguments = ""
					}
					emptyResponse.Choices[0].Delta.ToolCalls = copiedToolCalls
				}
				finishReason = constant.FinishReasonToolCalls
				if err := handleStream(c, info, emptyResponse); err != nil {
					logger.LogError(c, err.Error())
				}
				response.ClearToolCalls()
				if response.IsFinished() {
					response.Choices[0].FinishReason = nil
				}
			} else if err := handleStream(c, info, emptyResponse); err != nil {
				logger.LogError(c, err.Error())
			}
		}

		if err := handleStream(c, info, response); err != nil {
			logger.LogError(c, err.Error())
		}
		if isStop {
			_ = handleStream(c, info, helper.GenerateStopResponse(id, createAt, info.UpstreamModelName, finishReason))
		}
		return true
	})

	if err != nil {
		return usage, err
	}

	if !complete {
		if apiErr := helper.FailoverIncompleteStream(c, info); apiErr != nil {
			return &dto.Usage{}, apiErr
		}
	}

	if !complete && helper.ClientListening(c, info) {
		apiErr := helper.SurfaceIncompleteStream(c, info.RelayFormat, info)
		if info.ClaudeConvertInfo != nil {
			info.Done = true
		}
		return &dto.Usage{}, apiErr
	}

	response := helper.GenerateFinalUsageResponse(id, createAt, info.UpstreamModelName, *usage)
	if handleErr := handleFinalStream(c, info, response); handleErr != nil {
		common.SysLog("send final response failed: " + handleErr.Error())
	}
	return usage, nil
}
