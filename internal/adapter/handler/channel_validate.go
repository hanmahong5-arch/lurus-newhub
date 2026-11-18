package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/gin-gonic/gin"
)

func validateChannelConfigDocuments(channel *repo.Channel) error {
	if channel.ParamOverride != nil {
		if err := relaycommon.ValidateParamOverride(*channel.ParamOverride); err != nil {
			return err
		}
	}
	if channel.HeaderOverride != nil {
		if err := relaycommon.ValidateHeaderOverride(*channel.HeaderOverride); err != nil {
			return err
		}
	}
	if channel.ModelMapping != nil {
		if err := relaycommon.ValidateModelMapping(*channel.ModelMapping); err != nil {
			return err
		}
	}
	if channel.Setting != nil {
		if err := relaycommon.ValidateChannelSetting(*channel.Setting); err != nil {
			return err
		}
	}
	return nil
}

func legacyChannelConfigErrorResponse(err error) gin.H {
	var verr *relaycommon.ChannelConfigValidationError
	if errors.As(err, &verr) {
		return gin.H{
			"success": false,
			"code":    verr.Code,
			"message": fmt.Sprintf("[%s] %s", verr.Code, verr.Message),
		}
	}
	return gin.H{"success": false, "message": err.Error()}
}

// validateChannelContent runs save-time document, model-length, and VertexAI region validations shared by v1 and v2.
func validateChannelContent(channel *repo.Channel, isAdd bool) error {
	if err := validateChannelConfigDocuments(channel); err != nil {
		return err
	}
	if isAdd {
		if channel == nil || channel.Key == "" {
			return fmt.Errorf("channel cannot be empty")
		}
		for _, m := range channel.GetModels() {
			if len(m) > 255 {
				return fmt.Errorf("模型名称过长: %s", m)
			}
		}
	}
	if channel.Type == constant.ChannelTypeVertexAi {
		if channel.Other == "" {
			return fmt.Errorf("部署地区不能为空")
		}
		regionMap, err := common.StrToMap(channel.Other)
		if err != nil {
			return fmt.Errorf("部署地区必须是标准的Json格式，例如{\"default\": \"us-central1\", \"region2\": \"us-east1\"}")
		}
		if regionMap["default"] == nil {
			return fmt.Errorf("部署地区必须包含default字段")
		}
	}
	return nil
}

func validateChannel(channel *repo.Channel, isAdd bool) error {
	if err := validateChannelContent(channel, isAdd); err != nil {
		return err
	}
	return validateChannelEgress(channel)
}

func getVertexArrayKeys(keys string) ([]string, error) {
	if keys == "" {
		return nil, nil
	}
	var keyArray []interface{}
	if err := common.Unmarshal([]byte(keys), &keyArray); err != nil {
		return nil, fmt.Errorf("批量添加 Vertex AI 必须使用标准的JsonArray格式，例如[{key1}, {key2}...]，请检查输入: %w", err)
	}
	cleanKeys := make([]string, 0, len(keyArray))
	for _, key := range keyArray {
		var keyStr string
		switch v := key.(type) {
		case string:
			keyStr = strings.TrimSpace(v)
		default:
			bytes, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("Vertex AI key JSON 编码失败: %w", err)
			}
			keyStr = string(bytes)
		}
		if keyStr != "" {
			cleanKeys = append(cleanKeys, keyStr)
		}
	}
	if len(cleanKeys) == 0 {
		return nil, fmt.Errorf("批量添加 Vertex AI 的 keys 不能为空")
	}
	return cleanKeys, nil
}
