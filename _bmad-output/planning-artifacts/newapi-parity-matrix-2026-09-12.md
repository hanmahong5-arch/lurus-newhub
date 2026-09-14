# New API parity matrix — 2026-09-12

Upstream HEAD: `be36cbb8` (2026-09-12). newhub HEAD: `2011f968`.

Status definitions:
- **equivalent** — same capability, same observable behaviour.
- **better** — capability present and strictly exceeds upstream on some verifiable axis (e.g. added hardening, extra field, extra test).
- **partial** — capability partly present; a documented sub-feature or mode is missing.
- **missing** — no counterpart found in newhub (grep/route/entity absent).
- **not_applicable** — architecturally inapplicable given newhub's delegated-identity / PG-only / stateless-pod / single-tenant-vs-multi-tenant design; not simply unbuilt.

Rows marked `[skeptic flipped X→Y: ...]` in the note column had their status changed during adversarial re-verification; the marker is preserved verbatim.

> **Correction (2026-09-13, async-tasks re-run):** the `tasks-plugins` rows whose evidence reads "grep TaskPlugin → 0 hits" are wrong about the upstream side — upstream HEAD be36cbb8 ships a JS task-plugin protocol (86 files: `/v1/tasks/:key` submit/status/artifacts, signed artefact capability URLs, SSRF allow-lists, per-plugin billing expressions). The newhub-side status of those rows (missing) is re-verified and stands; the JS plugin runtime is out of scope by design, while the generic task surface and artefact listing are planned as cycle-8 lanes L8–L10. Upstream's artefact *store* is itself still a disabled stub (`service/task_artifact_store.go`), so row tasks-plugins-34 is a shared gap, not a newhub deficit.

## Totals (342 rows checked across 10 domains)

| Status | Count |
|---|---|
| equivalent | 154 |
| better | 19 |
| partial | 44 |
| missing | 101 |
| not_applicable | 24 |
| **Total** | **342** |

Skeptic flips applied: 11. Rows independently re-verified: 174.

---

## Domain: Relay wire formats, conversions and request features (`wire-formats`)

| id | feature | status | value | effort | migration | newhub evidence / note |
|---|---|---|---|---|---|---|
| wire-formats-01 | Chat Completions endpoint | equivalent | 5 | S | no | internal/adapter/handler/router/relay-router.go:145 (POST /v1/chat/completions -> handler.Relay RelayFormatOpenAI); internal/app/relay/compatible_handler.go:1 — same wire, relayed through Relay()/compatible_handler |
| wire-formats-02 | Claude Messages endpoint | equivalent | 5 | S | no | internal/adapter/handler/router/relay-router.go:139-142 (POST /v1/messages -> RelayFormatClaude); internal/app/relay/claude_handler.go:1 — native Claude Messages format supported end to end |
| wire-formats-03 | Responses Compact endpoint | missing | 4 | L | no | grep -rn 'compact' internal/ -i (excl _test) -> 0 relevant hits; grep -rn 'responses/compact\|ResponsesCompact\|responses_compaction' -> 0 hits; relay-router.go:150-151 only registers POST /responses, no /compact variant — newhub has /v1/responses but no compaction/chaining variant; no OpenAIResponsesCompactionRequest DTO |
| wire-formats-04 | Gemini native API endpoint | equivalent | 4 | S | no | internal/adapter/handler/router/relay-router.go:41-49 (/v1beta/models group); internal/app/relay/gemini_handler.go:1 — native Gemini pass-through + conversion present |
| wire-formats-05 | Realtime WebSocket API | equivalent | 4 | S | no | internal/adapter/handler/router/relay-router.go:118-123 (GET /realtime -> RelayFormatOpenAIRealtime); internal/adapter/handler/relay.go:37 imports gorilla/websocket — WS realtime route wired through same Relay() dispatcher with full governance chain |
| wire-formats-06 | Audio processing endpoints | equivalent | 3 | S | no | relay-router.go:169-177 (/audio/transcriptions,/translations,/speech); internal/pkg/dto/audio.go:1 — all three audio routes present |
| wire-formats-07 | Embeddings endpoint | equivalent | 4 | S | no | relay-router.go:164-166 (POST /v1/embeddings); internal/app/relay/embedding_handler.go:1 — present |
| wire-formats-08 | Images generation and editing | equivalent | 3 | S | no | relay-router.go:154-162 (/edits, /images/generations, /images/edits); internal/app/relay/image_handler.go:1 — generations+edits present; /images/variations explicitly RelayNotImplemented (matches upstream stub) |
| wire-formats-09 | Reranking endpoint | equivalent | 2 | S | no | relay-router.go:180-183 (POST /rerank); internal/app/relay/rerank_handler.go:1; internal/pkg/dto/rerank.go:1 — present |
| wire-formats-10 | Playground mode | better | 2 | S | no | relay-router.go:62-85 (playgroundRouter POST /pg/chat/completions); internal/adapter/handler/playground.go:1 — same endpoint wrapped with full tenant governance chain (PoolBalanceCheck, CostSpikeLimit, EntitlementCheck, rate limits, concurrency cap) that upstream's playground lacks entirely |
| wire-formats-11 | Stream Options with include_usage | equivalent | 4 | S | no | internal/pkg/dto/openai_request.go:259 (IncludeUsage); internal/app/relay/compatible_handler.go:55,65,70 (ShouldIncludeUsage wiring) — present |
| wire-formats-12 | Format conversions (OpenAI<->Claude, OpenAI<->Gemini) | partial | 5 | M | no | claude_handler.go:1 / compatible_handler.go:1 / gemini_handler.go:1 (per-format inline conversion); internal/app/convert.go:1; grep -rln 'response_registry\|relayconvert\|FormatConvert\|ConvertResponse' -> 0 hits — conversions hand-written per-handler, not a generic bidirectional conversion registry like upstream's relaykit/relayconvert; harder to extend to new format pairs |
| wire-formats-13 | Reasoning effort support | equivalent | 4 | S | no | internal/pkg/dto/openai_request.go:38 (ReasoningEffort) — present |
| wire-formats-14 | Reasoning budget parameters | equivalent | 3 | S | no | openai_request.go:87,95 (EnableThinking, THINKING); internal/pkg/dto/claude.go:210,411,416 (Thinking struct + GetBudgetTokens) — present for Claude/Qwen/Gemini paths |
| wire-formats-15 | Prompt caching with retention policy | equivalent | 4 | S | no | openai_request.go:71-72 (PromptCacheKey, PromptCacheRetention); openai_response.go:226 (PromptCacheHitTokens) — present |
| wire-formats-16 | Seed parameter for reproducibility | equivalent | 2 | S | no | openai_request.go:53 (Seed) — present |
| wire-formats-17 | Logprobs and token probability tracking | equivalent | 2 | S | no | openai_request.go:58-59 (LogProbs, TopLogProbs); openai_response.go:82 (Logprobs) — present |
| wire-formats-18 | Prediction input (speculative decoding) | equivalent | 2 | S | no | openai_request.go:75 (Prediction) — present |
| wire-formats-19 | JSON schema response format | equivalent | 4 | S | no | openai_request.go:14-19,51 (ResponseFormat/JsonSchema) — present |
| wire-formats-20 | Service tier parameter | equivalent | 2 | S | no | openai_request.go:808 (ServiceTier); channel_settings.go:30 (AllowServiceTier, filtered by default) — present, plus channel-level allow-flag not clearly in upstream |
| wire-formats-21 | Store parameter (data retention toggle) | equivalent | 2 | S | no | openai_request.go:69 (Store); channel_settings.go:31 (DisableStore) — present |
| wire-formats-22 | Safety identifier | equivalent | 1 | S | no | openai_request.go:65 (SafetyIdentifier); channel_settings.go:32 (AllowSafetyIdentifier, filtered by default) — present, privacy-protective default off |
| wire-formats-23 | Parallel tool calls | equivalent | 3 | S | no | openai_request.go:54 (ParallelTooCalls); openai_response.go:376 (ParallelToolCalls) — present |
| wire-formats-24 | Max tool calls limit | equivalent | 2 | S | no | openai_request.go:820 (MaxToolCalls) — present on Responses request DTO |
| wire-formats-25 | Input modality support | equivalent | 4 | S | no | openai_request.go:1 (image_url/input_audio/files content types); compatible_handler.go:1 — multi-modal parsing present, same dto shapes as upstream |
| wire-formats-26 | Audio output support | equivalent | 3 | S | no | openai_request.go:85; audio.go:1; relay-router.go:169-177 (/audio/speech) — present via audio DTO + route |
| wire-formats-27 | Reasoning content streaming | equivalent | 3 | S | no | openai_response.go:89,106-117 (ReasoningContent + helpers) — present |
| wire-formats-28 | Cache token billing accounting | better | 4 | S | no | openai_response.go:277-278 (ClaudeCacheCreation5mTokens/1hTokens), :347-353 (CachedCreationTokens wire-semantics); usage_cache_write_wire_test.go, usage_wire_semantics_test.go — tracks 5m vs 1h cache-creation tiers separately with dedicated wire-semantics tests beyond upstream's flatter accounting |
| wire-formats-29 | Parameter override system | equivalent | 4 | S | no | internal/adapter/provider/common/override.go:1 (ValidateParamOverride/ApplyParamOverride); channel.go:706-707,1108-1123; invoked by all relay handlers — JSON-path override system present, channel-scoped |
| wire-formats-30 | Header override system | equivalent | 3 | S | no | override.go:1 (ValidateHeaderOverride); channel.go:220,711-712,1125-1134 — header set/delete override present, channel-scoped |
| wire-formats-31 | Web search parameters | equivalent | 2 | M | no | [skeptic flipped partial→equivalent: reviewer claimed newhub's only web-search is the standalone /api/v2/lutu/search Tavily endpoint and WebSearchOptions is unused in relay; false — internal/adapter/provider/claude/relay-claude.go:101-145 actively converts OpenAI-wire web_search_options into Anthropic's native web_search_20250305 tool (UserLocation, SearchContextSize→MaxUses mapped) and injects it as a tool on the outbound Claude request; this is real wired per-format conversion, so the capability is present end-to-end.] openai_request.go:81,791 (WebSearchOptions); web_search.go:47-154 (separate PostWebSearch at /api/v2/lutu/search using Tavily, not wired into chat completions) |
| wire-formats-32 | Conversion diagnostics reporting | missing | 3 | M | no | grep -rln 'diagnostic\|lossy' -i (excl _test) -> only unrelated hits; grep -rln 'ConversionDiagnostics\|conversion_diagnostics' -> 0 hits — no structured incompatibility/severity tracking across format conversions |
| wire-formats-33 | Model mapping and renaming | equivalent | 4 | S | no | internal/app/relay/helper/model_mapped.go:1 (ModelMappedHelper); channel.go (ModelMapping); responses_handler.go:33-35 — present across all relay handlers |
| wire-formats-34 | Streaming annotations (citations) | equivalent | 2 | S | no | openai_response.go:448 (Annotations) — field present in stream delta DTO |
| wire-formats-35 | System prompt injection | equivalent | 2 | S | no | channel_settings.go:7-9 (SystemPrompt, SystemPromptOverride); claude_handler.go:76-91, compatible_handler.go:94-124, gemini_handler.go:99-120 — present per-format |
| wire-formats-36 | Model discovery endpoints | equivalent | 3 | S | no | relay-router.go:17-38 (GET /v1/models, /v1/models/:model), :41-48 (GET /v1beta/models) — present, plus per-vendor-header dispatch at same route |
| wire-formats-37 | Alpha Search web search endpoint | missing | 2 | M | no | grep -n 'alpha/search\|AlphaSearch' relay-router.go -> 0 hits; web_search.go registers only /api/v2/lutu/search, not /v1/alpha/search — no standalone POST /v1/alpha/search route |
| wire-formats-38 | Stream obfuscation control | missing | 1 | S | no | grep -n 'include_obfuscation\|Obfuscation' openai_request.go channel_settings.go -> 0 hits |
| wire-formats-39 | Metadata and client context | equivalent | 2 | S | no | openai_request.go:74,803; openai_response.go:387 — present |
| wire-formats-40 | Vision high-resolution images | equivalent | 1 | S | no | openai_request.go:86 (VlHighResolutionImages) — present |

### newhub-only capabilities cited in this domain
- Tenant credit-pool gate on every billed relay route (PoolBalanceCheck) — relay-router.go:66,102.
- Cost-spike breaker (CostSpikeLimit) wrapping all relay/task groups incl. /suno, /v1/audio/music, /v1beta — relay-router.go:69,105,217-222; internal/app/cost_spike.go:1.
- Per-token/tenant + per-model rate limiting layered on relay dispatch (BusinessModelRateLimit after Distribute) — relay-router.go:118-121,133-136.
- In-flight concurrency cap (RelayConcurrencyLimit) per token/tenant — relay-router.go:76-79,113-116.
- Relay wire-format stamping middleware so auth/limit rejections answer in the caller's native error shape — relay-router.go:43-46,57-59,99-101.
- Free unmetered Claude token-counting endpoint sharing the same auth/routing chain — relay-router.go:143 (POST /v1/messages/count_tokens, never billed).
- Circuit breaker registry per upstream channel with Prometheus state metrics — relay.go:41-51 (resilience.NewRegistry).
- Channel-level allow-flags gating cost-affecting passthrough params off by default — channel_settings.go:30-32.
- Separated 5-minute vs 1-hour Claude prompt-cache-creation billing tiers with dedicated wire-semantics test suite — openai_response.go:277-278,347-353; usage_wire_semantics_test.go:1.

---

## Domain: Provider adapters and channel management (`providers-channels`)

| id | feature | status | value | effort | migration | newhub evidence / note |
|---|---|---|---|---|---|---|
| providers-channels-01 | Multi-provider channel support | partial | 5 | L | no | internal/pkg/constant/channel.go:2-58 (~50 provider types incl. Ollama/Aws/Cohere/MiniMax/Dify/Jina/Cloudflare/SiliconFlow/VertexAi/Mistral/DeepSeek/MokaAI/VolcEngine/BaiduV2/Xinference/Xai/Coze/Kling/Jimeng/Vidu/Submodel/DoubaoVideo/Sora/Replicate); internal/adapter/provider/ (30+ subdirs) — equivalent for classic LLM vendors, but no ChannelTypeAdvancedCustom/TaskPlugin/Sub2API/NewAPI/Codex constants |
| providers-channels-02 | Channel CRUD and discovery | equivalent | 5 | S | no | api-router.go:138-166; channel.go (GetAllChannels/AddChannel/UpdateChannel/DeleteChannel/SearchChannels) |
| providers-channels-03 | Channel test and health check automation | equivalent | 4 | M | no | [skeptic flipped partial→equivalent: original grep only searched internal/lifecycle and internal/app; the scheduled auto-test loop actually lives in channel-test.go and is wired at cmd/server/main.go:243 — a scheduled background auto-test task does exist (config-gated via CHANNEL_TEST_FREQUENCY, master-only).] channel-test.go:672-720 (AutomaticallyTestChannelsWithContext, master-gated ticker); cmd/server/main.go:241-244; monitor_setting.go:16-32 (CHANNEL_TEST_FREQUENCY); api-router.go:144-147 (test endpoints); app/channel.go:29 (AutoBan gate) |
| providers-channels-04 | Balance and quota tracking per channel | equivalent | 4 | S | no | channel-billing.go (UpdateChannelBalance/UpdateAllChannelsBalance); channel.go:29-30 (Balance, BalanceUpdatedTime) |
| providers-channels-05 | Weighted channel selection and retry | equivalent | 5 | S | no | channel.go:23,36 (Weight, Priority); app/channel_select.go (weighted random + priority fallback + auto-group retry) |
| providers-channels-06 | Model-to-channel mapping and upstream sync | equivalent | 4 | M | no | [skeptic flipped partial→equivalent: original grep for literal 'upstream_updates|UpstreamModelUpdate' doesn't exist in this codebase, but the same preview-diff-then-apply capability exists under SyncUpstreamPreview/SyncUpstreamModels with full UI wiring.] api-router.go:260-261 (/models/sync_upstream/preview, /models/sync_upstream); model_sync.go:247-283,484; web/src/pages/v2/Channel, useModelsData.jsx, ModelsActions.jsx |
| providers-channels-07 | Channel ability management (model availability) | equivalent | 3 | S | no | api-router.go:156 (POST /channel/fix); repo/channel.go (ability matrix) |
| providers-channels-08 | Multi-key channel mode | equivalent | 4 | S | no | channel.go:820,1616-2050 (ManageMultiKeys); channel.go:56-60 (ChannelInfo.IsMultiKey/MultiKeySize/MultiKeyStatusList) |
| providers-channels-09 | Channel grouping and tagging | equivalent | 3 | S | no | channel.go:32,38 (Group, Tag); channel.go:1026-1152 (ChannelTag struct + tag ops) |
| providers-channels-10 | Channel cloning | equivalent | 2 | S | no | api-router.go:165 (POST /channel/copy/:id); channel.go (CopyChannel) |
| providers-channels-11 | Channel parameter and header overrides | equivalent | 4 | S | no | channel.go:39-40 (ParamOverride, HeaderOverride); channel.go:706-712,1108-1134; v2_channel.go:491-492 |
| providers-channels-12 | Channel affinity caching | equivalent | 4 | S | no | repo/channel_affinity.go; channel_affinity_test.go, channel_cache_polling_race_test.go |
| providers-channels-13 | Channel constraints and routing filters | missing | 3 | M | yes | grep -rln 'ChannelConstraint\|FilterKind\|SatisfiesFilters' -> 0 hits — no path-filter/task-plugin-identity constraint matrix |
| providers-channels-14 | Advanced Custom channels | missing | 4 | XL | no | grep -n 'AdvancedCustom' channel.go -> 0 hits — no arbitrary request/response transformation channel type |
| providers-channels-15 | Task Plugin channels | missing | 3 | L | yes | grep -n 'TaskPlugin' channel.go -> 0 hits — no plugin-registry channel type or task_plugin.bind permission model |
| providers-channels-16 | Sub2API and NewAPI nested gateways | missing | 2 | M | no | grep -n 'Sub2API\|ChannelTypeNewAPI' channel.go -> 0 hits |
| providers-channels-17 | Codex (ChatGPT Subscription) OAuth integration | missing | 2 | L | yes | grep -n 'ChannelTypeCodex' -> 0 hits; grep -rn 'codex/refresh\|RefreshCodexChannelCredential' -> 0 hits |
| providers-channels-18 | Ollama local model management | equivalent | 2 | S | no | api-router.go:159-162 (ollama pull/delete/version); channel.go (OllamaPullModel/...) |
| providers-channels-19 | Channel status management | equivalent | 4 | S | no | channel.go (UpdateChannelStatus/BatchUpdateChannelStatus/DeleteDisabledChannel); channel.go:19 (Status) |
| providers-channels-20 | Channel sorting and pagination | equivalent | 2 | S | no | channel.go (GetAllChannels sort/pagination); Priority/Balance/ResponseTime/TestTime fields |
| providers-channels-21 | Model sync from upstream catalog | equivalent | 3 | S | no | api-router.go:262 (POST /models/sync_channels); model_sync_worker.go, model_sync.go |
| providers-channels-22 | Model metadata management | equivalent | 2 | S | no | model_meta.go (CRUD) |
| providers-channels-23 | Vendor metadata management | equivalent | 2 | S | no | vendor_meta.go (SearchVendors/GetVendorMeta/CreateVendorMeta/UpdateVendorMeta) |
| providers-channels-24 | Model discovery (missing models report) | equivalent | 2 | S | no | missing_models.go handler + repo |
| providers-channels-25 | Channel API key security | better | 4 | S | no | api-router.go:143 (RootAuth+CriticalRateLimit+DisableCache+SecureVerificationRequired); channel.go:958,986 (governance.RecordAuditEvent) — rate-limits key reveal and writes to a tamper-evident audit hash chain beyond upstream's plain audit log + RootAuth+2FA |
| providers-channels-26 | Batch channel operations | equivalent | 3 | S | no | api-router.go:155,163; channel.go (DeleteChannelBatch, BatchSetChannelTag) |
| providers-channels-27 | Channel default base URLs | equivalent | 2 | S | no | constant/channel.go:60-113 (ChannelBaseURLs, ~56 entries) |
| providers-channels-28 | Channel model listing | equivalent | 2 | S | no | api-router.go:140-141 (/channel/models, /channel/models_enabled) |
| providers-channels-29 | Channel auto-ban on repeated failures | equivalent | 3 | S | no | channel.go:37 (AutoBan); app/channel.go:29; channel_notify_extra_test.go:79-105 — reactive auto-ban present; proactive polling is separate (see #03) |
| providers-channels-30 | Per-channel proxy configuration | equivalent | 3 | S | no | api_request.go:266-269 (Proxy → NewProxyHttpClient); channel.go:754,810,957,1335 (cache invalidation) — stored in JSON Setting field rather than dedicated column |
| providers-channels-31 | Channel model constraints and compatibility | equivalent | 3 | S | no | channel.go:31 (Models); repo/channel.go (ability matrix) |
| providers-channels-32 | Channel response time and reliability metrics | equivalent | 2 | S | no | channel.go:25-26 (ResponseTime, TestTime) |
| providers-channels-33 | Audit logging for sensitive channel operations | better | 3 | S | no | channel.go:958,986,1053,1217,1336 (governance.RecordAuditEvent); v2_channel.go:372,547,620; v2_channel_actions.go:208 — hash-chain audit vs upstream's plain audit log |
| providers-channels-34 | Channel read-only field protection | partial | 3 | M | no | api-router.go:136 (AdminAuth), :143 (extra RootAuth+2FA only on key reveal); grep -rn 'SensitiveWrite\|ChannelSensitiveWrite' -> 0 hits — all admin-role callers can write base_url/proxy/param_override/header_override, no separate sensitive-write permission tier |

### newhub-only capabilities cited in this domain
- Multi-tenant channel isolation (TenantId scoping) — channel.go:16; channel_cache_tenant_test.go, channel_select_tenant_test.go.
- OpenRouter free-model sync engine with circuit-breaker baseline — channel.go:43-44; internal/app/openrouter_sync/ (aggregator/classifier/ranker/scheduler/sync).
- Governance audit hash chain for admin/channel actions — see consolidated entry below.
- Tenant model rate-limit policy entity — internal/domain/entity/model_rate_limit.go.
- Cost-spike breaker middleware — see consolidated entry below.
- Per-project cost attribution — internal/domain/entity/project.go.
- Tenant credit pools / provisioned redemption batches — see consolidated entry below.
- SecureVerificationRequired step-up auth on channel key reveal (2FA-equivalent) — api-router.go:143.

---

## Domain: Pricing, ratios, quota accounting and settlement (`billing-pricing`)

| id | feature | status | value | effort | migration | newhub evidence / note |
|---|---|---|---|---|---|---|
| billing-pricing-01 | Pricing and group ratio retrieval | equivalent | 5 | S | no | pricing.go:10-48 (GetPricing: data/group_ratio/usable_group/supported_endpoint/auto_groups) |
| billing-pricing-02 | Model pricing configuration and updates | partial | 5 | M | no | v2_pricing_write.go:31-262 (UpdatePricingV2: model_ratio/completion_ratio/model_price only) — missing cache_ratio in patch whitelist and optimistic version locking; concurrent admin writes can clobber each other |
| billing-pricing-03 | Model pricing preview and conversion | missing | 4 | M | no | grep -rn 'preview' v2_pricing_write.go pricing.go -> 0 hits; grep -rln 'PricingPreview\|pricing/convert\|ConvertPricing' -> 0 hits |
| billing-pricing-04 | Reset model ratio to defaults | equivalent | 3 | S | no | pricing.go:51-71 (ResetModelRatio) |
| billing-pricing-05 | Quota display types (USD/CNY/TOKENS/CUSTOM) | better | 4 | S | no | general_setting.go:7-11 (adds QuotaDisplayTypeLute); billing.go:53-60 — fifth LUTE display mode beyond upstream's four |
| billing-pricing-06 | Currency exchange rate configuration | equivalent | 4 | S | no | billing.go:55; general_setting.go (USDExchangeRate) |
| billing-pricing-07 | Group-based pricing multipliers | equivalent | 4 | S | no | ratio_setting/group_ratio.go:14-18 (default/vip/svip) |
| billing-pricing-08 | Group-to-group pricing multipliers | equivalent | 3 | S | no | group_ratio.go:22-27,122 (GroupGroupRatio, GetGroupGroupRatio); pricing.go:24-29 |
| billing-pricing-09 | Token stat display toggle | equivalent | 3 | S | no | general_setting.go:59 — same toggle concept folded into quota_display_type |
| billing-pricing-10 | Model ratio and completion ratio | equivalent | 5 | S | no | ratio_setting/model_ratio.go (defaultModelRatio/defaultCompletionRatio) |
| billing-pricing-11 | Cache token billing | equivalent | 4 | S | no | cache_ratio.go:11-108 (defaultCacheRatio + defaultCreateCacheRatio); price.go:85 |
| billing-pricing-12 | Image token billing | equivalent | 4 | S | no | model_ratio.go (image ratio table); price.go; perception.go |
| billing-pricing-13 | Audio token billing | equivalent | 3 | S | no | model_ratio.go:311-379,732-753,832-842; quota.go:78,585 |
| billing-pricing-14 | Tiered expression billing mode | missing | 5 | XL | no | grep -rln 'tiered_expr\|TieredBilling\|billingexpr\|BillingMode' -> 0 non-test hits — no JS-expression dynamic tiered pricing engine |
| billing-pricing-15 | Model price vs ratio billing | equivalent | 4 | S | no | relay_task.go:150-163 (modelPrice*ratio precedence); ratio_setting (GetModelPriceCopy) |
| billing-pricing-16 | Per-provider billing expression overrides | missing | 3 | XL | no | [skeptic flipped not_applicable→missing: dependent-on-missing-feature is 'not built', not architecturally inapplicable, consistent with 14/28.] depends on billing-pricing-14 which doesn't exist; no host to attach a per-provider override to |
| billing-pricing-17 | Quota pre-consumption and settlement | equivalent | 5 | S | no | pre_consume_quota.go:71-269 (PreConsumeQuota, platformPreAuthorize); quota.go:896-1209 (PostConsumeQuota) — additionally wired through platform wallet pre-auth |
| billing-pricing-18 | Task/async fixed-price billing | equivalent | 3 | S | no | relay_task.go:150-270 (task modelPrice*ratio, PostConsumeQuota, task.Quota) |
| billing-pricing-19 | Violation fee (CSAM) surcharge | missing | 2 | S | no | grep -rin 'violation' -> only unrelated unique-constraint hits; grep -rin 'CSAM' -> 0 hits |
| billing-pricing-20 | Ratio configuration exposure endpoint | equivalent | 2 | S | no | ratio_config.go:12-26 (IsExposeRatioEnabled); expose_ratio.go:11-15 |
| billing-pricing-21 | Upstream ratio synchronization | partial | 3 | S | no | ratio_sync.go:55,350-415; api-router.go:327-331 — fetch-from-official-channels sync exists, but no models.dev pricing source integration |
| billing-pricing-22 | User and token quota reservation | equivalent | 5 | S | no | quota.go:697 (PreConsumeTokenQuota); pre_consume_quota.go:71 — GORM conditional-update path, atomically equivalent to a Redis Lua reservation script |
| billing-pricing-23 | Quota per unit configuration | equivalent | 3 | S | no | common/constants.go:21 (QuotaPerUnit = 500*1000.0) |
| billing-pricing-24 | Subscription/billing subscription query | equivalent | 4 | S | no | billing.go:96-100; billing_self.go:1-77 |
| billing-pricing-25 | Usage statistics reporting | equivalent | 4 | S | no | billing.go:53-60 (USD/CNY/TOKENS conversion applied to usage totals) |
| billing-pricing-26 | Model metadata pricing integration | equivalent | 2 | S | no | types/price_data.go (PriceData, OtherRatios); log.go, option.go |
| billing-pricing-27 | Pricing cache invalidation | equivalent | 2 | S | no | v2_pricing_write.go:200 (InvalidateExposedDataCache) |
| billing-pricing-28 | Billing expression JavaScript evaluation | missing | 4 | XL | no | grep -rln 'billingexpr' -> 0 hits; grep -rln 'tiered_expr\|TieredBilling' -> 0 non-test hits — same gap as #14 |
| billing-pricing-29 | Billing expression smoke testing | missing | 2 | M | no | [skeptic flipped not_applicable→missing: same reasoning as #16.] no host feature (billing-pricing-14/28) to validate |
| billing-pricing-30 | Pricing version tracking | missing | 2 | M | yes | grep -rin 'version.*hash\|sha256.*pricing\|pricing.*version' -> 0 relevant hits — no version hash/optimistic-lock field, consistent with #02 |

### newhub-only capabilities cited in this domain
- Cost-aware savings analyzer (Phase 1) — internal/app/savings/calc.go:1-60 (re-pricing engine estimating savings from routing to cheaper quality-equivalent models).
- Tenant credit pools with threshold alerting — see consolidated entry below (quota.go:759,856; credit_pool.go:16-24).
- Cost-spike circuit breaker — see consolidated entry below.
- Tenant-scoped model allowlist — see consolidated entry below (tenant_model_allowlist.go:33-175).
- Audit hash chain for financial/admin events — see consolidated entry below.
- Platform wallet pre-authorization integration — pre_consume_quota.go:42-71 (releasePlatformPreAuth, platformPreAuthorize).

---

## Domain: Top-up, payment providers, subscriptions, redemption, check-in (`topup-payments-subscriptions`)

| id | feature | status | value | effort | migration | newhub evidence / note |
|---|---|---|---|---|---|---|
| topup-payments-subscriptions-01 | Top-up payment method discovery | partial | 5 | M | no | v2_billing.go:69-79 (GetBillingPaymentMethods proxies common.GetPaymentMethods(platform)) — no local Epay/Stripe/Creem/Waffo enable flags or discount tiers, that logic lives in lurus-platform |
| topup-payments-subscriptions-02 | Top-up transaction history | equivalent | 4 | S | no | v2_billing.go:364-365 (GetTopUpsV2) |
| topup-payments-subscriptions-03 | Epay payment gateway integration | missing | 5 | XL | yes | grep -rn -i 'epay' -> 0 hits — top-ups go through platform's own payment stack |
| topup-payments-subscriptions-04 | Stripe payment flow | missing | 5 | XL | yes | grep -rn -i 'stripe' -> 0 hits; CreateBillingCheckout calls common.CreateCheckout against lurus-platform |
| topup-payments-subscriptions-05 | Creem payment integration | missing | 4 | XL | yes | grep -rn -i 'creem' -> 0 hits — delegated to platform, same as Stripe/Epay |
| topup-payments-subscriptions-06 | Waffo payment gateway | missing | 4 | XL | yes | grep -rn -i 'waffo' -> 0 hits |
| topup-payments-subscriptions-07 | Waffo Pancake hosted checkout | missing | 4 | XL | yes | grep -rn -i 'waffo' -> 0 hits |
| topup-payments-subscriptions-08 | Payment amount calculation | partial | 3 | M | no | v2_billing.go:81-86 (AmountCNY passed straight to common.CreateCheckout) — no local per-provider amount/discount preview endpoint |
| topup-payments-subscriptions-09 | Quota-to-currency conversion | equivalent | 4 | S | no | v2_billing.go:229-364 (TopUpV2, minTransferCNY/maxTransferCNY) |
| topup-payments-subscriptions-10 | Payment method discount tiers | missing | 3 | M | no | grep -rn 'AmountDiscount\|AmountOptions' -> 0 hits |
| topup-payments-subscriptions-11 | Subscription plans management | missing | 5 | XL | yes | grep -rln -i 'SubscriptionPlan\|QuotaResetPeriod' -> 0 hits — no plan catalog entity/endpoint at all; nearest concept is tenant_credit_pool (ResetPeriod but no title/price/duration semantics) |
| topup-payments-subscriptions-12 | Subscription plan CRUD (admin) | missing | 4 | L | yes | grep -rn 'SubscriptionPlan' internal/adapter/handler/*.go -> 0 hits |
| topup-payments-subscriptions-13 | Quota reset scheduling | partial | 4 | L | no | tenant_credit_pool.go:8-13 (PoolResetNone/Daily/Weekly/Monthly) — only tenant-level credit-pool reset cadence, no per-plan per-user schedule |
| topup-payments-subscriptions-14 | Group-based subscription upgrades/downgrades | missing | 4 | L | yes | grep -rn 'UpgradeGroup\|DowngradeGroup' -> 0 hits |
| topup-payments-subscriptions-15 | Wallet overflow policy | missing | 3 | M | yes | grep -rn 'AllowWalletOverflow' -> 0 hits; closest analog is PoolDrawReasonOverdraft (tenant-scoped, not user-subscription-scoped) |
| topup-payments-subscriptions-16 | User subscription status | partial | 4 | L | yes | [skeptic flipped missing→partial: reader's grep for 'subscription/self|UserSubscription' missed the actual endpoint — billing.go:20-44 GetIdentityOverview (/api/v2/user/identity-overview) surfaces plan_code/status/expires_at/auto_renew sourced from lurus-platform via gRPC.] identity_client.go:266-273 (AccountOverview.Subscription); identity_grpc_client.go:395-415 |
| topup-payments-subscriptions-17 | Billing preference | missing | 3 | S | no | grep -rn 'BillingPreference' -> 0 hits; not applicable without subscriptions |
| topup-payments-subscriptions-18 | Subscription purchase with wallet balance | not_applicable | 4 | XL | yes | v2_billing.go:229-364 (TopUpV2 is the wallet-to-quota transfer newhub actually offers); no subscription-plan purchase model exists |
| topup-payments-subscriptions-19 | Subscription purchase via Epay | not_applicable | 4 | XL | yes | depends on both Epay gateway (#03, missing) and subscription plans (#11, missing); delegated to platform by architecture |
| topup-payments-subscriptions-20 | Subscription purchase via Stripe | not_applicable | 4 | XL | yes | same as #19, delegated to platform |
| topup-payments-subscriptions-21 | Subscription purchase via Creem | not_applicable | 3 | XL | yes | same as #19, delegated to platform |
| topup-payments-subscriptions-22 | Subscription purchase via Waffo Pancake | not_applicable | 3 | XL | yes | same as #19, delegated to platform |
| topup-payments-subscriptions-23 | Manual subscription binding (admin) | missing | 3 | M | yes | grep -rn 'subscription/admin/bind' -> 0 hits — closest analog is internal_credit_pool_fund.go, platform-driven pool funding not per-user plan bind |
| topup-payments-subscriptions-24 | Subscription quota reset (admin) | missing | 3 | M | no | grep -rn 'subscriptions/reset' -> 0 hits |
| topup-payments-subscriptions-25 | Subscription invalidation (admin) | missing | 2 | S | no | grep -rn 'user_subscriptions' -> 0 hits |
| topup-payments-subscriptions-26 | Redemption code creation | better | 4 | S | no | redemption.go (CreateRedemption, tenant-scoped); internal_provisioned_redemptions.go:1-40 (idempotent batch issuance up to 500, migration 027) |
| topup-payments-subscriptions-27 | Redemption code management | equivalent | 3 | S | no | redemption.go:18-51; v2_redemption.go:1-393 — plus tenant isolation upstream lacks |
| topup-payments-subscriptions-28 | Redemption code expiry and status tracking | equivalent | 3 | S | no | domain/entity/redemption.go:10,18 (Status, ExpiredTime) |
| topup-payments-subscriptions-29 | User redemption code redemption | equivalent | 5 | S | no | v2_redemption.go:37-80 (RedeemCodeV2); switch_user_topup.go:36-60 (anonymous token-scoped variant upstream lacks) |
| topup-payments-subscriptions-30 | Daily check-in (sign-in rewards) | missing | 3 | M | yes | checkin_setting.go:1-37 (config exists, Enabled defaults false); misc.go:193 only reads the flag; grep -rn 'func.*Checkin\|func.*CheckIn' -> 0 hits — feature is unreachable even if flag flipped on |
| topup-payments-subscriptions-31 | Check-in configuration (admin) | partial | 2 | S | no | checkin_setting.go:6-17 — config plumbing exists but has nothing to configure (#30 missing) |
| topup-payments-subscriptions-32 | Payment compliance confirmation | missing | 4 | M | yes | grep -rn -i 'payment.compliance\|PaymentCompliance' -> 0 hits — no terms-of-payment gate before checkout |
| topup-payments-subscriptions-33 | Top-up history (admin view) | partial | 3 | M | no | v2_billing.go:364-365 (GetTopUpsV2 is tenant-scoped, not global); grep -rn 'topup/complete\|CompleteTopup' -> 0 hits |
| topup-payments-subscriptions-34 | Waffo Pancake catalog and pairing (admin) | missing | 2 | L | yes | grep -rn -i 'waffo' -> 0 hits |
| topup-payments-subscriptions-35 | Critical rate limiting on payments | missing | 3 | S | no | api-router.go:60 (/user/topup only UserAuth); api-v2-router.go:212,304,317 — no CriticalRateLimit-equivalent on redeem/topup routes |
| topup-payments-subscriptions-36 | Payment webhook verification and idempotency | partial | 4 | L | no | internal_credit_pool_fund.go:28-33 (idempotency via event_id UNIQUE, migration 031) — platform-to-newhub funding, not a public payment-gateway webhook; grep -rn -i 'webhook' internal/adapter/handler -> 0 hits |

### newhub-only capabilities cited in this domain
- Idempotent platform-driven redemption-code batch provisioning — internal_provisioned_redemptions.go:24-40; provisioned_redemption_batch.go:1-28; migration 027.
- Tenant credit pool with debit/credit draw ledger and overdraft tracking — see consolidated entry below.
- Platform-funded credit pool via idempotent internal API (SEAM S1) — internal_credit_pool_fund.go:14-40; migration 031.
- Tenant-scoped redemption code isolation (non-root sees only own tenant) — redemption.go:22-27.
- Ownership-gated checkout-order status polling — v2_billing.go:180-200 (403 on order_no mismatch; prevents enumeration).
- Anonymous relay-token-scoped redemption (SwitchUserTopup / SwitchRedeemAnonymous) — switch_user_topup.go:19-60; api-v2-router.go:304,317.
- Audit hash-chain tamper evidence on governance actions — see consolidated entry below.

---

## Domain: Authentication, authorization, sessions, audit and account security (`auth-security`)

| id | feature | status | value | effort | migration | newhub evidence / note |
|---|---|---|---|---|---|---|
| auth-security-01 | OAuth provider login (GitHub/Discord/LinuxDO/Telegram/WeChat) | not_applicable | 5 | XL | yes | oauth.go:1-80 (single vendor-neutral OIDC provider only) — identity delegated to platform IdP; multi-vendor social login is the platform's job |
| auth-security-02 | Custom OAuth provider setup (admin-configured OIDC/OAuth endpoints) | not_applicable | 4 | L | no | grep -rln 'custom_oauth\|CustomOAuth' -> 0 hits — one OIDC endpoint set only, sourced from deploy env; conflicts with single-IdP-per-tenant architecture |
| auth-security-03 | Email account binding with verification code | not_applicable | 5 | XL | yes | grep -rln 'EmailBinding\|BindEmail' -> 0 hits; misc.go:47-60 (password/email login hardcoded off) — no local password identity at all; column deliberately removed (migration 008) |
| auth-security-04 | Passkey registration and management (WebAuthn) | missing | 4 | XL | yes | grep -rln 'webauthn\|WebAuthn' -> only test/i18n/migration files, no implementation; grep -rn 'func.*Passkey' -> 0 hits — no WebAuthn library, handler, or credential table |
| auth-security-05 | Passkey login | missing | 4 | XL | yes | grep -rn 'passkey.*login\|LoginPasskey' -> 0 hits — login is OIDC bridge or session cookie only |
| auth-security-06 | Two-factor authentication (TOTP) | partial | 5 | S | no | [skeptic flipped equivalent→partial: re-verification confirms zero backup-code implementation anywhere (migrations/008 dropped two_fa_backup_codes/twofas tables), combined with #07's confirmed missing admin force-disable — a user who loses their TOTP device has NO recovery path.] totp.go:1-220 (enroll/confirm/disable, AES-256-GCM secret); secure_verification.go:34-60 — setup/enable/disable/verify present, but backup codes absent |
| auth-security-07 | Admin 2FA management (stats + force-disable) | partial | 3 | M | no | totp.go (self-service only); grep -n 'AdminDisable\|ForceDisable\|TotpStats' -> 0 hits |
| auth-security-08 | Login session management (list/revoke individual/revoke-others) | partial | 4 | L | yes | v2_sessions.go:11-20 (comment: "no multi-device session store... deferred to v3"); v2_session_revoke.go:31-40 (only clears current cookie) |
| auth-security-09 | Session refresh and logout w/ CSRF | partial | 5 | M | yes | v2_session_revoke.go:41-60 (logout clears cookie); grep -rn 'refresh_token\|RefreshToken' -> 0 hits — no refresh-token rotation, no X-Auth-Session CSRF header check |
| auth-security-10 | Personal access token (PAT) generation | equivalent | 5 | S | no | v2_token.go:22-40 (tokenView, masked key list), :649 (RotateTokenV2) — v2 tokens serve as PAT equivalent |
| auth-security-11 | Password management (change/reuse-prevent/strength) | not_applicable | 5 | XL | yes | grep -rn 'Password' domain/entity/*.go -> 0 hits; no password column exists |
| auth-security-12 | Account binding/unbinding of external identities | not_applicable | 4 | L | yes | grep -n 'func.*Unbind\|func.*Bind' oauth.go -> 0 hits; grep -rln 'ExternalIdentity' -> 0 hits — identity graph lives in platform IdP |
| auth-security-13 | Secure verification flows (step-up) | equivalent | 5 | S | no | secure_verification.go:23-60 (UniversalVerify, 300s timeout, TOTP-required policy) |
| auth-security-14 | Audit log retrieval (self + admin query) | equivalent | 5 | S | no | audit.go:14-46 (GetAuditEvents filters); v2_admin_audit.go:31-55 (JSON/CSV cursor export) |
| auth-security-15 | Audit log categories/taxonomy | equivalent | 3 | S | no | governance/audit_action.go:17-58 (full taxonomy); v2_admin_audit.go:22-29 (ListAuditActionsV2) |
| auth-security-16 | Audit logging for admin operations | partial | 4 | M | no | governance/audit.go:32 (RecordAuditEvent, 29 explicit call sites); no generic write-op auto-audit middleware — a newly added endpoint that forgets the call produces zero audit trail |
| auth-security-17 | Permission catalog and roles (custom roles/per-resource overrides) | missing | 4 | XL | yes | grep -rln 'casbin\|Casbin\|RBAC' -> 0 hits; auth.go:329-341 fixed 3-tier enum only |
| auth-security-18 | Role-based authorization (RBAC) | partial | 4 | XL | yes | auth.go:329-341; admin_jwt_auth.go:24-76 — fixed 3-role hierarchy, no Casbin engine or per-resource grants |
| auth-security-19 | Passkey domain (RPID) management | not_applicable | 2 | XL | yes | grep -rn 'RPID\|rpid' -> 0 hits — no passkey feature exists at all (#4/#5) |
| auth-security-20 | User session active/issuance limits | missing | 3 | M | yes | grep -rn 'max.*session\|MaxSession\|session.*limit' -> 0 hits |
| auth-security-21 | Session authentication caching (Redis, bounded staleness) | equivalent | 2 | S | no | CLAUDE.md SYNC_FREQUENCY; auth.go (Redis-backed gin-contrib/sessions) |
| auth-security-22 | Auth version tracking (invalidate sessions on account change) | missing | 2 | M | yes | grep -rln 'AuthVersion\|auth_version' -> 0 hits — relies on coarser SYNC_FREQUENCY polling instead |
| auth-security-23 | Login flow verification (stateful 2FA/passkey flow tokens) | partial | 4 | M | yes | secure_verification.go:23-60 (session-key timestamp, no flow-token/replay-ID model); grep -rln 'AuthFlow\|LoginFlow\|flow_id' -> 0 hits |
| auth-security-24 | External identity claims (atomic, dual-unique-index ownership) | not_applicable | 3 | L | yes | grep -rln 'ExternalIdentity' -> 0 hits — single OIDC linkage per account, ownership dedup is the platform IdP's job |
| auth-security-25 | Account security event notifications (email on binding/password/2FA change) | partial | 3 | M | no | user_notify.go exists for quota/usage notifications; no confirmed TOTP-enroll/disable email hook; binding-change notifications n/a since binding doesn't exist |
| auth-security-26 | Session metadata tracking (IP/UA/timestamps/revocation reason) | partial | 3 | L | yes | v2_sessions.go:20-22 (comment: access_token/cookie/IP/User-Agent "never serialised") — no per-session store to attach this to (#8) |
| auth-security-27 | Admin user management (create/update/delete/roles/quota/status) | equivalent | 4 | S | no | auth_helper_cover_test.go:214 (AdminUpdateUser signature); v2_admin_audit.go |
| auth-security-28 | Access token fingerprinting (SHA-256, audit by fingerprint not credential) | missing | 3 | M | yes | grep -n 'fingerprint\|Fingerprint\|sha256\|SHA256' v2_token.go repo/*.go -i token -> 0 hits — tokens masked, not SHA-256 fingerprinted |
| auth-security-29 | Session revocation with reasons | partial | 3 | L | yes | v2_session_revoke.go:31-56 — single "revoke current" action, no reason enum, no session store to attach reasons to (#8/#26) |
| auth-security-30 | OIDC integration (PKCE, userinfo, SSO) | equivalent | 4 | S | no | oauth.go:29-77 (configurable authorize/token/end_session/userinfo paths); CLAUDE.md OIDC_ENABLE_PKCE — primary and only auth integration path |

### newhub-only capabilities cited in this domain
- Audit tamper-evidence hash chain (row_hash + prev_hash linkage verification) — see consolidated entry below.
- Cost-spike circuit breaker (per-user token-rate window in Redis) — see consolidated entry below.
- Tenant model allowlist policy — see consolidated entry below.
- Credit pool exhaustion/misconfiguration audit signals — credit_pool.go:16,24 (RecordPoolExhausted/RecordPoolNotConfigured).
- Per-token relay scope allowlist (opt-in replace semantics) — v2_token.go:358,474-488 (nil=unchanged vs []=full clear, audited).
- Multi-tenant slug-scoped session/audit views — v2_sessions.go:11-30.
- CSV+JSON cursor-paginated audit export with Link-header pagination — v2_admin_audit.go:31-55.

---

## Domain: Async tasks (video/music/image), task plugins and plugin protocol (`tasks-plugins`)

| id | feature | status | value | effort | migration | newhub evidence / note |
|---|---|---|---|---|---|---|
| tasks-plugins-01 | Task Submission and Status Tracking (Generic) | partial | 5 | XL | no | video-router.go:15-16; relay-router.go:298-315 (/mj); task.go:279-310 (GetAllTask/GetUserTask); repo/task.go:32-53 — per-vendor hardcoded routes/handlers, no generic POST /v1/tasks/:pluginKey dispatch surface |
| tasks-plugins-02 | Task Artifact Retrieval and Proxying | partial | 5 | L | yes | video_proxy.go:20-100 (ownership check at :61, header passthrough+24h cache at :187-197) — single-artifact video-only proxy; no /artifacts listing, no multi-artifact model, no Range/conditional headers, no HEAD, no allowed-host allowlist |
| tasks-plugins-03 | Video Generation Endpoint | equivalent | 5 | S | no | video-router.go:15-17 (generations, :task_id, remix) |
| tasks-plugins-04 | Plugin Upload and Versioning | missing | 4 | XL | yes | grep -rln 'TaskPlugin\|task_plugin\|PluginKey' -> 0 hits — vendors are compiled-in Go adapters, adding one requires code+redeploy |
| tasks-plugins-05 | Plugin Listing and Inspection | missing | 3 | L | no | grep -rln 'TaskPlugin' -> 0 hits |
| tasks-plugins-06 | Plugin Icon Management | missing | 2 | S | no | grep -rln 'TaskPlugin' -> 0 hits |
| tasks-plugins-07 | Plugin Dry-Run Testing | missing | 4 | L | no | grep -rln 'dryrun\|DryRun' -> only unrelated advisory-lock test |
| tasks-plugins-08 | Plugin Marketplace Sources | missing | 2 | M | no | grep -rln 'marketplace' -> only unrelated i18n string |
| tasks-plugins-09 | Plugin Enable/Disable | missing | 4 | M | no | grep -rln 'TaskPlugin' -> 0 hits — vendor toggling means a code change, not a runtime flag |
| tasks-plugins-10 | Plugin Runtime Status and Diagnostics | missing | 3 | M | no | grep -rln 'TaskPlugin' -> 0 hits |
| tasks-plugins-11 | Plugin Factory Disable Settings | missing | 2 | S | no | grep -rln 'TaskPluginDisabledFactoryKeys' -> 0 hits |
| tasks-plugins-12 | OpenAI Responses Protocol | partial | 5 | L | yes | relay-router.go:148-150 (POST /v1/responses, synchronous conversion only) — no stateful GET /v1/responses/:responseId retrieval, no persistence |
| tasks-plugins-13 | OpenAI Video Protocol | equivalent | 5 | S | no | video-router.go:20-23 (create/retrieve/content) |
| tasks-plugins-14 | Plugin Protocol Observation and Polling | missing | 3 | M | no | polling exists (task.go UpdateTaskBulkWithContext, 15s ticker) but not plugin-generation-pinned tunables |
| tasks-plugins-15 | Plugin Routing Conflict Resolution | missing | 3 | L | no | routes are static Gin registrations at boot, no dynamic conflict validation |
| tasks-plugins-16 | Dynamic Plugin Route Registration | missing | 4 | XL | no | grep -rln 'TaskPlugin' -> 0 hits |
| tasks-plugins-17 | Artifact Type and Validation | missing | 4 | M | yes | repo/task.go:32-53 has opaque Data json.RawMessage, not a validated artifacts[] list |
| tasks-plugins-18 | Admin Task Queries and Filtering | partial | 3 | S | no | task.go:280-310 (filters: Platform/TaskID/Status/Action/timestamps/ChannelID) — no plugin snapshot field, no separate upstream_task_id, no request ID |
| tasks-plugins-19 | Task Artifact Content URL Construction | missing | 3 | M | no | no stateless host-signed capability URL scheme; VideoProxy requires session+DB lookup per request |
| tasks-plugins-20 | Dashboard Task Artifacts Endpoint | missing | 3 | M | no | grep -rn 'legacy_content_url\|LegacyContentUrl' -> 0 hits; no GET /api/task/:taskId/artifacts |
| tasks-plugins-21 | System Tasks (Background Jobs) | missing | 2 | M | yes | grep -rln 'SystemTask\|system-task\|log-cleanup' -> 0 hits — lifecycle.Manager runs jobs but exposes no polling API |
| tasks-plugins-22 | Midjourney Polling Integration (Legacy) | equivalent | 2 | S | no | relay-router.go:298-315 (full /mj action set); midjourney.go:22-77; task_refund.go:34-49 — wired through enforcement chain upstream lacks |
| tasks-plugins-23 | Task Plugin Channel Type | missing | 3 | L | yes | grep -rn 'ChannelType.*=.*61\|ChannelTypeTaskPlugin' -> 0 hits |
| tasks-plugins-24 | Plugin Version History and Rollback | missing | 4 | L | yes | grep -rln 'TaskPlugin' -> 0 hits — equivalent is a full binary redeploy via ArgoCD |
| tasks-plugins-25 | Plugin Metadata and Schema Validation | missing | 3 | L | no | grep -rln 'TaskPlugin' -> 0 hits |
| tasks-plugins-26 | Video Proxy with SSRF Protection | partial | 4 | M | no | video_proxy.go:127-153 (server-constructed URLs per channel type, sidesteps classic SSRF) — no explicit allowed-host allowlist, no Range/HEAD support |
| tasks-plugins-27 | Plugin Request Hook Signatures | missing | 4 | L | no | provider.TaskAdaptor interface (task_video.go:47-52) exists but compiled-in, not a plugin-exported hook contract |
| tasks-plugins-28 | Batch vs Per-Task Plugin Modes | missing | 3 | M | no | task.go:161-165 (updateSunoTaskAll batches per channel) but hardcoded per vendor, not plugin-declared |
| tasks-plugins-29 | Artifact Renderer Protocols | missing | 3 | L | no | grep -rn 'Artifact' -> 0 relevant hits |
| tasks-plugins-30 | Plugin Sandbox Isolation | not_applicable | 5 | XL | no | no user-uploaded-code execution surface at all; building one is a net-new attack surface, not a gap-closing task |
| tasks-plugins-31 | Plugin Fixture and Dry-Run Testing | missing | 3 | M | no | grep -rln 'TaskPlugin\|fixture' -> 0 hits |
| tasks-plugins-32 | Task Plugin Options API | missing | 2 | S | no | grep -rln 'task_plugin_options\|TaskPluginBind' -> 0 hits |
| tasks-plugins-33 | Plugin Generation and Routing Lifecycle | missing | 3 | L | no | no generation-atomic plugin registry; cross-node consistency achieved via identical compiled binaries instead |
| tasks-plugins-34 | Task Artifact Store Backend | missing | 2 | M | no | no artifact-store abstraction; VideoProxy branches directly on channel.Type per call |
| tasks-plugins-35 | Task Plugin Submission Diagnostics | partial | 2 | M | no | task.go:20-24 (SysLog), task_refund.go:34-49 — ad hoc logging, no consistent per-attempt diagnostics schema |
| tasks-plugins-36 | Legacy Video Result URL Handling | missing | 2 | S | no | grep -rn 'legacy_content_url\|LegacyContentUrl' -> 0 hits; video_proxy.go:150-153 reads a URL out of FailReason internally but doesn't surface/clear it in the API response |
| tasks-plugins-37 | Plugin API Version and Schema Documentation | missing | 2 | S | no | no plugin contract, so no contract docs |
| tasks-plugins-38 | Plugin Console Logging in Debug Mode | missing | 1 | S | no | grep -rln 'TaskPlugin' -> 0 hits |

### newhub-only capabilities cited in this domain
- Enforcement chain on MJ/task submit routes (cost-spike breaker, entitlement, model-rate-limit, business-rate-limit, concurrency-limit) — relay-router.go:298-303; cost_spike.go:47; entitlement.go:42.
- Task-level ownership check on artifact proxy (per-tenant isolation) — video_proxy.go:58-73.
- Quota refund on async task failure wired into billing/wallet path — task_refund.go:34-49 (repo.IncreaseUserQuota).
- Leader-election gated task polling (HA multi-replica safe) — task.go:83-87 (common.IsLeader() gate).

---

## Domain: Channel selection, retry/failover, rate limits, groups, transport and resilience (`routing-resilience-limits`)

| id | feature | status | value | effort | migration | newhub evidence / note |
|---|---|---|---|---|---|---|
| routing-resilience-limits-01 | Channel weighted random selection | better | 5 | S | no | channel_cache.go:197,258-321,276-298 (Hub smart-routing weight adjustment); group.go:14 — same priority/weight algorithm plus tenant-scoped filtering and live performance-based weight adjustment |
| routing-resilience-limits-02 | Automatic retry on failure with cross-group retry | equivalent | 5 | S | no | app/channel_select.go:88,112-177,14-51 (RetryParam) — cross-group retry ported 1:1, generalized with tenant scoping |
| routing-resilience-limits-03 | User-level model request rate limiting | equivalent | 5 | S | no | model-rate-limit.go:147,196-201; rate_limit.go:46,78 (GetGroupRateLimit) |
| routing-resilience-limits-04 | Global IP-based rate limiting | equivalent | 5 | S | no | rate-limit.go:106,120,127,134 (GlobalWebRateLimit/GlobalAPIRateLimit/CriticalRateLimit) |
| routing-resilience-limits-05 | Download and upload rate limiting | equivalent | 4 | S | no | rate-limit.go:141,145 (DownloadRateLimit/UploadRateLimit) |
| routing-resilience-limits-06 | Email verification and notification rate limiting | equivalent | 3 | S | no | notify-limit.go:105,112,144 (Redis-or-memory hourly limiter) |
| routing-resilience-limits-07 | Token groups with auto-group support | equivalent | 5 | S | no | channel_select.go:112-116 (auto-group branch, GetUserAutoGroup) |
| routing-resilience-limits-08 | User usable groups configuration | equivalent | 4 | S | no | group.go:26,31,36 (GetUserGroups, GetUserUsableGroups) |
| routing-resilience-limits-09 | Group-specific model ratio and rate limits | equivalent | 4 | S | no | rate_limit.go:78; model-rate-limit.go:331; group.go:16 |
| routing-resilience-limits-10 | Channel affinity caching with regex-based rules | partial | 4 | M | no | session_affinity.go:52,64; channel_select.go:98-110; channel_affinity.go:23 — conversation-scoped HMAC affinity exists, but no admin-configurable regex rules matching model/path/user-agent |
| routing-resilience-limits-11 | Channel affinity statistics and management | missing | 3 | M | no | grep -rln 'AffinityCacheStats\|ClearAffinity' -> 0 hits — only lever is env-var TTL/enable |
| routing-resilience-limits-12 | Channel affinity switch-on-success and keep-on-disabled options | partial | 3 | S | no | session_affinity.go:29-33 (hard-coded safety model); channel_affinity.go:23 — behaviour fixed, no admin toggle |
| routing-resilience-limits-13 | HTTP protocol policy (HTTP/1 vs HTTP/2 auto-detect) | missing | 3 | M | yes | grep -rn 'ForceHTTP1\|HTTP1Force\|HTTPTransportPolicy' -> 0 hits; ForceAttemptHTTP2:true is hardcoded |
| routing-resilience-limits-14 | HTTP/2 connection sharding | missing | 4 | L | yes | http_client.go:119-147 (single shared transport, no shard pool) |
| routing-resilience-limits-15 | Connection pool sizing (idle/per-host limits) | equivalent | 3 | S | no | http_client.go:121-122,213-214,248-249 |
| routing-resilience-limits-16 | Response header timeout | equivalent | 3 | S | no | http_client.go:60-65,133,218,257 (applyRelayTransportTimeouts) |
| routing-resilience-limits-17 | Per-channel proxy override | equivalent | 4 | S | no | http_client.go:193,210-264,179-190 (storeProxyClient, maxProxyClients=256 bound) |
| routing-resilience-limits-18 | TLS verification control | missing | 2 | S | no | grep -rn 'InsecureSkipVerify\|TLSInsecure' http_client.go setting -> 0 hits (only unrelated SMTP switch) |
| routing-resilience-limits-19 | SSRF protection for outbound requests | equivalent | 4 | S | no | http_client.go:32-42,126-132; webhook.go:91-95 — validated at both redirect-check and dial-time layers |
| routing-resilience-limits-20 | Trusted proxies configuration | equivalent | 3 | S | no | trusted_proxies.go; config.go (TRUSTED_PROXIES) — RemoteAddr is always private in R6 topology, an architectural caveat not a code gap |
| routing-resilience-limits-21 | Prefill groups (reusable model/endpoint collections) | equivalent | 2 | S | no | prefill_group.go:13,24; api-router.go:218-226 (RootAuth CRUD) |
| routing-resilience-limits-22 | Multi-key channels (polling, random, rotating modes) | partial | 3 | S | no | constant/multi_key_mode.go:6-7 (only Random and Polling) — 'rotating' mode from upstream absent |
| routing-resilience-limits-23 | Webhook notifications with HMAC signatures | better | 2 | S | no | webhook.go:28,35,61-89,91-95 — same HMAC scheme plus SSRF guard and Cloudflare-Worker-relay fallback |
| routing-resilience-limits-24 | Request body size limits | equivalent | 3 | S | no | body_size_limit.go (anonymous vs authenticated distinction) |
| routing-resilience-limits-25 | Streaming timeout | equivalent | 3 | S | no | http_client.go:58-59 (Timeout deliberately untouched); stream_scanner.go per-chunk timeout |
| routing-resilience-limits-26 | Stream scanner buffer size limit | equivalent | 2 | S | no | stream_scanner.go:26,163; init.go:144 (64MB default, env-configurable) |
| routing-resilience-limits-27 | Channel and token caching with Redis | equivalent | 4 | S | no | channel_cache.go:24,154,197,361,386 |
| routing-resilience-limits-28 | Channel cache sync frequency | equivalent | 3 | S | no | channel_cache.go:125,134 (SYNC_FREQUENCY=60s per CLAUDE.md) |
| routing-resilience-limits-29 | Disk cache for large payloads | missing | 2 | L | no | grep -rn 'DiskCache' -> 0 hits — bounded instead by STREAM_SCANNER_MAX_BUFFER_MB and body size limits |
| routing-resilience-limits-30 | Performance monitoring thresholds | missing | 2 | M | no | grep -rn 'MonitorCPUThreshold\|MonitorMemoryThreshold\|MonitorDiskThreshold' -> 0 hits — delegated to Netdata (external tool, not code in this repo) |
| routing-resilience-limits-31 | Token model limits (per-token allowlist) | equivalent | 4 | S | no | key_info.go:49,124; internal_api_ext.go:864-865,944-986 |
| routing-resilience-limits-32 | Channel filter constraints (task plugin, request path) | missing | 3 | L | no | grep -rn 'GetChannelConstraints\|ChannelFilter' -> 0 hits — routing inputs limited to group/model/tenant/retry plus affinity and allowlist |
| routing-resilience-limits-33 | Channel pin/override (origin task pinning) | missing | 3 | M | no | grep -rn 'ResolvedPin\|ChannelPin' -> 0 hits — closest analog (session affinity) is best-effort, not an authoritative pin |
| routing-resilience-limits-34 | Max token auto groups limit | missing | 2 | S | no | grep -rn 'MaxTokenAutoGroups\|MaxAutoGroups' -> 0 hits — auto-group loop has no ceiling |

### newhub-only capabilities cited in this domain
- Tenant-scoped channel selection (multi-tenant channel pool isolation) — channel_cache.go:167,194-197 (filtering happens before weight bucketing).
- Hub smart-routing: live performance-based weight adjustment — channel_cache.go:276-298 (hub.AdjustWeights).
- Session affinity for prompt-cache locality (conversation-scoped, HMAC-hashed key) — session_affinity.go:19-48; channel_select.go:94-110.
- Tenant model allowlist (observe/enforce policy) — see consolidated entry below.
- Cost-spike breaker middleware (observe-by-default circuit breaker on spend velocity) — see consolidated entry below.
- Structured fail-open metrics for every rate-limit degrade path (Redis outage visibility) — model-rate-limit.go:38-46,71-82,138.

---

## Domain: Admin/user console features and UX (`console-ux`)

| id | feature | status | value | effort | migration | newhub evidence / note |
|---|---|---|---|---|---|---|
| console-ux-01 | Multi-language UI support | partial | 3 | S | no | web/src/i18n/locales/ (en/fr/ja/ru/vi/zh — 6 of 7 upstream languages); no zh-TW |
| console-ux-02 | Dashboard with time-series analytics | equivalent | 5 | S | no | web/src/pages/v2/Dashboard/; usedata.go:13,34 |
| console-ux-03 | Model ranking/leaderboard view | missing | 3 | M | no | grep -rn 'ranking' -> only unrelated openrouter_sync ranker.go |
| console-ux-04 | Performance metrics per model | partial | 4 | L | no | [skeptic flipped missing→partial: original grep missed the actual feature — governance.go:53-56 GetGovernanceLatencyStats (avg/max latency per model) is wired at api-v2-router.go:453, but no admin UI page calls it (CostIntelligence page only calls /savings).] repo/governance.go:75 (GetLatencyStats) |
| console-ux-05 | System status and uptime monitoring | equivalent | 4 | S | no | api-router.go:32; misc.go:37,179 (Uptime Kuma) |
| console-ux-06 | User management (create/edit/delete/search) | equivalent | 5 | S | no | user.go; v2_admin.go; v2_user.go — identity fields differ (no local password) per architecture |
| console-ux-07 | User 2FA (two-factor authentication) management | partial | 3 | M | no | totp.go; api-router.go:73-76; TwoFactorAuth.jsx — no admin-facing 2FA stats or force-disable |
| console-ux-08 | User OAuth provider bindings | not_applicable | 2 | XL | yes | single vendor-neutral OIDC IdP; multi-provider binding matrix lives in the platform IdP |
| console-ux-09 | Channel management (CRUD + test + balance update) | equivalent | 5 | S | no | channel.go; channel-test.go; v2_channel.go; v2_channel_actions.go |
| console-ux-10 | Model configuration and pricing | equivalent | 5 | S | no | model_meta.go; internal_currency.go; web/src/pages/v2/Models/; RatioSetting.jsx |
| console-ux-11 | Token/API key management | equivalent | 5 | S | no | token.go; v2_token.go; v2_token_batch_test.go; web/src/pages/v2/Token/ |
| console-ux-12 | Usage logs and search | better | 4 | S | no | log.go:14,61; v2_log.go:181; v2_admin_audit.go:22; chain_verify.go:80 — plus hash-chained verifiable audit trail |
| console-ux-13 | Billing and wallet topup | partial | 5 | L | no | v2_billing.go:230,365 — money movement delegated to platform wallet, no direct multi-gateway integration; several upstream gateways (Waffo, Waffo-Pancake) entirely absent |
| console-ux-14 | Subscription plans management | missing | 4 | XL | yes | grep -rn 'SubscriptionPlan\|subscription_plan' -> 0 hits |
| console-ux-15 | Redemption code management | equivalent | 3 | S | no | redemption.go:17; switch_redeem.go — plus tenant-scoped visibility and replay-guard hardening |
| console-ux-16 | System settings configuration UI | equivalent | 5 | S | no | web/src/pages/v2/Settings/ (Auth/Branding/Content/General/Security/ModelConfig pages) |
| console-ux-17 | Payment compliance attestation | missing | 2 | M | no | grep -rln 'PaymentCompliance\|payment_compliance' -> 0 hits |
| console-ux-18 | Performance stats and cache management | missing | 2 | M | no | grep -rln 'ForceGC\|DiskCache\|GetPerformanceStats\|disk_cache' -> 0 hits — emptyDir pods have no persistent disk to manage anyway |
| console-ux-19 | System task scheduler | missing | 3 | L | yes | grep -rln 'system.task\|SystemTask' -> 0 hits — lifecycle.Manager has no admin CRUD UI |
| console-ux-20 | System instances monitoring | missing | 3 | M | no | grep -rln 'SystemInstance\|system-info/instances' -> 0 hits — replica health left to k8s/Netdata |
| console-ux-21 | Task plugin management | missing | 2 | XL | yes | grep -rln 'TaskPlugin\|plugin/task' -> 0 hits |
| console-ux-22 | Custom OAuth provider configuration | not_applicable | 2 | XL | yes | single vendor-neutral OIDC issuer/clientId configured at deploy-time, owner-gated |
| console-ux-23 | User profile and account settings | partial | 4 | S | no | api-router.go:55-57; api-v2-router.go:87,221-226 — profile+current-session revoke exist, no per-other-session revoke |
| console-ux-24 | Passkey registration and authentication | missing | 3 | S | no | [skeptic flipped partial→missing: passkey was dead settings scaffolding with no handler/route, since deleted outright per r5c_status_capability_test.go N5 comment; CLAUDE.md's "Auth = OIDC + Passkey" line is stale.] |
| console-ux-25 | Email verification and password reset | not_applicable | 4 | XL | yes | grep -rn 'reset_password\|ResetPassword' -> 0 hits — account lifecycle delegated entirely to platform IdP |
| console-ux-26 | Unified secure verification (MFA gateway) | equivalent | 3 | S | no | api-router.go:67-68; secure_verification.go; totp.go |
| console-ux-27 | Vendor/upstream provider management | equivalent | 3 | S | no | vendor_meta.go:13,28 |
| console-ux-28 | Model deployment management (IO.NET) | equivalent | 2 | S | no | deployment.go:16,28; ModelDeploymentSetting.jsx |
| console-ux-29 | Model sync and upstream updates | equivalent | 3 | S | no | model_meta.go; openrouter_sync/aggregator.go, ranker.go |
| console-ux-30 | Channel affinity cache stats | missing | 2 | M | no | grep -rln 'ChannelAffinity\|channel_affinity' -> 0 hits |
| console-ux-31 | Affiliate/referral program | missing | 3 | L | yes | no aff_transfer/GetAffCode hits in billing handlers |
| console-ux-32 | Daily check-in bonus | partial | 2 | S | no | checkin_setting.go:25; misc.go:193 — configured but not wired to any endpoint |
| console-ux-33 | Content customization (notice, FAQ, announcements) | equivalent | 3 | S | no | misc.go:221,232,272,207-210; ContentSettingPage.jsx |
| console-ux-34 | Site branding and UI customization | equivalent | 3 | S | no | misc.go (SystemName/Logo/Footer, HeaderNavModules); BrandingSettingPage.jsx |
| console-ux-35 | Group management (token quotas by group) | equivalent | 3 | S | no | group.go:14; prefill_group.go:13,24,50 |
| console-ux-36 | Authorization/permission management | missing | 3 | XL | yes | grep -rln 'authz\|Permission' router -> 0 hits — coarse role tiers instead of granular authz router |
| console-ux-37 | Waffo-Pancake subscription integration | missing | 2 | XL | yes | grep -rln -i 'waffo' -> 0 hits |
| console-ux-38 | Model ratio and billing expression editor | equivalent | 4 | S | no | RatioSetting.jsx; internal_currency.go; model_meta.go |
| console-ux-39 | Pricing and quota display configuration | equivalent | 3 | S | no | misc.go:157-163 |
| console-ux-40 | Multi-tenant token isolation | better | 4 | S | no | v2_token.go; v2_token_rotate_idor_test.go — real tenant→project→token hierarchy, strictly stronger than upstream's single-tier scoping |

### newhub-only capabilities cited in this domain
- Tenant credit pools (shared prepaid balance per tenant) — see consolidated entry below.
- Hash-chained, cryptographically verifiable audit trail — see consolidated entry below.
- Tenant→Project→Token cost attribution hierarchy — v2_project.go; migration 029.
- Tenant-scoped analytics/quota queries (root sees all tenants, tenant admin scoped to own) — usedata.go:13-21.
- Tenant-scoped redemption code visibility — redemption.go:17-27.
- Vendor-neutral OIDC + Passkey identity delegated to platform IdP (no local password store) — CLAUDE.md Auth line; oauth.go.

---

## Domain: Usage logs, analytics, dashboards, metrics, performance and health (`logs-analytics-observability`)

| id | feature | status | value | effort | migration | newhub evidence / note |
|---|---|---|---|---|---|---|
| logs-analytics-observability-01 | Consume usage logs with token metrics | equivalent | 5 | S | no | domain/entity/log.go:1-38; repo/log.go:500 (RecordConsumeLog) — request_id/session_id in Other JSON blob instead of dedicated columns |
| logs-analytics-observability-02 | Multi-type audit log events | better | 4 | S | no | log.go:41-48 (6 log types); audit_event.go; governance/audit.go — plus independent hash-chain audit_events table upstream lacks |
| logs-analytics-observability-03 | Admin-filtered sensitive metadata in logs | equivalent | 4 | S | no | repo/log.go:148 (SanitizeOtherForUser); log.go:27,77,197,253 |
| logs-analytics-observability-04 | Log search and filtering | better | 4 | S | no | repo/log.go:854,929,693,838,847 — plus tenant scoping and source_product attribution filter not in upstream |
| logs-analytics-observability-05 | Real-time usage statistics (quota/rpm/tpm) | better | 5 | S | no | v2_log_stat.go:1-58 — extended with cache_read/cache_write totals and by_product breakdown |
| logs-analytics-observability-06 | Hourly quota data dashboard | equivalent | 4 | S | no | usedata.go:13-49 — plus tenant scoping |
| logs-analytics-observability-07 | Group-based flow quota tracking | missing | 3 | M | yes | [skeptic flipped partial→missing: no group column and no group filter exist anywhere in usedata handler/repo/entity, not even the degraded fallback originally credited.] domain/entity/usedata.go:3-12 has no group/token_id/channel_id/node_name columns, unlike upstream's UseGroup/TokenID/ChannelID/NodeName |
| logs-analytics-observability-08 | Per-model performance metrics aggregation | missing | 4 | M | yes | grep -rn 'PerfMetric\|perf_metric' -> 0 hits for an entity/repo; only a Prometheus histogram exists (scrape/alert-oriented, not a queryable business aggregate) |
| logs-analytics-observability-09 | Model performance query API | missing | 4 | M | no | ttft.go:1-64 is Prometheus-only, no HTTP query endpoint |
| logs-analytics-observability-10 | Model rankings with growth tracking | missing | 3 | L | no | grep -rln 'Rankings' -> 0 hits |
| logs-analytics-observability-11 | Vendor rankings and top-movers dashboard | missing | 3 | L | no | grep -rln 'Rankings\|leaderboard' -> 0 hits |
| logs-analytics-observability-12 | Model and vendor usage history time series | missing | 2 | L | no | same rankings service absent as #10 |
| logs-analytics-observability-13 | Audit log with login and operation tracking | better | 4 | S | no | audit_event.go; governance/audit.go; cov_audit_hash_test.go — plus hash-chain tamper evidence upstream lacks entirely |
| logs-analytics-observability-14 | Audit log filtering and retrieval | equivalent | 3 | S | no | v2_admin_audit.go; repo/audit.go |
| logs-analytics-observability-15 | Request tracing with request_id and upstream_request_id | partial | 4 | S | yes | log_request_identity_test.go:60-61; repo/log.go:326 — request_id tracked in Other JSON blob, no dedicated upstream_request_id field |
| logs-analytics-observability-16 | Optional client IP logging per user | equivalent | 2 | S | no | repo/log.go:457,530 (RecordIpLog) |
| logs-analytics-observability-17 | System instance registration and uptime tracking | not_applicable | 2 | M | yes | metrics/instance.go:15-52 — lifecycle delegated to k3s/ArgoCD, app only publishes Prometheus gauges |
| logs-analytics-observability-18 | Runtime memory and GC statistics | missing | 2 | S | no | grep -rn 'runtime.GC\|debug.FreeOSMemory\|GOGC' -> 0 hits; grep -rln 'MemStats' -> 0 hits |
| logs-analytics-observability-19 | Disk cache statistics and management | not_applicable | 2 | L | no | no persistent disk in pod spec (emptyDir only); conflicts with stateless-pod model |
| logs-analytics-observability-20 | Log file listing and rotation management | not_applicable | 2 | M | no | no on-disk log files (stdout only) |
| logs-analytics-observability-21 | Uptime Kuma monitoring status integration | equivalent | 2 | S | no | uptime_kuma.go:131; api-router.go:32; misc.go:179 |
| logs-analytics-observability-22 | Performance stats endpoint with config info | missing | 2 | M | no | depends on disk-cache/container introspection that doesn't exist |
| logs-analytics-observability-23 | Optional error logging by request | better | 3 | S | no | repo/log.go:448 (RecordErrorLog) — 2026-08-30 fix widened it to middleware/pre-channel rejections beyond relay failures |
| logs-analytics-observability-24 | Data export to external systems | equivalent | 2 | S | no | repo/log.go:577; misc.go:165-166 |
| logs-analytics-observability-25 | Clickhouse and PostgreSQL log backend support | not_applicable | 2 | XL | no | common/database.go:12 (UsingClickHouse = false, dead flag) — PG-only architecture decision |
| logs-analytics-observability-26 | TTL and log retention cleanup | equivalent | 3 | S | no | repo/log.go:802 (DeleteOldLog); lifecycle/audit_cleanup.go:17-61 |
| logs-analytics-observability-27 | Per-model quota totals for rankings | missing | 2 | L | no | same rankings service absent as #10 |
| logs-analytics-observability-28 | Rankings cache with 5-minute TTL | missing | 2 | S | no | no rankings feature exists at all |
| logs-analytics-observability-29 | Admin performance stats reset and GC trigger | missing | 1 | S | no | grep -rn 'runtime.GC\|debug.FreeOSMemory' -> 0 hits |
| logs-analytics-observability-30 | Ranged log quota aggregation | equivalent | 3 | S | no | repo/log.go:716,770 (SumUsedQuota, SumUsedToken) |

### newhub-only capabilities cited in this domain
- Tenant-scoped log isolation (TenantScope / ForTenant / AllTenantsForAdmin) — repo/log.go:74-98.
- Hash-chain tamper-evident audit log (audit_events) — see consolidated entry below.
- Cross-product cost attribution in log stats (by_product breakdown, source_product filter) — v2_log_stat.go:47-58; repo/log.go:838.
- Cache token accounting in usage stats (cache_read_tokens/cache_write_tokens) — v2_log_stat.go:34-40.
- Prometheus-native leader/instance observability (Leader, LeaderTaskLastSuccess, InstanceInfo gauges) — metrics/instance.go:19-52.
- Detailed dependency health check with intentional-off-state classification — health.go:16-139.
- IDOR-hardened request-id log lookup (GetLogByKey/GetLogByRequestID with caller ownership checks) — repo/log.go:279,326.
- Schema-migration drift metric feeding health/readiness — metrics/migrations.go; health.go:63-70.

---

## Domain: Deployment, multi-node operation, setup, migrations, OpenAPI/docs, client tooling (`ops-deploy-docs`)

| id | feature | status | value | effort | migration | newhub evidence / note |
|---|---|---|---|---|---|---|
| ops-deploy-docs-01 | Docker Compose deployment | better | 5 | S | no | deploy/single-node/docker-compose.yml:1-140 — continuous WAL-G backup, tuned PG params, resource limits, healthcheck gating startup, beyond upstream compose |
| ops-deploy-docs-02 | Multi-database backend support | not_applicable | 1 | XL | no | repo/main.go:192-196 (SQL_DSN must be postgres://) — deliberate PG-only decision since 2026-06 |
| ops-deploy-docs-03 | Systemd service file | equivalent | 2 | S | no | lurus-api.service:1-19 |
| ops-deploy-docs-04 | Multi-node deployment with shared database | equivalent | 5 | S | no | doc/runbook/ha-deployment.md:1-40 (3 replicas share PG+Redis) |
| ops-deploy-docs-05 | Redis topology options for multi-node | partial | 3 | S | no | deployment.yaml (Redis required, SYNC_FREQUENCY=60) — only shared-Redis topology documented/verified, not "no Redis" or "independent per-node Redis" |
| ops-deploy-docs-06 | Node identity and auditing | better | 3 | S | no | common/instance.go:6-16; router/main.go:38; deployment.yaml:88-96 — Prometheus instance_info + X-Lurus-Instance header, richer than a plain hostname string |
| ops-deploy-docs-07 | System instance registry and health reporting | partial | 2 | M | yes | doc/runbook/ha-deployment.md:60-80 — no system_instances DB table or admin-panel node list, only Prometheus /metrics |
| ops-deploy-docs-08 | Trusted proxy configuration | equivalent | 4 | S | no | config.go:88-140; trusted_proxies.go:22-27 |
| ops-deploy-docs-09 | Session authentication with multi-node consistency | partial | 4 | L | yes | v2_session_revoke.go:29-33 (explicit single-device model, no per-session store) — no Access/Refresh JWT pair with revocation tombstones |
| ops-deploy-docs-10 | Session issuance limits and rate limiting | not_applicable | 1 | L | yes | meaningless under single-device session model; delegated to platform IdP |
| ops-deploy-docs-11 | Secure HTTPS Origin validation for Refresh/Logout | partial | 3 | M | no | grep -rn 'SESSION_COOKIE_SECURE\|OriginGuard\|SESSION_COOKIE_TRUSTED' -> 0 hits — CORS/CSRF via ALLOWED_ORIGINS, not the specific OriginGuard pattern |
| ops-deploy-docs-12 | Setup wizard with initial configuration | equivalent | 4 | S | no | setup.go:27-80 (atomic TryClaimSetup, TOCTOU-hardened beyond upstream) |
| ops-deploy-docs-13 | Automatic database schema migration | better | 4 | S | no | migration/runner.go, runner_pg_test.go — PG advisory-lock serialization across multi-replica boot, tamper-evident audit migrations, drift metric |
| ops-deploy-docs-14 | Frontend option migration for deprecated settings | missing | 1 | S | no | grep -rn 'frontend_option_migration\|FrontendOptionMigration' -> 0 hits |
| ops-deploy-docs-15 | OpenAPI 3.0.1 specification | equivalent | 3 | S | no | docs/openapi/*.json/yaml; openapi_contract_lock_test.go — plus a CI contract-lock test upstream lacks |
| ops-deploy-docs-16 | Developer Makefile with multi-target build | partial | 2 | S | no | makefile:1-13 (only build-frontend, start-backend) — missing dev-api/dev/test/reset-setup targets |
| ops-deploy-docs-17 | Error logging with separate database | equivalent | 3 | S | no | repo/log.go:55,280; repo/main.go:379-383; error_log_middleware_test.go — ClickHouse arm is dead (see #25) |
| ops-deploy-docs-18 | Streaming timeout and request body size limits | equivalent | 3 | S | no | .env.example:310,313; rate_limit_headroom_strip_test.go:75-110 |
| ops-deploy-docs-19 | Electron desktop application with system tray | not_applicable | 1 | XL | no | electron/ directory deleted at HEAD — desktop wrapper conflicts with multi-tenant SaaS/OIDC delivery model |
| ops-deploy-docs-20 | io.net model deployment management API | equivalent | 2 | S | no | deployment.go:1-60 |
| ops-deploy-docs-21 | io.net client library (pkg/ionet) | equivalent | 2 | S | no | pkg/ionet |
| ops-deploy-docs-22 | Health check endpoint with database liveness | better | 3 | S | no | misc.go (GetStatus); api-router.go:30; docker-compose.yml:61-66; ha-deployment.md:19-24 — shallow liveness split from deep readiness (DB+breaker+leader+migration drift) |
| ops-deploy-docs-23 | Configurable cache backend (Redis or in-memory) | equivalent | 3 | S | no | docker-compose.yml:27; ha-deployment.md:28-30 |
| ops-deploy-docs-24 | Azure API version override | equivalent | 1 | S | no | common/init.go:153 (AZURE_DEFAULT_API_VERSION) |
| ops-deploy-docs-25 | Pyroscope profiling integration | equivalent | 2 | S | no | common/pyro.go:9-56; cmd/server/main.go:320 |
| ops-deploy-docs-26 | Batch update operations for operators | equivalent | 2 | S | no | repo/channel.go:948; repo/token.go:549,579,615-618; repo/user.go:829,857,929,949 (BATCH_UPDATE_ENABLED) |
| ops-deploy-docs-27 | Stale session cleanup | not_applicable | 1 | S | no | no per-session audit table exists to clean up under the single-device session model |
| ops-deploy-docs-28 | Per-node in-memory rate limiting fallback | equivalent | 2 | S | no | [skeptic flipped partial→equivalent: fallback exists pervasively across every rate-limit middleware (rate-limit.go:107-116, model-rate-limit.go:260-286,338, business_rate_limit.go:34-36,357, concurrency_limit.go:167, common/redis.go:17,31 — a real runtime toggle), mirroring upstream's per-node in-memory pattern with the same single-node caveat stated in code comments; current 3-replica topology just doesn't exercise it, same relationship upstream's own docs describe.] |
| ops-deploy-docs-29 | Analytics integration (Google Analytics, Umami) | partial | 1 | S | no | .env.example:415-416 declares UMAMI_WEBSITE_ID/GOOGLE_ANALYTICS_ID but grep -rn in web/src -> 0 hits, dead config |
| ops-deploy-docs-30 | Model deployment settings management endpoint | equivalent | 2 | S | no | deployment.go:28-40 |

### newhub-only capabilities cited in this domain
- ArgoCD automated+selfHeal deployment with auto-pin manifest commits — .github/workflows/docker-image-main.yml:1-40; ha-deployment.md (kubectl set image/rollout undo ineffective by design).
- Leader-election gauge + per-pod metrics identity cross-check for HA drills — ha-deployment.md:60-80.
- WAL-G continuous PG backup baked into single-node compose — docker-compose.yml:75-90; archive-wal.sh.
- Atomic setup-claim (TOCTOU-safe) vs upstream's check-then-create — setup.go:50-57.
- OpenAPI contract-lock CI test — openapi_contract_lock_test.go:432.
- Shallow-liveness / deep-readiness probe split with documented PG-fan-out risk — ha-deployment.md:19-24.
- Migration-drift Prometheus signal — CLAUDE.md lurus_gateway_schema_migrations_pending; health.go checks.schema_migrations.

---

## newhub-only capabilities (deduplicated across domains)

- **Governance audit hash chain (tamper-evident, row_hash+prev_hash linkage)** — internal/app/governance/chain_verify.go (VerifyAuditChain, chain_verify.go:80); internal/domain/entity/audit_event.go; internal/domain/entity/cov_audit_hash_test.go; internal/pkg/migration/audit_tamper_pg_test.go; migration 024_audit_events_tamper_evidence.sql. Cited in: providers-channels, billing-pricing, topup-payments-subscriptions, auth-security, console-ux, logs-analytics-observability.
- **Cost-spike circuit breaker (per-user/tenant token-rate window)** — internal/app/cost_spike.go:31,47,54,70 (QueryCostSpikeWindow/RecordCostSpikeWindow/CostSpikeLimit); internal/adapter/middleware/cost_spike.go. Cited in: wire-formats, providers-channels, billing-pricing, auth-security, routing-resilience-limits, tasks-plugins (as part of the MJ/task enforcement chain).
- **Tenant credit pools with debit/credit draw ledger, overdraft tracking and threshold alerting** — internal/domain/entity/tenant_credit_pool.go:8-35 (PoolResetPeriod enum, PoolMaxBalanceUnlimited, PoolDrawReasonRelayDebit/Topup/Reset/Adjustment/Overdraft); internal/app/quota.go:759,856 (debitTenantPool, maybeAlertPoolThreshold); internal/app/credit_pool.go:16-24. Cited in: providers-channels, billing-pricing, topup-payments-subscriptions, console-ux.
- **Tenant model allowlist (observe/enforce policy)** — internal/app/tenantpolicy/allowlist.go:1-37,70,91,115; internal/adapter/handler/tenant_model_allowlist.go:33-175. Cited in: billing-pricing, auth-security, routing-resilience-limits.
- **Multi-tenant channel/log/session/redemption isolation (TenantId scoping enforced at query layer, not just route prefix)** — internal/domain/entity/channel.go:16; internal/adapter/repo/log.go:74-98; internal/adapter/handler/v2_sessions.go:11-30; internal/adapter/handler/redemption.go:22-27. Cited in: providers-channels, routing-resilience-limits, console-ux, logs-analytics-observability, auth-security.
- **Tenant→Project→Token cost-attribution hierarchy with IDOR-hardened isolation** — internal/adapter/handler/v2_project.go; internal/adapter/handler/v2_token_rotate_idor_test.go; migration 029. Cited in: providers-channels, console-ux.
- **Platform wallet pre-authorization / idempotent platform-funded credit pool** — internal/app/pre_consume_quota.go:42-71 (platformPreAuthorize, releasePlatformPreAuth); internal/adapter/handler/internal_credit_pool_fund.go:14-40 (event_id UNIQUE idempotency, migration 031). Cited in: billing-pricing, topup-payments-subscriptions.
- **Idempotent platform-driven redemption-code batch provisioning** — internal/adapter/handler/internal_provisioned_redemptions.go:24-40; internal/domain/entity/provisioned_redemption_batch.go:1-28; migration 027. Cited in: topup-payments-subscriptions.
- **Anonymous relay-token-scoped redemption/top-up (no full user session required)** — internal/adapter/handler/switch_user_topup.go:19-60; router/api-v2-router.go:304,317. Cited in: topup-payments-subscriptions.
- **Ownership-gated checkout-order status polling (prevents order_no enumeration)** — internal/adapter/handler/v2_billing.go:180-200. Cited in: topup-payments-subscriptions.
- **Cost-aware savings analyzer** — internal/app/savings/calc.go:1-60 (re-pricing engine estimating savings from routing to cheaper quality-equivalent models, conservative/aggressive scenarios). Cited in: billing-pricing.
- **Session affinity for prompt-cache locality (conversation-scoped, HMAC-hashed key, TTL, never resurrects disabled channels)** — internal/app/session_affinity.go:19-64; internal/app/channel_select.go:94-110. Cited in: routing-resilience-limits.
- **Hub smart-routing: live performance-based channel weight adjustment** — internal/adapter/repo/channel_cache.go:276-298 (hub.AdjustWeights). Cited in: routing-resilience-limits.
- **Per-token relay scope allowlist (opt-in replace semantics, nil vs empty-array distinction)** — internal/adapter/handler/v2_token.go:358,474-488. Cited in: auth-security.
- **Enforcement chain (cost-spike + entitlement + rate-limit + concurrency) mounted on every billed relay/task route including MJ/Suno/audio-music/v1beta** — internal/adapter/handler/router/relay-router.go:66,69,76-79,102,105,113-116,118-121,133-136,217-222,298-303. Cited in: wire-formats, tasks-plugins.
- **Circuit breaker registry per upstream channel with Prometheus state metrics** — internal/adapter/handler/relay.go:41-51. Cited in: wire-formats.
- **Free unmetered Claude token-counting endpoint** — internal/adapter/handler/router/relay-router.go:143. Cited in: wire-formats.
- **Separated 5-min vs 1-hour Claude prompt-cache-creation billing tiers with dedicated wire-semantics tests** — internal/pkg/dto/openai_response.go:277-278,347-353; usage_wire_semantics_test.go. Cited in: wire-formats.
- **OpenRouter free-model sync engine with circuit-breaker baseline** — internal/domain/entity/channel.go:43-44; internal/app/openrouter_sync/. Cited in: providers-channels, console-ux.
- **SecureVerificationRequired step-up auth (2FA-equivalent) on channel key reveal** — internal/adapter/handler/router/api-router.go:143. Cited in: providers-channels.
- **CSV+JSON cursor-paginated audit export with Link-header pagination** — internal/adapter/handler/v2_admin_audit.go:31-55. Cited in: auth-security, console-ux.
- **Task-level ownership check on async artifact proxy** — internal/adapter/handler/video_proxy.go:58-73. Cited in: tasks-plugins.
- **Quota refund on async task failure wired into billing/wallet path** — internal/adapter/handler/task_refund.go:34-49. Cited in: tasks-plugins.
- **Leader-election gated task polling (HA multi-replica safe)** — internal/adapter/handler/task.go:83-87. Cited in: tasks-plugins.
- **Cross-product cost attribution in log stats (by_product breakdown, source_product filter, cache token accounting)** — internal/adapter/handler/v2_log_stat.go:34-58; internal/adapter/repo/log.go:838. Cited in: logs-analytics-observability.
- **Prometheus-native leader/instance observability (Leader, LeaderTaskLastSuccess, InstanceInfo gauges)** — internal/pkg/metrics/instance.go:19-52. Cited in: logs-analytics-observability, ops-deploy-docs.
- **Detailed dependency health check with intentional-off-state classification (shallow liveness vs deep readiness)** — internal/adapter/handler/health.go:16-139; doc/runbook/ha-deployment.md:19-24. Cited in: logs-analytics-observability, ops-deploy-docs.
- **IDOR-hardened request-id log lookup (caller-ownership enforced)** — internal/adapter/repo/log.go:279,326. Cited in: logs-analytics-observability.
- **Schema-migration drift Prometheus signal cross-checked against /api/health** — internal/pkg/metrics/migrations.go. Cited in: logs-analytics-observability, ops-deploy-docs.
- **ArgoCD automated+selfHeal deployment with git-committed auto-pin manifests (no kubectl set image)** — .github/workflows/docker-image-main.yml; doc/runbook/ha-deployment.md. Cited in: ops-deploy-docs.
- **WAL-G continuous PostgreSQL backup baked into single-node Docker Compose** — deploy/single-node/docker-compose.yml:75-90; archive-wal.sh. Cited in: ops-deploy-docs.
- **Atomic setup-claim (TOCTOU-safe) vs upstream's plain check-then-create** — internal/adapter/handler/setup.go:50-57. Cited in: ops-deploy-docs.
- **OpenAPI contract-lock CI test (fails build on route/spec drift)** — internal/adapter/handler/router/openapi_contract_lock_test.go:432. Cited in: ops-deploy-docs.
- **Vendor-neutral OIDC + Passkey-branded identity fully delegated to platform IdP (no local password store)** — internal/adapter/handler/oauth.go. Cited in: auth-security, console-ux.

---

## Gap list by value (all partial + missing rows only, sorted by value desc, then effort asc: S<M<L<XL)

| id | feature | status | value | effort | migration |
|---|---|---|---|---|---|
| billing-pricing-02 | Model pricing configuration and updates | partial | 5 | M | no |
| topup-payments-subscriptions-01 | Top-up payment method discovery | partial | 5 | M | no |
| auth-security-09 | Session refresh and logout w/ CSRF | partial | 5 | M | yes |
| providers-channels-01 | Multi-provider channel support | partial | 5 | L | no |
| tasks-plugins-02 | Task Artifact Retrieval and Proxying | partial | 5 | L | yes |
| tasks-plugins-12 | OpenAI Responses Protocol | partial | 5 | L | yes |
| console-ux-13 | Billing and wallet topup | partial | 5 | L | no |
| tasks-plugins-01 | Task Submission and Status Tracking (Generic) | partial | 5 | XL | no |
| billing-pricing-14 | Tiered expression billing mode | missing | 5 | XL | no |
| topup-payments-subscriptions-03 | Epay payment gateway integration | missing | 5 | XL | yes |
| topup-payments-subscriptions-04 | Stripe payment flow | missing | 5 | XL | yes |
| topup-payments-subscriptions-11 | Subscription plans management | missing | 5 | XL | yes |
| auth-security-06 | Two-factor authentication (TOTP) | partial | 5 | S | no |
| console-ux-23 | User profile and account settings | partial | 4 | S | no |
| logs-analytics-observability-15 | Request tracing with request_id and upstream_request_id | partial | 4 | S | yes |
| billing-pricing-03 | Model pricing preview and conversion | missing | 4 | M | no |
| topup-payments-subscriptions-08 | Payment amount calculation | partial | 4 | M | no |
| topup-payments-subscriptions-32 | Payment compliance confirmation | missing | 4 | M | yes |
| auth-security-16 | Audit logging for admin operations | partial | 4 | M | no |
| auth-security-23 | Login flow verification (stateful 2FA/passkey flow tokens) | partial | 4 | M | yes |
| tasks-plugins-17 | Artifact Type and Validation | missing | 4 | M | yes |
| tasks-plugins-26 | Video Proxy with SSRF Protection | partial | 4 | M | no |
| routing-resilience-limits-10 | Channel affinity caching with regex-based rules | partial | 4 | M | no |
| logs-analytics-observability-08 | Per-model performance metrics aggregation | missing | 4 | M | yes |
| logs-analytics-observability-09 | Model performance query API | missing | 4 | M | no |
| topup-payments-subscriptions-13 | Quota reset scheduling | partial | 4 | L | no |
| topup-payments-subscriptions-16 | User subscription status | partial | 4 | L | yes |
| topup-payments-subscriptions-36 | Payment webhook verification and idempotency | partial | 4 | L | no |
| auth-security-08 | Login session management (list/revoke individual/revoke-others) | partial | 4 | L | yes |
| tasks-plugins-24 | Plugin Version History and Rollback | missing | 4 | L | yes |
| tasks-plugins-27 | Plugin Request Hook Signatures | missing | 4 | L | no |
| routing-resilience-limits-14 | HTTP/2 connection sharding | missing | 4 | L | yes |
| console-ux-04 | Performance metrics per model | partial | 4 | L | no |
| ops-deploy-docs-09 | Session authentication with multi-node consistency | partial | 4 | L | yes |
| wire-formats-03 | Responses Compact endpoint | missing | 4 | L | no |
| topup-payments-subscriptions-05 | Creem payment integration | missing | 4 | XL | yes |
| topup-payments-subscriptions-06 | Waffo payment gateway | missing | 4 | XL | yes |
| topup-payments-subscriptions-07 | Waffo Pancake hosted checkout | missing | 4 | XL | yes |
| topup-payments-subscriptions-12 | Subscription plan CRUD (admin) | missing | 4 | L | yes |
| topup-payments-subscriptions-14 | Group-based subscription upgrades/downgrades | missing | 4 | L | yes |
| auth-security-04 | Passkey registration and management (WebAuthn) | missing | 4 | XL | yes |
| auth-security-05 | Passkey login | missing | 4 | XL | yes |
| auth-security-17 | Permission catalog and roles (custom roles/per-resource overrides) | missing | 4 | XL | yes |
| auth-security-18 | Role-based authorization (RBAC) | partial | 4 | XL | yes |
| tasks-plugins-04 | Plugin Upload and Versioning | missing | 4 | XL | yes |
| tasks-plugins-16 | Dynamic Plugin Route Registration | missing | 4 | XL | no |
| billing-pricing-28 | Billing expression JavaScript evaluation | missing | 4 | XL | no |
| console-ux-14 | Subscription plans management | missing | 4 | XL | yes |
| providers-channels-14 | Advanced Custom channels | missing | 4 | XL | no |
| console-ux-01 | Multi-language UI support | partial | 3 | S | no |
| console-ux-24 | Passkey registration and authentication | missing | 3 | S | no |
| ops-deploy-docs-05 | Redis topology options for multi-node | partial | 3 | S | no |
| topup-payments-subscriptions-17 | Billing preference | missing | 3 | S | no |
| topup-payments-subscriptions-35 | Critical rate limiting on payments | missing | 3 | S | no |
| routing-resilience-limits-12 | Channel affinity switch-on-success/keep-on-disabled options | partial | 3 | S | no |
| routing-resilience-limits-22 | Multi-key channels (rotating mode) | partial | 3 | S | no |
| billing-pricing-21 | Upstream ratio synchronization | partial | 3 | S | no |
| tasks-plugins-18 | Admin Task Queries and Filtering | partial | 3 | S | no |
| wire-formats-32 | Conversion diagnostics reporting | missing | 3 | M | no |
| providers-channels-13 | Channel constraints and routing filters | missing | 3 | M | yes |
| topup-payments-subscriptions-10 | Payment method discount tiers | missing | 3 | M | no |
| topup-payments-subscriptions-15 | Wallet overflow policy | missing | 3 | M | yes |
| topup-payments-subscriptions-23 | Manual subscription binding (admin) | missing | 3 | M | yes |
| topup-payments-subscriptions-24 | Subscription quota reset (admin) | missing | 3 | M | no |
| topup-payments-subscriptions-30 | Daily check-in (sign-in rewards) | missing | 3 | M | yes |
| topup-payments-subscriptions-33 | Top-up history (admin view) | partial | 3 | M | no |
| auth-security-07 | Admin 2FA management (stats + force-disable) | partial | 3 | M | no |
| auth-security-20 | User session active/issuance limits | missing | 3 | M | yes |
| auth-security-25 | Account security event notifications | partial | 3 | M | no |
| auth-security-28 | Access token fingerprinting | missing | 3 | M | yes |
| tasks-plugins-09 | Plugin Enable/Disable | missing | 3 | M | no |
| tasks-plugins-10 | Plugin Runtime Status and Diagnostics | missing | 3 | M | no |
| tasks-plugins-14 | Plugin Protocol Observation and Polling | missing | 3 | M | no |
| tasks-plugins-19 | Task Artifact Content URL Construction | missing | 3 | M | no |
| tasks-plugins-20 | Dashboard Task Artifacts Endpoint | missing | 3 | M | no |
| tasks-plugins-28 | Batch vs Per-Task Plugin Modes | missing | 3 | M | no |
| tasks-plugins-31 | Plugin Fixture and Dry-Run Testing | missing | 3 | M | no |
| routing-resilience-limits-11 | Channel affinity statistics and management | missing | 3 | M | no |
| routing-resilience-limits-13 | HTTP protocol policy (HTTP/1 vs HTTP/2 auto-detect) | missing | 3 | M | yes |
| routing-resilience-limits-33 | Channel pin/override (origin task pinning) | missing | 3 | M | no |
| console-ux-03 | Model ranking/leaderboard view | missing | 3 | M | no |
| console-ux-07 | User 2FA (two-factor authentication) management | partial | 3 | M | no |
| console-ux-20 | System instances monitoring | missing | 3 | M | no |
| logs-analytics-observability-07 | Group-based flow quota tracking | missing | 3 | M | yes |
| ops-deploy-docs-11 | Secure HTTPS Origin validation for Refresh/Logout | partial | 3 | M | no |
| providers-channels-15 | Task Plugin channels | missing | 3 | L | yes |
| providers-channels-34 | Channel read-only field protection | partial | 3 | M | no |
| tasks-plugins-05 | Plugin Listing and Inspection | missing | 3 | L | no |
| tasks-plugins-15 | Plugin Routing Conflict Resolution | missing | 3 | L | no |
| tasks-plugins-23 | Task Plugin Channel Type | missing | 3 | L | yes |
| tasks-plugins-25 | Plugin Metadata and Schema Validation | missing | 3 | L | no |
| tasks-plugins-29 | Artifact Renderer Protocols | missing | 3 | L | no |
| tasks-plugins-33 | Plugin Generation and Routing Lifecycle | missing | 3 | L | no |
| routing-resilience-limits-32 | Channel filter constraints (task plugin, request path) | missing | 3 | L | no |
| console-ux-19 | System task scheduler | missing | 3 | L | yes |
| console-ux-31 | Affiliate/referral program | missing | 3 | L | yes |
| logs-analytics-observability-10 | Model rankings with growth tracking | missing | 3 | L | no |
| logs-analytics-observability-11 | Vendor rankings and top-movers dashboard | missing | 3 | L | no |
| auth-security-26 | Session metadata tracking | partial | 3 | L | yes |
| auth-security-29 | Session revocation with reasons | partial | 3 | L | yes |
| billing-pricing-16 | Per-provider billing expression overrides | missing | 3 | XL | no |
| console-ux-36 | Authorization/permission management | missing | 3 | XL | yes |
| billing-pricing-19 | Violation fee (CSAM) surcharge | missing | 2 | S | no |
| topup-payments-subscriptions-25 | Subscription invalidation (admin) | missing | 2 | S | no |
| topup-payments-subscriptions-31 | Check-in configuration (admin) | partial | 2 | S | no |
| tasks-plugins-06 | Plugin Icon Management | missing | 2 | S | no |
| tasks-plugins-11 | Plugin Factory Disable Settings | missing | 2 | S | no |
| tasks-plugins-32 | Task Plugin Options API | missing | 2 | S | no |
| tasks-plugins-36 | Legacy Video Result URL Handling | missing | 2 | S | no |
| tasks-plugins-37 | Plugin API Version and Schema Documentation | missing | 2 | S | no |
| routing-resilience-limits-18 | TLS verification control | missing | 2 | S | no |
| routing-resilience-limits-34 | Max token auto groups limit | missing | 2 | S | no |
| console-ux-32 | Daily check-in bonus | partial | 2 | S | no |
| logs-analytics-observability-18 | Runtime memory and GC statistics | missing | 2 | S | no |
| logs-analytics-observability-28 | Rankings cache with 5-minute TTL | missing | 2 | S | no |
| ops-deploy-docs-16 | Developer Makefile with multi-target build | partial | 2 | S | no |
| wire-formats-37 | Alpha Search web search endpoint | missing | 2 | M | no |
| providers-channels-16 | Sub2API and NewAPI nested gateways | missing | 2 | M | no |
| billing-pricing-29 | Billing expression smoke testing | missing | 2 | M | no |
| billing-pricing-30 | Pricing version tracking | missing | 2 | M | yes |
| auth-security-22 | Auth version tracking | missing | 2 | M | yes |
| tasks-plugins-08 | Plugin Marketplace Sources | missing | 2 | M | no |
| tasks-plugins-21 | System Tasks (Background Jobs) | missing | 2 | M | yes |
| tasks-plugins-34 | Task Artifact Store Backend | missing | 2 | M | no |
| tasks-plugins-35 | Task Plugin Submission Diagnostics | partial | 2 | M | no |
| routing-resilience-limits-30 | Performance monitoring thresholds | missing | 2 | M | no |
| console-ux-17 | Payment compliance attestation | missing | 2 | M | no |
| console-ux-18 | Performance stats and cache management | missing | 2 | M | no |
| console-ux-30 | Channel affinity cache stats | missing | 2 | M | no |
| logs-analytics-observability-22 | Performance stats endpoint with config info | missing | 2 | M | no |
| ops-deploy-docs-07 | System instance registry and health reporting | partial | 2 | M | yes |
| providers-channels-17 | Codex (ChatGPT Subscription) OAuth integration | missing | 2 | L | yes |
| topup-payments-subscriptions-34 | Waffo Pancake catalog and pairing (admin) | missing | 2 | L | yes |
| routing-resilience-limits-29 | Disk cache for large payloads | missing | 2 | L | no |
| logs-analytics-observability-12 | Model and vendor usage history time series | missing | 2 | L | no |
| logs-analytics-observability-27 | Per-model quota totals for rankings | missing | 2 | L | no |
| console-ux-21 | Task plugin management | missing | 2 | XL | yes |
| console-ux-37 | Waffo-Pancake subscription integration | missing | 2 | XL | yes |
| wire-formats-38 | Stream obfuscation control | missing | 1 | S | no |
| ops-deploy-docs-14 | Frontend option migration for deprecated settings | missing | 1 | S | no |
| ops-deploy-docs-29 | Analytics integration (Google Analytics, Umami) | partial | 1 | S | no |
| tasks-plugins-38 | Plugin Console Logging in Debug Mode | missing | 1 | S | no |
| logs-analytics-observability-29 | Admin performance stats reset and GC trigger | missing | 1 | S | no |
