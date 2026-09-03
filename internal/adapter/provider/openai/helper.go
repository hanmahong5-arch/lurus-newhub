package openai

import (
	"encoding/json"
	"strings"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
)

// 辅助函数
func HandleStreamFormat(c *gin.Context, info *relaycommon.RelayInfo, data string, forceFormat bool, thinkToContent bool) error {
	info.SendResponseCount++

	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		return sendStreamData(c, info, data, forceFormat, thinkToContent)
	case types.RelayFormatClaude:
		return handleClaudeFormat(c, data, info)
	case types.RelayFormatGemini:
		return handleGeminiFormat(c, data, info)
	}
	return nil
}

func handleClaudeFormat(c *gin.Context, data string, info *relaycommon.RelayInfo) error {
	var streamResponse dto.ChatCompletionsStreamResponse
	if err := common.Unmarshal(common.StringToByteSlice(data), &streamResponse); err != nil {
		return err
	}

	if streamResponse.Usage != nil {
		// This usage was re-parsed off the raw chunk, so the wire flag and the
		// vendor cache remaps are missing; the converter keys the Claude-wire
		// input_tokens subtraction on that flag (dto.Usage.AnthropicInputTokens).
		// Every caller of HandleStreamFormat feeds OpenAI-wire chunks (prompt
		// includes cached), the precondition applyUsagePostProcessing states.
		applyUsagePostProcessing(info, streamResponse.Usage, common.StringToByteSlice(data))
		info.ClaudeConvertInfo.Usage = streamResponse.Usage
	}
	claudeResponses := app.StreamResponseOpenAI2Claude(&streamResponse, info)
	for _, resp := range claudeResponses {
		helper.ClaudeData(c, *resp)
	}
	return nil
}

func handleGeminiFormat(c *gin.Context, data string, info *relaycommon.RelayInfo) error {
	var streamResponse dto.ChatCompletionsStreamResponse
	if err := common.Unmarshal(common.StringToByteSlice(data), &streamResponse); err != nil {
		logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
		return err
	}

	// A content-less finish_reason chunk with no usage is held back: the
	// upstream was asked for stream_options.include_usage, so its usage chunk
	// follows, and HandleFinalResponse turns that into the single STOP frame
	// carrying the billed usageMetadata. Forwarding the finish from here gave
	// Gemini-wire callers a STOP with estimated counts, then nothing.
	if finish, ok := contentlessFinish(&streamResponse); ok && streamResponse.Usage == nil {
		info.StreamFinishReason = finish
		return nil
	}

	geminiResponse := app.StreamResponseOpenAI2Gemini(&streamResponse, info)

	// 如果返回 nil，表示没有实际内容，跳过发送
	if geminiResponse == nil {
		return nil
	}

	geminiResponseStr, err := common.Marshal(geminiResponse)
	if err != nil {
		logger.LogError(c, "failed to marshal gemini response: "+err.Error())
		return err
	}

	// send gemini format response
	c.Render(-1, &common.CustomEvent{Data: "data: " + string(geminiResponseStr)})
	_ = helper.FlushWriter(c)
	return nil
}

// contentlessFinish reports the finish_reason of a chunk that carries one and
// no content (no text, no tool call): the OpenAI finish frame.
func contentlessFinish(streamResponse *dto.ChatCompletionsStreamResponse) (string, bool) {
	finish := ""
	for _, choice := range streamResponse.Choices {
		if choice.Delta.GetContentString() != "" || len(choice.Delta.ToolCalls) > 0 {
			return "", false
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			finish = *choice.FinishReason
		}
	}
	return finish, finish != ""
}

func ProcessStreamResponse(streamResponse dto.ChatCompletionsStreamResponse, responseTextBuilder *strings.Builder, toolCount *int) error {
	for _, choice := range streamResponse.Choices {
		responseTextBuilder.WriteString(choice.Delta.GetContentString())
		responseTextBuilder.WriteString(choice.Delta.GetReasoningContent())
		if choice.Delta.ToolCalls != nil {
			if len(choice.Delta.ToolCalls) > *toolCount {
				*toolCount = len(choice.Delta.ToolCalls)
			}
			for _, tool := range choice.Delta.ToolCalls {
				responseTextBuilder.WriteString(tool.Function.Name)
				responseTextBuilder.WriteString(tool.Function.Arguments)
			}
		}
	}
	return nil
}

func processTokens(relayMode int, streamItems []string, responseTextBuilder *strings.Builder, toolCount *int) error {
	streamResp := "[" + strings.Join(streamItems, ",") + "]"

	switch relayMode {
	case relayconstant.RelayModeChatCompletions:
		return processChatCompletions(streamResp, streamItems, responseTextBuilder, toolCount)
	case relayconstant.RelayModeCompletions:
		return processCompletions(streamResp, streamItems, responseTextBuilder)
	}
	return nil
}

func processChatCompletions(streamResp string, streamItems []string, responseTextBuilder *strings.Builder, toolCount *int) error {
	var streamResponses []dto.ChatCompletionsStreamResponse
	if err := json.Unmarshal(common.StringToByteSlice(streamResp), &streamResponses); err != nil {
		// 一次性解析失败，逐个解析
		common.SysLog("error unmarshalling stream response: " + err.Error())
		for _, item := range streamItems {
			var streamResponse dto.ChatCompletionsStreamResponse
			if err := json.Unmarshal(common.StringToByteSlice(item), &streamResponse); err != nil {
				return err
			}
			if err := ProcessStreamResponse(streamResponse, responseTextBuilder, toolCount); err != nil {
				common.SysLog("error processing stream response: " + err.Error())
			}
		}
		return nil
	}

	// 批量处理所有响应
	for _, streamResponse := range streamResponses {
		for _, choice := range streamResponse.Choices {
			responseTextBuilder.WriteString(choice.Delta.GetContentString())
			responseTextBuilder.WriteString(choice.Delta.GetReasoningContent())
			if choice.Delta.ToolCalls != nil {
				if len(choice.Delta.ToolCalls) > *toolCount {
					*toolCount = len(choice.Delta.ToolCalls)
				}
				for _, tool := range choice.Delta.ToolCalls {
					responseTextBuilder.WriteString(tool.Function.Name)
					responseTextBuilder.WriteString(tool.Function.Arguments)
				}
			}
		}
	}
	return nil
}

func processCompletions(streamResp string, streamItems []string, responseTextBuilder *strings.Builder) error {
	var streamResponses []dto.CompletionsStreamResponse
	if err := json.Unmarshal(common.StringToByteSlice(streamResp), &streamResponses); err != nil {
		// 一次性解析失败，逐个解析
		common.SysLog("error unmarshalling stream response: " + err.Error())
		for _, item := range streamItems {
			var streamResponse dto.CompletionsStreamResponse
			if err := json.Unmarshal(common.StringToByteSlice(item), &streamResponse); err != nil {
				continue
			}
			for _, choice := range streamResponse.Choices {
				responseTextBuilder.WriteString(choice.Text)
			}
		}
		return nil
	}

	// 批量处理所有响应
	for _, streamResponse := range streamResponses {
		for _, choice := range streamResponse.Choices {
			responseTextBuilder.WriteString(choice.Text)
		}
	}
	return nil
}

// claudeLastChunkEvents converts the held-back last chunk of an OpenAI-wire
// stream for a Claude-wire client. Shared by the normal end
// (HandleFinalResponse, which then closes the stream) and the incomplete end
// (HandleIncompleteStream, which then sends the error frame).
func claudeLastChunkEvents(info *relaycommon.RelayInfo, lastStreamData string, usage *dto.Usage) ([]*dto.ClaudeResponse, bool) {
	var streamResponse dto.ChatCompletionsStreamResponse
	if err := common.Unmarshal(common.StringToByteSlice(lastStreamData), &streamResponse); err != nil {
		common.SysLog("error unmarshalling stream response: " + err.Error())
		return nil, false
	}

	// The last chunk is numbered like every other one: HandleStreamFormat
	// counts the chunks it converts, and the converter keys its message_start
	// branch on SendResponseCount == 1. Without this a two-chunk stream
	// (content, then the usage chunk) re-entered that branch here and sent a
	// second message_start.
	info.SendResponseCount++
	info.Usage = usage
	// When the upstream inlines usage in its final finish chunk, the
	// converter's terminal message_delta is built from THIS re-parsed usage
	// (not from the billed one above), so it needs the same wire stamp and
	// vendor remaps; see handleClaudeFormat. Idempotent on the standard
	// usage-only last chunk.
	if streamResponse.Usage != nil {
		applyUsagePostProcessing(info, streamResponse.Usage, common.StringToByteSlice(lastStreamData))
	}

	return app.StreamResponseOpenAI2Claude(&streamResponse, info), true
}

// geminiLastChunkFrame converts the held-back last chunk of an OpenAI-wire
// stream into the Gemini-wire frame, attaching the billed usage when there is
// one. Returns false when the chunk carries nothing worth a frame.
func geminiLastChunkFrame(info *relaycommon.RelayInfo, lastStreamData string, usage *dto.Usage) (string, bool) {
	var streamResponse dto.ChatCompletionsStreamResponse
	if err := common.Unmarshal(common.StringToByteSlice(lastStreamData), &streamResponse); err != nil {
		common.SysLog("error unmarshalling stream response: " + err.Error())
		return "", false
	}

	// 这里处理的是 openai 最后一个流响应，其 delta 为空，有 finish_reason 字段
	// 因此相比较于 google 官方的流响应，由 openai 转换而来会多一个 parts 为空，finishReason 为 STOP 的响应
	// 而包含最后一段文本输出的响应（倒数第二个）的 finishReason 为 null
	// 暂不知是否有程序会不兼容。

	// The terminal frame carries the billed usage — the same figures the
	// OpenAI-format final frame and the consume log get — so vendor remaps
	// (DeepSeek/Moonshot cache fields) and a usage-only last chunk both reach
	// the caller. A zeroed usage (abnormal end, not billed) is not attached:
	// nothing is invented for a stream that did not finish.
	if usage != nil && (usage.PromptTokens > 0 || usage.CompletionTokens > 0 || usage.TotalTokens > 0) {
		streamResponse.Usage = usage
	}

	geminiResponse := app.StreamResponseOpenAI2Gemini(&streamResponse, info)

	// openai 流响应开头的空数据
	if geminiResponse == nil {
		return "", false
	}

	geminiResponseStr, err := common.Marshal(geminiResponse)
	if err != nil {
		common.SysLog("error marshalling gemini response: " + err.Error())
		return "", false
	}
	return string(geminiResponseStr), true
}

// streamSawFinish reports whether any chunk of the stream carried a
// finish_reason. Only consulted for streams that ended without [DONE]; it
// walks from the end because the finish frame is the last or second-to-last
// chunk of a normal stream.
func streamSawFinish(streamItems []string) bool {
	for i := len(streamItems) - 1; i >= 0; i-- {
		var chunk dto.ChatCompletionsStreamResponse
		if err := common.Unmarshal(common.StringToByteSlice(streamItems[i]), &chunk); err != nil {
			continue
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil && *choice.FinishReason != "" && *choice.FinishReason != "null" {
				return true
			}
		}
	}
	return false
}

// HandleIncompleteStream is HandleFinalResponse's counterpart for a stream the
// upstream never finished: no [DONE] and no finish_reason. The held-back last
// chunk is still forwarded (it is real content the caller should have), then
// the caller gets its wire's in-band error instead of an invented normal end.
// Before this every wire read as a complete answer: the Claude wire got
// message_delta{stop_reason:end_turn} + message_stop, the OpenAI wire a
// zero-usage frame + [DONE], the Gemini wire a bare EOF. Nothing is billed for
// such a stream (the caller zeroes usage), so nothing is quoted either.
func HandleIncompleteStream(c *gin.Context, info *relaycommon.RelayInfo, lastStreamData string) {
	switch info.RelayFormat {
	case types.RelayFormatClaude:
		if info.ClaudeConvertInfo != nil {
			if claudeResponses, ok := claudeLastChunkEvents(info, lastStreamData, &dto.Usage{}); ok {
				for _, resp := range claudeResponses {
					_ = helper.ClaudeData(c, *resp)
				}
			}
			info.Done = true
		}
	case types.RelayFormatGemini:
		if frame, ok := geminiLastChunkFrame(info, lastStreamData, nil); ok {
			c.Render(-1, &common.CustomEvent{Data: "data: " + frame})
			_ = helper.FlushWriter(c)
		}
	}
	// OpenAI wire: the last chunk already went out through sendFinalStreamData.
	helper.StreamError(c, info.RelayFormat, helper.ReportIncompleteStream(c, info))
}

func handleLastResponse(lastStreamData string, responseId *string, createAt *int64,
	systemFingerprint *string, model *string, usage **dto.Usage,
	containStreamUsage *bool, info *relaycommon.RelayInfo,
	shouldSendLastResp *bool) error {

	var lastStreamResponse dto.ChatCompletionsStreamResponse
	if err := common.Unmarshal(common.StringToByteSlice(lastStreamData), &lastStreamResponse); err != nil {
		return err
	}

	*responseId = lastStreamResponse.Id
	*createAt = lastStreamResponse.Created
	*systemFingerprint = lastStreamResponse.GetSystemFingerprint()
	*model = lastStreamResponse.Model

	if app.ValidUsage(lastStreamResponse.Usage) {
		*containStreamUsage = true
		*usage = lastStreamResponse.Usage
		if !info.ShouldIncludeUsage {
			*shouldSendLastResp = lo.SomeBy(lastStreamResponse.Choices, func(choice dto.ChatCompletionsStreamResponseChoice) bool {
				return choice.Delta.GetContentString() != "" || choice.Delta.GetReasoningContent() != ""
			})
		}
	}

	return nil
}

func HandleFinalResponse(c *gin.Context, info *relaycommon.RelayInfo, lastStreamData string,
	responseId string, createAt int64, model string, systemFingerprint string,
	usage *dto.Usage, containStreamUsage bool) {

	// Compute perception extension for the final streaming chunk
	estimatedQuota := helper.EstimateQuotaFromUsage(info, usage)
	lurusExt := helper.ComputeLurusExtension(info, usage, estimatedQuota)

	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		if info.ShouldIncludeUsage && !containStreamUsage {
			response := helper.GenerateFinalUsageResponse(responseId, createAt, model, *usage, lurusExt)
			response.SetSystemFingerprint(systemFingerprint)
			helper.ObjectData(c, response)
		}
		helper.Done(c)

	case types.RelayFormatClaude:
		claudeResponses, ok := claudeLastChunkEvents(info, lastStreamData, usage)
		if !ok {
			return
		}
		// Whatever the last chunk was, the Claude-wire client gets exactly one
		// message_delta (stop_reason + billed usage) and one message_stop.
		claudeResponses = append(claudeResponses, app.CloseClaudeStream(info)...)
		for _, resp := range claudeResponses {
			_ = helper.ClaudeData(c, *resp)
		}
		info.ClaudeConvertInfo.Done = true

	case types.RelayFormatGemini:
		geminiResponseStr, ok := geminiLastChunkFrame(info, lastStreamData, usage)
		if !ok {
			return
		}

		// 发送最终的 Gemini 响应
		c.Render(-1, &common.CustomEvent{Data: "data: " + string(geminiResponseStr)})
		_ = helper.FlushWriter(c)
	}
}

func sendResponsesStreamData(c *gin.Context, streamResponse dto.ResponsesStreamResponse, data string) {
	if data == "" {
		return
	}
	helper.ResponseChunkData(c, streamResponse, data)
}
