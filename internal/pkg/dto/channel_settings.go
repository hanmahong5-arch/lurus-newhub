package dto

import "strings"

type ChannelSettings struct {
	ForceFormat            bool   `json:"force_format,omitempty"`
	ThinkingToContent      bool   `json:"thinking_to_content,omitempty"`
	Proxy                  string `json:"proxy"`
	PassThroughBodyEnabled bool   `json:"pass_through_body_enabled,omitempty"`
	SystemPrompt           string `json:"system_prompt,omitempty"`
	SystemPromptOverride   bool   `json:"system_prompt_override,omitempty"`
	// Region and DataCollection are operator-declared facts about where the
	// channel's upstream runs and what it retains (L8). They carry no meaning
	// on their own; a request's `provider` object (ProviderFilter) matches
	// against them at channel selection. Free text, compared case-insensitively
	// after trimming — "eu"/"us"/"cn" by convention. DataCollection is only
	// ever tested for DataCollectionDeny (zero data retention).
	Region         string `json:"region,omitempty"`
	DataCollection string `json:"data_collection,omitempty"`
	// Plan-account fields (internal/app/planquota). PlanKind declares the
	// channel is a monthly-plan account: zhipu_coding / kimi_coding / minimax;
	// ""/"none" = ordinary pay-as-you-go key, never probed. PlanThresholdPct
	// (0 = global default 95) is the used-percent at which a window parks the
	// channel until its reset. ExpiresAt is the plan end (unix seconds, 0 = never).
	// TestMode is all / auto_ban_only / none for scheduled channel tests
	// ("" = auto_ban_only for plan channels, global mode otherwise).
	PlanKind         string  `json:"plan_kind,omitempty"`
	PlanThresholdPct float64 `json:"plan_threshold_pct,omitempty"`
	ExpiresAt        int64   `json:"expires_at,omitempty"`
	TestMode         string  `json:"test_mode,omitempty"`
	// PlanMonthlyFeeCNY4 is the monthly fee of the subscription plan this
	// channel resells, in 0.0001 CNY. 0 = not a plan channel (pay-per-use);
	// the usage report only computes utilization when it is > 0.
	PlanMonthlyFeeCNY4 int64 `json:"plan_monthly_fee_cny4,omitempty"`
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

// DataCollectionDeny is the one value of ChannelSettings.DataCollection (and
// of ProviderFilter.DataCollection) that means anything: the upstream keeps
// nothing of the prompt or completion. Any other value is "no promise made".
const DataCollectionDeny = "deny"

// ProviderFilter is a request's routing constraint, parsed by the distributor
// from the body's `provider` object and carried on the gin context under
// constant.ContextKeyProviderFilter. A zero filter is no constraint. Region is
// exact-match (case-insensitive) against ChannelSettings.Region; DataCollection
// only constrains when it is DataCollectionDeny, in which case the channel must
// have declared the same — a channel that says nothing is NOT assumed
// zero-retention.
type ProviderFilter struct {
	Region         string
	DataCollection string
}

// NormalizeProviderFilter canonicalises the raw request values so the
// distributor and the channel setting compare like with like ("EU " == "eu").
func NormalizeProviderFilter(region, dataCollection string) ProviderFilter {
	return ProviderFilter{
		Region:         strings.ToLower(strings.TrimSpace(region)),
		DataCollection: strings.ToLower(strings.TrimSpace(dataCollection)),
	}
}

func (f ProviderFilter) IsZero() bool {
	return f.Region == "" && f.DataCollection != DataCollectionDeny
}

// Matches reports whether a channel with settings s may serve a request
// carrying this filter.
func (f ProviderFilter) Matches(s ChannelSettings) bool {
	if f.Region != "" && !strings.EqualFold(strings.TrimSpace(s.Region), f.Region) {
		return false
	}
	if f.DataCollection == DataCollectionDeny && !strings.EqualFold(strings.TrimSpace(s.DataCollection), DataCollectionDeny) {
		return false
	}
	return true
}

// String renders the constraining parts only, in the form the wire error
// sentence uses: "region=eu, data_collection=deny".
func (f ProviderFilter) String() string {
	parts := make([]string, 0, 2)
	if f.Region != "" {
		parts = append(parts, "region="+f.Region)
	}
	if f.DataCollection == DataCollectionDeny {
		parts = append(parts, "data_collection="+DataCollectionDeny)
	}
	return strings.Join(parts, ", ")
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
