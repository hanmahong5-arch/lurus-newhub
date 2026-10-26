package bifrosteng

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/maximhq/bifrost/core/schemas"
)

// ChatConvertible reports whether req can be dispatched through Bifrost without
// silently dropping fields. Callers must fall through to legacy when false
// (pass-through channels are checked separately).
func ChatConvertible(req *dto.GeneralOpenAIRequest) bool {
	if req == nil {
		return false
	}
	for i := range req.Messages {
		if !messageConvertible(&req.Messages[i]) {
			return false
		}
	}
	for _, tool := range req.Tools {
		if tool.Type != "" && tool.Type != "function" && tool.Type != dto.CustomType {
			return false
		}
		if tool.Type == dto.CustomType && len(tool.Custom) == 0 {
			return false
		}
	}
	return true
}

func messageConvertible(m *dto.Message) bool {
	if m == nil {
		return false
	}
	if m.IsStringContent() {
		return true
	}
	for _, block := range m.ParseContent() {
		switch block.Type {
		case "text", "image_url", "input_audio", "file", "refusal":
			// mapped in toBifrostMessages
		case "video_url":
			return false
		default:
			if block.Type != "" {
				return false
			}
		}
	}
	return true
}

func toBifrostMessages(msgs []dto.Message) ([]schemas.ChatMessage, error) {
	out := make([]schemas.ChatMessage, 0, len(msgs))
	for i := range msgs {
		m := &msgs[i]
		role := schemas.ChatMessageRole(strings.ToLower(m.Role))
		switch role {
		case schemas.ChatMessageRoleUser, schemas.ChatMessageRoleAssistant,
			schemas.ChatMessageRoleSystem, schemas.ChatMessageRoleTool,
			schemas.ChatMessageRoleDeveloper:
		default:
			role = schemas.ChatMessageRoleUser
		}

		cm := schemas.ChatMessage{Role: role}
		if m.Name != nil {
			cm.Name = m.Name
		}

		content, err := toMessageContent(m)
		if err != nil {
			return nil, err
		}
		cm.Content = content

		if role == schemas.ChatMessageRoleTool && m.ToolCallId != "" {
			id := m.ToolCallId
			cm.ChatToolMessage = &schemas.ChatToolMessage{ToolCallID: &id}
		}

		if role == schemas.ChatMessageRoleAssistant {
			if toolCalls := m.ParseToolCalls(); len(toolCalls) > 0 {
				cm.ChatAssistantMessage = &schemas.ChatAssistantMessage{
					ToolCalls: toAssistantToolCalls(toolCalls),
				}
			}
			if m.ReasoningContent != "" {
				r := m.ReasoningContent
				if cm.ChatAssistantMessage == nil {
					cm.ChatAssistantMessage = &schemas.ChatAssistantMessage{}
				}
				cm.ChatAssistantMessage.Reasoning = &r
			} else if m.Reasoning != "" {
				r := m.Reasoning
				if cm.ChatAssistantMessage == nil {
					cm.ChatAssistantMessage = &schemas.ChatAssistantMessage{}
				}
				cm.ChatAssistantMessage.Reasoning = &r
			}
		}

		out = append(out, cm)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("bifrosteng: empty messages")
	}
	return out, nil
}

func toMessageContent(m *dto.Message) (*schemas.ChatMessageContent, error) {
	if m.IsStringContent() {
		s := m.StringContent()
		return &schemas.ChatMessageContent{ContentStr: schemas.Ptr(s)}, nil
	}
	blocks := m.ParseContent()
	if len(blocks) == 0 {
		// empty / null content is valid for tool-call-only assistant turns
		return nil, nil
	}
	out := make([]schemas.ChatContentBlock, 0, len(blocks))
	for _, b := range blocks {
		cb, err := toContentBlock(b)
		if err != nil {
			return nil, err
		}
		if cb != nil {
			out = append(out, *cb)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &schemas.ChatMessageContent{ContentBlocks: out}, nil
}

func toContentBlock(b dto.MediaContent) (*schemas.ChatContentBlock, error) {
	switch b.Type {
	case "text", "":
		text := b.Text
		return &schemas.ChatContentBlock{
			Type: schemas.ChatContentBlockTypeText,
			Text: schemas.Ptr(text),
		}, nil
	case "image_url":
		img := b.GetImageMedia()
		if img == nil || img.Url == "" {
			return nil, fmt.Errorf("bifrosteng: image_url missing url")
		}
		var detail *string
		if img.Detail != "" {
			d := img.Detail
			detail = &d
		}
		return &schemas.ChatContentBlock{
			Type: schemas.ChatContentBlockTypeImage,
			ImageURLStruct: &schemas.ChatInputImage{
				URL:    img.Url,
				Detail: detail,
			},
		}, nil
	case "input_audio":
		audio := b.GetInputAudio()
		if audio == nil || audio.Data == "" {
			return nil, fmt.Errorf("bifrosteng: input_audio missing data")
		}
		var format *string
		if audio.Format != "" {
			f := audio.Format
			format = &f
		}
		return &schemas.ChatContentBlock{
			Type: schemas.ChatContentBlockTypeInputAudio,
			InputAudio: &schemas.ChatInputAudio{
				Data:   audio.Data,
				Format: format,
			},
		}, nil
	case "file":
		file := toInputFile(b.File)
		if file == nil {
			return nil, fmt.Errorf("bifrosteng: file block empty")
		}
		return &schemas.ChatContentBlock{
			Type: schemas.ChatContentBlockTypeFile,
			File: file,
		}, nil
	case "refusal":
		text := b.Text
		return &schemas.ChatContentBlock{
			Type:    schemas.ChatContentBlockTypeRefusal,
			Refusal: schemas.Ptr(text),
		}, nil
	case "video_url":
		return nil, fmt.Errorf("bifrosteng: video_url not supported")
	default:
		return nil, fmt.Errorf("bifrosteng: unsupported content type %q", b.Type)
	}
}

func toInputFile(raw any) *schemas.ChatInputFile {
	if raw == nil {
		return nil
	}
	if f, ok := raw.(*schemas.ChatInputFile); ok {
		return f
	}
	m, ok := raw.(map[string]any)
	if !ok {
		// last resort: JSON round-trip
		b, err := json.Marshal(raw)
		if err != nil {
			return nil
		}
		var out schemas.ChatInputFile
		if err := json.Unmarshal(b, &out); err != nil {
			return nil
		}
		return &out
	}
	out := &schemas.ChatInputFile{}
	if v := common.Interface2String(m["file_data"]); v != "" {
		out.FileData = &v
	}
	if v := common.Interface2String(m["file_url"]); v != "" {
		out.FileURL = &v
	}
	if v := common.Interface2String(m["file_id"]); v != "" {
		out.FileID = &v
	}
	if v := common.Interface2String(m["filename"]); v != "" {
		out.Filename = &v
	}
	if v := common.Interface2String(m["file_type"]); v != "" {
		out.FileType = &v
	}
	if out.FileData == nil && out.FileURL == nil && out.FileID == nil {
		return nil
	}
	return out
}

func toAssistantToolCalls(calls []dto.ToolCallRequest) []schemas.ChatAssistantMessageToolCall {
	out := make([]schemas.ChatAssistantMessageToolCall, 0, len(calls))
	for i, tc := range calls {
		idx := uint16(i)
		typ := tc.Type
		if typ == "" {
			typ = "function"
		}
		name := tc.Function.Name
		args := tc.Function.Arguments
		id := tc.ID
		item := schemas.ChatAssistantMessageToolCall{
			Index: idx,
			Type:  &typ,
			Function: schemas.ChatAssistantMessageToolCallFunction{
				Name:      &name,
				Arguments: args,
			},
		}
		if id != "" {
			item.ID = &id
		}
		out = append(out, item)
	}
	return out
}

func toChatParams(req *dto.GeneralOpenAIRequest) *schemas.ChatParameters {
	if req == nil {
		return nil
	}
	p := &schemas.ChatParameters{}
	set := false
	if req.Temperature != nil {
		p.Temperature = req.Temperature
		set = true
	}
	if req.TopP > 0 {
		p.TopP = schemas.Ptr(req.TopP)
		set = true
	}
	if req.MaxTokens > 0 {
		mt := int(req.MaxTokens)
		p.MaxCompletionTokens = &mt
		set = true
	} else if req.MaxCompletionTokens > 0 {
		mt := int(req.MaxCompletionTokens)
		p.MaxCompletionTokens = &mt
		set = true
	}
	if req.FrequencyPenalty != 0 {
		p.FrequencyPenalty = schemas.Ptr(req.FrequencyPenalty)
		set = true
	}
	if req.PresencePenalty != 0 {
		p.PresencePenalty = schemas.Ptr(req.PresencePenalty)
		set = true
	}
	if req.User != "" {
		p.User = &req.User
		set = true
	}
	if req.Seed != 0 {
		seed := int(req.Seed)
		p.Seed = &seed
		set = true
	}
	if stops := normalizeStop(req.Stop); len(stops) > 0 {
		p.Stop = stops
		set = true
	}
	if req.ResponseFormat != nil {
		var rf interface{} = req.ResponseFormat
		p.ResponseFormat = &rf
		set = true
	}
	if tools := toChatTools(req.Tools); len(tools) > 0 {
		p.Tools = tools
		set = true
	}
	if req.ToolChoice != nil {
		if tc := toToolChoice(req.ToolChoice); tc != nil {
			p.ToolChoice = tc
			set = true
		}
	}
	if !set {
		return nil
	}
	return p
}

func normalizeStop(stop any) []string {
	switch v := stop.(type) {
	case nil:
		return nil
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func toChatTools(tools []dto.ToolCallRequest) []schemas.ChatTool {
	if len(tools) == 0 {
		return nil
	}
	// Round-trip via JSON so Parameter schemas land in Bifrost's OrderedMap fields.
	raw, err := json.Marshal(tools)
	if err != nil {
		return nil
	}
	var out []schemas.ChatTool
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func toToolChoice(raw any) *schemas.ChatToolChoice {
	if raw == nil {
		return nil
	}
	if s, ok := raw.(string); ok {
		return &schemas.ChatToolChoice{ChatToolChoiceStr: &s}
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var st schemas.ChatToolChoiceStruct
	if err := json.Unmarshal(b, &st); err != nil {
		return nil
	}
	return &schemas.ChatToolChoice{ChatToolChoiceStruct: &st}
}

func fromBifrostUsage(u *schemas.BifrostLLMUsage) *dto.Usage {
	if u == nil {
		return &dto.Usage{}
	}
	out := &dto.Usage{
		PromptTokens:              u.PromptTokens,
		CompletionTokens:          u.CompletionTokens,
		TotalTokens:               u.TotalTokens,
		PromptTokensIncludeCached: true, // Bifrost normalizes to OpenAI-wire semantics
	}
	if u.PromptTokensDetails != nil {
		out.PromptTokensDetails = dto.InputTokenDetails{
			CachedTokens: u.PromptTokensDetails.CachedTokens,
			AudioTokens:  u.PromptTokensDetails.AudioTokens,
			ImageTokens:  u.PromptTokensDetails.ImageTokens,
			TextTokens:   u.PromptTokensDetails.TextTokens,
		}
	}
	if u.CompletionTokensDetails != nil {
		out.CompletionTokenDetails = dto.OutputTokenDetails{
			AudioTokens:     u.CompletionTokensDetails.AudioTokens,
			ReasoningTokens: u.CompletionTokensDetails.ReasoningTokens,
			TextTokens:      u.CompletionTokensDetails.TextTokens,
		}
	}
	return out
}

func bifrostMessageText(msg *schemas.ChatMessage) string {
	if msg == nil || msg.Content == nil {
		return ""
	}
	if msg.Content.ContentStr != nil {
		return *msg.Content.ContentStr
	}
	var b strings.Builder
	for _, block := range msg.Content.ContentBlocks {
		if block.Text != nil {
			b.WriteString(*block.Text)
		}
	}
	return b.String()
}

func fromAssistantToolCalls(calls []schemas.ChatAssistantMessageToolCall) []dto.ToolCallResponse {
	if len(calls) == 0 {
		return nil
	}
	out := make([]dto.ToolCallResponse, 0, len(calls))
	for _, tc := range calls {
		item := dto.ToolCallResponse{
			Function: dto.FunctionResponse{
				Arguments: tc.Function.Arguments,
			},
		}
		if tc.ID != nil {
			item.ID = *tc.ID
		}
		if tc.Type != nil {
			item.Type = *tc.Type
		} else {
			item.Type = "function"
		}
		if tc.Function.Name != nil {
			item.Function.Name = *tc.Function.Name
		}
		out = append(out, item)
	}
	return out
}

func toOpenAITextResponse(resp *schemas.BifrostChatResponse, model string) *dto.TextResponse {
	out := &dto.TextResponse{
		Id:      resp.ID,
		Object:  "chat.completion",
		Created: int64(resp.Created),
		Model:   model,
		Choices: make([]dto.OpenAITextResponseChoice, 0, len(resp.Choices)),
	}
	if out.Model == "" {
		out.Model = resp.Model
	}
	for _, ch := range resp.Choices {
		choice := dto.OpenAITextResponseChoice{Index: ch.Index}
		if ch.FinishReason != nil {
			choice.FinishReason = *ch.FinishReason
		}
		if ch.ChatNonStreamResponseChoice != nil && ch.ChatNonStreamResponseChoice.Message != nil {
			msg := ch.ChatNonStreamResponseChoice.Message
			choice.Message = dto.Message{
				Role:    string(msg.Role),
				Content: bifrostMessageText(msg),
			}
			if msg.ChatAssistantMessage != nil {
				if toolCalls := fromAssistantToolCalls(msg.ChatAssistantMessage.ToolCalls); len(toolCalls) > 0 {
					choice.Message.SetToolCalls(toolCalls)
				}
				if msg.ChatAssistantMessage.Reasoning != nil {
					choice.Message.ReasoningContent = *msg.ChatAssistantMessage.Reasoning
				}
			}
		}
		out.Choices = append(out.Choices, choice)
	}
	if usage := fromBifrostUsage(resp.Usage); usage != nil {
		out.Usage = *usage
	}
	return out
}

func toStreamChunk(resp *schemas.BifrostChatResponse, model string) *dto.ChatCompletionsStreamResponse {
	out := &dto.ChatCompletionsStreamResponse{
		Id:      resp.ID,
		Object:  "chat.completion.chunk",
		Created: int64(resp.Created),
		Model:   model,
		Choices: make([]dto.ChatCompletionsStreamResponseChoice, 0, len(resp.Choices)),
	}
	if out.Model == "" {
		out.Model = resp.Model
	}
	if resp.SystemFingerprint != "" {
		fp := resp.SystemFingerprint
		out.SystemFingerprint = &fp
	}
	for _, ch := range resp.Choices {
		choice := dto.ChatCompletionsStreamResponseChoice{
			Index:        ch.Index,
			FinishReason: ch.FinishReason,
		}
		if ch.ChatStreamResponseChoice != nil && ch.ChatStreamResponseChoice.Delta != nil {
			d := ch.ChatStreamResponseChoice.Delta
			delta := dto.ChatCompletionsStreamResponseChoiceDelta{
				Content:   d.Content,
				Reasoning: d.Reasoning,
			}
			if d.Role != nil {
				delta.Role = string(*d.Role)
			}
			if len(d.ToolCalls) > 0 {
				delta.ToolCalls = fromAssistantToolCalls(d.ToolCalls)
			}
			choice.Delta = delta
		}
		out.Choices = append(out.Choices, choice)
	}
	if resp.Usage != nil {
		out.Usage = fromBifrostUsage(resp.Usage)
	}
	return out
}

func bifrostErrMessage(err *schemas.BifrostError) string {
	if err == nil {
		return "bifrost error"
	}
	if err.Error != nil && err.Error.Message != "" {
		return err.Error.Message
	}
	return "bifrost error"
}

func bifrostErrStatus(err *schemas.BifrostError) int {
	if err != nil && err.StatusCode != nil && *err.StatusCode > 0 {
		return *err.StatusCode
	}
	return 500
}
