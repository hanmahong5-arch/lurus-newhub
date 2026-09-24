# 第十四轮 — 探针优先（cycle 14）

基线 HEAD `773145d8`（main，PR #190 + #191 已上线，prod 3/3 + UAT 1/1 同 digest `445612a5`）。

## 为什么这一轮换组织原则

第十三轮的真缺陷（402 上的全角货币符）不是任何闸门发现的，是一条活探针发现的。复盘出的结构原因有两条，都不靠加测试能解决：

1. 字面量 ASCII ≠ wire ASCII——金额是跨包插进来的，静态扫描看不见。
2. 同一个错误码有两条路径，绿的那条测的是另一条。

所以本轮先设计探针、再让探针决定车道。侦察阶段 12 个面 + 每面一个怀疑者（24 agent，1792 次工具调用），产出 73 条 finding（怀疑者判 70 STANDS / 2 WRONG / 1 ALREADY_COVERED）+ 36 条 auditor_missed；**怀疑者只杀掉 3 条这个比例本身不可信，所有进入车道的 P1 均由操作员亲手重验**。与此并行，操作员自己在 UAT 上跑了 4 轮活探针，独立发现 9 条，其中 2 条是本轮最重的。

## 亲手重验过的 P1（四条，每条都给出重验证据）

### P1-A 出厂默认下中转层没有任何故障转移

`internal/pkg/common/constants.go:108` 是 `var RetryTimes = 0`。`relay.go:381` 的循环条件是 `retryParam.GetRetry() <= common.RetryTimes`，在 0 时只跑一次；`relay.go:514` 的 `shouldRetry(..., common.RetryTimes-retryParam.GetRetry())` 拿到 0。

**live 核实**：生产库 `options` 表一共只有 6 行，且 `RetryTimes` 无行（`ChannelDisableThreshold` 同样无行——这一点同时纠正了记忆里"生产该值由 DB option 设为 5"的旧记载）。所以生产今天就是 `RetryTimes=0`。

上游一次 5xx，客户直接拿到 5xx，哪怕同模型另有可用渠道。`channel_select.go:74-165` 整套跨分组、跨优先级重试注释描述的机制一次都不会执行。

### P1-B 渠道断路器会永久卡在 half_open

- `resilience/circuitbreaker.go:86-90`：Open 且超时到，切 HalfOpen 并**放行**一个探测请求。
- `circuitbreaker.go:92-93`：HalfOpen 下 `allow()` **无条件返回 false**，且该状态**没有超时出口**（超时检查只存在于 Open 分支）。
- `relay.go:507`：只有 `types.IsUpstreamFailure(newAPIError)` 为真才调 `RecordFailure`。

所以只要那个被放行的探测请求以非上游错误收场（客户 4xx、402 余额不足、客户端取消），`RecordSuccess` 与 `RecordFailure` 都不会被调用，断路器永久卡死，该渠道**到进程重启为止不再接任何流量**。

### P1-C 计费断路器被它自己的就绪探针卡死

- `deploy/k8s/r6-stage/deployment.yaml:338-344`：readinessProbe 每 **5 秒**打 `/api/health`。
- `health.go:106`：调 `common.BillingBreakerAllow()`——**这是会改状态的函数**。
- `billing_breaker.go:45-49`：Open 且超时到，翻 half-open、**消耗掉唯一的探测名额**、返回 nil。健康处理器只记一句 `"billing":"ok"`，**从不调用 Success 或 Failure**。
- `billing_breaker.go:53-54`：half-open 对所有调用方返回错误，且只有 Success/Failure 能离开该状态。

平台抖一下把断路器打开后，5 秒内就绪探针就把它永久卡在 half-open，此后每次预授权/结算/释放都被拒，直到 Pod 重启。更糟的是 `BillingBreakerIsOpen()` 对 half-open 返回 false（`billing_breaker.go:61-63` 注释写明），**降级路径也不会启动**——既不计费也不降级。三副本乘每 5 秒一次，这个卡死几乎是必然的。

### P1-D 管理端补充信用池：扣一次钱、入两次账

- `tenant_credit_pool.go:312-322` 用 `Idempotency-Key` 调 `debitWallet`，平台侧去重，钱只扣一次。
- `tenant_credit_pool.go:336` 的 `TryFinalizeStrandedTopup` **只处理滞留意图**（只有 TopupPool 与 revert 双双失败才会写滞留行）。

对一次**成功过**的补充做同 key 重试，落不到滞留分支，于是直接走到 `tenant_credit_pool.go:370` 的 `repo.TopupPool`，**第二次给池子入账**。

仓内已经有正确的原语 `repo.FundPoolIdempotent`（`repo/tenant_credit_pool.go:387-472`，唯一索引 `(tenant_id, event_id)`，migration 031），内部路径在用，管理端没用。

## 操作员活探针独立发现（UAT，四轮，字节为证）

### PA 浏览器来源在中转 API 上被空体 403（P1）

`POST /v1/chat/completions` 带 `Origin: https://app.customer.example` 得 **403 且响应体为空**；同一请求不带 Origin 则正常走到鉴权。影响 `/v1`、`/v1/messages`、`/mj`、`/internal` 全部，且发生在**鉴权之前**。

根因：`relay-router.go:14` 把控制台那套 `middleware.CORS()`（`AllowOrigins` 就是 `ALLOWED_ORIGINS` 里的两个控制台域名）原样挂到了中转路由上。

作为安全控制它什么也没挡住——`/v1` 是 bearer 鉴权、不吃 cookie，本来就没有 CSRF 面，攻击者不发 Origin 即可绕过；它只伤害诚实的浏览器客户端，而且不给任何可调试信息。**没有任何测试能看见它，因为没有测试会带 Origin 头。**

### PB 成功的中转调用把上游厂商的响应头原样转发给客户

实测到的头包括 `Server: openresty`、`Eo-Cache-Status`、`Eo-Log-Uuid`、`X-Ds-Trace-Id`、`Strict-Transport-Security`。这既暴露了我们把请求路由给了哪家供应商，也把上游的 HSTS 施加到我们自己的域上。

### PC `X-Model-Provider` 是假的

`perception.go:157` 与 `helper/common.go:63` 写的是 `constant.GetChannelTypeName(info.ChannelType)`——**适配器族**而不是供应商。实测一个 deepseek 模型返回 `X-Model-Provider: OpenAI`。该头在 `docs/openapi/relay.json` 里有正式文档，并专门列进 `cors.go:24` 的 `CORSExposedHeaders` 供浏览器读取。

### PD 流式响应缺三个已文档化的头

非流式有 `X-Request-Cost`、`X-Quota-Remaining`、`X-Model-Provider`；流式一个都没有（头必须在首帧前写完，那时费用还不知道），而契约没有说明这个不对称。末帧的 `x_lurus` 另把 `billing_mode:"legacy"` 发给了客户。

### PE wire 错误消息五处不体面

- **重复前缀**：`distributor.go:335` 返回 `"invalid request, "` 拼错误，`distributor.go:71` 再前置一次 `"Invalid request, "`，客户看到 `Invalid request, invalid request, unexpected end of JSON input`。
- **结构体名外泄**：`max_tokens: -5` 得到 `json: cannot unmarshal number -5 into Go struct field GeneralOpenAIRequest.max_tokens of type uint`。
- **内部组件名**：`distributor.go:259,272` 把 `(distributor)` 写进 404/503 消息。
- **报错指错原因**：`common/gin.go:88-90` 对非 json/form 的 Content-Type 直接跳过解析（源码注释是 skip for now），于是"内容类型不支持"变成了"没写模型名"。
- **中文 401**：`middleware/auth.go:156,191,205,224,232,343,360` 八处用 `c.JSON(gin.H{...})` 直写中文。该文件**就在语言闸的扫描根里**，闸看不见它不是因为根不够宽，而是因为 sink 列表只认 `abortWithOpenAiMessage`、`MidjourneyErrorWrapper`、`types.NewError*`、`errors.New`、`fmt.Errorf`，不认 `c.JSON`。

### PF 发布的 OpenAPI 规范标准解析器解不开

`docs/openapi/relay.json` 与 `api.json` 带 UTF-8 BOM，标准 JSON 解析器（含 Go 的 `encoding/json`）直接拒绝；`api-v2.json` 是干净的，所以这不是约定而是不一致。

**而我们自己的契约锁 `openapi_contract_lock_test.go:52` 明写了一行把 BOM 前缀剥掉再解析**——测试是照着我们的产物写的，不是照着"别人能不能用"写的。这是本轮论点最干净的一例。

### PG 心跳把"没带头"报成"已吊销"

`user_heartbeat.go:18-21` 的契约注释写死 401 等于永久吊销、客户端立即锁 UI；而 `user_heartbeat.go:80` 对"缺 Authorization 头"走的就是 401 revoked。

## 不作为缺陷、但明确记为未核实

5 条告警指向的序列在运行实例上零样本：`lurus_billing_advisory_meter_lost_total`、`lurus_gateway_circuit_breaker_state`、`lurus_gateway_credit_pool_lookup_miss_total`、`lurus_gateway_panics_recovered_total`、`lurus_gateway_relay_failover_suppressed_total`。

五条都已声明（`metrics/billing.go:104`、`metrics/metrics.go:230`、`metrics.go:441`、`metrics.go:162`、`metrics/panic.go:17`），无样本是因为它们是带 label 的 Vec 且对应条件从未发生。正确结论是**这 5 条告警链路从未被走通过一次**，不是"死序列"。配合 Slack webhook 未设（owner O2），它们的有效性至今零证据。

## Lane 结构

文件所有权互斥；`internal/adapter/handler/router/*.go`、`cmd/server/main.go`、`deploy/k8s/**`、`web/src/App.jsx` 由末位串行 W 独占。

| Lane | 主题 | 量 |
|---|---|---|
| L1 | 两个断路器的永久卡死（P1-B、P1-C）+ 渠道选择阶段无转移 | M |
| L2 | 出厂默认与文档不符（P1-A）+ shipped-defaults 闸 | S |
| L3 | 信用池重复入账（P1-D）+ checkout 随机幂等键 | M |
| L4 | wire 消息卫生（PE 五条）+ 把语言闸的 sink 与根都加宽 | M |
| L5 | wire 信封正确性（Claude 的 nil type、SetMessage 被丢弃、Gemini 路由信封） | M |
| L6 | 响应头卫生与诚实（PA CORS、PB 上游头、PC 假供应商、PD 流式不对称） | M |
| L7 | 发布契约可用性（PF BOM、7 个不存在的 v2 路由、README）+ 严格解析闸 | S |
| L8 | 控制台把读失败渲染成"零"（三页）+ 失败态闸 | M |
| L9 | 平台集成诚实（配置轮询、VIP 用量状态码、对账端点鉴权、400 误标余额不足） | M |
| W | 接线：router 挂载、manifest、闸门登记、real-chain 测试 | S |

## 每条车道的 Owned 文件

- **L1**：`internal/pkg/resilience/circuitbreaker.go`、`internal/pkg/common/billing_breaker.go`、`internal/adapter/handler/health.go`、`internal/adapter/handler/relay.go` 及各自测试。
- **L2**：`internal/pkg/common/constants.go`、`internal/adapter/repo/option.go`、新建 `internal/pkg/gates/shipped_defaults_test.go`、`.env.example`、相关 runbook。
- **L3**：`internal/adapter/handler/tenant_credit_pool.go`、`internal/adapter/repo/tenant_credit_pool.go`、`internal/adapter/handler/v2_billing.go` 的 checkout 幂等键部分及测试。
- **L4**：`internal/adapter/middleware/distributor.go`、`internal/pkg/common/gin.go`、`internal/adapter/middleware/auth.go`、`internal/adapter/middleware/wire_message_language_gate_test.go`。
- **L5**：`internal/pkg/types/error.go`、`internal/pkg/types/gemini_error.go`、`internal/adapter/middleware/wire_format.go` 及测试。
- **L6**：`internal/adapter/middleware/cors.go`、`internal/app/relay/helper/perception.go`、`internal/app/relay/helper/common.go`、上游响应头剥离点。
- **L7**：`docs/openapi/` 全部、`README.md`、新建严格解析闸（由 W 代挂到 router 包）。
- **L8**：`web/src/pages/v2/Admin/ModelRateLimits/`、`web/src/pages/v2/Admin/Authz.jsx`、`web/src/pages/v2/Billing/index.jsx`、`web/src/helpers/loadState.js`、`web/src/components/settings/ContentSettingPage.jsx`。
- **L9**：`internal/pkg/common/billing_config_poll.go`、`billing_client.go`、`billing_cache.go`、`identity_client.go`、`internal/adapter/handler/switch_reconciliation.go`。

## 执行协议

1. 九条车道并行开发，每条一个对抗验收加一轮修复。prompt 钉死：只写自己 Owned 文件、可读任何文件、禁 git 操作、禁改 prod、禁 ssh、禁出现任何 AI 模型名或 CLI 工具名、每个 oracle 先红后绿。
2. 每条车道验收通过后由操作员逐路径 stage 做 WIP commit；五个外来的 `coverage_*` 文件永不触碰。
3. W 串行接线加自有 real-chain 测试；操作员亲手收尾（散文、绝对词、闸门变异自检）。
4. 本机闸门：`go vet` 与 `go build`；`go test -short -count=1 -p 2 ./...`；结构闸门集；`golangci-lint run --new-from-rev=origin/main`；web 的 lint、eslint、test、build。`-race` 与覆盖率棘轮走 CI。
5. PR 到 CI 全绿后 merge，auto-pin，ArgoCD 收敛，然后**逐条用本轮设计的探针在 UAT 实证**（生产只读），最后报告。

## 验证要点（诚实闸）

- 本轮每一条"修好了"都必须用**与发现它时同一条探针**复测，并贴出字节。
- 每个新闸门自己做变异验证；闸门的**盲区必须写进它自己的头注释**（第十三轮的教训）。
- 否定式断言先证明查询本身可信。本轮已经有两次教训：一次是 `grep -P` 在本机 locale 下直接报错、被 `|| echo 0` 吞成"0 处"；一次是下"options 表没有这个键"之前先数了总行数。
- UAT 证不了的明写"未核实"，不降级成邻近信号。

## Owner 事项

沿用 O1、O2、O7、O-refund、O-retention、O-tgps、O-heartbeat、O-scrape、O-pool、O-pitr、O-erasure-backfill、O-erasure-inflight-refund、O-legacy-tenant、O-ratio-fetch、O-task-payer。本轮新增两条：

- **O-retry**：`RetryTimes` 改出厂默认后，生产是否还要用 option 覆盖成别的值由 owner 定；本轮只改默认并加闸。
- **O-alarm-proof**：那 5 条从未出现过样本的告警，需要 owner 在 R6 上人为制造一次条件把链路走通（而且 O2 的 webhook 必须先设，否则走通了也没人收到）。
