# Runbook — Channel Circuit Breaker Stuck Open

> **Source**: netdata alarm `newhub_channel_breaker_open`,
> `deploy/r6-host-netdata/health.d/newhub.conf` (see the file's "STATUS"
> header for whether it is installed on R6 today).
> **Triggered by**: `lurus_gateway_circuit_breaker_state{channel_id=...}`
> (gauge, 0=closed/1=open/2=half_open) >= 1 for 30 minutes — set via
> `metrics.RecordCircuitBreakerState`, called from
> `internal/adapter/handler/relay.go`.
> **Severity**: warning (netdata `to: sysadmin`).
> **Last review**: 2026-09-16.
>
> **LIVE STATUS (2026-09-15)**: unbound on the R6 host — no netdata chart
> currently matches this series (no channel's breaker has tripped Open since
> the go.d job started scraping). This is a data-volume gap, not a code
> defect.

## Symptom

A relay channel's circuit breaker has been Open for at least 30 minutes —
the channel is being skipped for routing because recent requests through it
failed past the breaker's threshold.

## Detect

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_channel_breaker_open
curl -s http://localhost:30850/metrics | grep lurus_gateway_circuit_breaker_state
```
The alarm's chart label names the `channel_id`.

## Reconcile

- Cross-reference `newhub_upstream_5xx_burst` for the same channel/provider —
  a stuck-open breaker following a 5xx burst is the expected sequence.
- Check the provider's own status page.

## Recover

If the provider has recovered, the breaker should close automatically on the
next successful probe. To manually take the channel out of rotation while
the provider is degraded: `PUT /api/v2/{tenant_slug}/channels/{id}` with
`status=2` (`common.ChannelStatusManuallyDisabled`).

## Verify

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_channel_breaker_open
```

## Prevent

Nothing newhub-side — the breaker tripping is the correct response to a
failing upstream.
