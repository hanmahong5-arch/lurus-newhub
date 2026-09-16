# Cycle 9 — completion cycle: close what cycles 7–8 built but left one consumer short

Branch: `feat/cycle9-completion` from `8878225b` (cycle-8 landing + the two UAT follow-ups).
Method: twelve read-only re-verification agents against HEAD → three independent lane plans
(operator / enterprise buyer / long-horizon maintainer) → two judges → this synthesis.
Workflow run `wf_0cf24af7-6a3` (17 agents, 2.0M tokens, 26 min).

## 1. What the re-verification changed

The parity matrix (`newapi-parity-matrix-2026-09-12.md`) is now materially stale in newhub's
favour. Rows it calls `missing` that are in fact shipped on HEAD:

| row | matrix | HEAD | proof |
|---|---|---|---|
| logs-analytics-observability-08/09 | missing | shipped | `GetModelPerformanceV2` `v2_admin_analytics.go:33`, route `api-v2-router.go:537` |
| logs-analytics-observability-10 | missing | shipped | `v2_analytics_rankings.go`, routes `api-v2-router.go:217,542` |
| logs-analytics-observability-27/28 | missing | shipped | `writeRankingsResponse:148`, `rankingsCacheTTL = 5 * time.Minute:25` |
| console-ux-03/04 | missing/partial | shipped | `web/src/pages/v2/Admin/ModelPerformance/index.jsx`, `Analytics/Rankings.jsx` |
| routing-resilience-limits-11 | missing | shipped | `session_affinity.go:381-499`, `v2_admin_routing.go:47-135` |
| routing-resilience-limits-13 | missing | shipped | cycle-7 `force_http1` |
| billing-pricing-02/03 | partial/missing | shipped | cycle-7 pricing CAS + preview |
| auth-security-08 | partial | shipped | `v2_sessions.go`, `v2_session_revoke.go`, migration 033 |
| auth-security-16 | partial | shipped | cycle-7 fail-closed audit backstop |
| tasks-plugins-18 | partial | shipped | cycle-8 L10 filters |
| topup-payments-subscriptions-35 | missing | shipped | cycle-8 L1 redemption limiter |
| console-ux-23 | partial | shipped | v2 profile page |

Rows the judges struck off as premises that are **false**, not merely deferred:

- **Runtime/GC admin endpoint (logs-analytics-18/-29).** `router/main.go:43` serves
  `promhttp.Handler()` over the *default* gatherer and `internal/pkg/metrics/metrics.go` uses
  `promauto` throughout with no private registry, so `go_goroutines`,
  `go_memstats_heap_alloc_bytes`, `go_gc_duration_seconds` and the process collector are already
  exposed and already scraped. There is no blindness to fix. A forced `runtime.GC()` button on a
  1Gi-limit pod is rejected outright.
- **`MultiKeyModeRotating` (routing-resilience-limits-22).** The local upstream checkout does not
  define it; the row is void, not deferred.
- **A functional index on `logs.other->>'request_id'` as migration 037.**
  `migrations/029_create_projects.sql:61-74` forbids it in first-party prose and
  `internal/pkg/migration/runner.go:324-338` is the mechanism: `BeginTx` → one `ExecContext` of the
  whole body → `Commit`, so `CREATE INDEX CONCURRENTLY` is structurally impossible and a plain
  `CREATE INDEX` on `logs` holds ShareLock against the relay's own consume-log writes.
  **If any agent re-proposes this, reject it on sight.**

What the re-verification found that no matrix row names:

- **Step-up verification can be satisfied with no credential at all.**
  `secure_verification.go:41-42` documents it and the code implements it: with `enrolled == false`
  the only rejection is `req.Method != "session"`, so any authenticated enabled user sets
  `SecureVerificationSessionKey` with an empty credential. `v2_admin_security.go:77-81` concedes the
  consequence in first-party prose. That gate is what stands in front of channel-key reveal
  (`api-router.go:151`) and 2FA force-disable. **Measured 2026-09-15: production has exactly one
  privileged account (`users` role 100, id 1) and it has no row in `user_totps`** — so today that
  gate is exactly equal to "holds a session cookie".
- **Every delegated grant is permanent.** `034_create_admin_permission_grants.sql:54-62` has no
  expiry column and `repo/admin_permission_grant.go` filters `revoked_at IS NULL` only.
- ~~**Zero live alerting.**~~ **WRONG — corrected 2026-09-15, see §8 L9.** The repo-side claim is
  true as far as it goes: `deploy/k8s/r6-stage/newhub-prometheus-rule.yaml:1-13` says in its own
  header that nothing evaluates its rules, and `alert_wiring_honesty_test.go` exists solely to stop
  Go comments claiming otherwise. But the conclusion was drawn from the repo alone: the R6 **host**
  already ran `/data/obs-pack/runtime/netdata/config/health.d/newhub.conf` with 8 alarm templates
  (4 bound, 4 unbound). L9 therefore adopts that host file into
  `deploy/r6-host-netdata/` instead of writing new alarms beside it. This is the same mistake the
  2026-08-24 audit made about off-site backups — a negative conclusion about production must query
  the host layer too, not just `deploy/k8s/`.

## 2. Measured facts that set this cycle's defaults

Both are `SELECT`-only queries run by the operator against the live databases on 2026-09-15:

```
newhub     : role 1 -> 11 users, role 100 -> 1 user, role 10 -> 0 users
newhub     : users.role >= 10 AND status=1 JOIN user_totps -> id 1, role 100, has_totp = f
newhub_uat : id 1 role 100 (no totp), id 3 role 10 (no totp), ids 2/4/5 role 1
```

Consequences, binding:

- **L2 ships enforcing by default.** Requiring a grant for a non-root admin to write a channel key
  or base URL breaks nobody in production: there are no non-root admins. UAT has exactly one
  (id 3), which is the probe subject.
- **L3 ships its enforcement behind a flag that defaults OFF.** Turning "step-up needs a real
  credential" on today would lock the only production root out of channel-key reveal and 2FA
  force-disable, because that account has no enrolled factor. The *audit* half of L3 ships
  unconditionally, so the credential-free grant stops being invisible either way.

## 3. Lanes (binding order L1 → L9)

Development order is the landing order and the migration order. One migration this cycle: **037**.

### L1 — expiry on delegated permission grants (migration 037)

**Gap.** A grant is forever. `repo/admin_permission_grant.go` has one liveness predicate,
`revoked_at IS NULL`. Handing an ops admin `channel:sensitive_write` (L2) without this is handing
out a standing credential-substitution capability.

**Scope.** Add `expires_at BIGINT NULL` to `admin_permission_grants`; a grant is active when
`revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now)`. `POST …/authz/grants` accepts an
optional `ttl_seconds` (bounded: 1 .. 90 days); the audit detail records it. The list response
carries `expires_at` and a derived `expired` boolean. The console grant table shows it.

**The trap, and the ruling.** `034`'s unique index is
`uk_admin_permission_grants_active … WHERE revoked_at IS NULL`, so an *expired but unrevoked* row
still occupies the slot and a re-grant of the same (user, resource, action) would 409 forever.
The index is NOT changed (re-shaping a partial unique index on a live table is a second migration's
worth of risk). Instead `CreatePermissionGrant` treats an existing row that is expired as
re-grantable: inside the same transaction it stamps `revoked_at = now` on the expired row and then
inserts the new one. An existing row that is *live* still returns `ErrGrantExists`.

**Files.** `migrations/037_admin_permission_grants_add_expires_at.sql`,
the migration-count test, `internal/domain/entity/admin_permission_grant.go`,
`internal/adapter/repo/admin_permission_grant.go` + `_test.go`,
`internal/adapter/handler/v2_admin_authz.go` + `_test.go`,
`web/src/pages/v2/Admin/Authz.jsx` + `Authz.test.jsx`.

**Oracles.**
- `TestGrant_ExpiredIsNotActive` — insert a grant with `expires_at` in the past;
  `HasActivePermissionGrant` returns false. Deleting the expiry clause makes it red.
- `TestGrant_ReGrantAfterExpirySucceedsAndRevokesTheOldRow` — expired row + create → 200, and the
  old row now has a non-null `revoked_at`; exactly one active row remains.
- `TestGrant_ReGrantWhileLiveStill409` — the pre-existing behaviour is unchanged.
- `TestGrant_TTLBounds` — `ttl_seconds` 0 / negative / > 90d → 400 `GRANT_INVALID`.
- `TestEmbeddedFS_VersionsAreContiguous` (existing) must stay green with 037.

**UAT probe.** Create a grant with `ttl_seconds=60` for user 3 → list shows `expires_at`; the
granted route answers 200; after 60 s the same session gets 403 and the list shows `expired:true`;
re-granting the same pair returns 200 (not 409) and the audit trail carries both rows.

### L2 — `channel:sensitive_write`: stop any admin silently swapping a channel key or base URL

**Gap.** `api-router.go:151` guards merely *reading* a channel key with
`RootAuth + CriticalRateLimit + DisableCache + SecureVerificationRequired`, while `:157`
`PUT /api/channel/` — which *replaces* the key — sits under a bare `AdminAuth()`. v2 is the same
shape: `v2_channel.go:455-465` applies `Key` and `BaseURL` after only the tenant-admin check.
`internal/app/authz/catalog.go:15-21` names this exact follow-up in its own comment.

**Scope.** Add `channel: [sensitive_write]` to the catalogue. A **sensitive field set** =
`key`, `base_url`, `param_override`, `header_override`, the per-channel proxy setting, `type`,
`other` and `openai_organization` — the fields that decide *where traffic goes or what credential
it carries*. (The last three were added during the repair round: `type` selects the derived
upstream host when `base_url` is empty, `other` feeds api_version/region/plugin/bot_id, and
`openai_organization` rides as a header on the credential. `OtherSettings`/`json:"settings"` can
also steer a request and is an explicit non-goal, so the set is "what this gate covers", not
"everything with that property".) Enforcement is in-handler (no router edit). The entry points grew
past the four originally planned — v1 `POST /api/channel/`, v1 `PUT /api/channel/`, v2 `POST
…/channels`, v2 `PUT …/channels/:id`, plus v1 `CopyChannel`, the key-removal branches of v1
`ManageMultiKeys`, and v1 `PUT /api/channel/tag`; `channel_sensitive_write.go` is the live list.
Authorization is decided **before** the document and egress validators run, so an ungranted caller
cannot use the validator as an oracle and the refusal is always audited. Root always passes. A
non-root admin without an active grant gets **403 `PERMISSION_DENIED` with no partial write** — not
a silent strip, which would be a key rotation that reports success and does nothing. Updates that
touch no sensitive field are unaffected. Every refusal writes an audit row.

**Default: enforcing.** Measured in §2: zero non-root admins in production.

**Files.** `internal/app/authz/catalog.go` + `_test.go`,
`internal/adapter/handler/channel_sensitive_write.go` + `_test.go` (the shared predicate),
`internal/adapter/handler/channel.go` + `channel_test.go`,
`internal/adapter/handler/v2_channel.go` + `v2_channel_test.go`,
`internal/app/governance/audit_action.go` (append only).

**Oracles.**
- `TestChannelSensitiveWrite_FieldSetIsExhaustive` — a table-driven test that lists the sensitive
  fields and asserts the predicate flags each one; it also asserts a *non*-sensitive field (name,
  models, group, priority) does not. Removing a field from the predicate turns it red.
- `TestUpdateChannel_V1_NonRootAdminWithoutGrant403` and the v2 twin — **both routes**, because a
  v2-only gate is bypassable through v1.
- `TestUpdateChannel_V1_RefusedWriteLeavesRowUnchanged` — re-read the row after the 403 and compare
  every column; a silent partial write fails here.
- `TestCreateChannel_NonRootAdminWithoutGrant403` — creation carries the same power.
- `TestUpdateChannel_RootAlwaysPasses` and `_NonSensitiveFieldsUnaffected`.
- `TestAdminWriteRoutesAreAudited` (existing) must still name no new unaudited route.

**UAT probe.** As UAT user 3 (role 10): `PUT` a channel's `base_url` → 403 `PERMISSION_DENIED`
plus the audit row; `PUT` the same channel's `name` → 200; root grants `channel:sensitive_write`;
the same call now returns 200 and the channel's base URL really changed (re-read it); revoke →
403 again.

### L3 — step-up that means something: audit the credential-free path, and offer a flag to close it

**Gap.** §1. Today a stolen session cookie satisfies every "secure verification required" gate for
any user with no enrolled factor — which is every privileged account in production.

**Scope, three parts.**
1. **Unconditional:** when `UniversalVerify` grants verification with `method: "session"` and the
   user has no enrollment, write an audit row (`auth.stepup_without_credential`) naming the user and
   the fact that no credential was presented. This ships on by default; it cannot lock anyone out
   and it converts an invisible weakness into an auditable event.
2. **Flag `SECURE_VERIFICATION_REQUIRE_ENROLLMENT`, default `false`:** when true, the no-enrollment
   branch answers **403 `STEP_UP_ENROLLMENT_REQUIRED`** with a message pointing at the enrolment
   page, instead of passing.
3. **Honesty:** `VerificationStatusResponse` gains `enrollment_required` so the console can show the
   real policy rather than assuming the legacy one, and the doc comment's "legacy behavior"
   paragraph is rewritten to say what it costs.

**Files.** `internal/adapter/handler/secure_verification.go` + `_test.go`,
the setting package for the flag, `internal/app/governance/audit_action.go` (append, after L2),
`doc/runbook/incident-response.md` (break-glass: how to turn it off), `.env.example`.

**Oracles.**
- `TestUniversalVerify_NoEnrollment_WritesCredentialFreeAuditRow` — flag off; assert the row exists
  and names the user. Deleting the audit call turns it red.
- `TestUniversalVerify_NoEnrollment_FlagOn_403EnrollmentRequired` — and asserts the session key was
  **not** set (checking only the status code would pass a handler that 403s after verifying).
- `TestUniversalVerify_Enrolled_UnchangedUnderBothFlagStates` — the TOTP path is untouched.
- `TestVerificationStatus_ReportsEnrollmentRequired`.

**UAT probe.** Flag off: root verifies → 200 and the new audit row appears. Flip the flag on the
UAT deployment: the same call returns 403 `STEP_UP_ENROLLMENT_REQUIRED` and channel-key reveal is
refused; flip it back. Production keeps the default until the owner enrols a factor for root
(owner item **O1**).

### L4 — VideoProxy egress parity: the SSRF guard the newer route has and the older one does not

**Gap.** `git grep ValidateOutboundURL -- '*.go' | grep -v _test` → `channel.go:1370`,
`task_media_guard.go:219`, `v2_channel_actions.go:53`, `ssrf_guard.go` — and **nothing in
`video_proxy.go`**, which serves the same class of URL with only `allowedArtifactScheme`
(`:199`) and `isSelfOrLoopURL` (`:210`). Two first-party comments already admit the asymmetry:
`metrics.go:562` and `task_media_guard.go:20`.

**Scope.** `VideoProxy` calls `app.ValidateOutboundURL` with the same refusal shape and the same
metric labels as `GetTaskArtifactContent`. The two admitting comments are corrected in the same
commit — a comment that documents a hole must not outlive the hole.

**Files.** `internal/adapter/handler/video_proxy.go` + `video_proxy_test.go`,
`internal/pkg/metrics/metrics.go` (comment + label doc; **this lane owns this file**),
`internal/adapter/handler/task_media_guard.go` (comment only).

**Oracles.**
- `TestVideoProxy_RefusesPrivateAddress` — a `http://10.0.0.1/x` target is refused with the
  egress-check reason and the metric increments; deleting the call turns it red.
- `TestVideoProxy_RefusesDomainResolvingToPrivateAddress` (shipped name; the plan first called it
  `TestVideoProxy_RefusesDNSRebindShape`) — whatever `ValidateOutboundURL` already covers, asserted
  through this route so the two routes cannot drift again.
- `TestTaskMediaRoutes_BothCallTheEgressGuard` — a source-level assertion that every handler
  serving a vendor-supplied URL calls the guard. This is the anti-drift oracle; it is what stops a
  third route shipping without it.

### L5 — conversion-fidelity diagnostics: record what cross-wire conversion silently drops

**Gap.** `convert.go:15 ClaudeToOpenAIRequest` builds the OpenAI request from a handful of fields
and discards the rest without a word; `:610 GeminiToOpenAIRequest` likewise. A customer whose
`top_k` or `tool_choice` never reached the vendor has no way to learn that from the product.

**Scope.** Both request-side converters compute a deterministic, sorted, de-duplicated list of the
caller's field names that were dropped or downgraded. It is stashed on `RelayInfo` and projected
into the log row's `other` as `conversion_dropped` by `app.GenerateTextOtherInfo`
(`log_info_generate.go:34`) — the single success-path funnel: `compatible_handler.go:490` calls it
and `log_info_generate.go:96,108,125` wrap it, so **neither `quota.go` nor `compatible_handler.go`
is edited**. Visibility is **public**: the caller set the field, so telling them it was ignored is
the entire point. Bounded: at most 16 names, each at most 32 bytes.

**Explicitly out of scope, stated so it cannot be mistaken for an oversight:** the terminal-error
path in `internal/adapter/handler/relay.go:775-805` builds its own `other` map key by key and does
**not** carry the diagnostics. `relay.go` is on the do-not-regress untouched list. A failed request
therefore has no `conversion_dropped`; the lane report must say so.

**Files.** `internal/app/convert.go` + `convert_test.go`,
`internal/adapter/provider/common/relay_info.go`, `internal/app/log_info_generate.go` + `_test.go`,
the `other`-key classification source, `internal/app/log_other_projection_lock_test.go`.

**Oracles.**
- `TestClaudeToOpenAI_ReportsDroppedFields` — a Claude request carrying fields the converter does
  not map yields exactly those names; the assertion is on the *exact set*, so both a missing name
  and an invented one fail.
- `TestGeminiToOpenAI_ReportsDroppedFields` — same shape.
- `TestConversionDiagnostics_EmptyWhenNothingDropped` — a minimal request produces no key at all
  (an always-present empty array would be noise in every log row).
- `TestOtherProjectionIsFullyClassified` (existing) — must name `conversion_dropped` as public;
  this is the default-deny gate and it goes red if the key is added without classification.
- `TestConversionDiagnostics_Bounded` — 100 dropped fields truncate to 16 with a marker.

**UAT probe.** Send a Claude-wire request carrying a droppable field to a UAT channel; read the
resulting log row's `other` and show `conversion_dropped` listing exactly that field; send the
minimal request and show the key absent.

### L6 — console: search logs by `request_id` and `upstream_request_id`

**Gap.** `v2_log.go:217-221` binds `request_id`, `session_id` and (admin-only)
`upstream_request_id`, and `repo/log.go:916,919,979,982` wire them into the query — while
`grep -rn 'request_id' web/src/pages/v2/Log/index.jsx` returns **zero hits**. Cycle 7 shipped the
capture and cycle 8 shipped the filter; nobody can reach either from the product.

**Scope.** Frontend only. The Log page gains the two filter inputs (the upstream one only for
admins, mirroring the backend's own gate) and shows both ids in the detail panel with a
copy-to-clipboard affordance. **The lane always sends a time range** — the backend filter is a JSON
extract with no supporting index (§1), so an unbounded id search would scan the whole `logs` table.
No Go file is edited: adding a mandatory time window to a shipped admin API is a behaviour change
and does not belong in a frontend lane.

**Files.** `web/src/pages/v2/Log/index.jsx`, `web/src/pages/v2/Log/index.test.jsx`.

**Oracles.**
- `renders the request-id filter and sends it on the query` — asserts the outgoing query string
  contains `request_id=` **and** a bounded start timestamp; dropping the time bound turns it red.
- `hides the upstream-request-id filter from non-admin users`.
- `shows both ids in the detail panel` — a row whose `other` carries `upstream_request_id`.

### L7 — rankings by group

**Gap.** `v2_analytics_rankings.go:119-120` is
`if by != "model" && by != "vendor" { … }` while `entity/log.go:22` already carries a `Group`
column that is populated and indexed. The dimension that answers "which pricing/routing group is
burning the quota" is one validator away.

**Scope.** Accept `by=group`; the repo query groups on the column with the identifier **quoted**
(`GROUP BY "group"` — `group` is a SQL reserved word and an unquoted version fails on PG while
passing on the hermetic SQLite tier, which is exactly the kind of green-locally/red-in-prod trap
this cycle must not ship). Empty group strings rank as a single explicit `(ungrouped)` bucket
rather than being dropped. The console Rankings page gains the third dimension.

**Files.** `internal/adapter/handler/v2_analytics_rankings.go` + `_test.go`,
`internal/adapter/repo/log.go` + its rankings test (**this lane owns the repo file**),
`web/src/pages/v2/Analytics/Rankings.jsx` + `Rankings.test.jsx`.

**Oracles.**
- `TestRankings_ByGroup_QuotesTheReservedIdentifier` — asserts the generated SQL contains the
  quoted identifier. This is the trap oracle; it must be written before the query.
- `TestRankings_ByGroup_UngroupedBucket` — rows with an empty group produce one labelled bucket.
- `TestRankings_ByGroup_TenantScoped` — the tenant-scoped route never returns another tenant's
  groups.
- `TestRankings_RejectsUnknownDimension` — `by=whatever` still 400s.

**UAT probe.** `GET /api/v2/admin/analytics/rankings?by=group` on UAT returns the seeded groups with
non-zero quota totals, and the tenant-scoped route returns only that tenant's.

### L8 — one admin Diagnostics page for the backends that shipped blind

**Gap.** Two shipped, root-gated backends have zero frontend consumers:
session-affinity stats/purge (`api-v2-router.go:531-533`) and TOTP adoption
(`api-v2-router.go:558`). `grep -rn 'routing/affinity' web/src` and `grep -rn 'totp-stats' web/src`
both return nothing.

**Scope.** **One** page — `admin/diagnostics` — with two panels: affinity (hit/miss/stale counters,
backend, entry count, purge-one and purge-all with a confirm) and security posture (TOTP adoption:
enrolled vs total, and the count of privileged accounts with no factor, which is the number §2
measured as 1). Root-only, using the `minRole: 100` precedent already in the nav shell. This
lane **owns the console shell files**; no other lane may edit them.

**Files.** `web/src/pages/v2/Admin/Diagnostics/index.jsx` + `index.test.jsx`, `web/src/App.jsx`,
the nav shell, the App-level route test.

**Oracles.**
- `calls both endpoints on mount and renders their numbers` — with the responses mocked; a panel
  rendering a hard-coded number fails.
- `purge-all asks for confirmation and sends the request only after it`.
- `is not reachable below role 100` — asserted through the nav config, not by reading the page.

### L9 — the alerting last mile: repo-owned netdata alarms with a series oracle

**Gap.** Nothing on this service alerts anyone. `newhub-prometheus-rule.yaml:1-13` says so itself;
`alert_wiring_honesty_test.go` exists only to keep Go comments from claiming otherwise. Cycles 7
and 8 shipped a pile of metrics whose only consumer is a dashboard nobody watches at 03:00.

**Scope.** Repo-owned netdata alarm definitions under `deploy/r6-host-netdata/health.d/`
(the directory-as-source-of-truth shape already used by `deploy/r6-host-nginx/`), an idempotent
install script, and a runbook entry per alarm — `doc/runbook/INDEX.md` is a hard gate, so a
page-level alarm without a runbook is not shippable. First batch is **only alarms that can be
provoked on demand**, so each one gets a live proof: upstream 5xx burst, rate-limit degradation,
and failover suppression.

**The repo oracle.** `TestNetdataAlarmsNameOnlyLiveSeries` parses the alarm files, extracts every
metric name, and asserts each is actually written by production code — reusing the machinery behind
the existing `TestDeclaredSeriesHaveAProductionWriter`. This is what makes an alarm file reviewable
in CI: the classic failure is an alarm on a series nobody emits, which is silent forever. The
honesty test is extended so the new directory asserts the *opposite* of the reference-only YAML —
these files ARE installed, and the install script is named.

**Honest limits, stated in the lane report and the runbook.** Alarm *delivery* to a human still
depends on the host's `health_alarm_notify.conf` recipients, which is an owner item (**O2**). This
lane proves the alarm transitions state and is visible in netdata's alarm API; it does not claim
anyone is paged.

**Files.** `deploy/r6-host-netdata/health.d/newhub.conf`, `deploy/r6-host-netdata/README.md`,
`scripts/install-netdata-alarms.sh`, a new runbook page, `doc/runbook/INDEX.md`,
`internal/pkg/metrics/netdata_alarm_series_test.go`,
`internal/pkg/metrics/alert_wiring_honesty_test.go`. **This lane must not edit `metrics.go`** (L4
owns it).

**UAT probe (operator-run, not agent-run).** Install on R6, drive the fault simulator to produce an
upstream-5xx burst, and show the alarm moving CLEAR → WARNING in netdata's alarm log with the
timestamps, then back to CLEAR.

## 4. Do-not-regress

| Item | Lane | Re-check |
|---|---|---|
| default-deny `other` projection | L5 adds `conversion_dropped` (public) | `TestOtherProjectionIsFullyClassified`, `TestInternalOtherKeys_NoPublicField` |
| `relay.go` untouched (retry gate, breaker, headroom, re-selection) | none — L5's error path is explicitly out of scope | `git diff` shows no `relay.go` hunk |
| `quota.go` / `PreConsumeQuota` / `PostConsumeQuota` untouched | none | `git diff` empty for those files |
| `channel_select.go`, `channel_cache.go`, `session_affinity.go`, `smart_routing.go` untouched | L8 only *reads* the affinity API | unchanged |
| hash-chained audit: rows only via `RecordAuditEvent` | L1, L2, L3 | `VerifyAuditChainV2` green on UAT after each |
| `TestAdminWriteRoutesAreAudited` names no new unaudited route | L2 | that test |
| `TestV2IDOR_Completeness` | L1 (grant by id), L2 (channel by id) | that test |
| abort-code structural gate; OpenAPI enum/path lock | L2, L3 (new codes) | `abort_code_structural_test`, `openapi_contract_lock_test` |
| migration contiguity | L1 (037) | `TestEmbeddedFS_VersionsAreContiguous` |
| `authHelper` per-request status/role re-validation | L2 calls it first, never bypasses | `TestRootOrGranted_*` |
| cycle-8 grant semantics: global (`tenant_id IS NULL`) only | L1 adds expiry, does **not** widen scope | the tenant-scope rejection test |
| `r5c_status_capability_test.go` passkey-absence lock | none | unchanged |
| i18n: v2 console uses `tr(key, fallback)` and does **not** edit locale JSON | L6, L7, L8 | locale files unchanged in the diff |

## 5. Deliberately not doing

- **System instance registry / `system_instances` table** (console-ux-20, ops-deploy-docs-07). The
  gap is real — `common.InstanceID()` and `common.IsLeader()` exist and both per-pod endpoints
  disclaim cluster awareness. Deferred on deliverability: ~20 files across six packages (a new GORM
  entity must be registered in three independent places), and it competes for 037 against grant
  expiry, which is a two-line schema change guarding a live capability. Cycle 10.
- **Runtime/GC admin endpoint and forced `runtime.GC()`** — premise disproved (§1).
- **Rankings-cache reset route** — the TTL is five minutes; waiting it out is not an incident.
- **Session-scoped `GET /api/task/self/:task_id/artifacts`** — there is no task page anywhere in
  `web/src`, so it would ship with zero consumers. Pair it with a page or not at all.
- **Admin cross-tenant top-up history** — `GET /api/v2/admin/logs/export` is already root-gated and
  filterable by tenant and type; the delta is ergonomics.
- **Any index on `logs.other`** (§1), **generic conversion registry** (wire-formats-12 — rewriting
  six converters against no failing test), **HTTP/2 sharding**, **affinity regex rules and
  switch-on-success toggles** (they land on `session_affinity.go`/`channel_select.go`),
  **new channel types** (providers-channels-14/16/17), **`MultiKeyModeRotating`** (void, §1),
  **JS billing-expression engine**, **signed capability URLs for artefacts** (a net-new crypto
  primitive), **subscription/payment rows** (external blocker).

## 6. Owner items

- **O1 (L3).** Enrol a TOTP factor for the production root account, then flip
  `SECURE_VERIFICATION_REQUIRE_ENROLLMENT=true`. Until then the credential-free path stays open in
  production and is merely audited. This is the single highest-value action available to the owner
  this cycle and it costs one enrolment.
- **O2 (L9).** Netdata alarm recipients (`health_alarm_notify.conf` on the R6 host). Without one,
  "alerting exists" means "visible in the netdata API", not "someone is paged".
- **O3 (carried).** A real vendor-account channel on UAT — still the only way to prove the
  Responses compact/registry round trips from cycle 8 end to end.
- **O4 (carried).** Real per-vendor context-length thresholds and ratios; cycle 8's mechanism still
  ships empty.
- **O5 (L2 scope).** Confirm the sensitive-field set. It is now **eight** fields: `key`,
  `base_url`, `param_override`, `header_override`, the per-channel proxy setting, `type`, `other`
  and `openai_organization` (the last three added in the repair round — see §3 L2). Adding
  `models`/`group` would make ordinary channel administration require a grant; the plan
  deliberately leaves them out. Still an owner decision: whether `OtherSettings`/`json:"settings"`
  should join the set in a later cycle.

## 7. Verification protocol

**Local gates (before push; lint and the full suite never run concurrently).**

```
go vet ./... && go build ./...
go test -short -p 2 ./...
go test -run 'TestV2IDOR_Completeness|TestConsoleCallsResolveToRegisteredRoutes|TestAdminWriteRoutesAreAudited|TestOtherProjectionIsFullyClassified|TestDeclaredSeriesHaveAProductionWriter|TestAllAuditActions_|TestOpenAPIContract_|TestAbortWithOpenAiMessage_EveryCallSiteCarriesACode|TestEmbeddedFS_VersionsAreContiguous|TestNoAlertFileClaimsDeploymentItDoesNotHave' -p 2 ./...
golangci-lint run --new-from-rev=origin/main ./internal/... ./cmd/...
cd web && bun run lint && bun run eslint && bun test && bun run build
```

`-race` and the coverage ratchet are CI-only; a lane is not green until the PR's CI run says so.
Every `-run` filter must match more than zero tests, and the count is recorded.

**Mutation checks.** Each lane shows its named oracle red on the reverted behaviour, then green on
a byte-identical restore. Commit before mutating; never `git checkout --` over uncommitted work.
L1 drop the expiry clause → `_ExpiredIsNotActive`. L2 remove one field from the predicate →
`_FieldSetIsExhaustive`; remove the v1 call site → `_V1_NonRootAdminWithoutGrant403`. L3 delete the
audit call → `_WritesCredentialFreeAuditRow`. L4 delete the guard call → `_RefusesPrivateAddress`.
L5 stop stashing the list → `_ReportsDroppedFields`; add the key without classifying it →
`TestOtherProjectionIsFullyClassified`. L6 drop the time bound → the filter test. L7 unquote the
identifier → `_QuotesTheReservedIdentifier`. L8 hard-code a panel number → the mount test.
L9 rename a metric in the alarm file → `TestNetdataAlarmsNameOnlyLiveSeries`.

**UAT probes.** Per lane, §3. Preconditions: bridge token from the UAT secret; the digest recorded
before and after each probe (ArgoCD may converge mid-probe — compare pod `startTime` with artefact
timestamps); `VerifyAuditChainV2` green after every lane that writes audit rows. Production stays
read-only: the only production commands this cycle are the two `SELECT`s already run in §2.

**Landing.** One commit per lane in the order L1 → L9, one PR, `main` only, ArgoCD auto-pin.
Contested files and their single owners: `governance/audit_action.go` — L2 owns, L3 appends after
it; `internal/pkg/metrics/metrics.go` — L4 only; `internal/adapter/repo/log.go` — L7 only;
`web/src/App.jsx` and the nav shell — L8 only; locale JSON — nobody.

## 8. Operator rulings after the first acceptance round (2026-09-15, binding)

Every finding below was checked against the tree before ruling. Findings are cited by the
acceptor ids in the workflow journal (`A` = tests/mutation acceptor, `B` = buyer/reference
acceptor). A ruling of ACCEPT means the repair lane implements the acceptor's expected fix as
written unless the ruling narrows or widens it.

### Correction to §4/§7 that applies to every frontend lane

The line "locale JSON — nobody" was wrong. Verified 2026-09-15: `web/src/i18n/locales/{en,zh}.json`
nest every v2 console key under `translation.console.*` — `console.admin.authz.*` carries 23 keys in
both files and cycle 8's `console.admin.system_tasks.*` keys are there too. The v2 convention is
therefore **both**: `tr(key, fallback)` in the JSX **and** the key in both locale files with a real
Chinese value. L1, L6, L7 and L8 add their keys to both files; the i18n integrity gate
(`every console.* key present in zh.json also resolves in en.json`) is the oracle. Lanes run
sequentially, so the shared file is not a collision.

### L1 — grant expiry

- **R1 (A-1, B-1).** The in-transaction recycle of an expired row must be auditable.
  `CreatePermissionGrant` returns the recycled grant ids; `CreateGrantV2` records one
  `ActionPermissionRevoked` per id **after** the transaction commits, actor = the requesting admin,
  detail `{grant_id, grantee_user_id, resource, action, tenant_id: null, reason: "superseded_expired"}`.
  Never record inside the tx closure. Oracle: re-grant after expiry through the handler → a
  `permission_revoked` row naming the old id **and** the `permission_granted` row both exist.
- **R2 (A-2, A-3, A-5).** Handler-level oracles for: `expired` true/false on the list; `expires_at`
  on the 201 body (ttl → `created_at + ttl`, no ttl → null); exactly one active row after re-grant.
- **R3 (A-4, B-3).** REAL-CHAIN: `TestRootOrGranted_ExpiredGrant403` in
  `internal/adapter/middleware/root_or_granted_test.go` **and** an expired-grant case in
  `internal/adapter/handler/router/audit_routes_root_or_granted_test.go` through the real router.
  Retitle the handler test's "REAL-CHAIN oracle" comment to what it is.
- **R4 (A-6, B-6).** Console sends `ttl_seconds` whenever the field is non-empty and parses; the
  server rejects 0. Vitest case.
- **R5 (B-2).** Add the four keys under `console.admin.authz` in both locale files (see the
  correction above).
- **R6 (A-7, A-8, B-4, B-5).** Migration header: "a nullable `*int64` field (`ExpiresAt`), like
  `RevokedAt` on the same struct, maps to BIGINT"; the `channel:sensitive_write` sentence may stay in
  the present tense only because L2 is now on the tree — the repair verifies `catalog.go` carries it.
  Integration guide: `越界返回 **400**`.
- `RevokePermissionGrantsForUser` also sweeping already-expired rows: accepted as-is.

### L2 — channel:sensitive_write

- **R1 (A-1, B-1).** Value-diff, not presence, for `base_url`, `param_override`, `header_override`
  (nil and "" equal). `key` stays presence-based (the console never sends it on update; multi-key
  append makes a value diff wrong). Oracles: the exact console-shaped rename body
  (`web/src/pages/v2/Channel/index.jsx:445-455`) by a non-root admin without a grant → 200 and the
  name persisted; the same body with a different `base_url` → 403 and the row unchanged.
- **R2 (B-3).** Widen the sensitive set by `type`, `other` and `openai_organization`, all value-diffed:
  `type` changes the derived upstream host when `base_url` is empty; `other` feeds
  api_version/region/plugin/bot_id in `distributor.go`; `openai_organization` rides as a header on the
  credential. `settings` stays covered only through its proxy member — name the remaining settings
  members as non-goals in the file header. Plan O5 is amended accordingly.
- **R3 (B-4).** Gate `CopyChannel` (existing = nil, the clone as the request) and the
  `delete_key` / `delete_disabled_keys` branches of `ManageMultiKeys`. Tests for both.
- **R4 (B-5).** Gate `EditTagChannels` when the body carries `base_url`, `param_override`,
  `header_override` or `key` — presence-based here, because the tag editor applies one value to many
  rows. Test. The "tag editor out of scope" non-goal is withdrawn.
- **R5 (B-7, A-4).** One test per API family through the **real** registration
  (`router.SetApiRouter` for v1 `PUT /api/channel/`, `router.SetApiV2Router` for v2) with a session
  whose role comes from the seeded user row (pattern: `root_seal_test.go`, `v2_completeness_test.go`
  in the router package). Reword the test-file header to what it drives.
- **R6 (A-3).** Test that forces the grant lookup to error → 403 and no mutation.
- **R7 (B-6).** Replace the falsely named unknown-resource case with a genuinely unknown pair
  (`wallet`/`write`); add a positive 201 for `channel:sensitive_write` with a seeded admin grantee.
- **R8 (B-8).** Rename the table test; add a reflection guard over the request struct's json tags —
  every field is in the sensitive set or the explicit non-sensitive set, else the test fails.
- **R9 (A-5).** `TestCatalog_SliceMatchesMap`.
- **R10 (A-2, B-2).** Integration guide: both catalogue entries; a 403 subsection next to the
  channel-save error-shape paragraph naming every gated route (after R3/R4: v1 POST/PUT, tag editor,
  copy, multi-key delete; v2 POST/PUT).
- **R11 (B-9, B-10).** Name all three helper callers. Add the one-line comment pinning the gate to
  the AdminAuth-mounted routes; do **not** widen the root check to JWT roles.

### L3 — step-up

- **R1 (A-1).** REAL-CHAIN subtest on `buildSecureVerifyFlowRouter` with the flag on:
  `POST /api/verify` → 403 `STEP_UP_ENROLLMENT_REQUIRED`; `POST /api/channel/1/key` with the cookies →
  403 `VERIFICATION_REQUIRED`.
- **R2 (A-2, B-8).** Add the action to `TestIsValidAuditAction`.
- **R3 (A-3, B-1).** Status oracle for the verified=true branch under both flag states.
- **R4 (A-4, B-2, A-5, A-6, B-6, B-3).** Prose: "handed to the audit writer on every pass through
  this branch; the write is a best-effort background insert"; drop always/unconditionally/every; name
  the four mount points; fix the `.env.example` self-contradiction; append the flag clause to
  `v2_admin_security.go:77-81`.
- **R5 (A-7).** `t.Cleanup` restoring a no-op audit writer.
- **R6 (B-7).** In the flag-on refusal, record `governance.ActionAuthFailed` with
  `{"step":"secure_verify","reason":"enrollment_required"}` — the throttle precedent in the same
  function — no new action. Assert it.
- **R7 (A-10, B-5).** AMENDMENT — the lane now also owns `web/src/services/secureVerification.js`
  (+ its test) and `web/src/hooks/common/useSecureVerification.jsx`: return `enrollmentRequired` from
  `checkAvailableVerificationMethods`, `hasSession = !totpEnrolled && !enrollmentRequired`, fix the
  service doc comment, vitest for the enable-2FA-first path.
- **R8 (B-4).** Integration guide §A row for `POST /api/verify` + `GET /api/verify/status` (flag,
  audit action, 403 code), ending `目前没有兄弟产品接入这组端点`. Not `relay.json`.
- **R9 (B-9).** Document the accepted literals (`true/false/1/0`) in `.env.example`. No `/api/health`
  change.
- **R10 (A-8).** The PR-body claim is corrected: `enrollment_required` is on `GET /api/verify/status`
  only.

### L4 — VideoProxy egress parity

- **R1 (A-1, B-4).** Add `RelayMidjourneyImage` (`internal/app/relay/mjproxy_handler.go`) to the
  registry; widen the assertion to accept either `ValidateOutboundURL(` or
  `ValidateURLWithFetchSetting(`; the comment names the semantic difference. Unifying the mj guard is
  out of scope — do not edit `mjproxy_handler.go`.
- **R2 (B-5).** Keep fail-closed DNS resolution (same posture as the artefact route). Document it in
  the integration guide row and the `video_proxy.go` comment; add a test with a proxied channel and an
  unresolvable host asserting the explicit 502 `artifact_request_rejected`.
- **R3 (B-6).** Log scheme + host + path only (`url.Redacted()` keeps query strings, and Gemini
  carries the key there); apply to the adjacent sinks in the same file.
- **R4 (A-3, B-7).** Rename to `TestVideoProxy_RefusesDomainResolvingToPrivateAddress`.
- **R5 (A-4).** Hoist the message to a package const in `task_media_guard.go`, both call sites use
  it, equality assertion in `TestTaskMediaRoutes_BothCallTheEgressGuard`.
- **R6 (B-1, B-2, B-3).** Integration guide :97/:98 corrected and the new 502 documented;
  `relay.json` gains the 502 response on `/v1/videos/{task_id}/content` mirroring the artefact path.
- **R7 (B-8).** `ssrf_guard.go` doc comment: enumerate the two task-media routes and qualify the
  latency sentence (allowed outside the file list).
- **R8 (A-5).** Count-free wording in `metrics.go`.
- **R9 (A-2).** Consumer note corrected: `2l-bs-docs` enumerates the route in six locale overview
  pages; the cross-repo follow-up asks whether the public docs should note the 502 class.

### L5 — conversion-fidelity diagnostics

- **R1 (A-1).** The classification entry must be enforced. In `log_other_projection_lock_test.go`
  tie the two maps: every `wantUserVisible` key that appears in `governance.FieldClassification`
  must be `TierPublic`, every `wantInternal` key that appears there must be `TierInternal`, **and**
  `conversion_dropped` must be present in `FieldClassification` — deleting the `classification.go`
  line or flipping its tier turns the build red.
- **R2 (A-2, B-7).** REAL-CHAIN: one test in the `openai` provider package (it may import `app`
  without a cycle) that calls `(&openai.Adaptor{}).ConvertClaudeRequest(c, info, req)` with a
  `top_k`-bearing request and then `app.GenerateTextOtherInfo(c, info, ...)` over the **same**
  `*RelayInfo`, asserting `other["conversion_dropped"] == ["top_k"]`. No hand-seeded field.
- **R3 (A-5, B-1) — scope widened.** Stale diagnostics across retries are a real correctness
  defect. Verified 2026-09-15: every relay handler calls `info.InitChannelMeta(c)`
  (`relay_info.go:232`) at the start of each attempt, after `getChannel` →
  `SetupContextForSelectedChannel` has re-pointed the gin context at the re-selected channel. That
  is the per-attempt seam and it is a lane file: **reset `info.ConversionDropped = nil` inside
  `InitChannelMeta`**. Oracle: drive two attempts over one `RelayInfo` — converter attempt sets the
  list, a second `InitChannelMeta` clears it, a native-format attempt leaves it empty — and assert
  the settled projection reflects the last attempt. Do not edit `relay.go` or the native adaptors.
- **R4 (A-3, B-3).** Gemini `tools` entries with no `functionDeclarations` are reported with the
  dotted wire names `tools.googleSearch`, `tools.googleSearchRetrieval`, `tools.codeExecution`,
  `tools.urlContext` (each ≤ 32 bytes). Exact-set test with a `googleSearch`-only body.
- **R5 (B-2).** Report `thinking` when `r.Thinking != nil` and the branch produced neither
  `Reasoning` nor a model-name change — computed from the branch's outcome, never from the channel
  type (that would leak upstream identity into a public key). Test on a non-OpenRouter `RelayInfo`
  with a model name lacking the suffix.
- **R6 (B-4).** Report `requests` when `len(r.Requests) > 0`. Test.
- **R7 (B-5).** Report `tools` for the Claude converter when any incoming tool carries a non-function
  `type` or a `cache_control` block (the conversion keeps only name/description/schema). Test.
- **R8 (B-6).** `metadata` is reported only when it carries keys other than `user_id` — newhub
  consumes `metadata.user_id` for `other.end_user` (`relay_info.go` `deriveEndUserHash`), so
  reporting it as dropped would send customers to a ticket about attribution that works. Comment and
  guide say so. Test both shapes.
- **R9 (A-4, B-8).** Test the 32-byte cap through `boundDroppedFields` with a long name; publish the
  truncation sentinel's literal in guide §J. Keep the cap.
- **R10 (A-7, B-9, the OVERSTATED list).** Prose: drop every absolute; state precisely which
  categories are covered (top-level unread fields; the named partial/conditional cases above) and
  which residuals remain after R4–R8 (name them in guide §J so "key absent" is not sold as full
  fidelity); `relay_info.go` comment softened to cite the error path by file:line instead of "never".
- **R11 (A-6).** Report accuracy only: 9 new tests, `log_info_generate_test.go` is a lane file.

### L6 — console request-id search

- **R1 (A-1, B-1).** When `upstreamRequestId` is non-empty, `fetchLogs` must go to the tenant-wide
  route (`/logs/all`) — the only route that binds the parameter — and the page reflects
  `tenantWide = true` so the visible scope matches. The input renders only for admins. Oracle asserts
  the URL contains `/logs/all?`, the parameter, and a bounded `start_time`. Tighten `u.includes('/logs')`
  to the exact prefix.
- **R2 (B-2).** Anchor the implicit lookback on the **end** bound when one is set, so
  `start_time <= end_time` always holds; test the end-only + id case.
- **R3 (B-3, A-6).** Show the effective window while an id filter is active
  (`tr('console.log.id_window_hint', …)`); test presence with an id and absence without.
- **R4 (B-4).** Seed both filters from the URL query like the sibling filters; test.
- **R5 (B-5).** Pass the same effective `start_time` to `fetchStat`; label the header while an id
  filter is active; test that the two calls carry the same `start_time`.
- **R6 (B-6).** Trim both ids. Test a padded value.
- **R7 (A-3).** Paging and both toggles must carry the id filter and the bound; test page 2.
- **R8 (A-2, A-4, A-5, B-7).** Prose to what the code does (name the route, drop "always"/"every").
- **R9 (locale correction).** Add every new `console.log.*` key to `en.json` and `zh.json` under the
  nested `console.log` block with real Chinese values.

### L7 — rankings by group

- **R1 (A-1, B-1).** The SQLite claim is false and both comment blocks are rewritten: `group` is
  reserved in Postgres **and** SQLite, the quoted identifier is required on both, and the DryRun test
  exists to pin the exact production SQL. The sentences "accepts it either way" and "cannot see that
  failure at all" must not survive anywhere in the tree.
- **R2 (B-2).** No second copy of the identifier. Build the expression from the existing SSOT
  `logGroupCol` (`repo/main.go`), make the hermetic tier initialise it the way production boot does
  (`InitCol()` in the SQLite test setup), and add the guard test that the rankings expression
  contains `logGroupCol`.
- **R3 (B-7).** The quoting oracle asserts on the `GROUP BY` clause, not on the identifier appearing
  anywhere.
- **R4 (B-6, A-5).** Admin-route `by=group` test through `setupAnalyticsRouter` with two tenants:
  unfiltered merges both, `tenant_id=A` returns only A.
- **R5 (A-2).** REAL-CHAIN: one test through `router.SetApiV2Router` for
  `GET /api/v2/:tenant_slug/analytics/rankings?by=group`; the hand-seeded harness comment is
  reworded to what it drives.
- **R6 (B-3, B-4, B-5, B-9, A-3, A-4).** Docs and comments: the dimension is the request's
  **using group** (token/user group, may change on cross-group retry), not the channel group; drop the
  index claim; `model|vendor|group` at all four doc sites and the guide's route-table row; publish the
  old and new 400 message strings.
- **R7 (B-8).** Name the `(ungrouped)` collision as a known non-goal in the const comment and §K.
- **R8 (B-10, locale correction).** Add `console.rankings.by_group` and update
  `console.rankings.sub` in `en.json` and `zh.json`; touch the other locale files only if they
  already carry the `console.rankings` block (mirror whatever the sibling keys do there).
- **R9 (A-6, A-7).** Drop `/renderSQL` and the authoring-order clause.

### L8 — admin diagnostics page

- **R1 (A-1).** Capture the `ConfirmDialog` props in the mock and assert
  `confirmText === 'PURGE ALL'`; a missing prop makes purge-all permanently unusable in production.
- **R2 (B-1, A-4).** `Promise.allSettled` with a per-panel state `ok | forbidden | error`; a failed
  panel renders an explicit unavailable marker, never `no`/`0.0%` defaults; the other panel still
  renders. Test with `totp-stats` rejecting 500.
- **R3 (B-2).** `mem entries` renders only when `backend === 'memory'`; otherwise a sub-label
  explaining it is the in-process fallback map. Test the redis case.
- **R4 (B-3).** `doPurgeAll` wraps in try/catch like the `ModelRateLimits` precedent; test that a
  rejected purge leaves the dialog open.
- **R5 (A-3, A-5, A-6).** Cover the 403 branch; assert the dialog closes after success and
  `enabled: false` renders `no`; purge-one shows the server message on a non-204 response.
- **R6 (A-2, B-4, B-5).** Reword the two absolutes to the scoped, cited form; render `scope` from the
  response; the acceptance line becomes "nav entry hidden below role 100, page renders a refusal when
  the server refuses".
- **R7 (locale correction).** Add every new `console.admin.diagnostics.*` key (and the nav label
  key) to `en.json` and `zh.json`.

### L9 — netdata alarms: the premise was incomplete, and the lane's output would never bind

Verified on the R6 host on 2026-09-15 (read-only):

- **A `newhub.conf` already exists on the host** —
  `/data/obs-pack/runtime/netdata/config/health.d/newhub.conf`, 173 lines, mtime 2026-08-20, bind-mounted
  read-only into the `obs-netdata` container at `/etc/netdata/health.d/newhub.conf` **per file** (a new
  file dropped into that directory is not mounted). It carries eight `template:` alarms ported from
  `deploy/grafana/newhub-alerts.yaml` — a repo file that commit `e0425425` deleted, so the host file's
  own "tuning SOT" no longer exists. Nothing in the repo owns it today.
- **Chart naming is `on: prometheus.newhub.<metric>`** (context) with chart ids
  `prometheus_newhub.<metric>-<label>=<value>…`, one chart per label-set, single dimension, counters
  rate-converted by go.d, **`update_every = 10`**. The lane wrote `prometheus_local_newhub.<metric>`
  and `alarm:` blocks with `lookup: sum -5m` — none of its three alarms would ever bind, and the
  arithmetic assumes a 1 s interval.
- **Binding status via the netdata API:** four of the eight host templates are bound and CLEAR
  (`platform_breaker_open`, `billing_outbox_failures`, `billing_outbox_backlog`,
  `relay_5xx_elevated`); four are **unbound** because no chart matches (`credit_pool`,
  `channel_breaker_open`, `cost_spike_429`, `quota_cap_402`) — dead alarms, exactly what the repo
  oracle exists to catch. `relay_5xx_elevated` is bound only to the
  `path=/api/health status=503` chart, i.e. it watches health checks, not relay traffic.
- **Notification is configured:** `SEND_EMAIL="NO"`, `SEND_CUSTOM="YES"`,
  `role_recipients_custom[sysadmin]="default"` — a custom webhook sender exists. O2 is therefore
  "verify where the custom sender delivers", not "no recipient".

Rulings, replacing the lane's shape:

- **R1.** **Adopt the host file into the repo.** `deploy/r6-host-netdata/health.d/newhub.conf` becomes
  a faithful copy of the live file (the operator provides it — the repair agent must not connect to
  R6), then the three new alarms are **merged into it** in the same `template:` /
  `on: prometheus.newhub.<metric>` / `chart labels:` / `lookup: average -Nm` (rate, `units: …/s`)
  model the file already uses. The `README.md` states the provenance (host copy of 2026-08-20, adopted
  2026-09-15) and that the repo is now the source of truth.
- **R2 (A-1, B-2, B-1, B-3).** The oracle reads the **`on:` line** of every `template:`/`alarm:`
  block, strips the `prometheus.newhub.` prefix, and requires the remainder to be a series the repo
  both declares and writes, building the wire name from the parsed `Namespace`/`Subsystem`/`Name`
  literals of each `promauto` block (no fabricated `lurus_gateway_` prefix). The `# series:` annotation
  becomes a cross-check that must equal the `on:` metric, and every block must carry one. Dead
  templates in the adopted file are handled by the oracle, not by hand: `chart labels:` filters whose
  label **values** the code never emits (e.g. a `status=429` on `requests_total` if the recorder never
  writes it) are flagged by a second check that parses the label filter and asserts the label key is
  one the declared vector carries; a template the oracle cannot prove live is either fixed or moved
  under a `# DEAD (reason, date)` section that the oracle skips and the README lists.
- **R3 (B-4, B-11).** Header, README and every runbook state: the scrape is one NodePort round-robin
  across three replicas at `update_every = 10`; rates are per-replica samples, so thresholds are
  calibrated on the live probe, not derived. Use `lookup: average -Nm` on the rate-converted
  dimension, never `sum` of samples as an event count.
- **R4 (B-5, A-7, B-8).** `scripts/install-netdata-alarms.sh` writes the file to
  `/data/obs-pack/runtime/netdata/config/health.d/newhub.conf` (the bind source), reloads with
  `docker exec obs-netdata netdatacli reload-health`, fails loudly when the container is absent, and
  requires root only when the destination is not writable. Delete the stock-config fallback prose.
- **R5 (A-3).** Honest live-state prose: the conf header and INDEX rows say "adopted from the host
  copy of 2026-08-20; re-installed from the repo — pending until the operator's probe" with a date;
  the honesty test requires the installer name **and** a dated status line, not the absence of the
  reference-only marker.
- **R6 (A-4, B-7).** Implement the runbook gate: every `template:`/`alarm:` block's `info:` (or a
  `# runbook:` line) names a `doc/runbook/*.md` that exists and is linked from `INDEX.md`. The adopted
  file's existing eight alarms get runbook pointers too (short pages are acceptable; a dead template
  gets no runbook and is listed as dead).
- **R7 (A-5, B-10).** Fix `declaredMetricVarRe` in `declared_series_written_test.go` with
  `(?:var\s+)?` and reuse it; if the widened gate exposes a declared-but-unwritten series, report it
  as a finding, do not weaken the regex.
- **R8 (A-6).** `rate_limit_degraded` `info:` and runbook say "either a fail-open admission or a lost
  success recording; read the `check` label"; narrow the label filter only if the emitted label values
  are proved from `r6_rate_limit_degraded.go`.
- **R9 (B-6).** Runbooks name the real route `PUT /api/v2/{tenant_slug}/channels/{id}` and the
  integer status value.
- **R10 (B-9, the hygiene list).** Runbook prose: cite the three call sites and their branches;
  distinguish the fail-closed web/API limiters from the fail-open relay limiters by file:line.
- **R11 (A-8).** The report states how the local rehearsal bypassed the root check.
- **O2 is amended:** the operator verifies where `SEND_CUSTOM` delivers during the probe and records
  it in the README; until then "alerting exists" means "visible in the netdata API and sent to the
  custom sender".
