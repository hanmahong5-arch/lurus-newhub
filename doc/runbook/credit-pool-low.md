# Runbook — Tenant Credit Pool Low / Exhausted

> **Source**: netdata alarm `newhub_credit_pool`,
> `deploy/r6-host-netdata/health.d/newhub.conf` (see the file's "STATUS"
> header for whether it is installed on R6 today).
> **Triggered by**: `lurus_gateway_credit_pool_balance{tenant_id=...}`
> (gauge) — warn when `0 < balance < 1000`, crit when `balance <= 0`. Set
> from several call sites in `internal/app/quota.go`,
> `internal/adapter/handler/tenant_credit_pool.go`,
> `internal/adapter/middleware/pool_balance_check.go` and
> `internal/app/credit_pool_reconcile.go`/`credit_pool_reset.go`.
> **Severity**: warning / critical (netdata `to: sysadmin`).
> **Last review**: 2026-09-16.
>
> **LIVE STATUS (2026-09-15)**: unbound on the R6 host — no netdata chart
> currently matches this series (no tenant pool balance has been `Set()`
> since the go.d job started scraping; see `newhub.conf`'s comment above
> this template for detail). This is a data-volume gap, not a code defect.

## Symptom

A tenant's credit pool is low (<1000 quota units) or exhausted (<=0, at
which point the relay starts returning HTTP 402 for that tenant's tokens).

## Detect

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_credit_pool
curl -s http://localhost:30850/metrics | grep lurus_gateway_credit_pool_balance
```
The alarm value/chart label identifies the `tenant_id` directly.

## Reconcile

- Confirm via the admin console or `/internal` API whether the tenant's pool
  is genuinely near zero (expected, needs a topup) vs. a reconciliation gap
  (see `doc/runbook/wallet-revert-stranded.md` for the stranded-debit case).

## Recover

Direct the Reseller/tenant to top up the pool. No newhub-side automatic
recovery — quota gating on an exhausted pool is intentional.

## Verify

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_credit_pool
```
returns to CLEAR once the balance rises back above the warn threshold.

## Prevent

Nothing newhub-side — pool exhaustion is expected tenant behavior, not a
defect.
