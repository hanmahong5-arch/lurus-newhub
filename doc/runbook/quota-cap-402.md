# Runbook — Quota-Cap 402 Burst

> **Source**: netdata alarm `newhub_quota_cap_402`,
> `deploy/r6-host-netdata/health.d/newhub.conf` (see the file's "STATUS"
> header for whether it is installed on R6 today).
> **Triggered by**: `lurus_gateway_requests_total{status=402}` rate > 5/s
> over a 10-minute average.
> **Severity**: warning/informational (netdata `to: sysadmin`).
> **Last review**: 2026-09-16.
>
> **LIVE STATUS (2026-09-15)**: unbound on the R6 host — no netdata chart
> currently matches `status=402` on this series (production has not
> returned a 402 since the go.d job started scraping), for the same reason
> as `newhub_cost_spike_429`. Data-volume gap, not a code defect.

## Symptom

Many users/tenants are simultaneously at or past their credit pool /
MaxQuota ceiling — the relay is returning HTTP 402. Often benign (e.g. a
billing-cycle boundary where many tenants exhaust quota around the same
time), but worth checking if it accelerates.

## Detect

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_quota_cap_402
curl -s http://localhost:30850/metrics | grep 'lurus_gateway_requests_total{' | grep 'status="402"'
```
Cross-reference `newhub_credit_pool` (`credit-pool-low.md`) for which
tenants are exhausted.

## Reconcile

- If the burst correlates with a specific billing/reset date, it is
  expected — see `doc/runbook/tenant-onboarding.md` for the pool reset
  cadence.
- If it is broad and unexpected, check for a regression in pool crediting
  (`internal/app/credit_pool_reconcile.go`, `credit_pool_reset.go`) or a
  pricing/rate change that unexpectedly increased consumption.

## Recover

No newhub-side automatic action — 402 on an exhausted pool is intentional
quota enforcement, not a bug. Direct affected tenants to top up.

## Verify

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_quota_cap_402
```

## Prevent

Nothing to prevent in the common case; investigate only if the rate keeps
accelerating outside an expected billing-cycle window.
