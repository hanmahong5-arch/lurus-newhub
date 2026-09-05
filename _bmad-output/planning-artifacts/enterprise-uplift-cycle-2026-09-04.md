# Enterprise Uplift Cycle — 2026-09-04

Planning artefact for one implementation cycle of `2b-svc-newhub`. Input: eight
dimension assessments (billing, tenancy, observability, contract, console,
testing, operability, security) produced against `main` at `9ec715bb`. Every
claim below that names a file:line and feeds a lane was re-checked on that HEAD
on 2026-09-05; assessment claims that were not re-checked are marked as such in
the backlog and stay out of lanes.

Working-tree note: six untracked files exist on HEAD
(`internal/app/coverage_lift_test.go`, `internal/app/coverage_lift_tokens_test.go`,
`internal/app/coverage_seam2_extra_test.go`, `internal/app/coverage_seam_extra_test.go`,
`internal/app/openrouter_sync/coverage_extra_test.go`,
`internal/adapter/middleware/zz_probe_envelope_test.go`). None belong to this
cycle. Lanes must not stage, edit, delete or depend on any of them. They do
compile today (`go vet ./internal/app/ ./internal/adapter/middleware/
./internal/adapter/repo/ ./internal/adapter/handler/router/ ./internal/pkg/types/`
exited 0 on 2026-09-05), so package-level test runs are not blocked by them.

## 1. Scorecard

| Dimension | Score now | Rationale (compressed) |
|---|---|---|
| Billing | 6.0 | Settlement core is strong (decimal pricing, wire-keyed cache pricing, atomic pre-deduct, settle/release outbox, invariance matrix). Holes sit at the seams between the three ledgers: platform ledger sees 1/5 tokens; the no-pre-auth wallet debit is fire-and-forget with no outbox; a zero-usage settlement leaves the platform hold frozen until TTL; sub-0.00005 LB settlements truncate to 0; no FX layer. |
| Tenancy | 5.5 | Control plane is consistently tenant-scoped (v1/v2 admin handlers, log repo, route-completeness tests). The data plane is tenant-blind: channel selection keys on (group, model) only, and a tenant admin can register a channel in group `default` that then serves other tenants' prompts. Narrow-scope internal keys are tenant-bound on only 6 endpoints; v1 task/MJ admin lists are cross-tenant. |
| Observability | 5.0 | Rich instrumentation and an honesty gate for undeployed rule files, but nothing pages: no health config in repo; incomplete streams count as `success`; `/api/health` says healthy with Redis unreachable; leader-gated money/compliance tasks have zero telemetry. |
| Contract | 6.5 | Handler-stage errors are wire-native and test-locked; every middleware-stage rejection (401/402/429/503) on `/v1/messages` and `/v1beta` still emits the OpenAI envelope with `type:new_api_error`, `code:""`. Anthropic-wire `error.type` never uses the vendor enum. Published OpenAPI spec is stale and untested. |
| Console | 6.5 | No fake status chrome, server-driven payment methods, real 403 states, near-complete i18n. First-run path names models that are string literals rather than the tenant's routable list; live Models page carries a "design-mock" banner over real data; tenant invites have no console surface. |
| Testing | 6.0 | Four go/ast structural gates, PG sentinel, zero-match guards, coverage ratchets. CI runs only `-short` (23 non-short gates never execute, including the erasure cascade proof whose skip text claims CI covers it); 120 web skip-locks with no rot detection (1 already rotted); 5 unconditional-skip pseudo-tests. `main` has no branch protection. |
| Operability | 6.0 | Merge to digest to auto-pin to ArgoCD chain is credible; migrations honest on health/metrics. Fallback deploy/rollback scripts health-check the UAT host while acting on the production namespace; secret inventory disagrees with the manifest (OIDC_CLIENT_ID); page-severity and incident runbooks target the retired service; INDEX links a missing runbook. |
| Security | 6.5 | Hashed internal keys, scope gating, tenant-scoped OIDC fallback, SSRF guard, masked channel keys, tamper-evident audit, resumable PIPL erasure. Live gaps: gin trusts every proxy so `ClientIP()` is caller-chosen (every IP-keyed limiter and the audit IP column are forgeable); sessions never re-validate status/role after disable; erasure leaves TOTP secrets and image prompts. |

## 2. Ranked backlog (enterprise value / effort, live defects first)

Effort scale: S=1, M=2. "Live" = reproducible on the deployed HEAD without
speculative preconditions.

| # | Candidate | Live | Value | Effort | Ratio | HEAD evidence (re-checked 2026-09-05 unless marked) |
|---|---|---|---|---|---|---|
| 1 | tenancy:TI-1 tenant-scope relay channel selection | yes | 2.0 | M | 1.00 | `internal/adapter/repo/channel_cache.go:150-175` selects from `group2model2channels[group][model]` with no tenant term; `internal/adapter/repo/ability.go:96-129` DB path same; `internal/adapter/middleware/distributor.go:136-141` passes no tenant and never checks `channel.TenantId` after selection; `internal/adapter/handler/relay.go:570` re-selects the same way on retry; `internal/app/session_affinity.go:247-273` re-pins by channel id with no tenant check; `internal/adapter/handler/v2_channel.go:313-321` lets a tenant admin create a channel in group `default` whose abilities land in the shared pool via `internal/adapter/repo/channel.go:548-556`. The only tenant guard on the relay path is the sk-key channel override at `distributor.go:62-89`. |
| 2 | security:SEC-1 gin trusted proxies | yes | 1.0 | S | 1.00 | `cmd/server/main.go:326` `engine := gin.New()`; no `SetTrustedProxies`/`TrustedPlatform`/`ForwardedByClientIP` anywhere in non-test Go (grep, 0 hits); gin `v1.12.0` (`go.mod:23`) trusts all by default; host nginx appends `$proxy_add_x_forwarded_for` (`deploy/r6-host-nginx/lurus-newhub.conf:38`, `test-newhub.conf:43`); `ClientIP()` feeds `rate-limit.go:22,74,160`, `auth.go:440` (token IP allow-list), `governance/audit.go:66`, `repo/log.go:428,497`. |
| 3 | testing:TI-1 run non-short tier in CI | yes | 1.0 | S | 1.00 | Not re-checked this cycle (assessment cites `.github/workflows/go-ci.yml:117,242,477,544` and `internal/lifecycle/privacy_erasure_test.go:119-120`). Deferred: verification requires a CI run, which this cycle cannot trigger. |
| 4 | contract:C1 wire-native middleware rejections | yes | 1.5 | M | 0.75 | `internal/adapter/middleware/utils.go:14-30` always writes `{"error":{"message","type":"new_api_error","code":""}}`; `internal/adapter/handler/relay.go:213-233` is the only wire switch and runs only inside the handler; `auth.go:424-429` writes its own OpenAI 402 body; `pool_balance_check.go:71-79,109-117` write raw OpenAI-shaped 402s; `relay-router.go:71-82,232-237` mount TokenAuth/PoolBalanceCheck/rate limits at group level ahead of the wire-specific handlers at `:104-105,:158-161,:249`; no relay-format context key exists in `internal/pkg/constant/`. |
| 5 | contract:C2 Anthropic-enum error.type, non-empty OpenAI code | yes | 0.7 | S | 0.70 | `internal/pkg/types/error.go:226-254` sets the Anthropic-wire `Type` to the internal `errorType` string or upstream OpenAI code; `utils.go:15-17` code defaults to `""`. Shares `utils.go` with lane 3, so deferred to next cycle, first pick. |
| 6 | billing:BL-1 release platform pre-auth on zero-usage settle | yes | 0.5 | S | 0.50 | `internal/app/quota.go:983` `if relayInfo.IdentityAccountID > 0 && totalQuota > 0 {` gates the whole platform phase; `internal/app/relay/compatible_handler.go:413-419` forces `quota = 0` on zero tokens then calls `PostConsumeQuota(relayInfo, quotaDelta, FinalPreConsumedQuota, true)` at `:459`; `internal/adapter/provider/openai/relay-openai.go:269-271` returns `usage, nil` after an incomplete stream so `relay.go:339` (`releasePreConsumedOnFailure`, error-only) never runs; `pre_consume_quota.go:36` states the invariant "every pre-auth MUST be either settled or released". |
| 7 | operability:OPS-1 fallback scripts gate on UAT host | yes | 0.5 | S | 0.50 | Not re-checked this cycle (assessment cites `scripts/deploy-stage.sh:64`, `scripts/stage-rollback.sh:32`). |
| 8 | operability:OPS-2 OIDC_CLIENT_ID secret drift | yes | 0.5 | S | 0.50 | Not re-checked this cycle (`deploy/k8s/r6-stage/deployment.yaml:129-133` vs `secret-template.yaml:3-13`). |
| 9 | tenancy:TI-3 tenant-scope v1 task/MJ lists | yes | 0.5 | S | 0.50 | Not re-checked this cycle (`handler/midjourney.go:423-436`, `handler/task.go:289-306`). |
| 10 | console:C3 stop labelling live data as mock; i18n leaks | yes | 0.5 | S | 0.50 | Not re-checked this cycle (`web/src/components/hifi/WIPBanner.jsx:55-58`, `Models/index.jsx:209-215`). |
| 11 | testing:TI-3 go/ast gate on pseudo-tests | yes | 0.5 | S | 0.50 | Not re-checked this cycle (`handler/model_sync_worker_test.go:253,267,276,371,431`). |
| 12 | operability:OPS-3 rewrite page/incident runbooks | yes | 1.0 | M | 0.50 | Not re-checked this cycle (`doc/runbook/wallet-revert-stranded.md:36-55`, `incident-response.md`). |
| 13 | console:C1 quickstart names a routable model | yes | 1.0 | M | 0.50 | Not re-checked this cycle (`web/src/pages/v2/Dashboard/index.jsx:84`, `Token/index.jsx:155-178`, `Playground/index.jsx:30-43`). |
| 14 | tenancy:TI-2 bind narrow internal keys to tenant whitelist | yes | 1.0 | M | 0.50 | Not re-checked this cycle (`repo/internal_api_key.go:172-184` used at 6 sites only). Requires live key scope verification before rollout. |
| 15 | security:SEC-3 erasure covers TOTP + prompts | yes | 0.4 | S | 0.40 | Not re-checked this cycle (`lifecycle/privacy_erasure.go:98-180`). |
| 16 | observability:OBS-3 alert honesty gate over runbooks | yes | 0.4 | S | 0.40 | Not re-checked this cycle (`metrics/alert_wiring_honesty_test.go:160-175`). |
| 17 | console:C2 tenant invite issuance in console | no (gap) | 0.8 | M | 0.40 | Not re-checked this cycle. |
| 18 | testing:TI-2 web skip-lock rot gate | yes | 0.8 | M | 0.40 | Not re-checked this cycle (1 rotted lock at `cx_oidc_redirect.test.jsx:284`). |
| 19 | billing:BL-3 flag-gated identity fallback | no (policy) | 0.8 | M | 0.40 | Owner sign-off required on flag default; behaviour change for every unlinked token of a linked user. |
| 20 | billing:BL-2 durable no-pre-auth debit | yes | 0.7 | M | 0.35 | `internal/app/quota.go:1059-1084` debit is `AsyncGo`, error only `SysLog`; `billing_outbox.go:18` has only settle/release actions. Entity change on `billing_outbox`. |
| 21 | observability:OBS-1 incomplete streams as SLI failures | yes | 0.7 | M | 0.35 | `relay-openai.go:269-271` returns `usage, nil` after `HandleIncompleteStream`. Touches `handler/relay.go` (lane 1 file), so deferred. |
| 22 | security:SEC-2 session re-validation on disable | yes | 0.7 | M | 0.35 | Not re-checked this cycle (`middleware/auth.go:41,163-177`, `repo/user.go:379-384`). Touches `auth.go` (lane 3 file), so deferred. |
| 23 | observability:OBS-2 honest /api/health + leader telemetry | yes | 0.6 | M | 0.30 | Not re-checked this cycle (`handler/health.go:47-61,77-85`). |
| 24 | contract:C3 OpenAPI drift test | yes | 0.5 | M | 0.25 | Not re-checked this cycle (`docs/openapi/relay.json`). Large spec regeneration diff. |

## 3. Lanes for this cycle

Constraints applied: at most four lanes; each at most M; lanes run
sequentially on the same working tree, so file sets are disjoint (checked by
listing below); every lane names its full-package test runs, an oracle, and
the exact hand mutation that must turn the oracle red. No lane may perform
git operations, touch production/R6, or run the full test suite or the
linter; the integrator does that once at the end.

Disjointness check (files any lane edits or creates):

- Lane 1: `internal/adapter/repo/channel_cache.go`, `internal/adapter/repo/ability.go`, `internal/app/channel_select.go`, `internal/app/session_affinity.go`, `internal/adapter/middleware/distributor.go`, `internal/adapter/handler/relay.go`, `internal/adapter/middleware/tenant_relay_selection_test.go` (new), `internal/app/channel_select_tenant_test.go` (new), `internal/adapter/repo/channel_cache_tenant_test.go` (new)
- Lane 2: `cmd/server/main.go`, `internal/adapter/handler/router/trusted_proxies.go` (new), `internal/adapter/handler/router/trusted_proxies_test.go` (new), `internal/pkg/config/config.go`, `internal/pkg/config/config_test.go`, `.env.example`
- Lane 3: `internal/pkg/constant/context_key.go`, `internal/adapter/handler/router/relay-router.go`, `internal/adapter/middleware/wire_format.go` (new), `internal/adapter/middleware/utils.go`, `internal/adapter/middleware/auth.go`, `internal/adapter/middleware/pool_balance_check.go`, `internal/adapter/middleware/rejection_envelope_wire_test.go` (new), `internal/adapter/handler/router/relay_wire_stamp_test.go` (new)
- Lane 4: `internal/app/quota.go`, `internal/app/pre_consume_quota.go` (comment-only), `internal/app/post_consume_zero_usage_release_test.go` (new)

No file appears in two lanes. Lanes 1 and 4 both edit files in package
`internal/app`, and lanes 2 and 3 both add files to package
`internal/adapter/handler/router`; that is fine sequentially because the
files differ, but each lane must re-run `go vet` on any package another lane
shares before declaring green.

### Lane 1: Tenant-scope relay channel selection (tenancy:TI-1)

Policy embedded (state it in code comments, do not widen it): a channel whose
`TenantId` is `default` (or empty) is platform-shared and may serve any tenant;
a channel owned by any other tenant serves only callers of that tenant. Root
sk-key override semantics at `distributor.go:62-89` are unchanged.

Spec:

1. `internal/adapter/repo/channel_cache.go`: add a tenant parameter to the
   in-memory selection. `GetRandomSatisfiedChannel(group, model string, retry int)`
   becomes `GetRandomSatisfiedChannelForTenant(tenantID, group, model string, retry int)`;
   keep the old name as a thin wrapper passing `""` (meaning "no filter") so
   the diff stays surgical. Filter the candidate id slice at select time via
   `channelsIDM[id].TenantId`: keep a channel if its `TenantId` is
   `""`/`default` or equals `tenantID`. When `tenantID == ""` keep today's
   behaviour. The priority/retry bucketing that follows must run on the
   filtered slice, not the raw one.
2. `internal/adapter/repo/ability.go`: the DB path `GetChannel` gains the same
   tenant parameter; `getChannelQuery` must filter on
   `channels.tenant_id IN ('default','',?)`. The `abilities` table has no
   tenant column, so join through `channels.id`.
3. `internal/app/channel_select.go`: `RetryParam` gains `TenantID string`; both
   `repo.GetRandomSatisfiedChannel` call sites (`:136`, `:174`) pass it.
4. `internal/app/session_affinity.go`: `lookupAffinityChannel` must reject a
   pinned channel whose `TenantId` is neither shared nor the caller's tenant
   (treat as `stale`, drop the binding). Otherwise a binding pinned before
   the fix keeps the hole open for the life of the affinity key.
5. `internal/adapter/middleware/distributor.go:136-141`: resolve the caller
   tenant via `GetTenantContext(c)` (already used at `:76`) and pass
   `TenantID` in the `RetryParam`. Fail closed exactly like `:76-79`: if the
   tenant cannot be resolved on the weighted-selection path, 403; do not
   silently fall back to the unfiltered pool. After selection, assert
   `channel.TenantId` is shared or equal to the caller tenant; if not, 503
   with the existing "no available channel" wording (defence in depth; must
   be unreachable once steps 1 to 4 are correct).
6. `internal/adapter/handler/relay.go:570`: the retry re-selection passes the
   same `TenantID` (read it from the tenant context, not from the previous
   channel).

What NOT to change: the `v2_channel.go` create path (a tenant may still
create channels in group `default`; the fix is at selection), `channel.go`
ability insertion, the `auto` group walk order, pool/quota logic, any file
owned by lanes 2 to 4.

Test packages (run in full, `go test -count=1 -p 2`):
`./internal/adapter/repo/`, `./internal/app/`, `./internal/adapter/middleware/`,
`./internal/adapter/handler/`.

Oracle (new, hermetic SQLite tier, reuse helpers from
`internal/adapter/middleware/tenant_relay_guard_r3_test.go`):

- `TestDistribute_TenantOwnedChannelNeverServesOtherTenant` in
  `internal/adapter/middleware/tenant_relay_selection_test.go`: seed tenants
  A and B; seed channel A1 (`TenantId=A`, group `default`, model `m`,
  weight 1000, priority 0) and channel P1 (`TenantId=default`, same
  group/model, weight 1); enable memory cache and rebuild it; run
  `Distribute()` 50 times behind a tenant-B token context and assert the
  selected `channel_id` is P1 every time (never A1). Variant with ONLY A1
  present: assert HTTP 503 (no channel), not a relay through A1. Positive
  control: tenant-A token can select A1.
- `TestGetRandomSatisfiedChannelForTenant_FiltersForeignTenant` in
  `internal/adapter/repo/channel_cache_tenant_test.go`: same seeding, direct
  call, 100 draws, plus the DB-path twin through `GetChannel` with
  `MemoryCacheEnabled=false`.
- `TestLookupAffinityChannel_ForeignTenantIsStale` in
  `internal/app/channel_select_tenant_test.go`: pre-store an affinity record
  pointing at A1, call with a tenant-B `RetryParam`, assert nil and the
  `stale` outcome.

Mutation (revert by hand, run the three tests, all must go RED): in
`channel_cache.go` replace the tenant filter predicate with `true` (keep
every id) and in `ability.go` drop the `tenant_id IN (...)` clause;
separately, in `session_affinity.go` remove the tenant check. Each of the
three tests must fail on its own mutation; record the three failing outputs.

### Lane 2: Trusted proxies so ClientIP cannot be spoofed (security:SEC-1)

Spec:

1. `internal/pkg/config/config.go`: add `SecurityConfig.TrustedProxies []string`
   loaded from `TRUSTED_PROXIES` via the existing `envStringSlice` helper.
   Default: `10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 127.0.0.0/8, ::1/128, fe80::/10`
   (the pod only ever sees the host nginx / cluster network, per the
   deployment facts in the service instructions file at the repo root). Unit-test the default and the override in
   `config_test.go`.
2. `internal/adapter/handler/router/trusted_proxies.go` (new):
   `func ConfigureTrustedProxies(engine *gin.Engine, cidrs []string) error`
   calling `engine.SetTrustedProxies(cidrs)`, plus one `common.SysLog` boot
   line stating the effective CIDRs. Return the error rather than panicking;
   `main.go` treats a parse error as fatal at boot.
3. `cmd/server/main.go:326`: immediately after `engine := gin.New()`, call the
   helper with `config.Get().Security.TrustedProxies`.
4. `.env.example`: document `TRUSTED_PROXIES` with the default and a one-line
   reason.

What NOT to change: `metricsAuthMiddleware` (keys on header presence, not
`ClientIP`), rate-limit key functions, any deployment manifest (auto-pin
writes those; no explicit env is needed because the default covers the live
topology), any file owned by lanes 1, 3, 4.

Test packages (run in full): `./internal/adapter/handler/router/`,
`./internal/pkg/config/`, plus `go build ./cmd/server` and
`go vet ./cmd/server`.

Oracle: `TestTrustedProxies_SpoofedXFFIgnored` in
`internal/adapter/handler/router/trusted_proxies_test.go`: build `gin.New()`,
apply `ConfigureTrustedProxies(engine, defaults)`, mount a handler that
echoes `c.ClientIP()`; send a request with `RemoteAddr=10.42.0.1:1234` and
`X-Forwarded-For: 203.0.113.9, 198.51.100.7`; assert the echo is
`198.51.100.7` (rightmost untrusted hop). Second case:
`RemoteAddr=203.0.113.50:1234` (untrusted) with the same header: echo is
`203.0.113.50` (header ignored entirely). Third case: same engine, mount an
IP-keyed memory limiter with limit 1 (`GlobalWebRateLimit` with
`common.GlobalWebRateLimitNum=1`, restored after the test, or an equivalent
exported seam) and send two requests from the same `RemoteAddr` with
different leftmost XFF values: second is 429.

Mutation: comment out the `SetTrustedProxies` call inside
`ConfigureTrustedProxies` (leave the function otherwise intact). Case 1 then
echoes `203.0.113.9`, case 2 echoes `203.0.113.9`, case 3 returns 200 twice:
all RED. Record the failing output.

### Lane 3: Wire-native error envelope for middleware-stage rejections (contract:C1)

Spec:

1. `internal/pkg/constant/context_key.go`: add `ContextKeyRelayFormat`.
2. `internal/adapter/middleware/wire_format.go` (new): `StampRelayFormat()`
   middleware sets `ContextKeyRelayFormat` from the request path:
   `/v1/messages` and `/v1/messages/count_tokens` map to the Anthropic-wire
   `types.RelayFormat` value used at `relay-router.go:105`; any path under
   `/v1beta/` that is not under `/v1beta/openai/` maps to the Google-wire
   value used at `relay-router.go:158,249`; everything else maps to OpenAI.
   Also `renderRejection(c, apiErr *types.NewAPIError)` that switches on the
   stamped format exactly like `handler/relay.go:213-233`: Anthropic wire
   renders `{"type":"error","error":<Anthropic-wire converter on *types.NewAPIError, error.go:226>}`, Google wire
   renders the Google-wire frame from the converter in the sibling file of `error.go` in `internal/pkg/types` (status-name twin), default renders
   `{"error":apiErr.ToOpenAIError()}`. The OpenAI branch must reproduce
   today's body for the fields existing locks assert (`type:new_api_error`,
   `code` present, request-id suffix on `message`).
3. `internal/adapter/handler/router/relay-router.go`: register
   `middleware.StampRelayFormat()` as the FIRST `Use` on `relayV1Router`
   (before `TokenAuth` at `:72`), on the `/v1beta` group (before `:233`), on
   the `/v1beta/models` group (`:42-43`) and on the `/v1beta/openai/models` group (`:50-51`, which
   stamps OpenAI). gin snapshots a group's chain at `Group()` time, so the
   stamp must precede every rejecting middleware in the same group.
4. `internal/adapter/middleware/utils.go`: `abortWithOpenAiMessage` builds the
   `types.NewAPIError` it already implies (status + message + optional code)
   and calls `renderRejection`; keep `c.Abort()`, the log line and
   `recordMiddlewareErrorLog` unchanged.
5. `internal/adapter/middleware/auth.go:424-429`: the token-quota 402 goes
   through `renderRejection` with the `apiErr` it already builds (keep the
   request-id suffix and the hint options).
6. `internal/adapter/middleware/pool_balance_check.go:71-79,109-117`: build a
   `types.NewAPIError` (status 402, code `pool_not_configured` /
   `pool_exhausted`, message unchanged) and render through the same switch;
   the OpenAI body must keep `code`, `message`, `tenant_id`, so let the
   OpenAI branch accept an extra map for these two sites rather than
   dropping `tenant_id`.

What NOT to change: `handler/relay.go` (lane 1), `types/error.go` (the
Anthropic enum mapping is contract:C2, deferred), the OpenAI wire `type`
value, `distributor.go` (lane 1; it already calls `abortWithOpenAiMessage`,
so it is covered without edits), the untracked `zz_probe_envelope_test.go`
(do not read, edit or delete it; the new lock below replaces its purpose and
the integrator decides its fate).

Test packages (run in full): `./internal/adapter/middleware/`,
`./internal/adapter/handler/router/`, `./internal/pkg/types/`.

Oracle: `TestMiddlewareRejection_EnvelopeIsWireNative` in
`internal/adapter/middleware/rejection_envelope_wire_test.go`: a gin engine
with groups mirroring `relay-router.go` (`/v1` and `/v1beta` with
`StampRelayFormat()` then `TokenAuth()`), hermetic DB, bad key. Assert:
`/v1/messages` 401 body contains `"type":"error"` and `"error":{` and does
NOT contain `new_api_error`; `/v1beta/models/x:generateContent` 401 body
starts with `{"error":{"code":401` and contains `"status":"UNAUTHENTICATED"`;
`/v1/chat/completions` 401 body still contains `"type":"new_api_error"`.
Second test through the real `PoolBalanceCheck` with an exhausted pool on
`/v1/messages` (seed helpers from `tenant_relay_guard_r3_test.go`): 402 body
has `"type":"error"` and not `new_api_error`; the same on
`/v1/chat/completions` still has `"code":"pool_exhausted"` and `tenant_id`.
Router-side `TestRelayRouter_StampsWireBeforeTokenAuth` in
`internal/adapter/handler/router/relay_wire_stamp_test.go`: build via the
real `SetRelayRouter`, hit `/v1/messages` and
`/v1beta/models/x:generateContent` without a key and assert non-OpenAI
envelopes (proves the ordering in step 3, which a middleware-only test
cannot).

Mutation: in `wire_format.go` make `renderRejection` ignore the stamped
format and always take the default branch. All non-OpenAI assertions in both
test files go RED; the `/v1/chat/completions` assertions stay green (proves
the OpenAI wire is untouched). Record the failing output.

### Lane 4: Release the platform pre-auth when a request settles to zero (billing:BL-1)

Spec:

1. `internal/app/quota.go:983`: keep the existing block for `totalQuota > 0`.
   Add a sibling arm for
   `relayInfo.IdentityAccountID > 0 && totalQuota <= 0 && relayInfo.PlatformPreAuthID > 0`:
   call `releasePlatformPreAuth(relayInfo)`, then set
   `relayInfo.PlatformPreAuthID = 0` (mirror of the settle arm at `:1021`,
   so a later `ReturnPreConsumedQuota` cannot release twice), then one
   `common.SysLog` line naming the pre-auth id and reason `zero_usage`. Do
   NOT report usage or mirror a usage event from this arm (no money moved,
   no tokens).
2. `internal/app/pre_consume_quota.go`: comment-only. The note at `:36` may
   cite the new arm as the zero-usage counterpart. No behavioural change;
   `releasePlatformPreAuth` keeps its own "do not clear" semantics.

What NOT to change: the `localQuotaConsistent`/`advisory` refund branch,
`ReturnPreConsumedQuota`, the outbox entity, `compatible_handler.go`,
provider handlers (the incomplete-stream "deliberately not billed" contract
stands), any file owned by lanes 1 to 3.

Test packages (run in full): `./internal/app/`.

Oracle: `TestPostConsumeQuota_ZeroUsageReleasesPlatformPreAuth` in
`internal/app/post_consume_zero_usage_release_test.go`, following
`TestReleasePlatformPreAuth_FailoverEnqueuesOutbox`
(`pre_consume_extra_test.go:229-251`): `db := setupServiceTestDB(t)`;
`restore := setupOutbox(t, db)`; seed a user with quota so the local refund
succeeds; `relayInfo{UserId, IdentityAccountID: 42, PlatformPreAuthID: 991500, FinalPreConsumedQuota: pre, PlatformGoverned: true}`;
call `PostConsumeQuota(relayInfo, -pre, pre, false)`; assert exactly one
`billing_outbox` row with `action='release'` and `pre_auth_id=991500`, and
`relayInfo.PlatformPreAuthID == 0`. Positive control in the same file:
identical setup with `quota = pre + 10` yields a `settle` row for that id
and no `release` row. Guard: call `ReturnPreConsumedQuota` afterwards and
assert the release row count is still exactly one (no double enqueue).

Mutation: delete the new arm (or change its condition to `false`). The
zero-usage test then finds zero outbox rows and `PlatformPreAuthID` still
`991500`: RED; the positive control stays green. Record the failing output.

## 4. Deferred (with why)

| Candidate | Why deferred this cycle |
|---|---|
| contract:C2 | Shares `middleware/utils.go` with lane 3 and edits `types/error.go`; first pick next cycle once lane 3's renderer exists (mapping then lands in one place). |
| testing:TI-1 | Oracle needs a CI run to prove the non-short tier executes; this cycle cannot push. Next cycle with integrator-owned CI dispatch. |
| operability:OPS-1 | Pure script/doc fix, high ratio, but no runtime risk today (ArgoCD path is primary); batch with OPS-2/OPS-3 into one docs+scripts lane next cycle. |
| operability:OPS-2 | Same batch as OPS-1. |
| operability:OPS-3 | Same batch as OPS-1; needs a `deploycontract` test package that OPS-1/2 will create. |
| tenancy:TI-2 | Requires live verification of the platform-core key's scopes before the guard can ship without breaking the only live consumer. |
| tenancy:TI-3 | Small and mechanical; lane budget exhausted by higher-severity items. Next cycle. |
| console:C1 | Frontend lane; four backend lanes already fill the cycle. Next cycle. |
| console:C2 | Credential-handling UI; needs owner decision on invite TTL defaults. |
| console:C3 | Next cycle, together with console:C1 (same test files). |
| testing:TI-2 | Runtime cost of the double vitest pass needs measuring before wiring into CI. |
| testing:TI-3 | Gate must special-case untracked files it cannot own; wait until the working tree is clean of foreign untracked tests. |
| security:SEC-2 | Edits `middleware/auth.go`, owned by lane 3 this cycle. Next cycle. |
| security:SEC-3 | Small; lane budget. Next cycle with the erasure test file. |
| observability:OBS-1 | Edits `handler/relay.go`, owned by lane 1 this cycle; also benefits from the lane-3 stamp for wire-correct accounting. |
| observability:OBS-2 | Lane budget; no dependency conflicts. Next cycle. |
| observability:OBS-3 | Lane budget; pair with the OPS-3 runbook rewrite. |
| billing:BL-2 | Entity change on `billing_outbox` plus replay semantics; do after BL-1 lands so release and debit share one outbox contract. |
| billing:BL-3 | Owner-gated flag default and rollback path; policy decision, not engineering. |
| contract:C3 | Large spec regeneration diff; do after C1/C2 so the spec is regenerated once. |

## 5. Next cycle

Suggested order once this cycle's four lanes are integrated and green:

1. contract:C2 (S): enum mapping on top of the lane-3 renderer.
2. security:SEC-2 (M): session re-validation in `auth.go`, now free.
3. Operability docs+scripts lane: OPS-1 + OPS-2 + OPS-3 + OBS-3 (one
   `internal/pkg/deploycontract` test package, four small fixes).
4. testing:TI-1 (S) with an integrator-triggered CI run as the oracle.
5. observability:OBS-1 (M): `handler/relay.go` is free and the wire stamp
   exists.
6. tenancy:TI-3 + security:SEC-3 (S+S): both are scope/cascade additions with
   existing test files to extend.
7. Frontend lane: console:C1 + console:C3.

Owner decisions to collect before then: billing:BL-3 flag default;
console:C2 invite TTL defaults; tenancy:TI-2 whether the platform-core key
becomes an explicit all-tenant grant or gets whitelisted.
