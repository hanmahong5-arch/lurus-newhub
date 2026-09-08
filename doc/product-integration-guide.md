# 产品接入指南 — Lurus 统一登录平台

> v1.0 (2026-02-10) · 反馈 support@quantumnous.com · 文档 https://docs.lurus.cn

Lurus 平台提供:统一身份认证 (Zitadel OAuth2/OIDC) + AI 网关 (OpenAI 兼容 API) + 集中计费 (额度/订阅)。接入后获得:一次注册全产品通用 SSO、统一 OpenAI 格式调用、自动扣费 + 自助充值、用量监控 + Token 管理。

## 架构

登录流程: 产品前端 → `https://api.lurus.cn/login/{product-slug}` → Zitadel 登录页 → OAuth 授权 → Lurus 回调 (创建/关联租户 + 分配 Session) → Lurus 控制台 → 用户创建 API Token (`sk-xxx`) → 配置到产品后端 → `POST /v1/chat/completions`。

数据隔离: 用户账号**共享** (SSO);API Token / 使用日志 / 额度计费 **按产品 (tenant) 隔离**;AI 渠道配置共享 (可选按产品定制)。

## 接入步骤

**1. Zitadel 创建 Application** (https://auth.lurus.cn/ui/console,账号联系管理员)
- Organization `lurus` → Applications → New
- Name `{产品英文名}`(如 `product-b`) · Type `Web` · Auth Method `PKCE`
- Redirect URI: `https://api.lurus.cn/api/v2/oauth/callback`
- Post Logout Redirect URIs: `https://api.lurus.cn`、`https://{产品域名}`
- Grant Types: Authorization Code + Refresh Token
- 记录 **Client ID**(如 `234567890123456789@lurus`)

**2. Lurus 注册租户** — 提供给管理员: slug(小写字母/数字/连字符)、name、zitadel_org_id、zitadel_client_id、admin_email。管理员执行:

```sql
INSERT INTO tenants (slug, name, zitadel_org_id, zitadel_client_id, status, created_at)
VALUES ('product-b', 'Product B', '{org_id}', '{client_id}', 1, NOW());
INSERT INTO tenant_admins (tenant_id, user_email, role)
VALUES ((SELECT id FROM tenants WHERE slug = 'product-b'), 'admin@product-b.com', 'owner');
```

完成后获得登录入口 `https://api.lurus.cn/login/product-b`。

**3. 产品后端环境变量**:
```bash
LURUS_API_BASE_URL=https://api.lurus.cn
LURUS_API_KEY=sk-xxxxxxxxxxxx   # 从控制台获取
```

## 前端集成 (三种方式)

- **方式 1 直接链接(推荐)** — `<a href="https://api.lurus.cn/login/product-b">使用 Lurus 账号登录</a>`。无需代码;用户登录后进 Lurus 控制台,手动复制 Token。
- **方式 2 嵌入式** — 不跳转控制台,在产品内完成登录。`handleLogin` 存 `return_url` 后 `window.location.href = '.../login/product-b'`;登出 `POST /api/v2/oauth/logout` (credentials:'include');会话检查 `GET /api/v2/auth/session-info` (credentials:'include',返回 `data.success && data.data.id`)。
- **方式 3 回调页面(自动取 Token)** — 管理员 `UPDATE tenants SET custom_redirect_url='https://yourapp.com/auth/callback' WHERE slug='product-b'`;回调页用 URL `token` 参数 `POST /api/v2/product-b/auth/exchange-token {temp_token}` 换取 `api_key`,存 localStorage 后跳回 `return_url`。

## 后端集成

OpenAI SDK 兼容 — 仅改 `base_url`。Python:
```python
from openai import OpenAI
client = OpenAI(api_key=os.getenv("LURUS_API_KEY"), base_url="https://api.lurus.cn/v1")
resp = client.chat.completions.create(model="gpt-4o", messages=[...], temperature=0.7)
```

Node.js / Go: 标准 HTTP `POST {LURUS_API_BASE}/v1/chat/completions`,Header `Authorization: Bearer ${LURUS_API_KEY}` + `Content-Type: application/json`,body `{model, messages, temperature}`,读 `choices[0].message.content`;非 2xx 时读 `error.message`。

## 测试验证

```bash
# 登录: 访问 https://api.lurus.cn/login/product-b → 输入测试账号 → 跳转 https://api.lurus.cn/console
# AI 调用:
curl -X POST https://api.lurus.cn/v1/chat/completions \
  -H "Content-Type: application/json" -H "Authorization: Bearer sk-xxxxxxxxxxxx" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"test"}]}'
# 返回 chat.completion + usage{prompt_tokens, completion_tokens, total_tokens}

# 用量查询:
curl https://api.lurus.cn/api/v2/product-b/user/me -H "Authorization: Bearer sk-xxxxxxxxxxxx"
# 返回 quota / used_quota / remaining_quota / daily_quota{limit,used,remaining} / subscription{plan_code,status,expires_at}
```

## 常见问题

- **Q1 登录后看不到我的产品?** Lurus 是 AI 网关不是产品平台。用户登录→建 Token→配置到产品。无缝体验用方式 3。
- **Q2 多产品数据会混吗?** 不会。账号 SSO 共享,但每产品独立 `tenant_id`,Token 绑定租户,日志/计费按租户隔离,A 产品 Token 不能在 B 用。
- **Q3 为产品配专属模型?** 联系管理员 `INSERT INTO tenant_channels (tenant_id, channel_id, priority) VALUES (...)`。
- **Q4 额度不足?** 两处独立的额度闸,响应不同:(a) 钱包/Token/租户资金池本地额度耗尽 → 402,OpenAI 线 `type=insufficient_quota`/Anthropic 线 `type=billing_error`,`code` 视具体原因为 `insufficient_user_quota` / `token_quota_exhausted` / `pool_exhausted`;(b) 平台侧 entitlement 校验(按 `X-Lurus-Product` 归属的产品配额)拒绝 → 429,OpenAI 线 body 为 `{"error":{"message","type":"rate_limit_error","code":"quota_exceeded","metadata":{"upgrade_url":...}}}`;Claude/Gemini 调用方走各自原生信封(`{"type":"error","error":{...}}` / `{"error":{...}}`),两者均**不带** `upgrade_url`(信封结构没有 metadata 位置),只能靠 message 文案里的 `quota_exceeded` 判定,不要依赖顶层 `upgrade_url` 字段。都提示用户去钱包充值或订阅。
- **Q5 限制调用频率?** Lurus 内置 `daily_quota` 日限额;或产品侧自实现限流。
- **Q6 白标?** 当前不支持完全白标,可:自定义 Zitadel 登录页主题、用方式 3、隐藏控制台 (Token 自动管理)。
- **Q7 支持哪些模型?** 列表 https://api.lurus.cn/console/models。常用: OpenAI gpt-4o/gpt-4o-mini、Anthropic claude-3-5-sonnet、Google gemini-1.5-pro、国内 qwen-max/glm-4/deepseek-chat。
- **Q8 技术支持?** 邮箱 support@quantumnous.com · 文档 https://docs.lurus.cn · 企业微信群(联系管理员)。

## 附录

### A. API 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/v1/chat/completions` | POST | 对话模型 |
| `/v1/embeddings` | POST | 文本向量化 |
| `/v1/images/generations` | POST | 图片生成 |
| `/v1/audio/transcriptions` | POST | 语音转文字 |
| `/v1/key` | GET | 只持一把 key 查自身额度/限流/所属租户资金池状态,无需控制台权限(详见 §F) |
| `/v1/generation` | GET | 按 `id`(己方 X-Request-Id)反查一次调用的费用/供应商/用量(详见 §F) |
| `/api/v2/{tenant}/user/me` | GET | 用户信息 |
| `/api/v2/{tenant}/tokens` | GET / POST | 查询 / 创建 Token |
| `/api/v2/{tenant}/logs` | GET | 使用日志 |
| `/api/v2/{tenant}/billing/topup` | POST | 发起充值 |

### B. 错误码

2026-09-08 起,网关自身发起的拒绝(中间件层 401/402/403/429/5xx、`/v1/dashboard/*` 读库失败等)不再用 `new_api_error` 占位 `error.type`——`type` 改按调用方使用的 wire 语义映射(OpenAI 语义 vs Anthropic 语义,402/503/529 三处分叉),`code` 改为跨 wire 稳定的机器可读值,两者都不再为空。上游供应商自身返回的错误(`upstream_error`)不受影响,原样透传。仅有的例外是 Midjourney wire(`/mj/*`)的 `abortWithMidjourneyMessage`:该 wire 的官方信封没有稳定 code 字段位置,`type` 沿用 `new_api_error` 字面量,是本表以外唯一允许出现该字面量的位置。

| HTTP | OpenAI 线 `type` | Anthropic 线 `type` | `code`(稳定,跨 wire 相同) | 说明 | 处理 |
|------|------|------|------|------|------|
| 400 | `invalid_request_error` | `invalid_request_error` | `invalid_request` 等 | 请求本身有问题(缺模型名/渠道 id 格式错/请求体解析失败) | 检查请求体 |
| 401 | `authentication_error` | `authentication_error` | `invalid_request` / `session_required` / `token_disabled`(令牌被禁用,主鉴权路径) | Key 无效、未登录、令牌被禁用 | 检查 Token / 重新登录 / 在令牌管理里启用或换一把 |
| 402 | `insufficient_quota` | `billing_error` | `insufficient_user_quota` / `token_quota_exhausted` / `pool_exhausted` / `pool_not_configured`(租户信用池未配置,`CREDIT_POOL_REQUIRED=enforce` 时) / `tenant_quota_exceeded`(租户月度配额超限) | 钱包/Token/租户资金池额度不足 | 提示充值,`metadata.topup_url` 见 §F |
| 403 | `permission_error` | `permission_error` | `model_blocked` / `group_not_allowed` / `ip_not_allowed` / `channel_specify_forbidden` / `scope_not_granted` / `user_banned` / `tenant_suspended` / `token_disabled`(令牌被禁用,playground 鉴权路径) | 模型/分组/IP/scope 未授权,账号或租户被封禁,或(playground 路径)令牌被禁用 | 按 `code` 定位具体原因,联系管理员放开 |
| 404 | `not_found_error` | `not_found_error` | `model_not_found` | 模型未配置任何可用渠道(区别于"渠道全部暂时不可用"的 503) | 换模型 |
| 413 | `request_too_large` | `request_too_large` | `read_request_body_failed` | 请求体超限 | 缩小请求 |
| 429 | `rate_limit_error` | `rate_limit_error` | `request_rate_limit_exceeded` / `quota_exceeded` / `cost_spike_limit_exceeded` / `business_rate_limit_exceeded` / `concurrency_limit_exceeded` 等 | 限流(见下方 Q4 的另一类 429) | 稍后重试,读 `Retry-After`/`X-RateLimit-*`(见 §E,并非全部 429 都携带 —— `quota_exceeded` 与 `cost_spike_limit_exceeded` 也带 `X-RateLimit-Scope`/`Type`,见 §E 表) |
| 500 | `api_error` | `api_error` | `gateway_internal` | 网关自身处理失败(非上游供应商故障) | 重试;持续出现联系运维 |
| 500 | `upstream_error` | `upstream_error` | 供应商原样透传 | AI 服务商故障 | 重试 / 切模型 |
| 503 | `api_error` | `overloaded_error` | `channel:all_keys_cooling` / `model_not_found`(无可用渠道时复用此状态码) 等 | 模型配置存在但渠道暂时全部不可用/维护中 | 等待恢复,读 `Retry-After` |

完整 `code` 枚举(所有网关自身可能返回的机器码,不含上游供应商透传值)见 `docs/openapi/relay.json` 的 `components.schemas.GatewayError.code.enum`,由 CI 锁与 `internal/pkg/types` 的 `ErrorCode` 常量表逐条互校,新增/改名任一侧都会挂红。

### B2. 账号绑定与计费归属(2026-09-07 起)

- 用户经 OIDC 浏览器登录或首个 JWT 请求时,hub 把平台账号写入 `users.lurus_account_id`(只写一次;已绑定不同账号的用户跳过并记日志)。
- **之后**铸造的令牌带 `identity_account_id`,统一计费开启时按平台钱包预授权/扣款;**绑定之前**已存在的令牌保持本地额度计费,管理员设的令牌上限不变。同一用户因此可能同时存在两种计费制:对账时按令牌的 `identity_account_id` 区分。
- 钱包治理的令牌不再消耗用户的本地欢迎额度;钱包余额不足直接拒绝,不回落到本地额度。

### C. Webhook 通知 (可选)

提供 Webhook URL 接收用户充值/订阅事件:`POST https://yourapp.com/webhooks/lurus`,body `{event, tenant_slug, user_id, data{amount, quota_before, quota_after, timestamp}, signature: "sha256=..."}`(signature 验真实性)。

### D. 跨产品归因 (`X-Lurus-Product`)

兄弟产品经 hub 中转时在每个请求带 `X-Lurus-Product: <product>` 头,值须在服务端 allow-list 内(`llm-api` 默认、`kova`、`lutu`、`lucrum`、`switch`、`creator`、`memorus`、`tally`);未知或缺省值**静默**折算为默认产品,不报错、不回显。归因贯穿三处:

| 落点 | 说明 |
|------|------|
| 平台钱包 `WalletDebit` / `WalletPreAuthorize` 的 `product_id` | 钱按产品入账;`ReportUsage` 无该字段(proto 待补) |
| 日志行 `other.source_product` | 成功行、handler 层与中间件层的错误行都带;归因早于任何模型/渠道解析,绑定失败也能落对产品 |
| `GET /api/v2/{tenant}/logs?source_product=switch`、`GET /api/v2/{tenant}/logs/stat?source_product=switch` | 普通用户即可查自己产品的份额,无需 root。`logs/stat` 的窗口总量与每个 `by_product` 行都带 `cache_read_tokens` / `cache_write_tokens`(prompt-cache 命中/创建的 token 数,汇总自日志行的 `other.cache_tokens` / `other.cache_creation_tokens`) |
| `/metrics` 的 `relay_requests_total` / `relay_errors_total` / `relay_total_duration` | 三者均带 `product` label;`billing_debit_amount_cny` 带 `product,op` label。仅宿主 netdata 直连抓取,不对公网/兄弟产品开放读取权限 |
| `/metrics` 的 `lurus_gateway_relay_time_to_first_token_seconds` | 带 `provider,model,product` label 的直方图;仅流式请求且真正收到过上游首个 token 时才有一次观测。非流式请求与在收到上游首个 token 之前就失败的请求不贡献样本;已经收到首个 token 之后才失败或客户端断开的流式请求仍计入一次观测。OpenAI Realtime 会话(双向 websocket,不是一问一答的请求/响应)整体排除在这个直方图之外 |

`logs/stat` 的 `by_product` 语义**不对称**,集成时注意:总量字段(`total_quota` 等)受 `source_product` 过滤,而 `by_product` 明细**始终**是同一时间窗内全部产品的拆分(不受该过滤影响),用来看"我之外的花销去了哪"。未打标的历史行归入默认产品,不会出现空名分组。

### E. 响应头目录

| 头 | 方向 | 说明 |
|------|------|------|
| `X-Request-Id` | 入站(可选)/出站(恒有) | 入站:调用方自己传的 id(8-36 位 `[A-Za-z0-9._-]`,上限对齐审计表 `audit_events.request_id` 的 `varchar(36)` 列宽——超过 36 位不会被原样回显,只会被网关重新铸造,否则该次请求的审计行会因写入超长值失败而被静默丢弃)会被原样回显,方便调用方不做往返查询就能自行关联;不传或格式不对时网关退回读 `traceparent`(W3C Trace Context)的 32 位 trace id(恒 ≤36 位),再退回自己生成。出站:成功/失败响应上恒有此头,是 `GET /v1/generation?id=` 的查询键(见 §F)。遗留别名 `X-Oneapi-Request-Id` 同值双发,计划一个发布周期后下线,新集成不要依赖它。 |
| `X-Model-Provider` | 出站(尽力而为) | 服务本次请求的上游供应商名。OpenAI 线非流式响应与任意 wire 的流式响应上必有;Claude/Gemini 原生非流式响应路径不保证存在,不要用来做强判断。 |
| `X-RateLimit-Limit` / `X-RateLimit-Remaining` / `X-RateLimit-Reset` | 出站(放行响应,尽力而为) | 命中的限流层级里最紧的一档:上限/当前窗口剩余/窗口重置时间(Unix 秒)。 |
| `X-RateLimit-Scope` | 出站(限流/配额类 429 拒绝响应必有,放行响应尽力而为) | 限流键所属主体:`ip` / `key`(网关 internal API key 桶,rate-limit.go 的 keyed 拒绝点)/ `user`(ModelRequestRateLimit,model-rate-limit.go)/ `token` / `tenant` / `model`(BusinessRateLimit / BusinessModelRateLimit,均按 rpm\|tpm)/ `account`(平台 entitlement 429)。**不是每个 429 都带**——`quota_exceeded`(entitlement)带 `account`,`cost_spike_limit_exceeded`(cost spike)带 `user`,并发类拒绝(concurrency_limit.go)带 `token`/`tenant`,其余限流层级见下一行 `Type` 对应关系。放行前就被拒的请求(TokenAuth/资金池/entitlement 之前的 401/402/403)和上游归因的失败(供应商 5xx/429、`channel:*` 含 503 cooling)不带网关自己的 `X-RateLimit-*`;放行之后才被拒的非限流错误(如 413 请求体过大、Distribute 的 400/403)可能仍带着放行时的余量快照——把它当"上一次放行的快照"读,不是对这次拒绝的说明。`ip`/`key` 只出现在 `/internal/*` 与非中转路由上,`/v1` 中转路由不会发这两个值。 |
| `X-RateLimit-Type` | 出站(限流/配额类 429 拒绝响应必有,放行响应尽力而为) | 限流维度:`requests` / `rpm`(每分钟请求数,BusinessRateLimit/BusinessModelRateLimit)/ `tpm`(每分钟 token 数,同样以 429 强制执行;`Remaining` 按已结算用量计算,可能滞后一次尖峰)/ `concurrency` / `quota`(entitlement)/ `cost`(cost spike)。 |
| `Retry-After` | 出站(402/429/503 拒绝,携带已知恢复时间时) | 建议等待秒数。 |
| `X-Request-Cost` / `X-Quota-Remaining` | 出站(仅 OpenAI 线非流式成功响应) | 本次请求消耗的钱包配额 / 调用方本次后的剩余余额,同单位,浮点数的字符串形式。 |
| `X-Session-Id` | 入站(可选) | 调用方自带的会话粘滞键,用于把同一会话的多轮请求尽量路由到同一渠道(减少上下文缓存失效)。网关只存它的 HMAC(按调用方+分组+模型加盐),原始值不落库,不回显。 |
| 用户维度哈希 | 内部/日志 | 网关不落调用方传入的终端用户原始标识——`EndUserHash` 是按租户加盐的 HMAC(取前 16 字符),只用于按用户维度聚合成本查询,不可逆推原始标识。 |

### F. 只持一把 key 的调用方(无控制台权限)

某些集成场景下调用方只拿到一把 `sk-...`,没有登录控制台的身份(如后端服务转发)。两个只读端点让这类调用方不经控制台也能自查:

- **`GET /v1/key`** — 一次拿到这把 key 自身的额度上限/已用/剩余、所属分组与模型白名单、RPM/TPM 限流(自身 + 所属租户)、所属租户资金池状态(`pool` 为 `null` 表示未配置资金池 = 不限额,不是错误)。`limit` 是总额度上限(剩余+已用),`limit_remaining` 是剩余可用,`usage` 是已用——三者均为 **quota 整数**(DB 计价单位,不是 USD);`token.UnlimitedQuota` 为真时 `limit`/`limit_remaining` 都是 `null`。镜像 OpenRouter 的 `GET /api/v1/key`。
- **`GET /v1/generation?id=<X-Request-Id>`** — 用自己发出的入站 `X-Request-Id`,或网关在原始响应上回显的 `X-Request-Id`(见 §E),反查该次调用的费用/供应商/用量/首字延迟,不必等 `/logs` 分页查询。`quota` 字段是 quota 整数,`total_cost` 是本响应体里**唯一** USD 计价字段(`quota / quota_per_unit`;`quota_per_unit` 是管理员可改的选项,默认 500000,当前值以 `GET /api/status` 返回的 `quota_per_unit` 为准;该值未设置时 `total_cost` 为 0)。`id` 不属于调用方自己的 token/租户,或从未出现过,一律 404(不是 400/403),避免向未持有该 id 的调用方泄露"格式对/不对"的探测信号。镜像 OpenRouter 的 `GET /api/v1/generation`。

两者均走标准 `Authorization: Bearer sk-...`,无需 flag,无写副作用。单位约定:`/v1/key` 的 `limit`/`limit_remaining`/`usage` 与 `/v1/generation` 的 `quota` 都是 quota 整数;`/v1/generation` 的 `total_cost` 是本指南里这两个端点唯一的 USD 计价字段。
