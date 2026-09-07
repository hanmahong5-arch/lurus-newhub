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
- **Q4 额度不足?** API 返回 402 `insufficient_quota` `quota_exceeded`;提示用户去钱包充值或订阅。
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
| `/api/v2/{tenant}/user/me` | GET | 用户信息 |
| `/api/v2/{tenant}/tokens` | GET / POST | 查询 / 创建 Token |
| `/api/v2/{tenant}/logs` | GET | 使用日志 |
| `/api/v2/{tenant}/billing/topup` | POST | 发起充值 |

### B. 错误码

| HTTP | 类型 | 说明 | 处理 |
|------|------|------|------|
| 401 | `invalid_api_key` | Key 无效 | 检查 Token |
| 402 | `insufficient_quota` | 额度不足 | 提示充值 |
| 429 | `rate_limit_exceeded` | 超速率 | 稍后重试 |
| 500 | `upstream_error` | AI 服务商故障 | 重试 / 切模型 |
| 503 | `service_unavailable` | 维护中 | 等待恢复 |

### C. Webhook 通知 (可选)

提供 Webhook URL 接收用户充值/订阅事件:`POST https://yourapp.com/webhooks/lurus`,body `{event, tenant_slug, user_id, data{amount, quota_before, quota_after, timestamp}, signature: "sha256=..."}`(signature 验真实性)。

### D. 跨产品归因 (`X-Lurus-Product`)

兄弟产品经 hub 中转时在每个请求带 `X-Lurus-Product: <product>` 头,值须在服务端 allow-list 内(`lurus-api` 默认、`kova`、`lutu`、`lucrum`、`switch`、`creator`、`memorus`、`tally`);未知或缺省值**静默**折算为默认产品,不报错、不回显。归因贯穿三处:

| 落点 | 说明 |
|------|------|
| 平台钱包 `WalletDebit` / `WalletPreAuthorize` 的 `product_id` | 钱按产品入账;`ReportUsage` 无该字段(proto 待补) |
| 日志行 `other.source_product` | 成功行、handler 层与中间件层的错误行都带;归因早于任何模型/渠道解析,绑定失败也能落对产品 |
| `GET /api/v2/{tenant}/logs?source_product=switch`、`GET /api/v2/{tenant}/logs/stat?source_product=switch` | 普通用户即可查自己产品的份额,无需 root |

`logs/stat` 的 `by_product` 语义**不对称**,集成时注意:总量字段(`total_quota` 等)受 `source_product` 过滤,而 `by_product` 明细**始终**是同一时间窗内全部产品的拆分(不受该过滤影响),用来看"我之外的花销去了哪"。未打标的历史行归入默认产品,不会出现空名分组。
