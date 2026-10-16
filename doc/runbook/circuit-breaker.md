# Runbook - 渠道熔断器 (circuit breaker) 语义与读法

> **范围**: 熔断器本身怎么工作、什么算失败、怎么看状态、客户端看到 503 + `Retry-After` 该怎么办。
> 告警触发的处置流程见 `channel-breaker-open.md`;兜底路由见 `routing-fallback.md`。
> **代码**: `internal/pkg/resilience/circuitbreaker.go`(状态机),`internal/adapter/handler/relay.go`(`channelBreakers`、重试循环),
> `internal/adapter/handler/relay_breaker_outcome.go`(结果归类),`internal/pkg/types/error.go`(`IsUpstreamFailure`)。

## 1. 状态

每个渠道一个熔断器,按首次使用懒创建,**每个副本各自独立**(反映该 pod 自己观察到的情况)。

| 状态 | 含义 |
|------|------|
| `closed` | 正常放行。连续失败计数,成功即清零;开了失败窗口时,距上次失败超过窗口的旧失败也会被忘掉 |
| `open` | 连续失败达到阈值后跳闸,请求在本进程内被直接跳过,不发给上游 |
| `half_open` | 冷却期结束后只放行**一个**探测请求;探测成功 → `closed`,失败 → 回到 `open` 并重新计时 |

默认参数(环境变量覆盖): `CB_THRESHOLD=5` 连续失败跳闸 · `CB_TIMEOUT_SEC=30` Open 冷却 ·
`CB_HALF_OPEN_TIMEOUT_SEC=60` 探测请求迟迟不回报时,过期后放行新的探测(防止探测丢失导致渠道永久被排除) ·
`CB_FAILURE_WINDOW_SEC`(代码默认 0 = 关;r6-stage/UAT 清单设 300): 一串失败只有彼此间隔都在窗口内才累计,
否则早上 4 次 + 晚上 1 次也会把一个安静的渠道熔断。

半开探测: Open 渠道在冷却结束后的**下一次真实请求**才触发转 `half_open`(状态读取接口不会推进状态机,
否则一次状态查询就会吃掉探测名额)。所以 `state: "open"` 但 `probe_eligible_unix` 已过去,表示"下一个请求就是探测"。
探测请求结束后必须回报成功/失败/inconclusive 三者之一,否则名额不会归还。

## 2. 什么算失败,什么不算

只有"上游的问题"才计入连续失败(`types.IsUpstreamFailure`):

| 计入失败 | 不计入失败(inconclusive,不动计数) |
|----------|-------------------------------------|
| 渠道类错误(`channel:*`,如无可用 key) | 调用方 4xx(400/401/403/404/422/429 等) |
| 上游 5xx(含供应商 503 "Service is too busy") | 客户端取消 / 挂断(`context canceled`)|
| 上游超时 408 / 504 / 524 | 流式响应中客户端先断开(`client_gone`)|
| 状态码无法归类的错误(默认按失败,宁可多冷却) | 网关自己产生的错误(转换失败 500、适配器不支持 501 等,未触达上游) |

inconclusive 在 `closed` / `open` 下是空操作;在 `half_open` 下归还探测名额并回到 `open`、冷却重新计时
(不会让一个不断发 4xx 的客户端把半死的上游砸醒)。

**客户端挂断不是上游故障**: 2026-10-15 事故中 55 次 "context canceled" 来自调用方超时挂断。2026-10-16 之前
非流式请求的挂断会被记成熔断失败: 适配器把错误包成 `do_request_failed` 时隐藏原文的选项把 `context.Canceled`
因果链切断了,`IsUpstreamFailure` 只看到一个匿名 500。现在:
- 隐藏错误文本时保留原因链(`types/error_cause.go`),`IsUpstreamFailure` **先**判 `context.Canceled` 再判渠道类错误;
- 重试循环把请求自身的 context 传给 `reportBreakerOutcomeCtx`: 请求已被取消时,不论错误长什么样都按 inconclusive 处理,
  也不计入 `lurus_channel_*` 错误序列;
- 网关**自己**的上游超时(`context.DeadlineExceeded`)仍算上游失败;客户端自己的超时在服务端只表现为连接关闭,即 Canceled。

**成功**: 非流式正常完成,或流式正常结束,立即 `closed` 并清零计数。

## 3. 怎么看状态

1. 管理端点(仅 root,**只反映被访问的那个副本**):

   ```bash
   curl -sS "https://hub.lurus.cn/api/v2/admin/gateway/health" -H "Authorization: Bearer $HUB_TOKEN"
   ```

   返回 `data.routes[]`(`channel_id`、`channel_name`、`provider`、`state`、`consecutive_fails`、`threshold`、
   `last_fail_unix`、`probe_eligible_unix`)以及 `open` / `half_open` / `tracked` 计数,`replica_scoped: true`。
   **不在列表里 ≠ 健康**: 自启动以来没有流量的渠道不会出现(`lazy_registered: true`)。
   多副本部署时对每个 pod 各查一次,或看下面的指标。
2. 指标(Netdata go.d 抓取):
   `lurus_gateway_circuit_breaker_state{channel_id}`(0 closed / 1 open / 2 half_open)、
   `lurus_gateway_circuit_breaker_trips_total`、`lurus_gateway_circuit_breaker_rejections_total`。
3. 日志: 每次状态切换一行 `circuit breaker channel #N: closed → open`。
4. 某次请求的路由尝试(`route_attempts`)里,被跳过的渠道结果是 `breaker_open`。

## 4. 客户端看到的 503 + `Retry-After`

当一个模型的所有候选渠道都因熔断 Open 被跳过、没有任何上游被调用时,网关返回:

```
HTTP/1.1 503 Service Unavailable
Retry-After: 30
{"error":{"message":"all upstream channels for this model are temporarily unavailable (circuit breakers open)", ...}}
```

含义: 这是**网关主动拒绝**,不是上游刚刚的回答;不会计费、不会再重试(skip-retry)。`Retry-After` 固定 30 s,与默认
`CB_TIMEOUT_SEC` 一致,表示最早大约这么久之后会有一次半开探测。

客户端建议: 遵守 `Retry-After` 做带抖动的指数退避,不要紧循环重试(紧循环只会让半开探测更难成功);
批处理类调用方应把 503 视为"暂停并稍后重试",而不是逐条失败。

另一类 `no available channel for model <m> in group <g>` 不是熔断,而是该分组**没有可选渠道**
(配置问题,2026-10-15 有 21 次;分发器在确认该模型从未配置过时返回 404,否则 503),`Retry-After` 帮不上忙,要补渠道或改分组。

## 5. 熔断之外的减损手段

- 同一供应商的多条渠道共享故障域,熔断器按渠道独立,不能互相兜底;跨供应商兜底见 `routing-fallback.md`
  (主模型渠道全部 Open 时才会触发,依赖 `Registry.AllOpen`)。
- 手动摘除渠道: `PUT /api/v2/{tenant_slug}/channels/{id}`,`status=2`。
- 单个渠道长期 Open: 先看供应商状态页,再按 `channel-breaker-open.md` 和 `upstream-5xx-burst.md` 处理。
