# ADR: Observability Stack

**Status**: Accepted (partial implementation) — **superseded on tracing/dashboarding, see 2026-09-07 update below**
**Date**: 2026-02-03
**Relates to**: ADR-API-008 (architecture.md), Epic 4

> **UPDATE 2026-09-07**: the stack this ADR describes never fully landed and
> has since been overtaken by a platform-wide decision (root `lurus/CLAUDE.md`
> HARD RULE): monitoring is self-hosted Netdata, service-side zero changes.
> Concretely, as of this date:
> - **Jaeger is no longer deployed (the OTLP collector was stopped)** — the
>   architecture diagram below still shows "staging: deployed", but the
>   OTLP→jaeger-collector export path has been stopped, and
>   `OTEL_TRACING_ENABLED` defaults to false and deploy/ never sets it.
>   Distributed tracing is Not Implemented, same as the Current State table
>   already said in 2026-02.
> - **Grafana: no kustomization in this repo applies it.** `deploy/grafana/` held 3 dashboard/
>   alert JSON+YAML files that no kustomization ever applied; they were deleted
>   2026-09-07 rather than continue to describe a Grafana instance that does
>   not exist.
> - Prometheus `/metrics` (the one piece of this ADR that IS real) is scraped
>   directly by the host netdata `go.d` job, not by a Prometheus server this
>   repo deploys — see `internal/pkg/metrics/alert_wiring_honesty_test.go` for
>   the gate that keeps Go source from re-describing either of the above as
>   live.
> The Architecture diagram and References section below are left as originally
> written (historical record of the 2026-02 decision); read them against this
> update, not as current state.

> **UPDATE 2026-09-08 (C08-TTFT-CACHE-SERIES)**: two additive observability
> pieces, both zero-collector-change (Netdata scrapes `/metrics` unchanged):
> - `lurus_gateway_relay_time_to_first_token_seconds{provider,model,product}`
>   (histogram) — wall-clock time from relay start to the first token
>   received from upstream, stamped by whichever streaming adaptor's first
>   `RelayInfo.SetFirstResponseTime()` call fires for that request. The
>   capture point is provider-side and varies by adaptor: the
>   shared stream scanner (most text/chat providers) stamps it before
>   forwarding the first non-terminal SSE event to the caller, the Cloudflare
>   adaptor stamps it after forwarding, and the AWS/Cohere adaptors stamp it
>   at their own first-chunk point — none of them measure the byte reaching
>   the caller's socket. Written once per request from
>   `handler.observeRelayOutcome`'s end-to-end site, only when
>   `RelayInfo.HasSendResponse()` is true (streamed and actually received a
>   first token) and the computed duration is positive; non-streaming and
>   failed-before-first-byte requests correctly contribute nothing, while a
>   request that received a first token and then failed or had the client
>   disconnect still contributes. OpenAI Realtime sessions
>   (`RelayFormatOpenAIRealtime`) are excluded outright — a long-lived
>   bidirectional websocket session is a different traffic shape from a
>   request/response call and does not belong in this histogram.
>   `internal/pkg/metrics/ttft.go`.
> - `GET /api/v2/:tenant_slug/logs/stat` (and `/stat/all`) gained
>   `cache_read_tokens` / `cache_write_tokens` on both the window totals and
>   each `by_product` row — SUMs of the existing `other.cache_tokens` /
>   `other.cache_creation_tokens` JSON fields `log_info_generate.go` already
>   wrote onto text/claude consume rows (Wss/Audio rows carry `cache_tokens`
>   as 0 via the shared base generator but never `cache_creation_tokens`;
>   Midjourney rows carry neither key), extracted the same way
>   `source_product` is (`repo.OtherTextExpr`). Zero for a window with no
>   reported cache hits; upstream-reported except on OpenRouter channels,
>   where `cache_creation_tokens` may be derived from the upstream-reported
>   cost instead (`quota.go` `CalcOpenRouterCacheCreateTokens`).

> **UPDATE 2026-09-09 (L3 operator signals)**: three additive series, still
> zero-collector-change:
> - `lurus_gateway_leader` (gauge) — 1 while this process holds the HA
>   leader lease, 0 otherwise. Single writer: `common.SetLeader`.
> - `lurus_gateway_leader_task_last_success_timestamp_seconds{task}`
>   (gauge) — unix timestamp of the last successful run of a leader-gated
>   periodic task (`lifecycle.LeaderTask`); initialised to 0 for each task
>   name at registration so a task that has never once succeeded still
>   exports a series (rather than none at all) for a `time() - last_success
>   > X` alert to catch. 🔴 A demoted leader keeps exporting its *last*
>   timestamp forever — the series is not cleared or reset on step-down —
>   so any alert on it must be qualified with `lurus_gateway_leader == 1`,
>   or a follower's stale-but-once-real timestamp will mask the condition.
> - `lurus_gateway_instance_info{pod,namespace,version}` (gauge, constant
>   1) — this pod's identity, set once at boot from the k8s downward API
>   (`POD_NAME`/`POD_NAMESPACE`) before `/metrics` is mounted. `/metrics`
>   also gains an `X-Lurus-Instance` response header (same pod value),
>   mounted after `metricsAuthMiddleware` so a rejected scrape never leaks
>   it. `GET /api/health` gains only the coarse `checks.leader`
>   (`held`|`standby`) — it is a public, unauthenticated endpoint, so it
>   deliberately does not carry the pod identity.

## Context

Production Lurus API needs metrics, tracing, and alerting for incident response and performance optimization. The team is 2 people running on a single K3s node, so the stack must be lightweight and optional (feature-flagged).

### Current State

| Capability | Status |
|------------|--------|
| Structured logging (slog, JSON in prod) | Implemented |
| pprof profiling (`ENABLE_PPROF=true`) | Implemented |
| Active connections counter (StatsMiddleware) | Implemented |
| Prometheus metrics (`/metrics`) | Implemented |
| Performance benchmarks (relay latency) | Implemented |
| Distributed tracing | Not implemented |
| Alerting rules | Not implemented |

## Decision

Adopt OpenTelemetry (OTel) SDK as the instrumentation layer, with Prometheus for metrics and Jaeger for tracing. All observability features are gated behind environment variables and disabled by default.

### Architecture

```
Application (OTel SDK)
  |- Metrics -> Prometheus Exporter -> /metrics -> Prometheus -> Grafana
  |- Traces  -> OTLP Exporter -> Jaeger (staging: deployed)
  '- Logs    -> slog + trace_id injection -> stdout -> Loki (future)
```

### Key Metrics Exposed

- `http_request_duration_seconds` (histogram, by method/path/status)
- `http_requests_total` (counter)
- `active_connections` (gauge)
- `relay_request_duration_seconds` (histogram, by model/provider)
- Go runtime metrics (goroutines, GC, memory)

### Feature Flags

| Env Var | Default | Controls |
|---------|---------|----------|
| `ENABLE_METRIC` | `false` | Prometheus `/metrics` endpoint |
| `OTEL_TRACING_ENABLED` | `false` | Distributed tracing export |
| `ENABLE_PPROF` | `false` | Go pprof endpoints |

### Sampling Strategy

- 10% normal requests
- 100% errors and P99 latency outliers
- 0% health check endpoints (`/api/status`)

## Consequences

- (+) Zero overhead when disabled (feature flags)
- (+) Industry-standard stack (Prometheus + Grafana + Jaeger)
- (+) OTel SDK allows swapping backends without code changes
- (-) Distributed tracing adds ~1-2ms per request when enabled
- (-) Prometheus scraping requires ServiceMonitor in K8s

## References

- Prometheus endpoint: `internal/lifecycle/metrics.go`
- Benchmarks: `internal/adapter/handler/benchmark_test.go`
- Staging Jaeger: deployed in `infra` namespace
