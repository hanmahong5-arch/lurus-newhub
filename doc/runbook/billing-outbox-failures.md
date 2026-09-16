# Runbook — Billing Outbox Permanent Failures

> **Source**: netdata alarm `newhub_billing_outbox_failures`,
> `deploy/r6-host-netdata/health.d/newhub.conf` (see the file's "STATUS"
> header for whether it is installed on R6 today).
> **Triggered by**: `lurus_billing_outbox_failed_total` (counter) > 0 over a
> 1h average — `internal/app/billing_outbox.go:180`, incremented when a
> queued settlement entry exhausts its retry budget.
> **Severity**: critical (netdata `to: sysadmin`).
> **Last review**: 2026-09-16.

## Symptom

A pre-authorized relay charge could not be settled to lurus-platform after
exhausting retries. This needs manual reconciliation — the money movement is
now in an unknown state between newhub and platform.

## Detect

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_billing_outbox_failures
curl -s http://localhost:30850/metrics | grep lurus_billing_outbox_failed_total
```
Cross-reference the newhub billing outbox table (`internal/app/billing_outbox.go`)
for the specific failed entry/entries and their last error.

## Reconcile

- Read the failed entry's stored error to classify: platform-side rejection
  (bad request shape, unknown tenant) vs. transport failure (platform
  unreachable — should also show as `newhub_platform_breaker_open`).
- For a transport failure once platform recovers, the entry may already be
  past its retry budget — reconciliation is manual (see `doc/runbook/database.md`
  for query patterns).

## Recover

No automatic recovery ships with this alarm — it is observability for a
condition that already needs a human. Reconciling the specific entry (either
retrying manually against platform or writing it off) is an owner action.

## Verify

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_billing_outbox_failures
```

## Prevent

Investigate the underlying platform-side rejection reason so future entries
of the same shape do not repeat the failure.
