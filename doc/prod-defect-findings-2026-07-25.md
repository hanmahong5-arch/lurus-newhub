# Production defects surfaced by the coverage loop — 2026-07-25

Base: `origin/main` @ `378d7eb3`. Every item below was found by a test written
during the coverage uplift loop (see `test-coverage-uplift-loop-2026-07-25.md`).

**None of them were fixed.** The loop's contract was test-only: writers were told
to leave a `// FINDING:` comment, assert the *current* behaviour so the baseline
is locked, and report. Fixing is a separate decision because it changes
production code.

Nine were independently re-verified at `file:line` by the orchestrator and are
marked **[CONFIRMED]**; the rest are as reported by the writer that found them
and should be re-read before anyone acts on them.

## Ranked by severity

| rank | location | class |
| --- | --- | --- |
| 1 | `handler/provisioning.go` `RevokeProvisionedKey` | **[CONFIRMED]** tenant isolation break |
| 2 | `setting/ratio_setting/model_ratio.go:432-439` | **[CONFIRMED]** platform-wide billing outage |
| 3 | `provider/claude/relay-claude.go:740-747` | **[CONFIRMED]** nil-pointer panic on relay |
| 4 | `provider/claude/relay-claude.go:762` | **[CONFIRMED]** nil-pointer panic on relay |
| 5 | `provider/claude/relay-claude.go:596-605` | **[CONFIRMED]** panic stalls a stream silently |
| 6 | `repo/option.go:211-224` | **[CONFIRMED]** silent write loss on pricing config |
| 7 | `handler/v2_models_write.go:34-43,112-117` | **[CONFIRMED]** model prices silently dropped |
| 8 | `repo/token_cache.go:31` | money — quota decrement silently dropped |
| 9 | `pkg/search/client.go:246-263` | **[CONFIRMED]** retry loop can run the operation zero times |
| 10 | `provider/claude/relay-claude.go:250-253` | **[CONFIRMED]** range-copy bug: empty role reaches upstream |
| 11 | `pkg/dto/openai_request.go:139-142` | billing — token estimate undercounts |
| 12 | `provider/gemini/relay-gemini.go:462-513` | user text silently dropped before upstream |

Full detail for every finding follows, in the order they were reported.

---

### internal/pkg/tracing/tracing.go:73-84

**Defect** — With the go.mod-pinned OpenTelemetry SDK, resource.Default() now advertises the SDK-embedded semconv schema URL (observed: https://opentelemetry.io/schemas/1.40.0) while tracing.go explicitly builds a second resource with the pinned `semconv "go.opentelemetry.io/otel/semconv/v1.26.0"` import. resource.Merge deterministically rejects mismatched schema URLs.

**Expected vs actual** — Expected: Init(ctx, Config{Enabled:true,...}) succeeds and wires a real tracer/exporter whenever OTEL_TRACING_ENABLED=true. Actual (locked in by TestCoreAppBootTracing_Init_EnabledCurrentlyFailsOnSchemaConflict, reproduced deterministically, no network involved): Init always returns a non-nil 'conflicting Schema URL' error before ever setting enabled/tracer/tracerProvider, for every SampleRate/Endpoint combination tried.

**Impact** — Setting OTEL_TRACING_ENABLED=true in any environment (including production) currently does nothing: distributed tracing silently never turns on. cmd/server/main.go:583-587 treats the error as non-fatal (logs 'Failed to initialize tracing' and continues boot), so the failure is easy to miss in a busy startup log and nobody gets traces despite the flag being set.

### internal/pkg/common/redis.go:246-274

**Defect** — RedisIncr only issues the INCR when the target key's current TTL is > 0; if the key has no TTL it silently returns nil without incrementing anything. checkRedisLimit's first call initializes the counter via RedisSet(key, "1", getDuration()) — if getDuration() is 0 (NOTIFICATION_LIMIT_DURATION_MINUTE=0), the key is written with no expiration, so every subsequent RedisIncr call on it is a silent no-op.

**Expected vs actual** — Expected: the per-window notification counter increments on every call under the configured limit, blocking once NotifyLimitCount is reached. Actual (locked in by TestCoreAppBootCheckRedisLimit_ZeroDurationNeverBlocks_FINDING): with duration=0 the counter is written once as "1" and never increments again — checkRedisLimit allows every single call forever for that key (which itself never expires).

**Impact** — If NOTIFICATION_LIMIT_DURATION_MINUTE is ever misconfigured to 0, the Redis-backed notification rate limit is silently and permanently bypassed (unlimited notifications, e.g. billing/quota-threshold alert spam) instead of falling back to a safe default — and the underlying RedisIncr no-op-on-no-TTL behavior is a landmine for any other caller relying on it to increment a persistent key.

### internal/lifecycle/graceful_shutdown_test.go

**Defect** — These pre-existing timing-budget tests intermittently fail when the host is under heavy concurrent build/test load (observed across 3 separate full-package runs this session): 'no requests were handled', 'expected DeadlineExceeded, got: <nil>', 'expected counter between 5-15, got 3'. Isolated re-runs of just these tests pass cleanly, confirming it's load-induced scheduling delay, not a logic bug or something introduced by my new files.

**Expected vs actual** — Expected: deterministic pass on every -count=1 run. Actual: ~2 of 3 full-package runs this session had one of these three tests fail under concurrent load from other build/test processes on the same machine.

**Impact** — Noisy/flaky CI signal for internal/lifecycle under load, and a real risk of false-red merges. Per task instructions I did not modify these tests; flagging per the 'known-flaky, report don't fix' guidance.

### internal/adapter/repo/option.go:211-224

**Defect** — UpdateOption 调用 DB.FirstOrCreate(&option, ...) 和 DB.Save(&option) 时完全丢弃两者返回的 error，最终返回值只来自与数据库无关的 updateOptionMap()。

**Expected vs actual** — 预期：若 DB 写入(创建/持久化 Option 行)失败，调用方应能从返回的 error 感知失败。实际：即使 FirstOrCreate/Save 因表不存在、连接断开等原因失败，UpdateOption 仍返回 nil；已用 cov_repo-deep_option_findings_test.go 的 TestUpdateOption_SilentlySwallowsDBWriteError_FINDING 在真 PG 上实证——先删除 options 表模拟持久化失败，UpdateOption 仍返回 nil，且事后重建表核实该 key 确实 0 行落库，而 common.OptionMap 内存态却已被更新为新值。

**Impact** — 调用方(包括后台任务/管理端配置更新接口)会误以为配置修改成功，但实际未持久化；进程重启或其它副本 loadOptionsFromDatabase 时会读到旧值，造成配置与调用方预期不一致且无任何错误信号可供告警，属于静默数据丢失。按任务指示锁定现状、未修改被测代码。

### internal/pkg/setting/operation_setting/monitor_setting.go:26-35

**Defect** — CHANNEL_TEST_FREQUENCY env var is only applied when parsed value > 0; a value of "0" or a negative number is silently ignored instead of explicitly disabling auto-test-channel, and there is no way via this env var to turn auto-test off once it was previously enabled.

**Expected vs actual** — Expected: '0' is a plausible admin intent for "disable auto-test", or at least a documented/rejected input. Actual: any frequency <= 0 leaves whatever AutoTestChannelEnabled/Minutes state was already in memory untouched (locked by TestGetMonitorSetting_ZeroOrNegativeFrequencyIgnored).

**Impact** — An admin trying to disable periodic channel testing by setting CHANNEL_TEST_FREQUENCY=0 will not get the expected effect if the setting was already enabled from a prior config; the ambiguity could cause the automatic-channel-test background job to keep running against operator expectations.

### internal/pkg/setting/system_setting/passkey.go:46

**Defect** — The Origins field is treated as "unset" (and overwritten with ServerAddress) not only when it is an empty string, but also when it is the literal string "[]". This special-case string comparison is undocumented and fragile.

**Expected vs actual** — Expected: only a genuinely empty Origins value triggers the ServerAddress fallback. Actual: an admin-submitted literal "[]" (a plausible 'empty array' placeholder from a JSON-editing UI) is silently overwritten too (locked by TestGetPasskeySettings_OriginsPlaceholderTreatedAsUnset).

**Impact** — If any admin UI or migration ever persists "[]" as a deliberate 'no extra origins' value for WebAuthn/passkey login, it will be silently replaced by ServerAddress instead of being respected, potentially widening the accepted origin set for passkey authentication beyond what was configured.

### internal/pkg/logger/logger.go:87-96

**Defect** — When a log-rotation goroutine is already in flight (setupLogWorking==true), logCount is not reset and keeps incrementing indefinitely on every log call until the in-flight SetupLogger() call finishes and clears the flag.

**Expected vs actual** — Expected: logCount growth is bounded near maxLogCount (1,000,000) between rotations. Actual: if SetupLogger() is slow (e.g. filesystem contention) logCount can grow far past the threshold with no cap (locked by TestCheckLogRotation_SuppressedWhileAlreadyWorking).

**Impact** — Low severity in practice (Go int is 64-bit and rotation is normally near-instant), but under sustained slow-disk conditions this removes any bound on the counter used to decide 'is it time to rotate', so a stuck rotation could mask that log rotation is effectively stalled for a very long time.

### internal/adapter/provider/cohere/adaptor.go:35-37

**Defect** — ConvertOpenAIRequest dereferences `*request` unconditionally with no nil-check, unlike every sibling CN adapter (baidu, tencent, xunfei, zhipu, zhipu_4v) which all guard `if request == nil`.

**Expected vs actual** — Expected: return a clean business error (as siblings do) for a nil/malformed relay request. Actual: `requestOpenAI2Cohere(*request)` panics with a nil-pointer dereference, crashing the request-handling goroutine. Locked in with TestAdaptor_ConvertOpenAIRequest_NilRequestPanics (recover-based regression test).

**Impact** — Any relay path that can reach cohere's ConvertOpenAIRequest with a nil GeneralOpenAIRequest (e.g. an upstream conversion bug or a malformed pipeline stage) crashes instead of returning a 4xx, turning a client-input problem into an availability incident for that request.

### internal/adapter/provider/zhipu_4v/image.go:83-91

**Defect** — The per-image loop checks `url == ""` and `continue`s (dropping the entry, only logging zhipu_image_missing_url) BEFORE checking whether data.B64Json/B64Image is populated.

**Expected vs actual** — Expected: an entry that carries inline base64 image data (b64_json/b64_image, the OpenAI-style response_format=b64_json convention which legitimately omits url) should still be forwarded to the client. Actual: it is silently dropped whenever url and image_url are both empty, even though usable b64 image bytes are present. Locked in with TestZhipu4vImageHandler/missing_url_on_a_data_item_is_skipped,_not_fatal.

**Impact** — If the upstream Zhipu image-generation API (or a future response_format=b64_json mode) ever returns entries without a url field, the tenant is billed/consumes quota for image generation but receives an empty `data` array in the response -- a silent product/billing mismatch rather than a visible error.

### internal/adapter/provider/vertex/adaptor.go:349-351

**Defect** — ConvertRerankRequest silently returns (nil, nil) instead of an explicit "not implemented" error, unlike every sibling stub in the same file (ConvertEmbeddingRequest, ConvertAudioRequest, ConvertOpenAIResponsesRequest all return errors.New("not implemented")).

**Expected vs actual** — Expected: rerank calls routed to the vertex adaptor fail fast with a clear "not implemented" error, consistent with the other unsupported request types in this same file. Actual: the call succeeds with a nil payload and nil error, which -- unless every relay call site special-cases a nil converted-request -- risks marshaling an empty/garbage body and sending it upstream instead of returning a clean 4xx to the caller.

**Impact** — If a tenant is ever routed a rerank request through a Vertex AI channel (e.g. future model-routing misconfiguration), the request silently proceeds with no request body instead of erroring immediately, producing a confusing late-stage upstream failure instead of the fast, clear rejection every other unsupported vertex request type gives. Locked in via a regression test (TestAdaptor_StubConverters) with a FINDING comment; not fixed per instructions (would require changing non-test source).

### internal/adapter/handler/release.go:180-181

**Defect** — DownloadArtifact spawns a fire-and-forget goroutine (`go func(){ _ = releaseService.HandleDownload(...) }()`) that closes over the package-level var `releaseService`. Another swarm agent's test file swaps that global around each test (`prevService := releaseService; ...; defer func(){ releaseService = prevService }()`), and the deferred restore can run before the leaked goroutine executes, so the goroutine dereferences a nil/stale `releaseService` receiver.

**Expected vs actual** — Expected: `go test ./internal/adapter/handler/...` is deterministically green. Actual: reproduced a `panic: runtime error: invalid memory address or nil pointer dereference` at internal/app/release_service.go:226 with ONLY that other file's release/download tests running (isolated by temporarily removing all 7 of my files); a full-package run is therefore flaky — it passed twice (62.4%→63.2%→63.3%) and failed twice across my repeated full-suite runs, purely from goroutine-vs-defer scheduling, not from any content difference in my files.

**Impact** — Not a production defect (production never re-swaps `releaseService` mid-request), but it makes the package's `go test` suite non-deterministic in CI/local, which can mask a real regression under it or cause spurious pipeline failures. Out of my assigned identity/token/log/audit scope and in someone else's already-existing _test.go, so I did not modify it per the hard constraint — flagging for the owning agent/owner to add goroutine synchronization (e.g. WaitGroup) or make the test not race the global.

### internal/adapter/provider/api_request.go:318-319

**Defect** — doRequest unconditionally calls req.Body.Close() and c.Request.Body.Close() on the success path (after a successful client.Do) with no nil check. A request built the idiomatic client-side way for a bodyless call — http.NewRequestWithContext(ctx, method, url, nil) — leaves req.Body == nil, so a request that otherwise succeeds panics instead of returning a clean error.

**Expected vs actual** — Expected: a nil req.Body should be a no-op on close (or be guarded), returning the response normally. Actual: `_ = req.Body.Close()` panics with 'invalid memory address or nil pointer dereference'. Locked down as a regression baseline in TestDoRequest_NilRequestBodyPanicsOnSuccess_Finding (internal/adapter/provider/cov_prov_api_request_test.go), which currently asserts the panic occurs — that test must be updated if the underlying bug is ever fixed.

**Impact** — provider.DoRequest is called directly (not via DoApiRequest) by internal/adapter/provider/jimeng/adaptor.go and internal/app/relay/claude_count_tokens.go, both of which build their own *http.Request; jimeng's builder forwards whatever requestBody its caller supplies via http.NewRequest(method, url, requestBody). Any current or future GET-style/bodyless call reaching provider.DoRequest with a nil body would crash the handling goroutine with an unrecovered panic instead of returning a normal upstream error, turning what should be a clean error response into a hard crash for that request.

### internal/adapter/provider/volcengine/adaptor.go SetupRequestHeader

**Defect** — When RelayMode is audio speech and ApiKey does not split into exactly 2 pipe-separated parts (malformed 'appid|token'), the inner Authorization-header set is skipped, and the function returns early (`return nil`) before the fallback `req.Set("Authorization", "Bearer "+info.ApiKey)` at the bottom of the function can run.

**Expected vs actual** — Expected: a malformed channel API key should either error out at setup time or fall back to some Authorization header. Actual: the request is sent upstream with NO Authorization header at all, silently failing open into an upstream 401/403 rather than surfacing a clear local error.

**Impact** — An operator who misconfigures a Volcengine TTS channel key (missing the required appid|token pipe format) gets a confusing opaque upstream auth failure instead of an immediate, actionable local validation error. Locked in as a regression baseline via TestProvOllamaVolc_VE_SetupRequestHeader_AudioSpeech_MalformedApiKey_SendsNoAuthHeader (volcengine) and the analogous FINDING in ollama's SetupRequestHeader test — not fixed, per instructions not to alter non-test files.

### internal/adapter/provider/aws/relay-aws.go:108-120

**Defect** — In doAwsClientRequest's Nova-model branch, a local `awsReq := &bedrockruntime.InvokeModelInput{...}` is built and its Body marshaled, but -- unlike the Claude stream/non-stream branches immediately below it, which both explicitly do `a.AwsReq = awsReq` -- this branch never assigns the built request onto the adaptor. `a.AwsReq` is left at its zero value (nil interface).

**Expected vs actual** — Expected: after DoRequest builds a Nova request, a.AwsReq should hold the *bedrockruntime.InvokeModelInput so DoResponse can execute it. Actual: a.AwsReq stays nil; DoResponse's Nova path (handleNovaRequest) does `a.AwsClient.InvokeModel(ctx, a.AwsReq.(*bedrockruntime.InvokeModelInput))`, a single-return type assertion on a nil interface, which panics the request goroutine instead of returning a clean error. Locked in as a regression baseline by TestAdaptor_DoRequest_AKSKMode_NovaModelDoesNotPersistAwsReq_FINDING (recovers the panic and fails the test if it stops panicking).

**Impact** — Any AWS Bedrock channel configured with AK/SK authentication (ClientModeAKSK, i.e. not the newer bearer-API-key mode) serving a 'nova-*' model crashes the serving goroutine on every request instead of returning an error response to the caller -- a live-traffic outage for that specific channel+model combination, discovered only by this test rather than existing coverage.

### internal/adapter/provider/dify/relay-dify.go:153-158

**Defect** — For a non-system/assistant (i.e. user) message containing an image_url content block, the code declares `var file *DifyFile` (nil) and then, when `media.IsRemoteImage()` is true (URL starts with "http"), writes directly through the nil pointer: `file.Type = media.MimeType; file.TransferMode = "remote_url"; file.URL = media.Url` -- without ever allocating `file = &DifyFile{}` first (the else branch, which calls uploadDifyFile for non-remote/base64 images, does get a non-nil pointer back).

**Expected vs actual** — Expected: a remote (http/https) image_url content block in a user message should build a DifyFile{Type, TransferMode:"remote_url", URL} and append it to the request's Files list. Actual: dereferencing the nil `file` pointer panics immediately with a nil-pointer dereference, crashing the request goroutine. Locked in as a regression baseline by TestRequestOpenAI2Dify_UserRemoteImage_FINDING_NilPointerPanic (recovers the panic and fails the test if it stops panicking).

**Impact** — Any Dify channel request whose user message includes a remote (http/https) image URL -- the common case for multimodal chat via image_url -- crashes the serving goroutine on every such request instead of relaying it, a total functional outage for that traffic class on any Dify-backed channel.

### internal/adapter/handler/internal_backfill.go:59

**Defect** — When a UserIdentityMapping resolves to a platform account whose ID is 0 (a degenerate/placeholder account response), the handler still records that user in `userToAccount` (map key exists), so `users_matched` counts it as a successful match even though the update loop's `accountID <= 0` guard correctly refuses to write it to the token.

**Expected vs actual** — Expected: users_matched should only count users whose account resolution is actually usable (accountID > 0). Actual: users_matched=1 while tokens_updated=0 for this case — the two counters diverge in a way that looks like a partial-failure but the API response gives no signal that the 'match' was unusable.

**Impact** — cosmetic/observability — no money or tenant-isolation impact (the write guard still prevents a token from ever getting IdentityAccountID<=0), but an operator monitoring users_matched vs tokens_updated after a backfill run could be misled into thinking the mismatch is a bug in the update path rather than an upstream data quality issue. Locked as TestInternalBackfillTokenAccountIDs_ZeroAccountIDNotApplied (marked FINDING in the test) instead of changing the handler.

### internal/pkg/common/init.go:139-175

**Defect** — initConstantEnv only overwrites constant.TaskPricePatches when the TASK_PRICE_PATCH env var is non-empty; there is no else-branch that clears it.

**Expected vs actual** — Expected: calling initConstantEnv with TASK_PRICE_PATCH unset resets/clears any previously-set TaskPricePatches to reflect 'no override configured'. Actual: a previously-set value (e.g. from a prior call in the same process) is left untouched — locked in as current behavior by TestInitConstantEnv_EmptyTaskPricePatchLeavesFieldUntouched.

**Impact** — Not exploitable today since InitEnv/initConstantEnv is only ever called once at process boot in production. Would become a stale-config bug if the code is ever changed to support hot-reload of env-driven pricing patches without a process restart — a stale per-model price patch could silently persist after an operator intended to remove it.

### internal/adapter/handler/provisioning.go: RevokeProvisionedKey

**Defect** — CreateProvisionedKey and ListProvisionedKeys both call repo.InternalKeyAllowedForTenant(apiKey, tenant.Id) to enforce the (api_key, tenant) whitelist before acting. RevokeProvisionedKey does NOT — it only checks that the token's TenantId matches the tenant resolved from the URL slug, with no whitelist gate on the calling API key at all.

**Expected vs actual** — Expected: a Reseller's narrow provisioning key, whitelisted for tenant-alpha only, gets 403 TENANT_NOT_AUTHORIZED when it tries to act on tenant-beta, same as Create/List. Actual: TestRevokeProvisionedKey_NarrowKeyNotWhitelistedForTenant (this PR) proves the narrow key successfully revokes a tenant-beta token (200 OK, token soft-deleted) as long as it can address the correct beta slug + key_id.

**Impact** — Cross-tenant credential sabotage: a Reseller (or a party holding a leaked narrow provisioning key meant for one customer) can knock out a sibling tenant's live API tokens by enumerating small integer key_ids against that tenant's slug — a denial-of-service on another customer's production traffic, and a tenant-isolation break in the money-adjacent provisioning surface.

### internal/adapter/handler/setup.go:84

**Defect** — The 1-12 username-length validation uses Go's len(string), which counts UTF-8 BYTES, not runes/characters. The Chinese rejection message ('用户名长度必须在1-12个字符之间') literally promises a character-count bound.

**Expected vs actual** — Expected: a 5-character Chinese username (well within '1-12 characters') passes validation. Actual: TestPostSetup_UsernameLength_ByteLengthNotRuneLength shows a 5-rune CJK username (15 UTF-8 bytes) is rejected with the length-bound error, because 15 > 12 in byte terms.

**Impact** — First-run bootstrap (root admin account creation) silently rejects legitimate short Chinese usernames for a Chinese-market product, with an error message that describes a rune-count rule the code doesn't actually implement — a first-impression onboarding bug, not money-path but customer-facing on day one.

### internal/adapter/handler/health.go:45

**Defect** — When RedisEnabled=true but RDB is nil (a mis-initialized/never-connected client), the health check reports checks.redis="disabled", identical to the intentional off state, rather than a distinct label like "not_configured"/"error".

**Expected vs actual** — Expected: an operator reading /api/health can tell 'Redis intentionally off' apart from 'Redis wiring is broken'. Actual: TestGetHealthDetailed_RedisEnabledButClientNil_ReportsDisabled shows both collapse to the same "disabled" string.

**Impact** — Low severity / observability gap: masks a real misconfiguration behind a benign-looking label, potentially delaying detection of a broken Redis-backed session/quota-cache path in production.

### internal/adapter/handler/playground.go:56-60

**Defect** — 当调用者用 access token 访问 Playground 时，handler 构造错误用 types.NewError(err, types.ErrorCodeAccessDenied, types.ErrOptionWithSkipRetry())，没有附带任何状态码 option，NewError 默认回落到 http.StatusInternalServerError(500)。

**Expected vs actual** — 期望：access-denied 语义应返回 4xx（如 401/403），便于客户端区分“权限不足”与“服务端故障”；实际：返回 500，与真正的服务端异常无法区分。已在 cov_handler-relay_edge_test.go 的 TestHandlerRelay_Playground_AccessTokenDenied 中锁定当前行为（断言 500），并加了 FINDING 注释，未改动源码。

**Impact** — 客户端/监控系统按状态码分类告警时，会把这类纯粹的权限拒绝误计为服务端错误（拉高错误率指标、触发不必要的 on-call），且前端无法用标准的 401/403 处理逻辑（如跳转登录）区分处理。

### internal/adapter/repo/token_cache.go:31 cacheIncrTokenQuota

**Defect** — Incrementing a token's cached RemainQuota on a key that was never cacheSetToken'd (or whose hash currently has no TTL) returns nil (success) while performing NO write — RedisHIncrBy gates the whole operation on the target key already carrying a TTL>0.

**Expected vs actual** — Expected: either an error surfaces, or the increment is applied unconditionally (mirroring the caller's presumed intent to adjust quota). Actual: err==nil and RemainQuota is left completely unchanged — a caller checking only `err == nil` cannot distinguish 'quota adjusted' from 'cache entry absent, nothing happened'. Locked by TestCacheIncrTokenQuota_OnUncachedKeyIsSilentNoop and TestCacheSetToken_ZeroSyncFrequencyLeavesEntryWithoutTTL in cov_repo_token_cache_test.go.

**Impact** — Money path (quota tracking): if a decrement is silently dropped due to a cold/expired cache entry while the caller believes it succeeded, the cached remaining quota can drift out of sync with the DB's authoritative value — under-decrementing lets a token spend more than intended until the next DB read reconciles it.

### internal/domain/entity/redemption.go:8 TenantId field

**Defect** — Redemption.TenantId carries `gorm:"default:'default'"`. Creating a row with the Go zero value (empty string "") via GORM's Create() omits the column from the INSERT entirely, so the database's own default clause fires — the persisted tenant_id is the literal string "default", never "".

**Expected vs actual** — A caller intending to seed/mark a row with an explicit empty tenant_id (e.g. an orphan/unlinked marker distinct from the real 'default' tenant) instead silently gets the row folded into the 'default' tenant's data. Locked by TestDeleteInvalidRedemptionsByTenant_EmptyTenantIDInputGetsCoercedByGORMDefault in cov_repo_redemption_tenant_test.go (asserts stored TenantId=="default" after Create with TenantId="").

**Impact** — Same class of risk as the previously-fixed tenant-id-drift bug (2026-06-25 root-fix doc): if any code path relies on TenantId=="" as a distinguishable sentinel (vs. the real 'default' tenant), GORM's default-tag coercion silently merges that data into the 'default' tenant's namespace instead — a soft form of tenant-boundary blur, not a hard IDOR, but a data-hygiene trap for any future migration/backfill code.

### internal/pkg/setting/ratio_setting/model_ratio.go:432-439

**Defect** — UpdateModelRatioByJSONString unconditionally resets the live in-memory modelRatioMap to an empty map BEFORE attempting to parse the caller-supplied JSON. If the JSON is malformed, the parse fails and the handler correctly reports success:false — but by then every model's real billing ratio has already been wiped from memory.

**Expected vs actual** — Expected: a rejected/failed update leaves the previously-configured model ratios untouched (fail closed, no side effect). Actual: a single malformed admin request (verified with a real handler call in TestCovHandlerPricing_UpdateOption_ModelRatio_MalformedJSON_WipesLiveState) zeroes every model's ratio in memory while the API response claims nothing succeeded, giving the operator no indication that billing state was just destroyed.

**Impact** — money loss / billing correctness: after this happens, every relay request is priced using an empty ratio map (falls back to whatever the ratio lookup's default/not-found behavior is) until an admin notices and calls ResetModelRatio or otherwise re-seeds it — a narrow malformed-input mistake becomes a platform-wide billing outage with a misleading "failed, nothing changed" error message.

### internal/adapter/handler/v2_models_write.go:34-43

**Defect** — createModelV2Req accepts model_ratio, completion_ratio, model_price, and enable_groups from the caller (all documented with json tags, no binding to reject them), but CreateModelV2 never copies any of them into the persisted repo.Model — the entity has no such columns and the fields are dropped on the floor.

**Expected vs actual** — Expected: either the fields are persisted somewhere (e.g. written into ratio_setting/model_ratio config) or the API rejects/ignores them with a visible warning. Actual: verified with a real handler call (TestCovHandlerPricing_CreateModelV2_PricingFieldsSilentlyDropped) that POSTing model_ratio=999.5, completion_ratio=2.5, model_price=3.3, enable_groups=["default","vip"] still returns HTTP 201 success, and the reloaded row plus the modelV2View response show enable_groups=null and no model_ratio/completion_ratio/model_price fields at all — the submitted pricing values vanish with zero error or warning.

**Impact** — money loss / tenant confusion: a tenant admin using POST /api/v2/:tenant_slug/models to create a model with a specific price believes they have set that model's billing rate; in reality the model bills at whatever the platform's default/global ratio for that model name happens to be (or is unconfigured), which can silently under- or over-charge relative to what the admin intended and believes was configured.

### internal/pkg/dto/openai_request.go:139-142

**Defect** — The `if message.Name != nil { NameCount++; texts = append(..., *message.Name) }` block is nested INSIDE `if message.Content != nil`. A message that carries a Name but has nil Content (e.g. a tool-role message whose content was stripped/omitted) silently fails to have its Name counted in NameCount or included in the token-estimation CombineText.

**Expected vs actual** — Expected: Name should be counted/included independent of whether Content is nil, since Name is semantically present regardless. Actual: for Message{Name: &name, Content: nil}, NameCount stays 0 and the name text never enters CombineText, undercounting tokens used for billing/quota estimation. Verified with test TestGeneralOpenAIRequest_GetTokenCountMeta/message_name_only_counted_when_content_non-nil in cov_openai_request_test.go.

### internal/pkg/dto/openai_request.go:977-988

**Defect** — MediaInput has a `Detail` field (json tag `detail`) and GetTokenCountMeta() later reads `input.Detail` to populate FileMeta.Detail, but the input_image branch of ParseInput() never reads `item["detail"]` from the source JSON, so MediaInput.Detail is always left as the zero value.

**Expected vs actual** — Expected: a client-supplied `"detail":"high"` on a Responses-API input_image content part should propagate through to FileMeta.Detail (used for image token/price-ratio tiering downstream). Actual: FileMeta.Detail is always empty string for any Responses-API image input, silently discarding the client's requested detail level. Verified with test TestOpenAIResponsesRequest_GetTokenCountMeta/input_image_with_url_contributes_file_meta_not_text in cov_openai_request_test.go.

### internal/pkg/dto/error.go:53-68

**Defect** — json.RawMessage captures the literal 4 bytes `null` for a JSON "error": null field, so len(e.Error) > 0 is true and common.GetJsonType returns "null" (not "object"/"string"), which falls into the `default: return string(e.Error)` branch instead of falling through to check e.Message/e.Msg/e.Err/e.ErrorMsg/etc.

**Expected vs actual** — Expected: an explicitly-null "error" field in an upstream error payload should be treated the same as an absent error field, falling through to the other message fields (Message/Msg/Err/ErrorMsg/Header.Message/Response.Error.Message). Actual: callers receive the literal string "null" instead of the real fallback message, e.g. {"error": null, "message": "real message"} returns "null" instead of "real message" -- confusing/wrong if surfaced to end users, logs, or billing error records. Verified with test TestGeneralErrorResponse_ToMessage/error_as_JSON_null_is_treated_as_non-empty_raw_bytes,_masking_fallback_fields in cov_misc_test.go.

### internal/pkg/search/logs_index.go:99-120

**Defect** — LogDocument carries ChannelType/RelayMode/UpstreamModel/TotalLatencyMs (the 'governance' fields added under that comment), and Log has matching fields, but ConvertDocumentToLog never copies them back (nor Other). SearchLogs calls ConvertDocumentToLog on every search hit, so any caller reading these fields off a search result always sees zero values regardless of what was actually indexed.

**Expected vs actual** — Expected: a round trip through ConvertLogToDocument -> ConvertDocumentToLog preserves all shared fields, matching how every other field behaves. Actual: ChannelType, RelayMode, UpstreamModel and TotalLatencyMs are silently dropped to their zero values on every SearchLogs call. Reproduced in TestConvertDocumentToLog_DropsGovernanceFields in cov_logs_index_test.go.

### internal/pkg/search/config.go:246-263

**Defect** — The retry loop is `for i := 0; i < RetryCount; i++`. When RetryCount is 0 (the package var's zero value before loadConfig ever runs, or an operator setting MEILISEARCH_RETRY_COUNT=0 meaning 'no retries, just try once') the loop body never executes, so `fn` — the actual index/search HTTP call — is never attempted at all.

**Expected vs actual** — Expected: RetryCount<=0 should either run the operation exactly once, or fail fast with a clear configuration error. Actual: the operation is skipped entirely and the function falls through to `fmt.Errorf("failed after %d retries: %w", RetryCount, err)` with err still nil, producing a malformed message containing the fmt verb artifact `%!w(<nil>)` instead of a meaningful error. Reproduced in TestRetryWithBackoff_ZeroRetryCountNeverCallsFnAndReturnsGarbledError in cov_config_test.go.

### internal/adapter/provider/gemini/relay-gemini.go:462-513

**Defect** — The markdown-image scanning loop (`for { startIdx := strings.Index(text, "!["); ... text = text[closeIdx+1:] }`) never emits the text that trails the LAST markdown image in a message. After the loop exits (no more "![" found), the leftover `text` variable is only appended via the `if !hasMarkdownImage { parts = append(...) }` branch, which is false as soon as any image was found in the message.

**Expected vs actual** — Repro: a user message with content "before ![pic](data:image/png;base64,XXXX) after" is expected to produce 3 parts (text-before, image, text-after=" after"). Actual: it produces exactly 2 parts (text-before, image) -- the trailing " after" text is silently dropped and never reaches Gemini. This can silently strip trailing instructions/questions that follow an inline image in a prompt. Confirmed by test TestCovertOpenAI2Gemini_MarkdownImage_SplitsTextAndImage in internal/adapter/provider/gemini/cov_convert_openai_test.go (test asserts the actual buggy 2-part behavior with a `// FINDING:` comment above it).

### internal/adapter/provider/claude/relay-claude.go:762

**Defect** — `if claudeResponse.Usage.ServerToolUse != nil && ...` runs unconditionally after the requestMode switch, for BOTH RequestModeCompletion and RequestModeMessage. In completion mode, claudeInfo.Usage is computed from claudeResponse.Completion via the text estimator; claudeResponse.Usage (the raw *ClaudeUsage pointer from the JSON body) is never populated. The legacy Anthropic /v1/complete API (claude-2.x / claude-instant models, RequestModeCompletion) does not return a top-level "usage" object at all, so claudeResponse.Usage is nil and this line nil-pointer-panics on every real non-streaming completion-mode response.

**Expected vs actual** — Expected: a nil check (claudeResponse.Usage != nil && ...) matching the optional/omitempty nature of the field, or a handled bad_response_body-style error. Actual: guaranteed panic for the mainline completion-mode success path. Reproduced and confirmed in TestHandleClaudeResponseData_CompletionMode_NoUsageField_Panics (panic recovered locally only to document the defect without crashing the test binary).

### internal/adapter/provider/claude/relay-claude.go:740-747

**Defect** — `claudeInfo.Usage.PromptTokens = claudeResponse.Usage.InputTokens` (and the following 5 lines) unconditionally dereference claudeResponse.Usage, which is `*ClaudeUsage` with `json:"usage,omitempty"`. The preceding GetClaudeError() check only protects against an explicit Claude "error"-typed response; a JSON-valid but incomplete/malformed success-shaped body (missing "usage") panics here instead of returning a graceful error.

**Expected vs actual** — Expected: graceful bad_response_body error when usage is absent. Actual: nil-pointer panic; in the real ClaudeHandler call path (no local recover) this propagates to the global RelayPanicRecover middleware and surfaces as an ungraceful 500. Reproduced in TestHandleClaudeResponseData_MessageMode_MissingUsageAndError_Panics.

### internal/adapter/provider/claude/relay-claude.go:596-605

**Defect** — `claudeInfo.ResponseId = claudeResponse.Message.Id` and `claudeInfo.Usage.PromptTokens = claudeResponse.Message.Usage.InputTokens` unconditionally dereference claudeResponse.Message (*ClaudeMediaMessage, omitempty) and its nested Usage (*ClaudeUsage, omitempty). A malformed/truncated upstream "message_start" SSE event that omits "message" (or "usage" within it) panics instead of returning a handled error.

**Expected vs actual** — Expected: nil guard producing a parse error, mirroring how GetClaudeError() is handled for the sibling "error" event type. Actual: nil-pointer panic. In production this is caught by the SafeGo wrapper around the stream data-handler goroutine (helper/stream_scanner.go), silently stalling that individual SSE stream rather than crashing the process. Reproduced in TestFormatClaudeResponseInfo_MessageStart_NilMessage_Panics.

### internal/adapter/provider/claude/relay-claude.go:250-253

**Defect** — Intends to default an empty message.Role to "user" via `for i, message := range textRequest.Messages { if message.Role == "" { textRequest.Messages[i].Role = "user" } ; fmtMessage := dto.Message{Role: message.Role, ...}`. `message` is the per-iteration range copy taken BEFORE the mutation, so writing to `textRequest.Messages[i].Role` never updates the local `message.Role` used to build fmtMessage — the normalization is a no-op.

**Expected vs actual** — Expected: a single outgoing dto.ClaudeMessage with Role="user" for an empty-role input message. Actual: the empty role survives into the outgoing message sent upstream (Role=""), and because "" != "user" it also spuriously trips the 'first message must be user' placeholder-injection logic, producing an extra injected user message. Confirmed in TestRequestOpenAI2ClaudeMessage_EmptyRoleDefaultsToUser (2 messages returned instead of 1, second one still Role="").

### internal/adapter/provider/openai/relay-openai.go:328

**Defect** — streamTTSResponse is dead code: grep -rn "streamTTSResponse" internal/ only matches its own definition and one comment inside itself - no caller anywhere in the module (not from OpenaiTTSHandler, which returns a non-streaming response, nor anywhere else).

**Expected vs actual** — Expected: a function this elaborate (flusher-vs-fallback streaming copy loop) to be wired into a real streaming-TTS response path. Actual: it is unreachable in production; I added a direct unit test (TestStreamTTSResponse_CopiesBodyBytesToClient / _EmptyBody_WritesNothing) to lock in its behavior in case it's wired up later, but left the !ok (non-flusher) fallback branch untested because it is unreachable even from that direct call: gin.ResponseWriter's interface embeds http.Flusher, so any value assignable to gin.Context.Writer already satisfies the type assertion inside the function - not fixed, since fixing/wiring it is a business-code change outside this task's scope.

### internal/adapter/handler/provisioning.go RevokeProvisionedKey

**Defect** — RevokeProvisionedKey does not call repo.InternalKeyAllowedForTenant, unlike CreateProvisionedKey and ListProvisionedKeys which both enforce that whitelist gate.

**Expected vs actual** — Expected: a provisioning key scoped to tenant A should be rejected when used to revoke a token belonging to tenant B (matching Create/List behavior). Actual: RevokeProvisionedKey only checks tenant slug resolution, not the (api_key, tenant) whitelist, so a tenant-A-scoped key can revoke a live tenant-B token.

**Impact** — Cross-tenant IDOR on the token-revocation money path: any caller holding a narrowly-scoped provisioning key for one tenant can disable another tenant's production API keys, causing denial of service to a paying tenant they should have zero authority over.
