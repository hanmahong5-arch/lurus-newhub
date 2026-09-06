# Enterprise Uplift — Cycle 2026-09-06

Baseline HEAD: `8b519853` (main, clean apart from five untracked `internal/app/*coverage*_test.go`
files owned by another workstream — off limits for every lane in this cycle).

Scope rules for this cycle:

- Four lanes, implemented **sequentially by separate agents on one shared working tree**.
- Lane file sets are **disjoint at file granularity** (verified below). A lane may *run* tests in a
  package another lane edits; it may not *edit* a file another lane owns.
- No commits, no branches, no PRs, no production or remote-host access from any lane.
- `-race` is unavailable on the build host (no cgo). Package tests run as
  `go test -count=1 -p 2 <packages>`.

## 1. Scorecard

| Dimension | Score now | Rationale (condensed) |
|---|---|---|
| Billing correctness | 6.0 | Arithmetic layer is strong (decimal accumulation, wire-keyed cache pricing, sub-unit floors, overdraft semantics, settle-to-outbox fallback, honest residual counters). The weakness is *ledger routing*: the one field that decides whether a relay reaches the platform wallet is never written by any self-service key-creation path, and the playground path destroys the token identity before settlement. Both silent, both live. |
| Tenant isolation | 6.5 | Console v1/v2 surface is genuinely isolated (slug guard, tenant-scoped admin readers, two structural route-table forcing functions, fail-closed internal-key whitelist). Held down by one authenticated relay entry point mounted without tenant context, tenant-blind admin task/image listings, and internal keys that are not tenant-bound on user-addressed money/PII endpoints. |
| Observability honesty | 5.0 | Inventory is good (~50 series with a deliberate taxonomy, a build-failing alert-wiring honesty gate). Delivery and veracity are not: three replicas publish three colliding copies of every series behind one load-balanced port, no alert rules exist in-repo, the health endpoint reports healthy while degraded, and every leader-gated compliance job is untelemetered. |
| Contract stability | 5.0 | Relay failure plumbing is above average and recently hardened (per-wire envelopes at both rejection stages, SDK-shaped in-band stream errors, incomplete-stream honesty). Everything around it lags: discovery panics on an empty catalogue and answers 200 for models that do not exist, error type/code values are not any vendor's enum, the advertised catalogue is tenant-blind, and the API description file is four months stale with no drift gate. |
| Console honesty | 5.5 | Real honesty primitives exist (KPIs degrade to a dash, deferred-feature markers, job-grouped nav with role gating, full translation parity). But the first-run path prints vendor model literals the tenant may not route, the one control that would show real models is dead against the server's real envelope, a "design-mock only" banner is stamped once per row over live data, and tenant onboarding has no console entry point despite a shipped backend. |
| Test integrity | 6.5 | Unusually strong anti-hollow machinery (all-skip sentinel, selector-emptiness guard, coverage gate on a real database with committed before/after, a gate that reads the CI file and locks its literals, structural route/AST gates). Three measured holes: the v2 handler harness is a hand-maintained route-table duplicate that has already drifted on a money endpoint, the newest isolation lock mounts a fabricated authenticator, and five named tests are pure unconditional skips. |
| Operability | 6.0 | Delivery half is strong (one build to dual auto-pin with content predicates, blocking artifact scan, per-ref concurrency, GitOps convergence with written rationale, lock-free migration status probe, degraded-CDN rollback recipe). Operate-under-incident half is not: the emergency scripts mutate one instance and verify another, the on-call runbook targets a service retired in 2026-04, and the committed secret schema is missing a key the deployment mounts non-optionally. |
| Security/compliance | 5.5 | Foundations are real and non-cosmetic (append-only hash-chained audit events with a single sanctioned redaction path, crash-resumable erasure cascade, per-scope internal auth, encrypted step-up secrets with replay marking, token IP allowlists with audit rows). Two of the three things a buyer actually tests have live holes: one relay route bypasses tenant scoping and every enforcement gate, and console sessions never re-validate the account so a ban does not revoke access. |

## 2. Verification performed before ranking

Every claim a lane depends on was re-read against HEAD rather than trusted from the input
assessments. Confirmed by direct read:

- `internal/adapter/handler/router/relay-router.go:63-67` — the playground group is
  `Use(middleware.PlaygroundAuth(), middleware.Distribute())` only; the sibling `/v1` group at
  `:81-99` mounts six additional gates.
- `internal/adapter/middleware/auth.go:663-718` — `PlaygroundAuth` never injects tenant context,
  never checks the owning user's status, never seeds the using-group.
- `internal/adapter/middleware/distributor.go:139-141` — the comment asserts as fact that the token
  authenticator "always injects tenant_context on the real relay path"; `callerTenantID` at `:149`
  resolves to empty for the playground chain, which disables both the selection filter and the
  post-selection cross-tenant assertion at `:197-202`.
- `internal/adapter/handler/playground.go:80-85` — the authenticated token context is overwritten
  with a throwaway token value that has no id and no key.
- `internal/app/quota.go:866-868` — the tenant pool debit is guarded on `relayInfo.TokenId > 0`, so
  the clobbered identity skips it; `debitTenantPool` at `:753-757` resolves the tenant from the
  token row.
- `internal/adapter/repo/token.go:295-317` — `AutoCreateDefaultToken` hardcodes `TenantId: "default"`.
- `internal/app/token_service.go:139-159` and `internal/adapter/handler/v2_token.go:253-271` —
  neither constructor sets the platform account field; a repo-wide grep finds it only in the struct
  definition at `internal/adapter/repo/token.go:35`.
- `internal/adapter/handler/model.go:218,220` — the Anthropic-format branch indexes element `0` and
  `len-1` of a slice that is empty for a caller with no routable models; `:279-287` returns HTTP 200
  with an error body for an unknown model, always in the OpenAI envelope.
- `internal/adapter/handler/health.go:46-58,93` — an unreachable cache records a check string but
  never clears the flag that is the sole input to the top-level status word.

Baseline test runs on this host (both green, selector match confirmed non-zero):

```
$ go test -count=1 -p 2 -v -run 'TestDistribute_TenantOwnedChannelNeverServesOtherTenant' ./internal/adapter/middleware/
=== RUN   TestDistribute_TenantOwnedChannelNeverServesOtherTenant
--- PASS: TestDistribute_TenantOwnedChannelNeverServesOtherTenant (0.01s)
ok  	github.com/LurusTech/lurus-hub/internal/adapter/middleware	0.208s

$ go test -count=1 -p 2 -run 'TestHealth' -v ./internal/adapter/handler/
--- PASS: TestHealth_SchemaMigrations_OkWhenNothingPending (0.00s)
--- PASS: TestHealth_SchemaMigrations_PendingIsReportedButNotFatal (0.00s)
ok  	github.com/LurusTech/lurus-hub/internal/adapter/handler	0.167s
```

Hermetic harnesses the lane oracles rely on all exist: `internal/app/testutil_test.go:41`
(`setupServiceTestDB`), `internal/adapter/repo/sqlite_testutil_test.go:25` (`setupSQLiteDB`),
`internal/adapter/middleware/cover_helpers_test.go:39` (`setupCoverDB`),
`internal/adapter/middleware/tenant_relay_selection_test.go:30` (`seedTenantRelayChannel`),
`internal/adapter/middleware/final_cover_test.go:26` (`mountWithSession`),
`internal/app/credit_pool_reconcile_test.go:26` (pool seeding).

## 3. Ranked backlog

Live, customer-reachable defects first, then ordered by enterprise value divided by effort.
L1..L4 marks the item's lane this cycle; everything else is deferred with a reason in section 5.

| # | Item | Dimension(s) | Effort | Live? | Value/effort note | Disposition |
|---|---|---|---|---|---|---|
| 1 | Playground relay route is tenant-blind, ungated and unmetered (`relay-router.go:63-67`, `auth.go:663-718`, `playground.go:80-85`) | security SEC-A, tenancy TI-A, testing TI-A, billing BL-B | M | Yes | Four independent assessments converge on one route. Breaks the headline isolation claim, skips the tenant spend gate and the runaway-cost fuse, and loses the pool debit and project attribution. Reachable by every logged-in console user. | **L1** |
| 2 | Model discovery panics on an empty catalogue; unknown models answer 200 in the wrong envelope (`model.go:218,220,279-287`) | contract AC-1 | S | Yes | Two functions in one file. A brand-new tenant's very first call returns a 500 carrying a runtime error string and the upstream project's name. Highest value per line in the backlog. | **L3** |
| 3 | Self-service keys never carry the platform account, so wallet-linked spend is never charged (`token_service.go:139-159`, `v2_token.go:253-271`, gate at `quota.go:983`) | billing BL-A | M | Yes | Un-metered revenue plus a spurious payment-required for a funded customer. The code comment at `internal_api_ext.go:658-663` states the opposite invariant. | **L2** |
| 4 | Auto-created first key hardcodes the bootstrap tenant (`repo/token.go:295-317`) | billing BL-C | S | Yes | One tenant's spend draws another tenant's pool, or is treated as unlimited. Durable: the wrong tenant persists for the life of the key. Makes the L1 money assertion meaningful in production, not only in a seeded test. | **L2** |
| 5 | Health endpoint reports healthy while the cache is unreachable and the billing breaker is open (`health.go:46-58,93`) | observability OBS-B | S | Yes | The one endpoint a buyer's own monitoring consumes, and it lies at exactly the moment no rate or cost ceiling is being enforced. Body-only change; status-code contract untouched. | **L4** |
| 6 | Console sessions never re-validate the account, so a ban does not revoke access (`auth.go:163-178`; the authoritative row is already read at `:234-236`) | security SEC-B | S | Yes | Very high value per line, but it edits the same file L1 owns. | Deferred - file conflict |
| 7 | Emergency rollback and deploy scripts mutate one instance and health-check another (`scripts/stage-rollback.sh:30-32`, `scripts/deploy-stage.sh:60,65`) | operability OPS-A | S | Yes | A failed production rollback can print a success line and exit zero. Fully disjoint, zero runtime risk. Strongest deferred item. | Deferred - cycle budget |
| 8 | The v2 handler test harness registers a money route production never serves (`v2_testutil_test.go:248` vs `api-v2-router.go:274-280`) | testing TI-B | S | Yes | Eight billing tests assert against an endpoint that returns not-found in production. Measured red today. | Deferred - cycle budget |
| 9 | Deferred-feature banner stamped over live catalogue data, once per row (`WIPBanner.jsx:56`, `Models/index.jsx:308,360`) | console CON-2 | S | Yes | Honesty markers that overfire corrode trust in the honest ones. Frontend only. | Deferred |
| 10 | Dead model picker and hardcoded vendor model literals in every first-run snippet (`Playground/index.jsx:164`, `Token/index.jsx:152-178`) | console CON-1 | M | Yes | The buyer's first five minutes end in an error. Partly blocked by item 13. | Deferred |
| 11 | Admin image/task listings are tenant-blind (`midjourney.go:424-447`, `task.go:289-310`) | tenancy TI-B | M | Yes | Cross-tenant free-text disclosure through a customer-held role; needs a schema decision, since those rows carry no tenant column. | Deferred |
| 12 | Wire error type/code are not any vendor's enum (`types/error.go:204,233,243`, `middleware/utils.go:29-30`) | contract AC-2 | M | Yes | Highest-frequency contract surface, but it must renegotiate several existing locks; that is careful work, not bulk work. | Deferred |
| 13 | Advertised model catalogue is tenant-blind while routing is tenant-scoped (`repo/ability.go:31-36` vs `:70-75`) | contract AC-3 | M | Yes | Must land after item 2: fixing catalogue scope first makes the empty-catalogue crash more reachable, not less. | Deferred - ordering |
| 14 | Metrics carry no per-replica identity, so three replicas publish colliding series (`router/main.go:37`, `r6-stage/deployment.yaml:13`) | observability OBS-A | M | Yes | Largest single score delta in the input, but it re-keys the collector's chart dimensions and needs a coordinated operator step on a host this cycle may not touch. | Deferred - needs operator coordination |
| 15 | Leader election and the compliance jobs it gates have no telemetry (`lifecycle/leader_election.go:63,170`) | observability OBS-C | M | Partly | Depends on item 14 to be interpretable across replicas. | Deferred - ordering |
| 16 | Erasure cascade never deletes the step-up enrollment (`lifecycle/privacy_erasure.go:96-180`; the primitive exists at `repo/user_totp.go:54-59`) | security SEC-C | S | Yes | Makes a completion attestation accurate; the already-completed-rows reconciliation makes it more than a one-liner. | Deferred |
| 17 | Tenant invite flow has no console entry point and the settings panel denies it exists (`api-v2-router.go:390-391` vs `Settings/index.jsx:1402`) | console CON-3 | M | Yes | End-to-end onboarding exits the product; needs care with a one-time credential in the DOM. | Deferred |
| 18 | On-call incident runbook targets a service retired in 2026-04 (`doc/runbook/incident-response.md:3`) | operability OPS-C | M | Yes | Real writing work plus a rot gate; pairs naturally with item 7. | Deferred |
| 19 | Committed secret schema omits a key the deployment mounts non-optionally (`r6-stage/deployment.yaml:129-133` vs `secret-template.yaml:11-32`) | operability OPS-B | S | Yes | Rebuild-from-git yields pods that cannot start. Deploy-tree only. | Deferred |
| 20 | Five named tests are unconditional skips; frontend skip locks have no ratchet (`model_sync_worker_test.go:253,267,276,371,431`) | testing TI-C | S | Partly | Prevents decay rather than fixing a reachable defect; the frontend locks were measured honest. | Deferred |
| 21 | Narrow-scope internal keys are not tenant-bound on user-addressed endpoints (`internal_api.go:243,364-390`) | tenancy TI-C | M | No | Defense in depth; the back-compat surface for the live platform key is the whole risk. | Deferred |

### Lane file-disjointness check

| File | L1 | L2 | L3 | L4 |
|---|---|---|---|---|
| `internal/adapter/middleware/auth.go` | edit | | | |
| `internal/adapter/middleware/distributor.go` | edit (comment) | | | |
| `internal/adapter/handler/router/relay-router.go` | edit | | | |
| `internal/adapter/handler/playground.go` | edit | | | |
| `internal/adapter/middleware/playground_tenant_test.go` | new | | | |
| `internal/adapter/handler/router/pg_chain_mount_test.go` | new | | | |
| `internal/app/quota_playground_pool_test.go` | new | | | |
| `internal/app/token_service.go` | | edit | | |
| `internal/adapter/handler/v2_token.go` | | edit | | |
| `internal/adapter/repo/token.go` | | edit | | |
| `internal/app/token_service_identity_link_test.go` | | new | | |
| `internal/adapter/repo/token_autocreate_owner_test.go` | | new | | |
| `internal/adapter/handler/model.go` | | | edit | |
| `internal/adapter/handler/model_discovery_contract_test.go` | | | new | |
| `internal/adapter/handler/router/model_discovery_probe_test.go` | | | new | |
| `internal/adapter/handler/health.go` | | | | edit |
| `internal/adapter/handler/health_test.go` | | | | edit |

No file appears in two columns. L1 and L2 both add new files under `internal/app/`, with distinct
names, and neither touches the five untracked files that belong to another workstream.

Lane order is L1, L2, L3, L4. L1 and L2 interlock in production but not in test: L1 resolves the
playground tenant from the owning user rather than from the token row, so it does not depend on the
L2 fix to the auto-created token, and the L1 money oracle seeds its token tenant explicitly.

## 4. Lanes

### L1 - Playground relay: real tenant context, real token identity, real enforcement chain

Covers backlog item 1 (security SEC-A, tenancy TI-A, testing TI-A, billing BL-B).

**Files owned:** `internal/adapter/middleware/auth.go`, `internal/adapter/middleware/distributor.go`,
`internal/adapter/handler/router/relay-router.go`, `internal/adapter/handler/playground.go`, plus
three new test files: `internal/adapter/middleware/playground_tenant_test.go`,
`internal/adapter/handler/router/pg_chain_mount_test.go`,
`internal/app/quota_playground_pool_test.go`.

**Run in full:** `./internal/adapter/middleware/`, `./internal/adapter/handler/router/`,
`./internal/app/`, `./internal/adapter/handler/`.

**Spec.**

1. In `PlaygroundAuth` (`internal/adapter/middleware/auth.go:663-718`), after the token is resolved
   and before `c.Next()`:
   - Resolve the tenant from the **owning user**, not from the token row: `repo.GetUserCache(userId)`,
     take `TenantId`, default to `"default"` when empty or when the lookup errors. Reading the user
     rather than the token is deliberate: it makes this lane independent of the L2 fix to the
     auto-created token row, and it matches how the session authenticator resolves tenant at
     `auth.go:234-236`.
   - Reject a disabled or suspended tenant with the same shape the session authenticator uses at
     `auth.go:243-253` (forbidden plus `TENANT_DISABLED`), skipping the bootstrap tenant, and
     **failing open on lookup error** exactly as that code does.
   - Reject a disabled owning user before setting up the relay context.
   - Call `repo.InjectTenantContext(c, tenantId, userId)` and set the structured tenant context the
     same way the session path does, so `GetTenantContext` resolves for the distributor.
   - Seed the using-group from the resolved token or user, so the group ratio lookup and group
     eligibility stop falling back to the global list.
   - Do **not** double-inject on the bearer-token branch: that branch delegates to the token
     authenticator, which already injects, and returns immediately - keep that.
2. In `internal/adapter/handler/router/relay-router.go:63-67`, mount on the playground group, at
   `Group()`/`Use()` time and in the same order as the `/v1` group at `:81-99`: `PoolBalanceCheck`,
   `CostSpikeLimit`, `EntitlementCheck`, `ModelRequestRateLimit`, `BusinessRateLimit`,
   `RelayConcurrencyLimit` - all **before** `Distribute`. If any one of them proves infeasible on
   this chain, stop and report with evidence rather than silently omitting it. Preserve the existing
   comment discipline explaining why the order is what it is.
3. In `internal/adapter/handler/playground.go:80-85`, stop destroying the authenticated identity.
   Carry the real id, key, tenant, project and user of the token that `PlaygroundAuth` resolved into
   the context the relay rebuilds from, keeping only the display-name override. The pool-debit guard
   at `internal/app/quota.go:866` and the tenant resolution at `:753-757` then work unchanged.
4. In `internal/adapter/middleware/distributor.go:139-141`, correct the comment: it currently states
   as fact that the token authenticator always injects tenant context on the real relay path. After
   this lane both authenticators inject; say that, and name which chains remain deliberately
   tenant-blind.

**What NOT to change.** Do not touch `internal/app/quota.go`: the `TokenId > 0` guard is correct and
the fix is to stop zeroing the id. Do not change the playground skip of the per-key quota decrement
(`quota.go:885` keys off the playground flag, which stays set) - per-key caps must not start applying
to playground traffic in this lane. Do not touch the relay format stamp on this group; the playground
always answers the OpenAI-compatible shape. Do not touch `internal/adapter/repo/token.go` (L2 owns
it) or the session authenticator's frozen-status check (deferred item 6). Do not weaken any existing
assertion in `tenant_relay_selection_test.go` or `tenant_relay_guard_r3_test.go`.

**Oracle.** Three new tests, all red before the change:

- `internal/adapter/middleware/playground_tenant_test.go`
  - `TestPlaygroundAuth_ForeignTenantChannelNeverServed`: reuse `setupCoverDB`,
    `seedTenantRelayChannel` and `mountWithSession`. Seed a channel owned by tenant `tenant-a` at
    weight 1000 and a shared channel owned by `default` at weight 1, using the model the existing
    helper already seeds. Session user, user row and token all in `tenant-b`. Drive the real
    `PlaygroundAuth()` plus `Distribute()` chain 50 times, require the shared channel every time,
    and assert the tenant context at the terminal handler equals `tenant-b`.
  - `TestPlaygroundAuth_DisabledUser_Rejected` and `TestPlaygroundAuth_DisabledTenant_Rejected`,
    mirroring `tenant_relay_guard_r3_test.go:212`.
  - `TestPlaygroundAuth_TenantLookupError_FailsOpen`: a lookup failure must not lock the console out.
- `internal/adapter/handler/router/pg_chain_mount_test.go`, in the style of
  the existing `b1_*_chain_mount_test.go` in that package: build the real engine with `SetRelayRouter`, walk
  `engine.Routes()`, and for the playground route assert the handler chain contains each of the six
  gate names **and** that each appears before the distributor. Fail fast if the walk finds no
  playground route, so the scan cannot pass vacuously. Assert on name suffixes, not exact strings.
- `internal/app/quota_playground_pool_test.go`: with `setupServiceTestDB` plus the pool seeding
  pattern from `credit_pool_reconcile_test.go:26`, seed tenant `t-pg` with a pool of 1000 and a token
  in that tenant, build a relay info the way the playground path does (playground flag set, **real**
  token id), call `PostConsumeQuota` with a cost of 100 and no pre-consumption, and assert the pool
  balance is exactly 900. Negative control: a second tenant pool must be untouched.

**Mutation that proves the oracle** (perform by hand on a clean tree, record the failing output,
then restore):

1. In `internal/adapter/handler/playground.go`, delete the real id, key and tenant fields from the
   token handed to the context setup, restoring the user-name-group-only literal. Expected:
   `internal/app/quota_playground_pool_test.go` goes red with the pool still at 1000.
2. In `internal/adapter/middleware/auth.go`, delete the `repo.InjectTenantContext` call added to
   `PlaygroundAuth`. Expected: `TestPlaygroundAuth_ForeignTenantChannelNeverServed` goes red,
   selecting the `tenant-a` channel.
3. In `internal/adapter/handler/router/relay-router.go`, remove `middleware.PoolBalanceCheck()` from
   the playground group. Expected: the mount test goes red naming that gate.

**Risk to state in the lane report.** Group resolution narrows: a user whose group is restricted may
lose playground access to groups the global fallback previously allowed. Playground calls for a
tenant with no pool row, or with an exhausted pool, will start being refused the same way the main
relay route refuses them, and cost-spike and rate limits will start applying. Both are the intended
semantics and both are user-visible.

### L2 - Token creation stamps the owner's tenant and platform account

Covers backlog items 3 and 4 (billing BL-A, BL-C).

**Files owned:** `internal/app/token_service.go`, `internal/adapter/handler/v2_token.go`,
`internal/adapter/repo/token.go`, plus `internal/app/token_service_identity_link_test.go` and
`internal/adapter/repo/token_autocreate_owner_test.go`.

**Run in full:** `./internal/app/`, `./internal/adapter/repo/`, `./internal/adapter/handler/`.

**Spec.**

1. `BuildCleanToken` (`internal/app/token_service.go:139-159`): resolve the owning user and stamp the
   platform account field from the user's account link when present. Keep the existing signature so
   `internal/adapter/handler/token.go:171` needs no edit - do the lookup inside. A user with no
   account link leaves the field at zero, which is current behaviour and is correct.
2. `CreateTokenV2` (`internal/adapter/handler/v2_token.go:253-271`): the same stamp, resolved from
   the user in the tenant context.
3. `AutoCreateDefaultToken` (`internal/adapter/repo/token.go:295-317`): replace the hardcoded
   bootstrap tenant with the owning user's tenant, falling back to the bootstrap tenant only when the
   user tenant is empty, and stamp the platform account field the same way. The insert must go
   through the tenant-scoped writer with the resolved tenant, not the literal.

**What NOT to change.** Do **not** copy the self-heal endpoint's unlimited-quota behaviour: stamp the
account field only, and leave the remaining-quota and unlimited flags exactly as the caller set them,
or the per-key spending caps a customer chose will silently disappear. Do not backfill existing rows
from application startup - re-tenanting or re-linking a live key changes what it can route to and
which ledger it draws, so that is an explicit operator action and out of scope for code. Do not touch
`internal/app/quota.go`, the middleware, or the router (L1 owns those). Do not modify, stage or read
the five untracked coverage files under `internal/app/`.

**Oracle.**

- `internal/app/token_service_identity_link_test.go` on the hermetic tier (`setupServiceTestDB`,
  `seedTestUser`): seed a user carrying platform account 77001, build a token through
  `BuildCleanToken` and insert it the way the v1 creation handler does, reload the row and assert the
  account field equals 77001. Second case: a user with no account link yields zero, and the caller
  remaining-quota and unlimited flags survive untouched.
- Money-level companion in the same file: build a relay info from the reloaded token, stub the
  existing wallet-debit seam at `internal/app/quota.go:47` with a recorder, call `PostConsumeQuota`
  with a non-zero cost, and assert the recorder saw exactly one debit for account 77001. This is the
  assertion that pins the gate on the money-moving call rather than on a proxy field.
- `internal/adapter/repo/token_autocreate_owner_test.go` on `setupSQLiteDB`: seed a user in tenant
  `acme`, call `AutoCreateDefaultToken`, assert the row tenant is `acme`.
- Two-pool companion in `internal/app`: seed pools for `acme` (1000) and the bootstrap tenant (1000),
  settle 100 through the auto-created token, assert `acme` fell to 900 **and** the bootstrap pool is
  still exactly 1000.

**Mutation.**

1. In `internal/app/token_service.go`, delete the line that stamps the platform account field.
   Expected: the identity-link test reports the field as 0, and the recorder test reports zero debits.
2. In `internal/adapter/repo/token.go`, restore `TenantId: "default"` in `AutoCreateDefaultToken`.
   Expected: the repo test reports the bootstrap tenant, and the two-pool test reports the bootstrap
   pool drained to 900 while `acme` stayed at 1000 - the assertion that separates this from a
   cosmetic field fix.

### L3 - Model discovery contract

Covers backlog item 2 (contract AC-1).

**Files owned:** `internal/adapter/handler/model.go`, plus
`internal/adapter/handler/model_discovery_contract_test.go` and
`internal/adapter/handler/router/model_discovery_probe_test.go`.

**Run in full:** `./internal/adapter/handler/`, `./internal/adapter/handler/router/`.

**Spec.**

1. `ListModels`, Anthropic-format branch (`internal/adapter/handler/model.go:207-222`): guard the
   first and last element reads. With an empty catalogue the response must be an empty data array
   with null first and last identifiers and no-more set false, HTTP 200, no panic. Keep the non-empty
   behaviour byte-identical.
2. `RetrieveModel` (`internal/adapter/handler/model.go:264-288`): the unknown-model branch must
   answer HTTP 404, not 200.
3. Same branch: emit the caller's wire envelope, switching on the model type the way the success branch
   already does at `:266-277`. The Anthropic-format error body must carry that vendor not-found error
   type and must not carry the OpenAI-only parameter field.

**What NOT to change.** Do not touch the success payloads or the third wire branch shapes. Do not
touch `internal/adapter/handler/router/relay-router.go` (L1 owns it) - the router-level probe is a
new test file that builds the real engine and asserts; it re-mounts nothing. Do not attempt the
tenant scoping of the catalogue query in this lane (deferred item 13): keeping discovery tenant-blind
here is deliberate, because narrowing it first would make the empty-catalogue crash more reachable,
not less.

**Oracle.** `internal/adapter/handler/model_discovery_contract_test.go`, table-driven:

- Empty catalogue on the Anthropic wire (model-limit enabled with an empty limit map) returns 200
  with an empty data array and null first and last, and does not panic.
- Unknown model on the OpenAI wire returns 404.
- Unknown model on the Anthropic wire returns that vendor's error envelope with the not-found type and
  no parameter field.
- Non-empty catalogue on both wires is unchanged - negative control, so the guard cannot be a blanket
  rewrite.

Plus `internal/adapter/handler/router/model_discovery_probe_test.go`: build the real engine with the
relay router, issue the discovery request with the version and key headers that select the
Anthropic-format branch for a caller with no routable models, and assert the response is not a 500
carrying the upstream project's panic type. Fail fast if the route is not found, so the probe cannot
pass vacuously.

**Mutation.**

1. Restore the unguarded `useranthropicModels[0].ID` and `useranthropicModels[len(...)-1].ID`.
   Expected: the empty-catalogue row panics with an index-out-of-range, and the router probe reports
   the 500 panic envelope.
2. Restore `c.JSON(200, ...)` in the unknown-model branch. Expected: the 404 row goes red.
3. Remove the wire switch from the unknown-model branch. Expected: the Anthropic-envelope row goes
   red on the parameter field.

### L4 - Health endpoint stops reporting healthy while degraded

Covers backlog item 5 (observability OBS-B).

**Files owned:** `internal/adapter/handler/health.go`, `internal/adapter/handler/health_test.go`.

**Run in full:** `./internal/adapter/handler/`.

**Spec.** Keep the HTTP status code derivation exactly as it is (`health.go:86-89`): only a failed
database check may produce a service-unavailable code, because the readiness probe gates traffic on
this endpoint and the deployment manifest comment is explicit that a billing blip must not take
replicas out of rotation. Change only the body status word at `:93`: report `degraded` when any check
is neither `ok` nor an intentional off-state. The intentional off-states are the disabled cache, the
legacy billing mode, the not-configured database, and an unknown migration snapshot; `unreachable`,
`error`, `not_configured` for an enabled cache, `circuit_open` and `pending:N` are all degraded.
Introduce the classification as a small named helper, so the intentional-off list is a readable,
testable set rather than an inline condition.

**What NOT to change.** Do not change the status code on any path. Do not add checks. Do not touch
any other file in the handler package (L2 and L3 own files there). Before changing the string, grep
the repository and the deploy tree for consumers that compare the status word literally, and record
the result in the lane report.

**Oracle.** New cases in `internal/adapter/handler/health_test.go`:

- Cache enabled with a client pointed at a closed listener, database healthy: HTTP 200, body status
  `degraded`, cache check `unreachable`.
- Billing breaker forced open: HTTP 200, body status `degraded`.
- Pending migrations reported: HTTP 200, body status `degraded` - the code already refuses to fail
  readiness here, and the body should still be honest.
- All-ok: HTTP 200, status `healthy` - negative control.
- Closed database: still HTTP 503 - the assertion that proves the readiness contract did not change.
- Cache disabled and billing in legacy mode: status `healthy`, proving intentional off-states are not
  misreported as degradation.

**Mutation.** Restore the original single-flag status expression
(`map[bool]string{true: "healthy", false: "degraded"}[healthy]`). Expected: the first three cases go
red reporting `healthy`, while the all-ok and closed-database cases stay green - which is what proves
the change was to the body and not to readiness.

## 5. Deferred

| Item | Why deferred |
|---|---|
| security SEC-B - session never re-validates the account, so a ban does not revoke access | Edits `internal/adapter/middleware/auth.go`, which L1 owns; sequential lanes on a shared tree must be file-disjoint. First item of next cycle. |
| operability OPS-A - rollback and deploy scripts verify the wrong instance | Cycle budget of four lanes. Fully disjoint and zero runtime risk, so it is the cheapest carry-over. |
| testing TI-B - v2 handler harness registers a money route production never serves | Cycle budget; also wants the eight affected billing tests converted to direct handler invocations rather than deleted, which is judgement work. |
| console CON-2 - deferred-feature banner stamped over live data, once per row | Cycle budget; frontend-only, no dependency on any lane. |
| console CON-1 - dead model picker and hardcoded vendor model literals in first-run snippets | Partly blocked by contract AC-3: the query behind the model list is tenant-blind, so shipping it as a routing guarantee would be dishonest. |
| tenancy TI-B - admin image and task listings are tenant-blind | Those rows carry no tenant column; scoping needs an explicit decision about rows owned by distributor keys and deleted users, not a silent join. |
| contract AC-2 - wire error type and code are not any vendor's enum | Must renegotiate several existing locks one by one, verifying in each case that the old value was provably wrong; too easy to soften a lock to fit the change under lane time pressure. |
| contract AC-3 - advertised catalogue is tenant-blind | Ordering: must follow AC-1, otherwise the empty-catalogue crash becomes more reachable. |
| observability OBS-A - no per-replica identity on the metrics exposition | Largest single delta available, but it re-keys the collector's chart dimensions and only pays off once the scrape targets individual replicas - an operator step on a host this cycle is not permitted to touch. |
| observability OBS-C - leader election and its gated compliance jobs are untelemetered | A fleet-wide leader gauge is uninterpretable until OBS-A lands. |
| security SEC-C - erasure cascade leaves the step-up enrollment behind | Small, but needs a step-ordering decision plus a reconciliation for already-completed requests; not a one-liner. |
| console CON-3 - no console entry point for tenant onboarding | One-time credential handling in the browser needs care that does not fit a spare lane. |
| operability OPS-C - on-call runbook targets a service retired in 2026-04 | Real writing work; pairs with OPS-A and should land with it. |
| operability OPS-B - committed secret schema omits a non-optional mounted key | Deploy-tree only; carry with OPS-A and OPS-C as one operability lane. |
| testing TI-C - pseudo-tests and unratcheted frontend skip locks | Prevents decay rather than fixing anything reachable; the frontend skip locks were measured to be honest today. |
| tenancy TI-C - internal keys not tenant-bound on user-addressed endpoints | Defense in depth, and the entire risk is back-compat for the live platform key plus the endpoints that resolve a user before the row exists. |

## 6. Next cycle

Proposed shape, assuming this cycle lands:

1. **Session revocation** (SEC-B) first: the cheapest remaining live defect, and the file it needs is
   free once L1 is merged. Pair it with the negative controls that lock the fail-open-on-lookup-error
   decision, and check the bridge branch agrees with the session branch.
2. **One operability lane** bundling OPS-A, OPS-B and OPS-C behind a single new hermetic gate package
   that reads the deploy tree and the runbooks - the same shape the existing alert-wiring honesty
   gate already uses. These three share a theme and touch no runtime code, so they merge cleanly.
3. **Replica identity on metrics** (OBS-A), then **leader and compliance-job telemetry** (OBS-C), in
   that order, with the collector re-key documented in the deploy README before the change lands
   rather than discovered afterwards.
4. **Wire error taxonomy** (AC-2), then **tenant-scoped catalogue** (AC-3), taking the existing locks
   one at a time.

Two standing cautions carried forward, both directly relevant to L1 and L2:

- A verified pass with a scope caveat attached is a defect report, not a footnote. If a lane passes
  but its oracle covers only part of the claim, say so in the lane report and pin the uncovered part
  with a named follow-up.
- Every mutation must be performed on a clean tree. Check the tree is clean before reverting
  anything, or an unrelated uncommitted change is destroyed by the restore. The five untracked files
  under `internal/app/` belong to another workstream and must survive every lane untouched.
