# Runbook — STRANDED Wallet Debit (Pool Topup Revert Failed)

> **Source**: ADR 2026-05-18 (tenant-credit-pool) §9 Q4 — Tier 1.5 follow-up,
> superseded by the automatic reconciler (`internal/app/credit_pool_reconcile.go`).
> **Triggered by**: log line containing `STRANDED wallet debit` emitted by
> `internal/adapter/handler/tenant_credit_pool.go:372` during pool topup.
> **Severity**: informational unless `newhub_credit_pool_stranded_open` stays
> above 0 for more than a few sweep intervals (see "Escalate" below) — the
> event self-heals automatically in the common case.
> **Last review**: 2026-09-07.

## This is now mostly self-healing — read this first

A Reseller called `POST /api/v2/admin/tenants/<id>/credit-pool/topup`. The
handler debited the Reseller's platform wallet, then the pool topup failed
(most commonly `ErrPoolWouldExceedCeiling`), and the compensating
`CreditWalletGRPC` revert ALSO failed (gRPC unavailable, platform restart,
network blip).

The handler no longer just logs and gives up. It calls
`app.RecordStrandedTopup`, which persists the event as an open
`credit_pool_fund_events` row (`source = 'topup_stranded'`, `UNIQUE(tenant_id,
event_id)`). From there, **two automatic paths can close it, and manual SQL
is a last resort, not the default**:

1. **Background sweep** — `app.ReconcileStrandedTopups`, started by
   `app.StartCreditPoolReconcileWithContext` (`cmd/server/main.go`), runs
   every `CREDIT_POOL_RECONCILE_INTERVAL_SECONDS` (default 300s / 5min),
   leader-gated so only one replica acts. Each tick retries the compensating
   pool credit for every open stranded event, using the same ceiling guard as
   a normal topup, inside one DB transaction that claims the event
   (`source` → `'topup_reconciled'`), credits `tenant_credit_pools`, records
   the settled balance, and appends a `tenant_credit_pool_draws` row
   (`reason = 'topup'`) so the pool ledger conservation law still holds.
2. **Online retry** — if the Reseller (or their client) retries the same
   topup call with the same `Idempotency-Key`, `app.TryFinalizeStrandedTopup`
   runs before a fresh `TopupPool` would: the wallet debit is deduped
   upstream (same key ⇒ no second charge), and if a stranded/reconciled event
   exists for that key it settles that event instead of crediting the pool a
   second time. The caller sees `"reconciled": true` in the response.

A stranded event is idempotent and safe to leave alone for one or two sweep
intervals — it either closes on the next tick or the online retry closes it
sooner. **Do not touch the DB or the wallet manually until you have checked
whether the reconciler already closed it (see Detect) and, if not, identified
why it is failing (see Reconcile).**

## Detect

### Is anything still open right now?

```
curl -s http://localhost:30850/metrics | grep newhub_credit_pool_stranded
```
(Or via Netdata's `prometheus` job scrape — see CLAUDE.md `/metrics` access
notes.) `newhub_credit_pool_stranded_open` is a gauge recomputed after every
sweep tick — 0 means every previously-recorded stranded event is closed.
`newhub_credit_pool_stranded_total` is a monotonic counter of how many were
ever recorded (not how many are open).

### Which events, and why are they still open

```bash
ssh root@100.122.83.20 \
  "kubectl exec -n database lurus-pg-0 -- \
   psql -U postgres -d newhub -c \
   \"SELECT id, tenant_id, event_id, pool_id, amount, created_at
       FROM credit_pool_fund_events WHERE source = 'topup_stranded'
       ORDER BY created_at;\""
```

Every row here is an event the reconciler has not yet closed. If a row is
older than a few sweep intervals, tail the logs for its `event_id` to see why
compensation keeps failing:

```bash
ssh root@100.122.83.20 \
  "kubectl logs -n lurus-newhub -l app=lurus-newhub --since=6h \
   | grep '<event_id>'"
```

Two log lines matter:
- `credit-pool reconcile: stranded topup still failing event_id=... tenant=...
  consecutive_failures=N err=...` — emitted by `common.SysError` every failed
  sweep attempt for that event; `err` is almost always
  `ErrPoolWouldExceedCeiling` (permanent until the ceiling changes) or a pool
  lookup failure (pool row missing).
- `credit-pool reconcile: settled stranded topup event_id=... new_balance=...`
  — emitted by `common.SysLog` the moment it closes. If you see this, the
  event is done; no further action.

The original `STRANDED wallet debit — pool topup AND revert both failed.`
line (handler-emitted, once, at failure time) is context for the initial
page — it does NOT mean the event is still open by the time you read it; the
sweep may have already closed it. Always re-check the gauge/table above
before acting.

## Reconcile — find out why the sweep keeps failing

If `newhub_credit_pool_stranded_open` stays > 0 and `consecutive_failures` is
climbing for a specific event, the compensating credit is hitting the same
guard a normal topup would:

```bash
ssh root@100.122.83.20 \
  "kubectl exec -n database lurus-pg-0 -- \
   psql -U postgres -d newhub -c \
   \"SELECT id, tenant_id, current_balance, max_balance, updated_at
       FROM tenant_credit_pools WHERE tenant_id = '<tenant>';\""
```

- **`current_balance + amount > max_balance`** (ceiling exceeded): this is
  the common permanent-until-fixed case. Raise `max_balance` (via the
  console/admin API, or a reviewed SQL `UPDATE`) enough to fit the stranded
  amount. **Do not manually credit `current_balance` yourself** — once the
  ceiling allows it, the very next sweep tick (≤5min) claims the event and
  credits it automatically, using the same transaction that also writes the
  `tenant_credit_pool_draws` row. A hand-written credit here plus the
  automatic one is a double credit.
- **Pool row missing / tenant offboarded**: the sweep can never succeed.
  This is the one case that needs manual recovery — see below.

## Recover — manual path (ONLY when the reconciler structurally cannot close it)

This is now rare: it applies only when the blocking condition is not
something the reconciler will ever resolve on its own (pool deleted, tenant
fully offboarded, or a policy decision to refund instead of complete the
topup). If the blocker is a ceiling, fix the ceiling above instead — do not
use this section for that case.

The recovery decision (refund vs. force-credit) is **policy** (Anita-approved),
not mechanical, same as before. Whichever option you pick, the manual SQL
transaction MUST also flip the fund event's `source` from `'topup_stranded'`
to exactly `'topup_manually_closed'` (`app.FundEventSourceManuallyClosed`).
That value is terminal in code: the sweep only claims `'topup_stranded'`
rows, and an online retry with the same `Idempotency-Key` is answered
`409 POOL_TOPUP_INTENT_CLOSED` by `app.TryFinalizeStrandedTopup` instead of
running a fresh `TopupPool` — the wallet debit for that key was deduped
upstream, so a fresh pool credit would be free money. Any other value is NOT
recognised and leaves the retry path open, so do not invent your own.
Tell the Reseller the key is void; a new intent needs a new key.

### Option A — Refund the wallet AND close the event

```bash
REVERSAL_KEY="manual_revert_$(date +%s)_<ticket_id>"

curl -sS -X POST \
  -H "Authorization: Bearer $IDENTITY_SERVICE_INTERNAL_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "account_id": <account_id>,
    "amount":     <amount_in_LB>,
    "reason":     "manual_stranded_revert",
    "description":"Manual revert: pool topup STRANDED on <date>, ticket <id>",
    "source":     "newhub_runbook",
    "idempotency_key": "'"$REVERSAL_KEY"'"
  }' \
  "$IDENTITY_SERVICE_URL/internal/v1/wallet/credit"
```

`amount_in_LB` = `quota_units / QuotaPerUnit` (`currency.LucToLut()`, same
conversion `TopupCreditPool` uses).

```sql
UPDATE credit_pool_fund_events
   SET source = 'topup_manually_closed'
 WHERE event_id = '<event_id>' AND tenant_id = '<tenant_id>'
   AND source = 'topup_stranded';
-- must affect exactly 1 row — 0 means the sweep already claimed it
-- (re-check via Detect before proceeding with the wallet refund above).
```

### Option B — Force-credit the pool AND close the event

Use when the Reseller wants the topup to land and the blocker is permanent
(e.g. pool being retired, so raising the ceiling is not the right fix).
Requires a DB write — needs 4-eyes review.

```sql
BEGIN;

UPDATE tenant_credit_pools
   SET current_balance = current_balance + <amount>,
       updated_at = NOW()
 WHERE id = <pool_id>;

INSERT INTO tenant_credit_pool_draws
       (pool_id, tenant_id, direction, amount, reason,
        actor_user_id, created_at)
VALUES (<pool_id>, '<tenant_id>',
        -1,                       -- credit
        <amount>,
        'adjustment',             -- NOT 'topup' — preserves audit clarity
        <ops_actor_user_id>,
        NOW());

UPDATE credit_pool_fund_events
   SET source = 'topup_manually_closed', new_balance = current_balance + <amount>
 WHERE event_id = '<event_id>' AND tenant_id = '<tenant_id>'
   AND source = 'topup_stranded';
-- must affect exactly 1 row — 0 means the sweep already claimed it; ROLLBACK
-- and re-check via Detect instead of crediting twice.

COMMIT;
```

Then re-check `current_balance` matches expectation before closing the
transaction.

## 4-eyes Review

Either manual option's write step MUST be reviewed by a second engineer
before execution. Record both names in the incident postmortem.

Acceptable evidence of 4-eyes:

- Slack message in `#oncall` showing the exact command and the
  reviewer's `+1` response, with timestamps.
- Pair-programmed via screenshare (note the reviewer's name).

## Verify

After any manual recovery, re-run Detect:

1. `SELECT source FROM credit_pool_fund_events WHERE event_id = '<event_id>'
   AND tenant_id = '<tenant_id>'` is `'topup_manually_closed'`, not
   `'topup_stranded'`.
2. `newhub_credit_pool_stranded_open` has dropped by 1. The gauge is
   recomputed only by the sweep tick (`refreshStrandedOpenGauge`), so allow
   up to one interval (default 5 min) before reading it.
3. Wallet balance / pool `current_balance` match the option you chose.
4. A retry with the original `Idempotency-Key` now answers
   `409 POOL_TOPUP_INTENT_CLOSED` and moves no money (probe it on UAT with the
   same key if in doubt).

For the common case (no manual recovery needed — the sweep or an online
retry closed it), just confirm `newhub_credit_pool_stranded_open` is back to
its pre-incident value and the `credit-pool reconcile: settled stranded
topup` log line exists for the event_id.

File the postmortem in `doc/audit/` with:
- timestamp of original failure
- root cause (pool_err + revert_err, or "self-healed by sweep at <time>")
- whether manual recovery was needed, and if so which option + actor + reviewer

## Escalate

Escalate (page, don't just watch) when either:
- `newhub_credit_pool_stranded_open` stays > 0 for more than ~3 sweep
  intervals (default 15min) for the same event, or
- `consecutive_failures` in the log line keeps climbing with the same
  `err=` — that's a permanent blocker (ceiling or missing pool) that needs a
  human decision, not more retries.

## Drill

This runbook is verified by manual drill, NOT automated test. To re-run
the drill (recommended quarterly), on STAGE:

1. Force the pool ceiling below the planned topup
   (`UPDATE tenant_credit_pools SET max_balance = current_balance`).
2. Stop the Platform service (`scale --replicas=0`) so revert grpc fails too.
3. Call topup from a Reseller token — observe the `STRANDED wallet debit`
   log line and the new `credit_pool_fund_events` row (`source =
   'topup_stranded'`).
4. Restart Platform service, but leave the ceiling low — confirm the sweep
   (or set `CREDIT_POOL_RECONCILE_INTERVAL_SECONDS` low for the drill) keeps
   retrying and failing (`consecutive_failures` rising), i.e. Escalate would
   have fired.
5. Raise the ceiling back — confirm the very next sweep tick closes the
   event automatically (no manual SQL needed) and
   `newhub_credit_pool_stranded_open` returns to 0.
6. Only then walk the manual Recover section once, against a second
   synthetic event, to keep the muscle memory current for the rare case it's
   actually needed.

Log the drill in `doc/process.md`.
