# Cycle 18 — trends, reliability, and the scorecard (2026-09-27)

## Method

Two orchestrated passes over a clean worktree of main `a84c6797`, the
operator having delegated the decisions:

1. **Research (26 agents).** A competitor/trend sweep (OpenRouter, LiteLLM,
   Portkey, Cloudflare AI Gateway, Helicone, Bifrost, Kong, upstream New API;
   changes since 2026-06, each capability graded have/partial/missing against
   this code), a scorecard built only from what CI actually enforces, and a
   refute-first pass over the five findings the 09-26 review had left
   unverified plus a money-path and a console audit. Every defect candidate
   then faced two independent skeptics (correctness, reproducibility); it
   survived only if both failed to refute it. 13 candidates → 10 verified →
   **7 confirmed, 3 refuted**.
2. **Implementation (18 agents).** Eight lanes with disjoint file ownership,
   each test-first (failing test pasted before the fix), each checked by an
   independent verifier that re-ran the tests, reverted the production diff
   to confirm the new tests go red, and re-applied it. One lane needed a
   repair round (L1: a plural i18n key rendered raw on the failed-read badge).

## Refuted (not planned, recorded so nobody re-finds them)

| Claim | Why it is not a defect |
|---|---|
| A hub-side pre-auth timeout strands a 1 h wallet hold with no trace | The platform rolls the hold back when the request context is cancelled, and its 5-minute reconciliation expires anything left; the reference id is not logged, which is a logging gap, not stranded money |
| The OIDC verifier is RSA-only while the IdP advertises EdDSA/ES* | Accurate, and a test pins the allow-list on purpose; the live JWKS is RS256-only. Latent until the IdP rotates to another alg |
| The entitlement deny verdict is cached 5 min with no invalidation | The deny branch is unreachable: the platform never emits the `quota_remaining` key that would produce it |

## Confirmed and fixed

| # | Defect | Fix | Test that pins it |
|---|---|---|---|
| L1 | Seven console pages rendered a failed read as an **empty success** (a 500 on tokens said "No tokens yet. Create one"); the gate itself listed them as `KNOWN_UNFIXED` | One shared read hook `hooks/common/useTenantRead` (status vocabulary from `classifyLoad`, stale-response guard, `retry()` that settles) + `HfLoadError` on tasks/channel/token/cmdk/models/pricing/redemption | `failed_read_is_not_empty.test.jsx` KNOWN_UNFIXED 7 → `{}`; 8 hook tests; +9 page failure-branch tests |
| L2 | **Every** platform pre-auth failure became 402 "insufficient balance, please top up" — timeouts, breaker-open, `wallet_frozen` alike. A funded customer was told to pay on our outage; SDKs treat 402 as terminal; no error-log row; `relay_errors_total` filed the outage under `insufficient_quota` | `preAuthFailure(err)`: `ErrInsufficientBalance` → 402 + top-up (unchanged); `PlatformRejectedError` → 402 named after the platform's reason, no top-up link, error-log row; anything else → **503 `billing_unavailable` + Retry-After 5 s**. Three "money lost" paths now increment `lurus_billing_money_path_lost_total{stage}` with a structured log; settle 400 is typed `PlatformRejectedError`; the hold's two exits are `abandonPreAuth` / `settleOrPark` (source gate: `PlatformPreAuthID = 0` nowhere else); the Phase-5 wallet settlement moved verbatim to `quota_settle.go` (quota.go 1389 → 1253) | `pre_consume_platform_failure_test.go` (4 shapes), `billing_money_lost_test.go`, `settle_rejected_test.go`; the whole `quota_conservation` series unchanged and green |
| L3 | The newapi→hub bridge replays **every** hub 401 on newapi, but hub answered 401 for disabled keys, expired keys and DB lookup failures too — a key disabled in hub kept working through the old gateway, and one DB blip moved a request and its billing there silently | Lookup failure → **503 `query_data_error` + Retry-After 5** (and audited as `token_lookup_failed`, not as an auth failure); every 401 carries **`X-Lurus-Token-State: unknown\|disabled\|expired`**, the 503 `lookup_failed`; expiry has its own code `token_expired`; header documented in relay.json and CORS-exposed. The bridge change (`map $upstream_http_x_lurus_token_state`) is written up in the integration guide for the newapi repo's owner | `token_auth_state_test.go` (4), `token_validate_sentinel_test.go` (4); `TestL3TokenAuth_Disabled_Still401` still green |
| L4 | Dashboard "recent requests" showed **every timestamp in January 1970** (unix seconds fed to a millisecond formatter); seven private date formatters, Audit in UTC and Log/Authz local | `helpers/formatting` is the single source (`formatTimeUTC`, `formatShortTs`, `formatClockTime(x,{ms:false})`); six private formatters deleted; `time_single_source.test.js` forbids new ones and pins the 8 remaining inline `toLocale*/toISOString` sites in six files not in this lane as a two-sided table | Dashboard test feeds `created_at: 1750000000` and asserts no 1970; Audit/Log render `2025-06-15 15:06:40` (UTC) vs local `23:06:40` |
| L5 | Invoice months were **re-priced at today's exchange rate** for every row without a wallet charge (credit-pool, local-quota, pre-041 rows — most of production) | `logs.priced_cny4` (migration **042**, ledger-reserved): the CNY value of the row's quota at record time, written by `governance.EnrichLogParams` for every consume row; invoices read charged → priced → today's derivation only for pre-042 rows, and say `estimated: true` when they had to | `TestInvoiceAmount_PoolFundedRowIsRateStable`: 7.3 → 6.5 edit, amount stays 1.0658 |
| L6 | Per-channel **SOCKS5 proxies dialled with no timeout** and ignored ctx; a proxy that accepts but never answers hung the relay goroutine (RELAY_TIMEOUT is unset in production) | `newSocksDialContext`: `net.Dialer{Timeout: RelayDialTimeout}` for the TCP leg, `context.WithTimeout` around the handshake, both transport sites share it | `http_client_socks_dial_test.go`: silent listener, 300 ms bound observed at both sites, with and without a ctx deadline |
| L7 | Gates that did not exist: coverage gate on only 3 packages, no whole-module floor, no vulnerability scan, e2e flaky count unread, no `.skip` ratchet, **zero alarms on relay latency or error rate** | Coverage gates 3 → 5 (`pkg/common` 82, `pkg/metrics` 72) + whole-module floor 79 (measured 82.5); `govulncheck` job (non-blocking on day one: 25 reachable advisories, 20 of them stdlib fixed in a newer toolchain — flip to blocking in the PR that gets it to zero); e2e `stats.flaky > 1` fails the job; `skip_ratchet.test.js` CEILING 93; alarms 31 → **38** (relay error-rate 10 m/1 h, overhead slow-share, channel-select slow-share, plus L2's money-lost) | `coverage_honesty_test.go`, `netdata_alarm_series_test` (README count = conf count = 38) |
| L8 | (trend, missing) No way for a regulated customer to pin a request to a region or to zero-retention channels — OpenRouter shipped in-region routing this month | Channel setting `region` / `data_collection: deny`; request body `provider.region` / `provider.data_collection` (parsed where `prompt_cache_key` already is); `GetRandomSatisfiedChannelWhere(pred)` with a DB-path twin; a session-affinity pin that fails the filter is ignored; a miss is `ErrNoChannelSatisfiesPredicate` → the existing 503 with the filter named in the message | repo (memory + DB path), app (region/zdr/both, miss, pin override), middleware (5 body shapes) |

## Scorecard

| Dimension | Before (main `a84c6797`) | After this cycle |
|---|---|---|
| Go coverage gates | 3 packages (app 87 / repo 77 / handler 73); no module floor | 5 packages (+ common 82, metrics 72) + whole-module floor 79 |
| Web coverage | thresholds st 60 / br 54 / fn 55 / ln 60 | unchanged thresholds; +34 tests (hook, failure branches, time source) — raise in the PR that measures with coverage on |
| Read-failure gate | 7 pages `KNOWN_UNFIXED` | 0 |
| Empty `catch` in pages/v2 | 52 | 46 (the rest are write-path handlers whose toast the interceptor owns) |
| Size ratchets (rows lowered) | — | quota.go 1389→1253, auth.go 807→732, repo/log.go 1055→1033, Channel 1908→1904, Token 1639→1632, Pricing 805→804, Dashboard 1190→1179, Log 1630→1623, Settings 1909→1889 |
| Alarms | 31; 0 on relay latency/error rate | 38 |
| Security gates | gosec, golangci debt ceiling, bun audit, Trivy | + govulncheck (advisory until zero) |
| Flaky / skip | no flaky metric; 93 web skips unbounded | e2e flaky > 1 fails; skip ceiling 93, ratchets down only |
| Contract | relay.json 47 ops | + 2 error codes (`billing_unavailable`, `token_expired`), `X-Lurus-Token-State`, `provider` request object; api-v2 invoices `estimated` |
| Private date formatters | 7 | 0 (+ 8 inline sites in 6 files pinned for their owners) |

## Deliberately not in this cycle

- **Settle idempotency** — the platform locks only `status='active'` and
  ignores the idempotency key; a settle that timed out after committing
  becomes a phantom "permanently failed" outbox row. Platform repo.
- **Bridge replay only on `unknown`** — `deploy/hub-bridge.yaml` lives in the
  newapi repo; L3 emits the header it needs.
- **Alarms on the R6 host / migration 042 on the R6 databases** — operator
  steps; the conf is verified by the series test, the migration runs at boot.
- **Dialog consolidation (15 hand-rolled overlays → one accessible dialog)**,
  the pure-move split of Channel/Token/Flows/Users, JSON-schema structured
  output on Claude channels, Responses-over-WebSocket, spend limits by
  model/metadata, project budget caps, multi-threshold budget alerts — next
  cycle's candidates; each collides with files this cycle rewrote.
- `calculateDisplayAmount` duplicate + `cny_quota_boundary` blind spot — S,
  first add-on when a lane finishes early next time.

## Operator follow-ups

1. newapi repo: `map $upstream_http_x_lurus_token_state $replay { unknown 1; default 0; }` in the bridge, replay only when set.
2. R6 host: install the updated `newhub.conf` (38 alarms) via `scripts/install-netdata-alarms.sh`.
3. After deploy: `schema_migrations` max = 042 on `newhub` and `newhub_uat`; `logs.priced_cny4 bigint default 0`.
4. Bump the Go toolchain and the four modules govulncheck names, then flip the job to blocking.
