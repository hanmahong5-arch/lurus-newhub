# OSS best-practice cycle 6 — 2026-09-09

Scope: `2b-svc-newhub` only. Base: `main` @ `d1351475` (2026-09-09). Mandate: on top of cycle 5
(headroom headers, request/session identity, `/v1/generation` + `/v1/key`, wire-native error
taxonomy, pool reset + alert hook, TTFT histogram, channel-config validation) make the things a
buyer, an SDK author, a browser consumer and an operator touch first *true*: tenant-level model
policy with a typed writer, discovery that equals routing, honest leader/instance signals, bounded
retention batches, a pricing lookup that honours configured discounts, a console that stops
inventing entitlements and revives its dead model picker, and a wire/header directory that browsers
can read and that is locked to the code. Six lanes run sequentially on the same tree by separate
agents; file sets are disjoint (verified in §8). Lane numbering is the binding execution order
(L1 before L2; L1, L2, L3 before L6; L6 last because it documents what the others shipped).

Untouchable (someone else's WIP, never stage/read/delete as ours):
`internal/app/coverage_lift_test.go`, `internal/app/coverage_lift_tokens_test.go`,
`internal/app/coverage_seam2_extra_test.go`, `internal/app/coverage_seam_extra_test.go`,
`internal/app/openrouter_sync/coverage_extra_test.go`. Because these live in `package app`, no lane
adds a test to `internal/app` this cycle; the allow-list logic lands in a fresh package
`internal/app/tenantpolicy` so its oracle cannot be polluted by them.

Hard rules carried by every lane: no git operations, no production/R6 access, no SQL migration
this cycle (root `doc/coord/migration-ledger.md` line 134 still reads `| 033 | (next available) |`,
verified 2026-09-09), `go test -count=1 -p 2 <pkgs>` only (no `./...`, no `-race`, no linter
locally), web gates `cd web && bun run test && bun run lint && bun run eslint && bun run
check:casing` (scripts verified in `web/package.json:52-58`: `lint` = prettier check, `eslint`,
`test` = vitest run, `check:casing`) must stay green for every lane, locale parity en/zh for every
new key (fr/ja/ru/vi carry no `console` namespace — `grep -c '"console"' fr.json` = 1 header hit,
`tier_free` = 0 — and their ratchet ceilings in `web/src/i18n/i18n-integrity.test.js:331-336`
[fr 221, ja 221, ru 221, vi 217] must not move), every hot-path REJECT ships default-observe behind
an env flag with a live-written counter, old error bodies still parse on the inbound side
(`internal/app/channel.go:86-120` keys on `Code`/`Type`, never on message text), no AI model names
or assistant-tool names in any file (the single live upstream model is written `<live-model>`
throughout), every "passed" claim comes with the command and its tail output, `git status --short`
after each lane shows only that lane's files plus the five untouchable files.

How this plan was synthesised: three lens plans (architect / buyer / operator) were scored by two
judges. Summed totals: operator 93.25, architect 87.25, buyer 87.0. The operator plan is the base.
Grafted (valued by both judges): the architect `useTenantModels` lane (dead Playground swap list),
the buyer canonical-pricing-name lane in its unconditional half only (no variant flag), the buyer
tenant-scoped `/v1/models` lane sequenced after the allow-list, and the architect faultsim-free
UAT probe recipe (RetryTimes option + unresolvable-host probe channel) carried into the deferred
retry-exclusion spec. Dropped with the judges' concrete reasons: architect L6-3 (its own
"helper→app is acyclic" claim is only true for non-test deps — `internal/app/r5a_percall_floor_wiring_test.go`
is `package app` and imports `internal/app/relay/helper`, re-verified on d1351475, so
`go test ./internal/app` would fail with an import cycle in test; plus a no-flag rewrite of both
money paths that UAT cannot exercise), architect L6-1 (default-ON retry-loop change with no
production observable — one channel, `RetryTimes` default 0 at `constants.go:108` per the judges —
and an L-sized blast radius across six packages), buyer L1's `BIZ_TPM_MEASURE` half (two new Redis
writes in the settlement funnel every relay passes through, `quota.go:1030-1031`, for a measure
with zero live users and an undecided owner item; its histogram name also broke the
`lurus_gateway` namespace), buyer L6 (bundled attempts headers + error.metadata + three docs into
one L lane), and operator L-D's scheduled-retention half (M-sized job, endpoint, main.go wiring and
runbook for 25k rows and no owner window — shrunk to the batching fix that makes the previous
plan's "batched audit retention" claim true). Operator L-A (English 429) and L-F (CORS + directory
truth) are merged into one contract lane because they share the middleware package and the guide.
Every file:line below was re-opened on HEAD d1351475 during synthesis unless marked "catalog, not
re-opened".

---

## 1. Thesis

Cycle 5 put the vocabulary on the wire; cycle 6 makes it true where consumers meet it. Production
has had zero relay traffic since the cycle-5 deploy (`/metrics` on 2026-09-09 shows no TTFT,
`credit_pool_reset_total`, `credit_pool_alert_total`, `relay_errors_total` or `relay_failover_*`
series; production DB: 0 tokens with rpm/tpm limits, 0 model_rate_limits rows, 4 tenants, 1 channel,
25,134 log rows of which the last 7 days are only the channel probe and errors), so nothing shipped
last cycle has been measured live. This cycle therefore prefers lanes whose value does not depend
on traffic: policy that is observable before it enforces, signals an operator reads on a quiet
cluster, truth fixes in documentation and console, and a batching defect that would bite the moment
retention is switched on. Six lanes, zero migrations, no change to the retry loop, the stream gate
or either settlement formula:

1. **L1 tenant model allow-list** — one `tenant_configs` row, observe-first, typed 403
   `model_blocked` under enforce, a root-only typed endpoint as the sole writer, the two unrouted
   free-form tenant-config handlers deleted and a router-reference lock so they cannot return.
2. **L2 discovery = routing** — `/v1/models` and `/v1/models/:model` answer from the caller's
   tenant-routable (and, under enforce, allow-listed) set; retrieve stops answering 200 from a
   static table for models no channel routes.
3. **L3 operator signals + bounded batches** — `lurus_gateway_leader`, per-LeaderTask last-success,
   `X-Lurus-Instance` + `lurus_gateway_instance_info` via the downward API, `checks.leader` in
   `/api/health` (never degrading a follower), and the `id IN (SELECT … LIMIT ?)` batching that
   both GORM dialects need because they drop `Limit()` on `Delete()`; the audit interval env is
   finally read.
4. **L4 canonical pricing name** — cache-read / cache-write / image ratio getters normalise the
   model name exactly like every model-ratio getter already does; exact key wins; no flag because
   the change can only move a price toward the operator's configured entry.
5. **L5 console truth** — the Settings Subscription tab stops inventing SLA/audit/support tiers and
   the phantom `subscription_plan` block goes; one `useTenantModels` hook replaces four parsers and
   revives the Playground swap list that can never populate in production.
6. **L6 wire-contract truth** — every gateway 429 speaks English with a body (the memory user
   limiter returns none today), CORS exposes the cycle-5 headers and accepts
   `X-Request-Id`/`X-Session-Id`/`X-Lurus-Product`/`traceparent`, `relay.json` 429 rows document
   the `X-RateLimit-Limit`/`Remaining` they already emit, the Scope/Type value sets are locked to
   the middleware literals, the guide's `X-Session-Id` row tells the truth under a doc lock, and
   `.env.example` gains a row for every flag this cycle introduced.

Both cycle-5 residuals have exactly one owner: English 429 messages → L6; CORS exposure → L6.
If the day overruns, L2 and then L4 slip to next cycle without changing any other lane's spec
(L6's one-sentence `/v1/models` note is conditional on L2 having landed).

## 2. Reference pages studied (URLs as published by the projects)

| Project | What was borrowed / compared this cycle | URL |
|---|---|---|
| LiteLLM | team/key `models` allow-list with prefix wildcards, key models gating `/v1/models`, one cost function feeding headers/logs, bounded spend-log retention, attempted-retries header (deferred) | https://github.com/BerriAI/litellm , https://docs.litellm.ai/docs/proxy/virtual_keys |
| Bifrost | virtual-key `allowed_models` → 403 `model_blocked`, `/v1/models` lists only what the key may query, `extra_fields.provider` (deferred) | https://github.com/maximhq/bifrost |
| Portkey AI Gateway | retry/fallback policy as data, retry-attempt-count header (deferred) | https://github.com/Portkey-AI/gateway |
| OpenRouter | `error.metadata.provider_name` (deferred), model variants priced as the base model (variant half deferred), header directory readable by browsers | https://openrouter.ai/docs |
| Helicone | `Helicone-Session-Id` stored and filterable — the truth the guide row must state | https://github.com/Helicone/helicone |
| Langfuse | `sessionId` ≤200 US-ASCII stored; data retention never deletes without an explicit policy (3-day floor) | https://github.com/langfuse/langfuse |
| Kong AI Gateway | English 429 message carrying the numbers and the stable code; `ai-rate-limiting-advanced` headroom headers are only useful when exposed | https://docs.konghq.com/hub/kong-inc/ai-rate-limiting-advanced/ |
| Higress | `rejected_msg` English 429; observe vs deny for policy | https://github.com/alibaba/higress |
| OpenTelemetry semantic conventions | `service.instance.id` is a required resource attribute; pod name via the downward API is the recommended source | https://opentelemetry.io/docs/specs/semconv/resource/ |
| Kubernetes downward API | `POD_NAME` / `POD_NAMESPACE` via `fieldRef` | https://kubernetes.io/docs/concepts/workloads/pods/downward-api/ |
| gin-contrib/cors | `Config.ExposeHeaders` / `AllowHeaders` (mechanism for L6) | https://github.com/gin-contrib/cors |
| GORM dialects (module cache, re-opened) | `gorm.io/driver/postgres@v1.5.11 postgres.go:77` `DeleteClauses: {DELETE, FROM, WHERE}` (+`RETURNING` at :83); `github.com/glebarez/sqlite@v1.11.0 sqlite.go:61` `{DELETE, FROM, WHERE, RETURNING}` — neither renders `LIMIT` on delete | https://github.com/go-gorm/postgres , https://github.com/glebarez/sqlite |
| One API / New API (upstream base) | canonical billing identities for model variants (variant half deferred); log retention knob | https://github.com/songquanpeng/one-api , https://github.com/QuantumNous/new-api |

## 3. Capability scorecard (previous → now) vs best-of-breed

Live-data caveat: every "now" score is a code-reading score. Production has zero relay traffic
since the cycle-5 deploy and UAT has one TTFT sample from the cycle-5 probe, so no cycle-5
observability or governance claim has been measured live. "Expected after" is an expectation,
re-scored next cycle against live data.

| Area | previous → now | Best-of-breed | Evidence on d1351475 (opened this synthesis unless noted) | Lanes | Expected after |
|---|---|---|---|---|---|
| routing_resilience | 6 → 6 | Portkey / LiteLLM / Bifrost: failed target removed from the pool on retry, backoff with jitter, attempts/model-id headers | Retry loop `relay.go:377` re-draws from the same tier; `use_channel` written at `relay.go:548-550` and read back only at `:514` / `:775` (never by selection); no sleep/backoff (catalog); stream-safety gate ahead of every retry `relay.go:662` / `:937`; breaker fed only by upstream failures `relay.go:491` via `types.IsUpstreamFailure` (`error.go:498`) | none (NH6-05/06/07 deferred with the faultsim-free probe recipe) | 6 |
| governance_keys_budgets | 5.5 → 6.5 | LiteLLM budgets with reset windows + team model access; Bifrost `allowed_models` → 403 | Token-level allow-list only (`distributor.go:92-109`, 403 `model_blocked` at :97/:107); tenant context available in Distribute (:156-158); `tenant_configs` helpers `tenant_config.go:117/:175/:190`; `tpm_limit` still meters settled quota (`quota.go:1030-1031`) — owner item, not this cycle | L1, L2 | 7 |
| billing_cost_transparency | 6 → 6.5 | OpenRouter cost on every wire + `/generation`; LiteLLM one cost function | `/v1/generation` shipped (cycle 5). Cache/cache-write/image ratio getters look up the raw name (`cache_ratio.go:149-165`, `model_ratio.go:816-824`; `grep -c FormatMatchingModelName cache_ratio.go` = 0; control: `model_ratio.go:424,462,498,586,735,745,756,764` normalise); three hand-copied quota formulas (deferred NH6-04) | L4 | 6.75 |
| observability | 5.5 → 6.5 | Helicone/Langfuse session filters in UI + export; OTel `service.instance.id` mandatory | Row identity + backend filters shipped (cycle 5; `v2_log.go:129/:218`). No leader signal (`grep leader health.go` = 0; `internal/pkg/metrics` directory listing has no leader/instance file), `LeaderTask.Run` swallows results `leader_election.go:172-173`, `/metrics` served bare `router/main.go:37`, no `POD_NAME` in either manifest (`env:` blocks at r6-stage :68 / r6-uat :72, `grep fieldRef` = 0) | L3 | 7 |
| security_guardrails | 6 → 6 | Kong / Higress / Bifrost tenant-level policy with observe vs deny | No tenant model policy (see governance); typed 403 `model_blocked` exists (`types/error.go:113`); dead free-form tenant-config writers `tenant.go:467/:498` routed nowhere (`grep GetTenantConfigs\|UpdateTenantConfig router/ cmd/` = 0; control `handler.GetTenant` at `api-v2-router.go:364`) | L1 | 6.5 |
| api_compat_dx | 5 → 6.5 | OpenRouter / LiteLLM: header directory readable by browsers, models list = routable list, English machine-readable messages | CORS has no `ExposeHeaders` (`cors.go:9-24`; grep internal+web/src = 0; control `AllowHeaders` :19); gateway 429s Chinese (`business_rate_limit.go:393-403`, `concurrency_limit.go:205-216`, `model-rate-limit.go:200/:229`) and the memory user limiter aborts with no body (:270-277/:283-289); `/v1/models` tenant-blind (`model.go:173/:181` → `ability.go:31-36`) while routing is tenant-scoped (`ability.go:59-67`); `RetrieveModel` answers from the static map `model.go:275`; Playground swap list dead (`Playground/index.jsx:159-170` maps an object as an array; test :378-390 mocks an array shape); guide `X-Session-Id` row `:154` says 不落库,不回显 while `relay_info.go:434-444` + `governance.go:117` store it raw | L2, L5, L6 | 7.5 |
| operations_config | 6.5 → 6.5 | LiteLLM scheduled bounded retention; Langfuse explicit policy | `DeleteOldLog` `repo/log.go:794` uses `Limit(limit).Delete` at :803 (dropped by both dialects — module cache re-opened), only caller `handler/log.go:256`; `repo/audit.go:82-84` same shape; `AUDIT_CLEANUP_INTERVAL_SECONDS` exists only in the comment `audit_cleanup.go:18` (control `CREDIT_POOL_RECONCILE_INTERVAL_SECONDS` read at `credit_pool_reconcile.go:389`) | L3 (batching + interval only) | 7 |

## 4. Stale citations corrected from the previous plan (§9/§12 of cycle 5, HEAD 1bd6a20f → d1351475)

| Previous citation | Now | Note |
|---|---|---|
| `relay.go:610,885` (RecordFailoverSuppressed) | `relay.go:662` (in `shouldRetry` :646) and `:937` (in `shouldRetryTaskRelay` :929) | re-opened |
| `relay.go:462,478` breaker; `error.go:404` IsUpstreamFailure; `:445-461` RelayErrorType | `relay.go:491` `channelBreakers.RecordFailure`; `types/error.go:498` `IsUpstreamFailure`; `:539` `RelayErrorType` | re-opened |
| `relay_outcome.go:46-61` | catalog: `:25-33` / `:62-82` | catalog, not re-opened |
| `perception.go:134-151` SetPerceptionHeaders | `perception.go:133`; `ComputeLurusExtension` :95; `EstimateQuotaFromUsage` :21 | re-opened |
| `log_other_projection_lock_test.go:340` | `:359` `TestOtherProjectionIsFullyClassified` | re-opened |
| `relay.go:281-308` sensitive-word rejection | `relay.go:331-337` (`ErrorCodeSensitiveWordsDetected` at :335) | re-opened |
| `business_rate_limit.go:224-227,126-133,257-275` | catalog: `:355-366` / `:129-137` / `:409-431`; `bizReject` is `:387-405` | bizReject re-opened, rest catalog |
| `faultsim.go:61-67` | `FaultSimEnabled` `:48`, `faultSimAuthorized` `:85` | re-opened |
| `quota.go:944-960` TPM funnel | `quota.go:1030-1031` (`RecordBusinessTPMUsage` / `RecordBusinessTPMModelUsage`) | re-opened |
| `helper/common.go:59-64` X-Model-Provider | `helper/common.go:63` | re-opened |
| `repo/log.go:738` DeleteOldLog | `repo/log.go:794` (Limit().Delete at :803) | re-opened |
| `repo/log.go:451` RecordConsumeLog | `repo/log.go:500` (metrics.RecordTokens at :506) | re-opened |
| C21 route `/api/v2/:slug/admin/tenants/:id/model-allowlist` | the root admin group is `/api/v2/admin` (`api-v2-router.go:357-358` `RootJWTAuth`), tenants at `:359-376`; model-limits trio at `:374-376` | re-opened; the operator plan's `:373-375` was one line off |
| §9 "Leader-only batched audit retention — untouched" | leader-only is true (`audit_cleanup.go:41/:51`) but **batched is false**: `repo/audit.go:82-84` `Limit(limit).Delete` is dropped by `postgres.go:77/:83` and `sqlite.go:61` | L3 makes it true instead of restating it |
| §12 "C20 … `DeleteOldLog` relies on `Limit().Delete()` which the PG dialect drops" | both dialects drop it (SQLite too), so the hermetic tier can prove the fix by statement count | L3 |
| §3 `relay.go:178-200` Retry-After | catalog: `:185-197` / `:199-207` | catalog, not re-opened |
| Architect L6-3 "`go list -deps ./internal/app` contains no relay/helper" | true for non-test deps only; `internal/app/r5a_percall_floor_wiring_test.go` (`package app`) imports `internal/app/relay/helper` | lane dropped (import cycle in test) |

## 5. Ranked catalog (evidence, source projects, disposition)

Scores are the catalog's `score_delta` (expected capability movement). Disposition: SHIP-Ln, DEFER (spec in §12), OWNER (§11).

| # | id | Title | Source projects | Evidence on d1351475 | Δ | Disposition |
|---|---|---|---|---|---|---|
| 1 | NH6-08 | Per-tenant model allow-list in `tenant_configs`, observe-first, typed 403, typed root endpoint as sole writer; dead tenant-config handlers deleted | Bifrost `allowed_models`; LiteLLM team models + wildcards | `distributor.go:44-47` (`original_model` set, then `if ok {` pinned branch), `:92-109` token gate, `:156-158` tenant context; `tenant_config.go:117/:175/:190`; `tenant.go:467/:498` unrouted; `tenant_model_limits.go:24-36` guard pattern; `credit_pool_reset.go:58-63` env pattern | 0.5 | SHIP-L1 |
| 2 | NH6-05 | Retry never re-dials the channel that just failed | LiteLLM cooldown; Portkey ordered fallbacks | `relay.go:377` loop; `use_channel` `:548-550`; no exclusion in selection (catalog grep) | 0.5 | DEFER (judges: default-ON routing change, no production observable; probe recipe carried) |
| 3 | NH6-11 | Scheduled batched log retention + audit batching + interval env | LiteLLM retention; Langfuse policy | `repo/log.go:803`, `repo/audit.go:82-84`, `audit_cleanup.go:18` comment-only env; dialects drop LIMIT | 0.4 | SHIP-L3 (batching + interval) / DEFER (scheduled job, metrics, endpoint) |
| 4 | NH6-10 | TPM in tokens via parallel window | Kong tokens_count_strategy; Higress; LiteLLM | `quota.go:1030-1031` records quota; owner item §11.5 undecided; prod tokens with tpm>0 = 0 | 0.4 | DEFER (both judges) |
| 5 | NH6-01 | One `useTenantModels` hook; dead Playground swap list | reference consoles' one-hook-per-endpoint | `v2_models.go:108-116` returns `{items,…}`; `Playground/index.jsx:159-170` maps the object; test `:378-390` mocks an array; parsers `Models/index.jsx:100/:124`, `CommandPalette/index.jsx:89` + `:113` | 0.3 | SHIP-L5 (graft) |
| 6 | NH6-02 | CORS expose/allow | gin-contrib/cors; Helicone; Kong | `cors.go:9-24` no ExposeHeaders; mounts `api-router.go:14/:100`, `api-v2-router.go:18`, `dashboard.go:14`, `relay-router.go:14` | 0.3 | SHIP-L6 |
| 7 | NH6-03 | Canonical pricing name for cache/cache-write/image ratios (+ variant fallback) | New API canonical identities; OpenRouter variants | `cache_ratio.go:149-165`, `model_ratio.go:816-824` raw lookups; `:902-920` `FormatMatchingModelName`; sole production caller `helper/price.go:83-90` | 0.3 | SHIP-L4 (normalisation half, no flag) / DEFER (variant fallback) |
| 8 | NH6-04 | One token-quota core + third matrix column | LiteLLM one cost function | `perception.go:21` estimate; settlement paths `quota.go:297`, `compatible_handler.go` (catalog) | 0.3 | DEFER (import cycle in test, both money paths, hermetic-only) |
| 9 | NH6-06 | `X-Lurus-Attempts` / `X-Lurus-Upstream-Model` + 429 directory truth lock | LiteLLM / Portkey attempts headers; Kong 429 tables | `relay_info.go:73-74` `UpstreamModelName`/`IsModelMapped`; `openapi_contract_lock_test.go:198` checks only Retry-After+Scope (`:230-234`); Scope/Type literal sites listed in L6 | 0.3 | SHIP-L6 (directory/lock half) / DEFER (header code half, rides NH6-05) |
| 10 | NH6-12 | Leader gauge, per-task last-success, `checks.leader` | OTel semconv | `common/leader.go:23-25`; `leader_election.go:172-173`; `health.go:102-107` intentional-off map | 0.3 | SHIP-L3 |
| 11 | NH6-13 | Per-replica identity (POD_NAME, instance_info, X-Lurus-Instance) | OTel semconv; k8s downward API | `router/main.go:37`; `leader.go:37-43` NodeHolderID random suffix; manifests `env:` at :68 / :72 | 0.3 | SHIP-L3 (header + info gauge) / DEFER (opt-in per-series label) |
| 12 | NH6-07 | `error.metadata` on upstream failures | OpenRouter; Bifrost | `types/error.go` Metadata forwarded unmasked (catalog `:296-303`) | 0.2 | DEFER (rides NH6-05/06) |
| 13 | NH6-09 | `/v1/models` + retrieve from the tenant-routable set | Bifrost; LiteLLM | `model.go:173/:181` → `ability.go:31-36`; `abilityTenantScope` `:59-67`; `RetrieveModel` `:273-275`; `GetGroupEnabledModels` callers: `model.go:173/:181`, `user.go:336` | 0.2 | SHIP-L2 (graft, after L1) |
| 14 | NH6-16 | Settings Subscription tab stops inventing tiers | LiteLLM UI; newhub no-fabrication rule | `Settings/index.jsx:93-121` `ENTITLEMENT_BY_GROUP`, `:357-366` billing-summary probe, `:979-980` render, `:1037-1046` upgrade button "Wave B"; `Billing/index.jsx:816-824`; `grep subscription_plan --include=*.go internal` = 0; `identity_client.go:705` `BillingSummary` | 0.2 | SHIP-L5 |
| 15 | NH6-14 | Log page request_id/session_id filters | Helicone; Langfuse | backend filters `v2_log.go:129/:218`; console has none (catalog) | 0.2 | DEFER |
| 16 | NH6-18 | v2 redemption PUT | legacy PUT semantics | `api-v2-router.go:204-210` GET/POST/DELETE only (task facts) | 0.2 | DEFER |
| 17 | NH6-17 | Guide `X-Session-Id` row truth + doc lock | Helicone; Langfuse | guide `:154` 不落库,不回显 vs `relay_info.go:434-444`, `governance.go:117`, `classification.go:74` TierPublic, `v1_generation.go:97`, `v2_log.go:129/:218` | 0.1 | SHIP-L6 |
| 18 | NH6-20 | English 429 bodies + body on the memory limiter | Kong; Higress | `business_rate_limit.go:387-405`, `concurrency_limit.go:203-218`, `model-rate-limit.go:197-201/:226-230/:270-277/:283-289` | 0.1 | SHIP-L6 |
| 19 | NH6-15 | CSV export filter parity | LiteLLM; Helicone | `v2_log_export.go` (catalog) | 0.1 | DEFER (with NH6-14) |
| 20 | NH6-19 | `tokens_processed_total` cache types | LiteLLM; OpenRouter | `repo/log.go:506` RecordTokens | 0.1 | DEFER |

## 6. Lanes (execution order is binding)

Common to every lane: run only the listed packages with `go test -count=1 -p 2`; run `go vet` on
each package touched; record the RED run on HEAD before the GREEN run; run the mutation list and
paste the red output; end with `git status --short`.

### L1 — Per-tenant model allow-list (observe-first, typed 403 under enforce) with a root-only typed endpoint as the sole writer; unrouted tenant-config handlers deleted

- kind: mixed (borrow + un-dead removal) · candidates: NH6-08 · effort: M
- Borrowed vocabulary: Bifrost virtual-key `allowed_models` → 403 `model_blocked`; LiteLLM team
  models with trailing-`*` prefix wildcards. The JSON field is `allowed_models`.

**Files**
- `internal/app/tenantpolicy/allowlist.go` (new package)
- `internal/app/tenantpolicy/allowlist_test.go`
- `internal/adapter/middleware/distributor.go`
- `internal/adapter/middleware/distributor_tenant_allowlist_test.go`
- `internal/pkg/metrics/tenant_policy.go`
- `internal/pkg/metrics/tenant_policy_test.go`
- `internal/adapter/handler/tenant_model_allowlist.go`
- `internal/adapter/handler/tenant_model_allowlist_test.go`
- `internal/adapter/handler/tenant.go`
- `internal/adapter/handler/cover_r2_billing_test.go`
- `internal/adapter/handler/router/api-v2-router.go`
- `internal/adapter/handler/router/handlers_are_routed_test.go`
- `internal/adapter/handler/router/v2_completeness_test.go`

Test packages: `./internal/app/tenantpolicy/`, `./internal/adapter/middleware/`, `./internal/pkg/metrics/`,
`./internal/adapter/handler/`, `./internal/adapter/handler/router/`.

**Spec**
1. Policy row (no migration): `tenant_configs` `config_key='models.allowlist'`, `config_type`
   json (the repo's JSON type constant), value = JSON array of ≤256 entries, each ≤128 bytes,
   either an exact model name (compared after `ratio_setting.FormatMatchingModelName`,
   `model_ratio.go:902`, exactly as the token gate does at `distributor.go:104`) or a prefix ending
   in `*`. Key absent = unrestricted; `[]` = deny all (stated on the endpoint's doc comment and in
   the guide row L6 writes).
2. `tenantpolicy.LoadModelAllowlist(tenantID) (list []string, configured bool, err error)` via
   `repo.GetTenantConfigJSON` (`tenant_config.go:117`); `gorm.ErrRecordNotFound` → `configured=
   false, nil`; any other error → `configured=false, err` (caller fails OPEN and logs — same
   posture as the rate limiters). Process-local TTL cache 30 s keyed by tenantID with
   `Invalidate(tenantID)`. `tenantpolicy.ModelAllowed(list, model) bool`.
   `tenantpolicy.Mode()` reads `TENANT_MODEL_ALLOWLIST_MODE` fresh per call like
   `creditPoolResetModeEnv` (`credit_pool_reset.go:58-63`): only the literal `enforce` enforces,
   anything else (including `Enforce`, empty, garbage) observes.
3. `internal/pkg/metrics/tenant_policy.go` (own file, reusing the `namespace`/`subsystem` consts
   at `metrics.go:9-12`): `lurus_gateway_tenant_model_denied_total{tenant_id,action}` CounterVec
   + `RecordTenantModelDenied(tenantID, action)`; the middleware is the production writer so
   `declared_series_written_test.go:146` (`TestDeclaredSeriesHaveAProductionWriter`) stays green.
4. `distributor.go`: ONE block inserted after `c.Set("original_model", …)` (`:44-46`) and before
   `if ok {` (`:47`) so both the pinned-channel path (`:47-90`) and the selection path (`:91+`) are
   covered. Resolve `tc, err := GetTenantContext(c)` (`oidc_auth.go:1167`, the same call the
   selection path makes at `:156`); if `tc == nil` or `tc.TenantID == ""` or `modelRequest == nil`
   → skip. `configured && !ModelAllowed` → `RecordTenantModelDenied(tenantID, observed|enforced)`;
   enforce → `abortWithOpenAiMessage(c, http.StatusForbidden, "Model <m> is not allowed for this
   tenant", string(types.ErrorCodeModelBlocked))` (`utils.go:17`; the existing code constant
   `types/error.go:113`, so the `relay.json` enum lock and `abort_code_structural_test.go:29`
   stay green) and return; observe → one `common.SysLog` line and continue.
5. Handlers (`handler/tenant_model_allowlist.go`): `GET/PUT/DELETE
   /api/v2/admin/tenants/:id/model-allowlist` registered directly after the model-limits trio
   (`api-v2-router.go:374-376`) inside `tenantMgmt` under `adminRoute` (`RootJWTAuth`, `:357-358`).
   Tenant existence guard copied from `requireModelLimitTenant` (`tenant_model_limits.go:24-36`,
   `default` carve-out included). PUT body `{"allowed_models":[…]}` validated (trimmed, no empty
   strings, no duplicates after normalisation, ≤256, each ≤128 bytes) → `repo.SetTenantConfigJSON`
   (`:175`) + `Invalidate`; DELETE → `repo.DeleteTenantConfig` (`:190`) + `Invalidate`; GET →
   `{configured, allowed_models, mode}`. PUT response carries `"propagation_seconds": 30` (other
   replicas converge within the cache TTL).
6. `v2_completeness_test.go`: add the three routes to the RootJWTAuth exempt block (`:113-126`
   pattern, same wording as the model-limits entries at `:124-126`). If the sweep instead
   classifies the by-id PUT/DELETE as mutations, add swept entries naming
   `TestTenantModelAllowlist_UnknownTenant404` — never a bare exemption.
7. Dead code: delete `GetTenantConfigs` (`tenant.go:467`) and `UpdateTenantConfig` (`:498`) —
   routed nowhere (`grep -rn 'GetTenantConfigs\|UpdateTenantConfig' internal/adapter/handler/router/
   cmd/` = 0; control `handler.GetTenant` at `api-v2-router.go:364`) — and delete their only test
   `TestR2Bill_TenantConfigs` (`cover_r2_billing_test.go:460-520`). Keep `repo.UpdateTenantConfig`
   / `repo.ListTenantConfigs` (repo layer; `sqlite_repo_extra2_test.go:246` uses the former). The
   five untouchable files do not reference the two handlers (grep over `*_test.go` = only
   `cover_r2_billing_test.go` and `sqlite_repo_extra2_test.go`, re-run in the lane).
8. New `router/handlers_are_routed_test.go`: parse `handler/tenant.go`,
   `handler/tenant_model_limits.go`, `handler/tenant_model_allowlist.go` with `go/parser`, collect
   exported funcs whose sole parameter is `*gin.Context`, assert each appears as
   `handler.<Name>` in a non-test file under `router/`; `t.Fatal` if 0 handlers were scanned.
9. Do NOT change: the token-level gate (`:92-109`), the 404/503 narrowing (`:177-195`),
   `abilityTenantScope`, any error code, `.env.example` or the guide (L6 documents
   `TENANT_MODEL_ALLOWLIST_MODE` and the 403 row). No migration.

**Oracle**
- `go test -count=1 -p 2 ./internal/adapter/middleware/ -run TestDistribute_TenantAllowlist`:
  `setupCoverDB` (`cover_helpers_test.go:39`) + `mountDistribute` (`distributor_cover_test.go:287`)
  with `c.Set("tenant_context", …)` for tenant `t-allow` and a channel/ability seeded for model
  `m1` via `seedTenantRelayChannel` (`tenant_relay_selection_test.go:30`). (a) no row → 200,
  counter delta 0; (b) `repo.SetTenantConfigJSON("t-allow","models.allowlist",["other-*"])` +
  default mode → 200 and `{t-allow,observed}` +1; (c) `t.Setenv(TENANT_MODEL_ALLOWLIST_MODE,
  "enforce")` → 403 `error.code=model_blocked`, `error.type=permission_error` on
  `/v1/chat/completions` and `/v1/messages`; (d) `["m*"]` under enforce → 200; (e) pinned path
  (`c.Set("specific_channel_id", …)`) under enforce → 403; (f) `[]` under enforce → 403, under
  observe → 200 + counter; (g) `TENANT_MODEL_ALLOWLIST_MODE=Enforce` (capitalised) → observes.
  RED on HEAD: (b) counter 0, (c)/(e)/(f) 200.
- `./internal/app/tenantpolicy/ -run .`: exact/wildcard/normalised matching, cache TTL and
  `Invalidate`, `ErrRecordNotFound` → `configured=false`, mode parsing.
- `./internal/adapter/handler/ -run TestTenantModelAllowlist`: via `SetupV2TestRouter` with a root
  JWT — PUT 400 on duplicates/empty/oversize, 404 unknown tenant, GET round-trip, DELETE →
  `configured=false`, chain test (PUT via handler then Distribute denies under enforce).
- `./internal/adapter/handler/router/ -run 'TestV2IDOR_Completeness|TestHandlersAreRouted'`;
  `./internal/pkg/metrics/ -run 'TenantModelDenied|TestDeclaredSeriesHaveAProductionWriter'`.

**Mutation** (each must go red)
1. Delete the inserted distributor block → (b) stays 0, (c) flips 403→200.
2. Move the block below `if ok {` into the else branch only → (e) flips 403→200.
3. Make `Mode()` return enforce for any non-empty value → (g) red.
4. Re-add `func GetTenantConfigs(c *gin.Context)` to `tenant.go` without a route →
   `handlers_are_routed_test` red.
5. Register the routes without completeness entries → `TestV2IDOR_Completeness` red.

**UAT probe** (root session on `https://test-newhub.lurus.cn`, recipe in
`doc/uat/business-acceptance-tests.md`)
BEFORE (current digest): `PUT /api/v2/admin/tenants/$TENANT_ID/model-allowlist` → 404;
`GET /api/v2/lurus/config` → 404 (the deleted handlers were never reachable — must still be 404
AFTER). AFTER: `PUT {"allowed_models":["not-the-live-model-*"]}` → 200
`{configured:true, mode:"observe", propagation_seconds:30}`; `POST /v1/chat/completions` with
`<live-model>` and a tenant key → 200 (observe) and `curl http://localhost:30851/metrics` (tunnel,
no forwarded headers) shows `lurus_gateway_tenant_model_denied_total{tenant_id="…",
action="observed"} 1`; `DELETE` → 200; the same relay → counter unchanged. Enforce half only if the
owner sets `TENANT_MODEL_ALLOWLIST_MODE=enforce` on the UAT overlay: same call → 403
`error.code=model_blocked`, `/api/v2/lurus/logs` row `other.error_code=model_blocked`; revert env
and list afterwards.

**Enterprise acceptance**
- A root operator declares in one place which models a tenant may call; with the default observe
  mode nothing is rejected and every would-be denial is visible as a counter series and a log line
  before enforcement is switched on per environment.
- Under enforce the denial is the already-documented typed 403 `model_blocked`, covers both the
  normal selection path and the `sk-<key>-<channelId>` pinned path, and fails open with a log if
  the policy row cannot be read — never a 500.
- No free-form tenant-config writer exists in the binary; a structural test keeps unrouted
  handlers from reappearing, so `models.allowlist` can only be written by the validating endpoint.

**Consumer note**
Default (observe): no wire change for switch / lutu / platform-core relay callers; new counter
series `lurus_gateway_tenant_model_denied_total{tenant_id,action}`. Under enforce (per-environment
env; production stays observe until the owner flips it): relay callers of a restricted tenant get
403 `error.code=model_blocked` / `type=permission_error` (already documented at guide `:109` and
in the `relay.json` code enum). Root console/API: three new root-only endpoints
`GET/PUT/DELETE /api/v2/admin/tenants/:id/model-allowlist`. Removed: nothing reachable (`GET
/:slug/config` and `PUT /:slug/config/:key` were never routed). Policy changes propagate to other
replicas within 30 s.

**Cross-repo follow-up**: root `doc/coord/contracts.md` newhub section — add the three admin
endpoints and `TENANT_MODEL_ALLOWLIST_MODE` (observe default); `service-status.md` one line
"allow-list observe-only until owner flips". Deferred G3-4 (`/v1/key` effective-models field)
reuses `tenantpolicy.LoadModelAllowlist` next cycle.

### L2 — `/v1/models` and `/v1/models/:model` answer from the caller's tenant-routable (and, under enforce, allow-listed) set

- kind: fix · candidates: NH6-09 · effort: S · runs after L1 (uses `tenantpolicy.Mode`,
  `LoadModelAllowlist`, `ModelAllowed`)
- Borrowed vocabulary: Bifrost `/v1/models` with a virtual key lists only what the key may query;
  LiteLLM key models gate discovery.

**Files**
- `internal/adapter/repo/ability.go`
- `internal/adapter/repo/ability_tenant_models_test.go`
- `internal/adapter/handler/model.go`
- `internal/adapter/handler/model_tenant_scope_test.go`
- `internal/adapter/handler/model_discovery_contract_test.go`

Test packages: `./internal/adapter/repo/`, `./internal/adapter/handler/`.

**Spec**
1. `repo/ability.go`: `GetGroupEnabledModelsForTenant(group, tenantID string) []string` = the
   `:31-36` query with `abilityTenantScope(tenantID)` (`:59-67`) appended to the WHERE string and
   args; `tenantID == ""` reproduces the old query byte-for-byte. Keep `GetGroupEnabledModels` for
   `user.go:336`.
2. `handler/model.go`: extract the visible-set construction of `ListModels` (`:112-191`, including
   the `acceptUnsetRatioModel` hiding at `:115-125` / `:139-144` / `:183-188`, the token-limit
   branch, and the `auto` group expansion at `:171-178`) into `visibleModels(c)
   ([]dto.OpenAIModels, error)`. Resolve `tenantID` with `repo.GetTenantID(c)`
   (`tenant_context.go:47`; `TokenAuth` injects it at `auth.go:601`); call the tenant-scoped query
   at both `:173` and `:181`. Then, only when `tenantpolicy.Mode() == "enforce"` and
   `LoadModelAllowlist` reports `configured`, filter with `ModelAllowed` (observe shows
   everything — previous owner item 6 answered: hide under enforce only).
3. `RetrieveModel` (`:273-306`): answer 200 with the entry from `visibleModels` whose `Id ==
   :model` (metadata from the static `openAIModelsMap` entry at `:275` when present, else the
   `owned_by "custom"` shape `ListModels` already emits at `:190-191`), and the existing
   wire-native 404 (`:283-306`) otherwise. Envelopes for both wires byte-identical.
4. `model_discovery_contract_test.go`: keep every row green; `TestRetrieveModel_KnownModel_
   Unchanged` (`:184`) must now seed the model as routable (ability row or token `model_limit`)
   because "known" is redefined as "routable for this caller"; add rows: token `model_limit {a}`
   + `GET /v1/models/b` → 404; custom routable model via an ability row → 200 `owned_by custom`.
5. Do NOT change the Anthropic/Google-wire list envelopes (`:193-242`), the router wiring
   (`relay-router.go:18-40`), `relay.json` (L6 adds the one sentence), route-time selection, or
   the 404-vs-503 narrowing in the distributor.

**Oracle**
- `./internal/adapter/repo/ -run TestGetGroupEnabledModelsForTenant` (seeding as
  `tenant_relay_selection_test.go:30`): channel A tenant `tenant-a` model `a-only`, channel D
  tenant `default` model `shared-model`; `tenant-b` → `[shared-model]`; `tenant-a` → both; `""` →
  both and `== GetGroupEnabledModels(g)`.
- `./internal/adapter/handler/ -run 'TestListModels_TenantScope|TestRetrieveModel|TestListModels'`:
  a `tenant-b` caller lists only `shared-model` (RED on HEAD: both); enforce + allow-list
  `[shared-model]` hides `a-only` for `tenant-a`, observe keeps it; retrieve of an unroutable
  static-table model → 404 (RED on HEAD: 200).

**Mutation**: revert the `:173/:181` call sites → `tenant-b` sees `a-only`; revert `RetrieveModel`
→ 200 for the unroutable static model; remove the enforce filter → `a-only` visible under enforce;
pass `""` as tenantID everywhere → repo equality test still green but the handler tenant test red
(the pair is the lock).

**UAT probe**: with the tenant key, `GET /v1/models/<live-model>` → 200 before and after;
`GET /v1/models/<a static-catalogue vendor model that is not on the single UAT channel>` → BEFORE
200 from the static table, AFTER 404 `code=model_not_found` on the OpenAI wire and
`type=not_found_error` with `x-api-key` + `anthropic-version` headers. Allow-list half after L1 with
UAT enforce: PUT `["not-the-live-model-*"]` → `GET /v1/models` returns `data=[]`; observe →
`[<live-model>]`. Cross-tenant half is hermetic-only (UAT and production each have one channel
owned by `default`).

**Enterprise acceptance**
- An SDK user in any tenant never sees a model in `/v1/models` that routing will refuse with
  503/404 — list, retrieve and route use one tenant-scoped definition.
- Under enforce the tenant's model policy is reflected in discovery immediately, so a sibling
  product's model picker only offers what the tenant may call.

**Consumer note**: OpenAI/Anthropic/Google-wire SDK clients enumerating models (no register entry
for `/v1/models` in `contracts.md`: grep = 0; control `X-Lurus-Product` = 2 hits): `/v1/models`
may return a shorter, truthful list per key; `GET /v1/models/:model` now returns 404 (existing
wire-native envelopes) for models the caller cannot route where it returned the static catalogue
entry. Under `TENANT_MODEL_ALLOWLIST_MODE=enforce` the list is additionally filtered by tenant
policy. One extra subquery per `/v1/models` call.

**Cross-repo follow-up**: `contracts.md` newhub section gets a `/v1/models` row (currently
absent); 2l-bs-docs one sentence that `/v1/models` is per-key and tenant-scoped and that retrieve
404s for unroutable models.

### L3 — Operator signals + bounded batches: leader gauge, per-task last-success, instance identity, `checks.leader`; `id IN (SELECT … LIMIT ?)` batching for log and audit deletes; audit interval env actually read

- kind: mixed (fix + borrow) · candidates: NH6-12, NH6-13 (header + info gauge half), NH6-11
  (batching + interval half) · effort: L (decomposed below; 22 files, three independent
  red→green steps that can be accepted separately)
- Borrowed vocabulary: OpenTelemetry semconv `service.instance.id` via the downward API;
  LiteLLM bounded retention batches; Langfuse explicit retention policy.

**Files**
- `internal/pkg/common/leader.go`
- `internal/pkg/common/instance.go`
- `internal/pkg/common/instance_test.go`
- `internal/pkg/metrics/instance.go`
- `internal/pkg/metrics/instance_test.go`
- `internal/lifecycle/leader_election.go`
- `internal/lifecycle/leader_election_test.go`
- `internal/lifecycle/leader_step_test.go`
- `internal/lifecycle/audit_cleanup.go`
- `internal/lifecycle/audit_cleanup_test.go`
- `internal/adapter/handler/router/main.go`
- `internal/adapter/handler/router/metrics_instance_test.go`
- `internal/adapter/handler/health.go`
- `internal/adapter/handler/health_test.go`
- `internal/adapter/repo/log.go`
- `internal/adapter/repo/log_retention_batch_test.go`
- `internal/adapter/repo/audit.go`
- `internal/adapter/repo/audit_e3_test.go`
- `deploy/k8s/r6-stage/deployment.yaml`
- `deploy/k8s/r6-uat/deployment.yaml`
- `doc/runbook/ha-deployment.md`
- `doc/runbook/database.md`

Test packages: `./internal/pkg/common/`, `./internal/pkg/metrics/`, `./internal/lifecycle/`,
`./internal/adapter/handler/router/`, `./internal/adapter/handler/`, `./internal/adapter/repo/`.

**Step A (red→green): truly batched deletes + audit interval**
- RED `repo/log_retention_batch_test.go` (SQLite hermetic tier): install a statement-counting gorm
  logger (pattern: the `logger.Interface` embedding recorder at `repo/leader_election_test.go:130-140`),
  seed 5 rows older than the cutoff + 2 newer, `DeleteOldLog(ctx, AllTenantsForAdmin(), cutoff, 2)`
  → exactly 3 DELETE statements, returns 5, 2 rows remain. On HEAD's `:803` the count is 2 (one
  unbounded DELETE + one empty) → RED. Same body PG-gated on `TEST_POSTGRES_DSN` (skip if unset) —
  the only proof of plan shape on the production dialect.
- RED `audit_e3_test.go` `TestDeleteExpiredAuditEvents_BatchesCorrectly` (`:64`) gains the
  statement count: 5 due rows, limit 2 → 3 DELETEs (HEAD 2 → RED).
- RED `audit_cleanup_test.go`: `t.Setenv(AUDIT_CLEANUP_INTERVAL_SECONDS, "1")` → an exported
  interval resolver returns 1 s (HEAD ignores the env → RED).
- GREEN `repo/log.go` `DeleteOldLog` keeps its signature; the body becomes, per iteration,
  `LOG_DB.Where("id IN (?)", scope.apply(LOG_DB.Model(&Log{}).Select("id").Where("created_at < ?",
  targetTimestamp)).Order("id").Limit(limit)).Delete(&Log{})` — a subquery `LIMIT` is rendered by
  both dialects; keep the `ctx.Err()` check (`:798-800`) and the `RowsAffected < limit` exit
  (`:808-810`). `repo/audit.go:76-93` `DeleteExpiredAuditEvents`: same `id IN (SELECT id … ORDER BY
  id LIMIT ?)` shape; row set identical; chain-verify tolerance unchanged. `audit_cleanup.go`:
  read `AUDIT_CLEANUP_INTERVAL_SECONDS` exactly like `credit_pool_reconcile.go:389-392` (positive
  int seconds, else the 24 h default at `:21`); nothing else in `:31-73` changes.
- `doc/runbook/database.md`: a short "retention deletes are batched (1000-row id subqueries)"
  note under the existing log-cleanup guidance; no new policy text (scheduled retention is
  deferred, §12).

**Step B (red→green): leader gauge + per-task last-success + `checks.leader`**
- RED `metrics/instance_test.go`: `SetLeader(true)` → `testutil.ToFloat64(Leader) == 1`, false →
  0; `RecordLeaderTaskSuccess("x")` → `{task="x"}` gauge within 2 s of now (HEAD: the vars do not
  exist → RED). `lifecycle/leader_election_test.go`: extend `TestLeaderTask_RunsOnlyWhenLeader`
  (`:15`) with `fn` returning nil then error → the `{task}` gauge is set once and not moved by
  the failing run. `leader_step_test.go`: `TestLeaderManagerStep_AcquiresFreeLeaseAndPromotes`
  (`:69`) asserts gauge == 1 after the step, `…DemotesWhenAnotherHolderOwnsTheLease` (`:103`)
  asserts 0 — proves the choke point on the real step path. `health_test.go`: with
  `healthSnapshotAll` (`:144`), `SetLeader(true)` → `checks.leader == "held"` and status healthy;
  `SetLeader(false)` → `"standby"` AND status still healthy (extends
  `TestGetHealthDetailed_BodyStatus_IntentionalOffStatesStayHealthy` `:284`).
- GREEN `metrics/instance.go` (own file; `namespace`/`subsystem` from `metrics.go:9-12`; promauto
  pattern as `ttft.go`): `Leader` Gauge `lurus_gateway_leader`, `LeaderTaskLastSuccess` GaugeVec
  `lurus_gateway_leader_task_last_success_timestamp_seconds{task}`, `InstanceInfo` GaugeVec
  `lurus_gateway_instance_info{pod,namespace,version}`; helpers `SetLeader(bool)`,
  `RecordLeaderTaskSuccess(task)`, `SetInstanceInfo(pod, ns, version)`. `common/leader.go:23-25`
  `SetLeader` additionally calls `metrics.SetLeader(v)` — `common` already imports `metrics`
  (`billing_breaker.go:10`) and `go list -deps ./internal/pkg/metrics` contains neither
  `internal/pkg/common` nor `internal/lifecycle` (verified this synthesis: 0 hits; lane re-runs
  it), so no cycle; every writer (`repo/main.go:257` boot lease, `leader_election.go:61/:68/:70/:92`)
  is covered by the single choke point. `leader_election.go:172-173`: replace `_ = t.fn(ctx)`
  with `if err := t.fn(ctx); err == nil { metrics.RecordLeaderTaskSuccess(t.name) }` — no change
  to poll/interval/`lastRun` semantics (`:152-171`), `step` (`:53-70`) or release-on-shutdown
  (`:85-92`). `lifecycle` gains its first `metrics` import (grep = 0 today). Only secret rotation
  is a `LeaderTask` today (`secret_rotation.go:27`); the non-LeaderTask loops (audit cleanup,
  credit-pool reconcile, privacy erasure, openrouter reaper/scheduler) are NOT touched (deferred
  D-06 — `credit_pool_reconcile.go` sits in `package app` beside the untouchable tests).
  `health.go`: after the billing check (`:76-85`) add `checks["leader"] = "held"` if
  `common.IsLeader()` else `"standby"`; register `"leader": {"held": true, "standby": true}` in
  `healthIntentionalOffStates` (`:102-107`); status-code logic (`:87-90`) untouched.

**Step C (red→green): instance identity**
- RED `common/instance_test.go`: `t.Setenv` precedence `POD_NAME` > `HOSTNAME` > `"node"`.
  `router/metrics_instance_test.go`: `t.Setenv(POD_NAME=test-pod-a)`, build the engine via the
  real `SetRouter` path the existing router tests use, `GET /metrics` with `RemoteAddr 127.0.0.1`
  and no forwarded headers → 200, header `X-Lurus-Instance == test-pod-a`, body contains
  `lurus_gateway_instance_info{` with `pod="test-pod-a"` (HEAD: header and series absent → RED).
  `health_test.go`: body `instance` non-empty and the `X-Lurus-Instance` header present.
- GREEN `common/instance.go`: `InstanceID()` = `POD_NAME`, else `HOSTNAME`, else `"node"` —
  deliberately NOT `NodeHolderID` (`leader.go:37-43` appends a random suffix per process).
  `router/main.go:37`: mount becomes `router.GET("/metrics", metricsAuthMiddleware(),
  instanceHeader(), gin.WrapH(promhttp.Handler()))` where `instanceHeader` sets
  `X-Lurus-Instance: common.InstanceID()`; call `metrics.SetInstanceInfo(common.InstanceID(),
  os.Getenv("POD_NAMESPACE"), common.Version)` (`constants.go:13`) once in `SetRouter` before the
  mount. `metricsAuthMiddleware` (`:85`) and the `SecurityHeaders` ordering note (`:29-34`)
  untouched. `health.go` body gains top-level `"instance": common.InstanceID()` and the same
  header. Manifests: in `deploy/k8s/r6-stage/deployment.yaml` (`env:` at `:68`) and
  `deploy/k8s/r6-uat/deployment.yaml` (`env:` at `:72`) add `POD_NAME`
  (`valueFrom.fieldRef.fieldPath: metadata.name`) and `POD_NAMESPACE` (`metadata.namespace`) —
  declarative, converged by ArgoCD, no imperative cluster action. `doc/runbook/ha-deployment.md`
  replicas row (`:19`) drill step becomes: read `lurus_gateway_leader` / `checks.leader` on each
  pod's `X-Lurus-Instance`, kill the pod reporting 1, watch another flip to 1 within the lease TTL.
- DELIBERATELY OMITTED (deferred D-05): the `METRICS_INSTANCE_LABEL` per-series relabelling
  gatherer — it re-keys every netdata chart and puts a custom Gatherer on the scrape path; the
  header + info gauge already attribute each scrape.
- Do NOT change series names/label sets of existing metrics, lease TTL/renew math
  (`leader_election.go:12-14`), `DeleteHistoryLogs` role/scope logic (`handler/log.go:250-256`),
  `TenantScope` fail-closed semantics (`repo/log.go:60-89`, catalog), `retention_until == 0`
  never-expires, or leader gating of audit cleanup. No new flag (additive observability; the
  batching fix has no behavioural surface beyond statement shape).

**Oracle** (all red on HEAD as stated above)
`./internal/adapter/repo/ -run 'TestDeleteOldLog_Batches|TestDeleteExpiredAuditEvents_BatchesCorrectly'`;
`./internal/lifecycle/ -run 'TestAuditCleanupInterval|TestLeaderTask|TestLeaderManagerStep_AcquiresFreeLeaseAndPromotes|TestLeaderManagerStep_DemotesWhenAnotherHolderOwnsTheLease'`;
`./internal/pkg/metrics/ -run 'Instance|Leader|TestDeclaredSeriesHaveAProductionWriter'`;
`./internal/pkg/common/ -run TestInstanceID`;
`./internal/adapter/handler/router/ -run TestMetricsInstance`;
`./internal/adapter/handler/ -run TestGetHealthDetailed`.

**Mutation**
1. Revert `repo/log.go` to `Limit().Delete()` → statement-count test red (2 DELETEs).
2. Revert `audit.go` likewise → audit batching count red.
3. Remove `metrics.SetLeader` from `common.SetLeader` → `leader_step` assertions red.
4. Remove `"leader"` from `healthIntentionalOffStates` → the standby case reports degraded → red.
5. Revert `:172-173` to `_ = t.fn(ctx)` → the `{task}` gauge assertion red.
6. Replace `InstanceID` with `NodeHolderID` → `X-Lurus-Instance == test-pod-a` red (random suffix).
7. Skip the env read in `audit_cleanup.go` → interval test red.

**UAT probe** (1 replica, tunnel to `:30851`, no forwarded headers)
BEFORE (current digest): `curl -sD- http://localhost:30851/metrics | grep -ci x-lurus-instance` →
0 (control: `grep -c Content-Type` → 1); `grep -c lurus_gateway_leader` → 0 (control
`lurus_gateway_schema_migrations_pending` → 1). AFTER: `X-Lurus-Instance` equals the pod from
`kubectl get pods -n lurus-newhub-uat`; `lurus_gateway_leader 1`;
`lurus_gateway_instance_info{pod="<that pod>",namespace="lurus-newhub-uat",version="…"} 1`;
`curl https://test-newhub.lurus.cn/api/health` → `checks.leader:"held"`, `instance:"<pod>"`,
status unchanged; `lurus_gateway_leader_task_last_success_timestamp_seconds{task="secret-rotation"}`
appears after the first rotation pass. Production after auto-pin (read-only): five curls of the
direct NodePort `/metrics` collect up to three distinct `X-Lurus-Instance` values matching
`kubectl get pods -n lurus-newhub`, exactly one reporting `lurus_gateway_leader 1` — the first
hard evidence of the scrape interleave. Batching half: hermetic-only (statement counts on both
dialects; the manual admin delete `handler/log.go:256` keeps limit 100 and can be exercised on UAT
as a no-regression check: `DELETE /api/log?target_timestamp=<old>` returns the same count as
before).

**Enterprise acceptance**
- An operator can name which replica holds the lease (gauge + `checks.leader` +
  `X-Lurus-Instance`) and run the HA drill in `doc/runbook/ha-deployment.md` with something to
  watch; a follower is never reported unhealthy.
- Every `/metrics` scrape and `/api/health` response is attributable to a pod, so counters that
  appear to reset on the shared NodePort scrape can be explained per instance; existing series
  names and label sets are byte-identical.
- Retention deletes on both the logs and the tamper-evident audit table are genuinely bounded
  batches on PostgreSQL (statement-counted on the real dialect), so switching a policy on at 25k+
  rows cannot lock a table in one transaction; `AUDIT_CLEANUP_INTERVAL_SECONDS` does what its
  comment has said since it was written.

**Consumer note**: Wire: none for switch / lutu / platform-core. `/api/health` (readiness probe +
operators) gains additive keys `checks.leader` (`held|standby`) and top-level `instance`, plus
header `X-Lurus-Instance`; status/HTTP-code semantics unchanged. `/metrics` gains header
`X-Lurus-Instance` and three series. Manifests r6-stage/r6-uat gain `POD_NAME`/`POD_NAMESPACE`
env (downward API) — an ArgoCD rollout of 3 production pods. Admin `DELETE /api/log` behaviour
unchanged (same rows deleted, now in id-subquery batches).

**Cross-repo follow-up**: host netdata go.d job `newhub` scrapes one NodePort; once the interleave
is evidenced, add one job per pod IP (or a headless service) — infra change outside this repo.
`service-status.md`: note the new health keys.

### L4 — Canonical pricing name for cache-read, cache-write and image ratios (money defect, no flag)

- kind: fix · candidates: NH6-03 (unconditional normalisation half only) · effort: S
- Borrowed vocabulary: New API canonical billing identities (only the unconditional half; the
  `MODEL_VARIANT_PRICING` observe/enforce fallback is deferred with its spec).

**Files**
- `internal/pkg/setting/ratio_setting/cache_ratio.go`
- `internal/pkg/setting/ratio_setting/model_ratio.go`
- `internal/pkg/setting/ratio_setting/cache_ratio_normalise_test.go`

Test packages: `./internal/pkg/setting/ratio_setting/` (regression net: `./internal/app/relay/
-run TestBillingInvariance` must stay green — no formula changes).

**Spec**
`GetCacheRatio` (`cache_ratio.go:149-157`), `GetCreateCacheRatio` (`:159-165`) and `GetImageRatio`
(`model_ratio.go:816-824`) look up the raw name while every model-ratio getter normalises first
(`FormatMatchingModelName` at `model_ratio.go:424,462,498,586,735,745,756,764`; `grep -c
FormatMatchingModelName cache_ratio.go` = 0). `FormatMatchingModelName` (`:902-920`) rewrites the
vendor thinking-budget family names to their `<family>-thinking-*` wildcard and the two gizmo
families to `*-gizmo-*`. Fix: in each of the three getters try the exact raw key first
(preserves any operator entry keyed by the raw name — behaviour-preserving for every existing
exact entry), then the `FormatMatchingModelName(name)` key; defaults (1 / 1.25 / 1) unchanged when
neither hits. Take the read lock once per call as today (`GetCreateCacheRatio` has no lock today;
keep it lock-free). The sole production caller `helper/price.go:83-90` is unchanged; note the
observable consequence at `:85-86`: `cacheCreationRatioDefaulted` becomes false when the wildcard
entry hits — intended, the operator's configured cache-write ratio then wins over the Anthropic-wire
1.25 default, which is exactly what the operator configured. `PriceData` semantics,
`CacheCreationRatioForWire`, the 1h multiplier and both settlement paths are untouched, so the
billing invariance matrix remains the oracle for formulas. Do NOT add a flag (the change can only
move a price toward the operator's configured entry), do NOT touch `GetModelRatio` (`:458`) /
`GetModelPrice` (`:420`) family-fallback markup, do NOT edit the guide (L6 adds the one-line
pricing note).

**Oracle**
`go test -count=1 -p 2 ./internal/pkg/setting/ratio_setting/ -run 'Normalises|ExactKeyWins'`:
with the cache-ratio map `{"<family>-thinking-*": 0.25}`, `GetCacheRatio("<family>-thinking-1024")`
== `(0.25, true)` (RED on HEAD: `(1, false)`); same shape for `defaultCreateCacheRatio` and
`imageRatioMap` with a gizmo name; exact-key test: map `{"<family>-thinking-1024": 0.5,
"<family>-thinking-*": 0.25}` returns 0.5. Existing `ratio_coverage_test.go`, `model_family_test.go`,
`model_equivalence_test.go` stay green; `./internal/app/relay/ -run TestBillingInvariance` stays
green.

**Mutation**: revert any one getter to the raw lookup → its normalisation test red; swap the
lookup order (normalised first) → the exact-key test red.

**UAT probe**: hermetic-only — `<live-model>` is an exact key with no thinking-budget or gizmo
suffix, so its cache/image ratios resolve identically before and after. Post-deploy sanity: one
`<live-model>` call whose `X-Request-Cost` and `GET /v1/generation?id=…` `quota` equal the
pre-deploy values for the same prompt.

**Enterprise acceptance**
- A cache-read / cache-write / image discount the operator configured under a family or wildcard
  entry is charged as configured for thinking-budget and gizmo names — the invoice matches the
  price list.
- No customer with an exact-name price entry sees any change (exact key wins), and the two
  settlement paths remain byte-identical because only the ratio lookup, not the formula, changed.

**Consumer note**: relay callers sending thinking-budget or gizmo family names on a deployment
that configured the wildcard cache/cache-write/image ratio now see that ratio applied
(`X-Request-Cost`, `x_lurus.cost_lb` and `/v1/generation.quota` move toward the configured value —
down for discounts < 1, up for image ratios > 1). switch / lutu / platform-core: none on the live
model; no API shape change.

**Cross-repo follow-up**: none.

### L5 — Console truth: Settings Subscription tab stops inventing entitlements; one `useTenantModels` hook revives the Playground swap list and replaces four parsers

- kind: mixed (fix + consolidate) · candidates: NH6-16, NH6-01 · effort: M
- Borrowed vocabulary: LiteLLM UI renders only stored entitlements; the shared-hook-per-endpoint
  convention of every reference console; newhub's own no-fabrication rule (Log detail panel, MFA
  "unavailable" at `Settings/index.test.jsx:543`).

**Files**
- `web/src/pages/v2/Settings/index.jsx`
- `web/src/pages/v2/Settings/index.test.jsx`
- `web/src/pages/v2/Billing/index.jsx`
- `web/src/pages/v2/Billing/index.test.jsx`
- `web/src/pages/v2/Billing/balance.test.jsx`
- `web/src/hooks/models/useTenantModels.js`
- `web/src/hooks/models/useTenantModels.test.jsx`
- `web/src/pages/v2/Playground/index.jsx`
- `web/src/pages/v2/Playground/index.test.jsx`
- `web/src/pages/v2/Models/index.jsx`
- `web/src/pages/v2/Models/index.test.jsx`
- `web/src/pages/v2/CommandPalette/index.jsx`
- `web/src/pages/v2/CommandPalette/index.test.jsx`
- `web/src/pages/v2/no_inline_models_fetch.test.js`
- `web/src/i18n/locales/en.json`
- `web/src/i18n/locales/zh.json`

Test packages: web (`cd web && bun run test -- Settings Billing Playground useTenantModels Models
CommandPalette no_inline_models_fetch i18n-integrity`, then `bun run lint`, `bun run eslint`,
`bun run check:casing`).

**Spec — Settings half**
Verified on HEAD: `Settings/index.jsx:93-121` `ENTITLEMENT_BY_GROUP` maps group → invented SLA
`99.5%`, audit days, support tiers with the comment that the backend registry is not implemented
(`:91`); `fetchSubscription` (`:345`) probes `/api/v2/user/billing/summary` for
`subscription_plan` (`:357-366`) which the Go `BillingSummary` (`identity_client.go:705`) does not
have (`grep -rn subscription_plan internal --include=*.go` = 0 — the lane re-runs this); the panel
renders from `ENTITLEMENT_BY_GROUP[subData.group]` at `:979-980`; badge testid
`subscription-tier-badge` at `:1012`; upgrade button `:1037` with title default `Plan upgrades
available in Wave B` (`:1039-1040`); the test at `:320`/`:445` matches `/wave b/i`.
`Billing/index.jsx:816-824` guards the same phantom field; fixtures `Billing/index.test.jsx:96`
and `balance.test.jsx:107` seed it. Changes: delete `ENTITLEMENT_BY_GROUP` and the billing-summary
probe; `fetchSubscription` reads only `/api/v2/${tenantSlug}/user/me` (existing call) and keeps
the cached-profile short-circuit; badge keeps `subscription-tier-badge` for the existing loading
assertion and adds `data-testid="subscription-group"` on the mono value showing `p.group`
verbatim; empty/missing group → `tr('console.settings.group_none', 'no group assigned')` — never
"Free"; caption `tr('console.settings.group_source_user_group', "from your account's routing
group")`; replace the four entitlement rows with one line `data-testid="subscription-entitlements-
unpublished"` `tr('console.settings.entitlements_not_published', 'SLA, audit retention and support
terms are not published by this deployment')`; keep the disabled Upgrade button, title →
`tr('console.settings.upgrade_plan_title', 'Contact your administrator')` and update the two
`/wave b/i` matches. `Billing/index.jsx`: delete `:816-824` and the two fixture fields; add a
source lock in `Billing/index.test.jsx` asserting no file under `web/src/pages/v2` contains
`subscription_plan`. Locales: add `group_none` / `group_source_user_group` /
`entitlements_not_published` and the new `upgrade_plan_title` text under
`translation.console.settings` in en.json and zh.json; remove
`tier_free/tier_pro/tier_enterprise/ent_*/audit_days_one/audit_days_other/free_tier_note`
(`en.json:898-916` region) ONLY after `grep -rn 'tier_free\|ent_routing\|ent_sla\|ent_support\|
ent_audit\|audit_days' web/src --include=*.jsx --include=*.js` returns only the Settings lines
being deleted (true on HEAD; fr/ja/ru/vi have 0 hits). Do NOT invent a plan from
`entity.Tenant.PlanType` (root-managed; no member-facing v2 route returns it); do NOT touch the
Billing tab inside Settings (its test `:323-360` keeps calling `/user/billing/summary`).

**Spec — models hook half**
Verified on HEAD: `v2_models.go:108-116` returns `data` as `{items,total,limit,offset,
vendor_counts}`; `Playground/index.jsx:159-170` does `(res.data.data || []).map(...)` on that
object — the TypeError dies in the empty `catch` at `:169`, `availableModels` stays `[]` and the
swap dropdown shows only `console.common.loading` (`:772`) forever; its test
`Playground/index.test.jsx:378-390` mocks an array shape the backend never sends. Three more
parsers: `Models/index.jsx:100` and `:124` (two copies, both `d.items ?? []`),
`CommandPalette/index.jsx:89` + `:113`. `grep -rn useTenantModels web/src` = 0 (control
`useTenantSlug` used in 18 files). RED: rewrite the Playground swap test mock to the real
`{items:[{model_name:'…'},…]}` shape — red on HEAD (dropdown stays loading); new hook test; new
`web/src/pages/v2/no_inline_models_fetch.test.js` asserting no file under `web/src/pages/v2`
contains the literal `/models?` or ``/models` `` except through the hook (fail-fast if it scans 0
files). GREEN: `web/src/hooks/models/useTenantModels.js` exporting `useTenantModels(tenantSlug,
{limit=100, offset=0, vendor='', keyword='', enabled=true})` → `{items, total, vendorCounts,
loading, error, refetch}`; ONE parser `const d = res?.data?.data ?? {}; items =
Array.isArray(d.items) ? d.items : []` (never treats `data` as an array); `API.get` with
`skipErrorHandler` so pages keep their own toast policy; cancellation guard as
`Models/index.jsx:117-128`; slug from the caller (pages use `useTenantSlug`,
`hooks/common/useTenantSlug.js:18`). Models page: replace both fetch copies with the hook (vendor
as input, `refetch()` where `fetchModels` was called after add). CommandPalette: the models group
reads from the hook while keeping `Promise.allSettled` per-source degradation (`:86`),
`MAX_PER_GROUP` (`:46`) and the drop-empty-group rule (test `:174`) for the other sources.
Playground: lazy (enabled only when the swap menu opens), maps `items.model_name`, shows loading
only while loading and a new `console.playground.no_models` line when loaded and empty (English
default in the call so the fr/ja/ru/vi ratchets do not move). Do NOT change `ListModelsV2`, vendor
pill behaviour, `hooks/models/useModelsData.jsx` (legacy table), or anything under `internal/`.

**Oracle**
- Settings: rewritten test (`:302-320`) with `/user/me → {group:'vip'}` asserts
  `subscription-group` text `vip`, `subscription-entitlements-unpublished` present, none of
  `99.5`, `99.95`, `dedicated`, `business hours`, `365`, `community`, `Free` in the section,
  upgrade button disabled with title matching `/administrator/i`, and `API.get` never called with
  `/user/billing/summary` after clicking Subscription (RED on HEAD: one call at `:360`); with
  group `''` the badge reads `no group assigned`. Billing tests green with the fixtures removed;
  the `subscription_plan` source lock green. `i18n-integrity` ratchet rows unchanged.
- Hook: the rewritten Playground swap test is RED on HEAD and green after; hook test: array
  payload or `success:false` yields `items=[]` and `error` set without throwing; `refetch`
  re-issues exactly one request; Models and CommandPalette existing tests stay green with exactly
  one `/models` request per mount; the literal lock is red if any page re-inlines the fetch.
- `bun run lint`, `bun run eslint`, `bun run check:casing` exit 0 (snake_case reads only:
  `model_name`, `vendor_counts`, `p.group`).

**Mutation**
1. Re-add a hard-coded `99.5%` row → the not-contains assertion red.
2. Restore the billing-summary probe in `fetchSubscription` → the no-call assertion red.
3. Map group `''` to `Free` → the empty-group test red.
4. Re-add `summary.subscription_plan` to `Billing/index.jsx` → the source lock red.
5. Restore the Playground parser to `(res.data.data || []).map` → the swap test red.
6. Return `d.items` without the `Array.isArray` guard and feed `success:false` → hook test red.
7. Re-inline an `API.get('/models?')` in `Models/index.jsx` → the literal lock red.
8. Remove `console.playground.no_models` from en.json while keeping a Chinese default →
   `i18n-integrity` (`:185`) red.

**UAT probe** (bridge session on `https://test-newhub.lurus.cn`)
Settings → Subscription. BEFORE (current digest): badge `Free` + rows `shared pool / best effort /
7 days / community` for the bridge user (group `default`), and the network panel shows
`GET /api/v2/user/billing/summary` fired by this tab. AFTER: badge shows the literal `.data.group`
from `GET /api/v2/lurus/user/me`, one "not published" line, no entitlement rows, and no
`/api/v2/user/billing/summary` request from this tab; the Billing page renders unchanged (its own
summary call remains). Playground (`/console/lurus/playground`), click swap on column 0. BEFORE:
dropdown shows only loading while the network tab shows `GET /api/v2/lurus/models` 200 with
`data.items=[{model_name:"<live-model>"}]`. AFTER: dropdown lists `<live-model>` (testid
`playground-swap-model-<live-model>`, `:782`); Models page and the command palette still show the
single model with exactly one `/models` request per page mount.

**Enterprise acceptance**
- A prospective buyer opening Settings reads only facts the backend asserts (the routing group) and
  an explicit statement that SLA/audit-retention/support terms are not published — no
  misrepresentation on an enterprise-sold console; the fiction cannot silently return (source lock
  + Subscription test).
- The Playground model swap sold to tenants works on every deployment, and its unit test encodes
  the real wire shape instead of a fictional one.
- One client seam consumes `/api/v2/:slug/models`, so the tenant allow-list (enforce) and any
  future pricing join reach every console surface through one parser.

**Consumer note**: Console users only. Settings > Subscription: tier badges Free/Pro/Enterprise,
SLA %, audit-retention days and support tier removed; the account's routing group and an
"entitlements not published" line shown; the tab stops calling `/api/v2/user/billing/summary`
(the Billing tab still does). Billing: the never-rendered plan row removed. Playground swap list
populates. Locale keys `tier_*/ent_*/audit_days_*/free_tier_note` removed from en/zh; new keys
under `console.settings` and `console.playground`. No API, header or sibling-product change;
platform-core's billing-summary contract untouched.

**Cross-repo follow-up**: none (if platform later exposes a real plan it lands in the now-empty
slot rather than a local table).

### L6 — Wire-contract truth: English 429 bodies on every gateway reject site, CORS exposes cycle-5 headers and allows inbound correlation headers, 429 rows and Scope/Type enums locked, `X-Session-Id` row corrected under a doc lock, `.env.example` rows for this cycle's flags

- kind: mixed (consolidate + fix + borrow) · candidates: NH6-20, NH6-02, NH6-17, NH6-06
  (directory/lock half) · effort: M · runs LAST
- Owns both cycle-5 residuals (English 429 message; CORS exposure).
- Borrowed vocabulary: Kong / Higress English 429 sentence with numbers and code; gin-contrib/cors
  `Config.ExposeHeaders`; OpenRouter / Helicone header directory readable by browsers.

**Files**
- `internal/adapter/middleware/business_rate_limit.go`
- `internal/adapter/middleware/concurrency_limit.go`
- `internal/adapter/middleware/model-rate-limit.go`
- `internal/adapter/middleware/rate_limit_message_lock_test.go`
- `internal/adapter/middleware/cors.go`
- `internal/adapter/middleware/cors_headers_test.go`
- `internal/adapter/handler/router/openapi_contract_lock_test.go`
- `internal/adapter/handler/guide_session_id_lock_test.go`
- `docs/openapi/relay.json`
- `doc/product-integration-guide.md`
- `.env.example`

Test packages: `./internal/adapter/middleware/`, `./internal/adapter/handler/router/`,
`./internal/adapter/handler/`.

**Spec — English 429 bodies (six sites, all re-opened)**
1. `bizReject` (`business_rate_limit.go:387-405`): scopeLabel 令牌/租户/模型 at `:393-399`,
   measure at `:400-403`, Sprintf at `:404`. Replace with
   `fmt.Sprintf("%s %s limit exceeded: %d per minute (%s); retry after %d s", scope, measure,
   limit, bizRateLimitErrorCode, retryAfter)` where `scope` is the literal already written to
   `X-RateLimit-Scope` at `:391` (`token|tenant|model`) and `measure` = `requests` for rpm /
   `tokens` for tpm.
2. `ccReject` (`concurrency_limit.go:203-218`): scopeLabel `:205-208`, Sprintf `:216` → `"%s
   concurrency limit exceeded: %d in-flight requests (%s); wait for an in-flight request to
   finish"` using the scope literal written at `:213` and `concurrencyLimitErrorCode`.
3. Redis user limiter `model-rate-limit.go:197-201` (Chinese at `:200`) and `:226-230` (`:229`):
   `"user requests limit exceeded: %d per %d min (%s)"` and `"user total requests limit exceeded:
   %d per %d min including failed requests (%s)"` with `types.ErrorCodeRequestRateLimitExceeded`;
   numbers unchanged.
4. Memory backend `memoryRateLimitHandler` (`:262-290`): the two reject branches `:270-277` and
   `:283-289` do `c.Status(429)` + `c.Abort()` with NO body — replace both with
   `abortWithOpenAiMessage(c, http.StatusTooManyRequests, <same English text as the redis twin>,
   string(types.ErrorCodeRequestRateLimitExceeded))` after the existing header writes
   (`abort_code_structural_test.go:29` counts every call site; the honesty floors at `:82-88`
   only rise).
5. Keep: status codes, code constants, `X-RateLimit-*` / `Retry-After` writes (`:389-392`,
   `:210-214`, `:197-199`, `:226-228`, `:271-274`, `:284-286`), `ClearRateLimitHeadroomHeaders`
   order, `metrics.RecordRateLimited`, fail-open branches, `ValidateRateLimits`' admin-facing
   Chinese, all log lines. No flag (message text is explicitly not a contract; guide `:112` tells
   consumers to key on `code`).

**Spec — CORS**
`cors.go:9-24`: package vars `CORSExposedHeaders` = `X-Request-Id, X-Oneapi-Request-Id,
X-RateLimit-Limit, X-RateLimit-Remaining, X-RateLimit-Reset, X-RateLimit-Scope, X-RateLimit-Type,
Retry-After, X-Model-Provider, X-Request-Cost, X-Quota-Remaining, X-Lurus-Instance` and
`CORSAllowedHeaders` = the existing seven (`:19-22`) + `X-Request-Id, X-Session-Id,
X-Lurus-Product, traceparent`; `corsConfig.ExposeHeaders = CORSExposedHeaders`,
`corsConfig.AllowHeaders = CORSAllowedHeaders`. Keep `AllowOrigins` from config (`:12`,
`config.go:44/:150`), `AllowCredentials` (`:13`), `AllowMethods` (`:14`); no wildcard (the
`:15-18` comment stays true). All five mounts (`api-router.go:14/:100`, `api-v2-router.go:18`,
`dashboard.go:14`, `relay-router.go:14`) pick it up. No flag — additive CORS metadata for origins
already in `ALLOWED_ORIGINS`. Do NOT add `X-Lurus-Attempts`/`X-Lurus-Upstream-Model` (deferred
with NH6-06's code half); when they land the exposed-lock below forces them in.

**Spec — locks in `openapi_contract_lock_test.go`**
(a) extend `TestOpenAPIContract_429sDocumentRetryAfterAndScope` (`:198`, checks at `:230-234`)
to also require `X-RateLimit-Limit` and `X-RateLimit-Remaining` on every documented 429
(`bizReject :390`, `ccReject :212`, `model-rate-limit :197/:226/:271/:284` all write them via
`setRateLimitResponseHeaders`; keep `Reset` out — `bizReject` clears it at `:389`);
(b) new `TestOpenAPIContract_DocumentedResponseHeadersAreCORSExposed`: every header name used under
any response's `headers` object in `relay.json` must be in `middleware.CORSExposedHeaders`
(case-insensitive); the reverse (exposed but undocumented) is allowed; fail fast if fewer than 5
distinct names are scanned;
(c) new `TestOpenAPIContract_RateLimitScopeTypeEnumsMatchMiddleware`: regex-scan
`internal/adapter/middleware/*.go` non-test files for `Set("X-RateLimit-Scope", "<lit>")` /
`Set("X-RateLimit-Type", "<lit>")` / `bizReject(c, "<scope>", "<type>"` / `ccReject(c, "<scope>"`
literals (today: `cost_spike.go:121-122` user/cost, `entitlement.go:132-133` account/quota,
`model-rate-limit.go:198-199/:227-228/:273-274/:285-286` user/requests, `rate-limit.go:78/:99`
requests, `business_rate_limit.go:508/:534` token|tenant rpm, `business_model_rate_limit.go:130`
model rpm, `concurrency_limit.go:214/:247/:261` concurrency token|tenant) and assert each appears
in the `XRateLimitScope` (`relay.json:5092-5094`) / `XRateLimitType` (`:5096-5098`) description
text; fail fast on 0 literals (`abort_code_structural_test.go:82` pattern). The lane runs (c) BEFORE
editing anything and records whether it is green on HEAD (expected: all literals present).

**Spec — `relay.json`**
On every 429 response object that has `X-RateLimit-Scope` (first at `:470-476`) add
`X-RateLimit-Limit` and `X-RateLimit-Remaining` refs (components `XRateLimitLimit` /
`XRateLimitRemaining` already exist for the 200 rows) with the description note "Remaining is
always 0 on a 429; Reset is not sent on rejects". If L2 landed: one sentence on the `/v1/models`
and `/v1/models/{model}` descriptions that the list is per-key and tenant-scoped and that retrieve
404s for unroutable models. No path, code-enum or schema change otherwise.

**Spec — guide (`doc/product-integration-guide.md`)**
Rewrite the `X-Session-Id` row (`:154`, currently 网关只存它的 HMAC…原始值不落库,不回显) into two
facts: (1) log correlation — the raw value is stored on the log row as `session_id` when ≤200
printable-ASCII bytes (else dropped, not truncated; `relay_info.go:434-444` `deriveSessionId`),
written at `governance.go:117`, `TierPublic` (`classification.go:74`), returned by
`GET /v1/generation` (`v1_generation.go:57/:97`) and a filter on `/api/v2/:slug/logs` and
`/logs/stat` (`v2_log.go:129/:218`) — use an opaque conversation id, never personal data;
(2) channel affinity — separately, an HMAC of the value keys the sticky-channel binding. Add
`session_id` to the `/v1/generation` bullet (`:162`). §E (`:143-155`): one sentence on which
headers a browser on an allowed origin can read and send, and a note under the `X-RateLimit-Limit`
row that the pair is also present on gateway 429s with Remaining 0. §B 429 row (`:112`): "all
gateway-originated 429 `error.message` texts are English; key on `code`". §B 403 row (`:109`):
`model_blocked` is also raised by the tenant allow-list under `TENANT_MODEL_ALLOWLIST_MODE=
enforce`. §B pricing note (one line): wildcard/family cache and image ratios are now honoured (L4).
Only if L2 landed: §A `/v1/models` row notes per-key tenant scope.

**Spec — `guide_session_id_lock_test.go`** (handler package): reads
`../../../doc/product-integration-guide.md`, finds the `X-Session-Id` table row, asserts it contains
`session_id` and `/v1/generation` and does not contain `不落库` or `不回显` — RED on HEAD.

**Spec — `.env.example`**: rows added ONLY if the identifier exists on the tree at lane start
(grep first, skip otherwise): `TENANT_MODEL_ALLOWLIST_MODE=observe` (L1) beside
`CREDIT_POOL_RESET_MODE` (`:286`), `AUDIT_CLEANUP_INTERVAL_SECONDS=86400` (L3), and a comment line
that `POD_NAME`/`POD_NAMESPACE` are set by the manifest (L3); same comment style as
`RATE_LIMIT_HEADERS_ENABLED` (`:258`). Do NOT touch `security_headers.go`, any route, error codes,
or the inbound parser `app/channel.go:86-120`.

**Oracle**
- `./internal/adapter/middleware/ -run 'TestRateLimitMessage|TestCORS'`:
  `rate_limit_message_lock_test.go` drives the real sites — token rpm via `runBizRL`
  (`business_rate_limit_test.go:70`) under `eachBizBackend` (`:87`) with limit 1 and two requests;
  token tpm via `runBizRLEstimate` (`business_rate_limit_tpm_test.go:48`) + `bizFreezeClocks`
  (`:31`); model scope via `runBizChainRL` (`rate_limit_headroom_test.go:262`); concurrency via
  `ccTestRouter` (`concurrency_limit_test.go:19`); user scope via `memoryRateLimitHandler(60, 0, 1)`
  mounted directly with `c.Set("id", 1)`, two requests, `common.RedisEnabled=false`. For every
  429 recorder: valid JSON, `error.message` non-empty, every rune < 0x80, matches
  `^(token|tenant|model|user) .* limit exceeded: \d+ .*\((request_rate_limit_exceeded|business_rate_limit_exceeded|concurrency_limit_exceeded)\)`,
  `error.code` equals the parenthetical, `X-RateLimit-Scope` equals the leading word; the
  memory-backend case additionally asserts `Content-Type` starts with `application/json` and
  `Body.Len() > 0` — RED on HEAD. The test fails fast if fewer than 6 reject sites were
  exercised. `cors_headers_test.go` on `gin.New()+CORS()` with `config.Get().CORS.AllowedOrigins`
  containing `https://allowed.example`: (a) GET with the allowed Origin →
  `Access-Control-Expose-Headers` contains every `CORSExposedHeaders` name; (b) OPTIONS with
  `Access-Control-Request-Method: POST` and `Access-Control-Request-Headers:
  authorization,content-type,x-lurus-product,x-session-id,x-request-id,traceparent` → 204 and
  Allow-Headers lists all four new names; (c) control: Origin `https://evil.example` → no
  Allow-Origin and no Expose header; (d) a relay-shaped engine (`router.Use(middleware.CORS())`
  then a POST `/v1/chat/completions` stub) carries the Expose header on the POST. RED on HEAD: no
  Expose header; preflight rejects `x-lurus-product`.
- `./internal/adapter/handler/router/ -run TestOpenAPIContract`: (a) red on HEAD's `relay.json`,
  green after; (b) red if a documented header is missing from the list; (c) green on HEAD, red
  under mutation.
- `./internal/adapter/handler/ -run TestGuideSessionIdLock`: red on HEAD, green after.

**Mutation**
1. Restore the Chinese literal at `business_rate_limit.go:404` → rune<0x80 assertion red (token rpm).
2. Revert `model-rate-limit.go:270-277` to `c.Status`+`c.Abort` → memory-backend body assertion red.
3. Drop the code parenthetical from `ccReject` → regex red (concurrency).
4. Change the scope word to `key` in the bizReject text while leaving the header → leading-word
   assertion red.
5. Delete the `ExposeHeaders` assignment → (a), (d) and lock (b) red.
6. Drop `X-Lurus-Product` from `CORSAllowedHeaders` → preflight (b) red.
7. Remove `X-RateLimit-Limit` from one 429 row in `relay.json` → extended 429 check red.
8. Temporarily add `Set("X-RateLimit-Scope","bogus")` in a middleware → lock (c) red; remove
   `account` from the `XRateLimitScope` description → lock (c) red.
9. Restore `不落库` in the guide row → doc lock red.

**UAT probe** (`https://test-newhub.lurus.cn`)
429 half: `PUT /api/v2/lurus/tokens/<id> {"rate_limit_rpm":1}`; two `POST /v1/chat/completions`
`<live-model>` within 60 s with that key. BEFORE (current digest): second response 429,
`error.message` begins `令牌每分钟请求数已达上限`. AFTER: `error.message` ==
`token requests limit exceeded: 1 per minute (business_rate_limit_exceeded); retry after N s
(request id: …)` with `X-RateLimit-Scope: token`, `X-RateLimit-Limit: 1`,
`X-RateLimit-Remaining: 0` unchanged — exactly what the docs now state. Restore `rate_limit_rpm 0`.
Memory-backend half hermetic-only (UAT runs Redis DB 3). CORS half: BEFORE `curl -sD - -o /dev/null
-X OPTIONS https://test-newhub.lurus.cn/v1/chat/completions -H 'Origin: https://test-newhub.lurus.cn'
-H 'Access-Control-Request-Method: POST' -H 'Access-Control-Request-Headers:
authorization,content-type,x-lurus-product'` → `Access-Control-Allow-Headers` lacks
`x-lurus-product`; a POST `<live-model>` with that Origin and the UAT key shows no
`Access-Control-Expose-Headers` line. AFTER: preflight lists `x-lurus-product`, `x-session-id`,
`x-request-id`, `traceparent`; the POST carries `Access-Control-Expose-Headers` containing
`X-Request-Id` and `X-RateLimit-Scope`; Origin `https://evil.example` still gets no
`Access-Control-Allow-Origin`. Doc half: its behavioural truth is already live — relay with
`X-Session-Id: uat-conv-<ts>`, then `GET /v1/generation?id=<echoed X-Request-Id>` returns
`session_id: uat-conv-<ts>` on the current digest, which is the point of the correction.

**Enterprise acceptance**
- A foreign integrator reading `error.message` from any gateway-originated 429 (token / tenant /
  model / user / concurrency, both limiter backends) gets an English sentence with the limit, the
  window and the stable code; the dev-tier user limiter returns the same wire-native JSON envelope
  instead of an empty body; status, code, type and headers are byte-identical to before.
- A browser SDK of a sibling product on an allowed origin can read the request id (making
  `GET /v1/generation` usable), headroom, cost and instance headers, and can send product
  attribution, session and request ids — cycle-5 features stop being invisible to browsers;
  unlisted origins gain nothing.
- The public header directory cannot drift: documented headers must be exposed, 429 rows document
  what the code emits, Scope/Type value sets are locked to the middleware literals, and the
  `X-Session-Id` retention statement matches the code so integrators stop putting personal data
  in it.

**Consumer note**: switch, lutu, platform-core relay callers and outside SDKs: `error.message`
text on gateway 429s with codes `business_rate_limit_exceeded` / `concurrency_limit_exceeded` /
`request_rate_limit_exceeded` changes from Chinese to English; `error.code`, `error.type`, HTTP
status, `Retry-After` and `X-RateLimit-*` unchanged (`contracts.md` grep `X-RateLimit` = 0 hits;
no consumer keys on the text; the console shows relay errors only via the server-side Playground
path). Memory-backend deployments (none in prod/UAT) gain a JSON body on the user-limiter 429.
Browser callers on `ALLOWED_ORIGINS` (`hub.lurus.cn`, `identity.lurus.cn`; UAT
`test-newhub.lurus.cn`): responses gain `Access-Control-Expose-Headers` and preflights accept
`X-Request-Id` / `X-Session-Id` / `X-Lurus-Product` / `traceparent` — product attribution from
browsers stops falling to the default (non-browser SDKs unaffected). 2l-bs-docs (mirrors
`relay.json` + guide): 429 rows gain `X-RateLimit-Limit`/`Remaining` documentation (no runtime
change); guide `X-Session-Id` semantics corrected (the value IS stored on the log row and visible
in the console/export); guide notes English 429 messages, the tenant allow-list 403, the pricing
correction and (if L2 landed) `/v1/models` tenant scope. `.env.example` gains rows for
`TENANT_MODEL_ALLOWLIST_MODE` and `AUDIT_CLEANUP_INTERVAL_SECONDS`. No status code, default or
route changes.

**Cross-repo follow-up**: root `doc/coord/changelog.md` under newhub: "429 `error.message` now
English on all gateway rate-limit rejects; key on `error.code` as the guide already says" and the
`X-Session-Id` retention correction (sibling products must not put personal data in it);
`contracts.md` newhub section one line "CORS exposes X-Request-Id / X-RateLimit-* / Retry-After /
X-Request-Cost / X-Quota-Remaining / X-Lurus-Instance to allowed origins"; 2l-bs-docs regenerates
from `docs/openapi/relay.json`. Owner question: English twin of the Chinese-only guide.

## 7. Lane summary (binding order)

| # | Lane | kind | effort | depends on | risk posture |
|---|---|---|---|---|---|
| L1 | Tenant model allow-list + typed endpoint + dead handlers deleted | mixed | M | — | observe default; enforce only by env; fails open |
| L2 | `/v1/models` + retrieve from the tenant-routable set | fix | S | L1 | discovery narrows to truth; 404 for unroutable retrieves |
| L3 | Leader gauge, per-task last-success, instance identity, `checks.leader`; bounded delete batches; audit interval env | mixed | L (3 steps) | — | additive signals; identical row sets; manifests declarative |
| L4 | Canonical pricing name for cache/cache-write/image ratios | fix | S | — | lookup-only; exact key wins; matrix stays green |
| L5 | Console truth: Settings honesty + `useTenantModels` | mixed | M | — | web only |
| L6 | Wire-contract truth: English 429, CORS, locks, guide, `.env.example` | mixed | M | L1, L2 (docs), L3 (`.env` row, `X-Lurus-Instance`) | message text + additive CORS metadata + docs |

Effort total is above one calendar day for a single agent; the lanes are sequential and
individually acceptable, and L2 then L4 are the designated slips (§1).

## 8. File disjointness (every file, one owner)

| File | Owner |
|---|---|
| `internal/app/tenantpolicy/allowlist.go`, `allowlist_test.go` | L1 |
| `internal/adapter/middleware/distributor.go`, `distributor_tenant_allowlist_test.go` | L1 |
| `internal/pkg/metrics/tenant_policy.go`, `tenant_policy_test.go` | L1 |
| `internal/adapter/handler/tenant_model_allowlist.go`, `tenant_model_allowlist_test.go`, `tenant.go`, `cover_r2_billing_test.go` | L1 |
| `internal/adapter/handler/router/api-v2-router.go`, `handlers_are_routed_test.go`, `v2_completeness_test.go` | L1 |
| `internal/adapter/repo/ability.go`, `ability_tenant_models_test.go` | L2 |
| `internal/adapter/handler/model.go`, `model_tenant_scope_test.go`, `model_discovery_contract_test.go` | L2 |
| `internal/pkg/common/leader.go`, `instance.go`, `instance_test.go` | L3 |
| `internal/pkg/metrics/instance.go`, `instance_test.go` | L3 |
| `internal/lifecycle/leader_election.go`, `leader_election_test.go`, `leader_step_test.go`, `audit_cleanup.go`, `audit_cleanup_test.go` | L3 |
| `internal/adapter/handler/router/main.go`, `metrics_instance_test.go` | L3 |
| `internal/adapter/handler/health.go`, `health_test.go` | L3 |
| `internal/adapter/repo/log.go`, `log_retention_batch_test.go`, `audit.go`, `audit_e3_test.go` | L3 |
| `deploy/k8s/r6-stage/deployment.yaml`, `deploy/k8s/r6-uat/deployment.yaml`, `doc/runbook/ha-deployment.md`, `doc/runbook/database.md` | L3 |
| `internal/pkg/setting/ratio_setting/cache_ratio.go`, `model_ratio.go`, `cache_ratio_normalise_test.go` | L4 |
| `web/src/pages/v2/Settings/index.jsx`, `index.test.jsx`; `web/src/pages/v2/Billing/index.jsx`, `index.test.jsx`, `balance.test.jsx` | L5 |
| `web/src/hooks/models/useTenantModels.js`, `useTenantModels.test.jsx`; `web/src/pages/v2/Playground/index.jsx`, `index.test.jsx`; `web/src/pages/v2/Models/index.jsx`, `index.test.jsx`; `web/src/pages/v2/CommandPalette/index.jsx`, `index.test.jsx`; `web/src/pages/v2/no_inline_models_fetch.test.js` | L5 |
| `web/src/i18n/locales/en.json`, `zh.json` | L5 |
| `internal/adapter/middleware/business_rate_limit.go`, `concurrency_limit.go`, `model-rate-limit.go`, `rate_limit_message_lock_test.go`, `cors.go`, `cors_headers_test.go` | L6 |
| `internal/adapter/handler/router/openapi_contract_lock_test.go` | L6 |
| `internal/adapter/handler/guide_session_id_lock_test.go` | L6 |
| `docs/openapi/relay.json`, `doc/product-integration-guide.md`, `.env.example` | L6 |

Overlap check performed per package: `router/` (L1 api-v2-router/handlers_are_routed/
v2_completeness; L3 main/metrics_instance; L6 openapi_contract_lock), `handler/` (L1 tenant*,
cover_r2; L2 model*; L3 health*; L6 guide lock), `metrics/` (L1 tenant_policy; L3 instance),
`middleware/` (L1 distributor; L6 cors/biz/cc/model-rate-limit), `repo/` (L2 ability; L3 log/audit),
`lifecycle/` (L3 only), `web/` (L5 only; en/zh.json owned by L5 alone because L6 writes no locale
key). No file has two owners.

## 9. do_not_regress (every lane's acceptance re-checks the items its files touch)

- Streaming-safety failover gate ahead of every retry rule, suppression counted — `relay.go:662`
  (`shouldRetry` :646) and `:937` (`shouldRetryTaskRelay` :929); no lane touches `relay.go`.
- Circuit breaker fed only by upstream-attributable failures — `relay.go:491` gated by
  `types.IsUpstreamFailure` (`error.go:498`); `RelayErrorType` classes (`:539`) unchanged.
- Per-attempt route trace persisted on the request's own log row with a hard cap —
  `internal/app/route_attempts.go:27-82` (catalog); untouched.
- Priority-tier fallback tenant-scoped before the weighted draw, live EWMA-shaded weights,
  cross-group auto retry, session-affinity re-pin — `channel_cache.go`, `smart_routing.go`,
  `channel_scorer.go`, `channel_select.go` (catalog); untouched.
- All-keys-cooling → 503 + Retry-After; generic `RetryAfterUnix` → Retry-After — `relay.go:185-207`
  (catalog); untouched.
- Terminal-error headroom hygiene: upstream Retry-After forwarded and the admit-path
  `X-RateLimit-*` snapshot stripped on upstream-originated failures — `relay.go:218-230`,
  `isUpstreamOriginatedError` `:639`, `rate-limit.go:236-253` (catalog); L6 keeps
  `ClearRateLimitHeadroomHeaders` ordering at `bizReject :389` / `ccReject :210`.
- Retry re-selection stays inside the tenant boundary — `relay.go:587` `getChannel` (catalog
  `:599-611`); untouched.
- Bounded key cooldown deadlines and master-only reaper — `openrouter_pool/cooldown.go`,
  `reaper.go` (catalog); untouched.
- In-process env-gated fault simulator — `faultsim.go:48` `FaultSimEnabled`, `:85`
  `faultSimAuthorized`, wiring lock `router/faultsim_wiring_test.go` (catalog); untouched; NOT
  enabled on UAT (`grep -c FAULTSIM deploy/k8s/r6-uat/deployment.yaml` = 0).
- In-band wire-native error frames after a stream has started; `StreamEndReason` → status
  `client_gone`; abnormal stream end neither billed nor quoted — `relay_outcome.go`,
  `relay-openai.go` (catalog); untouched.
- Cost/quota perception headers and `x_lurus` field names — `perception.go:133`
  `SetPerceptionHeaders`, `:95` `ComputeLurusExtension`, `types/lurus_extension.go`; untouched.
- Cache pricing keyed on wire semantics (`PromptTokensIncludeCached`) and the Anthropic-wire
  cache-write 1.25 default — `dto/openai_response.go`, `types/price_data.go` (catalog); L4 changes
  only which map key the three getters read, and states in the lane report that an operator's
  wildcard cache-write entry now wins over the wire default (intended).
- Decimal end-to-end settlement with round-half-up parity and the `ChargeableInputNonZero` floor —
  `quota.go`, `compatible_handler.go` (catalog); the billing invariance matrix
  (`billing_invariance_matrix_test.go`, `provider_billing_census_test.go`) stays green under L4
  (no formula change).
- Product attribution resolved once and written to every row — `governance.go:105-110` (catalog);
  `session_id` write at `:117` documented by L6, never changed.
- Default-deny projection gate — `log_other_projection_lock_test.go:359`; `request_id`/`session_id`
  `TierPublic` (`classification.go:74`); no lane adds an `other.*` key.
- `X-Session-Id` bounded before it touches the row: ≤200 bytes, printable ASCII, dropped not
  truncated — `relay_info.go:434-444`; L6 documents it, never relaxes it.
- `/v1/generation` ownership enforced in the repo, 404 for unowned ids; server-side JSON filters
  tenant-scoped — `repo/log.go` (catalog); untouched.
- Sensitive-word rejection semantics before channel selection — `relay.go:331-337`; untouched.
- Rate limiting fails open with an explicit log; Retry-After and Reset describe the same instant;
  TPM from settled usage only — `business_rate_limit.go` (catalog `:355-366`, `:129-137`,
  `:409-431`); L6 changes message text only.
- Tightest-scope headroom collapsed into ONE `X-RateLimit-*` set; reject sites erase any earlier
  admit snapshot (`bizReject :389`, `ccReject :210`); opt-out `RATE_LIMIT_HEADERS_ENABLED`
  (`.env.example:258`); L6 keeps every header write listed in its spec.
- Three enforcement scopes with 0 = unlimited and admin validation (`entity/token.go`,
  `entity/tenant.go`, `entity/model_rate_limit.go`, catalog); L6 leaves `ValidateRateLimits`'
  admin-facing text alone.
- Relay gate order — `relay-router.go:100-121` (`StampRelayFormat`, `TokenAuth`, …,
  `BusinessRateLimit`, `RelayConcurrencyLimit`, `Distribute` + `BusinessModelRateLimit`); no lane
  reorders it.
- Model denial is a typed 403 `model_blocked` with wire-native type (`distributor.go:97/:107`,
  `types/error.go:113`); unknown-vs-down discrimination 404 only when no ability row exists, 503
  otherwise (`distributor.go:177-195`) — L1 inserts before `:47` and never touches these.
- Tenant-scoped channel selection and fail-closed confinement of the `sk-<key>-<channelId>` pin to
  the caller's tenant — `distributor.go:47-90`, `:156-165`; `repo/ability.go:59-67`
  `abilityTenantScope`; `tenant_relay_selection_test.go`. L2 adds a tenant-scoped variant beside
  the tenant-blind query, never changes selection.
- Every middleware-stage rejection leaves a queryable error-log row — `middleware/utils.go:17`
  `abortWithOpenAiMessage`; L6's memory-backend sites now go through it (more rows, not fewer).
- `/v1/models` hides models without a configured ratio/price unless self-use or opted in
  (`model.go:115-125`, `:139-144`, `:183-188`); Anthropic branch safe on an empty catalogue
  (`model_discovery_contract_test.go:49`); `RetrieveModel` answers a wire-native 404 (`:283-306`)
  — L2 keeps the envelopes byte-identical.
- Structural gates every new rejection/metric must pass: every abort site carries a code
  (`abort_code_structural_test.go:29`, floors `:82-88`); `relay.json` code enum bidirectionally
  locked (`openapi_contract_lock_test.go:263`) and every documented 429 carries Retry-After +
  Scope (`:198`, extended by L6); every declared series has a production writer
  (`declared_series_written_test.go:146`); no undeployed alerting described
  (`alert_wiring_honesty_test.go`).
- Every by-id v2 mutation classified swept-by-named-test or exempt-with-reason
  (`v2_completeness_test.go:31-60`); L1 adds RootJWTAuth entries in the `:113-126` style.
- Tenant decision is a compile-time argument for every cross-user log query and delete —
  `repo/log.go:60-89` `TenantScope` fail-closed (catalog); `DeleteHistoryLogs` scopes tenant admins
  (`handler/log.go:250-256`); L3 keeps `scope.apply` inside the id subquery and adds no
  tenant-facing purge.
- Leader-only background work is a DB lease with catch-up-on-acquire
  (`leader_election.go:125-175`); audit retention is leader-only (`audit_cleanup.go:41/:51`) —
  L3 makes it batched as well; lease TTL/renew math (`:12-14`) untouched.
- Health body distinguishes intentional-off states from faults (`health.go:102-107`) — L3
  registers `held`/`standby` there so a follower is never degraded; the HTTP code moves only on
  a failed database check (`:87-90`).
- `/metrics` double-guarded (`metricsAuthMiddleware` `router/main.go:85` + host nginx 404) — L3 adds
  a header middleware after the auth middleware, never before it.
- Rehearsal endpoints with an explicit `?mode=` that rejects typos (`internal_maintenance.go`,
  catalog) — no new endpoint this cycle.
- Narrow internal keys are fail-closed per tenant; `/internal/balance/topup` and
  `/internal/quota/adjust` deliberately carry no tenant guard — owner item, not lane work.
- At-rest ciphertext versioned (`totp.go` `v1:` prefix); `CryptoSecret` falls back to
  `SessionSecret` (`init.go:58-62`, catalog) — removing the fallback would brick existing TOTP rows.
- Command palette degrades per data source with `Promise.allSettled` and drops empty groups
  (`CommandPalette/index.jsx:86`, test `:174`); `useTenantSlug` resolves the slug synchronously
  (`hooks/common/useTenantSlug.js:18`) — L5's hook feeds the palette without re-coupling it.
- Log detail panel renders nothing it cannot prove; Settings MFA panel reports "unavailable"
  rather than "enabled" on failure (`Settings/index.test.jsx:543`) — L5 converges on this rule.
- Inbound error-body parsing keys on `Code` / vendor `Type` — `app/channel.go:86-120`; L6's
  message change is safe by construction.
- Pinned consumer shapes: `GET /api/v2/switch/user/info`, `POST /api/v2/switch/user/topup`,
  `GET /api/v2/switch/app/releases/latest`, `POST /api/v2/lutu/search`, `/internal/v1/*`
  provisioning/fund/redemptions — untouched.
- Migration ledger: ID 033 remains `(next available)` (root `doc/coord/migration-ledger.md:134`);
  no lane writes under `migrations/`.

## 10. Cross-repo follow-ups (owner executes in the root repo; no lane writes there)

1. `doc/coord/contracts.md` newhub section: (a) three admin endpoints
   `GET/PUT/DELETE /api/v2/admin/tenants/:id/model-allowlist` + `TENANT_MODEL_ALLOWLIST_MODE`
   (observe default); (b) a `/v1/models` row (currently absent) stating per-key tenant scope and
   that `/v1/models/:model` may 404; (c) one line on the CORS-exposed header list.
2. `doc/coord/changelog.md` newhub: 429 `error.message` language now English (key on `code`);
   `X-Session-Id` retention correction; `/api/health` additive keys `checks.leader` / `instance`
   and header `X-Lurus-Instance`.
3. `doc/coord/service-status.md` newhub: allow-list observe-only until the owner flips; new
   health keys; `POD_NAME`/`POD_NAMESPACE` manifest env.
4. 2l-bs-docs: regenerate from `docs/openapi/relay.json`; mirror the corrected `X-Session-Id` row,
   the 429 header table, the `/v1/models` sentence.
5. Host netdata go.d job `newhub`: after L3's production interleave evidence, one job per pod IP
   (or a headless service) so per-pod series stop interleaving — infra, not code.

## 11. Owner items (decisions no lane may take)

1. Migration 033 stays unspent; decide C24 (per-key renewing budget) vs ATTR-1 (`source_product`
   column) before the next cycle reserves ledger line 134.
2. When UAT and then production flip `TENANT_MODEL_ALLOWLIST_MODE=enforce` (L1 ships observe).
3. Inject `FAULTSIM_TOKEN` into `deploy/k8s/r6-uat/deployment.yaml` (declaratively, via the secret
   template; `grep -c FAULTSIM` = 0 today, control `E2E_BRIDGE_TOKEN` at `:134`) OR accept the
   faultsim-free probe recipe (root `PUT /api/option {"key":"RetryTimes","value":"1"}` +
   same-priority probe channel on an unresolvable public host, weight 100 vs 1) as the acceptance
   path for next cycle's NH6-05/06/07 routing lane. Record the production `RetryTimes` option value
   before flipping anything.
4. `BIZ_TPM_MEASURE` default (tokens vs quota) — carried from cycle 5 §11.5; NH6-10 ships only when
   this is decided (its histogram-only variant can ride a later lane as evidence).
5. Production log-retention window (recommend ≥ 90 days given billing-evidence use) before next
   cycle's scheduled-retention lane (§12 NH6-11 remainder).
6. English twin of the Chinese-only integration guide.
7. Pool reset semantics before `CREDIT_POOL_RESET_MODE=enforce` on production (carried).
8. Deprecation window for the `X-Oneapi-Request-Id` alias (carried).
9. Rule carried: never guard `/internal/balance/topup` or `/internal/quota/adjust` with a tenant
   whitelist (platform-core's `balance:write` key has no whitelist rows).
10. After L3 lands: whether per-series pod labels (D-05) are still needed once the interleave is
    evidenced by `X-Lurus-Instance`.

## 12. Deferred (with the reason and the spec to reuse)

- **NH6-05 retry same-tier exclusion** — highest-value routing change (Δ0.5) but a default-ON
  change in the retry loop with no production observable (one channel; `RetryTimes` default 0) and
  an L-sized blast radius (both judges). Spec to reuse verbatim (architect L6-1): `RetryParam`
  (`channel_select.go:14-25`, catalog) gains `ExcludeChannelIDs []int`; `getChannel`
  (`relay.go:587`) fills it from `c.GetStringSlice("use_channel")` (task relay inherits via
  `:855/:880`, catalog); repo `GetRandomSatisfiedChannelForTenantExcluding` filtering AFTER the
  tenant filter and BEFORE the single-candidate short-circuit, advancing to the next lower tier
  when the target tier empties and falling back to the unfiltered draw when every tier is empty
  (never fail selection purely by exclusion); DB path `getChannelQuery` (`ability.go:108`) gains
  `channel_id NOT IN (?)` with the same fall-through; kill-switch `RELAY_RETRY_EXCLUDE_USED`
  (default on) read fresh per call; own metrics file `route_policy.go` with
  `lurus_gateway_relay_retry_exclusion_total{result=excluded|fell_through|unfiltered}`. Oracle:
  two equal-priority channels behind httptest upstreams (A weight 100 → 500, B weight 1 → 200),
  `RetryTimes=1`, 20 requests all 200 with `route_attempts` exactly `[A upstream_error, B success]`
  (red on HEAD with p ≈ 1 − (11/121)^20). UAT: the faultsim-free recipe in owner item 3.
- **NH6-06 code half (`X-Lurus-Attempts` / `X-Lurus-Upstream-Model`)** — rides NH6-05 so the
  attempts count is meaningful. Spec: `helper.SetRouteHeaders(c, attempts, upstreamModel, mapped)`
  called right after `addUsedChannel` (`relay.go:408`, catalog) and in the deferred renderer after
  `ClearRateLimitHeadroomHeaders`; `X-Lurus-Attempts = len(use_channel)`, `X-Lurus-Upstream-Model`
  only when `IsModelMapped` (`relay_info.go:73-74`); never channel id/name/URL; lane must grep
  providers for `Header().Del(` before relying on pre-dispatch headers; add both names to
  `CORSExposedHeaders` (L6's lock (b) then forces their `relay.json` rows); extend
  `TestOpenAPIContract_ChatClassPathsDocumentRequestId` (`:163`) to require `X-Lurus-Attempts`.
- **NH6-07 `error.metadata`** — rides NH6-05/06. Spec: `(*NewAPIError).MergeMetadata(map[string]
  any)` adds only ABSENT keys (upstream OpenRouter `raw`/`provider_name` never clobbered); renderer
  merge only when `isUpstreamOriginatedError` (`relay.go:639`): `{provider_name, upstream_status,
  error_class: types.RelayErrorType, gateway_code, request_id, attempts}`; only constant strings
  and integers (Metadata bypasses masking); Anthropic/Google wires unchanged; in-band stream
  frames inherit via `helper.StreamError`. UAT without faultsim: a probe channel on an
  unresolvable public host.
- **NH6-04 one token-quota core** — the architect placement (helper → app) fails with an import
  cycle in test (`r5a_percall_floor_wiring_test.go` is `package app` and imports helper); the core
  must live in a leaf package (e.g. `internal/pkg/billing/quotacore`) imported by `app`, `relay`
  and `helper`. Spec otherwise as catalog: `TokenQuotaTerms{BillablePrompt, CacheRead,
  CacheWrite5m, CacheWrite1h, CacheWriteRemainder, Image, Audio, Completion; ratios}` +
  `TokenQuotaCore(t) decimal.Decimal`; both settlement paths build terms AFTER their
  `PromptTokensIncludeCached` deductions and keep every add-on/floor/Round after the core;
  `perception.go:21` `EstimateQuotaFromUsage` builds the same terms (adds the 1h split and audio
  term it lacks today) and the invariance matrix gains a third column with a new Anthropic 1h
  cache-write row. First billing lane next cycle, ahead of C04 / COST-BREAKDOWN.
- **NH6-03 variant-fallback half** — `CanonicalPricingName(name) (canonical, isVariant)` strips one
  trailing `:variant` and every `@key:value` modifier after an exact miss; `MODEL_VARIANT_PRICING`
  observe|enforce (default observe) read fresh per call; observe counts
  `lurus_gateway_model_pricing_fallback_total{source=variant,mode}` from `helper.ModelPriceHelper`
  (`price.go:48`, the one production caller with request context); enforce returns the canonical
  ratio in `GetModelRatio` (`:458`) / `GetModelPrice` (`:420`) after the exact miss and before
  family fallback, and `GetModelPricingSource` (`:494`) reports `variant_fallback`. No live buyer
  sends variant names today.
- **NH6-10 TPM in tokens** — owner item 4 first. Spec (buyer L1): parallel
  `rl:biz:tpm:tokens:tok:/tenant:/model:` keys beside `business_tpm.go:45-47` (catalog),
  `PostConsumeQuotaWithUsage` as the usage-carrying variant with `PostConsumeQuota` the nil
  wrapper, readers select the window by `BIZ_TPM_MEASURE`, histogram
  `lurus_gateway_rate_limit_tpm_tokens_per_quota{scope}` (namespace-correct name) written in the
  `quota.go:1022-1033` closure.
- **NH6-11 remainder (scheduled log retention)** — `LOG_RETENTION_DAYS` (0 = idle, 3-day floor),
  `LOG_RETENTION_MODE` observe|enforce, `LOG_RETENTION_INTERVAL_SECONDS`, `NewLeaderTask("log-
  retention", …)` started from the master-only block in `cmd/server/main.go`, own metrics file
  `retention.go` (`candidates` gauge, `deleted_total`, `last_run_timestamp_seconds`),
  `POST /internal/admin/log-retention?mode=observe|enforce&days=N` (ScopeAdmin; `?days=` honoured
  in observe only), runbook section in `doc/runbook/database.md`. L3's batching and (if it lands)
  L3's `RecordLeaderTaskSuccess` make it a pure wiring lane next cycle. Consumer note to carry:
  `/v1/generation` lookups older than the window 404 once enforce is on.
- **D-05 per-series pod label** (`METRICS_INSTANCE_LABEL`, labelled Gatherer) — re-keys every
  netdata chart; revisit only if L3's interleave evidence shows per-series attribution is needed.
- **D-06 last-success for non-LeaderTask leader loops** — one-line `metrics.RecordLeaderTaskSuccess`
  calls in `credit_pool_reconcile.go:368-378` (catalog), `privacy_erasure.go`,
  `openrouter_pool/reaper.go`, `openrouter_sync/scheduler.go`; deferred to keep L3 disjoint from
  `package app` and from the untouchable `openrouter_sync/coverage_extra_test.go`.
- **NH6-14 Log page request_id/session_id filters + deep link** and **NH6-15 CSV export parity**
  (`v2_log_export.go:48-54/:166-176`, catalog) — one console lane next cycle on L5's hook pattern;
  specs as catalog (filters object, URL seeding, detail-panel testids, click-to-filter; Go export
  reads `source_product/request_id/session_id` and appends columns LAST).
- **NH6-18 v2 redemption PUT** — status write on money-adjacent data plus the api-v2 openapi
  redeem-path drift (`api-v2.json:1036`, catalog); spec as catalog (used = terminal 409, swept
  completeness entry, masked key, audit `redemption.updated`); collides with L1's router files this
  cycle.
- **NH6-19 `tokens_processed_total` cache types** — additive counter from the settlement record
  (`repo/log.go:506`); rides NH6-04's terms struct.
- **G1-04 retry backoff**, **G1-05 observable route health**, **G1-06 generic multikey cooldown**,
  **G1-07 per-channel timeout budgets** — all after NH6-05; specs unchanged from cycle 5 §12.
- **G2-4 IETF RateLimit structured headers** — no consumer asking; L6's `CORSExposedHeaders` and
  lock (b) force `RateLimit`/`RateLimit-Policy` to be exposed and documented together when it lands.
- **G2-5 tenant daily token budget** — depends on NH6-10 and owner item 4; reuses L1's
  `tenant_configs` + observe/enforce + typed-endpoint pattern.
- **G3-3 priced model catalog** — no registered consumer of `/internal/models/catalog`; needs
  NH6-04's quote helper and L2's tenant-scoped ability query (now available).
- **G3-4 `/v1/key` effective models** — one-hour follow-up on `key_info.go` once L1's loader exists
  (`tenant_model_allowlist` null = not configured, `[]` = deny all, plus `mode`).
- **C04 usage on every wire**, **COST-BREAKDOWN** — after NH6-04; need switch/lutu tolerance
  confirmation for extra keys inside vendor usage objects.
- **C18 audit changesets** — empty details on channel-update audit rows (`v2_channel.go:547-548`,
  `channel.go:1336-1337`, catalog); M across five write handlers with redaction risk.
- **G5-5 internal-seam guard consolidation** — six inline `InternalKeyAllowedForTenant` copies and
  un-coded internal denials; touches ~25 test files and 403 wording platform-core sees; rule: never
  guard `/internal/balance/topup` or `/internal/quota/adjust`.
- **G5-6 / C23 at-rest key runbook** — owner-led secret provisioning on R6; code half
  (`AtRestKeySource` + `checks.at_rest_key`) rides the next ops lane; `init.go:58-62` fallback must
  stay.
- **C24 token periodic budget**, **ATTR-1 source_product column** — need migration 033 (owner item 1).

## 13. Next cycle

1. First routing lane: NH6-05 exclusion + NH6-06 headers + NH6-07 metadata as one lane on the
   faultsim-free probe (owner item 3), then G1-04 backoff on top.
2. First billing lane: NH6-04 core in a leaf package (import-cycle-safe), then NH6-03 variant
   fallback, C04, COST-BREAKDOWN, NH6-19.
3. Ops lane: NH6-11 scheduled retention (pure wiring on L3's batching), D-06 task timestamps,
   G5-6/C23 code half; D-05 only on evidence.
4. Console lane: NH6-14 + NH6-15 on L5's hook pattern; NH6-18 redemption PUT.
5. Governance follow-ups on L1's pattern: G3-4 `/v1/key` effective models; G2-5 daily token budget
   once owner item 4 is decided (NH6-10 first).
6. Migration 033 decision (owner item 1).
7. Re-score §3 against live data: first real relay traffic on production, TTFT distribution,
   `tenant_model_denied_total`, leader gauge interleave, headroom headers observed by the switch
   client.

## 14. Operator amendments after the vet (binding; override the lane text above where they conflict)

Vet outcome: L2, L3, L5, L6 approved; L1 and L4 majority-refuted. Both refutations were re-verified on
HEAD d1351475 by the operator and are salvageable with the spec changes below, so all six lanes run in
the binding order L1 → L2 → L3 → L4 → L5 → L6. Spot-checks the operator ran before development:
`gorm.io/driver/postgres@v1.5.11` `postgres.go:77` DeleteClauses = DELETE/FROM/WHERE (no LIMIT), so
`repo/log.go:803` and `repo/audit.go:82-84` issue one unbounded DELETE; `AUDIT_CLEANUP_INTERVAL_SECONDS`
appears only in the comment at `audit_cleanup.go:18`; `Playground/index.jsx:163` maps an object returned
by `v2_models.go:108-116`; `model-rate-limit.go:270-277/:283-289` reject with no body;
`model.go:173/:181` call the tenant-blind `GetGroupEnabledModels` (`ability.go:31-36`) while
`abilityTenantScope` (`:59-67`) exists; `GetTenantConfigs`/`UpdateTenantConfig` (`tenant.go:467/:498`)
have zero references under `router/` and `cmd/`; `cache_ratio.go:149-157` looks up the raw name while
`model_ratio.go:420-424` normalises first.

- **L1 (salvaged)** — `repo.GetTenantConfig` (`tenant_config.go:26-35`) re-wraps `gorm.ErrRecordNotFound`
  into a fresh `errors.New("config not found")`, so the spec's `errors.Is(err, gorm.ErrRecordNotFound)`
  branch can never fire and every unconfigured tenant would take the "fail open + log" branch on every
  relay. Fix: export `var ErrTenantConfigNotFound = errors.New("config not found")` from
  `internal/adapter/repo/tenant_config.go` and return it (same text) from `GetTenantConfig`;
  `tenantpolicy.LoadModelAllowlist` treats `errors.Is(err, repo.ErrTenantConfigNotFound)` as
  `(nil, configured=false, nil)`. `internal/adapter/repo/tenant_config.go` and its test file (extend the
  existing one that covers this file, or add `tenant_config_test.go`) join L1's file list. Add a hermetic
  lock in `internal/app/tenantpolicy`: the missing-row case returns a nil error (so the middleware's
  log branch is unreachable for it) — the acceptor must revert the sentinel and see it go red. The
  middleware helper `seedTenantRelayChannel` (`middleware/tenant_relay_selection_test.go:30`) has the
  signature `(t, db, id, tenantID, weight)` and seeds its own fixed model; the distributor oracle must
  use that model name or seed the ability row explicitly.
- **L2** — the repo oracle's seed helper is `internal/adapter/repo/channel_cache_tenant_test.go:23`
  `seedTenantRelayChannel(t, id, tenantID, group, model)` (in-package), not the middleware file. If
  `internal/app/tenantpolicy` is absent on the tree when L2 starts (L1 aborted), implement the
  tenant-routable half only and report the allow-list filter as SKIPPED_DEPENDENCY.
- **L4 (salvaged, narrowed)** — `defaultCreateCacheRatio` (`cache_ratio.go:78-108`) has no runtime write
  path (`option.go:420-441` has cases for CacheRatio and ImageRatio, none for cache creation), so no
  operator can hold a wildcard cache-write entry. The money defect is real only for `GetCacheRatio`
  (`cacheRatioMap`, writer `option.go:436`) and `GetImageRatio` (`imageRatioMap`, writer `:438`).
  `GetCreateCacheRatio` may be normalised for consistency, but the lane's acceptance criteria, consumer
  note, comments and the guide line must not claim any cache-write billing change; strike "cache-write"
  from the acceptance bullet and replace the consumer sentence about the 1.25 default with "cache-write
  lookup is now consistent with the other getters; no operator-visible change because that map has no
  admin write path". The oracle for `GetCreateCacheRatio` stays hermetic-only and is labelled as such.
- **L5** — the `no_inline_models_fetch` literal lock must match only `API.get(` calls whose argument
  contains `/models` under `web/src/pages/v2`; the add-model `API.post` at `Models/index.jsx:174` stays
  and must not trip the lock.
- **L6** — `abort_code_structural_test.go` counts abort sites; the two new `abortWithOpenAiMessage` sites
  in `memoryRateLimitHandler` raise the middleware floor, which the lane updates upward (never down).

Carried from the TokenHub 0.8.0 comparison (`tokenhub-0.8.0-comparison-2026-09-09.md` §5) into §13 for
the next cycle: breaker exponential backoff rides G1-04/NH6-05; guardrail `mask` action rides C15 after
the dead options are deleted; invoice USD conversion (`v2_billing_invoices.go:23`) and CSV export are new
S candidates; migration expand/contract phase gate is a new M candidate for the ops lane.
