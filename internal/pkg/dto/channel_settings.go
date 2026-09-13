package dto

type ChannelSettings struct {
	ForceFormat            bool   `json:"force_format,omitempty"`
	ThinkingToContent      bool   `json:"thinking_to_content,omitempty"`
	Proxy                  string `json:"proxy"`
	PassThroughBodyEnabled bool   `json:"pass_through_body_enabled,omitempty"`
	SystemPrompt           string `json:"system_prompt,omitempty"`
	SystemPromptOverride   bool   `json:"system_prompt_override,omitempty"`
	// ForceHTTP1 is never persisted directly — there is no console field or
	// JSON key for it in this struct's own document. It is derived at relay
	// time (RelayInfo.InitChannelMeta, provider/common/relay_info.go) from the
	// channel's param_override key __lurus_force_http1:true, and read by
	// provider.doRequest to select app.GetHttpClientFor's HTTP/1.1-only
	// transport for this one channel without affecting siblings.
	ForceHTTP1 bool `json:"-"`
}

// ForceHTTP1ParamKey is the param_override key an operator sets, via the
// channel's existing param_override editor, to force that channel's outbound
// transport to HTTP/1.1 (L5). Shared here so relay_info.go's InitChannelMeta
// and any other reader of a channel's raw param_override map (e.g. the task
// FetchTask polls, which run outside a live RelayInfo) derive the same value
// from one definition instead of duplicating the key string.
const ForceHTTP1ParamKey = "__lurus_force_http1"

// ParamOverrideForceHTTP1 reads the ForceHTTP1ParamKey control key out of a
// channel's raw param_override map. Any non-bool value (missing key, wrong
// JSON type) is treated as false — a malformed override must never be
// mistaken for an explicit opt-in.
func ParamOverrideForceHTTP1(paramOverride map[string]interface{}) bool {
	v, ok := paramOverride[ForceHTTP1ParamKey].(bool)
	return ok && v
}

type VertexKeyType string

const (
	VertexKeyTypeJSON   VertexKeyType = "json"
	VertexKeyTypeAPIKey VertexKeyType = "api_key"
)

type AwsKeyType string

const (
	AwsKeyTypeAKSK   AwsKeyType = "ak_sk" // 默认
	AwsKeyTypeApiKey AwsKeyType = "api_key"
)

type ChannelOtherSettings struct {
	AzureResponsesVersion string        `json:"azure_responses_version,omitempty"`
	VertexKeyType         VertexKeyType `json:"vertex_key_type,omitempty"` // "json" or "api_key"
	OpenRouterEnterprise  *bool         `json:"openrouter_enterprise,omitempty"`
	AllowServiceTier      bool          `json:"allow_service_tier,omitempty"`      // 是否允许 service_tier 透传（默认过滤以避免额外计费）
	DisableStore          bool          `json:"disable_store,omitempty"`           // 是否禁用 store 透传（默认允许透传，禁用后可能导致 Codex 无法使用）
	AllowSafetyIdentifier bool          `json:"allow_safety_identifier,omitempty"` // 是否允许 safety_identifier 透传（默认过滤以保护用户隐私）
	AwsKeyType            AwsKeyType    `json:"aws_key_type,omitempty"`
}

func (s *ChannelOtherSettings) IsOpenRouterEnterprise() bool {
	if s == nil || s.OpenRouterEnterprise == nil {
		return false
	}
	return *s.OpenRouterEnterprise
}
