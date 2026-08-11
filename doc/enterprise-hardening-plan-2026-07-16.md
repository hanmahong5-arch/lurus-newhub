# Enterprise Hardening Plan — 2026-07-16

Continuation of the enterprise uplift. This batch closed three verified,
high-leverage gaps; this document records what shipped and the ranked roadmap
of items that were **confirmed against HEAD but deliberately deferred** to keep
each change surgical. Every roadmap item cites evidence so it can be re-verified
before it is picked up — do not open work from this list without re-confirming
the finding still holds on the current `main`.

Leverage = how much correctness/scale/clarity one change buys, per line touched.
Effort: S ≤ half a day, M ≤ two days, L larger.

---

## Shipped this batch

| PR | Class | Change |
|----|-------|--------|
| #54 | security | SSRF gate on tenant-admin channel egress (`base_url` + proxy) at write time and at the test/fetch sinks; corrected the misleading `#nosec` on the relay path |
| #55 | observability / scale | dropped the unbounded `user_id` label from `quota_consumed_total`; added DB connection-pool telemetry (`go_sql_*`) and `panics_recovered_total{source}` |
| #56 | maintainability | single source of truth for the `schema_migrations` count (was 8 hard-coded literals); added a migration-file contiguity guard |
| #58 | security / obs | max-review self-fixes: closed SSRF bypasses in #54 (`isPrivateIP` missed `0.0.0.0`/`::`/`100.64.0.0/10`; v1 write paths + `FetchModels` unguarded; DNS fail-open; IP-whitelist-mode break; `UpdateChannelV2` lockout; bare-proxy); Grafana `user_id`→`tenant_id`; panic over-count; migration SSOT gaps |
| #59 | correctness / money | row locks silently dropped — `tx.Set("gorm:query_option","FOR UPDATE")` is a GORM v2 no-op, so `Redeem` (redemption codes) and `GetChannelForUpdate` never locked; switched to `clause.Locking`, added a hermetic DryRun regression test |
| #60 | security | **R1 shipped** (see below) — transport-layer SSRF dial guard on the default relay/egress client |

---

## Roadmap — ranked by leverage

### R1 — SSRF transport-layer dial guard (defeats DNS rebinding) · security · M · leverage 5 · ✅ SHIPPED (#60)
Shipped in `internal/app/relay_dial_guard.go`: the default relay/egress client's
`DialContext` re-resolves the destination at connection time and refuses internal
addresses (`common.IsPrivateOrInternalIP`), so a NO_PROXY-direct internal target
or an already-in-effect DNS rebind is blocked even though the relay hot path does
not re-validate per request. Proxy-aware as required: the env egress proxy(es) and
the operator worker (`system_setting.WorkerUrl`) are exempted — a dial to the
proxy/worker is the egress control point, not the relay destination. Gated on the
operator's existing `EnableSSRFProtection` policy and honors `AllowPrivateIp`, so
it enforces exactly the write-time policy at dial time and adds no new failure
mode relative to the currently-effective config.

Residuals (deliberately not band-aided): (a) explicit per-channel proxy clients
(http/socks5) are not dial-guarded — their proxy address is validated at write
time and the proxy is the egress point; (b) non-pinning resolve→dial leaves a
narrow TOCTOU window vs. a rebind landing between this resolve and the OS dial
resolve — write-time validation remains the primary gate. Behavior note for the
owner before deploy: user-configured **notification** sinks (webhook/bark/gotify)
also ride the default client, so with SSRF protection on (default) a notification
to an internal address that does NOT route through the operator worker is now
blocked; the escape hatch is `AllowPrivateIp` or routing via the worker.

### R2 — Config surface unification · maintainability · L (or S for the rule only) · leverage 3
Configuration is read three ways: the `config.Config` struct, package-level
`os.Getenv` vars in `internal/pkg/common/identity_client.go`, and inline
`os.Getenv` in middleware (`internal/adapter/middleware/oidc_auth.go`). 23 boolean
feature flags, ~130 `os.Getenv` call sites. "Where does this switch come from?"
requires searching three places. Do NOT attempt a big-bang migration; instead
adopt the rule "new config goes through `config.Config`" and fold the three
existing clusters (identity / billing / oidc) opportunistically. The S slice is
just the lint/review rule + a documented inventory.

### R3 — v1/v2 channel validation divergence · correctness · M · leverage 4 · ✅ SHIPPED (#61)
Shipped: extracted `validateChannelContent(channel, isAdd)` (settings format,
model-name length on add, VertexAI region) as the single source; v1
`validateChannel` = `validateChannelContent` + `validateChannelEgress`
(behavior-identical) and `CreateChannelV2` calls the same helper, so the v2
create path now enforces model-length + VertexAI region like v1. Scoped to
create: `UpdateChannelV2` keeps its merge + gated-egress shape (adding the
VertexAI check unconditionally on update would re-introduce the #58 lockout for
a grandfathered channel, and the region field is not updatable via v2 update).
Egress stays out of the shared helper so each caller keeps its own gate.

### R4 — Decompose the two largest hot-path functions · clarity · M · leverage 4 · ⏸ DEFERRED (poor risk/reward)
`ManageMultiKeys` (~462 lines) and `PostConsumeQuota` (~202 lines, settlement).
Re-assessed 2026-07-18: `PostConsumeQuota` threads state (`err`,
`localQuotaConsistent`, `advisory`, `totalQuota`) across its phases, so a
"mechanical" seam split is NOT low-risk — a subtle change on the settlement path
is a money bug. And the payoff a decomposition usually buys (per-seam
testability) is already realized: the money path has dedicated tests
(`post_consume_actual_cost_test.go`, `post_consume_compensation_test.go`,
`post_consume_settle_test.go`, `advisory_ledger_test.go`, `quota_consume_test.go`).
A blind refactor here is churn with regression risk for marginal clarity gain —
do it only when a real edit to these functions is needed, test-first.

### R5 — Error-response taxonomy for handlers · clarity / DX · L · leverage 3 · ⏸ DEFERRED (API-contract risk)
~1000 bare `c.JSON` sites with varying body shapes. Re-assessed 2026-07-18: this
is not just churn — changing the error body shape of live endpoints is an
API-contract change that can break existing consumers (frontend + any API
clients). Worth doing behind an explicit shape decision + a compatibility pass,
not as an incidental refactor. Introduce `common.ApiErrorCode` and adopt it for
NEW handlers first; migrate existing sites only with consumer sign-off.

### R6 — `user_cache.go` "debt" · ❌ NOT REAL (roadmap misread, corrected 2026-07-18)
Re-verified: the "~15 TODOs" in `internal/adapter/repo/user_cache.go` are all
`context.TODO()` — the Go placeholder-context idiom passed to Redis calls, NOT
`// TODO` work items. The original grep matched the substring. There is no cache
consistency/invalidation debt backlog here. Threading a real request-scoped
context through these helpers (to propagate cancellation to Redis) is a broad
signature change across many call sites for low value; not worth it. Item closed.

### D5 — Data-analytics depth · ✅ AT CEILING (verified 2026-07-18)
The uplift plan's R5.1 (per-model/per-tenant p50/p95 latency + error rate) is
already shipped: `repo.GetModelPerformance` / `GetModelPerformanceV2` returns
`P50LatencyMs`, `P95LatencyMs`, `ErrorRate`, request/error counts, per-model and
per-tenant. The only theoretical gap is TTFT (time-to-first-token), which needs
relay-layer streaming instrumentation + a migration — a feature, not a hardening
tweak — and p50/p95 total latency is the standard SLO metric. Do not manufacture
a half-measured TTFT to pad the score.

---

## Loop status (2026-07-18) — code-controllable ceiling reached

Score-driven iteration to date: `#54–#61` shipped the high-leverage
code-controllable work. Composite enterprise-readiness ≈ **7.2 → ~9.2**. Every
remaining roadmap item has been re-verified against HEAD: R1/R3 shipped, D5 at
ceiling, R6 not real, R4/R5 are risky/contract-changing and deliberately
deferred. The **code-only ceiling is ~9.5** — the residual gap to 10 is
owner-gated (D4 backup/PG-HA/CD, D6 PROD-ization), below. Pushing the code
further would be metric-gaming, not product improvement.

---

## Loop status update (2026-08-07) — the ceiling claim was about *this roadmap*, not the codebase

The 2026-07-18 note above is still accurate for the R1–R6/D5 roadmap, but it
was NOT a statement that the code had no defects left. A separate source — the
coverage loop of 2026-07-25 — had produced `doc/prod-defect-findings-2026-07-25.md`,
33 reported production defects that were never triaged into this plan (the
report itself sat uncommitted in a worktree for 13 days).

Closed in PR #70: all 33 re-verified against HEAD by symbol (not by the
report's stale line numbers) — **29 confirmed and fixed, 4 were misreports**
(passkey `Origins` is never consumed by auth code; volcengine audio header;
`TASK_PRICE_PATCH`; redemption `TenantId`). Highest-severity three: a missing
tenant guard on `RevokeProvisionedKey` (cross-tenant token revocation), the
clear-before-parse pattern in five `ratio_setting.Update*ByJSONString`
functions (one malformed admin JSON = platform-wide billing failure), and
dropped write errors in `repo.UpdateOption`.

82 regression tests added; each fix was checked by reverting its production
file and confirming the test goes red (30/30). PR #70 CI 13/13 green including
`-race` and pg-integration. The same PR bumps `golang.org/x/text` to 0.39.0,
clearing the Trivy HIGH (CVE-2026-56852) that had been failing the image build
on every PR.

Method note for the next pass: a "no remaining work" conclusion is only as
broad as the inventory it was drawn from. Before declaring a ceiling, list the
defect sources that exist (audit docs, coverage-loop findings, open PRs) and
say which ones were checked.

---

## Owner-gated (deploy / promote) — unchanged from the promote runbook

See `doc/stage-prod-promote-runbook.md`. Summary: STAGE→PROD promote (R1 is a
fresh deploy, not a DNS cutover); S1 cash-path arming (platform-core redeploy +
mint newhub key + real fund E2E); OIDC SSO enablement (register the newhub
client with the IdP); platform backup PRs; upstream fast-forward. These move
production state or shared-owner repos and are held for the owner.

---

## Deferred deliberately (not defects)

- Metric-schema change in #55 (`quota_consumed_total` lost `user_id`) is
  intentional and correct; any dashboard grouping by user must move that
  breakdown to the consumption log / audit trail.
- The 23 feature flags are a real cognitive cost but not individually wrong;
  R2 addresses the surface, not the flags.
