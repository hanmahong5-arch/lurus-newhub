package model_setting

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting/config"
)

//var claudeHeadersSettings = map[string][]string{}
//
//var ClaudeThinkingAdapterEnabled = true
//var ClaudeThinkingAdapterMaxTokens = 8192
//var ClaudeThinkingAdapterBudgetTokensPercentage = 0.8

// ClaudeSettings 定义Claude模型的配置
type ClaudeSettings struct {
	HeadersSettings                       map[string]map[string][]string `json:"model_headers_settings"`
	DefaultMaxTokens                      map[string]int                 `json:"default_max_tokens"`
	ThinkingAdapterEnabled                bool                           `json:"thinking_adapter_enabled"`
	ThinkingAdapterBudgetTokensPercentage float64                        `json:"thinking_adapter_budget_tokens_percentage"`
}

// claudeFallbackMaxTokens is the value used when neither the requested model
// nor a "default" entry is present in DefaultMaxTokens — which an admin can
// produce by publishing a map without a "default" key.
const claudeFallbackMaxTokens = 8192

// 默认配置
var defaultClaudeSettings = ClaudeSettings{
	HeadersSettings:        map[string]map[string][]string{},
	ThinkingAdapterEnabled: true,
	DefaultMaxTokens: map[string]int{
		"default": claudeFallbackMaxTokens,
	},
	ThinkingAdapterBudgetTokensPercentage: 0.8,
}

// 全局实例
var claudeSettings = defaultClaudeSettings

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("claude", &claudeSettings)
}

// GetClaudeSettings 获取Claude配置。
//
// Returns the live registered object, repairing a DefaultMaxTokens map that
// arrived without a "default" entry. The repair used to write 8192 into the
// published map on every call — a write into shared state from a per-request
// read path, which is the same fatal concurrent-map exposure the reflect
// writer had. It now publishes a replacement map under the configuration write
// lock, so a map that has been handed to a reader is not written again.
func GetClaudeSettings() *ClaudeSettings {
	config.RLock()
	_, hasDefault := claudeSettings.DefaultMaxTokens["default"]
	config.RUnlock()

	if !hasDefault {
		republishClaudeDefaultMaxTokens()
	}
	return &claudeSettings
}

// republishClaudeDefaultMaxTokens installs a copy of DefaultMaxTokens that
// carries the fallback entry. It re-checks under the write lock because two
// callers can find the entry missing at the same time.
func republishClaudeDefaultMaxTokens() {
	config.Lock()
	defer config.Unlock()

	if _, ok := claudeSettings.DefaultMaxTokens["default"]; ok {
		return
	}
	repaired := make(map[string]int, len(claudeSettings.DefaultMaxTokens)+1)
	for model, maxTokens := range claudeSettings.DefaultMaxTokens {
		repaired[model] = maxTokens
	}
	repaired["default"] = claudeFallbackMaxTokens
	claudeSettings.DefaultMaxTokens = repaired
}

func (c *ClaudeSettings) WriteHeaders(originModel string, httpHeader *http.Header) {
	config.RLock()
	headers, ok := c.HeadersSettings[originModel]
	config.RUnlock()

	if ok {
		for headerKey, headerValues := range headers {
			// get existing values for this header key
			existingValues := httpHeader.Values(headerKey)
			existingValuesMap := make(map[string]bool)
			for _, v := range existingValues {
				existingValuesMap[v] = true
			}

			// add only values that don't already exist
			for _, headerValue := range headerValues {
				if !existingValuesMap[headerValue] {
					httpHeader.Add(headerKey, headerValue)
				}
			}
		}
	}
}

func (c *ClaudeSettings) GetDefaultMaxTokens(model string) int {
	config.RLock()
	defer config.RUnlock()

	if maxTokens, ok := c.DefaultMaxTokens[model]; ok {
		return maxTokens
	}
	if maxTokens, ok := c.DefaultMaxTokens["default"]; ok {
		return maxTokens
	}
	return claudeFallbackMaxTokens
}
