package constant

type ContextKey string

const (
	ContextKeyTokenCountMeta  ContextKey = "token_count_meta"
	ContextKeyPromptTokens    ContextKey = "prompt_tokens"
	ContextKeyEstimatedTokens ContextKey = "estimated_tokens"

	ContextKeyOriginalModel    ContextKey = "original_model"
	ContextKeyRequestStartTime ContextKey = "request_start_time"

	/* token related keys */
	ContextKeyTokenUnlimited         ContextKey = "token_unlimited_quota"
	ContextKeyTokenKey               ContextKey = "token_key"
	ContextKeyTokenId                ContextKey = "token_id"
	ContextKeyTokenGroup             ContextKey = "token_group"
	ContextKeyTokenSpecificChannelId ContextKey = "specific_channel_id"
	// ContextKeyTokenSpecificChannelRootOverride marks that the sk-<key>-<channelId>
	// override was issued by a root (cross-tenant) operator, so middleware.Distribute
	// lets it target a channel owned by any tenant. Absent/false = a mere
	// tenant-admin override, which Distribute confines to the caller's own tenant.
	ContextKeyTokenSpecificChannelRootOverride ContextKey = "specific_channel_root_override"
	ContextKeyTokenModelLimitEnabled ContextKey = "token_model_limit_enabled"
	ContextKeyTokenModelLimit        ContextKey = "token_model_limit"
	ContextKeyTokenCrossGroupRetry   ContextKey = "token_cross_group_retry"
	// ContextKeyProjectId carries the authenticated token's cost-attribution
	// project (migration 029). 0 = unassigned. Set by SetupContextForToken and
	// copied into RelayInfo.ProjectId, because the settlement path has no
	// gin.Context. A label, not an authorization claim — never gate on it.
	ContextKeyProjectId ContextKey = "project_id"

	/* channel related keys */
	ContextKeyChannelId                ContextKey = "channel_id"
	ContextKeyChannelName              ContextKey = "channel_name"
	ContextKeyChannelCreateTime        ContextKey = "channel_create_time"
	ContextKeyChannelBaseUrl           ContextKey = "base_url"
	ContextKeyChannelType              ContextKey = "channel_type"
	ContextKeyChannelSetting           ContextKey = "channel_setting"
	ContextKeyChannelOtherSetting      ContextKey = "channel_other_setting"
	ContextKeyChannelParamOverride     ContextKey = "param_override"
	ContextKeyChannelHeaderOverride    ContextKey = "header_override"
	ContextKeyChannelOrganization      ContextKey = "channel_organization"
	ContextKeyChannelAutoBan           ContextKey = "auto_ban"
	ContextKeyChannelModelMapping      ContextKey = "model_mapping"
	ContextKeyChannelStatusCodeMapping ContextKey = "status_code_mapping"
	ContextKeyChannelIsMultiKey        ContextKey = "channel_is_multi_key"
	ContextKeyChannelMultiKeyIndex     ContextKey = "channel_multi_key_index"
	ContextKeyChannelKey               ContextKey = "channel_key"

	ContextKeyAutoGroup           ContextKey = "auto_group"
	ContextKeyAutoGroupIndex      ContextKey = "auto_group_index"
	ContextKeyAutoGroupRetryIndex ContextKey = "auto_group_retry_index"

	/* user related keys */
	ContextKeyUserId      ContextKey = "id"
	ContextKeyUserSetting ContextKey = "user_setting"
	ContextKeyUserQuota   ContextKey = "user_quota"
	ContextKeyUserStatus  ContextKey = "user_status"
	ContextKeyUserEmail   ContextKey = "user_email"
	ContextKeyUserGroup   ContextKey = "user_group"
	ContextKeyUsingGroup  ContextKey = "group"
	ContextKeyUserName    ContextKey = "username"

	ContextKeyLocalCountTokens ContextKey = "local_count_tokens"

	ContextKeySystemPromptOverride ContextKey = "system_prompt_override"

	// ContextKeySessionAffinity carries the hashed conversation identifier used
	// to pin multi-turn traffic to one channel. Empty for one-shot requests.
	ContextKeySessionAffinity ContextKey = "session_affinity_key"

	// ContextKeyRouteAttempts carries the per-attempt routing trace
	// ([]app.RouteAttempt) assembled during retries and written to the log row.
	ContextKeyRouteAttempts ContextKey = "route_attempts"

	// ContextKeyRelayFormat carries the wire format (types.RelayFormat) the
	// inbound request speaks, stamped by middleware.StampRelayFormat from the
	// request path before any middleware that might reject it. handler.Relay
	// only learns this deep inside itself — too late for a middleware-stage
	// 401/402/429 to answer in the caller's own wire shape instead of always
	// answering OpenAI's, which a Claude/Gemini SDK cannot parse.
	ContextKeyRelayFormat ContextKey = "relay_format"
)
