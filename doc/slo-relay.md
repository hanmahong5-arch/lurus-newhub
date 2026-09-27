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

> **[2026-09-07] Numerator change — `status="success"` no longer includes abandoned streams.**
> The success-rate SLI above reads `relay_total_duration_seconds_count{status="success"}` /
> total. Before `observeRelayOutcome` (`internal/adapter/handler/relay_outcome.go`), a stream
> the caller disconnected from mid-flight had `apiErr == nil` (there is nobody left to write an
> error frame to) and so was counted as an ordinary `"success"`. That request now gets its own
> status label, `"client_gone"`, and is excluded from the `"success"` numerator — so a rise in
> client-side disconnects (client timeouts, users cancelling generations) now shows up as a drop
> in the SLI even though nothing on our side changed. To compute the old (pre-2026-09-07)
> series for a continuous before/after comparison, sum both labels:
> `(relay_total_duration_seconds_count{status="success"} + relay_total_duration_seconds_count{status="client_gone"})`
> / total.
>
> Unchanged and still counted as `"success"`: a stream the upstream truncated while the caller
> was still listening. That request gets `relay_errors_total{error_type="upstream_5xx"|"upstream_timeout"}`
> +1 and a 502/504 frame on the wire, but the relay helper then returns without an error, so on
> `relay_total_duration_seconds` / `relay_requests_total` it is a `"success"`. The SLI numerator
> therefore still overstates success by that share; use `relay_errors_total` to see it.

## Alerting (netdata, 2026-09-27)

Until cycle 18 nothing evaluated any row of the table above: the host runs
Netdata, not Prometheus, and `deploy/r6-host-netdata/health.d/newhub.conf` had
31 alarms with none on `relay_overhead_duration_seconds`,
`relay_total_duration_seconds` or `channel_select_duration_seconds`. The
"RELAY SLO" section of that file now carries six blocks (four alarms, two
threshold-less helpers) — **in-repo only; not installed on R6 until the
operator's next `scripts/install-netdata-alarms.sh` run**, and the two
histogram pairs carry a live check to run first (that section's ⚠VERIFY note).

None is the SLI verbatim. Netdata's `lookup` reads one chart, and under go.d's
chart model a label value or a histogram's `_sum`/`_count` are different charts,
so the ratios in the table cannot be divided there. What each alarm evaluates
instead:

| SLI | Alarm | Evaluates | Gap to the SLI |
|---|---|---|---|
| Success rate > 99.5% | `newhub_relay_error_burst` (crit) | `relay_requests_total{status="error"}` per route, 10m average > 0.05/s (30 failures) | Absolute floor: equals 0.5% at 10 req/s on that route, stricter below it |
| Success rate > 99.5% | `newhub_relay_error_budget_slow_burn` (warn) | same series, 1h average > 0.01/s (36 failures) | Absolute floor: equals 0.5% at 2 req/s; the slow-burn leg the 10m window misses |
| Overhead P99 < 50ms | `newhub_relay_overhead_slow_share` (warn) | `100 × (1 − rate(le=0.05) / rate(le=+Inf))` over 5m > 1% | The SLO's exact contrapositive, not a substitute — but 5m, not 7d, and it rests on go.d's bucket-chart layout |
| Channel select P99 < 10ms | `newhub_channel_select_slow_share` (warn) | `100 × (1 − rate(le=0.01) / rate(le=+Inf))` over 5m > 1% | same |

Why the latency alarms are a bucket share and not a mean of `_sum/_count`: a
mean of 20ms is compatible with 5% of requests at 300ms, which is precisely the
P99 breach the SLO exists to catch, and `_sum` and `_count` are separate charts
anyway. P99 < 50ms holds if and only if fewer than 1% of requests exceed 50ms,
and 50ms / 10ms are declared bucket bounds of the two histograms, so the share
is computable inside one chart — with a helper template
(`newhub_relay_overhead_req_rate` / `newhub_channel_select_req_rate`) supplying
the denominator over the same 5-minute window, the stock Netdata idiom.

The `status="client_gone"` note above applies unchanged: those requests are
neither `success` nor `error`, so they are outside both error-rate alarms.

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

When **success rate** drops below 99.5%: check `circuit_breaker_state` (open breakers) and `relay_errors_total{error_type,provider,model,product}` (breaks down WHY a provider is failing — upstream_5xx / upstream_timeout / upstream_rate_limit / insufficient_quota / etc., see `types.RelayErrorType`) for the offending channel. Verify retries are firing via `retry_attempts_total`.

## Verification — first 7 days

- [ ] STAGE pod scrape `:3000/metrics` returns `relay_overhead_duration_seconds` and `relay_total_duration_seconds`
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
