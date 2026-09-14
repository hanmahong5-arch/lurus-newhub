# New API parity — cycle 7 plan (2026-09-12)

HEAD at synthesis: `2011f968` (main). Inputs: `newapi-parity-matrix-2026-09-12.md` (145 corrected
gap rows), three lens plans (operator / enterprise-buyer / architect), two judge reports. Every
`file:line` below was re-read on this HEAD during synthesis; nothing is carried over from a lens plan
unverified.

## 1. Mandate & method

Owner mandate (verbatim intent): newhub offers no fewer features than upstream New API and is more
robust, reliable, leading and valuable; platform (`2l-svc-platform`) changes are authorised when
newhub needs them.

Method:

1. Spine = the highest-scoring lens plan (enterprise-buyer, §2).
2. Grafted the judges' "best ideas from losers" where they survive verification.
3. Removed every lane a judge called fatal unless the judge could be shown wrong (§5 records each
   removal or rebuttal with evidence).
4. Re-anchored every surviving lane on the real tree: exact files, routes, defaults, test names,
   and a UAT probe whose artefact can only exist if the behaviour runs.
5. Ordered lanes so earlier lanes are never re-touched by later ones; file overlaps are declared
   per lane (§3, "files") and summarised in §4.

Architectural invariants (a lane violating one is disqualified): tenant-scoped state; identity in
the platform IdP; money through the platform when unified billing is on; PG-only with migrations
from 033 (ledger reservation first); Prometheus `/metrics` scraped by netdata, no stack change;
every existing gate stays green; every lane provable on `https://test-newhub.lurus.cn`; production
read-only.

## 2. Scoreboard (judge scores)

| Lens | Judge 1 | Judge 2 | Total | Rank |
|------|---------|---------|-------|------|
| enterprise-buyer | 66 | **80** | **146** | 1 — spine |
| operator | **78** | 60 | 138 | 2 — grafts |
| architect | 47 | 64 | 111 | 3 — two grafts, one rebuttal |

Judge 1 ranked the operator plan first on citation accuracy; judge 2 ranked the buyer plan first on
mandate fit ("wins the deal"). Summed, buyer wins by 8 points. The operator plan's accuracy is
preserved by re-anchoring every buyer lane on verified `file:line` and by adopting its pricing audit
event, audit-coverage CI test, unaudited-write counter, per-channel HTTP/1 override and affinity
purge. Architect grafts adopted: `cache_ratio` in the pricing patch whitelist; the two-tenant
isolation mutation oracle for rankings. Architect ideas rejected with evidence: §5.

## 3. Lanes (binding order L1 → L7)

Common conventions for every lane:

- Error envelope: `{"success":false,"message":"…","error_code":"…"}` (existing shape,
  `v2_pricing_write.go:56-60`).
- Every new admin write route is registered under `adminRoute` (`api-v2-router.go:357-358`,
  `RootJWTAuth`) or under an existing `UserAuth` + `TenantSlugGuard` group, never a new auth tier.
- Every new v2 route gets a `swept`/`exempt` entry in
  `internal/adapter/handler/router/v2_completeness_test.go` (`TestV2IDOR_Completeness :32`).
- Every new console call gets an entry that
  `router/frontend_route_contract_test.go:116 TestConsoleCallsResolveToRegisteredRoutes` resolves.
- Every new Prometheus series must have a production writer
  (`internal/pkg/metrics/declared_series_written_test.go TestDeclaredSeriesHaveAProductionWriter`).
- UAT login for probes: `POST /api/v2/bridge/exchange` with `E2E_BRIDGE_TOKEN` and `user_id=1`
  (seeded root; `deploy/k8s/r6-uat/README.md:41-48`). `RootJWTAuth` falls back to the session
  cookie when no `Authorization` header is present (`middleware/admin_jwt_auth.go:67-73`), so a
  bridge cookie reaches `/api/v2/admin/*` on UAT.

### L1 — pricing-integrity: optimistic version lock, DB-first persistence, dry-run preview, `cache_ratio`, audit row

**Why.** `POST /api/v2/:tenant_slug/pricing` (`api-v2-router.go:252-261`, handler
`v2_pricing_write.go:53 UpdatePricingV2`) moves live billing ratios with: (a) no concurrency guard —
`persistRatioMapIfChanged` (`:206-248`) does marshal → `updateFn` (in-memory, `:243`) →
`repo.UpdateOption` (`:247`, `repo/option.go:211`), last write wins; (b) memory mutated before the
DB write, so a failed DB write leaves the process serving ratios the database does not hold (the
comment at `:245-246` calls this "best-effort"); (c) zero audit rows — `grep -n "governance\|
RecordAuditEvent" v2_pricing_write.go` → 0 hits, and `audit_action.go` has no pricing action; (d)
`cache_ratio` cannot be patched — `updatePricingRequest :33-38` has only model/completion/price,
while the sync whitelist `ratio_sync.go:47 ratioTypes` already includes `cache_ratio`, so an
operator can import a cache ratio from upstream but cannot edit it. Who notices: the tenant/platform
admin editing prices (silent clobber, no preview), the finance reviewer (no "who changed what"),
the customer billed by a ratio nobody can trace.

**Gap ids.** billing-pricing-02, billing-pricing-03, billing-pricing-30 (version tracking, closed by
the counter), auth-security-16 (pricing slice only).

**Spec.**

1. Version counter: option key `PricingVersion` (int64 as decimal string) in the existing `options`
   table (`repo.UpdateOption`, `entity.Option`); no new column, no migration. Read via the options
   map the same way `ratio_setting` reads its maps.
2. `GET /api/v2/:tenant_slug/pricing` (`v2_pricing.go GetPricingV2`) adds top-level
   `data.version` (int64; 0 when the key has never been written).
3. `POST /api/v2/:tenant_slug/pricing` request stays a JSON array (console sends `batch`,
   `web/src/pages/v2/Pricing/index.jsx:124`). Add **optional** header `If-Match-Pricing-Version:
   <int64>` (header, not body, so the array body shape is unchanged for existing callers). If the
   header is present and `!= PricingVersion` → `409 {"error_code":"PRICING_VERSION_CONFLICT",
   "current_version":N}` and **nothing** is written (memory or DB). If absent → guard skipped
   (rollout compatibility; owner decision O1 governs when it becomes required).
4. Persistence order in `persistRatioMapIfChanged` becomes DB-first: `repo.UpdateOption` →
   `updateFn` (in-memory). On DB failure the in-memory map is untouched and the handler answers
   `500 PERSIST_FAILED` as today. The three persists plus the `PricingVersion` increment run inside
   one `repo.DB.Transaction`; the in-memory `updateFn` calls run only after commit. Compare-and-set
   on the version: `UPDATE options SET value=$new WHERE key='PricingVersion' AND value=$expected`
   (rows affected 0 → 409) so two admins racing with the same header cannot both win.
5. `updatePricingRequest` gains `CacheRatio *float64 json:"cache_ratio,omitempty"`, validated `> 0`
   like the others, persisted through a fourth `persistRatioMapIfChanged(items, "CacheRatio",
   ratio_setting.GetCacheRatioCopy(), ratio_setting.UpdateCacheRatioByJSONString)`
   (`cache_ratio.go:183`, `:135`).
6. Response adds `data.new_version`.
7. `POST /api/v2/:tenant_slug/pricing/preview` — same auth chain (`UserAuth` + `TenantSlugGuard`
   + `requirePlatformRoot` inside the handler, exactly as `UpdatePricingV2`), same array body,
   returns `{"success":true,"data":{"version":N,"diffs":[{"model_name","field","old","new"}],
   "updated_count":n}}`; never calls `repo.UpdateOption`, never calls
   `ratio_setting.InvalidateExposedDataCache`, never bumps the version. POST because the request
   carries a body (judge 1 flagged the buyer's GET-with-body).
8. Audit: new constants in `internal/app/governance/audit_action.go` — `ActionPricingUpdated =
   "pricing.updated"`, `ResourcePricing = "pricing"` — registered in `validAuditActions`. After a
   successful commit, `governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin,
   c.GetInt("id"), governance.ActionPricingUpdated, governance.ResourcePricing, 0, details))` where
   `details` is the same diff array as the preview plus `{"from_version":N,"to_version":N+1}`.
   Actor type: `ActorAdmin` (the caller passed `requirePlatformRoot`).
9. Console: `Pricing/index.jsx` sends `If-Match-Pricing-Version` from the last GET; on 409 shows
   `console.pricing.version_conflict` (new i18n key with an English value in `en.json` and a
   Chinese value in `zh.json`) and re-fetches. A "Preview" button calls the preview route and
   renders the diff table before "Save".

**Files.** `internal/adapter/handler/v2_pricing_write.go`, `v2_pricing_write_test.go`,
`v2_pricing.go`, `v2_pricing_test.go`, `internal/app/governance/audit_action.go`,
`audit_action_test.go`, `internal/adapter/handler/router/api-v2-router.go` (one route line),
`router/v2_completeness_test.go` (one `exempt` entry: preview is read-only, root-gated),
`web/src/pages/v2/Pricing/index.jsx`, `index.test.jsx`, `web/src/i18n/locales/{en,zh}.json`.
Overlaps: `audit_action.go` (L2, L6 add different constants — additive, no conflict),
`api-v2-router.go` and `v2_completeness_test.go` (every lane; additive lines).

**Tests.** (all in `internal/adapter/handler`, hermetic tier)
- `TestV2PricingWrite_VersionConflict_NoWrite`: seed `PricingVersion=3`; POST with
  `If-Match-Pricing-Version: 2` → 409, `current_version==3`, `ratio_setting.GetModelRatioCopy()`
  unchanged, options row unchanged.
- `TestV2PricingWrite_VersionRace_SecondWriterLoses`: two sequential POSTs both carrying `3`; first
  200 with `new_version==4`, second 409; final map equals first writer's body.
- `TestV2PricingWrite_NoHeader_SkipsGuard`: legacy body without header → 200 (rollout lock; deleted
  when O1 flips).
- `TestV2PricingWrite_DBFailure_LeavesMemoryUntouched`: inject `repo.UpdateOption` failure via the
  existing test DB seam → 500 and `GetModelRatioCopy()` unchanged (mutation: restore the old
  memory-first order → red).
- `TestV2PricingWrite_CacheRatioRoundTrip`: patch `cache_ratio` → `GetCacheRatioCopy()[model]`
  equals the value and the `CacheRatio` option row contains it.
- `TestV2PricingWrite_AuditRow`: one `audit_events` row with `Action=="pricing.updated"`, details
  containing `from_version`/`to_version` (mutation: delete the `RecordAuditEvent` call → red).
- `TestV2PricingPreview_NeverPersists`: preview returns diffs, `updated_count`, and zero option
  writes (count `options` rows before/after) and version unchanged.
- `TestAllAuditActions_ContainsKnown` extended with `pricing.updated`.
- web: `Pricing/index.test.jsx` — header sent from last GET; 409 shows the conflict toast and
  re-fetches; Preview renders a diff row per changed field.

**UAT probe.** `GET pricing` → read `version` (V). `POST` with `If-Match-Pricing-Version: V` →
200 `new_version==V+1`. Repeat the same request with the stale `V` → **409 body containing
`current_version: V+1`** (artefact 1). `GET /api/v2/admin/audit/events?action=pricing.updated`
(`handler/audit.go:15`, filter by `action`) → **one row whose `details` holds `from_version: V`**
(artefact 2). Preview call → **`options` row count unchanged and `version` still V+1** (artefact 3,
via `GET pricing`).

**Migration.** None (option row).

**Platform change.** None.

**Risk.** The hot relay read path (`ratio_setting.GetModelRatioCopy`) is untouched; the CAS and
transaction sit on the admin write only. The header is optional this cycle so the console cannot be
broken by a partial deploy; O1 decides when it becomes mandatory.

**Consumer-visible behaviour changes.** `GET pricing` gains `data.version`; `POST pricing` gains
`data.new_version` and a new 409 code; new route `POST pricing/preview`; `cache_ratio` accepted;
`audit.actions` list gains `pricing.updated`.

### L2 — audit-completeness: fail-closed fallback for admin writes, coverage endpoint, CI structural test, unaudited-write counter

**Why.** Auditing is 100% manual: `governance.RecordAuditEvent` is called from 29 handler files
(`grep -rl` on `internal/adapter/handler/*.go`; 28 of them construct the event with
`governance.NewAuditEvent(c, …)`, 0 with `NewDetachedAuditEvent`). No middleware, no test, no metric
notices a forgotten call — L1's pricing route is the live proof. The SOC 2 / ISO 27001 question "is
every privileged write logged, provably?" is today answered "by convention". Who notices: the
buyer's security reviewer (coverage number), the operator (a counter that moves when a route is
unaudited), the developer (CI fails before the route ships).

**Gap ids.** auth-security-16.

**Spec.**

1. `governance.NewAuditEvent` (`audit.go:55`) sets `c.Set(governance.AuditedContextKey, true)` on
   construction. Because all 28 files that audit inside a request use `NewAuditEvent(c, …)`, this
   marks every existing site with a **one-line change and no per-handler edit** (the buyer plan's
   29-file `MarkAudited` sweep, which judge 1 flagged as under-coverable, is not needed). A handler
   that audits only on some branches is still covered: the fallback fires exactly on the branches
   it forgot.
2. `middleware.AuditWriteGuard()` (`internal/adapter/middleware/audit_write_guard.go`): applied to
   `adminRoute` (`api-v2-router.go:357-358`) and the internal `adminGroup`
   (`internal-api-router.go:152-153`). After `c.Next()`, if the method is POST/PUT/PATCH/DELETE and
   `AuditedContextKey` is unset, it records a **typed** fallback event
   `governance.NewAuditEvent(c, actorType, actorID, governance.ActionAdminWriteUnaudited,
   governance.ResourceRoute, 0, details)` where `actorType` = `ActorAdmin` on `adminRoute`
   (`c.GetInt("id")`) or `ActorSystem` with the internal key id on `adminGroup`, `details` =
   `{"route":"<gin FullPath>","method":"POST","status":201}`. It fires **for every status**, not
   only 2xx (judge 2: rejected writes must not vanish). The action name lives in the taxonomy
   (`ActionAdminWriteUnaudited = "admin.write_unaudited"`, `ResourceRoute = "route"`), so
   `IsValidAuditAction` and `ListAuditActionsV2` keep working — no route-path-as-action rows (judge
   1's objection).
3. Prometheus counter `lurus_gateway_admin_write_unaudited_total{route}` in
   `internal/pkg/metrics/metrics.go`, incremented next to the fallback write (production writer
   satisfies `TestDeclaredSeriesHaveAProductionWriter`). Label cardinality is bounded by the route
   table (gin `FullPath()`, never the raw URL).
4. `GET /api/v2/admin/audit/coverage` (`RootJWTAuth`, `handler/v2_admin_audit.go`): walks
   `engine.Routes()` for mutating routes under `/api/v2/admin` and `/internal/admin`, returns
   `{"total_admin_write_routes":N,"routes_with_explicit_audit":[…],"routes_relying_on_fallback":[…],
   "fallback_events_last_24h":M}`. "Explicit" = the CI classification below (a generated Go map
   `auditExplicitRoutes` in `handler/audit_coverage_gen.go` produced by the same AST walk the test
   runs); `M` = count of `admin.write_unaudited` rows in the last 24h.
5. CI structural test `router/audit_coverage_test.go TestAdminWriteRoutesAreAudited`: enumerates
   the same route set plus the root-gated write outside `/admin` (`POST /api/v2/:tenant_slug/pricing`
   — a literal allow-list of non-`/admin` root writes in the test), resolves each handler via
   `runtime.FuncForPC` → source file, and parses the handler body with `go/ast`, following one level
   of same-package function calls, looking for `governance.NewAuditEvent` /
   `governance.RecordAuditEvent`. Routes with none must appear in an in-file `knownFallbackRoutes`
   slice with a reason; any other unaudited route fails the test with the route and handler name.
   The slice is the merge-review artefact (must shrink, never grow silently — reviewers check it
   in the PR diff). On today's HEAD the test fails on pricing until L1 lands, which is why L1 is
   first.

**Files.** `internal/adapter/middleware/audit_write_guard.go`, `audit_write_guard_test.go`,
`internal/app/governance/audit.go`, `audit_test.go`, `internal/app/governance/audit_action.go`,
`audit_action_test.go`, `internal/pkg/metrics/metrics.go`, `metrics_test.go`,
`internal/adapter/handler/v2_admin_audit.go`, `v2_admin_audit_test.go`,
`internal/adapter/handler/audit_coverage_gen.go` (new), `internal/adapter/handler/router/
audit_coverage_test.go` (new), `router/api-v2-router.go` (2 lines: `adminRoute.Use(…)`, coverage
route), `router/internal-api-router.go` (1 line). Overlaps: `audit_action.go` with L1/L6
(additive); `api-v2-router.go` with all.

**Tests.**
- `TestAuditWriteGuard_FallbackRowWhenHandlerSilent`: fake admin POST with no audit call → exactly
  one `admin.write_unaudited` row, details route/method/status; counter +1.
- `TestAuditWriteGuard_NoDuplicateWhenHandlerAudits`: handler calls `NewAuditEvent(c,…)` → zero
  fallback rows, counter unchanged.
- `TestAuditWriteGuard_FiresOnRejectedWrite`: handler answers 403 without auditing → fallback row
  with `status:403`.
- `TestAuditWriteGuard_ReadRoutesIgnored`: GET → nothing.
- `TestNewAuditEvent_MarksContext` in `governance/audit_test.go`.
- `TestAdminWriteRoutesAreAudited` (router): red on HEAD for `POST /api/v2/:tenant_slug/pricing`
  before L1, green after; mutation: delete `RecordAuditEvent` from `handler/v2_admin_users.go`'s
  role change → red naming that route.
- `TestAuditCoverageV2_ReportsFallbackRoutes`: coverage endpoint lists the fake route under
  `routes_relying_on_fallback`.
- `TestAllAuditActions_ContainsKnown` extended with `admin.write_unaudited`.

**UAT probe.** Call an admin write that today has no audit call — pick one from the CI test's
`knownFallbackRoutes` on the day of the probe (if the list is empty, use a `PUT
/api/v2/admin/options` with an invalid key so the handler rejects before auditing) → **one
`admin.write_unaudited` row visible at `GET /api/v2/admin/audit/events?action=admin.write_unaudited`**
whose `details.route` matches (artefact 1); **`lurus_gateway_admin_write_unaudited_total{route=…}`
≥ 1 on `/metrics`** scraped from inside the pod (`kubectl exec … wget -qO- localhost:3000/metrics`,
artefact 2); `GET /api/v2/admin/audit/coverage` returns `total_admin_write_routes` equal to the CI
test's count (artefact 3).

**Migration.** None.

**Platform change.** None.

**Risk.** `RecordAuditEvent` writes asynchronously (`gopool.Go`, `audit.go:39`); the fallback uses
the same path, so it inherits the existing at-most-once-per-process ordering. The fallback row is
a typed, correctly attributed actor (`c.GetInt("id")` after `RootJWTAuth`) — it never invents a
resource id. Hash-chain (`audit_event.go:43-44`) is untouched: fallback rows go through the same
`AuditWriter`.

**Consumer-visible behaviour changes.** New action `admin.write_unaudited` in the taxonomy; new
route `GET /api/v2/admin/audit/coverage`; new metric series.

### L3 — upstream-request-id: capture the provider's own request id at the single relay seam and surface it admin-only

**Why.** The gateway stamps its own `request_id` on every log row (`repo/log.go:119
setRequestIdIfAbsent`, filterable via `OtherTextExpr :847`, `:909`), but nothing captures the
vendor's id (`x-request-id` on OpenAI-compatible upstreams, `request-id` on Anthropic). Both losing
lens plans placed the capture in per-adapter files that never read `resp.Header` (`grep
resp.Header internal/adapter/provider/openai/*.go` → 0) or at log-write time when the response is
gone. The single seam is `internal/adapter/provider/api_request.go:263 doRequest`, called by
`DoApiRequest :93`, `DoFormRequest :126` and `DoTaskApiRequest :346` — every HTTP upstream call
(61 call sites of `DoRequest` across handlers resolve to it). Who notices: the operator opening a
vendor ticket ("we never saw that call" ends the moment the vendor's own id is on the row), the
tenant admin reading the log detail.

**Gap ids.** logs-analytics-observability-15 (tasks-plugins-35 and wire-formats-32 are
**not** taken — see §5).

**Spec.**

1. `RelayInfo` (`provider/common/relay_info.go:91`) gains `UpstreamRequestId string` next to
   `SessionId`, same rationale comment (settlement path has no gin.Context).
2. In `doRequest` after `resp, err := client.Do(req)` succeeds (`api_request.go:306`): read the
   first non-empty of `x-request-id`, `request-id`, `openai-request-id`, `cf-ray` (ordered list in
   one package-level slice `upstreamRequestIdHeaders`), bound to 128 printable-ASCII bytes
   (drop, not truncate — same rule as `X-Session-Id`, `relay_info.go:434-444`), store in
   `info.UpstreamRequestId`. Never errors; empty when absent.
3. `governance.EnrichLogParams` (`governance.go:45`, writes at `:105-121`): `if
   info.UpstreamRequestId != "" { params.Other["upstream_request_id"] = … }` — written only when
   non-empty, matching the `session_id` rule at `:114-116`.
4. Classification: `internal/app/governance/classification.go` adds `"upstream_request_id":
   TierInternal` beside `upstream_model :78` (it names the upstream, so admin-only, like
   `channel_id`); `internal/app/log_other_projection_lock_test.go` adds it to `wantInternal :112`;
   `internal/adapter/repo` `internalOtherKeys` gains it (the lock test's own instruction at
   `:377-378`). This is the item cycle-6 §9 said "no lane adds an `other.*` key" about — this cycle
   does, deliberately, and passes the default-deny gate by declaration rather than discovery.
5. Query: `repo.LogQueryParams` gains `UpstreamRequestID string`, applied with
   `jsonOtherTextExpr("upstream_request_id")` beside the `request_id` filters (`log.go:909`,
   `:972`); `handler/v2_log.go` binds `?upstream_request_id=` **only on the tenant-admin path**
   (`GetAllLogsV2`, the route gated by `requireTenantAdmin`) and on `ExportAdminLogsV2`; the
   self-service list ignores the parameter (the field is internal-tier).
6. Error rows: `relay.go:762-775` (error-log `other` build) also copies `info.UpstreamRequestId`
   when the failure came back from upstream with headers (the `types.IsUpstreamFailure` branch),
   so a 5xx from the vendor also carries the vendor's id.

**Files.** `internal/adapter/provider/api_request.go`, `api_request_test.go`,
`internal/adapter/provider/common/relay_info.go`, `internal/app/governance/governance.go`,
`governance_test.go`, `internal/app/governance/classification.go`, `classification_test.go`,
`internal/app/log_other_projection_lock_test.go`, `internal/adapter/repo/log.go`,
`log_projection_tier_test.go`, `internal/adapter/handler/v2_log.go`, `v2_log_test.go`,
`internal/adapter/handler/relay.go` (error-row `other` only, `:762-775`). Overlap: `relay.go` is on
cycle-6 §9's "no lane touches" list — this lane touches only the error-row map build, none of the
retry/breaker/headroom lines listed there (§4 records the re-check).

**Tests.**
- `TestDoRequest_CapturesUpstreamRequestId` (provider, httptest upstream sending `x-request-id`):
  `info.UpstreamRequestId` equals the header; table cases for each header name, absent → "",
  oversized/non-printable → "".
- `TestEnrichLogParams_UpstreamRequestId_WrittenOnlyWhenPresent` (governance).
- `TestOtherProjectionIsFullyClassified` stays green with the new key declared;
  `TestSanitizeOtherForUser_KeepsLatencyStripsPricing` extended: a row carrying
  `upstream_request_id` returns it to admin and strips it for the user (mutation: move the key to
  `wantUserVisible` → the tier test goes red).
- `TestGetAllLogsV2_UpstreamRequestIdFilter` (handler): tenant-admin filter returns the seeded row;
  cross-tenant seeded row is not returned; self-service route ignores the parameter.

**UAT probe.** One relay call through the UAT channel with `-D` to capture headers; then
`GET /api/v2/lurus/logs/all?upstream_request_id=<value from the upstream's own header>` as the
tenant admin → **exactly one row, whose `other.upstream_request_id` equals the value** (artefact).
If the UAT upstream sends no such header the probe uses `cf-ray` (present on Cloudflare-fronted
vendors) — and if none of the four headers is present the field is legitimately empty and the
probe is re-run against a channel that sends one; an empty field on a vendor without the header is
**not** a defect (do-not-regress note in §4).

**Migration.** None (JSON `other` key).

**Platform change.** None.

**Risk.** Header names drift; the list is data, not code, and absence is silent by design. The key
is internal-tier, so no data-exposure regression for tenant users.

**Consumer-visible behaviour changes.** Admin log detail/list rows may carry
`other.upstream_request_id`; new admin filter parameter.

### L4 — model & vendor rankings: period-over-period leaderboard from `GetModelPerformance`, zero table

**Why.** `repo.GetModelPerformance(start, end, tenantID, model)` (`analytics.go:45`) already returns
tenant-scoped per-model requests, errors, error_rate, tokens, quota, avg/p50/p95 latency and is
wired at `GET /api/v2/admin/analytics/model-performance` (`api-v2-router.go:470`,
`v2_admin_analytics.go:33`). Log rows carry an indexed `channel_type` (`entity/log.go:25
idx_gov_channel_type`), so a vendor rollup needs no join. What is missing is rank, trend and share —
the tab a buyer bake-off compares against LiteLLM/Portkey. The architect's `model_perf_hourly`
table would duplicate this (§5). Who notices: tenant admin (which model/vendor is reliable for
*their* traffic), platform root (across tenants).

**Gap ids.** logs-analytics-observability-10, -11, -12, -27, -28, console-ux-03.

**Spec.**

1. Repo: `GetVendorPerformance(start, end, tenantID string)` in `analytics.go` — same aggregate as
   `GetModelPerformance` grouped by `channel_type` (name via
   `constant.GetChannelTypeName`, already used at `governance.go:107`).
2. Repo: `GetRankings(start, end, tenantID, by string, limit int) ([]RankingRow, error)` — runs the
   model or vendor aggregate over `[start,end]` and the immediately preceding window of equal
   length, computes per row `rank`, `rank_delta` (0 for new entrants, flagged `is_new`),
   `token_share_pct`, `quota_share_pct`, `requests_growth_pct` (prev==0 → null), sorted by tokens
   desc, `limit` ≤ 20.
3. Handlers (`internal/adapter/handler/v2_admin_analytics.go` for root,
   `internal/adapter/handler/v2_analytics_rankings.go` new for tenant):
   - `GET /api/v2/admin/analytics/rankings?by=model|vendor&hours=24&tenant_id=` — `adminRoute` +
     `CriticalRateLimit`, root only, optional tenant filter (same contract as model-performance).
   - `GET /api/v2/:tenant_slug/analytics/rankings?by=&hours=` — mounted under a new
     `apiV2.Group("/:tenant_slug/analytics")` with `UserAuth` + `TenantSlugGuard`; the handler
     resolves `middleware.GetTenantContext(c)` and gates with `requireTenantAdmin(c, tenantCtx)`
     exactly like `GetAllLogStatV2` (`v2_log_stat.go:87-113`); tenant id comes from the context,
     never from the query. Cross-tenant market share is therefore impossible by construction —
     the architect's isolation objection is honoured without abandoning the buyer feature.
   - `hours` ∈ [1, 720], default 24; `by` default `model`.
   - Response: `{"success":true,"data":{"by":"model","window":{"start","end"},"cached_at":ts,
     "rows":[…]}}`. Cached in-process 5 minutes keyed by `(tenant_id, by, hours)` in a
     `sync.Map` with timestamp (`cached_at` in the body exposes hits); per-pod cache is documented.
4. Console: `web/src/pages/v2/Analytics/Rankings.jsx` (new) under the existing Analytics
   navigation group, calling the tenant route; i18n keys `console.rankings.*` in en/zh.

**Files.** `internal/adapter/repo/analytics.go`, `analytics_test.go`,
`internal/adapter/handler/v2_admin_analytics.go`, `v2_admin_analytics_test.go`,
`internal/adapter/handler/v2_analytics_rankings.go` (new), `v2_analytics_rankings_test.go` (new),
`router/api-v2-router.go`, `router/v2_completeness_test.go`, `web/src/pages/v2/Analytics/
Rankings.jsx`, `Rankings.test.jsx`, `web/src/i18n/locales/{en,zh}.json`. Overlap: router and
completeness test only.

**Tests.**
- `TestGetRankings_RankDeltaSign` (repo, PG or hermetic tier as `TestGetModelPerformanceV2_
  ExactAggregates` uses): fixture with model A up / B down across two windows → deltas +1/−1.
- `TestGetRankings_SharesSumTo100` (± 0.01).
- `TestGetRankings_VendorGroupsByChannelType`.
- `TestGetRankings_TenantIsolation`: two tenants seeded; tenant A's rows never include tenant B's
  tokens (mutation: drop the `tenant_id` where clause → red). This is the architect's oracle,
  ported.
- `TestTenantRankingsV2_ForbiddenForNormalUser`, `TestTenantRankingsV2_AdminSeesOwnTenantOnly`
  (mirrors `v2_log_stat_test.go:163/198`).
- `TestRankingsV2_CacheHitWithin5Min`: second call returns identical `cached_at` and the repo spy
  counts one query.
- `TestGetModelPerformanceV2_NoTenantFilterSpansTenants` stays green (unchanged endpoint).

**UAT probe.** Fire 3 relay calls for model X and 1 for model Y (UAT catalogue), then
`GET /api/v2/lurus/analytics/rankings?by=model&hours=1` as the tenant admin → **rows for X and Y with
`token_share_pct` summing ≈100 and X ranked 1** (artefact 1); immediate second call → **identical
`cached_at`** (artefact 2); `?by=vendor` → one row for the channel's vendor with the same total
(artefact 3).

**Migration.** None.

**Platform change.** None.

**Risk.** Two windows double the aggregate cost on `logs`; bounded by `hours ≤ 720`, the 5-minute
cache and `CriticalRateLimit`; the existing `(tenant_id, created_at)`-style indexes serve both
windows. Per-pod cache ages differ across the 3 replicas — documented, not a bug.

**Consumer-visible behaviour changes.** Two new read routes; new console page.

### L5 — routing operator levers: per-channel `force_http1` and session-affinity stats/purge

**Why.** (a) `ForceAttemptHTTP2: true` is hard-coded at `http_client.go:123` (default client),
`:215` and `:250` (proxy clients); an upstream with a flaky HTTP/2 implementation is
indistinguishable from an outage and today has no per-channel escape hatch. (b) Session affinity
(`session_affinity.go`) silently re-pins conversations to a channel; the only visibility is the
Prometheus counter `lurus_gateway_session_affinity_total{result}` (`metrics.go:174`) and the only
lever is env TTL (`.env.example:238-242`). An operator who suspects "the client keeps hitting the
bad channel because of affinity" has no way to see or evict a pin. Both are zero-migration,
neither touches the selection algorithm (`channel_select.go` stays on §9's "untouched" list —
the new lookup/purge helpers live in `session_affinity.go` next to `affinityLoad/affinityStore`).

**Gap ids.** routing-resilience-limits-13, routing-resilience-limits-11, console-ux-30.

**Spec.**

A. `force_http1`
1. Channel `ParamOverride` JSON (`entity/channel.go:256 GetParamOverride`) key
   `"__lurus_force_http1": true` (prefixed so it can never be forwarded as a request parameter —
   the override merge must skip `__lurus_*` keys; add that skip where param override is applied to
   the outbound body).
2. `info.ChannelSetting` already carries per-channel transport choices (`Proxy`,
   `api_request.go:266`); add `ForceHTTP1 bool` populated where `ChannelSetting` is built from the
   channel, read in `doRequest`: `app.GetHttpClientFor(proxyURL string, forceHTTP1 bool)`.
3. `http_client.go`: `GetHttpClientFor` returns the existing shared client when `!forceHTTP1 &&
   proxyURL==""` (pointer-identical, no regression), otherwise a cached client keyed by
   `proxyURL + "|h1"`/`"|h2"` in the existing `proxyClients` map (bounded by `maxProxyClients`,
   `storeProxyClient :183`) — cache key is transport shape, **not** channel id, so cardinality stays
   O(proxies × 2). The H1 transport sets `ForceAttemptHTTP2: false` and
   `TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}`, keeping
   `applyRelayTransportTimeouts` (`:60`) and the dial guard.
4. Console: channel edit form's Advanced section gets a "Force HTTP/1.1" switch writing that key.

B. Affinity stats / purge (root, platform-scoped)
1. `GET /api/v2/admin/routing/affinity` → `{"enabled":bool,"ttl_seconds":n,"backend":"redis"|
   "memory","memory_entries":n,"counters":{"hit":n,"miss":n,"stale":n}}`. Counters come from an
   in-process `atomic` triple incremented in `recordAffinityOutcome` (`session_affinity.go:289`)
   beside the Prometheus call — same increment site, so they cannot drift from `/metrics`; reading
   the Prometheus registry back (operator plan) is avoided.
2. `DELETE /api/v2/admin/routing/affinity/:key` — `:key` is the HMAC-hashed affinity key exactly as
   `DeriveSessionAffinityKey` (`:78`) produces it; deletes `affinityRedisPrefix+key` or the
   `affinityMem` entry; 204 on success, 404 when absent. Audited with new
   `ActionRoutingAffinityPurged = "routing.affinity_purged"`.
3. `DELETE /api/v2/admin/routing/affinity?all=true` — memory: reset map; Redis: `SCAN`
   `affinityRedisPrefix*` in batches of 500 with `UNLINK`; response `{"purged":n}`; audited.
4. A relay response header `X-Lurus-Affinity-Key: <key>` is added **only** when the request
   carried an affinity source (`X-Session-Id` or `user`), so an operator can find the key to purge
   from a client's own trace. Exposed through the CORS `ExposeHeaders` list the way cycle-6 L6
   exposes `X-RateLimit-*`.

**Files.** A: `internal/app/http_client.go`, `http_client_test.go`,
`internal/adapter/provider/api_request.go` (client selection lines `:266-274` only — overlap with
L3, which edits `:306+`; land L3 first, L5 rebases), `internal/adapter/provider/common/relay_info.go`
(ChannelSetting field; overlap with L3 additive), the param-override merge site (`grep -rn
"GetParamOverride()" internal/adapter/provider internal/app | grep -v _test` — one file),
`web/src/pages/v2/Channels/*` edit form + test. B: `internal/app/session_affinity.go`,
`session_affinity_test.go`, `internal/adapter/handler/v2_admin_routing.go` (new),
`v2_admin_routing_test.go` (new), `internal/app/governance/audit_action.go` (additive),
`internal/adapter/middleware/cors.go` (one header in `ExposeHeaders`), `router/api-v2-router.go`,
`router/v2_completeness_test.go`.

**Tests.**
- `TestGetHttpClientFor_DefaultIsSharedPointer` (regression: no override → `GetHttpClient()`).
- `TestGetHttpClientFor_ForceHTTP1Transport`: `ForceAttemptHTTP2==false`, `TLSNextProto` empty
  map; `TestGetHttpClientFor_CacheKeyIncludesProtocol` (h1 and h2 for the same proxy are distinct
  and both bounded by `maxProxyClients`).
- `TestDoRequest_ForceHTTP1NegotiatesHTTP1`: `httptest.NewUnstartedServer` with `EnableHTTP2`,
  TLS; forced channel → `resp.ProtoMajor==1`; sibling → `2` (mutation: drop `TLSNextProto` → red).
- `TestParamOverride_SkipsLurusKeys`.
- `TestAffinityStats_ReflectsMemoryEntries` (seed via `affinityStore`, `resetAffinityMemForTest`
  `:235`), `TestAffinityPurge_KeyRemovedThenMiss` (`lookupAffinityChannel` returns nil after
  purge; `recordAffinityOutcome("miss")` counted), `TestAffinityPurge_Unknown404`,
  `TestAffinityPurgeAll_Redis` (miniredis tier used by `cov_r5core_redis_backend_test.go`).
- `TestAffinityKeyHeader_OnlyWhenSourcePresent` (handler).

**UAT probe.** A: set `__lurus_force_http1` on a UAT test channel pointing at
`https://http2.golang.org/reqinfo` (echoes negotiated protocol; or any echo endpoint that reports
`r.Proto`) via the channel test route → response body shows **`HTTP/1.1`**, and the sibling channel
without the flag shows **`HTTP/2.0`** in the same run (artefact: the two echo bodies). B: two relay
calls with the same `X-Session-Id` → second response carries `X-Lurus-Affinity-Key`; `GET
…/routing/affinity` shows **`hit` incremented by 1**; `DELETE …/affinity/<key>` → 204; third call →
`miss` **+1** and `GET /api/v2/admin/audit/events?action=routing.affinity_purged` shows the row.

**Migration.** None.

**Platform change.** None.

**Risk.** A: the flag is off unless explicitly set; the shared client path is pointer-identical.
B: purge is root-only and audited; `SCAN` is bounded and never `KEYS`.

**Consumer-visible behaviour changes.** New relay response header `X-Lurus-Affinity-Key` (only
when an affinity source is present); new admin routes; new audit action; new channel override key.

### L6 — TOTP recovery codes + admin 2FA stats and force-disable (with user notification)

**Why.** TOTP is self-service only (`handler/totp.go:31/55/115/175`; routes `api-router.go:73-76`)
with no recovery path: `grep -rn "backup_code\|BackupCode\|RecoveryCode" internal/` → 0. A user who
loses the device is locked out of every `SecureVerificationRequired` action (channel key reveal,
TOTP disable itself) until an engineer runs a DB delete — there is no admin endpoint (`grep -rn
"totp" router/api-v2-router.go` → 0). Who notices: the locked-out user (self-recovers), the
support operator (one audited click instead of SQL), the security reviewer (adoption %).

**Gap ids.** auth-security-06, auth-security-07, console-ux-07, auth-security-25 (TOTP slice only).

**Spec.**

1. Entity `UserTOTPBackupCode` (`internal/domain/entity/user_totp_backup_code.go`): `id bigint pk
   autoincrement`, `user_id int index`, `code_hash varchar(64) uniqueIndex` (hex SHA-256 of
   `user_id || ":" || code` — **one-way hash, never AES-GCM**; the buyer plan's "hashed the same
   way as the secret" was reversible encryption, judge 1 fatal flaw, corrected here),
   `created_at bigint`, `used_at bigint default 0`. Table `user_totp_backup_codes`, created by
   `repo.ensureUserTOTPBackupCodeTable()` with the **same lazy AutoMigrate pattern as its sibling**
   (`entity/user_totp.go:10-12`, `repo/user_totp.go:16-20`). Owner decision O2 records that this
   is a schema change without a numbered migration, per that precedent.
2. `POST /api/user/totp/confirm` (`TotpConfirm :115`) on success generates 10 codes
   (`crypto/rand`, `XXXX-XXXX` base32 without `0/O/1/I`), stores hashes, returns
   `backup_codes: [...]` **once**; `GET /api/user/totp/status` adds `backup_codes_remaining: n`
   and never the codes.
3. `POST /api/user/totp/backup-codes/regenerate` — `UserAuth` + `CriticalRateLimit` +
   `SecureVerificationRequired`: deletes all unused codes, issues 10 new ones, audited
   `ActionAuthTotpBackupRegenerated = "auth.totp_backup_regenerated"`.
4. Step-up: `UniversalVerify` (`secure_verification.go:44`, branch `req.Method == "totp"`)
   accepts `method: "totp_backup"`: same `totp.AllowAttempt`/`RecordFailure` throttle; consumes the
   code with a single `UPDATE … SET used_at=now WHERE code_hash=? AND user_id=? AND used_at=0` and
   requires rows-affected 1 (replay fails closed, no read-then-write); sets
   `secure_verified_at` exactly as the TOTP branch; audits `auth.login_success`-style detail
   `{"step":"secure_verify","method":"totp_backup","remaining":n}`.
5. Admin (`handler/v2_admin_security.go` new, under `adminRoute` so L2's guard applies):
   - `GET /api/v2/admin/security/totp-stats?tenant_id=` → `{"enrolled":n,"pending":n,
     "total_users":n,"adoption_pct":f,"backup_codes_exhausted":n}` (users with an enabled TOTP and
     0 unused codes). Tenant filter joins `users.tenant_id`.
   - `POST /api/v2/admin/security/users/:id/totp/force-disable` — `RootJWTAuth` **plus**
     `SecureVerificationRequired` (the acting root must have stepped up themselves — judge 2's
     "stolen root JWT strips everyone's 2FA" objection); body `{"reason":"…"}` required, ≤200
     chars; deletes the `user_totps` row and all backup codes; audits new
     `ActionTotpAdminDisabled = "auth.totp_admin_disabled"` with actor = root id, resource
     `user`, resource_id = target, details `{reason}`; notifies the target via
     `app.NotifyUser(ctx, userId, email, setting, dto.Notify{…})` (`internal/app/user_notify.go:26`
     — the real hook; the operator plan's `totp.go:151/165/195` are audit calls, not notifications,
     judge 1 fatal flaw corrected); 404 when the user had no enrollment.
6. Console: Settings → Security shows remaining backup codes and a Regenerate button; the
   step-up dialog gains a "use a recovery code" toggle; admin Users page row action "Disable 2FA"
   with reason prompt.

**Files.** `internal/domain/entity/user_totp_backup_code.go` (new),
`internal/adapter/repo/user_totp.go`, `repo/user_totp_backup_code.go` (new),
`repo/user_totp_backup_code_test.go` (new), `internal/app/totp/totp.go` (code generation +
hash helper), `totp_test.go`, `internal/adapter/handler/totp.go`, `totp_flow_test.go`,
`internal/adapter/handler/secure_verification.go`, `secure_verification_test.go`,
`internal/adapter/handler/v2_admin_security.go` (new), `v2_admin_security_test.go` (new),
`internal/app/governance/audit_action.go` (additive), `router/api-router.go` (1 route),
`router/api-router-totp_test.go` (`TestSetApiRouter_TotpWiring` extended), `router/api-v2-router.go`,
`router/v2_completeness_test.go`, `web/src/pages/v2/Settings/*`, `web/src/pages/v2/Users/*`,
`web/src/i18n/locales/{en,zh}.json`.

**Tests.**
- `TestTotpConfirm_ReturnsBackupCodesOnce`: 10 codes in confirm body; status never returns them;
  DB holds 10 hashes, no plaintext (`grep` the row values for any returned code → 0).
- `TestUniversalVerify_BackupCodeConsumedOnce`: first use → `secure_verified_at` set,
  `used_at>0`; second use → 403 and `RecordFailure` called (mutation: remove `AND used_at=0` →
  red).
- `TestUniversalVerify_BackupCodeThrottled` (reuses `TestTotpVerify_FailureThrottle` fixture).
- `TestBackupCodes_RegenerateInvalidatesUnused`.
- `TestAdminTotpStats_TenantScoped` (two tenants; filter returns only one).
- `TestAdminTotpForceDisable_RequiresStepUp` (no `secure_verified_at` → 403
  `VERIFICATION_REQUIRED`), `_RemovesRowAndCodes_AuditsAndNotifies` (notify spy), `_404NoEnrollment`.
- `TestTotpStepUp_EndToEnd` remains green (TOTP path untouched).

**UAT probe.** As the bridge user: enroll + confirm (TOTP code computed by the probe script from
the returned secret) → capture one backup code; `POST /api/verify` (existing route for
`UniversalVerify`, `api-router.go:67`) with `method:"totp_backup"` → 200; **repeat with the same code → 403**
(artefact 1: the second response); `GET /api/user/totp/status` → **`backup_codes_remaining: 9`**
(artefact 2). As root (stepped up): `POST …/users/<id>/totp/force-disable` → 200; `GET
/api/user/totp/status` as the user → `enabled:false`; `GET /api/v2/admin/audit/events?action=
auth.totp_admin_disabled` → **row with resource_id = user id** (artefact 3).

**Migration.** Schema change: new table `user_totp_backup_codes(id, user_id, code_hash, created_at,
used_at)` via lazy AutoMigrate (sibling precedent). No numbered file unless O2 says otherwise.

**Platform change.** None (TOTP is a newhub step-up factor, not a login factor; identity stays in
the platform IdP).

**Risk.** Force-disable is privilege-escalation-adjacent; mitigations: root + own step-up +
reason + audit + notification + `CriticalRateLimit`. O3 sets the alert threshold. Hash uses
`user_id` as domain separator so identical codes across users cannot collide on the unique index.

**Consumer-visible behaviour changes.** `confirm` response gains `backup_codes`; `status` gains
`backup_codes_remaining`; new `method: totp_backup`; two admin routes; two audit actions.

### L7 — per-device session registry: list, revoke-by-id, revoke-others, reasons (behind a flag)

**Why.** `ListSessionsV2` (`v2_sessions.go:19-21`) documents "There is no multi-device session
store … deferred to v3" and returns one synthetic row; `RevokeCurrentSessionV2`
(`v2_session_revoke.go:44`) can only clear the caller's own cookie. An auditor fails "can a user see
and kill a stolen session?" on reading that comment. The substrate exists: gin-contrib/sessions
v1.0.4 exposes `Session.ID()` (`sessions.go:26`), and the Redis store (`cmd/server/session_store.go
:49 → sessionredis.NewStoreWithDB`) keys every session as `session_<id>` (`redistore.go:293`), so
deleting that key is an authoritative logout. Who notices: the end user (device list, revoke), the
security reviewer (revocation reasons in the audit chain).

**Gap ids.** auth-security-08, -26, -29, console-ux-23, ops-deploy-docs-27 (as a consequence),
auth-security-20 (cap only).

**Spec.**

1. Migration **033** `create_user_sessions` (reserve in root `doc/coord/migration-ledger.md:134`
   first — owner action O4; PG-only, idempotent `CREATE TABLE IF NOT EXISTS`):
   `user_sessions(id bigserial pk, session_key varchar(128) unique not null, user_id int not null,
   tenant_id varchar(36) not null default 'default', ip varchar(45) not null default '',
   user_agent varchar(255) not null default '', auth_method varchar(32) not null default '',
   created_at bigint not null, last_seen_at bigint not null, revoked_at bigint not null default 0,
   revoke_reason varchar(32) not null default '')`, indexes `(user_id, revoked_at)`,
   `(last_seen_at)`. Entity `internal/domain/entity/user_session.go`, repo
   `internal/adapter/repo/user_session.go`.
2. Flag `SESSION_REGISTRY_ENABLED` (default `false` in code; `true` in
   `deploy/k8s/r6-uat/deployment.yaml` for this cycle; prod flips after UAT soak — O5). When off,
   every endpoint behaves exactly as today (synthetic row, `/current` only).
3. Registration: in `authHelper` (`middleware/auth.go:36`) after a session-cookie login is admitted
   (not on bearer/token paths), if `session.ID() != ""`: upsert the row keyed by `session_key`,
   touching `last_seen_at` **at most once per 60 s per key** (compare against an in-process
   `sync.Map` of last-touch timestamps; miss → write). First sight creates the row with ip
   (`c.ClientIP()`, trusted-proxy aware per #157), UA truncated to 255, `auth_method` from the
   existing `auth_method` resolution in `ListSessionsV2`. Cookie-store deployments
   (`main.go:375-377`, `session.ID()==""`) register nothing — the list then shows only the synthetic
   current row and revoke-by-id is 404 (documented; UAT and prod both run the Redis store).
4. Endpoints (existing group `tenantSessions`, `api-v2-router.go:221-227`):
   - `GET /api/v2/:tenant_slug/sessions` → rows for the caller (`user_id`, `revoked_at==0`, seen in
     the last 30 days) with `is_current` (`session.ID()` match), `created_at`, `last_seen_at`,
     `ip` (masked to /24 for IPv4, /48 for IPv6 — the current whitelist test
     `TestV2Sessions_WhitelistEnforced` is extended, not weakened: raw IP and UA never serialised;
     `user_agent_family` is a coarse parse), `auth_method`. Same envelope keys as today so
     `Settings/index.jsx` keeps rendering.
   - `DELETE /api/v2/:tenant_slug/sessions/:id` → only if the row's `user_id` is the caller (404
     otherwise); sets `revoked_at`, `revoke_reason='user_revoked'`; `common.RedisDel("session_"
     + session_key)`; audit `auth.session_revoked` (existing action) with `{"session_id":id,
     "reason":…}`; 204.
   - `DELETE /api/v2/:tenant_slug/sessions/others` → all rows of the caller except current, reason
     `user_revoked_others`; returns `{"revoked":n}`. Literal segment registered before `/:id`.
   - `DELETE …/sessions/current` unchanged in behaviour, additionally marks its own row
     `reason='logout'`.
   - Root: `DELETE /api/v2/admin/users/:id/sessions` (under `adminRoute`, L2-guarded) → reason
     `admin_revoked`, audited. Closes the "compromised account" runbook step.
5. Defence in depth for revoke: `authHelper` also rejects a session whose row has `revoked_at>0`
   (one indexed lookup by `session_key`, only when the flag is on and only on the cookie path) —
   covers the window between Redis DEL and a replica's local cache, and any future non-Redis store.
6. Cap (auth-security-20): `SESSION_MAX_ACTIVE_PER_USER` default `0` (unlimited); when >0, on
   first-sight registration the oldest rows beyond the cap are revoked with reason
   `cap_exceeded`.
7. Canary: `cmd/server/session_store_test.go` gains `TestRedisSessionKeyFormat_Canary` — writes a
   session through the real store against miniredis and asserts the key is `"session_" +
   session.ID()`; a library bump that changes the prefix goes red in CI, not in a production
   revoke.
8. Console: Settings → Sessions list renders the rows with "Revoke" / "Sign out other devices".

**Files.** `migrations/033_create_user_sessions.sql` (new), `internal/domain/entity/
user_session.go` (new), `internal/adapter/repo/user_session.go` (new), `user_session_test.go`
(new), `internal/adapter/middleware/auth.go` (registration + revoked check in `authHelper`),
`auth_test.go`, `internal/adapter/handler/v2_sessions.go`, `v2_sessions_test.go`,
`v2_session_revoke.go`, `v2_session_revoke_test.go`, `internal/adapter/handler/v2_admin_users.go`
(one handler), `v2_admin_users_test.go`, `cmd/server/session_store_test.go`, `router/
api-v2-router.go`, `router/v2_completeness_test.go` (replace the `/current` exempt entry at `:80`
with `/current`, `/others` exempt + `/:id` swept by `TestV2SessionRevokeByID_NotOwned404`),
`deploy/k8s/r6-uat/deployment.yaml` (flag), `.env.example` (two rows), `web/src/pages/v2/Settings/*`,
`web/src/i18n/locales/{en,zh}.json`. Overlaps: `audit_action.go` not touched (reuses
`auth.session_revoked`); router/completeness with all lanes.

**Tests.**
- `TestUserSessionRegistry_UpsertThrottled60s` (repo/middleware): two requests within 60 s → one
  write; after the window → second write.
- `TestV2SessionRevokeByID_NotOwned404` (IDOR), `TestV2SessionRevokeByID_DeletesRedisKeyAnd401`
  (miniredis: after revoke, a request bearing the revoked cookie gets 401 — mutation: remove
  the `RedisDel` **and** the `revoked_at` check → red; remove only one → still green, proving
  defence in depth).
- `TestV2SessionsOthers_KeepsCurrent`.
- `TestV2Sessions_WhitelistEnforced` extended (no raw ip/ua).
- `TestAuthHelper_FlagOff_NoRegistryWrites` (observe/rollback lock).
- `TestRedisSessionKeyFormat_Canary`.
- `TestAdminRevokeUserSessions_AuditsReason`.
- `TestV2Sessions_HappyPath`/`_TenantIsolation` remain green with the flag off.

**UAT probe.** Two bridge exchanges (two cookie jars A and B, same `user_id=1`); `GET
/api/v2/lurus/sessions` from A → **2 rows** (artefact 1); `DELETE …/sessions/<B's id>` from A →
204; any authenticated call from jar B → **401** (artefact 2); `psql`/admin audit shows
`auth.session_revoked` with `reason: user_revoked` (artefact 3); `kubectl exec … redis-cli -n 3
EXISTS session_<B key>` → 0 (artefact 4).

**Migration.** 033 as specified (columns listed above). Ledger reservation precedes the SQL.

**Platform change.** None. Role/status changes already invalidate sessions per request
(`authHelper` re-validates status/role from cache — comment at `auth.go:42-49`; PR #167), so the
architect's `auth_version` column is unnecessary and is not built.

**Risk.** Highest in the cycle: `authHelper` runs on every cookie-authenticated request. Mitigated
by the flag (default off), the 60 s throttle, the single indexed lookup, and UAT soak before prod.

**Consumer-visible behaviour changes.** `GET sessions` may return >1 row with `is_current`; new
`DELETE …/:id`, `DELETE …/others`, admin revoke; `ip` is masked.

## 4. Do-not-regress

Carried forward from `oss-best-practice-cycle6-2026-09-09.md` §9 for the items these lanes touch,
plus the newhub-only capabilities in the matrix. Each lane's acceptance re-runs the named
tests/greps.

| Item (cycle-6 §9 wording) | Lane that touches it | Re-check |
|---|---|---|
| "no lane adds an `other.*` key"; default-deny projection gate `log_other_projection_lock_test.go:359`; `request_id`/`session_id` TierPublic `classification.go:73-74` | L3 adds `upstream_request_id` **declared** TierInternal | `TestOtherProjectionIsFullyClassified`, `TestInternalOtherKeys_NoPublicField`, tier test extension |
| "no lane touches `relay.go`" (retry gate `:662/:937`, breaker `:491`, headroom `:185-230`, retry re-selection `:587`) | L3 edits only the error-row `other` build at `:762-775` | `git diff` of `relay.go` confined to that hunk; `TestRelay*` green; `-race` |
| Product attribution written to every row `governance.go:105-110`; `session_id` at `:117` never changed | L3 adds lines after `:121` | `TestEnrichLogParams_*` |
| `channel_cache.go`, `smart_routing.go`, `channel_scorer.go`, `channel_select.go` untouched (session-affinity re-pin) | L5 touches `session_affinity.go` only (stats/purge helpers), never `lookupAffinityChannel`'s selection logic | `TestAffinityStoreLoad_MemoryFallback`, `channel_select_affinity_test.go` green |
| Relay gate order `relay-router.go:100-121` | none | unchanged |
| Structural gates: every abort site carries a code; every declared series has a production writer; OpenAPI code enum locked | L1 (409), L2 (counter), L5 (204/404), L6, L7 | `abort_code_structural_test`, `TestDeclaredSeriesHaveAProductionWriter`, `openapi_contract_lock_test` |
| Every by-id v2 mutation classified in `v2_completeness_test.go:31-60` | L1, L4, L5, L6, L7 add entries | `TestV2IDOR_Completeness` |
| At-rest ciphertext versioned (`totp.go` `v1:` prefix); `CryptoSecret` fallback | L6 adds hashes beside, never changes `EncryptSecret` | `TestTotpStepUp_EndToEnd` |
| Tenant decision is a compile-time argument for cross-user log queries (`repo/log.go:60-89`) | L3 filter, L4 rankings | `TestGetRankings_TenantIsolation`, `TestGetAllLogsV2_UpstreamRequestIdFilter` |
| `/metrics` double-guarded | L2, L5 add series only | `metricsAuthMiddleware` test |
| Migration ledger 033 `(next available)` | L7 reserves 033 (O4) | ledger diff in the root repo precedes the SQL |
| `CostSpikeLimit` observe/enforce precedent | L7 flag default off | `TestAuthHelper_FlagOff_NoRegistryWrites` |

Newhub-only capabilities (matrix list) that these lanes brush and must keep: tenant credit-pool
gate and cost-spike breaker on every billed route (no lane touches `quota.go`/breaker); hash-chained
audit trail (L1/L2/L5/L6/L7 add rows through the same writer, chain untouched — `VerifyAuditChainV2`
must stay green on UAT after each lane); `SecureVerificationRequired` step-up on key reveal (L6
extends the factor set, never bypasses the gate); tenant-scoped analytics (L4 tenant route uses
context, never query); IDOR-hardened request-id lookup (`GetLogByRequestID :326` untouched);
session affinity re-pin (L5 reads/purges, never reselects); wire-format stamping and native error
envelopes (no lane touches `relay-router.go`).

Vendor without a request-id header: an empty `upstream_request_id` is **not** a defect (L3).

## 5. Deliberately not doing (with the judge finding or evidence)

- **admin-topup-visibility (buyer lane 5)** — fatal per both judges: `CreateBillingCheckout`
  (`v2_billing.go:89-95`) answers 503 without a linked platform account; UAT runs OIDC and unified
  billing off and epay credentials are pending, so the "order_no appears in list" artefact cannot
  exist on UAT. Revisit after epay credentials land (owner item, not a lane).
- **conversion_notes / wire-format diagnostics (buyer lane 4 half, wire-formats-32,
  tasks-plugins-35)** — the files cited (`internal/adapter/handler/{compatible,claude,gemini}_
  handler.go`) do not exist there (they are under `internal/app/relay/`), and a per-conversion
  `other.*` list is a second undeclared key family against the projection gate. Not this cycle.
- **Architect F entitlement plans** — `tenant_credit_pools.tenant_id` is `uniqueIndex`
  (`entity/tenant_credit_pool.go:51`), so "per-user sub-pool" reshapes the money ledger; it also
  changes what `PoolBalanceCheck` gates; and "plan_code in the checkout webhook" assumes a webhook
  newhub does not have. Not buildable as specified; O6 asks the owner whether plans are wanted at
  all given the platform owns entitlements.
- **Architect D `model_perf_hourly` table + flush** — duplicates `GetModelPerformance`
  (`analytics.go:45`) and adds a second telemetry store beside Prometheus/netdata. L4 delivers the
  same leaderboard with zero schema.
- **Architect B regex affinity rules** — puts regex matching on `channel_select.go`, a §9
  "untouched" hot path, for a v4 gap. L5 ships visibility/purge only.
- **Architect B/judges' `MultiKeyModeRotating`** — the local upstream checkout
  (`2b-svc-newapi/constant/multi_key_mode.go`) has only `random`/`polling`, identical to ours;
  `polling` already increments per use. The gap row's "rotating" semantics could not be verified
  against any upstream source in the tree. Not planned until someone points at the upstream
  commit (O7).
- **Architect G zh-TW seeded from zh.json** — parity by filename; both judges. A real zh-TW locale
  needs a translator (owner item, no lane).
- **Architect C `auth_version` column** — unnecessary: `authHelper` already re-validates
  status/role from cache on every request (`auth.go:42-49`, PR #167).
- **Operator origin guard (auth-security-09, ops-deploy-docs-11)** — correct but not grafted by
  either judge; CORS already short-circuits same-origin and `ALLOWED_ORIGINS` is enforced
  (`cors.go:49`). Deferred; if scheduled, ship observe-first as the operator specified.
- **Operator AST-only canary without closure** — superseded by L2, which closes (fallback row)
  and detects (test + counter).
- **Payment rails, subscriptions, JS billing expressions, plugin runtime, AdvancedCustom /
  Sub2API / Codex channels, passkey credential tables, RBAC catalog, disk cache / GC / instance
  registry admin** — excluded by the invariants (platform owns money and identity; emptyDir pods;
  netdata) or by size with no buyer/operator pain point beyond "upstream has it". Row ids:
  topup-payments-subscriptions-03/04/05/06/07/11/12/14/16/23/24/30, billing-pricing-14/16/28/29,
  tasks-plugins-01/04/05/…/38, providers-channels-01/14/15/16/17, auth-security-04/05/17/18,
  console-ux-14/18/20/21/24/36/37, logs-analytics-observability-18/19/20/22/25/29,
  ops-deploy-docs-07/09/16, routing-resilience-limits-14/18/29/30.
- **Task artifact listing (tasks-plugins-02/19/20/34/36)** — touches 11 vendor adaptors and needs
  a real Suno/video task on UAT; ship vendor-by-vendor in a later cycle behind the existing
  ownership-checked `VideoProxy` (`video_proxy.go:61-66` is the regression guard).

## 6. Owner decisions

- **O1** L1: when does `If-Match-Pricing-Version` become mandatory (400 when absent)? Proposal:
  the release after the console change ships; `TestV2PricingWrite_NoHeader_SkipsGuard` is deleted
  the same day.
- **O2** L6: new security table via the lazy AutoMigrate precedent (`UserTOTP`, `BillingOutbox`)
  or a numbered migration 034 with a ledger reservation? The plan follows the precedent; say if
  the convention changes for new tables.
- **O3** L6: alert threshold for `auth.totp_admin_disabled` per root per day (proposal: >3/day
  pages via the existing audit-count alert path).
- **O4** L7: reserve migration ID 033 `create_user_sessions` in root
  `doc/coord/migration-ledger.md:134` before any SQL is written; confirm no parallel reservation.
- **O5** L7: `SESSION_REGISTRY_ENABLED` prod flip date after UAT soak (proposal: 7 days with
  `lurus_gateway_*` auth latency unchanged).
- **O6** Subscription/entitlement plans: does the owner want a plan catalogue in newhub at all,
  or is that a platform entitlement concern? Blocks any future lane on
  topup-payments-subscriptions-11/12/16.
- **O7** `MultiKeyModeRotating`: provide the upstream reference (commit or doc) that defines
  "rotating" distinct from `polling`; without it the row stays unplanned.
- **O8** Passkey: CLAUDE.md's "OIDC + Passkey" line is stale (no handler, no route, scaffolding
  removed per the 2026-09-10 memory); decide whether passkey lives in the platform IdP (default per
  the identity invariant) and correct CLAUDE.md.
- **O9** Per-channel TLS-verify-skip (routing-resilience-limits-18): explicit risk sign-off
  required before it is ever scheduled; not in L5.
- **O10** zh-TW: fund a human translation or leave the row open.

## 7. Verification protocol

**Local gates (every lane, before push).**
```
go vet ./... && go build ./...
go test -short -p 2 ./...                      # hermetic tier (-p 2: OOM guard on this machine)
go test -run 'TestV2IDOR_Completeness|TestConsoleCallsResolveToRegisteredRoutes|TestAdminWriteRoutesAreAudited|TestOtherProjectionIsFullyClassified|TestDeclaredSeriesHaveAProductionWriter|TestAllAuditActions_' ./...
cd web && bun run lint && bun run eslint && bun test src/i18n/i18n-integrity.test.js && bun run build
```
CI is the only oracle for `-race` and the coverage ratchet; a lane is not "green" until the PR's
CI run is linked in the lane report.

**Mutation checks (each lane must show the named test going red, then green).**
L1 restore memory-first order → `TestV2PricingWrite_DBFailure_LeavesMemoryUntouched`; drop the
audit call → `TestV2PricingWrite_AuditRow`; flip the CAS comparison → `_VersionConflict_NoWrite`.
L2 delete one existing `RecordAuditEvent` in `v2_admin_users.go` → `TestAdminWriteRoutesAreAudited`
names the route; remove the `c.Set` in `NewAuditEvent` → `_NoDuplicateWhenHandlerAudits`.
L3 move the key to `wantUserVisible` → tier test red; blank the header list → capture test red.
L4 drop `tenant_id` where clause → `TestGetRankings_TenantIsolation`.
L5 drop `TLSNextProto` → `TestGetHttpClientFor_ForceHTTP1Transport` (unit oracle) and
`TestDoRequest_ForceHTTP1NegotiatesHTTP1` (integration, same mutation); skip the map delete →
`TestAffinityPurge_KeyRemovedThenMiss`.
L6 remove `AND used_at=0` → `_BackupCodeConsumedOnce`; remove the step-up middleware from the
real route (`api-v2-router.go`) → `TestSetApiV2Router_ForceDisableTotp_RequiresOwnStepUp`
(`router/v2_admin_security_wiring_test.go`) — `_RequiresStepUp` hand-mounts the middleware and
stays green under this mutation.
L7 remove `RedisDel` and the `revoked_at` check → `_DeletesRedisKeyAnd401` (each alone stays green;
both → red).
Rule from the repo's own memory: commit before mutating; never `git checkout --` over uncommitted
work.

**UAT probes (per lane, §3).** Preconditions: bridge token read from the UAT secret
(`deploy/k8s/r6-uat/README.md`); UAT credit pool topped up before relay probes (e2e drains it);
digest under test recorded before and after (ArgoCD may converge mid-probe — compare pod
`startTime` with artefact timestamps). Each artefact is copied verbatim into the lane report with
the command that produced it; a probe that cannot produce its artefact marks the lane
`⏳ 待验证`, never PASS.

**Order of landing.** L1 → L2 → L3 → L4 → L5 → L6 → L7, one PR each, `main` only, ArgoCD
auto-pin; L2's CI test is expected red on HEAD until L1 merges; L5 rebases over L3's
`api_request.go` hunk; L7 waits for O4.


## 8. Operator amendments after the refuter round (binding; override §3 where they conflict)

Fourteen refuter reports (two per lane) were checked against HEAD `2011f968`. Three lanes were
formally refuted by one refuter each (L1, L5, L7); all are salvageable. Amendments:

### L1 — pricing-integrity
- **Transaction primitive.** `repo.UpdateOption` (`internal/adapter/repo/option.go:211-228`)
  writes through the package-global `DB` and calls `updateOptionMap` itself; wrapping calls to it
  in `repo.DB.Transaction` does not route them through the tx. Add
  `UpdateOptionTx(tx *gorm.DB, key, value string) error` in `option.go` that only does
  `tx.Save(&entity.Option{Key: key, Value: value})` (no in-memory side effect). The handler runs
  the **four** ratio-map persists (model_ratio, completion_ratio, model_price, cache_ratio) plus
  the `PricingVersion` CAS (`UPDATE options SET value=? WHERE key='PricingVersion' AND value=?`,
  rows-affected must be 1) inside one `repo.DB.Transaction`; only after commit does it apply the
  in-memory updates through the same `ratio_setting.Update*ByJSONString` functions
  `updateOptionMap` uses. `internal/adapter/repo/option.go` joins the Files list.
- **Partial-failure oracle.** Add `TestV2PricingWrite_PartialBatchFailure_RollsBackEarlierFields`:
  fail the third persist (or the CAS), then assert (a) the `options` rows of the first two fields
  are unchanged in the DB and (b) memory is unchanged. `_DBFailure_LeavesMemoryUntouched` alone
  does not cover this.
- **Audit row is asynchronous.** `governance.RecordAuditEvent` runs via `gopool.Go`
  (`internal/app/governance/audit.go:29-41`). `TestV2PricingWrite_AuditRow` must poll (bounded)
  for the row; the UAT probe polls `GET /api/v2/admin/audit/events?action=pricing.updated` with
  retries instead of a single immediate GET.

### L2 — audit-completeness
- **Money routes are named, not swept.** `TopupCreditPool` / `CreateCreditPool` /
  `DeleteCreditPool` (`internal/adapter/handler/tenant_credit_pool.go`, zero `governance.` calls
  today), `CreateSwitchPreset`, and the three `/internal/admin` maintenance handlers each gain a
  real `governance.RecordAuditEvent` call in this lane. `knownFallbackRoutes` may not contain any
  route that moves balances; the CI test asserts that list against a small deny-list of money
  handlers.
- **Mark at the persisting call.** Set the audited-context flag inside `RecordAuditEvent` (the
  call that writes the row), not in `NewAuditEvent`; the decoupled construct/record pattern at
  `internal_privacy_erase.go:153-156` shows why.
- **Actor attribution.** `RootJWTAuth`'s Bearer-JWT branch (`admin_jwt_auth.go:79-103`) never
  sets `id`, so `c.GetInt("id")` is 0 for JWT-authenticated root admins (pre-existing). Fallback
  rows record `admin_sub` when `id == 0`; the Risk paragraph is corrected accordingly and this
  gap is listed as an owner item, not claimed closed.
- **Coverage endpoint.** `*gin.Context` has no `Engine()` accessor in gin v1.12.0. Capture the
  mutating admin route list at router build time (package-level slice populated in the router
  setup) and have `GET /api/v2/admin/audit/coverage` read that plus the runtime counters.
- **UAT probe.** `/internal/admin/*` needs `ScopeAdmin`, which the only provisioned key lacks —
  no probe there. The fallback path is proven by the unit test; the UAT artefact is the
  `/audit/coverage` payload plus `pricing.updated` / credit-pool audit rows appearing after the
  L1 probe and a small pool top-up on UAT.

### L3 — upstream-request-id
- **Error rows without signature threading.** `doRequest` (`api_request.go:263-306`) stashes the
  captured id with `c.Set("upstream_request_id", v)` next to the `RelayInfo` write;
  `recordRelayErrorLog` (`relay.go:744`) reads it with `c.GetString`, matching how it reads
  `original_model` / `channel_id`. No change to `processChannelError` /
  `recordTerminalRelayError` signatures; the breaker line at `relay.go:491` stays untouched.
- **File corrections.** `ExportAdminLogsV2` lives in `v2_log_export_admin.go:67` (root-only);
  the tenant-admin filter goes into `GetAllLogsV2` (`v2_log.go:181-240`); `LogQueryParams` is an
  alias — the struct is `internal/domain/entity/log.go:85-115`. The `upstream_model` tier entry
  is `classification.go:79`. `openai/audio.go:32` copies all upstream headers to the client
  writer (TTS pass-through) — unrelated, leave it.
- **Round-2 scope ratification.** The round-2 repair added `internal/app/http.go`
  (`UpstreamHeadersNotForwarded`, consumed by `IOCopyBytesGracefully`) and
  `internal/app/net_fetch_extra_test.go` to close the finding that a vendor sending its own
  `X-Request-Id`/`X-Oneapi-Request-Id` under the OpenAI-wire header name could overwrite the
  gateway's minted id on the non-stream copy path. Operator ratifies this file-list extension;
  `provider.upstreamRequestIdHeaders` (`api_request.go:266`) and `app.UpstreamHeadersNotForwarded`
  are kept in sync by `TestUpstreamRequestIdHeaders_AllSkippedFromClientResponse`
  (`api_request_test.go`), not by comment convention. `openai/audio.go`, `minimax/tts.go`,
  `task/suno/adaptor.go` and `handler/video_proxy.go` copy every upstream header verbatim and are
  intentionally left outside this skip-set (same "leave it" amendment above); documented as a
  known gap in `doc/product-integration-guide.md` (§E, `X-Request-Id` row) rather than fixed in
  code this cycle.

### L4 — rankings
- **No latency subqueries.** Rankings never surface p50/p95; add a latency-free aggregate
  (`GetModelUsageTotals(start, end, tenantID)` or a `skipLatency` parameter) so the two-window
  call is two GROUP BY queries, not hundreds of unindexed percentile subqueries against `logs`.
- **Throttle and cache.** The tenant route gets `CriticalRateLimit()` like the root route; the
  `hours` parameter is snapped to presets `{1, 6, 24, 168, 720}` before it becomes a cache key.
- **Corrections.** `GetChannelTypeName` precedent is `internal/adapter/repo/governance.go:46`;
  both routes are GET without a resource id, so `v2_completeness_test.go` skips them (drop it
  from Files); the console entry goes in the `operations & insights` nav section (minRole 10)
  beside `admin-analytics` in `web/src/components/hifi/HFShell.jsx`.

### L5 — routing operator levers
- **Filter site.** `GetParamOverride()` is called only from `middleware/distributor.go:486`; the
  `__lurus_*` skip lives in `ApplyParamOverride` / `applyOperationsLegacy`
  (`internal/adapter/provider/common/override.go:35/297`) so all call sites inherit it, with
  `TestParamOverride_SkipsLurusKeys` against `override.go`.
- **Honest scope of `force_http1`.** It affects only clients obtained through `GetHttpClientFor`
  in `api_request.go`. AWS, Coze, Vertex service-account, MJ-proxy and the task relays build
  their clients directly (`aws/relay-aws.go:46`, `coze/relay-coze.go:284`,
  `vertex/service_account.go:117/160`, `relay/mjproxy_handler.go:42`, `provider/task/*`) and are
  **not** covered this lane; the integration guide and the param_override help text say so
  explicitly, and the UAT probe uses an OpenAI-wire channel.
- **Separate cache.** Keep `proxyClients` keyed by bare `proxyURL`; H1 clients go in their own
  map (same key scheme), so existing callers of `NewProxyHttpClient` are untouched and the
  cardinality claim holds.
- **Console.** `web/src/pages/v2/Channels/` does not exist; the v2 `Channel/index.jsx` modal has
  no param_override editor. No console switch this lane — operators set the key through the
  legacy `EditChannelModal.jsx` param_override editor
  (`web/src/components/table/channels/modals/EditChannelModal.jsx:2825-2859`); document that
  path.

### L6 — TOTP recovery
- `/user/totp/backup-codes/regenerate` gets its own limiter mark (`"TB"` via
  `rateLimitFactory`), not `CriticalRateLimit()`'s IP-keyed `"CT"` bucket shared with
  `/channel/:id/key` and `/user/totp/disable`.
- Force-disable requires the session-authenticated root path (`SecureVerificationRequired`
  needs `id` in context); Bearer-JWT root callers receive 401 and a test asserts exactly that.
- Split the admin stat: `no_codes_issued` (enrolled before this lane) vs `exhausted` (issued and
  all consumed); no backfill is attempted.
- Add `router/openapi_contract_lock_test.go` and `middleware/abort_code_structural_test.go` to
  Files.

### L7 — session registry
- **Additive response.** Keep `current`, `active_tokens`, `request_count`, `last_seen` as
  emitted today (`v2_sessions.go:78-85`); add `is_current`, `created_at`, `last_seen_at`, `ip`
  (masked /24), `user_agent_family`. Add `TestV2Sessions_HappyPath_RegistryEnabled` pinning the
  flag-on key set; rewrite the forbidden list in `TestV2Sessions_WhitelistEnforced` to raw-IP /
  raw-UA patterns (the bare `ip` substring would trip on the new key).
- **Throttle across replicas.** Production runs 3 replicas; the 60 s last-seen throttle is a
  Redis `SET NX EX 60` guard (in-process map only when Redis is absent), with a test that N
  independent throttle instances against one session key do not amplify writes.
- Citation: per-request re-validation lives at `auth.go:187`.
- Migration **033 `create_user_sessions` is reserved** in the root ledger (2026-09-12).

### Owner decisions — resolved under the owner's blanket authorisation for this cycle
O1 header becomes mandatory the release after the console change ships (keep the skip-guard
test until then) · O2 lazy AutoMigrate precedent for the backup-code table · O3 alert at
>3 `auth.totp_admin_disabled` per root per day · O4 done (033 reserved) · O5 prod flip after a
7-day UAT soak · O6 entitlement plans are deferred to the next planning cycle (built on platform
entitlements, not a newhub ledger) · O7 stays unplanned · O8 the stale "OIDC + Passkey" line in
`CLAUDE.md` is corrected in this cycle's PR · O9 no TLS-verify-skip · O10 no zh-TW.

### Landing
One PR with one commit per lane (L1→L7), like cycle 6; the L2 CI test therefore never sees a
HEAD without L1. Cycle 8 planning (large parity items: async task artefacts, stateful Responses,
entitlement plans, RBAC catalogue, passkey in the platform IdP) starts from
`newapi-parity-matrix-2026-09-12.md` §"Gap list by value".

## 9. Post-repair amendments (what shipped differs from §3/§8 here)

Three acceptance rounds (dev → repair → repair) plus an operator pass changed the following
against the text above. Where they conflict, this section is authoritative.

- **L1 scope widened.** The legacy root writes to the four ratio maps — `PUT /api/option/`
  (also reached through `PUT /api/v2/admin/options`) and `POST /api/option/rest_model_ratio` —
  go through the same transaction, `PricingVersion` compare-and-swap and `pricing.updated`
  audit row as the console batch (`writePricingOptionVersioned`, details carry
  `source: legacy_option_api | legacy_reset`). `repo.InvalidatePricingCache` drops the
  catalogue cache after a versioned write. The batch is merged on the database's committed
  baseline (`SELECT … FOR UPDATE` on the four rows), not on this replica's memory; the version
  the console reads comes from the row, not the option cache. The audit/preview `old` value is
  the effective ratio (family fallback or the catalogue default), never a placeholder 0.
  Consumer notes for the PR body: a DB error while reading the version makes the console send
  header 0, which the next CAS rejects with 409 (retry after refetch); each POST takes five
  row locks; header-less writers serialise on the version row (PostgreSQL behaviour, not
  reproducible in the SQLite tier); preview diffs are computed from this replica's memory while
  the write merges on the DB rows.
- **L2.** `credit_pool.funded` (resource `credit_pool`, actor `system` = the internal API key
  id) is emitted by the internal fund route; the round-1 note that it was "explicitly not
  audited" is withdrawn. A guarded handler that panics still produces no fallback row and no
  counter increment (the code after `c.Next()` is skipped during the unwind) — the sweep only
  keeps the pending entry from leaking. Root admins authenticated by Bearer JWT are recorded
  with actor id 0 plus `admin_sub` (pre-existing gap, owner item).
- **L3.** The captured id is reset at the top of each retry iteration so channel B's error row
  cannot carry channel A's id. The vendor-side `X-Request-Id` family is stripped from client
  responses on the shared copy path; the raw-copy paths that remain are listed in the
  integration guide with their exact behaviour (`Set` replaces the gateway id, the video proxy's
  `Add` appends a second value). The capture-list/skip-set lock is one-directional.
- **L5, coverage of `__lurus_force_http1` corrected twice.** Final truth: every request that
  goes through `provider.doRequest` (`DoApiRequest`, `DoTaskApiRequest`) and, after round 3,
  the task adaptors' `FetchTask` polls (`app.GetHttpClientFor` with the channel's flag; a
  structural test enumerates `provider/task/*/adaptor.go`). Not covered: baidu access-token
  fetch, dify and replicate uploads, the ali image poll, AWS signing, Coze result poll, Vertex
  service-account token exchange, MJ-proxy image fetch, and the v2 channel-test route
  (`channelTestHTTPClient`); only the legacy `GET /api/channel/test/:id` path exercises the
  pin. §8's sentence "the task relays are not covered" is superseded. `DELETE
  …/routing/affinity?all=true` returns `complete:false` when the SCAN round cap is hit and the
  audit row says so; the console help text names the uncovered categories.
- **L6.** Backup codes live in a lazily created table (`UserTOTP` precedent) and the privacy
  erasure guards on `HasTable`; `notified` in the force-disable response is true only when a
  notification target existed; the admin stat splits `no_codes_issued` from `exhausted`.
- **L7.** After a remote revoke the browser is not locked out: both 401 branches of
  `authHelper` clear the cookie with the store's own Domain/Secure/SameSite
  (`middleware.SessionCookieBaseOptions`), so the next login mints a new session id — the UAT
  probe gains a "re-login from the revoked jar → 200 with a new id" step. `user_sessions` rows
  are hard-deleted by the privacy-erasure cascade and by `DeleteUserById`, and a daily
  leader-gated sweep (`lifecycle.StartSessionSweepWithContext`) removes rows revoked more
  than 30 days ago or idle more than 90 days. Listing is bounded by `created_at` as well as
  `last_seen_at` because the Redis TTL is fixed at login. With the flag off, the new DELETE
  routes answer JSON 404 `SESSION_NOT_FOUND` / `{"revoked":0}` (they did not exist before this
  lane); pre-existing endpoints are unchanged. The response is additive (old keys kept).
- **Prose rule applied across all lanes:** absolute words (every/never/always/only/all/exactly)
  and counts of enumerated lists were removed from comments and docs unless a test in the same
  package proves them.
