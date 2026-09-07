# Runbook — STRANDED Wallet Debit (Pool Topup Revert Failed)

> **Source**: ADR 2026-05-18 (tenant-credit-pool) §9 Q4 — Tier 1.5 follow-up.
> **Triggered by**: log line containing `STRANDED wallet debit` emitted by
> `internal/adapter/handler/tenant_credit_pool.go:165` during pool topup.
> **Severity**: page (manual recovery + reconciliation required).
> **Last review**: 2026-05-19.

## Symptom

A Reseller called `POST /api/v2/admin/tenants/<id>/credit-pool/topup`. The
handler debited the Reseller's platform wallet, then the pool topup
failed (most commonly `ErrPoolWouldExceedCeiling`), and the compensating
`CreditWalletGRPC` revert ALSO failed (gRPC unavailable, platform
restart, network blip).

Result: the wallet was charged, the pool was not credited. The user
sees their wallet balance go down with no corresponding pool increase.

Log signature:

```
STRANDED wallet debit — pool topup AND revert both failed.
  account=<id> tenant=<slug> amount=<N>
  pool_err=<err> revert_err=<err>
```

This is the only place the literal token `STRANDED` is written by newhub
code, so a substring grep is sufficient.

## Detect

### Live tail

```bash
ssh root@100.98.57.55 \
  "kubectl logs -n lurus-system -l app=lurus-api --since=1h | grep STRANDED"
```

### One-shot count (last 24h)

```bash
ssh root@100.98.57.55 \
  "kubectl logs -n lurus-system -l app=lurus-api --since=24h \
   | grep -c STRANDED"
```

### Parse fields

Each line carries `account=`, `tenant=`, `amount=`, `pool_err=`,
`revert_err=`. Pull them for the reconciliation step:

```bash
ssh root@100.98.57.55 \
  "kubectl logs -n lurus-system -l app=lurus-api --since=24h" \
  | awk '/STRANDED/' \
  | sed -E 's/.*account=([0-9]+).*tenant=([^ ]+).*amount=([0-9]+).*/account=\1 tenant=\2 amount=\3/'
```

## Reconcile

Before any write, prove the imbalance from both sides — never trust the
log alone (the log says "debit succeeded, revert failed", not "the
debit is still on the wallet"; a half-completed retry may have settled
it already).

### Step 1 — Read the platform wallet

```bash
curl -sS \
  -H "Authorization: Bearer $IDENTITY_SERVICE_INTERNAL_KEY" \
  "$IDENTITY_SERVICE_URL/internal/v1/wallet/<account_id>" \
  | jq '{ balance, transactions: .recent_transactions[:5] }'
```

Look for the most recent `pool_topup` debit at the failing amount. If
the matching `pool_topup_revert` credit is also present → already
reconciled, no further action (close the alert, file a postmortem note).

### Step 2 — Read the newhub pool

```bash
ssh root@100.98.57.55 \
  "kubectl exec -n lurus-system deployment/postgres -- \
   psql -U lurus -d lurus -c \
   \"SELECT id, tenant_id, current_balance, max_balance, updated_at
       FROM tenant_credit_pools WHERE tenant_id = '<tenant>';\""
```

Then look at the most recent topup-direction draw to see whether the
topup landed:

```sql
SELECT id, direction, amount, reason, actor_user_id, created_at
  FROM tenant_credit_pool_draws
 WHERE pool_id = <id>
   AND direction = -1   -- credit
 ORDER BY id DESC LIMIT 5;
```

### Step 3 — Determine the fault state

|                          | Wallet debited | Wallet NOT debited |
|--------------------------|----------------|---------------------|
| **Pool credited**        | Already balanced — no-op | **Pool over-credited** (rare) — debit wallet by `amount` |
| **Pool NOT credited**    | **Stranded debit** — credit pool OR refund wallet | Already balanced — no-op |

The common case is the bottom-left cell: wallet was debited, pool was
not credited, revert failed.

## Recover

The recovery decision is **policy** (Anita-approved), not mechanical.
Two options:

### Option A — Refund the wallet (preferred)

Restores the Reseller's wallet to its pre-topup state. Reseller can
re-issue the topup themselves once whatever caused the original failure
is fixed (ceiling raised, pool resized, etc.).

```bash
# Replay CreditWalletGRPC manually via Platform's internal API.
# Uses a NEW idempotency key so the original failed-revert attempt
# doesn't collide.
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

`amount_in_LB` = `quota_units / QuotaPerUnit` (500000 — matches the handler's
conversion `currency.LucToLut()` in `TopupCreditPool`, `tenant_credit_pool.go`).

### Option B — Manually credit the pool

Use when the Reseller WANTS the topup to land (e.g. they fixed the
ceiling themselves, or the failure was transient and they don't want
to re-issue). Requires a DB write — needs 4-eyes review.

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

COMMIT;
```

Then re-check `current_balance` matches expectation before closing the
transaction.

## 4-eyes Review

Either option's write step (the gRPC call OR the SQL transaction) MUST
be reviewed by a second engineer before execution. Record both names
in the incident postmortem.

Acceptable evidence of 4-eyes:

- Slack message in `#oncall` showing the exact command and the
  reviewer's `+1` response, with timestamps.
- Pair-programmed via screenshare (note the reviewer's name).

## Verify

After recovery, re-run the two reads from "Reconcile":

1. Wallet balance is now back to the pre-topup value (Option A) OR the
   pool current_balance is now `<old> + <amount>` (Option B).
2. Either a new `pool_topup_revert` credit row exists on the wallet OR
   a new `adjustment` row exists on `tenant_credit_pool_draws`.
3. Re-grep the last hour of logs — no new `STRANDED` lines for this
   account / tenant pair.

File the postmortem in `doc/audit/` with:
- timestamp of original failure
- root cause (pool_err + revert_err)
- recovery option chosen + actor + reviewer
- whether Tier 2 outbox follow-up (see below) was filed

## Prevent

This recovery path exists because pool topup is necessarily non-atomic
across two services (Platform wallet + newhub pool DB). Tier 2 plans an
outbox pattern that removes the need for manual recovery:

1. Topup handler writes a `billing_outbox` row + debits wallet in the
   same transaction.
2. Background worker reads outbox, calls TopupPool, marks the row
   `applied`.
3. On topup failure, worker calls CreditWalletGRPC with the outbox
   row's deterministic idempotency key — retry budget bounded, never
   stranded after exhaustion (worker pages instead).

Until that lands, every STRANDED line walks through this runbook.

## Drill

This runbook is verified by manual drill, NOT automated test. To re-run
the drill (recommended quarterly):

1. On STAGE: force the pool ceiling below the planned topup
   (`UPDATE tenant_credit_pools SET max_balance = current_balance`).
2. Stop the Platform service (`scale --replicas=0`) so revert grpc
   fails too.
3. Call topup from a Reseller token — observe STRANDED log line.
4. Restart Platform service.
5. Walk this runbook end-to-end.
6. Confirm balances reconcile.
7. Restore the pool ceiling.

Log the drill in `doc/process.md`.
