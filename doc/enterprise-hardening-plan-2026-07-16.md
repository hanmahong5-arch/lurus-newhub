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
