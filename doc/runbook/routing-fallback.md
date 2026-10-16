# Runbook - 跨供应商兜底路由 (routing policy, strategy `fallback`)

> **适用**: 某个模型的主渠道整体不可用(没有渠道,或全部熔断 Open)时,自动把请求改写到备用模型。
> **性质**: 租户 opt-in,永久默认关闭(ADR `doc/decisions/2026-05-09-cost-aware-routing.md`)。
> **相关**: `doc/runbook/circuit-breaker.md`(熔断语义)· `doc/decisions/2026-10-10-decision-model-routing.md`(同一张表、同一套资格规则)。
> **代码**: `internal/app/routingfallback/`、`internal/adapter/middleware/fallback_routing.go`、`internal/adapter/handler/breaker_view.go`、`internal/adapter/handler/v2_routing_policy.go`。

## 1. 什么时候该用

只有**主、备是不同供应商**时才有意义。同一供应商的两条渠道共享一个故障域:供应商过载时两条一起失败、
一起熔断,切换到"同一供应商的另一条渠道"等于没切。

### 实例: 2026-10-15 01:00-02:00 UTC(模型名已中性化)

- `model-a` 在渠道 6(provider-a 原生类型)和渠道 1(通用兼容类型,base 指向 provider-a 同一个 API)上各有一条。
  两条都是 provider-a,**一个故障域**。
- 该小时:2 次成功,28 次上游 503 "Service is too busy"(供应商过载),55 次 "context canceled",
  175 次 503 "all upstream channels for this model are temporarily unavailable (circuit breakers open)"
  (`Retry-After: 30`)。调用方只有一个内部批处理 token。
- 另有 21 次 "no available channel for model <m> in group internal":该模型在该分组根本没有渠道。
- 事后看:两条渠道都 Open 的那一段,175 次拒绝本可以由 provider-b 上的 `model-b` 兜住;21 次"没有渠道"同理
  (reason `no_channel`)。**兜底不能修复**前 28 次真实的上游 503(熔断要连续 `CB_THRESHOLD` 次失败才 Open),
  也不能修复 55 次 context canceled(那是客户端挂断,见熔断 runbook)。

选备用模型的原则: 不同供应商、能力相当、价格可接受(按**实际服务的模型**计费,见 §6)、对该租户可路由。

## 2. 契约要点 (FALLBACK 车道)

- 表 `routing_policies`(migration 054),每个 `(tenant, public_model)` **只有一行**,所以同一个模型
  要么是 `decision` 策略,要么是 `fallback` 策略,不能叠加。
- `strategy` 取值: `decision`(默认,既有)或 `fallback`;其他值 PUT 返回 400。无 migration(列是 `varchar(16)`)。
- `fallback` 的 `candidates` 是**有序**列表 `[{id, model, criteria}]`,1..8 项;`criteria` 可空;
  `model` 必须是该租户当前可路由的公开模型,且不能等于 `public_model`。
- `evaluator_model` / `instructions` / `min_confidence` / `default_candidate` 对 fallback 被忽略
  (接受并原样存储,校验不要求它们)。
- 策略出生即 `enabled=false`;不 PUT `enabled=true` 就什么都不会发生。

## 3. 运行时行为

位置: `Distribute()` 选渠道之前,与 decision routing 同一处。

0. **只覆盖两个端点**: `/v1/chat/completions` 和 `/v1/responses`(与 decision routing 共用 `routingdecision.SupportedPath`)。
   其他端点(embeddings、rerank、原生格式 messages 等)**静默跳过**: 不改写、无响应头、无指标,连策略都不查。
   给这些端点配了 fallback 策略也不会生效。
1. 租户没有该模型的**启用**的 fallback 策略 → 什么都不做: 无额外 DB 查询(走既有策略索引缓存,15 s TTL),无响应头,无指标。
   同一模型每租户只有一行策略,所以只有在该模型没有启用的 decision 策略时,fallback 才会被咨询。
2. 资格检查(复用 decision routing 的 `routingdecision.Check`)。下列任一成立即 `ineligible`,不改写,但响应仍带头和指标:
   - 请求体读不出来(`unreadable_body`);
   - 非首轮,即有 assistant/tool 历史(`not_first_turn`);
   - 带 `tools` / `tool_choice` / `functions` / `function_call`(`tools`);
   - `previous_response_id`,或会话亲和键 `X-Session-Id` / `prompt_cache_key` / `metadata.user_id`;
   - 推理模型,或请求带 `reasoning_effort` / `reasoning`(`reasoning`);
   - `metadata` 含 `pin:` 或 `audit:true`;
   - 内容含非文本部分,如图片/多模态(`non_text_content`);
   - 没有用户文本(`no_user_text`)。
   具体命中哪条守卫只写进内部日志(`routing_ineligible_why`),不返回给调用方。
3. 查主模型在该请求分组下的候选渠道(渠道缓存,不查库):
   - 没有渠道 → `no_channel`;全部渠道的熔断都是 Open → `primary_unavailable`;
   - 否则 `primary_ok`,不改写。
4. `no_channel` / `primary_unavailable` 时,取**第一个**自身渠道不是"全部 Open/不存在"的候选,把请求 `model` 改写成它。
   候选同样要过该调用方的模型权限检查(token 模型限制 / 租户白名单,`modelPermitted`,与 decision routing 相同),
   不被允许的候选被跳过。没有可用候选 → `no_candidate`,请求原样往下走,照常得到 503。
   请求体改写失败时同样降级为 `no_candidate`(请求按原样发出,头里也如实写 `no_candidate`)。
5. 熔断状态在 `handler` 包,中间件不能 import 它: `handler/breaker_view.go` 在 init 时把
   `channelBreakers.AllOpen` 注册进 `routingfallback.BreakerAllOpen`。`AllOpen` 只读,不创建熔断器、不消耗半开探测名额;
   Open 且仍在冷却期,或 HalfOpen 且探测仍在进行,都算"不可用"。

## 4. 配置

### 控制台

租户管理员: 控制台「组织策略 → 模型路由」页,为某个模型选择路由策略「故障转移」,按优先级排列备用模型
(可上移/下移),保存后再打开「启用」(会二次确认)。

### API

路径里 `~` 是"我自己的租户"保留别名。需要租户管理员权限;`$HUB_TOKEN` 是你的管理员访问凭证,不要把真值写进脚本或工单。

```bash
# 创建并启用: model-a 不可用时依次尝试 model-b、model-c
curl -sS -X PUT "https://hub.lurus.cn/api/v2/~/routing-policies/model-a" \
  -H "Authorization: Bearer $HUB_TOKEN" -H "Content-Type: application/json" \
  -d '{
    "strategy": "fallback",
    "enabled": true,
    "candidates": [
      {"id": "b", "model": "model-b", "criteria": "provider-b, comparable quality"},
      {"id": "c", "model": "model-c", "criteria": ""}
    ]
  }'

curl -sS "https://hub.lurus.cn/api/v2/~/routing-policies/model-a" -H "Authorization: Bearer $HUB_TOKEN"   # 读取
curl -sS -X DELETE "https://hub.lurus.cn/api/v2/~/routing-policies/model-a" -H "Authorization: Bearer $HUB_TOKEN"  # 回退/关闭
```

PUT 是整份覆盖。校验全有或全无;400 的常见原因: 未知 `strategy`、`candidates` 为空或超过 8 项、
候选模型不可路由、候选等于 `public_model`。多副本之间策略索引最多 15 s 收敛。
不带 `strategy` 字段的 PUT 按 `decision` 处理(与旧客户端兼容)。

## 5. 读响应头

只要有 fallback 策略被"咨询"(**`/v1/chat/completions` 或 `/v1/responses`** 上命中启用策略的请求,包括 `ineligible`),响应就带。
其他端点不带这两个头(见 §3 第 0 条):

| 头 | 含义 |
|----|------|
| `X-Routed-Model` | 实际服务的模型(没改写时等于请求的模型) |
| `X-Routing-Reason` | `fallback:<reason>` |

`reason` 取值: `primary_ok`(主模型健康,未改写)· `no_channel`(主模型在该分组无渠道,**已改写**)·
`primary_unavailable`(主模型渠道全部熔断 Open,**已改写**)· `ineligible`(请求不符合资格,未改写)·
`no_candidate`(需要兜底但没有可用候选或改写失败,未改写,接着得到正常 503)。

`no_channel` / `primary_unavailable` 只在确实改写后才出现;改写没成就是 `no_candidate`。
保险起见仍可用 `X-Routed-Model` 是否不同于请求模型来判断。
没有策略的租户看到的响应与以前逐字节一致。decision 策略的头是 `decision:<reason>`,二者前缀不同,可区分。

## 6. 指标、审计、计费

- 指标 `lurus_routing_fallback_total{reason}`,`reason` 为上面五个封闭枚举,低基数。
  告警思路: `primary_unavailable` 或 `no_channel` 持续非零 = 主供应商在出问题,去看
  `doc/runbook/circuit-breaker.md` 和 `upstream-5xx-burst.md`。
- 审计/日志沿用 decision routing 的风格(请求前后模型、reason);**从不记录用户文本**。
- **计费按实际服务的模型**: 改写发生在计价之前,账单、日志、额度扣减用的都是 `X-Routed-Model` 的价格,
  不是请求模型的价格。备用模型更贵时,账单会变高,选备用模型时先看价格。
  同样,重试不跨租户、`route_attempts` 有上限、流式 failover 闸门在重试之前 - 这些既有保证不变。

## 7. 排障

| 现象 | 查什么 |
|------|--------|
| 配了策略却没兜底 | 策略是否 `enabled=true`;响应头 reason 是否 `ineligible`(带 tools?多轮?多模态?)或 `primary_ok`(熔断其实没 Open) |
| 策略已启用,但响应里完全没有 `X-Routing-*` 头 | 调用的端点不是 `/v1/chat/completions` 或 `/v1/responses`(embeddings、rerank、原生格式等被静默跳过),fallback 对它们永远不生效;也可能该模型存在 decision 策略(头前缀是 `decision:`) |
| 一直 `no_candidate` | 候选模型在该分组有没有渠道、渠道是否也 Open(`GET /api/v2/admin/gateway/health`) |
| 备用模型也失败 | 备用与主模型是不是同一供应商(共享故障域) |
| 想临时停用 | PUT `enabled=false` 或 DELETE,15 s 内各副本收敛 |
