# Development Progress / 开发进度

> Last Updated: 2026-05-10
> Archive: doc/archive/process_v20260205.md (entries before 2026-02-04)
> **New Rule**: 每条目 ≤ 15 行（HARD LIMIT），只记录已完成工作的极简摘要

---

## 2026-05-10: Layer C bridge endpoint — POST /api/v2/auth/zita-bootstrap

昨晚遗留问题: entity.User 加了 LurusAccountID 但 GORM AutoMigrate 看的是 repo.User，
column 实际从未落地。今日修复+落实 bridge endpoint。

- `repo.User`: 加 `LurusAccountID *int64`（与 entity.User 同步；双 User 待整合）
- `migrations/011`: 手动 psql 跑掉，DB column + partial unique index 落地
- `repo.GetUserByLurusAccountID`: tenant-isolation-bypass lookup
- `handler.ZitaBootstrap`: SDK cookie → find-or-create user (`lurus_<account_id>`)
  → set V1 gin session → return user JSON for localStorage
- `ZitadelRedirect.jsx`: POST bridge 替换昨晚合成的假 user

Verification: `go build → OK`；`bun run build → OK`；STAGE 401 from SDK middleware
without cookie (route registered, image `main-20260510-9a01764` live).
**未验证**: 浏览器端真实 SDK cookie 的 e2e — 需手动登录 test-newhub 触发。
Pending: 删 oauth.go/zitadel_auth.go (~1.2k LOC) 等浏览器 e2e 通过后再做。
---

## 2026-02-25: 计费系统安全威胁模型修复（P0+P1）

修复 10 个安全漏洞，覆盖无限充值、签名绕过、竞争条件、幂等等问题。
- P0-1 `subscription_cron.go`: netDelta 分三步（扣费→补额→reset daily），TotalQuota=0 不补 quota
- P0-2 `topup_creem.go/subscription_payment.go`: 删除 TestMode 签名绕过，secret 空时直接拒绝
- P0-3 `topup.go`: 删除跨 Pod 无效的 LockOrder/UnlockOrder，EpayNotify 改走 ManualCompleteTopUp（DB FOR UPDATE）
- P0-4 `subscription.go`: ActivateSubscription 加 FOR UPDATE 幂等检查，状态非 Pending 则拒绝
- P0-5 `internal_api.go`: InternalGrantSubscription 补充 RecordLog 审计
- P0-6 `subscription_payment.go`: 金额不足改为返回 error 拒绝激活，容差改固定 50 cents
- P1-1 `rate-limit.go/api-v2-router.go`: 兑换码接口加 5次/分钟 IP 限速
- P1-2 `topup.go`: AdminCompleteTopUp 补充管理员 ID 审计日志
- P1-3 `user.go`: ResetDailyQuota 加 last_daily_reset < todayStart 幂等条件
- P1-4 `auth.go`: lurus-api-User header 改可选（仅验证不作为 ID 来源）

Verification: `go build ./... → OK`; `go vet ./internal/adapter/middleware/... → OK`
Remaining: P2 系列（int64 溢出、CSRF、JWT aud 验证）待排期。

## 2026-02-25: 计费系统评估 + 自动续费实现

完成多产品计费能力评估，修复自动续费 TODO。
- `subscription_cron.go: processOneAutoRenewal()` — 实现余额扣费自动续费，原子事务（双重检查锁 + gorm FOR UPDATE）
- 修正逻辑：扣 `plan.Price * QuotaPerUnit` + 补 `plan.TotalQuota`，net delta 一次写库
- `doc/billing-system-guide.md` — 修正支付宝/微信状态（仅 OAuth，非支付），更新自动续费描述（24h 触发），新增多产品能力评估章节
- 结论：AI 网关计费 ✅ 可撑，多产品中台 ❌ 需独立服务（推荐路径 B）

Verification: `go build ./... → OK`
Remaining: 自动续费余额不足时邮件通知（TODO 标注），退款/发票系统在 lurus-billing 规划中。

---

## 2026-02-13: Epic 6 Complete — Code Review & Security Hardening

**Completed Stories**: 6-1 ~ 6-10 (对抗性审查 + P0/P1/P2 修复)

**Key Deliverables**:
- P0-1: Git history cleanup (git-filter-repo, 5019 commits, secrets.yaml removed)
- P1: Config externalization (MinIO bucket, CORS, alipay prefix → env/constant)
- P2: context.Background() cleanup (22 fixes in 15 files)
- Tests: 40+ new tests (Alipay 14, Release 13, Model Sync 16)
- Docs: DEPLOY.md, TESTING.md, code review reports

**Verification**: `go test ./... → PASS`, `go build → OK`, Git history clean

**Remaining**: Force push cleaned history + credential rotation (out-of-sprint)

---

## 2026-02-11: Download System + SSO Phase 1

Backend API for release downloads + cross-domain SSO (cookie-based).

**Files**: migrations/005, domain/entity/release*, app/release_service, handler/release

**Verification**: `go build → 93MB`, 编译通过

**Status**: ⏳ 待数据库迁移 + MinIO 配置 + 前后端联调

---

## 2026-02-06: Tech Debt Cleanup

Fixed user_mapping insecure password, removed dead code, created 3 ADRs (HA/v1-deprecation/observability).

**Verification**: `go test ./... → PASS`, `go build → OK`

---

## 2026-02-05: Epic 2-5 Complete — Tests, Performance, Observability, DevEx

**Epic 2**: 187 service tests, 100+ adaptor tests, 50 controller tests, 34 security tests
**Epic 3**: Benchmarks (p95 <50ms), object pools, HA deployment (2 replicas + PDB)
**Epic 4**: Prometheus /metrics (11 types), OpenTelemetry tracing, 10 alerting rules
**Epic 5**: OpenAPI spec (45 endpoints), staging env, 6 runbooks

**Verification**: `go test ./internal/pkg/metrics → 7 PASS`, `go test ./internal/pkg/tracing → 9 PASS`

---

## 2026-02-05: Epic 1 Complete — Multi-Tenant Production Launch

Deployed V2 API to K3s with Zitadel OIDC auth.

**Key Commits**: 80323446b, 6232258ad, d85e5d422, d74a16a65

**Verification**: ArgoCD sync, pod 1/1 Running, /api/status→200, V2 login→302 to auth.lurus.cn

---

## 2026-02-04: Architecture Migration — Hexagonal Restructure

Migrated `biz/data/server` → `domain/app/adapter` (hexagonal architecture).

**Verification**: `go build ./cmd/server → PASS`, `go test ./... → PASS`

> **Archive Note**: Entries before 2026-02-04 in `doc/archive/process_v20260205.md`

---

## 2026-02-25: 修复注册流程邮件发送失败

排查 `SendEmailVerification` 全链路，发现三个根因并逐一修复：

1. `SMTPServer=localhost` → 改为 `stalwart.mail.svc`（K8s 集群内服务名）
2. Stalwart brute-force 封锁 Pod CIDR → 在 `stalwart-config` ConfigMap 加 `[server.allowed-ip] "10.42.0.0/16"=true "10.43.0.0/16"=true`，滚动重启
3. `SMTPAccount=noreply@lurus.cn` → Stalwart 内部目录只支持短账户名，改为 `noreply`；`SMTPFrom` 保持完整地址

Verification: `curl https://api.lurus.cn/api/verification?email=tpy@lurus.cn → {"success":true}`；Stalwart 日志确认 `queue.queue-message-authenticated` + `delivery.completed`

---

## 2026-02-25: 配置 DKIM 签名

Stalwart `auth.dkim.sign = 'rsa-lurus.cn'`（selector=`default`）即实际出站签名配置，`session.data.sign` 为入站处理。
关键修复：`%{file:...}%` 宏在 RocksDB 中不展开，需内联 PEM；将 PKCS#8 转为 PKCS#1 写入 config.toml。

**DNS**: 添加 `default._domainkey.lurus.cn TXT "v=DKIM1; k=rsa; p=MIIBIjAN..."`

**Verification**:
- `dig TXT default._domainkey.lurus.cn +short` → 返回完整公钥记录
- 私钥提取公钥与 DNS 公钥 base64 完全一致（openssl 确认 2048-bit RSA）
- 实测：noreply→QQ Mail 投递成功，`DKIM-Signature: v=1; a=rsa-sha256; s=default; d=lurus.cn`

## 2026-02-25: Security Fix Supplement Tests (P0-1/P0-2/P0-4/P0-6/P1-3/P1-4)
Fixed 1 broken test (empty_secret_test_mode now expects false). Added 6 new/extended test files covering processOneAutoRenewal (5 subtests), verifyCreemSubscriptionSignature (4 subtests), amount tolerance validation (4 subtests), ActivateSubscription idempotency, ResetDailyQuota idempotency, lurus-api-User header (4 subtests).
Also fixed GREATEST() SQLite incompatibility in subscription_cron.go/subscription.go via quotaDeductSafe() helper.
Verification: `go test ./internal/adapter/handler/... -run "TestVerifyCreemSignature|TestVerifyCreemSubscription|TestAmountValidation"` → PASS; `go test ./internal/adapter/repo/... -run "TestProcessOneAutoRenewal|TestSubscription_ActivateSubscription_Idempotent|TestResetDailyQuota_Idempotent"` → PASS; `go test ./internal/adapter/middleware/... -run "TestAuthHelper"` → PASS; `go build ./...` → OK.

## 2026-02-25: 测试DB迁移PG + new-api增量融合
Part1: testutil_test.go 重写，移除 glebarez/sqlite，改用 TEST_POSTGRES_DSN；SetupTestDB 创建独立 test_repo_<nano> 数据库，cleanup 时 DROP；quotaDeductSafe 移除 SQLite 分支直接用 GREATEST。
Part2: (2a) stream_scanner.go TrimSuffix("\r")→TrimSpace+空串跳过；(2b) processHeaderOverride 跳过 Accept-Encoding；(2c) GeminiUsageMetadata 加 ToolUsePromptTokenCount，提取 buildUsageFromGeminiMetadata 消除两处重复；(2d) MiniMax 添加 MiniMax-Text-01/MiniMax-01/minimax-text-01；(2e) Gemini 添加 gemini-2.0-flash-lite/2.5-flash-preview-04-17/2.5-pro-preview-05-06/2.0-flash-thinking-exp-01-21。
Verification: `go build ./...` → OK (0 errors). PostgreSQL 集成测试待 TEST_POSTGRES_DSN 注入后验证。

## 2026-03-17: lurus-api 瘦身 — 删除 v1 auth + 前端清理 + P0 幂等修复

**Phase 1-3 (Go backend)**: 删除 30 个文件（checkin/invitation/OAuth/2FA/Passkey/SMS/admin_config），精简 User entity（移除 Password/OAuth IDs/aff 字段），清理 router 路由 ~80 条。认证统一委托 Zitadel OIDC。
**Phase 4 (Frontend)**: 删除 16 个 React 文件（LoginForm/RegisterForm/2FA/Passkey/OAuth/checkin/affiliate），修改 12 个文件移除 v1 auth 调用。净删 ~6,438 行。
**P0 fix**: `InternalTopupBalance` 加 order_id 幂等检查（查 LOG_DB 已有记录），防止 platform 支付重试导致重复充值。

Verification: `go test ./... → 20/20 PASS`; `cd web && bun run build → OK`; `CGO_ENABLED=0 GOOS=linux go build ./... → OK`
Commit: `24477bcd9` pushed to `origin/main`。
Remaining: AuthSettingPage.jsx 仍有 Passkey 管理员配置（死设置，不影响功能）；P1 async wallet bridge 无重试机制。

## 2026-03-18: Zitadel OIDC 登录修复 (3 项)

1. **Hairpin NAT**: Pod 内 `auth.lurus.cn` 解析到公网 IP 43.226.46.164，回连被拒。修复: deployment 加 `hostAliases` 指向 Traefik ClusterIP `10.43.175.138` + NetworkPolicy 增 kube-system 443/8443 出站规则。
2. **Email fallback**: `CreateUserFromZitadelClaims` 增加 email 回退匹配（跨租户），老用户首次 OIDC 登录自动建 mapping。
3. **租户数据迁移**: root/marvin 的 `tenant_id` 从 `default` 迁移至 `356204220778610952`；tokens/channels/logs/redemptions 同步迁移；重复用户 (id=6,7) 已软删除。

Verification: `kubectl exec ... wget auth.lurus.cn/.well-known/openid-configuration → OK`; Pod OIDC token exchange 畅通。
Commits: `6600e6c52` (ZitadelRedirect), `bd6fe89dc` (email fallback), deploy `5bbb940` (NetworkPolicy).

## 2026-05-05 · 12 个月战略规划落地为 BMAD artifacts

将"从多租户 LLM 网关 → LLM 治理与成本平台"的战略转化为可执行 epic：
- **Phase 1 (Q2)**: E7 可靠性硬基建 / E8 newapi fork 解耦 / E9 modality 瘦身
- **Phase 2 (Q3)**: E10 5-min TTFT / E11 v1 web 退役 / E12 套餐计费
- **Phase 3 (Q4)**: E13 ClickHouse Insights / E14 PII 审计 / E15 智能路由
- **Phase 4 (2027-Q1+)**: 外部 SaaS 准备

战略决策 D1-D4 已 accepted（停止 fork / 内部优先 / 砍 Tier-3 modality / 自建 Insights）。
新增 north star metrics（产品视角替代纯基础设施视角）。
Active sprint 2026-Q2-S1: 并行启动 Story 7-1（provider 熔断器）+ Story 8-1（fork 审计）。

Artifacts: `_bmad-output/planning-artifacts/{epics.md, sprint-status.yaml, story-7-1-*, story-8-1-*}`

## 2026-05-05 · Story 8-1 fork 审计 — 颠覆性发现

按"自己决策"原则先做 8-1（廉价高杠杆调研）。审计揭示 lurus.yaml 描述与代码事实严重脱节：

- newapi / newhub **不是派生关系**，是 `songquanpeng/one-api` 的两个独立 fork
- newhub 无 upstream remote、零 newapi 引用、Lurus 原创仅 2,183 LOC（governance/hub/openrouter_pool/openrouter_sync/nats）
- 双线演化：90 天 newapi 211 commits、newhub 117 commits，各搞各的 quota / 计费 / 事件机制
- newapi prod (87k LOC) / newhub stage (89k LOC)，89% 同源代码重复

→ Epic 8 语义从 "fork decoupling" 变更为 **"consolidation"**。推荐 Option A: retire newapi，整合到 newhub。

Deliverables:
- `doc/decisions/2026-05-05-newapi-newhub-fork-audit.md`（含 Q1-Q4 决策项）
- `scripts/audit/fork-ownership.sh`（quarterly 重跑）
- Story 8-1 → review（待 Anita 选 Option A/B/C）
- Story 8-2/8-3/8-4/8-5 已重写为 Option A 执行路径，blocked 状态


## 2026-05-07 · Story 7-1 完成 — audit 改写 + 失败分类外科修复

按"自己决策"启动 Story 7-1。Recon 阶段发现既有实现：
- `internal/pkg/resilience/circuitbreaker.go` 已有完整 Registry + 状态机
- `internal/adapter/handler/relay.go:242,304,313` 已接入 (Allow / RecordSuccess / RecordFailure)
- `internal/pkg/metrics` 已有 CircuitBreakerState/Trips/Rejections 三指标
- 测试套件 `circuitbreaker_test.go` 7 个 case 全过

唯一真 gap: RecordFailure 不区分 4xx/5xx —— 用户错误也会跳闸，影响其他租户。

Surgical fix:
- 新增 `types.IsUpstreamFailure(*NewAPIError) bool` (37 行) + 17 个表驱动测试
- relay.go:313 加 if-wrap (4 行) —— 唯一调用点

总变更 ~95 LOC（原 Story 估 400 LOC）。Karpathy ②: 简单优先生效。

Verification:
- go build ./internal/... ✅
- go test ./internal/pkg/types/... ✅ 17/17
- go test ./internal/pkg/resilience/... ✅ 无回归
- handler 包 TestListTokensV2_Pagination panic 预存在（git stash 验证 main 同失败）

Status: 7-1 → review (审计 + 修复完成；STAGE 故障注入演练后 → done)

## 2026-05-07 · "全做" 三 Story 收尾

按用户"全做"指令，三 story 并行：

**8-2 newapi port list** (audit only):
- 90 天 newapi 211 commits 过滤 → Lurus 原创 ~12 条
- 4 项需移植 (P0×2 / P1×2): cost-spike protection, auth hardening, llm.image.generated event, usage milestone
- ~2 工作日，待 Anita confirm Option A 后启动 PR
- Skip: admin api (newhub 已等价)、session config (已等价)、deploy/ops、上游 sync

**7-4 quota real backpressure** (code change ~65 LOC):
- 审计揭示 backpressure 已存在: enforceTenantQuota 返回 402 + ErrorCodeTenantQuotaExceeded
- 真 gap = Retry-After header 仅对 OpenRouter 池冷却返回
- 修复: tenant_quota.go 设 RetryAfterUnix=nextMonthStartUnix; relay.go 通用 Retry-After 段
- 运维侧建议: TENANT_QUOTA_ENFORCEMENT_ENABLED 默认翻 true (manifest 决策)
- 测试: NextMonthStartUnix 跨年 / 5 个 tenant_quota 测试全过

**7-2 PG HA ADR** (decision doc, no code):
- 现状: R6 单节点 docker-compose pg:15, 无备份无副本
- 反对 Patroni: 单 host = 假 HA, 4-6 周 ops 投资在 1 tenant 阶段过度
- 推荐分阶段:
  - Phase 1.0 (1 天) wal-g backups → MinIO + restore runbook
  - Phase 1.1 (2 天) streaming replica (待第 2 host)
  - Phase 2.x (3 天) managed PG (待 5+ 付费租户)
- 决策文档: lurus/doc/decisions/2026-05-07-newhub-pg-ha.md (含 Q1-Q4)

Verification:
- go build ./internal/... ✅
- 3 story 测试全过 (IsUpstreamFailure 17/17, NextMonth 3/3, EnforceTenantQuota 5/5)
- 既有 resilience / types / app 包无回归

Sprint 进展: 5 个 review 状态 story (7-1, 7-4, 7-2 ADR, 8-1, 8-2). 下一推荐 7-2.1 (备份, 1 天) — 移除最大数据风险。

## 2026-05-07 (cont.) · Story 7-2.1 wal-g 备份链路完成

按 7-2 ADR Phase 1.0 落地，~1 天工作量真实写到代码：

**6 个新增/修改文件**:
- `deploy/single-node/Dockerfile.postgres-walg` — postgres:15 + wal-g v3.0.0 binary
- `deploy/single-node/archive-wal.sh` — wrapper 处理 WALG_S3_PREFIX 空值（fresh boot 不卡住）
- `deploy/single-node/docker-compose.yml` — PG 切自定义镜像，加 archive_mode=on + archive_command + archive_timeout=60s（RPO ≤1min）
- `deploy/single-node/.env.example` — 7 个新 WALG_* 变量含运维注释
- `doc/runbook/pg-restore.md` — §A 全量 / §B PITR / §C 单表 三条路径 + 故障排查矩阵
- `scripts/pg-restore-drill.sh` — 月度自动化 drill（throwaway PG → fetch LATEST → 表存在断言 → PASS/FAIL）

**Verification**:
- `docker compose config` 渲染 ✅（修了 .env 内联注释被当 value 的 bug）
- `bash -n scripts/pg-restore-drill.sh` ✅
- archive_command 渲染干净（弃用 $$ escape 改用 wrapper script）

**SLO**: RPO ≤5min / RTO ≤30min / 月度 drill 100% pass
**部署**: 操作手册见 Story doc，~10 步 R6 操作

**Sprint 进展**: 6 个 review story (7-1, 7-2 ADR, 7-2.1, 7-4, 8-1, 8-2)。Phase 1 加固期实质性推进。

## 2026-05-07 (cont.) · v2 polish + Story 8-2.1 cost-spike port

**v2 frontend polish**:
- `prettier --write` 把 19 个 hi-fi 文件格式化干净
- HFShell: useLocation 自动派生 active state（不再每页硬编码 active 属性）
- HFShell: topbar 加亮/暗主题切换按钮（持久化到 localStorage `lurus-hf-theme`）
- vite build ✅

**Story 8-2.1 cost-spike protection port** (newapi → newhub):
- Per-user 5-min 滑动窗口 / Redis ZSET / 阈值默认 50000 quota units
- 触发后自动 DisableUserById + HTTP 429
- Files: 6 文件改/新增, ~210 LOC
- 接入点: relay-router.go `relayV1Router.Use(CostSpikeLimit)` after TokenAuth
- 记录点: PostConsumeQuota 异步 hook 内调用 RecordCostSpikeWindow
- 防御场景: agent 死循环、token 流量爆炸 → 单用户 5min 内烧掉巨额钱包
- Tests: 8/8 (parseCostSpikeMember 边界用例) ✅
- go build ✅
- DoD 缺最后一项: STAGE 注入测试


## 2026-05-07 (cont.) · 8-2.2 + 8-2.3 ports done

**8-2.2 auth hardening**:
- 审计揭示原 8-2 audit 过度——newapi commit `da3cb48f` 实际只含 4 个 sub-fix，其中 3 个 newhub 已就位（pprof bind 127.0.0.1 / authHelper comma-ok / DeleteUser 不同 shape）
- 真 gap：VideoProxy 没有 ownership 检查 = 跨租户视频泄露漏洞
- Fix: video_proxy.go 加 `task.UserId != requesterID && role < admin → 403` + 结构化日志
- ~22 LOC，go build pass

**8-2.3 NATS llm.image.generated**:
- Newhub NATS 基础设施已具备（`internal/pkg/nats/publisher.go`），加 typed event helpers
- Files: events.go (130 LOC) + events_test.go (41 LOC) + image_handler.go (+15)
- 公认 envelope 格式跨 newapi/newhub/2l-svc-platform 三方 compat (event_id UUID dedup)
- truncateRune rune-safe 80 字符截断（CJK 安全）
- 4 个 fail-open 路径（nil publisher / userID≤0 / marshal err / publish err）
- image_url 暂留空（newapi fc49e72d 的 response-writer 包裹延后到 8-2.3.1）
- 10/10 unit tests pass


## 2026-05-07 (cont.) · 8-2.4 NATS llm.usage.milestone port

**决策**: 不与现有 `quota.threshold` 合并，加新事件类型
- threshold = 月度% (50/80/95/100% of MaxQuota), 计费单位, 告警语义
- milestone = 终生绝对值 (1k/10k/100k/1M tokens), token 单位, 成就语义
- 跨 fork wire compat：newapi 已分离两类，保持一致

**Files**:
- usage_milestone.go (117 LOC) — tier ladder + INCRBY + SETNX claim
- usage_milestone_test.go (80 LOC, 9 tests) — 纯函数 crossedMilestones + no-op safety
- compatible_handler.go +7 — postConsumeQuota 末尾单点 hook（mirror newapi 的 hook 位置）

**No-op fallbacks**: invalid userID, totalTokens≤0, Redis disabled, INCRBY error
**TTL**: 1 年 dedup 键 — 内存有界，覆盖任意保留窗口
9/9 tests pass · go build pass · DoD 缺 STAGE 事件流验证


## 2026-05-07 (cont.) · 8-2.3.1 image URL extraction follow-up

**Goal**: 让 8-2.3 的 `image_url` 不再永远空，inbox 可渲染缩略图

**Approach**: tee-style `BufferedResponseWriter` 包裹 `c.Writer` 一次（在 ImageHelper 入口）
- 64 KiB 硬上限 buffer，OOM 不可能（用户字节透传不变）
- 3-tier shape walker: OpenAI/DALL-E `{"data":[{"url":...}]}` → Replicate `{"output":...}` → 通用 `*url*` depth-1/2 fallback
- 适配 OpenAI/DALL-E/Replicate/Ali/Jimeng/Gemini/Zhipu (6 个 provider 均归一化到 OpenAI shape)

**Files**:
- response_capture.go (217 LOC) — 写器 + 解析器
- response_capture_test.go (287 LOC, 21 tests) — byte-identical pass-through, cap 边界, 各种 shape, garbage/HTML safety
- image_handler.go (+13/-7) — 入口包裹 + 出口 ExtractImageURL

**Invariants verified**: 字节透传零修改，64 KiB cap 即使 200MB 也安全，HTML/garbage/truncated 不 panic
21/21 tests pass · go build pass · DoD 缺 STAGE 真实事件验证


## 2026-05-07 (cont.) · 9-1 Tier 3 modality usage audit

**Goal**: D3 决策已 accepted（砍 MJ/Suno/Realtime/Music/Video），量化代码足迹便于排期

**Findings**:
- 后端 7,082 LOC + ~46 文件可删
  - MJ: 1,781 LOC · Suno: 314 · Music: 228 · Realtime: 134+43branches · Video: 4,625
- 8 个 ChannelType 待禁用 (2/5/36/50/51/52/54/55)
- 2 张 DB 表 (`midjourneys` / `tasks`) drop
- 关键安全发现：`provider/task/{ali,doubao,gemini,jimeng,vertex}/` 是 video task 变体；chat 实现在 `provider/{name}/`，删除任务路径不影响 chat
- Realtime 在 `provider/openai/relay-openai.go` 有 43 行分支，必须同步删，避免死代码

**排期建议**: 9-1 (this) → 9-2 announce (1d, 需 PROD usage 数据 <5% 收入 / <10 租户/模态) → 90d → 9-3 删码 (2d)

**Live data 待操作员**: 3 条 SQL 写在 story 文档里，跑 PROD `consume_logs` 即可
DoD 缺 PROD 用量数据；纯码审计完成



## 2026-05-07 (cont.) · 7-5 chaos drill + SLO dashboard

**Goal**: 让 Phase 1 的 12 个 review story 可被运维真实验证（不只是声明已完成）

**Files**:
- `scripts/stage-smoke.sh` — 11 检查项跑遍所有 review story 的 DoD，env 缺失则 skip
- `scripts/chaos-drill.sh` — 5xx 注入 / slow-loris / cost spike 三场景，PROD URL 自动拒绝
- `deploy/grafana/newhub-slo.json` — 12 panel 仪表板：avail / p95 / 网关 overhead / breaker / 计费 health
- `deploy/grafana/newhub-alerts.yaml` — 11 alert / 5 group：page / ticket / info 三级
- `story-7-5-chaos-drill-slo-dashboard.md` — operator runbook + SLO 定义

**SLO 目标**：avail 99.5% / p95 < 3s / 网关 overhead < 50ms（北极星指标）
breaker stuck open > 30m → ticket / flapping > 5/10m → info
platform breaker open > 5m → page

**部署侧**: 7 commits f5cfa126..20b23add 推到 origin/main，CI 手动触发了 docker-image-main.yml (run 25491707513) — 历史模式显示该 workflow 不自动跑 push 触发。
bash -n syntax check 通过 · JSON valid · DoD 缺第一次 STAGE 真跑

## 2026-05-08 · zita-sdk-go Layer C kickoff (ADR-0011)

**Goal**: 把 newhub 从直连 Zitadel 切到统一 zita SDK；platform Layer A/B 当日完成 (4 模块迁完 + sdk MVP 推 origin/main)，newhub Layer C 起第一刀

**Files (4 commits, d9b5f8d1..d407fc53)**:
- `internal/pkg/common/zita_client.go` — `ZitaClient` global + `InitZitaClient` (env 缺失则 nil，不阻断 boot)
- `internal/adapter/handler/zita_login.go` — `GET /api/v2/auth/zita-login`，open-redirect guard 限 *.lurus.cn
- `internal/adapter/handler/me_zita.go` — `GET /api/v2/me/zita` 诊断端点（SDK middleware 验签 + AccountID 回显）
- `web/src/components/auth/ZitadelRedirect.jsx` + `helpers/utils.jsx` — "登录"按钮 + 401 fallback 切到 zita-login
- `Dockerfile` + `.github/workflows/docker-image-main.yml` — checkout zita-sdk-go full sha (短 sha 被 actions/checkout 当 branch)
- `deploy/k8s/r6-stage/deployment.yaml` — `IDENTITY_PUBLIC_URL=https://identity.lurus.cn` + 修 image repo (`lurus-api` → `lurus-newhub`，老 tag 还卡在 4-23 stale image)

**E2E 验证**:
- curl: 注册 anitazita1778227195 → cookie .lurus.cn → `/api/v2/me/zita` 返 `{"account_id":7}` ✅
- 浏览器: 同样链路 + cookie store + 跨子域行为符合 SDK 假设 ✅

**Platform 侧待修**（已转告 platform session）:
- `/login?return_to=...` return_to 参数被忽略，登录后跳自家 `/hub` 而非 newhub
- `/api/v1/auth/login-or-register` 还是 TODO，前端按钮 UX 撒谎

**留给明天**: oauth.go 951 LOC + zitadel_auth.go 300 LOC + 4 test 文件清理；user 表加 `lurus_account_id` 列 migration；老 v2 路由删除


## 2026-05-18 · Hardening swarm (dev-only, single session)

**Scope**: Lane A (quality floor) + Lane D (backend tests + scoped CI gate) + Lane B partial (TenantSwitcher real-data only).
Lane C (Epic 7 STAGE validation) explicitly deferred — needs operator SSH + multi-min observation windows.

**Lane A**: `web/vitest.config.js` + `playwright.config.ts` + `tests/e2e/story-11-2-token-persistence.spec.ts` + `web-ci.yml` (PR-blocking: lint/eslint/vitest/build; e2e nightly via workflow_dispatch). `bun run test` 2/2; `bun run test:e2e` 1 skipped (correct, no E2E_BRIDGE_TOKEN).

**Lane D**: 9 new handler tests in `audit_governance_test.go` (4 audit + 5 governance) — all pass. Repaired pre-existing `TestListTokensV2_Pagination` (handler shape `items`/`p`/`size` drift from commit `23a87f72`). Added `go-ci.yml` with `go build` + `go vet` + scoped test gate. **Did not** add broad `go test -short ./...` — pre-existing OAuth/JWKS test debt (TestValidateIDToken_*) would block every PR; documented in `test-debt-findings.md`.

**Lane B partial**: `HFShell.jsx` `useRealTenants` hook calls `/api/v2/admin/tenants` for root, single-tenant fallback otherwise. TenantSwitcher DEMO_TENANTS removed. 5 component tests added. Dashboard/Settings/Log mocks NOT cleared — multi-hour effort, deferred.

**Acceptance**: `_bmad-output/planning-artifacts/hardening-swarm-2026-05-18-acceptance.md` — explicit marker-vs-measurement separation per §4.1 ⑥; lists what this swarm does NOT claim (Epic 7 review→done; broad CI gate; UI walkthrough).


## 2026-05-18 (cont.) · Lane B pass 2 — Dashboard realtime KPI wire-up

`qps` / `ttft p50` / `error rate` tiles were `—` placeholders with "coming soon" copy. No `/api/v2/{slug}/metrics/usage` exists, so derived client-side from last 5min of `/logs`.

**Files**: `web/src/pages/v2/Dashboard/kpis.js` (pure helpers) + `kpis.test.js` (17 cases) + `index.jsx` widened fetch to `size=200&start_time=now-300`.
**Honesty (§4.1 ①)**: empty window renders `—` + "no traffic in last 5 min", not 0% / 0 / perfect zero.
**Test count**: vitest 24/24, vite build green.


## 2026-05-18 (cont.) · Lane B pass 3 — WIPBanner + dead-mock removal

**WIPBanner** shared component (`web/src/components/hifi/WIPBanner.jsx`, role=status, reason+todo props).

**Injected**: Playground / Models / Chat / Flows top of body — explicit "not yet wired" marker with backend dependency listed.
**Log page**: deleted `CLUSTERS` (4 fake error clusters) and `LIVE_ROWS` (8 hardcoded log lines + fake cursor) arrays; tabs now render WIPBanner + "endpoint not implemented" empty state.

**§4.1 ⑥**: Banner = marker; mock-data deletion = measurement hygiene. Fake "live tail at 14:02:11.481 acme contoso initech" can no longer leak into stakeholder demos as if it were real telemetry.

**Tests**: vitest 28/28 (added 4 WIPBanner specs); vite build green.


## 2026-05-18 (cont.) · Reseller-MVP swarm (option β)

3 parallel sonnet agents调研 (Portkey/Helicone/LiteLLM/OpenRouter/Vercel AI/Cloudflare; Langfuse/LangSmith/Helicone analytics/PromptLayer/Phoenix/Datadog LLM; OpenAI/Anthropic/OpenRouter/Stripe/Vercel/Cloudflare key UX) — 17 个高置信改进点 + 4 个跨证伪 anti-recommendation. 见 `competitive-intel-2026-05-18.md`.

**Swarm 4 lanes**:
- **L1 ADR-only**: tenant credit pool + Provisioning API — 加 2 表 + 2 列；per-request 条件 UPDATE 防 race；pool→token 双闸 (402→429)；OpenRouter Management Key 模式. 5 个开放问题等 Anita.
- **L2 ADR-only**: budget threshold alerts — NATS→platform notifier 派发；Redis dedup TTL；扩 newhub-alerts.yaml. **🔴 重要发现**: 现有 11 条 alert YAML 用 `lurus_hub_*`，但 metrics.go namespace 是 `lurus_gateway_*` — 自从某次 subsystem 改名后所有 alert silently 死了。Story 7-5 DoD 的 alert 部分应重审.
- **L3 实施**: Settings/Security 接 `/api/v2/client/sessions` (single-session 真数据) + WIPBanner 注明多设备未实现；Notifications/Team mock 删 + banner；Token 行加 `created_time`/`accessed_time` 显式标签.
- **L4 实施**: Dashboard ttft tile 升级 P50/P95/P99 三档 + cost-by-model 真接（替换 bubble chart）+ OnboardingCurlBlock (零 token 时显)；kpis.js +4 个派生函数 + 11 个 vitest case.

**测试**: vitest 39/39 (smoke 2 + TenantSwitcher 5 + kpis 28 + WIPBanner 4), vite build green.
**Acceptance**: `_bmad-output/planning-artifacts/reseller-mvp-2026-05-18-acceptance.md`.



## 2026-05-18 (cont.) · Fix dead alerts (自行决策路径)

L2 ADR 调研发现 11 条 alert + 15 个 dashboard panel 全用错指标前缀 `lurus_hub_*`（实际 emit 是 `lurus_gateway_*` / `lurus_billing_*`）—— silently dead 不知多久。

**修复**: sed 二步替换 (billing 先, gateway 后) on `deploy/grafana/newhub-alerts.yaml` + `newhub-slo.json`. YAML 顶部加防回归注释指向 `internal/pkg/metrics/{metrics,billing}.go` 的 namespace/subsystem 真源。`grep -c lurus_hub_` = 0; YAML/JSON 解析通过.

**操作员影响**: 下次 ArgoCD sync ConfigMap 后 11 条 alert 开始针对真指标评估。之前隐形的合规告警会开始触发。这是预期行为，不是回归。

**Story 7-5 状态**: 仍 review 但 alert pack DoD 真正修了；首次 STAGE drill 还等 operator.


## 2026-05-18 (cont.) · Lane B 二次扫荡 — Billing + Pricing

发现 Billing 整页 + Pricing 整页仍是设计 mock（INVOICES $8,420.40 / ROWS gpt-4o $2.5 等），两页各加 WIPBanner 指向 Epic 12 SKU 决策。**不删 mock 数据**因这些是 UI 占位 layout（不像 Log Cluster 那种伪 telemetry）；banner 提供"这页不真"的明确标记。

测试 39/39，build 绿，go handler 测试 10/10。


## 2026-05-18 (evening) · Q3 Phase 2 Swarm — Credit Pool + Provisioning API

5 lanes / 2 waves，6 commits 全部 landed：
- `f6a492ad` schema draft（migration 012 + entity + repo stub）
- `395d9065` Lane δ：NATS pool_threshold（schema+Redis 双 dedup）+ 3 Prometheus 指标 + 3 Grafana panels + 2 alerts
- `e9f26560` Lane α：5 admin + 2 provisioning handlers + pool-gate on 5 relay groups + PostConsumeQuota Phase 2.5
- `a8f3de37` Lane ε：ADR §4.2 `Bearer→X-API-Key` 修正（Switch 集成阻断）+ contract spec + runbook + 5 retro + epic-7 roll-up
- `7ca1aa18` Lane γ：CreditPoolDrawer（11 vitest cases）+ Tenants pool button + Token "by user #N"
- `8e29a97e` Lane β：entity + **repo atomic race invariant**（20 goroutines × 10 vs pool 50 → 5 ok / 0 negative）+ middleware contract

3 agent timed out 中段，剩余在 main 手工补完。Build/vet/tests 全绿。STAGE drill 与 G1/G2/G3 仍 pending — 不冒充 "done"。

## 2026-06-03 · CI: pg-integration disposable-PG gate (真 PG / 钱路 e2e)

承接 race 闸门 re-block (#11)，闭合审计 doc 里 "真 PG / 钱路 e2e" 这条 deferred followup。

- **病灶**: `internal/adapter/repo` 的 ~11 个集成测试经 `SetupTestDB` 取 PG，无 `TEST_POSTGRES_DSN` 即 `t.Skip` → CI 里从不跑、报空绿（§4.1③ hollow skip）。覆盖钱路不变量（quota 增减原子性、token validate/expire/exhaust、tenant-whitelist auth、daily-reset 幂等）。
- **修复 (PR #12 `887724bb`, 2 commits)**: go-ci.yml 加 `pg-integration` job — 起 disposable `postgres:16` service、设 DSN、跑 `./internal/adapter/repo/...`。反-hollow 卫士: `TestIntegrationPGHarness_RealPostgres` 哨兵用 `SELECT version()` 证活 PG，grep step 在哨兵被 skip/缺席时 fail job。新增 `TestIntegrationUserQuota_ConcurrentDebit_NoLostUpdate`（仅真 PG 有意义: 50 并发 `DecreaseUserQuota` → quota 恰好落 0）。BLOCKING，非 report-only。
- **证据（本地实跑，Docker v28.2.2 可用 → 不像 -race 只能靠 CI）**: `docker run postgres:16-alpine` + DSN → **719 PASS / 0 FAIL / 7 SKIP**（7 = `-short` stress + SQLite-only），0 个 "DSN not set" skip。CI 复现绿（PostgreSQL 16.14，哨兵 + 并发 e2e 均 PASS）。merge 后 main CI 全 8 job 绿。
- **范围**: off `origin/main`，纯增量（CI job + 2 测试文件），**零 money-path 源改动**；money-path linkage WIP 仍只在 feature 分支、不在 main，无冲突。
- **未做（followup）**: PG-only 路径跑 `-race`（这些路径从未经探测器，可能暴露既存 race，单独硬化）。

## 2026-09-07 · METRICS-HONESTY: dead series + phantom dashboards retired

`channel_health`/`channel_consecutive_errors`/`channel_errors_total`（连同
`RecordChannelError`/`SetChannelHealth`/`ResetChannelErrors`）从
`internal/pkg/metrics/metrics.go` 删除 — grep 全仓零非测试调用方，三条自
`metrics.go` 建文件（2026-02-05，即约 7 个月）起从未写入的指标。`deploy/grafana/`（3 个从未被任何 kustomization apply 的
dashboard/alert 文件）整目录删除；`doc/decisions/observability.md` 补
2026-09-07 更新块订正 Jaeger "staging: deployed" 的过期声明（OTLP collector
已停,监控栈=Netdata 自托管）。`billing_debit_amount_cny` 标签从 `tenant_id`
改 `product,op`，补上 `quota.go` PostConsumeQuota 结算分支（此前只有直接
`DebitWalletGRPC` 调用点写这个指标,pre-auth settle 分支动了真钱却零观测）与
HTTP fallback `DebitWallet` 的写入点。`relay_requests_total`/
`relay_errors_total`/`relay_total_duration` 加 `product` 标签,`status` 新增
`client_gone`（`handler.relayOutcome`,调用方断连不再算 "success"）。新增
`internal/pkg/metrics/declared_series_written_test.go` 闸门：每个
`promauto.New*` 变量必须有真实生产写入方，否则 CI 测试失败。
