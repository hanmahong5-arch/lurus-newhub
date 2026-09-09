# TokenHub v0.8.0 对照分析 — 2026-09-09

> 复核基线:三档研究员输出经反证/复核后修正,本文档全部采用复核后结论(标 `[corrected]` 的行)。
> newhub 引用均为 `2b-svc-newhub` 仓库路径;TokenHub 引用均为 clone 于
> `C:\Users\Anita\AppData\Local\Temp\tokenhub-0.8.0` 的路径。

## 1. TokenHub 0.8.0 定位与事实卡

一句话:TokenHub 是一个面向**单团队自托管 + 第三方插件生态**的开源 LLM 网关(Apache-2.0,
Go 1.26 + Next.js,SQLite-first/PostgreSQL 可选多实例),核心差异化不在网关本体能力(与
newhub 大量重叠或更弱),而在**插件平台**(manifest/权限/信任分级/市场)、**FinOps 精细度**
(精确 decimal 费率卡、按 weekday/时区分窗定价、按费率卡配置的 cache-write 5m/1h 价格)、**运营护栏**
(guardrails 引擎、多渠道告警、审批流)以及**竞对迁移工具**(`tokenhub-migrate` CLI +
LiteLLM source adapter)。

事实卡:
- 语言/框架:Go 1.26 backend + Next.js frontend(newhub:Go 1.25 + Gin/GORM + React/Vite)。
- DB:SQLite-first,PostgreSQL 用于多实例部署(newhub:PostgreSQL-only,SQLite 仅 hermetic 单测)。
- 许可:Apache-2.0(newhub 私有,基于 New API 演进)。
- 发布节奏:首个 commit 2026-06,约每两周一个 release,1.3k stars —— 项目仅 3 个月龄,
  比 newhub 团队认知里"成熟稳定基座"的形象年轻得多。
- 插件模型:manifest 驱动(`plugin.yaml`)+ 权限矩阵 + 信任分级 + 市场索引;**v0.8.0 明确写明
  外部可执行插件(stdio-json-v1 subprocess)当前**不可用**,"pending host-level isolation"**
  ——引用见本文档 §2 plugin area 的 `design` 列表第 7 条原文("current runtime rejects all
  external commands before launch pending host-level isolation")。即 v0.8.0 的插件平台目前
  只有**内建(builtin)**插件可跑,第三方可执行插件是路线图特性,非已交付能力。

## 2. 七领域对照矩阵

> 表内 `newhub` 列已按复核结果修正(`session-affinity`、`cache-affinity` 两行原判定被复核
> 改判,已在此体现)。因原始条目达 125 行,矩阵保留全部行但压缩 evidence/delta 文字。

### 2.1 Gateway core(路由/failover/health/streaming/协议)

| 能力 | newhub | newhub 证据 | 差异 | 价值 | 借鉴 | 工作量 |
|---|---|---|---|---|---|---|
| Chat Completions streaming | has | relay.go:56-77, relay_adaptor.go TextHelper | 等价 | high | n/a | n/a |
| Anthropic Messages 兼容端点 | has | relay.go:317-380 RelayFormatClaude | 等价,newhub 另有原生错误信封 | high | n/a | n/a |
| Embeddings | has | relay.go:71 RelayModeEmbeddings | 等价 | medium | n/a | n/a |
| 后台异步 Responses(background:true 轮询) | missing | responses_handler.go:22-113 纯同步 | 无 job 表/无轮询端点 | low | 需 ResponseJob 表+worker,当前无消费者 | L |
| 图片生成异步 job 追踪 | missing | relay.go:59-60 ImageHelper 同步 | 无 job id/并发 slot | low | 同上,无消费者 | M |
| 半开断路器指数退避 | partial | resilience/circuitbreaker.go:69-140 固定 30s | 无随连续失败增长的冷却 | medium | trips 计数器+`min(base*2^trips,max)` | S |
| 候选路由优先级/权重排序 | has | channel_cache.go:154-327 | 等价+按租户过滤 | high | n/a | n/a |
| Session affinity | **partial**[corrected] | session_affinity.go:64-120 单一调用点+有界内存 fallback | TokenHub 三路径独立注入均有 tenant 隔离,并非"仅限 Codex/Gemini";newhub 真实优势收窄为单调用点+文档化降级路径 | high | n/a | n/a |
| 配额准入(RPM/TPM+预扣) | partial | business_rate_limit.go:179-217 + pre_consume_quota.go | 两套机制而非单一 bucket 表 | low | n/a | n/a |
| 跨副本并发租约 | has | concurrency_limit.go:19-73 Redis TTL 租约 | 等价设计 | high | n/a | n/a |
| Health/readiness | has | health.go:63-112 含 migration 状态 | 等价 | medium | n/a | n/a |
| 流式 transform hook | has | relay.go:245-256 + response_capture.go | 计费/错误信封聚焦,非通用命名 hook 链 | low | n/a | n/a |
| Provider 主动探活 | partial | channel-test.go 按需 + 反应式断路器 | 无定时主动探测循环(设计取舍) | low | n/a | n/a |
| 作用域路由策略(tag/region/env) | partial | channel_cache.go:167-196 仅 tenant+group+model | 无 tag/region/env 维度 | low | 需新 Channel 字段,无当前需求 | M |
| 图片生成 cache locality affinity | **not_applicable**[corrected] | session_affinity.go 通用机制覆盖 | TokenHub 该特性实为通用无状态 rendezvous 路由,非图片专属;原判定前提有误但结论未变 | none | n/a | n/a |
| API key 鉴权(hash-only) | has | middleware token auth + CLAUDE.md scope 表 | 等价+租户/产品域 | high | n/a | n/a |
| Prometheus /metrics | has | router/main.go:37,85-110 | 鉴权模型不同但同样非公网暴露 | medium | n/a | n/a |
| CORS+trusted proxy | has | middleware/cors.go, trusted_proxies.go | 等价 | high | n/a | n/a |

### 2.2 Plugin platform(manifest/权限/信任/市场/生命周期/hooks/UI/provider plugin)

19 行中 newhub **11 行 not_applicable(无插件运行时,故无对象可比)、6 行 missing、2 行 partial**
(`background_jobs`、`action_commands` —— newhub 有硬编码后台任务/管理端点,但无声明式
schedule/retry 或 schema 化 action 注册)。newhub **无任何 has/better**——这是 newhub 与
TokenHub 差距最大的领域,但差距的性质是"newhub 不是插件生态,不需要是"而非功能缺陷:
newhub 是内部 sibling-product(platform-core/switch/lutu)专用的单镜像交付,没有第三方插件
作者群体。关键行:

- `manifest_yaml_schema`/`gateway_hooks`/`provider_plugin_protocol`:均 missing,newhub
  provider 是编译进二进制的 Go 包(`internal/adapter/provider/<vendor>/`,36 个目录),加一个
  vendor = 一次 Go PR + 重新部署。
- `gateway_hooks` 的价值判定 medium(而非 low):若未来 sibling product 需要在不重新部署
  newhub 的前提下拦截 relay 阶段,一个最小 Go interface hook registry(仅 2-3 个既有 seam:
  relay.go 选渠道前、quota.go 计费后)能以 TokenHub 全量方案一小部分的成本拿到大部分价值。

### 2.3 Billing/FinOps

18 行:partial 8、missing 5、not_applicable 4、better 1(`cost-spike-enforcement`)。better 行
的复核结论极重要:对 TokenHub 源码逐项 grep `cost.spike|CostSpike|anomaly|Anomaly` 全树**零命中**,
其 `docs/*.md` 对 `spike` 亦零命中(对照 grep `type RouteAttempt struct` 命中 `types.go:1247`),
即 TokenHub v0.8.0 没有成本尖峰熔断的等价物(README 的 "cost controls" 指预算),而 newhub 的对应机制
(`internal/adapter/middleware` CostSpikeLimit + `router/b1_gemini_chain_mount_test.go:214-267`
真实 429 测试)是可运行、经测试验证的。

关键 missing/partial:
- `rate-cards-exact-pricing`(partial):newhub 是 float64 全局 ratio,无 decimal 精度/版本/
  immutable revision;`ratio_setting/model_ratio.go:27,458`。
- `cache-write-pricing-tiers`(**has**,操作员复核改判):newhub 已按 5m/1h 分层计价——
  `internal/app/relay/helper/price.go:87-89` 把 1h 倍率定为 5m 的 `6/3.75`(`:17` 常量),
  `PriceData.CacheCreation5mRatio/1hRatio` 在 `internal/app/quota.go:315-316` 参与结算并落到
  日志 `cache_creation_ratio_5m/1h`(`log_info_generate.go:131-135`)。与 TokenHub 的差别只剩
  1h 倍率是固定常量而非按费率卡逐模型配置;原判定"单一 1.25x 倍率"为假。
- `customer-statements-export`(partial):`v2_billing_invoices.go:41` 月度 JSON 桶,自身注释
  承认"无 USD 汇率接入";无 CSV。
- `margin-statements`(missing):无 provider 侧成本聚合,故无法算 margin(`margin` 全仓 0 命中)。
- `analytics-token-cost-api`(partial):`v2_log_export.go:65-150` 有 CSV 导出但无 watermark/
  cursor/group_by/granularity,无专用只读 analytics 凭证类型。

### 2.4 Identity/RBAC/projects/API keys/审计/OAuth

18 行:partial 6、better 2(`team_isolation`、`audit_events`)、has 3、not_applicable 3、
missing 4。两条 better 均确证成立(billing 领域重复列出的审计哈希链同一条证据):
- `team_isolation`:newhub 硬隔离层是 Tenant(每个 repo 调用带 tenantID,`v2_project_idor_test.go:
  57-179` 全量 IDOR 测试),TokenHub 的边界是 Team(query-time `userHasTeam()` 过滤,无对等硬租户层)。
- `audit_events`:`audit_event.go:17-59` PrevHash/RowHash SHA-256 链 + `AuditChainHead` +
  `governance/chain_verify.go`;grep TokenHub 全树 `RowHash|PrevHash|hash_chain|tamper` 在
  AuditEvent 所在包零命中——TokenHub 有更丰富的字段级 before/after 快照但无防篡改链。

关键 missing:`scoped_routing_policies`、`content_security_policies`、`approval_workflows`、
`cost_reconciliation`(均 low/medium value,当前单模型无外部买家阶段优先级不高)。

### 2.5 Ops/security(guardrails/告警/备份/egress/部署/migration/observability/benchmark)

17 行:partial 8、missing 7、has 1(`metrics-prometheus`)、not_applicable 1(`backup-restore`,
架构不同不可比)。无 better/无 rejected advantage 存活的"更强"行——本领域 newhub 总体落后
TokenHub 的运营护栏精细度,但落后项多为 low/medium value、S/M 工作量,是本次借鉴清单主力来源:
- `guardrails-engine`(partial):newhub 是单一关键词黑名单+仅 block;TokenHub 是可插拔
  detector(pattern/PII/LLM)+可配置 action(allow/mask/block/audit),`engine.go:77-166`。
  newhub 已有 `SensitiveWordReplace`(`sensitive.go:52`)但 relay.go 未使用——**mask 动作已经
  写好但没接**,是最便宜的一条借鉴。
- `alerts-multi-channel`(partial):newhub 4 渠道(email/webhook/Bark/Gotify),TokenHub 9+
  (Feishu/DingTalk 带 HMAC 签名/企微/Slack/Discord/Telegram/WhatsApp)。
- `migrations-schema`(partial):newhub 迁移全部 forward-only 幂等 SQL,无 expand/contract
  阶段区分,破坏性迁移的安全靠人工流程(migration-ledger)而非代码强制。
- `benchmark-blackbox`/`benchmark-internal`:newhub 无黑盒压测工具/baseline+CI 门,当前
  无生产流量,优先级低。

Ops 领域三条确证的 newhub 优势(见 §3):产品线归因 metrics 标签、WAL-G 连续 PITR 备份、
迁移的 advisory-lock 并发安全——但该领域第三条("迁移与 leader election 解耦"的具体机制对比)
经复核**被推翻**:TokenHub 同样用 `pg_advisory_lock`,原文声称的"TokenHub 用时钟 lease 替代"
在其文档中找不到,newhub 的 advisory lock 机制本身是对的,只是不是相对 TokenHub 的独有优势。

### 2.6 Providers/models

17 行:has 6、missing 5、partial 3、not_applicable 3(Codex 订阅桥接三行,架构不适用)。
- `provider_catalog_158_entries`(missing):newhub ~36 vendor 均是编译进二进制的 Go 包,
  TokenHub 158 条目走数据驱动 catalog(`builtin_provider_catalog_plugins.go:44-100`)。
- `model_catalog_comprehensive_metadata`(missing):newhub `entity.Model`(`model_meta.go:
  17-34`)无 pricing/context_window/modalities/reasoning 字段;定价在 `model_rate_limit.go`
  按渠道单独配置,不作为 catalog 元数据对外暴露。
- `reasoning_effort_configuration`/`playground_reasoning_controls`(partial):newhub 按
  adapter 内联处理(OpenAI 靠 model 名后缀解析,Claude 另一套 budget 字段),无跨 provider
  统一 map/自动 fallback。
- Codex 订阅桥接三行 not_applicable:newhub 只把 Codex 当作**客户端**(工具版本追踪),
  从不代理个人订阅额度作为上游——这与 newhub 的 B2B 计量转售模式结构性不兼容,不是缺口。

### 2.7 Console/SDK/migration tooling

18 行:missing 8(多为 migration-* 系列)、has 3、partial 2、not_applicable 4、better 1
(`i18n-three-langs`)。
- migration 系列(`migration-cli`/`litellm-source-adapter`/`migration-bundle-schema`/
  `migration-id-strategies`/`migration-secret-resolution`)全 missing:newhub 无任何网关间
  迁移工具,`console_migrate.go:16` 只迁移自身遗留 option key,不是竞对导入工具。
- `security-policies`(missing):无内容审核/护栏管理页面,呼应 §2.5 的 guardrails 缺口。
- `sdk-openai-compat`(missing):无任何 committed SDK/smoke-test 脚本,集成指引是纯 prose
  (`doc/product-integration-guide.md:48`)。
- `i18n-three-langs`(better,确证):newhub 6 语言(en/fr/ja/ru/vi/zh)vs TokenHub 3
  (en/ja/zh-CN)。

## 3. newhub 领先项(经复核存活)

> 每条给出 newhub file:line 与对应 TokenHub 侧的零命中 grep(已在原始输入中执行并给出结果)。
> 原始 7 组 advantages 清单共约 20 条,skeptic 复核后 **4 条被 REJECTED**(见 §8),以下仅保留
> 存活项。

1. **可控故障注入上游 harness**(`internal/adapter/handler/faultsim.go`,`FAULTSIM_TOKEN` 门控)
   ——用于在 UAT 上真实制造中断流/断路器跳变/failover 抑制,而非仅单测断言。TokenHub 树内
   `grep -rln 'faultsim|fault_sim|chaos|inject.*failure' backend/internal/server/` 只命中
   两个不相关测试文件,无等价可控失败上游实现。
2. **In-process 原生 provider adaptor**(`internal/adapter/provider/<vendor>/`,36 目录)
   ——避免 TokenHub `stdio-json-v1` subprocess 插件协议的每请求 IPC 开销,代价是加 vendor
   需要 Go PR。
3. **防篡改哈希链审计**(`internal/domain/entity/audit_event.go:36-59` PrevHash/RowHash +
   `AuditChainHead` + `internal/app/governance/chain_verify.go`)——TokenHub AuditEvent 有
   before/after 快照但无链,树内 `RowHash|PrevHash|hash_chain|tamper` 在其 AuditEvent 包零命中。
4. **可运行、路由挂载、有真实 429 测试的成本尖峰断路器**(middleware CostSpikeLimit,
   `router/b1_gemini_chain_mount_test.go:214-267`)——TokenHub 源码树与 docs 对
   `cost.spike|CostSpike|anomaly|Anomaly|spike` 全零命中(对照 grep `type RouteAttempt struct`
   命中),无等价物。
5. **独立于 model 白名单的 per-token relay ACTION scope**(`internal/domain/entity/token.go:
   29-32`)——TokenHub `APIKey` 结构(`types.go:123-150`)有 AllowedModels/ModelAccessMode,
   无等价 action-scope 字段。
6. **跨每一条 project/token CRUD 路径独立测试验证的硬租户 IDOR 隔离**
   (`internal/adapter/handler/v2_project_idor_test.go:57-179`,404 而非 403,避免存在性泄露)。
7. **relay metrics 的跨产品成本归因标签**(`internal/pkg/metrics/metrics.go:39-51`
   `RelayRequestsTotal{product=...}`,来自 `X-Lurus-Product`)——TokenHub `metrics.go:41-97`
   仅 model/provider/project 标签,无跨消费方归因维度。
8. **持续 WAL-G PITR 备份作为部署拓扑内置后台进程**(`deploy/single-node/Dockerfile.postgres-walg`
   + `doc/runbook/pg-restore.md:1-40`,RTO≤30min/RPO≤5min)——TokenHub 备份是手工 SQLite
   快照/下载/恢复(`admin_backup_export_routing.go:25-68`),无连续归档等价物。
9. **Playground 页面与真实多租户信用池钱包/计费打通**(`web/src/pages/v2/Playground/`)——
   TokenHub `frontend/app/(console)/playground/page.tsx` 存在但未接入信用池式钱包系统。
10. **CommandPalette(⌘K)页面**(`web/src/pages/v2/CommandPalette/index.jsx`)——TokenHub
    frontend 全树 `command.palette|cmdk` 零命中。
11. **Whitelabel 支持**(`LURUS_WHITELABEL_MASTER_SECRET`,CLAUDE.md 记载的部署时密钥+
    per-tenant whitelabel 接线)——TokenHub frontend+docs 全树 `whitelabel|white.label` 零命中。
12. **6 语言 i18n**(`web/src/i18n/locales/`:en/fr/ja/ru/vi/zh)vs TokenHub 3 语言
    (`frontend/features/admin/i18n/runtime.tsx:7-99`:en/ja/zh-CN)。

## 4. TokenHub 领先项

1. **可插拔 guardrail detector 引擎**(pattern/PII/LLM 三类,allow/mask/block/audit 四动作)
   ——`backend/internal/guardrails/engine.go:77-166`。
2. **Manifest 驱动 + stdio-json-v1 外部插件协议**(路线图特性,v0.8.0 尚不可执行)
   ——`backend/internal/plugin/manifest.go`。
3. **158 条声明式 provider catalog**(数据驱动,加 vendor 不需要改代码)
   ——`backend/internal/server/builtin_provider_catalog_plugins.go:44-100`。
4. **含定价层级/reasoning 元数据的模型 catalog**(`ProviderCatalogModel` 结构)
   ——`backend/internal/server/types.go`(input_price_usd_per_1m/cache_read/write/pricing_periods/
   modalities/Capabilities)。
5. **按费率卡逐模型配置的 cache-write 5m/1h 价格**
   ——`backend/internal/metering/pricing.go:15-16`(`CacheWrite5m`/`CacheWrite1h`)。
   领先幅度很小:newhub 同样分 5m/1h 结算,只是 1h 倍率是固定常量
   (`internal/app/relay/helper/price.go:17,87-89`)。
6. **精确 decimal、版本化、immutable 的费率卡**(currency/effective_from/revision)
   ——docs/billing-pricing。
7. **`tokenhub-migrate` CLI(inspect/extract/plan/apply/verify/rollback)+ LiteLLM source
   adapter**——`backend/internal/migration/cli/`、`backend/internal/migration/source/litellm/`。
8. **9+ 渠道告警,DingTalk HMAC-SHA256 签名 webhook**
   ——`backend/internal/server/admin_notifications_http.go:638-646`。
9. **敏感操作审批工作流**
   ——`backend/internal/server/admin_notifications_http.go:75-139,181-187,869-983`。
10. **Expand/contract 迁移阶段闸**(contract 阶段迁移在 backfill 未完成/备份未验证前拒绝执行)
    ——`backend/internal/dbschema/migrations_manifest.json`,`docs/database-evolution.md`。
11. **黑盒 HTTP 压测工具 + checked-in baseline + CI 分配预算门**
    ——`docs/performance-benchmarking.md:1-99`,`benchmarks/baselines/`。
12. **跨 provider reasoning-effort 配置 map + 不支持模型自动 fallback 剥离**
    ——`frontend/features/admin/domain/provider-reasoning-options.ts:1-40`,
    `playground-logic.ts`(`playgroundReasoningEfforts`/`clampPlaygroundMaxTokens`)。

## 5. 值得借鉴前 12 项(按 (价值×复用性)÷工作量排序)

> `overlap` 标注对应 `oss-best-practice-cycle-2026-09-08.md` §12/§13 的 deferred id,重合项
> 意味着已有更细的落地 spec,借鉴时应直接复用该 spec 而非另起草案。

| # | 名称 | 领域 | 价值 | 工作量 | 落地草案 | overlap |
|---|---|---|---|---|---|---|
| 1 | 断路器指数退避冷却 | resilience | medium | S | `internal/pkg/resilience/circuitbreaker.go` struct 加 `consecutiveOpenTrips`,Open→HalfOpen 分支算 `timeout=min(base*2^trips, CB_TIMEOUT_MAX_SEC)`;oracle=连续 3 次半开失败后探测间隔应递增而非恒为 30s | **C11-RETRY-POLICY**(下周期 relay.go 首条 lane,建议直接并入而非单独起草) |
| 2 | 汇率接入发票 USD 字段 | billing | low(但零成本) | S | 把 `operation_setting.USDExchangeRate` 接入 `ListInvoicesV2` 的 `AmountUSD`(源码注释里已经写明这是待办);oracle=非零汇率下发票 USD≠0 | 无 |
| 3 | 发票 CSV + USD 导出 | billing | medium | S | `ListInvoicesV2` 加 `format=csv` 分支,复用 `v2_log_export.go` 的 csv writer 模式;oracle=下载文件含 AmountUSD 列且金额与 JSON 一致 | 无 |
| 4 | 渠道代理连通性测试端点 | ops | low | S | 新增 `POST /v2/channel/:id/test-proxy`,复用既有 SSRF resolver(`channel.go`)+ 15s TLS dial;oracle=指向不可达代理返回明确失败而非保存后才发现 | 无 |
| 5 | Analytics 只读专用凭证 scope | identity | low | S | 在既有 internal-API-key 模型上加 `ScopeAnalyticsRead` + `project_id` claim,不新建平行凭证系统;oracle=该 scope 的 key 调用 balance/user 端点应 403 | 无 |
| 6 | ~~Cache-write 5m/1h 分层计价~~ **已撤回**(操作员复核):newhub 已分层结算,见 §2.3 改判;剩余差异(1h 倍率可按模型配置)价值 low,不立项 | billing | low | — | — | — |
| 7 | Analytics 增量拉取(cursor+group_by) | billing | medium | M | `ExportLogsV2`/`GetLogStatV2` 加 `granularity`/`group_by` query param + `X-Newhub-Next-Cursor` 响应头;oracle=两次分页拉取按 cursor 拼接后与一次性全量拉取结果集合相同 | 无 |
| 8 | 只读安全审计角色 | identity | medium | M | 在现有 role>=10/100 两级之外加一个 `security_admin`(数值域外或独立 bool),仅读 audit/log,不能写;oracle=该角色调用写端点 403,读 audit 200 | 无 |
| 9 | 多渠道告警(Feishu/DingTalk 签名/Slack) | ops | medium | M | 复用 `internal/app/webhook.go` 通用 HTTP 传输,新增 per-channel payload builder(参照 DingTalk HMAC-SHA256 签名);oracle=触发一次告警,对应渠道 Mock server 收到格式正确的请求体 | 无 |
| 10 | Guardrail mask 动作接通 | ops/security | medium | M | `sensitive.go:52` 的 `SensitiveWordReplace` 已实现但 `relay.go` 未调用——先把现成函数接上作为 `mask` 模式,再考虑加 PII regex detector;oracle=命中敏感词时响应体含替换后文本而非直接 403 | **C15-GUARDRAIL-OBSERVE**(依赖先删除死配置项 `StopOnSensitiveEnabled`/`StreamCacheQueueLength`,复用该条已有的清理顺序) |
| 11 | 迁移 expand/contract 阶段闸 | ops | medium | M | 迁移 embed manifest 加 `phase: expand|contract` 元数据字段,runner.go 对 contract 阶段迁移要求存在配套 backfill-complete 标记行才自动跑,否则退回手工执行;oracle=标记为 contract 且无 backfill 标记时,runner 应跳过并记录 pending 而非直接执行 | 无 |
| 12 | 模型 catalog 定价/推理元数据 | providers | medium | M | `entity.Model` 加可选 JSON metadata 列(pricing hint/context_window/modalities),只读暴露于 `GET /v1/models`;oracle=返回体含新字段且不影响既有客户端解析(向后兼容)。**注意:新列 = migration,033 未定前不可做**,先只用现有价格表派生只读字段 | **C17-MODELS-PRICING-AVAILABILITY**(该条已规划改 `/internal/models/catalog` 的 `available` 语义并需要 platform consumer note,建议将本条元数据扩展并入 C17 一次性做完,避免同一结构体改两次) |

## 6. 明确不借鉴

- **外部可执行插件运行时(manifest/权限矩阵/信任分级/市场)**:TokenHub 自身 v0.8.0 都还
  "pending host-level isolation"、拒绝所有外部命令——连原作者都没敢上生产。newhub 是内部
  sibling-product 专用单镜像交付,没有第三方插件作者群体,引入整套沙箱/信任分级基建
  投入产出比极差。
- **SQLite-first 存储策略**:newhub 已是 PG-only(2026-06 起硬性收口,MySQL/SQLite 生产
  fallback 已删除),retreat 到 SQLite-first 与既有架构决策直接冲突。
- **声明式 UI 模板/插件贡献 Admin UI slot**:newhub console 由同一团队维护后端,没有第三方
  UI 贡献者,YAML 模板层纯增加维护面。
- **`tokenhub-migrate` CLI + LiteLLM source adapter 本体**:对内部消费者(platform-core/
  switch/lutu)没有"外部竞对配置需要导入"的场景;但该特性作为**战略信号**值得记录(见 §7),
  不建议现在复制实现。
- **黑盒压测 harness + baseline 门**:当前无生产流量(2026-08 验收记录:单渠道单模型探活为主),
  没有真实基线可比,建 baseline 会是自证空转。
- **通用 background job 声明式平台**(cron+MaxConcurrency+Retry 描述符+运行时插件添加):
  newhub 只有约 5 个硬编码 lifecycle task,为此建一个新子系统是过度设计;若确需 retry/backoff
  配置,直接在现有 Task 结构加字段。
- **Codex 订阅桥接 / StepFun / GPT-6 Astra 预设**:均是面向个人开发者消费级订阅套利或特定
  厂商预设,与 newhub 的 B2B 计量转售模式结构性不兼容,无 sibling-product 需求信号。

## 7. 战略定位

TokenHub 不是 newhub 的直接竞品(TokenHub 面向自托管个人/小团队+插件生态,newhub 是内部
platform 产品组的多租户计费网关),但它是一个有用的"三个月后我们会不会被绕过"的压力测试:
它用**声明式插件平台**把"加一个 provider/加一个后台任务/加一个告警渠道"从"Go PR + 重新部署"
降到"上传一个 manifest",这是 newhub 结构性做不到也不必做到的(内部消费者、无第三方插件
作者)。真正该警惕的信号是它**自带 `tokenhub-migrate` + LiteLLM source adapter**——这意味着
该项目的商业假设里包含"帮你从 LiteLLM/OneAPI 迁移过来",即它把自己定位为**网关品类的迁移
目的地**,面向外部买家而非内部团队。newhub 目前没有外部买家迁移场景,但如果 Lurus 未来要向
非 sibling-product 的外部客户出售网关能力,"零成本迁移"会是买家决策的关键摩擦点,值得记入
`doc/decisions/` 作为观察项而非现在行动。newhub 在计费精细度(decimal 费率卡/发票 USD 与
CSV)、guardrails、多渠道告警上确有具体、低成本可补的缺口(见 §5),但在真正决定 B2B 网关
胜负的维度——租户硬隔离、防篡改审计、成本尖峰断路器、跨产品成本归因——newhub 已经领先,
每条都以 TokenHub 树内零命中 grep 加对照 grep 复核过(§3)。

## 8. 数据说明

复核覆盖范围:7 个领域全部 125 行的 `newhub_status`/evidence 均由研究员给出 grep 结果,
skeptic 对其中标 `correction` 的行(2 行:`session-affinity`、`cache-affinity`)与全部 7 组
`advantages` 列表(约 20 条)逐条重验。

被推翻的结论:
1. `session-affinity` 从"better"降级为"partial"——TokenHub 有对等的多协议 session affinity
   (三条独立调用链 + HMAC 租户隔离),原判定"仅限 Codex/Gemini"为假。
2. `cache-affinity` 的前提描述错误——TokenHub 该特性是通用无状态路由而非图片生成专属,
   结论(not_applicable)未变但论证链条被替换。
3. Gateway core 领域"per-attempt routing trace 为 newhub 独有"——**REJECTED**,TokenHub
   有对等且更细的 `RouteAttempt`/`RouteAttemptLog`(`types.go:1247,675`,`store_routing_calls.go:
   912`)。
4. Gateway core 领域"per-wire cache-token billing 测试面 TokenHub 缺失"——**REJECTED**,
   TokenHub 有 OpenAI/Anthropic 双 wire 专用 cache-token 测试文件。
5. Plugin 领域"newhub leader-election 后台任务协调 TokenHub 无等价物"——**REJECTED**,
   TokenHub 有 `ClusterLease`/`RunClusterTask` 集群级协调原语。
6. Identity 领域"newhub Redis 租约并发限制器 TokenHub 只有静态计数器"——**REJECTED**,
   TokenHub 有等价的 DB 行+heartbeat 续租机制。
7. Ops 领域"newhub 迁移与 leader election 解耦是相对优势"——**REJECTED**,TokenHub 同样用
   `pg_advisory_lock`,原文声称的"TokenHub 用时钟 lease 替代"在其文档中查无实据。

操作员合成后抽查(2026-09-09,对 HEAD d1351475 与 TokenHub clone 各自 grep):
- `cache-write-pricing-tiers` 由 partial 改判 has(§2.3),§4 第 5 条收窄,§5 第 6 条撤回。
- "TokenHub 文档声称有成本尖峰熔断"删除——其 docs 对 `spike` 零命中,是无等价物,不是声称落空。
- §5 第 1 条补齐真实路径 `internal/pkg/resilience/circuitbreaker.go`;第 12 条标注需 migration。
- 抽查通过:6 语言 locale 目录、WAL-G Dockerfile 与 pg-restore runbook、`USDExchangeRate`
  与 `v2_billing_invoices.go:23` 待办注释、`SensitiveWordReplace` 仅测试调用、`FAULTSIM_TOKEN`、
  审计链 `PrevHash/RowHash`、whitelabel handler、cost-spike 路由测试文件均在 HEAD 上核实。

未核实项(标注但未逐行 grep 复核,仅按研究员原始 evidence 采纳,建议下次周期若涉及借鉴落地
前再核一次):Providers 领域两条"newhub 优势"(cache-token 计费精度、B2B 计费原语架构差异)
研究员原文自称"directional not confirmed"/"not independently verified in TokenHub source"——
本文档未将其计入 §3 newhub 领先项,仅在此存档避免遗漏。7 个领域的 `design`/`verify.confirmed`
计数字段均来自研究员自报,未逐条抽查确认数字本身(仅对标 `correction`/`rejected_advantages`
字段列出的具体条目做了处理)。
