# Cycle 22 — 借鉴 TokenHub 0.9.0:能力路由、统一检索计量、决策模型路由

日期:2026-10-10 · 分支 `feat/tokenhub-09-parity`(叠在 `feat/data-sedimentation` #247 之上)· migration 053/054(根 ledger 已预留)

## 背景

TokenHub 0.9.0(Apache-2.0)的亮点是 Embeddings + Rerank + 语义路由:按模型能力自动选 Provider、统一检索计量、决策模型做路由评估。newhub 已有 `/v1/embeddings`、`/v1/rerank`、`/v1/systemone`(TypeSafe 57 / 自托管 58)、Dify 上游、GPT-6 家族定价,**不重做**。本轮只搬 TokenHub 领先的四块。

## 差距(按业务价值)

1. **模型模态与按能力过滤渠道**:`abilities` 无能力列;`EndpointTypeEmbeddings` 从不自动推断;选渠道只看 (group, model) 名;18 个 adapter 对 rerank 返回 `nil,nil` 假装支持 ⇒ 静默误路由。TokenHub:modality 准入 + 路由失败 501 带 `reasons[]`。
2. **统一检索计量**:rerank 把 TotalTokens 复制进 PromptTokens;无 search unit、无 documents 计数;usage 缺失时用预估值且无法区分;分析接口无工作负载维度。TokenHub:search unit 与 token 分开计价、未知价 ≠ 免费、usage 缺失记 unreported、留证。
3. **契约健壮性**:embeddings 无条数/维度/`encoding_format` 校验,响应写死 `[]float64`;rerank 无 `top_n` 范围、无降序校验、无矛盾 usage 拒绝;无 Voyage adapter;TypeSafe 响应无概率一致性校验。
4. **决策模型路由**:systemone 概率输出完全不参与路由。

## 硬约束

- ADR `doc/decisions/2026-05-09-cost-aware-routing.md`:自动路由**永不默认、永久 opt-in**;带 tool_call 的中途请求、`pin:`/`audit:true`、推理模型、多轮非首轮禁止自动路由;开启时须返回 `X-Routed-Model`/`X-Routing-Reason`。
- TypeSafe 对大陆 451,R6 不可达且不做代理绕过 ⇒ 决策路由 provider 无关:评估模型是任意 systemone 公开模型,走现有渠道选择;评估不可用 → 默认候选,零故障放大。
- cycle-6 do_not_regress:重试不越租户边界、`route_attempts` 有上限、流式安全 failover 闸在重试前、cache 计费按 wire 语义、产品归因只解析一次、`other.*` 投影默认拒绝。
- 已决不借鉴:插件运行时、SQLite-first、tokenhub-migrate/LiteLLM adapter、压测 baseline、Codex 订阅桥接、第四个 Dify(ADR-0027)。
- 工程:改默认值同步 SQL seed/DB default/ORM tag;关闭态零行为;前端只用 Bun;生产构建成功才算完成;监控只加低基数 Prometheus 指标。

## 不做 / 推迟

- **embedding_spaces 向量空间治理**:无「同一公开 embedding 模型挂多条异构路由」的客户;先把「同一模型名的所有渠道必须同模态」做成硬校验。
- **Helm chart**:部署 = Kustomize + ArgoCD;出现 customer-hosted newhub 时再做。
- **Responses 续接绑定表**:已有 `response_registry`(036);续接请求直接跳过评估。
- **插件计量**:无插件运行时。
- **Dify app 暴露成 chat 模型**:dify adapter 已是这个形态;只补接入文档。

## 第一波(后端,默认关闭或零行为变化)

### L1 模型模态 + 按能力过滤渠道(migration 053)

- 数据:`abilities.modality VARCHAR(16) NOT NULL DEFAULT ''`、`models.modality VARCHAR(16) NOT NULL DEFAULT ''`(管理员覆盖;entity.Model 的 GORM 表名)。
- 推断(纯函数):管理员覆盖 > 渠道类型(57/58 → decision;Jina 且名含 rerank → rerank)> 名字模式(`rerank|reranker` → rerank;`embed|embedding|bge|gte|e5-|voyage|sentence` → embedding;`tts|whisper|audio` → audio;`dall-e|image|flux` → image)> 静态 adapter 能力表(repo 不能 import provider,所以是零依赖包里的表,并由 relay 包的一致性测试与真实 adapter 对齐)> chat。在 abilities 重建处写入。
- 路由过滤:候选过滤加 relay mode → 所需模态映射,开关 `ROUTING_MODALITY_FILTER=off|observe|enforce`(默认 observe:只计 `lurus_routing_modality_mismatch_total{relay_mode}` 并 SysLog 一次/渠道;enforce:剔除不匹配渠道,无候选 501 `provider_capability_not_supported`,`error.details={stage:"route_selection",upstream_attempted:false,reasons:[{code,message,route_count}]}`,code ∈ `modality_mismatch|adapter_unsupported|price_not_configured`,不泄露端点/凭证)。模态 '' 视为兼容。
- `endpoint_type.go`:按模态返回 `embeddings`/`jina-rerank`/`systemone` 端点。
- do_not_regress:过滤在租户过滤之后、优先级分桶之前;不改重试边界。

### L2 统一检索计量(migration 053 同一文件)

- 数据:`logs.usage_unit`(`token|search_unit|request`)、`logs.usage_quantity BIGINT`、`logs.usage_source`(`upstream|estimated|unreported`)、`logs.retrieval_documents INTEGER`。无索引。
- 计价:`ratio_setting.SearchUnitPrice map[string]float64`(options,照 `ModelPrice`);rerank:配置了 search unit 价 → `units = ceil(documents/100) × queries`(Cohere 口径)× 价;否则 token×倍率;**不再把 TotalTokens 复制进 PromptTokens**;上游无 usage → `usage_source=estimated`;上游 usage 自相矛盾(`total=0 且 prompt>0`、负数)→ 502 `invalid_provider_usage`,不计费,计 `lurus_relay_invalid_usage_total{relay_mode}`。embeddings:`retrieval_documents` = 输入条数,`usage_unit=token`。非 pass-through rerank 响应必须按 `relevance_score` 降序,否则 502 `invalid_provider_response`;`return_documents=false` 时不带 `document`。
- 事件契约:`llm.usage.recorded` 追加可选字段 `relay_mode`、`usage_unit`、`usage_quantity`、`usage_source`、`retrieval_documents`,`v` 保持 1,契约文本改为「可追加可选标量字段,消费者须忽略未知字段」。
- 分析:rankings 的 `by` 加 `relay_mode` 与 `usage_unit`。

### L3 契约健壮性 + Voyage + TypeSafe 响应校验(无 migration)

- embeddings:输入 1–2048 条且每条非空;`dimensions` 1–65536;`encoding_format` ∈ {float, base64},响应 `Embedding` 改 `json.RawMessage` 透传;`input_type` 与 `task` 互斥;不支持的字段 400 `unsupported_parameter`(仅非 pass-through)。
- rerank 请求侧:`documents` 1–2048 且非空、`top_n` 1..len;所有 `nil,nil` 的假 rerank/embedding 实现改为显式 `ErrNotImplemented`,handler 层映射为 501 skip-retry。
- Voyage adapter(新渠道类型,照 jina 包)。
- TypeSafe/System One 响应校验:概率和 1±0.01;所选概率 ≥ max−0.01;score 期望偏差 ≤ 0.01×(N−1);legend/问题 ID/类型一致;失败 → 502 `invalid_provider_response`,不切渠道、上游 usage 有效时仍计费(写进 runbook §5)。

### L4 决策模型路由(migration 054)

- 表 `routing_policies`(见 SQL);候选 `[{id, model, criteria}]` 1–32,criteria ≤2048B,instructions ≤4096B;`model` 必须是该租户可路由的公开模型。
- 评估(`internal/app/routingdecision/`):最近一条纯文本用户消息(≤8192B)→ 一道 `choice` 题,criteria = 候选 criteria + `no_preference`;评估模型走现有 systemone 渠道选择(同租户),评估失败不重试;超时 `ROUTING_DECISION_TIMEOUT_MS`(默认 1000)、进程并发 8;confidence ≥ min → 改写目标模型;否则默认候选。
- 接入:distributor 在租户白名单与 token 模型限制之后、渠道选择之前,每请求最多一次,只对 OpenAI chat / Responses;禁区(reason=`ineligible`):非首轮、`tools`/`tool_choice`、`previous_response_id`/session affinity、`reasoning_effort`/推理模型、metadata `pin:`/`audit:true`。
- 透明:`X-Routed-Model`、`X-Routing-Reason: decision:<applied|low_confidence|no_preference|evaluator_unavailable|ineligible|single_candidate>`;审计 `routing.decision`(不记用户文本);指标 `lurus_routing_decision_total{reason}`、`lurus_routing_decision_latency_seconds`。
- 计费:评估用量单独一条消费日志(model=evaluator_model,`other.routing_eval=true`)。
- 管理 API:`GET/PUT/DELETE /api/v2/:tenant_slug/routing-policies/:model`(租户管理员,写审计)。关闭态:无 enabled 策略 → 15s 缓存查找,零 DB 热路径。
- faultsim:`ok-decision` 成功模式(固定概率分布,可被 criteria 关键词触发)。

## 第二波(控制台、文档、分析)

- **L5 新控制台(web/next)**:组织策略页「决策路由」页签;渠道运维页模态列与「不匹配」标记;定价页检索价编辑(区分「未配置」与「显式 0」);模型目录显示模态与计价单位;i18n 中英。
- **L6 分析与文档**:rankings `relay_mode|usage_unit` 分组接前端;`docs/openapi/relay.json` 补错误码;`doc/product-integration-guide.md` 加 Dify / LangChain / LlamaIndex 接入;runbook `systemone-channels.md` 加决策路由与响应校验;`doc/runbook/retrieval-metering.md` 新建。
- **L7 GPT-6 sol/luna**:核实 `model_family` 对 sol/luna 的定价与 cache 倍率,缺则补并加表驱动测试。

## 验证

- 每条 lane:hermetic 单测 + 变异证红;`go build ./...`、`go vet`、lint 零新增;web/next lint/typecheck/vitest/build 全绿。
- CI:真 PG 层(053/054 幂等、默认值、BIGINT)、`-race`、覆盖率闸不下调。
- 全栈验收:TC-R1 rerank search unit 精确金额、TC-R2 embeddings 2049 条 → 400、TC-R3 enforce 下不兼容渠道 501 带 reasons、TC-D1 决策路由在 `ok-decision` 下改写模型并返回头。
- 合并后生产默认态:`ROUTING_MODALITY_FILTER=observe`、无策略 → 行为与今天完全一致。
