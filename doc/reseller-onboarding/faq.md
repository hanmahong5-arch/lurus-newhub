# 经销商 FAQ — 10 个最常问的问题

> 每条引代码/ADR 出处。服务 `2b-svc-newhub` (hub.lurus.cn), STAGE `test-newhub.lurus.cn` (R6)。深度细节见 `technical-checklist.md`(canonical, grep-cited)。

**Q1 计费?** CreditPool 预付池。贵司从 Platform 钱包 topup 到 pool(记 `tenant_credit_pool_draws` ledger),EndUser 每次 relay 按 `cost = upstream_cost_cny × (1 + markup%)` 实时扣;`max_balance` 可配 ceiling(`-1`=不限);余额 ≤ `alert_threshold_pct`(默认 80%)发 NATS 告警;耗尽→402。来源 `tenant_credit_pool.go:228-310` + `pool_balance_check.go:35-80` + ADR `2026-05-09-cost-aware-routing.md` §Q1。

**Q2 客户 Key 你们能看?** 技术上 platform admin scope `*` 可查任意租户;合同上不会。Reseller narrow-scope key(仅 `provisioning`)需 `internal_api_key_tenants` 白名单 `(api_key_id, tenant_id)` 行才能跨租户,缺行=403 fail-closed。跨租户访问记 audit log(`GET /api/v2/admin/audit/events`, RootJWTAuth, rate-limited)。来源 `provisioning.go:69-89` + `migrations/013_*` + `api-v2-router.go:267`。

**Q3 撞墙?** (a) per-channel circuit breaker `internal/pkg/resilience/circuitbreaker.go`(env `CB_THRESHOLD`/`CB_TIMEOUT_SEC`, 三态);(b) 多 channel fallback;(c) `IsUpstreamFailure` 过滤避免 5xx 误判 retry。诚实: STAGE chaos drill 仍排期(`sprint-status.yaml` `wave_a_kickoff`),无完整 RTO/RPO 数据,pilot 期补齐。

**Q4 换模型/路由?** 四种 per-tenant mode: `strict`(pin,默认)/`family-pinned`/`quality-tier`/`shadow`(双跑对比不切)。auto-route 必须 opt-in + quality SLA 条款(drift 超阈值自动 revert + 退款)。来源 ADR `2026-05-09-cost-aware-routing.md` §2。pilot 期建议 `strict` + `shadow`。

**Q5 上游涨价我吃亏?** 不会。wallet pass-through + 固定 markup%(合同数字,无隐藏 margin),涨价直接 pass-through 到 EndUser 账单。Subscription 部分固定月费。来源 ADR §Q1。

**Q6 数据保护?** 每租户隔离: (a) 所有 query 加 `tenant_id` filter(schema 级, 如 `GetLogsV2`/`ListTokensV2`);(b) 跨租户走 `internal_api_key_tenants` 白名单;(c) Zitadel Org 一对一映射 tenant + OIDC org claim 校验。At-rest 加密依赖 R1/R6 PG(K8s Secret 管 SQL_DSN, pgvector 加密 TBD)。来源 `api-v2-router.go:103-127` + `provisioning.go:69-89` + `doc/runbook/tenant-onboarding.md`。

**Q7 对账?** 每次 relay 产生 (1) `logs` 行(model/tokens/cost/timestamp/token_id) + (2) `tenant_credit_pool_draws` ledger(append-only, pool_id/amount/log_id/reason)。对账: `SELECT SUM(amount) FROM tenant_credit_pool_draws WHERE tenant_id=? AND direction=debit` = `(initial - current)_balance`。Atomic invariant 已测(`TestDebitPool_AtomicRace`: 20 goroutine×10 vs pool 50 → 5 ok/15 reject/0 negative)。来源 `migrations/012_*.sql:52-74` + `sprint-status.yaml:241`。

**Q8 Switch 白标?** HMAC-SHA256 sidecar。(1) 我方 `GET /api/v2/admin/whitelabel/hmac-key?tenant_slug=<贵司>`(RootJWTAuth)→ 64-hex key;(2) 贵司打包流水线签 `whitelabel.json`(logo/默认 endpoint/租户 Key);(3) EndUser 启动验签,篡改拒载。算法 `sha256(master_secret + tenant_slug)`, 确定性, 无 DB 行。来源 `v2_admin_whitelabel.go:45-97` + ADR `2026-05-20-orphan-features-3-whitelabel-hmac.md`。

**Q9 你们 down?** 客户端: HTTP 503/502 → Switch retry + 切备用 endpoint。服务端: circuit breaker 熔断坏 channel + 切兄弟 channel;multi-key pool 轮转;Postgres 单实例 StatefulSet,每日 pg_dump + 宿主异地 rsync + 每周恢复演练,无流复制、无 WAL 归档(RPO 最长 24h,`doc/runbook/pg-restore.md`);Redis 故障退化到 cookie session。诚实: monthly uptime ~98%(目标 99.5%),STAGE chaos drill 未跑通,多 region failover 未实现。来源 `sprint-status.yaml:26-28` + epic-7。

**Q10 退出?** (1) **数据导出**: `GET /api/v2/{slug}/logs/export`(CSV, cap 50k 行/次);token 列表 `GET /api/v2/{slug}/tokens` + Provisioning `GET /internal/v1/provisioning/tenants/{slug}/keys`。(2) **Key 失效**: `DELETE` 单 key 软删 + 运维 `DeleteTenant`(`api-v2-router.go:230`)。(3) **剩余 Pool 退款**: `current_balance` 反向 credit 回钱包(`CreditWalletGRPC`, 原子, `tenant_credit_pool.go:279-284`)。合同退出条款建议 30 天预通知 + 数据保留 90 天(待商务填)。
