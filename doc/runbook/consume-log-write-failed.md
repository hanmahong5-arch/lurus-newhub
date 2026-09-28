# Consume-log insert failed after the charge

Alarm: `newhub_consume_log_write_failed` on
`lurus_billing_consume_log_write_failed_total` (netdata,
`deploy/r6-host-netdata/health.d/newhub.conf`). Counter written by
`repo.RecordConsumeLog` (`internal/adapter/repo/log.go`), one increment per
row whose `LOG_DB.Create` returned an error.

## What it means

`RecordConsumeLog` is the last step of every relay path, after the quota
debit and the wallet settlement. When its insert fails:

- **Money is correct.** `users.quota`, the tenant credit pool, the token's
  `used_quota` and the platform wallet were all already updated.
- **The usage record is gone.** The `logs` row is what
  `/api/v2/billing/invoices` (`priced_cny4` / `charged_cny4`), the dashboard,
  per-model analytics and the customer's own usage view read. Every consumer
  of those sees less usage than was charged for the affected window.

The process log line `failed to record log: <error>` (same replica, same
timestamp) carries the database error.

## Triage

1. Confirm the window and replica: the counter is per replica, `kubectl -n
   lurus-newhub logs deploy/lurus-newhub --all-containers | grep 'failed to
   record log'`.
2. Read the error. The two cases seen or expected:
   - **Schema drift** (`column ... does not exist`, `relation ... does not
     exist`): a migration did not run on this database. Check
     `lurus_gateway_schema_migrations_pending` and `/api/health`
     `checks.schema_migrations`; run the missing migration. Every row in the
     window is lost until it is fixed.
   - **Connection / pool errors**: usually alongside `newhub_db_slow_queries`
     (`doc/runbook/db-pool-saturation.md`). Rows are lost for the duration of
     the outage only.
3. The consume log is not retried and cannot be rebuilt from newhub alone.
   The platform wallet ledger (`billing.wallet_transactions`, one row per
   settle / debit with the newhub request id as reference) is the source to
   reconcile invoices against for the window; usage tokens per model are
   not recoverable, only the charged amounts.

## Related

- `newhub_billing_money_lost` is the opposite failure: the row may exist but
  the wallet movement did not happen (`doc/runbook/billing-outbox-failures.md`).
- `logs.priced_cny4` / `charged_cny4` semantics: migration 041/042 notes in
  `internal/adapter/handler/v2_billing_invoices.go`.
