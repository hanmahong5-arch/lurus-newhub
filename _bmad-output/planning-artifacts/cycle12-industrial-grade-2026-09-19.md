# 第十二轮 — 工业级生产水平（cycle 12）

日期 2026-09-19。基线 `main@4c43f8a6`（cycle 11 已上线）+ 分支 `feat/cycle12-industrial-grade` 首个提交 `a53766b2`（测试隔离修复）。草稿 PR #188。

## 1. Context

第十一轮把「第一个客户会撞上的问题」修完后，owner 问的是：品味、优雅性、各工程指标如何，然后要求列可靠改进计划并全面推进到工业级生产水平。本轮先实测了指标，再用 12 个只读侦察维度（每维度一个取证 agent + 一个对抗复核 agent，24 agent / 4.0M token / 43 分钟）对 HEAD 取证，最后由我逐条对 HEAD 亲手复核后成文。

### 1.1 实测指标（2026-09-19，CI 数字来自 main@4c43f8a6 的跑；本机计数排除别人的五个 `coverage_*` 未跟踪文件）

| 指标 | 实测 |
|---|---|
| Go 源码 / 测试 | 133,839 行 · 711 文件 / 264,863 行 · 1,052 文件（比 1.98） |
| Web 源码 / 测试 | 97,053 / 76,001 行（比 0.78） |
| Go 覆盖率 | app 89.7% · repo 78.8% · handler 75.5%，闸 86 / 77 / 64 |
| Web 覆盖率 | 语句 67.8 · 分支 63.1 · 函数 64.1 · 行 68.3，闸 60 / 54 / 55 / 60 |
| lint 债务 | 全仓 646 条，上限 666；新增零容忍 |
| Go CI 近 20 次 main | 17 绿 3 红，3 红全部是 `-race` job（09-08、09-09、09-19） |
| 大文件 | >800 行 40 个，>1500 行 7 个；最大 `web/src/components/table/channels/modals/EditChannelModal.jsx` 3,289 行 |
| 路由注册 | 446 处（v1 + v2 双栈） |
| 前端产物 | 101 chunk 共 12.4 MB 未压缩；入口 chunk 6.57 MB |
| i18n 叶键 | en 4,020 · zh 3,766 · vi 3,244 · ru 2,703 · fr 2,688 · ja 2,667 |
| 运行 | prod 3 副本 0 重启；30 天 12,772 次调用里真人 14 次 |

### 1.2 本轮触发事实（全部由我对 HEAD 亲手核实过 file:line）

1. **`-race` job 是三次红的唯一失败 job**，每次一个不同的测试泄漏：09-19 main 是 `session_billing_poll_test.go:150` 泄漏的轮询 goroutine（已修，`a53766b2`）；09-09 是 `internal/pkg/search/sync.go:103` 无 join 的 worker pool 提交；PR #188 上又红了一次，这次是 `relay.go:723` 的裸 `gopool.Go → app.DisableChannel` 在 `context_tier_channel_test_test.go:71` 的 cleanup 之后还在跑。handler 包有 8 处裸 spawn 绕过 `AsyncGo` seam（`v2_channel.go:381/572/645` 三处 `go repo.InitChannelCache()`，`relay.go:722/729`，`channel-test.go:658`，`release.go:191`，`tool_version_worker.go:59`，`playground.go:218`，`ratio_sync.go:131`；`model_sync.go` 四处是 wg join 的不算）。`internal/app/governance/audit.go:69` 也是裸 `gopool.Go`。没有任何结构闸挡新的裸 spawn。
2. **入口 chunk 6.57 MB 的 67% 是图标库**：`web/src/helpers/render.jsx:25` `import * as LobeIcons from '@lobehub/icons'` + `:439` 用运行时字符串索引，tree-shake 不掉；`helpers/index.js:25` 桶导出把它拉进每个页面。另外 App.jsx:24-37 静态 import 了 10 个 legacy 页面。
3. **静态资产走 60 次/180 秒的 web 限流**：`internal/adapter/handler/router/web-router.go:18` `router.Use(middleware.GlobalWebRateLimit())` 在 `:20` `static.Serve` 之上；生产 manifest 没有 `GLOBAL_WEB_RATE_LIMIT*`（`grep -c` = 0），跑代码默认 60/180s；UAT 因此在 08-30 白屏过、显式设了 600。`vite-plugin-compression` 产出的 5.75 MB `.br/.gz` 被 `web/embed.go:6` 嵌进二进制却从不服务（gin gzip 在线重压）。`/api/v2` 整组无 gzip（`api-v2-router.go:14-36` 只有 CORS/body/identity/限流）。
4. **控制台把模型输出当活 HTML 渲染**：`web/src/components/common/markdown/MarkdownRenderer.jsx:108-111` 取 `code.language-html` 的 innerText，`:230` `dangerouslySetInnerHTML`；`web/src/helpers/sanitize.js:38` 的 DOMPurify `sanitizeHtml` 全仓零调用（只有 `escapeHtml` 被 CodeViewer 用）；全仓 11 处 `dangerouslySetInnerHTML` sink，无 CSP（`security_headers.go:9-14` 明写不设）。`DocumentRenderer/index.jsx:218` 的 `useEffect` 在 `:168`/`:187` 两个 early return 之后（条件 hook），法务页内容是 HTML 时整页白屏；eslint 加载了 react-hooks 插件却零规则（`.eslintrc.cjs:12,16`），`--rule rules-of-hooks:error` 实测 16 处（11 处生产代码）。
5. **约 18k 行 legacy 表格代码无路由可达**：`web/src/components/table/{channels,tokens,usage-logs,models}` 零外部 importer，`web/src/hooks/{channels,tokens,usage-logs}` 只被这四个目录 import。最大的两个前端文件（EditChannelModal.jsx 3,289 行、useChannelsData.jsx 1,334 行）都在里面 —— 该删不该拆。
6. **v2 admin 对 role-10 的拒绝是 HTTP 200 `{success:false}`**：`middleware/admin_jwt_auth.go:69-73` 无 Authorization 头时回落 `authHelper(c, RoleRootUser)`，`auth.go:305-311` 对权限不足回 200；HFShell 六个根管理入口对 role 10 可见；`pages/v2/Admin/Gateway/index.jsx:60-62` 只 catch 403，其它任何失败（502、断网）都渲染「无熔断」的全绿；`Admin/Settings/index.jsx:216-222` 只在 success 时 setOptions，失败时所有开关显示为关。`/console/v2/admin/*` 只有 PrivateRoute，role-1 用户直接输 URL 就能进。`router/v2_admin_system_tasks_mount_test.go:113-127` 把 200 形态钉成了约定。switch 全仓 `/api/v2/admin` 零命中（消费者只有控制台）。
7. **多租户闭合缺口**：公开 `GET /api/pricing` 与 `/api/v2/switch/pricing` 的目录来自 `repo/pricing.go:99 GetAllEnableAbilityWithChannels()`（`repo/ability.go:21-25` 无租户过滤）⇒ 每个租户的私有模型名与分组名对匿名可读；`GET /api/user/models`、`/api/channel/models_enabled`、`/api/models/missing` 对非 root 也不过滤；`GET /api/channel/tag/models`（`channel.go:1530-1568`）任意 tag 串可读他租户模型列表；**`PUT /api/channel/`**：`PatchChannel` 嵌入 `repo.Channel`（`channel.go:1252-1256`，`entity/channel.go:16` `tenant_id` 有 json tag），`:1275 enforceTenantScope` 只查 `originChannel.TenantId`，请求体里的 `tenant_id` 可以把自己的渠道送给别的租户或 `default` 池。
8. **配置热重载在无锁改活对象**：`internal/pkg/setting/config/config.go:96-130` 用 reflect 把 option 值直接写进已注册的活 struct（`gemini.go:9` `SafetySettings map[string]string`，`:52` relay 路径读它），每 60 秒 option-sync tick 都写 ⇒ `fatal error: concurrent map read and map write` 类崩溃；`setting/sensitive.go:26-35` 先置空再 append，relay 的敏感词过滤能读到空表；`repo/option.go` 有 **18 处** `x, _ = strconv.Parse*(value)` 丢弃解析错误 ⇒ 一个空或畸形的管理值把 `QuotaPerUnit` 这类钱变量静默置 0；`handler/option.go:22 GetOptions` 对只读迭代拿的是写锁。
9. **出站依赖无界**：`internal/pkg/common/redis.go:40-45` 只设 `PoolSize`，无 Dial/Read/Write 超时，`ContextTimeoutEnabled` 默认 false ⇒ 所有 ctx deadline 对 Redis 命令无效，Redis 挂起时每条命令 3 秒；`identity_grpc_client.go:43` `WaitForReady(true)` + 5 秒 + HTTP 回退 ⇒ platform 挂时每次 relay 预授权多 5 秒、控制台请求多 10 秒；NATS `publisher.go` 的 `Publish(_ context.Context, …)` 丢弃 ctx 且默认 MaxReconnects 用尽后永久死掉无指标；`cmd/server/main.go:407` `http.Server` 无 `ReadHeaderTimeout/IdleTimeout`；`repo/channel_cache.go:31-36` 两次 `DB.Find` 丢错误后用空表整体替换活路由表（一次失败查询 = 该副本 relay 路由清空）；`repo/main.go:128-130` GORM 用默认 logger（彩色明文进 JSON 流、record-not-found 当 ERROR、错误时打印绑定参数含 API key）；`:216-217` 每副本每池 `SQL_MAX_OPEN_CONNS` 默认 1000、`SetConnMaxLifetime` 60 秒（`PrepareStmt: true` 下每分钟丢一次预编译语句）。GOMAXPROCS 一项被我否决：`go.mod` 是 `go 1.25.1`，Go 1.25 运行时已按 cgroup CPU 配额设 GOMAXPROCS，不需要手工设；GOMEMLIMIT 仍无人设。
10. **Lugo 组织 ↔ newhub 租户**：`entity/tenant.go:18` `IDPOrgID`（列 `zitadel_org_id`，unique not null）已 1:1 映射 IdP 组织；OIDC 回调 `oauth.go:326` 只按 `stateData.TenantSlug` 取租户，从不校验 `claims.OrgID` 与 `tenant.IDPOrgID` 一致 ⇒ 组织 A 的账号可以在租户 B 的登录 URL 上被自动开通进租户 B；身份会话桥接路径 `zita_bootstrap.go:65,204-206` 只认邀请码否则 `default`，且不跑 `TenantCanAddUser` 座位上限（只有死的 OIDC 路径 `user_mapping.go:271-277` 跑）；`repo/tenant.go:148-150 DeleteTenant` 是软删，`middleware/auth.go:355/681/894` 的守卫写法是 `tErr == nil && tenant.IsDisabled()` ⇒ 被删租户的 token 继续中转花钱（控制台 fail-closed、钱路 fail-open）。
11. **i18n**：`web/src/i18n/i18n.js:24-29` 六个语言包全部静态 import，`:44 fallbackLng: 'zh'`、无 `supportedLngs` ⇒ 任何非 en/zh 浏览器语言（de/es/pt/ko/ar…）直接得到 100% 中文（含登录页）；fr/ja/ru/vi 对 console 命名空间约 3% 完成度却在 `LanguageSelector.jsx:47-70` 与 `PageLayout.jsx` 可选、且被浏览器自动选中；`?lng=ja` 一次访问会通过 localStorage 永久污染。
12. **运维文档说假话**：`doc/runbook/pg-restore.md` 是退役的 docker-compose/wal-g 拓扑（首条命令 `docker compose stop lurus-api`）；`doc/enterprise-ai-gateway-positioning.md` §5 声称 HPA/PDB/拓扑分布/已部署分级告警，`deploy/` 里都不存在（PDB/HPA 在 `deploy/k8s/r6-stage/README.md:30-36` 是有意不做）。

### 1.3 Owner 问的两个问题（答案，供以后引用）

- **公司其它产品有没有多租户？** 有，而且真源在 Lugo（= platform 的品牌名，ADR-0019；服务标识 `platform-core`、`identity.lurus.cn` 不变）：`2l-svc-platform/migrations/007_organizations.sql` 的 `identity.organizations / org_members / org_api_keys / billing.org_wallets`，`083_org_settings.sql`，`029_org_kova_services.sql`；API 有 `v1 GET/POST /organizations`、成员、密钥、钱包、设置，admin 列表/创建，internal `POST /orgs/resolve-api-key`、`/orgs/:id/subscriptions/issue`。tally 用 PG RLS 按 `tenant_id` 隔离并复用 platform 账户。newhub 的 `tenants` 表是自己的投影，键是 IdP 组织 id。
- **lugo 可以结合使用吗，且不耦合？** 可以，而且已经结合了一半：OIDC 登录路径按 org claim 解析租户并可自动建租户。剩下的一半（身份会话桥接路径不看组织、投影没有 platform 组织 id、组织的停用/改名/删除到不了网关）要靠 **API-only 的投影**：newhub 保留自己的 `tenants` 表，只新增可空的 `platform_org_id`，经 platform 的 internal capability API 拉「账号所属组织」再放置用户，事件或轮询刷新 name/status（永不投影 slug —— switch 靠 slug 路由），feature flag 默认关。platform 侧需要新增一个端点（`GET /internal/v1/accounts/:id/organizations`，既有 `account:read` scope），是跨仓契约，owner 事项。禁止共享库、禁止直连 schema（`lurus.yaml` cross_group_policy）。本轮只做 newhub 单方就能做的三道守卫 + 设计稿（L9），投影本体等 platform 端点落地后再做。

## 2. 已拍板的决定（执行时不再争论）

- **v2 admin 对非 root 会话回 HTTP 403 `{success:false, error_code:"PERMISSION_DENIED"}`**，v1 的 200 形态一字不动（switch 消费 v1）。`v2_admin_system_tasks_mount_test.go` 的 200 钉子改成 403 钉子。
- **CSRF 源守卫**只对**带 cookie 会话且无 `Authorization`/`X-API-Key`** 的非 GET 请求生效：`Sec-Fetch-Site` 为 `same-origin`/`none` 放行，`same-site`/`cross-site` 403；头缺失时看 `Origin` 是否在 CORS 白名单，两者都缺失放行（老客户端）。relay、switch、lutu、internal API 全部带凭证头，不受影响。
- **i18n 采用「只发 zh + en」**：`supportedLngs: ['zh','en']`，非 zh 一律回落 en；fr/ja/ru/vi 的 JSON **保留在仓库但不再 import**（不打进包、不在选择器出现），闸门按覆盖率 ≥ 95% 决定选择器可见集。这是可逆的诚实，不是删翻译。
- **被删/不存在租户的守卫先 observe 后 enforce**：`TENANT_MISSING_MODE` 默认 `observe`（记指标 + 日志、放行），生产指标为 0 一周后 owner 翻 `enforce`。已存在行但 `IsDisabled()` 的行为不变。
- **配置热重载改成 copy-on-write**：注册的 config 先解码进全新副本再原子换指针，读者拿快照；敏感词/自动停用关键词/SSRF 名单同样原子发布；`group_ratio_setting` 去别名。**不动** `QuotaPerUnit` 的结算期冻结（钱路，下一轮）。
- **option 解析失败保留旧值**并计数 + SysLog，绝不把钱变量置 0。
- **Redis 超时** 读写 1000ms、拨号 2000ms、`ContextTimeoutEnabled=true`、`MaxRetries=1`、`PoolTimeout` = 读超时 + 1s；启动 ping 仍走 `DBConnectPingTimeout`。**identity gRPC** 去掉 `WaitForReady(true)`，gRPC 腿 2000ms，HTTP 回退用剩余预算，总预算 5s；账号/权益调用挂到既有 breaker。
- **SQL 连接池**在两份 manifest 显式 `SQL_MAX_OPEN_CONNS=20`、`SQL_MAX_IDLE_CONNS=5`（3 副本 × 2 池 × 20 = 120 < 200 且给 platform/zitadel 留 80；PG `max_connections` 真值由 owner 确认后可调）；`GOMEMLIMIT=900MiB`；**不设 GOMAXPROCS**（Go 1.25 已按 cgroup）。
- **入口包**：图标库改异步组件；legacy 静态页面全部 lazy；`/assets/` 免 web 限流并直接服务预压缩文件；`/api/v2` 加 gzip 但**排除三条 CSV 流式导出**（`v2_admin_audit.go:195`、`v2_log_export.go:141`、`v2_log_export_admin.go:106` —— gin gzip 会把流式响应整体缓冲）。
- **死代码删除**：`components/table/{channels,tokens,usage-logs,models}` + `hooks/{channels,tokens,usage-logs}` 整目录删；`components/settings/*` 六个孤儿**不删**（`RatioSetting.jsx` 是全仓唯一的模型/分组倍率编辑器，删了就没人能改价 —— owner 事项 O-ratio）。
- **文件体量棘轮**（Go + JS）由 W 在所有 lane 落地后用当时行数写入，只防增长，本轮不拆大文件（拆分 = 下一轮，前提是先有棘轮）。
- **文件所有权**：并行 lane 互不相交；`internal/adapter/handler/router/*.go`（含测试，**例外：`web-router.go` 及其两个新测试归 L2，`v2_admin_system_tasks_mount_test.go` 归 L4**）、`cmd/server/main.go`、`internal/pkg/config/config.go` 之外的 `deploy/k8s/**`、`web/src/App.jsx`、`web/src/z1_App.test.jsx`、`doc/runbook/INDEX.md`、`.env.example` 由串行 **W** 独占。locale `en.json`/`zh.json`：各 lane 只改自己的子树（L3 `console.admin.*`；L10 只改两个 Token 页缺失的扁平中文键与 `common.changeLanguage`）。`.github/workflows/go-ci.yml` 归 L1，`web-ci.yml` 归 L2。
- 上线流程与第十一轮相同：PR → CI 14 项绿 → merge → auto-pin → ArgoCD → 按 lane 的 UAT 探针实证（生产只读）。

## 3. Lane 结构

| Lane | 主题 | 量 |
|---|---|---|
| L1 | 测试隔离与 CI 闸门：search/governance/handler 的 AsyncGo seam + 结构闸 + 测试卫生 + `-shuffle` + 棘轮 | M |
| L2 | 首屏与传输：图标库异步、`/assets/` 免限流 + 预压缩直出、包体预算闸、chunk 命名 | M |
| L3 | 控制台诚实与前端安全：HTML sink 全部过 DOMPurify、rules-of-hooks、死目录删除、role-10 导航/全绿假象 | L |
| L4 | v2 拒绝形状 403、CSRF 源守卫、会话注册表诚实、TOTP/凭证桶 fail-to-memory | M |
| L5 | v1 管理面多租户闭合：定价目录、发现端点、tag、PUT tenant_id、探测日志归属 | L |
| L6 | 配置热重载安全：copy-on-write、原子名单、去别名、解析失败保旧值、写锁→读锁、结构闸 | M |
| L7 | 出站依赖可靠性：Redis/gRPC/NATS/health/ali/ollama/webhook 超时与降级 | M |
| L8 | 数据库与缓存可靠性：channel cache 失败保旧表、GORM logger、连接池寿命、池饱和告警 | S |
| L9 | Lugo 组织绑定与租户生命周期：org 一致性、claim 键、占位符、座位上限、删租户守卫、投影设计稿 | M |
| L10 | i18n 诚实：只发 zh+en、回落 en、选择器闸、`tr(` 盲区、键回显 | S |
| W | 接线（串行末位）：main.go / manifests / App.jsx / 路由挂载 / 闸门登记 / 体量棘轮 / real-chain 测试 | M |
| 手工 | 运维文档真相：pg-restore 重写、定位文档 §5、文档声明闸（我亲手做） | S |

---

## L1 — 测试隔离与 CI 闸门

**Owned**：`internal/pkg/search/{sync.go, cov_sync_test.go, cov_helpers_test.go}` + 新 `async_seam_test.go`；`internal/app/governance/audit.go` + 新 `async_seam_test.go`；`internal/adapter/handler/{v2_channel.go, relay.go, release.go, tool_version_worker.go, playground.go, ratio_sync.go, async_seam_test.go}` + 新 `async_seam_structural_test.go` + 新 `audit_writer_pin_test.go`；`internal/adapter/handler/router/async_seam_test.go`（加 governance seam 一行）；`internal/adapter/handler/v2_testutil_test.go`、`cover_r2_auth_test.go`、以及 lane 枚举出的 `SetAuditWriter` 调用测试文件（**除 `v2_admin_users_test.go`**，那份归 L4，由 W 收尾转换）；`internal/pkg/common/rate-limit.go` + 新 `rate_limit_stop_test.go`；`internal/adapter/middleware/middleware_cover_test.go`（:371 加 `t.Cleanup(Stop)`）；`.github/workflows/go-ci.yml`；`internal/app/coverage_honesty_test.go`。

**改动**
- search：`var AsyncGo = func(f func()) { asyncPool.Go(f) }`，四处提交（sync.go:77/103/125/147）改走它；`async_seam_test.go` 的 TestMain 置为同步；删 `drainAsyncPool`（cov_sync_test.go:44-57 及 :87 调用，注释里的"哨兵跑完即无 worker 在旧任务里"对 bytedance gopool 是假的）；`newFakeMeiliServer` 注册 `t.Cleanup(srv.Close)`。
- governance：`var AsyncGo = gopool.Go`，audit.go:69 改走它；本包 TestMain 同步；handler 与 router 两个 TestMain 各加 `governance.AsyncGo = func(f func()) { f() }`。
- handler：`v2_channel.go:381/572/645` → `AsyncGo(func() { repo.InitChannelCache() })`；`relay.go:722/729`、`release.go:191`、`tool_version_worker.go:59`、`playground.go:218`、`ratio_sync.go:131` 逐处判定：fire-and-forget 的走 `AsyncGo`；本身有 wg/channel join 的（`model_sync.go:294/299/499/503` 已确认是 join 的）进豁免表并写理由。`channel-test.go:658` 归 L5，L1 只把结论写进 hand-off。
- 结构闸 `TestNoUnseamedSpawnsInHandlerPackage`：AST 遍历 handler 包非测试文件，凡 `go` 语句或 `gopool.Go/CtxGo` 调用不在豁免表即失败；豁免表条目要带理由；扫描到 0 个文件 fail-fast。
- 测试卫生：新 helper `pinAuditWriter(t, db)`（`SetAuditWriter` + `t.Cleanup` 还原），替换 lane 枚举到的全部裸 `SetAuditWriter(&pinnedAuditWriter{…})` 站点（v2_admin_users_test.go 除外）；`v2_testutil_test.go:98-99` 的 `QuotaForNewUser`/`LogConsumeEnabled` 进 cleanup 还原；`cover_r2_auth_test.go:157-162` 写入的四个 OptionMap 键 cleanup 删除。
- `InMemoryRateLimiter` 加 `stop` 通道与 `Stop()`（sync.Once），清理循环 select 上它；生产无人调 Stop。
- CI：go-ci.yml 的 race 步骤加 `-shuffle=on`（种子会打印，红了先拿种子再谈重跑）；coverage 闸 app 86→87、handler 64→73（repo 77 不动：78.8-2 < 77，棘轮只升不降）；lint 上限 666→646 并在测量块加一行出处（run 35447252110）。`coverage_honesty_test.go` 镜像同步。

**Oracle**
- `TestSyncLogsBatchAsync_HonoursSeam`：同步 seam 下 `SyncLogsBatchAsync` 返回即 `len(fm.requests())==1`，不轮询。今天红（sync.go:103 写死 pool）。变异：把 :103 改回 `asyncPool.Go`。
- `TestRecordAuditEvent_HonoursSeam`：计数 writer，调用后立刻 ==1。变异：audit.go 改回 `gopool.Go`。
- `TestNoUnseamedSpawnsInHandlerPackage`：落地前红并**点名全部**裸站点（不是子集），落地后绿。变异：在任一 handler 加一行 `go func(){}()`。
- `TestInMemoryRateLimiter_CleanerStops`：Init → NumGoroutine 记录 → Stop → 1s 内回落。变异：去掉 select 的 stop 分支。
- `TestRaceJobIsShuffled`：解析 go-ci.yml，race 步骤含 `-race` 与 `-shuffle=on`。变异：删 `-shuffle=on`。
- `coverage_honesty_test.go`：只改 yml 一侧即红。
- CI 的 `-race` job 本身（本机无 cgo，CI 是唯一 oracle）。

**Hand-off → W**：`v2_admin_users_test.go` 的 `SetAuditWriter` 站点改用 `pinAuditWriter`；`channel-test.go:658` 的 seam 改动已交 L5。

**UAT**：无行为变化；证据 = PR 上 `-race` job 绿 + 结构闸红→绿的输出贴进 PR。

## L2 — 首屏与静态资产传输

**Owned**：`web/src/helpers/render.jsx`（只做把 `getLobeHubIcon` 搬出去）、新 `web/src/helpers/lobeIcon.jsx`、`web/src/helpers/index.js`、新 `web/src/helpers/h1_lobe_icon_async.test.jsx`、新 `web/src/z9_entry_icon_barrel.test.js`、`web/vite.config.js`、新 `web/scripts/check-bundle-budget.mjs` + `web/scripts/bundle-budget.test.mjs` + `web/bundle-budget.json`、`.github/workflows/web-ci.yml`、`internal/adapter/handler/router/web-router.go` + 新 `web_router_ratelimit_scope_test.go` + 新 `web_router_precompressed_test.go`。

**改动**
- `LobeHubIcon` 组件：模块级 memo 的 `import('@lobehub/icons')`，同步先渲染现有的首字母 `<Avatar>` 回退，加载完替换；`getLobeHubIcon` 签名保留、内部改用组件；render.jsx 不再 import `@lobehub/icons` 任何符号（31 个具名 import 一并搬进 lobeIcon.jsx）。
- `web-router.go`：web 限流只挂在 NoRoute（SPA HTML）路径，`/assets/` 不限；`/assets/<f>` 请求按 `Accept-Encoding` 先找 embed FS 里的 `<f>.br` 再 `<f>.gz`，命中则以 `Content-Encoding` + `Vary: Accept-Encoding` + 原 Content-Type 直出，未命中走原 static.Serve；gzip 中间件对 `/assets/` 跳过（避免双压）。
- `vite.config.js`：`output.chunkFileNames` 当 facade 是 `index` 时用父目录名（`assets/v2-Dashboard-[hash].js`）。
- 预算闸：脚本读 `web/dist/index.html` 取入口与 modulepreload，报 entryChunkBytes / firstPaintJsBytes / largestChunkBytes / totalJsBytes / jsChunkCount，任一超过 `bundle-budget.json` 即失败，低于预算 20% 打印"请下调"；web-ci.yml 的 Vite build job 在 `bun run build` 后跑它。首个预算值 = 本 lane 改完后的实测 + 5%。
- 提交信息必须带实测 before/after：`bun run build` 前后 `wc -c web/dist/assets/index-*.js`（以及 .br 若存在）。

**Oracle**
- `h1_lobe_icon_async.test.jsx`：mock `@lobehub/icons` 为延迟模块，断言同步渲染出回退、`await findByTestId` 出真图标。
- `z9_entry_icon_barrel.test.js`：从 `src/index.jsx`/`src/App.jsx` 走同步 import 图（不跟 `lazy(() => import())`），断言可达集合里没有任何模块 import `@lobehub/icons`。今天红。变异：在 render.jsx 加回一行静态 import。
- `web_router_ratelimit_scope_test.go`：内存限流预算 2/180：`/assets/x.js` 5 次全 200，`/console/v2/dashboard` 第 3 次 429。今天红。变异：把 `router.Use(GlobalWebRateLimit())` 挪回 static.Serve 之上。
- `web_router_precompressed_test.go`：小 embed.FS 含 `x.js/.br/.gz`：`br, gzip` → `Content-Encoding: br` 且字节等于 x.js.br；只 `gzip` → gzip；无 Accept-Encoding → 原文；`Vary` 存在。今天红。
- `bundle-budget.test.mjs`：合成 dist 五格（低于/超过/低 20%/零 js/缺 index）。

**Hand-off → W**：App.jsx:24-37 十个静态页面 + `PersonalSetting/Setup/OidcCallback` 改 `lazy`，`:162-169`、`:185-192` 补 `<Suspense>`（`NotFound/Forbidden/OidcRedirect/SetupCheck` 保持静态）；`api-v2-router.go:23` 后挂 `gzip.Gzip(DefaultCompression, gzip.WithExcludedPaths([三条 CSV 导出路径]))`（gin-contrib/gzip v0.0.6 有该选项；若无则用自写路径判断），并写挂载测试：`GET /api/v2/relays/recommended` 带 `Accept-Encoding: gzip` → `Content-Encoding: gzip`；CSV 导出路径 → 无 `Content-Encoding`。

**Docs**：`deploy/r6-host-nginx/` 头注释加一句：nginx 的 `proxy_set_header Connection "upgrade"` 是小写，gin gzip 只对含大写 `Upgrade` 的请求跳过压缩 —— 改成大写会静默关掉全部压缩。

**UAT**：域名 `curl -sI https://test-newhub.lurus.cn/assets/<entry>.js -H 'Accept-Encoding: br'` 得 `content-encoding: br`；`/api/v2/relays/recommended` 带 gzip 头得 `content-encoding: gzip`；CSV 导出无；直连 30851 连打 `/assets/…` 400 次全 200；入口 chunk 字节数 before/after 写进 PR。

## L3 — 控制台诚实与前端安全

**Owned**：`web/.eslintrc.cjs`、`web/package.json`（只改 `eslint` 脚本）、11 处生产 rules-of-hooks 站点所在文件：`web/src/components/common/DocumentRenderer/index.jsx`、`web/src/components/common/markdown/MarkdownRenderer.jsx`、`web/src/pages/Chat2Link/index.jsx`、`web/src/pages/Setting/Personal/SettingsSidebarModulesUser.jsx`、`web/src/pages/Setting/Ratio/UpstreamRatioSync.jsx`（5 处测试文件里的违规也修：`cx_siderbar_legacy_links.test.jsx`、`q1_system_setting.test.jsx`、`v2/Pricing/index.test.jsx`）；`web/src/helpers/sanitize.js` + 新 `sanitize.test.js`；其余 sink 文件：`web/src/components/layout/Footer.jsx`、`NoticeModal.jsx`、`web/src/helpers/utils.jsx`、`web/src/pages/Home/index.jsx`、`web/src/components/settings/BrandingSettingPage.jsx`、`OtherSetting.jsx`；删除整目录 `web/src/components/table/{channels,tokens,usage-logs,models}`、`web/src/hooks/{channels,tokens,usage-logs}`；新 `web/src/no_orphan_component_dirs.test.js`；`web/src/components/hifi/HFShell.jsx` + `HFShell.test.jsx`；`web/src/pages/v2/Admin/{Gateway,CostIntelligence,Settings}/index.jsx` + 各自测试；`web/src/pages/v2/Analytics/Rankings.jsx`（只改 :83-85 假注释）；`web/src/helpers/auth.jsx`（加 `RootRoute`）；`en.json`/`zh.json` 的 `console.admin.*` 子树。

**改动**
- eslint：`'react-hooks/rules-of-hooks': 'error'`、`'react-hooks/exhaustive-deps': 'warn'`，`eslint` 脚本加 `--max-warnings=<当前 exhaustive-deps 实测数>`（棘轮，只降）；修 11 处生产违规（DocumentRenderer :218 的 useEffect 提到 early return 之前，用条件在 effect 内部判断）。
- sanitize：11 处 `dangerouslySetInnerHTML` 全部改为 `sanitizeHtml(...)` 后再注入（CodeViewer 已 escape 的保持）；`DocumentRenderer` 自带的假 `sanitizeHtml`（:51）删掉改用 helpers 的；`sanitize.test.js` 表驱动：`<img onerror>`、`<script>`、`javascript:` href、`<iframe>` 全被剥，普通 `<b>`/链接保留。
- 死目录删除 + 闸门 `no_orphan_component_dirs.test.js`：遍历 `web/src/components/*/` 与 `web/src/hooks/*/`，凡目录外无非测试 importer 即失败（白名单空）。
- HFShell：`:333, :341, :361, :411, :420, :429, :447` 七项加 `minRole: 100`（`:439 admin-rankings` 有租户回退，保留）；`:235-244` 的 legacy bridge 注释改成只说"系统访问令牌仍在 legacy"。
- Gateway / CostIntelligence / Admin Settings：非 403 失败渲染错误态（重试按钮 + 文案），绝不渲染全绿或全关；Settings 的开关在 options 未加载时显示未知态而非关。新 key 进 `console.admin.*`。
- `no_fabricated_status.test.js` 头注释补一句本轮加的第三类盲区说明（不改规则）。

**Oracle**
- `bunx eslint src --ext .js,.jsx`：改 config 前注入 `--rule` 有 16 错；改后 0 错且 warning 数等于脚本上限。变异：把任一 hook 包回 `if` 里。
- `DocumentRenderer` 测试：先渲染 loading 再喂 HTML 内容不抛 "Rendered more hooks"。今天红。
- `sanitize.test.js` + 各 sink 页测试：注入 `<img src=x onerror=alert(1)>` 后 DOM 无 `onerror`。今天红（MarkdownRenderer :230 原样注入）。变异：去掉任一 sink 的 `sanitizeHtml`。
- `no_orphan_component_dirs.test.js`：删目录前红并点名四个目录；变异：加回一个无 importer 的目录。
- `HFShell.test.jsx`：role 10 渲染的 rail 不含六个根管理项；变异：删任一 `minRole:100`。
- Gateway/Cost/Settings 测试：mock 502 → 错误态可见、无 "no open breakers"/无全关开关。今天红。变异：把 catch 改回只认 403。
- `web/src/pages/v2/no_fabricated_status.test.js`、`frontend_route_contract.test.jsx`、`i18n-integrity.test.js` 必须仍绿。

**Hand-off → W**：App.jsx：`/console/chat/:id?` 加 `<PrivateRoute>`；`/console/setting`、`/console/openrouter-sync` 换 `<RootRoute>`；`/console/v2/admin/*` slug 表按 HFShell 的 minRole 加守卫（audit/rankings 保持 role 10）；`z1_App.test.jsx` 三张表对应调整；`router/console_consumes_endpoints_test.go:66` 的 `GET /api/log/self` 行（唯一消费者是被删的 usage-logs 表格）移到 `noConsoleConsumer` 并写理由（v1 API 客户端保留端点）。

**UAT**：bridge 登 role-10（user 3）看 rail 无根管理项；直接打开 `/console/v2/admin/gateway` 落 forbidden；Chat 里让模型输出 `<img src=x onerror=alert(1)>` 的 html 代码块，DOM 里无 `onerror`；`/privacy-policy`、`/user-agreement` 正常渲染。

## L4 — v2 拒绝形状、CSRF 源守卫、会话注册表诚实、凭证桶 fail-to-memory

**Owned**：`internal/adapter/middleware/admin_jwt_auth.go` + 其既有测试文件、新 `internal/adapter/middleware/browser_origin_guard.go` + `_test.go`、`internal/adapter/handler/router/v2_admin_system_tasks_mount_test.go`、`internal/adapter/handler/v2_admin_users.go` + `v2_admin_users_test.go`、`internal/adapter/handler/v2_session_revoke.go` + `_test.go`、`internal/adapter/handler/zita_logout.go`、新 `internal/adapter/middleware/session_cookie_options_test.go`、新 `internal/adapter/handler/session_clear_options_test.go`、`internal/app/totp/totp.go` + 新 `throttle_backend_error_test.go`、`internal/adapter/middleware/rate-limit.go` + 新 `rate_limit_failclosed_marks_test.go`、`internal/pkg/metrics/r6_rate_limit_degraded.go`、`doc/runbook/rate-limit-degraded.md`、`doc/runbook/incident-response.md`（SESSION_REGISTRY 段）、`internal/adapter/handler/secure_verification.go`（只改 :43-47 假注释）。

**改动**
- `RootJWTAuth`：无 Authorization 时不再调 `authHelper`，自己读会话：未登录 401 `UNAUTHENTICATED`、role < 100 → **403** `{success:false, error_code:"PERMISSION_DENIED", message:"platform admin role required"}`；JWT 分支不变。`authHelper` 一字不动（v1 形态）。
- `BrowserOriginGuard()`：按 §2 的规则；只在非 GET/HEAD/OPTIONS 生效；带 `Authorization` 或 `X-API-Key` 直接跳过；拒绝时 403 `CROSS_SITE_REQUEST` 并计 `lurus_gateway_csrf_rejected_total{reason}`。
- 会话注册表：`v2_admin_users.go:323-329` 与 `v2_session_revoke.go:228-235` 在 flag 关时回 **409** `SESSION_REGISTRY_DISABLED`，不再 200 `revoked:0`；runbook 写清生产开关位置。
- TOTP：`AllowAttempt` 只把 `redis.Nil` 当"无失败"，其它错误落到内存计数；`RecordFailure` 同样在 Redis 错误时喂内存计数。
- 限流降级按 mark 分策：新 `rateLimitMemoryFallbackMarks`（凭证/滥用桶：兑换码、bootstrap、TOTP 备份码、内部 key 的 IP 桶等，lane 枚举 `rateLimitFactory` 的全部 mark 并逐一分类），这些桶在 Redis 错误时改走进程内内存限流器（真实的按副本上限），其它桶维持放行；两条路径都计 `rate_limit_degraded_total` 并分 label。
- 两处裸 cookie 清除改 `middleware.SessionClearOptions()`；`secure_verification.go:43-47` 注释改为事实（enrol/confirm 不在 step-up 闸后，账号不会锁死）。

**Oracle**
- 真链路测试（W 写挂载测试，本 lane 写中间件级）：role-10 会话 `GET /api/v2/admin/gateway/health` → 403 + error_code；未登录 → 401。`v2_admin_system_tasks_mount_test.go:113-127` 改成 403 钉子。变异：把 403 改回 200。
- `browser_origin_guard_test.go`：`Sec-Fetch-Site: same-site` + cookie + POST → 403；`same-origin` → 通过；带 `Authorization` 的 cross-site → 通过；无任何头 → 通过；`Origin` 不在白名单 → 403。变异：删 `same-site` 分支。
- `TestAdminRevokeUserSessions_FlagOff` 改为 409 且 `revoked_at` 不变。今天红。
- `throttle_backend_error_test.go`：死 Redis 客户端 → `RecordFailure`×6 → `AllowAttempt` false。今天红。
- `rate_limit_failclosed_marks_test.go`：死 Redis，`rateLimitFactory(1,60,"RD")` 第 2 次 429；`"GA"` 两次都 200。今天红。变异：把 "RD" 移出集合。
- `session_clear_options_test.go`：结构扫描 `session.Options(` 站点不得出现复合字面量。

**Hand-off → W**：`api-v2-router.go` 在 CORS 之后、鉴权之前挂 `BrowserOriginGuard()`；`api-router.go` 的 `/api` 组同样挂；新 `router/csrf_origin_guard_mount_test.go`（真挂载：cookie + same-site POST `/api/v2/admin/tenants` 403；same-origin 不 403；`POST /api/v2/bridge/exchange` 无 cookie 不受影响）；`.env.example` 加 `SESSION_REGISTRY_ENABLED` 说明。

**Docs**：根 `doc/coord/contracts.md` 记 v2 admin 拒绝形态变化（消费者只有控制台；switch/lutu 零调用已 grep）—— owner 收尾。

**UAT**：role-10 bridge 会话 `GET /api/v2/admin/gateway/health` → 403；`curl -X POST` 带 cookie + `Sec-Fetch-Site: cross-site` → 403，`same-origin` → 非 403；bridge 登录 + 夜跑 e2e 保持绿；`POST /api/v2/admin/users/:id/sessions/revoke`（UAT flag 开）仍 200，改临时关 flag 的探针不做（manifest 改动=部署）。

## L5 — v1 管理面多租户闭合

**Owned**：`internal/domain/entity/ability.go`、`internal/adapter/repo/{ability.go, pricing.go, missing_models.go, channel.go}`、`internal/adapter/handler/{pricing.go, switch_pricing.go, v2_pricing.go, user.go, model.go, missing_models.go, channel.go, channel-test.go, v1_cross_tenant_idor_test.go}` + 新 `pricing_tenant_scope_test.go` + 新 `channel_tenant_id_immutable_test.go` + 新 `channel_test_log_attribution_test.go`。

**改动**
- 定价目录按租户投影：`AbilityWithChannel` 加 `ChannelTenantId`（`channels.tenant_id`），`updatePricing` 建目录时带上；公开 `/api/pricing` 与 `/api/v2/switch/pricing` 只列 `tenant_id IN ('default','')` 渠道的模型；已登录调用者 = 共享 ∪ 本租户；`switch_pricing.go:67-72` 的 group_ratio 像 v1（`pricing.go:33-39`）一样只回可用分组。JSON 形状字节不变。
- `GET /api/user/models`（`user.go:323-348`）、`/api/channel/models_enabled`、`/api/models/missing` 对非 root 走新的 `*ForTenant` repo 函数（镜像 `ability.go:49 GetGroupEnabledModelsForTenant`）。
- `GET /api/channel/tag/models`（`channel.go:1530-1568`）非 root 走 `GetChannelsByTagAndTenant`；tag 模式分页 `channel.go:119,150` 非 root 走按租户的 tags/count。
- **`PUT /api/channel/`**：非 root 请求体的 `tenant_id` 一律被 `originChannel.TenantId` 覆盖（root 才能改归属），并对试图改的请求计审计事件；`channel_tenant_id_immutable_test.go` 钉。
- 探测日志归属：`channelProbeOptions` 加 `ActorUserID`/`TenantID`，手工 `/test/:id` 的消费日志记到发起人与其租户（不再 user 1 / default）；自动探活不写日志（cycle 11）不变；`channel-test.go:658` 的 `gopool.Go` 改走 `AsyncGo`（L1 hand-off）。

**Oracle**
- `pricing_tenant_scope_test.go`：租户 acme 私有模型 `acme-private-ft` 不出现在匿名 `/api/pricing`，出现在 acme 会话的响应；`switch/pricing` 的 group_ratio 不含 acme 专属分组。今天红。变异：去掉投影过滤。
- `TestV1UserModels_ListTenantScoped`、`TestV1ModelsEnabled_ListTenantScoped`、`TestV1MissingModels_ListTenantScoped`、`TestV1ChannelTagModels_ListTenantScoped`、tag 模式分页 case（`total==1`）。今天红。变异：改回无租户 repo 函数。
- `channel_tenant_id_immutable_test.go`：role 10 PUT 带 `tenant_id:"other"` → 存储行 tenant 不变；root 可改。今天红。
- `channel_test_log_attribution_test.go`：role-10 acme 管理员 `/test/:id` 后 logs 行 `user_id`=本人、`tenant_id`=acme。今天红。

**Hand-off → W**：`router/idor_completeness_test.go`：`GET /api/user/models` 从 `listExempt` 移到 `listScoped`，`/api/channel/models` 的豁免理由改成事实（`model.go:344` 返回编译期目录，不查库）；新 `router/v1_model_discovery_tenant_scope_test.go` 真链路（tenant A 看不到 B）。

**Docs**：根 `doc/coord/contracts.md`：`/api/v2/switch/pricing` 与 `/api/pricing` 收窄为共享渠道目录（switch 消费者 note）—— owner 收尾。

**UAT**：匿名 `GET /api/pricing` 不含 UAT 私有租户模型（先种一条 tenant=probe 的渠道）；role-10 `PUT /api/channel/` 带 `tenant_id` 改不动；role-10 `GET /api/channel/tag/models?tag=<他租户 tag>` 得空。用完删种的渠道。

## L6 — 配置热重载安全

**Owned**：`internal/pkg/setting/config/config.go` + 新 `config_cow_test.go`、`internal/pkg/setting/model_setting/{gemini.go, claude.go, global.go}`、`internal/pkg/setting/sensitive.go`、`internal/pkg/setting/operation_setting/operation_setting.go`、`internal/pkg/setting/system_setting/fetch_setting.go`、`internal/pkg/setting/ratio_setting/group_ratio.go`、`internal/adapter/repo/option.go`、`internal/adapter/handler/option.go`、`internal/adapter/handler/setup.go`、`internal/app/sensitive.go`、`internal/app/channel.go`（只改关键词读法）、新 `internal/pkg/metrics/option_reload.go`、新测试 `internal/adapter/repo/{option_race_test.go, option_group_ratio_alias_test.go, option_owned_globals_gate_test.go, option_parse_keep_previous_test.go}`、`internal/app/{sensitive_race_test.go, ssrf_guard_race_test.go}`。

**改动**
- `config.go`：注册对象改为 `atomic.Pointer[T]` 形态（`Register` 接受当前指针 + 提交函数）；`updateConfigFromMap` 解码进**深拷贝**（map/slice 显式克隆）再一次性 `Store`；`UpdateConfigFromMap` 返回真实错误（今天 :254 无条件 nil）且 `repo/option.go:666` 不再丢弃；各 `GetXxxSettings()` 返回快照指针，调用方名称不变。
- `sensitive.go`、`operation_setting.go`：名单改 `atomic.Pointer[[]string]`，`SensitiveWords()`/`AutomaticDisableKeywords()` 访问器，`…FromString` 本地建好再 Store（永不发布半成品）；`app/sensitive.go`、`app/channel.go` 改读访问器。
- `fetch_setting.go`：`GetFetchSetting()` 返回不可变快照（随 config.go 的 COW 免费获得，但要保证发布后切片不再被改）。
- `group_ratio.go`：去掉注册 struct 对 `GroupRatio/GroupGroupRatio` 的别名，只保留既有 option 键写路径；`group_ratio_setting.group_ratio` 键改成拒绝 + 日志。
- `repo/option.go` 18 处 `_ = strconv.Parse*`：解析失败**保留旧值**、`SysError` 带键名、`lurus_gateway_option_parse_rejected_total{key}` +1；`handler/option.go:22` 改 `RLock`；`setup.go` 两处直接赋值 option 变量改走 option 写路径。
- 结构闸 `option_owned_globals_gate_test.go`：从 `option.go` 的 `updateOptionMap` 自动派生"option 拥有的全局变量集"（<80 个 fail-fast），扫描非测试 Go 文件，凡在 loader/init 之外对这些变量赋值即失败。

**Oracle**
- `config_cow_test.go` / `option_race_test.go`：goroutine A 循环 `repo.SetOptionMapValue("gemini.safety_settings", …)`，goroutine B 循环读 `model_setting.GetGeminiSafetySetting(key)`，500ms 内不 panic 且每次读到完整 map（本机无 -race 也能证：改活 map 会 `fatal error: concurrent map read and map write`）。今天红。变异：Store 改回就地写。
- `sensitive_race_test.go`：A 循环 `SensitiveWordsFromString("alpha\nbravo")`，B 循环 `CheckSensitiveText("… alpha …")` 断言恒为 true。今天红（能读到空表）。
- `ssrf_guard_race_test.go`：A 交替写 domain_list，B 断言 `evil.invalid` 恒被拒。
- `option_group_ratio_alias_test.go`：先写 `GroupRatio` 再写 `group_ratio_setting.group_ratio`，断言前者生效且后者被拒。今天红。
- `option_parse_keep_previous_test.go`：`QuotaPerUnit=500000` 后写入 `"abc"` → 仍 500000 且计数 +1。今天红（变成 0）。变异：改回 `_ =`。
- `option_owned_globals_gate_test.go`：落地前红（setup.go 两处）；变异：在 relay.go 加 `common.RetryTimes = 1`。
- 既有 `ratio_setting` 互斥锁不动（已正确）。

**Hand-off → W**：无路由改动；`.env.example` 无新 env。

**UAT**：`PUT /api/option {"key":"QuotaPerUnit","value":"abc"}` 后 `/api/status` 里的单位价不变且 `/metrics` `option_parse_rejected_total{key="QuotaPerUnit"}`=1；恢复原值；连打 60 次 `PUT /api/option` gemini 安全设置同时跑 200 次 relay（faultsim），pod 零重启。

## L7 — 出站依赖可靠性

**Owned**：`internal/pkg/common/{redis.go, identity_grpc_client.go, billing_breaker.go}` + 新 `redis_deadline_test.go`、`identity_timeout_test.go`；`internal/pkg/nats/{publisher.go, events.go}` + 新 `publisher_ctx_test.go`；`internal/adapter/handler/health.go` + 新 `health_deadline_test.go`；`internal/adapter/provider/ali/image.go` + 新 `image_timeout_test.go`；`internal/adapter/provider/ollama/relay-ollama.go`；`internal/app/{webhook.go, user_notify.go}` + 新 `webhook_timeout_test.go`；`internal/app/quota.go`（只改 :1427 的 `context.TODO()`）；`internal/pkg/metrics/metrics.go`（加 `nats_connected` gauge）；`internal/pkg/config/config.go`（`ServerConfig` 加 `ReadHeaderTimeout`/`IdleTimeout`，env `HTTP_READ_HEADER_TIMEOUT` 默认 10s、`HTTP_IDLE_TIMEOUT` 默认 120s）。

**改动**：按 §2 的数值：Redis 选项（env `REDIS_OP_TIMEOUT_MS`、`REDIS_DIAL_TIMEOUT_MS` 可调）；gRPC 去 `WaitForReady`、`IDENTITY_GRPC_TIMEOUT_MS` 默认 2000、HTTP 回退用剩余预算、账号/权益调用挂 breaker；NATS `PublishMsg(..., natsgo.Context(ctx))`、`MaxReconnects(-1)` + `RetryOnFailedConnect(true)` + 断连/重连回调驱动 `nats_connected`；`health.go:46-48` 的 2s 上限在 ContextTimeoutEnabled 后成真，注释改成实测；ali `image.go:193-206` 与 ollama `:292/:459` 改 `http.NewRequestWithContext` + 带超时的共享 client；webhook 走 ctx + 10s 总预算 + 读 deadline 包装；`quota.go:1427` `context.TODO()` → `WithTimeout(Background, 10s)`。**不改** `ReadTimeout/WriteTimeout`（会切断 SSE 与大上传），注释写明。

**Oracle**
- `redis_deadline_test.go`：accept 但永不应答的 TCP 监听 → `RDB.Get` 带 200ms ctx 在 500ms 内返回。今天红（3s）。变异：`ContextTimeoutEnabled=false`。
- `identity_timeout_test.go`：gRPC 指向已关闭端口 + HTTP 回退指向永不响应的 httptest → `UpsertAccountGRPC(context.Background())` 3s 内返回。今天红（约 10s）。**注意**复核者指出侦察给的原 oracle 写法是坏的：断言必须落在总耗时与错误类型上，不能只断言 gRPC 腿。
- `publisher_ctx_test.go`：阻塞 3s 的 stub，200ms ctx → 500ms 内返回 deadline 错误。今天红。
- `health_deadline_test.go`：挂起 Redis → `GetHealthDetailed` 2.5s 内返回、`checks.redis=="unreachable"`、HTTP 200。今天红。
- `image_timeout_test.go` / `webhook_timeout_test.go`：挂起上游 + 200ms ctx → 1s 内返回错误。今天红。

**Hand-off → W**：`cmd/server/main.go:407` 的 `http.Server` 读 `config.Get().Server.ReadHeaderTimeout/IdleTimeout`，并抽 `buildHTTPServer(port, handler)` 供 W 的 `http_server_timeouts_test.go` 断言 `ReadHeaderTimeout>0, IdleTimeout>0, ReadTimeout==0, WriteTimeout==0`；`.env.example` 加六个新 env。

**Docs**：`doc/runbook/rate-limit-degraded.md`（L4 拥有）之外，新 `doc/runbook/platform-dependency-degraded.md`（L7 拥有）：platform-core 挂时 relay/控制台各自的表现与时间界。

**UAT**：`/api/health` 在 UAT 正常 <100ms；`/metrics` 出现 `nats_connected`（UAT NATS 关，值应为 0 且不影响 relay）；faultsim relay 20 次全 200（证明 gRPC/HTTP 回退改动没伤正常路径；UAT billing-unified 关）。

## L8 — 数据库与缓存可靠性

**Owned**：`internal/adapter/repo/{channel_cache.go, channel_cache_resilience_test.go, main.go}` + 新 `gorm_logger.go` + `gorm_logger_test.go`；新 `internal/pkg/metrics/db_observability.go`；`deploy/r6-host-netdata/health.d/newhub.conf` + `README.md`；新 `doc/runbook/db-pool-saturation.md`。

**改动**
- `InitChannelCache`：两次 `DB.Find` 的错误不再丢：任一失败 → 保留现有缓存、`SysError`、`lurus_gateway_channel_cache_sync_failed_total{query}` +1、返回错误；成功路径不变。
- GORM logger 显式构造：`Colorful:false`、`IgnoreRecordNotFoundError:true`、`ParameterizedQueries:true`、`SlowThreshold` 来自 `DB_SLOW_QUERY_MS`（默认 200）、writer 走 slog JSON；`lurus_gateway_db_slow_query_total{db}` 计数。
- 连接池：`SQL_MAX_LIFETIME` 默认 60 → 1800 秒，新增 `SetConnMaxIdleTime`（`SQL_MAX_IDLE_TIME` 默认 300）；open/idle 仍由 env 决定（manifest 由 W 设 20/5）。
- netdata：`newhub_db_pool_saturating` 告警块（基于既有 `go_sql_wait_count` 类 series，名字以 `declared_series_written_test` 能证明有写者为准），头部 STATUS 注明"仓内新增、未安装"（O2）。

**Oracle**
- `channel_cache_resilience_test.go` 扩：把 `DB` 换成已关闭连接 → 调用后旧缓存仍在、计数 +1。今天红（缓存被清空）。变异：去掉错误检查。
- `gorm_logger_test.go`：构造出的 `logger.Config` 四项断言；驱动一次 `First()` 未命中 → writer 无 ERROR 行；一条慢查询（阈值 0）→ 计数 +1 且日志行不含绑定参数值。今天红。
- `TestSQLPoolDefaults`：`SQL_MAX_LIFETIME` 未设时 ≥ 1800s、`ConnMaxIdleTime` 已设。今天红。
- netdata 三个闸（`netdata_alarm_series_test`、`declared_series_written_test`、`alert_wiring_honesty_test`）必须仍绿。

**Hand-off → W**：两份 manifest 加 `SQL_MAX_OPEN_CONNS: "20"`、`SQL_MAX_IDLE_CONNS: "5"`、`GOMEMLIMIT: "900MiB"`；`deploy/k8s/deploy_consistency_test.go` 新 case：声明 `replicas>1` 的 manifest 必须显式设 `SQL_MAX_OPEN_CONNS`，且 `replicas × 2 × SQL_MAX_OPEN_CONNS ≤ 150`（常量出处写注释）、设了 memory limit 必须有 `GOMEMLIMIT`；`.env.example` 加四个 env；`doc/runbook/INDEX.md` 加行。

**UAT**：部署后 pod 日志无 ANSI 色码、无 `record not found` 的 ERROR 行；`/metrics` 出现 `db_slow_query_total`、`channel_cache_sync_failed_total`=0；`kubectl get pod -o jsonpath` 看到三个新 env。

## L9 — Lugo 组织绑定与租户生命周期

**Owned**：`internal/adapter/handler/{oauth.go, tenant.go, zita_bootstrap.go}` + 新 `oidc_callback_org_binding_test.go`、`oidc_claim_key_test.go`、`oauth_org_param_test.go`、`zita_bootstrap_seat_cap_test.go`；`internal/adapter/middleware/{oidc_auth.go, auth.go}` + 新 `tenant_gate_test.go`；`internal/adapter/repo/tenant.go`（加 `TenantGate` 帮助函数）；新 `internal/pkg/metrics/tenant_gate.go`；新 `_bmad-output/planning-artifacts/lugo-org-projection-design-2026-09-19.md`。

**改动**
- OIDC 回调（`oauth.go:326-348`）：`tenant.IDPOrgID` 与 `claims.OrgID` 都非空且不等 → 403 `TENANT_ORG_MISMATCH` + 审计事件；任一为空维持现状。
- `OIDC_CLAIM_ORG_ID` 覆盖：把 `middleware.resolveOIDCClaims` 的原始 claims 覆盖逻辑抽成共享 helper，`validateIDToken` 也用它（一处解析器，`oauth.go:97-100` 的注释变成真的）。
- 占位符守卫：`buildOIDCAuthURL` 对以 `_PLACEHOLDER` 结尾的 org id 不发 `organization=` 参数并 SysLog 一次；`CreateTenant` 拒绝 `_PLACEHOLDER` 结尾的 `zitadel_org_id`（400）。
- 桥接路径座位上限：`autoCreateBridgedUser` 前跑 `repo.TenantCanAddUser(tenantID)`，超限 → 403 `TENANT_SEAT_LIMIT` 并审计。
- 删除/缺失租户守卫：`repo.TenantGate(tenantID) (ok bool, reason string)`：id 为空 → ok（legacy 行）；行存在且 IsDisabled → 拒；行不存在（含软删）→ 按 `TENANT_MISSING_MODE`（默认 `observe`：ok + `lurus_gateway_tenant_gate_total{outcome="missing_observed"}` +1 + SysLog；`enforce`：拒 403 `TENANT_DISABLED`）。`auth.go:355/681/894` 三处改调它。
- 设计稿：投影模型（`platform_org_id` 可空唯一列、刷新方式、name/status 投影、slug 永不投影、多组织歧义回落 default）、platform 端点契约原文、风险表、下一轮 lane 草案。

**Oracle**
- `oidc_callback_org_binding_test.go`（复用 `oidc_callback_link_test.go:150-222` 的 harness）：租户 orgb（IDPOrgID org-B）的登录 URL + org-A 的 ID token → 403 且不建用户。今天红。变异：删比较。
- `oidc_claim_key_test.go`：`OIDC_CLAIM_ORG_ID=urn:example:org:id` 的 ID token → `claims.OrgID=="org-X"`。今天红。
- `oauth_org_param_test.go`：占位符无 `organization=`；`org-123` 有。今天红。
- `zita_bootstrap_seat_cap_test.go`：`max_users=1` 的租户，第二个邀请码首登 → 403。今天红。
- `tenant_gate_test.go`：软删租户 observe 模式 → 放行且计数；enforce 模式 → 403；禁用租户两种模式都 403；空 id 放行。今天红。变异：`enforce` 分支改放行。

**Hand-off → W**：`.env.example` 加 `TENANT_MISSING_MODE`；两份 manifest 不设（默认 observe）。

**Docs**：设计稿 + 根 `doc/coord/contracts.md` 预登记 platform 端点需求（owner）。

**UAT**：软删一个 scratch 租户后其 token 的 relay 仍 200（observe）且 `/metrics` `tenant_gate_total{outcome="missing_observed"}` ≥1；`POST /api/v2/admin/tenants` 带 `zitadel_org_id: "X_PLACEHOLDER"` → 400；用完删 scratch 租户与 token。

## L10 — i18n 诚实

**Owned**：`web/src/i18n/i18n.js`、新 `web/src/i18n/locale-coverage.test.js`、`web/src/i18n/i18n-integrity.test.js`、`web/src/i18n/locales/{en,zh}.json`（只加两个 Token 页扁平中文键：`删除 {{n}} 个令牌?`、`选中令牌的密钥将立即失效`，并把 `common.changeLanguage` 的 zh 值改成「切换语言」）、`web/src/i18n/locales/ru.json`（只改 `common.changeLanguage`）、`web/src/components/layout/headerbar/LanguageSelector.jsx` + `cx_headerbar_parts.test.jsx`、`web/src/components/layout/PageLayout.jsx`、`web/src/index.jsx`（Semi 语言包只留 zh_CN/en_GB）、`web/i18next.config.js`。

**改动**：`supportedLngs: ['zh','en']`、`fallbackLng: { 'zh-*': ['zh'], default: ['en'] }`（i18next 的 fallbackLng 对象形态）、`nonExplicitSupportedLngs: true`；删 fr/ja/ru/vi 的 import 与 resources（**文件保留**）；选择器与 PageLayout 下拉只列 zh/en；`i18n-integrity.test.js:79,87` 的正则加 `tr(` 别名并注释 46 个别名文件；`:366-373` 四语种棘轮改为"不发布语种不参与"；键回显不变量：任何已发布包里 `value === key` 且 key 像点分标识符即失败。

**Oracle**
- `locale-coverage.test.js`：(a) 从 JSON 计算各语种对 en 的叶键覆盖率；(b) 断言 `i18n.js` 注册的语种集合 == 覆盖率 ≥ 95% 的集合 ∪ {zh}；(c) 断言选择器组件源码里的语种集合 == 同一集合；(d) `changeLanguage('ja')` 后 `resolvedLanguage==='en'`。今天红。变异：在 i18n.js 加回 `fr`。
- 整合闸新 case：`tr('删除 {{n}} 个令牌?')` 在 en.json 可解析。今天红（`Token/index.jsx:1660,1662`）。
- 键回显 case：今天红（`common.changeLanguage` 在 zh/ru 回显）。
- `cx_headerbar_parts.test.jsx` 更新。

**Hand-off → W**：无。

**UAT**：`curl -s https://test-newhub.lurus.cn/ -H 'Accept-Language: ja'` 下发的 SPA 在浏览器解析到 en（用 Playwright 的 `locale: 'ja-JP'` 跑登录页断言英文文案，或 API 级看 bundle 不含 `Changer de langue`）；入口 chunk 不含 `Сменить язык`。

## W — 接线（串行，全部 lane 验收后）

顺序：L7 config → main.go（`buildHTTPServer` + 超时）→ manifests（SQL 池 / GOMEMLIMIT）+ `deploy_consistency_test.go` 新 case → L2 App.jsx lazy + `api-v2-router.go` gzip 挂载（排除 CSV）+ 挂载测试 → L4 源守卫两处挂载 + `csrf_origin_guard_mount_test.go` → L3 App.jsx 守卫（PrivateRoute/RootRoute/admin slug）+ `z1_App.test.jsx` + `console_consumes_endpoints_test.go` 行 → L5 IDOR 闸门条目 + `v1_model_discovery_tenant_scope_test.go` → L1 的 `v2_admin_users_test.go` audit writer 站点 → `.env.example` 全部新 env → `doc/runbook/INDEX.md` 行（L7/L8 两份 runbook）→ **体量棘轮**：`internal/pkg/gates/source_size_ratchet_test.go`（非测试 Go 文件 >800 行者的逐文件上限表，用当时行数；扫描 0 文件 fail-fast；变异：给表内文件 +50 行）与 `web/src/file_size_ratchet.test.js`（同规则，js/jsx；删除后剩余的 >800 行文件）→ 全量闸门。并行期已知必红、W 之后才绿：`console_consumes_endpoints_test.go`（L3 删表格后 `/api/log/self` 无消费者）、L1 的结构闸（等 L5 改 `channel-test.go:658`）、`bun run build`（App.jsx 仍静态 import 被 L3 改动过的页面时若签名变了）—— lane 内禁止"修"它们。

## 4. 执行协议

1. 本文件落 `_bmad-output/planning-artifacts/cycle12-industrial-grade-2026-09-19.md`，独立 commit。
2. dev workflow：10 个 lane 并行（每 lane 一个开发 agent：只写自己 Owned 的文件、可读任何文件、禁 git 状态变更/禁 prod/禁 ssh、新文件禁厂商模型名与工具名、每个 oracle 先红后绿并把红/绿命令与输出写进返回值、写完跑自己的测试集与 `go vet`/eslint/prettier；外来编译失败或红测 = 别的 lane 在改，等 60 秒重试最多 5 次），每 lane 一个对抗验收 agent（含 "auditor missed" 槽位；验收可做变异但必须字节级还原并附 sha256）。
3. 我读验收结果、写 operator 决定，开修复轮（只读自己 `## Lx` 段 + 决定），每 lane 验收通过即由我逐路径 stage 该 lane 文件做 WIP commit（`git status` 先看 staged；五个外来 `coverage_*` 永不碰）。
4. W lane 串行落地 hand-off + 自有测试 + 棘轮；我亲手收尾（散文、绝对词、每条接线变异自检）；运维文档三件我亲手做。
5. 本机闸门（lint 与全量测试不并跑）：`go vet ./... && go build ./...`；`go test -short -count=1 -p 2 ./...`；结构闸门集 `-run`；`golangci-lint run --new-from-rev=origin/main ./internal/... ./cmd/...`（改到的行必须把该行的存量 lint 也清掉）；`cd web && bun run lint && bunx eslint src --ext .js,.jsx && bun run test && bun run build`；`-race` 与覆盖率棘轮只在 CI。
6. 根 repo `doc/coord/{changelog,contracts}.md` 各加一条（switch/pricing 收窄、v2 admin 403、platform 端点需求）—— 两文件有别人的 WIP，只加不提交，owner 收尾。
7. PR #188 → CI 14 项绿 → merge → auto-pin → ArgoCD（prod 3/3 + UAT 1/1 同 digest）→ 按各 lane 的 UAT 探针逐条实证（生产只读；UAT 种的租户/渠道/token 用完删）→ 报告。

## 5. 验证要点（诚实闸）

- 每个"能用"必须用 UI 自己发出的请求体或真实 HTTP 往返证明；每个闸门自身做变异；否定式断言先枚举所有拼法。
- 指标类改动（入口 chunk、覆盖率闸、lint 上限、连接池）commit 必附 measured before/after。
- UAT 证不了的（Redis 挂起、platform 挂、NATS 断连、`-race`、CSRF 在真实浏览器矩阵）明写"CI/单测证明，未在 UAT 触发"。
- 每条被侦察标为 DOWNGRADED/REFUTED 的项都不进 lane；进了 lane 的每条我都亲手对 HEAD 看过 file:line（§1.2）。

## 6. do-not-regress（本轮碰到的现役行为）

- v1 `authHelper` 的 200 `{success:false}` 形态（switch 消费）；`GET /api/channel/test/:id` 仍 AdminAuth（switch）；`/api/v2/switch/*`、`/api/v2/relays/*`、`/api/v2/lutu/*` 的响应形状；`POST /api/v2/bridge/exchange` 无 cookie 路径；OIDC 回调空 org 的既有放行；`default` 租户的状态豁免（`auth.go` 三处）；`ratio_setting` 既有互斥锁；`middleware.Cache()` 对 `/` 的 no-cache；`ReadTimeout/WriteTimeout` 保持 0；cycle 11 的探针免限流与 fail-open 方向（凭证桶改内存限流是加严不是回退）；faultsim/e2e bridge 在生产不存在。

## 7. 不做（记录，下一轮候选）

- `QuotaPerUnit`/汇率在预消费期冻结（钱路）；v1 全局目录写路由（models/vendors/deployments）补审计 —— 更好的做法是退役这些 v1 门；组织投影本体（等 platform 端点）；兑换/结算等服务端中文错误改 error code + `Accept-Language` 协商；`logs` 保留任务（`LOG_RETENTION_DAYS`，owner 定窗口）与租户索引 migration 039（先在根 ledger 预留）；faultsim `ok` 模式 + 容量基线脚本与 runbook；SLI gauge 给 netdata；大文件拆分（channel.go/quota.go/Settings/Log/Token；`components/settings` 六孤儿的去留看 O-ratio）；`semi.css` 全量样式；`DrainAsync`；`SECURE_VERIFICATION_REQUIRE_ENROLLMENT` 默认翻转（先 O1）；`TestV2IDOR_ListScopeCompleteness`（≈20 条 v2 列表路由逐条分类）；`/console/user` 的 v2 租户用户管理；`users.tenant_id` 的迁移端点；GET→POST 改 `/api/channel/test|update_balance`（switch 契约）。

## 8. Owner 事项（git 改不到）

- O1 生产 root 注册 TOTP（`user_totps` 仍空）。
- O2 netdata 重载：第九轮三条 + `newhub_settlement_failed` + 本轮 `newhub_db_pool_saturating`；`OBS_SLACK_WEBHOOK_URL` 仍未设。
- O7 生产 `ChannelDisableThreshold` 60；邀请链接密码路径验证。
- **O-session**：`SESSION_REGISTRY_ENABLED=true` 进生产 manifest（UAT 已 soak 两个月；本轮把 off 态改成 409 之后控制台会明确报"未开启"）。
- **O-pool**：确认 R6 `database` 实例 `max_connections` 与 platform/zitadel 占用，决定是否上调 20/5。
- **O-org**：platform 侧 `GET /internal/v1/accounts/:id/organizations`（契约见 L9 设计稿）；生产 `tenants.zitadel_org_id` 是否仍是 `*_PLACEHOLDER`；IdP 是否发 `org_id` claim、是否认 `organization=` 参数。
- **O-ratio**：模型/分组倍率没有任何可达的编辑器（`RatioSetting.jsx` 零 importer；v2 Admin/Settings 注释说"在 v1"是假的）—— 决定重新挂载还是做 v2 编辑器。
- **O-pricing**：公开价目表在多租户主机上是否仍是产品意图；生产 `SELECT tenant_id, count(*) FROM channels GROUP BY 1` 决定 L5 收窄的实际可见面。
- **O-lang**：产品实际售卖语种；本轮只发 zh/en，四语种文件保留待补。
- **O-tgps**：`terminationGracePeriodSeconds 40` vs 长流（`STREAMING_TIMEOUT=300`/块）—— 滚动更新会切断长流，是否接受。
- 根仓 `doc/coord/*` 两文件的行由 owner 提交。
