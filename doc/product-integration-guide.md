# 产品接入指南 — Lurus 统一登录平台

> v1.1 (2026-09-19,L6 重写 :1-82 —— 原文档描述的是已退役服务的接入方式,详见文末各节改动说明) ·
> 反馈 support@quantumnous.com · 文档 https://docs.lurus.cn

Lurus 平台提供:统一身份认证 (OIDC) + AI 网关 (OpenAI 兼容 API) + 集中计费 (额度/订阅)。接入后获得:一次注册全产品通用 SSO、统一 OpenAI 格式调用、自动扣费 + 自助充值、用量监控 + Token 管理。

## 架构

Host: `hub.lurus.cn`。登录流程: 用户访问 `https://hub.lurus.cn/login`(控制台的登录页,`OidcRedirect`
组件)→ 跳转统一身份平台完成登录 → 回跳 `POST /api/v2/auth/zita-bootstrap` 建立 newhub 会话
(`internal/adapter/handler/zita_bootstrap.go`)→ 该平台账号在本服务首次出现时自动建号,落入
`default` 占位租户,除非携带一个有效的邀请码(见下)→ 用户在控制台创建 API Token (`sk-xxx`) → 配置到
产品后端 → `POST /v1/chat/completions`。

数据隔离: 用户账号在统一身份平台层面**共享**(同一账号理论上可能在不同租户下各有一条 newhub 用户记
录,取决于它在哪个租户的邀请链接下完成了第一次登录);API Token / 使用日志 / 额度计费 **按租户隔
离**;AI 渠道配置共享(可选按租户设置模型限流/白名单,见 Q3)。

## 接入步骤

**1. 创建租户** — root 管理员经控制台 Tenants 页,或直接调用管理 API:

```bash
curl -X POST https://hub.lurus.cn/api/v2/admin/tenants \
  -H "Content-Type: application/json" -H "Cookie: session=<root_admin_session>" \
  -d '{"zitadel_org_id":"<占位字符串,见下方说明>","slug":"product-b","name":"Product B"}'
# 201 → {"success":true,"data":{"id":"uuid","slug":"product-b","name":"Product B","status":1,...}}
```

`zitadel_org_id` 目前仍是必填字段(物理列名,idp-migration 完成前不会改名)——只有
`doc/runbook/tenant-onboarding.md` 描述的另一条、基于 OIDC org 声明做租户映射的登录路径会读它;下面第
2 步的邀请码路径不读这个字段,可以填任意占位字符串。完整的租户生命周期/资金池步骤见
`doc/runbook/tenant-onboarding.md`。

**2. 邀请第一个用户** — 新租户此时还没有任何用户,签发一个一次性邀请码:

```bash
curl -X POST https://hub.lurus.cn/api/v2/admin/tenants/<id>/invites \
  -H "Content-Type: application/json" -H "Cookie: session=<root_admin_session>" \
  -d '{"ttl_hours": 72}'
# 201 → {"success":true,"data":{"id":7,"code":"<32位十六进制,仅此一次返回>","status":1,...}}
```

把 `https://hub.lurus.cn/login?invite=<code>` 发给对方(控制台 Tenants 页的"邀请"抽屉做同一件事,
签发后展示一次性只读链接框 + 复制按钮)。收件人**首次**登录统一身份平台时会落入这个租户,而不是
`default`。邀请码单次有效:一旦被消费(或过期/被撤销)就不能再用;该账号之后的任何登录都不会再读它
——租户归属在第一次登录时就定死了,已经登录过的账号不会被邀请码改派。

**3. 产品后端环境变量**(拿到 Token 之后,与登录流程无关):
```bash
LURUS_API_BASE_URL=https://hub.lurus.cn
LURUS_API_KEY=sk-xxxxxxxxxxxx   # 从控制台 Token 页获取
```

## 前端集成

登录入口固定是 `https://hub.lurus.cn/login`(可带 `?invite=<code>`,见上一节)。`/login/<slug>` 这条老
路径还在,它渲染的是同一个登录屏、不做任何按租户的分流——请统一用 `/login`(可带 `?invite=`)。会话
检查 `GET /api/v2/auth/session-info`(`credentials:'include'`,返回
`data.success && data.data.id`);登出 `POST /api/v2/oauth/logout`(`credentials:'include'`)。控制台
里生成的 API Token 需要手动复制粘贴到产品后端——目前没有自动回传 Token 的回调页机制。

## 后端集成

OpenAI SDK 兼容 — 仅改 `base_url`。Python:
```python
from openai import OpenAI
client = OpenAI(api_key=os.getenv("LURUS_API_KEY"), base_url="https://hub.lurus.cn/v1")
resp = client.chat.completions.create(model="<从 GET /v1/models 或控制台 Models 页取一个本租户可路由的模型 id>", messages=[...], temperature=0.7)
```

Node.js / Go: 标准 HTTP `POST {LURUS_API_BASE}/v1/chat/completions`,Header `Authorization: Bearer ${LURUS_API_KEY}` + `Content-Type: application/json`,body `{model, messages, temperature}`,读 `choices[0].message.content`;非 2xx 时读 `error.message`。

## 测试验证

```bash
# 登录: 浏览器访问 https://hub.lurus.cn/login(带邀请码则 ?invite=<code>)→ 统一身份平台登录 → 回到控制台仪表盘
# AI 调用:
curl -X POST https://hub.lurus.cn/v1/chat/completions \
  -H "Content-Type: application/json" -H "Authorization: Bearer sk-xxxxxxxxxxxx" \
  -d '{"model":"<从 GET /v1/models 或控制台 Models 页取一个本租户可路由的模型 id>","messages":[{"role":"user","content":"test"}]}'
# 返回 chat.completion + usage{prompt_tokens, completion_tokens, total_tokens}

# 用量查询(同一把 sk- key,只读,见附录 F):
curl https://hub.lurus.cn/v1/key -H "Authorization: Bearer sk-xxxxxxxxxxxx"
# 返回这把 key 自身的 limit/limit_remaining/usage(quota 整数)、所属分组与模型白名单、RPM/TPM 限流、所属租户资金池状态
# 注意: /api/v2/{tenant}/user/me 走的是控制台会话鉴权(Bearer 处填 Token 页的 access token,不是这里的 sk- key),
# 用 sk- key 调用会得到 200 + {"success":false,"message":"...access token 无效"} —— 一个 200 形状的失败,不要在自动化里只看状态码
```

## 常见问题

- **Q1 登录后看不到我的产品?** Lurus 是 AI 网关不是产品平台。用户登录→控制台建 Token→手动配置到产品后端,目前没有自动取 Token 的回调机制。
- **Q2 多产品数据会混吗?** 不会。账号在统一身份平台层面共享,但每租户独立 `tenant_id`,Token 绑定租户,日志/计费按租户隔离,A 租户 Token 不能在 B 用。
- **Q3 为租户配专属模型?** 没有"绑定渠道优先级"的写入口。可用的两个管理端点:按模型限流 `PUT /api/v2/admin/tenants/:id/model-limits`,或按模型白名单 `PUT /api/v2/admin/tenants/:id/model-allowlist`(默认 `observe` 只记录不拒绝,需要管理员显式切到 `enforce` 才会真正挡掉白名单外的模型)。
- **Q4 额度不足?** 两处独立的额度闸,响应不同:(a) 钱包/Token/租户资金池本地额度耗尽 → 402,OpenAI 线 `type=insufficient_quota`/Anthropic 线 `type=billing_error`,`code` 视具体原因为 `insufficient_user_quota` / `token_quota_exhausted` / `pool_exhausted`;(b) 平台侧 entitlement 校验(按 `X-Lurus-Product` 归属的产品配额)拒绝 → 429,OpenAI 线 body 为 `{"error":{"message","type":"rate_limit_error","code":"quota_exceeded","metadata":{"upgrade_url":...}}}`;Claude/Gemini 调用方走各自原生信封(`{"type":"error","error":{...}}` / `{"error":{...}}`),两者均**不带** `upgrade_url`(信封结构没有 metadata 位置),只能靠 message 文案里的 `quota_exceeded` 判定,不要依赖顶层 `upgrade_url` 字段。都提示用户去钱包充值或订阅。
- **Q5 限制调用频率?** Lurus 内置 `daily_quota` 日限额;或产品侧自实现限流。兑换/激活码相关的四个 `/api` 端点(`POST /api/user/topup`、`POST /api/v2/{tenant}/redeem`、`POST /api/v2/switch/redeem`、`POST /api/v2/switch/user/topup`)额外共享同一个按调用方 IP 计的 `RD` 桶(5 次/60 秒,`rate-limit.go`)——这是**一个** IP 在 60 秒窗口内跨这四个端点合计 5 次,不是每个端点各 5 次;第 6 次起收到空 body 的 429,带 `X-RateLimit-Scope: ip`/`Retry-After`(cycle-8 L1)。
- **Q6 白标?** 当前不支持完全白标,可:自定义统一身份平台登录页主题、隐藏控制台(Token 自动管理)。
- **Q7 支持哪些模型?** 可路由的模型集来自 `GET /v1/models`(需 sk-token)或控制台 Models 页;本文不列举具体型号——目录会变,且不同租户的可路由集不同(见附录 A `/v1/models` 行)。
- **Q8 技术支持?** 邮箱 support@quantumnous.com · 文档 https://docs.lurus.cn · 企业微信群(联系管理员)。

## 附录

### A. API 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/v1/models` `/v1/models/:model` | GET | 模型发现:列表原本就从(租户盲的)ability 集作答,2026-09-09 起改为按调用方所属租户可路由的模型集(不再看得到别的租户的渠道);`:model` 单条查询原本直接答静态目录、与路由是否可达无关,现在改用同一个租户可路由集判断——查询一个该租户路由不到的模型返回 404 而不是静态目录里的 200,OpenAI 线 `type=invalid_request_error`、`param=model`、`code=model_not_found`(注意与下方 B 表 404 行的 `type=not_found_error` 不同);Anthropic 线 `type=not_found_error`,与 B 表一致,但该信封**没有 `code` 字段**——这一条要按 `type` 判,不要按本文其余各处推荐的 `code` 判;token 自带 `model_limit` 列表时,列表/单条查询按该列表作答,不做租户可路由性交叉;若该租户的模型白名单处于 `enforce` 模式,列表与单条查询都再按白名单收窄(两者共用同一个可见集,所以被白名单挡掉的模型单条查询也返回 404)。需要 sk-token;不带 token 的浏览器会话场景见下一行 `/api/v2/{tenant}/models/routable` |
| `/api/v2/{tenant}/models/routable` | GET | 控制台自用的会话鉴权模型发现(L1,cycle-11),不需要 sk-token,只需已登录会话;把上一行 `/v1/models` 列表的核心(同一个租户可路由集 + 同一份白名单收窄)抽出来给控制台四个页面(Chat/Playground/Dashboard/Token 的示例片段)直接问"这个租户能路由什么",取代此前控制台读手工目录表 `models`(结构上答不了这个问题)的 `GET /api/v2/{tenant}/models`。与 `/v1/models` 的差异:没有 token,所以不应用 token 的 `model_limit`/`group` 覆盖;`items` 按 id 排序(`/v1/models` 不排序)。响应 `{success:true,data:{items:[{id,owned_by,supported_endpoint_types}]}}`,`items` 为空数组而非 `null`。目前没有兄弟产品接入这个端点(控制台自用) |
| `/v1/chat/completions` | POST | 对话模型 |
| `/v1/embeddings` | POST | 文本向量化 |
| `/v1/images/generations` | POST | 图片生成 |
| `/v1/audio/transcriptions` | POST | 语音转文字 |
| `/v1/responses/:response_id` | GET / DELETE | OpenAI Responses API 有状态半面(cycle-8 L7, tasks-plugins-12)。POST `/v1/responses`(`store` 不为字面量 `false`、渠道类型在 `SupportsResponsesStateful` 允许列表内——本周期仅 OpenAI,与 `/v1/responses/compact` 的 `SupportsResponsesCompact` 是两张独立的表——且渠道非多密钥、且渠道响应/流事件里出现过非空 `id` 时)成功后把 `response_id` 与产生它的渠道/模型记入 `response_registry`;这两条路由据此把请求原样转发回**同一个渠道**(读该行做鉴权+定位,不经过权重选择/`Distribute()`)。**多密钥渠道不写入本表**(cycle-8 L7 修复轮 B-F3):行只钉渠道 id,不钉密钥下标,而多密钥渠道每次请求随机/轮询选密钥,回放到错误密钥上会复现本特性本要避免的"渠道自己的 404";密钥下标列是 migration 036(本周期冻结)之外的 cycle-9 跟进项。流式 POST 在**首个**携带非空 `id` 的事件即暂存(不一定是 `response.completed`——早于它的 `response.created`/`queued`/`in_progress` 同样携带 id,一个连接在收到完成事件前掉线的后台流客户端因此仍留得下可寻址的行)。归属判定为同租户且同一用户(O7);不存在的 id、归属于他人的 id、渠道已被禁用或渠道类型已不再受支持四种情况返回完全相同的 `404 response_not_found` **网关信封**,从不返回 403,调用方无法用状态码或报文区分四者——但这与渠道自己返回的 404 是两种不同的报文:一旦行存在且渠道可达,渠道自身状态码/报文(含它自己的 404)会原样透传,不带 `error.code`。不解析 usage、不计费、不写任何配额行——费用已在原始 POST 时结清。挂在独立分组(`StampRelayFormat`+`TokenAuth`+自带的 `ResponsesStateRateLimit`——每 IP 30 次/60 秒的 `RS` 桶,与 `CriticalRateLimit`(渠道 key 揭示/TOTP 禁用/审计导出共用的"CT" 桶)完全分离,429 走 `abortWithOpenAiMessage` 带 `error.code`;cycle-8 L7 修复轮 B-F1 订正——原计划文档误引 `modelsRouter` 为"同一先例",而 `modelsRouter` 只挂 `TokenAuth`,若真挂 `CriticalRateLimit` 会让这条 `/v1` 路由的轮询流量与上述 `/api/*` 控制台路由共用限流桶,互相锁人),不经过资金池/成本尖峰/并发链的 `Distribute()` 半;上游 `Content-Type` 为 `text/event-stream` 时增量转发并逐块 flush,否则整体转发。行过期由 `RESPONSE_REGISTRY_TTL_DAYS`(默认 30 天,只影响之后新写入的行——调小该值不会缩短已写入行的保留期,过期时间在写入时就已按当时的值算好戳死)控制,每小时由 leader 副本清扫,并纳入 PIPL 抹除级联硬删除(`repo.HardDeleteUserResponseRegistry`,与 `user_sessions` 同一步)。目前没有兄弟产品接入这两个端点;公开开发者文档(`2l-bs-docs` 仓 `docs/api/overview.md` 及其语言分支)的端点列表尚未加入这两条,是本轮已知的跨仓跟进项 |
| `/v1/responses/compact` | POST | `/v1/responses` 的文档化字段子集透传端点(cycle-8 L6, wire-formats-03)。这是一份白名单,不是排除列表:请求体先按该子集重新编码,只有 `model`/`input`/`instructions`/`previous_response_id`/`parallel_tool_calls`/`service_tier`/`prompt_cache_key`/`prompt_cache_retention` 真正转发给上游。`tools`/`reasoning`/`text` 会被解析(供客户端兼容),但不转发给上游(与参考实现一致)。`dto.OpenAIResponsesRequest` 其余字段——`include`/`max_output_tokens`/`metadata`/`store`/`temperature`/`tool_choice`/`top_p`/`truncation`/`user`/`max_tool_calls`/`prompt`/`stream`——一律被丢弃,渠道 `PassThroughRequestEnabled`/`PassThroughBodyEnabled` 打开时同样如此(该开关只跳过本网关自己的请求转换,不能绕过这道子集门;pass-through 分支同样应用渠道的禁用字段策略——如 `AllowServiceTier=false` 时 `service_tier` 仍会被剥离)。本周期未建模 `prompt_cache_options`(父类型也没有该字段)。渠道类型不在支持列表(本周期仅 OpenAI)时返回 `400 responses_compact_unsupported`,零上游调用。成功响应字节原样转发,`usage` 由渠道响应解析,缺失时按请求前估算的 prompt token 数计费、completion 记 0;渠道在 `200` 响应体内携带 `error` 字段时识别为上游错误并拒绝,不转发、不计费。会话亲和(`X-Session-Id`/`prompt_cache_key`)与 `/v1/responses` 共用同一套 scope 推导,同一 `prompt_cache_key` 在两个端点间派生相同的亲和 key。与 `/v1/responses` 共用同一条鉴权/资金池/限流/熔断链。目前没有兄弟产品接入这个端点 |
| `/v1/tasks/:platform` `/v1/tasks/:platform/:task_id` | POST / GET | 通用异步任务提交/状态查询(cycle-8 L8)。一条路由服务全部已编译 task 适配器(ali/doubao/gemini/hailuo/jimeng/kling/music/sora/suno/vertex/vidu,由 `TestGenericTaskPlatforms_CoverCompiledAdaptors` 锁定为与 `internal/adapter/provider/task/*` 目录数相等,而非固定数字),复用与 `/suno`、`/v1/audio/music`、`/kling/v1`、`/v1/videos` 相同的 InitTask/轮询链路;`:platform` 不是任一已编译适配器名、或 Distribute 依据请求体 `model` 选中的渠道类型与 `:platform` 声明不一致(如 `POST /v1/tasks/kling` 的 `model` 解析到一个 Suno 渠道)时均返回 404、`code=task_platform_unknown`,后者在任何上游调用之前拒绝。请求体契约:ali/doubao/gemini/hailuo/jimeng/kling/sora/vertex/vidu 九个数值适配器接受统一的 `TaskSubmitReq` 形状(`model`/`prompt`/`image` 或 `images`/`size`/`seconds`/`metadata` 等);suno 接受自身提交体并额外需要顶层 `action`(`MUSIC`/`LYRICS`,专用路由原从 URL 的 `:action` 段读取,此处改从请求体读取并内部转发);music 与专用 `/v1/audio/music` 同体。Kling/Jimeng 供应商原生请求体(`model_name`/`image`、`req_key` 等)的转换中间件不挂在本路由上,原生格式仍只走各自专用路由。计费:本接口按请求体顶层 `model` 计费(该 model 须已在渠道上配置价格),与专用 `/suno/submit/:action` 按 action 派生模型名(如 `suno_music`)计费是两条独立定价路径,同一逻辑任务经两条路由可能定价不同。状态查询直接读 `tasks` 表(由后台轮询器每 15 秒刷新,本接口自身不发上游请求),查询按 `(user_id, task_id)` 限定作用域(`task_id` 只是索引不是唯一约束,避免撞号顶替),归属校验 fail-closed——他人任务、不存在的 `task_id`、存在但 platform 不匹配三种情况返回完全相同的 404 报文 `{"error":{"type":"invalid_request_error","message":"Task not found"}}`(sora 额外接受经 OpenAI 类型渠道提交的任务),不像专用的 `GET /v1/videos/:task_id/content`(视频内容代理)对越权访问返回 403。响应体是既有 `relay.TaskModel2Dto` 投影加 `platform`/`project_id`/`request_id` 三列,不含 `channel_id`/`user_id`/`quota`/`group`/`properties`。专用的 Kling/Jimeng/视频/Suno/Music 路由行为不变,与本行并存。任务行新增 `project_id`(成本归因)与 `request_id`(提交时的网关请求 id,支持排查) 两列(migration 035)。过滤入口(cycle-8 L10):既有的 `GET /api/task/`(管理端,全租户)与 `GET /api/task/self`(用户端,`user_id` 恒定 scope)新增 `project_id`/`request_id` 两个精确匹配 query 参数,`request_id` 服务端 trim 并截到 64 字符(列宽);用户端传入自己不拥有的 `project_id` 返回空列表(`total=0`),不是 403——与 `project_id` 天然叠加在已有的 `user_id` scope 之上、不需要额外归属校验同一套道理,`GET /api/v2/:tenant_slug/logs` 的 `project_id` 过滤(`v2_log.go`)是同一约定。目前没有兄弟产品接入这组端点 |
| `/v1/tasks/:platform/:task_id/artifacts` `/v1/tasks/:platform/:task_id/artifacts/:key/content` | GET | 通用异步任务产物列表/内容代理(cycle-8 L9,建立在上一行的通用任务面之上,同一归属校验/同一 404 报文;不按 task 状态过滤——非 SUCCESS 的任务照样返回当前已有的 URL,可能是空列表)。列表接口仅元数据、不发上游请求,投影两处来源:①`Task.Data` 里**顶层**、形如 URL 的字符串字段(`http(s)://`/`data:` 开头)——这只对 suno/music 有意义,它们的 `Data` 是扁平对象;②对一个 SUCCESS 任务,若①未产出 `video` 这个 key,再看 `task.FailReason`——ali/kling/jimeng/doubao/vidu/hailuo/vertex 这 7 个数值适配器的 `Data` 是嵌套的供应商原始报文(如 kling 的 `data.task_result.videos[0].url`),真正解析出的结果 URL 由轮询器写进 `FailReason`(与专用 `GET /v1/videos/:task_id/content` 的默认分支读的是同一字段),看起来像 URL 就投影成 `video` 这个 key;sora/gemini 两个平台的 `FailReason` 是**本网关自己的** `/v1/videos/:task_id/content` 地址(不是供应商资产 URL),不投影——这两个平台的产物只能继续走专用视频代理路由。换言之:在①②都不命中之前(如任务尚未 SUCCESS、或两处都没有可用 URL),列表就是空数组。`key` 是 `Data` 里的原始字段名(对象形态)/数组下标(数组形态)/或固定值 `video`(FailReason 投影);`size` 只对 `data:` URL 有值(解码字节数),`http(s)` URL 恒为 0(未知,不为了拿大小发一次上游请求);`type` 取 `video/image/audio/text/json/other` 之一。内容代理接口按 `:key` 取那一条 URL:`data:` 直接内联解码返回零出站请求,`http(s)` 走一次受限 GET 并把上游 `Content-Type` 原样带回,并在响应上加 `X-Content-Type-Options: nosniff`/`Cache-Control: private, no-store`/`Referrer-Policy: no-referrer`(本路由的鉴权也接受 `?key=` query 参数,因此是可被浏览器直接导航的 URL,不能被缓存或经 Referer 泄漏)。**仅支持整体 GET**:不支持 `HEAD`、不转发/响应 `Range`/`If-*` 条件请求头,调用方不能用于可拖动进度的播放器。受限体现在四处,均为 `502` 且各自带 `code=artifact_request_rejected`:scheme 白名单(仅 http/https)、自指 host 拒绝(产物 URL 若与本次请求自身 Host 头同源则拒绝,防回环,与 `fetch_setting` 的私网 IP 放行策略是两道独立的检查——`fetch_setting`/`ValidateOutboundURL` 现已同时应用在本接口与下面的专用视频代理,cycle-9 L4 补上了后者此前缺失的这一道检查)、`fetch_setting` 出站检查(私网/域名黑白名单)、单次转发 200MiB 上限——已知 `Content-Length` 超限在写任何响应头之前就直接拒绝(不会返回一个字节数与声明不符的 200),未知长度的流式响应仍会在到达上限处截断,但会记入 `lurus_gateway_task_media_guard_rejections_total{route="artifact_content",reason="size_cap"}`。未知 `:key` 返回 `404`,`code=artifact_not_found`。这一对与专用的 `GET /v1/videos/:task_id/content`(视频内容代理,仍是独立路由、仍是 403 越权语义、**观测行为不完全一致**——见下条)共用了同一份 scheme 白名单/自指 host 守卫/转发上限(`task_media_guard.go`),cycle-9 L4 起也共用了 `fetch_setting` 出站检查,但不共用鉴权语义——两者是各自路由上各自的归属检查。目前没有兄弟产品接入这组端点 |
| `GET /v1/videos/:task_id/content` | GET | 专用视频内容代理(既有路由,cycle-8 L9 在其上加了新的前置校验,不是重写)。新增的 scheme/自指 host 检查与上一行共用同一份实现(`task_media_guard.go`),命中时统一返回 `502`;scheme 校验命中的那条分支刻意沿用了旧文案 `"Failed to fetch video content"`——因为这条分支此前必然会落到 `client.Do()` 的网络层失败并产生完全相同的状态码/文案(Go 标准库的 `http.Client` 本身就拒绝拨号非 http(s) scheme),所以这条校验对可观测行为而言是纵深防御,不是"新拒绝原因";自指 host 检查则是**真正新增**的拒绝(此前会真的去拨这个 URL)。**行为不再对所有历史输入字节相同**:上游声明的 `Content-Length` 超过 200MiB 上限时,此前会在 `200` 状态下静默截断转发(客户端拿到的字节数与声明的 `Content-Length` 不一致——一个已损坏的文件);现在改为在写任何响应头之前就返回 `502`。未知长度流仍在上限处截断,但会记入 `lurus_gateway_task_media_guard_rejections_total{route="video_proxy",reason="size_cap"}`。响应额外带 `X-Content-Type-Options: nosniff`。cycle-9 L4 补上了此前唯独这条路由没有的 `fetch_setting` 出站检查(`app.ValidateOutboundURL`,与渠道 egress 及 `GET .../artifacts/{key}/content` 同一函数):私网/回环目标域名或字面量 IP 返回 `502`,响应体与错误码复用 artifacts 路由的形状(`code=artifact_request_rejected`),计入同一指标 `reason="egress_check"`。已知运维约束:该检查按 fail-closed 原则解析目标域名,即便渠道配置了 per-channel 代理(本路由是两条路由里唯一支持代理的一条)也照样解析——代理是给最终的取内容请求用的,不改变出站检查自己解析目标域名这一步;因此一个"因为网关侧解析不了供应商域名才配了代理"的渠道,域名解析失败时同样会在这道检查上收到 `502`,而不是照常经代理取到内容。|
| `/v1/key` | GET | 只持一把 key 查自身额度/限流/所属租户资金池状态,无需控制台权限(详见 §F) |
| `/v1/generation` | GET | 按 `id`(己方 X-Request-Id)反查一次调用的费用/供应商/用量(详见 §F) |
| `/api/v2/{tenant}/user/me` | GET | 用户信息 |
| `/api/v2/{tenant}/tokens` | GET / POST | 查询 / 创建 Token |
| `/api/v2/{tenant}/logs` | GET | 使用日志 |
| `/api/v2/{tenant}/logs/all?upstream_request_id=` | GET | 租户管理员(`requireTenantAdmin`)专用的日志列表,可按供应商自己的 request/trace id 精确匹配过滤:取上游响应头 `x-request-id` / `request-id` / `openai-request-id` / `cf-ray` 中第一个非空的值(≤128 字节可打印 ASCII,否则视为未发送),落在管理员可见字段 `other.upstream_request_id` 上(普通用户 `/api/v2/{tenant}/logs` 看不到该字段,也不支持这个查询参数)。根管理员导出 `GET /api/v2/admin/logs/export` 接受同名参数、同语义;供应商完全没发送这些头时该字段为空,不算缺陷;该 CSV 导出没有 `other` 列(过滤只用来缩小行范围,字段本身不落进文件),v2 控制台日志页当前也没有这个过滤输入框或明细展示 |
| `/api/v2/{tenant}/analytics/rankings?by=model\|vendor\|group&hours=` | GET | 租户管理员(`requireTenantAdmin`)专用的模型/供应商/分组用量排行榜:按 token 用量降序给出 rank/环比 rank_delta(新上榜的 is_new=true、rank_delta=0)/requests_growth_pct(无上一窗口基线时为 null)/token_share_pct/quota_share_pct(份额基于当前窗口全部分组的总量,不是仅返回的最多 20 行);`by=vendor` 按 `channel_type` 聚合(名称经 `constant.GetChannelTypeName` 解析,`channel_type=0` 的历史行不计入任何 vendor 行);`by=group` 详见下面 §K;`hours` 会被收敛到 `{1,6,24,168,720}` 五档之一再作为缓存键(空值/非整数回落到默认 24h,超出 [1,720] 先截断再收敛),未知 `by` 值返回 400。响应体除 `rows` 外还带 `hours`(实际命中的档位)、`total_tokens`/`total_quota`(当前窗口全部分组的总量,不是仅返回的最多 20 行的求和,`token_share_pct`/`quota_share_pct` 即基于这两个总量计算);在进程内缓存 5 分钟(`cached_at` 可看出是否命中缓存;各副本各自维护自己的缓存,`cached_at` 在副本间可能不同,是预期行为不是缺陷)。两条路由都挂在 `CriticalRateLimit`(每 IP 20 次/20 分钟的 `CT` 桶,与渠道 key 揭示、TOTP 禁用、`/analytics/model-performance` 等 CriticalRateLimit 路由共享同一限流桶)之后,超额返回 429。根管理员等价端点 `GET /api/v2/admin/analytics/rankings?by=&hours=&tenant_id=` 额外接受 `tenant_id`(留空=跨租户)。目前没有兄弟产品接入这两个端点 |
| `/api/v2/{tenant}/billing/topup` | POST | 发起充值 |
| `/api/v2/{tenant}/sessions` | GET / DELETE(`:id`、`others`、`current`) | 控制台会话列表与撤销,整体挂在 `SESSION_REGISTRY_ENABLED`(默认关)后面:关闭时列表返回空数组并带 `"registry_enabled": false`(cycle-12 L4 起;此前是一条代表当前请求的合成行,五个浏览器登录也只显示一台设备),`DELETE :id` 一律 404、`DELETE others` 一律 409 `SESSION_REGISTRY_DISABLED`(此前是 200 `{"revoked":0}`,与「没有其它设备」无法区分),均不触碰数据库(2026-09-12 起,回滚或某次开关期遗留的行都不会被这两个端点动到);打开后列表按已登录设备逐条返回(`is_current`/`created_at`/`last_seen_at`、`ip` 按 /24(v4)或 /48(v6)掩码、`user_agent_family` 粗粒度),`DELETE :id` 撤销自己名下的一台设备(IDOR 404 语义,不属于自己的 id 与不存在的 id 同样 404)、`others` 一键撤销除当前设备外的全部。根管理员等价端点 `DELETE /api/v2/admin/users/:id/sessions`(压缩账号处置步骤,同样受该 flag 门控;关闭时同样回 409 `SESSION_REGISTRY_DISABLED`)。目前没有兄弟产品接入这组端点 |
| `POST /api/verify` `GET /api/verify/status` | POST / GET | 控制台二次确认(step-up)端点,挡在渠道 key 揭示(`/api/channel/:id/key`)、TOTP 禁用/备用码重置(`/api/user/totp/disable`、`/api/user/totp/backup-codes/regenerate`)与 2FA 强制关闭(`/api/v2/admin/security/users/:id/totp/force-disable`)四个入口前面(`middleware.SecureVerificationRequired`)。已启用 TOTP 的用户必须传 `method:"totp"`/`"totp_backup"` 加有效码;没有启用 TOTP 的用户默认经 `method:"session"` 无凭证通过(不检查任何凭证,仅凭已登录 session),这次放行记一条审计(`auth.stepup_without_credential`,命名到用户,不含凭证信息)。`SECURE_VERIFICATION_REQUIRE_ENROLLMENT`(默认关)打开后,未启用 TOTP 的用户改为 `403 STEP_UP_ENROLLMENT_REQUIRED`,且不再无凭证放行(同样记一条 `auth.failed` 审计,`reason:enrollment_required`)。`GET /api/verify/status` 响应体新增 `enrollment_required` 字段,反映该开关当前值(进程级配置,与调用者无关),控制台据此判断"session"这一路是否可用。默认关是基于 2026-09-15 的生产数据:唯一 role>=10 的账号(root)当时没有 TOTP 记录,打开会锁死它对渠道 key 揭示与 2FA 强制关闭的访问,见 `doc/runbook/incident-response.md` 的 break-glass 步骤。目前没有兄弟产品接入这组端点 |
| `GET`/`POST /api/v2/admin/tenants/:id/invites`、`DELETE /api/v2/admin/tenants/:id/invites/:invite_id` | GET / POST / DELETE | root 专用的租户邀请码管理(L6,见"接入步骤"第 2 步)。`POST` 签发一次性码(`ttl_hours` 可选,`<=0`/省略=永不过期),201 响应带明文 `code`。`GET` 列表投影为 `{id, code_prefix(Code 前 8 位), status, expired_time, consumed_by_account_id, consumed_at, created_by_user_id, created_at}`——列表投影刻意丢弃 `code`,只留前缀;`IssueTenantInvite` 的 201 是目前唯一带出明文码的产出点(`internal/adapter/handler/tenant_invite.go`);`:id` 不是已存在的租户 id 时两个端点均 404。`DELETE` 撤销一个仍处于 `status=1`(pending)的码,已消费/已撤销/属于其它租户的 id 一律 404(与"不存在"同一报文,不给探测信号)。`status`:`1` 待使用、`2` 已消费(`consumed_by_account_id` 记录消费者的平台账号 id)、`3` 已撤销。码本身只在 `handler.ZitaBootstrap` 的首次登录自动建号分支被消费(`internal/adapter/repo/tenant_invite.go` 的 `ConsumeTenantInvite`),不影响任何既有用户的登录。目前没有兄弟产品接入这组端点 |

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

> **无代码锚点**(2026-09-19 复核):本节描述的"租户级充值/订阅事件出站 webhook"在当前代码里找不到实
> 现——`internal/app/webhook.go` 的 `SendWebhookNotify` 是真实存在的函数,但它的调用方是**用户级**告
> 警通知(`internal/app/user_notify.go`,配额告警等,读 `user_setting.WebhookUrl`/`WebhookSecret`),
> 不是按租户配置、以充值/订阅事件为 payload 的机制;没有 `tenant_slug`/`quota_before`/`quota_after`
> 这个 body 形状的任何写入或读取路径。以下段落保留供将来对照,不代表现有行为。

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

**渠道敏感字段写入闸门(`channel:sensitive_write`,cycle-9 L2)**——上面这条 `/api/channel/`
POST/PUT 和它的 v2 对应端点,现在对**非 root 管理员**(role 10)额外挡一道:请求实际改动
`key`/`base_url`/`param_override`/`header_override`/渠道级 proxy(`setting` blob 里的 `proxy`
成员)/`type`/`other`/`openai_organization` 这八个字段中任意一个时,调用方必须持有一条有效的
`channel:sensitive_write` 授权行(见上"授权管理端点"一节),否则 **403**
`{"success":false,"message":"insufficient permission","error_code":"PERMISSION_DENIED"}`,且
这次写入**完全不落地**(逐列核对过——不是把敏感字段静默剥掉再存非敏感部分)。"改动"按值比较,不
按字段是否出现在请求体里判——两个渠道编辑器都会在每次保存时把这几个字段原样带上(包括不动它们
的纯改名请求),按出现与否判会把每一次编辑都拒掉。models/group/name/priority/weight/status 不在
这个集合里,普通管理员改这些不需要授权。

覆盖的写入面(v1 与 v2 各自独立闸门,不是共用一次检查):
- v1 `POST /api/channel/`(新建)、`PUT /api/channel/`(编辑)
- v1 `POST /api/channel/copy/:id`(复制——克隆行携带源渠道的 key/base_url,和新建同等力度)
- v1 `POST /api/channel/multi_key/manage` 的 `delete_key`/`delete_disabled_keys` 两个 action(删
  除存量 key 与替换 key 属同一类凭证变更;该端点的其余 action——enable/disable/get_key_status——
  不碰 key 列,不受影响)
- v1 `PUT /api/channel/tag`(标签批量编辑器——对这一个端点按字段"是否出现"判,不按值比较,因为它
  把一个值套用到多行,没有单一"原值"可比;它没有 `base_url`/`key` 字段可传,只有
  `param_override`/`header_override` 落在这次闸门里)
- v2 `POST /api/v2/:tenant_slug/channels`(新建)、`PUT /api/v2/:tenant_slug/channels/:id`(编辑)

root(role ≥ 100)在以上任何一条路由上都不受影响。

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
  (`{"resource":"audit","actions":["read"]}` 与 cycle-9 L2 新增的
  `{"resource":"channel","actions":["sensitive_write"]}` 两条)和两档固定角色
  (`tenant-admin` min_role 10 / `root` min_role 100)。`channel:sensitive_write` 解锁的不是这四个
  审计只读路由,是 §G 之后新增小节描述的渠道写路由上的一道独立闸门——两条目录行对应两套完全不同的
  受保护路由,不要假设"能授权就是能读审计"。
- `GET /api/v2/admin/authz/grants` — 200,列出全部授权行(含已撤销)。每行新增(cycle-9 L1)
  `expires_at`(unix 秒,nullable)和服务端派生的 `expired` 布尔——`expired` 只看
  `expires_at` 是否已过 now,和 `revoked_at` 无关,两者可以同时为真(一条既过期又被显式撤销
  的行)。
- `POST /api/v2/admin/authz/grants` `{"user_id":int,"resource":"audit","action":"read",
  "tenant_id":null,"ttl_seconds":int|null}` — **201**
  `{"success":true,"data":{"id":n,"expires_at":int|null}}`;`ttl_seconds`(cycle-9 L1)是可选
  字段,省略时该授权行永久有效,和这个字段存在之前的行为完全一致;给出时必须落在
  `1..7776000`(90 天)闭区间内,越界返回 **400** `GRANT_INVALID`(复用既有 error_code,
  不是新码)。**400** `GRANT_INVALID` 同样覆盖:当 `user_id` 不存在或该用户角色
  `< RoleAdminUser`(L4 修复轮 B-F4——此前接受任意正数 `user_id`,写错一位数字会静默铸出一条
  永远打不开任何门的"active"行)、`(resource,action)` 不在目录里、或 `tenant_id` 非 null
  (见下"全局"一节);**409** `GRANT_EXISTS` 当同一 `(user_id, resource, action)` 已有一条
  **仍然存活**(未撤销且未过期)的行——一条已过期但从未显式撤销的旧行不会导致这个 409:
  `CreatePermissionGrant` 在同一事务里先把过期旧行的 `revoked_at` 置位,再插入新行,新旧两行
  都在授权行的历史列表里可见。
- `DELETE /api/v2/admin/authz/grants/:id` — **200**(置 `revoked_at`);**404** 当 id 不存在或
  已撤销(两种情况同形状,不可区分)。对一条已过期但未撤销的行调用同样返回 200(允许显式撤销一
  条已经在功能上失效的行)。

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
`resource="authz"`,`details` 带 `{grantee_user_id,resource,action,tenant_id:null}`;创建端点
(cycle-9 L1)额外带 `ttl_seconds`(给了就是那个整数,没给就是 JSON `null`)。当写调用
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

### J. 转换保真诊断 (`other.conversion_dropped`,cycle-9 L5)

Claude-wire (`/v1/messages`) 与 Gemini-wire (`/v1beta/...`) 两条跨协议转换路径
(`internal/app/convert.go` 的 `ClaudeToOpenAIRequest` / `GeminiToOpenAIRequest`)只把一部分请求
字段映射到上游 OpenAI 格式请求,调用方设置了但转换器不认识的字段(如 Claude-wire 的 `top_k` /
`tool_choice`,Gemini-wire 的 `toolConfig` / `thinkingConfig` 等)此前被静默丢弃,调用方无法从产品
里得知。成功行的日志 `other` 现在带一个新键:

| 字段 | 类型 | 语义 |
|------|------|------|
| `other.conversion_dropped` | `string[]`,可选 | 本次请求里调用方**自己设置过**但转换器未能映射(或只做了部分映射)到上游请求的字段名,按上游 wire 的原始命名(Claude-wire 用 `top_k`/`tool_choice` 这类 snake_case;Gemini-wire 用 `toolConfig`/`thinkingConfig` 这类 camelCase),排序去重,只列出下面"覆盖范围"里判定为丢弃的字段——请求没有触发任何判定时该键完全不出现(不是空数组)。上限 16 个名字,超出时保留排序后的前 15 个并追加字面量 `…(truncated)` 作为第 16 个元素(这个字符串本身不是字段名,遇到它说明列表被截断,不是发现了一个叫这个名字的字段);单个名字超过 32 字节会被截断到 32 字节。TierPublic(`internal/app/governance/classification.go`),普通用户可见,`GET /api/v2/{tenant}/logs`(普通用户自查)与管理端的 `GET /api/v2/{tenant}/logs/all` 均不剥离这个键 |

**覆盖范围**——这个键覆盖的判定分三类,精确到调用方能依据它做什么:

1. **顶层从不读取的字段**:转换器代码里完全没有引用的请求字段,例如 Claude-wire 的
   `context_management`/`output_config`/`output_format`/`container`/`mcp_servers`/
   `service_tier`/`max_tokens_to_sample`/`prompt`,Gemini-wire 的
   `safetySettings`/`cachedContent`/`responseSchema`/`seed` 等一整批 `GenerationConfig` 字段——调
   用方设置了这些字段,这个请求就必然带上对应名字。
2. **有条件映射,按分支实际结果判定**:`thinking` 只有在转换分支真正产出了上游 `Reasoning` 载荷
   或改写了模型名(`-thinking` 后缀)时才算保留,否则报 `thinking`;`metadata` 只有当它携带
   `user_id` 以外的键时才报——newhub 自己会读 `metadata.user_id` 投影进 `other.end_user`
   (`internal/adapter/provider/common/relay_info.go` 的 `deriveEndUserHash`),只含 `user_id` 的
   `metadata` **不会**出现在 `conversion_dropped` 里,这不是遗漏。
3. **部分映射,整字段名归并报告**:`tools` 在两条 wire 上都只做了部分转换——Claude-wire 只保留
   工具的 `name`/`description`/`input_schema`,某个工具带非 `function` 的 `type`(例如 Anthropic
   的内置 `web_search` 工具)或带 `cache_control` 块,就报 `tools`(不区分是这个数组里第几个工具、
   丢的是哪一部分);Gemini-wire 的工具数组里只转换带 `functionDeclarations` 的条目,
   `googleSearch`/`googleSearchRetrieval`/`codeExecution`/`urlContext` 这四种内置工具各自用独立
   的点号名字报告(`tools.googleSearch` 等),不归并进 `tools`。Gemini-wire 顶层的批量字段
   `requests`(只有 vertex 适配器和 token 计数会读)也在此列,报 `requests`。

**未覆盖的残留(键缺席≠该请求完全保真)**:上面三类之外的任何丢失都不会出现在这个键里——已知的
残留包括:Claude-wire 工具的哪个具体子字段被丢(`tools` 只是整字段名的粗粒度标记);未来
Anthropic/Google 新增的工具类型在代码更新前不会被识别;`thinking.type != "enabled"` 之外还有
`thinking` 语义细节(比如具体 budget 数值被上游拒绝)不在这个诊断的范围内,它只回答"这个设置有没
有以任何形式影响上游请求"。

**范围边界,明确写出以免被当成疏漏**:只有成功结算的请求会带这个键——它在
`app.GenerateTextOtherInfo`(即 `compatible_handler.go:490` 调 `PostConsumeQuota` 之后走的那条
success-path 日志生成函数,Claude/audio/wss 三个变体生成器都会经过它)里投影。`internal/adapter/
handler/relay.go`(`recordRelayErrorLog`,约 757-803 行)的终态错误日志路径是另一套独立、逐键拼装
`other` 的代码,不读取这个字段——一次失败的请求(包括触发这段转换代码之后才失败的请求)不会有
`conversion_dropped`。这不是本 cycle 计划做但没做完,是 L5 明确排除在范围外的部分。

**目前没有兄弟产品接入这个字段**(`2c-gui-switch`/`2c-app-lutu`/`2l-bs-docs` 均无
`conversion_dropped` 命中)。

### K. Rankings 新增 `by=group` 维度(cycle-9 L7)

`/api/v2/{tenant}/analytics/rankings` 与根管理员端点 `/api/v2/admin/analytics/rankings` 的 `by`
查询参数在既有 `model`/`vendor` 之外新接受第三个取值 `group`——按 `logs` 表已有的 `group` 列
(`internal/domain/entity/log.go` 的 `Group` 字段)聚合。这一列记录的是**请求实际使用的分组**
(调用方令牌/用户的分组,auto 跨组重试时可能变动——见 `internal/adapter/middleware/auth.go:747`
写入 `ContextKeyUsingGroup`、`internal/adapter/provider/common/relay_info.go:96` 的
`RelayInfo.UsingGroup` 注释),不是渠道自身的 `Group` 列;聚合表达式是
`COALESCE(NULLIF("group", ''), '(ungrouped)')`,一个表达式而非裸列,不会用到该列上的 btree
索引,查询成本与 `by=model`/`by=vendor` 相同的窗口扫描一致。响应形状与 `by=model`/`by=vendor`
完全一致(`rank`/`rank_delta`/`is_new`/`requests`/`requests_growth_pct`/`total_tokens`/
`token_share_pct`/`quota`/`quota_share_pct`)。空字符串 `group` 的行会被合并成一个显式的
`(ungrouped)` 分组,不会以空名称单独出现,也不会被丢弃——已知非目标:分组名字面量就是
`(ungrouped)` 时无法与空分组桶区分(分组名是自由文本,`internal/adapter/middleware/auth.go:738-744`
只检查是否在比例表里出现,不限制取值)。未知 `by` 取值(既非 `model`/`vendor` 也非 `group`)仍然
400,错误信息由 `by must be model or vendor` 改为 `by must be model, vendor or group`
(`internal/adapter/handler/v2_analytics_rankings.go`)——按文本匹配旧信息的调用方需要更新。这是
纯增量:不改变 `by=model`/`by=vendor` 的既有行为、不改变响应形状、不影响租户范围(`by=group`
同样只返回调用方自己租户内的分组)。

**目前没有兄弟产品接入 `by=group`**(`2c-gui-switch`/`2c-app-lutu`/`2l-bs-docs` 均未见对
`analytics/rankings` 端点的调用)。

### L. 结算失败标记 (`other.settlement`,cycle-11 L7)

三个走 `app.SettleConsume` 的结算点(`relay.postConsumeQuota` / `relay/compatible_handler.go`、
`app.PostClaudeConsumeQuota`、`app.PostAudioConsumeQuota`)在 `PostConsumeQuota` 结算调用返回
错误时,除了保留原有的 `logger.LogError` 日志行,现在还会在这一行的日志 `other` 里写一个新键:

| 字段 | 类型 | 语义 |
|------|------|------|
| `other.settlement` | `string`,可选,唯一取值 `"failed"` | 本次请求的结算调用(`app.PostConsumeQuota`,经由 `app.SettleConsume`)返回了错误。该请求结算成功时这个键完全不出现(不是写 `"ok"` 之类的值)。TierPublic(`internal/app/governance/classification.go`),普通用户可见,`GET /api/v2/{tenant}/logs`(普通用户自查)与管理端的 `GET /api/v2/{tenant}/logs/all` 均不剥离这个键 |

**这个键不说明的事**:`other.settlement` 只反映结算调用本身是否报错,不说明这笔请求对应的
租户信用池(credit pool)是否已经被扣款——`PostConsumeQuota` 的租户池扣款
(`internal/app/quota.go:1000-1002`)先于可能失败的 token 配额更新
(`internal/app/quota.go:1016-1046`)执行,后者失败时只补偿了 user 配额那一条腿,池扣款不会撤销。
一行带 `other.settlement="failed"` 的日志,其租户池仍可能已经被真实扣款过。控制台上对应的
"settlement failed"/"结算失败" 徽章同样不是退款凭证,只说明"这次结算调用报了错"。

**覆盖范围边界**:这个键只在上面三个结算点写入。`internal/app/relay/mjproxy_handler.go`(Midjourney
代理任务回调)、`internal/app/relay/relay_task.go`(异步任务结算,如 Suno)里直接调用
`app.PostConsumeQuota` 的调用点,以及 `internal/app/quota.go` 的 `PostWssConsumeQuota`(实时/
WebSocket 结算路径,根本不调用 `PostConsumeQuota`)都不在这个 cycle 的覆盖范围内——这些路径上的
结算失败仍然只有一行 `common.SysLog`,既不带 `other.settlement`,也不计入新增的
`lurus_billing_settlement_failed_total` 指标。详见 `doc/runbook/settlement-failed.md` 的
"Not covered this cycle" 一节。

**目前没有兄弟产品接入 `other.settlement`**(`2c-gui-switch`/`2c-app-lutu`/`2l-bs-docs` 均无对
`other.settlement` 或 `settlement` 字段的读取)。
