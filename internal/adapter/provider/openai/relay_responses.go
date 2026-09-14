package openai

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer app.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	err = common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	if responsesResponse.HasImageGenerationCall() {
		c.Set("image_generation_call", true)
		c.Set("image_generation_call_quality", responsesResponse.GetQuality())
		c.Set("image_generation_call_size", responsesResponse.GetSize())
	}

	// Stash the vendor-minted response id (cycle-8 L7, tasks-plugins-12) so
	// relay.ResponsesHelper's post-consume hook can pin it to the channel
	// that produced it in response_registry — read only if non-empty; an
	// empty id (a vendor that omitted it, or a parse that left the zero
	// value) must not become a registry row nobody can address later.
	if responsesResponse.ID != "" {
		c.Set("responses_id", responsesResponse.ID)
	}

	// 写入新的 response body
	app.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	// OpenAI-wire semantics: the responses API's input_tokens INCLUDES
	// input_tokens_details.cached_tokens (see dto.Usage.PromptTokensIncludeCached).
	usage := dto.Usage{PromptTokensIncludeCached: true}
	if responsesResponse.Usage != nil {
		usage.PromptTokens = responsesResponse.Usage.InputTokens
		usage.CompletionTokens = responsesResponse.Usage.OutputTokens
		usage.TotalTokens = responsesResponse.Usage.TotalTokens
		if responsesResponse.Usage.InputTokensDetails != nil {
			usage.PromptTokensDetails.CachedTokens = responsesResponse.Usage.InputTokensDetails.CachedTokens
			usage.PromptTokensDetails.CachedCreationTokens = responsesResponse.Usage.InputTokensDetails.CachedCreationTokens
		}
	}
	if info == nil || info.ResponsesUsageInfo == nil || info.ResponsesUsageInfo.BuiltInTools == nil {
		return &usage, nil
	}
	// 解析 Tools 用量
	for _, tool := range responsesResponse.Tools {
		buildToolinfo, ok := info.ResponsesUsageInfo.BuiltInTools[common.Interface2String(tool["type"])]
		if !ok || buildToolinfo == nil {
			logger.LogError(c, fmt.Sprintf("BuiltInTools not found for tool type: %v", tool["type"]))
			continue
		}
		buildToolinfo.CallCount++
	}
	return &usage, nil
}

// OaiResponsesCompactHandler handles the upstream response for POST
// /v1/responses/compact (cycle-8 L6, wire-formats-03). Unlike
// OaiResponsesHandler — which fully unmarshals the vendor's response into
// dto.OpenAIResponsesResponse, inspects it for built-in tool calls/image
// generation, and re-serves it from that struct — this endpoint's contract
// is pass-through: the caller gets the vendor's response bytes back
// unmodified, UNLESS the vendor carried a typed error in-band inside an
// HTTP 200 body (some OpenAI-compatible vendors do this), in which case the
// call is refused as an upstream error and never billed — the same guard
// OaiResponsesHandler applies via OpenAIResponsesResponse.GetOpenAIError.
// Usage is the only other field read, to bill the request. When the
// vendor's response carries no usage object (or the body fails to parse —
// forwarded regardless; a billing-estimate fallback must never block the
// caller from seeing the vendor's own bytes), PromptTokens falls back to the
// pre-request estimate and CompletionTokens to 0 — there is no way to
// reconstruct output tokens from an opaque body.
func OaiResponsesCompactHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer app.CloseResponseBodyGracefully(resp)

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	var compactResp dto.OpenAIResponsesCompactionResponse
	_ = common.Unmarshal(responseBody, &compactResp) // best-effort: only Usage/Error are read; the body is forwarded regardless of parse outcome

	if oaiError := compactResp.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		// A vendor error carried in-band inside a 200 must never be billed —
		// return before the bytes are copied to the caller, matching the
		// non-compact handler's guard above.
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	// write the vendor's bytes back to the caller verbatim — no re-marshal.
	app.IOCopyBytesGracefully(c, resp, responseBody)

	// OpenAI-wire semantics, same as OaiResponsesHandler: input_tokens
	// INCLUDES input_tokens_details.cached_tokens.
	usage := dto.Usage{PromptTokensIncludeCached: true}
	if compactResp.Usage != nil {
		usage.PromptTokens = compactResp.Usage.InputTokens
		usage.CompletionTokens = compactResp.Usage.OutputTokens
		usage.TotalTokens = compactResp.Usage.TotalTokens
		if compactResp.Usage.InputTokensDetails != nil {
			usage.PromptTokensDetails.CachedTokens = compactResp.Usage.InputTokensDetails.CachedTokens
			usage.PromptTokensDetails.CachedCreationTokens = compactResp.Usage.InputTokensDetails.CachedCreationTokens
		}
	} else {
		usage.PromptTokens = info.GetEstimatePromptTokens()
		usage.CompletionTokens = 0
		usage.TotalTokens = usage.PromptTokens
	}
	return &usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer app.CloseResponseBodyGracefully(resp)

	// OpenAI-wire semantics, same as the non-stream handler above.
	var usage = &dto.Usage{PromptTokensIncludeCached: true}
	var responseTextBuilder strings.Builder

	helper.StreamScannerHandler(c, resp, info, func(data string) bool {

		// 检查当前数据是否包含 completed 状态和 usage 信息
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err == nil {
			sendResponsesStreamData(c, streamResponse, data)
			// Stash the vendor-minted response id (cycle-8 L7,
			// tasks-plugins-12) on the FIRST event that carries one — NOT
			// only on "response.completed" (cycle-8 L7 repair round,
			// findings B-F4/A-F2): a background+stream client
			// (POST background:true + stream:true, exactly the flow GET
			// exists to serve) that reads an early event
			// (response.created/queued/in_progress, all of which already
			// carry the same id) and then loses the connection before
			// "response.completed" would otherwise leave no registry row —
			// a 404 for a response the vendor did create and did bill.
			// Every event carries the SAME id for one response, so
			// re-stashing on a later event is a harmless no-op, not a
			// correctness risk. Stream twin of OaiResponsesHandler's stash
			// above — same "non-empty only" guard.
			if streamResponse.Response != nil && streamResponse.Response.ID != "" {
				c.Set("responses_id", streamResponse.Response.ID)
			}
			switch streamResponse.Type {
			case "response.completed":
				if streamResponse.Response != nil {
					if streamResponse.Response.Usage != nil {
						if streamResponse.Response.Usage.InputTokens != 0 {
							usage.PromptTokens = streamResponse.Response.Usage.InputTokens
						}
						if streamResponse.Response.Usage.OutputTokens != 0 {
							usage.CompletionTokens = streamResponse.Response.Usage.OutputTokens
						}
						if streamResponse.Response.Usage.TotalTokens != 0 {
							usage.TotalTokens = streamResponse.Response.Usage.TotalTokens
						}
						if streamResponse.Response.Usage.InputTokensDetails != nil {
							usage.PromptTokensDetails.CachedTokens = streamResponse.Response.Usage.InputTokensDetails.CachedTokens
							usage.PromptTokensDetails.CachedCreationTokens = streamResponse.Response.Usage.InputTokensDetails.CachedCreationTokens
						}
					}
					if streamResponse.Response.HasImageGenerationCall() {
						c.Set("image_generation_call", true)
						c.Set("image_generation_call_quality", streamResponse.Response.GetQuality())
						c.Set("image_generation_call_size", streamResponse.Response.GetSize())
					}
				}
			case "response.output_text.delta":
				// 处理输出文本
				responseTextBuilder.WriteString(streamResponse.Delta)
			case dto.ResponsesOutputTypeItemDone:
				// 函数调用处理
				if streamResponse.Item != nil {
					switch streamResponse.Item.Type {
					case dto.BuildInCallWebSearchCall:
						if info != nil && info.ResponsesUsageInfo != nil && info.ResponsesUsageInfo.BuiltInTools != nil {
							if webSearchTool, exists := info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview]; exists && webSearchTool != nil {
								webSearchTool.CallCount++
							}
						}
					}
				}
			}
		} else {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
		}
		return true
	})

	if usage.CompletionTokens == 0 {
		// 计算输出文本的 token 数量
		tempStr := responseTextBuilder.String()
		if len(tempStr) > 0 {
			// 非正常结束，使用输出文本的 token 数量
			completionTokens := app.CountTextToken(tempStr, info.UpstreamModelName)
			usage.CompletionTokens = completionTokens
		}
	}

	if usage.PromptTokens == 0 && usage.CompletionTokens != 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}

	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	return usage, nil
}
