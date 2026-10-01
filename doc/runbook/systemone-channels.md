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
5. 渠道测试通过后再放量。TypeSafe 限速(文档)100K token/s、40 req/s,且会动态调整;超限返回 429,网关会换渠道或重试。

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
