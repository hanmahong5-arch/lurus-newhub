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

---

## Roadmap — ranked by leverage

### R1 — SSRF transport-layer dial guard (defeats DNS rebinding) · security · M · leverage 5
PR #54 validates channel egress at write time and at the admin test/fetch
sinks, but the relay hot path (`internal/adapter/provider/api_request.go:295`)
does not re-resolve per request, so a TTL-based DNS-rebinding target that passes
write-time validation can still be reached at relay time. The robust fix is a
custom `DialContext` on the shared outbound client that validates the *resolved*
IP at connection time — this protects every current and future sink at the
transport layer and cannot be forgotten. Care required: the K8s deployment uses
`ProxyFromEnvironment` with an egress proxy, so a naive dial-time IP filter would
validate the proxy's own (private) IP; the guard must be proxy-aware
(validate the CONNECT target, or only when no proxy applies per `NO_PROXY`).

### R2 — Config surface unification · maintainability · L (or S for the rule only) · leverage 3
Configuration is read three ways: the `config.Config` struct, package-level
`os.Getenv` vars in `internal/pkg/common/identity_client.go`, and inline
`os.Getenv` in middleware (`internal/adapter/middleware/oidc_auth.go`). 23 boolean
feature flags, ~130 `os.Getenv` call sites. "Where does this switch come from?"
requires searching three places. Do NOT attempt a big-bang migration; instead
adopt the rule "new config goes through `config.Config`" and fold the three
existing clusters (identity / billing / oidc) opportunistically. The S slice is
just the lint/review rule + a documented inventory.

### R3 — v1/v2 channel validation divergence · correctness · M · leverage 4
`CreateChannelV2` (`internal/adapter/handler/v2_channel.go`) inlines its own
required-field/name-length checks and never calls the v1 `validateChannel`
(`internal/adapter/handler/channel.go`), whose model-name and VertexAI-region
rules the v2 path therefore lacks — the two paths already disagree on what a
valid channel is. Have the v2 handler call the shared `validateChannel` and only
wrap tenant-scope/RBAC differences around it, so a validation fix lands in one
place. (The SSRF egress gate added in #54 is already shared via
`validateChannelEgress` — use the same pattern.)

### R4 — Decompose the two largest hot-path functions · clarity · M · leverage 4
`ManageMultiKeys` (`internal/adapter/handler/channel.go`, ~462 lines) mixes
add/delete/update/reorder with batch validation and cache refresh; its branches
cannot be unit-tested in isolation. `PostConsumeQuota`
(`internal/app/quota.go`, ~202 lines) is the settlement path — changing one
branch means reading the whole function. Split each along its natural seams
(per-action for the former, compute → persist → notify → audit for the latter)
with a unit test per seam. Highest editing-risk code, so highest clarity payoff.

### R5 — Error-response taxonomy for handlers · clarity / DX · L · leverage 3
Handlers emit ~1000 bare `c.JSON(http.Status…)` responses across ~87 files with
inconsistent body shapes (`success` / `message` / `error` keys vary). The relay
layer already proves a single error constructor works
(`internal/app/relay/helper`). Add `common.ApiErrorCode(c, status, code, msg)`
as the standard shape, lint new bare 4xx/5xx `c.JSON`, and migrate existing
sites in batches. Improves frontend consumption and support triage.

### R6 — `user_cache.go` debt burndown · reliability · S (inventory) · leverage 2
`internal/adapter/repo/user_cache.go` concentrates ~15 TODOs on cache
consistency/invalidation — the highest-risk file to change and the most
under-documented. Convert the TODOs into tracked tickets and decide each
(fix vs. accept). Most provider-adapter TODOs elsewhere are vendor-API
limitations and are low value; do not sweep them in.

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
