# Lugo 组织 → newhub 租户投影（设计稿，cycle 12 / L9）

日期 2026-09-19。状态 **设计稿，未实现**。本轮（cycle 12）只落地 newhub 单方就能做的三道守卫（见 §1），
投影本体等 platform 侧端点落地后再开 lane（§5 草案）。

本文所有跨仓事实都标了取证路径；没有取证路径的一律写成"待确认"，不写成结论。

---

## 0. 一句话

newhub 保留自己的 `tenants` 表作为**投影**，新增一列可空唯一的 `platform_org_id`，
经 platform 的 internal capability API 读「账号属于哪个组织」来放置用户、并刷新 `name`/`status`；
**永不投影 slug**；feature flag 默认关；禁止共享库、禁止跨库直连
（`lurus.yaml:698-700` `cross_group_policy.data_access: "API-only. No direct DB access across product group boundaries."`）。

---

## 1. 本轮已落地的部分（不是投影，是投影的前置守卫）

| # | 守卫 | 位置 | 行为 |
|---|---|---|---|
| G1 | 组织一致性 | `internal/adapter/handler/oauth.go` `OIDCCallback` | 租户 `IDPOrgID` 与 ID token 的 `org_id` 都有值且不等 → 403 `TENANT_ORG_MISMATCH` + 审计 `auth.failed{reason:tenant_org_mismatch}` |
| G2 | 一处 claim 解析器 | `internal/adapter/middleware/oidc_auth.go` `ResolveConfiguredExtraClaims` + `oauth.go` `validateIDToken` | 浏览器回调也读 `OIDC_CLAIM_*`（此前只有 bearer JWT 路径读） |
| G3 | 占位符守卫 | `oauth.go` `orgIDIsBound` / `buildOIDCAuthURL` / `handler/tenant.go` `CreateTenant` | `*_PLACEHOLDER` 结尾的 org id：不发 `organization=` 参数、不参与 G1 比较、不允许新建 |
| G4 | 桥接座位上限 | `handler/zita_bootstrap.go` `autoCreateBridgedUser` + `repo.TenantHasFreeSeat` | 首登超 `tenants.max_users` → 403 `TENANT_SEAT_LIMIT` + 审计 `auth.failed{reason:tenant_seat_limit}` + `tenant_gate_total{outcome="seat_limit_denied"}`。闸在**唯一的 insert 上**，所以两个桥接入口（`ZitaBootstrap`、`ProvisionV2`）都受管；邀请码在闸之前**只 peek 不消费**（`repo.PendingInviteTenantID`），被拒的登录不烧码。控制台的 `user_count`（`GetTenantStats`、`GET /api/v2/admin/tenants/:id`）已换成同一个席位数，不再与闸分叉 |
| G5 | 租户生命周期闸 | `repo.TenantGate` + `middleware/auth.go` 三处 | 软删/不存在的租户：`TENANT_MISSING_MODE=observe`（默认）计数放行、`enforce` 403 |

G1/G3 的"不比较"分支就是为投影留的口子：生产两行租户（`default`、`switch`）的
`zitadel_org_id` 目前是 `ZITADEL_DEFAULT_ORG_ID_PLACEHOLDER` / `SWITCH_ORG_ID_PLACEHOLDER`
（`migrations/021_pg_baseline_gaps.sql:172`、`migrations/030_seed_switch_tenant_and_credit_pool.sql:71`），
投影落地前它们没有任何真实组织可绑。

**G1 今天的实际爆炸半径 = 0，明写出来免得被读成"已经守住了"**：真实部署今天只有这两行租户，
两行都带占位符，所以任何组织的账号沿 `default` 租户的登录 URL 进来**仍然会被自动开通进去**
——G1 一次都不会触发。它守的是"O-org 把占位符换成真组织 id 之后"的那天，以及任何新建的
真绑定租户（`CreateTenant` 自本轮起拒绝占位符，见 G3，所以新行只会是真绑定）。
这不是失败，是有意的分期；但读这份文档的人必须知道，"守卫在位"≠"今天有东西被守住"。
**怎么知道哪天开始真的守住了**：`lurus_gateway_tenant_gate_total` 的四个 org 结局
（`org_claim_absent` / `org_tenant_unbound` / `org_matched` / `org_mismatch_denied`）
逐次登录计数 —— `org_claim_absent` 独大 = IdP 根本不发 org claim（O-org 第 3 问的答案）；
`org_tenant_unbound` 独大 = claim 有了但租户还是占位符（O-org 第 2 问）；
两者归零、`org_matched` 开始增长 = G1 从此真的在守。

---

## 2. 两侧现状（取证）

### 2.1 platform（Lugo）侧

| 事实 | 取证 |
|---|---|
| `identity.organizations(id BIGSERIAL, name, slug UNIQUE, owner_account_id, status DEFAULT 'active', plan DEFAULT 'free')` | `2l-svc-platform/migrations/007_organizations.sql:6-14` |
| `identity.org_members(org_id, account_id, role DEFAULT 'member')` | 同上 `:18-22` |
| `identity.org_api_keys`、`billing.org_wallets(org_id PK, balance…)` | 同上 `:28-37`、`:42-48` |
| 公开 API `GET /api/v1/organizations`（**当前用户**所属组织，bearer/cookie 鉴权） | `2l-svc-platform/api/openapi.yaml:3366,3394-3406` |
| `Organization` 响应体 = `{id:int64, name, slug, status}` | 同上 `:1119-1125` |
| 已有 internal 组织端点：`/internal/v1/orgs/{id}/subscriptions/issue`、`/internal/v1/orgs/resolve-api-key`、`/internal/v1/orgs/{id}/services/kova-tester`(+`/revoke`) | 同上 `:4343,4382,4407,9455` |
| internal 账号读端点的鉴权形状：`security: [{internalKey: []}]` + `x-required-scope: account:read` | 同上 `:3661-3676`（`/internal/v1/accounts/by-id/{id}`） |
| **按账号列组织的 internal 端点不存在** | 2026-09-19 实跑三种拼法全 0 命中：`grep -c "accounts/{id}/organizations" api/openapi.yaml`（spec 形态）= 0；`grep -rn "accounts/:id/organizations" --include=*.go`（gin 路由形态，排除 worktrees）无输出；`grep -rn "accounts/%d/organizations" --include=*.go`（调用方 fmt 形态）无输出。三种之外的写法（如按 query 参数过滤的 `/internal/v1/organizations?account_id=`）没查，以 owner 复核为准 |

### 2.2 newhub 侧

| 事实 | 取证 |
|---|---|
| `tenants.zitadel_org_id`（Go 字段 `IDPOrgID`）size 128、**unique not null**、有索引 | `internal/domain/entity/tenant.go:18` |
| 物理列名保留 `zitadel_org_id`，改名要预留 migration ID（owner-gated） | 同上 `:11-17` 的 TODO |
| OIDC 自动建租户按 `claims.OrgID` 走 `repo.CreateTenantFromIDP` | `internal/adapter/handler/oauth.go` `OIDCCallback` 的 `OIDC_AUTO_CREATE_TENANT` 分支 |
| 会话桥接路径（v2 控制台真实登录路径）只认邀请码，否则 `default` | `internal/adapter/handler/zita_bootstrap.go` `resolveInviteTenant` / `autoCreateBridgedUser` |
| newhub → platform internal 调用的既有形状：`IdentityServiceURL + /internal/v1/...` + `IdentityServiceInternalKey` | `internal/pkg/common/billing_client.go:95,149,182`、`internal/pkg/common/identity_client.go:15-26` |
| 账号解析已有 gRPC 腿 + HTTP 回退 | `common.GetAccountByZitadelSubGRPC`（`oauth.go` 回调里调用） |

**结论**：两边的「组织」是两套主键（platform `BIGSERIAL`，newhub 的 IdP org id 字符串）。
投影不是改主键，是加一列。

---

## 3. 投影模型

### 3.1 列

```sql
-- migration ID 先在 lurus/doc/coord/migration-ledger.md 预留（本文不预留，owner 事项）
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS platform_org_id BIGINT;
CREATE UNIQUE INDEX IF NOT EXISTS uk_tenants_platform_org_id
    ON tenants (platform_org_id) WHERE platform_org_id IS NOT NULL;
```

- **可空**：未绑定组织的租户（含目前两行占位符租户）保持 NULL，一切行为不变。
- **部分唯一索引**：一个 Lugo 组织最多映射一个 newhub 租户；NULL 不参与唯一。
- 与 `zitadel_org_id` 并存：前者是 **platform 的组织主键**，后者是 **IdP 的组织标识**。
  两者都可为空/占位；哪一个先有值取决于组织是先在 platform 建还是先在 IdP 建。

### 3.2 投影什么，不投影什么

| 字段 | 投影？ | 理由 |
|---|---|---|
| `name` | ✅ 覆盖 | 展示字段，platform 是真源 |
| `status`（`active`/`suspended`） | ✅ 映射到 `tenants.status`（`active→1`，`suspended→3`） | 组织停用必须到达网关，否则「在 Lugo 停掉」是假的 |
| `slug` | ❌ **永不投影** | newhub 的 slug 是路由键：`/api/v2/:tenant_slug/*` 与 switch 的分销路由都按它走（`middleware.TenantSlugGuard` → `repo.GetTenantBySlug`）。platform 改名会当场把所有已发出的控制台 URL 打 404。改 slug 只能是 newhub 侧的显式管理操作 |
| `plan`（free/team/enterprise） | ⚠️ 只读展示，不驱动 `plan_type` | newhub 的 `plan_type` 参与配额/席位口径；两套计划名对齐前不能自动覆盖（owner 决定映射表） |
| 钱包余额 | ❌ | 钱路由既有的 pre-auth/settle 链负责，投影不碰钱 |

### 3.3 用户放置

新增一条解析顺序，只在**首次登录**（`ZitaBootstrap` 的 auto-create 分支）生效：

1. `?invite=<code>` 命中 → 邀请绑定的租户（现状，优先级最高，显式意图优先于推断）
2. flag 开启且 platform 返回该账号的组织列表：
   - 恰好 1 个组织 → 该组织映射的租户；租户不存在则**不自动建**（第一版；见 §6 风险 R3）
   - 0 个 / ≥2 个组织 → 回落 `default`，并记一条 SysLog + 指标（歧义必须可见，不能静默挑一个）
3. 其余 → `default`（现状）

**已有用户的租户永不因投影改变**：换租户会把它的 token、日志、额度池归属全部改写，只能是显式管理操作。

### 3.4 刷新方式

第一版 **轮询**，不是事件：

- leader-only 后台任务（`lifecycle.Manager` + `TickerTask`，与既有 master-only 任务同一机制），周期 `LUGO_ORG_SYNC_INTERVAL`（默认 300s）。
- 每轮：取 `platform_org_id IS NOT NULL` 的租户 → 批量问 platform → 只写有差异的 `name`/`status`。
- 失败：整轮放弃并计数（`lurus_gateway_lugo_org_sync_failures_total`），**不写半个结果**，不改任何行。
- 选轮询不选 NATS 事件的理由：newhub 的 NATS egress 已被 NetworkPolicy 收窄且 UAT 关闭，
  事件链多一个可静默断掉的环节；一个 5 分钟窗口的投影延迟对 name/status 是可接受的，
  对「停用要立刻到达」不可接受 → 所以 **status 同时在登录路径上按需拉一次**（§3.5）。

### 3.5 登录路径上的按需校验

`ZitaBootstrap` / OIDC 回调在 flag 开启时，对**已绑定组织**的租户做一次 platform 查询（走既有 breaker + 超时预算）：
组织 `suspended` → 拒绝登录（与 `tenant.IsDisabled()` 同一形状 403 `TENANT_DISABLED`）。
platform 不可达 → **放行**并计数（fail-open，与本轮 `TenantGate` 的姿态一致：身份平台抖动不能锁死登录）。

---

## 4. 需要 platform 新增的端点（契约原文，owner 事项 O-org）

```yaml
  /internal/v1/accounts/{id}/organizations:
    get:
      tags: [Internal]
      x-audience: internal
      operationId: listAccountOrganizations
      summary: List the organizations an account belongs to
      security: [{ internalKey: [] }]
      x-required-scope: account:read          # 复用既有 scope，不新增
      parameters: [{ $ref: '#/components/parameters/AccountIDPath' }]
      responses:
        '200':
          description: Organizations this account is a member of (may be empty).
          content:
            application/json:
              schema:
                type: object
                properties:
                  organizations:
                    type: array
                    items: { $ref: '#/components/schemas/Organization' }   # {id,name,slug,status}
        '404': { $ref: '#/components/responses/NotFound' }   # 账号不存在
```

要点：

- **复用 `account:read`**，不新增 scope；线上那把 key `platform-core` 的 scope 是否含 `account:read` 需 owner 确认
  （newhub 侧调用方读 `IDENTITY_SERVICE_INTERNAL_KEY`，`internal/pkg/common/identity_client.go:26`）。
- 返回体逐字复用既有 `Organization` schema（`api/openapi.yaml:1119-1125`），不发明字段。
- 成员角色（`org_members.role`）**不需要**：newhub 不做组织内角色映射（见 §6 R4）。
- 注册流程：改前查 `lurus/doc/coord/contracts.md` 的消费者，改后更新 contracts + service-status + changelog
  （`lurus.yaml:700`）。

---

## 5. 下一轮 lane 草案

| 步 | 内容 | 依赖 |
|---|---|---|
| P1 | migration：`tenants.platform_org_id` + 部分唯一索引（ID 先在根 ledger 预留） | 无 |
| P2 | `repo` 层：`GetTenantByPlatformOrgID` / `BindTenantPlatformOrg`（一次性绑定，已绑不改绑，镜像 `LinkUserPlatformAccount` 的 never-re-bind 语义） | P1 |
| P3 | platform 客户端：`common.ListAccountOrganizations(ctx, accountID)`，超时进既有预算、挂既有 breaker | platform 端点上线 |
| P4 | `ZitaBootstrap` 放置顺序（§3.3）+ 歧义指标 | P2 P3 |
| P5 | leader-only 刷新任务（§3.4）+ 失败计数 | P2 P3 |
| P6 | 登录路径 status 校验（§3.5） | P3 |
| P7 | 管理端：`PUT /api/v2/admin/tenants/:id/platform-org`（显式绑定/解绑，审计） | P2 |

flag：`LUGO_ORG_PROJECTION_ENABLED`，默认 `false`；两份 manifest 先不设。
每步的 oracle 都要是「真 HTTP 往返 + 变异实测」，不接受手搭结构体的绿。

---

## 6. 风险表

| # | 风险 | 后果 | 对策 |
|---|---|---|---|
| R1 | slug 被投影 | 已发出的控制台 URL 与 switch 分销路由当场 404 | §3.2 硬规则：永不投影 slug；P1 的 migration 不含 slug 列写路径，闸门断言刷新任务的 UPDATE 列集合 ⊆ {name,status,updated_at} |
| R2 | 组织→租户 1:N 或 N:1 | 用户被放进错租户，钱记到别人头上 | 部分唯一索引挡 N:1；≥2 组织的账号回落 `default` 并计数，不猜 |
| R3 | 自动建租户 | 一个组织成员的第一次登录就凭空造一个租户（slug 从哪来？和既有 slug 撞了怎么办） | 第一版**不自动建**：未映射组织 → `default` + 计数；建租户仍是管理操作（`CreateTenant`，已有保留 slug 校验 `ValidateTenantSlug` 与本轮的占位符校验） |
| R4 | 角色投影 | 组织 owner 自动变成 newhub root，等于把 IdP 的角色模型嫁接到网关的权限模型 | 不投影角色。newhub 的 role 只由 newhub 管理端改 |
| R5 | platform 不可达 | 登录/首登被身份平台抖动阻断 | 全部 fail-open + 计数（§3.5）；刷新任务失败整轮放弃 |
| R6 | 投影写回 platform | 两边互相覆盖 | 单向：platform → newhub。newhub 侧任何组织字段的修改都不回写 |
| R7 | `plan` 自动映射 | 两套计划名口径不同，配额/席位被静默改写 | 只读展示，不驱动 `plan_type`（§3.2） |
| R8 | 生产 org id 仍是占位符 | 投影对象不存在 | 绑定是显式的 `platform_org_id`，与 `zitadel_org_id` 解耦；占位符行走 §1 G3 的不比较分支 |
| R9 | 席位上限是"读后写"不是"预留" | 同一租户两个首登并发时都读到 `used == max_users-1`，两个都放行，超卖 N=并发数 | 记录不修（cycle 12 决定，下一轮做 DB 级守卫：`users(tenant_id)` 上的计数式 `UPDATE … WHERE seats < max_users` 或部分唯一索引）。上限是**套餐限额不是安全边界**，超卖数量有界且在控制台席位数上可见（本轮已把控制台的 user_count 换成同一个席位数，见 §1 G4） |

---

## 7. 待确认（owner）

1. 线上 internal key `platform-core` 是否含 `account:read` scope。
2. 生产 `tenants.zitadel_org_id` 是否仍是 `*_PLACEHOLDER` 两行。
3. IdP 是否发 `org_id` claim、是否认 authorize 的 `organization=` 参数
   （不发/不认时 §1 G1 永不触发，是"守卫在位但无事可守"，不是"守卫坏了"）。
4. platform `plan`（free/team/enterprise）与 newhub `plan_type` 的映射表。
5. platform 侧新端点的排期（§4）。
