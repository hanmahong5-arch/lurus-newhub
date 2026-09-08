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
