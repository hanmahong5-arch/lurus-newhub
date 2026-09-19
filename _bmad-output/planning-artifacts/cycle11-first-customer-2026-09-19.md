# 第十一轮 — 第一个客户会撞上的问题（cycle 11）

**基线.** `main @ cb79e310`（PR #186 合入后）。计划日期 2026-09-19。零 migration。

---

## 1. 为什么是这一批

第十轮（PR #186）把"后端做好了、控制台不读"那一类补完之后，2026-09-17 做了一次只读审计
（15 个只读代理 + 逐条对 HEAD `cb79e310` 亲手复核 + UAT 实证）。结论：功能面已不是短板
（342 行对照矩阵又有约 45 行转为已实现，剩余 missing 大半是平台侧已有或刻意不做），
**真正会砸在第一个企业客户身上的是一批今天就在代码里的诚实性/可用性缺陷**，每条 S–M。

前提事实：生产 30 天 12,772 次调用里 12,758 次是我们自己的探活，真人 14 次。
所以这些是"第一个客户会撞上"，不是"正在伤害客户"。

上一版计划（09-03 缺陷批次 + M1–M5）已全部交付（faultsim / alert_wiring_honesty_test /
first_response_single_writer_test / provider_billing_census_test 均在 HEAD）。

### 触发本轮的事实（均已亲手核实，file:line 对 `cb79e310`）

1. **Chat 页写死一个生产路由不到的模型且无选择器**（`web/src/pages/v2/Chat/index.jsx:64,83`）；
   Dashboard 首条 curl 与 Token 四种片段同样写死厂商模型名。UAT 用页面自己的请求体实测
   `502 model_not_found`；生产 0 条渠道能路由它。第十轮我以"页面能用"为由删掉横幅，证据是我
   手搭的请求体而不是页面自己发的——**横幅是假的，但删掉后页面对每个用户都是坏的。**
   `GET /api/v2/:slug/models` 读的是手工目录表 `models`，结构上答不了"这个租户能路由什么"；
   唯一可路由来源是 `/v1/models` 的 `visibleModels`（`handler/model.go:132-239`，需 sk-token）。
2. **k8s 探针路径在 Redis 限流器后面**：`/api/status`、`/api/health` 挂在 `api-router.go:15`
   之后（`:30/:31`），`rate-limit.go:44-50` 在 Redis 报错时 500+Abort ⇒ Redis 抖动 = 三副本同时
   掉出 Service，再被 liveness 重启，Redis 未恢复时启动 FatalLog。
3. **唯一生产渠道每天被自己停掉几次**：`ChannelDisableThreshold=5` s（DB option）vs 非流式
   16-token 探活 15–109 s；`testChannel` 手搭的 Request 没有 ctx（`channel-test.go:107-112`）而
   relay 用 `c.Request.Context()`（`api_request.go:77`）⇒ 无 deadline（跑过 900 s）；三副本各自探
   （`taskreg.Register(..., leaderOnly=false)` :717）；延迟封禁绕过 `ShouldDisableChannel`
   （:611-617），下一次干净探活即自动启用（`app/channel.go:132`）。探活写 user 1 消费日志
   （:397-409）污染排行榜/`quota_data`。
4. **`/api/v2` 整组无限流**（`api-v2-router.go:14-30` 只有 CORS/body/identity；
   `GlobalWebRateLimit` 在 `SetWebRouter` 挂、gin group 快照使其够不到）；chat session
   无每用户/每会话上限。
5. **Settings 展示 us-west / eu-frankfurt / ap-shanghai"可用"**（`Settings/index.jsx:127-133,1802-1847`），
   后端零实现；Variants 页展示编造的 QPS/错误率/事故行（`Variants/index.jsx:43-74`，路由 `App.jsx:383`）。
6. **邀请码没有投递路径**：只从 `POST /api/v2/auth/zita-bootstrap?invite=` 消费
   （`zita_bootstrap.go:173-196`），控制台两处调用都是裸 URL（`OidcRedirect.jsx:83-87`、
   `helpers/api.js:85`）⇒ 13 个 SSO 用户全落 default 租户；`/login?invite=` 可经 `return_to`
   完整穿过 IdP 往返（`zita_login.go:56-66` 只校 scheme/host）。集成指南前 82 行写的是退役服务
   （11 处 `api.lurus.cn`、`tenant_admins`、`exchange-token`、`custom_redirect_url` 均不存在）。
7. **`POST /api/v2/oauth/refresh`**（`api-v2-router.go:39`，公开、无限流）把轮换后的
   access/refresh token 原样回显（`oauth.go:600-605`）且从不更新 `oauth_token_expires_at`；
   零消费者（web、e2e、1.9M 行本地访问日志 0 次调用）；唯一跨仓引用是
   `2c-app-lutu/lib/services/gateway_api.dart:112-115` 的死包装（`lib/` 零调用者，lutu 真正的
   刷新走 `auth_service.dart:314-339` 直打 IdP）。
8. **结算失败仍写全价消费日志**（`compatible_handler.go:475-477`、`quota.go:527-530`、`:719-722`），
   无指标；v1 `GET /api/task/` `/api/mj/` 管理列表不过滤租户（`repo/task.go:160-201`、
   `repo/midjourney.go:37-60`），IDOR 闸门只扫三个前缀（`idor_completeness_test.go:37`）；
   `POST /api/channel/fix` 对 role≥10 开放且 `InitChannelCache` 在 abilities 落后时 nil-map panic
   （`channel_cache.go:37-43,52-55`）、同步循环无 recover（`:134-147`）。

## 2. 对记录的更正

第十轮 L3 的裁决"Chat 页面能用，横幅是假的"只对了一半。横幅确实是假的（内容不是 design-mock，
补全真跑）；但"页面能用"是用我手搭的请求体证的，页面自己发出的请求体在生产上必败。本轮 L1
的验证要点由此而来：**每条"能用"必须用 UI 自己发出的请求体证明**。

## 3. 已拍板的决定（执行时不再争论）

- 新增 session 鉴权的 `GET /api/v2/:tenant_slug/models/routable`，从 `visibleModels` 抽出核心与
  `/v1/models` 共用（token `model_limit` 只在 `/v1/models` 应用）；控制台四处（Chat、Playground、
  Dashboard、Token）全部改吃它；无可路由模型时渲染诚实空态而不是一条必败的命令。
- 探针路径只对**直连无转发头**的请求免限流；Redis 后端错误改为放行 + `RecordRateLimitDegraded`
  （D1 形态），不再 500。
- 自动探活：请求 deadline；leader-only（手写 `IsLeader` 门，不用 `NewLeaderTask`——它在每次
  lease 获得时立即跑一遍带封禁能力的 pass，每次 rollout 有三次）；延迟类封禁需连续 3 次且
  **永不封禁某模型的唯一启用渠道**（错误类封禁不变）；自动探活不写消费日志改写指标，手工测试保留日志行。
- `POST /api/v2/oauth/refresh` **下线**（非加鉴权）；同轮在 lutu 仓删死包装（独立 commit，
  只 stage 三个路径，根 `doc/coord/changelog.md` 记一行）。
- `/api/v2` 挂独立预算的按 IP 限流（`GLOBAL_V2_RATE_LIMIT*`，默认 600/180 s；UAT 显式 3000 并带注释）；
  chat session 每用户 200、每会话 500 条。
- 删 Region 面板和 Variants 页（含 `TweaksPanel.jsx`）；新增 `no_fabricated_status.test.js`
  （禁具名伪造声明 + 零 API 调用的路由页必须在带理由的静态白名单里），注释如实说明它不是
  "UI 声明能力必有后端"的通用 oracle。
- 邀请码走 `/login?invite=CODE`：OidcRedirect 读 query、放进 `return_to`、附到 bootstrap POST；
  `ensureSession` 保持裸调用（邀请码只在首登消费）；新增 admin 列表端点（只回 `code_prefix`，
  明文只在签发的 201 出现一次）+ Tenants 页 Invites 抽屉；集成指南前 82 行按真路由重写。
- 结算失败：保留日志行，加 `other.settlement="failed"`（用户可见，Log 页显示"未计费"）+
  `lurus_billing_settlement_failed_total{path}` + 仓内 netdata 告警块；**不改扣费语义**。
- v1 task/MJ 列表对 role<100 按租户过滤（root 全平台）；IDOR 闸门扩前缀并加"无 :id 的列表 GET
  必须有 `*_ListTenantScoped` 测试或带理由豁免"规则；`/api/channel/fix`、`/api/channel/test`
  （全租户探活 pass）、`/api/channel/update_balance` 改 RootAuth；`InitChannelCache` 内层 map
  惰性创建 + 同步 tick 加 recover。
- 生产 `ChannelDisableThreshold=5` 是 DB option，代码默认值到不了生产 ⇒ owner 事项 O7
  （`PUT /api/option` 设 60）；本轮只做 hysteresis + 唯一渠道保护。
- 文件所有权：并行 lane 各自拥有互不相交的文件；`internal/adapter/handler/router/*.go`（含测试）、
  `cmd/server/main.go`、`deploy/k8s/**`、`web/src/App.jsx`、`web/src/components/hifi/HFShell.jsx`、
  `web/src/z1_App.test.jsx` 由末位串行 **W** 独占；其它 lane 把改动写成逐字 hand-off。
  `v2_chat_session.go` / `repo/chat_session.go` 归 L5（含 L1 需要的"PATCH 持久化 model"）。
  locale `en.json`/`zh.json` 是唯一共享文件：各 lane 只改自己 `console.<page>.*` 子树
  （fr/ja/ru/vi 只含 shell/common/nav/projects，本轮不扩；parity 闸是 zh→en）。

## 4. Lane 结构

| Lane | 主题 | 量 |
|---|---|---|
| L1 | 模型来源：routable 端点 + Chat 选择器 + Playground/Dashboard/Token 去字面量 | M |
| L2 | 探针免限流 + Redis 错误放行 + 日志文件裁剪 | S |
| L3 | 自动探活：deadline / leader-only / hysteresis / 唯一渠道保护 / 指标替代日志 | M |
| L4 | 诚实性：删 Region 与 Variants + `no_fabricated_status` 闸门 | S |
| L5 | 下线 oauth/refresh + `/api/v2` 限流 + chat session 上限 + PATCH model | S |
| L6 | 邀请码投递链路 + admin 列表 + Invites 抽屉 + 集成指南/入驻 runbook | M |
| L7 | 结算失败诚实化：seam + 标记 + 指标 + Log 徽章 + 告警块 + runbook | S |
| L8 | v1 管理面：task/MJ 租户过滤 + channel cache nil-map/recover | S |
| W | 接线（串行末位）：路由/挂载/main.go/manifest/App.jsx/HFShell + real-chain 测试 + 闸门登记 | S |

### L1 — 控制台只说租户能路由的模型

**Owned**：`internal/adapter/handler/model.go`；新 `internal/adapter/handler/v2_models_routable.go` +
`_test.go`；新 `web/src/hooks/models/useRoutableModels.js` + `.test.jsx`；
`web/src/pages/v2/{Chat,Playground,Dashboard,Token}/index.jsx` + 各自 `index.test.jsx`；
`Models/index.jsx`（只改 :466-477 注释）；`en.json`/`zh.json` 的 `console.{chat,playground,dashboard,token}`。

**改动**
- `model.go`：把 `visibleModels`（:132-241）拆成 `acceptUnsetRatioModels(c)`、
  `tenantRoutableModels(c, userID, tenantID, acceptUnsetRatio) ([]dto.OpenAIModels, error)`
  （:182-224 的 group/auto-union/ratio 过滤/投影）、`narrowByTenantAllowlist(tenantID, in)`（:227-238）；
  `visibleModels` = token limit 分支（:152-180 不变）或 `tenantRoutableModels`，再
  `narrowByTenantAllowlist`。行为逐字节不变，由 `model_tenant_scope_test.go`、
  `model_discovery_contract_test.go`、`cover_r2_channel_test.go:656,698` 守。
- `v2_models_routable.go`：`ListRoutableModelsV2`：`c.GetInt("id")`、`middleware.GetTenantContext(c)`
  （`oidc_auth.go:1167`）→ 先 `repo.GetPricing()`（否则冷进程的 `supported_endpoint_types` 是空，
  `pricing.go:85-95,192-228`）→ 核心 → 投影 `{id, owned_by, supported_endpoint_types}`；响应
  `{success,data:{items:[…]}}`，items 永不为 JSON null；401 `UNAUTHENTICATED`。doc comment 写明
  token `model_limit`/token group 不在此应用。
- `useRoutableModels(tenantSlug, {enabled, skipErrorHandler})` → `{items, loading, error, resolved, refetch}`
  （`resolved` 区分"未知"与"已知为空"），纯函数 `routableFor(items, wire)`、`firstRoutableModel`、
  `defaultCompareModels(items, max=3)`、`intersectTokenLimits(items, token)`；`WIRE_OPENAI='openai'`、
  `WIRE_ANTHROPIC='anthropic'`。
- Chat：删 `DEFAULT_MODEL`；`[model, setModel]`，resolved 后默认第一个；头部 pill 改
  `<select data-testid='chat-model-select'>`；空列表渲染 `console.chat.no_routable_models` 且发送禁用；
  `openSession` 采用会话自带 `model`；`persistSession` 的 PATCH 也带 `model`（服务端由 L5 落地）；
  `send()` catch 读 `err.response.data.error_code === 'model_not_found'` → 带模型名的指引 toast +
  inline hint + `refetch`。新 key：`model_label`、`no_routable_models`、`models_load_failed`、`model_not_found`。
- Playground：删 `DEFAULT_MODELS`/`VENDOR_FOR`；`readURLParams` 读 `prefill_model`（修 Models 页
  `try ↗` 死链）；swap▾ 列表改 routable；resolved 后把草稿里不可路由的模型剔除，空则取前三；
  无模型时禁跑 + `console.playground.no_models`；`prefill_unroutable` 提示。
- Dashboard `OnboardingCurlBlock({username, tenantSlug, model, resolved})`：curl 用第一个 OpenAI-wire
  可路由模型；`resolved && !model` → `onboarding_no_models` 诚实空态，不渲染 `<pre>`。
- Token：`buildSnippets(key, host, {openaiModel, anthropicModel})`；`langTabsFor(anthropicModel)`
  （无 anthropic-wire 模型则隐藏该 tab，当前 tab 回落 curl）；候选先 `intersectTokenLimits` 所选
  token 的 `model_limits`；无候选 → `snippet_no_models` 空态。

**Oracle（→ 必红的变异）**
- Go：`TestListRoutableModelsV2_TenantScope`（租户 B 看不到 A 独占模型 → 改用 `GetEnabledModels`）；
  `_EmptyIsEmptyList`（`"items":[]` 非 null）；`_SameSetAsV1Models`（与 `ListModels` 同库同 ctx
  集合相等，含 `TENANT_MODEL_ALLOWLIST_MODE=enforce` → 去掉 `narrowByTenantAllowlist`）；
  `_EndpointTypesFromChannelType`（Anthropic 型渠道含 `anthropic` → 去掉 `repo.GetPricing()`）；
  `_Unauthenticated401`。
- vitest：Chat `defaults the send model to the first routable model, not a literal`
  （`sendPayload.model === ROUTABLE[0].id`，替换 :201 → 恢复 `useState('<字面量>')`）；
  `honest empty state + send disabled`；`persists the model on PATCH`；`model_not_found guidance + refetch`；
  `picker changes the model sent`；`opening a saved session adopts its model`。Playground
  `first three routable by default`（→ 恢复 `DEFAULT_MODELS`）、`reads ?prefill_model=`（→ 删读取行）、
  `drops non-routable draft models`、`refuses to run when nothing routable`。Dashboard
  `onboarding curl uses first routable OpenAI-wire model`（→ 恢复 :114 字面量）+ 空态。Token
  `snippets use first routable model`、`Anthropic tab only when a routable model speaks anthropic`
  （→ 写死 `LANG_TABS`）、`narrows to token model_limits`、空态；:184-197 host 测试保持绿。
- 测试 fixture 用中性名（`rt-alpha`），新文件禁厂商模型名。

**Hand-off → W**：`api-v2-router.go:272` `tenantModels` 组内加
`tenantModels.GET("/routable", handler.ListRoutableModelsV2)`；`console_consumes_endpoints_test.go`
加 `"tenant models routable (v2)": "/models/routable"`；新 `router/models_routable_real_chain_test.go`
（无 session 401 / 本租户 200 / 他租户 slug 403 `TENANT_MISMATCH` / 未知 slug 404；变异：路由挂到
`tenantModels` 组外）。

**Docs**：`doc/product-integration-guide.md` 附录 A 加一行，`/v1/models` 行交叉引用。

**UAT**：bridge 会话 `GET /api/v2/lurus/models/routable` 列出 ids → `POST …/chat/send` 用
`items[0].id` 得 200，用不在列表的名字仍 502 `model_not_found`（列表即路由真源）；铸一把 token
`GET /v1/models` 集合一致；浏览器 Chat 选择器预选第一个、发送成功；Token/Dashboard 片段含该模型。
若 UAT 列表为空：先证四处诚实空态，再种一条指向 faultsim 的渠道复跑非空分支。

### L2 — 探针不再依赖 Redis，限流器 Redis 错误放行，日志文件裁剪

**Owned**：`internal/adapter/middleware/rate-limit.go`、`model-rate-limit.go`（:71-73 注释）、
`redis_miniredis_cover_test.go`（:183-205、:208-222）、新 `direct_request.go` + `_test.go`、
新 `api_rate_limit_probe_bypass_test.go`；`internal/pkg/metrics/r6_rate_limit_degraded.go`；
`internal/pkg/logger/logger.go` + 新 `log_file_retention_test.go`；`doc/runbook/rate-limit-degraded.md`。

**改动**
- `redisRateLimiterKeyed`：LLen 错误 → `metrics.RecordRateLimitDegraded("web_rate_limit_backend")` +
  `r6aRateLimitDegradedLogf` + 放行；时间戳解析错误 → `web_rate_limit_corrupt` + `rdb.Del(key)` 自愈 +
  放行；两个 label 预注册进 `init()`（:58-62）；内存路径不变。
- `middleware.IsDirectInClusterRequest(c)`：RemoteAddr 为 loopback/private 且
  `X-Forwarded-For`/`X-Real-IP`/`Forwarded` 三头皆空（复刻 `router/main.go:100-106`）。
  `GlobalAPIRateLimit()` 包一层：`probeBypassPaths[c.FullPath()] && IsDirectInClusterRequest(c)` →
  `c.Next()`，`probeBypassPaths = {"/api/status","/api/health"}`。经 nginx 转发的 `/api/health`
  照常限流（它是一次 DB ping）。
- `logger.go`：`SetupLogger` 记住当前 fd，换写器后 2 s 宽限关闭旧 fd，`pruneLogFiles(dir, keep)`
  只留最新 `LOG_FILE_RETAIN`（默认 3）个 `oneapi-*.log`。

**Oracle**：`TestRedisRateLimiterKeyed_BackendDown_FailsOpen_Miniredis`（替换 :183：`mr.Close()` 后
不 Abort、非 500、计数 +1 → 恢复 500）；`_CorruptTimestamp_FailsOpenAndClearsKey_`（key 被删 →
去掉 `Del`）；`TestGlobalAPIRateLimit_DirectProbePathsBypassLimiter`（`GlobalApiRateLimitNum=1`：
直连 `/api/health`×3 全 200，直连 `/api/other` 第 2 次 429，带 XFF 的 `/api/health` 第 2 次 429 →
删 bypass 分支）；`TestIsDirectInClusterRequest_Table`（六格）；
`TestSetupLogger_PrunesRotatedFilesBeyondRetain`（→ 删 prune 调用）。

**Hand-off → W**：两份 deployment 容器加 `args: ["--log-dir="]`（stdout 已由 kubelet 采集；注释写明原因）
与 `emptyDir: {sizeLimit: 512Mi}`（data/tmp）；`cmd/server/main.go:426-427` 的 30 s 改读
`config.Get().Server.GracefulShutdownTimeout`（env 今天是死配置）；两份 pod spec 加
`terminationGracePeriodSeconds: 40`；readiness `timeoutSeconds` 2→4 并改注释（handler 上限 DB 1.5 s +
Redis 2 s）；可选 `router/main.go:106` 改调 `IsDirectInClusterRequest`；新
`router/r11_probe_bypass_mount_test.go`（照 `r6a_rate_limit_mount_test.go:8-30`：`SetApiRouter`
真挂载，直连 ×3 全 200、转发 ×2 第 2 次 429）。

**Docs**：`rate-limit-degraded.md:52-56` 改为 fail-open + 两个新 label；`.env.example` 的
`LOG_FILE_RETAIN` 由 L5 代加。

**UAT**：R6 宿主直连 `http://localhost:30851/api/health` 400 连发全 200，经域名连发出现 429；
`describe pod` 无探针失败；`/metrics` 两个新 series 为 0；W 落地后 `kubectl exec … ls /data/logs` 不存在。

### L3 — 自动探活不再自伤

**Owned**：`internal/adapter/handler/channel-test.go`、`context_tasks_integration_test.go`（:297-333）；
新 `channel_probe_policy.go` + `_test.go`、`channel_probe_auto_test.go`；新
`internal/adapter/repo/ability_sole_channel.go` + `_test.go`；新 `internal/pkg/metrics/channel_probe.go`；
新 `doc/runbook/channel-auto-ban.md`。`context_tier_channel_test_test.go` 不动。

**改动**
- deadline：`channelProbeTimeout()` = `CHANNEL_TEST_TIMEOUT_SECONDS`（默认 120）；`testChannel` :114 后
  `c.Request = c.Request.WithContext(ctx)`；超时以 `localErr` 包 `context.DeadlineExceeded` 表达，
  归入延迟类。
- leader-only：`AutomaticallyTestChannelsWithContext` tick 分支加 `if !common.IsLeader() { continue }`
  （`audit_cleanup.go:95-98` 形态），`taskreg.Register(..., true, active)`；重写 :682-707 陈旧注释；
  删无调用者的非 ctx 版 `AutomaticallyTestChannels()`（:654-680）。
- hysteresis：`gopool.Go` 循环体抽成 `autoProbeChannel(channel)`，决策抽成纯函数
  `evaluateProbeOutcome(channel, result, elapsed, threshold, streaks) probeVerdict{Err, BanReason(""|error|latency), Streak, SoleModels, Outcome}`：
  超时→延迟路径；`app.ShouldDisableChannel` 错误类不变；延迟超阈 → `latencyBreachTracker.Breach`
  （进程内 map，leader 持有；干净 pass `Reset`），`Streak>=3` 时查 `soleEnabledModelsFn`
  （seam → `repo.SoleEnabledModelsForChannel(channelID, tenantID)`：该渠道服务的 (group, model) 中
  没有其它可达启用渠道的对），非空则不封并计 `latency_ban_skipped_sole_channel`；一次超阈即 `Err`
  非 nil 以阻止 `ShouldEnableChannel` 把慢渠道重新启用。封禁仍走 `processChannelError` →
  `app.DisableChannel`（审计+通知不变）。
- 指标替代日志：`testChannel` → `probeChannel(channel, model, endpoint, channelProbeOptions{RecordConsumeLog})`；
  手工 v1 路径 `RecordConsumeLog:true` 不变，自动路径 false。`metrics/channel_probe.go`：
  `lurus_gateway_channel_probe_total{outcome=ok|error|timeout|latency_breach}`、
  `lurus_gateway_channel_probe_duration_seconds`、
  `lurus_gateway_channel_auto_status_total{action=disable_error|disable_latency|enable|latency_ban_skipped_sole_channel}`，
  label 预注册。
- `ChannelDisableThreshold` 代码默认不改。

**Oracle**：`TestProbeChannel_DeadlineBoundsHangingUpstream`（上游阻塞至 ctx 结束，
`CHANNEL_TEST_TIMEOUT_SECONDS=1`，3 s 内返回且 `errors.Is(DeadlineExceeded)` → 删 `WithContext`）；
`TestEvaluateProbeOutcome_LatencyBanNeedsThreeConsecutiveBreaches`（→ 常量改 1 / 删 Reset）；
`_SoleChannelNeverLatencyBanned`（→ 删唯一渠道检查）；`_TimeoutIsLatencyNotError`；
`TestSoleEnabledModelsForChannel`（四格，含 `enabled=false` 与跨租户 → 删 `enabled=true` 谓词）；
`TestAutoProbe_WritesMetricsNotConsumeLog`（自动 0 行 + 计数 +1；手工 1 行 → 恢复自动路径写日志）；
`TestAutoProbe_ThirdBreachFlipsStatusToAutoDisabled`（两条渠道、阈值 1 ms、第三次才 AutoDisabled →
拆掉 evaluate 接线）；`TestChannelHealthTest_FollowerNeverLaunchesPass`（`SetLeader(false)` 300 ms 内
从未 running、taskreg `LeaderOnly==true` → 删 IsLeader 门）；既有 `_SuccessfulTickStampsHeartbeat`
加 `SetLeader(true)`。

**Hand-off → W**：`doc/runbook/INDEX.md` 加 channel-auto-ban 行（INDEX 归 L7，W 在两 lane 后补一行）；
`api-router.go:152,154` `/test` 与 `/update_balance` 改 `RootAuth`（与 L8 的 `/fix` 同一批，
注释"operator-only"）。

**Docs**：`channel-auto-ban.md`（判定规则、streak/唯一渠道语义、指标、env、owner option、手工 vs 自动、
follower 在 `/admin/system/tasks` 的显示）；`.env.example` 的 `CHANNEL_TEST_TIMEOUT_SECONDS` 由 L5 代加。

**UAT**：`GET /api/v2/admin/system/tasks` 显示 `channel-health-test.leader_only=true`；UAT 渠道指向
faultsim slow-headers、阈值临时设 0.1、加第二条同模型渠道 → 三次 `GET /api/channel/test` 后才
status=3；移除第二条再启用 → 保持启用且 `latency_ban_skipped_sole_channel` 递增；自动 pass 后
`token_name=模型测试` 行数不变，手工测试 +1；`delay_ms=125000` 手工测试约 120 s 超时返回。
UAT 改的 option 用完恢复。

### L4 — 删掉伪造的状态，并挡住它回来

**Owned**：`web/src/pages/v2/Settings/index.jsx` + `index.test.jsx`；删 `web/src/pages/v2/Variants/index.jsx`、
`web/src/components/hifi/TweaksPanel.jsx`（唯一消费者是 Variants 与 `static-pages.test.jsx:72-88`）；
`static-pages.test.jsx`；新 `web/src/pages/v2/no_fabricated_status.test.js`；`en.json`/`zh.json` 的
`console.settings` 五个 key（:920-921、:1007-1009）。

**改动**：删 `SECTIONS` :108、`REGIONS` :127-133、区块 :1802-1847，改头注释 :30；`ComingSoon` 保留给
integrations/danger。闸门 (a) 扫 `src/pages/v2` 非测试文件与六个 locale：禁
`us-west|eu-frankfurt|ap-shanghai|data residency|data_residency|region_current|region_available|section_region`，
文件数 >10 守卫；(b) 解析 `App.jsx` 的 lazy 映射 + slug 表 + 字面 `path='/console/v2/…'`，每个路由页
目录若无 `API\.(get|post|put|patch|delete)\(` 也不 import `hooks/models/`，必须在
`STATIC_PAGE_ALLOWLIST = {'design-system': …, 'states': …, 'account-disabled': …}` 里，且白名单项
必须仍被路由且仍零消费（防腐烂）。头注释明写：这不是"UI 声明能力必有后端"的通用 oracle。

**Oracle**：恢复 `['region',…]`+`REGIONS` → (a) 红；新增零 API 路由页不进白名单 → (b) 红；给 `States`
加 `API.get` 而保留白名单 → 腐烂检查红；删 `'states'` 项 → 红。`Settings/index.test.jsx` :786 去掉
Region 走查，:841-855 换成 `region / data-residency section is gone`（→ 恢复 section 红）。

**Hand-off → W**：`App.jsx:70` 删 `V2Variants` lazy；`:395` 删 `['variants', V2Variants]`；
`HFShell.jsx:83` 删 `variants: 'dashboard'`；`z1_App.test.jsx:101,209` 删对应 mock/条目。

**UAT**：`/console/v2/variants` 落 NotFound；Settings 左栏无 Region；`grep -l eu-frankfurt web/dist/assets/*.js` 为空。

### L5 — 下线 oauth/refresh、`/api/v2` 限流、会话上限、PATCH model

**Owned**：`internal/adapter/handler/oauth.go`（删 :558-606 与 `refreshAccessToken` :794-~830）、
`cover_r2_auth_test.go`（删 :988-1017）、`oauth_helpers_extra_test.go`（删 :173-217）、
`docs/openapi/api-v2.yaml`（:151-165）/`api-v2.json`（:192 起对象）、`repo/l4_tenant_reserved_slug.go:38`
注释；`internal/pkg/common/constants.go`、`init.go`（:112-123 块）、`.env.example`；新
`middleware/rate-limit-v2.go` + `rate_limit_v2_test.go`；`handler/v2_chat_session.go` + `_test.go`、
`repo/chat_session.go` + 新 `chat_session_cap_test.go`。跨仓：`2c-app-lutu` 删 `gateway_api.dart:112-115`、
`gateway_api_test.dart:311-318`、`token_provider_test.mocks.dart:188-190`（独立 commit，只 stage 这三条
路径，该仓有别人的 README.md WIP）。

**改动**：`GLOBAL_V2_RATE_LIMIT_ENABLE/…_LIMIT(600)/…_DURATION(180)` → `GlobalV2RateLimit()` =
`rateLimitFactory(..., "GV")`（自继承 L2 的 fail-open）。预算校验：控制台最坏 ≈ 6 XHR + Gateway 12/min +
SystemTasks 4/min + Log 尾随 20/min ≈ 108/180 s，5× 余量。会话上限：`maxChatSessionsPerUser=200`
（repo 事务内 COUNT `tenant_id+user_id`，`ErrChatSessionLimitReached` → 400 `CHAT_SESSION_LIMIT_REACHED`，
软上限写进注释）、`maxChatSessionMessages=500`（`parseChatSessionMessages` 加 `len` 检查，Create/Update
共用 → 400 `INVALID_REQUEST`）。PATCH：`updateChatSessionRequest` 加 `Model *string`，
`UpdateChatSessionOwned(..., model *string, msgs)` 非 nil 时写入（≤128，同 create 规则）。

**Oracle**：`TestGlobalV2RateLimit_TripsAtBudgetAndIsOwnBucket`（`GV` 与 `GA` 分桶：v2 第 3 次 429 带
`X-RateLimit-Limit: 2`，同 IP `/api/x` 仍 200 → mark 改成 `GA`）；`_DisabledIsPassThrough`；
`TestV2ChatSession_Create_RejectsWhenSessionCapReached`（200 行后 400，另一用户 200 → 删 COUNT）；
`_Create/_Update_RejectsOversizedMessageList`（501 → 400，500 → 200，PATCH 失败不改存量 → 删 len 检查）；
`TestCreateChatSession_CapCountsOnlyCallerRows`；`TestV2ChatSession_Update_PersistsModel`（→ 忽略 `req.Model`）。
删除的符号由编译本身锁。

**Hand-off → W**：删 `api-v2-router.go:39`；删 `v2_completeness_test.go:89` 条目；新
`router/r11_oauth_refresh_retired_test.go`（真挂载 POST → 404；变异：加回路由行）；`api-v2-router.go:23`
后 `apiV2.Use(middleware.GlobalV2RateLimit())` 并注释 group 快照原因；`r6-uat/deployment.yaml:124` 后加
`GLOBAL_V2_RATE_LIMIT: "3000"` 带 e2e 单 IP 注释；新 `router/r11_v2_rate_limit_mount_test.go`
（`GlobalV2RateLimitNum=3`，转发 4 次第 4 次 429）；`deploy/k8s/r6-uat/README.md` 差异表加行。

**Docs**：`.env.example` 加 `GLOBAL_V2_RATE_LIMIT*`、`LOG_FILE_RETAIN`、`CHANNEL_TEST_TIMEOUT_SECONDS`；
openapi 删 refresh 路径、chat session 端点补两个 400 码；根 `doc/coord/changelog.md` 记 refresh 下线 +
lutu 包装删除。

**UAT**：`POST /api/v2/oauth/refresh` → 404；`seq 3100 | xargs -P8 curl …/api/v2/switch/presets` 约 3000
后出现 429 且头 `X-RateLimit-Limit: 3000`，夜跑 e2e 保持绿；bridge 会话建 201 个 session 第 201 个 400，
PATCH 501 条 400，随后 DELETE 清理。

### L6 — 邀请码能被真人用上，集成指南说真话

**Owned**：`internal/adapter/repo/tenant_invite.go` + `_test.go`、`handler/tenant_invite.go` +
`tenant_invite_admin_test.go`；新 `web/src/pages/v2/Tenants/InvitesDrawer.jsx` + `.test.jsx`；
`Tenants/index.jsx` + `index.test.jsx`；`web/src/components/auth/OidcRedirect.jsx` +
`cx_oidc_redirect.test.jsx`；`en.json`/`zh.json` 的 `console.tenant`；`doc/product-integration-guide.md`
（:1-82 + 附录 A 行）；`doc/runbook/tenant-onboarding.md`。

**改动**：`repo.ListTenantInvites(tenantID, limit, offset) ([]TenantInvite, int64, error)`；
`handler.ListTenantInvites`（`GET /api/v2/admin/tenants/:id/invites`，视图
`{id, code_prefix(Code[:8]), status, expired_time, consumed_by_account_id, consumed_at, created_by_user_id, created_at}`，
未知租户 404）。`InvitesDrawer({tenantId, tenantName, onClose})` 照 `CreditPoolDrawer` 形态：签发
（`ttl_hours` 默认 72）→ 一次性链接 `inviteLink(origin, code) = ${origin}/login?invite=${encodeURIComponent(code)}`
只读框 + 复制 + "仅显示一次"提示；列表（前缀/状态/过期/消费者/创建）；pending 行可撤销。
`Tenants/index.jsx` 加 `invitesTarget` 状态、行按钮 `tenant-invites-btn-<id>`、挂载。`OidcRedirect`：
`readInviteCode()`（`/^[A-Za-z0-9_-]{1,64}$/`），有码时 bootstrap 路径 `…/zita-bootstrap?invite=…`、
`returnTo = ${origin}/login?invite=…`；无码时字符串逐字不变（保 cx :134-138）；不加任何新 `t()` 中文源 key
（fr/ja/ru/vi 有棘轮）。`ensureSession` 不动。

**Oracle**：Go `TestListTenantInvites_NeverReturnsFullCode`（签发得 `code`，列表原文不含它、无 `code` 键、
`code_prefix==code[:8]` → 视图投影 `Code`）；`_ShowsStatusTransitions`（撤销→3，
`ConsumeTenantInvite(code, 4242)`→2 且消费者 4242）；`_UnknownTenant_404`；repo
`TestListTenantInvites_ScopedByTenant`（→ 删 `tenant_id` 谓词）。vitest `cx_oidc_redirect`：
`forwards ?invite= to the bootstrap POST and carries it in return_to`（两处拼接任一删掉即红）+ 既有裸调用
与 dashboard return_to 断言；`InvitesDrawer.test.jsx`：一次性链接由 origin+code 构成（→ 去掉
`/login?invite=`）、撤销 DELETE 后重取、行由 `code_prefix` 渲染、纯函数格；`Tenants/index.test.jsx` 打开抽屉。

**Hand-off → W**：`api-v2-router.go:455` 前加 `tenantMgmt.GET("/:id/invites", handler.ListTenantInvites)`；
`v2_completeness_test.go` exempt 加该 GET（RootJWTAuth；投影只含前缀，引用测试名）；
`console_consumes_endpoints_test.go` 加 `"tenant invites (v2)": "/invites"`；新
`router/tenant_invites_list_real_chain_test.go`（非 root 被拒；root 200 且 body 无任何 32-hex）。

**Docs**：指南 :1-82 重写（中文，无模型名）：host `hub.lurus.cn`、`POST /api/v2/admin/tenants` 建租户、
邀请链接流程（签发 API 或抽屉 → 发 `https://hub.lurus.cn/login?invite=<code>` → 首次 SSO 登录落入该
租户；单次、可过期、后续登录忽略）、删 `/login/{product-slug}`、`tenant_admins`/`zitadel_client_id` SQL、
`custom_redirect_url`、`exchange-token`、方式 3；保留 Q5 与附录；附录 C 标注无代码锚点；附录 A 加
routable 与 invites 三行。`tenant-onboarding.md` 加 Phase 3b（三条 curl + 浏览器流）、Phase 5 表加行、
Troubleshooting 加"用户落在 default"三因（已有 hub 会话的浏览器打开链接 / 码过期撤销已用 / 非首登；
查审计 `tenant.invite_consumed`）。

**UAT**：root bridge 会话：签发 201 得 `code`；列表只含 `code_prefix` 且 body 不含全码；撤销后 status=3；
浏览器 Tenants 抽屉端到端。**邀请链接的 SSO 往返在 UAT 结构性不可证**（SSO 关、`zita-bootstrap` 未注册、
`bridge/exchange` 不读 invite）——不得拿 bridge 登录冒充；服务端消费逻辑由 `zita_bootstrap_invite_test.go`
覆盖，生产验证列为 owner 事项（新建 scratch 租户签发、全新浏览器 profile 走 SSO、`/user/me` 返回该租户、
列表 status=2、审计出现 `tenant.invite_consumed`）。

### L7 — 结算失败不再伪装成已计费

**Owned**：`internal/app/quota.go`（:527-530、:719-722）、`internal/app/relay/compatible_handler.go`
（:475-478、`other` 生成后一行）；新 `internal/app/settlement_outcome.go` + `_test.go`、
`internal/app/relay/settlement_outcome_text_test.go`；`internal/app/log_other_projection_lock_test.go`
（`settlement` 进 `wantUserVisible`）；`internal/app/governance/classification.go`
（`"settlement": TierPublic`）；`internal/pkg/metrics/billing.go`；`deploy/r6-host-netdata/health.d/newhub.conf`
+ `README.md`（11→12）；`doc/runbook/INDEX.md`（11→12 + 行）；新 `doc/runbook/settlement-failed.md`；
`web/src/pages/v2/Log/index.jsx` + `index.test.jsx`（`other.settlement==='failed'` → "未计费"徽章，
key `console.log.uncharged` 进 `en/zh`）。

**改动**：`var PostConsumeQuotaFn = PostConsumeQuota`（seam，同 `quota.go:42 AsyncGo` 形态）；
`SettleConsume(ctx, relayInfo, quotaDelta, preConsumed, path) error`（调 seam，出错时保留原
`logger.LogError` 文案 + `BillingSettlementFailedTotal{path}.Inc()`）；`FlagSettlementOutcome(other, settleErr)`
置 `other["settlement"]="failed"`；三处站点各替换 3 行调用并在 `other := Generate…OtherInfo(...)` 后加一行；
`RecordConsumeLog` 不变。指标 `lurus_billing_settlement_failed_total{path=text|claude|audio}` 预注册。
netdata `template: newhub_settlement_failed`（`on: prometheus.newhub.lurus_billing_settlement_failed_total`，
`# series:`/`# runbook:` 注释，warn `>0`），头部 `# STATUS:` 加日期注"仓内新增、未安装"。
`PostWssConsumeQuota` 本轮不动，runbook 注明。

**Oracle**：`TestSettleConsume_CountsAndFlagsOnFailure`（→ 删 flag / 删 Inc）；
`TestPostClaudeConsumeQuota_SettlementFailureFlagsLogRow`（sqlite LOG_DB 行的 `Other` 含
`settlement:"failed"`、`{path="claude"}`+1 → 恢复直调）；`TestPostAudioConsumeQuota_…`；
`TestPostConsumeQuota_TextSite_…`（relay 包，swap `app.PostConsumeQuotaFn`）；
`TestSettleConsume_SuccessLeavesRowUnflagged`（→ 无条件打标）；既有投影锁与
`netdata_alarm_series_test.go`/`declared_series_written_test.go`/`alert_wiring_honesty_test.go` 必须仍绿；
vitest Log `renders an uncharged badge when other.settlement is failed`（→ 删读取）。

**Docs**：`settlement-failed.md`（含 INDEX 约定头、查行 SQL、对账步骤是 owner 决策、与 breaker/outbox
告警的区别）。

**UAT**：`/metrics` 三个 `path` series 为 0；netdata `alarms?all` 出现 `newhub_settlement_failed`
（安装是 O2 一并的 owner 动作）；真失败需 DB 级故障（临时撤销 UAT 角色的 UPDATE），owner 可选。

### L8 — v1 管理面加固

**Owned**：`handler/task.go`（:343-378，改 :352-354 注释）、`handler/midjourney.go`（:424-444）、
`repo/task.go`（:160-201、:340）、`repo/midjourney.go`（:37-60、:144）、`entity/task.go`
（`SyncTaskQueryParams.TenantID`）、`entity/midjourney.go`（`TaskQueryParams.TenantID`）、
`handler/v1_cross_tenant_idor_test.go`、`repo/channel_cache.go`（:23-56、:134-147）+ 新
`channel_cache_resilience_test.go`。

**改动**：repo 列表/计数加 `user_id IN (SELECT id FROM users WHERE tenant_id = ?)`（`usedata.go:125`
先例）；handler `role < RoleRootUser` 时 `queryParams.TenantID = c.GetString("tenant_id")`。
`InitChannelCache` :52 内层 map 惰性创建；`syncChannelCacheOnce()`（`defer recover` →
`metrics.RecordPanic("channel_cache_sync")` + `SysError` 带栈；seam `initChannelCacheFn`）由
`SyncChannelCacheWithContext` 调用；启动时的 recover 不动。

**Oracle**：`TestV1Task_ListTenantScoped` / `TestV1Midjourney_ListTenantScoped`（`SetupV2TestRouter`，先
AutoMigrate 对应表；role 10 见 1 行、root 见 2 → 删 TenantID 赋值或子查询）；
`TestInitChannelCache_ToleratesGroupWithoutAbilityRows`（→ 撤 nil 守卫即 panic）；
`TestSyncChannelCacheOnce_RecoversPanic`（`PanicsRecovered{channel_cache_sync}`+1 → 删 recover）；
顺手 `TestV1{Channel,User,Redemption}Search_ListTenantScoped`（现有代码已过滤，只补锁）。

**Hand-off → W**：`api-router.go:164` `/fix` 加 `middleware.RootAuth()`（注释 operator-only，同 :268-270）；
`:152` `/test`、`:154` `/update_balance` 同样 RootAuth（L3 也要求）；`idor_completeness_test.go`：
`/fix` 豁免理由改为 RootAuth；前缀加 `/api/task/`、`/api/mj/`；新增第二条规则——范围内无 `:id` 且路径
不含 `/self` 的 GET 必须出现在 `listScoped`（具名 `*_ListTenantScoped` 测试）或 `listExempt`（带理由）：
swept = channel/redemption/user/task/mj 五个列表 + 三个 search；exempt = `/api/user/token`、
`/api/user/models`、`/api/user/totp/status`（自服务）；`/api/channel/models`、`/models_enabled`、
`/tag/models` 读 handler 后归类；新 `router/r11_channel_fix_root_only_test.go`（role 10 → 200
`success:false` 权限不足；root → `success:true`；变异：去掉 RootAuth）。

**UAT**：`switch` 租户 role-10 bridge 会话 `GET /api/task/?p=1`、`GET /api/mj/?p=1` 的 `total` ≤ root
会话；`POST /api/channel/fix` role 10 拒、root 成；建渠道 + `/fix` 一轮后 pod 日志无
`channel cache sync panic`，`/metrics` 出现 `panics_recovered_total{source="channel_cache_sync"}`。

### W — 接线（串行，全部 lane 验收后）

顺序：L2 manifest/main.go → L5 路由删除 + v2 限流挂载 + UAT env + README 行 → L8/L3 三条 RootAuth +
IDOR 闸门扩展 → L1/L6 路由 + 闸门登记 → L4 的 App.jsx/HFShell/z1_App → W 自有 mount/real-chain 测试
（`r11_probe_bypass_mount`、`r11_oauth_refresh_retired`、`r11_v2_rate_limit_mount`、
`r11_channel_fix_root_only`、`models_routable_real_chain`、`tenant_invites_list_real_chain`）→
`doc/runbook/INDEX.md` 的 channel-auto-ban 行 → 全量闸门。

并行阶段已知必红、W 之后才能绿：`router/frontend_route_contract_test.go`、`no_fabricated_status` (b)
（Variants 仍被路由）、`bun run build`（App.jsx 仍 import Variants）、`z1_App.test.jsx`——lane 内禁止"修"它们。

## 5. Do not regress（本轮触碰的既有行为）

- `visibleModels` 对 `/v1/models` 的输出逐字节不变（token `model_limit` 分支、auto-union、ratio 过滤、
  allowlist 收窄）；`model_tenant_scope_test.go`、`model_discovery_contract_test.go` 是锁。
- 限流器的 **内存** 路径与 429 头形状（`X-RateLimit-*`、`Retry-After`）不变；改的只是 Redis 后端出错
  时从 500 变放行；经转发头到达的 `/api/health` 仍受限流。
- 错误类封禁（`app.ShouldDisableChannel`）的判定与 `app.DisableChannel` 的审计 + 通知路径不变；
  手工 `GET /api/channel/test/:id` 仍写消费日志。
- 结算的扣费语义（`PostConsumeQuota*` 的钱路）一字不改；只加标记与指标。
- 第十轮闸门：`TestConsoleReadsEveryUserFacingEndpoint`（新端点必须登记消费者）、
  `web/src/pages/v2` 零 `WIPBanner`；第九轮：`alert_wiring_honesty_test`、`declared_series_written_test`、
  `netdata_alarm_series_test`（新 series/告警块必须同时有写者与声明）；i18n zh→en parity 与 markup 零。
- `TestV2IDOR_Completeness` 现有前缀与豁免不缩水，只扩。
- `TestEmbeddedFS_VersionsAreContiguous`：本轮零 migration，038 之后不得出现新文件。
- UAT 夜跑 e2e（33 passed / 1 skip）在 v2 限流 3000 下保持绿。

## 6. 不做

`ChannelDisableThreshold` 的生产值（DB option → O7）；`PostWssConsumeQuota` 的结算标记；fr/ja/ru/vi
locale 扩展；`ensureSession` 读邀请码（非首登不消费）；对 `oauth/refresh` 加鉴权而非下线；任何 migration。

## 7. 执行协议

1. 本文件落 `_bmad-output/planning-artifacts/`，单独 commit。
2. 开发编排：8 个 lane 并行（小档模型开发，prompt 钉死：只写自己 owned 文件、可读任何文件、禁 git/
   禁 prod/禁 ssh、新文件禁厂商模型名与工具名、每个 oracle 先红后绿、写完跑自己的测试集与 eslint/prettier）；
   每 lane 一个对抗验收（大档模型，含 "auditor missed" 槽位）→ 修复轮（只读自己 `## Lx` 段 + operator
   追加决定，追加决定内联进 lane 段而不是文末）。
3. 每 lane 验收通过即由我逐路径 stage 该 lane 文件做 WIP commit（`git status` 先看 staged，`coverage_*`
   五个文件永不碰），会话用量上限中途死掉可用 `skip_lanes` 重跑。
4. W lane 串行落地 hand-off 清单 + 自有测试；我亲手收尾（散文、注释绝对词、闸门变异自检）。
5. 本机闸门（lint 与全量测试不并跑）：

```
go vet ./... && go build ./...
go test -short -count=1 -p 2 ./...
go test -run '<结构闸门集 + TestConsoleReadsEveryUserFacingEndpoint + no_fabricated_status + IDOR + alert honesty + netdata series + deploy consistency>' -p 2 ./...
golangci-lint run --new-from-rev=origin/main ./internal/... ./cmd/...
cd web && bun run lint && bunx eslint src --ext .js,.jsx && bun run test && bun run build
```

   `-race` 与覆盖率棘轮只在 CI。
6. lutu 仓：独立 commit（只 stage 三个路径），根 repo `doc/coord/changelog.md` 一行；push lutu 之外的
   任何仓改动前 `git status --short` 逐条核对。
7. PR → CI 14 项绿 → merge → auto-pin → ArgoCD 收敛（prod 3/3 + UAT 1/1 同 digest）→ 按各 lane 的 UAT
   探针逐条实证（生产只读；UAT 改的 option/渠道用完恢复）→ 报告（含 §2 的更正记录）。

## 8. 验证要点（诚实闸）

- 每条"能用"必须用 **UI 自己发出的请求体**证明（Chat 用页面默认模型发送得 200），不用手搭 payload。
- 每个闸门自身做变异验证（删掉被守内容后闸门必须红）。
- 否定式断言（"零消费者"、"无调用"）先枚举所有拼法再下结论（本轮 oauth/refresh 已被 lutu 一例证明
  只查两个仓不够）。
- UAT 证不了的（邀请链接 SSO 往返、结算失败真触发、告警投递）明写"未核实/owner 验证"，不降级成邻近信号。

## 9. Owner 事项（git 改不到）

- O1 生产 root 注册 TOTP（`user_totps` 仍空）后翻 `SECURE_VERIFICATION_REQUIRE_ENROLLMENT`。
- O2 netdata：`obs-netdata` 内 `OBS_SLACK_WEBHOOK_URL` 未设 = 没人收到告警；重启加载第九轮三条 + 本轮
  `newhub_settlement_failed`（重启会瞎掉 R6 全部服务监控，owner 定时机）；DR 演练最近日志 08-21，
  核一下周任务是否仍在跑。
- O3 UAT 真实厂商多能力渠道；O4 上下文档位；PRICE-1/2；epay 凭证；`SESSION_REGISTRY_ENABLED` 生产开关
  签字（UAT 已 soak）。
- **O7 新**：生产 `PUT /api/option {"key":"ChannelDisableThreshold","value":"60"}`；本轮上线后到生产用
  全新浏览器 profile 走一次邀请链接 SSO 验证。
