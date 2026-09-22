# ADR-2026-05-18: Budget Threshold Alerts & Notification Subscriptions

**Status**: Proposed  
**Date**: 2026-05-18  
**Author**: Architect (lurus-newhub)  
**References**: story-7-5 newhub-alerts.yaml

---

## 1. Context

Today, when a Reseller's end-customer exhausts their token quota, the sequence is:
hard block (HTTP 402) → customer confusion → support ticket to Reseller → Reseller ticket to Lurus.
There is zero advance warning: no 50% nudge, no 80% escalation, no webhook for programmatic top-up automation.

This is a known industry minimum bar. Portkey, LiteLLM, Bifrost, and CostHawk all ship deduped 50/80/100 webhook+email alerts on per-key monthly caps. Anthropic Console configures $10/$50/$100 billing thresholds. Datadog LLM monitors default-enable error-rate and cost-spike rules. Helicone surfaces cache hit rate drops as a first-class alert. Story 7-5 delivered the Grafana SLO dashboard and 11 operator-facing alert rules but no customer-facing quota warnings and no out-of-the-box default rule pack.

The business impact is direct: Resellers in B2B mode cannot build self-service sub-tenant management without budget webhooks, because they have no reliable signal to top up a customer key before the hard wall is hit.

---

## 2. Decision

Add a two-layer alerting system to newhub:

**Layer 1 — Prometheus operator alerts (default rule pack)**: Extend `newhub-alerts.yaml` with 6 new rules covering cost spike (hourly + daily), P99 latency, 5xx error rate, zero-response rate, and cache hit drop. These fire to Alertmanager (operator channel) and are enabled by default.

**Layer 2 — Customer-facing quota threshold notifications**: An in-process scheduled evaluator checks `token.remain_quota / token.used_quota` ratios at a configurable interval (default 5 min), emits NATS events to `LLM_EVENTS` stream, and the existing lurus-platform notification consumer delivers webhook + email. A new `notification_subscription` table stores per-user delivery preferences. API endpoints under `/api/v2/{slug}/notifications/subscriptions` manage subscriptions.

Neither layer requires a new microservice; both are additive to existing code paths.

---

## 3. Event Taxonomy

| Event Name | Trigger Condition | Scope | Expected Frequency | Audience |
|---|---|---|---|---|
| `quota.threshold.50` | `used_quota / (used_quota + remain_quota) >= 0.50` AND `!unlimited_quota` | Per-token | Once per billing period per token | Token owner, tenant admin |
| `quota.threshold.80` | Same ratio >= 0.80 | Per-token | Once per billing period per token | Token owner, tenant admin |
| `quota.threshold.100` | `remain_quota <= 0` | Per-token | Once per billing period per token | Token owner, tenant admin (urgent) |
| `tenant.quota.threshold.80` | Sum of `used_quota` across all tenant tokens / total allocated >= 0.80 | Per-tenant | Once per billing period per tenant | Tenant owner (Reseller admin) |
| `tenant.quota.threshold.100` | Tenant-wide quota fully consumed | Per-tenant | Once per billing period per tenant | Tenant owner (Reseller admin) |
| `cost.spike.hourly` | Last-60-min quota spend > 3× hourly average of prior 7 days | System-wide / per-tenant | At most once per hour per tenant | Operator on-call |
| `cost.spike.daily` | Today's quota spend > 150% of 7-day daily average | System-wide / per-tenant | At most once per day per tenant | Operator on-call |
| `latency.p99.spike` | P99 relay duration > 8s over rolling 10 min | System-wide | At most once per 10 min | Operator on-call |
| `error.rate.spike` | 5xx rate > 5% over rolling 5 min | System-wide | At most once per 5 min | Operator on-call |
| `balance.low` | Tenant credit pool < 10% of ceiling | Per-tenant | At most once per day | Tenant owner — depends on L1 tenant credit pool ADR (T2.A); blocked until that schema lands |
| `zero.response.rate` | Empty completions (`completion_tokens == 0`) > 10% of relay requests over rolling 10 min | System-wide | At most once per 10 min | Operator on-call |
| `cache.hit.drop` | `lurus_gateway_cache_hits_total{result="hit"}` rate drops > 30% vs. 24h baseline | System-wide | At most once per hour | Operator on-call — depends on exact-match cache feature (T3.B); rule defined now, enabling deferred |

**Note on `balance.low`**: the competitive reference (Anthropic Console configurable dollar thresholds) maps cleanly to a tenant credit pool, but newhub today has no credit pool at the tenant level — only per-token hard caps. This event type must be declared now so the notification schema can accommodate it, but it cannot fire until T2.A (tenant credit pool) lands.

---

## 4. Architecture Choices

### 4.1 Compute Path

**Decision: Prometheus rules for system-wide metrics; in-process scheduler for per-token quota ratios.**

Prometheus cannot efficiently evaluate per-token quota ratios across potentially tens of thousands of tokens without a dedicated gauge per token. Pushing a `lurus_gateway_quota_used_ratio{tenant_id, token_id}` gauge from the hot path on every relay request is feasible but noisy. A background scheduler querying Postgres in batches (e.g., `SELECT id, tenant_id, used_quota, remain_quota FROM tokens WHERE unlimited_quota = false AND status = 1`) every 5 minutes is far cheaper and naturally deduplicates. This is analogous to how LiteLLM's budget scheduler works.

System-wide metrics (cost spike, latency, error rate, zero-response, cache hit) already exist as Prometheus counters and histograms and map naturally to PromQL. Operator alerts continue to route through Alertmanager.

### 4.2 Dispatch Path

**Decision: NATS publish to `LLM_EVENTS` stream; lurus-platform notification consumer handles delivery.**

Direct webhook from newhub would require newhub to maintain an HTTP client, retry budget, failure store, and unsubscribe logic. lurus-platform already owns `POST /internal/v1/notify` and consumes `LLM_EVENTS`. Routing `quota.threshold.*` events through NATS keeps newhub stateless on the delivery side and reuses the platform's existing dedup + retry + channel routing. Cost: one NATS publish per threshold crossing — negligible.

Operator alerts (Prometheus rules) continue to route to Alertmanager via the existing `newhub-alerts.yaml` mechanism. These are separate channels and must not be conflated.

### 4.3 Dedup

**Decision: Redis with TTL keyed by `{event_type}:{token_id}:{billing_period_epoch}`.**

Alert state must survive pod restart (risk #5 in §9). In-memory state is ruled out. Redis already exists in-cluster (`redis://redis:6379`, DB 0). A dedup key with TTL equal to the billing period (e.g., 30 days) ensures each threshold fires at most once per period per token without a DB write. On quota reset (top-up), the dedup key must be actively deleted so the 80% threshold can re-arm.

For Prometheus-sourced operator alerts, Alertmanager's built-in `group_wait` / `repeat_interval` provides dedup at the Alertmanager layer.

### 4.4 Subscription

**Decision: Per-user notification preferences stored in `notification_subscription` table; tenant-level defaults derived from owner-role users at dispatch time.**

Each user can independently configure which events they want, on which channel (webhook/email/in_app), and to which endpoint. The notification dispatcher (platform side) resolves `tenant_id → all users with owner role who have a subscription for this event_type` at dispatch. This avoids a separate tenant-level defaults table for now while still covering the Reseller admin use case. If per-tenant defaults become needed they can be a separate table without schema migration.

---

## 5. Default Alert Rule Pack (Prometheus YAML Draft)

> **Draft only — do not merge to production manifest until tested on STAGE.**
> Extends `deploy/grafana/newhub-alerts.yaml`. Severity convention (`page` / `ticket` / `info`) and `service: lurus-hub` label are preserved from the existing file.
>
> **Metric name note**: existing alerts.yaml references `lurus_hub_*` but the live Go code (`internal/pkg/metrics/metrics.go`) registers metrics as `lurus_gateway_*` (namespace=`lurus`, subsystem=`gateway`) and `lurus_billing_*`. The rule YAML below uses the correct registered names. The existing 11 rules reference `lurus_hub_*` — this mismatch should be reconciled in a follow-up (search-replace in alerts.yaml).

```yaml
  - name: lurus-hub-cost-spike
    interval: 60s
    rules:
      - alert: CostSpikeHourly
        expr: |
          sum(increase(lurus_gateway_quota_consumed_total[1h]))
          > 3 * avg_over_time(
              sum(increase(lurus_gateway_quota_consumed_total[1h]))[7d:1h]
            )
        for: 0m
        labels:
          severity: ticket
          service: lurus-hub
        annotations:
          summary: "hourly quota spend {{ printf \"%.0f\" $value }} > 3× 7-day hourly avg"
          description: "Possible abuse or a high-traffic spike. Check tenant breakdown in the SLO dashboard."
          runbook: "https://docs.lurus.cn/runbooks/newhub/cost-spike"

      - alert: CostSpikeDaily
        expr: |
          sum(increase(lurus_gateway_quota_consumed_total[24h]))
          > 1.5 * avg_over_time(
              sum(increase(lurus_gateway_quota_consumed_total[24h]))[7d:1d]
            )
        for: 0m
        labels:
          severity: ticket
          service: lurus-hub
        annotations:
          summary: "daily quota spend {{ printf \"%.0f\" $value }} > 150% of 7-day daily avg"
          description: "Matches Datadog LLM cost monitor default threshold. Verify it's organic growth."
          runbook: "https://docs.lurus.cn/runbooks/newhub/cost-spike"

  - name: lurus-hub-latency-p99
    interval: 30s
    rules:
      - alert: RelayP99LatencySpike
        expr: |
          histogram_quantile(0.99,
            sum(rate(lurus_gateway_relay_total_duration_seconds_bucket[10m])) by (le)
          ) > 8
        for: 10m
        labels:
          severity: ticket
          service: lurus-hub
        annotations:
          summary: "relay P99 latency {{ printf \"%.1f\" $value }}s > 8s over 10m"
          description: "P99 spike sustained. Check upstream provider health and circuit breaker state."
          runbook: "https://docs.lurus.cn/runbooks/newhub/latency"

  - name: lurus-hub-error-rate
    interval: 30s
    rules:
      - alert: RelayErrorRateSpike
        expr: |
          sum(rate(lurus_gateway_relay_requests_total{status="error"}[5m]))
          / sum(rate(lurus_gateway_relay_requests_total[5m])) > 0.05
        for: 5m
        labels:
          severity: page
          service: lurus-hub
        annotations:
          summary: "relay 5xx error rate {{ printf \"%.1f\" ($value*100) }}% > 5% over 5m"
          description: "Sustained upstream failures. Check channel health and provider status pages."
          runbook: "https://docs.lurus.cn/runbooks/newhub/error-rate"

  - name: lurus-hub-zero-response
    interval: 60s
    rules:
      - alert: ZeroResponseRateHigh
        # needs new counter: lurus_gateway_completion_empty_total — see §6
        expr: |
          sum(rate(lurus_gateway_completion_empty_total[10m]))
          / sum(rate(lurus_gateway_relay_requests_total{status="success"}[10m])) > 0.10
        for: 10m
        labels:
          severity: ticket
          service: lurus-hub
        annotations:
          summary: "empty completions {{ printf \"%.1f\" ($value*100) }}% > 10% over 10m"
          description: "High fraction of zero-token responses. Likely upstream returning empty choices[]."
          runbook: "https://docs.lurus.cn/runbooks/newhub/zero-response"

  - name: lurus-hub-cache-hit-drop
    interval: 120s
    rules:
      - alert: CacheHitRateDrop
        # lurus_gateway_cache_hits_total already exists (metrics.go CacheHits counter)
        expr: |
          (
            sum(rate(lurus_gateway_cache_hits_total{result="hit"}[1h]))
            / sum(rate(lurus_gateway_cache_hits_total[1h]))
          )
          < 0.70 * (
            avg_over_time(
              (sum(rate(lurus_gateway_cache_hits_total{result="hit"}[1h]))
               / sum(rate(lurus_gateway_cache_hits_total[1h])))[24h:1h]
            )
          )
        for: 30m
        labels:
          severity: info
          service: lurus-hub
        annotations:
          summary: "cache hit rate dropped >30% vs 24h baseline (Helicone: cache drop is revenue signal)"
          description: "Could indicate cache invalidation bug or request fingerprint churn. Check cache_type breakdown."
          runbook: "https://docs.lurus.cn/runbooks/newhub/cache"
```

---

## 6. New Metrics / Instrumentation Required

| Metric | Type | Labels | Status | Notes |
|---|---|---|---|---|
| `lurus_gateway_quota_consumed_total` | Counter | `tenant_id`, `user_id` | **Exists** as `QuotaConsumed` in metrics.go | Used for cost spike rules |
| `lurus_gateway_cache_hits_total` | Counter | `cache_type`, `result` | **Exists** as `CacheHits` in metrics.go | Used for cache hit drop rule |
| `lurus_gateway_relay_total_duration_seconds` | Histogram | `provider`, `model`, `status` | **Exists** as `RelayTotalDuration` in metrics.go | Used for P99 rule |
| `lurus_gateway_relay_requests_total` | Counter | `provider`, `model`, `status` | **Exists** as `RelayRequestsTotal` in metrics.go | Used for error rate rule |
| `lurus_gateway_completion_empty_total` | Counter | `tenant_id`, `model` | **NEW** — must add | Increment when `completion_tokens == 0` in `RecordConsumeLogParams`; check in relay post-processing |
| `lurus_gateway_quota_threshold_event_total` | Counter | `tenant_id`, `token_id`, `threshold` (50/80/100) | **NEW** — optional | Useful for dashboarding; not required for correctness since dedup lives in Redis |

**What does not need a new metric**: quota ratios for per-token threshold alerts are computed by the in-process scheduler from Postgres, not from Prometheus. This avoids a high-cardinality gauge explosion (one label set per token).

**Metric name mismatch**: the existing `newhub-alerts.yaml` references `lurus_hub_requests_total`, `lurus_hub_request_duration_seconds_bucket`, `lurus_hub_circuit_breaker_state`, `lurus_hub_billing_circuit_breaker_state`, `lurus_hub_billing_outbox_pending`, `lurus_hub_billing_outbox_failed_total`. None of these names are registered in Go code; the live names are `lurus_gateway_*` and `lurus_billing_*`. This is a pre-existing bug that will cause all 11 existing alert rules to never fire. It must be fixed before any alert work is meaningful — a one-line search-replace in the YAML, not a code change.

---

## 7. Storage Schema for Subscriptions

```sql
-- notification_subscription: per-user, per-event-type delivery preference
CREATE TABLE notification_subscription (
    id            BIGSERIAL PRIMARY KEY,
    tenant_id     VARCHAR(36)  NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id       INTEGER      NOT NULL,                    -- newhub user.id
    event_type    VARCHAR(64)  NOT NULL,                    -- e.g. quota.threshold.80
    channel       VARCHAR(16)  NOT NULL CHECK (channel IN ('webhook','email','in_app')),
    endpoint      TEXT         NOT NULL,                    -- URL or email address
    enabled       BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, user_id, event_type, channel)
);

CREATE INDEX idx_notif_sub_tenant_event ON notification_subscription (tenant_id, event_type) WHERE enabled = TRUE;
```

**Tenant-level defaults**: not a separate table. At dispatch time, the platform notification consumer resolves `tenant_id + event_type` → all enabled subscriptions for users with owner role in that tenant. This can be made explicit with a future `tenant_notification_default` table if per-tenant opt-out becomes necessary.

**Cascade on tenant delete**: `ON DELETE CASCADE` ensures stale subscriptions are cleaned up when a tenant is removed (risk #3 in §9).

---

## 8. API Contract (Sketch)

All routes require Zitadel JWT (`/api/v2/{slug}/*` auth middleware). Tenant slug in the path enforces tenant isolation at the router level.

```
GET    /api/v2/{slug}/notifications/subscriptions
       → 200 [{id, event_type, channel, endpoint, enabled, created_at}]

POST   /api/v2/{slug}/notifications/subscriptions
       body: {event_type, channel, endpoint}
       → 201 {id}
       side-effect: validate endpoint reachability (HEAD request, 5s timeout) before saving (risk #3)

PUT    /api/v2/{slug}/notifications/subscriptions/:id
       body: {endpoint?, enabled?}
       → 200 {id}

DELETE /api/v2/{slug}/notifications/subscriptions/:id
       → 204
```

**Internal dispatch bridge** (newhub → platform):

```
POST /internal/v1/notify/dispatch
     Authorization: Bearer <IDENTITY_SERVICE_INTERNAL_KEY>
     body: {
       tenant_id:  string,
       event_type: string,           // quota.threshold.80
       payload:    {token_id, token_name, threshold_pct, used_quota, remain_quota, model_name?},
       idempotency_key: string       // Redis dedup key — platform side can also dedup
     }
     → 202 Accepted (fire-and-forget)
```

This call is made by newhub's in-process threshold scheduler; the actual webhook/email delivery is handled by the platform notification consumer. Newhub does not wait for delivery confirmation.

---

## 9. Risk Register

| # | Risk | Likelihood | Mitigation |
|---|---|---|---|
| R1 | **Alert storm**: a single noisy token crosses 80% → scheduler emits event every evaluation cycle | High without dedup | Redis dedup key per `{event_type}:{token_id}:{billing_period}`. One event fires once. Alertmanager `repeat_interval: 4h` for Prometheus rules. |
| R2 | **Webhook receiver down**: newhub's HTTP client blocks on delivery, backs up scheduler | Medium | Dispatch to NATS publish only (< 1ms); delivery responsibility belongs to platform. newhub has zero synchronous webhook calls. |
| R3 | **Stale subscription**: user deleted, endpoint URL still active; deliveries go to a third party | Low | `ON DELETE CASCADE` removes subscriptions on user/tenant delete. `POST .../subscriptions` validates endpoint with a HEAD probe before saving. |
| R4 | **Tenant data leakage**: alert payload includes token key, prompt content, or cross-tenant data | Low | Payload schema is explicit and whitelisted (token_id, token_name, threshold_pct, used/remain quota integers). No key value, no prompt text, no cross-tenant fields. Schema validated in unit tests. |
| R5 | **Dedup state lost on pod restart**: 50/80/100 re-fires after every restart | Medium without Redis | Redis-backed dedup key with TTL. Pod restart does not evict Redis. If Redis itself restarts, dedup window temporarily resets — acceptable because an extra alert is annoying but not harmful. |

---

## 10. Test Plan

**Unit (no external deps)**
- Threshold evaluator: given a set of tokens with varying `used_quota/remain_quota` ratios, assert correct events emitted at exactly 50%, 80%, 100% crossings.
- Dedup window: assert that crossing 80% twice within the same billing period emits exactly one event.
- Payload schema: assert no key, no prompt content fields leak into event payload.
- `CostSpikeHourly` PromQL expression: unit-test with `promtool test rules` against synthetic time series.

**Integration (test DB + local Redis)**
- Subscription CRUD: POST creates, GET lists, PUT updates enabled flag, DELETE removes, re-POST after DELETE re-creates cleanly.
- Alert dispatch happy path: threshold evaluator fires → NATS publish recorded → platform `/internal/v1/notify` stub called with correct payload.
- Cascade delete: deleting a tenant removes all associated subscriptions.

**Load**
- 1,000 tokens across 100 tenants: threshold scheduler completes one evaluation cycle in < 10s (Postgres batch query, not N+1).
- 100 concurrent relay requests hitting quota wall simultaneously: assert exactly one `quota.threshold.100` event per token (dedup holds under concurrent write).

---

## 11. Open Questions for Anita

1. **NATS vs. direct platform HTTP for dispatch**: the ADR recommends NATS publish to `LLM_EVENTS` to reuse platform's existing consumer. If the platform team has not yet wired `LLM_EVENTS` → notification delivery (only `IDENTITY_EVENTS` and `LUCRUM_EVENTS` are confirmed), should newhub call `POST /internal/v1/notify` directly instead? Confirm which events the platform consumer currently handles.

2. **Webhook payload signing**: Stripe and GitHub sign webhook payloads with HMAC-SHA256 and expose a `X-Hub-Signature-256` header. Should newhub do the same? This requires storing a per-subscription signing secret and surfacing it to the user once on creation. Adds ~1 day of implementation but is the industry expectation for production Reseller integrations.

3. **Default subscriptions on tenant create**: should the owner user of a newly created tenant be auto-subscribed to `quota.threshold.80` and `quota.threshold.100` (opt-out model), or should subscriptions require explicit creation (opt-in)? Opt-out maximizes Reseller protection; opt-in reduces notification noise for internal/dev tenants.

4. **Billing period definition**: the dedup key TTL is set to "billing period." Newhub today has no concept of a billing period attached to a token — `expired_time` is a wall-clock expiry, not a rolling 30-day window. Does quota reset monthly? On a fixed calendar day? On top-up? The scheduler needs this to correctly compute the dedup TTL and `used_quota / (used_quota + remain_quota)` denominator.

5. **Metric name mismatch in existing newhub-alerts.yaml**: the 11 existing rules reference `lurus_hub_*` metric names; the Go code registers `lurus_gateway_*` and `lurus_billing_*`. All existing alerts are currently silently non-functional. Fixing this is a 5-minute YAML edit, but it will cause alerts that were previously always-silent to start firing in STAGE. Confirm before applying the fix in production.

---

## 12. Out of Scope (Explicit)

1. **SMS and push notifications**: carrier integration complexity and cost are not justified by the current 0-paying-tenant baseline. Revisit when `paying_tenants_M12 >= 5`.
2. **AI-judge / LLM-as-guardrail alerts**: inline LLM evaluation doubles relay latency and adds per-request cost. Competitive intel (anti-recommendation A) confirms this is disabled in production by most gateway users.
3. **Prompt-quality drift and semantic anomaly alerts**: require embedding infrastructure and a semantic similarity baseline; Tier 3 structural work (T3.A). Defer to Epic 14 (PII Audit & Compliance) or later.
4. **Custom user-defined alert expressions (PromQL or DSL)**: SaaS-tier feature; not appropriate for current internal-first deployment. Revisit post-Phase 3.
5. **Tenant-level credit pool alerts (`balance.low` full implementation)**: blocked on T2.A (tenant credit pool schema). The event type is declared in this ADR's taxonomy so the notification schema can accommodate it, but no implementation is in scope here.
6. **Inbound webhook verification / retry dashboard UI**: the delivery retry loop lives entirely in lurus-platform. Newhub is not responsible for exposing retry status or DLQ management.
7. **Per-model cost breakdown alerts**: PromQL can express this now (slice `lurus_gateway_quota_consumed_total` by model label), but per-model alert thresholds require per-model baselines that don't exist yet. Defer to Epic 13 (ClickHouse Insights Plane).
