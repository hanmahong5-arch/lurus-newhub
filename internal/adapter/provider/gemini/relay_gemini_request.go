package gemini

import (
	"strings"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/model_setting"
	"github.com/gin-gonic/gin"
)

var geminiSupportedMimeTypes = map[string]bool{
	"application/pdf": true,
	"audio/mpeg":      true,
	"audio/mp3":       true,
	"audio/wav":       true,
	"image/png":       true,
	"image/jpeg":      true,
	"image/webp":      true,
	"text/plain":      true,
	"video/mov":       true,
	"video/mpeg":      true,
	"video/mp4":       true,
	"video/mpg":       true,
	"video/avi":       true,
	"video/wmv":       true,
	"video/mpegps":    true,
	"video/flv":       true,
}

const thoughtSignatureBypassValue = "context_engineering_is_the_way_to_go"

func CovertOpenAI2Gemini(c *gin.Context, textRequest dto.GeneralOpenAIRequest, info *relaycommon.RelayInfo) (*dto.GeminiChatRequest, error) {
	geminiRequest := dto.GeminiChatRequest{
		Contents: make([]dto.GeminiChatContent, 0, len(textRequest.Messages)),
		GenerationConfig: dto.GeminiChatGenerationConfig{
			Temperature:     textRequest.Temperature,
			TopP:            textRequest.TopP,
			MaxOutputTokens: textRequest.GetMaxTokens(),
			Seed:            int64(textRequest.Seed),
		},
	}

	attachThoughtSignature := (info.ChannelType == constant.ChannelTypeGemini ||
		info.ChannelType == constant.ChannelTypeVertexAi) &&
		model_setting.GetGeminiSettings().FunctionCallThoughtSignatureEnabled

	if model_setting.IsGeminiModelSupportImagine(info.UpstreamModelName) {
		geminiRequest.GenerationConfig.ResponseModalities = []string{
			"TEXT",
			"IMAGE",
		}
	}

	adaptorWithExtraBody, err := applyGeminiExtraBody(&geminiRequest, textRequest.ExtraBody, info.UpstreamModelName)
	if err != nil {
		return nil, err
	}
	if !adaptorWithExtraBody {
		ThinkingAdaptor(&geminiRequest, info, textRequest)
	}

	safetySettings := make([]dto.GeminiChatSafetySettings, 0, len(SafetySettingList))
	for _, category := range SafetySettingList {
		safetySettings = append(safetySettings, dto.GeminiChatSafetySettings{
			Category:  category,
			Threshold: model_setting.GetGeminiSafetySetting(category),
		})
	}
	geminiRequest.SafetySettings = safetySettings

	applyGeminiToolsAndFormat(&geminiRequest, textRequest)

	toolCallIDs := make(map[string]string)
	var systemContent []string
	for _, message := range textRequest.Messages {
		if message.Role == "system" || message.Role == "developer" {
			systemContent = append(systemContent, message.StringContent())
			continue
		}
		if message.Role == "tool" || message.Role == "function" {
			appendToolFunctionResponse(&geminiRequest, message, toolCallIDs)
			continue
		}
		content, hasParts, convErr := convertOpenAIMessageToGemini(c, message, attachThoughtSignature, toolCallIDs)
		if convErr != nil {
			return nil, convErr
		}
		if hasParts {
			geminiRequest.Contents = append(geminiRequest.Contents, content)
		}
	}

	if len(systemContent) > 0 {
		geminiRequest.SystemInstructions = &dto.GeminiChatContent{
			Parts: []dto.GeminiPart{
				{
					Text: strings.Join(systemContent, "\n"),
				},
			},
		}
	}

	return &geminiRequest, nil
}

func hasFunctionCallContent(call *dto.FunctionCall) bool {
	if call == nil {
		return false
	}
	if strings.TrimSpace(call.FunctionName) != "" {
		return true
	}

	switch v := call.Arguments.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(v) != ""
	case map[string]interface{}:
		return len(v) > 0
	case []interface{}:
		return len(v) > 0
	default:
		return true
	}
}

func getSupportedMimeTypesList() []string {
	keys := make([]string, 0, len(geminiSupportedMimeTypes))
	for k := range geminiSupportedMimeTypes {
		keys = append(keys, k)
	}
	return keys
}
