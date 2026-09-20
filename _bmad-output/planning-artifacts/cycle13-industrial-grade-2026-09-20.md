# 第十三轮 — 工业级生产水平（续）：钱路守恒、API 语言、数据生命周期、租户到达数据、优雅下线（cycle 13）

## 1. Context

第十二轮（PR #188/#189）上线后 owner 说"继续 开workflow"。本轮先用 12 个只读侦察维度（每维度一个取证 agent + 一个对抗复核 agent + 末尾一个 lane 合成 agent，25 agent / 5.6M token / 50 分钟）对 HEAD `b48dd32b` 取证，得到 151 条经复核保留的 finding；进入 lane 的每一条都由我对 HEAD 亲手重读过 file:line（§1.2）。合成 agent 的 10 lane 草案被我改成 11 lane + W：它排除的"SIGTERM 切断在途流不结算"、"日志保留 + 039"、"擦除级联"被我拉回来；它建议的容量 lane 与租户用户管理 lane 因剩余 token 预算（约 14.6M）推到下一轮（§7）。

### 1.1 实测指标（2026-09-20，本机 git-tracked 计数；CI 数字来自 main@40da9dde 的绿跑 35478209863）

| 指标 | 实测 |
|---|---|
| Go 源码 / 测试 | 138,306 / 277,351 行 |
| Web 源码 / 测试 | 79,771 / 66,185 行（第十二轮删掉约 18k 行无路由 legacy 后） |
| Go CI 各 job 墙钟 | `-race -shuffle` 11m43s（最长）· coverage gate 5m · gosec 3m48s · pg-integration 3m · lint 1m13s |
| main 近 3 次 Go CI | #187 红 · #188 红 · #189 绿（每次红都是 `-shuffle` 抓到真实测试隔离缺陷，已修） |
| >800 行文件 | 40（棘轮冻结；本轮不拆） |
| 生产 | 3 副本 0 重启 7h+；镜像 `3fb429b8…`；`logs` 29,607 行 / 20 MB，最新一行停在 09-19 13:58（探活不再写日志，30 小时零真人流量） |
| PG（R6 `database/lurus-pg-0`） | **`max_connections = 100`**，当前占用 38（newhub 13 / zitadel 8 / uat 5 / identity 3 / lucrum 3 / 其它 6）；`archive_mode=off`、`wal_level=replica`（无 PITR） |
| netdata | 仓内 `newhub.conf` 15 条模板，**线上只加载 3 条**（outbox 两条 + platform breaker）；抓取目标仍是 `localhost:30850`（NodePort 随机落在三副本之一） |
| 租户表 | `default`(org 370089515844436016) · `lurus-default`(test-org…) · `probe-invite`(空 org) · `switch`(`SWITCH_ORG_ID_PLACEHOLDER`)；channels 只有 1 条（default） |

### 1.2 本轮触发事实（全部由我对 HEAD 亲手核实过 file:line）

1. **异步任务退款只退四账本之一**：`handler/task_refund.go:45-53` 只 `IncreaseUserQuota` + 两个 used 计数；而提交时 `app.PostConsumeQuota` 动了 users.quota、租户信用池（`quota.go:999-1001`）、tokens.remain_quota（`:1016-1021`）和钱包。每个失败的 MJ/Suno/video 任务永久烧掉客户 key 的 remain_quota 和租户池余额。video 任务按实际 token 重结算（`task_video.go:198`）同样只动 users.quota，且写的是 system 行不是 consume 行。
2. **daily 配额计的是结算差额不是请求成本**：`quota.go:983-984` 用 `quota`（= 实际 − 预扣），紧挨着的 `:989-1002` 池扣款用 `quota + preConsumedQuota` 并在注释里写明"用差额是错的"。
3. **`/v1/realtime` 从不结算预扣**：`PostWssConsumeQuota`（`quota.go:231-333`）只更新 used 计数 + 写日志，没有 `SettleConsume`；`relay.go:494-501` 成功即返回，`releasePreConsumedOnFailure` 不会跑。每个 realtime 会话多扣 `FinalPreConsumedQuota`。
4. **token 写失败时只补偿用户不补偿池**：`quota.go:1012-1046` 的补偿只 `IncreaseUserQuota`，Phase 2.5 已扣的池不退。一次请求在三本账上落成三个不同金额。
5. **账单把没扣过钱的行也算钱**：`v2_billing_invoices.go:174-186` 与 `billing_self.go:53` 对 `type=consume` 无条件 `SUM(quota)`；手工渠道测试写全额 consume 行且代码自述"usage nobody paid for"（`channel-test.go:472-478`），`other.source=channel_test`、`other.settlement=failed` 两个标记零消费者。
6. **402 与四类中转错误用中文回答 API 客户**：`repo/token.go:159/183/209`（令牌额度用尽）经 `auth.go:560` 直接进三种 wire 的 `error.message`；`distributor.go:335` `无效的请求`、`channel_cache.go:317/328`、`relay.go:650`、`price.go:83`、`mjproxy_handler.go:295/469`。唯一语言闸只覆盖 429（`rate_limit_message_lock_test.go:106-110`）。
7. **兑换错误把驱动错误文本发给客户端**：`repo/redemption.go:234` 把任何事务错误包成 `兑换失败，<driver text>`；`v2_redemption.go:93`、`switch_user_topup.go:58` 原样回显；只有 `switch_redeem.go:386-399` 有 sanitizer。且 `该兑换码已被使用`（`redemption.go:165`）不含 switch 分类器要的子串 `已使用`（`2c-gui-switch/internal/redemption/redeem.go:217`）⇒ switch 把"已用"错分成"不存在"。
8. **`logs` 无任何保留路径**：唯一删除是同步无界的 `DELETE /api/log/`（`repo/log.go:809-835` ← `handler/log.go:256`）；生产 manifest `deployment.yaml:116-119` 却写着"经同一 DeleteOldLog 保留老化，体量有界"。`LOG_RETENTION*` 全仓零命中。Log 统计三端点无 `start_time` 时全租户无界聚合（`v2_log_stat.go:129-130`）且不挂 `CriticalRateLimit`（对比 `api-v2-router.go:254/591`）；`logs` 无 `(tenant_id, created_at)` 索引（live `\di` 核过：最近的是 `idx_tenant_user_created (tenant_id,user_id,created_at)`）。runner 把每个文件包在一个事务里（`runner.go:324-330`）⇒ `CREATE INDEX CONCURRENTLY` 结构性不可用；启动探针 5+30×5=155s（`deployment.yaml:270-276`）⇒ 长 migration 会被 SIGKILL。
9. **擦除级联漏掉最私密的四张表**：`lifecycle/privacy_erasure.go` 与 `repo/privacy_erasure.go` 对 chat_sessions/chat_messages/midjourneys/tasks/quota_data 零命中（grep 核过），而擦除请求被标 completed 并写审计"已完成"。`download_logs` 永久存匿名下载者的 IP/UA/Referer（`release_service.go:206-218`），无 user_id 无保留。
10. **CSRF 守卫被任意 Authorization 头绕过**：`browser_origin_guard.go:154-157` 先看凭证头存在与否再看 cookie，而 `authHelper` 先用 cookie（`auth.go:58-96`）；其自身测试把这条绕过钉成 200。`GET /api/v2/auth/zita-logout?return_to=` 无校验重定向（`zita_logout.go:51-58`），两文件外的 `isLurusReturnURL` 只有一个调用者。登录三处（`oauth.go:464-477`、`zita_bootstrap.go:124-131`、`v2_bridge.go:96-104`）不轮换会话 id，`auth.go:104-110` 自述 redistore 保留传入 id。CSV 导出三处首格无公式注入转义。`POST /api/ratio_sync/fetch` 是唯一无 SSRF 快照的 body-URL 出站。
11. **URL 里的租户到不了数据**：`v2_provision.go:351` 首次用户硬编码 `autoCreateBridgedUser(accountID, "default")`，注释自认"pre-existing gap"；`switch_user_info.go` / `user_heartbeat.go` 零处 `TenantGate`（挂起租户的 switch 端用户仍能把兑换码烧进死账户、仍被告知 active），而同族的 `switch_redeem.go:202-210` 会拒；`GET /:slug/projects/spend` 用 `projectTenantCtx`（无角色）返回全租户花费。
12. **第一屏把取数失败渲染成"最近 5 分钟无流量"**：`Dashboard/index.jsx:273-292` 两个请求 `skipErrorHandler` + 空 catch（注释"Intentionally silent"）；全 web 零 ErrorBoundary（grep 核过）而 App.jsx 有 46 处 lazy() + hash 命名 chunk ⇒ 每次滚动发布让打开的控制台白屏；App.jsx 六个 minRole-10 页面（channel/models/pricing/redemption/projects/flows，`:392-404`）挂的是 PrivateRoute；`EditUserModal` 提交的 password/daily_quota/base_group/fallback_group 被 `repo.User.Edit`（`user.go:637-643`）丢弃却提示成功。
13. **SIGTERM 在 +35s 切断在途流并以 exit 1 退出**：`main.go:427-431` Shutdown 超时返回 error → `main.go:85-88` `FatalLog` + `os.Exit(1)`；无 readiness 翻转、无在途计数、无结算。preStop 5 + graceful 30 < tgps 40。`usedata.go:33-35` ctx.Done 时不刷 quota_data。`notify-limit.go:17/44/146` 一个 `sync.Once` 两个 body。
14. **特权 v1 写零审计**：`internal_api.go:498-607` 四个 internal key 写 handler 零 `governance.` 调用（全文件仅 2 处且都在 quota adjust）；`GetChannelKey`（`channel.go:715-722`）只写一条 logs 行，而 `DELETE /api/log/` 又能无审计地删它；`/api/openrouter-sync/{jobs,api-pool,last-status,categories}` 四读挂 AdminAuth 且 `channel_pool.go:131` 无租户谓词（任何 role-10 可读全部租户 OpenRouter 渠道与 12 字符 key 前缀）。
15. **告警盲区**：抓取停止时 15 条告警全部变 UNDEFINED 而非 WARNING；渠道自动停用零告警；`credit_pool_debit_lost` 等四个"alert on any increase"计数器零告警；`schema_migrations_pending` 与 leader task 新鲜度自称"the condition to page on"却无告警；`method` 标签是原始客户端方法（无鉴权无限流路径可无限造序列，`metrics/middleware.go:30-31`）。
16. **CI 经济**：`go-ci.yml:3-19` 只对 Go 文件触发 ⇒ manifest/migration/runbook/web 改动不跑任何 Go 闸门（`1b6ff0de` 的 UAT manifest 改动就没跑 `TestDeploymentPoolBudget`）；`internal/domain/entity` 的三条 PG 并发审计链测试不在任何 DSN job 的包列表；夜跑 e2e 在 bridge token 失效时 34 skipped 仍绿（`web-ci.yml:159` 裸跑）；repo 包 sqlite fixture 改了 `QuotaForNewUser`/`LogConsumeEnabled` 不恢复（583 个调用点）；`totp_flow_test.go:585-586` 任何非 200 都 skip；两个体量棘轮 shrink 只 log 不 fail。

### 1.3 我否决 / 修正的侦察项

- DATALIFE-M3 "三个 logs 索引无读者"——`repo/governance.go:56-57` 对 `request_fingerprint` 有 `!= ''` 谓词 + `COUNT(DISTINCT)`，不是零读者；本轮不删任何索引。
- SECURITY-2/4 要求 api-keys 写路由挂 TOTP step-up——生产 root 尚未注册 TOTP（O1），挂上等于把 internal key 管理锁死；本轮只补审计。
- CAPACITY-2 "把 `MAX_REQUEST_BODY_MB` 降到 8"——多图 vision 请求的 base64 体常超 8 MiB；本轮只加每租户在途上限，不动体积上限。
- 合成器"pod 标签打到 12 条告警序列"（SLI-2）与"进程内 SLI gauge"（SLI-1）——线上只加载了 3/15 条告警（O2 未做），先把可加载的东西补齐；这两项等 O2 后做。
- 合成器排除的 OPS-1（在途流被切）与 DATALIFE-1/OPS-2（日志保留）——都是"每次发布/每个 DPA 审查都会撞上"的，拉回本轮。

## 2. 已拍板的决定（执行时不再争论）

- **钱路守恒**：凡动钱的路径一律经 `app.PostConsumeQuota` / `app.SettleConsume`，四账本（users.quota、tokens.remain_quota、租户池、钱包）同进同退。异步任务退款与 video 重结算改走同一原语；钱包那一腿在 newhub 没有退款 RPC（platform 无契约 ⇒ **O-refund**），退款时对钱包只记指标 `lurus_billing_task_refund_wallet_unreversed_total` + SysLog，不装作退了。
- **daily 配额**按请求实际成本（`quota + preConsumedQuota`）计；退款路径不回滚 daily_used（`IncreaseDailyUsed` 拒负数，历史值不可恢复，写明）。
- **账单只算真扣过的钱**：无 migration，谓词用 `other` 文本标记（`"settlement":"failed"` 与 `"source":"channel_test"`，与 `json.Marshal` 输出逐字一致），账单响应新增 `unbilled_quota` 桶让差额可见；`logs.billable` 列留给下一次 migration。`QuotaPerUnit`/`USDExchangeRate` 写入加正数范围守卫（预消费期冻结仍不做）。
- **API 错误 `error.message` 英文-only 是契约**（与既有 429 锁同规则）；控制台按 `error_code` 映射。兑换错误：repo 只返回类型化哨兵（驱动错误进 SysError 不进用户串），`该兑换码已被使用` → `该兑换码已使用`（switch 现有分类器即刻正确），其余含 `过期`/`禁用`/`不存在` 的子串**不改**（switch 契约）。新增 AST 语言闸覆盖 middleware/repo/relay 三包流向 wire 的字符串字面量，带具名缩减白名单与 sites-seen 下限。
- **日志保留**：leader-only `LeaderTask`，`LOG_RETENTION_DAYS` 默认 0 = 关；非钱行（error/system/manage）下限 30 天；consume/topup 行走 `LOG_RETENTION_MONEY_DAYS` 下限 400 天（保 13 个月账单可出）；每 pass 批次上限；`download_logs` 同任务 `DOWNLOAD_LOG_RETENTION_DAYS` 默认 90；指标 `lurus_gateway_log_retention_deleted_total{table}` + 待删行数 gauge。窗口值由 owner 定（**O-retention**），本轮生产 manifest 不设 = 关。
- **Migration 039 只做一件事**：`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_logs_tenant_created_id ON logs (tenant_id, created_at DESC, id DESC)`，经 runner 新指令 `-- lurus:no-transaction`（该文件不开事务、只允许 CIC 语句、结构闸守）。不删任何索引。ID 在根 ledger 已预留（本轮）。
- **Log 统计**：无 `start_time` 时服务端默认 30 天窗口；`/logs/stat`、`/logs/stat/all`、`/logs/export` 挂 `CriticalRateLimit`（W）。
- **擦除级联**补四张表：chat_messages/chat_sessions 硬删、midjourneys 硬删、tasks 抹 `properties/data/fail_reason`（保 quota 与 id）、quota_data.username 抹成 `ErasedMarker`；加"AutoMigrate 每个含个人数据的模型要么被级联覆盖要么在带理由的豁免表里"的强制函数测试。`download_logs` 只存掩码 IP（`repo.MaskIP`）+ 截断 UA/Referer。
- **CSRF 守卫**：先判 cookie，无 cookie 直接放行；凭证头不再是豁免依据（等价于删掉那个分支）。relay/switch/lutu/internal 都无 hub cookie，不受影响。
- **登录轮换会话 id**：三处登录写身份前把底层 gorilla session 的 `ID` 置空（`sessions.Default(c)` 断言 `interface{ Session() *gsessions.Session }`，redistore `Save` 对空 ID 铸新 id），并 `DEL` 旧 key；**不改 cookie 名**（`__Host-` = 全员强制重登，O-cookie）。
- **logout `return_to`** 走 `isLurusReturnURL`，不合法回 `/login`。**CSV** 首字符 `= + - @ \t \r` 前缀单引号，三处导出共用一个 helper。**ratio_sync** 用 `app.ValidateOutboundURL` + `app.GetHttpClient`。
- **URL 租户到达数据**：provision 用已解析的 `tenant.Id` 建用户；已存在用户属于另一租户 → `403 TENANT_MISMATCH` + 审计事件（不再静默发别租户的 token）。`authenticateSwitchRawToken`、`UserHeartbeat`、`SwitchReconciliation`、`GetCreditPoolForEndUser` cookie 臂、`ProvisionV2` 都过 `repo.TenantGate`（default 豁免与 observe 语义不变）；heartbeat 对挂起租户回 `403 TENANT_DISABLED`（switch 只把 401 当吊销，403 由 switch 侧决定展示 ⇒ 契约注记 + O-heartbeat）。`projects/spend` 改 `projectAdminCtx`。
- **控制台**：取数页面三态（rows / forbidden / error-with-retry），本轮 Dashboard、Admin/Users、Admin/Audit + 共享 `classifyLoad`；`ErrorBoundary` 包 PageLayout 与每个路由 Suspense；`vite:preloadError` 每 session 自动刷新一次；App.jsx 六个 minRole-10 路由改 `AdminRoute`；`EditUserModal` 删 password 字段、`repo.User.Edit` 真持久化 daily_quota/base_group/fallback_group；15 个不解析的 `console.*` 键补齐 + 引用侧→en 闸。
- **审计**：internal key create/update/delete/toggle、channel key reveal、logs purge 记审计事件（新 action 常量，details 永不含 key）；不加 step-up（O1）。OpenRouter 四读改 RootAuth + IDOR 前缀（W）。
- **告警**（仓内 conf + README；安装 = O2）：scrape-stale、channel auto-disable（error/latency/sole-skipped）、四个钱守恒计数器 + `upstream_insufficient_balance`、leader task age gauge（新导出）与 `schema_migrations_pending`、`panics_recovered`；`method` 标签白名单；README 计数闸；UAT 校准的阈值要按副本数折算（写进 conf）。
- **优雅下线**：SIGTERM → 立即置 draining（readiness `/api/health` 回 503 `draining`）→ `Shutdown(budget)` → 超时时记录在途连接数 + 计数器并 **返回 nil**（不再 FatalLog exit 1）；`GRACEFUL_SHUTDOWN_TIMEOUT=75s`、`terminationGracePeriodSeconds=90`、preStop 5s（覆盖 ≤75s 的流；更长流仍会被切，写进 runbook，**O-tgps** 决定是否上调到覆盖 `STREAMING_TIMEOUT`）。quota_data 在 ctx.Done 刷盘；notify-limit 单一 Once + main 接线。
- **连接池**：`max_connections=100` 实测 ⇒ 两份 manifest `SQL_MAX_OPEN_CONNS=12`、`SQL_MAX_IDLE_CONNS=12`（idle==open，`ConnMaxIdleTime` 已管回收）；预算闸改为"池数 = 有 `LOG_SQL_DSN` 则 2 否则 1"、每份 manifest 预算 40、注释记录实测 100 与各服务占用；**O-pool** = 是否把 PG 上调到 200。启动探针 `failureThreshold` 30→120（10 分钟 migration 预算）+ 闸；`RELAY_MAX_CONCURRENT_PER_TENANT=64`。
- **CI**：go-ci `paths` 加 `deploy/**`、`migrations/**`、`scripts/**`、`doc/runbook/**`、`web/src/**` + 反向闸（Go 测试读到的目录必须在 paths 里）；pg-integration 加 `./internal/domain/entity/...` 与 `-shuffle=on`；web-ci e2e 后置断言 `results.json` passed ≥ 30 且 setup 未 skip；所有 job `timeout-minutes`；非 main 分支 `concurrency` 取消旧跑。
- **体量棘轮** shrink → fail（行数降了必须同步降上限）。
- **文件所有权**：并行 lane 互不相交。串行 **W** 独占：`internal/adapter/handler/router/*.go`（含测试）、`cmd/server/main.go`、`deploy/k8s/**`、`web/src/App.jsx`、`web/src/z1_App.test.jsx`、`web/src/components/hifi/HFShell.jsx`、`doc/runbook/INDEX.md`、`.env.example`、`.github/workflows/*.yml`、`internal/pkg/gates/**`、`web/src/file_size_ratchet.test.js`。locale `en.json`/`zh.json`：各 lane 只改自己的子树（L3 `console.errors.*`；L6 `console.log.*`；L7 `console.dashboard.*`、`console.admin.*`、`console.palette.*`、`console.common.{yes,no,error}`、`console.settings.notify_unseeded`）。`internal/pkg/metrics/**` 全归 L10（L1 的 `path=realtime` 标签值懒注册即可）。
- 上线流程同前两轮：PR → CI 全绿 → merge → auto-pin → ArgoCD → 按 lane 的 UAT 探针实证（生产只读）。

## 3. Lane 结构

| Lane | 主题 | 量 | migration |
|---|---|---|---|
| L1 | 钱路守恒：daily 成本、realtime 结算、池补偿、任务退款/重结算四账本 | M | 否 |
| L2 | 账单只算真扣过的钱 + QuotaPerUnit 范围守卫 | S | 否 |
| L3 | API 一律英文 + 兑换哨兵 + 语言闸 + v2 token 错误码 | M | 否 |
| L4 | 特权 v1 写审计（internal key / channel key reveal / logs purge） | S | 否 |
| L5 | 擦除级联补全 + download_logs 最小化 | M | 否 |
| L6 | 日志保留任务 + 统计默认窗口 + runner 无事务指令 + **039** | M | **是** |
| L7 | 控制台诚实：Dashboard/Users/Audit 三态、ErrorBoundary、EditUserModal、palette 键 | M | 否 |
| L8 | 浏览器面安全：CSRF 修正、logout 重定向、会话 id 轮换、CSV、ratio_sync SSRF | M | 否 |
| L9 | URL 租户到达数据：provision、switch 原始 token、heartbeat、credit-pool、projects/spend | M | 否 |
| L10 | 告警补齐 + method 标签白名单 + leader age gauge + 文档 | S | 否 |
| L11 | 优雅下线：draining/在途计数/exit 0、quota_data 刷盘、notify-limit Once | M | 否 |
| W | 接线：路由/守卫/main.go/manifest/workflows/App.jsx/HFShell/棘轮/.env/INDEX | M | — |

---

## L1 — 钱路守恒

**Owned（存在）**：`internal/app/quota.go`、`internal/app/quota_consume_test.go`、`internal/adapter/handler/task_refund.go`、`internal/adapter/handler/task_refund_test.go`、`internal/adapter/handler/task_video.go`、`internal/adapter/repo/tenant_credit_pool.go`（repo，非 handler 同名文件）。**Created**：`internal/app/quota_conservation_c13_test.go`、`internal/adapter/handler/task_video_test.go`、`internal/adapter/handler/task_refund_ledgers_test.go`。

**改动（按优先级）**
1. MONEY-M1：`quota.go:983-984` 改 `dailyConsumed := quota + preConsumedQuota; if userLedger && dailyConsumed > 0 { PostConsumeDailyQuota(..., dailyConsumed) }`；注释写明退款路径不回滚 daily_used 及原因。
2. MONEY-3：`PostWssConsumeQuota` 末尾在 `RecordConsumeLog` 前：`quotaDelta := 0 - relayInfo.FinalPreConsumedQuota`（逐事件 `PreWssConsumeQuota` 已把整个会话扣完），`SettleConsume(ctx, relayInfo, quotaDelta, relayInfo.FinalPreConsumedQuota, "realtime")`，`FlagSettlementOutcome(other, err)`；当 `FinalPreConsumedQuota == 0` 不调用。
3. MONEY-4：Phase 3 失败分支（`!localQuotaConsistent && !advisory`）里把 Phase 2.5 的 `poolDebit` 退回池：新 repo 原语 `CreditPoolAdjustment(tenantID string, amount int, reason string) error`（内部走 `topupPoolInTx` + `PoolDrawReasonAdjustment`，写 draw 行），失败记 `CRITICAL` SysError。
4. MONEY-1：`refundTaskQuota` 签名改为接收 `(ctx, task 的 userId/tokenId/tokenKey/tenantId/channelId, quota, logContent)`（task 行已带 token_id/tenant_id，`repo.InitTask` 打的）；依次 `IncreaseUserQuota`、`IncreaseTokenQuota(tokenId, tokenKey, quota)`、`CreditPoolAdjustment(tenantId, quota, "task_refund:<task_id>")`、两个 used 计数回滚；钱包腿：`metrics` 包不可改 ⇒ 用 `common.SysLog` + 已有 `metrics.BillingAdvisoryMeterLost`？**否**——不得在 L1 改 metrics 包；用 SysError 写明 `wallet leg not reversed (O-refund)`，指标由 L10 在 `metrics/billing.go` 预留名 `lurus_billing_task_refund_wallet_unreversed_total`（L1 通过 `metrics.BillingTaskRefundWalletUnreversed` 调用——**该符号由 L10 创建**；L1 在 L10 落地前先用 SysLog 占位，W 阶段接上）。
5. MONEY-M4：`UpdateVideoSingleTask` 重结算改为构造 task 的 RelayInfo 等价物后 `app.PostConsumeQuota(info, quotaDelta, 0, false)`（正差补扣、负差退款走同一路径），并把原 consume 行的 `Quota` 更新为 `actualQuota`（`repo` 新增 `UpdateConsumeLogQuota(logID, quota)` 或按 task_id 定位），不再写 system 行冒充。保留"只在成功恢复后"的既有门。

**Oracle（→ 必红的变异）**
- `TestPostConsumeQuota_DailyUsedCountsTotalCost`：`FinalPreConsumedQuota=1200`、delta −200 → `daily_used==1000`；delta +500 → 1700。变异：改回 `quota` → 两例红。
- `TestPostWssConsumeQuota_RefundsUnsettledPreConsume`：预扣 1200、一个事件 `eventQuota`，结束后 `users.quota == start − eventQuota`、`tokens.remain_quota` 同。变异：删 SettleConsume 调用 → 红。既有 `TestPostWssConsumeQuota_Arithmetic` 保持绿。
- `TestPostConsumeQuota_TokenWriteFailureCompensatesPool`：用 repo seam 让 `DecreaseTokenQuota` 失败，断言池 `current_balance` 回到起点且有 adjustment draw 行。变异：删补偿 → 红。
- `TestRefundTaskQuota_ReversesTokenQuotaAndPool`：经 `app.PostConsumeQuota` 扣款后 refund，断言 tokens.remain_quota 与池余额回到起点、draw 行存在。变异：删 `IncreaseTokenQuota` 行 → token 断言红而池断言仍绿；删池贷记 → 池断言红。
- `TestUpdateVideoSingleTask_ResettleMovesEveryLedger`：total_tokens 推高 actualQuota，断言 users.quota / tokens.remain_quota / 池三者都按差额动，且该用户当月 consume 行之和 == actualQuota。变异：改回裸 `DecreaseUserQuota` → 红。
- 结构锁：`TestEveryPreConsumeSiteHasASettleOrRelease`（`internal/app` 包内 AST：`PreConsumeQuota` 的每个调用包必须出现 `SettleConsume`/`ReturnPreConsumedQuota`/`releasePreConsumedOnFailure` 之一；sites-seen ≥ 3）。

**Hand-off → W**：无路由改动；`metrics.BillingTaskRefundWalletUnreversed` 接线（见 L10）。

**Docs**：`doc/runbook/settlement-failed.md` 加 realtime 段与任务退款四账本说明。

**UAT**：faultsim 走不到 realtime/task；用 hermetic 测试 + `/metrics` 出现 `lurus_billing_settlement_failed_total{path="realtime"}` 序列（L10 声明）为准；生产只读核 `settlement_failed` 各 path 为 0。

## L2 — 账单只算真扣过的钱

**Owned（存在）**：`internal/adapter/handler/v2_billing_invoices.go`、`internal/adapter/handler/v2_billing_invoices_test.go`、`internal/adapter/handler/billing_self.go`、`internal/adapter/repo/log.go`（只改 `GetUserLogStatByPeriod` 及新增谓词 helper）、`internal/adapter/repo/option.go`（仅 `ValidateOptionValue` 的数值范围）、`internal/adapter/repo/option_json_probe_test.go`。**Created**：`internal/adapter/handler/billing_self_test.go`、`internal/adapter/repo/log_billable_test.go`。

**改动**
- repo 新增 `BillableConsumePredicate(db *gorm.DB) *gorm.DB`：`type = consume AND (other IS NULL OR other = '' OR (other NOT LIKE '%"settlement":"failed"%' AND other NOT LIKE '%"source":"channel_test"%'))`，常量与 `channel-test.go` 的 `channelProbeLogSource`、`settlement_outcome.go` 的键逐字一致（用同一常量，不手抄）。
- `aggregateInvoiceMonths` 与 `GetUserLogStatByPeriod` 用该谓词；账单响应每月新增 `unbilled_quota`（被排除行之和）与 `unbilled_request_count`，`amount_cny` 只按 billable 算。
- `ValidateOptionValue`：`QuotaPerUnit`、`USDExchangeRate` 必须 `> 0` 且 `< 1e9`，否则 400（沿用第十二轮的"写前校验、行不写"）。

**Oracle**
- `TestInvoiceMonths_ExcludesUnbilledRows`：同月三行（干净 / `source=channel_test` / `settlement=failed`），`quota_sum` 只含干净行，另两行进 `unbilled_quota`。变异：删谓词 → 红。`TestGetSelfUsage_ExcludesUnbilledRows` 同形。
- `TestProbeChannel_RowIsUnbilled`：`probeChannel` 写的行被谓词排除（走真实 `RecordConsumeLog` 落 sqlite 再查）。变异：改标记键名 → 红（证明常量共用）。
- `TestUpdateOption_RejectsNonPositiveQuotaPerUnit`：`0`/`-1`/`abc` → 400 且 `common.QuotaPerUnit` 不变。变异：删守卫 → 红。

**Hand-off → W**：无。**Docs**：`doc/runbook/settlement-failed.md` 加"账单排除规则"段。**UAT**：root 会话 `GET /api/v2/lurus/billing/invoices` 响应含 `unbilled_quota` 字段；手工 `GET /api/channel/test/:id` 一次后 `unbilled_request_count` +1 而 `amount_cny` 不变。

## L3 — API 一律英文 + 兑换哨兵 + 语言闸

**Owned（存在）**：`internal/adapter/repo/token.go`、`internal/adapter/repo/l3_token_exhausted_sentinel_test.go`、`internal/adapter/middleware/distributor.go`、`internal/adapter/repo/channel_cache.go`、`internal/adapter/handler/relay.go`（只改 `:650` 一处字符串）、`internal/app/relay/helper/price.go`（只改 `:83`）、`internal/app/relay/mjproxy_handler.go`（`:295/:469`）、`internal/adapter/repo/redemption.go`、`internal/adapter/handler/switch_redeem.go`、`internal/adapter/handler/switch_redeem_test.go`、`internal/adapter/handler/v2_redemption.go`、`internal/adapter/handler/v2_redemption_test.go`、`internal/adapter/handler/switch_user_topup.go`、`internal/adapter/handler/switch_user_topup_test.go`、`internal/adapter/handler/v2_token.go`、`internal/adapter/handler/v2_token_test.go`、`internal/app/token_service.go`、`web/src/helpers/errorMessages.js`、`web/src/helpers/errorMessages.test.js`（若存在）、locale `console.errors.*`。**Created**：`internal/adapter/middleware/wire_message_language_gate_test.go`、`internal/adapter/middleware/wire_message_402_test.go`、`internal/adapter/repo/redemption_test.go`。

**改动**
- `token.go:183/209` 两段改英文（保留数字与补救指引；`ErrorCodeTokenQuotaExhausted` 与 `token_remain_quota_units` 元数据不变）；`:159` 哨兵文本改 `token unavailable`；更新 sentinel 测试常量。
- `distributor.go:335/355/363`、`channel_cache.go:317/328`、`relay.go:650`、`price.go:83`、`mjproxy_handler.go:295/469` 改英文；状态码、error code、字段不变。
- 兑换：`repo/redemption.go` 导出类型化哨兵 `ErrRedemptionInvalid / ErrRedemptionUsed / ErrRedemptionExpired / ErrRedemptionWrongTenant / …`，`:234` 不再把驱动错误包进用户串（`SysError` 记原文，返回 `ErrRedemptionFailed`）；`ErrRedemptionUsed` 文本 `该兑换码已使用`；`sanitizeRedeemError` 及 `switchRedeemKnownErrors` 迁到 repo 旁供三处共用；三个 handler 映射 哨兵 → (status, `error_code`, message)，`v2_redemption.go` 与 `switch_user_topup.go` 回 `error_code`（`REDEMPTION_INVALID / REDEMPTION_USED / REDEMPTION_EXPIRED / REDEMPTION_TENANT_MISMATCH / REDEMPTION_FAILED`），message 文本对 switch 保持含分类子串。
- ERRCODES-6：`v2_token.go` 校验失败带 `error_code`（`TOKEN_NAME_INVALID / TOKEN_MODEL_LIMIT_INVALID / …`），`errorMessages.js` 加映射 + `console.errors.*` en/zh 键。
- 语言闸：`wire_message_language_gate_test.go` 用 go/ast 扫 `internal/adapter/middleware`、`internal/adapter/repo`、`internal/app/relay`（含子包）里流向 `abortWithOpenAiMessage` / `types.NewError*` / `MidjourneyErrorWrapper` / `errors.New`+`fmt.Errorf` 且被上述函数包裹的字符串字面量，任何 rune ≥ 0x80 即失败；具名白名单初始为空；`sitesSeen >= 20` 否则 fail-fast。`wire_message_402_test.go`：三种 wire × {额度用尽, 已禁用} 六格断言 `error.message` 全 ASCII 且 code 为 `token_quota_exhausted`。

**Oracle**：闸自身变异——在任一覆盖点放回一个中文字面量 → 红；`sitesSeen` 归零 → 红。402 六格：恢复中文后缀 → 6/6 红。兑换：`TestRedeem_DriverErrorNeverReachesCaller`（制造唯一约束冲突）三 handler 的 message 都是固定回退文案；`TestRedeemMessagesKeepSwitchClassifierMarkers`（每个哨兵文本含 `已使用/过期/禁用/停用/账户/不存在` 之一，注释指向 switch `redeem.go:215-234`，HEAD 上对 `该兑换码已被使用` 红）。

**Hand-off → W**：无。**Docs / 契约**：根 `contracts.md` 记 error_code 新增与 `已使用` 措辞变更（switch 无需改）。**UAT**：用尽额度的 token 打 `/v1/chat/completions` → 402 body 全 ASCII；截断 JSON body → 400 message 全 ASCII；`POST /api/v2/lurus/redeem` 用已用码 → 400 `error_code=REDEMPTION_USED`。

## L4 — 特权 v1 写审计

**Owned（存在）**：`internal/adapter/handler/internal_api.go`、`internal/adapter/handler/internal_api_test.go`、`internal/adapter/handler/channel.go`（只改 `GetChannelKey`）、`internal/adapter/handler/log.go`（只改 `DeleteHistoryLogs`）、`internal/app/governance/audit_action.go`、`internal/adapter/handler/audit_coverage_gen.go`、`internal/adapter/handler/openrouter_pool.go`、`internal/adapter/repo/channel_pool.go`。**Created**：`internal/adapter/handler/internal_api_audit_test.go`、`internal/adapter/handler/channel_key_reveal_audit_test.go`、`internal/adapter/handler/openrouter_pool_test.go`。

**改动**：新 action `internal_key.created/updated/deleted/toggled`、`security.channel_key_accessed`、`logs.purged`（加入允许集）；四个 key handler + `GetChannelKey`（details = channel id + tenant，**永不含 key**）+ `DeleteHistoryLogs`（details = cutoff、scope、删除行数）调 `governance.RecordAuditEvent`；`rootGatedWritesOutsideAdmin` 与 `AuditExplicitRoutes` 登记这些 v1 路由。`ListOpenRouterMultiKeyChannels` 加 `scope TenantScope` 参数（root 全量、非 root 按租户），`GetOpenRouterApiPoolStatus` 按调用者传 scope。

**Oracle**：real-chain（真 `SetApiRouter`）：root `POST /api/api-keys/` 后恰好一条 `internal_key.created` 审计行且 details 不含返回的 key；`POST /api/channel/:id/key` 后一条 `security.channel_key_accessed`；role-10 `DELETE /api/log/` 后一条 `logs.purged` 带行数且这条审计行本身在 purge 后仍在。变异：删各 `RecordAuditEvent` → 对应测试红。`TestV1OpenRouterApiPool_TenantScoped`：两租户各一条 OpenRouter 渠道，role-10 见 1、root 见 2。变异：去掉 scope → 红。审计覆盖：`audit_coverage_test.go` 的 AST 走查对这六个 handler 找到 governance 调用（W 登记路由）。

**Hand-off → W**：`api-router.go:204/210/212/213` 四读加 `middleware.RootAuth()`；`idor_completeness_test.go` 两个前缀表加 `/api/openrouter-sync/`；`audit_coverage_test.go` 把六条 v1 路由纳入。**UAT**：root 建/删一把 internal key → `GET /api/v2/admin/audit/events` 出现两条；role-10 `GET /api/openrouter-sync/api-pool` → 403（W 落地后）。

## L5 — 擦除级联补全 + download_logs 最小化

**Owned（存在）**：`internal/lifecycle/privacy_erasure.go`、`internal/lifecycle/privacy_erasure_test.go`、`internal/adapter/repo/privacy_erasure.go`、`internal/adapter/repo/privacy_erasure_integration_test.go`、`internal/domain/entity/privacy_erasure.go`（若步骤枚举在此）、`internal/app/release_service.go`、`internal/app/release_service_test.go`、`internal/domain/entity/release.go`。**Created**：`internal/adapter/repo/erasure_model_coverage_test.go`。

**改动**：mappings 与 logs 步骤之间加第五步 `ErasureStepContentDeleted`：按 `AnonymizeLogsBatch` 的批次形态硬删 `chat_messages`（经 session 归属）→ `chat_sessions` → `midjourneys`，`tasks` 抹 `properties/data/fail_reason` 为空（保 quota、ids），`quota_data.username` → `ErasedMarker`；游标在步骤间推进以支持 crash-resume。强制函数测试：走 `repo/main.go` AutoMigrate 模型列表，每个含 `Username/Content/Prompt/Properties/IpAddress/UserAgent` 类字段的模型必须出现在级联覆盖表或带理由的豁免表（豁免表初始只含真正无个人数据的表）。`HandleDownload` 存 `repo.MaskIP(ip)`、UA/Referer 截到 512 字节；`DownloadLog.IpAddress` 列类型 `inet` 改 `varchar(64)`？——**不改列类型**（GORM 对 inet 列写掩码串 `1.2.3.0` 仍合法），只写掩码。

**Oracle**：扩展 `privacy_erasure_integration_test.go`：种 chat session + 2 条消息、1 条 mj、1 条带 `properties.input` 的 task、1 条 quota_data，跑完级联后断言 chat 行为 0、mj 行为 0、`tasks.properties` 不含 prompt、`quota_data.username == ErasedMarker`（HEAD 上四项全红）。变异：删 chat 步骤 → 红。强制函数测试 HEAD 红（四张表未覆盖）。`TestHandleDownload_StoresMaskedIP`：落库的 ip 不等于请求 RemoteAddr 且等于 `MaskIP` 结果。变异：存原值 → 红。

**Hand-off → W**：无。**Docs**：`doc/runbook/privacy-erasure.md`（若存在，否则 `doc/runbook/` 现有隐私 runbook）加第五步与 download_logs 说明。**UAT**：对一个 scratch 用户发起擦除（既有 admin 端点），完成后 `chat_sessions`/`chat_messages` 该用户为 0 行（UAT 库只读 SQL 核）。

## L6 — 日志保留 + 统计默认窗口 + runner 无事务指令 + 039

**Owned（存在）**：`internal/pkg/migration/runner.go`、`internal/pkg/migration/*_test.go`（既有）、`internal/domain/entity/log.go`（只加注释，不动索引）、`internal/adapter/handler/v2_log_stat.go`、`internal/adapter/handler/v2_log_stat_test.go`、`web/src/pages/v2/Log/index.jsx`、`web/src/pages/v2/Log/index.test.jsx`、`doc/runbook/database.md`、locale `console.log.*`。**Created**：`internal/lifecycle/log_retention.go`、`internal/lifecycle/log_retention_test.go`、`internal/adapter/repo/log_retention.go`、`internal/adapter/repo/log_retention_test.go`、`migrations/039_logs_tenant_created_index.sql`、`internal/pkg/migration/runner_no_transaction_pg_test.go`、`internal/pkg/migration/no_transaction_files_structural_test.go`、`doc/runbook/log-retention.md`。

**改动**
- runner：`applyOne` 识别文件首行 `-- lurus:no-transaction`：不 `BeginTx`，在专用 `*sql.Conn` 上直接执行，再单独一条语句记录版本；advisory lock 与 timeout 豁免不变；结构闸：no-transaction 文件只允许 `CREATE INDEX CONCURRENTLY IF NOT EXISTS` 语句（正则 + 注释允许）。文件头注释写明 CIC 失败会留 INVALID 索引且 `IF NOT EXISTS` 会跳过它，给出 `DROP INDEX CONCURRENTLY` 修复步骤。
- 039：`-- lurus:no-transaction` + `CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_logs_tenant_created_id ON logs (tenant_id, created_at DESC, id DESC);`。`entity/log.go` 加对应 `index:idx_logs_tenant_created_id` 标签？——**不加**（GORM auto-migrate 会用非 CONCURRENTLY 建同名索引；注释说明该索引由 039 拥有，AutoMigrate 不得声明）。
- 保留任务：`lifecycle/log_retention.go` 照 `session_sweep.go`：`taskreg.Register("log-retention", …, true, nil)` + `NewLeaderTask`；env `LOG_RETENTION_DAYS`（0=关）、`LOG_RETENTION_MONEY_DAYS`（0=关，下限 400）、`LOG_RETENTION_BATCH`（默认 500）、`LOG_RETENTION_MAX_BATCHES_PER_PASS`（默认 200）、`LOG_RETENTION_INTERVAL_SECONDS`（默认 86400）、`DOWNLOAD_LOG_RETENTION_DAYS`（默认 90）；非钱行窗口下限 30（低于下限 = 拒绝并 SysError，不删）；`repo/log_retention.go` 提供 `DeleteLogsBefore(ctx, cutoff, types []int, batch, maxBatches)`（复用 `DeleteOldLog` 的 id 子查询批删形态）与 `DeleteDownloadLogsBefore`；指标 `lurus_gateway_log_retention_deleted_total{table}`、`lurus_gateway_log_retention_pending_rows{table}`（**由 L10 在 metrics 包声明**；L6 调用 `metrics.RecordLogRetention(table, n)`、`metrics.SetLogRetentionPending(table, n)`，L10 落地前先写 SysLog）。
- 统计默认窗口：`serveLogStatV2` 及 `/stat/all`、`by_product` 在无 `start_time` 时绑定 `created_at >= now-30d`，响应回显 `window_start` 让控制台知道；Log 页 `computeStartTimeSec` 无显式起点时默认 7 天（UI 显示"最近 7 天"，可清除），`console.log.default_window` 键。
- `doc/runbook/database.md`：修正池数值（12/12，实测 `max_connections=100`）、生产核验主机 `hub.lurus.cn`（`/metrics` 边缘 404 需在节点上读）、readiness 4s；加 Migration 预算 10 分钟与 no-transaction 段；`doc/runbook/log-retention.md` 新建（窗口语义、下限、账单关系、指标、owner 决定）。

**Oracle**：`runner_no_transaction_pg_test.go`（PG tier）：带标记的 CIC fixture 应用成功并进 `schema_migrations`；同 body 无标记 → PG 25001。变异：删标记解析 → 两例都失败。结构闸：给 no-transaction 文件加一条 `UPDATE` → 红。`log_retention_test.go`：`LOG_RETENTION_DAYS=30`，种 now−40d(type error)、now−40d(type consume)、now−10d，一 pass 只删第一条；`=3` → 拒绝不删；未设 → 不删。变异：删 consume 豁免 → 红。`TestGetLogStatV2_DefaultWindowBound`：无 `start_time` 时查询绑定非零 `created_at >=`（用 sqlite DryRun 或响应 `window_start`）。变异：删默认 → 红。Log 页测试：无起点时请求带 `start_time`。

**Hand-off → W**：`cmd/server/main.go` 在 session sweep 旁启动 `lifecycle.StartLogRetentionWithContext(ctx)`；`api-v2-router.go` `/logs/stat`、`/logs/stat/all`、`/logs/export` 加 `middleware.CriticalRateLimit()`；两份 manifest 的 `ERROR_LOG_ENABLED` 注释改为"保留由 log-retention 任务负责，窗口由 LOG_RETENTION_DAYS 决定（未设=关）"，`startupProbe.failureThreshold` 30→120 + `deploy_consistency_test` 断言 `initialDelay + failureThreshold×period ≥ 600`；`.env.example` 加六个 env；`INDEX.md` 加 log-retention 行。

**UAT**：`GET /api/v2/admin/system/tasks` 出现 `log-retention` 且 `leader_only=true`；部署后 `schema_migrations` 含 039、`\di logs` 出现 `idx_logs_tenant_created_id`（VALID）；`GET /api/v2/lurus/logs/stat` 无参数时响应含 `window_start`；`/metrics` 出现 `lurus_gateway_log_retention_pending_rows`。生产：同索引存在（只读）。

## L7 — 控制台诚实

**Owned（存在）**：`web/src/pages/v2/Dashboard/index.jsx` + `index.test.jsx`、`web/src/pages/v2/Admin/Users/index.jsx` + `index.test.jsx`、`web/src/pages/v2/Admin/Audit/index.jsx` + `index.test.jsx`、`web/src/pages/v2/Admin/Diagnostics/index.jsx`（只把 `classifySettled` 换成共享 helper）、`web/src/index.jsx`、`web/src/components/table/users/modals/EditUserModal.jsx`、`web/src/pages/v2/CommandPalette/index.jsx`、`web/src/i18n/i18n-integrity.test.js`、`internal/adapter/repo/user.go`（只改 `Edit` 的 update map）、locale 子树 `console.dashboard.*`、`console.admin.*`、`console.palette.*`、`console.common.{yes,no,error}`、`console.settings.notify_unseeded`。**Created**：`web/src/helpers/loadState.js` + `.test.js`、`web/src/components/common/ErrorBoundary.jsx` + `.test.jsx`、`web/src/components/table/users/modals/EditUserModal.test.jsx`、`internal/adapter/repo/user_edit_persists_test.go`。

**改动**：`classifyLoad(settledOrError) -> 'ok'|'forbidden'|'unauthenticated'|'error'`；Dashboard 记 `meStatus/logsStatus`，非 ok 时 KPI/图表渲染"无法加载 — 重试"分支而非 `hasRealtimeData=false`，`qps_idle` 文案只在 ok 且空时出现；Users/Audit 三态（rows / forbidden / error-with-retry），空态文案只在 ok 且空时出现。`ErrorBoundary`（class 组件，面板 + 重新加载按钮 + `console.error`），`index.jsx` 包 `<PageLayout/>`；`vite:preloadError` 监听：sessionStorage 标记，每 session 只 `location.reload()` 一次。`EditUserModal` 删 password 字段与其 payload；`repo.User.Edit` update map 加 `daily_quota/base_group/fallback_group`（沿用现有零值语义：仅当调用方显式给出——用指针或 `Select` 列表，写明）。15 个键补进 en/zh；`i18n-integrity.test.js` 新增"src/pages/v2 与 src/components 引用的每个 `console.*` 键必须在 en.json 解析"，源文件 < 20 或键 < 500 fail-fast。

**Oracle**：Dashboard 五格（502 / 网络拒绝 / 200 success:false / 403 / 401）各断言错误分支可见且 `no traffic in last 5 min`、格式化 `0` KPI 不出现；一格 200 有行断言真数字。变异：恢复 hasRealtimeData 回退 → 五格红。Users/Audit 同形（含"empty 文案不出现"）。ErrorBoundary：lazy 拒绝 → 面板文案存在且 body 非空；同步 throw 的子组件不拖垮 shell；`vite:preloadError` 两次派发只 reload 一次。变异：从 index.jsx 摘掉 boundary → 红。`user_edit_persists_test.go`：PUT 带 daily_quota 后 GET 读回。变异：从 map 删 daily_quota → 红。i18n 引用闸 HEAD 红（15 键）。

**Hand-off → W**：App.jsx 六行 → `AdminRoute`（`z1_App.test.jsx` 表同步）；每个路由 Suspense 内包 `ErrorBoundary`；`api-v2-router.go` `tenantChannels` 组的会话拒绝包 `captureSessionDenial/rewriteAsV2Denial`（403 `PERMISSION_DENIED`）。

**UAT**：bridge role-10 会话打开 `/console/v2/dashboard`，隧道断开后端（或临时把 `E2E_BASE_URL` 指向 404）截图应显示"无法加载"而非零 KPI——用 Playwright 脚本模拟 502（`page.route`）实证；`/console/v2/channel` 以 role-1 访问 → 落 admin-gate 拒绝页。

## L8 — 浏览器面安全

**Owned（存在）**：`internal/adapter/middleware/browser_origin_guard.go` + `_test.go`、`internal/adapter/handler/zita_logout.go`、`internal/adapter/handler/zita_login.go`（只为导出/复用 `isLurusReturnURL`）、`internal/adapter/handler/v2_log_export.go` + `_test.go`、`internal/adapter/handler/v2_log_export_admin.go`、`internal/adapter/handler/v2_admin_audit.go` + `_test.go`、`internal/adapter/handler/oauth.go`、`internal/adapter/handler/oauth_test.go`、`internal/adapter/handler/zita_bootstrap.go`（只改登录写身份段）、`internal/adapter/handler/v2_bridge.go` + `_test.go`、`internal/adapter/handler/ratio_sync.go`。**Created**：`internal/adapter/handler/csv_cell.go` + `_test.go`、`internal/adapter/handler/zita_logout_test.go`、`internal/adapter/handler/session_rotation.go` + `_test.go`、`internal/adapter/handler/ratio_sync_test.go`。

**改动**：守卫先 `hasBrowserSessionCookie` 后判方法/来源，删凭证头豁免（决策表注释同步更新）。`rotateSessionID(c)`：`sessions.Default(c)` 断言 `interface{ Session() *gsessions.Session }`（gin-contrib/sessions v1.0.4 的具体类型有该方法；若断言失败则回退为 `session.Clear()` + 记 SysError 一次并返回 error 让调用方继续——不得静默），记旧 id，置 `ID=""`，`Save` 后 `common.RDB.Del("session_<old>")`（key 前缀从 `session_store.go` 常量取，不手抄）；三处登录在写身份前调用；会话注册表（cycle 7）登记新 sid。logout：`if returnTo != "" && !isLurusReturnURL(returnTo) { returnTo = "/login" }`，POST 臂 `redirect_to` 同。`csvCell(s)`：首字符 `= + - @ \t \r` → 前缀 `'`；三处导出每格经它。ratio_sync：组合 URL 后 `app.ValidateOutboundURL` 拒绝私网/环回（固定文案），改用 `app.GetHttpClient()`。

**Oracle**：守卫：{cookie + cross-site + 垃圾 Authorization} → 403 `CROSS_SITE_REQUEST`，{cookie + cross-site + 垃圾 X-API-Key} → 403，{无 cookie + 凭证头} → 200；变异：把头检查挪回 cookie 前 → 两行红。轮换：登录响应 `Set-Cookie` 的 session 值 ≠ 请求带入的值，旧 Redis key 不存在（miniredis）；变异：删轮换 → 红；三处各一例。logout 表：{GET,POST} × {`https://evil.example/`, `//evil.example/`, `/dashboard`, `https://hub.lurus.cn/x`, `javascript:alert(1)`}，Location / `redirect_to` 永不为站外绝对 URL；变异：删校验 → 站外行红。CSV：helper 表 + 每个导出一例 token 名 `=HYPERLINK(...)` 的首格以 `'` 开头；变异：某一导出去掉 helper → 该例红。ratio_sync：`{"base_url":"http://127.0.0.1:6379"}` → 固定拒绝且 httptest 监听器零连接；变异：删校验 → 红。

**Hand-off → W**：无。**契约**：会话轮换对 switch/lutu 无影响（不用 hub cookie）；写进 contracts。**UAT**：bridge 登录两次比对 cookie 值不同；`GET /api/v2/auth/zita-logout?return_to=https://evil.example/` → 302 到 `/login`；带 cookie + `Sec-Fetch-Site: cross-site` + `Authorization: Bearer junk` 的 POST → 403；导出 CSV 含前缀。

## L9 — URL 里的租户必须到达数据

**Owned（存在）**：`internal/adapter/handler/v2_provision.go` + `_test.go`、`internal/adapter/handler/switch_user_info.go` + `_test.go`、`internal/adapter/handler/switch_reconciliation.go` + `_test.go`、`internal/adapter/handler/user_heartbeat.go` + `_test.go`、`internal/adapter/middleware/oidc_auth.go`（只改 cookie 臂的租户门）、`internal/adapter/handler/tenant_credit_pool.go`（handler；`GetCreditPoolForEndUser`）、`internal/adapter/handler/v2_project.go` + `_test.go`、`internal/adapter/handler/totp_flow_test.go`。**Created**：`internal/adapter/handler/tenant_reaches_data_c13_test.go`。

**改动**：`ProvisionV2`：`autoCreateBridgedUser(accountID, tenant.Id)`；已存在用户 `user.TenantId != tenant.Id`（且都非 default）→ `403 TENANT_MISMATCH` + `governance.RecordAuditEvent(ActionProvisionTenantMismatch)`；解析租户后 `repo.TenantGate`。`authenticateSwitchRawToken`、`SwitchReconciliation`、`UserHeartbeat`（slug 分支后）、`GetCreditPoolForEndUser`（cookie 臂，OIDC Bearer 臂已有）都过 `repo.TenantGate(token.TenantId)`；heartbeat 挂起 → `403 {success:false,error_code:"TENANT_DISABLED",status:"suspended"}`；topup 挂起 → 复用 `switch_redeem.go:209` 的客户文案 + `error_code=TENANT_DISABLED`，不消费兑换码。`GetProjectSpendV2` → `projectAdminCtx`，改 `:282-284` 假注释。`totp_flow_test.go:585-586` 改为只在 `verifyCode == confirmCode` 时 skip，非 200 → `t.Fatalf`。

**Oracle**：provision：`acme` 租户 + 无 hub 用户的 account → 用户与 token 的 `TenantId == acme`（HEAD 红）；已在 `beta` 的用户打 `/api/v2/acme/provision` → 403 `TENANT_MISMATCH` + 审计行；变异：恢复 `"default"` 字面量 → 红。挂起租户表测试：topup 不入账且兑换码仍 enabled、user/info 非 200、heartbeat 非 200 active、credit-pool/me cookie 臂 403；翻回 enabled → 今日行为；变异：逐个删 `TenantGate` → 对应格红。`TestGetProjectSpendV2_ForbiddenForNormalUser`（照 `v2_log_stat_test.go:198`）。totp：把 step-up 改成恒 403 后 `-run TestBackupCodes_RegenerateInvalidatesUnused -v` 必须 FAIL 而非 SKIP（看 RUN 行）。

**Hand-off → W**：`router/tenant_slug_guard_completeness_test.go`（新）：每条 gin path 以 `/api/v2/:tenant_slug` 开头的路由要么挂 `TenantSlugGuard`，要么在带理由的豁免表（今日四条 + 各自替代检查名）；变异：从某组摘掉守卫 → 闸点名。**契约**：heartbeat 403 `TENANT_DISABLED` 与 topup 的 `error_code` 写进 contracts（switch owner 决定展示，O-heartbeat）。**UAT**：把 `probe-invite` 租户置 suspended（admin API，用完恢复）：该租户 token 的 `POST /api/v2/switch/heartbeat` → 403 `TENANT_DISABLED`；`GET /api/v2/switch/user/info` → 403；恢复后 200。

## L10 — 告警补齐 + 指标基数 + 文档

**Owned（存在）**：`deploy/r6-host-netdata/health.d/newhub.conf`、`deploy/r6-host-netdata/README.md`、`internal/pkg/metrics/**`（全部，含既有测试与 `netdata_alarm_series_test.go`、`declared_series_written_test.go`、`alert_wiring_honesty_test.go`）、`doc/runbook/ha-deployment.md`、`doc/runbook/seam-s1-activation.md`、`doc/runbook/channel-auto-ban.md`、`doc/uat-handbook.md`。**Created**：`doc/runbook/metrics-scrape-stale.md`、`internal/pkg/metrics/method_label_test.go`、`internal/pkg/metrics/leader_task_age_test.go`。

**改动**：`metrics/middleware.go` 方法白名单（GET/HEAD/POST/PUT/PATCH/DELETE/OPTIONS/TRACE/CONNECT，其余 `other`）；`instance.go` 新 gauge `lurus_gateway_leader_task_age_seconds{task}`（由既有成功戳的伴随刷新计算）；新声明 `lurus_gateway_log_retention_deleted_total{table}`、`lurus_gateway_log_retention_pending_rows{table}`（供 L6）、`lurus_billing_task_refund_wallet_unreversed_total`（供 L1），导出 `RecordLogRetention / SetLogRetentionPending / BillingTaskRefundWalletUnreversed`；`BillingSettlementFailedTotal` 预注册 `path=realtime`。conf 新增块（每块带 `# series:`/`# runbook:`）：`newhub_metrics_scrape_stale`（`on: prometheus.newhub.lurus_gateway_instance_info`，`calc: $now - $last_collected_t`，warn>60 crit>300）、`newhub_channel_auto_disabled`（`chart labels: action=disable_error` 与 `disable_latency` 两块）、`newhub_channel_sole_latency_ban_skipped`、`newhub_credit_pool_debit_lost`(crit>0)、`newhub_billing_advisory_meter_lost`、`newhub_billing_zero_amount_charge`、`newhub_credit_pool_stranded_open`、`newhub_upstream_insufficient_balance`、`newhub_task_stalled`（age gauge，warn > 3×interval）、`newhub_schema_migrations_pending`(warn>0 持续 15m)、`newhub_panics_recovered`、`newhub_log_retention_backlog`；SCRAPE TOPOLOGY 块写"UAT 单副本测得的速率换生产阈值要除以副本数"；`newhub_rate_limit_degraded` 的 `!` 位置加"待 live 验证"日期注释（不盲改）；README 计数改为实数并列出全部块及安装状态；`seam-s1-activation.md` 改 ns/PG pod/7 个 secret key/3 副本；`ha-deployment.md:94-96` 指向仓内告警文件；`uat-handbook.md` 同类退役标识。

**Oracle**：`method_label_test.go`：GET + 两个垃圾方法 → 注册表恰多一个 `other` 标签集；变异：删白名单 → 两个。`leader_task_age_test.go` 冻结时钟。`netdata_alarm_series_test.go` 对每个新 `on:` 解析到已声明序列（既有闸自动覆盖）；新增反向断言"Go 注释里自称 page/alert 的序列必须出现在某个 `on:`"（HEAD 对 `metrics.go:415`、`migrations.go:14`、`panic.go:11` 红）；README 计数闸：`currently defines N alarms` 的 N == conf 非 DEAD 块数且每个块名出现在 README。

**Hand-off → W**：`deploy_consistency_test.go` 的退役标识闸加 `lurus-staging`、`lurus-pg-1`、`pg-restore-drill.sh` 模式并把 `doc/*.md` 顶层纳入走查；`INDEX.md` 加 metrics-scrape-stale / log-retention 行。**UAT**：`/metrics` 出现 `lurus_gateway_leader_task_age_seconds`、`log_retention_*`；对 UAT 打一个非法 HTTP 方法（`curl -X FOO`）后 `lurus_gateway_requests_total{method="other"}` 出现且不出现 `method="FOO"`。

## L11 — 优雅下线

**Owned（存在）**：`internal/adapter/handler/health.go`（+ 其测试）、`internal/adapter/repo/usedata.go`、`internal/app/notify-limit.go`、`internal/app/notify_limit_test.go`、`doc/runbook/staging-deploy.md`。**Created**：`internal/lifecycle/drain.go` + `drain_test.go`、`internal/adapter/repo/usedata_flush_test.go`、`internal/adapter/handler/health_draining_test.go`、`doc/runbook/graceful-drain.md`。

**改动**：`lifecycle.Drainer`：`MarkDraining()`（atomic）、`IsDraining()`、`Shutdown(ctx, srv *http.Server, inflight func() int) (cut int, err error)`：先 MarkDraining，再 `srv.Shutdown(ctx)`；超时时读 `metrics.ActiveConnections`（若不可读则用 `srv` 的连接状态钩子计数，`ConnState` 在 `buildHTTPServer` 由 W 挂）记录 `cut` + SysLog，**返回 nil**（超时视为预期结局），计数器 `lurus_gateway_shutdown_cut_requests_total`（L10 声明；落地前 SysLog）。`/api/health` 与 `/api/status`：`IsDraining()` 时回 503 `{"status":"draining"}`（readiness 先摘流量；liveness 用同路径——写明 kubelet 在 tgps 内不会因此重启，因为 SIGTERM 已发出）。`usedata.go` ctx.Done 臂调 `SaveQuotaDataCache()`，删死的 `UpdateQuotaData`。`notify-limit.go`：删 `startCleanupTask`，`checkMemoryLimit` 的懒启动改调 `InitNotifyLimitCleanup`（单一 Once），`TestInitNotifyLimitCleanup` 改成有断言（goroutine 起、cancel 后退）。

**Oracle**：`drain_test.go`：慢 handler 在途时调 `Shutdown`（预算 200ms）→ 返回 nil、`cut == 1`；无在途 → `cut == 0` 且早于预算返回；变异：把超时改回返回 err → 红。`health_draining_test.go`：MarkDraining 后 `/api/health` 503 `draining`；变异：删分支 → 红。`usedata_flush_test.go`：记一桶、cancel ctx → quota_data 有行；变异：删刷盘 → 红。notify-limit：两种顺序下两测试都有意义（`-shuffle` 两个种子实跑 `-v`）。

**Hand-off → W**：`main.go`：shutdown goroutine 改调 `lifecycle` drainer（超时不再 FatalLog），`buildHTTPServer` 挂 `ConnState` 计数；`InitNotifyLimitCleanup(ctx)`/`Stop` 接进 lifecycle；两份 manifest `GRACEFUL_SHUTDOWN_TIMEOUT=75s`、`terminationGracePeriodSeconds: 90`、preStop 5 不变 + `deploy_consistency_test` 断言 `tgps ≥ preStop + graceful + 10`；`.env.example` 说明。**Docs**：`graceful-drain.md`（时间线、被切流的判据、O-tgps）。**UAT**：`kubectl rollout restart` 期间保持一条 `faultsim-slow-headers`（延迟 60s）流打开：pod 日志出现 `draining` 行与 `cut=1`（或 0 若在预算内完成），pod 退出码 0（`kubectl get pod -o jsonpath` 上一容器 `exitCode`），`/api/health` 在 SIGTERM 后立即 503。

## W — 接线（串行，全部 lane 验收后）

顺序：L6 main.go 任务启动 + 路由限流 + manifest 探针预算 → L11 main.go drain + `ConnState` + manifest tgps/graceful → L4 openrouter RootAuth + IDOR 前缀 + 审计覆盖登记 → L7 App.jsx 六行 AdminRoute + ErrorBoundary + tenantChannels 403 + z1_App 表 → L9 tenant-slug 守卫完整性闸 → 连接池 12/12 + 预算闸改算法（`LOG_SQL_DSN` 判池数，预算 40，注释记实测 100）+ `RELAY_MAX_CONCURRENT_PER_TENANT=64` → workflows（go-ci paths + 反向闸；pg-integration 加 entity + `-shuffle=on`；web-ci e2e `results.json` 断言；`timeout-minutes`；`concurrency`）→ `.env.example`（六个保留 env、`GRACEFUL_SHUTDOWN_TIMEOUT`、`RELAY_MAX_CONCURRENT_PER_TENANT`、OPS-6 的十二个只在 manifest 的变量）+ 解析/manifest 对账闸 → 退役标识闸模式（L10）→ `INDEX.md` 三行 → 两个棘轮 shrink→fail 并把本轮缩小的行数写进表 → 全量闸门。并行阶段已知必红、W 之后才绿：`router/console_consumes_endpoints_test.go`（若 L7/L6 新增投影）、`faultsim_wiring_test.go`（无新 mode，本轮应不动）、`z1_App.test.jsx`（L7 改了 index.jsx 不改 App.jsx，应不红）——lane 内禁止"修"W 独占文件。

## 4. 执行协议

1. 计划落盘（本文件）+ 根 ledger 预留 039 + 分支 `feat/cycle13-industrial-grade`。
2. dev workflow：11 lane 并行（M 档 lane 用高档模型、S 档用轻量模型；prompt 钉死：只写自己 owned/created 文件、可读任何文件、禁 git/禁 prod/禁 ssh/禁 `bun run build`、新文件禁厂商模型名与工具名、每个 oracle 先红后绿、写完只跑自己包的测试 `-run` 并看 `=== RUN` 行、外来编译失败等 60s 重试 5 次）；每 lane 一个对抗验收（含 "auditor missed" 槽）→ 修复轮（只读自己 `## Lx` 段 + operator 决定文件 `%TEMP%/c13_decisions.md`）。
3. 每 lane 验收后由我逐路径 stage 该 lane 文件做 WIP commit（先 `git status` 看 staged；五个外来 `coverage_*` 永不碰）。
4. W（串行 Agent）落地 hand-off 清单 + 自有测试；我亲手收尾（散文、绝对词、闸门变异自检、`-run -v` 看 RUN 行）。
5. 本机闸门（lint 与全量测试不并跑）：`go vet ./... && go build ./...`；`go test -short -count=1 -p 2 ./...`；结构闸集 `-run`；`-shuffle=on` 对本轮改过的包各跑两个种子；`golangci-lint run --new-from-rev=origin/main ./internal/... ./cmd/...`；`cd web && bun run lint && bunx eslint src --ext .js,.jsx && bun run test && bun run build && bun scripts/check-bundle-budget.mjs`；`-race` 与覆盖率棘轮只在 CI。
6. PR → CI 全绿（含新 paths 触发）→ merge → auto-pin → ArgoCD（prod 3/3 + UAT 1/1 同 digest；039 在两库都 VALID）→ 按 lane 的 UAT 探针（生产只读）→ 根仓 coord 两文件加段 → 报告 + 记忆。

## 5. 验证要点（诚实闸）

- 每条"能用"用真实链路证明（real-chain 路由、真实 `RecordConsumeLog` 落库再查、UAT 探针），不用手搭形状。
- 每个闸门自身做变异验证；`-run` 必须 `-v` 看到 `=== RUN`。
- 否定式断言先枚举所有拼法（本轮已被 `request_fingerprint` 教训：谓词不止 `Where("col = ?")` 一种）。
- UAT 证不了的（realtime/任务退款、擦除全表、平台钱包腿）明写"hermetic 证/owner 验证"。
- 改指标或体量的 commit 附 measured before/after（棘轮行、bundle 预算、pool 数）。

## 6. do-not-regress（本轮碰到的现役行为）

- v1 `authHelper` 的 200 `{success:false}` 形态（switch 消费）；`switch_redeem.go` 现有中文哨兵子串（switch 分类器）；`GET /api/channel/test/:id` AdminAuth；`/api/v2/switch/*`、`/api/v2/lutu/*` 响应形状（heartbeat 新增 403 为加严）；`POST /api/v2/bridge/exchange` 无 cookie 路径（轮换只在 cookie 会话）；`TenantGate` 的 default 豁免与 observe 语义；`DeleteOldLog` 手工路径；账单 `amount_cny` 字段名；cycle 12 的 CSRF 决策表其余行；`metricsAuthMiddleware`；`ReadTimeout/WriteTimeout` 保持 0；faultsim/e2e bridge 在生产不存在；`QuotaPerUnit` 既有合法值范围内的写入。

## 7. 不做（记录，下一轮候选）

- **容量 lane**（CAPACITY-1/M1 的 `ZRANGE 0 -1` → O(1) 桶；CAPACITY-5 stream scanner 每块一 goroutine+timer；CAPACITY-9 faultsim `ok` 模式 + `cmd/loadgen` + 容量 runbook；CAPACITY-7 pprof 挂 gin 走 metrics 鉴权；CAPACITY-M3/M4 定价重建与 quota_data 刷盘的全局锁）——零真人流量下是潜伏项，与基线一起做。
- **租户用户管理**（CONSOLE-3 全量：`GET/PUT /api/v2/:slug/users[/:id]`、sessions 撤销、v2 Users 页、HFShell 入口）——owner 先答范围（O-tenant-users）。
- SLI-1 进程内 SLI gauge、SLI-2 pod 标签/逐 pod 抓取——等 O2。
- ERRCODES-3 全套 error_code 方案与 42 处绕过 `resolveErrorMessage` 的调用点、-7 四种拒绝形状统一、-9/-10/-M1/-M2/-M3/-M6。
- V1DOORS-6（io.net 19 路由 6,931 行零消费者，其一会花运营者 stake）与 -7（prefill_group）退役、V1DOORS-2/SECURITY-M2 v1 写面纳入审计覆盖、V1DOORS-M3/SECURITY-1 状态改变的 GET 改 POST（switch 契约）——owner 决定（O-v1doors）。
- MONEY-2 预消费期冻结 `QuotaPerUnit`（本轮只加范围守卫）；MONEY-M2 缓存先于 DB 的四个 quota helper；DATALIFE-M1 quota_data 双写者；DATALIFE-6 审计 `retention_until=0` 行（024 触发器阻止回填，owner）；DATALIFE-7 导出 OFFSET 分页。
- SPLITS 全部（棘轮已改 shrink→fail 后再拆）；SPLITS-M1 非当前语言的 locale 表懒加载（入口 chunk 70% 是两张 locale 表）；SPLITS-M2 八个时间戳渲染器统一。
- TESTS-1（metrics 两个走仓闸占 race job 49%）、TESTS-6 每周非 short `-race` job、TESTS-M5 fixture 每次全量 AutoMigrate、TESTS-M6 web 覆盖率地板。
- `__Host-` cookie 改名（O-cookie）；api-keys 写 step-up（O1）。

## 8. Owner 事项（git 改不到）

- **O1** 生产 root 注册 TOTP（`user_totps` 仍空）——step-up 类修复全部卡在这。
- **O2** netdata 重载：线上只加载 3/15 条（本轮实测 `alarms?all` 只见 outbox×2 + platform_breaker）；本轮再加 12 块；`OBS_SLACK_WEBHOOK_URL` 仍未设 = 没人收到任何告警。
- **O-pool** PG `max_connections=100`（实测）：本轮把 newhub 收到 12/12（prod 36 + UAT 12），是否上调 PG 到 200（需重启 `lurus-pg-0`，影响全部服务）。
- **O-pitr** `archive_mode=off`：每日 pg_dump 的 RPO 是 24h；PITR 需 WAL 归档到 `/data` + 空间预算。
- **O-refund** platform 侧退款 RPC（`WalletDebit` 的反向）——没有它，失败任务的钱包腿只能记指标。
- **O-retention** `LOG_RETENTION_DAYS` / `LOG_RETENTION_MONEY_DAYS` 的合同值（本轮默认关）。
- **O-tgps** 是否把 `GRACEFUL_SHUTDOWN_TIMEOUT`/tgps 抬到覆盖 `STREAMING_TIMEOUT=300`（本轮 75/90）。
- **O-heartbeat** switch 对 heartbeat 403 `TENANT_DISABLED` 的展示（今日只把 401 当吊销）。
- **O-scrape** 三副本 NodePort 随机抓取：pod 标签（×3 基数）还是逐 pod 抓取脚本。
- **O-cookie** `__Host-session` 改名 = 部署时全员重登。
- **O-v1doors** io.net deployments / prefill_group 退役与 v1 目录写门。
- **O-tenant-users** role-10 能否改同租户用户的角色/配额；删除是否永远归 platform。
- 旧项照旧：O-session（生产 `SESSION_REGISTRY_ENABLED`）、O-org、O-ratio、O-pricing、O-lang、O7；根仓 `doc/coord/*` 两文件与 ledger 039 行由 owner 提交。
