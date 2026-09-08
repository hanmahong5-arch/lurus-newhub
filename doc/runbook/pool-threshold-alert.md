# Runbook — CreditPoolBalanceLow / CreditPoolExhausted

> **Source**: ADR 2026-05-18 (tenant-credit-pool) + Lane δ alerts.
> **⚠️ NOT WIRED (verified 2026-09-07)**: the two rules below live only in
> `deploy/k8s/r6-stage/newhub-prometheus-rule.yaml`, a `PrometheusRule` CRD no
> kustomization applies and R6 runs no Prometheus Operator to accept — R6's
> monitoring is host netdata, which does not evaluate this file. Neither alert
> fires or pages anyone today; a low/exhausted pool is visible only by reading
> `lurus_gateway_credit_pool_balance` directly or the admin credit-pool
> endpoints. `deploy/grafana/newhub-alerts.yaml`, the file this runbook
> originally pointed at, was undeployed Grafana JSON and was deleted
> 2026-09-07. Treat the table below as the design target for when this gets
> wired to a real alerting backend, not as a live behaviour.
> **Last review**: 2026-05-18 (content), 2026-09-07 (deployment status).

## Alert pair (design target — see NOT WIRED note above)

| Alert | Severity | Condition | Duration |
|---|---|---|---|
| `CreditPoolBalanceLow` | warning | `lurus_gateway_credit_pool_balance < 1000 and > 0` | 10m |
| `CreditPoolExhausted` | page | `lurus_gateway_credit_pool_balance <= 0` | 5m |

Both labels include `tenant_id`. The exhausted CONDITION is
**user-impacting** whether or not anything alerts on it: all relay calls for
that tenant are returning HTTP 402 from `middleware.PoolBalanceCheck`. Finding
out today means reading the gauge or the admin endpoint — nothing pages.

## Triage — 5-minute path

1. **Identify the tenant**.
   ```
   tenant_id={{ $labels.tenant_id }}
   ```

2. **Identify the Reseller (pool owner)**.
   ```sql
   SELECT created_by_user_id, max_balance, current_balance, reset_period, next_reset_at
     FROM tenant_credit_pools
    WHERE tenant_id = '<tenant_id>';
   ```

3. **Look at recent debit pattern** — the admin endpoint (there is no Grafana
   panel; see the NOT WIRED note at the top of this runbook):
   ```
   GET /api/v2/admin/tenants/<tenant_id>/credit-pool/usage?limit=50
   ```

4. **Decide**:
   - Normal burn-down before reset (`next_reset_at` within hours) → no action,
     pool will reset.
   - Unexpected spike → ask Reseller to topup OR temporarily raise ceiling.
   - Suspect runaway loop on the end-customer side → revoke their token via
     Provisioning API or admin UI.

## Reseller contact procedure

1. Slack / WeChat / email the Reseller using the contact stored in the
   tenants admin record. The user_id from step 2 above maps to a row in
   `users` — look up email / phone there.
2. Reseller can topup themselves via UI (drawer → Topup form), which
   funds from their platform wallet:
   ```
   POST /api/v2/admin/tenants/<tenant_id>/credit-pool/topup
   { "amount": <quota_units>, "reason": "ops-requested topup" }
   ```
3. If Reseller is unresponsive >24h AND pool fully exhausted: page the
   on-call (severity `page` should already do this); decide between:
   - Manual unlimit (`DELETE` + recreate with `max_balance: -1`) — buys
     time, defers Reseller billing question; **only with Anita's approval**.
   - Accept user-facing outage; document and bill Reseller per contract.

## Common false positives

- **Reset moment**: pool drops to zero, alert fires, scheduled reset
  refills it within seconds. The `for: 5m` window catches transient
  resets, but a sticky reset bug could still page. Check `last_reset_at`
  vs `next_reset_at` — if those look stuck, file a bug.
- **New tenant, never topped up**: pool has `current_balance=0` from
  creation. Either topup or set `max_balance=-1` (unlimited) explicitly.
- **PostConsumeQuota over-debit race**: rare — pool gate let a request
  through but post-consume found the pool exhausted (concurrent debits).
  Logged in app server as "pool debit non-fatal". This drift self-heals
  on next reset; if persistent, raise alert threshold or add gate-side
  reservation.

## Reference

- ADR: `_bmad-output/planning-artifacts/adr-2026-05-18-tenant-credit-pool.md`
- Pool schema: `migrations/012_create_tenant_credit_pools.sql`
- Gate middleware: `internal/adapter/middleware/pool_balance_check.go`
- Debit call-site: `internal/app/quota.go` PostConsumeQuota Phase 2.5
- NATS event: subject `llm.pool.threshold` published by
  `internal/pkg/nats/pool_threshold.go` (dual schema + Redis dedup).
