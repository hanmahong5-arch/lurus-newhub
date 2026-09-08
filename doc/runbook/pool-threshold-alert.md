# Runbook — tenant credit-pool reset + threshold alert

> **Source**: ADR 2026-05-18 (tenant-credit-pool) + Lane δ alerts + Lane
> L4-POOL-RENEWAL-AND-ALERT (2026-09-08, un-dead: wired the reset schedule and
> the threshold publisher onto the debit path).
> **Still true**: `deploy/k8s/r6-stage/newhub-prometheus-rule.yaml`'s
> `CreditPoolBalanceLow` / `CreditPoolExhausted` rules are a `PrometheusRule`
> CRD no kustomization applies, and R6 runs no Prometheus Operator to accept
> one — R6's monitoring is host netdata, which does not evaluate this file.
> **No pager consumes either rule.** Treat the rule table below as the design
> target for a real alerting backend, not as live behaviour.
> **What changed 2026-09-08**: before this lane, `reset_period` /
> `next_reset_at` on `tenant_credit_pools` were schema with no reader — a
> pool configured to renew just drained to zero and stayed there — and the
> threshold publisher (`internal/pkg/nats/pool_threshold.go`) short-circuited
> to a silent no-op whenever `LLM_QUOTA_NATS_ENABLED` was false, which is
> every environment except production. Both gaps are closed; see the two
> sections below. **Last review**: 2026-05-18 (content), 2026-09-07
> (deployment status), 2026-09-08 (reset job + alert wiring).

## Scheduled reset

`CREDIT_POOL_RESET_MODE` (`.env.example`) controls whether the reset pass
writes anything:

| Mode | Behaviour |
|---|---|
| `observe` (default) | Every due pool (`next_reset_at <= now`, `reset_period` not `none`/empty, `max_balance > 0`) is evaluated and counted (`credit_pool_reset_total{action="observed"}`), but the balance is untouched. |
| `enforce` | Each due pool is refilled to `max_balance` in its own CAS-guarded transaction, `last_reset_at`/`next_reset_at` advance, `alert_fired_at` clears, and a `reset`-reason credit draw records the delta — `credit_pool_reset_total{action="reset"}` increments and a `billing.pool_reset` audit event is written. |

Wiring: the pass runs inside the leader's existing stranded-topup reconcile
ticker (`internal/app/credit_pool_reconcile.go`, same 300s interval,
`CREDIT_POOL_RECONCILE_INTERVAL_SECONDS` to override) — no separate
scheduler. It can also be rehearsed on demand without waiting for a tick:

```
POST /internal/admin/reset-due-pools?mode=observe|enforce
X-API-Key: <admin-scope key>
```

`mode` overrides `CREDIT_POOL_RESET_MODE` for that call only; omitted falls
back to the environment's current mode. A `mode` value that IS supplied but
is neither `observe` nor `enforce` is rejected with `400` (not silently
treated as `observe`) —
`{success:false, message:"unrecognised mode \"<value>\" — accepted values: \"observe\", \"enforce\""}`.
Otherwise response is
`{success, data:{mode, pools:[{tenant_id,pool_id,delta,action,next_reset_at}]}}`.

**Operational caveats before turning `enforce` on**:

- `observe` mode never advances `next_reset_at` (it is read-only by design —
  see the mode table above). The same due pool is therefore re-evaluated and
  re-counted (`credit_pool_reset_total{action="observed"}`) on **every**
  tick (default 300s) for as long as the environment stays in `observe` —
  this is expected, not a bug, and is not itself actionable.
- The **first** `enforce` pass an environment ever runs catches up **every**
  pool whose `next_reset_at` is already in the past, not just the newest
  one — with the ADR default `reset_period=monthly`, that is most existing
  pools the day `enforce` is turned on. Always rehearse `?mode=observe`
  first and review the full `pools[]` list before flipping to `enforce`, so
  a large simultaneous refill batch is not a surprise.
- Weekly (`reset_period=weekly`) pools reset at the next Monday 00:00 UTC;
  daily at the next UTC midnight; monthly at the 1st of next month 00:00
  UTC (`internal/adapter/repo/tenant_credit_pool.go` `nextResetAt`).
- `?mode=enforce` on the rehearsal endpoint intentionally bypasses
  `CREDIT_POOL_RESET_MODE` for that one call — this is by design (an
  operator with the admin scope can force a single enforce pass even while
  the environment default stays `observe`), not a gap to close.
- The threshold alert (below) can re-fire once per `poolSchemaDedupWindow`
  (1h) per pool while the balance stays below `alert_threshold_pct` — it is
  not a one-shot notification. A re-fire is triggered by the next debit that
  lands after the window has elapsed (the check runs on every post-consume
  debit, there is no timer), so a pool with no traffic does not re-fire;
  debits (and further overdraft) continue normally between fires. The alert never
  alters or blocks the debit that triggered it, and for a streamed request
  the response bytes are already on the wire by the time it runs. It does
  run synchronously in the same request goroutine as the debit
  (`internal/app/quota.go` `maybeAlertPoolThreshold`, called from
  `debitTenantPool`, itself called from `PostConsumeQuota`), bounded by
  `creditPoolAlertHookTimeout` (2s) — so on the rare crossing where the
  publish attempt is slow, that goroutine (and the concurrency-limit slot
  `RelayConcurrencyLimit` releases on its `c.Next()` return) can be held for
  up to that long. This does not delay bytes already sent to the client,
  but is not "never slows" the relay path either.

**Refill-to-ceiling / overdraft-forgiveness decision (owner sign-off required
before `enforce` on production)**: the credited delta is
`max_balance − current_balance`. If a pool went negative (overdraft — see
"PostConsumeQuota over-debit race" below), the next reset forgives that debt
by refilling all the way to the ceiling rather than to zero-then-topup. This
is the intended and simplest semantic for a pre-paid pool but it does mean an
unpaid overdraft is written off on the pool's own schedule, not carried
forward. Confirm this is the desired billing behaviour before turning
`CREDIT_POOL_RESET_MODE=enforce` on for a production tenant; the switch
tenant's pool (`reset_period=none`, migration 030) is never selected by this
pass regardless of mode.

## Threshold alert

Crossing a pool's `alert_threshold_pct` (checked on every post-consume debit
and overdraft settle, `internal/app/quota.go` `maybeAlertPoolThreshold`) runs
`hubnats.PublishPoolThreshold` (`internal/pkg/nats/pool_threshold.go`)
whenever `maybeAlertPoolThreshold`'s own precheck (same 1h window,
`pool.AlertFiredAt`/`hubnats.SchemaDedupWindow()`) hasn't already skipped
it — that precheck exists purely to avoid a DB read on every below-threshold
debit and does not itself write anything, so a crossing it skips leaves
`alert_fired_at` exactly where the last real fire left it. Once
`PublishPoolThreshold` does run, its own two dedup layers (schema
`alert_fired_at` window + Redis SETNX, both as before) still govern whether
a given crossing actually does anything, but `LLM_QUOTA_NATS_ENABLED` being
false no longer skips the pass before those dedup layers run. When both the
caller precheck and the publisher's dedup let a crossing through:

- `tenant_credit_pools.alert_fired_at` is set (durable dedup record).
- A `billing.pool_threshold` audit event is written
  (`{pool_id, balance, max_balance, threshold_pct, delivery}`).
- `credit_pool_alert_total{tenant_id, delivery}` increments, where
  `delivery` is `nats` — only once the payload has actually reached the
  wire without error — or `recorded_only` (every other environment today,
  or a production crossing where NATS itself was never reached — marked/
  audited/counted, never published). Two cases increment neither label: a
  crossing where a publish was attempted (NATS enabled) and failed — the
  audit row still records it (`delivery: "recorded_only"` plus an `error`
  field) — and a crossing where the `alert_fired_at` mark itself could not
  be written (DB error; no audit row, `fired=false`). In both cases the
  honest counter is `credit_pool_alert_hook_error_total`, not this series —
  see `internal/pkg/nats/pool_threshold.go`.

**No pager consumes any of this.** The condition — and, in production, the
event itself — is visible by reading the gauge, the audit trail
(`GET /api/v2/admin/audit/events?action=billing.pool_threshold`), or the
counter. **platform-core has no subscriber for `llm.pool.threshold` today**
(verified 2026-09-08 against `2l-svc-platform` `modules/notification`: its
NATS consumer subscribes to a number of subjects, but among the `llm.*`
subjects it handles only `llm.quota.threshold`, `llm.image.generated`, and
`llm.usage.milestone` — `SubjectPoolThreshold` is not among them). The event
is published to JetStream `LLM_EVENTS` once per pool per 1h window while the
pool stays below threshold, on every environment where NATS is enabled
(production today); it is durable on the stream but nothing reads it —
treat `delivery=nats` as "recorded on the wire", not "delivered to a
consumer". Adding a platform-core subscriber (or formally deciding this
event stays `recorded_only`-by-design forever) is an owner follow-up, not
something this lane closes. Wire contract (subject, payload shape) belongs
in the root `doc/coord/contracts.md` event registry next to the
`llm.quota.threshold` row — `llm.pool.threshold` is not registered there yet
(cross-repo follow-up, owner action, not
`doc/product-integration-guide.md`, which does not document NATS events).

## Alert pair (design target for a future alerting backend — see header)

| Alert | Severity | Condition | Duration |
|---|---|---|---|
| `CreditPoolBalanceLow` | warning | `lurus_gateway_credit_pool_balance < 1000 and > 0` | 10m |
| `CreditPoolExhausted` | page | `lurus_gateway_credit_pool_balance <= 0` | 5m |

Both labels include `tenant_id`. The exhausted CONDITION is
**user-impacting** whether or not anything alerts on it: all relay calls for
that tenant are returning HTTP 402 from `middleware.PoolBalanceCheck`.

## Triage — 5-minute path

1. **Identify the tenant**.
   ```
   tenant_id={{ $labels.tenant_id }}
   ```

2. **Identify the Reseller (pool owner)**.
   ```sql
   SELECT created_by_user_id, max_balance, current_balance, reset_period, next_reset_at, alert_fired_at
     FROM tenant_credit_pools
    WHERE tenant_id = '<tenant_id>';
   ```

3. **Look at recent debit pattern** — the admin endpoint (there is no Grafana
   panel; see the header note):
   ```
   GET /api/v2/admin/tenants/<tenant_id>/credit-pool/usage?limit=50
   ```
   and, for a threshold crossing specifically:
   ```
   GET /api/v2/admin/audit/events?action=billing.pool_threshold
   ```
   This is the global admin route (`handler.GetAuditEvents`) — it has no
   `tenant_id` filter (it ignores one if you pass it; it always queries
   across every tenant), so narrow by eyeballing the tenant_id field inside
   each returned event's JSON, or by `resource_id` (the pool's numeric ID)
   if you already know it from step 1.

4. **Decide**:
   - Normal burn-down before reset (`next_reset_at` within hours, and
     `CREDIT_POOL_RESET_MODE=enforce` is on for this environment) → no
     action, the reconcile tick will refill it.
   - `next_reset_at` is in the past and the balance never moved → mode is
     still `observe`, or the pool's `reset_period`/`max_balance` doesn't
     qualify (`none`, empty, or `max_balance <= 0`) — rehearse
     `?mode=observe` to see why it's excluded before flipping to enforce.
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
3. If Reseller is unresponsive >24h AND pool fully exhausted: this is a
   manual escalation today — nothing pages on-call automatically. Decide
   between:
   - Manual unlimit (`DELETE` + recreate with `max_balance: -1`) — buys
     time, defers Reseller billing question; **only with Anita's approval**.
   - Accept user-facing outage; document and bill Reseller per contract.

## Common false positives / non-issues

- **Reset moment**: with `CREDIT_POOL_RESET_MODE=enforce`, the pool refills
  the instant the leader's reconcile tick (or a rehearsal `?mode=enforce`
  call) processes it — `last_reset_at` should be within one tick interval of
  `next_reset_at`'s scheduled time. If `last_reset_at` is stuck well behind
  `next_reset_at`, check the leader is actually leading (`common.IsLeader()`)
  and that the reconcile ticker is running.
- **New tenant, never topped up**: pool has `current_balance=0` from
  creation. Either topup or set `max_balance=-1` (unlimited) explicitly.
- **PostConsumeQuota over-debit race**: rare — pool gate let a request
  through but post-consume found the pool exhausted (concurrent debits).
  Recorded as a `relay_overdraft` draw (negative balance), logged as
  `pool_overdraft`. This drift self-heals on the next `enforce` reset (which
  forgives the overdraft, see "Refill-to-ceiling" above); if persistent
  outside a reset window, raise the alert threshold or add gate-side
  reservation.

## Reference

- ADR: `_bmad-output/planning-artifacts/adr-2026-05-18-tenant-credit-pool.md`
- Pool schema: `migrations/012_create_tenant_credit_pools.sql`
- Gate middleware: `internal/adapter/middleware/pool_balance_check.go`
- Debit call-site + alert hook: `internal/app/quota.go` `debitTenantPool` /
  `maybeAlertPoolThreshold`
- Reset job: `internal/app/credit_pool_reset.go`,
  `internal/adapter/repo/tenant_credit_pool.go` `ResetDuePools`
- Rehearsal endpoint: `internal/adapter/handler/internal_maintenance.go`
  `InternalResetDuePools`
- NATS event: subject `llm.pool.threshold` published by
  `internal/pkg/nats/pool_threshold.go` (dual schema + Redis dedup; marks
  `alert_fired_at` and increments `credit_pool_alert_total` itself for the
  no-publisher / successful-publish cases, publishes only when
  `LLM_QUOTA_NATS_ENABLED` and a publisher is wired; the audit row is
  written by the caller in `internal/app/quota.go`, not by this package).
