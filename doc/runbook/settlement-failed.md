# Runbook — Settlement Failed

> **Source**: netdata alarm `newhub_settlement_failed`,
> `deploy/r6-host-netdata/health.d/newhub.conf` — see the conf file's own
> "STATUS"/"STATUS UPDATE 2026-09-19" header for whether it is installed on
> R6 today (as of 2026-09-19, added in-repo only, NOT installed — see the
> README "Install" section for the command and O2 in the cycle-11 plan).
> **Triggered by**: `lurus_billing_settlement_failed_total{path}` (counter)
> averaged over 5 minutes, any nonzero rate — `internal/app/settlement_outcome.go`,
> incremented by `app.SettleConsume` when the consume-quota settlement call
> for a relay request returns an error. `path` is one of `text` (relay's
> OpenAI-compatible site, `relay/compatible_handler.go`), `claude`
> (`app.PostClaudeConsumeQuota`), or `audio` (`app.PostAudioConsumeQuota`).
> **Severity**: warning (netdata `to: sysadmin` — see the README's "Ownership
> boundary" for what that does and does not mean today).
> **Last review**: 2026-09-19.

## Symptom

A consume-quota settlement call failed for at least one relay request in the
last 5 minutes, on one of the three sites that route through
`app.SettleConsume` (see "Not covered this cycle" below for the sites that
do not). The consume log row for that request was still written at the same
quota it would have carried on success — `RecordConsumeLog` and the debit
path itself are both untouched by this cycle's change — but the row now
also carries `other.settlement="failed"` (rendered as a "settlement failed"
badge on the v2 Log page, `web/src/pages/v2/Log/index.jsx`), because the
settlement call behind that number may not have actually landed.

**The badge is not a refund record.** `PostConsumeQuota`'s Phase A tenant
pool debit (`internal/app/quota.go:1000-1002`, `debitTenantPool`) runs
*before* the Phase 3 token-quota update that can fail
(`internal/app/quota.go:1016-1046`) — so on a Phase-3 failure the tenant
credit pool has already been drawn down even though the row is flagged
`settlement="failed"` and only the user-quota leg was compensated. A flagged
row can correspond to a pool that really was debited; see "Reconcile" below
before assuming otherwise.

## How this differs from the breaker/outbox alarms

`newhub_platform_breaker_open` and `newhub_billing_outbox_failures`
(`doc/runbook/platform-billing-breaker-open.md`,
`doc/runbook/billing-outbox-failures.md`) watch the platform pre-auth
settlement path specifically — a circuit breaker state and a permanently-
failed outbox retry queue. `newhub_settlement_failed` watches the three
consume-quota sites that go through `app.SettleConsume` this cycle
(`relay.postConsumeQuota` in `relay/compatible_handler.go`,
`app.PostClaudeConsumeQuota`, `app.PostAudioConsumeQuota`), regardless of
which internal `PostConsumeQuota` branch the settlement call takes (legacy
debit, pre-auth settle, or local-only) — see "Not covered this cycle" for
the relay paths this does not watch. A pre-auth entry that lands in the
outbox is a KNOWN, retried failure with its own alarm;
`newhub_settlement_failed` is what fires on the failures that
`PostConsumeQuota`'s own error return surfaces on the three covered sites —
including ones that never reach the outbox at all (e.g. a local
quota-write error). The three counters can co-occur for the same underlying
incident; they are not redundant with each other.

## Not covered this cycle

`app.PostConsumeQuota` has other callers, among them the three below, that are not routed through
`app.SettleConsume`, and a settlement failure on any of them still writes a
full-price consume log row with **no** `other.settlement` flag and **no**
counter increment — only the `common.SysLog` line each site already wrote:

- `internal/app/relay/mjproxy_handler.go:223` and `:526` — Midjourney-proxy
  task callbacks.
- `internal/app/relay/relay_task.go:208` — async task (e.g. Suno) settlement
  on fetch.
- `internal/app/quota.go:231` `PostWssConsumeQuota` — the realtime/websocket
  settlement path never calls `PostConsumeQuota` at all, so it cannot reach
  `app.SettleConsume` by construction.

These are this cycle's documented non-goals, not an oversight discovered
after the fact — extending the seam to them is next-cycle scope.

## Detect

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_settlement_failed
curl -s http://localhost:30850/metrics | grep 'lurus_billing_settlement_failed_total{'
```

Find the flagged rows (PostgreSQL — the runtime DB; `other` is stored as
JSON text, see `internal/adapter/repo/savings.go`'s `jsonOtherTextExpr` for
the same cast used by the app's own queries):

```sql
SELECT id, user_id, token_id, model_name, quota, created_at
FROM logs
WHERE NULLIF(other, '')::jsonb ->> 'settlement' = 'failed'
ORDER BY created_at DESC
LIMIT 50;
```

## Reconcile

Whether a flagged row needs a manual wallet/pool correction, and what that
correction looks like, is an **owner decision** — it depends on which
`PostConsumeQuota` branch failed (legacy fire-and-forget debit vs. pre-auth
settle vs. local-only) and what the stored error says, and — per the Symptom
section above — whether the tenant credit pool debit at
`internal/app/quota.go:1000-1002` already ran before the failure. This
runbook does not prescribe an automatic fix: read the error alongside
`doc/runbook/platform-billing-breaker-open.md` and
`doc/runbook/billing-outbox-failures.md` to classify it, then decide with
the operator whether the affected user's balance or the tenant pool needs
correcting.

## Recover

No automatic recovery ships with this alarm — it is observability, not
enforcement, same as the two runbooks above.

## Verify

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_settlement_failed
```

## Provoking this alarm on demand (proof, not incident)

`PostConsumeQuotaFn` (the seam `app.SettleConsume` calls) has no faultsim
knob — unlike the upstream-facing alarms, this one is triggered by a local
DB/wallet failure, not an upstream HTTP response. Provoking it live needs a
DB-level fault, e.g. temporarily revoking `UPDATE` on the relevant table
from the UAT database role for the duration of one relay call, then
restoring it — **owner action, optional**, not required for this lane's
acceptance. There is no in-process way to drive this from outside the
process the way the fault-sim `http_500`/`timeout` model names drive the
upstream-facing alarms.

## Prevent

Nothing to prevent at the alarm level — a settlement call can fail for
reasons outside this service (DB contention, platform unreachable). The
alarm exists so a failure is visible instead of only a log line.
