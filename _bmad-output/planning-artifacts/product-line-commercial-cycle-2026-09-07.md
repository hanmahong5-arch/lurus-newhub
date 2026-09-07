# Product-Line Commercial Grade — Cycle 2026-09-07

Baseline HEAD: `43ba242a` (main, clean apart from five untracked `internal/app/*coverage*_test.go`
and `internal/app/openrouter_sync/coverage_extra_test.go` files owned by another workstream —
off limits for every lane: never edit, stage, delete or cite them).

Scope rules for this cycle:

- Four lanes, implemented **sequentially by separate agents on one shared working tree**.
- Lane file sets are **disjoint at file granularity** (verified in §4.0). A lane may *run* tests in
  a package another lane edits; it may not *edit* a file another lane owns.
- Every lane's file list names the test file(s) in every package it changes. A lane whose only
  money-moving or contract-changing edit has no test in its own package is invalid by definition.
- No commits, no branches, no PRs, no production or remote-host access from any lane. No full-suite
  `go test ./...`, no lint run — the integrator does both once at the end.
- `-race` is unavailable on the build host (no cgo). Package tests run as
  `go test -count=1 -p 2 <packages>`. Frontend: `cd web && bun run test -- <path>`.
- Migration ID 033 (`migrations/033_logs_source_product.sql`) is reserved in the root ledger but
  **not used this cycle** (§4.3 explains why the JSON expression is enough at 24k rows).

Live facts (production DB, read-only, measured 2026-09-07 — treated as ground truth, not
re-measured): all 24 365 log rows belong to tenant `default`; tenants `switch`, `lurus-default`,
`probe-invite` have 0 users / 0 tokens / 0 logs; `credit_pool_fund_events` has exactly 1 row
(2026-08-23); `privacy_erasure_requests` 0; `provisioned_redemption_batches` 0; `releases` 0;
options `switch_app.*` 0 rows; the `logs` table has **no product column** (attribution lives in
`other` JSON as `source_product`). Platform-core has `NEWHUB_BASE_URL`/`NEWHUB_API_KEY` configured;
the internal key `platform-core` carries scopes `balance:write, user:delete, provisioning`.

## 1. Scorecard

| Dimension | Score now | Rationale (condensed) |
|---|---|---|
| Product line (cross-product integration) | 4.5 | The attribution primitive exists and reaches money and the log row, but no sibling product can *consume* it: no tenant-scoped filter/breakdown by product, no metric label, MJ/Task paths fold to the default, and the header→row→wallet chain has zero assertions above the pure resolver. Platform seams are correct but rejections are invisible. |
| Billing correctness | 5.5 | Settlement core is solid and mutation-locked. Three silent ledger faults: tenant credit-pool topup debits the platform wallet at 1 LB = 1000 quota while every other money path uses 1 LB = 500 000 (500× divergence, live path with one historical row); OIDC login never persists `users.lurus_account_id` so browser and API-key spend go to different ledgers; product breakdown is root-only and contracts.md names the wrong default id. |
| Tenant isolation | 6.5 | URL-tenant boundary is real and tested. Two live holes confirmed by probe: console sessions never re-check status/role against the DB (disabled or demoted user keeps access for the 90-day cookie); platform session-bearer resolution is pinned to tenant `default`, so any non-default tenant user is rejected with a misleading "access token invalid". Internal-key tenant denials leave no audit row and no metric. |
| Observability | 5.0 | Relay error taxonomy, honest health body and migration drift are above average. Zero per-product series; alert rules exist nowhere in git; leader election and the four leader-gated compliance jobs emit nothing; client-abandoned streams are neither counted nor billed. |
| Contract stability | 5.5 | Native error envelopes per wire are strong. Gateway-originated OpenAI errors stamp a foreign vendor `type` with empty `code`; Anthropic errors carry no `type`; the OpenAPI file documents three phantom billing routes and omits every switch/lutu/internal route; no versioning/deprecation headers. |
| Console honesty | 5.0 | Structure is honest (WIP banners, role-gated nav, i18n parity), but the quickstart and Chat page hardcode models production does not serve, Subscription fabricates SLA/retention/support tiers, and tenant invite issuance has a backend but no console surface. |
| Test integrity | 6.0 | CI anti-hollow machinery is unusually good. The chain a sibling team most needs — `X-Lurus-Product` → `RelayInfo.SourceProduct` → log row / wallet — is coverage-green and assertion-hollow at both hops; the erasure cascade test is Short-gated while every CI job passes `-short`; several permanently dead tests; the switch publish path is tested by poking `OptionMap` directly. |
| Operability | 5.5 | Delivery pipeline is strong. The last mile for a buyer is broken: the product integration guide is fiction from 2026-02, a new tenant's first relay is a guaranteed 402 (`pool_not_configured`), emergency scripts verify the UAT host and omit `OIDC_CLIENT_ID`, and platform→newhub internal traffic has no metric or audit. |
| Security / compliance | 6.5 | Perimeter is enterprise-shaped (hashed scoped keys, fail-closed whitelist, JWKS/PKCE, SSRF gate, hash-chained audit, resumable erasure). Held down by the stale-session hole, the tenant-unbounded/unaudited `/internal/balance/topup`, no pre-auth per-IP throttle on `/v1`, and TOTP secrets surviving an attested erasure. |

## 2. Verification performed before ranking

Every claim a lane depends on was re-read against HEAD `43ba242a` rather than trusted from the
input assessments. Confirmed by direct read:

- `internal/adapter/handler/relay.go:270` — `relayInfo.SourceProduct = ratio_setting.ResolveSourceProduct(c.GetHeader(ratio_setting.SourceProductHeader))`
  is the **only** writer of `SourceProduct`; the MJ path (`relay.go:733`) and Task path
  (`relay.go:803`) call `relaycommon.GenRelayInfo` and never set it.
- `internal/adapter/provider/common/relay_info.go:412` — `genBaseRelayInfo(c, request)` is the
  common constructor every `GenRelayInfo*` variant goes through; `relay_info.go:113` declares
  `SourceProduct string`.
- `internal/app/governance/governance.go:106-110` — `EnrichLogParams` writes
  `params.Other["source_product"]` with the `DefaultSourceProduct` fallback. `enrich_test.go`
  (`TestEnrichLogParams_*` at :25/:33/:51) never asserts on it.
- `internal/adapter/handler/relay.go:696-729` — `recordRelayErrorLog` builds `other` with
  `request_path/error_type/error_code/status_code/channel_*/relay_mode/upstream_model/admin_info`
  and **no** `source_product`; failed requests are un-attributable.
- `internal/adapter/handler/v2_log_stat.go:87-175` — `serveLogStatV2` hand-writes its aggregates;
  filters are `type/model_name/token_name/start_time/end_time/project_id` only (`:89-98`, `:113`,
  `:162`). `v2_log.go:117/:199` and `entity/log.go:85-104` (`LogQueryParams`) have no product
  field; `repo/log.go:782/:809/:836/:859` apply model/project filters only.
- `internal/adapter/repo/savings.go:63-77` — `jsonSourceProductExpr()` already exists (PG:
  `NULLIF(other, '')::jsonb ->> 'source_product'`, SQLite: `json_extract`), used by
  `GetSpendByProduct` (`:45-52`), whose only consumer is the root-only governance savings handler.
- `internal/pkg/setting/ratio_setting/model_equivalence.go:23` — `DefaultSourceProduct = "llm-api"`;
  allow-list at `:30-39` = `{llm-api, kova, lutu, lucrum, switch, creator, memorus, tally}`.
  Root `doc/coord/contracts.md:56` states the default is `lurus-api` — **doc/code mismatch**.
- `internal/adapter/handler/tenant_credit_pool.go:281` —
  `walletAmount := float64(req.Amount) / 1000.0 // 1 LB ≈ 1000 quota units, matches existing relay accounting`.
  The comment is false: `internal/app/quota.go:985` uses `float64(totalQuota) / common.QuotaPerUnit`
  (500 000); `internal/pkg/currency/currency.go:30-42` defines `LucToLut()` from `QuotaPerUnit`.
  The revert path at `tenant_credit_pool.go:341` credits the same wrong `walletAmount`. Seams
  `debitWallet`/`creditWallet` are package vars (`tenant_credit_pool.go:23-24`); the existing
  `stubWalletSeams` (`tenant_credit_pool_stranded_test.go:30-48`) counts calls and **discards the
  amount**.
- `internal/adapter/middleware/auth.go:36-41` — `authHelper` reads `role`/`status` from the session;
  the disabled check at `:172` and the role check at `:189` use those frozen values. The DB-backed
  `repo.GetUserCache(userId)` at `:235` is called **after** both checks and only `TenantId` is used.
  `repo/user_cache.go:59-105` populates `Status` from the DB on cache miss (`:34`), so the fresh
  status is one field away. `entity/user.go:102-116` `UserBase` has **no `Role`**.
- `internal/adapter/repo/user.go:379-384` — `DisableUserById` updates the DB only; no
  `invalidateUserCache` (contrast `AdminDeleteUser` `:458`, `AdminUpdateUser` `:443` which refresh
  the cache).
- `internal/adapter/middleware/auth.go:96` — `repo.GetUserByIDPSubject(idMapping.ZitadelSub, "default")`;
  `repo/user_mapping.go:20-23` filters `tenant_id = ?` — non-default tenant users are unresolvable
  via a platform session bearer.
- `internal/adapter/middleware/internal_api_auth.go:11-31` (`InternalApiAuth`) and `:46-75`
  (`RequireScope`) reject with JSON only: no `SysLog`, no metric. `internal/pkg/metrics/metrics.go`
  has no `internal_api*` series (full `Name:` inventory read).
- Tenant whitelist guard `repo.InternalKeyAllowedForTenant` (`repo/internal_api_key.go:172`) is
  inlined six times: `provisioning.go:76/:193/:328`, `internal_provisioned_redemptions.go:158/:283`,
  `internal_credit_pool_fund.go:99`. `governance/audit_action.go:96-97` only has
  `internal_key.tenant_granted/revoked`.
- Test helpers that the lanes reuse exist at: `relay_info_project_test.go:newRelayInfoCtx`,
  `v2_log_stat_test.go:seedStatLog`, `v2_testutil_test.go:SetupV2TestRouter`,
  `tenant_credit_pool_admin_extra_test.go:setupAdminPoolRouter`,
  `auth_helper_cover_test.go:buildRoleRouter/serveRole`, `cover_helpers_test.go:setupCoverDB`,
  `gap_r3_auth_cover_test.go:mintIdentitySessionToken` (`:139` is the default-tenant twin),
  `cov_handler-relay_edge_test.go:handlerRelaySetupDB`; miniredis pattern in
  `repo/cov_repo_token_cache_test.go`; `prometheus testutil.ToFloat64` pattern in
  `middleware/business_rate_limit_test.go`.

Not verified (kept as assessment claims, not lane preconditions): the probe results "disabled-after-
login → 200" and "tenantX bearer → access token invalid" were reported by the tenancy assessment;
the lanes' oracles re-establish them by construction (tests must be red on HEAD before the fix).

## 3. Ranked backlog

Ranking key: enterprise value ÷ effort, live defects and cross-product attribution first.
"Live" = reachable with today's routes/keys on hub.lurus.cn.

| # | Candidate | Live? | Effort | Evidence (file:line) | Disposition |
|---|---|---|---|---|---|
| 1 | billing:BILL-A credit-pool topup wallet unit (500×) | yes, money | S | `tenant_credit_pool.go:281,341` vs `quota.go:985`, `currency.go:42` | **Lane 1** |
| 2 | tenancy/security:SEC-B session status+role re-validation, cache invalidation on disable | yes, auth | M | `auth.go:39-41,172,189,235`; `user.go:379-384`; `entity/user.go:102-116` | **Lane 2** |
| 3 | tenancy:AUTH-TENANT-BEARER non-default tenant bearer resolution | yes, sibling auth | S | `auth.go:96`; `user_mapping.go:20-23` | **Lane 2** (same file) |
| 4 | product_line:XP-1 + console:UX-2 (API half) + testing:TI-1 — attribution consumable + chain locked | yes, attribution | M | `relay.go:270,733,803`; `v2_log_stat.go:89-98`; `entity/log.go:85-104`; `governance.go:106-110`; `relay.go:696-729` | **Lane 3** |
| 5 | product_line:XP-2 + tenancy:IK-TENANT-DENY-AUDIT — internal seam visibility | yes, dormant seams | S | `internal_api_auth.go:16,27,51,71`; six inline guards; `metrics.go` (no series) | **Lane 4** |
| 6 | operability:OPS-SECRETS-BOOTSTRAP secrets/health-URL consistency test | DR path | S | `deploy-stage.sh:100-112`, `stage-rollback.sh:32` | deferred (next cycle #1) |
| 7 | operability:ONBOARD-402-POOL CreateTenant builds pool / warns | yes, onboarding | M | `tenant.go:129-150`; `pool_balance_check.go:79` | deferred (next cycle #2) |
| 8 | console:UX-1 quickstart/Chat catalogue-derived model | yes, first call | S | `Dashboard/index.jsx:84`; `Chat/index.jsx:33,52` | deferred |
| 9 | observability:OBS-P2 stream end by reason+product | yes, unbilled | S | `stream_scanner.go:357-369` | deferred (relay helper package free; next cycle) |
| 10 | observability:OBS-P1 product label on relay metrics | attribution | M | `relay.go:420`; `repo/log.go:450-456` | deferred — conflicts with Lane 3 files (`relay.go`, `repo/log.go`); land after Lane 3 |
| 11 | contract:AC-2 native error taxonomy | integration | M | `middleware/utils.go:29`; `types/error.go:37,226-236` | deferred — needs cross-repo grep of switch/lutu for `new_api_error` first |
| 12 | security:SEC-D `/internal/balance/topup` tenant guard + audit | yes, money-in | S | `internal_api.go:364-430` | deferred — depends on Lane 4's shared guard; next cycle |
| 13 | testing:TI-2 un-dead Short-gated erasure test + dead placeholders | CI honesty | S | `privacy_erasure_test.go:119-121` | deferred |
| 14 | product_line:XP-3 / testing:TI-3 switch publish round-trip + download_url validation | seam 404 live | S/M | `switch_app_release_test.go:22-47`; `option.go:50-110` | deferred — owner has not decided to publish via hub; validation rule needs the switch client pin confirmed |
| 15 | observability:OBS-P3 leader/compliance liveness on health | compliance | S | `leader_election.go:54-75`; `health.go:19-101` | deferred |
| 16 | billing:BILL-B OIDC login persists `lurus_account_id` + token propagation | yes, ledger split | M | `oauth.go:395-403`; `oidc_auth.go:817-819` | deferred — touches `auth`-adjacent middleware and needs `BILLING_UNIFIED` off-state proof; unique-index collision policy is an owner call |
| 17 | operability:GUIDE-ROUTE-CONTRACT rewrite integration guide + route contract test | onboarding | M | `doc/product-integration-guide.md:1-44` | deferred — write after Lane 3 lands so the guide documents the real filter |
| 18 | contract:DOC-1 OpenAPI drift gate | onboarding | M | `docs/openapi/api-v2.yaml:503-577` | deferred |
| 19 | console:UX-3 fabricated entitlements + invite issuance UI | console | M | `Settings/index.jsx:93-122,1404-1408` | deferred |
| 20 | security:SEC-C TOTP row in erasure cascade | compliance | S | `privacy_erasure.go:98-180` | deferred |
| 21 | contract:ATTR-1 migration 033 persisted column + response echo | attribution | L | `entity/log.go:45,80` | deferred — JSON expression suffices at 24k rows; column is the index follow-up |
| 22 | billing:BILL-C allow-list `fable/acest/unica/mira` + legacy debit description | attribution | S | `model_equivalence.go:30-39`; `quota.go:1055` | deferred — allow-list must be seeded on platform first (cross-repo, owner) |
| 23 | product_line:XP-1 entitlement product alignment | latent | S | `entitlement.go:20,64` | deferred — only matters once platform issues product-scoped entitlements; `GetEntitlements` has no seam |

## 4. Lanes

### 4.0 File disjointness (verified by listing)

| File | L1 | L2 | L3 | L4 |
|---|---|---|---|---|
| `internal/adapter/handler/tenant_credit_pool.go` | x | | | |
| `internal/adapter/handler/tenant_credit_pool_stranded_test.go` | x | | | |
| `internal/adapter/middleware/auth.go` | | x | | |
| `internal/adapter/middleware/auth_helper_cover_test.go` | | x | | |
| `internal/adapter/middleware/gap_r3_auth_cover_test.go` | | x | | |
| `internal/domain/entity/user.go` | | x | | |
| `internal/adapter/repo/user.go` | | x | | |
| `internal/adapter/repo/user_cache.go` | | x | | |
| `internal/adapter/repo/user_mapping.go` | | x | | |
| `internal/adapter/repo/user_disable_cache_test.go` (new) | | x | | |
| `internal/adapter/provider/common/relay_info.go` | | | x | |
| `internal/adapter/provider/common/relay_info_project_test.go` | | | x | |
| `internal/adapter/handler/relay.go` | | | x | |
| `internal/adapter/handler/relay_attribution_test.go` (new) | | | x | |
| `internal/app/governance/enrich_test.go` | | | x | |
| `internal/app/log_other_projection_lock_test.go` | | | x | |
| `internal/domain/entity/log.go` | | | x | |
| `internal/adapter/repo/log.go` | | | x | |
| `internal/adapter/repo/log_source_product_filter_test.go` (new) | | | x | |
| `internal/adapter/handler/v2_log.go` | | | x | |
| `internal/adapter/handler/v2_log_stat.go` | | | x | |
| `internal/adapter/handler/v2_log_test.go` | | | x | |
| `internal/adapter/handler/v2_log_stat_test.go` | | | x | |
| `internal/pkg/metrics/metrics.go` | | | | x |
| `internal/adapter/middleware/internal_api_auth.go` | | | | x |
| `internal/adapter/middleware/internal_api_auth_test.go` | | | | x |
| `internal/adapter/handler/internal_tenant_guard.go` (new) | | | | x |
| `internal/adapter/handler/internal_tenant_guard_test.go` (new) | | | | x |
| `internal/adapter/handler/provisioning.go` | | | | x |
| `internal/adapter/handler/internal_provisioned_redemptions.go` | | | | x |
| `internal/adapter/handler/internal_credit_pool_fund.go` | | | | x |
| `internal/adapter/handler/internal_credit_pool_fund_test.go` | | | | x |
| `internal/adapter/handler/internal_provision_test.go` | | | | x |
| `internal/app/governance/audit_action.go` | | | | x |

No file appears in two columns. Packages overlap (`handler`, `middleware`, `repo`, `governance`) —
that is allowed; files do not.

Dirty-tree guard for every lane: before starting, `git status --short` must show only the five
foreign untracked files plus the previous lanes' own files; a lane must not revert or reformat
anything outside its column.

### 4.1 Lane 1 — Credit-pool topup debits the platform wallet in the SSOT unit

Candidates: `billing:BILL-A`.

Files:
- `internal/adapter/handler/tenant_credit_pool.go`
- `internal/adapter/handler/tenant_credit_pool_stranded_test.go`

Test packages (run in full): `./internal/adapter/handler/`

Spec:
1. In `TopupCreditPool` (`tenant_credit_pool.go:281`) replace
   `walletAmount := float64(req.Amount) / 1000.0` with
   `walletAmount := float64(req.Amount) / currency.LucToLut()` (import
   `internal/pkg/currency`; do not hardcode 500000 — `currency.go:36` forbids it). Rewrite the
   trailing comment to state the real invariant (1 LB = `QuotaPerUnit` quota, same as
   `quota.go:985`, `v2_billing.go` transfer and `internal_api.go` topup).
2. The revert path (`:341`) already reuses `walletAmount`; confirm it does and leave the flow
   otherwise untouched (idempotency key, description, product id `newhub`).
3. Extend `stubWalletSeams` (or add a sibling `stubWalletSeamsCapture`) in
   `tenant_credit_pool_stranded_test.go` so the debit and credit stubs record the `amount`
   argument in addition to counting calls. Do not change existing call-count assertions.
4. Do NOT touch `repo.TopupPool`, pool balance arithmetic, `max_balance` semantics, the
   idempotency-key handling, or any other handler in the package.

Oracle (drives the real router → `TopupCreditPool` → `debitWallet` seam):
- `TestTopupCreditPool_WalletDebitUsesQuotaPerUnit` in `tenant_credit_pool_stranded_test.go`,
  built on `setupAdminPoolRouter(t, true)`: POST
  `/api/v2/admin/tenants/{id}/credit-pool/topup` with `{"amount": 500000}`; assert the captured
  debit amount `== float64(500000)/currency.LucToLut()` (== 1.0 with the default
  `QuotaPerUnit`) using an exact float comparison against the computed value, never a literal.
- `TestTopupCreditPool_RevertCreditsSameAmountAsDebit`: same fixture with the pool's
  `max_balance` set low enough that `repo.TopupPool` fails; assert one credit call and captured
  credit amount `== captured debit amount`.
- Run: `go test -count=1 -p 2 ./internal/adapter/handler/ -run 'TestTopupCreditPool' -v` (must
  list ≥ 2 matched tests), then the whole package.

Mutation (prove red by hand): restore `walletAmount := float64(req.Amount) / 1000.0`; the first
test must fail with captured `500` vs expected `1`. Revert the mutation before finishing.

Enterprise acceptance:
- A tenant admin topping up 500 000 quota (1 LB at default pricing) sees exactly 1 LB leave the
  platform wallet: probe = the test above plus, on UAT, `POST .../credit-pool/topup {amount:500000}`
  followed by the platform wallet transaction row showing `amount = 1.0000`, `product_id = newhub`.
- The pool balance shown at `GET /api/v2/admin/tenants/{id}/credit-pool` and the wallet debit
  reconcile through `currency.LucToLut()` with zero residual, for any amount, on the same day.

Cross-repo follow-up: platform owner must decide on a one-off reconciliation of the single
historical `credit_pool_fund_events` row (2026-08-23) that was debited at the old unit (it
over-debited the wallet by a factor of 500 relative to the pool credit). No contract text changes;
add a changelog line in root `doc/coord/changelog.md` under newhub: "credit-pool topup wallet unit
aligned to QuotaPerUnit".

### 4.2 Lane 2 — Sessions re-validate status and role; disable invalidates the cache; platform bearer resolves any tenant

Candidates: `tenancy:SEC-B`, `security:SEC-B`, `tenancy:AUTH-TENANT-BEARER`.

Files:
- `internal/adapter/middleware/auth.go`
- `internal/adapter/middleware/auth_helper_cover_test.go`
- `internal/adapter/middleware/gap_r3_auth_cover_test.go`
- `internal/domain/entity/user.go`
- `internal/adapter/repo/user.go`
- `internal/adapter/repo/user_cache.go`
- `internal/adapter/repo/user_mapping.go`
- `internal/adapter/repo/user_disable_cache_test.go` (new)

Test packages (run in full): `./internal/adapter/middleware/`, `./internal/adapter/repo/`

Spec:
1. `entity.UserBase` (`entity/user.go:102-116`): add `Role int \`json:"role"\``. In
   `repo/user_cache.go` populate it wherever `UserBase` is built from a `User` (`GetUserCache`
   DB fallback at `:23-45`, `updateUserCache` `:45`) and add `updateUserRoleCache` mirroring
   `updateUserStatusCache` (`:173`). Rollout rule: a cached hash written before this change decodes
   `Role == 0`; treat `0` as "unknown → keep the session role", never as a demotion
   (`RoleGuestUser` is 0, but no console session is ever minted for a guest).
2. `repo.DisableUserById` (`repo/user.go:379-384`): after the UPDATE succeeds call
   `invalidateUserCache(id)` (same shape as `AdminDeleteUser` `:458`). Return the UPDATE error
   first; cache invalidation errors are logged, not returned (mirrors existing helpers).
3. `authHelper` (`middleware/auth.go:36-250`): after `userId` is resolved and before the status
   check at `:172`, call `repo.GetUserCache(userId)` **once**; on success override `status` with
   `userCache.Status` and, when `userCache.Role > 0`, override `role` with `userCache.Role`.
   Reuse the same `userCache` for the tenant lookup at `:235` (delete the second call). On lookup
   error fail OPEN exactly as the tenant check does today (`:241-244`). Keep the SDK-bridge
   self-heal at `:62-68` from re-persisting an `Enabled` status for a user the cache says is
   disabled: write the fresh status into the session when they differ.
   Do NOT touch `TokenAuth` (`:490-500`), `PlaygroundAuth`, the OIDC middleware, or the
   `useAccessToken` branch beyond what is needed to feed `userId` into the shared re-validation.
4. Platform session bearer (`auth.go:96`): replace `repo.GetUserByIDPSubject(sub, "default")` with
   a tenant-agnostic lookup. Add `repo.GetUserByIDPSubjectAnyTenant(idpSubject string)` in
   `repo/user_mapping.go` that selects the active mapping ordered by `is_active DESC, id ASC`
   (deterministic if a subject were ever mapped twice) and returns the user plus its tenant id.
   Keep `GetUserByIDPSubject` unchanged for its other callers.

Oracle (all on the real `UserAuth()` / `AdminAuth()` chain via `buildRoleRouter/serveRole` and
`setupCoverDB`):
- `TestAuthHelper_SessionStatusRevalidated_DisabledInDB` (`auth_helper_cover_test.go`): seed an
  enabled user, session `{id, username, role: RoleCommonUser, status: UserStatusEnabled}`; call
  `repo.DisableUserById(id)`; GET through `UserAuth()` must return the ban response
  (`success:false`, message contains "封禁") — on HEAD this returns 200 success.
- `TestAuthHelper_SessionRoleRevalidated_DemotedInDB`: seed `RoleAdminUser`, session role Admin;
  `repo.AdminUpdateUser(id, RoleCommonUser, status, quota, group)`; GET through `AdminAuth()` must
  return the "权限不足" response — on HEAD 200 success.
- `TestAuthHelper_SessionRevalidation_FailsOpenOnLookupError`: point `repo.DB` at a closed
  connection (or a user id that does not exist while the session is otherwise valid) and assert
  the existing behaviour is preserved (the negative control for fail-open).
- `TestAuthHelper_IdentitySessionToken_ResolvesNonDefaultTenantUser`
  (`gap_r3_auth_cover_test.go`, twin of `:139`): tenant `tenantX` row, user `TenantId="tenantX"`,
  mapping `TenantID="tenantX"`, minted platform session token; assert 200, body id == user id,
  `c.GetString("tenant_id") == "tenantX"`. On HEAD: `success:false`, "access token" message.
- `TestDisableUserById_InvalidatesUserCache` (`repo/user_disable_cache_test.go`, miniredis
  pattern from `cov_repo_token_cache_test.go`): warm the cache with `updateUserCache(user)`
  (Status enabled), call `DisableUserById`, assert `GetUserCache(id).Status ==
  common.UserStatusDisabled` without advancing time. On HEAD the cached `Enabled` is returned.
- Run: `go test -count=1 -p 2 ./internal/adapter/middleware/ -run 'TestAuthHelper' -v` (≥ 4
  matched), `go test -count=1 -p 2 ./internal/adapter/repo/ -run 'TestDisableUserById' -v`, then
  both packages in full.

Mutation (prove red by hand): (a) delete the `status = userCache.Status` override → first test
returns 200; (b) delete the `role` override → second test returns 200; (c) remove
`invalidateUserCache` from `DisableUserById` → miniredis test reads `Enabled`; (d) restore the
`"default"` literal at `auth.go:96` → tenantX test returns "access token" failure.

Enterprise acceptance:
- Disable a user in the admin console, then replay that user's existing session cookie against
  `GET /api/user/self`: expected `success:false` "封禁" on the very next request (no logout, no
  60 s wait). Probe on UAT with two browser sessions.
- Demote a tenant admin to common user; the demoted session's next `GET /api/v2/admin/*` or
  `AdminAuth`-gated v1 route returns "权限不足" immediately.
- A user provisioned into a non-default tenant calling any `UserAuth` route with a platform
  session bearer (`Authorization: Bearer <HS256 identity session>`) is admitted and
  `tenant_id` in the request context equals the user's tenant (probe: UAT tenant `lurus` user +
  bridge-minted session token → `GET /api/user/self` returns that user).

Cross-repo follow-up: none required. Note for platform/switch/lutu owners in root
`doc/coord/service-status.md` (newhub slice): "platform session bearer now resolves users in any
tenant (was: default only)".

### 4.3 Lane 3 — Per-product attribution is consumable and locked end to end

Candidates: `product_line:XP-1` (minus entitlement alignment), `console:UX-2` (API half),
`testing:TI-1`, `contract:ATTR-1` (echo/filter half, without the column).

Files:
- `internal/adapter/provider/common/relay_info.go`
- `internal/adapter/provider/common/relay_info_project_test.go`
- `internal/adapter/handler/relay.go`
- `internal/adapter/handler/relay_attribution_test.go` (new)
- `internal/app/governance/enrich_test.go`
- `internal/app/log_other_projection_lock_test.go`
- `internal/domain/entity/log.go`
- `internal/adapter/repo/log.go`
- `internal/adapter/repo/log_source_product_filter_test.go` (new)
- `internal/adapter/handler/v2_log.go`
- `internal/adapter/handler/v2_log_stat.go`
- `internal/adapter/handler/v2_log_test.go`
- `internal/adapter/handler/v2_log_stat_test.go`

Test packages (run in full): `./internal/adapter/provider/common/`, `./internal/adapter/handler/`,
`./internal/app/governance/`, `./internal/adapter/repo/`, and
`go test -count=1 -p 2 ./internal/app/ -run 'TestOtherProjection'` (the `internal/app` package
contains four foreign untracked test files; they are compiled by any run of that package. If they
fail to compile, report it — do not edit, delete or move them).

Spec:
1. Resolve once at the base constructor: in `genBaseRelayInfo` (`relay_info.go:412`) set
   `info.SourceProduct = ratio_setting.ResolveSourceProduct(c.GetHeader(ratio_setting.SourceProductHeader))`.
   Delete the duplicate at `handler/relay.go:270` (keep the explanatory comment, moved). Effect:
   MJ (`relay.go:733`) and Task (`relay.go:803`) paths now carry the caller's product; playground
   and channel-test callers get `llm-api` by default (acceptable, documented in the comment).
2. Error rows carry the product: in `recordRelayErrorLog` (`relay.go:696-729`) add
   `other["source_product"] = ratio_setting.ResolveSourceProduct(c.GetHeader(...))` (the
   `RelayInfo` is not in scope there; resolving the header again is the same pure function).
3. Projection lock: in `internal/app/log_other_projection_lock_test.go` add `EnrichLogParams`
   to the driven generators and classify `source_product` (and `data_flow_source`/`data_flow_dest`
   if the lock now sees them for the first time) as user-visible. `source_product` must NOT be
   added to `repo.internalOtherKeys` (`repo/log.go:155`) — it is the caller's own tag. Do not
   change any existing classification.
4. Query dimension: add `SourceProduct string` to `entity.LogQueryParams` (`entity/log.go:85-104`)
   with a comment mirroring the `ProjectID` one (empty = no filter). In `repo/log.go`
   `GetUserLogsWithParams` and `GetTenantLogsWithParams` apply
   `Where(jsonSourceProductExpr()+" = ?", params.SourceProduct)` next to the `ModelName` filters
   (`:782`, `:836`). Reuse `jsonSourceProductExpr` from `savings.go:67` unchanged (same package).
5. Handlers: `GetLogsV2` and `GetAllLogsV2` (`v2_log.go:117`, `:199`) read
   `c.Query("source_product")` into the params. `serveLogStatV2` (`v2_log_stat.go:87`) reads the
   same query, applies it to both `windowQuery` and `rateQuery`, and adds a `by_product` array to
   the response: `[{source_product, total_requests, total_quota, prompt_tokens,
   completion_tokens}]` grouped by `COALESCE(<jsonSourceProductExpr>, 'llm-api')` over the same
   window filters (excluding the product filter itself, so the caller sees the full split even
   when filtering). Response shape for every existing field is unchanged.
6. Do NOT add a `logs` column, do NOT touch `governance.go` (it already writes the key),
   `savings.go`, `metrics.go`, the entitlement middleware, or the web console. Migration 033 is
   **not used** (`migration_033_used = false`): the JSON expression is indexed-enough at 24k rows
   and is the same expression the governance savings endpoint already runs in production.

Oracle:
- `TestGenBaseRelayInfo_CarriesSourceProduct` (`relay_info_project_test.go`, uses
  `newRelayInfoCtx()`): header `X-Lurus-Product: switch` → real `relaycommon.GenRelayInfo(c,
  types.RelayFormatOpenAI, req, nil)` yields `SourceProduct == "switch"`; header `evil` → `llm-api`;
  a Task-format call (`GenRelayInfo(c, types.RelayFormatTask, nil, nil)`) with header `lutu` →
  `"lutu"` (this is the MJ/Task fold-fix).
- `TestEnrichLogParams_SourceProductFromRelayInfo` (`enrich_test.go`): real `EnrichLogParams(c,
  &RelayInfo{SourceProduct:"kova"}, params)` → `params.Other["source_product"] == "kova"`; empty →
  `ratio_setting.DefaultSourceProduct`.
- `TestRelay_SourceProductHeader_ReachesErrorLogRow` (`relay_attribution_test.go`): reuse
  `handlerRelaySetupDB` + `errorLogFallbackEnable` + the sensitive-word rejection setup from
  `relay_sensitive_rejection_test.go:27-40`; call the real `Relay(c, types.RelayFormatOpenAI)`
  with header `kova`; read the single `LogTypeError` row and assert `other.source_product ==
  "kova"`; second case header `fable` → `"llm-api"` (documents the fold).
- `TestGetUserLogsWithParams_SourceProductFilter` (`repo/log_source_product_filter_test.go`,
  hermetic SQLite tier): three rows with `other` = `{"source_product":"lutu"}`,
  `{"source_product":"switch"}`, `""`; filter `lutu` → exactly 1 row; empty filter → 3.
- `TestGetLogsV2_SourceProductFilter` (`v2_log_test.go`, existing router/tenant fixture): tenant A
  rows lutu+switch, tenant B row lutu; `GET /api/v2/{slugA}/logs?source_product=lutu` → 1 row
  whose `other.source_product == "lutu"`; tenant B's row never appears.
- `TestGetLogStatV2_SourceProductFilterAndBreakdown` (`v2_log_stat_test.go`, `seedStatLog`
  extended with an `other` argument or a sibling seeder): rows switch(quota 30), switch(20),
  kova(5) → `?source_product=switch` gives `total_quota == 50`, `total_requests == 2`, and
  `by_product` contains both `{switch, 50}` and `{kova, 5}`; unfiltered `total_quota == 55`.
- `TestOtherProjectionIsFullyClassified` (existing lock) stays green only with the new
  classification in place.
- Run: `go test -count=1 -p 2 ./internal/adapter/provider/common/ -run 'SourceProduct' -v`,
  `go test -count=1 -p 2 ./internal/adapter/handler/ -run 'SourceProduct' -v` (≥ 3 matched),
  `go test -count=1 -p 2 ./internal/app/governance/ -run 'TestEnrichLogParams' -v`,
  `go test -count=1 -p 2 ./internal/adapter/repo/ -run 'SourceProduct' -v`, then the listed
  packages in full.

Mutation (prove red by hand): (a) delete the `SourceProduct` assignment in `genBaseRelayInfo` →
`TestGenBaseRelayInfo_CarriesSourceProduct` reads `""`, and the Task case reads `""`; (b) delete
`params.Other["source_product"] = sourceProduct` at `governance.go:110` → enrich test and
projection lock red; (c) delete `other["source_product"]` in `recordRelayErrorLog` → error-row test
reads missing key; (d) delete the `Where(jsonSourceProductExpr()...)` in `GetUserLogsWithParams`
→ repo and `GetLogsV2` tests return 3/2 rows; (e) delete the `by_product` block → stat test fails
on the missing array.

Enterprise acceptance:
- A sibling product team sends `X-Lurus-Product: switch` and can read its own spend without root:
  `GET /api/v2/{slug}/logs/stat?source_product=switch&start_time=…` returns totals that equal the
  sum of `quota` over `GET /api/v2/{slug}/logs?source_product=switch` rows for the same window,
  and `by_product` sums to the unfiltered `total_quota` (probe on UAT with two tagged relay calls
  and one untagged).
- Every relay log row — success or error, chat/MJ/Task — carries `other.source_product`
  (probe on UAT: one 400 rejection with header `lutu` shows `source_product: lutu` on its error row
  in `GET /api/v2/{slug}/logs?type=5`).
- An unknown header value is visibly attributed to `llm-api`, never dropped and never echoed as
  the raw string.

Cross-repo follow-up (root repo, owner): correct `doc/coord/contracts.md:56` — the default product
id is `llm-api` (code constant), not `lurus-api`; append "consumer-readable per-product usage:
`GET /api/v2/{slug}/logs?source_product=` and `/logs/stat?source_product=` → `by_product`" to the
same paragraph; the `ReportUsage` proto still lacks `product_id` (lurus-proto-go field +
platform handler read + `identity_grpc_client.go:178` pass-through) — unchanged, still owner-gated.
Allow-list extension for `fable/acest/unica/mira` requires platform `003_seed_products` first
(deferred #22).

### 4.4 Lane 4 — Platform seam traffic and rejections become observable and audited

Candidates: `product_line:XP-2`, `tenancy:IK-TENANT-DENY-AUDIT`.

Files:
- `internal/pkg/metrics/metrics.go`
- `internal/adapter/middleware/internal_api_auth.go`
- `internal/adapter/middleware/internal_api_auth_test.go`
- `internal/adapter/handler/internal_tenant_guard.go` (new)
- `internal/adapter/handler/internal_tenant_guard_test.go` (new)
- `internal/adapter/handler/provisioning.go`
- `internal/adapter/handler/internal_provisioned_redemptions.go`
- `internal/adapter/handler/internal_credit_pool_fund.go`
- `internal/adapter/handler/internal_credit_pool_fund_test.go`
- `internal/adapter/handler/internal_provision_test.go`
- `internal/app/governance/audit_action.go`

Test packages (run in full): `./internal/pkg/metrics/`, `./internal/adapter/middleware/`,
`./internal/adapter/handler/`, `./internal/app/governance/`

Spec:
1. `metrics.go`: add `InternalApiRequests` = `lurus_gateway_internal_api_requests_total`
   CounterVec with labels `route` (the gin registered pattern from `c.FullPath()`, bounded by the
   route table) and `outcome` ∈ `{ok, unauthorized, forbidden_scope, forbidden_tenant}`. Never
   label by key id, key name, or tenant slug. Register alongside the existing series; if the
   alert-wiring honesty gate in this package enumerates series, follow its convention.
2. `internal_api_auth.go`: `InternalApiAuth` increments `outcome=unauthorized` on both 401 paths
   (`:16`, `:27`) and emits one `common.SysLog` line `internal_api: unauthorized route=<pattern>
   reason=<missing|invalid>`; never include any part of the presented key. `RequireScope`
   increments `forbidden_scope` on the 403 paths (`:51`, `:71`) with a `SysLog` naming the key
   **name** and the required scope, and increments `ok` when the scope check passes (so "platform
   never called" is distinguishable from "called and refused" — the existing per-key
   `last_used_at` is not scrapeable). Leave `RequireAnyScope` behaviour unchanged except for the
   same counters.
3. New `handler/internal_tenant_guard.go`: `requireInternalKeyTenant(c *gin.Context, tenantID
   string) bool` that (a) reads the key from `c.Get("internal_api_key")`, (b) calls the existing
   `repo.InternalKeyAllowedForTenant`, (c) on denial writes the identical 403 JSON
   (`TENANT_NOT_AUTHORIZED`, same message text), records a governance audit event with the new
   `governance.ActionInternalKeyTenantDenied = "internal_key.tenant_denied"` (resource = key id,
   details: key name, tenant id, route pattern — no key material), increments
   `outcome=forbidden_tenant`, and returns false. Replace the six inline blocks
   (`provisioning.go:76/:193/:328`, `internal_provisioned_redemptions.go:158/:283`,
   `internal_credit_pool_fund.go:99`) with the guard call. Response bodies and status codes are
   byte-identical to today for both the allowed and denied paths.
4. Do NOT change `repo.InternalKeyAllowedForTenant`, the pre-auth IP limiter, scope constants,
   `/internal/balance/topup` (deferred #12), or any success-path handler logic.

Oracle:
- `TestInternalApiAuth_CountsAndLogsUnauthorized` (`internal_api_auth_test.go`): mount the real
  `InternalApiAuth()` on a gin router; request with an invalid key; assert
  `testutil.ToFloat64(metrics.InternalApiRequests.WithLabelValues(route, "unauthorized"))` delta
  == 1 and a captured log line (redirect `common.SysLog` output via the package's existing log
  capture or a pipe) contains `unauthorized` and `route=` and does NOT contain the presented key.
- `TestRequireScope_CountsForbiddenScopeAndOk`: real `InternalApiAuth()+RequireScope(
  repo.ScopeBalanceWrite)` with a hermetic key lacking the scope → `forbidden_scope` delta 1 and a
  log line containing the key name and `balance:write`; with a key holding the scope →
  `ok` delta 1.
- `TestInternalFundCreditPool_TenantDenied_AuditedAndCounted` (`internal_credit_pool_fund_test.go`,
  pattern of `TestInternalFundCreditPool` `:152`): narrow `balance:write` key with NO
  `internal_api_key_tenants` row → 403 `TENANT_NOT_AUTHORIZED` AND an `audit_events` row with
  action `internal_key.tenant_denied`, resource id == key id, details containing the tenant id and
  route pattern AND `forbidden_tenant` delta 1. Add the mirror
  `TestCreateProvisionedKey_TenantDenied_Audited` in `internal_provision_test.go`.
- `TestRequireInternalKeyTenant_ScopeAllBypass` (`internal_tenant_guard_test.go`): ScopeAll key
  → true, no audit row, no counter increment (guards against the guard regressing ScopeAll).
- Run: `go test -count=1 -p 2 ./internal/adapter/middleware/ -run 'InternalApiAuth|RequireScope' -v`,
  `go test -count=1 -p 2 ./internal/adapter/handler/ -run 'TenantDenied|RequireInternalKeyTenant' -v`
  (≥ 3 matched), then the four packages in full.

Mutation (prove red by hand): (a) remove the `Inc()` in `RequireScope`'s 403 path → delta 0;
(b) restore the inline block at `internal_credit_pool_fund.go:99` in place of the guard → no audit
row, counter 0, test red while the 403 itself still passes (this is exactly the proxy-oracle trap
the test must not fall into: assert audit + counter, not only the status code); (c) remove the
`SysLog` in `InternalApiAuth` → captured log empty.

Enterprise acceptance:
- An operator can distinguish "platform never called" from "platform called and was refused" from
  `/metrics` alone: after one whitelisted fund call and one call for a non-whitelisted tenant on
  UAT, `curl -s localhost:30851/metrics | grep lurus_gateway_internal_api_requests_total` shows
  `outcome="ok"` 1 and `outcome="forbidden_tenant"` 1 on the fund route, and Netdata charts it
  with no collector change.
- The denial is visible to the tenant admin/root in the audit console:
  `GET /api/v2/admin/audit/events?action=internal_key.tenant_denied` returns the row naming the
  key and tenant, and the pod log carries one grep-able line with the key name and route and no
  secret.

Cross-repo follow-up: platform-core owner gets a newhub-side oracle for the BillingOutbox / erasure
/ provisioning integrations; add to root `doc/coord/contracts.md` newhub internal-API section: "seam
rejections are counted in `lurus_gateway_internal_api_requests_total{route,outcome}` and tenant
denials are audited as `internal_key.tenant_denied`". No platform code change required.

## 5. Deferred

| Candidate | Why deferred this cycle |
|---|---|
| operability:OPS-SECRETS-BOOTSTRAP | Pure repo-internal, but four lanes are the cap; it is next cycle's #1 (DR path breaks on `OIDC_CLIENT_ID` and scripts verify the UAT host). |
| operability:ONBOARD-402-POOL | Pool default (max_balance/balance) is a product decision; needs owner input before code, and `tenant.go` is not in any lane. |
| console:UX-1 | Frontend-only, S; cap reached. Next cycle with UX-2 console half. |
| console:UX-2 (web half: product column/filter in Log page) | API half ships in Lane 3; the console column follows once the query parameter exists. |
| observability:OBS-P1 | Edits `relay.go` and `repo/log.go`, both owned by Lane 3; land after Lane 3 with the label read from `RelayInfo.SourceProduct`. |
| observability:OBS-P2 | S and disjoint, but cap reached; also wants the product label from OBS-P1 to be meaningful. |
| observability:OBS-P3 | Cap reached; no live incident depends on it this week. |
| contract:AC-2 | Needs a cross-repo grep of switch/lutu for `new_api_error` string-matching before changing `type`; M effort. |
| contract:ATTR-1 (migration 033 column + response echo) | L effort; the JSON expression already used in production covers 24k rows. 033 stays reserved as the index follow-up when volume grows. |
| contract:DOC-1 | Cap reached; write after Lane 3 so the regenerated OpenAPI documents `source_product`/`by_product`. |
| testing:TI-2 | Cap reached; S; no overlap — next cycle. |
| product_line:XP-3 / testing:TI-3 | Owner has not decided to publish switch releases via hub (0 `releases`, 0 `switch_app.*` options); validation rule must be confirmed against the switch client pin first. |
| billing:BILL-B | Unique-index collision policy for `lurus_account_id` and `BILLING_UNIFIED`-off proof are owner calls; touches `oidc_auth.go` adjacent to Lane 2's package. |
| billing:BILL-C allow-list `fable/acest/unica/mira` + legacy debit description | Allow-list must be seeded in platform `identity.products` first or `require_product_attribution` rejects; cross-repo half goes first. Legacy description is cosmetic. |
| product_line:XP-1 entitlement product alignment | Only matters once platform issues product-scoped entitlements; `GetEntitlements` has no seam for an honest test. |
| security:SEC-D `/internal/balance/topup` guard + audit | Should reuse Lane 4's shared guard; sequencing it behind Lane 4 keeps the guard single-sourced. Live key whitelist for `default` must be checked first. |
| security:SEC-C TOTP in erasure cascade | S; cap reached; `privacy_erasure_requests` is 0 live so no data is currently mis-attested. |
| operability:GUIDE-ROUTE-CONTRACT | Write after Lane 3 so the guide documents the real filter and the `llm-api` default. |
| console:UX-3 | Cap reached; M frontend. |
| Cross-repo: `ReportUsage` proto `product_id` (lurus-proto-go + platform handler + `identity_grpc_client.go:178`) | Owner-gated; carried in Lane 3's follow-up, not dropped. |
| Cross-repo: platform `003_seed_products` rows for `fable/acest/unica/mira` | Owner-gated; prerequisite for BILL-C. |
| Cross-repo: root `contracts.md:56` default id `lurus-api` → `llm-api` | Carried in Lane 3's follow-up. |

## 6. Next cycle

1. OPS-SECRETS-BOOTSTRAP (S) and ONBOARD-402-POOL (M, after the owner sets the pool default) —
   the two remaining "new tenant / DR by the book fails" defects.
2. OBS-P1 + OBS-P2 together (product label on relay series and stream-end reasons) now that
   `SourceProduct` is resolved at the base constructor.
3. SEC-D on top of the shared tenant guard; AC-2 after the cross-repo `new_api_error` grep.
4. UX-1 + UX-2 console half + GUIDE-ROUTE-CONTRACT + DOC-1 as one "integrator-facing surface"
   batch once the API from Lane 3 is live.
5. Re-measure the scorecard on production after this cycle's deploy: the product_line dimension
   is expected to move on (a) `by_product` visible to a tenant admin, (b) internal-seam counters
   non-zero, (c) the 500× topup unit fixed — each verified by the live probes in §4, not by tests.
