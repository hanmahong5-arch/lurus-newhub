package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/openrouter"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// --- Conversion-fidelity diagnostics (cycle 9, L5) ---
//
// ClaudeToOpenAIRequest and GeminiToOpenAIRequest each map a fixed subset of
// the caller's wire request onto dto.GeneralOpenAIRequest. The helpers below
// report the caller-set fields that are dropped or downgraded before the
// request reaches the vendor:
//   - top-level fields the converter never reads at all (the bulk of the
//     list in claudeDroppedFields / geminiDroppedFields below);
//   - `thinking`, which is only conditionally honoured (see the branch
//     outcome check in ClaudeToOpenAIRequest);
//   - `tools`, which is only partially honoured — Claude tool entries keep
//     name/description/schema and drop type/cache_control, Gemini tool
//     entries with functionDeclarations are mapped while
//     googleSearch/googleSearchRetrieval/codeExecution/urlContext siblings
//     on the same request are reported under their own `tools.*` names;
//   - `metadata`, reported only when it carries keys other than user_id,
//     because newhub itself consumes metadata.user_id for other.end_user
//     (see the comment on that check below).
//
// This is never a static list of every field the converter doesn't map —
// only fields the CALLER actually set, so an always-present list would be
// noise on every request that used none of them. It also does not cover
// every way a request can be degraded in translation (see the residuals
// named above); doc/product-integration-guide.md §J lists them so an absent
// key is not read as a full-fidelity guarantee.

const (
	// conversionDroppedMax bounds conversion_dropped so a request that sets
	// many unmapped fields cannot grow the log row without limit.
	conversionDroppedMax = 16
	// conversionDroppedNameMax bounds a single field name — enforced by
	// boundDroppedFields and covered by
	// TestConversionDiagnostics_BoundsNameLength (convert_test.go). None of
	// the wire names emitted below come close to this today; the cap exists
	// so a future name cannot be the reason a log row grows unbounded.
	conversionDroppedNameMax = 32
	// conversionDroppedTruncated marks that the list was cut at
	// conversionDroppedMax. Published verbatim in
	// doc/product-integration-guide.md §J so a consumer can recognise the
	// marker instead of reading it as an invented field name.
	conversionDroppedTruncated = "…(truncated)"
)

// boundDroppedFields sorts, de-duplicates and caps a raw list of dropped wire
// field names to the conversion_dropped contract. Shared by both converters
// so the two wires cannot drift onto different shapes.
func boundDroppedFields(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(names))
	uniq := make([]string, 0, len(names))
	for _, n := range names {
		if n == "" {
			continue
		}
		if len(n) > conversionDroppedNameMax {
			n = n[:conversionDroppedNameMax]
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		uniq = append(uniq, n)
	}
	if len(uniq) == 0 {
		return nil
	}
	sort.Strings(uniq)
	if len(uniq) > conversionDroppedMax {
		uniq = uniq[:conversionDroppedMax-1]
		uniq = append(uniq, conversionDroppedTruncated)
	}
	return uniq
}

// claudeToolProbe reads only the two dto.Tool fields the Claude->OpenAI tool
// conversion (ClaudeToOpenAIRequest below) throws away: it keeps
// name/description/input_schema and drops everything else the caller put on
// a tool entry.
type claudeToolProbe struct {
	Type         string          `json:"type,omitempty"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

// claudeToolsLossy reports whether any incoming Claude tool carries a
// non-function `type` (e.g. Anthropic's server-side web_search) or a
// `cache_control` block — both are silently dropped by the tool conversion
// below, which only copies name/description/input_schema.
func claudeToolsLossy(toolsAny any) bool {
	if toolsAny == nil {
		return false
	}
	probes, err := common.Any2Type[[]claudeToolProbe](toolsAny)
	if err != nil {
		return false
	}
	for _, p := range probes {
		if p.Type != "" && p.Type != "function" {
			return true
		}
		if len(p.CacheControl) > 0 {
			return true
		}
	}
	return false
}

// claudeMetadataHasExtraKeys reports whether the caller's metadata object
// carries anything besides user_id. newhub itself reads metadata.user_id
// (deriveEndUserHash, provider/common/relay_info.go) and projects it into
// other.end_user, so a metadata object containing only user_id is consumed,
// not dropped — reporting it as dropped would tell a caller their end-user
// attribution is broken when it works. A malformed (non-object) metadata
// value is conservatively reported as dropped.
func claudeMetadataHasExtraKeys(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return true
	}
	for k := range m {
		if k != "user_id" {
			return true
		}
	}
	return false
}

// claudeDroppedFields returns the caller-supplied dto.ClaudeRequest field
// names ClaudeToOpenAIRequest does not map onto dto.GeneralOpenAIRequest, or
// maps only partially (tools, metadata — see claudeToolsLossy /
// claudeMetadataHasExtraKeys above). Each check fires only when the caller
// actually set the field. thinkingDropped is computed by the caller from the
// thinking branch's actual outcome (ClaudeToOpenAIRequest), not passed a
// static predicate here, because whether `thinking` survives depends on the
// channel-specific branch that already ran.
func claudeDroppedFields(r dto.ClaudeRequest, thinkingDropped bool) []string {
	var dropped []string
	if r.TopK != 0 {
		dropped = append(dropped, "top_k")
	}
	if r.ToolChoice != nil {
		dropped = append(dropped, "tool_choice")
	}
	if claudeToolsLossy(r.Tools) {
		dropped = append(dropped, "tools")
	}
	if thinkingDropped {
		dropped = append(dropped, "thinking")
	}
	if len(r.ContextManagement) > 0 {
		dropped = append(dropped, "context_management")
	}
	if len(r.OutputConfig) > 0 {
		dropped = append(dropped, "output_config")
	}
	if len(r.OutputFormat) > 0 {
		dropped = append(dropped, "output_format")
	}
	if len(r.Container) > 0 {
		dropped = append(dropped, "container")
	}
	if len(r.McpServers) > 0 {
		dropped = append(dropped, "mcp_servers")
	}
	if claudeMetadataHasExtraKeys(r.Metadata) {
		dropped = append(dropped, "metadata")
	}
	if r.ServiceTier != "" {
		dropped = append(dropped, "service_tier")
	}
	// MaxTokensToSample is the legacy v1/complete alias for max_tokens;
	// ClaudeToOpenAIRequest only ever reads MaxTokens (see the top of this
	// function), so a caller who set only the legacy field gets no max_tokens
	// on the upstream request at all.
	if r.MaxTokensToSample != 0 {
		dropped = append(dropped, "max_tokens_to_sample")
	}
	if r.Prompt != "" {
		dropped = append(dropped, "prompt")
	}
	return boundDroppedFields(dropped)
}

// geminiToolsDroppedNames returns the dotted wire names of the caller's
// Gemini tool entries the tool conversion below never reads: it only ever
// converts tool.FunctionDeclarations (per entry), so a googleSearch /
// googleSearchRetrieval / codeExecution / urlContext sibling on the same
// tools array vanishes with no diagnostic today. Reported per member present,
// independent of whether functionDeclarations is also set on the same entry.
func geminiToolsDroppedNames(r *dto.GeminiChatRequest) []string {
	var names []string
	for _, tool := range r.GetTools() {
		if tool.GoogleSearch != nil {
			names = append(names, "tools.googleSearch")
		}
		if tool.GoogleSearchRetrieval != nil {
			names = append(names, "tools.googleSearchRetrieval")
		}
		if tool.CodeExecution != nil {
			names = append(names, "tools.codeExecution")
		}
		if tool.URLContext != nil {
			names = append(names, "tools.urlContext")
		}
	}
	return names
}

// geminiDroppedFields returns the caller-supplied dto.GeminiChatRequest field
// names GeminiToOpenAIRequest does not map onto dto.GeneralOpenAIRequest, or
// maps only partially (tools — see geminiToolsDroppedNames above). Each check
// fires only when the caller actually set the field. Wire names match the
// Gemini JSON body the caller sent (camelCase), not the Go field.
func geminiDroppedFields(r *dto.GeminiChatRequest) []string {
	if r == nil {
		return nil
	}
	var dropped []string
	if len(r.Requests) > 0 {
		dropped = append(dropped, "requests")
	}
	dropped = append(dropped, geminiToolsDroppedNames(r)...)
	if len(r.SafetySettings) > 0 {
		dropped = append(dropped, "safetySettings")
	}
	if r.ToolConfig != nil {
		dropped = append(dropped, "toolConfig")
	}
	if r.CachedContent != "" {
		dropped = append(dropped, "cachedContent")
	}
	gc := r.GenerationConfig
	if gc.ResponseMimeType != "" {
		dropped = append(dropped, "responseMimeType")
	}
	if gc.ResponseSchema != nil {
		dropped = append(dropped, "responseSchema")
	}
	if len(gc.ResponseJsonSchema) > 0 {
		dropped = append(dropped, "responseJsonSchema")
	}
	if gc.PresencePenalty != nil {
		dropped = append(dropped, "presencePenalty")
	}
	if gc.FrequencyPenalty != nil {
		dropped = append(dropped, "frequencyPenalty")
	}
	if gc.ResponseLogprobs {
		dropped = append(dropped, "responseLogprobs")
	}
	if gc.Logprobs != nil {
		dropped = append(dropped, "logprobs")
	}
	if gc.MediaResolution != "" {
		dropped = append(dropped, "mediaResolution")
	}
	if gc.Seed != 0 {
		dropped = append(dropped, "seed")
	}
	if len(gc.ResponseModalities) > 0 {
		dropped = append(dropped, "responseModalities")
	}
	if gc.ThinkingConfig != nil {
		dropped = append(dropped, "thinkingConfig")
	}
	if len(gc.SpeechConfig) > 0 {
		dropped = append(dropped, "speechConfig")
	}
	if len(gc.ImageConfig) > 0 {
		dropped = append(dropped, "imageConfig")
	}
	return boundDroppedFields(dropped)
}

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

	// thinkingDropped is computed from what the branch above actually did —
	// not from info.ChannelType/isOpenRouter, which would leak upstream
	// identity into conversion_dropped (a TierPublic key). The caller set
	// thinking but the branch neither produced a Reasoning payload nor
	// changed the outgoing model name, so nothing carried the setting
	// upstream.
	thinkingDropped := claudeRequest.Thinking != nil &&
		openAIRequest.Reasoning == nil &&
		openAIRequest.Model == claudeRequest.Model

	// Convert stop sequences
	if len(claudeRequest.StopSequences) == 1 {
		openAIRequest.Stop = claudeRequest.StopSequences[0]
	} else if len(claudeRequest.StopSequences) > 1 {
		openAIRequest.Stop = claudeRequest.StopSequences
	}

	// Convert tools
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

	// Convert messages
	openAIMessages := make([]dto.Message, 0)

	// Add system message if present
	if claudeRequest.System != nil {
		if claudeRequest.IsStringSystem() && claudeRequest.GetStringSystem() != "" {
			openAIMessage := dto.Message{
				Role: "system",
			}
			openAIMessage.SetStringContent(claudeRequest.GetStringSystem())
			openAIMessages = append(openAIMessages, openAIMessage)
		} else {
			systems := claudeRequest.ParseSystem()
			if len(systems) > 0 {
				openAIMessage := dto.Message{
					Role: "system",
				}
				isOpenRouterClaude := isOpenRouter && strings.HasPrefix(info.UpstreamModelName, "anthropic/claude")
				if isOpenRouterClaude {
					systemMediaMessages := make([]dto.MediaContent, 0, len(systems))
					for _, system := range systems {
						message := dto.MediaContent{
							Type:         "text",
							Text:         system.GetText(),
							CacheControl: system.CacheControl,
						}
						systemMediaMessages = append(systemMediaMessages, message)
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
				openAIMessages = append(openAIMessages, openAIMessage)
			}
		}
	}
	for _, claudeMessage := range claudeRequest.Messages {
		openAIMessage := dto.Message{
			Role: claudeMessage.Role,
		}

		//log.Printf("claudeMessage.Content: %v", claudeMessage.Content)
		if claudeMessage.IsStringContent() {
			openAIMessage.SetStringContent(claudeMessage.GetStringContent())
		} else {
			content, err := claudeMessage.ParseContent()
			if err != nil {
				return nil, err
			}
			contents := content
			var toolCalls []dto.ToolCallRequest
			mediaMessages := make([]dto.MediaContent, 0, len(contents))

			for _, mediaMsg := range contents {
				switch mediaMsg.Type {
				case "text":
					message := dto.MediaContent{
						Type:         "text",
						Text:         mediaMsg.GetText(),
						CacheControl: mediaMsg.CacheControl,
					}
					mediaMessages = append(mediaMessages, message)
				case "image":
					// Handle image conversion (base64 to URL or keep as is)
					imageData := fmt.Sprintf("data:%s;base64,%s", mediaMsg.Source.MediaType, mediaMsg.Source.Data)
					//textContent += fmt.Sprintf("[Image: %s]", imageData)
					mediaMessage := dto.MediaContent{
						Type:     "image_url",
						ImageUrl: &dto.MessageImageUrl{Url: imageData},
					}
					mediaMessages = append(mediaMessages, mediaMessage)
				case "tool_use":
					toolCall := dto.ToolCallRequest{
						ID:   mediaMsg.Id,
						Type: "function",
						Function: dto.FunctionRequest{
							Name:      mediaMsg.Name,
							Arguments: toJSONString(mediaMsg.Input),
						},
					}
					toolCalls = append(toolCalls, toolCall)
				case "tool_result":
					// Add tool result as a separate message
					toolName := mediaMsg.Name
					if toolName == "" {
						toolName = claudeRequest.SearchToolNameByToolCallId(mediaMsg.ToolUseId)
					}
					oaiToolMessage := dto.Message{
						Role:       "tool",
						Name:       &toolName,
						ToolCallId: mediaMsg.ToolUseId,
					}
					//oaiToolMessage.SetStringContent(*mediaMsg.GetMediaContent().Text)
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

			// NOTE (fidelity limitation): when a message carries BOTH text and
			// tool_use blocks, the text is intentionally dropped here — media
			// content is only attached when there are no tool calls. OpenAI's
			// format does allow content + tool_calls together, so this loses the
			// assistant's stated intent across a tool round-trip. Kept as-is
			// because some upstreams reject content alongside tool_calls; flip
			// only after validating against the target providers. Characterized
			// by TestClaudeToOpenAIRequest_TextWithToolUse_DropsText.
			if len(mediaMessages) > 0 && len(toolCalls) == 0 {
				openAIMessage.SetMediaContent(mediaMessages)
			}
		}
		if len(openAIMessage.ParseContent()) > 0 || len(openAIMessage.ToolCalls) > 0 {
			openAIMessages = append(openAIMessages, openAIMessage)
		}
	}

	openAIRequest.Messages = openAIMessages

	info.ConversionDropped = claudeDroppedFields(claudeRequest, thinkingDropped)

	return &openAIRequest, nil
}

// claudeTerminalUsage is the ONE place an OpenAI-wire usage becomes the
// terminal Claude usage — the `message_delta` of a stream and the body of a
// non-stream response must report the same numbers for the same request.
//
// It exists because they didn't. The three call sites were three hand-written
// literals; the two streaming ones were exact copies of each other and the
// non-streaming one had been written without the cache fields, so a Claude-wire
// client saw cache_read_input_tokens=0 on every non-streamed reply while the
// request was billed at the cache discount (measured 2026-09-01: upstream
// reported 3456 cached tokens, the settlement charged 127 instead of 477, and
// the client got a zero). Adding a field to one copy is how that happened, and
// three copies means it can happen again; one function plus
// TestClaudeTerminalUsage_StreamAndNonStreamAgree means it cannot.
//
// input_tokens is presented in Anthropic-wire semantics: the three prompt
// terms are mutually exclusive (input_tokens excludes cache read and cache
// creation; Anthropic prompt-caching guide), whereas every upstream that
// reaches this function speaks a wire whose prompt_tokens INCLUDES the cached
// slice (OpenAI chat/Responses, Gemini promptTokenCount, xAI). Until 2026-09-02
// the raw prompt_tokens was copied across while cache_read_input_tokens was
// also emitted, so a Claude-wire caller applying Anthropic arithmetic counted
// the cached slice twice (a 120-prompt/50-cached event read as 170 input
// tokens). dto.Usage.AnthropicInputTokens keys the subtraction on the wire
// flag stamped at the parse site; the settlement record is not modified.
//
// Known gap, deliberately not closed here: dto.Usage also carries
// ClaudeCacheCreation5mTokens / 1hTokens and ClaudeUsage has the matching
// fields, but no upstream reachable from this conversion populates them, so
// there is no way to prove a mapping live. Shipping an unverifiable wire change
// is worse than a documented zero — revisit when a Claude-wire passthrough
// upstream is available to probe against.
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

// claudeTerminalEvents closes a Claude-wire stream — message_delta (stop_reason
// + usage) then message_stop — and marks the conversion Done. It returns nil
// and leaves Done unset while no usage is known yet. OpenAI-wire upstreams the
// relay asks for stream_options.include_usage (every channel in
// streamSupportedChannels) send usage in a trailing chunk AFTER the
// finish_reason chunk; the finish chunk therefore cannot close the message,
// and the usage chunk (or HandleFinalResponse, which fills
// ClaudeConvertInfo.Usage with the billed figures) does. Until 2026-09-02 the
// finish chunk emitted message_stop with no message_delta at all — Claude-wire
// callers on those upstreams got no stop_reason, no usage and no cache figures,
// and the usage chunk was then dropped because Done was already set.
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

// CloseClaudeStream is the end-of-stream guarantee for Claude-wire clients,
// called once by HandleFinalResponse after the last chunk is converted. If the
// terminal pair has not gone out yet — finish chunk seen without usage and no
// usage chunk followed, or no finish chunk at all — it closes any open content
// block and emits message_delta + message_stop from the usage settlement has.
// FinishReason doubles as the "stop block already sent" marker: both branches
// of StreamResponseOpenAI2Claude emit content_block_stop in the same call that
// records it.
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

func StreamResponseOpenAI2Claude(openAIResponse *dto.ChatCompletionsStreamResponse, info *relaycommon.RelayInfo) []*dto.ClaudeResponse {
	if info.ClaudeConvertInfo.Done {
		return nil
	}

	var claudeResponses []*dto.ClaudeResponse
	if info.SendResponseCount == 1 {
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
		//claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
		//	Type: "ping",
		//})
		if openAIResponse.IsToolCall() {
			info.ClaudeConvertInfo.LastMessagesType = relaycommon.LastMessageTypeTools
			var toolCall dto.ToolCallResponse
			if len(openAIResponse.Choices) > 0 && len(openAIResponse.Choices[0].Delta.ToolCalls) > 0 {
				toolCall = openAIResponse.Choices[0].Delta.ToolCalls[0]
			} else {
				first := openAIResponse.GetFirstToolCall()
				if first != nil {
					toolCall = *first
				} else {
					toolCall = dto.ToolCallResponse{}
				}
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
			// 首块包含工具 delta，则追加 input_json_delta
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
		} else {

		}
		// 判断首个响应是否存在内容（非标准的 OpenAI 响应）
		if len(openAIResponse.Choices) > 0 {
			reasoning := openAIResponse.Choices[0].Delta.GetReasoningContent()
			content := openAIResponse.Choices[0].Delta.GetContentString()

			if reasoning != "" {
				claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
					Index: &info.ClaudeConvertInfo.Index,
					Type:  "content_block_start",
					ContentBlock: &dto.ClaudeMediaMessage{
						Type:     "thinking",
						Thinking: common.GetPointer[string](""),
					},
				})
				claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
					Index: &info.ClaudeConvertInfo.Index,
					Type:  "content_block_delta",
					Delta: &dto.ClaudeMediaMessage{
						Type:     "thinking_delta",
						Thinking: &reasoning,
					},
				})
				info.ClaudeConvertInfo.LastMessagesType = relaycommon.LastMessageTypeThinking
			} else if content != "" {
				claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
					Index: &info.ClaudeConvertInfo.Index,
					Type:  "content_block_start",
					ContentBlock: &dto.ClaudeMediaMessage{
						Type: "text",
						Text: common.GetPointer[string](""),
					},
				})
				claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
					Index: &info.ClaudeConvertInfo.Index,
					Type:  "content_block_delta",
					Delta: &dto.ClaudeMediaMessage{
						Type: "text_delta",
						Text: common.GetPointer[string](content),
					},
				})
				info.ClaudeConvertInfo.LastMessagesType = relaycommon.LastMessageTypeText
			}
		}

		// 如果首块就带 finish_reason，需要立即发送停止块
		if len(openAIResponse.Choices) > 0 && openAIResponse.Choices[0].FinishReason != nil && *openAIResponse.Choices[0].FinishReason != "" {
			info.FinishReason = *openAIResponse.Choices[0].FinishReason
			claudeResponses = append(claudeResponses, generateStopBlock(info.ClaudeConvertInfo.Index))
			claudeResponses = append(claudeResponses, claudeTerminalEvents(openAIResponse, info)...)
		}
		return claudeResponses
	}

	if len(openAIResponse.Choices) == 0 {
		// No choices: a non-standard frame, or the stream_options.include_usage
		// chunk that follows the finish_reason chunk. Nothing is emitted for it
		// here; it is always the last frame, and HandleFinalResponse closes the
		// message from it via CloseClaudeStream with the billed usage.
		return claudeResponses
	} else {
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
			if reasoning != "" || textContent != "" {
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
				} else {
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
	}

	return claudeResponses
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

func toJSONString(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func GeminiToOpenAIRequest(geminiRequest *dto.GeminiChatRequest, info *relaycommon.RelayInfo) (*dto.GeneralOpenAIRequest, error) {
	openaiRequest := &dto.GeneralOpenAIRequest{
		Model:  info.UpstreamModelName,
		Stream: info.IsStream,
	}

	// 转换 messages
	var messages []dto.Message
	for _, content := range geminiRequest.Contents {
		message := dto.Message{
			Role: convertGeminiRoleToOpenAI(content.Role),
		}

		// 处理 parts
		var mediaContents []dto.MediaContent
		var toolCalls []dto.ToolCallRequest
		for _, part := range content.Parts {
			if part.Text != "" {
				mediaContent := dto.MediaContent{
					Type: "text",
					Text: part.Text,
				}
				mediaContents = append(mediaContents, mediaContent)
			} else if part.InlineData != nil {
				mediaContent := dto.MediaContent{
					Type: "image_url",
					ImageUrl: &dto.MessageImageUrl{
						Url:      fmt.Sprintf("data:%s;base64,%s", part.InlineData.MimeType, part.InlineData.Data),
						Detail:   "auto",
						MimeType: part.InlineData.MimeType,
					},
				}
				mediaContents = append(mediaContents, mediaContent)
			} else if part.FileData != nil {
				mediaContent := dto.MediaContent{
					Type: "image_url",
					ImageUrl: &dto.MessageImageUrl{
						Url:      part.FileData.FileUri,
						Detail:   "auto",
						MimeType: part.FileData.MimeType,
					},
				}
				mediaContents = append(mediaContents, mediaContent)
			} else if part.FunctionCall != nil {
				// 处理 Gemini 的工具调用
				toolCall := dto.ToolCallRequest{
					ID:   fmt.Sprintf("call_%d", len(toolCalls)+1), // 生成唯一ID
					Type: "function",
					Function: dto.FunctionRequest{
						Name:      part.FunctionCall.FunctionName,
						Arguments: toJSONString(part.FunctionCall.Arguments),
					},
				}
				toolCalls = append(toolCalls, toolCall)
			} else if part.FunctionResponse != nil {
				// 处理 Gemini 的工具响应，创建单独的 tool 消息
				toolMessage := dto.Message{
					Role:       "tool",
					ToolCallId: fmt.Sprintf("call_%d", len(toolCalls)), // 使用对应的调用ID
				}
				toolMessage.SetStringContent(toJSONString(part.FunctionResponse.Response))
				messages = append(messages, toolMessage)
			}
		}

		// 设置消息内容
		if len(toolCalls) > 0 {
			// 如果有工具调用，设置工具调用
			message.SetToolCalls(toolCalls)
		} else if len(mediaContents) == 1 && mediaContents[0].Type == "text" {
			// 如果只有一个文本内容，直接设置字符串
			message.Content = mediaContents[0].Text
		} else if len(mediaContents) > 0 {
			// 如果有多个内容或包含媒体，设置为数组
			message.SetMediaContent(mediaContents)
		}

		// 只有当消息有内容或工具调用时才添加
		if len(message.ParseContent()) > 0 || len(message.ToolCalls) > 0 {
			messages = append(messages, message)
		}
	}

	openaiRequest.Messages = messages

	if geminiRequest.GenerationConfig.Temperature != nil {
		openaiRequest.Temperature = geminiRequest.GenerationConfig.Temperature
	}
	if geminiRequest.GenerationConfig.TopP > 0 {
		openaiRequest.TopP = geminiRequest.GenerationConfig.TopP
	}
	if geminiRequest.GenerationConfig.TopK > 0 {
		openaiRequest.TopK = int(geminiRequest.GenerationConfig.TopK)
	}
	if geminiRequest.GenerationConfig.MaxOutputTokens > 0 {
		openaiRequest.MaxTokens = geminiRequest.GenerationConfig.MaxOutputTokens
	}
	// gemini stop sequences 最多 5 个，openai stop 最多 4 个。只有超过 4 个时才截断 —
	// 直接 [:4] 会在只有 1~3 个 stop 序列时切片越界 panic（crash relay handler）。
	if stops := geminiRequest.GenerationConfig.StopSequences; len(stops) > 0 {
		if len(stops) > 4 {
			stops = stops[:4]
		}
		openaiRequest.Stop = stops
	}
	if geminiRequest.GenerationConfig.CandidateCount > 0 {
		openaiRequest.N = geminiRequest.GenerationConfig.CandidateCount
	}

	// 转换工具调用
	if len(geminiRequest.GetTools()) > 0 {
		var tools []dto.ToolCallRequest
		for _, tool := range geminiRequest.GetTools() {
			if tool.FunctionDeclarations != nil {
				functionDeclarations, err := common.Any2Type[[]dto.FunctionRequest](tool.FunctionDeclarations)
				if err != nil {
					common.SysError(fmt.Sprintf("failed to parse gemini function declarations: %v (type=%T)", err, tool.FunctionDeclarations))
					continue
				}
				for _, function := range functionDeclarations {
					openAITool := dto.ToolCallRequest{
						Type: "function",
						Function: dto.FunctionRequest{
							Name:        function.Name,
							Description: function.Description,
							Parameters:  function.Parameters,
						},
					}
					tools = append(tools, openAITool)
				}
			}
		}
		if len(tools) > 0 {
			openaiRequest.Tools = tools
		}
	}

	// gemini system instructions
	if geminiRequest.SystemInstructions != nil {
		// 将系统指令作为第一条消息插入
		systemMessage := dto.Message{
			Role:    "system",
			Content: extractTextFromGeminiParts(geminiRequest.SystemInstructions.Parts),
		}
		openaiRequest.Messages = append([]dto.Message{systemMessage}, openaiRequest.Messages...)
	}

	info.ConversionDropped = geminiDroppedFields(geminiRequest)

	return openaiRequest, nil
}

func convertGeminiRoleToOpenAI(geminiRole string) string {
	switch geminiRole {
	case "user":
		return "user"
	case "model":
		return "assistant"
	case "function":
		return "function"
	default:
		return "user"
	}
}

func extractTextFromGeminiParts(parts []dto.GeminiPart) string {
	var texts []string
	for _, part := range parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// geminiUsageMetadata is the ONE place an OpenAI-wire usage becomes the
// usageMetadata a Gemini-wire client reads — the streaming and non-streaming
// converters below both call it, so they cannot drift (the same defect class
// that left non-streamed Claude replies without cache figures; see
// claudeTerminalUsage).
//
// Field semantics differ between the two wires and are normalised here:
//   - OpenAI completion_tokens INCLUDES reasoning; Gemini reports thoughts
//     separately and candidatesTokenCount EXCLUDES them.
//   - Both wires count cached tokens inside the prompt total, so
//     cachedContentTokenCount is a straight copy of cached_tokens.
//
// provider/gemini.buildUsageFromGeminiMetadata is the inverse; the round-trip
// test in that package pins the two against each other.
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

// ResponseOpenAI2Gemini 将 OpenAI 响应转换为 Gemini 格式
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

		// 设置结束原因
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

		// 转换消息内容
		content := dto.GeminiChatContent{
			Role:  "model",
			Parts: make([]dto.GeminiPart, 0),
		}

		// 处理工具调用
		toolCalls := choice.Message.ParseToolCalls()
		if len(toolCalls) > 0 {
			for _, toolCall := range toolCalls {
				// 解析参数
				var args map[string]interface{}
				if toolCall.Function.Arguments != "" {
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
						args = map[string]interface{}{"arguments": toolCall.Function.Arguments}
					}
				} else {
					args = make(map[string]interface{})
				}

				part := dto.GeminiPart{
					FunctionCall: &dto.FunctionCall{
						FunctionName: toolCall.Function.Name,
						Arguments:    args,
					},
				}
				content.Parts = append(content.Parts, part)
			}
		} else {
			// 处理文本内容
			textContent := choice.Message.StringContent()
			if textContent != "" {
				part := dto.GeminiPart{
					Text: textContent,
				}
				content.Parts = append(content.Parts, part)
			}
		}

		candidate.Content = content
		geminiResponse.Candidates = append(geminiResponse.Candidates, candidate)
	}

	return geminiResponse
}

// StreamResponseOpenAI2Gemini 将 OpenAI 流式响应转换为 Gemini 格式
// geminiFinishReason maps an OpenAI finish_reason onto Gemini's FinishReason.
func geminiFinishReason(openAIFinishReason string) string {
	switch openAIFinishReason {
	case "length":
		return "MAX_TOKENS"
	case "content_filter":
		return "SAFETY"
	default: // stop, tool_calls, anything else
		return "STOP"
	}
}

func StreamResponseOpenAI2Gemini(openAIResponse *dto.ChatCompletionsStreamResponse, info *relaycommon.RelayInfo) *dto.GeminiChatResponse {
	// 检查是否有实际内容或结束标志
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

	// Skip only the truly empty frames (OpenAI's leading role-only chunk). A
	// usage-only chunk — what stream_options.include_usage produces AFTER the
	// finish_reason chunk — is the frame that carries the real counts and
	// must reach the caller as Gemini's terminal usageMetadata. Until
	// 2026-09-02 it was dropped here, so Gemini-wire callers on every
	// OpenAI-wire upstream only ever saw the pre-request estimate.
	if !hasContent && !hasFinishReason && !hasUsage {
		return nil
	}

	geminiResponse := &dto.GeminiChatResponse{
		Candidates: make([]dto.GeminiChatCandidate, 0, len(openAIResponse.Choices)),
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:     info.GetEstimatePromptTokens(),
			CandidatesTokenCount: 0, // 流式响应中可能没有完整的 usage 信息
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

		// 设置结束原因
		if choice.FinishReason != nil {
			finishReason := geminiFinishReason(*choice.FinishReason)
			candidate.FinishReason = &finishReason
		}

		// 转换消息内容
		content := dto.GeminiChatContent{
			Role:  "model",
			Parts: make([]dto.GeminiPart, 0),
		}

		// 处理工具调用
		if choice.Delta.ToolCalls != nil {
			for _, toolCall := range choice.Delta.ToolCalls {
				// 解析参数
				var args map[string]interface{}
				if toolCall.Function.Arguments != "" {
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
						args = map[string]interface{}{"arguments": toolCall.Function.Arguments}
					}
				} else {
					args = make(map[string]interface{})
				}

				part := dto.GeminiPart{
					FunctionCall: &dto.FunctionCall{
						FunctionName: toolCall.Function.Name,
						Arguments:    args,
					},
				}
				content.Parts = append(content.Parts, part)
			}
		} else {
			// 处理文本内容
			textContent := choice.Delta.GetContentString()
			if textContent != "" {
				part := dto.GeminiPart{
					Text: textContent,
				}
				content.Parts = append(content.Parts, part)
			}
		}

		candidate.Content = content
		geminiResponse.Candidates = append(geminiResponse.Candidates, candidate)
	}

	// A usage-only chunk has no choices; Gemini's wire still expects a
	// candidate on the terminal frame, carrying the finish reason the held
	// finish chunk (openai.handleGeminiFormat) recorded.
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
