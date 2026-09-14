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
- **Q5 限制调用频率?** Lurus 内置 `daily_quota` 日限额;或产品侧自实现限流。兑换/激活码相关的四个 `/api` 端点(`POST /api/user/topup`、`POST /api/v2/{tenant}/redeem`、`POST /api/v2/switch/redeem`、`POST /api/v2/switch/user/topup`)额外共享同一个按调用方 IP 计的 `RD` 桶(5 次/60 秒,`rate-limit.go`)——这是**一个** IP 在 60 秒窗口内跨这四个端点合计 5 次,不是每个端点各 5 次;第 6 次起收到空 body 的 429,带 `X-RateLimit-Scope: ip`/`Retry-After`(cycle-8 L1)。
- **Q6 白标?** 当前不支持完全白标,可:自定义 Zitadel 登录页主题、用方式 3、隐藏控制台 (Token 自动管理)。
- **Q7 支持哪些模型?** 列表 https://api.lurus.cn/console/models。常用: OpenAI gpt-4o/gpt-4o-mini、Anthropic claude-3-5-sonnet、Google gemini-1.5-pro、国内 qwen-max/glm-4/deepseek-chat。
- **Q8 技术支持?** 邮箱 support@quantumnous.com · 文档 https://docs.lurus.cn · 企业微信群(联系管理员)。

## 附录

### A. API 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/v1/models` `/v1/models/:model` | GET | 模型发现:列表原本就从(租户盲的)ability 集作答,2026-09-09 起改为按调用方所属租户可路由的模型集(不再看得到别的租户的渠道);`:model` 单条查询原本直接答静态目录、与路由是否可达无关,现在改用同一个租户可路由集判断——查询一个该租户路由不到的模型返回 404 而不是静态目录里的 200,OpenAI 线 `type=invalid_request_error`、`param=model`、`code=model_not_found`(注意与下方 B 表 404 行的 `type=not_found_error` 不同);Anthropic 线 `type=not_found_error`,与 B 表一致,但该信封**没有 `code` 字段**——这一条要按 `type` 判,不要按本文其余各处推荐的 `code` 判;token 自带 `model_limit` 列表时,列表/单条查询按该列表作答,不做租户可路由性交叉;若该租户的模型白名单处于 `enforce` 模式,列表与单条查询都再按白名单收窄(两者共用同一个可见集,所以被白名单挡掉的模型单条查询也返回 404) |
| `/v1/chat/completions` | POST | 对话模型 |
| `/v1/embeddings` | POST | 文本向量化 |
| `/v1/images/generations` | POST | 图片生成 |
| `/v1/audio/transcriptions` | POST | 语音转文字 |
| `/v1/responses/:response_id` | GET / DELETE | OpenAI Responses API 有状态半面(cycle-8 L7, tasks-plugins-12)。POST `/v1/responses`(`store` 不为字面量 `false`、渠道类型在 `SupportsResponsesStateful` 允许列表内——本周期仅 OpenAI,与 `/v1/responses/compact` 的 `SupportsResponsesCompact` 是两张独立的表——且渠道非多密钥、且渠道响应/流事件里出现过非空 `id` 时)成功后把 `response_id` 与产生它的渠道/模型记入 `response_registry`;这两条路由据此把请求原样转发回**同一个渠道**(读该行做鉴权+定位,不经过权重选择/`Distribute()`)。**多密钥渠道不写入本表**(cycle-8 L7 修复轮 B-F3):行只钉渠道 id,不钉密钥下标,而多密钥渠道每次请求随机/轮询选密钥,回放到错误密钥上会复现本特性本要避免的"渠道自己的 404";密钥下标列是 migration 036(本周期冻结)之外的 cycle-9 跟进项。流式 POST 在**首个**携带非空 `id` 的事件即暂存(不一定是 `response.completed`——早于它的 `response.created`/`queued`/`in_progress` 同样携带 id,一个连接在收到完成事件前掉线的后台流客户端因此仍留得下可寻址的行)。归属判定为同租户且同一用户(O7);不存在的 id、归属于他人的 id、渠道已被禁用或渠道类型已不再受支持四种情况返回完全相同的 `404 response_not_found` **网关信封**,从不返回 403,调用方无法用状态码或报文区分四者——但这与渠道自己返回的 404 是两种不同的报文:一旦行存在且渠道可达,渠道自身状态码/报文(含它自己的 404)会原样透传,不带 `error.code`。不解析 usage、不计费、不写任何配额行——费用已在原始 POST 时结清。挂在独立分组(`StampRelayFormat`+`TokenAuth`+自带的 `ResponsesStateRateLimit`——每 IP 30 次/60 秒的 `RS` 桶,与 `CriticalRateLimit`(渠道 key 揭示/TOTP 禁用/审计导出共用的"CT" 桶)完全分离,429 走 `abortWithOpenAiMessage` 带 `error.code`;cycle-8 L7 修复轮 B-F1 订正——原计划文档误引 `modelsRouter` 为"同一先例",而 `modelsRouter` 只挂 `TokenAuth`,若真挂 `CriticalRateLimit` 会让这条 `/v1` 路由的轮询流量与上述 `/api/*` 控制台路由共用限流桶,互相锁人),不经过资金池/成本尖峰/并发链的 `Distribute()` 半;上游 `Content-Type` 为 `text/event-stream` 时增量转发并逐块 flush,否则整体转发。行过期由 `RESPONSE_REGISTRY_TTL_DAYS`(默认 30 天,只影响之后新写入的行——调小该值不会缩短已写入行的保留期,过期时间在写入时就已按当时的值算好戳死)控制,每小时由 leader 副本清扫,并纳入 PIPL 抹除级联硬删除(`repo.HardDeleteUserResponseRegistry`,与 `user_sessions` 同一步)。目前没有兄弟产品接入这两个端点;公开开发者文档(`2l-bs-docs` 仓 `docs/api/overview.md` 及其语言分支)的端点列表尚未加入这两条,是本轮已知的跨仓跟进项 |
| `/v1/responses/compact` | POST | `/v1/responses` 的文档化字段子集透传端点(cycle-8 L6, wire-formats-03)。这是一份白名单,不是排除列表:请求体先按该子集重新编码,只有 `model`/`input`/`instructions`/`previous_response_id`/`parallel_tool_calls`/`service_tier`/`prompt_cache_key`/`prompt_cache_retention` 真正转发给上游。`tools`/`reasoning`/`text` 会被解析(供客户端兼容),但不转发给上游(与参考实现一致)。`dto.OpenAIResponsesRequest` 其余字段——`include`/`max_output_tokens`/`metadata`/`store`/`temperature`/`tool_choice`/`top_p`/`truncation`/`user`/`max_tool_calls`/`prompt`/`stream`——一律被丢弃,渠道 `PassThroughRequestEnabled`/`PassThroughBodyEnabled` 打开时同样如此(该开关只跳过本网关自己的请求转换,不能绕过这道子集门;pass-through 分支同样应用渠道的禁用字段策略——如 `AllowServiceTier=false` 时 `service_tier` 仍会被剥离)。本周期未建模 `prompt_cache_options`(父类型也没有该字段)。渠道类型不在支持列表(本周期仅 OpenAI)时返回 `400 responses_compact_unsupported`,零上游调用。成功响应字节原样转发,`usage` 由渠道响应解析,缺失时按请求前估算的 prompt token 数计费、completion 记 0;渠道在 `200` 响应体内携带 `error` 字段时识别为上游错误并拒绝,不转发、不计费。会话亲和(`X-Session-Id`/`prompt_cache_key`)与 `/v1/responses` 共用同一套 scope 推导,同一 `prompt_cache_key` 在两个端点间派生相同的亲和 key。与 `/v1/responses` 共用同一条鉴权/资金池/限流/熔断链。目前没有兄弟产品接入这个端点 |
| `/v1/tasks/:platform` `/v1/tasks/:platform/:task_id` | POST / GET | 通用异步任务提交/状态查询(cycle-8 L8)。一条路由服务全部已编译 task 适配器(ali/doubao/gemini/hailuo/jimeng/kling/music/sora/suno/vertex/vidu,由 `TestGenericTaskPlatforms_CoverCompiledAdaptors` 锁定为与 `internal/adapter/provider/task/*` 目录数相等,而非固定数字),复用与 `/suno`、`/v1/audio/music`、`/kling/v1`、`/v1/videos` 相同的 InitTask/轮询链路;`:platform` 不是任一已编译适配器名、或 Distribute 依据请求体 `model` 选中的渠道类型与 `:platform` 声明不一致(如 `POST /v1/tasks/kling` 的 `model` 解析到一个 Suno 渠道)时均返回 404、`code=task_platform_unknown`,后者在任何上游调用之前拒绝。请求体契约:ali/doubao/gemini/hailuo/jimeng/kling/sora/vertex/vidu 九个数值适配器接受统一的 `TaskSubmitReq` 形状(`model`/`prompt`/`image` 或 `images`/`size`/`seconds`/`metadata` 等);suno 接受自身提交体并额外需要顶层 `action`(`MUSIC`/`LYRICS`,专用路由原从 URL 的 `:action` 段读取,此处改从请求体读取并内部转发);music 与专用 `/v1/audio/music` 同体。Kling/Jimeng 供应商原生请求体(`model_name`/`image`、`req_key` 等)的转换中间件不挂在本路由上,原生格式仍只走各自专用路由。计费:本接口按请求体顶层 `model` 计费(该 model 须已在渠道上配置价格),与专用 `/suno/submit/:action` 按 action 派生模型名(如 `suno_music`)计费是两条独立定价路径,同一逻辑任务经两条路由可能定价不同。状态查询直接读 `tasks` 表(由后台轮询器每 15 秒刷新,本接口自身不发上游请求),查询按 `(user_id, task_id)` 限定作用域(`task_id` 只是索引不是唯一约束,避免撞号顶替),归属校验 fail-closed——他人任务、不存在的 `task_id`、存在但 platform 不匹配三种情况返回完全相同的 404 报文 `{"error":{"type":"invalid_request_error","message":"Task not found"}}`(sora 额外接受经 OpenAI 类型渠道提交的任务),不像专用的 `GET /v1/videos/:task_id/content`(视频内容代理)对越权访问返回 403。响应体是既有 `relay.TaskModel2Dto` 投影加 `platform`/`project_id`/`request_id` 三列,不含 `channel_id`/`user_id`/`quota`/`group`/`properties`。专用的 Kling/Jimeng/视频/Suno/Music 路由行为不变,与本行并存。任务行新增 `project_id`(成本归因)与 `request_id`(提交时的网关请求 id,支持排查) 两列(migration 035)。过滤入口(cycle-8 L10):既有的 `GET /api/task/`(管理端,全租户)与 `GET /api/task/self`(用户端,`user_id` 恒定 scope)新增 `project_id`/`request_id` 两个精确匹配 query 参数,`request_id` 服务端 trim 并截到 64 字符(列宽);用户端传入自己不拥有的 `project_id` 返回空列表(`total=0`),不是 403——与 `project_id` 天然叠加在已有的 `user_id` scope 之上、不需要额外归属校验同一套道理,`GET /api/v2/:tenant_slug/logs` 的 `project_id` 过滤(`v2_log.go`)是同一约定。目前没有兄弟产品接入这组端点 |
| `/v1/tasks/:platform/:task_id/artifacts` `/v1/tasks/:platform/:task_id/artifacts/:key/content` | GET | 通用异步任务产物列表/内容代理(cycle-8 L9,建立在上一行的通用任务面之上,同一归属校验/同一 404 报文;不按 task 状态过滤——非 SUCCESS 的任务照样返回当前已有的 URL,可能是空列表)。列表接口仅元数据、不发上游请求,投影两处来源:①`Task.Data` 里**顶层**、形如 URL 的字符串字段(`http(s)://`/`data:` 开头)——这只对 suno/music 有意义,它们的 `Data` 是扁平对象;②对一个 SUCCESS 任务,若①未产出 `video` 这个 key,再看 `task.FailReason`——ali/kling/jimeng/doubao/vidu/hailuo/vertex 这 7 个数值适配器的 `Data` 是嵌套的供应商原始报文(如 kling 的 `data.task_result.videos[0].url`),真正解析出的结果 URL 由轮询器写进 `FailReason`(与专用 `GET /v1/videos/:task_id/content` 的默认分支读的是同一字段),看起来像 URL 就投影成 `video` 这个 key;sora/gemini 两个平台的 `FailReason` 是**本网关自己的** `/v1/videos/:task_id/content` 地址(不是供应商资产 URL),不投影——这两个平台的产物只能继续走专用视频代理路由。换言之:在①②都不命中之前(如任务尚未 SUCCESS、或两处都没有可用 URL),列表就是空数组。`key` 是 `Data` 里的原始字段名(对象形态)/数组下标(数组形态)/或固定值 `video`(FailReason 投影);`size` 只对 `data:` URL 有值(解码字节数),`http(s)` URL 恒为 0(未知,不为了拿大小发一次上游请求);`type` 取 `video/image/audio/text/json/other` 之一。内容代理接口按 `:key` 取那一条 URL:`data:` 直接内联解码返回零出站请求,`http(s)` 走一次受限 GET 并把上游 `Content-Type` 原样带回,并在响应上加 `X-Content-Type-Options: nosniff`/`Cache-Control: private, no-store`/`Referrer-Policy: no-referrer`(本路由的鉴权也接受 `?key=` query 参数,因此是可被浏览器直接导航的 URL,不能被缓存或经 Referer 泄漏)。**仅支持整体 GET**:不支持 `HEAD`、不转发/响应 `Range`/`If-*` 条件请求头,调用方不能用于可拖动进度的播放器。受限体现在四处,均为 `502` 且各自带 `code=artifact_request_rejected`:scheme 白名单(仅 http/https)、自指 host 拒绝(产物 URL 若与本次请求自身 Host 头同源则拒绝,防回环,与 `fetch_setting` 的私网 IP 放行策略是两道独立的检查——`fetch_setting`/`ValidateOutboundURL` 只应用在本接口,不应用在下面的专用视频代理)、`fetch_setting` 出站检查(私网/域名黑白名单)、单次转发 200MiB 上限——已知 `Content-Length` 超限在写任何响应头之前就直接拒绝(不会返回一个字节数与声明不符的 200),未知长度的流式响应仍会在到达上限处截断,但会记入 `lurus_gateway_task_media_guard_rejections_total{route="artifact_content",reason="size_cap"}`。未知 `:key` 返回 `404`,`code=artifact_not_found`。这一对与专用的 `GET /v1/videos/:task_id/content`(视频内容代理,仍是独立路由、仍是 403 越权语义、**观测行为不完全一致**——见下条)共用了同一份 scheme 白名单/自指 host 守卫/转发上限(`task_media_guard.go`),但不共用 `fetch_setting` 出站检查,也不共用鉴权语义——两者是各自路由上各自的归属检查。目前没有兄弟产品接入这组端点 |
| `GET /v1/videos/:task_id/content` | GET | 专用视频内容代理(既有路由,cycle-8 L9 在其上加了新的前置校验,不是重写)。新增的 scheme/自指 host 检查与上一行共用同一份实现(`task_media_guard.go`),命中时统一返回 `502`;scheme 校验命中的那条分支刻意沿用了旧文案 `"Failed to fetch video content"`——因为这条分支此前必然会落到 `client.Do()` 的网络层失败并产生完全相同的状态码/文案(Go 标准库的 `http.Client` 本身就拒绝拨号非 http(s) scheme),所以这条校验对可观测行为而言是纵深防御,不是"新拒绝原因";自指 host 检查则是**真正新增**的拒绝(此前会真的去拨这个 URL)。**行为不再对所有历史输入字节相同**:上游声明的 `Content-Length` 超过 200MiB 上限时,此前会在 `200` 状态下静默截断转发(客户端拿到的字节数与声明的 `Content-Length` 不一致——一个已损坏的文件);现在改为在写任何响应头之前就返回 `502`。未知长度流仍在上限处截断,但会记入 `lurus_gateway_task_media_guard_rejections_total{route="video_proxy",reason="size_cap"}`。响应额外带 `X-Content-Type-Options: nosniff`。|
| `/v1/key` | GET | 只持一把 key 查自身额度/限流/所属租户资金池状态,无需控制台权限(详见 §F) |
| `/v1/generation` | GET | 按 `id`(己方 X-Request-Id)反查一次调用的费用/供应商/用量(详见 §F) |
| `/api/v2/{tenant}/user/me` | GET | 用户信息 |
| `/api/v2/{tenant}/tokens` | GET / POST | 查询 / 创建 Token |
| `/api/v2/{tenant}/logs` | GET | 使用日志 |
| `/api/v2/{tenant}/logs/all?upstream_request_id=` | GET | 租户管理员(`requireTenantAdmin`)专用的日志列表,可按供应商自己的 request/trace id 精确匹配过滤:取上游响应头 `x-request-id` / `request-id` / `openai-request-id` / `cf-ray` 中第一个非空的值(≤128 字节可打印 ASCII,否则视为未发送),落在管理员可见字段 `other.upstream_request_id` 上(普通用户 `/api/v2/{tenant}/logs` 看不到该字段,也不支持这个查询参数)。根管理员导出 `GET /api/v2/admin/logs/export` 接受同名参数、同语义;供应商完全没发送这些头时该字段为空,不算缺陷;该 CSV 导出没有 `other` 列(过滤只用来缩小行范围,字段本身不落进文件),v2 控制台日志页当前也没有这个过滤输入框或明细展示 |
| `/api/v2/{tenant}/analytics/rankings?by=model\|vendor&hours=` | GET | 租户管理员(`requireTenantAdmin`)专用的模型/供应商用量排行榜:按 token 用量降序给出 rank/环比 rank_delta(新上榜的 is_new=true、rank_delta=0)/requests_growth_pct(无上一窗口基线时为 null)/token_share_pct/quota_share_pct(份额基于当前窗口全部分组的总量,不是仅返回的最多 20 行);`by=vendor` 按 `channel_type` 聚合(名称经 `constant.GetChannelTypeName` 解析,`channel_type=0` 的历史行不计入任何 vendor 行);`hours` 会被收敛到 `{1,6,24,168,720}` 五档之一再作为缓存键(空值/非整数回落到默认 24h,超出 [1,720] 先截断再收敛),未知 `by` 值返回 400。响应体除 `rows` 外还带 `hours`(实际命中的档位)、`total_tokens`/`total_quota`(当前窗口全部分组的总量,不是仅返回的最多 20 行的求和,`token_share_pct`/`quota_share_pct` 即基于这两个总量计算);在进程内缓存 5 分钟(`cached_at` 可看出是否命中缓存;各副本各自维护自己的缓存,`cached_at` 在副本间可能不同,是预期行为不是缺陷)。两条路由都挂在 `CriticalRateLimit`(每 IP 20 次/20 分钟的 `CT` 桶,与渠道 key 揭示、TOTP 禁用、`/analytics/model-performance` 等 CriticalRateLimit 路由共享同一限流桶)之后,超额返回 429。根管理员等价端点 `GET /api/v2/admin/analytics/rankings?by=&hours=&tenant_id=` 额外接受 `tenant_id`(留空=跨租户)。目前没有兄弟产品接入这两个端点 |
| `/api/v2/{tenant}/billing/topup` | POST | 发起充值 |
| `/api/v2/{tenant}/sessions` | GET / DELETE(`:id`、`others`、`current`) | 控制台会话列表与撤销,整体挂在 `SESSION_REGISTRY_ENABLED`(默认关)后面:关闭时列表只返回一条代表当前请求的合成行,`DELETE :id` 一律 404、`DELETE others` 一律 `{"revoked":0}`,均不触碰数据库(2026-09-12 起,回滚或某次开关期遗留的行都不会被这两个端点动到);打开后列表按已登录设备逐条返回(`is_current`/`created_at`/`last_seen_at`、`ip` 按 /24(v4)或 /48(v6)掩码、`user_agent_family` 粗粒度),`DELETE :id` 撤销自己名下的一台设备(IDOR 404 语义,不属于自己的 id 与不存在的 id 同样 404)、`others` 一键撤销除当前设备外的全部。根管理员等价端点 `DELETE /api/v2/admin/users/:id/sessions`(压缩账号处置步骤,同样受该 flag 门控)。目前没有兄弟产品接入这组端点 |

### B. 错误码

2026-09-08 起,网关自身发起的拒绝(中间件层 401/402/403/429/5xx、`/v1/dashboard/*` 读库失败等)不再用 `new_api_error` 占位 `error.type`——`type` 改按调用方使用的 wire 语义映射(OpenAI 语义 vs Anthropic 语义,402/503/529 三处分叉),`code` 改为跨 wire 稳定的机器可读值,两者都不再为空。上游供应商自身返回的错误(`upstream_error`)不受影响,原样透传。仅有的例外是 Midjourney wire(`/mj/*`)的 `abortWithMidjourneyMessage`:该 wire 的官方信封没有稳定 code 字段位置,`type` 沿用 `new_api_error` 字面量,是本表以外唯一允许出现该字面量的位置。

| HTTP | OpenAI 线 `type` | Anthropic 线 `type` | `code`(稳定,跨 wire 相同) | 说明 | 处理 |
|------|------|------|------|------|------|
| 400 | `invalid_request_error` | `invalid_request_error` | `invalid_request` 等 | 请求本身有问题(缺模型名/渠道 id 格式错/请求体解析失败) | 检查请求体 |
| 401 | `authentication_error` | `authentication_error` | `invalid_request` / `session_required` / `token_disabled`(令牌被禁用,主鉴权路径) | Key 无效、未登录、令牌被禁用 | 检查 Token / 重新登录 / 在令牌管理里启用或换一把 |
| 402 | `insufficient_quota` | `billing_error` | `insufficient_user_quota` / `token_quota_exhausted` / `pool_exhausted` / `pool_not_configured`(租户信用池未配置,`CREDIT_POOL_REQUIRED=enforce` 时) / `tenant_quota_exceeded`(租户月度配额超限) | 钱包/Token/租户资金池额度不足 | 提示充值,`metadata.topup_url` 见 §F |
| 403 | `permission_error` | `permission_error` | `model_blocked` / `group_not_allowed` / `ip_not_allowed` / `channel_specify_forbidden` / `scope_not_granted` / `user_banned` / `tenant_suspended` / `token_disabled`(令牌被禁用,playground 鉴权路径) | 模型/分组/IP/scope 未授权,账号或租户被封禁,或(playground 路径)令牌被禁用。`model_blocked` 除了令牌自身模型白名单,`TENANT_MODEL_ALLOWLIST_MODE=enforce` 时也会由租户级模型白名单触发(默认 `observe` 只记录不拒绝)。`POST /api/v2/:tenant_slug/provision`(cycle-8 L2,平台 entitlement 换发 relay token)铸造的 `switch-provision-<plan_code>` 令牌按 entitlement `ent.models` claim 精确匹配模型名(不做 dated/undated 别名桥接,`FormatMatchingModelName` 只桥接 gpts/thinking-* 前缀)——plan 只列了 undated 别名时,调用方发 dated 变体一律 `model_blocked`;plan 变更(同一平台账号换 plan_code)会禁用上一个 plan 的令牌,已粘贴旧 key 的客户端收到 `token_disabled`,须从平台 onboarding 引导页重新复制新 key | 按 `code` 定位具体原因,联系管理员放开 |
| 404 | `not_found_error` | `not_found_error` | `model_not_found` / `task_platform_unknown`(`/v1/tasks/:platform` 的 `:platform` 不是任一已编译 task 适配器名) / `response_not_found`(`GET`/`DELETE /v1/responses/:response_id`——不存在、不属于该用户或租户、渠道已禁用、渠道类型已不再受支持四种情况共用同一网关报文;一旦行存在且渠道可达,渠道自己的 404 会原样透传,不带这个 `code`,与这四种情况是两种不同的报文) | 模型未配置任何可用渠道(区别于"渠道全部暂时不可用"的 503),通用任务路由的平台名无效,或 Responses API 有状态端点查无此 id | 换模型 / 检查 `:platform` 拼写 / 确认 `response_id` 与调用方身份匹配 |
| 413 | `request_too_large` | `request_too_large` | `read_request_body_failed` | 请求体超限 | 缩小请求 |
| 429 | `rate_limit_error` | `rate_limit_error` | `request_rate_limit_exceeded` / `quota_exceeded` / `cost_spike_limit_exceeded` / `business_rate_limit_exceeded` / `concurrency_limit_exceeded` 等 | 限流(见下方 Q4 的另一类 429)。**仅限中转路径**(`/v1/*` 等 relay 路由)网关自身发起的这类 429,`error.message` 都是英文句子,不要拿它做文本匹配——判定读 `code`。其中限流/并发中间件的拒绝(`request_rate_limit_exceeded`/`business_rate_limit_exceeded`/`concurrency_limit_exceeded`)统一是 `<scope> <requests\|tokens\|concurrency> limit exceeded: <n> ...(<code>)` 这一种形状;`quota_exceeded`(entitlement)与 `cost_spike_limit_exceeded`(cost spike)同样是英文,但句式不同、不含 `<n>`。`/api/*` 控制台路由与 `/internal/*` 内部路由上的 ip/key 限流器(`rate-limit.go` 的 keyed 拒绝点)429 只带头,**没有 body**,不要假设那类 429 存在 `error.message` | 稍后重试,读 `Retry-After`/`X-RateLimit-*`(见 §E,并非全部 429 都携带 —— `quota_exceeded` 与 `cost_spike_limit_exceeded` 也带 `X-RateLimit-Scope`/`Type`,见 §E 表) |
| 500 | `api_error` | `api_error` | `gateway_internal` | 网关自身处理失败(非上游供应商故障) | 重试;持续出现联系运维 |
| 500 | `upstream_error` | `upstream_error` | 供应商原样透传 | AI 服务商故障 | 重试 / 切模型 |
| 503 | `api_error` | `overloaded_error` | `channel:all_keys_cooling` / `model_not_found`(无可用渠道时复用此状态码) 等 | 模型配置存在但渠道暂时全部不可用/维护中 | 等待恢复,读 `Retry-After` |

完整 `code` 枚举(所有网关自身可能返回的机器码,不含上游供应商透传值)见 `docs/openapi/relay.json` 的 `components.schemas.GatewayError.code.enum`,由 CI 锁与 `internal/pkg/types` 的 `ErrorCode` 常量表逐条互校,新增/改名任一侧都会挂红。

定价:仅 cache-read(`GetCacheRatio`)与 image(`GetImageRatio`)这两个折扣的查找——先按调用方发来的原始模型名精确匹配,查不到再退化到 `FormatMatchingModelName` 收敛出的固定族名(仅 `gemini-2.5-flash-lite-thinking-*`/`gemini-2.5-flash-thinking-*`/`gemini-2.5-pro-thinking-*`/`gpt-4-gizmo-*`/`gpt-4o-gizmo-*` 这五个字面量,不是任意通配符——运营方要按这五个字面量配价才会命中,配 `gpt-4o-*` 之类自定义通配符不会生效)。调用方发的是某个 thinking-budget/gizmo 变体名(未单独配价)时,这两项折扣现在会回落到上述族名条目的价格,而不是默认值——`X-Request-Cost`/`x_lurus.cost_lb`/`GET /v1/generation` 的 `quota` 会随之更贴近价目表。cache-write(`GetCreateCacheRatio`)做了同样的查找一致性修正,但该价目表目前没有任何管理端点可写,所以没有运营方可见的计费变化。模型价格本身(`GetModelPrice`/`GetModelRatio`,定价里最大的组成部分)与 completion 比例**不**遵循这条"精确匹配始终优先"规则——它们无条件先做 `FormatMatchingModelName` 归一化,带 thinking-budget 后缀的精确模型名价格条目不会被读到。

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

浏览器跨域调用方(允许源见 `ALLOWED_ORIGINS`,当前 `hub.lurus.cn`/`identity.lurus.cn`):本表全部出站头都在 `Access-Control-Expose-Headers` 里,`response.headers.get(...)` 可读;**同源请求**(`Origin` 与被请求主机一致)按 CORS 规范根本不需要这套头,网关也不会发 `Access-Control-*`,`response.headers.get(...)` 直接就能读到——用同源地址做"跨域头是否生效"的探针会得到假阴性,要用另一个在允许源列表里的域名去验;`X-Request-Id`/`X-Session-Id`/`X-Lurus-Product`/`traceparent`/`tracestate` 在 `Access-Control-Allow-Headers` 里,预检不会剥掉这五个入站头(`tracestate` 与 `traceparent` 同时放行,是同一个 W3C trace-context 传播器会同时设置的一对——只放行前者会让整个请求在预检就被拒)。未在允许源列表内的调用方两者都拿不到。

| 头 | 方向 | 说明 |
|------|------|------|
| `X-Request-Id` | 入站(可选)/出站(恒有) | 入站:调用方自己传的 id(8-36 位 `[A-Za-z0-9._-]`,上限对齐审计表 `audit_events.request_id` 的 `varchar(36)` 列宽——超过 36 位不会被原样回显,只会被网关重新铸造,否则该次请求的审计行会因写入超长值失败而被静默丢弃)会被原样回显,方便调用方不做往返查询就能自行关联;不传或格式不对时网关退回读 `traceparent`(W3C Trace Context)的 32 位 trace id(恒 ≤36 位),再退回自己生成。出站:成功/失败响应上恒有此头,是 `GET /v1/generation?id=` 的查询键(见 §F)。遗留别名 `X-Oneapi-Request-Id` 同值双发,计划一个发布周期后下线,新集成不要依赖它。该"恒为网关自铸 id"的保证只覆盖走 `IOCopyBytesGracefully` 的非流式转发路径(OpenAI/Claude/Gemini/ollama/ali/mokaai 等主 wire)。以下几条路径把上游响应头原样转发,按拷贝方式分两种效果:`POST /v1/audio/speech`(OpenAI TTS,`openai/audio.go`)、MiniMax TTS(`minimax/tts.go`)与 Suno(`task/suno/adaptor.go`)三处用的是逐 key `Set`(gin 的 `c.Header`/`Header().Set` 都是覆盖写),若供应商自己也发送同名 `X-Request-Id`,会**替换**掉网关先设的值,客户端读到的是供应商的值;`VideoProxy`(`handler/video_proxy.go`)用的是 `Header().Add`,供应商的值是**追加成第二个值**,`Header.Get`/大多数客户端库仍读到网关先设的那个——这几条路径按 cycle-7 计划 §8 L3 操作员批注刻意排除在这次修复之外。 |
| `X-Model-Provider` | 出站(尽力而为) | 服务本次请求的上游供应商名。OpenAI 线非流式响应与任意 wire 的流式响应上必有;Claude/Gemini 原生非流式响应路径不保证存在,不要用来做强判断。 |
| `X-RateLimit-Limit` / `X-RateLimit-Remaining` / `X-RateLimit-Reset` | 出站(放行响应,尽力而为) | 命中的限流层级里最紧的一档:上限/当前窗口剩余/窗口重置时间(Unix 秒)。`Limit`/`Remaining` 这一对由中转链上的限流/并发中间件(BusinessRateLimit、BusinessModelRateLimit、RelayConcurrencyLimit、ModelRequestRateLimit——`token`/`tenant`/`model`/`user` 四个 scope;`ip`/`key` 一般只挂在 `/api/*` 与 `/internal/*` 上的 keyed 限流器——**`GET`/`DELETE /v1/responses/:response_id` 是唯一例外**,它挂自己的 ip 键控 `RS` 桶,同样带 `Limit`/`Remaining`,见下一行)写出的 429 拒绝响应上也必带(`Remaining` 恒为 0);entitlement 配额闸门(`account`)与 cost spike 熔断(`user`/`cost`)的 429 只带下一行的 `Scope`/`Type`,不带这一对。`Reset` 不在任何 429 上发——拒绝携带的是 `Retry-After`,不是一个窗口重置时刻。 |
| `X-RateLimit-Scope` | 出站(限流/配额类 429 拒绝响应必有,放行响应尽力而为) | 限流键所属主体:`ip` / `key`(网关 internal API key 桶,rate-limit.go 的 keyed 拒绝点)/ `user`(ModelRequestRateLimit,model-rate-limit.go)/ `token` / `tenant` / `model`(BusinessRateLimit / BusinessModelRateLimit,均按 rpm\|tpm)/ `account`(平台 entitlement 429)。**不是每个 429 都带**——`quota_exceeded`(entitlement)带 `account`,`cost_spike_limit_exceeded`(cost spike)带 `user`,并发类拒绝(concurrency_limit.go)带 `token`/`tenant`,其余限流层级见下一行 `Type` 对应关系。放行前就被拒的请求(TokenAuth/资金池/entitlement 之前的 401/402/403)和上游归因的失败(供应商 5xx/429、`channel:*` 含 503 cooling)不带网关自己的 `X-RateLimit-*`;放行之后才被拒的非限流错误(如 413 请求体过大、Distribute 的 400/403)可能仍带着放行时的余量快照——把它当"上一次放行的快照"读,不是对这次拒绝的说明。`ip`/`key` 只出现在 `/api/*` 控制台与 `/internal/*` 内部路由上——且那两个限流器(`rate-limit.go` 的 keyed 拒绝点)的 429 只写头,响应体是空的,`X-RateLimit-Scope: ip`/`key` 是那次拒绝唯一可读的信息,不要期待 `error.*`。**唯一例外**:`GET`/`DELETE /v1/responses/:response_id`(cycle-8 L7)同样发 `X-RateLimit-Scope: ip`,但它走的是 `abortWithOpenAiMessage`(自己的 `RS` 桶),不是 `rate-limit.go` 的 keyed 拒绝点,所以照样带 `error.code`(`request_rate_limit_exceeded`)——见上一行。 |
| `X-RateLimit-Type` | 出站(限流/配额类 429 拒绝响应必有,放行响应尽力而为) | 限流维度:`requests` / `rpm`(每分钟请求数,BusinessRateLimit/BusinessModelRateLimit)/ `tpm`(每分钟 token 数,同样以 429 强制执行;`Remaining` 按已结算用量计算,可能滞后一次尖峰)/ `concurrency` / `quota`(entitlement)/ `cost`(cost spike)。 |
| `Retry-After` | 出站(402/429/503 拒绝,携带已知恢复时间时) | 建议等待秒数。 |
| `X-Request-Cost` / `X-Quota-Remaining` | 出站(仅 OpenAI 线非流式成功响应) | 本次请求消耗的钱包配额 / 调用方本次后的剩余余额,同单位,浮点数的字符串形式。 |
| `X-Session-Id` | 入站(可选) | 调用方自带的会话粘滞键,两条独立用途:(1) 存储/回显——原始值只要 ≤200 字节可打印 ASCII,就原样写入这次调用日志行的 `session_id` 字段(公开可读级别,不是管理员专属),超限或含控制字符时整体丢弃、不截断;`GET /v1/generation`(见 §F)原样回显,`GET /api/v2/{tenant}/logs`可按它过滤(`logs/stat` 没有这个查询参数)——**只放不透明的会话/对话 id,不要放个人身份信息**,它会被落库和回显。(2) 渠道亲和——网关另外用 (调用方+分组+模型) 加盐对它做 HMAC,决定同一会话的多轮请求是否尽量路由回同一渠道(减少上游 prompt-cache 失效);这条 HMAC 从 2026-09 起会通过 `X-Lurus-Affinity-Key`(见下一行)回显,与上面落库/回显的明文 `session_id` 字段是两回事。 |
| `X-Lurus-Affinity-Key` | 出站(仅当本次请求携带可识别的会话来源时) | 上一行渠道亲和 HMAC 的回显值。来源三选一,优先级从高到低:`X-Session-Id` 请求头 / OpenAI `prompt_cache_key` / Claude `metadata.user_id`;一次性调用(三者都没有)不带此头,不会出现一个空字符串。目前唯一的用途是给运营方按此值调用管理端点清除单条绑定,调用方无需读它,能读到只是因为它已在 `Access-Control-Expose-Headers` 里。 |
| 用户维度哈希 | 内部/日志 | 网关不落调用方传入的终端用户原始标识——`EndUserHash` 是按租户加盐的 HMAC(取前 16 字符),只用于按用户维度聚合成本查询,不可逆推原始标识。 |

### F. 只持一把 key 的调用方(无控制台权限)

某些集成场景下调用方只拿到一把 `sk-...`,没有登录控制台的身份(如后端服务转发)。两个只读端点让这类调用方不经控制台也能自查:

- **`GET /v1/key`** — 一次拿到这把 key 自身的额度上限/已用/剩余、所属分组与模型白名单、RPM/TPM 限流(自身 + 所属租户)、所属租户资金池状态(`pool` 为 `null` 表示未配置资金池 = 不限额,不是错误)。`limit` 是总额度上限(剩余+已用),`limit_remaining` 是剩余可用,`usage` 是已用——三者均为 **quota 整数**(DB 计价单位,不是 USD);`token.UnlimitedQuota` 为真时 `limit`/`limit_remaining` 都是 `null`。镜像 OpenRouter 的 `GET /api/v1/key`。
- **`GET /v1/generation?id=<X-Request-Id>`** — 用自己发出的入站 `X-Request-Id`,或网关在原始响应上回显的 `X-Request-Id`(见 §E),反查该次调用的费用/供应商/用量/首字延迟/`session_id`(该次请求带的 `X-Session-Id`,见 §E),不必等 `/logs` 分页查询。`quota` 字段是 quota 整数,`total_cost` 是本响应体里**唯一** USD 计价字段(`quota / quota_per_unit`;`quota_per_unit` 是管理员可改的选项,默认 500000,当前值以 `GET /api/status` 返回的 `quota_per_unit` 为准;该值未设置时 `total_cost` 为 0)。`id` 不属于调用方自己的 token/租户,或从未出现过,一律 404(不是 400/403),避免向未持有该 id 的调用方泄露"格式对/不对"的探测信号。镜像 OpenRouter 的 `GET /api/v1/generation`。

两者均走标准 `Authorization: Bearer sk-...`,无需 flag,无写副作用。单位约定:`/v1/key` 的 `limit`/`limit_remaining`/`usage` 与 `/v1/generation` 的 `quota` 都是 quota 整数;`/v1/generation` 的 `total_cost` 是本指南里这两个端点唯一的 USD 计价字段。

### G. 单渠道强制 HTTP/1.1 与会话亲和运维(root 专用)

**强制 HTTP/1.1** — 渠道 `param_override`(旧版参数覆盖编辑器,控制台里没有独立开关,入口见
`web/src/components/table/channels/modals/EditChannelModal.jsx` 的"参数覆盖"字段)里加一个内部
控制键 `"__lurus_force_http1": true`,把这一个渠道的出站传输锁定为 HTTP/1.1(常见场景:某上游的
HTTP/2 实现时断时续,和真正的下线区分不出来)。该键本身不会进入发往上游的请求体——
`ApplyParamOverride`/`applyOperationsLegacy` 在合并前会跳过所有 `__lurus_` 前缀键。值必须是 JSON
布尔;写成字符串或其它 `__lurus_` 未知键会被 `ValidateParamOverride` 拒绝,不会被静默当作 false
收下——但拒绝的 HTTP 形状取决于走哪条保存路径:v2 渠道 API(`PUT /api/v2/channel/:id`)返回
**400**;上面这条旧版编辑器实际调用的 `/api/channel/`(新建 `POST`、编辑 `PUT`)返回的是
**HTTP 200 `{"success":false,"code":...,"message":...}`**——脚本化对接时必须看 `success` 字段,
不能只看 HTTP 状态码。

**范围**:该开关覆盖的是"最终经
`provider.doRequest`(`internal/adapter/provider/api_request.go`)调用 `app.GetHttpClientFor`
建出的客户端"发出的请求,以及(cycle7 L5 修复轮起)每个 `provider/task/*/adaptor.go` 的
`FetchTask` 轮询——这条路径不按渠道类型分,按"哪次调用"分,同一渠道类型下不同调用可能
一个受影响、一个不受影响。下表由
`internal/adapter/provider/task_adaptor_force_http1_structural_test.go` 的
`TestProviderTaskAdaptors_FetchTaskUsesGetHttpClientFor` 对"覆盖"列里的 task 轮询逐个校验源码
(反悔改回旧客户端会让该测试变红);其余行未被测试锁住,凡本表与代码不一致以代码为准:

| 覆盖(会受这个键影响) | 不覆盖(该次调用自己建客户端,这个键对它没用) |
|---|---|
| AWS Bedrock,API Key 模式的主请求(`aws/adaptor.go` → `provider.DoApiRequest`) | AWS Bedrock,AKSK 凭证模式(`aws/relay-aws.go:46` 自建 `bedrockruntime.Client`) |
| Coze,建对话 + 流式主请求(`coze/adaptor.go` → `provider.DoApiRequest`) | Coze,轮询结果的那次请求(`coze/relay-coze.go:284`) |
| Vertex AI,聊天主请求(`vertex/adaptor.go` → `provider.DoApiRequest`) | Vertex AI,service-account 换 token(`vertex/service_account.go:117`、`:160`) |
| Task 类渠道主请求(`internal/adapter/provider/task/*/adaptor.go` 均经 `provider.DoTaskApiRequest`,覆盖 ali/doubao/gemini/hailuo/jimeng/kling/music/sora/suno/vertex/vidu 等) | Midjourney proxy 自己的图片拉取(`internal/app/relay/mjproxy_handler.go:42`、`:51`) |
| Task 类渠道的状态轮询(同一批 `provider/task/*/adaptor.go` 的 `FetchTask`,含 hailuo) | baidu 换 access token(`baidu/relay-baidu.go:226` `getBaiduAccessTokenHelper`) |
| | dify 文件上传(`dify/relay-dify.go:100`)、replicate 图片上传(`replicate/adaptor.go:480`) |
| | 阿里图片任务轮询(`ali/image.go:200` `updateTask`,自建裸 `&http.Client{}`) |
| | v2 渠道测试路由自己的客户端(`handler/v2_channel_actions.go:64` `channelTestHTTPClient`,见下方 UAT 探针说明) |

在"不覆盖"这一列的调用上设置这个键,保存会成功但对那次调用没有任何效果(不会报错,也不会生效)。

**会话亲和(session affinity)统计与清理** — root 专用管理端点,读/清 `internal/app/session_affinity.go` 维护的多轮会话粘滞绑定:

- `GET /api/v2/admin/routing/affinity` — `data` 是扁平对象(不是嵌套 `counters{}`):`{"enabled":bool,
  "ttl_seconds":n,"hit":n,"miss":n,"stale":n,"backend":"redis"|"memory","mem_entries":n}`(`enabled`/
  `ttl_seconds` 是 `SESSION_AFFINITY_ENABLED`/`SESSION_AFFINITY_TTL` 的实时读数,`mem_entries` 只在
  `backend=="memory"` 时有意义,`backend=="redis"` 时恒为 0);顶层再附带 `"success":true,"scope":"replica"`。
  **这些计数是应答该请求的那个副本的进程内计数**(生产 3 副本 behind 同一 NodePort,同一时刻 GET 落到哪个副本随机),不是集群汇总;要看全集群总量,读 `/metrics` 的 `lurus_gateway_session_affinity_total{result}`。
- `DELETE /api/v2/admin/routing/affinity/:key` — 用中转响应头 `X-Lurus-Affinity-Key`(见 §E)拿到的 HMAC 键清掉这一条绑定,204;键不存在 404;**Redis 故障时返回 5xx 而不是 404**——404 只代表"确认查过、没有这条",不代表"没查就假定没有"。
- `DELETE /api/v2/admin/routing/affinity?all=true` — 清理当前后端能扫到的绑定(Redis 用有界 `SCAN`+`UNLINK`,不用 `KEYS`;SCAN 有轮次上限,防止游标异常或超大 keyspace 让这次调用无限跑下去),200 + `{"success":true,"data":{"purged":n,"complete":bool}}`;`complete:false` 表示 SCAN 在游标归零前就撞到了轮次上限,可能还有绑定没清完(不带 `?all=true` 返回 400)。
- 两个清理端点都写审计动作 `routing.affinity_purged`。

### H. 后台任务心跳(root 专用,L3 2026-09-13)

`GET /api/v2/admin/system/tasks` — root 专用管理端点,列出**这个 pod** 上已注册进
`internal/pkg/taskreg` 的每个周期性后台任务,与它在 `/metrics` 上的
`lurus_gateway_leader_task_last_success_timestamp_seconds{task}` 时间戳交叉核对。
200 + `{"success":true,"data":{"is_leader":bool,"pod":"<pod-id>","tasks":[{"name":"...",
"interval_seconds":n,"leader_only":bool,"last_success_at":unix,"state":"ok"|"overdue"|
"standby","standby_reason":""|"follower"|"disabled"}]}}`。

**是 per-pod 视图,不是集群汇总**——taskreg 与该 gauge 都是进程内状态,这条端点只报告
**这个副本**注册和打点过的内容;要看跨副本情况,逐副本调用或直接读 `/metrics`。

**state 三态语义**(never a bare bool):
- `overdue` — `now - last_success_at > 2×interval_seconds`(从未成功过时,`leader_only`
  且当前持有 leader 租约的副本,窗口起点从 `max(process_start, 本副本最近一次赢得 leader
  租约的时间)` 算起,给新晋 leader 一个宽限期,不然一个跑了很多天的 follower 刚接过
  leader 就会把 24h 任务误报 overdue 整整一天;`leader_only` 且非 leader 直接落
  `standby`/`follower`,不走这条 overdue 窗口计算)。
- `standby`(`standby_reason="follower"`)— `leader_only=true` 且本副本当前不是
  leader:这个任务本来就不该由它跑,`last_success_at=0` 是预期状态,不是故障。
- `standby`(`standby_reason="disabled"`)— 该任务当前被运营方关闭(目前只有
  `channel-health-test` 有这个语义:`operation_setting.AutoTestChannelEnabled` 默认
  **关**,关闭时这个任务永远不会 overdue,因为它本来就没打算跑)。

**channel-health-test 的心跳语义是"已发起",不是"已跑完"**:`testAllChannels` 把逐渠道
测试甩给一个异步 goroutine 后立即返回 `nil`,打点紧跟在这次同步返回之后——也就是说
gauge 反映的是"这一轮测试已经发起",不是"每个渠道都测完了"。

`/metrics` 上对应的 series:`lurus_gateway_leader_task_last_success_timestamp_seconds
{task="audit-cleanup"|"privacy-erasure"|"credit-pool-reconcile"|"openrouter-pool-reap"|
"channel-health-test"|"secret-rotation"|"session-sweep"|"response-registry-sweep"|...}`
——taskreg 的注册集合会随后续 lane 增长,以上不是穷举,以 `taskreg.Register` 的实际调用点
为准。**目前没有兄弟产品接入这个端点或这些 series**(grep `2c-gui-switch`/`2c-app-lutu`/
`2l-bs-docs`/`2l-svc-platform` 均无 `system/tasks` 或 `leader_task_last_success` 命中)。

### I. 委托管理员权限(delegated permission grants,L4 2026-09-13)

**背景**——`/api/v2/admin/*` 整棵子树此前只有 `RootJWTAuth` 一道闸(root 或空,中间无级差)。
四个审计只读端点(`GET /api/v2/admin/audit/{events,actions,export,chain-verify}`)现在改挂
`middleware.RootOrGranted("audit","read")`,单独拎出到 `auditRoute`(`api-v2-router.go`)——
`/api/v2/admin/*` 其余所有路由(含同目录下的 `/audit/coverage`)不受影响,仍是纯 `RootJWTAuth`。

**新的闸门语义**(仅对这四个 GET 生效):
- root(session 或 Bearer JWT)——放行,和之前一样。
- Bearer JWT 且非 root——拒绝,和之前一样(`RootOrGranted` 的 JWT 分支只认 root,见下方"仅
  session 路径"说明)。
- session 角色 `< RoleAdminUser`(10)——拒绝,和之前一样。
- **session 角色 `>= RoleAdminUser` 且未持有有效 `audit:read` 授权行——这是真实行为变化**:
  之前 `RootJWTAuth` 对这类调用方回的是 **HTTP 200 `{"success":false,"message":"无权进行此操作，
  权限不足"}`**;现在改成 **HTTP 403 `{"success":false,"message":"insufficient permission",
  "error_code":"PERMISSION_DENIED"}`**。脚本化对接必须同时兼容旧形状(其它 admin 路由仍是它)和
  这四个路由的新形状。
- session 角色 `>= RoleAdminUser` 且持有一条**有效**(未撤销、resource/action 精确匹配)授权行
  ——放行,达到之前只有 root 才能达到的效果。

**授权管理端点**(仅 root,挂在 `adminRoute`,即 `RootJWTAuth`,不受上面这道闸影响):
- `GET /api/v2/admin/authz/catalog` — 200,返回本 cycle 可授权的 `(resource, action)` 静态目录
  (目前只有 `{"resource":"audit","actions":["read"]}` 一条)和两档固定角色
  (`tenant-admin` min_role 10 / `root` min_role 100)。
- `GET /api/v2/admin/authz/grants` — 200,列出全部授权行(含已撤销)。
- `POST /api/v2/admin/authz/grants` `{"user_id":int,"resource":"audit","action":"read",
  "tenant_id":null}` — **201** `{"success":true,"data":{"id":n}}`;**400** `GRANT_INVALID`
  当 `user_id` 不存在或该用户角色 `< RoleAdminUser`(L4 修复轮 B-F4——此前接受任意正数
  `user_id`,写错一位数字会静默铸出一条永远打不开任何门的"active"行)、`(resource,action)`
  不在目录里、或 `tenant_id` 非 null(见下"全局"一节);**409** `GRANT_EXISTS` 当同一
  `(user_id, resource, action)` 已有一条未撤销的行。
- `DELETE /api/v2/admin/authz/grants/:id` — **200**(置 `revoked_at`);**404** 当 id 不存在或
  已撤销(两种情况同形状,不可区分)。

**授权是全局的(GLOBAL)**——本 cycle `tenant_id` 只接受 `null`;`RootOrGranted` 的授权检查同样
只查 `tenant_id IS NULL` 的行。持有 `audit:read` 授权的租户管理员读到的是**全平台**审计流,
**包括 root 自己的操作行**,不受任何租户边界限制——这不是本 cycle 的疏漏,是 O5 决策的既定
范围;买方如果期望"只看自己租户的审计",这个功能目前不提供。

**授权行的生命周期**——授权不会活得比持有者更久:被授权用户删除(`DeleteUserById`)、隐私擦除
级联(`lifecycle/privacy_erasure.go`)、或角色被 root 降到 `RoleAdminUser` 以下(`PUT
/api/v2/admin/users/:id`)时,该用户名下所有未撤销的授权行会被批量撤销(`repo.
RevokePermissionGrantsForUser`),各记一条 `authz.permission_revoked` 审计行——重新提升角色
不会让旧授权复活,必须重新 `POST` 一条。

**仅 session 路径**——`RootOrGranted` 的 Bearer JWT 分支和 `RootJWTAuth` 的 JWT 分支响应行为一致
(只认 root,不查授权表;两者各自的 SysError 日志前缀不同——`root_or_granted.go:60` 记
"RootOrGranted: ...",`admin_jwt_auth.go:82` 记 "RootJWTAuth: ..."——但这只影响服务端日志,不影响
调用方看到的响应):JWT 分支从不往 gin context 写入 `"id"` 键(上游 cycle-7 §8 L2 遗留
缺口),`RootOrGranted` 的授权查找需要这个键,所以委托授权本 cycle 只在 cookie session 路径上
生效;持有非 root 管理员 JWT 的调用方在这四个路由上得到的结果和之前一样(拒绝),不会因为
持有授权行而放行。

**审计**——两个写端点各自记一条动作:`authz.permission_granted` / `authz.permission_revoked`,
`resource="authz"`,`details` 带 `{grantee_user_id,resource,action,tenant_id:null}`。当写调用
走 Bearer JWT 根路径时(`granted_by`/审计 `actor_id` 记 0,因为 JWT 分支不设 `"id"`),`details`
额外带 `granted_by_sub` 记 JWT 的 subject——这是记录约定,不是拒绝这类调用的理由。

**消费方**——目前没有兄弟产品调用 `/authz/*` 或依赖这四个路由的新拒绝形状。
`2c-gui-switch`(`internal/hub/admin/governance.go:252` events、`:316` export、`:390`
chain-verify)是这四个被搬迁路由中三个的**现役消费者**,它用 `Authorization` 头逐字发送凭证
(`internal/hub/admin/client.go:166`)——`RootOrGranted` 的 Bearer-JWT 分支和 `RootJWTAuth` 的
JWT 分支响应行为一致(仅服务端 SysError 日志前缀不同,见上"仅 session 路径"一节),所以它的行为
不受这次改动影响;上面写的"HTTP 200→403 形状变化"只发生在
**session-认证的非 root 管理员**这一类调用方身上,`2c-gui-switch` 用的是 Bearer 头,不落在这
个变化范围内。
