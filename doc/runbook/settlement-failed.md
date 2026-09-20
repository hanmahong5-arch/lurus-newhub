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
- `internal/app/quota.go` `PostWssConsumeQuota` — covered since cycle 13: it
  now routes the session's pre-consumption through `app.SettleConsume` with
  `path="realtime"` and flags the row's `other.settlement` on failure (see
  the realtime section at the end of this runbook).

These are this cycle's documented non-goals, not an oversight discovered
after the fact — extending the seam to them is next-cycle scope.

## Billing exclusion (cycle13 L2)

A `settlement="failed"` row (this alarm) and a manual channel-test probe row
(`other.source="channel_test"`, `internal/adapter/handler/channel-test.go`'s
`channelProbeLogSource`) are both `type=consume` rows with a real `Quota`
that nobody's wallet paid — the same two markers this alarm's badge and the
manual probe write onto the row itself. Since cycle13 L2, neither counts
toward a customer's billed total: `repo.BillableConsumePredicate`
(`internal/adapter/repo/log.go`) excludes both markers from
`GET /api/v2/:tenant_slug/billing/invoices` (`aggregateInvoiceMonths`,
`internal/adapter/handler/v2_billing_invoices.go`) and from
`GET /v1/billing/usage` (`repo.GetUserLogStatByPeriod`). The invoices
response surfaces what it excluded instead of silently dropping it: each
monthly bucket adds `unbilled_quota` (the excluded quota sum) and
`unbilled_request_count` (the excluded row count) alongside the existing
`quota`/`amount_cny`/`request_count`, which now cover only the billable
rows. A row appearing in `unbilled_quota` is not itself a refund signal —
see "Reconcile" below for what a flagged row's pool/wallet state actually
requires.

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

## Realtime sessions (`path="realtime"`, cycle 13)

A `/v1/realtime` request freezes an estimate like every other relay format
(`handler/relay.go:360` `app.PreConsumeQuota`) and then charges each usage
event as it arrives (`PreWssConsumeQuota` -> `PostConsumeQuota`). By the end
of the session the whole cost has already been taken, so the freeze is pure
surplus.

Until cycle 13 nothing gave it back: `WssHelper` returns as soon as
`PostWssConsumeQuota` comes back, and `releasePreConsumedOnFailure` only runs
on an error, so every successful realtime session kept
`FinalPreConsumedQuota` of the customer's money. `PostWssConsumeQuota` now
ends with

    SettleConsume(ctx, relayInfo, -FinalPreConsumedQuota, FinalPreConsumedQuota, "realtime")

whose two arguments sum to zero: the user balance and the key allowance get
the freeze back, the tenant pool and the daily counter are untouched (they
were moved per event), and a live platform pre-auth reaches
`PostConsumeQuota`'s zero-usage release arm instead of expiring on the
platform's TTL.

What you see when it fails: `lurus_billing_settlement_failed_total{path="realtime"}`
increments and the session's consume row carries `other.settlement="failed"` —
same two signals as the other paths in this runbook. A failure here means the
customer was NOT given the freeze back; reconcile by hand from the row
(`quota` is the session's charge, the freeze is in the request's pre-consume
log line).

## Async-task refunds and video re-settlement (cycle 13)

A task submission settles through `app.PostConsumeQuota`, which moves four
ledgers: `users.quota`, `tokens.remain_quota`, the tenant credit pool and
(for wallet-bridged accounts) the platform wallet. A refund
(`handler/task_refund.go`) now hands back the first three plus the two
`used_quota` counters. Two honest gaps remain, both visible in the log:

- **Wallet leg** — lurus-platform has no reverse of a settled debit
  (**O-refund**). Every refund of a wallet-bridged task logs
  `{"event":"task_refund_wallet_unreversed", ...}` with the account id and the
  amount. Reconciliation is a manual platform-side credit.
- **Unresolved payer** — neither `tasks` nor `midjourneys` carries a
  `token_id`/`tenant_id` column, so the refund recovers the payer from the
  submission's consume row (same user, channel and amount). When consume
  logging was off for that submission, or when two keys of the same user have
  identically-shaped rows, the payer is not knowable and the refund restores
  the balance only, logging
  `{"event":"task_refund_payer_unresolved", ...}` /
  `{"event":"video_resettle_payer_unresolved", ...}`. Crediting a guessed key
  or pool would move money onto a payer that never paid, which is why it is
  left short and reported instead.

A finished video task with real token usage is re-settled against the same
primitives (`handler/task_video.go` `resettleVideoTask`): the difference is
charged through `app.PostConsumeQuota` or handed back through the refund
path, and **the submission's consume row is rewritten to the actual amount**
rather than the difference being recorded as a `system` log row that no
invoice counts. The platform wallet leg of a re-settlement is deliberately
not fired from the poller: the task's wallet money is bound to the
submission's pre-auth, which this path has no handle on.

Two more residues of that payer lookup, recorded here rather than implied
(cycle-13 acceptance):

- `resolveTaskChargeLedger` inspects only the 50 newest matching consume rows
  (`taskChargeCandidates`). A user who submits more than 50 identically-shaped
  tasks (same channel, same estimate) inside the lookback window before the
  first of them settles pushes the real submission out of that window; the
  refund then resolves against a newer row of the same key. Key and tenant
  are still right — every candidate must name one key or the lookup declines
  — only `LogID` may point at a sibling submission.
- Video re-settlement rewrites the NEWEST row that matches the estimate, not
  provably the row of the task being settled: with several in-flight video
  tasks of the same estimate the rewrite can land on a sibling's row. The
  invoice total is unchanged either way (one row per task, each rewritten
  once); per-row attribution is what can be off.

Both go away when `tasks` carries `token_id` / `tenant_id` (and the consume
row's `log_id`) of its own — **O-task-payer**, a later migration, not this
cycle's.
