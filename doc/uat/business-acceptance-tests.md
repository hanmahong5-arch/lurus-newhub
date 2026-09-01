# Lurus Hub 业务验收测试（UAT / PROD）

> **版本**：2026-09-01（用例订正版。2026-08-31 首版整份重写；上一版 2026-06-05 的用例与"已实测"基线已全部作废）
>
> **首次全量执行：2026-09-01**，25 场景 live 跑完（UAT + PROD 只读），结果 21 PASS / 1 FAIL / 3 BLOCKED、P0=0 P1=0。
> 执行中发现本文自身有 **11 处用例错误**，其中三处会把**正确的产品行为判成 FAIL**；本版已逐条订正，订正点标 `【订正 2026-09-01】`。
> 唯一那条 FAIL（TC-M3）的两个根因已由 PR #120 修复并 live 复验，本版同步更新其 oracle。
> 面向**业务 / QA / 客户成功**的黑盒验收测试。不是 Go 单测（开发侧见 `TESTING.md`），
> 而是"产品对外能不能用"的端到端确认。每条场景给：目的 / env / 优先级 / 串联功能点 / 前置 / 步骤（动作 + oracle）/ 回归防守。
>
> **这是待执行的测试计划，不是执行报告。** 本文任何一行都不代表"已通过"——PASS/FAIL 由执行者在签字表里填。

## 目标环境（双轨）

| 轨 | Base URL | 身份 | 计费 | 关键差异 |
|----|----------|------|------|----------|
| **UAT** | `https://test-newhub.lurus.cn` | **bridge 会话**（`E2E_BRIDGE_TOKEN`） | 仅自有配额账本 | 隔离实例 `ns lurus-newhub-uat` / NodePort 30851 / 独立库 `newhub_uat` / Redis DB 3；**OIDC 关、统一计费关、NATS 关**；`CREDIT_POOL_REQUIRED=enforce`；web 限流 600/180s；session cookie host-only + Secure（必须走域名，隧道 `http://localhost:30851` 的浏览器流会丢 cookie） |
| **PROD** | `https://hub.lurus.cn` | **OIDC SSO**（identity.lurus.cn） | 统一计费（platform 钱包）+ 自有账本 | 生产实例 `ns lurus-newhub` / NodePort 30850；**有真实数据与真实客户**，涉钱步骤一律用最小金额 |

每条场景标 **env: UAT / PROD / BOTH**。标 UAT 的场景不要在 PROD 上跑写操作；标 PROD 的场景在 UAT 上结构性不可覆盖（见附录 A）。

> **路由真源** = `internal/adapter/handler/router/*.go`。本文所有 endpoint、状态码、字段名、错误码均已对 **HEAD 逐条核验（2026-08-31）**；与旧文档冲突处一律以本文为准。

---

## 0. 前置与约定

| 项 | 说明 |
|----|------|
| 工具 | `curl` / 浏览器 DevTools（控制台类场景走浏览器）；`jq` 建议装上，本文多处要读嵌套字段 |
| Base URL | `export BASE=https://test-newhub.lurus.cn`（UAT）或 `export BASE=https://hub.lurus.cn`（PROD） |
| 通过定义 | 一条场景的**全部** oracle 满足才算 PASS；任一不满足记 FAIL + 完整响应体（含状态码与响应头） |
| 不在范围 | 上游模型**回答质量**、压测/容量、前端像素级 UI |
| 副作用告知 | UAT 上这套用例**对库有副作用**（建 token、扣配额、消耗兑换码、写日志）。执行前知会 owner；执行后不要求回滚 |

### 0.1 需准备的凭证（开测前由管理员发放）

| 代号 | 凭证 | 获取方式 |
|------|------|----------|
| `$E2E_BRIDGE_TOKEN` | UAT bridge 令牌 | R6 侧 `kubectl -n lurus-newhub-uat get secret …`；轮换后须同步 repo secret |
| `$UID` | UAT 种子用户 id（root，用于 admin 类前置） | 由 owner 提供；bridge 用它换会话 |
| `$TENANT_SLUG` | **可路由**租户 slug（UAT = `lurus`） | 由 owner 提供，**不要**从 bridge 响应里取（见 §0.2.1） |
| `$TENANT_ID` | 租户 id（credit-pool 路由用的是 id 不是 slug） | `GET $BASE/api/v2/admin/tenants`（root 会话） |
| `$MODEL` | 当前渠道池里真实可路由的模型标识 | `GET $BASE/v1/models` + Bearer token，从返回的 `data[]` 里选 |
| `$MODEL_ALLOWED` / `$MODEL_FORBIDDEN` | 白名单内 / 白名单外但平台可路由的两个模型标识 | 同上，取两个不同值 |
| `$CODE` | 一次性兑换码（**32 位**，未使用） | **【订正 2026-09-01】现铸，不要从库里挑**：租户 admin `POST /api/v2/:slug/redemptions`（需 role≥10），码值只在**创建响应**的 `data.codes[0].key` 出现一次。① `GET /redemptions` 列表返回的是**同长同形掩码**（首尾各 4 位真实 + 24 个 `*`，见 §TC-C3 的 P3 注），拿它去兑换得 `400 无效的兑换码`，与"码不存在"同一文案；② `redemptions` 表有 `deleted_at` 软删列，直接按 `status=1` 挑行会挑到软删码，同样必然 400。一码一用，多个场景各需一枚 |
| `$INTERNAL_KEY` | internal API key（`lurus_ik_…`） | PROD：`重要信息.md`；UAT：按 §TC-G1 前置现场铸 |
| `ROOT_JWT` | PROD root 的 IdP access_token | 仅 PROD `/api/v2/admin/*` 用；UAT 不需要（见 §0.2.2 注） |

```bash
export BASE=https://test-newhub.lurus.cn
export TENANT_SLUG=lurus
export UID=1
```

### 0.2 全局硬前置（**缺一整套测试全翻车**）

#### 0.2.1 bridge 登录配方（UAT 唯一可用的登录方式）

UAT 关掉了 OIDC，仓库里**没有**任何密码/passkey 登录路由，唯一能拿到会话的是 bridge。它**只读 query 参数**（`v2_bridge.go:56,65`）——带 `Authorization` 头或 JSON body 一律 403 `invalid bridge token`：

```bash
curl -sS -i -c cookies.txt -X POST \
  "$BASE/api/v2/bridge/exchange?token=$E2E_BRIDGE_TOKEN&user_id=$UID"
```

Oracle：`HTTP 200` + 响应头有 `Set-Cookie`（写入 `cookies.txt`）+ `body.data.tenant_slug` 存在。

- ⚠️ **响应里的 `tenant_slug` 是装饰性的 `"default"`**，不是可路由 slug。所有 `/api/v2/:tenant_slug/*` 调用一律用前置给的 `$TENANT_SLUG`（UAT = `lurus`）。
- ⚠️ **bridge 限流 = 5 次 / 60s / IP**（`BootstrapRateLimit`，`api-v2-router.go:61-62` → `rate-limit.go:145-147`）。**全程复用同一份 `cookies.txt`**；确需重登时场景之间隔 60s 或计数，否则第 6 次拿到的 429 会被误判成功能缺陷。
- 浏览器流（控制台类场景）：SPA 路由守卫**只看 localStorage**（`web/src/helpers/auth.jsx:51-55`、`hooks/common/useTenantSlug.js:27-33`），单有 cookie 会被弹回 `/login`。在**域名**页面的 DevTools Console 里先执行：

```js
await fetch('/api/v2/bridge/exchange?token=<E2E_BRIDGE_TOKEN>&user_id=<UID>', { method: 'POST' })
  .then(r => r.json())
  .then(j => {
    localStorage.setItem('user', JSON.stringify(j.data));
    localStorage.setItem('tenant_slug', 'lurus');
  });
location.href = '/console/v2/dashboard';
```

  必须用 `https://test-newhub.lurus.cn`（session cookie 是 host-only + Secure，SSH 隧道的 `http://localhost:30851` 会被浏览器静默丢弃 cookie，症状是"bridge 200 但后续全 401"）。

#### 0.2.2 UAT `CREDIT_POOL_REQUIRED=enforce` —— 开测前必须给租户种 credit pool

`deploy/k8s/r6-uat/deployment.yaml:121` 设了 `CREDIT_POOL_REQUIRED=enforce`。租户没有 credit_pool 行时，`PoolBalanceCheck`（`pool_balance_check.go:68-79`）会在**读配额之前**就把每一次 `/v1`、`/v1/audio`、`/mj`、任务类路由的中转请求 402 掉，错误码 `pool_not_configured`。不种池 = 所有 relay 场景全红，且 TC-M4 里这个 402 与"配额耗尽"的 402 长得一模一样。

```bash
# 1) 取租户 id（root 会话即可；OIDC 关闭时 RootJWTAuth 回退到会话 root 校验，admin_jwt_auth.go:67-78）
curl -sS -b cookies.txt "$BASE/api/v2/admin/tenants"

# 2) 建 unlimited 池（max_balance=-1 是"无上限"哨兵，直接跳过池闸：pool_balance_check.go:102-105）
curl -sS -b cookies.txt -X POST "$BASE/api/v2/admin/tenants/$TENANT_ID/credit-pool" \
  -H 'Content-Type: application/json' \
  -d '{"max_balance":-1,"reset_period":"monthly","alert_threshold_pct":80}'
```

Oracle：`HTTP 201`，`body.data.max_balance == -1`。若返回 `409 POOL_ALREADY_EXISTS`，用 `GET $BASE/api/v2/admin/tenants/$TENANT_ID/credit-pool` 确认现存池 `max_balance == -1` 或 `current_balance > 0` 即可。

> ⚠️ 有限额度池 + `POST …/credit-pool/topup` 这条路在 UAT 上通常走不通：`TopupCreditPool` 要求操作者有 platform 钱包（`actor.LurusAccountID`），UAT 统一计费关闭时返回 **412 Precondition Failed**（`tenant_credit_pool.go:273-278`）。UAT 一律用 `max_balance:-1`；需要验"池耗尽 402"的场景移交 PROD 或运维直连 DB。

🔴 **每一个 402 断言都必须同时核对 `body.error.code != "pool_not_configured"`**，否则你验的是池没配，不是被测功能。

#### 0.2.3 金额换算：`quota_per_unit` 从 `/api/status` 读

```bash
curl -sS "$BASE/api/status" | jq '.data.quota_per_unit'   # 记为 $QPU，默认 500000
```

`quota_per_unit`（`misc.go:147`）是全站唯一单位价真源，控制台也是拿它换算的。**所有金额 oracle 一律写成数值 delta**（整数 quota 单位或 `round(delta_usd × $QPU)`），禁止"看起来合理""明显变小""大致相符"。USD = `quota / $QPU`。

#### 0.2.4 Token 创建的正确形状

- `POST /api/v2/:tenant_slug/tokens` 返回 **201 Created**（`v2_token.go:286`），**不是 200**。
- `body.data.key` 是完整明文 key（`sk-…`），**仅创建响应出现这一次**；列表接口返回的是掩码值。
- **`quota_usd` 不是合法字段**。未知字段被静默忽略 ⇒ token 以 `remain_quota=0` 建出来 ⇒ 第一次 relay 直接 402（`repo/token.go:218` → `middleware/auth.go:423-428`），看起来像计费缺陷其实是用例写错。合法配额字段：
  - `remain_quota`（**整数 quota 单位**，$1 = `$QPU`），或
  - `unlimited_quota: true`。
- 其他本文用到的合法字段：`model_limits_enabled` / `model_limits` / `rate_limit_rpm`。

#### 0.2.5 `balance` 与 `usage` 不是同一个维度

| 接口 | 维度 | 字段 |
|------|------|------|
| `GET /v1/billing/balance` | **token 维度**（`DisplayTokenStatEnabled` 默认 true，`common/constants.go:24` → `billing_service.go:42-50`） | `balance_lb` = 该 token 的 `remain_quota / $QPU`；`used_lb` = 该 token 的 `used_quota / $QPU` |
| `GET /v1/billing/usage?period=today` | **user 维度、当日聚合**（`billing_self.go:38-76` → `repo/log.go:803-808`，只按 `user_id + type=consume + created_at>=今日零点` 过滤） | `total_cost_lb`、`by_model[]` |

⇒ **二者不可画等号**。同一个用户当天的其他 token 流量会污染 `total_cost_lb`。精确对账一律用 **delta**：token 侧用 `balance_lb` 前后差，跨接口只能断言 `total_cost_lb_delta >= balance_delta`。

#### 0.2.6 UAT 必须有一个**可路由的上游渠道**，否则整组中转场景无从验起

**【新增 2026-09-01】** `newhub_uat` 是全新库，**默认 `channels` 与 `abilities` 都是空的**。没有渠道时，每一个"中转返回 200"的 oracle（TC-M1/M2/M3、TC-C1、TC-C6 正控、TC-G3 步骤 4-5、TC-G7 正控）都只能记 BLOCKED——也就是钱路验收整块作废。开测前先确认：

```bash
curl -sS -H "Authorization: Bearer <任意可用 token>" "$BASE/v1/models" | jq '.data[].id'   # 空数组 = 没渠道
```

种渠道**必须走管理 API，不能直接写库**——`abilities` 行是 `BatchInsertChannels` 生成的，直接 `INSERT INTO channels` 建出来的渠道路由不到，症状是 `404 model_not_found（distributor）`：

```bash
# root 会话；key 由 owner 提供
curl -sS -b cookies.txt -X POST "$BASE/api/channel/" -H 'Content-Type: application/json' -d '{
  "mode":"single",
  "channel":{"name":"uat-acceptance","type":1,"key":"<上游 key>",
             "base_url":"https://api.deepseek.com","models":"deepseek-chat",
             "group":"default","status":1,"priority":0,"weight":1}}'
```

Oracle：`success=true`，且 `GET /v1/models` 随后能列出该模型。⚠️ 这会产生**真实上游花费**（2026-09-01 实测 deepseek-chat 约 $0.000002/次，整轮验收 < $0.001）。

#### 0.2.7 `ERROR_LOG_ENABLED` 跑前核实 live env

代码默认 **false**（`internal/pkg/common/init.go:160`）；UAT/PROD 的 manifest 显式设为 `true`（`deploy/k8s/r6-uat/deployment.yaml:113`、`deploy/k8s/r6-stage/deployment.yaml:102`）。所有"失败调用应落日志"的 oracle 都依赖它。跑 TC-C6 / TC-G4 之前请运维确认 live 环境变量确为 `true`，否则整组是假红。

### 0.3 通用断言

- 管理面 API（`/api/*`、`/api/v2/*`）：HTTP 2xx + `body.success == true`；创建类是 **201**。
- 中转面（`/v1/*`）：走 chat-completions / messages 兼容结构；错误体形如 `{"error":{"code":…,"message":…,"metadata":{…}}}`。
- 错误体"三要素"：发生了什么 / 期望是什么 / 调用方能做什么（如 402 带 `metadata.topup_url` 或 `metadata.token_remain_quota_units`）。
- 日志行的失败判据是**整数 `type` 字段**：`1=topup, 2=consume, 5=error`（`entity/log.go:50-56`）。`GetLogsV2` 返回 `{"success":true,"data":{"logs":[…],"total":…}}`，**没有 `items`、没有 `status`、没有 `error` 字段**。
- v2 日志投影自 2026-08-31 起返回**分级 `other`**（部署含该提交的镜像后生效）：用户路由 `GET /api/v2/:slug/logs` 带 `cache_tokens / cache_creation_tokens / request_path / stage`（TierInternal 键仍剥）；`model_ratio / cache_ratio / admin_info.route_attempts` 只在**租户管理员路由** `GET /api/v2/:slug/logs/all` 可见。⚠️ 别指望 v1 自助日志 `/api/log/self/` 给 ratio——它对所有调用者无条件剥 TierInternal 键（`repo/log.go` formatUserLogs，2026-08-31 实核）。旧镜像上 v2 完全不带 `other`。

---

## 1. 测试流程（6 阶段门禁，逐阶段 gate）

```
阶段1 冒烟 ──gate──> 阶段2 会话&租户 ──gate──> 阶段3 Token & 中转
        │                                                      │
        └──────────── FAIL 任一 → 停测、报开发 ────────────────┘
阶段4 计费 ──gate──> 阶段5 治理 & 安全 ──gate──> 阶段6 验收签字
```

| 阶段 | 目标 | 入口 gate（必须先 PASS） | 场景 |
|------|------|--------------------------|------|
| 1 冒烟 | 服务在线、关键路由可达、边界按设计封堵 | — | TC-S1…S6 |
| 2 会话 & 租户 | 能登录、租户隔离生效 | 阶段1 全绿 + §0.2 前置全部就绪 | TC-C1、TC-C5、TC-C2(PROD) |
| 3 Token & 中转 | 发 key、过网关、拿到模型响应、白名单/生命周期 | 阶段2 全绿 | TC-M1、TC-M5 |
| 4 计费 | 计价一致、缓存计价、402→兑换→重试、限流不漏计费、单位价一致 | 阶段3 至少 TC-M1 PASS | TC-M2、TC-M3、TC-M4、TC-M6、TC-C3、TC-C4 |
| 5 治理 & 安全 | 该拒的必须拒、错误可观测 | 阶段3 全绿 | TC-G1…G7、TC-C6 |
| 6 验收 | 签字 | 1–5 全绿 | 签字表 |

**场景总数：25**（TC-S×6 + TC-M×6 + TC-C×6 + TC-G×7）

---

## 2. 阶段 1 — 冒烟（TC-S，无需认证）

| ID | 目的 | env | 动作 | Oracle |
|----|------|-----|------|--------|
| **TC-S1** | 网关存活 | BOTH | `curl -s -o /dev/null -w "%{http_code} %{time_total}s\n" "$BASE/api/status"` | `200`，耗时 < 1s |
| **TC-S2** | 配置体可读 & 单位价可取 | BOTH | `curl -sS "$BASE/api/status" \| jq '.success, .data.quota_per_unit, .data.version'` | `success=true`；`quota_per_unit` 为正整数（记为 `$QPU`）；`version` 非空。<br>**【订正 2026-09-01】** 再加两条:①两个环境的 `version` 必须**相同**（UAT 与 PROD 同 digest 是 `deploy/k8s/r6-uat/` 的设计不变量，双写 auto-pin 保证）；②**在整轮验收的开头和结尾各取一次 `version` 并比对**——2026-09-01 首跑时镜像在开跑 2-5 分钟后被一次自动部署换了版，早期步骤与后期步骤跑在不同代码上，是核验员而不是执行者发现的 |
| **TC-S3** | 详细健康检查 | BOTH | `curl -sS "$BASE/api/health" \| jq '.'` | `200`；`checks.schema_migrations` 无 pending（带 migration 的部署后尤其要看这一项） |
| **TC-S4** | **`/metrics` 必须 404**（设计如此） | BOTH | `curl -s -o /dev/null -w "%{http_code}\n" "$BASE/metrics"` | **`404`**。双层封堵：边缘 nginx `location = /metrics { return 404; }` + 应用层 `metricsAuthMiddleware`。**返回 200 + Prometheus 文本 = P0 缺陷**（真实余额指标公网可读）。唯一合法抓取者是宿主 netdata 直连 NodePort，不经 nginx |
| **TC-S5** | Switch 公开路由（若仍在编）| BOTH | `curl -sS "$BASE/api/v2/switch/tools/versions"`；`curl -sS "$BASE/api/v2/switch/presets"`；`curl -sS "$BASE/api/v2/switch/pricing"` | 三条均 `200` 且 `success=true`；`/presets` 的 `data` 是数组（空数组合法）；`/pricing` 的 `data` 含 `quota_per_unit`。**若某条 404，先确认该路由是否已下线再判 FAIL** |
| **TC-S6** | 未认证管理面必须拒 | BOTH | `curl -s -o /dev/null -w "%{http_code}\n" "$BASE/api/v2/admin/tenants"` | `401`。**不得是 200，也不得是 500** |

> **gate**：TC-S1/S2/S4/S6 任一 FAIL 即停测报开发。

---

## 3. 阶段 3–4 — 核心钱路（TC-M）

> 网关门禁链（`relay-router.go`）：`TokenAuth → PoolBalanceCheck → CostSpikeLimit → EntitlementCheck → ModelRequestRateLimit → BusinessRateLimit → RelayConcurrencyLimit → Distribute → Relay`。
> 本组所有场景的公共前置：§0.2.1 bridge 会话（复用 `cookies.txt`）、§0.2.2 credit pool 已种、§0.2.3 `$QPU` 已取。

### TC-M1 — 旗舰全链：开票 token → 多格式中转 → 自助账单对账 → 控制台可见

- **env**：UAT · **优先级**：P0
- **串联功能点**：bridge 会话 → 建 token(201) → `GET /v1/models` → chat-completions 非流式 → chat-completions 流式(SSE) → messages 原生格式 → `GET /v1/billing/balance` delta → 租户日志逐笔对账 → `GET /v1/billing/usage` 当日聚合 → 控制台日志页
- **前置**：§0.2 全部；`$MODEL` 已从 `/v1/models` 选定

| # | 动作 | Oracle |
|---|------|--------|
| 1 | `curl -sS -i -c cookies.txt -X POST "$BASE/api/v2/bridge/exchange?token=$E2E_BRIDGE_TOKEN&user_id=$UID"` | `200` + `Set-Cookie` 写入 `cookies.txt` |
| 2 | `curl -sS -b cookies.txt -X POST "$BASE/api/v2/$TENANT_SLUG/tokens" -H 'Content-Type: application/json' -d '{"name":"acc-m1","remain_quota":5000000,"unlimited_quota":false,"model_limits_enabled":false}'` | **`201`**，`success=true`，`data.key` 匹配 `^sk-`（记 `$TOKEN`），`data.id` 为数字（记 `$TOKEN_ID`） |
| 3 | `curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/v1/models"` | `200`，`data[]` 含 `$MODEL`（该 token 确实看得见这个模型） |
| 4 | `curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/v1/billing/balance"` | `200`；记 `$BAL_0 = body.balance_lb`、`$USED_0 = body.used_lb`（这是后续所有 delta 的基线） |
| 5 | `curl -sS -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -X POST "$BASE/v1/chat/completions" -d '{"model":"'"$MODEL"'","messages":[{"role":"user","content":"reply with exactly: OK"}],"max_tokens":5}'` | `200`，`usage.prompt_tokens > 0` 且 `usage.completion_tokens > 0`；记响应头里的 request id |
| 6 | `curl -sS -N -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -X POST "$BASE/v1/chat/completions" -d '{"model":"'"$MODEL"'","stream":true,"messages":[{"role":"user","content":"count 1 to 3"}],"max_tokens":20}'` | `200`，`Content-Type: text/event-stream`，≥1 个 `data: {…}` chunk，且以 `data: [DONE]` 结束 |
| 7 | `curl -sS -H "x-api-key: $TOKEN" -H 'anthropic-version: 2023-06-01' -H 'Content-Type: application/json' -X POST "$BASE/v1/messages" -d '{"model":"'"$MODEL"'","max_tokens":5,"messages":[{"role":"user","content":"reply with exactly: OK"}]}'` | `200`，`usage.input_tokens > 0` 且 `usage.output_tokens > 0`（同一把 token 无缝切换第二种 wire 格式，鉴权/配额链路复用） |
| 8 | `curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/v1/billing/balance"` | `200`；`$BAL_1 = body.balance_lb`，**`$BAL_1 < $BAL_0` 严格下降**；`used_lb > $USED_0` |
| 9 | `curl -sS -b cookies.txt "$BASE/api/v2/$TENANT_SLUG/logs?token_name=acc-m1&page=1&page_size=20"` | `200`；`data.logs` 中 `type == 2`（consume）的行**恰好 3 行**（步骤 5/6/7 各一笔；步骤 3/4 走的是 `modelsRouter`/`billingRouter`，无 Distribute 无扣费，不产生消费行），每行 `model_name == $MODEL` |
| 10 | 数值对账（本地计算） | `round(($BAL_0 - $BAL_1) × $QPU)` **逐单位等于**步骤 9 三行 `quota` 之和。差 1 个单位以上即 FAIL |
| 11 | `curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/v1/billing/usage?period=today"` | `200`；`by_model` 含 `$MODEL` 且其 `count >= 3`；`total_cost_lb >= ($BAL_0 - $BAL_1)`。**只能断言 `>=`**——这是 user 维度当日聚合，同用户其他 token 的流量会一并计入（见 §0.2.5） |
| 12 | 浏览器：按 §0.2.1 的 DevTools 配方登录 `https://test-newhub.lurus.cn`，打开 `/console/v2/log`，按 token 名 `acc-m1` 过滤 | 列表出现 3 行，`model` 列均为 `$MODEL`，耗时/时间与步骤 5/6/7 一一对应（数据处理管道把中转调用落到了可查日志，不只是计费侧内部字段） |

**回归防守**：流式路径的 usage "算了就扔"（异常结束不得报价）；三条计费路径漏记某一种 wire 格式；token 维度余额与 user 维度聚合被错误画等号；任务类路由前置门禁误伤 `/v1` 流式。

---

### TC-M2 — 同请求跨格式计价一致性（rounding 分叉回归防守）

- **env**：UAT · **优先级**：P0
- **串联功能点**：建 token → 逐次 balance 前后差 → chat-completions ×3 → messages 原生 ×3 → 逐单位比对
- **前置**：§0.2 全部。固定 prompt + `max_tokens=1` + `temperature=0`，尽量收敛 token 数波动。
- **回归点（方向已核正）**：历史上 **messages 原生路径 truncate、chat-completions 兼容路径 `Round(0)`**，同样的 `(prompt, completion)` 组合两种格式扣费不同。HEAD 已在 `internal/app/quota.go:387` 与 `internal/app/relay/compatible_handler.go:404` 统一为 `Round(0)`。本场景是**防回归**。

| # | 动作 | Oracle |
|---|------|--------|
| 1 | `curl -sS -b cookies.txt -X POST "$BASE/api/v2/$TENANT_SLUG/tokens" -H 'Content-Type: application/json' -d '{"name":"acc-m2","remain_quota":3000000}'` | `201`，`data.key` 匹配 `^sk-`（记 `$TOKEN`） |
| 2 | **循环 i=1..3**：<br>a. `curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/v1/billing/balance"` → `b_before`<br>b. `curl -sS -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -X POST "$BASE/v1/chat/completions" -d '{"model":"'"$MODEL"'","messages":[{"role":"user","content":"Say A"}],"max_tokens":1,"temperature":0}'`<br>c. `curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/v1/billing/balance"` → `b_after` | 每次 `200`；记录 `usage.prompt_tokens` / `usage.completion_tokens`（三次应相同）与 `delta_A_i = round((b_before - b_after) × $QPU)`（**整数 quota 单位，不做浮点比较**） |
| 3 | **循环 i=1..3**：同样的 before/after 夹逼，中间换成<br>`curl -sS -H "x-api-key: $TOKEN" -H 'anthropic-version: 2023-06-01' -H 'Content-Type: application/json' -X POST "$BASE/v1/messages" -d '{"model":"'"$MODEL"'","max_tokens":1,"temperature":0,"messages":[{"role":"user","content":"Say A"}]}'` | 每次 `200`；记录 `usage.input_tokens` / `usage.output_tokens` 与 `delta_B_i` |
| 4 | 筛出 token 数完全对齐的组：`input_tokens == prompt_tokens` 且 `output_tokens == completion_tokens` | 至少存在一对可比组；若一对都没有，本场景记 **BLOCKED**（模型输出不稳定），不记 FAIL |
| 5 | 比对 | 可比组内 **`delta_A == delta_B` 逐单位相等**。若出现系统性固定偏差（例如 A 恒比 B 少 1 个单位），判定 truncate/Round 分叉**回归复发 = FAIL** |

**回归防守**：`quota.go:387` 与 `compatible_handler.go:404` 的取整方式分叉——同一次调用按请求格式差一倍钱、小额调用整单免费。

---

### TC-M3 — prompt-cache 按上游 wire 语义计价，而非按渠道猜测

- **env**：UAT · **优先级**：P0
- **串联功能点**：建 token → 冷调用（cache write）→ 热调用（cache read）→ v1 自助日志读 `other` → 数值恒等式判别
- **前置**：§0.2 全部；需要一个**支持 prompt cache 且会在响应里回报缓存字段**的渠道/模型（记 `$MODEL`）。若 UAT 渠道池没有任何缓存能力，本场景记 **BLOCKED: UAT 渠道池无缓存供应商，待补种**，不记 FAIL。
- ⚠️ 缓存是否**自动发生取决于上游供应商**，这决定了本场景该怎么跑：
  - **Anthropic 系上游**：不会自动缓存，必须在内容块上显式带 `cache_control`，被缓存段要够长（≥1024 tokens 量级）。用裸字符串 `"system":"…"` 永远拿不到命中，那是用例错不是产品错。
  - **【订正 2026-09-01】OpenAI 兼容上游（当前 UAT 唯一渠道 = DeepSeek）**：**自动前缀缓存**，不认 `cache_control`，也**不单独计量 cache creation**。所以步骤 3 的 `cache_creation_input_tokens > 0` 在这类渠道上**结构性恒为 0**，那是渠道能力差异，**不记 FAIL**。正确跑法：构造一段 ≥1024 tokens 的**固定前缀**放进 `system`，仅改 user 内容，冷调用一次、热调用一次，判据落在热调用的 `cache_read_input_tokens > 0`。
  - 2026-09-01 实测配方（可直接抄）：20KB 重复文本作 `system`，冷调用 `usage.cache_read_input_tokens=0`，热调用 `=3456`，`input_tokens` 两次都是 3527。

| # | 动作 | Oracle |
|---|------|--------|
| 1 | `curl -sS -b cookies.txt -X POST "$BASE/api/v2/$TENANT_SLUG/tokens" -H 'Content-Type: application/json' -d '{"name":"acc-m3","remain_quota":3000000}'` | `201`，记 `$TOKEN` |
| 2 | `curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/v1/billing/balance"` | `200`，记 `$BAL_A` |
| 3 | **冷调用**：`curl -sS -H "x-api-key: $TOKEN" -H 'anthropic-version: 2023-06-01' -H 'Content-Type: application/json' -X POST "$BASE/v1/messages" -d '{"model":"'"$MODEL"'","max_tokens":1,"system":[{"type":"text","text":"<固定的 ≥1024 tokens 长文本>","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"ping1"}]}'` | `200`；`usage.cache_creation_input_tokens > 0`（冷写入成功——这是步骤 4 能命中的前提）且 `usage.cache_read_input_tokens` 为 0/缺失。查 balance 记 `delta_1` |
| 4 | **热调用**：同一 `$TOKEN`、**同一段带 `cache_control` 的 system**，仅把 user content 换成 `ping2` | `200`；`usage.cache_read_input_tokens > 0`（上游真的命中了缓存）。查 balance 记 `delta_2` |
| 5 | `curl -sS -b cookies.txt "$BASE/api/v2/$TENANT_SLUG/logs/all?token_name=acc-m3"`（**必须是 role≥100 的 root 会话**） | `200`；取热调用那一行的 `prompt_tokens` 与 `other` 里的 `cache_tokens` / `model_ratio` / `cache_ratio` / `completion_ratio` / `group_ratio`。<br>⚠️ ratio 键**只有这条管理员路由给**：`/api/log/self/` 对所有人剥 TierInternal 键、旧版 v2 `/logs` 完全不带 `other`。<br>**【订正 2026-09-01】** 原文只写"管理员会话"，实测**普通用户会话在这里拿 403 `Admin role required`**（`repo/log.go:142-149`）。另:**比率必须从这里实测取，不许套默认值**——判别式的两个形状相差 `cache_tokens × model_ratio × group_ratio`，用假设的比率去算等于自证自话 |
| 6 | **数值判别**（本地计算，用步骤 5 的实际比率） | 计费的 input 部分必须等于 **`(prompt_tokens − cache_tokens) + cache_tokens × cache_ratio`**（wire 语义扣减，`quota.go:307-333` 的 `if usage.PromptTokensIncludeCached { promptTokens -= cacheTokens }`）；<br>**必须严格不等于** `prompt_tokens + cache_tokens × cache_ratio`（缓存段被全价 input **再加**一遍缓存价的双计形状 = "按渠道猜测"回归复发）。两个形状相差 `cache_tokens × model_ratio × group_ratio`，足以判别。<br>**【订正 2026-09-01】** 式中的 `prompt_tokens` 必须是**上游原始值**。PR #120 之前 Claude 原生路径把**扣减后**的值落库（同一段前缀 `/v1/chat/completions` 记 3527、`/v1/messages` 记 71），代进式子得负数、判别式失效；**PR #120 起两种 wire 的日志统一记原始值**，直接用步骤 5 那一行即可。<br>2026-09-01 实测对账（比率全部取自步骤 5）：`(3527−3456) + 3456×0.25 + 4×1 = 939`，`939 × 0.135 × 1 = 126.765 → Round = 127`，与日志 `quota=127` **逐位相等**；双计形状 `(3527 + 3456×0.25 + 4) × 0.135 = 593`，被排除 |
| 7 | 兜底 sanity | `0 < delta_2 < delta_1`（命中缓存严格更便宜、但不为 0、不倒挂）。**这一条只作辅助，不能单独作为通过依据**。2026-09-01 实测 `delta_1=477`、`delta_2=127` |
| 8 | **【新增 2026-09-01】** 客户侧可见性：热调用响应体的 `usage.cache_read_input_tokens` | **`> 0`**（实测 3456）。PR #120 之前非流式 `/v1/messages` **恒返回 0**——请求按缓存折扣计了费，但客户端拿不到命中数据，基于 Anthropic SDK 的缓存命中率看板恒读零。流式路径一直是对的，只有非流式漏了，所以**必须用非流式验这一条** |

**回归防守**：缓存计价按渠道猜测导致的**双向真缺陷**——多收 11× 与少收免单；修法是把 wire 语义标记打在 usage 解析点、下游禁猜来源。

---

### TC-M4 — 配额耗尽 → 可操作 402 → 兑换充值 → 提额 → 重试成功 → 兑换码防重放

- **env**：UAT · **优先级**：P0
- **串联功能点**：极小额度 token → 402 `token_quota_exhausted` → 用户钱包读数 → `POST /redeem` → 钱包 delta → **PUT 提 token 上限** → 重试 200 → 同码重放 400 + 金额不动
- **前置**：§0.2 全部；一枚未使用的 32 位 `$CODE`，其面额（整数 quota 单位）记为 `$CODE_QUOTA`
- 🔑 **本场景的关键结构事实**：兑换码充的是**用户钱包**（`v2_redemption.go:88` 传的是 `tenantCtx.UserID`），**不动 token 的 `remain_quota` 上限**。而中转的第一道配额闸卡的是 token 行（`quota.go:679` `DecreaseTokenQuotaIfEnough`）。所以"充值后直接重试"在 HEAD 上必然仍是 402——必须插一步 `PUT token` 提额。这是设计，不是缺陷。

| # | 动作 | Oracle |
|---|------|--------|
| 1 | `curl -sS -b cookies.txt -X POST "$BASE/api/v2/$TENANT_SLUG/tokens" -H 'Content-Type: application/json' -d '{"name":"acc-m4","remain_quota":1}'` | `201`，记 `$TOKEN` 与 `$TOKEN_ID` |
| 2 | `curl -sS -i -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -X POST "$BASE/v1/chat/completions" -d '{"model":"'"$MODEL"'","messages":[{"role":"user","content":"hi"}],"max_tokens":5}'` | `402`；`body.error.code == "token_quota_exhausted"`；`body.error.metadata.reason == "token_quota_exhausted"`；`body.error.metadata.token_remain_quota_units == 1`；**且 `body.error.code != "pool_not_configured"`**（否则你验的是池没配） |
| 3 | `curl -sS -b cookies.txt "$BASE/api/v2/$TENANT_SLUG/user/me"` | `200`，记 `$QUOTA_0 = data.quota`（**原始整数单位**，`user.go:229`） |
| 4 | `curl -sS -b cookies.txt -X POST "$BASE/api/v2/$TENANT_SLUG/redeem" -H 'Content-Type: application/json' -d '{"key":"'"$CODE"'"}'` | `200`，`success=true`，`data.quota_added == $CODE_QUOTA`。注意字段名是 **`key`** 不是 `code`，且码必须**恰好 32 位**（`v2_redemption.go:79-85`） |
| 5 | `curl -sS -b cookies.txt "$BASE/api/v2/$TENANT_SLUG/user/me"` | `200`，`data.quota == $QUOTA_0 + $CODE_QUOTA`（**逐单位相等**，不接受约等于） |
| 6 | 重复步骤 2 的中转调用（**边界断言**） | 仍然 `402 token_quota_exhausted`。这证明"充值加的是用户钱包、不是 token 上限"这条耦合边界符合设计。**若这里意外 200，说明 token 上限被旁路，属 P1 缺陷** |
| 7 | `curl -sS -b cookies.txt -X PUT "$BASE/api/v2/$TENANT_SLUG/tokens/$TOKEN_ID" -H 'Content-Type: application/json' -d '{"remain_quota":1000000}'` | `200`，`success=true` |
| 8 | 再次重复步骤 2 的中转调用；随后 `curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/v1/billing/balance"` | 中转 `200`；`balance_lb` 相对提额后的值**严格下降**（整条链真的走通了计费判定，不只是数据库数字变了） |
| 9 | `curl -sS -i -b cookies.txt -X POST "$BASE/api/v2/$TENANT_SLUG/redeem" -H 'Content-Type: application/json' -d '{"key":"'"$CODE"'"}'` | **`400`**，`success=false`（`v2_redemption.go:89-95`） |
| 10 | `curl -sS -b cookies.txt "$BASE/api/v2/$TENANT_SLUG/user/me"` | `data.quota` 与**步骤 9 之前刚读的那次**逐字节相同（重放没有二次入账；只看状态码不算数）。<br>**【订正 2026-09-01】** 原文写"与步骤 5 相同"是**错的**,会把正确的防重放行为判成 FAIL:步骤 8 的两次真实中转**同时也扣用户钱包**（`internal/app/quota.go` 结算瀑布里的 `DecreaseUserQuota`），实测步骤 5 读到 20001000、步骤 10 读到 20000998，差的正是那 2 个单位。**所以必须在步骤 9 的重放之前再读一次 `user/me` 作为参照系** |

**回归防守**：兑换码重放二次入账；402 被措辞成"上游故障"的裸 500；池未配的 402 与配额 402 混淆。

---

### TC-M5 — 模型白名单拒绝 + 失败调用可观测 + token 生命周期铁三角

- **env**：UAT · **优先级**：P1
- **串联功能点**：带 `model_limits` 的 token → 越权模型 403 → 错误日志落库（`type==5`）→ rotate 旧 key 立即失效 → delete 新 key 立即失效
- **前置**：§0.2 全部；`ERROR_LOG_ENABLED` live 为 true（§0.2.7）；`$MODEL_ALLOWED` / `$MODEL_FORBIDDEN` 两个不同的模型名。
  **【订正 2026-09-01】** UAT 渠道池通常只有**一个**可路由模型，凑不出两个。此时把 `model_limits` 设成那个可路由模型，`$MODEL_FORBIDDEN` 用任意别的模型名即可——**本场景的判据本来就是两种拒绝的形状差异**：`403`（TokenAuth 阶段的白名单拒绝，期望值）vs `404 model_not_found`（Distribute 阶段"无可用渠道"）。看到 404 就说明白名单闸没生效或请求根本没走到它，正是要抓的东西。执行时把观测到的是哪一个写进证据。

| # | 动作 | Oracle |
|---|------|--------|
| 1 | `curl -sS -b cookies.txt -X POST "$BASE/api/v2/$TENANT_SLUG/tokens" -H 'Content-Type: application/json' -d '{"name":"acc-m5","remain_quota":1000000,"model_limits_enabled":true,"model_limits":"'"$MODEL_ALLOWED"'"}'` | `201`，记 `$TOKEN`、`$TOKEN_ID` |
| 2 | `curl -sS -i -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -X POST "$BASE/v1/chat/completions" -d '{"model":"'"$MODEL_FORBIDDEN"'","messages":[{"role":"user","content":"hi"}]}'` | **`403`**，响应体含"该令牌无权访问模型"或等价文案并指名 `$MODEL_FORBIDDEN`。**不得是 429、不得静默透传** |
| 3 | `curl -sS -b cookies.txt "$BASE/api/v2/$TENANT_SLUG/logs?token_name=acc-m5&page=1&page_size=20"` | `200`；`data.logs` 中存在一行 **`type == 5`** 且 `model_name == $MODEL_FORBIDDEN` 且 `token_name == "acc-m5"`，`content` 含步骤 2 的拒绝文案。（`model_name` 非空是因为 `Distribute` 在 abort 前先写了 `original_model`） |
| 4 | `curl -sS -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $TOKEN" "$BASE/v1/models"` | `200`（rotate 前旧 key 有效——建立对比基线） |
| 5 | `curl -sS -b cookies.txt -X POST "$BASE/api/v2/$TENANT_SLUG/tokens/$TOKEN_ID/rotate"` | `200`，`data.key` 是**全新**的 `sk-…`（记 `$TOKEN_NEW`），与 `$TOKEN` 不同 |
| 6 | `curl -sS -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $TOKEN" "$BASE/v1/models"` | **`401`**（旧 key 立即失效，无缓存宽限窗口） |
| 7 | `curl -sS -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $TOKEN_NEW" "$BASE/v1/models"` | `200`（新 key 立即可用） |
| 8 | `curl -sS -b cookies.txt -X DELETE "$BASE/api/v2/$TENANT_SLUG/tokens/$TOKEN_ID"` | `200`，`success=true` |
| 9 | `curl -sS -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $TOKEN_NEW" "$BASE/v1/models"` | **`401`**（删除作用于底层账户，不只是前端隐藏） |

**回归防守**：`ERROR_LOG_ENABLED` 默认关导致上线至今零条错误日志；rotate/delete 后旧 key 因缓存仍可用。

---

### TC-M6 — 任务类路由的前置限流：闸门在 Distribute 之前，且被拒请求分文不扣

- **env**：UAT · **优先级**：P1
- **串联功能点**：建带 `rate_limit_rpm` 上限的 token → 音乐任务路由连发 → 业务 429（带 JSON body）→ 余额/已用量分文未动
- **前置**：§0.2 全部。
- ⚠️ **不要指望默认配置能打出 429**：`ModelRequestRateLimit` 的总量维度在 HEAD 是关的（`setting/rate_limit.go:52-56`，`Count=0`、`SuccessCount=1000`，且 Redis 后端只在 `status < 400` 时记账）；`BusinessRateLimit` 只在 token 自身 rpm/tpm > 0 时才拒（`business_rate_limit.go:321,328`），而建 token 默认 `rate_limit_rpm=0`。**必须显式给 token 设上限**，否则这一步既证明不了"闸门在"，也证明不了"闸门不在"。

| # | 动作 | Oracle |
|---|------|--------|
| 1 | `curl -sS -b cookies.txt -X POST "$BASE/api/v2/$TENANT_SLUG/tokens" -H 'Content-Type: application/json' -d '{"name":"acc-m6","remain_quota":1000000,"rate_limit_rpm":2}'` | `201`，记 `$TOKEN` |
| 2 | `curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/v1/billing/balance"` | `200`；记 `$BAL_PRE = balance_lb`、`$USED_PRE = used_lb` |
| 3 | 60s 窗口内连发 3 次：<br>`for i in 1 2 3; do curl -sS -i -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -X POST "$BASE/v1/audio/music" -d '{"prompt":"test"}'; done` | 第 3 次 **`429`**，`body.error.message` 含"令牌每分钟请求数已达上限（2/min）"，并带 `Retry-After` / `X-RateLimit-*` 头。<br>**判别点**：这是**业务限流**的 429，形状上区别于 Distribute 阶段"无可用渠道"的 404/503（前两次若因 UAT 无对应上游渠道返回 404/503，恰好证明请求确实到达了这个受保护的路由组）。若三次全是 404/503 而永远打不出 429，说明限流没有挂在 Distribute 之前 = FAIL |
| 4 | `curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/v1/billing/balance"` | `balance_lb == $BAL_PRE` **且** `used_lb == $USED_PRE`（逐单位相等）。被 429 / 无渠道拒绝的调用**分文未扣**——闸门在计费点之前，不是先扣费再拒绝。`used_lb` 是这里的关键 oracle：泄漏的扣费会推动它 |

> **不要用 `GET /v1/billing/usage` 的 `by_model` 来验这一条**：该接口硬过滤 `type = consume`，而 429/无渠道写的是 `type = 5` 的错误行（且 429 根本不写），断言恒真、抓不到任何缺陷。

> **cost-spike 保险丝（移交运维，不在 QA curl 覆盖范围）**：`COST_SPIKE_ENFORCE` 默认 `observe`，命中只计数、不改状态码不改响应头，黑盒不可观测。但**不是无信号**：运维可用 `kubectl logs -n lurus-newhub-uat deploy/… | grep cost_spike_triggered` 看到结构化行（含 `enforce:false`、`action:"observed"`）确认计数分支执行；`enforce` 分支（429 + 自动下线）需要临时置 `COST_SPIKE_ENFORCE=true` 才能验，**移交运维，不作为业务验收项**。

**回归防守**：`/mj`、任务类音乐/图像三组路由从未挂任何限流与成本闸（执法必须在 Distribute 之前，否则 503 会掩盖 429）；被拒请求仍产生扣费。

---

## 4. 阶段 2–5 — 控制台用户旅程（TC-C）

### TC-C1 — UAT 浏览器登录 → 仪表盘 → 建 token → 中转 → 日志页对账

- **env**：UAT · **优先级**：P0
- **串联功能点**：DevTools bridge 登录 + localStorage 播种 → Dashboard KPI → Token 页建 token（201/掩码）→ 中转调用 → Log 页过滤 → usage delta 对账
- **前置**：§0.2 全部；浏览器用干净配置/隐身窗口，**必须访问域名**（cookie host-only + Secure）

| # | 动作 | Oracle |
|---|------|--------|
| 1 | 浏览器打开 `https://test-newhub.lurus.cn`，DevTools Console 执行 §0.2.1 的 bridge + localStorage 片段 | fetch 返回 `200`；`localStorage.user` 与 `localStorage.tenant_slug`（= `lurus`）均已写入 |
| 2 | 跳转 `/console/v2/dashboard`，等待渲染 | 页面渲染出 KPI 卡片（用量/花费/请求数），**不被弹回 `/login`**；Network 面板里 `GET /api/v2/$TENANT_SLUG/user/me` 与 `GET /api/v2/$TENANT_SLUG/logs` 均 `200`（这两条正是 Dashboard 挂载时发的） |
| 3 | Token 页点"新建 Token"，name=`acc-c1`，额度填 **`remain_quota` = 2500000**（= 5 USD × `$QPU`，`$QPU`=500000 时）；等价 API：`POST /api/v2/$TENANT_SLUG/tokens -d '{"name":"acc-c1","remain_quota":2500000}'` | **`201`**，响应含一次性明文 key（记 `$TOKEN`）；列表刷新后出现 `acc-c1`，**列表里的 key 是掩码值**——不要断言明文 key 再次出现 |
| 4 | `curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/v1/billing/usage?period=today"` | `200`；记 `$USAGE_0 = total_cost_lb` |
| 5 | `curl -sS -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -X POST "$BASE/v1/chat/completions" -d '{"model":"'"$MODEL"'","messages":[{"role":"user","content":"hi"}],"max_tokens":8}'` | `200`，`usage.prompt_tokens > 0` |
| 6 | 再发一次同样的调用 | `200` |
| 7 | 浏览器 Log 页（`/console/v2/log`）按 `token_name=acc-c1` 过滤 | 列表出现 2 行，`model` 列 = `$MODEL`，成本列**非 `—`**。<br>**【订正 2026-09-01】** 原文写"`quota` 列为正整数"是**错的**：v2 日志页那一列渲染的是 **USD 四位小数**（`fmtCost`），页面上不存在整数 quota 列。`q1`/`q2` 要从 API 取:`GET /api/v2/:slug/logs?token_name=acc-c1` 的 `logs[].quota`。<br>另:PR #120 之前极小额扣费（如 quota=2）在这一列渲染成 `$0.0000`——计费产品把自己显示成免费；现已按 legacy 的规矩落到最小可表示值（`$0.0001`），**看到 `$0.0000` 而 API 侧 quota>0 即为回归** |
| 8 | `curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/v1/billing/usage?period=today"` | `200`；`(q1 + q2) / $QPU == total_cost_lb − $USAGE_0`（**围绕步骤 5-6 取 delta**，绝对值会被同用户当日其他流量污染） |

> **~~已知缺陷~~ 已修复（2026-08-31，随下一次镜像部署生效）**
> `/console/topup` 的兑换入口曾 POST 到未注册的 `/api/v2/{slug}/redemptions/redeem`（必然 404）；现已改打 `POST /api/v2/:slug/redeem` 并按 v2 响应形状（`data.quota_added`）入账。**新镜像上验收改为正向断言**：充值页输码提交 → 成功弹窗含兑换额度、页面余额上升该额度。旧镜像仍按缺陷单处理。

**回归防守**：SPA 路由守卫只看 localStorage（只灌 cookie 会被弹回登录页，看起来像"登录坏了"）；`quota_usd` 这种不存在的字段被静默忽略导致 token 零额度；Log 页与账单接口的单位（整数 quota vs USD）混算。

---

### TC-C2 — PROD SSO 全链：自动跳转 IdP → 落地控制台 → 租户绑定 → 登出

- **env**：**PROD** · **优先级**：P0
- **串联功能点**：`/login` 自动重定向 → IdP 认证 → 落地 `/console/v2/dashboard` → 会话信息 → 租户绑定（防跨租户顶替）→ 登出 → 服务端会话失效
- **前置**：hub.lurus.cn；一个 IdP 侧已预配置、**租户绑定关系已知**的真实测试账号；隐身窗口/干净配置
- ⚠️ **页面上没有"SSO 登录"按钮**：`/login` 渲染的组件会自动发起 `POST /api/v2/auth/zita-bootstrap`，失败则 `window.location.href = '/api/v2/auth/zita-login?return_to=…'`。**打开页面等着，不要找按钮点**。

| # | 动作 | Oracle |
|---|------|--------|
| 1 | 隐身窗口打开 `https://hub.lurus.cn/login`，**不做任何点击**，等待 | 浏览器自动被重定向到 identity 域（`identity.lurus.cn`），地址栏主机名不再是 `hub.lurus.cn`。<br>**【订正 2026-09-01】** 两点:①**给足等待预算**——PROD 主 bundle 6.6MB，慢链路下需 ≥3 分钟才渲染完,过早判定会误判成"没跳转";②**不要断言 `<form>` 元素**——identity 前端没有用 `<form>`（实测 `FORM_COUNT: 0`），判据要落在用户名/密码**输入控件**上。服务端侧可用 `curl -sSI "$PROD/api/v2/auth/zita-login?return_to=…"` 直查 302 的 `Location`,与浏览器互为佐证 |
| 2 | 在 IdP 页面输入测试账号凭据并完成授权 | 浏览器最终落在 `https://hub.lurus.cn/console/v2/dashboard`（允许中间经过 `/login` 一跳）；页面顶栏显示 `display_name`/用户名，其 title 属性含账号邮箱。**不要断言停在 `/oauth/oidc`**——那是旧版 OIDC-direct 流的落点，不是这条链路 |
| 3 | 复制浏览器 session cookie，`curl -sS -b "<cookie>" "$BASE/api/v2/auth/session-info"` | `200`，`success == true`，`data.id` / `data.username` 与测试账号一致。**不要断言 `authenticated` 字段**（不存在），也**不要断言 `data.tenant_slug` 非空**——该字段只有旧版 OIDC 回调会填，现行登录路径永远留空 |
| 4 | `curl -sS -b "<cookie>" "$BASE/api/v2/$TENANT_SLUG/user/me"`（用该账号**自己**的租户 slug） | `200`，`data.email` / `data.username` 与 IdP 账号一致，`data.role` 与预置角色一致（**没有被"择优"提权成 admin**） |
| 5 | `curl -sS -o /dev/null -w "%{http_code}\n" -b "<cookie>" "$BASE/api/v2/<另一个租户 slug>/user/me"` | **`403 TENANT_MISMATCH`**。这是"外部声明→本地身份不得跨租户顶替"的可证伪 oracle |
| 6 | 浏览器点顶栏"退出登录" | `POST /api/v2/auth/zita-logout` 返回 `200`，浏览器落在 `https://hub.lurus.cn/login` |
| 7 | 用登出前保存的旧 cookie：`curl -sS -o /dev/null -w "%{http_code}\n" -b old_cookies.txt "$BASE/api/v2/auth/session-info"` | `401`（服务端会话真的失效了，不只是前端清了 cookie） |
| 8 | **另开一个全新浏览器配置**，只注入这条过期 cookie（**不写 localStorage**），访问 `/console/v2/dashboard` | 被重定向到 `/login`。<br>⚠️ 必须用全新配置：SPA 守卫只看 localStorage，在原浏览器里复现"旧 cookie 能进"什么也证明不了 |
| 9 | （**仅当被测的是旧版 OIDC-direct 会话时**）`curl -sS -i -b "<cookie>" -X POST "$BASE/api/v2/oauth/logout"` | `302`，`Location` 含 `post_logout_redirect_uri=https%3A%2F%2Fhub.lurus.cn%2F`。该端点只在会话里带 `oauth_id_token` 时才生效，现行登录路径下不适用——不适用时记 **N/A** 而非 FAIL |

> **~~已知缺陷~~ 已修复（2026-08-31，随下一次镜像部署生效）**
> Settings 页"撤销会话"曾被服务端 `data.redirect = "/console/v2/login"` 送进 NotFound（v2 路由表无此路径）；服务端 hint 与前端回退现均改为 `/login`。**新镜像上验收改为正向断言**：撤销后落地 `/login` 且旧 cookie 对 `session-info` 401。旧镜像仍按缺陷单处理。

**回归防守**：OIDC 邮箱回退可跨租户顶替账号并主动择最高权限；cookie domain 配置错误导致 hub 域浏览器拒收 cookie（SSO 静默坏掉）；登出只清前端不撤服务端会话。

---

### TC-C3 — 兑换码：铸码 → 兑换 → 钱包精确对账 → 重放必须失败

- **env**：UAT · **优先级**：P1
- **串联功能点**：（租户 admin）铸码 → 用户钱包读数 → `POST /redeem` → `quota_added` 与钱包 delta 三方对账 → 铸码列表状态 → 重放 400 + 金额不动
- **前置**：§0.2.1 会话；一枚未使用的 32 位 `$CODE`（面额 `$CODE_QUOTA`，整数 quota 单位）
- ⚠️ **UI 与 API 的分工**：`/console/v2/redemption` 是**管理员铸码页**（只发 `POST /api/v2/:slug/redemptions {name,count,quota}`），**没有"输码兑换"输入框**；用户侧兑换只有 API `POST /api/v2/:slug/redeem`，字段名是 **`key`**。唯一的兑换 UI 在 `/console/topup`——2026-08-31 前它打到未注册路由（必 404），已修复为同一条 `/redeem`（见 TC-C1 的修复框）。

| # | 动作 | Oracle |
|---|------|--------|
| 1 | （租户 admin 会话）浏览器 `/console/v2/redemption` 铸一枚码，或 `curl -sS -b cookies.txt -X POST "$BASE/api/v2/$TENANT_SLUG/redemptions" -H 'Content-Type: application/json' -d '{"name":"acc-c3","count":1,"quota":500000}'` | `200`/`201`，返回码值（32 位，记 `$CODE`，面额 `$CODE_QUOTA=500000`）。非租户 admin 调用应 `403` |
| 2 | `curl -sS -b cookies.txt "$BASE/api/v2/$TENANT_SLUG/user/me"` | `200`，记 `$QUOTA_0 = data.quota`（原始整数单位） |
| 3 | `curl -sS -b cookies.txt -X POST "$BASE/api/v2/$TENANT_SLUG/redeem" -H 'Content-Type: application/json' -d '{"key":"'"$CODE"'"}'` | `200`，`success=true`，`data.quota_added == $CODE_QUOTA` |
| 4 | `curl -sS -b cookies.txt "$BASE/api/v2/$TENANT_SLUG/user/me"` | `data.quota == $QUOTA_0 + $CODE_QUOTA`，**逐单位相等**；同时 `data.quota − $QUOTA_0 == data.quota_added`（三方对账） |
| 5 | （**需租户 admin**）`curl -sS -b cookies.txt "$BASE/api/v2/$TENANT_SLUG/redemptions"` | `200`；`data.redemptions[]` 中末 4 位匹配 `$CODE` 的那一行满足：`status != 1`（1 = 可用；已用码不是字符串"已使用"）、`used_user_id == $UID`、`redeemed_time > 0`、`quota == $CODE_QUOTA`。<br>非 admin 会话此步 `403` ⇒ 记 **N/A**，改由步骤 6-7 证明消耗 |
| 6 | `curl -sS -i -b cookies.txt -X POST "$BASE/api/v2/$TENANT_SLUG/redeem" -H 'Content-Type: application/json' -d '{"key":"'"$CODE"'"}'` | **`400`**，`success=false`（如"兑换码已使用"）。不得是 `200` |
| 7 | `curl -sS -b cookies.txt "$BASE/api/v2/$TENANT_SLUG/user/me"` | `data.quota` 与步骤 4 的值**逐字节相同**（重放没有二次入账） |

> ⚠️ **不要用 `GET /api/wallet/info` 做对账**：UAT 的 bridge 会话没有 platform 账户，该接口走内部回退返回 **USD**（`user.Quota / QuotaPerUnit`），而兑换加的是**整数 quota 单位**，两边单位不同，等式必然对不上。要用它就必须换算成 `$QUOTA_0/$QPU + $CODE_QUOTA/$QPU` 并比到 6 位小数。

**回归防守**：兑换码重放二次入账（该修复曾在分支里躺了 6 周而主干上洞活着）；quota 单位与 USD 混算。

---

### TC-C4 — 全站金额换算统一走服务端单位价（legacy/v2 分裂回归防守）

- **env**：BOTH · **优先级**：P1
- **串联功能点**：`/api/status` 读 `quota_per_unit` → Dashboard 花费卡片 → Token 页额度列 → 语言切换往返 → 与原始整数值对账
- **前置**：已登录会话（UAT 用 TC-C1 的浏览器会话；PROD 用 TC-C2 的）；钱包/token 有非零额度
- ⚠️ **别用定价接口取单位价**：`GET /api/v2/:slug/pricing` 与 `GET /api/pricing` 返回的是 `model_ratio` / `model_price` / `group_ratio`，**没有** `quota_per_unit`。唯一真源是 `/api/status`（控制台自己也是把它存进 localStorage 再做除法的）。
- ⚠️ **别用 Settings 的 Billing 卡片**：它渲染的是 platform 钱包的 **CNY** 数字（`GET /api/v2/user/billing/summary`，走 `OIDCAuth`）。UAT 上 OIDC 关闭该调用必失败、卡片显示错误态；PROD 上它是人民币，和 `quota → USD` 换算量纲根本不同。

| # | 动作 | Oracle |
|---|------|--------|
| 1 | `curl -sS "$BASE/api/status" \| jq '.data.quota_per_unit'` | `200`，得到 `$QPU`（正整数）。**后续所有换算都用这个实测值，不写死 500000** |
| 2 | `curl -sS -b cookies.txt "$BASE/api/v2/$TENANT_SLUG/user/me"` | `200`，记 `data.used_quota`（原始整数）为 `$USED_RAW` |
| 3 | 浏览器打开 `/console/v2/dashboard`，读"总花费"卡片显示的美元数 `$USD_ZH` | `$USD_ZH × $QPU == $USED_RAW`，误差 ≤ 1 分。换算结果必须随实测 `$QPU` 变化，而不是套死倍率 |
| 4 | 顶栏切换语言到 English | 页面文案变英文（如 `Balance` 取代 `余额`）；同一张卡片的数字 `$USD_EN` 与 `$USD_ZH` **逐字符相同**（同一份余额只换文案，不重算金额） |
| 5 | 英文界面下打开 Token 页，读某个 token 的额度美元列 `$USD_TOKEN`；同时 `curl -sS -b cookies.txt "$BASE/api/v2/$TENANT_SLUG/tokens"` 读同一 token 的 `remain_quota` | `$USD_TOKEN × $QPU == remain_quota`，误差 ≤ 1 分。**Dashboard 与 Token 页对同一原始值必须换算出同一个数**（不再出现同一余额一页 $400 另一页 $200 的分裂） |
| 6 | 切回中文 | 文案恢复中文，两处金额与步骤 3/5 **完全一致**（往返切换金额不漂移） |

**回归防守**：v2 页面写死 `QUOTA_PER_USD` 而单位价服务端可配 ⇒ 同一余额两套界面差一倍（写路径同样受影响）；语言切换触发金额重算。

---

### TC-C5 — 跨租户隔离：换 slug 必须 403，且一个字节的他租户数据都不许吐

- **env**：UAT · **优先级**：P0
- **串联功能点**：正控（自己的 slug 200）→ 换 slug 的 `user/me` / `tokens` / `logs` 全部 403 → 浏览器改 localStorage 复现租户切换 → KPI 保持空
- **前置**：TC-C1 的浏览器会话与 `cookies.txt`；`$SLUG_B` = 当前用户无权限的另一个 slug。
  **【订正 2026-09-01】** UAT 上**确实有第二个租户** `switch`（`tenants` 表 slug=switch），**优先用它**——真实他租户的判别力强于不存在的 slug。两种都跑更好，但要注意二者**状态码不同**（见下）。
- ⚠️ **控制台 URL 里没有租户段**：v2 路由是扁平的（`/console/v2/dashboard` 等），API 路径里的 slug 来自 `localStorage.tenant_slug`。"改地址栏切租户"不是可执行动作。

| # | 动作 | Oracle |
|---|------|--------|
| 1 | `curl -sS -b cookies.txt "$BASE/api/v2/$TENANT_SLUG/user/me"` | `200`，`data.id == $UID`（正控：守卫放行了匹配的 slug）。<br>**不要断言 `data.tenant_slug`**——该响应白名单里没有这个字段，断言必然假红。需要 slug 回显就用 bridge 响应里捕获的值 |
| 2 | `curl -sS -i -b cookies.txt "$BASE/api/v2/$SLUG_B/user/me"` | **存在的他租户 → `403 TENANT_MISMATCH`；不存在的 slug → `404 TENANT_NOT_FOUND`**（`TenantSlugGuard`）。两者都可，**判据是响应体不含任何用户字段**（`email` / `id` / `username` 一个都不许出现）。<br>**【订正 2026-09-01】** 原文步骤 2-4 写死 403,与本场景前置里"用不存在的 slug 也成立"自相矛盾——照写死的 403 执行会把**正确的** 404 判成 FAIL |
| 3 | `curl -sS -i -b cookies.txt "$BASE/api/v2/$SLUG_B/tokens"` | **`403`（他租户）/ `404`（不存在）**；响应体不含任何 token 名称或 key 前缀 |
| 4 | `curl -sS -i -b cookies.txt "$BASE/api/v2/$SLUG_B/logs"` | **`403`（他租户）/ `404`（不存在）**；不返回任何日志行，**连 `total` 计数也不得透出真实值**（0 条数据泄露） |
| 5 | 浏览器 DevTools：`localStorage.setItem('tenant_slug','$SLUG_B')` 然后刷新 `/console/v2/dashboard`（这正是顶栏租户切换器做的事） | 所有 `/api/v2/$SLUG_B/*` 的 XHR 均 `403 TENANT_MISMATCH`；KPI 卡片停在空占位（`—`），页面上**不出现任何一行/一个数字属于 `$SLUG_B`**；不得出现"空白仪表盘伪装成加载成功" |
| 6 | 复原：`localStorage.setItem('tenant_slug','$TENANT_SLUG')` 并刷新 | 回到正常仪表盘（确认步骤 5 的拒绝是 slug 导致，不是会话坏了） |

**回归防守**：跨租户顶替；gin 静态段吃掉 `/:tenant_slug` 使整组鉴权不执行；"挂在 `/:tenant_slug` 下 ≠ 写的是租户数据"。

---

### TC-C6 — 失败调用的可观测性：已认证 + 非 429 的错误必须落日志

- **env**：UAT · **优先级**：P1
- **串联功能点**：建可用 token → 正常调用（正控）→ 打一个不存在的模型（404）→ 日志页/日志 API 出现该错误行 → 匿名失败**不**落日志（边界）
- **前置**：§0.2 全部；`ERROR_LOG_ENABLED` live = true（§0.2.7）
- 🔑 **错误日志闸的两个条件**：**已认证**（`c.GetInt("id") > 0`）**且非 429**（`middleware/utils.go:47-53`）。用"已删除的 key"去触发 401 是**测不到**这条日志的——token 找不到时 `id` 根本没被设置，那条 401 按设计不落行。

| # | 动作 | Oracle |
|---|------|--------|
| 1 | `curl -sS -b cookies.txt -X POST "$BASE/api/v2/$TENANT_SLUG/tokens" -H 'Content-Type: application/json' -d '{"name":"acc-c6","unlimited_quota":true}'` | `201`，记 `$TOKEN`、`$TOKEN_ID` |
| 2 | `curl -sS -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -X POST "$BASE/v1/chat/completions" -d '{"model":"'"$MODEL"'","messages":[{"role":"user","content":"hi"}],"max_tokens":5}'` | `200`（正控：token 可用、池已配） |
| 3 | `curl -sS -i -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -X POST "$BASE/v1/chat/completions" -d '{"model":"nonexistent-model-xyz","messages":[{"role":"user","content":"hi"}]}'` | **`404`**，`body.error.code == "model_not_found"`（未知模型探测返回 404 而不是 503）；**且不是 429** —— 这两条正是错误日志闸的入口条件 |
| 4 | 浏览器 Log 页按 `token_name=acc-c6` 过滤；或 `curl -sS -b cookies.txt "$BASE/api/v2/$TENANT_SLUG/logs?token_name=acc-c6"` | `200`；`data.logs` 中出现一行 `type == 5`，`model_name == "nonexistent-model-xyz"`。<br>⚠️ `GetLogsV2` **没有 `status` 过滤参数**（合法参数：`page/page_size/type/model_name/start_time/end_time/token_name/after_id/project_id`） |
| 5 | `curl -sS -b cookies.txt "$BASE/api/v2/$TENANT_SLUG/logs?token_name=acc-c6"`（新镜像）或 `"$BASE/api/log/self/?token_name=acc-c6"`（旧镜像） | `200`；同一行的 `other` 含 `"stage":"middleware"`（中间件阶段的拒绝确实被打上了来源标记）。2026-08-31 起 v2 用户路由带脱敏 `other`（`stage` 属用户可见键）；旧镜像 v2 无 `other`，只能走 v1 路由 |
| 6 | **边界（负控）**：`curl -sS -b cookies.txt -X DELETE "$BASE/api/v2/$TENANT_SLUG/tokens/$TOKEN_ID"`，然后用已删除的 `$TOKEN` 再发一次步骤 2 的调用 | 中转返回 `401`；日志里**不新增**对应行。这是**设计如此**（匿名调用者不落日志，避免任意 key 刷爆日志表），FAIL 的判据是"新增了行"而不是"没新增"。该次 401 的可追溯痕迹在治理审计事件 `ActionAuthFailed`（`GET /api/v2/admin/audit/events`，需 root）里 |

**回归防守**：`ERROR_LOG_ENABLED` 默认关导致零条错误日志；中间件拒绝与 pre-channel 错误不落日志；`max_tokens:-5` 这类客户端错误被措辞成上游故障的 500。

---

## 5. 阶段 5 — 治理与安全边界（TC-G）

> 本组验的是"**该拒的必须拒**"。除 TC-G3 外，其余场景对业务数据只读或只产生一次性测试痕迹。
> **bridge 预算提醒**：TC-G2/G3/G4/G7 都需要会话。全组**共用一份 `cookies.txt`**；确需重新 bridge 时注意 5 次/60s/IP 的上限。

### TC-G1 — internal API：只认 `X-API-Key` + scope 逐条执法

- **env**：BOTH · **优先级**：P0
- **串联功能点**：`/metrics` 边界 404 → 未认证管理面 401 → Bearer 头被无视（401）→ 有 key 但缺 scope（403）→ 有 scope 的同一把 key 能过（正控）
- **前置**：
  - **PROD**：`$INTERNAL_KEY` = `platform-core` key（scope 只有 `balance:write` / `user:delete` / `provisioning`，**没有** `user:read`、**没有** `admin`）。
  - **UAT**：`newhub_uat` 是全新库，**没有任何预置 internal key**，必须先用 root bridge 会话现场铸一把：
    ```bash
    curl -sS -b cookies.txt -X POST "$BASE/api/api-keys/" -H 'Content-Type: application/json' \
      -d '{"name":"gov01-probe","scopes":["balance:write","user:delete","provisioning"]}'
    ```
    → `data.key` 即 `$INTERNAL_KEY`（该组是 root-only）。不铸就跑，步骤 3-5 全部退化成 401 `Invalid or expired API key`，判别力归零。

| # | 动作 | Oracle |
|---|------|--------|
| 1 | `curl -s -o /dev/null -w "%{http_code}\n" "$BASE/metrics"` | **`404`**，无 Prometheus 文本（边缘 nginx 封堵；与 TC-S4 同判据，作为本组基线） |
| 2 | `curl -s -o /dev/null -w "%{http_code}\n" "$BASE/api/v2/admin/tenants"` | `401`（无凭据）。不得是 200，也不得是 500 |
| 3 | `curl -sS -w "\n%{http_code}\n" -H "Authorization: Bearer $INTERNAL_KEY" "$BASE/internal/user/1"` | **`401`**，body `message == "API key required"`。internal 鉴权**只读 `X-API-Key` 头**（`internal_api_auth.go:14`），Bearer 形式被完全无视——这是文档化的既定契约，不是缺陷 |
| 4 | `curl -sS -w "\n%{http_code}\n" -H "X-API-Key: $INTERNAL_KEY" "$BASE/internal/user/1"` | **`403`**（key 有效并通过鉴权，但缺 `ScopeUserRead`）。"有效的 key ≠ 全部权限" |
| 5 | **正控**：`curl -s -o /dev/null -w "%{http_code}\n" -X DELETE -H "X-API-Key: $INTERNAL_KEY" "$BASE/internal/user/999999999"` | `404`（`USER_NOT_FOUND`）——**明确不是 403**。证明步骤 4 的 403 是 scope 特异性的，而不是这把 key 全局无效或服务挂了。用不存在的 id 保证零副作用 |

**回归防守**：`/metrics` 公网可读（真实余额指标外泄）；把"有 key"当成"有全部权限"；文档写 `Authorization: Bearer` 导致调用方永远 401。

---

### TC-G2 — bridge 端点三态判别：PROD 不存在 / UAT 错 token 拒 / UAT 对 token 通

- **env**：BOTH · **优先级**：P1
- **串联功能点**：PROD 404（路由未注册）→ UAT 403（token 错）→ UAT 200 + Set-Cookie → 会话可用
- **前置**：`$E2E_BRIDGE_TOKEN`。**本场景消耗 2 次 bridge 预算**（5 次/60s/IP）。

| # | 动作 | Oracle |
|---|------|--------|
| 1 | `curl -s -o /dev/null -w "%{http_code}\n" -X POST "https://hub.lurus.cn/api/v2/bridge/exchange?token=x&user_id=1"` | **`404`**。PROD 未设 `E2E_BRIDGE_TOKEN`，该路由**根本没注册**（`api-v2-router.go:61-62` 条件注册）。**返回 403 或 200 都是 P0——意味着生产开了后门** |
| 2 | `curl -s -o /dev/null -w "%{http_code}\n" -X POST "https://test-newhub.lurus.cn/api/v2/bridge/exchange?token=wrong-token-xyz&user_id=$UID"` | **`403`**。是 403 不是 404 ⇒ 路由在 UAT 上确实注册了；不是 200 ⇒ token 校验生效。<br>⚠️ 必须用 **query 参数**传错 token；用 header 形式传会因为"缺凭据"而 403，无法区分"token 错"与"请求形状错"，断言空转 |
| 3 | `curl -sS -i -c cookies.txt -X POST "https://test-newhub.lurus.cn/api/v2/bridge/exchange?token=$E2E_BRIDGE_TOKEN&user_id=$UID"` | `200` + `Set-Cookie`（证明步骤 2 的 403 是 token 特异性的，不是端点坏了） |
| 4 | `curl -sS -w "\n%{http_code}\n" -b cookies.txt "https://test-newhub.lurus.cn/api/v2/$TENANT_SLUG/user/me"` | `200` + 真实用户 JSON（`data.id == $UID`）。三态链闭合：PROD 不存在 / UAT 有守卫 / UAT 守卫后可用 |

**回归防守**：测试后门被误带进生产镜像/配置；bridge token 校验被旁路。

---

### TC-G3 — 限流判别：web 限流与业务限流是两套独立计数器，429 形状不同

- **env**：UAT · **优先级**：P1
- **串联功能点**：建带 rpm 上限的 token → 打满 web IP 桶（空 body 429）→ 立刻发中转（不应被 web 桶拦）→ 中转连发触发业务 429（JSON body）
- **前置**：§0.2 全部（**尤其是 credit pool**，否则步骤 4 会被 402 假通过）；UAT web 限流 = **600 req / 180s / IP**
- ⚠️ **本场景会把执行机 IP 的 web 桶打满 180s**，请**放在整个测试计划的最后**执行，或换一条出口 IP。

| # | 动作 | Oracle |
|---|------|--------|
| 1 | 复用 `cookies.txt`（必要时按 §0.2.1 重新 bridge） | 会话可用 |
| 2 | `curl -sS -b cookies.txt -X POST "$BASE/api/v2/$TENANT_SLUG/tokens" -H 'Content-Type: application/json' -d '{"name":"gov03-token","unlimited_quota":true,"rate_limit_rpm":2}'` | **`201`**，`data.key` 匹配 `^sk-`（记 `$TOKEN`），`data.id` 为数字。<br>⚠️ `rate_limit_rpm` 必须显式给：默认 0 时业务限流**永远不触发**（`business_rate_limit.go:321,328`），步骤 5 会变成不可证伪的空断言 |
| 3 | `for i in $(seq 1 610); do curl -s -o /dev/null -w "%{http_code}\n" "$BASE/"; done \| sort \| uniq -c` | 前 ~600 次 `200`，之后出现 `429`。**web 限流的 429 响应体是空的**。<br>**【订正 2026-09-01】** 原文说 `GlobalWebRateLimit` "只写状态码"是**错的**：`rate-limit.go:61` 与 `:80` 两条 deny 分支**都先调** `setRateLimitResponseHeaders`(`:188-192`) 再 `c.Status(429)`，web 429 **同样带** `Retry-After` / `X-RateLimit-Limit: 600` / `X-RateLimit-Remaining: 0`（实测）。⇒ **"有没有限流头"不构成判别力**，见步骤 5 |
| 4 | 紧接着（同一 IP、web 桶仍处于耗尽状态）发一次中转：`curl -sS -i -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -X POST "$BASE/v1/chat/completions" -d '{"model":"'"$MODEL"'","messages":[{"role":"user","content":"hi"}],"max_tokens":5}'` | **不是空 body 的 429**，且响应上**没有** `X-RateLimit-*` 头 —— `200`（或上游错误）皆可。<br>⚠️ **不接受 402 作为通过依据**：零额度 token 或未配池同样给 402，那种"通过"是请求死在两个限流器之前 |
| 5 | 60s 窗口内快速连发 20 次同样的中转调用 | 至少 1 次 `429`，且**同时满足**：① body 是 **JSON** `{"error":{…}}`（步骤 3 的 web 429 body 为空）；② **`X-RateLimit-Limit` 等于该 token 的 `rate_limit_rpm`（2），而不是 web 桶的 600**。<br>**【订正 2026-09-01】** 原文用"带 `Retry-After` / `X-RateLimit-*` 头"作判别是**空断言**——两种 429 都带这些头（见步骤 3 订正）。真正的判别是 **body 形状 + limit 数值**：limit=2 证明拦它的是 `BusinessRateLimit` 的 token 计数器，limit=600 说明还是 web 桶 |

**回归防守**：web 按 IP 限流把中转流量误伤（症状：单跑绿、连跑白屏 429）；限流器挂在错误的位置导致 503 掩盖 429。

---

### TC-G4 — 错误日志闸：已认证 + 非 429 的失败必须持久化到 internal 日志

- **env**：UAT · **优先级**：P1
- **串联功能点**：铸 `log:read` internal key → 建可用 token → 打不存在的模型（404，非 429）→ internal 日志 API 读到 `type==5` + `stage=middleware`
- **前置**：§0.2 全部；`ERROR_LOG_ENABLED` live = true

| # | 动作 | Oracle |
|---|------|--------|
| 1 | （root bridge 会话）`curl -sS -b cookies.txt -X POST "$BASE/api/api-keys/" -H 'Content-Type: application/json' -d '{"name":"gov04-logread","scopes":["log:read"]}'` | `200`/`201`，`data.key` 记为 `$INTERNAL_KEY`。**UAT 默认没有任何 internal key，不铸就没法跑这一条** |
| 2 | `curl -sS -b cookies.txt "$BASE/api/v2/$TENANT_SLUG/user/me"` | `200`，确认 `data.id == $UID` |
| 3 | `curl -sS -b cookies.txt -X POST "$BASE/api/v2/$TENANT_SLUG/tokens" -H 'Content-Type: application/json' -d '{"name":"gov04-token","unlimited_quota":true}'` | **`201`**，记 `$TOKEN`。用 `unlimited_quota` 是为了**跳过 TokenAuth 阶段的 402**——那条分支按设计不写错误日志，会让步骤 5 找不到行并被误判成缺陷 |
| 4 | `curl -sS -w "\n%{http_code}\n" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -X POST "$BASE/v1/chat/completions" -d '{"model":"nonexistent-model-xyz","messages":[{"role":"user","content":"hi"}]}'` | **`404`**，`body.error.code == "model_not_found"`；**且不是 429**。这一次调用同时满足"已认证"与"非 429"，正是错误日志闸的字面入口条件 |
| 5 | `curl -sS -H "X-API-Key: $INTERNAL_KEY" "$BASE/internal/log/user/$UID"` | `200`；响应是**对象**（`{"data":[…],"total":N,"page":…,"per_page":…}`，**不是数组**）；`data[]` 中存在一条 `model_name == "nonexistent-model-xyz"` **且 `type == 5`** 且 `other` 含 `"stage":"middleware"` 的记录。<br>⚠️ 日志行上**没有** `status`/`error` 字段，判据只有整数 `type` |

**回归防守**：中间件拒绝与 pre-channel 错误静默丢弃（上线至今零条错误日志）；未知模型 503 掩盖真实原因。

---

### TC-G5 — CORS：非白名单来源的带凭据跨源请求必须被拒（**同源短路陷阱**）

- **env**：BOTH · **优先级**：P1
- **串联功能点**：白名单 origin 预检通过（正控）→ 非白名单 origin 预检拒绝 → 浏览器跨源带凭据 fetch 被拦 → 同源 fetch 成功（对照）
- **前置**：浏览器里已有 `$BASE` 的有效会话
- 🔴 **陷阱**：`gin-contrib/cors` 对 **`Origin == "https://" + Host`** 的请求**直接短路，不发任何 CORS 头**。用 `Origin: https://hub.lurus.cn` 去测 `hub.lurus.cn` 会看到"没有 `Access-Control-Allow-Origin`"，从而把配置正确的系统判成 FAIL。**正控必须挑一个与 host 不同的白名单 origin**：
  - PROD：`Origin: https://identity.lurus.cn`
  - UAT：`Origin: http://localhost:5173`（UAT 的 `ALLOWED_ORIGINS` 是 `https://test-newhub.lurus.cn,http://localhost:5173`，**不含** hub.lurus.cn）

| # | 动作 | Oracle |
|---|------|--------|
| 1 | `curl -sS -i -X OPTIONS "$BASE/api/v2/$TENANT_SLUG/user/me" -H "Origin: <上面对应环境的白名单 origin>" -H "Access-Control-Request-Method: GET"` | 响应头 `Access-Control-Allow-Origin` **等于**该 origin，且有 `Access-Control-Allow-Credentials: true` |
| 2 | `curl -sS -i -X OPTIONS "$BASE/api/v2/$TENANT_SLUG/user/me" -H "Origin: https://evil.example.com" -H "Access-Control-Request-Method: GET"` | **`403`**，且**没有**任何回显 `evil.example.com` 的 `Access-Control-Allow-Origin` 头 |
| 3 | 浏览器：在**另一个源**的页面（任意非 `$BASE` 的站点）DevTools Console 执行<br>`fetch('$BASE/api/v2/$TENANT_SLUG/user/me',{credentials:'include'}).then(r=>r.text()).then(t=>console.log('DATA',t)).catch(e=>console.log('BLOCKED',e))` | Console 打印 `BLOCKED …` 并伴随 CORS 策略报错，页面 JS 拿不到响应体。<br>**【订正 2026-09-01】** 原文还要求"DevTools 里该请求响应状态 403 且无 ACAO"——**自动化下拿不到**:被 CORS 拦截的响应不会上报给 CDP/Playwright,那一半必然取不到证。把它拆成两条独立证据:①浏览器侧只断言 `BLOCKED`；②**服务端侧用带同一 `Origin` 头的 curl 直查** `403` + `access-control-*` 头计数为 0。<br>⚠️ **不要断言"cookie 被发送了"**：session cookie 是 `SameSite=Lax`，跨站请求本就不会携带它。CORS 与 SameSite 是**两层独立防线** |
| 4 | 在 `$BASE` 自己的页面上执行同样的 fetch（相对路径） | Console 打印 `DATA` + 真实用户 JSON（`200`）——证明步骤 3 的拦截是 Origin 导致，不是会话失效 |

**回归防守**：CORS 白名单缺口被同源短路掩盖（"缺口不影响同源"曾被误判两次）；带凭据的跨源读取。

---

### TC-G6 — 公开面收口：自助注册结构性关闭 + 保留段不得遮蔽鉴权路由

- **env**：BOTH · **优先级**：P1
- **串联功能点**：`/api/status` 注册投影为关 → 注册端点结构性不存在 → `/switch` 静态段不得旁路 `/:tenant_slug` 的鉴权 → 未认证管理面 401
- **前置**：无（全部匿名探测）

| # | 动作 | Oracle |
|---|------|--------|
| 1 | `curl -sS "$BASE/api/status" \| jq '.data.registration, .data.login_methods.password'` | `registration.enabled == false`、`registration.mode == "closed"`；`login_methods.password.enabled == false` 且 `.registration_enabled == false`（`misc.go:60-117` 硬编码为关） |
| 2 | `curl -s -o /dev/null -w "%{http_code}\n" -X POST "$BASE/api/user/register" -H 'Content-Type: application/json' -d '{"username":"acc-probe","password":"x"}'` | **`404`**。这不是一个"配置开关关着"，而是**结构性缺席**——路由表里根本没有自助注册/密码登录 handler（`misc.go:53-59` 记录了这条 grep 契约）。**返回 200/400/401 都说明有注册面被重新引入，属 P0** |
| 3 | `curl -s -o /dev/null -w "%{http_code}\n" "$BASE/api/v2/switch/channels"` | `401` / `403` / `404` 均可，**绝不能是 `200`**。这条探测守的是：gin 的静态段 `/switch` 不得吃掉 `/:tenant_slug/channels` 这条带 `AdminAuth` 的路由从而使整组鉴权不执行。未带任何凭据拿到 200 = 鉴权被旁路 |
| 4 | `curl -s -o /dev/null -w "%{http_code}\n" "$BASE/api/v2/switch/presets"` | `200`（对照组：真正的公开路由确实公开，证明步骤 3 的非 200 不是整个 `/switch` 组挂了） |
| 5 | `curl -s -o /dev/null -w "%{http_code}\n" "$BASE/api/v2/admin/tenants"` | `401`（未认证的管理面） |

**回归防守**：`/switch` 静态段吃掉 `/:tenant_slug` 使整组鉴权不执行（曾实测出 200 vs 401 的分叉）；注册面被无意重新打开（UAT 曾暴露"注册是 DB option 默认开"的安检问题）。

---

### TC-G7 — 禁用/删除的 token 立即失效 + 兑换码重放守卫（会话维度）

- **env**：UAT · **优先级**：P1
- **串联功能点**：建可用 token → 中转 200（正控）→ 删除 → 同一请求 401 → 兑换 200 + 钱包 delta → 重放 400 + 钱包不动
- **前置**：§0.2 全部；**另一枚**未使用的 32 位 `$CODE`（一码一用，不能与 TC-M4/TC-C3 共用）

| # | 动作 | Oracle |
|---|------|--------|
| 1 | 复用 `cookies.txt` | 会话可用 |
| 2 | `curl -sS -b cookies.txt -X POST "$BASE/api/v2/$TENANT_SLUG/tokens" -H 'Content-Type: application/json' -d '{"name":"gov07-token","unlimited_quota":true}'` | **`201`**，记 `$TOKEN_ID = data.id`、`$TOKEN = data.key` |
| 3 | `curl -sS -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -X POST "$BASE/v1/chat/completions" -d '{"model":"'"$MODEL"'","messages":[{"role":"user","content":"hi"}],"max_tokens":5}'` | **`200`**（正控。若是 402，先回 §0.2.2 检查池；"402→401"的序列证明不了任何"禁用即时生效"） |
| 4 | `curl -sS -b cookies.txt -o /dev/null -w "%{http_code}\n" -X DELETE "$BASE/api/v2/$TENANT_SLUG/tokens/$TOKEN_ID"` | `200` |
| 5 | **逐字节重复步骤 3 的同一条请求** | **`401`**（或 403），`body.error.message` 含"无效的令牌"。同样的 token + 同样的 body，删除前 200 删除后 401 ⇒ 即时生效，无缓存宽限窗口 |
| 6 | `curl -sS -b cookies.txt "$BASE/api/v2/$TENANT_SLUG/user/me"` → 记 `$Q0`；<br>`curl -sS -b cookies.txt -X POST "$BASE/api/v2/$TENANT_SLUG/redeem" -H 'Content-Type: application/json' -d '{"key":"'"$CODE"'"}'` | 兑换 `200`，`data.quota_added == N`（`N > 0`）；随后 `GET user/me` 的 `data.quota == $Q0 + N`，**逐单位相等** |
| 7 | 同码再兑一次 | **`400`**，`success=false`；再读 `GET user/me`，`data.quota` 与步骤 6 后的值**逐字节相同**（delta == 0） |

> ⚠️ **不要用 `POST /api/v2/switch/redeem` 做这条**：那是匿名设备兑换端点，要求设备指纹（缺失即 400），默认**拒绝** `default` 租户的码，而且它会新开一个匿名用户+token 而不是给当前会话入账；它对已用码返回的是 **`200` + `success:false`**，用"不得是 200"当判据会把正确的守卫判成 FAIL。

**回归防守**：token 删除后因缓存仍可用；兑换码重放二次入账；用错误的端点做验收导致"正确行为被判 FAIL"。

---

## 6. 缺陷分级 & 回归

| 级别 | 定义 | 例 | 处理 |
|------|------|----|------|
| **P0 阻断** | 核心路径不可用 / 边界失守 | TC-S1、TC-S4（`/metrics` 200）、TC-M1、TC-C5、TC-G2 步骤 1 FAIL | 停测、立即报开发 |
| **P1 严重** | 主功能受损或有绕过 | 计价偏差、缓存双计、限流缺位、错误日志不落库 | 当日修 |
| **P2 一般** | 体验问题 | 死按钮（`/console/topup` 404）、跳转到不存在的页面、文案 | 排期 |
| **P3 建议** | 优化项 | — | 记录 |

**回归规则**：
1. 任何 P0/P1 修复后，至少回归**该场景所在的整组** + 阶段 1 冒烟全套。
2. **行为类修复必须有 live 探针**——判据要选"只有真跑起来才会出现的工件"（新增的日志行、变化的余额数字、新出现的计数），编译通过与单测绿都不构成验收依据。
3. 涉及金额的修复，回归时必须给出 **measured before / after** 两组数值，不接受"逻辑上应该对了"。
4. PROD 上线前，UAT 侧全套重跑一遍；附录 A 的三类 PROD-only 项在 PROD 单独确认。

---

## 7. 阶段 6 — 验收签字

| 阶段 | 场景数 | PASS | FAIL | BLOCKED | N/A | 结论 | 签字 / 日期 |
|------|--------|------|------|---------|-----|------|-------------|
> 首次执行的实测填表见 **§7.1 执行记录**；下表留白供后续轮次使用。

| 1 冒烟（TC-S1…S6） | 6 | | | | | | |
| 2 会话 & 租户（TC-C1、TC-C5、TC-C2） | 3 | | | | | | |
| 3 Token & 中转（TC-M1、TC-M5） | 2 | | | | | | |
| 4 计费（TC-M2、M3、M4、M6、TC-C3、TC-C4） | 6 | | | | | | |
| 5 治理 & 安全（TC-G1…G7、TC-C6） | 8 | | | | | | |
| **合计** | **25** | | | | | □通过 □有条件通过 □不通过 | |

**业务负责人**：________  **QA**：________  **开发对接**：________  **执行环境/日期**：________

---

### 7.1 执行记录

| 轮次 | 日期 | 环境/版本 | 结果 | 备注 |
|------|------|-----------|------|------|
| 首次全量 | 2026-09-01 | UAT `main-20260901-76eb587` + PROD 只读 | **21 PASS / 1 FAIL / 3 BLOCKED / 0 N-A**，P0=0 P1=0 ⇒ **有条件通过** | 唯一 FAIL = TC-M3（缺陷定级 P2:钱对报表错）。3 条 BLOCKED（TC-C2 / TC-C4 / TC-G1）全部卡在 PROD 凭据 + "PROD 只读"约束，非产品缺陷。另开出 P2×3 + P3×3 |
| 缺陷修复 | 2026-09-01 | PR #120 → UAT/PROD `main-20260901-cf9ba9e` | TC-M3 的两个根因已修并 live 复验 ⇒ **TC-M3 转 PASS** | 非流式 `/v1/messages` 的 `cache_read_input_tokens` 实测 3456（此前恒 0）；两种 wire 的日志 `prompt_tokens` 统一为原始值 3527；计费恒等式用**实测比率**复算 `939 × 0.135 = 126.765 → 127`，与日志 `quota` 逐位相等 |

> **待解阻（需 owner）**：① PROD IdP 侧已预配置的测试账号（解 TC-C2，连带解 TC-C4）；② PROD `platform-core` internal key，且 TC-G1 步骤 5 是 `DELETE` 方法，需**显式豁免"PROD 只读"**才能执行。

> 填表约定：`BLOCKED` = 环境/数据不具备（如 UAT 渠道池无缓存供应商），**不等于** FAIL，但必须写明阻塞原因与解阻条件；`N/A` = 该 env 结构性不适用。

---

## 附录 A — PROD-only（UAT 结构性测不了的三类）

UAT 是有意做了减法的隔离实例（OIDC 关、统一计费关、NATS 关）。下面三类**不是漏测，是 UAT 上没有被测对象**，必须在 PROD 单独确认。

> 🔴 **PROD 风险提示**：hub.lurus.cn 有真实客户数据。以下步骤**只做读操作与最小金额写操作**；建 token 用最小额度并在验完后删除；兑换码用最小面额；**不要**在 PROD 跑 TC-G3（会把出口 IP 的 web 桶打满）、不要跑任何批量/连发类步骤。

### A.1 OIDC SSO 全链

- **为什么 UAT 测不了**：`OIDC_ENABLED=false`，UAT 上唯一的登录方式是 bridge（PROD 上 bridge 路由根本不注册）。
- **PROD 验法**：执行 **TC-C2** 全部 9 步。重点三条：`/login` 无需点击自动跳 IdP；登录后落 `/console/v2/dashboard`；换 slug 的 `user/me` 必须 `403 TENANT_MISMATCH`（防跨租户顶替）。
- **风险**：登出步骤会真正终止该测试账号的会话；用专用测试账号，不要用运营在用的账号。

### A.2 统一计费钱包扣款对账

- **为什么 UAT 测不了**：统一计费开关关闭，UAT 只有自有配额账本，没有 platform 钱包这一侧。
- **PROD 验法**：
  1. 记录调用前 platform 侧钱包余额与 hub 侧 `GET /v1/billing/balance` 的 `balance_lb`。
  2. 用最小额度 token 打**一次**真实中转（`max_tokens` 取最小值）。
  3. 核对：hub 侧 `balance_lb` 下降的整数 quota 单位数 == 日志行的 `quota`；platform 侧钱包出现对应的 `pre_auth` → `settled` 记录。
- **风险与已知约束**：platform 钱包字段精度是 `numeric(14,4)`，**微额调用（如 4e-6 量级）会被截成 0**——这是有意设计，不要当成"漏扣费"缺陷开单。设计上是否要改属 owner 决策，不在 QA 判定范围。判定"扣款链路通不通"要选**金额足够大到不被精度吃掉**的一次调用。

### A.3 NATS 配额/用量事件

- **为什么 UAT 测不了**：UAT 未接 NATS。
- **PROD 验法**：打一次中转后，由运维在 NATS 侧确认对应的用量/配额事件已发布（主题与消费者以 `lurus/doc/coord/contracts.md` 为准）；同时核对 hub 侧 `/api/health` 与 `/metrics`（**只能由宿主 netdata 直连 NodePort 抓，不经 nginx**）里的相关计数变化。
- **风险**：事件消费者是下游服务，重复投递或补投可能影响下游账；**只观测、不重放**。

---

_维护：本文场景对齐 `internal/adapter/handler/router/*.go` 与 `deploy/k8s/r6-uat/`、`deploy/k8s/r6-stage/`。路由、错误码、环境变量变更后必须同步更新本文，并在变更 PR 里注明受影响的场景 ID。_
