package app

import (
	"encoding/json"
	"sort"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

const (
	conversionDroppedMax       = 16
	conversionDroppedNameMax   = 32
	conversionDroppedTruncated = "…(truncated)"
)

// boundDroppedFields sorts, de-duplicates and caps a raw list of dropped wire
// field names to the conversion_dropped contract.
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

type claudeToolProbe struct {
	Type         string          `json:"type,omitempty"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

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
	if r.MaxTokensToSample != 0 {
		dropped = append(dropped, "max_tokens_to_sample")
	}
	if r.Prompt != "" {
		dropped = append(dropped, "prompt")
	}
	return boundDroppedFields(dropped)
}

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

func toJSONString(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
