package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider/ollama"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/gin-gonic/gin"
)

// OpenAIModel represents one upstream OpenAI-compatible `/v1/models` list item.
type OpenAIModel struct {
	ID         string         `json:"id"`
	Object     string         `json:"object"`
	Created    int64          `json:"created"`
	OwnedBy    string         `json:"owned_by"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Permission []struct {
		ID                 string `json:"id"`
		Object             string `json:"object"`
		Created            int64  `json:"created"`
		AllowCreateEngine  bool   `json:"allow_create_engine"`
		AllowSampling      bool   `json:"allow_sampling"`
		AllowLogprobs      bool   `json:"allow_logprobs"`
		AllowSearchIndices bool   `json:"allow_search_indices"`
		AllowView          bool   `json:"allow_view"`
		AllowFineTuning    bool   `json:"allow_fine_tuning"`
		Organization       string `json:"organization"`
		Group              string `json:"group"`
		IsBlocking         bool   `json:"is_blocking"`
	} `json:"permission"`
	Root   string `json:"root"`
	Parent string `json:"parent"`
}

// OpenAIModelsResponse wraps the upstream `/v1/models` data array.
type OpenAIModelsResponse struct {
	Data    []OpenAIModel `json:"data"`
	Success bool          `json:"success"`
}

func buildFetchModelsHeaders(channel *repo.Channel, key string) (http.Header, error) {
	var headers http.Header
	switch channel.Type {
	case constant.ChannelTypeAnthropic:
		headers = GetClaudeAuthHeader(key)
	default:
		headers = GetAuthHeader(key)
	}

	for k, v := range channel.GetHeaderOverride() {
		str, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("invalid header override for key %s", k)
		}
		if strings.Contains(str, "{api_key}") {
			str = strings.ReplaceAll(str, "{api_key}", key)
		}
		headers.Set(k, str)
	}
	return headers, nil
}

// FetchUpstreamModels queries the saved upstream provider for available model IDs.
func FetchUpstreamModels(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channel, err := repo.GetChannelById(id, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if enforceTenantScope(c, channel.TenantId) {
		return
	}

	baseURL := constant.ChannelBaseURLs[channel.Type]
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}

	if channel.Type == constant.ChannelTypeOllama {
		respondOllamaUpstreamModels(c, baseURL, strings.Split(channel.Key, "\n")[0])
		return
	}

	url := resolveUpstreamModelsURL(channel.Type, baseURL)
	key, _, apiErr := channel.GetNextEnabledKey()
	if apiErr != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("获取渠道密钥失败: %s", apiErr.Error())})
		return
	}
	headers, err := buildFetchModelsHeaders(channel, strings.TrimSpace(key))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	body, err := GetResponseBody("GET", url, channel, headers)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	var result OpenAIModelsResponse
	if err = json.Unmarshal(body, &result); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("解析响应失败: %s", err.Error())})
		return
	}

	var ids []string
	for _, model := range result.Data {
		modelID := model.ID
		if channel.Type == constant.ChannelTypeGemini {
			modelID = strings.TrimPrefix(modelID, "models/")
		}
		ids = append(ids, modelID)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": ids})
}

func respondOllamaUpstreamModels(c *gin.Context, baseURL, key string) {
	models, err := ollama.FetchOllamaModels(baseURL, key)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("获取Ollama模型失败: %s", err.Error())})
		return
	}
	data := make([]OpenAIModel, 0, len(models))
	for _, modelInfo := range models {
		metadata := map[string]any{}
		if modelInfo.Size > 0 {
			metadata["size"] = modelInfo.Size
		}
		if modelInfo.Digest != "" {
			metadata["digest"] = modelInfo.Digest
		}
		if modelInfo.ModifiedAt != "" {
			metadata["modified_at"] = modelInfo.ModifiedAt
		}
		d := modelInfo.Details
		if d.ParentModel != "" || d.Format != "" || d.Family != "" || len(d.Families) > 0 || d.ParameterSize != "" || d.QuantizationLevel != "" {
			metadata["details"] = modelInfo.Details
		}
		if len(metadata) == 0 {
			metadata = nil
		}
		data = append(data, OpenAIModel{
			ID:       modelInfo.Name,
			Object:   "model",
			Created:  0,
			OwnedBy:  "ollama",
			Metadata: metadata,
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func resolveUpstreamModelsURL(channelType int, baseURL string) string {
	switch channelType {
	case constant.ChannelTypeGemini:
		return fmt.Sprintf("%s/v1beta/openai/models", baseURL)
	case constant.ChannelTypeAli:
		return fmt.Sprintf("%s/compatible-mode/v1/models", baseURL)
	case constant.ChannelTypeZhipu_v4:
		if plan, ok := constant.ChannelSpecialBases[baseURL]; ok && plan.OpenAIBaseURL != "" {
			return fmt.Sprintf("%s/models", plan.OpenAIBaseURL)
		}
		return fmt.Sprintf("%s/api/paas/v4/models", baseURL)
	case constant.ChannelTypeVolcEngine:
		if plan, ok := constant.ChannelSpecialBases[baseURL]; ok && plan.OpenAIBaseURL != "" {
			return fmt.Sprintf("%s/v1/models", plan.OpenAIBaseURL)
		}
		return fmt.Sprintf("%s/v1/models", baseURL)
	case constant.ChannelTypeMoonshot:
		if plan, ok := constant.ChannelSpecialBases[baseURL]; ok && plan.OpenAIBaseURL != "" {
			return fmt.Sprintf("%s/models", plan.OpenAIBaseURL)
		}
		return fmt.Sprintf("%s/v1/models", baseURL)
	default:
		return fmt.Sprintf("%s/v1/models", baseURL)
	}
}

// FetchModels probes an unsaved candidate upstream URL + API key for model discovery after SSRF validation.
func FetchModels(c *gin.Context) {
	var req struct {
		BaseURL string `json:"base_url"`
		Type    int    `json:"type"`
		Key     string `json:"key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request"})
		return
	}

	baseURL := req.BaseURL
	if baseURL == "" {
		baseURL = constant.ChannelBaseURLs[req.Type]
	}
	if err := app.ValidateOutboundURL(baseURL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "base_url rejected: " + err.Error()})
		return
	}

	key := strings.Split(strings.TrimSpace(req.Key), "\n")[0]
	if req.Type == constant.ChannelTypeOllama {
		models, err := ollama.FetchOllamaModels(baseURL, key)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("获取Ollama模型失败: %s", err.Error())})
			return
		}
		names := make([]string, 0, len(models))
		for _, m := range models {
			names = append(names, m.Name)
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": names})
		return
	}

	client := &http.Client{Timeout: 120 * time.Second}
	request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, fmt.Sprintf("%s/v1/models", baseURL), nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	request.Header.Set("Authorization", "Bearer "+key)

	response, err := client.Do(request)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to fetch models"})
		return
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	var models []string
	for _, model := range result.Data {
		models = append(models, model.ID)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": models})
}
