# Newhub 企业交付能力推 10 分改造计划(2026-07-07)

前置评估:六维打分见会话评估(网关 7.5 / 多租户钱路 7 / 安全 7.5 / 运维 7.5 / 数据分析 6 / 工程交付 8,综合 7.2)。
本计划原则:**能复用 Lugo(platform-core)的经 capability API / NATS 复用,禁直连 schema、禁代码 import 耦合;数据面低延迟组件只借鉴模式、本地实现。**

## 一、Lugo 复用地图(实证走查 2l-svc-platform,2026-07-07)

| Lugo 能力 | 实现状态(证据) | newhub 复用方式 |
|---|---|---|
| 支付腿:alipay/wechat/stripe/creem/epay provider + PaymentCompletion Temporal workflow + webhook 幂等(Redis SET NX)+ 发票 PDF | 已实现可调用(`internal/adapter/payment/*.go`、`temporal/workflows/payment_completion.go:32`、`webhook.go:92,175,373`、`pkg/invoicedoc/pdf.go`) | **API 复用**:下单/回调/发票全归 Lugo;newhub 只消费 wallet 结果(现有 TopupCreditPool 链路已实证) |
| 通知:`POST /internal/v1/notify` + email/WS/FCM;**NATS LLM_EVENTS 消费者已在**(`consumer.go:100` 订阅 llm.quota.threshold/llm.usage.milestone,阈值 50/80/95/100) | 已实现可调用 | **NATS 事件复用**:newhub 本就是 publisher,扩事件类型即可;同步通知走 HTTP /internal/v1/notify |
| 限流:Redis 滑窗(ZSET)+ 本地 fallback,PerIP/PerUser/PerService(`pkg/ratelimit/limiter.go:106,156`) | 已实现 | **模式借鉴**(网关数据面延迟敏感,不跨服务调用;照实现模式在 newhub middleware 自建) |
| MFA:自建 TOTP enroll/verify(`app/mfa_service.go`,路由 router.go:518-522) | 半成品(无 OIDC ACR/amr、无通用 step-up API) | **模式借鉴**(备选方案 B);首选方案走 IdP ACR |
| 审计 append-only:DB trigger 阻断 UPDATE/DELETE(`migrations/081_audit_events_append_only.sql`) | 半成品(无哈希链) | **模式借鉴**:同款 trigger + 自建 hash chain |
| PG 备份:pg-backup CronJob(daily dump + weekly S3,默认 OFF)+ dr-drill manifests | 半成品(无 PITR) | 不复用服务;platform 侧 CronJob 已做全 DB 动态枚举(platform PR #42),apply 后 newhub db 自动受益 |
| 钱包/计费/身份(gRPC + Idempotency-Key) | 已复用且 STAGE 实证(2026-06-25 整链) | 现状保持 |

## 二、六维改造项(每项含验证方法)

### D1 网关 7.5→10
- **R1.1 per-user/per-token/per-model RPM+TPM 限流**:借 Lugo 滑窗模式自建 middleware(Redis DB 0,超限 429+Retry-After;新增 metric `rate_limited_total`)。验证:miniredis hermetic 单测 + 本地并发压测断言 429;-race CI。
- **R1.2 限流配置面**:tenant/token 加 rpm/tpm 字段(migration ID 先在 root migration-ledger 预留)+ console 设置页。验证:V2 API E2E 改配置即生效。

### D2 多租户+钱路 7→10
- **R2.1 TIER2 per-tenant unique**(users/tokens/redemptions 复合唯一):先 STAGE oracle 查冲突行做去重审计,再 struct tag + migration 同改。验证:pg-integration 真 PG 测试(跨租户同名 OK/同租户重复拒)。
- **R2.2 logs 租户隔离结构化**:logs 查询收敛到 repo 层强制 tenant scope(消除"漏写一处 Where 即越权"的结构性风险)。验证:越权单测(租户 A 查不到 B)+ 现有 E2E 14 项。
- **R2.3 topup 两步非原子加固**:stranded-debit 从日志兜底升级为 reconcile 后台任务 + 告警 metric。验证:注入失败的单测断言补偿动作。

### D3 安全 7.5→10
- **R3.1 step-up 真因子**(现 method="session" 无因子即盖章,`secure_verification.go:33-60`):方案 A(首选)OIDC 重认证——prompt=login(+acr_values),回跳校验 auth_time 新鲜度;方案 B(IdP 不支持时)照 Lugo TOTP 模式本地实现。验证:E2E——未 step-up POST /api/channel/:id/key→403,完成后→200。
- **R3.2 审计防篡改**:照 Lugo 081 加 append-only trigger + prev_hash/row_hash 哈希链 + 链校验端点。验证:pg-integration 断言 UPDATE 被 DB 拒;链校验单测。
- **R3.3 VerifyPhoneCode TOCTOU 原子化**(check-then-delete 释放锁窗口)。验证:并发单测多 goroutine 只 1 成功。

### D4 运维 7.5→10(封顶项 owner-gated)
- **R4.1 备份闭环**:owner apply platform PR #42/#43 + BACKUP_ENABLED + GRANT + newhub db 真 restore drill。验证:drill row-count gate PASS 报告。
- **R4.2 PG 高可用**:短期 wal-g PITR 演练;中期评估 CNPG/流复制(R6 资源约束,PROD 化时定)。验证:故障演练 runbook 实跑。
- **R4.3 CD 连贯性**:deploy-staging.yml 现 `if:false` 死禁用——改 R6 自托管 runner 或 CI 内 SSH 部署步。验证:push→自动 STAGE 部署→/api/health 200。
- **R4.4 pkg/common flaky -race 根治**(SafeGo 并发日志/verification store,预存于 main)。验证:CI -race 连续 3 轮绿。

### D5 数据分析 6→10
- **R5.1 模型性能监控产品化**:per-model/per-tenant p50/p95 延迟、错误率、TTFT 聚合端点 + console 页(数据已在 logs,只差聚合 API+UI)。验证:E2E + 与 /metrics 数值对账。
- **R5.2 用量报表**:CSV/月度账单导出;发票直接复用 Lugo invoice API。验证:E2E 下载与金额对账。
- **R5.3 通知闭环**:打通 quota threshold→NATS LLM_EVENTS→Lugo notification(先修 NATS 可达性:R5 桥 100.120.110.73:14222 不可达,改连 R6 集群内 NATS 或修桥)。验证:STAGE 真发事件,notification 投递记录可查。

### D6 工程/交付 8→10(多为 owner 决策)
- **R6.1** merge PR #39(覆盖率 69.1%,CI 全绿)。
- **R6.2** PROD 化决策:真客户落点(R6 正式化 vs R1)+ hub.lurus.cn 切换。
- **R6.3 支付接驳**:console topup→Lugo 下单 API→webhook→wallet→credit pool(后半段已实证);需 owner 配 provider 沙盒凭证。验证:STAGE 沙盒支付 E2E 全链。

## 三、批次与依赖

| 批次 | 项 | 特征 |
|---|---|---|
| 批次 1(本地可完成,先做) | R1.1/R1.2、R3.1/R3.2/R3.3、R2.3、R4.4、R5.1/R5.2 | 纯 newhub 代码,本地测试+CI 即可验证 |
| 批次 2(需 STAGE) | R2.1/R2.2(先去重审计)、R5.3、R6.3 | migration/跨服务,STAGE oracle 实证 |
| 批次 3(owner-gated) | R4.1/R4.2/R4.3、R6.1/R6.2、支付沙盒凭证 | 基建/决策/凭证在 owner |

诚实注记:批次 1+2 完成后预计 D1 10 / D2 9.5 / D3 9.5 / D5 9.5;D4 到 10 硬依赖 PG HA 与真 drill(批次 3),否则封顶 9。所有 migration ID 动手前在 root `doc/coord/migration-ledger.md` 预留。
