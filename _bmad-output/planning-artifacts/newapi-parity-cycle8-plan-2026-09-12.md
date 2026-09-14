# New API parity — cycle 8 plan (2026-09-12)

HEAD at synthesis: `77ff75b1` (main). Cycle 7 is mid-flight in the working tree (L1 pricing:
`v2_pricing_write.go` +451/-109, `repo/option.go` +74, `governance/audit_action.go` +8 at the time
of this read); none of cycle 7's seven lanes is on `main` yet. **Cycle 8 branches from the commit
that merges cycle 7**, and every file:line below that lives in a cycle-7-touched file is cited
against HEAD `77ff75b1` with a note where cycle 7 will shift it.

Inputs: `newapi-parity-matrix-2026-09-12.md`, `newapi-parity-cycle7-plan-2026-09-12.md`, eight
corrected deep-dives (stateful-responses, plans-entitlements, rbac, passkey-idp, tiered-billing,
channel-constraints-routing, payments-topup, console-ops-parity), three lens plans (operator /
enterprise-buyer / architect), two judge reports. Every `file:line` below was re-read on HEAD
during synthesis; nothing is carried from a lens plan or a dive unverified, and the corrections
attached to the dives override the dive text wherever they conflict.

## 1. Mandate & method

Owner mandate: newhub offers no fewer features than upstream New API and is more robust, reliable,
leading and valuable; platform (`2l-svc-platform`) changes are authorised when newhub needs them.
Cycle 7 ships pricing write integrity, fail-closed admin audit, upstream request id, rankings,
`force_http1` + affinity ops, TOTP recovery codes and the per-device session registry (migration
033). **Cycle 8 takes the large parity items** the cycle-7 landing note deferred: stateful
Responses, entitlement plans (as a relay-token gate on platform entitlements), RBAC delegation,
tiered billing — plus the two verified live defects the dives surfaced (a paid-tier bypass and an
unlimited anonymous redemption route).

Method:

1. Spine = the highest-scoring lens plan summed over both judges (operator, §2).
2. Grafted the judges' "best ideas from losers" where they survive re-verification on HEAD:
   the buyer's `RecordLeaderTaskSuccess`-inside-the-tick heartbeat (the only variant that does
   not restructure five ticker loops), the buyer's `authHelper`-first grant middleware (the only
   variant that is not dead on UAT's bridge-login path), the architect's single
   resource+tenant-scoped grant table (one migration, not two), the operator's explicit 034/035
   ledger sequencing and `store=false` opt-out.
3. Dropped every lane a judge called fatal unless the judge could be shown wrong with a
   `file:line`; §5 records each removal, and the two rebuttals (the Responses lanes are
   UAT-provable once one owner precondition is met — §3 L6/L7 give the vendor-independent
   artefact — and the entitlement lane's probe *is* stateable, it just needs a platform-side
   owner step that the lens plans left unstated).
4. Re-anchored every surviving lane on the real tree: exact files, routes, defaults, test
   names, and a UAT probe whose artefact can only exist if the behaviour runs.
5. Ordered lanes so no later lane modifies what an earlier lane produced; shared files are
   declared per lane and summarised in §4.

Architectural invariants (a lane violating one is disqualified): tenant-scoped state; identity in
the platform IdP; money through the platform when unified billing is on; PG-only with numbered
migrations from 034 (ledger reservation first); Prometheus `/metrics` scraped by netdata, no
stack change; every existing gate stays green; every lane provable on
`https://test-newhub.lurus.cn` (UAT: OIDC off, unified billing off, `ERROR_LOG_ENABLED=true`,
`CREDIT_POOL_REQUIRED=enforce` — `deploy/k8s/r6-uat/deployment.yaml:125-134`); production
read-only.

## 2. Scoreboard (judge scores)

| Lens | Judge 1 | Judge 2 | Total | Rank |
|------|---------|---------|-------|------|
| operator | 68 | **75** | **143** | 1 — spine |
| architect | 52 | **78** | 130 | 2 — unified grant table graft; four lanes rejected (§5) |
| enterprise-buyer | **72** | 55 | 127 | 3 — heartbeat mechanism, `authHelper`-first grant middleware, corrected baselines |

Judge 1 ranked the buyer plan first on tree accuracy; judge 2 ranked the architect plan first on
mandate coverage. Summed, the operator plan wins by 13: it is the only plan that takes both large
stateful-Responses items *and* closes the paid-tier bypass *and* keeps the dead rate limiters as a
standalone lane. Its two recorded defects — the "wrap in `NewLeaderTask`" misdescription and the
grant middleware silent on the session path — are replaced by the buyer's verified mechanisms.
The architect's `0NN` migration placeholders are resolved to 034/035 in that order.

Both judges' fatal findings, and how this plan answers each, are in §5.

## 3. Lanes (binding order L1 → L7)

Common conventions (carried from cycle 7 §3, re-verified):

- Error envelope: `{"success":false,"message":"…","error_code":"…"}`; relay-side rejections go
  through `abortWithOpenAiMessage(c, status, msg, code)` — the structural gate
  `middleware/abort_code_structural_test.go:38 TestAbortWithOpenAiMessage_EveryCallSiteCarriesACode`
  fails any call site without the code argument.
- Every new v2 by-id mutation gets a `swept`/`exempt` entry in
  `router/v2_completeness_test.go` (`exempt` map at `:73`; root-global precedent `:116-117`).
- Every new console call resolves through `router/frontend_route_contract_test.go`.
- Every new Prometheus series has a production writer
  (`internal/pkg/metrics/declared_series_written_test.go`).
- Every new `/v1` route is documented in `docs/openapi/relay.json` and every new `ErrorCode`
  literal is added to the `GatewayError.code` enum there
  (`router/openapi_contract_lock_test.go:99 TestOpenAPIContract_PathsExistInRealRouteTable`,
  `:289 TestOpenAPIContract_ErrorCodeEnumMatchesTypesPackage`).
- Audit rows are written through `governance.RecordAuditEvent` (`internal/app/governance/audit.go:32`,
  asynchronous via gopool — tests and probes poll, bounded) with actions registered in
  `audit_action.go`'s const block and `validAuditActions` (`:156`), never a new writer.
- UAT login for probes: `https://test-newhub.lurus.cn/bridge-login#t=<E2E_BRIDGE_TOKEN>`
  (`deploy/k8s/r6-uat/README.md:41-46`) or `POST /api/v2/bridge/exchange`; the bridge token is
  read from the UAT secret on R6. UAT's only upstream channel today is DeepSeek behind channel
  type 1 (`doc/uat/business-acceptance-tests.md:142-147`, `:259`), one routable model
  (`:306`).
- Migrations: `migrations/034_*.sql` and `035_*.sql` follow the 032 dual pattern
  (`migrations/032_create_tenant_invites.sql` header: entity in `repo.migrateDB` **and** an
  idempotent SQL twin; `CREATE TABLE/INDEX IF NOT EXISTS`, one transaction, no `ON CONFLICT`
  on the record). Ledger: root `doc/coord/migration-ledger.md:134-135` shows 033 reserved,
  034 `(next available)` — reserve **034 (L4) then 035 (L7)** before either SQL is written.

### L1 — redemption rate limiting: wire the two built, never-mounted limiters

**Why.** `middleware.RedemptionRateLimit()` (5/60 s/IP, mark `"RD"`) and `TopupRateLimit()`
(`"TU"`) are fully implemented at `internal/adapter/middleware/rate-limit.go:149-157`; a grep for
call sites outside that file returns none (only the factory map in
`middleware_cover_test.go:452-453`). Four live redemption-code entry points have no limiter:
`POST /api/user/topup` (`router/api-router.go:60`, `UserAuth` only),
`POST /api/v2/:tenant_slug/redeem` (`router/api-v2-router.go:212`, `UserAuth`+`TenantSlugGuard`),
`POST /api/v2/switch/redeem` (`:304`, **no auth at all** — the handler's own doc comment at
`switch_redeem.go:90` says "No middleware should be attached: this endpoint is anonymous by
design", which is the reason it needs an IP limiter, not a reason it cannot have one) and
`POST /api/v2/switch/user/topup` (`:317`, raw-token auth inside the handler). `apiV2` mounts only
`CORS` and `RequestBodySizeLimit` (`:18/:23`); `switchGroup` (`:299`) mounts nothing. All four call
`repo.Redeem` (or `RedeemCodeV2`) — they are code-guessing surfaces. Both judges kept this as a
standalone lane and rejected the architect's "land opportunistically" (no lane touched those
router lines, so it would have evaporated).

**Matrix ids.** topup-payments-subscriptions-35 (missing, value 3).

**Spec.** Insert `middleware.RedemptionRateLimit()` immediately before the handler on all four
routes — it is the redemption limiter for all four because all four consume a redemption code
(`switch_user_topup.go:56 repo.Redeem(key, token.UserId)` is redemption, not the unrouted
wallet-to-quota `TopUpV2` that `TopupRateLimit` was written for). `TopupRateLimit` stays
unmounted with a one-line comment naming its intended consumer (`TopUpV2`, unrouted by owner
decision — `api-v2-router.go:270`). No handler change, no new error shape: on trip the middleware
answers HTTP 429 with an empty body and `X-RateLimit-Scope: ip`, `X-RateLimit-Type: requests`,
`X-RateLimit-Limit/Remaining`, `Retry-After` (`rate-limit.go:74-82` Redis path, `:97-103`
memory path). Keyed by `c.ClientIP()`, which is correct behind the host nginx because
`TrustedProxies` is configured (`router/trusted_proxies_test.go`).

**Files.** `internal/adapter/handler/router/api-router.go`,
`internal/adapter/handler/router/api-v2-router.go`,
`internal/adapter/handler/router/r6d_switch_redeem_mount_test.go` (extend — its header says it
"does NOT prove … the middleware chain"; this lane makes it prove exactly that),
`internal/adapter/handler/router/redemption_rate_limit_mount_test.go` (new).

**Tests.** `TestRedemptionRoutes_RateLimitMounted` (table over the four routes; in-memory
limiter, `common.RedisEnabled=false`; six requests from one `X-Forwarded-For`; requests 1–5 reach
the handler — asserted by the handler's own pre-fix status, 200 `success:false` for
`/switch/redeem`, 400 for the others with a well-formed unknown code — request 6 is 429 with
`X-RateLimit-Scope: ip`); `TestRedemptionRoutes_RateLimitIsPerIP` (a second IP is not throttled
by the first); mutation: delete the `RedemptionRateLimit()` argument on one route → the table
test names that route.

**UAT probe.** Six `POST https://test-newhub.lurus.cn/api/v2/switch/redeem` with body
`{"code":"<32 hex that does not exist>","fingerprint":"probe"}` inside 60 s from one IP.
Pre-fix baseline (verified at `switch_redeem.go:124-131`): all six answer **HTTP 200**
`{"success":false,"message":"激活码不存在"}` — not 400, correcting the stateful-responses and
payments dives. Post-fix artefact: calls 1–5 unchanged, call 6 **HTTP 429** with
`X-RateLimit-Scope: ip` and `Retry-After`. Repeat for `POST /api/v2/lurus/redeem` (bridge
session) and `POST /api/v2/switch/user/topup` (raw token).

**Migration.** None. **Platform change.** None.

**Risk.** Low. The only failure mode is a legitimate UI retry loop hitting 5/60 s; the console's
redeem forms are single-submit (no retry loop found in `web/src` redeem callers — re-grep before
merge). Existing 429 shape is already documented for relay routes
(`TestOpenAPIContract_429sDocumentRetryAfterAndScope`); these are `/api` routes, outside
`relay.json`.

**Consumer-visible changes.** A sixth redemption attempt per IP per minute answers 429 on four
routes that previously never did. `2c-app-switch` (consumer of `/switch/redeem` and
`/switch/user/topup`, `doc/coord/contracts.md`) must treat 429 + `Retry-After` as retryable —
add to `contracts.md`/`changelog.md` per the root rules.

### L2 — entitlement tier → relay-token model gate, with downgrade revocation

**Why.** `ProvisionV2` (`internal/adapter/handler/v2_provision.go:116`) verifies a
platform-signed entitlement JWS offline (`entverify.Claims.Ent map[string]string`,
`internal/pkg/entverify/entverify.go:91-98`), gates `plan_code` on `cc_` (`:37`, `:182-186`) and
mints/refreshes/replays a token named `switch-provision-<plan_code>` (`:42`, `:183`). The mint
literal (`:386-397`) sets `Group: "default"` and never `ModelLimitsEnabled`/`ModelLimits`; the
refresh-in-place `Updates` map (`:303-306`) touches only `expired_time`/`accessed_time`; the
replay branch (`:270-291`) returns the row as-is. `claims.Ent["models"]` is read nowhere (grep
`Ent["models"]` → 0). Yet the platform emits it for all six seeded plans
(`2l-svc-platform/migrations/119_seed_claude_code_plans.sql:25-35`, `features.models` arrays;
`internal/app/entitlement_service.go:369,417` `anyToString` JSON-encodes the array into the
`ent` claim), and newhub already **enforces** `Token.ModelLimits` for every other token
(`entity/token.go:21-22`; `middleware/distributor.go:119,129` → 403 `ErrorCodeModelBlocked`).
Net: the cheapest plan's token can call every model the most expensive plan can — a live
under-billing path with the gate already built one layer down. Paired gap: on a plan change the
previous plan's token is never disabled (grep `switch-provision` → only the mint site), so a
downgrade never revokes.

**Matrix ids.** topup-payments-subscriptions-14 (group-based upgrades/downgrades, missing,
value 4) and -16 (user subscription status, partial) — delivered at newhub's token granularity on
platform entitlements, which is the O6 resolution cycle 7 recorded ("built on platform
entitlements, not a newhub ledger").

**Spec.**
1. `parseEntitlementModels(ent map[string]string) []string`: read `ent["models"]`; empty/absent →
   `nil` (unrestricted, today's behaviour); JSON array → the list (trimmed, deduplicated, empty
   strings dropped); non-empty but not a JSON array → split on `,` and `SysError` once (a
   platform encoding change must degrade to *more* restriction, never to unrestricted).
2. Apply on all three paths: mint literal gets `ModelLimitsEnabled: len(models)>0,
   ModelLimits: strings.Join(models, ",")`; the refresh `Updates` map adds both keys; the replay
   branch compares the existing row's `ModelLimits` with the claim and, if different, runs the
   same `Updates` before returning (a plan's model list can change between calls). Length guard:
   `ModelLimits` is `varchar(1024)`; reject with 500 `PROVISION_FAILED` if the join exceeds it.
3. Sibling revocation, on every successful verify before the mint/refresh/replay branch:
   `repo.DB.Model(&repo.Token{}).Where("user_id = ? AND name LIKE ? AND name <> ? AND status = ?",
   user.Id, "switch-provision-%", tokenName, common.TokenStatusEnabled).Updates(status=Disabled,
   accessed_time=now)`; for each affected row record `governance.ActionTokenStatusChanged`
   (existing, `audit_action.go:34`) with details `{"reason":"plan_changed","from":"<old
   name>","to":"<plan_code>"}` — reuse, no new action constant, so this lane does not touch
   `audit_action.go`. Token cache: go through the existing token-cache invalidation used by
   token status writes (`repo/token.go`), not a bare `UPDATE`, per the 2026-09-01 stale-cache
   lesson.
4. Log the resolved model list in the existing `SysLog` lines (`:275`, `:317`, `:418`).

**Files.** `internal/adapter/handler/v2_provision.go`,
`internal/adapter/handler/v2_provision_test.go` (existing hermetic harness: `httptest` JWKS +
`entverify.WithInsecureJWKS()` via `setProvisionVerifier`, `:85-91`),
`internal/adapter/repo/token.go` (only if a status-write helper with cache invalidation is not
already exported), root `doc/coord/contracts.md` (`:492` documents `/provision`) and
`doc/coord/changelog.md`.

**Tests.** `TestProvisionV2_ModelsClaim_SetsModelLimits` (mint: `model_limits_enabled=true`,
`model_limits` equals the exact CSV of the claim — assert the string, not non-empty);
`TestProvisionV2_ModelsClaim_RefreshInPlaceApplies` (existing row with limits off flips on);
`TestProvisionV2_ModelsClaim_ReplayReconciles` (replay with a changed list updates the row);
`TestProvisionV2_NoModelsClaim_Unrestricted` (regression guard);
`TestProvisionV2_MalformedModelsClaim_FailsClosed` (CSV fallback, not unrestricted);
`TestProvisionV2_PlanChange_DisablesSiblingToken` (plan A token enabled → provision plan B → A
is `Disabled`, audit row `token.status_changed` with `reason:plan_changed`);
`TestProvisionV2_SamePlanReplay_DoesNotSelfDisable` (the `name <> tokenName` boundary);
`TestDistributor_ProvisionedTokenBlockedModel_403` (end-to-end through
`middleware.Distribute()` with the minted token: allowed model passes TokenAuth+Distribute to a
stub, disallowed answers 403 `model_blocked`). Mutation: remove the `ModelLimitsEnabled` line →
first test red; remove the `name <>` clause → the same-plan test red.

**UAT probe.** No isolated entitlement issuer exists: `getProvisionVerifier` defaults to
`https://identity.lurus.cn/api/v1/entitlements/jwks` (`v2_provision.go:30-31,56`) and no UAT
manifest overrides `PLATFORM_JWKS_URL` (grep `deploy/` → 0). The lens plans wrote "mint a
cc_trial token via /provision" — `/provision` consumes, it does not mint. The stateable probe is
(owner step **O2**): on the platform, credit a dedicated test account's wallet via the admin
route `POST /admin/v1/accounts/:id/wallet/credit`
(`2l-svc-platform/internal/adapter/handler/router/router.go:1146`), check out the cheapest `cc_`
plan with `payment_method=wallet` (`SubscriptionHandler.Checkout`, `handler/subscription.go:210`;
no merchant credentials involved), fetch `GET /api/v1/entitlements/llm-api`
(`entitlement_token_handler.go:59`) → `entitlement_token`. Then `POST
https://test-newhub.lurus.cn/api/v2/switch/provision {"entitlement_token":…}`. Artefact 1
(DB, `newhub_uat`): the `tokens` row named `switch-provision-<plan>` has
`model_limits_enabled=true` and `model_limits` equal to the plan's list — before the fix both
are `false`/`''`. Artefact 2: a relay call with that key naming a model *outside* the list →
**403 `model_blocked`** (TokenAuth-stage shape); a call naming a listed model → **404
`model_not_found`** on UAT (no channel serves it — `business-acceptance-tests.md:306` records
exactly this 403-vs-404 discrimination as the intended oracle when only one model is routable).
Artefact 3 (downgrade): repeat with a second plan for the same account → the first token's
`status` flips to `Disabled` and the audit feed shows `token.status_changed`
`reason:plan_changed`. The 200-on-allowed-model leg needs a real multi-model channel (O3) and is
recorded `⏳ 待验证` until then, never PASS.

**Migration.** None. **Platform change.** None in code; O2 creates one wallet-funded
subscription for a test account on the single shared platform instance.

**Risk.** Low-medium. Parsing the wire shape wrong must fail closed (tested). Sibling
revocation runs on *every* verify, so a user holding two distinct `cc_` plans concurrently
would see the older token disabled on each alternate call — the seeded catalogue has one plan
per product per account, and O4 asks the owner to confirm.

**Consumer-visible changes.** `2c-app-switch` users on a plan with a `models` list get 403
`model_blocked` for models outside it (previously served); after a plan change the previous
plan's key answers 401 disabled. Documented in `contracts.md` §`/provision` and the switch
integration guide.

### L3 — background-task heartbeats: the five blind leader-gated jobs + admin feed

**Why.** `metrics.LeaderTaskLastSuccess` (`internal/pkg/metrics/instance.go:28-42`, GaugeVec by
`task`) and `metrics.RecordLeaderTaskSuccess(name)` (`:60-70`) exist and are written by exactly
one task, `secret-rotation` (`lifecycle/secret_rotation.go:27` via `NewLeaderTask`,
`leader_election.go:130`, seeds 0 at `:141`). Five other periodic jobs have no last-run signal
anywhere (grep `LastRunAt|last_run|LastSuccess` across `internal/,cmd/` → only
`openrouter_sync*` and the secret-rotation gauge): `lifecycle/audit_cleanup.go:62-95`,
`lifecycle/privacy_erasure.go:34-67`, `app/credit_pool_reconcile.go:368-404`,
`app/openrouter_pool/reaper.go:23-56`, `handler/channel-test.go:672` — all started as raw
`Start*WithContext`/`Auto*WithContext` loops from `cmd/server/main.go:230-242,283-297`, each
owning its own `time.NewTicker`+`select`. `openrouter_sync/scheduler.go` is **not** blind: `GET
/api/openrouter-sync/last-status` (`handler/openrouter_sync.go:238-259`) already returns
`last_run_at`/`last_error` per job, rendered at `web/src/pages/OpenRouterSync/index.jsx:279` —
the console-ops correction stands, five not six. Both judges rejected "wrap in `NewLeaderTask`":
that restructures each loop and, for channel-test (gated by `IsMasterNode` only, no `IsLeader`
check — `channel-test.go:673`), would silently change production from three-replica to
leader-only auto-testing.

**Matrix ids.** console-ux-19 (system task scheduler visibility), logs-analytics-observability
rows on background job health cited in the console-ops dive; ops-deploy-docs-07 stays excluded
(instance registry is k3s/ArgoCD/netdata).

**Spec.**
1. Inside each job, immediately after a successful pass, call
   `metrics.RecordLeaderTaskSuccess("<name>")`; at each `Start*WithContext` entry call
   `metrics.LeaderTaskLastSuccess.WithLabelValues("<name>").Set(0)` so the series exists at boot
   (the reason `NewLeaderTask` does it, `instance.go:31-37`). Names: `audit-cleanup`
   (`runAuditCleanup :95`, stamp when the delete loop returns without error),
   `privacy-erasure` (`runErasurePass :67`, stamp only when no request in the pass returned an
   error), `credit-pool-reconcile` (`runCreditPoolReconcileTick :368`, stamp when
   `ReconcileStrandedTopups` returns `err == nil`), `openrouter-pool-reap` (after `ReapOnce`
   returns nil, `reaper.go:42-56`), `channel-health-test` (after `testAllChannels` returns nil
   in `AutomaticallyTestChannelsWithContext`). Failure paths do not stamp. No change to any
   loop's schedule, gating, or error handling.
2. New `internal/pkg/taskreg` (no imports from `handler`/`lifecycle`, so no cycle):
   `Register(name string, interval func() time.Duration, leaderOnly bool)` called by each job at
   start (six calls: the five above plus `secret-rotation`), `Snapshot()` returns the registry.
   L7 registers its sweep here too (additive).
3. `GET /api/v2/admin/system/tasks` (under `adminRoute`, `RootJWTAuth`) in new
   `handler/v2_admin_system_tasks.go`: reads each registered task's gauge through the client
   library's `Write(&dto.Metric)` on `LeaderTaskLastSuccess.WithLabelValues(name)` (no `/metrics`
   text parsing), returns `{"success":true,"data":{"is_leader":bool,"pod":…,"tasks":[{"name",
   "interval_seconds","leader_only","last_success_at","overdue"}]}}` with `overdue = now -
   last_success_at > 2*interval` when `last_success_at>0`, else `process_uptime > 2*interval`.
   Per-pod by design (three prod replicas each answer for themselves; the endpoint is a health
   view, not an aggregator).
4. Console: `web/src/pages/v2/Admin/SystemTasks.jsx` (root nav entry in the `operations &
   insights` section, `web/src/components/hifi/HFShell.jsx:314-316`, `minRole` 100), table with a
   red badge when `overdue`. i18n keys in `en.json`/`zh.json`.

**Files.** `internal/lifecycle/audit_cleanup.go`, `internal/lifecycle/audit_cleanup_test.go`,
`internal/lifecycle/privacy_erasure.go`, `internal/lifecycle/privacy_erasure_test.go`,
`internal/lifecycle/secret_rotation.go` (one `taskreg.Register` call),
`internal/app/credit_pool_reconcile.go` (+ test), `internal/app/openrouter_pool/reaper.go`
(+ test), `internal/adapter/handler/channel-test.go`, `internal/pkg/metrics/instance.go`
(comment at `:31` no longer says "sole caller"), `internal/pkg/taskreg/taskreg.go` (+ test, new),
`internal/adapter/handler/v2_admin_system_tasks.go` (+ test, new),
`internal/adapter/handler/router/api-v2-router.go` (one route under `adminRoute`),
`internal/adapter/handler/router/frontend_route_contract_test.go`,
`web/src/pages/v2/Admin/SystemTasks.jsx` (+ test, new), `web/src/components/hifi/HFShell.jsx`,
`web/src/i18n/locales/{en,zh}.json`.

**Tests.** Per job, `Test<Job>_SuccessfulTickStampsHeartbeat` and
`Test<Job>_FailedTickDoesNotStamp` using `testutil.ToFloat64(metrics.LeaderTaskLastSuccess.
WithLabelValues(name))` (pattern at `leader_election_test.go:76`);
`TestTaskreg_RegisterSnapshot`; `TestSystemTasks_JSONMatchesGauge` (seed a gauge, assert the
JSON `last_success_at` equals the gauge value byte-for-byte);
`TestSystemTasks_OverdueBoundary` (injected clock at exactly `2*interval` and `+1 s`);
`TestSystemTasks_RootOnly` (admin role 10 → 403). Mutation: delete one
`RecordLeaderTaskSuccess` call → that job's stamp test red.

**UAT probe.** `GET https://test-newhub.lurus.cn/api/v2/admin/system/tasks` as root (bridge
session) → six tasks, each `last_success_at` non-zero and inside its interval (single replica =
always leader). Cross-check `curl -s http://localhost:30851/metrics` on R6 (direct NodePort,
the netdata path) for
`lurus_gateway_leader_task_last_success_timestamp_seconds{task="audit-cleanup"}` etc.: the two
numbers match exactly. Before the fix, five of the six series are absent from `/metrics`
altogether — absence→presence is the artefact.

**Migration.** None. **Platform change.** None.

**Risk.** Low. A typo in a task name yields a permanent-zero series, visibly distinct from a
healthy timestamp because of the `Set(0)`-at-boot step. `overdue` is a raw signal; alert
policy is deliberately not bundled.

**Consumer-visible changes.** New root-only endpoint and console page; five new
`{task=…}` series on `/metrics` (netdata scrapes them without configuration).

### L4 — admin permission grants: delegated `audit:read` without granting root

**Why.** `adminRoute := apiV2.Group("/admin"); adminRoute.Use(middleware.RootJWTAuth())`
(`router/api-v2-router.go:357-358`) gates the whole subtree including
`GET /admin/audit/events|actions|export|chain-verify` (`:458-462`). `RootJWTAuth`
(`middleware/admin_jwt_auth.go:59-104`) with no `Authorization` header falls back to
`authHelper(c, common.RoleRootUser)` (`:62-65`) — the session/bridge path every UAT and console
request takes — and with a Bearer JWT requires the `root` string role (`:83-90`). There is no
third granularity anywhere (`handler/v2_rbac.go:19-38` is the same two-tier check inline). A
compliance reader therefore needs full root. Upstream ships two fixed roles plus per-user
override deltas and a 3-resource registry — not a role editor — so the parity target is a
grant table, and both judges required (a) the four audit routes leave the `RootJWTAuth` group
(an in-handler OR is dead code because the group middleware aborts first) and (b) the check
runs on the session path (a JWT-claims-only check is dead on UAT). The architect's
resource+tenant-scoped table is grafted so cycle 9's channel `sensitive_write` (§5) needs no
second migration.

**Matrix ids.** auth-security-17 (permission catalog / per-resource overrides, missing, value
4), auth-security-18 (RBAC, partial, value 4), console-ux-36 (authz management, missing).

**Spec.**
- **Migration 034 `create_admin_permission_grants`** (ledger reservation first): table
  `admin_permission_grants(id bigserial PK, user_id bigint NOT NULL, tenant_id varchar(36) NULL,
  resource varchar(64) NOT NULL, action varchar(32) NOT NULL, granted_by bigint NOT NULL,
  created_at bigint NOT NULL, revoked_at bigint NULL)`; `CREATE UNIQUE INDEX IF NOT EXISTS
  uk_admin_permission_grants_active ON admin_permission_grants(user_id, COALESCE(tenant_id,''),
  resource, action) WHERE revoked_at IS NULL`; index on `user_id`. Entity
  `entity.AdminPermissionGrant` registered in `repo.migrateDB` **without** a GORM unique tag
  (GORM cannot express the partial index and would otherwise add a conflicting full unique
  index on every boot — the 031 lesson recorded in the ledger).
- **`middleware.RootOrGranted(resource, action string) gin.HandlerFunc`** (new
  `middleware/root_or_granted.go`): no `Authorization` header → `authHelper(c,
  common.RoleAdminUser)` (`auth.go:36`; sets `id`/`role` at `:263-264`, aborts on its own); if
  aborted return; `role >= RoleRootUser` → `Next()`; else `repo.HasActivePermissionGrant(
  c.GetInt("id"), nil, resource, action)` (direct DB read, no cache — small table, admin QPS) →
  `Next()` or `403 {"success":false,"message":"insufficient permission","error_code":
  "PERMISSION_DENIED"}` + `Abort()`. With a Bearer JWT and OIDC on: behave exactly as
  `RootJWTAuth`'s JWT branch (root only) — the JWT branch never sets `id` (cycle 7 §8 L2
  finding), so grants are session-path only this cycle; stated in the handler doc and tested.
- **Routes.** New `auditRoute := apiV2.Group("/admin/audit"); auditRoute.Use(
  middleware.RootOrGranted("audit","read"))`; move the four GETs there unchanged (keep
  `CriticalRateLimit()` on the three that have it). Everything else stays under `adminRoute`.
  This is a deliberate exception to cycle 7's "never a new auth tier" convention, confined to
  four read routes.
- **Grant management** (root-only, under `adminRoute`): `GET /api/v2/admin/authz/grants`,
  `POST /api/v2/admin/authz/grants {"user_id":int,"resource":"audit","action":"read",
  "tenant_id":null}` → 201 `{"success":true,"data":{"id":…}}`, 409 `GRANT_EXISTS` on an active
  duplicate, 400 `GRANT_INVALID` when `resource`/`action` is not in the static catalogue (this
  cycle: `{audit: [read]}`; `tenant_id` must be null for `audit`); `DELETE
  /api/v2/admin/authz/grants/:id` → 200 (sets `revoked_at`), 404 if absent/revoked.
  `GET /api/v2/admin/authz/catalog` returns the static catalogue and the two fixed roles
  (tenant-admin ≥10, root ≥100) — the read-only registry the rbac dive proposed, no table.
- **Audit.** New constants `ActionPermissionGranted = "authz.permission_granted"`,
  `ActionPermissionRevoked = "authz.permission_revoked"`, `ResourceAuthz = "authz"` in
  `audit_action.go` (const block + `validAuditActions`), fired by the two write handlers with
  details `{grantee_user_id, resource, action, tenant_id}` — the grant that unlocks the audit
  feed is itself in the feed.
- **Completeness lock.** `DELETE /api/v2/admin/authz/grants/:id` → `exempt` entry with the
  root-global reason (precedent `v2_completeness_test.go:116-117`).

**Files.** `migrations/034_create_admin_permission_grants.sql` (new),
`internal/domain/entity/admin_permission_grant.go` (new), `internal/adapter/repo/main.go`
(migrateDB registration), `internal/adapter/repo/admin_permission_grant.go` (+ test, new),
`internal/adapter/middleware/root_or_granted.go` (+ test, new),
`internal/app/authz/catalog.go` (+ test, new), `internal/adapter/handler/v2_admin_authz.go`
(+ test, new), `internal/adapter/handler/router/api-v2-router.go`,
`internal/adapter/handler/router/v2_completeness_test.go`,
`internal/app/governance/audit_action.go`, `internal/app/governance/audit_action_test.go`
(`TestAllAuditActions_*`), root `doc/coord/migration-ledger.md` (034), console:
`web/src/pages/v2/Admin/Authz.jsx` (+ test, new; root nav, `governance` section) and i18n.

**Tests.** `TestAdminPermissionGrantRepo_ActiveUniqueAndRevoke`;
`TestRootOrGranted_SessionRootPasses`, `_SessionAdminNoGrant403`, `_SessionAdminWithGrant200`,
`_GrantForOtherActionStill403`, `_RevokedGrant403`, `_BearerJWTNonRoot403` (documents the
JWT-branch scope); `TestAuditRoutes_MountedUnderRootOrGranted` (router-level: admin session
with grant reaches `GetAuditEvents`; the same session is still 403 on `GET /admin/tenants/:id`
— the negative test that proves the grant does not leak into `adminRoute`);
`TestAuthzGrants_CreateFiresAuditRow` (polls for `authz.permission_granted`);
`TestAuthzGrants_CatalogRejectsUnknownResource`. Mutation: comment out the grant lookup →
`_SessionAdminWithGrant200` red; register the four audit routes back under `adminRoute` →
`TestAuditRoutes_MountedUnderRootOrGranted` red.

**UAT probe.** Root (bridge, `user_id=1`) creates a second user with role 10 (existing admin
user API) and a bridge session for it; `GET /api/v2/admin/audit/events` with that session →
**403 `PERMISSION_DENIED`**. Root `POST /api/v2/admin/authz/grants {"user_id":<u>,"resource":
"audit","action":"read"}` → 201. Same admin session, same GET → **200** with a `data` array that
contains an `authz.permission_granted` row naming `<u>` (poll ≤5 s). `GET
/api/v2/admin/audit/chain-verify` as root → still valid. Same admin session `GET
/api/v2/admin/tenants/<id>` → **403** (no leak). Root `DELETE …/grants/<id>` → admin GET
returns 403 again. The 403→200→403 sequence plus the self-referential row is the artefact.

**Migration.** 034 as specified. **Platform change.** None.

**Risk.** Medium — a new authorization surface. Fail-closed by construction (no row → 403);
route-group scoping is the load-bearing property and has its own negative test; `tenant_id`
is present but only `NULL` is accepted this cycle (O5 decides tenant-scoped semantics before
cycle 9 uses the column).

**Consumer-visible changes.** Four audit GETs accept a delegated admin; new root endpoints
`/api/v2/admin/authz/{grants,catalog}`; new audit actions; new console page.

### L5 — declarative context-length pricing tiers (mechanism, default no-op)

**Why.** Pricing is flat by model name: `ratio_setting` maps are `map[string]float64`
(`cache_ratio.go:117-183` pattern), and `helper.ModelPriceHelper(c, info, promptTokens, meta)`
(`internal/app/relay/helper/price.go:48`) receives `promptTokens` but uses it only for the
pre-consume estimate (`:64-66`); grep `billingexpr|TieredBilling|tiered_expr` → 0. Upstream's
answer is an expression engine (~800 LOC of production code, `pkg/billingexpr` — the "~5.2k"
in the dive was corrected); newhub's need (per-provider >N-token context surcharges) is met by
a declarative tier list on the existing option pattern with no interpreter. The platform
wallet only ever sees the resulting int (`app.PreConsumeQuota(c, preConsumedQuota int, …)`
called at `relay.go:360` with `priceData.QuotaToPreConsume`; `identity.proto`
`WalletOperationRequest` carries amount/type/product only), so this is gateway-internal.
Sequenced after cycle 7 L1 so it rides the transaction+CAS+audit write path instead of
inventing a second (the dive's "in-flight" claim was corrected: L1 is in the working tree,
not on `main`, which is why this lane is planned as a rebase, not a piggyback).

**Matrix ids.** billing-pricing-14 (tiered billing mode, missing, value 5) delivered
declaratively; billing-pricing-16/28/29 stay excluded (§5).

**Spec.**
- `internal/pkg/setting/ratio_setting/context_tiers.go`: `type ContextTier struct{
  ThresholdTokens int; ModelRatio, CompletionRatio, CacheRatio *float64 }`; package map
  `map[string][]ContextTier` under the same RWMutex discipline as `cache_ratio.go`;
  `GetContextLengthTier(model string, promptTokens int) *ContextTier` (tiers sorted ascending,
  return the highest `ThresholdTokens <= promptTokens`, `nil` when no list or none qualify);
  `ContextLengthTiers2JSONString()`, `UpdateContextLengthTiersByJSONString(s) error`
  (validation: thresholds strictly ascending, ≥0, ratios >0 when present, ≤8 tiers/model;
  invalid input leaves memory untouched) calling `InvalidateExposedDataCache()`; default empty.
- `repo/option.go`: `common.OptionMap["ContextLengthTiers"]` beside `:99` (HEAD numbering) and
  a `case "ContextLengthTiers":` beside `:436`; L1's `UpdateOptionTx` is reused for the DB
  write.
- `price.go`, `!usePrice` branch only, after `cacheCreationRatio1h` (`:82`) and before `ratio :=
  modelRatio * groupRatioInfo.GroupRatio` (`:95`): `if t := ratio_setting.GetContextLengthTier(
  info.OriginModelName, promptTokens); t != nil { override the non-nil fields }`. Per-call-priced
  models (`usePrice`) are untouched. `PriceData` gains `ContextTierThreshold int` (0 = none)
  for `ToSetting()`/debug only — **no new `other.*` key** (the log projection gate); the
  logged `model_ratio` already reflects the override. Retry needs no change: `getChannel`
  (`relay.go:587-613`) re-runs only the group ratio because `OriginModelName` and
  `promptTokens` are invariant across retries — `relay.go` stays untouched (§4).
- Admin write (post-L1 `v2_pricing_write.go`): `updatePricingRequest` gains `ContextTiers
  *[]contextTierPatch json:"context_tiers,omitempty"` (`{threshold_tokens, model_ratio?,
  completion_ratio?, cache_ratio?}`; an explicit empty list clears the model's tiers);
  validated in `validatePricingBatch`; persisted as a fifth `UpdateOptionTx(tx,
  "ContextLengthTiers", …)` inside L1's transaction and covered by the same `PricingVersion`
  CAS and `ActionPricingUpdated` row (diff entries `field:"context_tiers"`);
  `PreviewPricingV2` includes it; `GET …/pricing` (`v2_pricing.go`) exposes `context_tiers`
  per model. Console `web/src/pages/v2/Pricing/index.jsx`: per-model tier editor rows.

**Files.** `internal/pkg/setting/ratio_setting/context_tiers.go` (+ test, new),
`internal/app/relay/helper/price.go`, `internal/app/relay/helper/price_test.go`,
`internal/pkg/types/price_data.go`, `internal/adapter/repo/option.go` (+ test),
`internal/adapter/handler/v2_pricing_write.go` (+ test), `internal/adapter/handler/v2_pricing.go`
(+ test), `web/src/pages/v2/Pricing/index.jsx`, `web/src/pages/v2/Pricing/index.test.jsx`,
`web/src/i18n/locales/{en,zh}.json`.

**Tests.** `TestGetContextLengthTier_EmptyIsNil`, `_BoundaryAtThreshold` (exactly
`threshold`, `threshold-1`, `threshold+1`), `_HighestQualifyingWins`;
`TestUpdateContextLengthTiers_RejectsNonAscending`, `_RejectsNegativeRatio`,
`_InvalidLeavesMemoryUntouched`; `TestModelPriceHelper_NoTiers_ByteIdenticalToFlat` (golden
`PriceData` captured before the change); `TestModelPriceHelper_TierOverridesModelRatio`
(short vs long prompt, same model → different `QuotaToPreConsume`, ratio ×2);
`TestModelPriceHelper_PerCallModelIgnoresTiers`;
`TestV2PricingWrite_ContextTiers_PersistedInSameTx` and `_RollsBackWithBatch` (extends L1's
partial-failure oracle); `TestV2PricingWrite_ContextTiers_AuditDiff`. Mutation: change `<=` to
`<` in the tier selection → boundary test red; drop the fifth `UpdateOptionTx` → persisted test
red.

**UAT probe.** As root, `POST /api/v2/lurus/pricing` with `If-Match-Pricing-Version` and
`{"model_name":"deepseek-chat","context_tiers":[{"threshold_tokens":0,"model_ratio":<r>},
{"threshold_tokens":3000,"model_ratio":<2r>}]}`. Two relay calls with the UAT key: a one-line
prompt and a ~20 KB fixed-prefix prompt (the 2026-09-01 recipe at
`business-acceptance-tests.md:150-153` produces ~3.5k prompt tokens). Read the two consume rows
via the admin log API: `prompt_tokens` and `quota` show the long call charged at ~2× the short
call's per-prompt-token rate (`quota / prompt_tokens` ratio ≈ 2, completion normalised out by
`max_tokens:1`), and the admin-visible `other.model_ratio` is `<r>` vs `<2r>`. Then clear the
tiers (`"context_tiers":[]`) and repeat the long call → back to `<r>`. A flat-ratio bug makes
the two rows identical, which is the failure this probe is designed to expose. Credit-pool
top-up first (e2e drains it).

**Migration.** None (option row). **Platform change.** None.

**Risk.** Medium — this is a money computation. The empty-tiers byte-identical golden test is
the merge gate; real thresholds/ratios for actual vendors are populated only after finance
sign-off (O6); the mechanism ships with every model's list empty.

**Consumer-visible changes.** None until a tier list is populated; then long-context calls on
tiered models cost more (documented per model on the pricing page).

### L6 — `POST /v1/responses/compact` pass-through with estimated-token billing

**Why.** `POST /v1/responses` exists (`router/relay-router.go:149-151` → `handler.Relay` →
`relay.ResponsesHelper`, `internal/app/relay/responses_handler.go:21`), `PreviousResponseID` is
already a pass-through field (`dto/openai_request.go:805`), and all 20 provider adaptors
implement `ConvertOpenAIResponsesRequest`. `/v1/responses/compact` does not exist (grep
`compact|Compaction` → 0; matrix wire-formats-03). It is the stateless half of the large
stateful-Responses item: one request, no table, no ownership surface — a bad deploy fails loud
on the next call. Judge finding "UAT-unprovable with a DeepSeek-only channel" is answered by
splitting the artefact: the *gate* and the *routing* are provable on UAT today; the *billed
consume row* needs the owner's real OpenAI channel (O3, shared with L7) and is marked
`⏳ 待验证` until it exists.

**Matrix ids.** wire-formats-03 (missing, value 4).

**Spec.**
- `types.RelayFormatOpenAIResponsesCompact = "openai_responses_compact"`;
  `relayconstant.RelayModeResponsesCompact`; `helper/valid_request.go` case;
  `middleware/wire_format.go:34-43 relayFormatForPath` keeps answering OpenAI for the path
  (the compact endpoint speaks the OpenAI wire; add the path to `relay_wire_stamp_test.go`).
- `dto.OpenAIResponsesCompactionRequest` (new file): `Model, Input, Instructions,
  PreviousResponseID, Tools, ParallelToolCalls, Reasoning, ServiceTier, PromptCacheKey,
  PromptCacheRetention, Text` — the documented subset, mirroring the existing field types in
  `openai_request.go:797-822`; **no** `Background`/`Conversation`/`ContextManagement`/`Stream`
  (unknown fields are dropped on unmarshal, matching the plain endpoint's behaviour);
  implements `dto.Request` (`GetTokenCountMeta` counts `Input`+`Instructions` like the parent).
  `dto.OpenAIResponsesCompactionResponse`: `id, object, model, output, usage{input_tokens,
  output_tokens,total_tokens}` — only `usage` is read; bytes are forwarded verbatim.
- Route `httpRouter.POST("/responses/compact", …)` next to `:149` with the identical chain
  (`TokenAuth → PoolBalanceCheck → CostSpikeLimit → EntitlementCheck → rate limits →
  Distribute → BusinessModelRateLimit`); `relay_info.go` gains `GenRelayInfoResponsesCompact`
  beside `:381`; `relayHandler` (`relay.go:56-77`) dispatches `RelayModeResponsesCompact` to
  `ResponsesHelper`.
- In `ResponsesHelper`: on the compact mode, `common.SupportsResponsesCompact(channelType)`
  (new, `internal/pkg/common/api_type.go`; this cycle `{constant.ChannelTypeOpenAI}` — the only
  newhub type among upstream's five, the others do not exist here per cycle 7 §5) must be
  true, else return `types.NewErrorWithStatusCode(…, types.ErrorCodeResponsesCompactUnsupported,
  400, ErrOptionWithSkipRetry())` (new code, added to `relay.json`'s enum). Convert the compact
  DTO to a full `OpenAIResponsesRequest` carrying only the subset, then the existing
  convert/override/`DoRequest` path (`:52-82`); the openai adaptor's `GetRequestURL` default
  branch (`openai/adaptor.go:172-176`) already yields `<base>/v1/responses/compact` from
  `RequestURLPath` (`relay_info.go:539`) — the Azure branch (`:139-142`) returns unsupported.
  `DoResponse` (`openai/adaptor.go:602` switch) gets a `RelayModeResponsesCompact` case →
  `OaiResponsesCompactHandler` in `openai/relay_responses.go`: copy status+body verbatim, parse
  `usage`; if absent, `usage.PromptTokens = info.GetEstimatePromptTokens()`
  (`relay_info.go:615`), completion 0. Billing: unchanged `postConsumeQuota` (`:112-114`) —
  `ModelPriceHelper` was already called at `relay.go:349` with the estimated tokens; no new
  billing path, no snapshot/restore trick needed because the price data is computed once per
  request here.

**Files.** `internal/pkg/types/relay_format.go`, `internal/adapter/provider/constant/relay_mode.go`,
`internal/pkg/types/error_code.go` (new code), `internal/pkg/dto/openai_responses_compaction.go`
(+ test, new), `internal/app/relay/helper/valid_request.go`,
`internal/adapter/provider/common/relay_info.go`, `internal/app/relay/responses_handler.go`
(+ test), `internal/adapter/handler/relay.go` (dispatch case only — see §4),
`internal/adapter/provider/openai/adaptor.go`, `internal/adapter/provider/openai/relay_responses.go`
(+ test), `internal/pkg/common/api_type.go` (+ test),
`internal/adapter/handler/router/relay-router.go`,
`internal/adapter/handler/router/relay_wire_stamp_test.go`,
`internal/adapter/handler/router/openapi_contract_lock_test.go` (no change expected; it must
stay green), `docs/openapi/relay.json` (sibling entry after `:500`; the dive's "0 hits" was
corrected — `/v1/responses` is documented, `/compact` is not).

**Tests.** `TestCompactionRequest_DropsUnsupportedFields` (a body with `background`,
`conversation`, `stream` round-trips without them); `TestSupportsResponsesCompact_Table`
(OpenAI true; Azure, Anthropic, DeepSeek-typed false); `TestResponsesHelper_CompactGate400`
(unsupported channel type → 400 `responses_compact_unsupported`, skip-retry, **no upstream
call** — httptest upstream asserts zero hits); `TestResponsesHelper_CompactForwardsSubset`
(httptest upstream receives `POST /v1/responses/compact` with exactly the subset keys);
`TestOaiResponsesCompactHandler_UsageParsedElseEstimated`;
`TestRelayRouter_ResponsesCompactMounted` (chain identical to `/responses`);
`TestOpenAPIContract_*` green with the new path and code. Mutation: remove the gate → gate
test red; forward the raw body instead of the subset → subset test red.

**UAT probe.** (a) Gate, vendor-independent: as root create a UAT channel of type 14
(Anthropic wire) with a dummy key serving a synthetic model name `compact-gate-probe`; `POST
https://test-newhub.lurus.cn/v1/responses/compact {"model":"compact-gate-probe","input":"x"}`
→ **400 `responses_compact_unsupported`**, and (ERROR_LOG_ENABLED) an error row (`type=5`)
for that model with no upstream latency. (b) Routing, DeepSeek channel: the same call with
`"model":"deepseek-chat"` → the vendor's own 404 passed through in the OpenAI error envelope
(pre-consume refunded), proving the route reached `<base>/v1/responses/compact`. (c) Billed
row: with the owner's real OpenAI channel (O3) the same call returns 200 and a consume row
with `relay_mode` = the compact mode and `quota > 0` derived from `prompt_tokens`. (a)+(b)
are the cycle-8 acceptance artefacts; (c) is `⏳ 待验证` until O3.

**Migration.** None. **Platform change.** None.

**Risk.** Medium, spread across packages rather than deep. The subset restriction is the
safety property (a smuggled `background:true` must not reach the vendor); the gate must fire
before `DoRequest` (tested by the zero-hit upstream).

**Consumer-visible changes.** New endpoint; new error code `responses_compact_unsupported`;
documented in `relay.json` and the integration guide.

### L7 — stateful `GET`/`DELETE /v1/responses/:response_id` pinned to the originating channel

**Why.** The largest genuine gap in the theme (tasks-plugins-12, value 5) and a newhub-only
capability for real provider channels (upstream's retrieve exists only inside its excluded
task-plugin protocol). Sequenced last: highest blast radius (new table, ownership check,
channel pinning), and it reuses L3's heartbeat for its sweep. The stateful-responses
correction is binding: pinning a channel is **not** the four-line context set at
`distributor.go:480-484` — key selection (`channel.GetNextEnabledKey()` at `:495`, key/base
URL at `:507-508`) lives in the same function, so the handler calls the exported
`middleware.SetupContextForSelectedChannel(c, channel, model)` (`:475`) in full. Upstream
request method already propagates (`provider/api_request.go:77,108,334` use
`c.Request.Method`), and the URL is generic (`RequestURLPath` at `relay_info.go:539` →
`openai/adaptor.go:172-176`), so GET/DELETE need no URL code.

**Matrix ids.** tasks-plugins-12 (Responses retrieve/delete, missing, value 5).

**Spec.**
- **Migration 035 `create_response_registry`** (reserved after 034): `response_registry(
  response_id varchar(128) PK, tenant_id varchar(36) NOT NULL, user_id bigint NOT NULL,
  token_id bigint NOT NULL, channel_id bigint NOT NULL, upstream_model varchar(128) NOT NULL,
  created_at bigint NOT NULL, expires_at bigint NOT NULL)`; indexes on `expires_at` and
  `(tenant_id, user_id)`. Entity registered in `repo.migrateDB` (032 dual pattern).
- **Insert hook.** `OaiResponsesHandler` / the stream handler in `openai/relay_responses.go`
  already parse the body for usage; they additionally stash the response `id`
  (`dto.OpenAIResponsesResponse.ID`, `openai_response.go:366`; for streams the
  `response.completed` event's `response.id`) with `c.Set("responses_id", id)`. In
  `ResponsesHelper` after `postConsumeQuota` (`:112-114`), if the request's `Store` raw value
  is not the literal `false` and an id was stashed: `repo.UpsertResponseRegistry(row)` with
  `expires_at = now + RESPONSE_REGISTRY_TTL_DAYS*86400` (env, default 30). Only for
  `SupportsResponsesCompact`-class channels (type OpenAI) — the vendors that persist state.
- **Routes.** New `responsesStateRouter := router.Group("/v1/responses");
  responsesStateRouter.Use(middleware.StampRelayFormat(), middleware.TokenAuth(),
  middleware.CriticalRateLimit())` — the `modelsRouter` precedent (`relay-router.go:18-19`,
  TokenAuth-only `/v1` group), added after the existing groups without touching the
  `relayV1Router` chain at `:100-121`; `GET "/:response_id"` and `DELETE "/:response_id"` →
  `handler.RelayResponsesRetrieve` / `RelayResponsesDelete` (new
  `handler/relay_responses_registry.go`). No `Distribute()`, no body parsing.
- **Handler.** `row := repo.GetResponseRegistry(id)` → 404 `response_not_found` if absent;
  ownership: `row.tenant_id == tenant_context.TenantID && row.user_id == c.GetInt("id")`
  (fail-closed default; O7 may widen to tenant) → same 404 on mismatch, never 403; record
  `governance.ActionResponseDenied` with `{response_id, requester_user_id}` on mismatch;
  `repo.GetChannelById(row.ChannelId, true)` (`repo/channel.go:411`) → 404 if missing or
  `Status != Enabled`; `middleware.SetupContextForSelectedChannel(c, channel,
  row.UpstreamModel)`; `info, _ := relaycommon.GenRelayInfo(c, types.RelayFormatOpenAIResponses,
  &dto.OpenAIResponsesRequest{Model: row.UpstreamModel}, nil)`; `info.RelayMode =
  RelayModeResponsesRetrieve|Delete` (new constants); `adaptor := relay.GetAdaptor(
  info.ApiType); adaptor.Init(info); resp := adaptor.DoRequest(c, info, nil)`; copy status,
  `Content-Type` and body verbatim (`io.Copy`) — no usage parsing, no pre/post-consume
  (GET/DELETE are unbilled by the vendor); on DELETE 2xx `repo.DeleteResponseRegistry(id)`.
  Audit `ActionResponseRetrieved`/`ActionResponseDeleted` (`ResourceResponse = "response"`)
  with `{response_id, channel_id, upstream_status}`. `DoResponse`'s `:602` switch gets the two
  modes routed to a byte-passthrough (or the handler bypasses `DoResponse` entirely — the
  implementer picks one and tests it).
- **Sweep.** `lifecycle.NewLeaderTask("response-registry-sweep", 1*time.Hour, fn)` deleting
  `expires_at < now` in batches of 1000; registered in L3's `taskreg`; started from `main.go`
  beside the other leader tasks.

**Files.** `migrations/035_create_response_registry.sql` (new),
`internal/domain/entity/response_registry.go` (new), `internal/adapter/repo/main.go`,
`internal/adapter/repo/response_registry.go` (+ test, new),
`internal/adapter/provider/openai/relay_responses.go` (+ test: id stash),
`internal/app/relay/responses_handler.go` (+ test: insert hook, `store=false`),
`internal/adapter/provider/constant/relay_mode.go`, `internal/pkg/types/error_code.go`,
`internal/adapter/handler/relay_responses_registry.go` (+ test, new),
`internal/adapter/handler/router/relay-router.go` (new group only),
`internal/lifecycle/response_registry_sweep.go` (+ test, new), `cmd/server/main.go`,
`internal/app/governance/audit_action.go` (+ test), `docs/openapi/relay.json`,
root `doc/coord/migration-ledger.md` (035), `.env.example` (`RESPONSE_REGISTRY_TTL_DAYS`).

**Tests.** `TestResponseRegistryRepo_UpsertGetDelete`;
`TestResponsesHelper_InsertsRegistryOnSuccess`, `_StoreFalseSkipsInsert`,
`_NonOpenAIChannelSkipsInsert`; `TestRelayResponsesRetrieve_SameUserPinsChannel` (httptest
upstream asserts method `GET`, path `/v1/responses/<id>`, and the pinned channel's key in
`Authorization` — the pin is proved by the key, not by a context value);
`_OtherUserSameTenant404`, `_OtherTenant404`, `_Absent404` (all three bodies byte-identical);
`_ChannelDisabled404`; `_NoQuotaRowWritten`; `TestRelayResponsesDelete_RemovesRowOn2xx`,
`_KeepsRowOnUpstreamError`; `TestResponseRegistrySweep_ExpiredOnly` (fake clock) and
`_StampsHeartbeat` (L3 pattern); `TestRelayRouter_ResponsesStateGroup_NoDistribute`;
`TestOpenAPIContract_*` green. Mutation: drop the `user_id` clause → `_OtherUserSameTenant404`
red; replace `SetupContextForSelectedChannel` with the four `SetContextKey` lines → the
Authorization assertion red (no key selected).

**UAT probe.** (a) Vendor-independent, today: `psql` on `newhub_uat` inserts a row
`(response_id='resp_probe_1', tenant_id='lurus', user_id=<A>, token_id=<A's token>,
channel_id=<DeepSeek channel id>, upstream_model='deepseek-chat', expires_at=now+1d)`.
`GET https://test-newhub.lurus.cn/v1/responses/resp_probe_1` with token A → the vendor's own
404 body passed through (DeepSeek has no such endpoint) **and** an audit row
`relay.response_retrieved` with `channel_id=<seeded id>` and `upstream_status=404` — the
registry, not weighted selection, chose the channel (no `Distribute` ran; the request carries
no `model`). Same GET with token B (another UAT user) → newhub's own 404
`response_not_found` envelope, identical to `GET /v1/responses/resp_does_not_exist` with
token B, and an audit row `relay.response_denied` — ownership enforced without revealing
existence. `DELETE` with token A → vendor 404 passed through, row kept (2xx only deletes).
(b) With the owner's real OpenAI channel (O3): `POST /v1/responses` → capture `id`; `GET` →
200, body `id` equal; audit `channel_id` equals the POST's; `DELETE` → 200 and the row is gone
(direct table check); second `GET` → 404. (a) is the cycle-8 artefact; (b) is `⏳ 待验证`
until O3.

**Migration.** 035 as specified. **Platform change.** None.

**Risk.** High relative to the cycle. Ownership scope is a security default and is decided
(O7) before the query is written; channel pinning must reuse the full selection path (tested
by the key assertion); unbounded growth is bounded by the sweep, which reports its own
heartbeat through L3 so a stalled sweep is visible.

**Consumer-visible changes.** Two new `/v1` routes; new error code `response_not_found`; new
audit actions; POST `/v1/responses` on OpenAI-type channels now records a registry row unless
`store:false`.

## 4. Do-not-regress

Cycle-6 §9 items and cycle-7 §4 items these lanes touch, plus newhub-only capabilities.

| Item | Lane that touches it | Re-check |
|---|---|---|
| "no lane adds an `other.*` key"; default-deny projection gate `log_other_projection_lock_test.go` | L5 adds `PriceData.ContextTierThreshold` **not** projected to `other`; L7 writes audit rows, no log `other` | `TestOtherProjectionIsFullyClassified`, `TestInternalOtherKeys_NoPublicField` |
| "no lane touches `relay.go`" (retry gate, breaker `:491`, headroom, retry re-selection `:587-613`) | L5 none (tier is retry-invariant, proved by design note); L6 adds one `case` to `relayHandler` `:56-77` only | `git diff relay.go` confined to that switch; `TestRelay*` green; `-race` |
| Relay gate order `relay-router.go:100-121`, wire-format stamping | L6 adds a route inside `httpRouter` with the inherited chain; L7 adds a **new** group after the existing ones | `relay_wire_stamp_test.go`, `r6a_rate_limit_mount_test.go` green; `:100-121` unchanged in diff |
| `channel_cache.go`, `smart_routing.go`, `channel_scorer.go`, `channel_select.go`, `session_affinity.go` untouched | none (the architect's path allowlist and regex rules are rejected, §5) | unchanged |
| Structural gates: abort code on every site; declared series written; OpenAPI enum/paths locked | L1 (429 via existing middleware), L3 (series), L4/L6/L7 (codes, paths) | `abort_code_structural_test`, `TestDeclaredSeriesHaveAProductionWriter`, `openapi_contract_lock_test` |
| Every by-id v2 mutation classified in `v2_completeness_test.go` | L4 (`DELETE …/authz/grants/:id`) | `TestV2IDOR_Completeness` |
| Tenant decision is a compile-time argument for cross-user queries | L4 grant lookup takes `user_id` positionally; L7 ownership compares tenant **and** user | `TestRelayResponsesRetrieve_OtherTenant404`, `_OtherUserSameTenant404` |
| Money-path invariants: `PreConsumeQuota`/`PostConsumeQuota`/`quota.go` untouched; credit-pool gate and cost-spike breaker on every billed route | L5 changes only the pre-consume input inside `ModelPriceHelper`; L6 bills through the unchanged `postConsumeQuota`; L7 never bills | `TestModelPriceHelper_NoTiers_ByteIdenticalToFlat`; `_NoQuotaRowWritten`; matrix invariants tests |
| Hash-chained audit trail: rows only through `RecordAuditEvent`, chain untouched | L2 (reused action), L4, L7 (new actions) | `VerifyAuditChainV2` green on UAT after each lane |
| `/metrics` double-guarded | L3 adds series only | `metricsAuthMiddleware` test |
| `authHelper` per-request status/role re-validation (`auth.go:42-49`, PR #167) | L4 calls `authHelper` first, never bypasses it | `TestRootOrGranted_SessionAdminNoGrant403` |
| Cycle-7 L1 transaction + CAS + audit for pricing writes; `UpdateOptionTx` never applies memory | L5 adds a fifth persist inside the same tx | `TestV2PricingWrite_PartialBatchFailure_RollsBackEarlierFields` extended |
| Cycle-7 L2 fail-closed admin audit; `TestAdminWriteRoutesAreAudited` | L4 grant writes audit explicitly (not the fallback) | that test names no new route |
| Cycle-7 L5 `__lurus_*` param-override keys and affinity purge | none | unchanged |
| Cycle-7 L7 session registry flag default off | L4's middleware runs after `authHelper`, which L7 extended | `TestAuthHelper_FlagOff_NoRegistryWrites` |
| `TestProvisionV2_*` idempotency/expiry/revocation suite (`v2_provision_test.go:113-544`) | L2 | whole file green; `_RevokedTokenNotResurrected` unchanged |
| Migration ledger contiguity (`TestEmbeddedFS_VersionsAreContiguous`) | L4 (034) before L7 (035) | ledger diff precedes each SQL; L7 never lands before L4 |
| `r5c_status_capability_test.go` passkey-absence lock | none (no status-bit lane this cycle) | unchanged |
| Multi-key cooldown reaper (newhub-only, `openrouter_pool/reaper.go`) semantics | L3 adds a stamp after `ReapOnce` only | `reaper_test.go` green |

Newhub-only capabilities brushed and kept: tenant credit-pool gate and cost-spike breaker on
every billed route (L6's route inherits both; L7's routes are unbilled and carry
`CriticalRateLimit`); hash-chained audit; `SecureVerificationRequired` step-up on key reveal
(untouched); tenant-scoped analytics (untouched); IDOR-hardened request-id lookup (untouched);
session-affinity re-pin (untouched); wire-format stamping and native error envelopes (L6/L7 add
codes through the existing enum lock); per-tenant channel isolation (`Channel.TenantId`,
untouched).

## 5. Deliberately not doing (with the judge finding or evidence)

- **Channel `write` vs `sensitive_write` split (buyer lane 5, architect lane 3 part b;
  providers-channels-34).** Deferred to cycle 9 with L4's table already carrying
  `resource`/`action`/`tenant_id`. Two verified reasons: (1) v1 `PUT /api/channel/`
  (`router/api-router.go:149` → `handler/channel.go:1233 UpdateChannel`) binds the whole channel
  including `Key` under `AdminAuth`, so a v2-only gate is bypassable — the lane must gate v1 and
  v2 symmetrically, which is a larger change than either plan scoped; (2) the buyer's
  "silently strip Key/BaseURL and answer 200" was judged a defect (a key rotation that reports
  success and does nothing); the correct shape is 403 `PERMISSION_DENIED`, which needs O5
  (tenant-scoped grant semantics) first.
- **Passkey/WebAuthn (architect lane 2; passkey-idp dive lane 2).** The correction stands and is
  adopted: Zitadel under `lurus-login` ships native passwordless and a live vendored `/passkey`
  route; the only reason it is inert is `2l-svc-platform/internal/pkg/zitadel/idps.go:297`
  setting `PASSWORDLESS_TYPE_NOT_ALLOWED` for every org from `ensureCustomLoginPolicy`
  (`:217`). That makes it a config flip + verification on the single shared platform instance
  that also backs production SSO — not provable on UAT (SSO off by design) and not
  production-read-only. Carried as **O8'** (§6), not a lane. The `login_methods.sso.mfa_available`
  / `passkey_available` status bits ride with that decision (they carry no runtime signal until
  the flip).
- **Regex affinity rules and per-channel request-path allowlist (architect lane 6;
  channel-constraints dive lanes 1/3).** Rejected by cycle 7 §5 ("Architect B regex affinity
  rules") and both judges: it puts regex on `channel_select.go`/`channel_cache.go`, both on the
  untouched list; it depends on cycle-7 L5's `v2_admin_routing.go` which is not on `main`; and
  the request-path matrix has no consumer without a plugin channel type (providers-channels-13
  "needs_migration yes" in the matrix). Channel pin header (routing-resilience-limits-33) and
  TLS-verify toggle (routing-resilience-limits-18, O9 in cycle 7: never without sign-off; the
  "SMTPInsecureSkipVerify precedent" was corrected — `common/email.go:46-47` is a hard-coded
  `InsecureSkipVerify: true`, itself a dormant finding for the owner) stay out.
- **Platform payment-compliance confirmation gate (buyer lane 6).** Fatal per judge 1:
  providers are registered at boot from env (`2l-svc-platform/cmd/core/main.go:967-975`), so
  the "provider activation write path" to gate does not exist; the only gate point is
  `CreateCheckout` on the shared production platform. Revisit when merchant credentials land
  (owner item).
- **Public payment-options proxy and `?reason=` credit-pool filter (payments dive lanes 2/3).**
  Real but S-sized and tied to the OIDC-off `platformUser` group finding
  (`api-v2-router.go:339-340` → `OIDCAuth()` 503s the whole `/api/v2/user/*` subtree on UAT);
  the structural fix (session fallback in `OIDCAuth`) is an owner call because it widens an
  auth path. Listed as **O9**.
- **Newhub-local `SubscriptionPlan`/`UserSubscription` catalogue** (rows 11/12/16 as tables) —
  cycle 7 resolved O6 as "built on platform entitlements"; L2 is that resolution.
- **Per-channel price-ratio override (architect lane 5 part b, tiered-billing dive lane 2).**
  Under-scoped per judge 1: it needs a novel refresh-on-retry seam at `relay.go:613`
  (touching the retry re-selection on the untouched list) and would put `PriceData` mutation in
  `getChannel`. Deferred; L5 lands the tier mechanism alone.
- **Grok/xAI violation fee (billing-pricing-19)** — value 1, no trigger in traffic, policy
  question (O10).
- **Upstream JS billing-expression engine (billing-pricing-14/16/28/29 as specified)** — L5
  covers the practical outcome declaratively; no interpreter surface.
- **Background mode for Responses** — upstream itself does not offer it for real channels
  (`Background` commented out upstream); out of "no fewer features" scope.
- **HTTP/2 connection sharding (routing-resilience-limits-14)** — no demonstrated per-origin
  contention; revisit after cycle-7 L5 ships.
- **zh-TW locale, `MultiKeyModeRotating`, task-artefact listing, plugin runtime, disk-cache /
  GC / instance registry** — unchanged from cycle 7 §5.
- **Wrapping `openrouter_sync/scheduler.go` in a heartbeat** — already observable
  (`GET /api/openrouter-sync/last-status`); would be additive noise.
- **`TopupRateLimit` mounting** — its consumer `TopUpV2` is unrouted by owner decision
  (`api-v2-router.go:270`); L1 leaves it with a comment.

## 6. Owner decisions

- **O1 (L4/L7 sequencing).** Reserve migration **034 `create_admin_permission_grants`** and
  **035 `create_response_registry`** in root `doc/coord/migration-ledger.md:134-135` in that
  order before any SQL is written; confirm no parallel reservation (cycle 7 O2 chose lazy
  AutoMigrate for the backup-code table — this plan uses numbered migrations because 034 needs a
  partial unique index GORM cannot express and 035 is a relay-path table that should show in
  `schema_migrations`/`lurus_gateway_schema_migrations_pending`).
- **O2 (L2 probe).** Authorise one wallet-funded subscription for a dedicated test account on
  the shared platform (`POST /admin/v1/accounts/:id/wallet/credit` → wallet checkout of the
  cheapest `cc_` plan) so UAT can obtain a real entitlement token; alternatively provide an
  https-hosted UAT JWKS and set `PLATFORM_JWKS_URL` in `deploy/k8s/r6-uat/deployment.yaml`.
  Without one of these the L2 UAT artefact cannot exist.
- **O3 (L6/L7 probes, L2 leg 3).** Provision one real OpenAI-account channel on UAT (type 1,
  `api.openai.com`, cheapest model, spend cap) — the only vendor that serves
  `/v1/responses/compact` and retains Responses state; the vendor-independent artefacts in L6(a),
  L6(b), L7(a) ship without it, the end-to-end ones stay `⏳ 待验证`.
- **O4 (L2 policy).** Confirm one live `switch-provision-*` token per user is the intended
  invariant (sibling revocation on every verify); if concurrent plans per account are a product
  case, revocation must key on a platform-supplied "superseded plan" claim instead.
- **O5 (L4 scope).** Semantics of `tenant_id` on grants for cycle 9: tenant-admin-issued,
  tenant-scoped grants (for channel `sensitive_write`) vs root-only global grants. This cycle
  accepts only `NULL` and only `audit:read`.
- **O6 (L5 values).** Real context-length thresholds and ratios per vendor model require
  finance sign-off before any live model's list is non-empty; the mechanism ships empty.
- **O7 (L7 ownership).** Default is same tenant **and** same user (token rotation keeps
  access; another user in the tenant does not). Widening to tenant-wide must be decided before
  the query is written and is a one-line change plus the `_OtherUserSameTenant404` test flip.
- **O8' (passkey, reframed).** Approve or decline flipping `passwordlessType` for the newhub
  org in `ensureCustomLoginPolicy` (`idps.go:297`) and name where the passkey-only login is
  proven (UAT runs SSO off). Until decided, no `passkey_available`/`mfa_available` status bit
  is added (they would advertise a capability users cannot reach).
- **O9 (`/api/v2/user/*` on UAT).** `OIDCAuth()` 503s unconditionally when OIDC is off
  (`middleware/oidc_auth.go`), so every billing/identity route is unreachable on UAT; adding a
  session fallback widens an auth path and is an owner call. Blocks the two small payments
  items in §5.
- **O10.** Violation-fee policy (charge vs ban) — unchanged from the dive; no lane until
  answered.

## 7. Verification protocol

**Local gates (every lane, before push).**
```
go vet ./... && go build ./...
go test -short -p 2 ./...                      # hermetic tier (-p 2: OOM guard on this machine)
go test -run 'TestV2IDOR_Completeness|TestConsoleCallsResolveToRegisteredRoutes|TestAdminWriteRoutesAreAudited|TestOtherProjectionIsFullyClassified|TestDeclaredSeriesHaveAProductionWriter|TestAllAuditActions_|TestOpenAPIContract_|TestAbortWithOpenAiMessage_EveryCallSiteCarriesACode|TestEmbeddedFS_VersionsAreContiguous' ./...
cd web && bun run lint && bun run eslint && bun test src/i18n/i18n-integrity.test.js && bun run build
```
CI is the only oracle for `-race` and the coverage ratchet; a lane is not "green" until the PR's
CI run is linked in the lane report. `-run` filters must match >0 tests (record the count).

**Mutation checks (each lane shows the named test red, then green; commit before mutating,
never `git checkout --` over uncommitted work).**
L1 drop `RedemptionRateLimit()` from one route → `TestRedemptionRoutes_RateLimitMounted` names
it. L2 remove the `ModelLimitsEnabled` assignment → `_ModelsClaim_SetsModelLimits`; remove
`name <> ?` → `_SamePlanReplay_DoesNotSelfDisable`. L3 delete one `RecordLeaderTaskSuccess` →
that job's `_SuccessfulTickStampsHeartbeat`. L4 comment out the grant lookup →
`_SessionAdminWithGrant200`; move the audit GETs back under `adminRoute` →
`TestAuditRoutes_MountedUnderRootOrGranted`. L5 `<=`→`<` → `_BoundaryAtThreshold`; drop the
fifth `UpdateOptionTx` → `_ContextTiers_PersistedInSameTx`. L6 remove the gate →
`TestResponsesHelper_CompactGate400` (upstream hit count 1); forward the raw body →
`_CompactForwardsSubset`. L7 drop the `user_id` clause → `_OtherUserSameTenant404`; replace
`SetupContextForSelectedChannel` with the four context sets → `_SameUserPinsChannel`
(missing `Authorization`).

**UAT probes (per lane, §3).** Preconditions: bridge token from the UAT secret; credit pool
topped up before relay probes; digest recorded before and after (ArgoCD may converge
mid-probe — compare pod `startTime` with artefact timestamps); `VerifyAuditChainV2` green after
every lane that writes audit rows. Owner preconditions: O2 before L2's probe, O3 before L6(c)/
L7(b). Each artefact is copied verbatim into the lane report with the command that produced it;
a probe that cannot produce its artefact marks the lane `⏳ 待验证`, never PASS.

**Order of landing.** Branch from the cycle-7 merge commit. L1 → L2 → L3 → L4 → L5 → L6 → L7,
one commit per lane, one PR (cycle-6/7 shape) or one PR per lane if the owner prefers smaller
review units; `main` only, ArgoCD auto-pin. L4 waits for O1 (034), L7 for O1 (035) and O7; L5
rebases onto cycle-7 L1's `v2_pricing_write.go`/`option.go`/`audit_action.go` hunks; L4 and L7
add to `audit_action.go` after cycle-7 L1/L2's additions. Files touched by more than one lane
(`api-v2-router.go`: L1/L3/L4; `relay-router.go`: L6/L7; `audit_action.go`: L4/L7;
`relay_mode.go`/`error_code.go`/`relay.json`: L6/L7; `taskreg`: L3/L7) are additive in every
later lane — no later lane edits an earlier lane's hunk.

## 8. Operator amendments after the refuter round (binding; override §3 where they conflict)

Fourteen refuter reports (two per lane) were checked against HEAD; the async-tasks theme, whose
skeptic stage failed in the planning run, was re-run separately and its lanes are added below.
Cycle 8 branches from the cycle-7 merge commit (PR #181), so every citation into
`v2_pricing_write.go`, `session_affinity.go`, `auth.go` etc. must be re-read there first.

### L1 — redemption rate limit
No structural defect. Baseline: an unknown code answers HTTP 200 `success:false` (not 400);
the probe's artefact is the 429 with `X-RateLimit-Scope: ip` after the limiter's budget.

### L2 — entitlement → ModelLimits gate
- **Sibling revocation must invalidate the token cache.** A blind `UPDATE tokens … SET status`
  leaves the warm Redis token entry authenticating relay traffic. Select the sibling rows and
  disable each through the existing `(*repo.Token).Update()` path (which invalidates the cache),
  never a bulk UPDATE. Lock: after provisioning plan B, a relay with token A's key through the
  real token-auth middleware (Redis-backed in the test) is rejected — DB row + audit row alone are
  not the oracle.
- Owner precondition O2 (a wallet-funded test subscription on the platform) stays; until it
  exists the lane is provable only with a seeded entitlement claim in the hermetic tier.

### L3 — background-task heartbeats
- The overdue formula must branch on `leader_only && !IsLeader()`: on the 3-replica production
  the two followers never stamp the leader-gated tasks, so `last_success_at == 0` there is
  "standby", not "overdue". The admin JSON and the console page show `standby` for those rows.
- The five tasks are raw ticker loops, not `NewLeaderTask` callbacks; the heartbeat call goes
  inside each loop's successful tick, and `Set(0)` at boot registers the series.

### L4 — permission grants
- **`RootOrGranted` cannot be layered on `authHelper` as written**: `authHelper` calls
  `c.Next()` itself on success and aborts on failure, so nothing after it can branch on the
  grant. Refactor first: split `authHelper` into `resolveSessionIdentity(c, minRole) (ok bool)`
  (sets `id`/`role`/tenant context, aborts with the existing responses on failure, does not call
  `c.Next()`) and keep `authHelper` as `if resolveSessionIdentity(...) { c.Next() }` so the
  existing middlewares are byte-for-byte unchanged in behaviour (lock: the middleware package's
  auth tests stay green). `RootOrGranted` = resolve → root passes → else
  `repo.HasActivePermissionGrant` → 403. The four audit GETs move to a sibling group under
  `RootOrGranted`; `/admin/tenants` and everything else stay under `RootJWTAuth`.
- Grants are global this cycle (no `tenant_id` scoping) — say so in the middleware doc and in
  the consumer notes; the 200→403 shape change for non-root callers without a grant is a
  consumer-visible change even though those callers were already rejected before.
- Migration **034 `create_admin_permission_grants`** must be reserved in the root ledger before
  SQL is written; the unique index is a partial index in SQL with a matching GORM tag, per the
  031 lesson.

### L5 — context-length pricing tiers
- **Settle on the actual prompt count.** The tier chosen at pre-consume time from the
  estimated prompt tokens must be re-evaluated at settlement with the upstream-reported
  `usage.PromptTokens` (in `compatible_handler.go` and the equivalent streaming/task settlement
  paths that read `PriceData.ModelRatio` for `!UsePrice` models); the log row's ratio and
  prompt_tokens must be on the same side of the threshold. Lock: estimate below / actual above a
  configured threshold → settled quota uses the higher tier.
- Persisted as a fifth `UpdateOptionTx` inside cycle-7 L1's transaction, with the diff/preview
  and `pricing.updated` details extended; real vendor numbers remain owner item O6 — the lane
  ships the mechanism with an empty default (no-op).

### L6 — `/v1/responses/compact`
- The pass-through branch (`PassThroughRequestEnabled`) in `ResponsesHelper` runs before the
  DTO conversion; the compact gate (field subset, no `background`/`stream`/`conversation`) must
  run **before** that branch so a smuggled field cannot reach the vendor in pass-through mode.
  Lock: pass-through on + `background:true` → 400 with zero upstream hits.
- `RelayModeResponsesCompact` is appended at the **end** of the `const (iota)` block in
  `relay_mode.go`, never inserted; add a test pinning the ordinals of the existing constants.
- `Path2RelayMode` needs an explicit `/v1/responses/compact` branch before the `/v1/responses`
  prefix match.

### L7 — response registry
- The retrieval handler must call `info.InitChannelMeta(c)` after `GenRelayInfo` (all existing
  call sites do) or `ApiType`/`ApiKey`/`ChannelBaseUrl` are nil.
- There is no `tenant_context` package: the ownership check reads
  `middleware.GetTenantContext(c)` and `c.GetInt("id")`; foreign or absent → identical 404.
- The registry insert after a successful `POST /v1/responses` is on the billed hot path: it
  must never fail the response — log + `lurus_gateway_response_registry_errors_total` on error,
  and it runs after `postConsumeQuota`. Migration **035** reserved after 034.
- `store:false` opts out; sweep via `NewLeaderTask` with the L3 heartbeat.

### Async generation tasks (theme re-run; matrix rows tasks-plugins-01/02/17/18/19/20/26)
Upstream has shipped its JS task-plugin protocol (86 files at HEAD 2026-09-12; the matrix's
"upstream 0 hits" wording for those rows is wrong and is corrected in the matrix header note),
but its artifact **store** is still a disabled stub — that half is a genuine upstream gap too.
newhub already has 11 compiled task adaptors (`provider/task/*`), pre-charge/true-up billing
and an ownership-checked video proxy; what is missing is the generic surface. Added lanes:
- **L8 — generic task surface + project scoping (S/M).** `POST /v1/tasks/:platform` and
  `GET /v1/tasks/:platform/:task_id` dispatching to the existing `provider.TaskAdaptor`
  (platform key = `constant.TaskPlatform`, no plugin registry); `entity.Task` gains `ProjectId`
  (same tag as `Token.ProjectId`, migration **036**, additive default 0) populated from the
  relay attribution; ownership fail-closed 404 mirroring `video_proxy.go`; the dedicated
  Kling/Jimeng/video routes stay as they are. UAT: a fault-simulator task vendor
  (`faultsim.go` gains a task endpoint that returns SUCCESS with a `data:` URL artefact) so the
  round trip is observable without a vendor key.
- **L9 — artefact listing + proxy generalisation (M).** `GET …/artifacts` projects
  `Task.Data`/`PrivateData` into `[]{key,type,mime_type,size,status}` (metadata only, zero
  schema); `GET …/artifacts/:key/content` reuses the video proxy's loop/self-URL guard, method
  allow-list and `data:` inliner for non-video types under the same ownership check (that 403
  test must stay red-on-revert).
- **L10 — task listing filter parity (S).** `upstream_task_id` / `request_id` filters on the
  admin and user task lists.
- Not planned: MinIO-backed artefact persistence (needs a real vendor pass to prove; behind a
  flag in a later cycle), plugin upload/marketplace/JS runtime (out of scope by design).

### Owner decisions — resolved under the blanket authorisation
O2 (funded test subscription) and O3 (real OpenAI-account channel on UAT) remain external
preconditions: lanes L2, L6, L7 ship with hermetic + seeded-row proof and stay `⏳ 待验证` on
UAT until those exist. O6 vendor tier numbers: mechanism ships empty. Migrations 034 → 035 →
036 are reserved in the root ledger in that order before any SQL. Lane order for development:
L1, L3, L4 (with the authHelper refactor first), L8, L9, L10, L2, L5, L6, L7.

### Migration renumbering (operator, 2026-09-13, binding)
The embedded-FS contiguity lock (`internal/pkg/migration/runner_more_unit_test.go`, strict `N == i+1`) fails on any tree where 036 exists without 035, and the development order runs L8 before L7. The root ledger was therefore swapped: **035 = `tasks_add_project_id`** (L8; adds `tasks.project_id` default 0 and `tasks.request_id` varchar(64) default empty with an index) and **036 = `create_response_registry`** (L7). Every §3/§8 reference to "035" for L7 reads 036, and "036" for L8 reads 035. Ledger order = development order = L4 (034) → L8 (035) → L7 (036).
