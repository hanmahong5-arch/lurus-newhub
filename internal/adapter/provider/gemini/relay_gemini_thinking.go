package gemini

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/model_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/reasoning"
)

const (
	pro25MinBudget       = 128
	pro25MaxBudget       = 32768
	flash25MaxBudget     = 24576
	flash25LiteMinBudget = 512
	flash25LiteMaxBudget = 24576
)

func isNew25ProModel(modelName string) bool {
	return strings.HasPrefix(modelName, "gemini-2.5-pro") &&
		!strings.HasPrefix(modelName, "gemini-2.5-pro-preview-05-06") &&
		!strings.HasPrefix(modelName, "gemini-2.5-pro-preview-03-25")
}

func is25FlashLiteModel(modelName string) bool {
	return strings.HasPrefix(modelName, "gemini-2.5-flash-lite")
}

func clampThinkingBudget(modelName string, budget int) int {
	isNew25Pro := isNew25ProModel(modelName)
	is25FlashLite := is25FlashLiteModel(modelName)

	if is25FlashLite {
		if budget < flash25LiteMinBudget {
			return flash25LiteMinBudget
		}
		if budget > flash25LiteMaxBudget {
			return flash25LiteMaxBudget
		}
	} else if isNew25Pro {
		if budget < pro25MinBudget {
			return pro25MinBudget
		}
		if budget > pro25MaxBudget {
			return pro25MaxBudget
		}
	} else {
		if budget < 0 {
			return 0
		}
		if budget > flash25MaxBudget {
			return flash25MaxBudget
		}
	}
	return budget
}

func clampThinkingBudgetByEffort(modelName string, effort string) int {
	isNew25Pro := isNew25ProModel(modelName)
	is25FlashLite := is25FlashLiteModel(modelName)

	maxBudget := 0
	if is25FlashLite {
		maxBudget = flash25LiteMaxBudget
	}
	if isNew25Pro {
		maxBudget = pro25MaxBudget
	} else {
		maxBudget = flash25MaxBudget
	}
	switch effort {
	case "high":
		maxBudget = maxBudget * 80 / 100
	case "medium":
		maxBudget = maxBudget * 50 / 100
	case "low":
		maxBudget = maxBudget * 20 / 100
	case "minimal":
		maxBudget = maxBudget * 5 / 100
	}
	return clampThinkingBudget(modelName, maxBudget)
}

func ThinkingAdaptor(geminiRequest *dto.GeminiChatRequest, info *relaycommon.RelayInfo, oaiRequest ...dto.GeneralOpenAIRequest) {
	if !model_setting.GetGeminiSettings().ThinkingAdapterEnabled {
		return
	}
	modelName := info.UpstreamModelName
	isNew25Pro := isNew25ProModel(modelName)

	if strings.Contains(modelName, "-thinking-") {
		parts := strings.SplitN(modelName, "-thinking-", 2)
		if len(parts) == 2 && parts[1] != "" {
			if budgetTokens, err := strconv.Atoi(parts[1]); err == nil {
				clampedBudget := clampThinkingBudget(modelName, budgetTokens)
				geminiRequest.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{
					ThinkingBudget:  common.GetPointer(clampedBudget),
					IncludeThoughts: true,
				}
			}
		}
	} else if strings.HasSuffix(modelName, "-thinking") {
		unsupportedModels := []string{
			"gemini-2.5-pro-preview-05-06",
			"gemini-2.5-pro-preview-03-25",
		}
		isUnsupported := false
		for _, unsupportedModel := range unsupportedModels {
			if strings.HasPrefix(modelName, unsupportedModel) {
				isUnsupported = true
				break
			}
		}

		geminiRequest.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{
			IncludeThoughts: true,
		}
		if !isUnsupported {
			if geminiRequest.GenerationConfig.MaxOutputTokens > 0 {
				budgetTokens := model_setting.GetGeminiSettings().ThinkingAdapterBudgetTokensPercentage * float64(geminiRequest.GenerationConfig.MaxOutputTokens)
				clampedBudget := clampThinkingBudget(modelName, int(budgetTokens))
				geminiRequest.GenerationConfig.ThinkingConfig.ThinkingBudget = common.GetPointer(clampedBudget)
			} else if len(oaiRequest) > 0 {
				geminiRequest.GenerationConfig.ThinkingConfig.ThinkingBudget = common.GetPointer(clampThinkingBudgetByEffort(modelName, oaiRequest[0].ReasoningEffort))
			}
		}
	} else if strings.HasSuffix(modelName, "-nothinking") {
		if !isNew25Pro {
			geminiRequest.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{
				ThinkingBudget: common.GetPointer(0),
			}
		}
	} else if _, level, ok := reasoning.TrimEffortSuffix(info.UpstreamModelName); ok && level != "" {
		geminiRequest.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{
			IncludeThoughts: true,
			ThinkingLevel:   level,
		}
		info.ReasoningEffort = level
	}
}

func applyGeminiExtraBody(geminiRequest *dto.GeminiChatRequest, extraRaw []byte, upstreamModelName string) (bool, error) {
	if len(extraRaw) == 0 || strings.HasSuffix(upstreamModelName, "-nothinking") {
		return false, nil
	}
	var extraBody map[string]interface{}
	if err := common.Unmarshal(extraRaw, &extraBody); err != nil {
		return false, fmt.Errorf("invalid extra body: %w", err)
	}
	googleBody, ok := extraBody["google"].(map[string]interface{})
	if !ok {
		return false, nil
	}
	if _, hasErrorParam := googleBody["thinkingConfig"]; hasErrorParam {
		return false, errors.New("extra_body.google.thinkingConfig is not supported, use extra_body.google.thinking_config instead")
	}
	if thinkingConfig, ok := googleBody["thinking_config"].(map[string]interface{}); ok {
		if _, hasErrorParam := thinkingConfig["thinkingBudget"]; hasErrorParam {
			return false, errors.New("extra_body.google.thinking_config.thinkingBudget is not supported, use extra_body.google.thinking_config.thinking_budget instead")
		}
		if budget, ok := thinkingConfig["thinking_budget"].(float64); ok {
			budgetInt := int(budget)
			geminiRequest.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{
				ThinkingBudget:  common.GetPointer(budgetInt),
				IncludeThoughts: true,
			}
		} else {
			geminiRequest.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{
				IncludeThoughts: true,
			}
		}
	}
	if _, hasErrorParam := googleBody["imageConfig"]; hasErrorParam {
		return false, errors.New("extra_body.google.imageConfig is not supported, use extra_body.google.image_config instead")
	}
	if imageConfig, ok := googleBody["image_config"].(map[string]interface{}); ok {
		if _, hasErrorParam := imageConfig["aspectRatio"]; hasErrorParam {
			return false, errors.New("extra_body.google.image_config.aspectRatio is not supported, use extra_body.google.image_config.aspect_ratio instead")
		}
		if _, hasErrorParam := imageConfig["imageSize"]; hasErrorParam {
			return false, errors.New("extra_body.google.image_config.imageSize is not supported, use extra_body.google.image_config.image_size instead")
		}
		geminiImageConfig := make(map[string]interface{})
		if aspectRatio, ok := imageConfig["aspect_ratio"]; ok {
			geminiImageConfig["aspectRatio"] = aspectRatio
		}
		if imageSize, ok := imageConfig["image_size"]; ok {
			geminiImageConfig["imageSize"] = imageSize
		}
		if len(geminiImageConfig) > 0 {
			imageConfigBytes, err := common.Marshal(geminiImageConfig)
			if err != nil {
				return false, fmt.Errorf("failed to marshal image_config: %w", err)
			}
			geminiRequest.GenerationConfig.ImageConfig = imageConfigBytes
		}
	}
	return true, nil
}
