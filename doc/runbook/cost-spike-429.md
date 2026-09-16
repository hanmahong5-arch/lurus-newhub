# Runbook — Cost-Spike 429 Burst

> **Source**: netdata alarm `newhub_cost_spike_429`,
> `deploy/r6-host-netdata/health.d/newhub.conf` (see the file's "STATUS"
> header for whether it is installed on R6 today).
> **Triggered by**: `lurus_gateway_requests_total{status=429}` rate > 1/s
> over a 5-minute average.
> **Severity**: warning (netdata `to: sysadmin`).
> **Last review**: 2026-09-16.
>
> **LIVE STATUS (2026-09-15)**: unbound on the R6 host — no netdata chart
> currently matches `status=429` on this series (production has not
> returned a 429 since the go.d job started scraping). The underlying
> series is written for any status code
> (`internal/pkg/metrics/middleware.go`'s generic per-request recorder), so
> this is a data-volume gap, not a code defect.

## Symptom

Multiple users/tokens are simultaneously hitting the 5-minute cost-spike
guard (`internal/adapter/middleware/cost_spike.go`) or the business rate
limiter's request-rate ceiling.

## Detect

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_cost_spike_429
curl -s http://localhost:30850/metrics | grep 'lurus_gateway_requests_total{' | grep 'status="429"'
```
`newhub_rate_limited_total` (`internal/pkg/metrics/ratelimit.go`) and
`CostSpikeBreachTotal` narrow down which limiter is rejecting.

## Reconcile

- A burst confined to one or few tenants/tokens is likely legitimate load or
  a misbehaving client retry loop, not an attack — check the `X-RateLimit-*`
  response headers the limiter sets.
- A broad burst across many tenants at once is unusual and worth a closer
  look (shared client library bug, or the guard's threshold miscalibrated).

## Recover

No newhub-side action needed in the common case — the limiter is working as
designed. If a specific token/tenant needs a temporary limit increase, that
is a per-tenant policy change (see `doc/runbook/tenant-onboarding.md`).

## Verify

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_cost_spike_429
```

## Prevent

Nothing to prevent — 429s from a correctly-functioning limiter are expected
background behavior at low rate; the alarm exists to catch a burst.
