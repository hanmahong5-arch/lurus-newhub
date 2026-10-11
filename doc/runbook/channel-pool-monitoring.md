# Channel Pool Monitoring Runbook

> 渠道/账号池运营告警(Netdata `newhub_channel_*` / `newhub_content_*` / `newhub_models_unroutable`)的处置手册。
> 指标定义:`internal/pkg/metrics/channel_ops.go`;gauge 刷新:`internal/adapter/handler/channel_metrics.go`(每 30 秒,每个副本各自跑)。
> 公开模型可用性:`GET /api/v2/public/model-status`(无鉴权,30 秒缓存,只给 model + status)。

## 先看哪里

1. 渠道维度的"为什么不可用":`GET /api/v2/:tenant_slug/channels/:id/health`(运维接口,给出逐 key 原因码)。
2. 指标只到 channel_id 级别,**不带 key 下标**。逐号细节只走上面这个接口。
3. 渠道很多、channel_id 基数太高时,设 `METRICS_DISABLED_LABELS=channel_id`(逗号分隔,可禁多个标签),
   被禁标签的值统一写成 `_`;此时 gauge 取各渠道里最坏的一个(state/用量取最大,到期/余额取最小),
   按渠道的告警会失去"哪一个渠道"的信息,只能当总开关用。

## 告警与处置

| 告警 | 含义 | 处置 |
|---|---|---|
| `newhub_channel_error_ratio` | 某渠道 5 分钟错误率超阈值(`lurus_channel_error_ratio_5m`,5 分钟内不足 20 次尝试时恒为 0) | 看 `lurus_channel_errors_total{reason}` 哪类原因在涨:`auth`=key 失效/封号,`quota`=余额或套餐用尽,`rate_limited`=被厂商限流,`upstream_5xx`/`timeout`/`network`=厂商或链路故障。调用 channel health 接口看是否已被冷却;确认是厂商故障则降权或临时停用该渠道 |
| `newhub_channel_all_keys_down` | 渠道状态 = 2(没有任何可路由的 key) | channel health 接口读原因码:`cooling_429` 等冷却到点自愈;`plan_window_exhausted` 等窗口重置;`auth_failed`/`balance_low`/`expired` 需要人工换 key 或充值,处理后用 key restore 接口做探测恢复 |
| `newhub_models_unroutable` | 有模型配置了渠道但当前没有任何可路由渠道(客户请求会 503) | `GET /api/v2/public/model-status` 找出 `down` 的模型,对应渠道逐个按上一条处理;确实无需再提供的模型从渠道模型列表里去掉 |
| `newhub_channel_plan_window_high` | 套餐窗口(5h/weekly)用量超过 90% | 阈值停调(`plan_threshold_pct`)会在默认 95% 时主动停调到窗口重置;此告警是预警:评估是否把流量切到备用渠道,或提前加号 |
| `newhub_channel_plan_expiring` | 套餐 72 小时内到期(`lurus_channel_expires_in_seconds`) | 续费或换新号,更新渠道 `expires_at`;到期后 planquota 会自动暂停该渠道 |
| `newhub_content_rejected_surge` | 内容规则拒绝请求突增(`lurus_content_rejected_total`) | 审计日志按 `content_rule.hit` 看是哪条规则;误伤则把规则改 observe 模式或收紧正则;确为滥用则查对应令牌/租户 |

## 其它相关指标(无告警,供排查)

- `lurus_channel_requests_total{channel_id,status_class}`:每次上游尝试一条,含重试。
- `lurus_channel_cooldown_total{channel_id,cause}`:`rate_limit`/`plan_window`/`balance_low`。
- `lurus_channel_balance{channel_id}`:上游余额探测结果(美元),仅有余额来源的渠道才有。
- `lurus_channel_key_affinity_total{outcome}`:key 级会话粘性 hit/miss/rebind;命中率 = hit/(hit+miss+rebind)。
- `lurus_content_rule_hits_total{rule_scope,kind,mode}`:规则命中次数,不含任何匹配内容。
