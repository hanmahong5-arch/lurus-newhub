# Runbook — Billing Outbox Backlog

> **Source**: netdata alarm `newhub_billing_outbox_backlog`,
> `deploy/r6-host-netdata/health.d/newhub.conf` (see the file's "STATUS"
> header for whether it is installed on R6 today).
> **Triggered by**: `lurus_billing_outbox_pending` (gauge) > 100 over a
> 5-minute max — `internal/app/billing_outbox.go:146`, set from the pending
> count of the settlement-retry queue.
> **Severity**: warning (netdata `to: sysadmin`).
> **Last review**: 2026-09-16.

## Symptom

The settlement-retry queue is growing — either platform's billing endpoints
are slow/unreachable (check `newhub_platform_breaker_open`), or the
newhub-side dequeue worker has stalled.

## Detect

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_billing_outbox_backlog
curl -s http://localhost:30850/metrics | grep lurus_billing_outbox_pending
```

## Reconcile

- Check `newhub_platform_breaker_open` and `newhub_billing_outbox_failures`
  for the same window — a backlog driven by platform unavailability should
  correlate with both.
- If neither correlates, check the newhub process's own logs for the outbox
  worker (`internal/app/billing_outbox.go`) for a stalled goroutine or panic
  (would also show in `PanicsRecovered`).

## Recover

Once platform billing recovers (or the stalled worker is restarted, e.g. via
a pod restart), the backlog drains as the retry loop catches up. No manual
queue manipulation is expected in the common case.

## Verify

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_billing_outbox_backlog
```

## Prevent

Nothing newhub-side beyond keeping the dequeue worker healthy — sustained
backlog growth is a downstream signal of platform billing availability.
