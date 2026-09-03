# Relay SLO — newhub gateway

Owner: newhub session
Status: W1 baseline (2026-05-09); revisit after 7 days of stage data.

## What we promise

| SLI | Window | Target | Source metric |
|---|---|---|---|
| Newhub overhead P99 | rolling 7d | **< 50ms** | `lurus_gateway_relay_overhead_duration_seconds` |
| Newhub overhead P50 | rolling 7d | < 5ms | same |
| End-to-end success rate | rolling 7d | **> 99.5%** | `lurus_gateway_relay_total_duration_seconds_count{status="success"}` / total |
| Channel select P99 | rolling 7d | < 10ms | `lurus_gateway_channel_select_duration_seconds` |

**Overhead** = time from request entry to first upstream call (validate + token-count + pricing + quota check + channel select + body io). The latency we own — separable from upstream wall time.

**Total** = end-to-end `Relay()` including all retries + upstream wall time. The number that matters to the customer.

## Why these targets

- **50ms overhead P99**: most enterprise B2B integrations budget 100-200ms of platform overhead on top of actual work. We target half that to leave headroom for ingress + their client-side processing.
- **99.5% success rate**: ~1 failure per 200 requests over 7 days. Aligns with single-provider uptime reality; retries cover us. Tighten to 99.9% when circuit-breaker + multi-provider failover are battle-tested.
- **TTFB / first-chunk streaming latency.** ⚠️ The line that used to sit here —
  "NOT yet measured; instrumenting requires touching every provider adapter
  (~20 files)" — was already false when it was written. Time to first token is
  recorded on every request as `other.frt` on the consume log, written from
  three places, and it has been user-visible in the console since 2026-09-01.
  What is missing is a *metric*: there is no histogram, so there is no
  percentile and therefore still no SLI.

  It is also not a ~20-file job. Every provider funnels through
  `RelayInfo.SetFirstResponseTime()`, so one histogram observation in that
  setter covers all of them — but only since 2026-09-03, when cloudflare and
  cohere stopped assigning `FirstResponseTime` directly and bypassing it.
  `TestFirstResponseTimeHasASingleWriter`
  (`internal/adapter/provider/common/`) keeps that true; without it, any
  instrumentation on the setter would silently omit those two providers and
  produce a percentile that looks complete and is not.

  When this is promoted to an SLI it must be documented as a **streaming-only**
  one. A non-streaming request emits no first token, so no `frt` is written at
  all — the population is streaming requests, and quoting it as if it covered
  all traffic would misstate the denominator.

  Until the histogram exists, `relay_duration_seconds` remains the proxy.

## PromQL queries

### Overhead distribution (sliding 5m)

```promql
histogram_quantile(0.50, sum(rate(lurus_gateway_relay_overhead_duration_seconds_bucket[5m])) by (le))
histogram_quantile(0.95, sum(rate(lurus_gateway_relay_overhead_duration_seconds_bucket[5m])) by (le))
histogram_quantile(0.99, sum(rate(lurus_gateway_relay_overhead_duration_seconds_bucket[5m])) by (le))
```

### Total latency P99 per provider

```promql
histogram_quantile(0.99,
  sum(rate(lurus_gateway_relay_total_duration_seconds_bucket[5m])) by (provider, le)
)
```

### Success rate per provider/model

```promql
sum(rate(lurus_gateway_relay_total_duration_seconds_count{status="success"}[5m])) by (provider, model)
/
sum(rate(lurus_gateway_relay_total_duration_seconds_count[5m])) by (provider, model)
```

### Top 5 slowest model routes

```promql
topk(5,
  histogram_quantile(0.99,
    sum(rate(lurus_gateway_relay_total_duration_seconds_bucket[5m])) by (provider, model, le)
  )
)
```

## Drill-down playbook

When **overhead P99** crosses 50ms:

1. Check `lurus_gateway_channel_select_duration_seconds` — most likely culprit (DB lookup + filter under contention).
2. If channel_select is fine, the remaining overhead is in validate / token-count / quota / body-io. **No per-phase metric exists yet** — add `relay_pipeline_phase_duration_seconds{phase=...}` (W1.5) and re-deploy.
3. Worst case: profile via `ENABLE_PPROF=true`, scrape `:8005/debug/pprof/profile?seconds=30`.

When **total P99** spikes but **overhead** is flat: upstream is slow. Cross-reference `relay_duration_seconds{provider}` to identify the bad channel; circuit breaker should auto-trip after `CB_THRESHOLD=5` consecutive failures.

When **success rate** drops below 99.5%: check `circuit_breaker_state` (open breakers) and `channel_consecutive_errors` for the offending channel. Verify retries are firing via `retry_attempts_total`.

## Verification — first 7 days

- [ ] STAGE pod scrape `:3000/metrics` returns `relay_overhead_duration_seconds` and `relay_total_duration_seconds`
- [ ] Grafana dashboard renders P50/P95/P99 panels for both
- [ ] Overhead distribution lands within `[1ms, 20ms]` for steady-state load
- [ ] Total distribution matches expected provider mix (Anthropic ~3-8s P99, OpenAI ~2-5s P99)
- [ ] Alert wired: page if overhead P99 > 100ms (2× SLO target) for 5 minutes

## Out of scope for W1

- Per-phase pipeline breakdown (validate/token/quota separately) — add when overhead spikes warrant it.
- Upstream TTFB **histogram** (the value itself is already recorded as `other.frt`;
  the single seam is `RelayInfo.SetFirstResponseTime`, kept single by
  `TestFirstResponseTimeHasASingleWriter`). Must be labelled a streaming-only SLI.
- Streaming first-chunk latency for SSE — needs response-writer middleware timing.
- Cache hit/miss metrics — N/A until W4 caching layer ships.
- SLO budget burn-rate alerts — set up when 7d baseline data exists.
