# System One 渠道运维(TypeSafe 托管 / Laya 自托管)

> 对外入口 `POST https://hub.lurus.cn/v1/systemone`(OpenAPI: `docs/openapi/relay.json`),与 TypeSafe 官方 API 字节兼容。
> 两种渠道类型共用一个适配器包 `internal/adapter/provider/systemone`。无告警触发,本文是接渠道与排障的操作手册。
> 数字来源:2026-10-01 对 laya-serve 0.3.22(CPU,Windows)的实测与源码核对,`laya` 部分只是单机实测,不是容量承诺。

## 1. 它是什么

System One 是「一个 state + 一组类型化问题 → 每题一个概率型答案」的接口:`noul`(是非概率)、`choice`(单选)、`score`(评分)。
网关只做转发与计量,不做推理。

| 渠道类型 | 值 | 上游 | base URL | key |
|---|---|---|---|---|
| TypeSafe | 57 | 托管 Jev | 默认 `https://api.typesafe.ai` | 必填 |
| System One 兼容(自托管) | 58 | 任意 Jev 兼容服务,实测对象是 `laya-serve` | **必填**,不带 `/v1` | 可选(服务端没开鉴权就留空,网关不会发空的 `Bearer `) |

对外模型名:

| 公开名 | 渠道类型 |
|---|---|
| `jev-latest` / `jev-preview` / `jev-1.13.0` | TypeSafe。目前前两个都指向 `jev-1.13.0`(上游文档) |
| `laya-auto` / `laya-english` / `laya-multilingual` / `laya-typed-decisions` | 兼容(自托管) |

默认价格:以上所有模型 = $0.042 / 百万输入 token,**只按输入 token 计费,输出免费**(与 TypeSafe 官方标价一致;laya 同价,作为自托管算力回收,可在模型倍率设置里改)。
当前不支持批量(`/v1/systemone/batch`)与流式。

## 2. 加 TypeSafe 渠道

1. 在 TypeSafe 控制台创建 API key(厂商侧操作,key 只在创建时可见)。
2. 网关控制台 → 渠道 → 新建,类型选 **TypeSafe**;base URL 留空用默认;key 填上一步的 key。
3. 模型填 `jev-latest,jev-preview,jev-1.13.0`,分组按需。`model_mapping` 一般不用填;若要把公开名钉到某个版本,映射成上游版本化 ID。
4. 价格已有默认,核对模型倍率里三个 `jev-*` 都有;缺倍率的模型请求会在定价阶段报错。
5. 渠道测试通过后再放量。TypeSafe 限速(文档)100K token/s、80 req/s(此前写 40 req/s,已按官方文档更正),且会动态调整;超限返回 429,网关会换渠道或重试。上下文窗口 64k token,state 加最长单题须 ≤32k(官方口径,超出由上游拒绝或截断,网关不预检)。官方 SDK 0.6 起 `Score.criteria` 是有序序列;网关 `Criteria` 字段是 `json.RawMessage` 原样透传,不受该变化影响。

## 3. 起 laya-serve 并加兼容渠道

```bash
pip install "laya[serve]"            # CPU 机器先装 CPU 版 torch 再装它;GPU 机装对应 CUDA 版
export LAYA_API_KEY=<随机长串>        # 必设:不设则完全免鉴权
export LAYA_HOST=127.0.0.1 LAYA_PORT=8765
export LAYA_MAX_LOADED=2             # 见下,避免按语言来回换检查点
export LAYA_PRELOAD=1                # 生产建议预加载,否则首个请求要先加载模型
laya-serve
```

要点(均为实测/源码):

- **内存**:每个常驻检查点约 2 GB RSS(实测 1.9~2.2 GB);检查点文件 english 842 MB、multilingual 644 MB。`LAYA_MAX_LOADED=1` 时换检查点会先建新的再逐出旧的,实测峰值 3.9 GB。
- **`LAYA_MAX_LOADED=2`**:中英混合流量必设。`=1` 时每次语言切换要多付约 1.7~2.8 s(实测 en→zh 1.95 s、zh→en 2.4 s)。两个检查点常驻的稳态内存是推算值(约 3.5 GB),未实测。
- **冷启动**:首次下载权重很慢(实测 english 77 s、multilingual 231 s);`LAYA_PRELOAD=0` 时首个请求承担这个代价。国内下载走镜像:`HF_ENDPOINT=https://hf-mirror.com`。
- **延迟(CPU,4 线程,单推理线程)**:英文 1 题约 270 ms、5 题约 970 ms;中文(multilingual 路由)1 题约 125 ms、5 题约 420 ms。同一次测试里 3 个 state × 5 题的 `/batch` 约 2.65 s。GPU(T4)第三方测得 p50 约 33~40 ms,本地未复测。
- **并发**:单推理线程,请求串行。在途请求超过 `LAYA_MAX_CONCURRENT`(默认 16)立刻 503 + `Retry-After: 1`,不排队(实测 24 并发 5 题请求:16 个 200、8 个 503)。容量不够就加实例并在网关侧挂多个渠道,而不是调大该值。
- **暴露面**:`/docs`、`/openapi.json`、`/redoc`、`/health` 全部免鉴权(`/health` 泄露已加载检查点、revision、设备)。只绑内网/回环,反向代理里**封掉这四个路径**,只放行 `POST /v1/systemone`。
- **R6 出站**:`lurus-newhub` 的 NetworkPolicy 只放公网 80/443,RFC1918 全部 except,集群内走不到内网自托管地址。需要走公网域名 + HTTPS 反代,或 per-channel 代理;放行策略改 git 源 manifest,不要 `kubectl patch`。

网关侧新建渠道:类型 **System One 兼容(自托管)**,base URL = 反代地址(不带 `/v1`),key = `LAYA_API_KEY`。

### 私网地址(建渠道会被 SSRF 闸拒)

渠道 base_url 是私网/回环地址时,建渠道直接失败:`channel base_url rejected: private IP address not allowed`。不要全局放开私网,只放行这一台(root 在「系统设置 → SSRF」或 `PUT /api/option/`):

| option key | 值 | 作用 |
|---|---|---|
| `fetch_setting.allow_private_ip` | `true` | 允许私网 IP 进入下面的名单判断 |
| `fetch_setting.ip_filter_mode` | `true` | IP 名单切成**白名单** |
| `fetch_setting.ip_list` | `["10.0.0.12/32"]` | 只写 laya-serve 那台的地址 |

域名形式的上游(如 `api.typesafe.ai`)不受 IP 白名单影响;其他私网地址仍被拒。2026-10-01 本机实测此配置下建渠道、转发、控制台「测试渠道」均通过。

### 价格

默认价 = TypeSafe 官方价 $0.042 / 1M 输入 token(倍率 0.021),输出恒为 0。运营在「定价」里给某个模型设了自己的倍率就以运营的为准;没设时回落到默认价(生产库存过倍率配置,整表会覆盖代码默认表,所以这里做了按模型名的兜底,否则加完渠道第一条请求就是「ratio or price not set」)。

### model_mapping(必填)

laya-serve 对**任何未知 model 名都静默回退到按文字脚本自动路由**(没有 404/422,`jev-latest` 也一样),所以必须把公开名显式映射成它认识的检查点名,否则调用方得到不可预期的检查点:

```json
{
  "laya-english": "english",
  "laya-multilingual": "multilingual",
  "laya-typed-decisions": "typed-decisions",
  "laya-auto": "laya"
}
```

`laya` 在上游表示「自动路由」。响应里的 `model` 由网关回填为调用方请求的公开名(laya-serve 自己恒返回常量 `laya-rl-agent`)。`typed-decisions` 检查点本次从未加载实测过,上线前先单独验。

## 4. 已知质量限制(自托管)

只来自观察到的答案与上游 README,不是基准测试:

- 零样本偏弱:一条中文 noul 样例(「否则我会取消订阅」是否威胁取消)返回 0.0687(自信地答「否」),同句英文返回 0.884。中文 choice 路由是对的。
- `confidence` 不能和 Jev 比:自托管是 1 − 归一化熵,noul 的 confidence = max(p, 1−p);Jev 是另一套公式,noul 没有该字段。**阈值不可迁移**,按渠道各自标定;自托管侧可以看 `answer_confidence`。
- 选项多了就不可用:超过约 20 个选项明显掉准;40、100 个选项实测概率近乎均匀。每题至多 100 选项(超出 413)。
- 过长 state **静默截断且返回 200**:english 窗口约 512 token、multilingual 约 1024,实测 38k 字符英文 state 返回 `usage.truncated=true`;网关对外保留 `truncated`(仅为 true 时出现)。state > 50000 字符、请求体 > 2 MiB、问题 > 64 个直接 413。
- english 检查点在非英文文本上会崩(自信地答错)。中文请走 `laya-multilingual` 或 `laya-auto`。
- `input_tokens` 随问题数近似线性放大(每题是独立的一行 state+题);与 Jev 的计数口径不同,不可相加或比较。

## 5. 错误映射(网关对调用方的行为)

错误体一律是网关的 OpenAI 风格信封(`error.message`),官方 SDK 按 HTTP 状态码映射异常。上游错误文本只取 `detail` 字符串 / `detail.message` / `detail[].msg` 并净化。

| 上游 | 状态 | 归类 | 网关动作 |
|---|---|---|---|
| 两者 | 401 / 403 / 404 / 405、连接错误 | **渠道故障**(凭据失效或 base URL 配错) | 换渠道;**绝不把上游 401 透给调用方**(否则调用方以为自己的 key 错了)。需要人工查渠道 key / base URL |
| 两者 | 400 / 413 / 422 | **调用方错误** | 原状态码返回净化后的消息,不重试、不计熔断失败 |
| 两者 | 429 / 529 / 503 | 上游过载,可重试 | 换渠道/重试;最终仍是限流时透传 `Retry-After`(含 `retry-after-ms` 换算)。laya 的 503 固定 `Retry-After: 1` |
| 两者 | 其他 5xx(含 Cloudflare 52x 的 HTML 页) | 上游故障 | 重试,计入熔断失败 |
| laya | 200 + `usage.truncated=true` | 不是错误 | 原样返回 `truncated: true`,调用方自行判断 |
| 两者 | 200 但响应自相矛盾(**响应校验失败**) | **上游内容缺陷**,不是渠道配置问题 | 返回 502 `invalid_provider_response`;**不切渠道**(skip-retry,错误码不带 `channel:` 前缀,不会自动封禁渠道;换渠道只会让调用方为同一个坏答案付两次钱);上游 `usage` 有效时**仍按输入 token 计费**(推理已发生),无有效 usage 则不计费;答案正文不透给调用方 |

与上表 401/403 的区别:401/403 是**渠道凭据/地址故障**,换渠道并可能自动封禁;响应校验失败是**这一次答案不可信**,不换渠道、不封禁,但仍计费。校验规则(`provider/systemone/validate.go`):概率和 1±0.01;choice 所选概率 ≥ 最大值−0.01(choice 为 null 表示弃权,合法);score 与 Σ序号×概率 的偏差 ≤ 0.01×(N−1);score 的 legend 必须与请求 criteria 逐级一致;回答的问题 ID / 类型必须与请求一致(请求中有而响应缺失的问题不判错)。`x-typesafe-request-id` 只写入日志的 upstream_request_id 槽位,不透给调用方。

排障顺序:调用方报 503「无可用渠道」→ 先看各 System One 渠道是否被熔断/自动封禁(`channel-breaker-open.md`、`channel-auto-ban.md`)→ 再直连上游验证(laya:`curl -H "Authorization: Bearer $LAYA_API_KEY" -d '{...}' $BASE/v1/systemone`;注意 401 = key 不一致)。

## 6. 客户怎么调

官方 SDK,只需改两个环境变量(base URL 不带 `/v1`,SDK 自己拼 `/v1/systemone`):

```bash
pip install typesafe-sdk
export TYPESAFE_BASE_URL=https://hub.lurus.cn
export TYPESAFE_API_KEY=<网关发的 key>          # 即普通 Bearer key
export TYPESAFE_DEFAULT_MODEL=jev-latest        # 可选;SDK 默认就是 jev-latest
```

curl:

```bash
curl https://hub.lurus.cn/v1/systemone \
  -H "Authorization: Bearer $HUB_KEY" -H "Content-Type: application/json" \
  -d '{
    "state": "Help! My payouts have been failing for 3 days.",
    "model": "jev-latest",
    "questions": {
      "is_urgent": {"type": "noul", "instructions": "Does this convey urgency?"}
    }
  }'
```

响应 `{model, answers, usage:{input_tokens, output_tokens[, truncated]}}`。
换成自托管只改 `model`(如 `laya-multilingual`);自托管渠道另外接受可选的 `lang`、`min_confidence`、`max_len`、`head_max_len`,发给 TypeSafe 渠道时这四个字段会被丢弃。批量/钩子字段会被网关拒绝。

响应头 `X-Request-Id` 可配合 `GET /v1/generation?id=<值>` 反查费用与用量。

## 7. 内容规则覆盖 systemone

租户/平台的内容规则(mask / reject,`doc/runbook` 里数据管控相关文档)对 `/v1/systemone` 与对话类接口一样生效,在请求解析前改写网关缓存的原始 body,所以托管与自托管两种渠道转发出去的都是处理后的字节。

覆盖的文本(全部按 `user` 角色匹配,`role_scope` 为 `user` 或 `any` 的规则命中):

- `state`:字符串;若调用方传对象/数组,则其中所有字符串值逐个处理(键名与数字不动)。
- 每题 `instructions` 与 `criteria`:字符串直接处理;数组/对象则递归处理其中的字符串值。

不处理:`type`、`labels`、`option_order`、`model`、`lang` 等其余字段与未知字段,原样转发;JSON 键顺序不变。

行为与其他格式一致:

| 模式 | 结果 |
|---|---|
| enforce + mask | 上游收到的 body 里命中片段被替换为占位符(如 `[PHONE]`) |
| enforce + reject | 返回 400 `content_rejected`,消息只含规则 ID,不含命中文本 |
| observe | 只计数与审计(`content_rule_hit`),body 不改 |
| body 非合法 JSON / 形状不符 | 不处理,交给正常解析路径报错;规则库读取失败时放行并记系统日志 |

注意:mask 会改变送入上游的文本,`usage.input_tokens` 按改写后的内容由上游计量;题目 id(`questions` 的键)不扫描,不要把敏感信息放在题目 id 里。

## 8. 决策模型路由(按请求内容自动选模型)

租户管理员可以给一个「对外模型名」配一条决策路由策略:请求到来时,网关先让一个评估模型(System One 类型模型,例如 `laya-auto`)读用户的第一句话,在管理员写好的候选里挑一个,再把请求的 `model` 改写成候选对应的真实模型。没有策略的租户行为与今天字节一致(代码入口 `internal/adapter/middleware/decision_routing.go` `ApplyDecisionRouting`,无策略时只做一次内存查表就返回)。

### 8.1 怎么配

`GET|PUT|DELETE /api/v2/{tenant_slug}/routing-policies/{model}`(租户管理员;契约见 `docs/openapi/api-v2.yaml` 的 `RoutingPolicyRequest`)。`PUT` 是整份覆盖,写入前整体校验,任何一项不合法则什么都不写:

| 字段 | 规则 |
|---|---|
| `enabled` | 启用必须同时给出 `evaluator_model`(否则 400 `EVALUATOR_REQUIRED`) |
| `evaluator_model` | 评估模型名,至多 128 字节 |
| `instructions` | 给评估模型的说明,至多 4096 字节;不写则用内置默认说明 |
| `min_confidence` | 取值 [0, 1];不写取 0.65 |
| `default_candidate` | 候选 id 之一;对外模型名本身不在候选里时必填 |
| `candidates[]` | 1–32 项,每项 `id`(1–64 字符,字母数字与 `_ . -`,不重复)、`model`(租户能路由的公开模型)、`criteria`(必填,至多 2048 字节) |

校验失败返回 400 与 `error_code`(`INVALID_CANDIDATES`、`MODEL_NOT_ROUTABLE`、`CRITERIA_TOO_LONG` 等,全集见 `internal/app/routingdecision/policy.go`)。写入与删除分别记审计 `routing.policy_set` / `routing.policy_deleted`;审计详情只记变更的形状,不记 `instructions` 原文。

```bash
curl -X PUT https://hub.lurus.cn/api/v2/~/routing-policies/auto-chat \
  -H "Authorization: Bearer <access token>" -H "Content-Type: application/json" \
  -d '{
    "enabled": true,
    "evaluator_model": "laya-auto",
    "min_confidence": 0.7,
    "default_candidate": "general",
    "candidates": [
      {"id": "general", "model": "model-a", "criteria": "Everyday questions and short answers."},
      {"id": "heavy",   "model": "model-b", "criteria": "Long analysis, code generation, multi-step reasoning."}
    ]
  }'
```

### 8.2 评估模型放哪

评估模型就是一个普通的 System One 渠道上的模型(本文 §2、§3),和被路由的模型一样按租户、权重、冷却选渠道。因此评估模型**可以是自托管的 laya,也可以部署在境外**:只要对应渠道的 base URL 从网关出站可达即可,策略里只写模型名,不关心它在哪。评估调用不经过 relay 处理器,所以单独计费、单独记日志,也不可能递归触发决策路由。评估按评估模型自己的价格结算(仅输入 token);评估模型没有定价时评估记 0 额度但仍写日志行,不会因此拒绝业务请求。

### 8.3 超时、并发与回落

- 超时:`ROUTING_DECISION_TIMEOUT_MS`,默认 1000 毫秒,取值 1–60000,非法值回落默认。调用方挂断会同时取消评估。
- 并发:进程内同时最多 8 个评估,**不排队**;满了的请求直接走默认路由(原因 `evaluator_unavailable`,审计里 `error_kind=busy`)。
- 回落原则:路由是锦上添花,任何失败都不让业务请求失败。评估超时、无可用评估渠道、评估模型答了没提供的选项、改写 body 失败、评估代码 panic,全部落到默认候选(`default_candidate`,没有则用对外模型名本身)。
- 只处理 `/v1/chat/completions` 与 `/v1/responses` 的**首轮纯文本**请求;多轮(`not_first_turn`)、带 tools、带 `previous_response_id`、会话亲和、推理参数、`metadata` 钉渠道或审计字段、无用户文本、非文本内容一律不评估(`ineligible`)。其他路径静默跳过,既无响应头也无指标。发给评估模型的用户文本上限 8192 字节。
- 改写目标必须是调用方本来就能直接请求的模型:令牌的模型限制、平台允许列表(enforce 模式)、租户自选清单都会对每个候选重新过一遍;过滤后只剩 1 个候选则直接路由、不付评估费(`single_candidate`)。

### 8.4 响应头与原因取值

命中策略的请求会带两个响应头;无策略的请求没有这两个头。

| 头 | 取值 |
|---|---|
| `X-Routed-Model` | 最终实际使用的模型名(未改写时等于请求的模型) |
| `X-Routing-Reason` | `decision:<reason>` |

`<reason>` 的全集(`internal/app/routingdecision/decide.go`,与指标标签一致):

| reason | 含义 |
|---|---|
| `applied` | 评估模型选中某候选且置信度 ≥ `min_confidence`,已改写 |
| `low_confidence` | 选中了但置信度不够,走默认候选 |
| `no_preference` | 评估模型明确回答「都不合适」,走默认候选 |
| `evaluator_unavailable` | 评估失败(超时/忙/无渠道/答非所问/改写失败),走默认候选 |
| `ineligible` | 请求不符合 §8.3 的资格条件,原样转发 |
| `single_candidate` | 过滤后只剩一个候选,直接路由 |

同样三项事实(请求的模型、最终模型、原因)也写进该请求消费日志的 `other.routed`。

### 8.5 审计与指标

- 审计 `routing.decision`(资源 `routing_policy`):每个**实际调用过评估模型**的请求写一条,详情含 `reason`、`requested_model`、`routed_model`、`candidate_id`、`choice`、`confidence`、`min_confidence`、概率分布、`evaluator_model`、评估输入/输出 token、`latency_ms`、`error_kind`(`busy|no_channel|timeout|invalid_choice|failed`)。**不含用户文本**。`ineligible` 与 `single_candidate` 量大且无评估,不审计,只有响应头与指标。
- 指标:`lurus_routing_decision_total{reason}`(命中策略的请求数,六个 reason 预注册为 0,可区分「没路由过」与「没接上」);`lurus_routing_decision_latency_seconds`(评估给请求增加的耗时直方图,仅实际调用评估的请求计入)。无策略的请求不计数。

### 8.6 与 §5 响应校验的衔接

评估走的是同一条 System One 通道,§5 的响应校验同样适用:评估模型返回自相矛盾的答案(概率和不为 1 等)会被判为 `invalid_provider_response`,此时路由层不会把错误抛给调用方,而是按 §8.3 落到默认候选(原因 `evaluator_unavailable`)。这类失败在审计里表现为 `error_kind=failed`;要定位是评估模型本身变坏还是渠道配置问题,先看 `routing.decision` 审计与 `lurus_routing_decision_total{reason="evaluator_unavailable"}` 的增速,再按 §5 的排障顺序直连评估渠道验证。
