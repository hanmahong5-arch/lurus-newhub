# Lurus AI Gateway for Enterprise — Joint Positioning, Contract SLA & Onboarding

Status: 2026-06-12 · Owner: newhub session · Scope: **product-layer integration only**
(no platform code changes; this is narrative + contract + runbook, not a code merge).

This document packages **newhub** (multi-tenant LLM gateway + usage analytics) and
**lurus-platform** (identity / billing / RLS) into one enterprise story without
merging the codebases. It is the "integrate at the product layer, not the code or
deploy layer" conclusion made concrete.

---

## 1. The product (what we sell as one thing)

> **Lurus AI Gateway for Enterprise** — a multi-tenant LLM gateway with per-tenant
> model pools, quota enforcement, real-time usage analytics, and unified
> identity + wallet billing. One activation, one bill, 30+ upstream providers,
> tenant-isolated logs/keys/quota.

| Capability | Delivered by | Customer-visible value |
|---|---|---|
| Multi-tenant relay (Channel/Token/quota/log isolation) | newhub | One gateway, many teams, no cross-tenant leak |
| 30+ provider adapters + per-tenant model pools | newhub | Vendor-neutral; switch/mix models without re-integrating |
| Real-time usage analytics + cost attribution | newhub | Per-product/per-tenant spend visibility |
| Identity (Zitadel OIDC, Passkey) | platform | SSO, enterprise auth |
| Unified wallet + metered billing | platform ↔ newhub seam | One balance, one invoice across products |
| RLS / data-isolation guarantees | platform | Compliance story (PIPL erasure, tenancy) |

## 2. Why two codebases, one product (the integration boundary)

Integrate the **narrative, contract, and onboarding** — never the repos. Three
durable reasons (these are architecture invariants, not preferences):

1. **Upstream contract.** newhub tracks New API upstream cherry-picks (monthly +
   security-immediately). Merging billing/identity into this fork would turn every
   upstream merge into cross-product surgery. The PG-only convergence and the
   `UPSTREAM-MERGE NOTE` markers exist to protect exactly this seam.
2. **Fault isolation (reversed-dependency).** When platform is unreachable, newhub
   *degrades* (billing breaker → cached-balance admit, P1-2) rather than failing.
   A same-process merge destroys that bulkhead — the gateway would die with the
   billing service.
3. **Constitution.** Root governance: "independent project, independent Pod;
   no micro-service splitting *within* a business." The two are P0 same-group
   collaborators, not one body.

## 3. Contract SLA table (the seams that make it one product)

Contract source of truth: `lurus/doc/coord/contracts.md`. Each seam below is a
real, shipped integration point with a proposed SLO. **SLOs marked _proposed_
become committed only once P1-5 (backups) + alert paging land — see §5.**

| Seam | Mechanism | Direction | Proposed SLO | Degrade behavior |
|---|---|---|---|---|
| Usage metering | gRPC `ReportUsage` (+ transactional outbox) | newhub → platform | ≥99.9% eventually-delivered (outbox, SKIP LOCKED + idempotent unique idx) | Outbox retries; never blocks relay |
| Wallet pre-auth/settle | gRPC `WalletPreAuthorize`/`Settle`/`Release` + breaker | newhub → platform | pre-auth p95 < 250ms; breaker-open ⇒ degrade | **P1-2**: breaker-open admits trusted balance from cache (bounded per-tenant cap), reconcile via legacy debit |
| Internal admin/identity | `/internal/*` + bearer `INTERNAL_API_KEY` + scopes | platform → newhub | per-key rate-limited (**P0-3**); 200 p99 < 100ms | 429 + Retry-After on abuse; scope-gated |
| Activation / top-up | activation code → newapi `POST /api/user/topup` | platform → newhub | idempotent (order_id) | Safe to retry |
| Account erasure (PIPL §47) | `/internal/v1/privacy/erase` (idempotent, unique event_id) | platform → newhub | processed ≤ cooling-off window | Idempotent replay |

### Relay SLOs (newhub-owned, `doc/slo-relay.md`)

| SLI | Target | Burn-rate alerting |
|---|---|---|
| Relay end-to-end success | **> 99.5%** | **P0-4** multi-window multi-burn (1h∧5m fast, 6h∧30m slow) |
| newhub overhead P99 | **< 50ms** | **P0-4** fast-burn page |
| Channel-select P99 | < 10ms | dashboard |

## 4. Unified onboarding narrative (one flow, two services)

1. Enterprise tenant provisioned in platform (Zitadel org + wallet).
2. Identity linked into newhub via `/internal/user/provision` (idempotent,
   fail-open if platform momentarily unresolvable — **P0-1 fixed the tenant-scoped
   500 here**).
3. Tenant funds the unified wallet (activation code / top-up).
4. Per-tenant model pool + tokens issued (provisioning API, rate-limited **P0-3**).
5. Relay traffic flows; spend meters to the one wallet (gRPC + outbox); analytics
   and burn-rate SLOs (**P0-4**) observe it.
6. Billing/identity outage ⇒ gateway **degrades, not dies** (**P1-2** + shallow
   liveness / deep readiness **P0-2**).

## 5. Reliability posture — honest tiering

**Today: serious pre-production**, one tier below "sign an enterprise SLA":
- Run plane: 3 static replicas on a single-node cluster — no HPA, no PDB, no topology
  spread (recorded as a decision in `deploy/k8s/r6-stage/README.md`, not drift); DB-lease
  leader election for master-only loops; preStop drain with `terminationGracePeriodSeconds`
  above the graceful-shutdown budget; shallow liveness / deep readiness split.
- Observability: Prometheus-format `/metrics` (25+ RED/USE series, 4 relay SLIs) scraped by
  the host Netdata. Alert rules live in `deploy/r6-host-netdata/health.d/`; the in-repo
  `PrometheusRule` is not deployed (no Prometheus operator on R6). Delivery of an alarm to
  a human is still owner-gated (no webhook target configured).
- Billing is hard to lose: transactional outbox (SKIP LOCKED + idempotent) + breaker +
  cached-balance degrade.
- Dependency hangs bounded: PG `statement_timeout`, Meilisearch and Tavily client timeouts;
  Redis, platform gRPC and NATS budgets are cycle-12 work
  (`_bmad-output/planning-artifacts/cycle12-industrial-grade-2026-09-19.md`).
- Backups: daily `pg_dump` CronJob to a PVC, host-cron off-site rsync, weekly restore drill
  (`doc/runbook/database.md`, `doc/runbook/pg-restore.md`); no WAL archiving, so RPO is up
  to 24 h.

**Gap to "SLA-capable" (minimal honest set), all owner-gated:**
1. RPO: a daily dump only; WAL archiving / PITR is a decision not yet taken (O-pitr).
2. Alert delivery: alarms exist in git, several are not loaded on the host and no webhook
   target is set — nobody is paged today.
3. Traffic: 30-day production traffic is our own probes (14 human calls); no measured
   capacity baseline yet.
4. One node: a node loss is a full outage; there is no second node to spread onto.

Execution-ready details + diffs: `doc/runbook/industrial-readiness-gated-actions.md`.

## 6. What we deliberately do NOT do pre-PMF (anti-over-engineering)

OTel tracing activation (no real traffic) · automated secret rotation (manual
kubectl suffices; only the *shared* `IDENTITY_SESSION_SECRET` is worth flagging) ·
an HPA (nothing to scale; 3 static replicas, no HPA) · PG full circuit
breaker (in-cluster timeout + `statement_timeout` suffices) · `hub.lurus.cn` DNS /
R1 launch (owner-gated on PMF; runbook-ready only).
