# 企业中转站交付路径：newhub × platform 接驳式分工

> **状态**：提案（proposal，未经 owner ratify）｜2026-06-09｜基于四路独立调研交叉综合
> 调研源：① fork 能力盘点（~40 项 vs 上游 New API）② platform openapi 全量集成面 ③ 既有 ADR/sprint 战略梳理 ④ 企业网关市场对标（LiteLLM/Portkey/Kong + 国内合规市场）
> 对齐既有决策：边界定律 ADR 2026-05-08、D1 newapi 退役 2026-05-27、开源战略 2026-05-28（生存期冻结 newhub 私有扩张）、D3 收钱链 2026-05-30、Q3 生存目标（MRR ≥¥10K / ≥5 付费客户）

## 一句话

卖的不是「另一个 NewAPI 站」，是 NewAPI 结构性给不了的**治理溢价 + 中国企业商务闭环**：
platform 出「账号/钱/票/合规」，newhub 出「LLM 域请求级治理」，两者只经标准插口接驳——且可行路径**不是造新能力，是修通 3-4 个收钱链接缝**（能力 90% 已建成）。

## 1. NewAPI 做不到、我们能交付的五件事

| # | 差异化 | NewAPI 为何做不到 | 我们的现状 |
|---|--------|------------------|-----------|
| 1 | 企业身份三件套 SSO/SCIM/RBAC | 架构性缺失（仅社区 OAuth 登录，扁平权限） | Zitadel OIDC + token scopes 已 shipped；SCIM/SAML design done（blocked on Okta trial） |
| 2 | 操作审计 + 合规 | schema 无审计设计，不满足等保 | newhub 7 年审计/指纹/cost-spike 已 shipped；platform PIPL 导出/注销 API 已建成 |
| 3 | 中国商务闭环：CNY 对公/专票/合同/数据不出境 | 开源工具无商业实体；LiteLLM/Portkey 同样给不了 | platform 发票 API 已建成（缺专票状态机/对公核销）；这是对国际产品的**真壁垒** |
| 4 | 可核验计费（反欺诈定位） | 市场 45.83% 中转端点模型造假，17 家主流站 11 家是无备案 OneAPI/NewAPI | usage reconciliation + switch 公开定价 + 请求指纹已 shipped =「可审计的诚实中转」卖点 |
| 5 | 一个企业账户全产品通用（接驳式） | 单体 NewAPI 无跨产品账户概念 | platform 统一钱包/订阅/entitlement；Switch/lutu/kova 共用——唯 newhub 接缝未通（见 §3） |

AGPL 注意：NewAPI 附加条款禁止未授权商用中转——本身就是竞品采购拦路虎，也是我们 2026-05-28 脱版决策（终局：控制面迁 platform-core，fork 退化无状态代理）的依据。**生存期内不在 fork 里堆新私有能力，只修接缝。**

## 2. 分工铁律（重申 ADR 2026-05-08，杜绝重复建设）

- **platform**：账号/SSO/MFA、Org 与子账号、钱包/支付/发票/退款、订阅/entitlement、通知、PIPL/DSAR。**对 LLM 无知**——只见「账户 X 消费 Y 元（product_id 归因）」。
- **newhub**：tenant/channel/token/relay、请求级治理（audit/fingerprint/scope/cost-spike）、成本智能、reseller provisioning、白标 HMAC。**不碰钱**：不签 session、不存密码、记账真源在 platform。

已识别的重复风险与处置：

| 重复点 | 处置 |
|--------|------|
| newhub 本地 quota/topup/兑换码（NewAPI 遗留小钱包） | 收敛：platform 钱包为记账真源，newhub quota 退化为缓存/限流器。spend-linkage 在做，`/internal/admin/convergence-stats` 是收敛度量 |
| newhub `tenants` 表 ∥ platform Org/Zita Tenant（两套组织模型平行未桥接） | 桥接 1:1（tenant.platform_org_id），**不在 newhub 造子账号/部门体系**——部门钱包、Org API key 全用 platform 已建成 API |
| 审计两处 | 分层：账户级 = platform adminListAuditEvents；请求级 = newhub audit_events。客户视图聚合展示，不互相复制存储 |
| 白标 | platform Org 级白标配置（ADR 0021，已建成未消费）管品牌/支付/issuer；newhub 只管 Switch 包签名 HMAC |
| 发票/对公/月结 | 全部 platform 职责；newhub 仅供 per-tenant/model 用量明细数据源 |

## 3. 五个标准插口（公司级接驳协议——所有产品 dock 同一套）

1. **身份**：zita SDK / lurus_session（newhub 已接：OptionalZitaIdentity + authHelper SDK 分支）
2. **计费**：ReportUsage（带 product_id）+ WalletDebit/PreAuth/Settle/Release
3. **权益**：GetEntitlements（功能门/下载门，release_gate 已示范）
4. **事件**：NATS（llm.quota.threshold 已发布 → notification 推送）
5. **LLM**：newhub `/v1/*`（lutu/lucrum/kova/Switch 全走它，不各自接厂商）

newhub 自身也只是 platform 的标准消费者——新产品接入成本 = 接 5 个插口，而非各自造轮子。

## 4. 收钱链断点与改进 backlog（按 ROI 排序）

### P0 — 不修就收不到钱

| 项 | 内容 | 归属 | 状态 |
|----|------|------|------|
| P0-1 | **SEAM S1**：platform 订阅/充值 ↛ newhub credit pool 供给（两套 entitlement 互不通信，客户付费后 gateway 仍 402）。D3 已拍板 model (b)：platform BillingOutbox 喂池 → newhub 需幂等 fund 接收端点 | newhub 侧端点 + platform 侧 outbox | newhub 侧 ✅ DONE（PR newhub#14 CI 14项全绿 2026-06-10：端点+UNIQUE(event_id) 幂等+migration 019 已 commit,contracts.md 已登记 IMPLEMENTED;剩 platform 侧 outbox 调用方 ❌ 未动） |
| P0-2 | 钱包幂等键（ADR D4）：platform `/wallet/debit\|credit` 无 Idempotency-Key 服务端去重，重试可能双扣 | platform | ❌ 未动（platform session） |
| P0-3 | `internal/app/quota.go:542` 池扣减与 quota 写非原子（2026-05-30 审计 HIGH） | newhub | ✅ DONE（batch 3 2026-06-10：overdraft 化非共享事务——ErrPoolExhausted→负余额+relay_overdraft 账行,守恒律无条件成立;DB 硬错误残留面=CRITICAL log+credit_pool_debit_lost_total;ADR doc/decisions/2026-06-10-pool-overdraft-semantics.md;race 守恒测试 20 并发全落账终值 −147） |
| P0-4 | hub.lurus.cn DNS + R6 STAGE seed 数据（drill 全阻塞在 DB 0 行） | owner 动作 | 🔒 owner-gated |

### P1 — 企业成交关键（deal-breaker 级）

- 双 issuer 支持（S3 地雷：rebrand 时全线 401）→ ✅ DONE（PR newhub#14 CI 全绿 2026-06-10：`OIDC_ISSUER` 逗号分隔,zitadel_auth/admin_jwt_auth/release_gate 三校验点共用 issuer set;oauth.go 回调有意不改——rebrand 前实例 issuer 未变;aud allow-list 仍未做）
- PIPL §47 级联删除 → ✅ newhub 侧 DONE（batch 3 2026-06-10：`POST /internal/v1/privacy/erase` 幂等端点 migration 020 + leader-only crash-resumable executor,处置表+诚实边界已登记 contracts.md;剩 platform 侧冷静期到期调用 worker ❌ 未动——newhub 不消费 NATS）
- Org↔tenant 桥（消费 platform Organization API，替代自建）
- 专票状态机（申请→审核→已开→邮寄）+ 对公转账核销 API —— **platform 职责**，newhub 不做
- SCIM/SAML（H1.1）—— 🔒 blocked on Okta trial（owner）
- 正式 SLA/合同/DPA 文档 —— 非代码，商务物料

### P2 — 治理加分项（生存期后）

- RBAC 部门级模型白名单（基于 token scopes 延伸）
- Guardrails 框架（PII masking/prompt injection——市场加分项非成交项）
- 钉钉/企微告警 sink（走插口 4，notification 侧加 channel，不进 newhub）
- 月结账期/部门级账单（platform）

### 明确不做（对齐冻结决策）

- 不在 newhub 里造：组织模型、钱包、订阅、发票、语义缓存、prompt registry
- Tier-3 模态（MJ/Suno/Realtime）继续按 E9 砍除路线

## 5. 持续改进环

- **度量**：convergence-stats（双账本收敛率）+ 每条接缝一个 e2e drill（STAGE）+ Q3 北极星（MRR/付费客户数）
- **节奏**：每 batch = 侦察（只读 agent）→ 修缝（写 agent，避开外部并发区/worktree 隔离）→ 独立 oracle 验证（build/test/grep，不信 agent 自报）→ 勾选本文档
- **2026-06-09 batch 1**：P0-1 newhub 侧 fund 端点 + P1 双 issuer（已启动）；P0-3 等分支收口；P0-2/专票升级到 platform session
- **2026-06-10 batch 2**：工作树收口 ✅ — CRLF 去噪+.gitattributes、三股工作拆 10 commit、merge main、PR newhub#14 CI 14 项全绿（race+pg-integration 含 FundPoolIdempotent）、coord 四件登记。batch 3 待办：P0-3 quota.go 池扣减原子化、PIPL 级联删除、~16 页 i18n sweep、platform session（P0-2 幂等键+BillingOutbox 调用方+专票）
- **2026-06-10 batch 3**：✅ — P0-3 overdraft 化 + PIPL §47 erase 端点/executor（migration 020）+ v2 console 全量 i18n（16 页+3 hifi 组件,console 树 938 keys en/zh parity,DesignSystem/States/Variants dev 页除外）。剩余皆 platform session / owner-gated：P0-2 幂等键、S1 outbox 调用方、PIPL 调用 worker、专票、P0-4 DNS/seed

## 附：遗留补丁处置

`lurus/doc/patches/2026-05-30-newhub-industrial.patch`（仅 web/：showError 人话化 + Billing 充值输入）与未提交的 v2 console i18n/错误体验工作**部分重叠**（showError 已被 errorMessages.js 方案超越）。处置：Billing 页 i18n sweep 时人工 reconcile 该补丁的充值输入部分，不直接 apply。
