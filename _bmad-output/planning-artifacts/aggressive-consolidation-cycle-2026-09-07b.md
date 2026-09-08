# Aggressive consolidation cycle — 2026-09-07 (b)

Scope: `2b-svc-newhub` only. Base: `main` @ `9dabbe12`. Mandate: this product first,
commercial grade fast; prefer REMOVE and MERGE over add. Six lanes run sequentially on the
same tree by separate agents; file sets are disjoint (verified in §4.7). Lane numbering is
the binding execution order (L2 before L3, L2 before L4 — see each lane's spec).

Untouchable (someone else's WIP, never stage/read as ours):
`internal/app/coverage_lift_test.go`, `internal/app/coverage_lift_tokens_test.go`,
`internal/app/coverage_seam2_extra_test.go`, `internal/app/coverage_seam_extra_test.go`,
`internal/app/openrouter_sync/coverage_extra_test.go`.

Hard rules carried by every lane: no git operations, no production/R6 access, no new SQL
migrations, `go test -count=1 -p 2 <pkgs>` only (no `./...`, no `-race`, no linter), web tests
via `cd web && bun run test -- <path>`, no model/tool names in any file, every "passed" claim
comes with the command and its tail output.

## 1. Scorecard

| Dimension | Now | Target after cycle | What moves it (lane) |
|---|---|---|---|
| product_line | 6 | 7 | seam denials auditable (L1), product label on relay series (L3), one product id + honest guide (L4), product column in console (L5) |
| billing | 5.5 | 6.5 | SSO users linked to the wallet like every other entry point (L4), wallet-charge metric observed on the live settle path (L3) |
| tenancy | 6 | 6.75 | one internal tenant guard incl. topup/adjust (L1), one TenantSlugGuard rule (L1) |
| observability | 4.5 | 5.5 | dead series/dashboards/claims deleted, declared-series-has-writer gate, client_gone outcome (L3); per-replica label deferred |
| contract | 4.5 | 6 | vendor-taxonomy `type`, non-empty `code`, English messages, one renderer, stubs retired (L2, L4) |
| console | 5 | 6 | legacy topup/redemption shells + ~9k dead lines removed, product attribution visible (L5) |
| testing | 5.5 | 6 | erasure cascade test un-deaded, placeholder/pseudo tests deleted, guard-less CI job removed (L6) |
| operability | 5.5 | 6.25 | DR scripts consistent with the manifest and pinned by a test, retired-service rot removed with a gate, never-run tag pipelines deleted (L6) |
| security | 6 | 6.75 | internal denials audited + metric, cross-tenant money path closed (L1), PIPL cascade covers TOTP (L6), session DB isolation (L6) |

## 2. Verification performed before ranking (HEAD 9dabbe12)

Every candidate's anchor was re-checked with grep/sed on HEAD; deviations from the assessments:

- `internal/pkg/metrics/metrics.go`: `channel_health` (195), `channel_consecutive_errors` (218),
  `channel_errors_total` (229) and `RecordChannelError`/`SetChannelHealth` (514/525) have zero
  non-test callers; `app.RecordDebitSuccess` (`internal/app/credit_pool.go:23`) has zero callers;
  `RecordBillingDebit` is called only from `identity_grpc_client.go:228` with `productID`. Confirmed.
- `internal/adapter/handler/relay.go`: status computed twice (113-115, 411-413); `RecordRelayTotal`
  (117), `RecordRelayError` (156), `RecordRelayRequest` (415) carry no product; a fourth
  `RecordRelayError` caller lives in `internal/app/relay/helper/stream_error.go:94` (not listed by
  the assessment — added to L3's file set).
- Six inline internal tenant guards confirmed at `provisioning.go:76,193,328`,
  `internal_provisioned_redemptions.go:158,283`, `internal_credit_pool_fund.go:99`;
  `InternalTopupBalance` (`internal_api.go:364`) and `InternalAdjustQuota` (:243) never call
  `InternalKeyAllowedForTenant`; topup writes no governance row (adjust does at :298).
  `internal_api_auth.go` denials (15-31, 52-79, 84-118) write nothing.
- `billing:BILL-MONEYIN-1` is NOT a pure retirement: the platform main tree calls newhub's
  `POST /internal/currency/exchange` (`2l-svc-platform/internal/adapter/handler/internal_api.go:1121`
  via `internal/pkg/lurusapi/client.go:77`) although `contracts.md` has no row for it. Deferred with
  a cross-repo follow-up instead of deleted.
- `contract:AC-2`: 0 hits for `new_api_error`, `api_not_implemented`, `quota_exceeded`,
  `upgrade_url` in `2c-gui-switch` and `2c-app-lutu` (excluding build dirs). Downstream compat lives in
  `internal/app/channel.go:85-125` (Code switch incl. `token_quota_exhausted`, Type switch incl.
  `authentication_error`/`permission_error`) — kept as-is.
- Test files that call `abortWithOpenAiMessage` directly: `error_log_middleware_test.go`,
  `r5e_model_rate_limit_default_test.go`; tests locking `new_api_error`: `rejection_envelope_wire_test.go`,
  `business_rate_limit_test.go`, `relay_inband_error_test.go`, `router/relay_wire_stamp_test.go`,
  `app/error_test.go`, `app/r2_channel_token_quota_test.go`, `types/error_more_test.go`; tests locking
  Chinese middleware messages: `auth_helper_cover_test.go`, `l3_token_quota_402_test.go`. All owned by L2.
- Web locale files are `web/src/i18n/locales/{en,zh,fr,ja,ru,vi}.json` (assessment names
  `en-US.json` do not exist). `web/package.json` gates: `test`=vitest, `lint`=prettier check,
  `eslint`, `check:casing`, `build`. i18n gate = `web/src/i18n/i18n-integrity.test.js`.
- Legacy console: `/console/topup` (App.jsx:235) and `/console/redemption` (:163) still routed;
  `components/dashboard` (16 files/3655 lines), `hooks/dashboard` (1701), `components/model-deployments`
  (657), `components/topup` (794), `components/table/redemptions` (3081) have no non-test importer
  outside the pages being deleted. `SiderBar.jsx:38-39` and `headerbar/UserArea.jsx:91` deep-link to
  the legacy shells; `cx_page_layout.test.jsx:184` and `cx_headerbar_parts.test.jsx:805` lock them.
- `web/src/pages/v2/Log/index.jsx` contains the string `product` 0 times; `by_product` consumed
  nowhere in `web/src` except CostIntelligence's unrelated `spend_by_product`.
- Operability: `deploy-stage.sh:100-112` writes 5 secret keys; `deployment.yaml` references
  `OIDC_CLIENT_ID` (133) without `optional`; both scripts health-check `test-newhub.lurus.cn`
  (`deploy-stage.sh:65`, `stage-rollback.sh:32` — the latter on `/api/status`). `scripts/migrate/` has
  its own `go.mod` (safe to delete). `electron/` present; `release.yml` and `electron-build.yml` are
  tag-only and the repo's only tags are two `archive/*`. GitHub reports `main` is NOT branch-protected
  (`gh api .../branches/main/protection` → 404), so removing the `handler audit + governance tests`
  job cannot strand a required check.
- `deployment.yaml` legitimately contains `lurus-system` (Redis/PG namespaces); the retired-identifier
  gate in L6 must NOT match the bare namespace, only the retired service/host/db identifiers.
- Testing: all four CI `go test` invocations pass `-short` (`go-ci.yml:117,242,477,544`);
  `privacy_erasure_test.go:119-121` skips under it. `release_test.go` double-gates on `-short` and
  `releaseService == nil` (191-296); `model_sync_worker_test.go` has 9 unconditional/gated skips.
- Session store: `cmd/server/main.go:379-386` strips the DSN path and calls `sessionredis.NewStore`
  (no DB) — production `/2` and UAT `/3` sessions both land in DB 0. `cmd/server` already has a
  `package main` test file (`billing_outbox_ticker_test.go`).
- `BILL-B2`: `oauth.go:402` and `oidc_auth.go:816-818` resolve the platform account into
  session/context only; stamp copies at `v2_token.go:258`, `token_service.go:154`, `repo/token.go:315`;
  guarded persist exists at `internal_api_ext.go:649-668`. OIDC callback / session-fallback test
  infrastructure exists (`oauth_test.go`, `oauth_security_test.go`, `oidc_session_fallback_test.go`,
  `session_fallback_account_id_test.go`).
- Literal `"lurus-api"` in production code: `billing.go:33`, `v2_billing.go:115,302,326` only;
  `entitlement.go:20` uses the constant `DefaultSourceProduct` = `llm-api`;
  `doc/product-integration-guide.md:114` still says the default is `lurus-api`.

## 3. Ranked backlog

Rank = enterprise value ÷ effort; live defects and code-deleting consolidations first.

| # | Candidate | Kind | Effort | Evidence (file:line) | Disposition |
|---|---|---|---|---|---|
| 1 | product_line:XP-SEAM-GUARD + tenancy:TI-C1 + security:SEC-A + tenancy:TI-F1 | consolidate | M | `internal_api_auth.go:15-31,52-118`; six guards (§2); `internal_api.go:243,364` | **L1** |
| 2 | tenancy:TI-R1 two hand-rolled TenantSlugGuard copies | retire | S | `tenant_credit_pool.go:176-191`; `v2_log_export.go:87-119`; `api-v2-router.go:93` | **L1** |
| 3 | contract:AC-2 native error taxonomy at the root | fix | M | `types/error.go:204,233,243`; `middleware/utils.go:16-32`; `rejection_envelope_wire_test.go:93` | **L2** |
| 4 | contract:AC-2b collapse hand-rolled envelopes, delete dead wrappers | consolidate | S | `cost_spike.go:101-107`; `utils.go:79`; `app/error.go:59-82`; the Anthropic-wire DTO file in `internal/pkg/dto/` (struct at line 444) | **L2** (entitlement/billing halves → L4) |
| 5 | contract:AC-4 retire ten 501 stubs | retire | S | `relay-router.go:199-210`; `relay.go:775-784`; `docs/openapi/relay.json:1879-2228` | **L2** (symbol deletion → L3) |
| 6 | observability:OBS-R1 delete lying observability surface + writer gate | retire | S | `metrics.go:190-233,404-416,513-531`; `credit_pool.go:20-25`; `deploy/grafana/*` | **L3** |
| 7 | product_line:OBS-P1-PRODUCT-LABEL product label on relay series | fix | M | `metrics.go:39-47,117-125`; `relay.go:117,156,415`; `stream_error.go:94` | **L3** |
| 8 | billing:BILL-METRIC-1 one wallet-charge metric on the live path | un-dead | S | `identity_grpc_client.go:228`; `identity_client.go:447`; `quota.go:1001` | **L3** |
| 9 | observability:OBS-R3 client_gone outcome, one status resolver | consolidate | S | `relay.go:113-115,411-413`; `stream_error.go:54` | **L3** |
| 10 | billing:BILL-B2 one platform-link rule (SSO persists `lurus_account_id`) | consolidate | M | `oauth.go:402`; `oidc_auth.go:816`; three stamp copies; `internal_api_ext.go:649` | **L4** |
| 11 | product_line:PRODUCT-ID-SSOT one product id | consolidate | S | `v2_billing.go:115,302,326`; `billing.go:33`; `entitlement.go:20,64`; guide:114 | **L4** |
| 12 | console:UX-CONS-1 retire legacy topup/redemption shells + dead trees | retire | M | `App.jsx:163,235`; `SiderBar.jsx:38-39`; `UserArea.jsx:91`; §2 line counts | **L5** |
| 13 | console:UX-LOG-PRODUCT + product_line:UX-2-PRODUCT-COLUMN | un-dead | S | `Log/index.jsx:213-228,763-771`; `v2_log_stat.go:245` | **L5** |
| 14 | operability:OPS-1 secrets/health-URL consistency + test | fix | S | `deploy-stage.sh:65,100-112`; `stage-rollback.sh:32`; `deployment.yaml:133` | **L6** |
| 15 | operability:OPS-3 retired-service rot + gate | retire | S | `incident-response.md:3,9-10,89-91`; `INDEX.md:24`; `scripts/migrate/` | **L6** |
| 16 | operability:OPS-4 never-run tag pipelines + electron shell | retire | S | `release.yml:11-12`; `electron-build.yml:4-8`; `electron/` | **L6** |
| 17 | testing:TI-1 + security:SEC-C erasure cascade un-deaded, TOTP row | un-dead | S | `privacy_erasure_test.go:119-121`; `privacy_erasure.go:98-181`; `go-ci.yml:117,242,477,544` | **L6** |
| 18 | testing:TI-2 repo pseudo-tests | retire | S | `sqlite_repo_extra3_test.go:434-444`; `sqlite_repo_test.go:2825-2829` | **L6** |
| 19 | testing:TI-3 guard-less `-run` CI job | retire | S | `go-ci.yml:113-119` vs `:477` | **L6** |
| 20 | security:SESS-DB session store honours Redis DB | fix | S | `cmd/server/main.go:379-386` | **L6** |
| 21 | operability:OPS-2 CreateTenant builds pool / warns (doc half) | fix | M | `tenant.go:130-183`; `pool_balance_check.go:61-79`; `tenant-onboarding.md` | doc half **L6**; code half deferred |
| 22 | observability:OBS-R2 per-replica label on /metrics + health | fix | M | `router/main.go:37`; `deployment.yaml:13`; `health.go:18-96` | deferred — next cycle #1 |
| 23 | billing:BILL-FORMULA-1 collapse cost estimate onto charge formula | consolidate | M | `perception.go:86`; `compatible_handler.go:370-382` | deferred — next cycle #2 |
| 24 | security:AUTH-1 one session principal resolver + tenant-disabled | consolidate | M | `oidc_auth.go:562-655`; `auth.go:287` | deferred — `auth.go` (L2) and `oidc_auth.go` (L4) both owned this cycle |
| 25 | console:UX-MODELS-1 one tenant-models hook | consolidate | M | `Dashboard/index.jsx:84`; `Chat/index.jsx:33,52`; `Playground/index.jsx:30-33` | deferred — locales owned by L5 |
| 26 | console:UX-SETTINGS-HONEST fabricated tiers, invite flow | fix | M | `Settings/index.jsx:93-122,1399-1423` | deferred — locales owned by L5 |
| 27 | observability:OBS-R4 leader/background-task liveness | fix | S | `leader_election.go:152-173`; `health.go` | deferred — with #22 |
| 28 | tenancy:TI-F2 legacy task/mj listings tenant-scoped | fix | S | `task.go:305`; `midjourney.go:435`; `api-router.go:209` | deferred — 0 non-default tenant users live; consider retiring the pages |
| 29 | contract:DOC-1 OpenAPI drift gate | un-dead | M | `docs/openapi/api-v2.yaml:475-577`; `README.md:88` | deferred — regenerate after L1-L4 change codes |
| 30 | testing:TI-4 48 SQLite bootstraps → one helper | consolidate | L | `v2_pricing_test.go:32-57` et al. | deferred — touches test files owned by L3/L6 |
| 31 | billing:BILL-MONEYIN-1 retire LUC→LUT exchange | retire | S | `internal_currency.go:72-90`; platform `internal_api.go:1121` | deferred — platform is a live caller (§2) |
| 32 | product_line:XP-3 switch publish round-trip | fix | S/M | `switch_app_release_test.go:22-47`; `option.go:50-110` | deferred — owner undecided (0 releases) |
| 33 | billing:BILL-C allow-list fable/acest/unica/mira | fix | S | `model_equivalence.go:30-39` | deferred — platform seed rows first |
| 34 | contract:ATTR-1 migration 033 column | fix | L | `entity/log.go:45,80` | deferred — no migration budget |

## 4. Lanes (execution order is binding)

### L1 — SEAM-GUARD: one internal tenant guard, every internal denial audited and counted, one TenantSlugGuard rule

Kind: consolidate. Effort: M. Candidates: product_line:XP-SEAM-GUARD, tenancy:TI-C1, security:SEC-A, tenancy:TI-F1, tenancy:TI-R1.

Spec:
1. `internal/adapter/middleware/internal_api_auth.go`: add one unexported exit
   `denyInternalSeam(c, status int, errorCode, message, reason string)` used by every denial in
   `InternalApiAuth`, `RequireScope`, `RequireAnyScope` and the new guard. It (a) writes the JSON body
   with the existing `success:false` + `message` (message strings for auth/scope denials stay
   byte-identical) plus an additive `error_code` field (`MISSING_API_KEY`, `INVALID_API_KEY`,
   `INSUFFICIENT_SCOPE`, `TENANT_NOT_AUTHORIZED`); (b) records
   `governance.RecordAuditEvent` with new constant `ActionInternalKeyDenied = "internal_key.denied"`
   (`internal/app/governance/audit_action.go`, next to `ActionInternalKeyTenantGranted`), resource
   `internal_key`, resource id = key id (0 when the key is unknown), details `{seam: c.FullPath(),
   reason, tenant, scope}` — never key material; (c) increments new counter
   `lurus_gateway_internal_seam_denied_total{seam, reason}` declared in NEW file
   `internal/pkg/metrics/internal_seam.go` (reason ∈ `missing_key|invalid_key|scope|tenant`; seam =
   `c.FullPath()`, bounded by the route table).
2. New `InternalTenantGuard()` middleware (same file): reads `c.Param("slug")`, resolves the tenant
   (404 `TENANT_NOT_FOUND` with the same body the handlers emit today), checks
   `key.HasScope(repo.ScopeAll) || repo.InternalKeyAllowedForTenant(key, tenant.Id)`, denies with 403
   `TENANT_NOT_AUTHORIZED` using the fund endpoint's remediation message as the single text, and sets
   `c.Set("internal_tenant", tenant)`. Mount it in `internal-api-router.go` on the provisioning group
   (`/tenants/:slug/keys`, `/redemptions`, `/redemptions/revoke`) and the pool-fund group. Handlers
   `CreateProvisionedKey`, `ListProvisionedKeys`, `RevokeProvisionedKey`,
   `InternalProvisionRedemptions`, `InternalRevokeProvisionedRedemptions`, `InternalFundCreditPool`
   read the tenant from context via a small `internalTenantFromContext(c)` helper and DELETE their
   slug lookup + whitelist blocks (six copies, three message strings).
3. Exported `RequireInternalKeyForTenant(c, tenantID string) bool` (same denial exit) called by
   `InternalTopupBalance` and `InternalAdjustQuota` after loading the user (`user.TenantId`).
   `InternalTopupBalance` additionally records a governance row with the existing
   `ActionBillingCredit` (resource user, details amount/reason) — no new action constant.
4. TI-R1: `api-v2-router.go:93` mounts `middleware.TenantSlugGuard()` after `OIDCAuth()` for
   `/:tenant_slug/credit-pool/me`; delete `tenant_credit_pool.go:176-191`; delete the unreachable
   block `v2_log_export.go:87-119` (its route already sits under `tenantLogs.Use(UserAuth, TenantSlugGuard)`,
   `api-v2-router.go:181-182`).
Do NOT change: status codes, `error_code` strings, the `ScopeAll` bypass, `repo.InternalKeyAllowedForTenant`,
rate limiters, any 2xx response shape, `internal/pkg/metrics/metrics.go` (owned by L3).

Files:
`internal/adapter/middleware/internal_api_auth.go`, `internal/adapter/middleware/internal_api_auth_test.go`,
`internal/adapter/middleware/internal_api_auth_integration_test.go`,
`internal/adapter/handler/router/internal-api-router.go`, `internal/adapter/handler/router/internal_seam_guard_test.go` (new),
`internal/adapter/handler/router/api-v2-router.go`, `internal/adapter/handler/router/l4_tenant_slug_shadowing_test.go`,
`internal/adapter/handler/provisioning.go`, `internal/adapter/handler/internal_provisioned_redemptions.go`,
`internal/adapter/handler/internal_credit_pool_fund.go`, `internal/adapter/handler/internal_api.go`,
`internal/adapter/handler/tenant_credit_pool.go`, `internal/adapter/handler/v2_log_export.go`,
`internal/adapter/handler/provisioning_test.go`, `internal/adapter/handler/credit_pool_fund_money_path_test.go`,
`internal/adapter/handler/internal_credit_pool_fund_test.go`, `internal/adapter/handler/fix_provisioning_revoke_test.go`,
`internal/adapter/handler/cov_handler-deploy_provisioning_test.go`, `internal/adapter/handler/cov_handler-money_edge_test.go`,
`internal/adapter/handler/revoke_creator_scoping_test.go`, `internal/adapter/handler/testutil_integration_test.go`,
`internal/adapter/handler/internal_api_test.go`, `internal/adapter/handler/internal_api_integration_test.go`,
`internal/adapter/handler/tenant_credit_pool_enduser_test.go`, `internal/adapter/handler/tenant_credit_pool_admin_extra_test.go`,
`internal/adapter/handler/v2_log_export_test.go`, `internal/adapter/handler/cov_handler-deep-b_logs_test.go`,
`internal/app/governance/audit_action.go`, `internal/pkg/metrics/internal_seam.go` (new), `internal/pkg/metrics/internal_seam_test.go` (new).

Oracle (`go test -count=1 -p 2 ./internal/adapter/middleware/ ./internal/adapter/handler/ ./internal/adapter/handler/router/ ./internal/pkg/metrics/ ./internal/app/governance/`):
- NEW `router/internal_seam_guard_test.go` builds the engine via `SetInternalApiRouter` on SQLite: (a) narrow
  provisioning key with no `internal_api_key_tenants` row → `POST /internal/v1/provisioning/tenants/<slug>/keys`
  403 `TENANT_NOT_AUTHORIZED`, exactly one audit row `internal_key.denied` with details.seam/tenant, counter
  `{seam,reason="tenant"}` +1; (b) no key → 401 + row `reason=missing_key`; (c) wrong scope → 403 + row
  `reason=scope`; (d) `ScopeAll` key → 2xx path unchanged.
- `internal_api_integration_test.go`: balance:write-only key without whitelist row → `POST /internal/balance/topup`
  403 and `repo.GetUserQuota` unchanged; `ScopeAll` key → 200 and one `billing.credit` audit row; same for
  `/internal/quota/adjust`. Existing `TestInteg_Topup_*` stay green.
- Existing `TENANT_NOT_AUTHORIZED` tests (`provisioning_test.go:262`, `credit_pool_fund_money_path_test.go`,
  `fix_provisioning_revoke_test.go`, `cov_handler-deploy_provisioning_test.go`) re-mount the middleware exactly
  as the router does and keep returning 403.
- `tenant_credit_pool_enduser_test.go` TenantMismatch/UnknownSlug still 403/404 with the inline block gone;
  `l4_tenant_slug_shadowing_test.go` gains an assertion that `GET /api/v2/:tenant_slug/credit-pool/me`'s
  handler chain contains `TenantSlugGuard`.

Mutation: restore any one inline guard block and bypass the middleware for that route → its audit/counter
assertion RED; delete `.Use(InternalTenantGuard())` → (a) RED; delete the `RequireInternalKeyForTenant` call in
topup → 403 assertion RED; drop `TenantSlugGuard()` from `api-v2-router.go:93` → l4 test RED.

Enterprise acceptance:
1. A sibling team's key misconfigured for a tenant produces an `internal_key.denied` row visible via
   `GET /api/v2/admin/audit/events` naming key id, seam and tenant — provable by the router test above.
2. `git diff --stat` shows a net deletion in `provisioning.go` + `internal_provisioned_redemptions.go` +
   `internal_credit_pool_fund.go` + `tenant_credit_pool.go` + `v2_log_export.go` of at least 80 lines.
3. Post-deploy live probe (integrator, not this lane): `curl -H 'X-API-Key: bogus' https://hub.lurus.cn/internal/user/1`
   → 401 with `error_code`, and `lurus_gateway_internal_seam_denied_total{reason="invalid_key"}` on the direct
   NodePort `/metrics` increases by 1.

Consumer note (contracts.md:463-475 fund, :481-488/:976 redemptions, :504-517 erasure/provisioning — platform-core):
status codes and `error_code` values unchanged; the three divergent 403 message strings converge to the fund
endpoint's wording; 401/403 bodies gain an additive `error_code`; non-`ScopeAll` keys calling
`/internal/balance/topup` or `/internal/quota/adjust` now need an `internal_api_key_tenants` row for the target
user's tenant (no consumer registered; platform's `lurusapi` client calls only `/internal/currency/*`).
`GET /api/v2/:tenant_slug/credit-pool/me` mismatch message changes to the middleware wording (same code).

Cross-repo follow-up: root `contracts.md` — add `error_code` + `internal_key.denied` audit action to the
provisioning/fund/redemptions rows; add the whitelist rule for topup/adjust.

### L2 — CONTRACT-TAXONOMY: vendor error types at the root, non-empty codes, one renderer, stubs retired

Kind: mixed (fix + consolidate + retire). Effort: L. Candidates: contract:AC-2, contract:AC-2b (cost_spike + dead wrappers), contract:AC-4.

Step 1 (fix, root) — `internal/pkg/types/error.go`:
- Add `wireErrorType(status int, wire string) string`: OpenAI wire 400→`invalid_request_error`,
  401→`authentication_error`, 403→`permission_error`, 404→`not_found_error`, 402→`insufficient_quota`,
  413→`request_too_large`, 429→`rate_limit_error`, 5xx→`api_error`; Anthropic wire identical except
  402→`billing_error`, 529→`overloaded_error`. The OpenAI-wire converter (:185-204) and the Anthropic-wire converter (:226-243) use it for
  gateway-originated errors (`ErrorTypeNewAPIError`) instead of stamping the internal constant; the
  Anthropic-wire converter branch that copies an empty `Code` into `Type` (:229-235) falls back to the mapped type.
  `ErrorCode` string values are NOT changed (`RelayErrorType` and `ShouldDisableChannel` key on them).
- `middleware/utils.go`: `abortWithOpenAiMessage(c, status int, code types.ErrorCode, message string)` —
  mandatory code, compile-forced. Add the missing `ErrorCode` constants in `error.go` for the 35 call sites
  (`auth.go`, `distributor.go`, `model-rate-limit.go`, `business_rate_limit.go`, `concurrency_limit.go`,
  `jimeng_adapter.go`); rewrite the Chinese-only messages in those files to English (request id is already
  appended by `MessageWithRequestId`). `pool_balance_check.go`/`wire_format.go` adjusted only if the renderer
  signature moves.
- Inbound compat: `internal/app/channel.go` keeps every existing Code/Type case; add a round-trip case in
  `r2_channel_token_quota_test.go` for the NEW 402 body and keep the LEGACY body case.
Step 2 (consolidate) — `cost_spike.go:101-107` builds a typed 429 error and calls `renderRejection`; delete
`abortWithMidjourneyMessage` (`utils.go:79-87`) and its test in `oidc_misc_cover_test.go:315-325`; delete
the two dead Anthropic-wire wrappers (`app/error.go:59-82`), the commented block (:32-56) and
the wrapper-only status-code struct in the Anthropic-wire DTO file (`internal/pkg/dto/`, line 444).
Step 3 (retire) — `relay-router.go:199-210`: replace the twelve `RelayNotImplemented` mounts with
`handler.RelayNotFound` (existing native 404, inside the authenticated group so 401-vs-404 ordering is
unchanged); delete the `/v1/files*` and `/v1/fine-tunes*` objects from `docs/openapi/relay.json`
(1879-2228). Leave the `RelayNotImplemented` symbol and `relay_helpers_extra_test.go:200-208` for L3
(owner of `relay.go`) to delete.
Do NOT change: `entitlement.go`, `handler/billing.go` (L4), `handler/relay.go` (L3), app-level token
messages (`token_service.go`, `v2_token.go` — L4), `ErrorCode` values, `ShouldDisableChannel` semantics.

Files:
`internal/pkg/types/error.go`, `internal/pkg/types/error_more_test.go`,
`internal/adapter/middleware/utils.go`, `internal/adapter/middleware/wire_format.go`, `internal/adapter/middleware/auth.go`,
`internal/adapter/middleware/distributor.go`, `internal/adapter/middleware/model-rate-limit.go`,
`internal/adapter/middleware/business_rate_limit.go`, `internal/adapter/middleware/concurrency_limit.go`,
`internal/adapter/middleware/jimeng_adapter.go`, `internal/adapter/middleware/pool_balance_check.go`,
`internal/adapter/middleware/cost_spike.go`,
`internal/adapter/middleware/rejection_envelope_wire_test.go`, `internal/adapter/middleware/business_rate_limit_test.go`,
`internal/adapter/middleware/error_log_middleware_test.go`, `internal/adapter/middleware/r5e_model_rate_limit_default_test.go`,
`internal/adapter/middleware/cost_spike_cover_test.go`, `internal/adapter/middleware/r4_cost_spike_enforce_test.go`,
`internal/adapter/middleware/oidc_misc_cover_test.go`, `internal/adapter/middleware/auth_helper_cover_test.go`,
`internal/adapter/middleware/l3_token_quota_402_test.go`,
`internal/adapter/handler/relay_inband_error_test.go`,
`internal/adapter/handler/router/relay-router.go`, `internal/adapter/handler/router/relay_wire_stamp_test.go`,
`internal/adapter/handler/router/relay_stub_retirement_test.go` (new),
`internal/app/error.go`, `internal/app/error_test.go`, `internal/app/channel.go`, `internal/app/r2_channel_token_quota_test.go`,
the Anthropic-wire DTO file under `internal/pkg/dto/`, `docs/openapi/relay.json`.

Oracle (`go test -count=1 -p 2 ./internal/pkg/types ./internal/adapter/middleware ./internal/app ./internal/adapter/handler ./internal/adapter/handler/router`):
- `rejection_envelope_wire_test.go` (real router + `StampRelayFormat` + `TokenAuth`/`PoolBalanceCheck`):
  `/v1/chat/completions` bad key → 401 body `"type":"authentication_error"` and non-empty `"code"`;
  `/v1/messages` same → `"error":{"type":"authentication_error"`; `/v1/messages` exhausted pool → 402
  `"error":{"type":"billing_error"`. The lock at :93 (`type=new_api_error`) is flipped.
- Mounted `CostSpikeLimit` (enforce, seeded spike) on both wires: `json.Unmarshal` into
  `struct{Error struct{Type, Code string}}` succeeds with non-empty fields.
- `r2_channel_token_quota_test.go`: legacy body `{"type":"new_api_error","code":"token_quota_exhausted"}` and
  the new body both → `ShouldDisableChannel == true`.
- NEW `router/relay_stub_retirement_test.go` (real `SetRelayRouter`, seeded key): `POST /v1/files` → 404 with
  `"type":"invalid_request_error"` and no `api_not_implemented`; `GET /v1/fine-tunes` same.
- `go vet ./internal/app/ ./internal/adapter/middleware/ ./internal/pkg/dto/` clean with the wrappers gone.

Mutation: revert `wireErrorType` in `error.go` → the three envelope assertions RED; restore the raw `gin.H` in
`cost_spike.go` → Anthropic-wire unmarshal RED; re-mount one stub → 404 test RED (501).

Enterprise acceptance:
1. An OpenAI-SDK or Anthropic-SDK caller sees only vendor-taxonomy `type` values and a stable `code` on every
   gateway rejection (proven by the wire test on both wires, no hand-built structs).
2. `grep -rn '"new_api_error"' internal --include=*.go | grep -v _test | grep -v types/error.go` returns 0 lines.
3. Inbound parsing of legacy downstream bodies keeps auto-disable behaviour (round-trip test).

Consumer note: `type` values on relay-wire rejections change from `new_api_error` to the vendor taxonomy,
`code` becomes non-empty, messages become English. switch/lutu: 0 string dependencies (§2); contracts.md:575,578
register status-only behaviour. Downstream newhub instances relaying through us as a channel will now
auto-disable the channel on our 401/403 (`authentication_error`/`permission_error` are in their Type switch)
— intended for bad keys, also fires for IP-allowlist/deprecated-group 403s. `/v1/files*`, `/v1/fine-tunes*`,
`/v1/images/variations`, `DELETE /v1/models/:model` answer 404 `invalid_request_error` instead of 501
(no contracts.md entry; 0 hits in switch/lutu).

Cross-repo follow-up: root `contracts.md` newhub relay row — document the taxonomy table and the non-empty
`code`; platform/creator/lucrum teams that relay through hub should be told 401/403 now carry vendor types.

### L3 — METRICS-HONESTY: delete the lying series, product label + client_gone on the real ones, one wallet-charge metric

Kind: mixed (retire + fix + un-dead). Effort: L. Candidates: observability:OBS-R1, product_line:OBS-P1-PRODUCT-LABEL, billing:BILL-METRIC-1, observability:OBS-R3. Runs after L2.

Step 1 (retire): delete `ChannelHealth`, `ChannelConsecutiveErrors`, `ChannelErrorsTotal` (`metrics.go:190-233`)
and `RecordChannelError`/`SetChannelHealth`/`ResetChannelErrors` (:513-531) with their tests
(`metrics_test.go:68-140`, refs in `internal/app/alpha7_coverage_test.go`); delete `app.RecordDebitSuccess`
(`credit_pool.go:20-25` incl. the false header); delete `deploy/grafana/` (3 files); fix the
`deploy/r6-host-netdata/` claim in `newhub-prometheus-rule.yaml:10` and the "CreditPoolBalanceLow alert fires"
sentence at `metrics.go:309-310`; correct `doc/runbook/pool-threshold-alert.md`, `doc/decisions/observability.md`
(Jaeger "deployed"), `doc/process.md`. NEW gate `internal/pkg/metrics/declared_series_written_test.go`: every
`promauto.New*` var in the package must be referenced by a non-test Go file outside the package (directly or via
an exported `Record*/Set*` helper that is). Extend `alert_wiring_honesty_test.go` to fail when a non-test Go
source names an `alert:` from the remaining rule file in the same sentence as `fires`/`pages`.
Step 2 (un-dead): keep the name `billing_debit_amount_cny`, change labels to `{product, op}`; observe with
`op="debit"` in `DebitWalletGRPC` (:228) AND the HTTP fallback `DebitWallet` (`identity_client.go:447`), and with
`op="settle"` at the successful `SettleWithBreaker` return in `quota.go:1001` (product =
`relayInfo.SourceProduct`). Pool topups keep sending `"newhub"` as product id (that is what platform receives;
`tenant_credit_pool.go` is not touched).
Step 3 (fix): add `product` to `relay_requests_total`, `relay_errors_total`, `relay_total_duration`
(`RecordRelayRequest`/`RecordRelayError`/`RecordRelayTotal`); callers `relay.go:117,156,415` and
`stream_error.go:94` pass `relayInfo.SourceProduct` (resolved once at `relay_info.go:115`).
Step 4 (consolidate): NEW `handler/relay_outcome.go` with one `relayOutcome(err *types.NewAPIError, endReason
string) string` → `success|error|client_gone`; `relay.go:113-115` and `411-413` call it; `RelayErrorsTotal` is
NOT incremented for `client_gone`.
Step 5: delete `RelayNotImplemented` (`relay.go:775-784`, dead after L2) and `relay_helpers_extra_test.go:200-208`.
Do NOT change: metric names, `metrics.go` families other than those listed, `internal/pkg/metrics/internal_seam.go`
(L1), `tenant_credit_pool.go`, `pre_consume_quota.go`, the five untracked `coverage_*` files.

Files:
`internal/pkg/metrics/metrics.go`, `internal/pkg/metrics/metrics_test.go`, `internal/pkg/metrics/alert_wiring_honesty_test.go`,
`internal/pkg/metrics/declared_series_written_test.go` (new),
`internal/app/credit_pool.go`, `internal/app/alpha7_coverage_test.go`, `internal/app/quota.go`, `internal/app/wallet_charge_metric_test.go` (new),
`internal/pkg/common/identity_grpc_client.go`, `internal/pkg/common/identity_client.go`,
`internal/pkg/common/identity_grpc_bufconn_test.go`, `internal/pkg/common/grpc_fallback_test.go`,
`internal/adapter/handler/relay.go`, `internal/adapter/handler/relay_outcome.go` (new), `internal/adapter/handler/relay_outcome_test.go` (new),
`internal/adapter/handler/relay_attribution_test.go`, `internal/adapter/handler/relay_helpers_extra_test.go`,
`internal/app/relay/helper/stream_error.go`, `internal/app/relay/helper/stream_error_test.go`,
`internal/adapter/provider/openai/stream_incomplete_test.go`,
`deploy/grafana/newhub-alerts.yaml` (delete), `deploy/grafana/newhub-slo.json` (delete), `deploy/grafana/newhub-stage.json` (delete),
`deploy/k8s/r6-stage/newhub-prometheus-rule.yaml`, `doc/runbook/pool-threshold-alert.md`, `doc/decisions/observability.md`, `doc/process.md`.

Oracle (`go test -count=1 -p 2 ./internal/pkg/metrics/ ./internal/pkg/common/ ./internal/app/ ./internal/app/relay/helper/ ./internal/adapter/handler/ ./internal/adapter/provider/openai/`):
- `declared_series_written_test.go` passes on the trimmed package; re-adding `ChannelHealth` → RED.
- `relay_attribution_test.go` (real `Relay()` through a sensitive-word rejection): `RelayErrorsTotal{...,"kova"}`
  +1 for header `kova`, `{...,"llm-api"}` +1 for header `bogus`.
- `identity_grpc_bufconn_test.go`: after `DebitWalletGRPC` the gathered family has labels `product`+`op="debit"`;
  `grpc_fallback_test.go`: HTTP fallback observes the same family (RED on HEAD).
- `wallet_charge_metric_test.go`: `PostConsumeQuota` with `PlatformPreAuthID>0` and a stubbed settle seam (same
  shape as `a2_legacy_debit_gate_test.go`) → `{product="switch",op="settle"}` count 0→1 (RED on HEAD).
- `relay_outcome_test.go` table; `stream_incomplete_test.go` (cancelled request context, `StreamEndReason`
  pinned via the deterministic path) → `relay_requests_total{status="client_gone"}` +1 and `status="success"` +0.
- `go vet ./internal/adapter/handler/` clean with `RelayNotImplemented` gone.

Mutation: revert the label plumbing in `relay.go` → kova series stays 0 → RED; remove the settle observation →
settle case RED; revert the resolver → `stream_incomplete_test` RED.

Enterprise acceptance:
1. `/metrics` exposes request/error rate per product (`product` label) and a wallet-charge histogram that moves
   on the production pre-auth→settle path — each proven by a test that drives the real handler/client.
2. No declared series without a production writer (gate), no dashboard for an undeployed Grafana, no comment
   claiming an alert pages someone.
3. Post-deploy live probe (integrator): one real relay with `X-Lurus-Product: switch` → `relay_requests_total{product="switch"}` +1 on the direct NodePort scrape.

Consumer note: `/metrics` is scraped only by the host netdata go.d job (repo instructions file; contracts.md has no newhub
`/metrics` row). Series removed: `channel_health`, `channel_errors_total`, `channel_consecutive_errors`.
`billing_debit_amount_cny` labels change `tenant_id` → `product`,`op`. `relay_requests_total`/`relay_errors_total`/
`relay_total_duration` gain `product`; `status` gains the value `client_gone`.

Cross-repo follow-up: none (no external PromQL consumer; the deleted Grafana JSON was the only reader).

### L4 — PLATFORM-LINK: one account-link rule, one product id, parseable platform-gated rejections

Kind: consolidate. Effort: L. Candidates: billing:BILL-B2, product_line:PRODUCT-ID-SSOT, contract:AC-2b (entitlement + billing.go halves). Runs after L2.

Step 1 (BILL-B2): move `backfillUserAccountLink` (`internal_api_ext.go:649-668`) to
`repo.LinkUserPlatformAccount(userID int, accountID int64) error` in `repo/user.go`, guarded: if
`GetUserByLurusAccountID(accountID)` returns a different user, skip-and-log (never re-bind, mirror
`internal_api_ext.go:677-685`); the transactional `UPDATE ... WHERE lurus_account_id IS NULL` + token backfill
stays as is. Call it best-effort from `OIDCCallback` (`oauth.go:402`, after the resolve) and from the JWT path
(`oidc_auth.go:816-818`, after the upsert); `internal_api_ext.go` calls the moved function. Login never fails on
link errors.
Step 2 (one stamp): `repo.IdentityAccountIDForUser(userID int) int64`; `v2_token.go:258`, `token_service.go:154`,
`repo/token.go:315` call it and delete their inline copies.
Step 3 (SSOT): `v2_billing.go:115,302,326` → `ratio_setting.DefaultSourceProduct`; `billing.go:33` →
`ratio_setting.ResolveSourceProduct(c.Query("product_id"))` (unknown folds to the default, no error);
`entitlement.go`: ask platform for `ratio_setting.ResolveSourceProduct(c.GetHeader("X-Lurus-Product"))` so the
gate and the debit agree per request; `doc/product-integration-guide.md:114` default → `llm-api`.
Step 4 (envelopes): `entitlement.go:90-96` → typed 429 (`code=quota_exceeded`, `upgrade_url` under
`error.metadata` via `ErrOptionWithUpgradeURL`) rendered by `renderRejection`; `billing.go:225,233` → 500 with an
OpenAI-native envelope instead of 200.
Do NOT change: `quota.go` (L3), `auth.go` (L2), `handleSessionFallback`'s two branches (AUTH-1 deferred),
`BILLING_UNIFIED` gating (`quota.go:1039`), `pre_consume_quota.go`, the `X-Lurus-Product` allow-list.

Files:
`internal/adapter/handler/oauth.go`, `internal/adapter/handler/oauth_test.go`, `internal/adapter/handler/oauth_security_test.go`,
`internal/adapter/handler/oidc_callback_link_test.go` (new),
`internal/adapter/middleware/oidc_auth.go`, `internal/adapter/middleware/oidc_auth_test.go`,
`internal/adapter/middleware/oidc_session_fallback_test.go`, `internal/adapter/middleware/session_fallback_account_id_test.go`,
`internal/adapter/handler/internal_api_ext.go`,
`internal/adapter/repo/user.go`, `internal/adapter/repo/token.go`, `internal/adapter/repo/user_platform_link_test.go` (new),
`internal/app/token_service.go`, `internal/app/token_service_identity_link_test.go`,
`internal/adapter/handler/v2_token.go`,
`internal/adapter/handler/v2_billing.go`, `internal/adapter/handler/v2_billing_test.go`,
`internal/adapter/handler/billing.go`, `internal/adapter/handler/cover_r2_billing_test.go`,
`internal/adapter/middleware/entitlement.go`, `internal/adapter/middleware/entitlement_product_test.go` (new),
`internal/adapter/middleware/final2_cover_test.go`, `internal/adapter/middleware/final8_cover_test.go`,
`internal/adapter/middleware/gap_r3_cover_test.go`, `internal/adapter/middleware/middleware_cover_test.go`,
`doc/product-integration-guide.md`.

Oracle (`go test -count=1 -p 2 ./internal/adapter/handler/ ./internal/adapter/middleware/ ./internal/adapter/repo/ ./internal/app/`):
- NEW `oidc_callback_link_test.go` (SQLite, httptest IdP for `oidcTokenPath()`/JWKS as the existing oauth tests
  do, `resolveAccountByZitadelSub`-style seam returning ID 4242): after `OIDCCallback`,
  `repo.GetUserByLurusAccountID(4242)` is the callback user and `app.BuildCleanToken` for that user has
  `IdentityAccountID == 4242`; second case: 4242 already bound to a user in another tenant → original binding
  untouched, login 302 still succeeds. (a) is RED on HEAD.
- `repo/user_platform_link_test.go`: idempotent, never re-binds, tokens with `identity_account_id=0` get
  backfilled with `unlimited_quota=true`.
- `token_service_identity_link_test.go`: the three mint paths produce the same `IdentityAccountID`.
- `v2_billing_test.go`: `TopUpV2` via httptest identity server records `product_id == "llm-api"` on debit and on
  the rollback credit; `cover_r2_billing_test.go`: `GetSubscription` error path → 500 with `error.type`.
- `entitlement_product_test.go`: real `EntitlementCheck` + `X-Lurus-Product: kova` → entitlements request carries
  `kova`; bogus header → `llm-api`; denied entry → 429 body unmarshals into `{Error{Type,Code}}` on both wires.

Mutation: remove the callback link call → (a) RED; restore any `"lurus-api"` literal → RED; restore the string
`error` in `entitlement.go` → unmarshal RED.

Enterprise acceptance:
1. "Who pays for this key" is a function of the account, not the login path: an SSO-only user's first token is
   wallet-linked (proven end-to-end through `OIDCCallback` → `BuildCleanToken`).
2. `grep -rn '"lurus-api"' internal --include=*.go | grep -v _test` returns only the tracing service name and
   entity comments (no billing/product call sites).
3. Integrator reading the guide and the platform ledger sees one id (`llm-api`).

Consumer note: platform `WalletDebit`/`WalletPreAuthorize`/`CreateCheckout` (contracts.md:51,56) receive
`llm-api` instead of the alias (platform already normalises via `CanonicalProductID`, `entity/product.go:26-28`);
wallet charges now also arrive for SSO-linked users previously local-only (switch users authenticating through
newhub OIDC, contracts.md:553, become wallet-billed per product) — no request/response shape changes.
`GET /dashboard/billing/*` (billing.go) returns 5xx on failure instead of 200 (no registered consumer).
`billing.go` no longer honours an arbitrary `product_id` query (folds to the allow-list).
Entitlement 429 moves `upgrade_url` into `error.metadata` (0 hits in switch/lutu).

Cross-repo follow-up: root `contracts.md:56` default id `lurus-api` → `llm-api` and note that SSO users are
wallet-linked; platform may drop the `lurus-api` alias after this lands; `ReportUsage` proto `product_id`
(lurus-proto-go + platform) remains owner-gated; platform seed rows for fable/acest/unica/mira.

### L5 — CONSOLE-ONE-SURFACE: retire legacy topup/redemption shells and dead trees, show product attribution

Kind: retire (+ un-dead). Effort: M. Candidates: console:UX-CONS-1, console:UX-LOG-PRODUCT, product_line:UX-2-PRODUCT-COLUMN.

Step 1 (retire): `App.jsx` routes `/console/topup` → `<Navigate to='/console/v2/billing' replace />` and
`/console/redemption` → `<Navigate to='/console/v2/redemption' replace />`; delete `pages/TopUp/index.js`,
`components/topup/` (incl. `cx_topup.test.jsx`), `pages/Redemption/index.jsx`, `components/table/redemptions/`,
`components/dashboard/` (incl. `cx_wallet_and_gauge.test.jsx`), `hooks/dashboard/`, `pages/ModelDeployment/index.jsx`,
`components/model-deployments/`; repoint `SiderBar.jsx:38-39` and `headerbar/UserArea.jsx:91` to the v2 paths;
update the locks in `z1_App.test.jsx:220,232`, `cx_page_layout.test.jsx:184`, `cx_headerbar_parts.test.jsx:805`;
update the two comments in `pages/v2/Billing/index.jsx:82,664` and `index.test.jsx:329` that describe the legacy shell.
Remove locale keys that only the deleted trees used (en/zh; other locales only if the integrity test demands).
Step 2 (un-dead): `pages/v2/Log/index.jsx` — `source_product` filter (URL-seeded like model/token/type at
213-228, sent as `?source_product=`), a `product` column reading `row.other?.source_product` (empty → the
documented default label `llm-api`, matching the backend fold rule in contracts.md:57), and a per-product strip from
`/logs/stat` `by_product`. New keys in `en.json`/`zh.json`.
Do NOT change: backend routes (`/api/redemption/*`, `/api/user/topup` stay this cycle), `/console/setting`,
`/console/user`, `/console/personal` (e2e and v2 Settings MFA depend on them), `HFShell.jsx`, any `pages/v2/*` other
than Log/Billing comments.

Files:
`web/src/App.jsx`, `web/src/z1_App.test.jsx`, `web/src/components/layout/SiderBar.jsx`,
`web/src/components/layout/cx_page_layout.test.jsx`, `web/src/components/layout/headerbar/UserArea.jsx`,
`web/src/components/layout/headerbar/cx_headerbar_parts.test.jsx`,
`web/src/pages/TopUp/index.js` (delete), `web/src/components/topup/` (delete dir), `web/src/pages/Redemption/index.jsx` (delete),
`web/src/components/table/redemptions/` (delete dir), `web/src/components/dashboard/` (delete dir), `web/src/hooks/dashboard/` (delete dir),
`web/src/pages/ModelDeployment/index.jsx` (delete), `web/src/components/model-deployments/` (delete dir),
`web/src/pages/v2/Billing/index.jsx`, `web/src/pages/v2/Billing/index.test.jsx`,
`web/src/pages/v2/Log/index.jsx`, `web/src/pages/v2/Log/index.test.jsx`,
`web/src/i18n/locales/en.json`, `web/src/i18n/locales/zh.json`.

Oracle (`cd web && bun run test -- src/z1_App.test.jsx src/components/layout src/pages/v2/Log src/pages/v2/Billing src/i18n`,
then `bun run lint`, `bun run eslint`, `bun run check:casing`, `bun run build`):
- `z1_App.test.jsx`: `/console/topup` renders the v2 billing route and `/console/redemption` the v2 redemption
  route (stubs `page-topup`/`page-redemption` removed); reverting the route change mounts the stub → RED.
- `grep -rn "components/dashboard\|hooks/dashboard\|pages/ModelDeployment\|components/model-deployments\|components/topup\|table/redemptions\|pages/TopUp\|pages/Redemption'" web/src --include=*.jsx --include=*.js` → 0 lines.
- `Log/index.test.jsx`: (1) URL `?source_product=kova` → fetch URL contains `source_product=kova`; (2) row with
  `other.source_product:'switch'` renders a cell `switch`, row without it renders `llm-api`; (3) mocked
  `/logs/stat` `by_product` renders one entry per product. All three RED on HEAD.
- i18n integrity test green; `bun run build` succeeds.

Mutation: revert `Log/index.jsx` → the three product cases RED; restore the `/console/topup` legacy element →
z1 RED.

Enterprise acceptance:
1. One billing/redemption surface: no route, sidebar or header link reaches a Semi-UI legacy shell (grep + z1).
2. `git diff --stat web/` net deletion ≥ 9,000 lines.
3. A tenant admin sees which product spent what from the Log page without calling the API by hand.

Consumer note: none in contracts.md (`/console/topup`, `/console/redemption`, `/api/redemption` have no rows;
switch uses its own `/api/v2/switch/user/topup`, contracts.md:575). Dropped admin conveniences: the legacy
redemption search and `DELETE /api/redemption/invalid` button are no longer reachable from the console (the v1
routes remain).

Cross-repo follow-up: none.

### L6 — OPS-AND-TEST-HONESTY: DR path pinned, retired-service rot deleted with a gate, dead pipelines and pseudo-tests removed, session DB isolation

Kind: mixed (fix + retire + un-dead). Effort: L. Candidates: operability:OPS-1, OPS-3, OPS-4, OPS-2 (doc half), testing:TI-1, TI-2, TI-3, security:SEC-C+TI-2, security:SESS-DB.

Step 1 (OPS-1): `deploy-stage.sh` secret write becomes a per-key merge patch that never clears keys it does not
own (`OIDC_CLIENT_ID` is reconciler-owned, `TAVILY_API_KEY` optional) and refuses bootstrap mode without
`OIDC_CLIENT_ID` unless `ALLOW_MISSING_OIDC_CLIENT_ID=1`; both scripts default `HEALTH_URL` to
`https://hub.lurus.cn/api/health`; `secret-template.yaml` lists `OIDC_CLIENT_ID` and drops the "DATA=6" claim;
`deploy/k8s/README.md:7`, `doc/runbook/deployment.md:18`, `staging-deploy.md:139-146` corrected. NEW
`deploy/k8s/deploy_consistency_test.go` (package `k8s_test`, reads the real files by path relative to the
module root): every non-optional `secretKeyRef.key` in `r6-stage/deployment.yaml` appears in the script's write
block and the template; both scripts' default `HEALTH_URL` host is `hub.lurus.cn` and path `/api/health`.
Step 2 (OPS-3 + OPS-2 doc): rewrite `incident-response.md`, `wallet-revert-stranded.md`, `tenant-onboarding.md`
(real 3-step: `POST /api/v2/admin/tenants` → `POST .../credit-pool` → platform fund, plus the `402
pool_not_configured` explanation) for ns `lurus-newhub`, Tailscale host, `deploy/lurus-newhub`; fix `INDEX.md:24`;
delete `scripts/migrate/`; fix defaults in `stage-smoke.sh`, `story-9-1-tier3-audit-drill.sh`,
`pg-restore-drill.sh`, `chaos-drill.sh`, `deploy/single-node/*`; sweep the remaining runbooks in §2's list.
Add `TestNoRetiredIdentifiers` to the same test file scanning `doc/runbook`, `scripts`, `deploy`: forbidden
tokens `deploy/lurus-api`, `app=lurus-api`, `lurus-api-secrets`, `100.98.57.55`, `hub-stage.lurus.cn`,
`\blurus_hub\b`, `staging-environment.md`, `kubectl -n lurus-system` (bare `lurus-system` is a live namespace
and must NOT be matched); an explicit allow-list for the one historical note in `deployment.md`; every
`[x](y.md)` link in `INDEX.md` resolves; `scripts/migrate` absent.
Step 3 (OPS-4): delete `.github/workflows/release.yml`, `.github/workflows/electron-build.yml`, `electron/`;
`TestWorkflowsHaveReachableTriggers` in the same test file: no workflow under `.github/workflows` is tag-only.
Step 4 (TI-3): delete the `handler audit + governance tests` job (`go-ci.yml:76-119`); the race job (:477) already
runs the 14 tests it filtered (`main` is not branch-protected, §2).
Step 5 (TI-1 + SEC-C): remove the `testing.Short()` gate at `privacy_erasure_test.go:119-121`; add the
`user_totps` deletion inside the tokens step of `privacy_erasure.go` (same transaction, no new step constant,
crash-resume cursor unchanged) with the repo helper in `repo/privacy_erasure.go`; fixture seeds one enabled
`UserTOTP` row and asserts 0 after the cascade, idempotent on re-run. Delete `release_test.go:189-296` (five
never-executable tests; hermetic coverage in `cov_handler-deploy_release_test.go`) and the nine skip-bodied tests in
`model_sync_worker_test.go`.
Step 6 (TI-2): delete `TestEntityImport_AuditEvent`, `TestTimeImport_Since`, `TestConstantImport_TaskPlatform`,
the two `BatchSetChannelTag` SQLite skip stubs and the `SumUsedTaskQuota` skip stub; turn
`TestChannelRepo_CountAllTags` into a seeded assertion (2 tags → 2).
Step 7 (SESS-DB): extract `parseRedisSessionTarget(url) (addr, password, db string)` in `cmd/server/main.go`
and call `sessionredis.NewStoreWithDB`; NEW `cmd/server/session_store_test.go`.
Do NOT change: `deploy/k8s/r6-stage/deployment.yaml`, `r6-uat/*`, `newhub-prometheus-rule.yaml` (L3),
`doc/runbook/pool-threshold-alert.md` (L3), `docker-image-main.yml`, `web-ci.yml`, `security-tests.yml`,
the erasure step enumeration/order, any production Go code other than `privacy_erasure.go`, `repo/privacy_erasure.go`, `cmd/server/main.go`.

Files:
`scripts/deploy-stage.sh`, `scripts/stage-rollback.sh`, `scripts/stage-smoke.sh`, `scripts/story-9-1-tier3-audit-drill.sh`,
`scripts/pg-restore-drill.sh`, `scripts/chaos-drill.sh`, `scripts/migrate/` (delete dir),
`deploy/k8s/r6-stage/secret-template.yaml`, `deploy/k8s/README.md`, `deploy/k8s/deploy_consistency_test.go` (new),
`deploy/single-node/.env.example`, `deploy/single-node/docker-compose.yml`,
`doc/runbook/staging-deploy.md`, `doc/runbook/deployment.md`, `doc/runbook/incident-response.md`,
`doc/runbook/wallet-revert-stranded.md`, `doc/runbook/tenant-onboarding.md`, `doc/runbook/INDEX.md`,
`doc/runbook/database.md`, `doc/runbook/ha-deployment.md`, `doc/runbook/industrial-readiness-gated-actions.md`,
`doc/runbook/pg-restore.md`, `doc/runbook/release-download-gate.md`, `doc/runbook/seam-s1-activation.md`,
`.github/workflows/release.yml` (delete), `.github/workflows/electron-build.yml` (delete), `.github/workflows/go-ci.yml`, `electron/` (delete dir),
`internal/lifecycle/privacy_erasure.go`, `internal/lifecycle/privacy_erasure_test.go`, `internal/adapter/repo/privacy_erasure.go`,
`internal/adapter/handler/release_test.go`, `internal/adapter/handler/model_sync_worker_test.go`,
`internal/adapter/repo/sqlite_repo_extra3_test.go`, `internal/adapter/repo/sqlite_repo_extra_test.go`, `internal/adapter/repo/sqlite_repo_test.go`,
`internal/adapter/repo/sqlite_repo_extra5_test.go`, `internal/adapter/repo/sqlite_repo_extra2_test.go`,
`cmd/server/main.go`, `cmd/server/session_store_test.go` (new).

Oracle:
- `go test -count=1 -p 2 ./deploy/k8s/` — consistency, retired-identifier, workflow-trigger tests green.
- `go test -count=1 -p 2 -short -v ./internal/lifecycle/ -run TestExecuteErasure_FullCascade` prints
  `--- PASS: TestExecuteErasure_FullCascade` (RED = `--- SKIP` on HEAD) and the TOTP assertion passes.
- `go test -count=1 -p 2 ./internal/adapter/handler/ -list . | grep -cE 'TestSyncAllChannelModels_MockChannels|TestFetchAndMergeModels_|TestBuildFetchModelsHeaders|TestParseModelResponse|TestListReleases_Integration|TestGetLatestRelease_Integration|TestGetReleaseByID_Integration|TestDownloadArtifact_Integration|TestInitReleaseService'` == 0 while `-list 'TestListReleases_'` ≥ 6.
- `go test -count=1 -p 2 ./internal/adapter/repo/ -list . | grep -cE 'Import_|BatchSetChannelTag|TestTaskRepo_SumUsedTaskQuota'` == 0 and `go vet ./internal/adapter/repo/` clean; `-run TestChannelRepo_CountAllTags` passes and fails if `CountAllTags` returns 0.
- `go test -count=1 -p 2 ./cmd/server/ -run TestParseRedisSessionTarget`: `redis://h:6379/2` → db `2`, `redis://p@h:6379` → db `0`.
- `grep -c 'TestGetAuditEvents|TestGetGovernance|TestListTokensV2_Pagination' .github/workflows/go-ci.yml` == 0 and `grep -c 'go test -short -race -count=1 -timeout=15m ./...' .github/workflows/go-ci.yml` == 1.

Mutation: restore `test-newhub` in either script or drop `OIDC_CLIENT_ID` from the write block → consistency test
RED; restore one retired identifier or `scripts/migrate/main.go` → retired-identifier test RED; restore
`release.yml` → workflow test RED; restore the `Short` gate → SKIP; remove the TOTP delete → cascade test RED;
revert `NewStoreWithDB` → the store test asserting the DB argument RED.

Enterprise acceptance:
1. A DR by the book (script) on a fresh cluster rolls out without `CreateContainerConfigError` and health-checks
   the instance it deployed (pinned by test, not prose).
2. No runbook, script or manifest names a service, host or database retired in 2026-04 (gate).
3. The only delivery path is `docker-image-main.yml` → auto-pin → ArgoCD; no workflow holds `contents:write`
   for a tag that never existed.
4. The PIPL cascade test runs in every CI job and covers TOTP secrets; the repo suite's PASS count contains no
   hollow names.
5. After deploy, sessions live in the DSN's DB (`redis-cli -n 2 KEYS 'session_*'` non-empty, DB 0 stops
   growing) — integrator's live probe; users re-login once.

Consumer note: platform `PurgeAccount` (contracts.md:504,520) — newhub deletes one more table (`user_totps`),
disposition table gains a row, call shape unchanged. Session DB change invalidates every existing browser session
once at deploy (users log in again). No other consumer (contracts.md has no rows for scripts, runbooks, CI jobs).

Cross-repo follow-up: root `contracts.md:520` disposition table — add the `user_totps` row.

### 4.7 Disjointness check

Per-file ownership was enumerated above; shared-package cases resolved by file:
`internal/pkg/metrics/` — L1 owns `internal_seam*.go`, L3 owns `metrics.go`/`metrics_test.go`/`alert_wiring_honesty_test.go`/`declared_series_written_test.go`.
`internal/adapter/middleware/` — L1 `internal_api_auth*.go`; L2 `utils.go`, `wire_format.go`, `auth.go`, `distributor.go`, `model-rate-limit.go`, `business_rate_limit.go`, `concurrency_limit.go`, `jimeng_adapter.go`, `pool_balance_check.go`, `cost_spike.go` + listed tests; L4 `oidc_auth.go`, `entitlement.go` + listed tests.
`internal/adapter/handler/` — L1 provisioning/internal/tenant_credit_pool/v2_log_export + tests; L2 `relay_inband_error_test.go`; L3 `relay.go`, `relay_outcome*.go`, `relay_attribution_test.go`, `relay_helpers_extra_test.go`; L4 oauth/v2_token/v2_billing/billing/internal_api_ext + tests; L6 `release_test.go`, `model_sync_worker_test.go`.
`internal/adapter/handler/router/` — L1 `internal-api-router.go`, `api-v2-router.go`, `l4_tenant_slug_shadowing_test.go`, `internal_seam_guard_test.go`; L2 `relay-router.go`, `relay_wire_stamp_test.go`, `relay_stub_retirement_test.go`.
`internal/app/` — L2 `error.go`, `error_test.go`, `channel.go`, `r2_channel_token_quota_test.go`; L3 `credit_pool.go`, `quota.go`, `alpha7_coverage_test.go`, `wallet_charge_metric_test.go`; L4 `token_service.go`, `token_service_identity_link_test.go`; L1 `governance/audit_action.go`.
`internal/adapter/repo/` — L4 `user.go`, `token.go`, `user_platform_link_test.go`; L6 `privacy_erasure.go` + five sqlite test files.
`doc/` — L3 `runbook/pool-threshold-alert.md`, `decisions/observability.md`, `process.md`; L4 `product-integration-guide.md`; L6 every other runbook listed.
`deploy/` — L3 `grafana/*`, `k8s/r6-stage/newhub-prometheus-rule.yaml`; L6 `k8s/README.md`, `k8s/deploy_consistency_test.go`, `k8s/r6-stage/secret-template.yaml`, `single-node/*`.
No file appears in two lanes.

## 5. Cross-repo follow-ups (carried, not dropped)

| Item | Owner | Trigger |
|---|---|---|
| root `contracts.md:56` default product id `lurus-api` → `llm-api`; SSO users wallet-linked | root governance | after L4 |
| root `contracts.md` provisioning/fund/redemptions rows: `error_code` field, `internal_key.denied` audit action, topup/adjust whitelist rule | root governance | after L1 |
| root `contracts.md` newhub relay row: vendor error taxonomy + non-empty `code` | root governance | after L2 |
| root `contracts.md:520` erasure disposition: `user_totps` row | root governance | after L6 |
| platform: `POST /internal/currency/exchange` is called by `internal_api.go:1121` but unregistered — register it in contracts.md or retire `ExchangeLucToLut` on platform first; then newhub retires `InternalExchangeLucToLut` (BILL-MONEYIN-1) | platform owner | next cycle |
| platform: `lurus-api` alias in `entity/product.go:26-28` may be dropped once L4 is live | platform owner | after L4 deploy |
| lurus-proto-go + platform: `ReportUsage` `product_id` | owner | unchanged from previous cycle |
| platform `003_seed_products`: rows for fable/acest/unica/mira (prerequisite for BILL-C) | owner | unchanged |

## 6. Deferred

| Candidate | Why deferred this cycle |
|---|---|
| observability:OBS-R2 per-replica label | Additive; L3 already reshapes `/metrics`; land pod label once series are honest — next cycle #1. |
| observability:OBS-R4 leader/background liveness | Additive; pairs with OBS-R2 (health.go). |
| billing:BILL-FORMULA-1 cost estimate collapse | Disjoint and M, but six-lane cap; next cycle #2. |
| security:AUTH-1 one session principal resolver | `auth.go` is L2's, `oidc_auth.go` is L4's this cycle; do it on the consolidated code next cycle. |
| console:UX-MODELS-1 tenant-models hook | Locale files owned by L5; additive; next cycle with UX-SETTINGS-HONEST. |
| console:UX-SETTINGS-HONEST | Same locale ownership; M rewrite. |
| tenancy:TI-F2 task/mj tenant scoping | 0 users in non-default tenants live; `repo/user.go` owned by L4; prefer retiring the legacy Task/Midjourney pages next cycle. |
| contract:DOC-1 OpenAPI drift gate | `relay.json` owned by L2; codes/envelopes change in L1/L2/L4 — regenerate once. |
| testing:TI-4 shared SQLite bootstrap | L; touches test files owned by L3/L6. |
| billing:BILL-MONEYIN-1 retire exchange | Platform main tree is a live caller (§2); cross-repo first. |
| product_line:XP-3 switch publish round-trip | Owner has not decided to publish via hub (0 releases). |
| billing:BILL-C allow-list additions | Platform seed rows first. |
| contract:ATTR-1 migration 033 | No migration budget; JSON expression suffices at 24k rows. |
| operability:OPS-2 code half (CreateTenant builds pool) | `tenant_credit_pool.go` owned by L1; pool values stay explicit owner input; runbook half lands in L6. |

## 7. Next cycle

1. OBS-R2 + OBS-R4 (per-replica identity and leader liveness) on top of the honest series.
2. BILL-FORMULA-1 and AUTH-1 — the two remaining "same rule, three copies" consolidations.
3. UX-MODELS-1 + UX-SETTINGS-HONEST + DOC-1 as the integrator-facing batch, regenerating OpenAPI from the
   real route table after this cycle's envelope changes.
4. Retire legacy Task/Midjourney console pages (or scope them, TI-F2) and the v1 `/api/redemption` + `/api/user/topup`
   routes once no e2e depends on them.
5. Re-measure on production after deploy: internal-seam counter non-zero, `relay_requests_total{product}` populated,
   an SSO-only user's token carrying `identity_account_id`, and `redis-cli -n 2` holding sessions — live probes,
   not tests.
