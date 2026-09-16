# Runbook — Platform Billing Breaker Open

> **Source**: netdata alarm `newhub_platform_breaker_open`,
> `deploy/r6-host-netdata/health.d/newhub.conf` (see the file's "STATUS"
> header for whether it is installed on R6 today; ownership and the "does
> this page anyone" limit are in `deploy/r6-host-netdata/README.md`).
> **Triggered by**: `lurus_billing_circuit_breaker_state` (gauge) >= 1 for 5
> minutes — `internal/pkg/common/billing_breaker.go` sets it to 2
> (open/tripped) or 1 (half-open) when calls to lurus-platform's billing
> gRPC endpoints fail past the breaker's threshold, 0 when closed.
> **Severity**: critical (netdata `to: sysadmin`).
> **Last review**: 2026-09-16.

## Symptom

Settlement to lurus-platform (PreAuthorize/Debit/ReportUsage) is halted:
relay quota is being consumed but the corresponding wallet debit is not
reaching platform. This is a revenue-leak condition, not merely a
degraded-service one.

## Detect

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_platform_breaker_open
curl -s http://localhost:30850/metrics | grep lurus_billing_circuit_breaker_state
```

## Reconcile

- Check lurus-platform's own health/logs for the failing billing gRPC calls.
- Check `lurus_billing_outbox_pending` / `lurus_billing_outbox_failed_total`
  (see `billing-outbox-backlog.md` / `billing-outbox-failures.md`) — a
  tripped breaker should be driving both up.

## Recover

The breaker self-closes once platform billing calls succeed again
(`internal/pkg/common/billing_breaker.go`); there is no manual reset. Once
closed, drain the outbox backlog that accumulated while it was open.

## Verify

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_platform_breaker_open
```
returns to CLEAR once the gauge drops below 1 for the alarm's `delay` window.

## Prevent

Nothing newhub-side — this is downstream of platform's billing endpoint
availability.
