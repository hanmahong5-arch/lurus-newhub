# Runbook — Platform Billing Breaker Open

> **Source**: netdata alarm `newhub_platform_breaker_open`,
> `deploy/r6-host-netdata/health.d/newhub.conf` (see the file's "STATUS"
> header for whether it is installed on R6 today; ownership and the "does
> this page anyone" limit are in `deploy/r6-host-netdata/README.md`).
> **Triggered by**: `lurus_billing_circuit_breaker_state` (gauge) >= 1 for 5
> minutes. The mapping, read off `internal/pkg/common/billing_breaker.go`:
> **0 = closed** (normal, `BillingBreakerSuccess`), **1 = open/tripped**
> (`BillingBreakerFailure` past the threshold, and again when a half-open probe
> fails), **2 = half-open** (`BillingBreakerAllow` letting one probe through
> after the timeout). This header said 2=open / 1=half-open until 2026-09-19;
> that was backwards, and the alarm's `>= 1` covers both states either way.
> **Severity**: critical (netdata `to: sysadmin`).
> **Last review**: 2026-09-19.

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

## Who reads this breaker

Written only by the money legs (`PreAuthorizeWithBreaker`, `SettleWithBreaker`,
`ReleaseWithBreaker`). Since cycle 12 the account and entitlement lookups
*read* it — while it is open they skip the gRPC leg and go straight to the HTTP
twin — but they never write it, so an identity-side blip cannot trip the money
path, and a deployment with `BILLING_UNIFIED_ENABLED` off (no money leg, so no
probe) cannot end up latched open. See
`doc/runbook/platform-dependency-degraded.md`.
