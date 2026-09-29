# 经销商技术 Due Diligence Checklist

> 给 {对方公司} 技术负责人在签 pilot 前过一遍。每一项都标了来源 — grep 哪个文件 / 哪个 ADR / sprint-status 哪段。
> 服务：`2b-svc-newhub`（hub.lurus.cn，STAGE: test-newhub.lurus.cn on R6）
> 当前部署状态参考：`_bmad-output/planning-artifacts/sprint-status.yaml` §`phase2_swarm_2026_05_18` + §`phase2_self_audit_2026_05_19`

---

## 1. 贵司能拿到什么

### 1.1 Provisioning API（程序化为 EndUser 创建 Key）

| Method | 路径 | 用途 |
|--------|------|------|
| `POST` | `/internal/v1/provisioning/tenants/{slug}/keys` | 创建一个 Token Key（一次性返回明文） |
| `GET` | `/internal/v1/provisioning/tenants/{slug}/keys?limit=&offset=&include_revoked=` | 列出已签发 Key（分页，最大 limit=100） |
| `DELETE` | `/internal/v1/provisioning/tenants/{slug}/keys/{key_id}` | 软删除一个 Key |

- 路由注册：`internal/adapter/handler/router/internal-api-router.go:133-139`
- Handler 实现：`internal/adapter/handler/provisioning.go:27-336`
- **Auth**: `Authorization: Bearer lurus_ik_<management-key>` 头，scope = `provisioning`（中间件 `internal/adapter/middleware/internal_api_auth.go` `InternalApiAuth`，scope check 在同文件 `RequireScope`）
  - **注意**：`X-API-Key` 仍接受但已弃用（响应带 `Warning: 299`）；两头都带时以 Bearer 为准。来源：`doc/contracts/switch-provisioning-api.md` §2
- **Cross-tenant 防护**：narrow-scope key（只有 `provisioning` scope）必须在 `internal_api_key_tenants` 表里有 `(api_key_id, tenant_id)` 白名单行；platform admin key（scope `*`）bypass。见 `internal/adapter/handler/provisioning.go:69-89`（Phase 2 self-audit 2026-05-19 安全修复）。
- **管理 Key TTL**：90 天，T-14d/T-7d/T-1d NATS 告警触发轮换（ADR §9 Q2，引于 `doc/contracts/switch-provisioning-api.md:53-58`）。

### 1.2 V2 多租户 API（贵司 Web Console / 后台用）

| Method | 路径 | Auth | 用途 |
|--------|------|------|------|
| `GET` | `/api/v2/{tenant_slug}/tokens` | UserAuth | 列 Token |
| `POST` | `/api/v2/{tenant_slug}/tokens` | UserAuth | 建 Token |
| `PUT/DELETE` | `/api/v2/{tenant_slug}/tokens/{id}` | UserAuth | 更新 / 删除 |
| `POST` | `/api/v2/{tenant_slug}/tokens/{id}/rotate` | UserAuth | 轮换 |
| `GET` | `/api/v2/{tenant_slug}/logs` (+ `/all`, `/cluster`, `/export`) | UserAuth | 调用日志 + CSV 导出（hard cap 50k 行） |
| `GET` | `/api/v2/{tenant_slug}/channels` 系列 | AdminAuth | 渠道（上游供应商）管理 |
| `GET` | `/api/v2/{tenant_slug}/models` | UserAuth | 可用模型清单 |
| `GET/POST` | `/api/v2/{tenant_slug}/pricing` | UserAuth | 单价 / markup% 配置 |
| `GET` | `/api/v2/{tenant_slug}/billing/invoices` | UserAuth | 账单 |
| `GET` | `/api/v2/{tenant_slug}/credit-pool/me` | ZitadelAuth | EndUser 自查 Pool 余额（whitelisted 字段） |
| `POST` | `/api/v2/{tenant_slug}/playground/run` | UserAuth | 多模型并行 Playground |
| `POST` | `/api/v2/{tenant_slug}/redeem` | UserAuth | 兑换码 |

- 完整路由：`internal/adapter/handler/router/api-v2-router.go:13-277`
- Wave 2/3 已落 ship：Dashboard QPS / TTFT P50 / Error rate（5-min window 派生）、CSV log export、模型 CRUD、Markup write path、Redemption codes。状态：`sprint-status.yaml` `hardening_swarm_2026_05_18.lane_b_v2_mock_clear`。

### 1.3 V1 兼容 API（newapi-style）

存在但 deprecation 进行中（参考 `epic-11: v1 Web UI Sunset` backlog）。Token / Log / Channel / Group / Redemption 的 v1 endpoint 在 `internal/adapter/handler/router/api-router.go`，适合迁移期客户暂时兼容用。新接入建议直接走 V2。

### 1.4 Relay endpoints（贵司 EndUser 实际发送 LLM 请求处）

- `/v1/chat/completions`, `/v1/completions`（OpenAI 格式）
- `/v1/messages`（Anthropic 原生）
- `/v1beta/models/*`, `/v1/models/*path`（Gemini 原生）
- `/v1/responses`, `/v1/images/*`, `/v1/audio/*`, `/v1/embeddings`, `/v1/rerank`
- `/v1/realtime`（WebSocket）
- `/mj/*`, `/suno/*`, `/v1/video*`, `/kling/v1/*`, `/jimeng/*`

来源：`internal/pkg/types/relay_format.go` + `internal/adapter/handler/router/relay-router.go` + `video-router.go`。

### 1.5 CreditPool 行为（预付 + 阈值告警）

- **数据模型**：`tenant_credit_pools`（一行/租户，含 `current_balance` / `max_balance` / `reset_period` / `alert_threshold_pct` / `alert_fired_at`）+ `tenant_credit_pool_draws`（append-only 账本，每次 debit 一行）。Schema: `migrations/012_create_tenant_credit_pools.sql:23-88`。
- **Relay 扣费 gate**：`internal/adapter/middleware/pool_balance_check.go:35-80`。无 pool 行 = 不限（bypass）；`max_balance == -1` = 不限；balance 用尽 = 402 `pool_exhausted`；DB 错误 = fail-open（schema dedup 兜底）。
- **Gate 覆盖的 relay group**（8 个）：`/v1`（chat）、`/v1beta`（Gemini）、`/mj`、`/suno`、`/v1/audio`（music）、`/v1/video*`、`/kling/v1`、`/jimeng`。来源：grep `PoolBalanceCheck` 在 `relay-router.go` + `video-router.go`。video 三个是 2026-05-19 self-audit 后补的（最早 ADR 只列 5 个，self-audit 发现缺口，已修）。
- **阈值告警**：达到 `alert_threshold_pct`（默认 80）发 NATS event 到 subject `pool.threshold.fired`（`internal/pkg/nats/pool_threshold.go:36-43` payload schema），Redis SETNX dedup 1h + DB `alert_fired_at` 1h schema dedup（双层）。
- **充值路径**：`POST /api/v2/admin/tenants/{id}/credit-pool/topup`，从 actor 的 Platform 钱包扣（`DebitWalletGRPC` → `TopupPool` 两步；中途失败会自动 `CreditWalletGRPC` 反向 revert；revert 也失败会写 STRANDED 日志给 ops，见 `tenant_credit_pool.go:262-290`）。
- **Reset 周期**：`monthly` 默认（ADR §9 Q1）；`none` 永不重置。

### 1.6 Switch GUI 白标 HMAC

- **目的**：给贵司 EndUser 分发的 Switch 桌面客户端，安装包里的 `whitelabel.json`（含 logo / 默认 endpoint / 租户 Key）由 HMAC-SHA256 签名，CDN / archive editor 篡改会被 Switch 启动时拒绝。
- **端点**：`GET /api/v2/admin/whitelabel/hmac-key?tenant_slug=<slug>` → 返回 `{"hmac_key": "<64 hex>"}`。Handler: `internal/adapter/handler/v2_admin_whitelabel.go:45-97`。
- **Auth**：`RootJWTAuth`（platform-admin 级，不是 reseller 自助）—— 我方运维代为 fetch + 配置进贵司打包流水线。
- **派生算法**：`sha256(LURUS_WHITELABEL_MASTER_SECRET + tenant_slug) → hex(32)`，确定性，无 DB 行。详见 ADR `doc/decisions/2026-05-20-orphan-features-3-whitelabel-hmac.md`。
- **失败语义**：master secret 未配 → 500 + 明确错误（fail-closed，不退化到 random）。租户不存在 → 404（防止 typo 签到错误 slug）。

---

## 2. 贵司需要准备的

### 2.1 一个 brand slug

- 格式约束：`a-z A-Z 0-9 - _`，长度 1~63，首字符不能是 `-` 或 `_`。来源：`internal/adapter/handler/oauth.go:879-899` `isValidTenantSlug()`。
- 建议格式：贵司品牌的 lowercase 简写，例如 `acme` / `acme-cloud` / `acme_inc`。会出现在所有 URL 里（`/api/v2/acme/tokens`）和 whitelabel HMAC 派生里，一经确定难改。

### 2.2 HMAC master secret —— 不需要贵司持有

- `LURUS_WHITELABEL_MASTER_SECRET` 是 **platform 侧**的全局 secret（存在我方 K8s Secret 里），贵司不需要自己保管。
- 我方按贵司 slug 派生贵司专属 HMAC Key，把这个 64-hex Key 一次性以加密信道（1Password / PGP）交给贵司打包工程师。
- 如果未来需要 per-reseller master secret（独立轮换），见 ADR §"What this ADR does NOT do" —— 当前架构不支持，需走单独 ADR。

### 2.3 预估月调用量

- 用于 calibrate `tenant_credit_pools.max_balance`。请提供：
  - 预估 EndUser 数（pilot 期 / 半年内）
  - 每用户每月调用次数 + 平均 prompt/completion token 数
  - 偏好模型组合（OpenAI / Anthropic / 国内 / 多家混路）
- 我方据此算出 quota unit 月预算 + 推荐充值频率。

### 2.4 客户群体定位

- 用于 calibrate SLA tier（`newhub.quality.sla_tier`: `none | bronze | silver | gold`，来源：ADR `2026-05-09-cost-aware-routing.md` §Q1）+ 路由策略（`strict` / `family-pinned` / `quality-tier` / `shadow`，同 ADR §2）。
- 例如金融 / 法律 / 医疗 → 默认 `strict` + gold SLA；个人开发者 / 创意工具 → `quality-tier` + bronze。

---

## 3. STAGE pilot 验收清单

R6 STAGE：`test-newhub.lurus.cn`。建议 pilot 期内逐项过完，作为上 PROD 的前置条件。

| # | 验收项 | 验证方法 | 来源 |
|---|--------|---------|------|
| 1 | 5 分钟内开租户 → 首次成功 chat 调用 | `POST /api/v2/admin/tenants` → 我方 issue 一个 internal_api_key → 贵司用 Provisioning API 建 1 个 user key → 用该 key 跑 `/v1/chat/completions` | 北极星 `ttft_minutes: 5` (`sprint-status.yaml:24`)；epic-10 backlog（"5-Minute Time-to-First-Token"）|
| 2 | 跨租户隔离 | 在贵司租户里建 user A 的 key + user B 的 key，用 A 的 key 读 B 的 log/quota → 应 403 / 404 | Provisioning cross-tenant guard `provisioning.go:69-89`；GetLogsV2 tenant ID filter (`v2_admin.go` + `api-v2-router.go:103-112`) |
| 3 | 跨 Reseller 隔离 | 模拟另一个 Reseller 的 narrow key 调贵司 slug 的 Provisioning API → 应 403 `TENANT_NOT_AUTHORIZED` | `InternalKeyAllowedForTenant()` + `internal_api_key_tenants` 白名单表（`migrations/013_create_internal_api_key_tenants.sql`） |
| 4 | CreditPool 余额对账 | 跑 N 次调用 → SUM(`tenant_credit_pool_draws.amount`) 应等于初始 `current_balance` 减去当前 `current_balance` | append-only ledger schema (`migrations/012_*.sql:52-74`) + atomic debit test (`TestDebitPool_AtomicRace`，sprint-status `phase2_swarm.repo_tests`) |
| 5 | Pool 耗尽 → 402 | 把 `max_balance` 设小，跑到 0，下一个 relay 调用应得 402 `pool_exhausted` 且不会真实打到上游 | `pool_balance_check.go:64-73` |
| 6 | 阈值 NATS 告警 | balance 跌破 `alert_threshold_pct` → NATS subject `pool.threshold.fired` 收到 1 条 event；1 小时内余额仍低不再次发（dedup） | `pool_threshold.go:22-29`（双层 dedup）；test cases 8/8（`sprint-status.yaml:242`） |
| 7 | Wallet topup revert | 强制让 `TopupPool` 失败，验证 `DebitWalletGRPC` 已 revert（看 `wallet_ledger` 是否产生反向 entry） | `tenant_credit_pool.go:276-290` + `doc/runbook/wallet-revert-stranded.md` |
| 8 | Whitelabel HMAC 派生 | `GET /api/v2/admin/whitelabel/hmac-key?tenant_slug=<贵司>` → 64-hex key；Switch 端用同算法验签 | `v2_admin_whitelabel.go:45-97` + `2c-gui-switch/bindings_whitelabel.go`（external repo）|

---

## 4. PROD 上线前要解决的 open questions

### 4.1 贵司需要回答

| 问题 | 决定影响 |
|------|---------|
| 计费节奏：月结 / 周结 / 预付？ | 决定 `tenant_credit_pools.reset_period` (`monthly` / `weekly` / `none`) + topup 频率 |
| Markup% 接受范围 | ADR `2026-05-09-cost-aware-routing.md` §Q1 是 "subscription + 钱包 markup%"；具体 % 走合同 |
| 客户数据保留期 | newhub log 默认无 TTL（参考 `epic-13: ClickHouse Insights Plane` backlog —— 长保留靠 Q4 上 ClickHouse） |
| 退出条款 | 数据导出 / Key 失效流程 / 合同剩余 pool 退款 |

### 4.2 我方要交付的（pilot 期内）

| 项 | 状态 | sprint-status 引用 |
|----|------|---------------------|
| STAGE chaos drill 跑通（circuit breaker / pg HA / WAL-G restore / quota backpressure / Phase 2 pool gate） | **待跑** | `phase2_swarm_2026_05_18.not_claimed_per_4_1_6:` "STAGE drill — pool gate / wallet topup / provisioning round-trip on R6" |
| 4 个 drill 脚本补缺口 | `chaos-drill.sh` 缺 Prometheus 验证 / log assertion / 自动 teardown；`pg-restore-drill.sh` 缺 RTO/RPO enforcement；`stage-smoke.sh` 多数 check 仅 wiring | `wave_a_kickoff_2026_05_20.day_1_recon_findings.drill_scripts` |
| Alertmanager receiver 接 Slack / PagerDuty | **未配** | 同上 `grafana.receiver_wired: false` |
| Migration 014 决策（Option A vs B） | **未决** | `phase2_self_audit_2026_05_19.not_claimed_per_4_1_6` |
| newhub 整合 newapi（D1 Option B）完成 | **进行中** | `doc/decisions/2026-05-20-d1-newapi-newhub-fork-final.md`；migration sequence T+0~T+28 |

### 4.3 双方协商的

- **SLA quality threshold**：auto-route 模式（`family-pinned` / `quality-tier`）要求合同条款 "drift below X% → 自动 revert 到 strict + 退款"。X 一户一谈。默认 pilot 期建议 `strict` + shadow，先看数据再开 auto-route。来源：`2026-05-09-cost-aware-routing.md` §5 Quality SLA contract clause。
- **响应延迟 SLA**：当前 P95 overhead 80ms（目标 50ms，未达），uptime ~98%（目标 99.5%）。来源：`sprint-status.yaml:26-28` `north_star_metrics.reliability_baseline`。pilot 期定 SLA 时务必基于实际测量值而非目标值。
- **支持 tier**：community / business-hours / 24×7。pilot 期默认 business-hours（北京时间工作日 10:00–19:00）。

---

## 5. 已知 drift / 诚实补充

来源全部为 `2b-svc-newapi/CLAUDE.md` "已知 Drift（追踪中）" + `sprint-status.yaml` `phase2_self_audit_2026_05_19.p1_p2_risk_register_deferred`：

- **NATS LLM_EVENTS 在 newapi 静默**：D1 切回 vanilla newapi 后该事件链路失业。newhub 侧 NATS publisher 完整（包括 `pool.threshold.fired`），但跨服务事件桥接尚未在 STAGE 验证。
- **C2~C9 risk register**：Phase 2 quota integration、frontend lifecycle、concurrent topup+debit race、monthly reset boundary、50M-row tokens 表的 migration 012 backfill —— 这些项只有 markers，没有 STAGE 测量。pilot 期间将逐项跑 drill 补齐证据。
- **Provisioning + admin handler 测试覆盖**：5 admin + 2 provisioning handler 当前没有 handler-level 测试（OAuth/JWKS test debt 阻塞 broad gate），逻辑层有 repo + entity 测试覆盖。来源：`phase2_swarm_2026_05_18.not_claimed_per_4_1_6` + `phase2_self_audit_2026_05_19.p1_p2_risk_register_deferred.C1`。

---

## 6. 联系节点

| 阶段 | 接口人 | 频次 |
|------|--------|------|
| 商务 / pilot 谈判 | Anita（Lurus Platform） | ad-hoc |
| 技术接入 / Provisioning API 联调 | Lurus tech lead（待派） | pilot 期 daily |
| 故障 / oncall | Lurus on-call rotation（business-hours pilot tier） | T+1h 内响应 |
| 周报 / 数据对账 | Anita | 周二 |
