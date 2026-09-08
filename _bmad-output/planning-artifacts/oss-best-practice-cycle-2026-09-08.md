# OSS best-practice cycle — 2026-09-08

Scope: `2b-svc-newhub` only. Base: `main` @ `1bd6a20f` (2026-09-08). Mandate: borrow the
practices sibling products and outside integrators touch first (self-describing limits, one
request identity, vendor error taxonomy, budgets that renew, honest observability), un-dead the
money primitives that already have columns and publishers, and lock the wire contract so it
stops rotting. Six lanes run sequentially on the same tree by separate agents; file sets are
disjoint (verified in §8). Lane numbering is the binding execution order (L1 before L3, L2
before L3, L2 before L5 — see each lane's spec).

Untouchable (someone else's WIP, never stage/read/delete as ours):
`internal/app/coverage_lift_test.go`, `internal/app/coverage_lift_tokens_test.go`,
`internal/app/coverage_seam2_extra_test.go`, `internal/app/coverage_seam_extra_test.go`,
`internal/app/openrouter_sync/coverage_extra_test.go`.

Hard rules carried by every lane: no git operations, no production/R6 access, no SQL
migration this cycle (ledger ID 033 stays `(next available)`, root
`doc/coord/migration-ledger.md` line 134 verified untouched), `go test -count=1 -p 2 <pkgs>`
only (no `./...`, no `-race`, no linter locally), web gates (`cd web && bun test && bun run
lint && bun run eslint && bun run check:casing` — scripts verified in `web/package.json:52-58`)
must stay green even though no lane touches `web/`, every hot-path REJECT ships default-observe
behind a flag with a live-written counter, old error bodies still parse on the inbound side
(`internal/app/channel.go:88-120`), no AI model names or assistant-tool names in any file, every
"passed" claim comes with the command and its tail output, `git status --short` after each lane
shows only that lane's files plus the five untouchable files.

How this plan was synthesised: three lens plans (architect / buyer / operator) were scored by two
judges. Summed totals: operator 91, buyer 86.5, architect 80. The operator plan is the base.
Grafted (valued by both judges or the catalog): buyer key-holder self-serve lane (row identity,
`/v1/generation`, `/v1/key`), the inbound `X-Request-Id` step of the architect identity lane,
the bidirectional OpenAPI drift lock of the buyer doc lane, the operator NATS-off dedup design
for the pool alert, and the operator channel-save validation lane. Dropped with the judges'
concrete reasons: architect L3 (39 files + false "zero test callers" claim on
`helper.SetRateLimitHeaders`, the caller is `perception_extra_test.go:144-152`), architect L1
(bundles a new allow-list feature into the taxonomy owner and omits the existing type locks),
architect L4 / buyer L-E (audit storm / parallel dedup beside the fail-closed publisher), buyer
L-D (spends migration 033 on always-on money-path writes with an out-of-repo precondition),
operator L6 (log retention: ops-facing, no hermetic wiring oracle, no growth pressure at 24k
rows). Every file:line below was re-opened on HEAD 1bd6a20f during synthesis unless marked
"catalog, not re-opened".

## 1. Thesis

newhub already owns the hard parts of an enterprise gateway (stream-safe failover with a
suppression metric, an upstream-only breaker, per-attempt route trace, wire-semantic cache
pricing, per-product attribution to wallet/logs/metrics, a hash-chained audit trail). What it
lacks is the layer a finance team and a sibling SDK actually touch: limits that announce their
headroom before the first 429, an id the caller mints and support finds on the row, a key that
can describe its own limits and cost, rejections typed the way vendor SDKs already branch on,
a tenant pool that renews and warns instead of sliding silently into 402, the two latency and
cache series operators need before any enforce decision, and channel configuration that fails
at save time instead of at relay time. With one live upstream model and no customer traffic,
the highest value per unit of blast radius is additive, flag-gated and documented: this cycle
lands six lanes with zero migrations, no new rejection on the relay chain (pool reset changes
nothing by default), one deletion (a dead header helper), and one drift lock so the contract
written in `docs/openapi/relay.json` and the integration guide can no longer diverge from the
route table or the error-code constants. Every relay.go-owning policy borrowing (retry backoff,
per-channel timeouts, generic key cooldown, guardrail modes) is deferred with its spec intact
because relay.go is spent on the one literal this cycle and none of them can be proven on UAT
beyond the fault simulator.

## 2. Reference projects studied (by the three lens plans; URLs as published by the projects)

| Project | What was borrowed / compared | URL |
|---|---|---|
| LiteLLM | X-RateLimit headroom headers, budget windows that reset, soft-budget alerts, team model access, `/key/info` | https://github.com/BerriAI/litellm |
| Portkey AI Gateway | request id echo, config validated at save time, retry/fallback policy as data, guardrail modes | https://github.com/Portkey-AI/gateway |
| OpenRouter | `/api/v1/generation?id=`, `/api/v1/key`, typed error metadata with provider name, `usage.cost` on every wire | https://openrouter.ai/docs |
| Helicone | request id echo, session / end-user dimensions on the row, TTFT | https://github.com/Helicone/helicone |
| Bifrost | vendor error taxonomy, pool alerts, key cooldown | https://github.com/maximhq/bifrost |
| Kong AI Gateway | X-RateLimit vocabulary with Reset, token-based windows, per-key health | https://docs.konghq.com/hub/kong-inc/ai-proxy/ |
| Higress | ai-security-guard observe vs deny, token budgets per day | https://github.com/alibaba/higress |
| Envoy AI Gateway | token-based rate-limit windows, secret handling at rest | https://github.com/envoyproxy/ai-gateway |
| OpenTelemetry GenAI semantic conventions | `gen_ai.server.time_to_first_token` histogram | https://opentelemetry.io/docs/specs/semconv/gen-ai/ |
| Langfuse | session / user cost attribution, cache token accounting | https://github.com/langfuse/langfuse |
| TensorZero | per-target retry + timeout classes | https://github.com/tensorzero/tensorzero |
| One API / New API (upstream base) | log retention knob, variant pricing, task polling bounds | https://github.com/songquanpeng/one-api , https://github.com/QuantumNous/new-api |

## 3. Capability scorecard vs best-of-breed (catalog, HEAD 1bd6a20f)

| Area | newhub today | Best-of-breed | Evidence (opened this synthesis unless noted) | Lanes this cycle | Expected after (an expectation, not a measurement — re-scored next cycle) |
|---|---|---|---|---|---|
| routing_resilience | 6 | Portkey / LiteLLM: per-target retry+timeout+fallback as data, backoff, Retry-After honoured | Has: stream-safety gate `relay.go:610` (`RecordFailoverSuppressed`), breaker fed only by `IsUpstreamFailure` `relay.go:462,478`, `RetryAfterUnix` → Retry-After `relay.go:178-200`. Lacks (catalog, not re-opened): no sleep/backoff in the loop, `RetryTimes` default 0, no same-tier exclusion, global-only timeouts | none (deferred C11/C12/C13/C22) | 6 |
| governance_keys_budgets | 5.5 | LiteLLM: budget windows that reset, soft-budget alerts, team model access | Has: three rate-limit scopes with fail-open `business_rate_limit.go:302-358`, credit pools with ledger `repo/tenant_credit_pool.go:122-228`. Dead: `NextResetAt` written only at create (`repo/tenant_credit_pool.go:92`; non-test grep = that line + the entity field), `ShouldAlert` has no caller outside the entity (`entity/tenant_credit_pool.go:83`), `PublishPoolThreshold` has no non-test caller (`nats/pool_threshold.go:134`; control `checkAndPublishQuotaThresholds` called at `quota.go:1252`). No headroom headers on admitted responses (`rate-limit.go:188-192` only from reject sites). tpm_limit counts settled quota (`quota.go:944-960`) while the entity promises tokens (`entity/token.go:44-47`) | L1, L2 (`/v1/key`), L4 | 7 |
| billing_cost_transparency | 6 | OpenRouter: cost on every wire, generation lookup, priced models | Has: cost headers `perception.go:134-151`, `x_lurus` extension. Lacks: request id never persisted on the row (`governance.go:95-113` writes data_flow_* and source_product only), no lookup by id, `/v1/models` price-blind (catalog) | L2 (`/v1/generation`) | 6.5 |
| observability | 5.5 | Helicone + OTel GenAI: sessions/users on the row, TTFT histograms | Has: honest product-labelled series through one chokepoint `relay_outcome.go:46-61`, default-deny projection gate `log_other_projection_lock_test.go:340`. Lacks: no TTFT series (only relay duration), no cache token totals in `/logs/stat` (`v2_log_stat.go:155,245`), no session/end-user/request id on the row | L2, L5 | 6.5 |
| security_guardrails | 6 | Kong / Higress: per-tenant patterns, observe vs deny | Has: sensitive-word rejection before channel selection (`relay.go:281-308`, catalog). Lacks: observe mode, tenant model allow-list, keys at rest (catalog) | none (deferred C15/C21/C23) | 6 |
| api_compat_dx | 5 | OpenRouter / LiteLLM: one header directory, typed errors, key introspection | `relay.json` has 51 empty `headers: {}` objects and 0 mentions of X-Request-Id/X-RateLimit; guide has 0 header rows (control: 2 X-Lurus-Product rows); inbound request id ignored (`request-id.go:11-18` always mints); 13 non-test `"new_api_error"` literal sites outside `types/error.go:37`; 29 code-less `abortWithOpenAiMessage` calls (auth 14 incl. the multi-line :506, distributor 9, jimeng 4, model-rate-limit 2) | L1, L2, L3 | 7 |
| operations_config | 6.5 | Portkey: config validated at save time | v2 channel update assigns override/mapping JSON raw (`v2_channel.go:466-483`, `json.Valid` count in that file = 0; control `validateChannelEgress` at :340,:500); legacy tag path checks bare `json.Valid` only (`channel.go:1043,1054`); log retention manual (catalog) | L6 | 7 |

## 4. Verification performed before ranking (HEAD 1bd6a20f)

Corrections to the lens plans that change specs below:

- `helper.SetRateLimitHeaders` (`internal/app/relay/helper/perception.go:157-166`) has exactly one
  caller and it is a test: `perception_extra_test.go:144-152` (`grep -rn SetRateLimitHeaders internal`
  = definition + those three test lines; control: `setRateLimitResponseHeaders` in
  `rate-limit.go:188-192` has callers at `rate-limit.go:61,80`, `business_rate_limit.go:238`,
  `concurrency_limit.go:197`, `model-rate-limit.go:196,223,267,277`). L1 deletes the helper AND
  the test case in the same lane.
- `business_rate_limit_test.go:147-148` (not :150) locks `type == "new_api_error"` on the 429
  body; L1 relaxes it before L3 changes the type.
- `model-rate-limit.go:197` and `:224` are code-less `abortWithOpenAiMessage` calls (the lens
  plans said the limiters "already pass codes"). They belong to L3 (typed code + scope header),
  so L1 does NOT touch `model-rate-limit.go`.
- Existing `new_api_error` test locks that flip when the type changes (grep, test files):
  `cover_r2_billing_test.go:582-583,614-615`, `relay_inband_error_test.go:107`,
  `business_rate_limit_test.go:147-148` (L1), `entitlement_product_test.go:132-133`,
  `rejection_envelope_wire_test.go:93-94` (the :61,:84,:122 assertions are "must NOT contain"
  and stay valid), `router/relay_wire_stamp_test.go:93` ("must not contain", stays valid),
  `app/error_test.go:94-130,178-179`, `r2_channel_token_quota_test.go:14-15,94`,
  `types/error_more_test.go:219,461`.
- The Anthropic-wire converter's `ErrorTypeOpenAIError` branch (`types/error.go:229-234`) renders
  `Type` from the OpenAI `Code`; the middleware helper (`utils.go:16-38`) builds its error through
  `WithOpenAIError` with the literal at `:30`. The root fix therefore routes the helper through
  `NewErrorWithStatusCode` (`error.go:312-325`, `errorType = ErrorTypeNewAPIError`) so BOTH
  converters' default branches (`:198-204`, `:238-243`) apply one status→type table.
- The Google-wire converter method exists (`error.go`, grep) and the Google-wire lock asserts only the envelope
  prefix and `"status":"UNAUTHENTICATED"` (`rejection_envelope_wire_test.go:75-80`), so the root
  fix does not disturb that wire.
- `RecordErrorLog` (`repo/log.go:394-395`) and `RecordConsumeLog` (`:445`) both take the gin
  context; L2 writes `other.request_id` there, covering the two error-row writers
  (`relay.go:730`, `middleware/utils.go:74`) and every consume path without touching
  `relay.go`/`utils.go`.
- `entity/log.go:21` carries the `token_id` index (not :10). `GetLogByKey` ownership rule is at
  `repo/log.go:254-285`.
- `quota.go:790-799` (pool debit success) and `:800-810` (overdraft) are the two debit branches;
  the pool row is loaded at `:764` via `repo.GetTenantCreditPool` with all columns, so
  `AlertThresholdPct`/`AlertFiredAt` are available for the alert hook.
- `publishPoolThreshold` (`nats/pool_threshold.go:180-235`) already separates dedup (steps 1-3)
  from publish (step 4); `PublishPoolThreshold` returns at `:135-141` before any dedup when NATS
  is disabled — the reason architect L4 / buyer L-E would storm audit rows on UAT.
- `credit_pool_reconcile.go:363-391` is the leader-gated 300s ticker; `main.go:283-296` starts it
  inside `if common.IsMasterNode`. L4 rides that ticker (no `main.go` edit, no lifecycle file).
- `internal-api-router.go:151-158` is the `ScopeAdmin` group with `rotate-due-tokens`;
  `internal_maintenance.go:23-44` is the handler pattern to mirror.
- `audit_action.go:105-112` billing action constants and the whitelist map at `:195-206`.
- `metrics.go:278-290` is the CreditPoolBalance "NOT ALERTED" comment;
  `alert_wiring_honesty_test.go` has `TestNoAlertNamedAsFiringInGoSource` (:287);
  `declared_series_written_test.go:146` walks the repo root (`filepath.Join("..","..","..")`).
- `relay.json` paths: `/v1/chat/completions` :186, `/v1/messages` :1236, `/v1/embeddings` :1474,
  `/v1/completions` :1514. Guide sections: A :85, B :98 (row :106 still names `new_api_error`),
  B2 :109, C :115, D :119.
- Root `doc/coord/contracts.md` (read-only, grep only): `X-RateLimit` = 0 hits, `pool.threshold`
  = 0 hits (control `X-Lurus-Product` = 2; `llm.quota.threshold` registered at :312), line 57 is
  the attribution note, the switch `/api/v2/switch/user/info` row sits in the block after :570.
- `deploy/k8s/r6-uat/deployment.yaml:123` sets `LLM_QUOTA_NATS_ENABLED` (NATS off on UAT);
  `faultsim.go:61-67` defines `slow_headers` / `http_500` / `rate_limit_429`.
- Harness names verified: `eachBizBackend` (:87), `bizTestLimits` (:32), `bizFreezeClock` (:59),
  `runBizRL` (:70) in `business_rate_limit_test.go`; `setupCoverDB`
  (`cover_helpers_test.go:39`), `mountWireRejectionRouter` (`rejection_envelope_wire_test.go:20`),
  `seedRelayUserToken` (`tenant_relay_guard_r3_test.go:70`); `setupSQLiteDB`
  (`repo/sqlite_testutil_test.go:25`); `TestUpdateChannelV2_PartialUpdate`
  (`v2_channel_test.go:172`); `TestRelay_SourceProductHeader_ReachesErrorLogRow`
  (`relay_attribution_test.go:76`); `routerRelayHasRoute` (`cov_router-relay_wiring_test.go:53`).

## 5. Ranked catalog (evidence, source projects, disposition)

| Rank | Id | Title | Evidence | Source projects | Disposition |
|---|---|---|---|---|---|
| 1 | C03-ERROR-TAXONOMY-L2 | vendor error types at the root, non-empty codes | `types/error.go:198-204,238-243` default branches stamp `string(e.errorType)`; `utils.go:30` literal; 29 code-less abort calls; 13 literal sites (list in §4) | Bifrost, OpenRouter, previous plan L2 | **L3** (metadata half deferred) |
| 2 | C02-HEADER-CONTRACT | inbound request id honoured, canonical header, documented | `request-id.go:11-18` mints unconditionally; no `GetHeader("X-Request-Id")`/`traceparent` read anywhere (grep = 0; control `GetHeader("X-Session-Id")` at `session_affinity.go:83`); `relay.json` 51 empty headers objects | Portkey, Helicone, LiteLLM | **L2** (request-id step) + **L3** (doc + lock); attempts/upstream-model header set deferred |
| 3 | C05-ROW-IDENTITY-LOOKUP | request_id/session_id/hashed end_user on the row, `/v1/generation`, `/logs` filters | `governance.go:95-113`; `entity/log.go:85-108` has no request/session filter; `dashboard.go:11-21` mounts only legacy billing under TokenAuth | OpenRouter, Helicone, Langfuse | **L2** |
| 4 | C01-RL-HEADROOM | headroom headers on admitted responses | `rate-limit.go:188-192` only from reject sites; `business_rate_limit.go:302-358` admit path writes nothing; dead helper `perception.go:157-166` | LiteLLM, Kong, Higress, Helicone | **L1** |
| 5 | C07-POOL-RESET-ALERT-UNDEAD | pool reset job + threshold alert wired | `repo/tenant_credit_pool.go:92`, `entity/tenant_credit_pool.go:83-89`, `nats/pool_threshold.go:134-153,180-235`, `quota.go:790-810` | LiteLLM, Bifrost | **L4** |
| 6 | C16-KEY-INTROSPECTION | `GET /v1/key` | `dashboard.go:17-20`; token fields `entity/token.go:9-58`; tenant limits `entity/tenant.go:27-33`; pool `repo/tenant_credit_pool.go:60`, `poolHealthForEndUser` `handler/tenant_credit_pool.go:132` | OpenRouter, LiteLLM | **L2** |
| 7 | C08-TTFT-CACHE-SERIES | TTFT histogram, cache totals in `/logs/stat` | `relay_outcome.go:46-61`; `relay_info.go:474` sentinel, `:557-559 HasSendResponse`; `v2_log_stat.go:155,245`; cache keys `log_info_generate.go:40,127` | OTel GenAI, Helicone, Langfuse | **L5** (tokens_processed cache types deferred: writer `repo/log.go:451` is L2's) |
| 8 | C14-CHANNEL-SAVE-VALIDATION | parse-then-save channel JSON | `v2_channel.go:466-483,487`; `channel.go:1043,1054`; `override.go:35,51,297,311` | Portkey | **L6** |
| 9 | C06-TPM-TOKENS-NOT-QUOTA | tpm_limit in tokens via dual window | `quota.go:944-960` records `totalQuota`; `entity/token.go:44-47` promises tokens; `business_tpm.go:185,221` | Kong, Envoy AI Gateway | deferred (spec kept, §12) |
| 10 | C24-TOKEN-PERIODIC-BUDGET | per-key renewing budget (migration 033) | catalog (`token.go:19-20` lifetime cap only; not re-opened beyond the struct) | Bifrost, LiteLLM | deferred (owner decision on 033 and on `budget_used` vs `RemainQuota`, §11) |
| 11 | C21-TENANT-MODEL-ALLOWLIST | tenant model allow-list observe-first | `tenant_config.go:117,175` JSON helpers exist; `distributor.go` model-limit block (abort sites :97,:107) | LiteLLM, Bifrost | deferred (needs L3's `model_blocked`; distributor.go is L3's) |
| 12 | C11-RETRY-POLICY | backoff, exclusion, opt-out | catalog (`relay.go:349-478`, `channel_cache.go:245-260`; not re-opened) | Portkey, Bifrost, TensorZero | deferred (relay.go owner) |
| 13 | C04-USAGE-EVERY-WIRE | `x_lurus` on Anthropic/Google wires, always-emit usage frame | catalog | OpenRouter, Bifrost | deferred (flag flip needs consumer confirmation) |
| 14 | C12-PER-CHANNEL-TIMEOUTS | per-channel header/idle/TTFT budgets | catalog | Portkey, TensorZero | deferred (after C11 and L5's TTFT distribution) |
| 15 | C15-GUARDRAIL-OBSERVE | guardrail modes, per-tenant patterns | catalog (`relay.go:272-308`) | Portkey, Kong, Higress | deferred |
| 16 | C13-KEY-COOLDOWN-GENERIC | provider-agnostic key cooldown | catalog | Kong, Bifrost, One API | deferred |
| 17 | C17-MODELS-PRICING-AVAILABILITY | priced `/v1/models`, honest catalogue | catalog | OpenRouter, LiteLLM | deferred (platform consumer note) |
| 18 | C22-LB-OBSERVABLE-MODEL-HEALTH | declared balancer, model health | catalog | Kong, LiteLLM | deferred |
| 19 | C23-CHANNEL-KEY-AT-REST | envelope encryption of channel keys | catalog | Envoy AI Gateway, Kong | deferred (KEK runbook first) |
| 20 | C20-LOG-RETENTION | scheduled retention, observe default | catalog (`handler/log.go:256`, `repo/log.go:738`) | One API | deferred (operator L6 design kept, §12) |
| 21 | C18-AUDIT-CHANGESETS | before/after in audit rows | catalog | LiteLLM | deferred |
| 22 | C19-TENANT-DAILY-BUDGET | tenant daily token window | catalog | Higress, Kong | deferred (needs C06) |
| 23 | C09-VARIANT-PRICING | variant pricing fallback | catalog | New API upstream | deferred |
| 24 | C10-TASK-POLL-BOUND | bounded task polling + refund | catalog | New API upstream | deferred |
| carried | OBS-R2, OBS-R4, BILL-FORMULA-1, AUTH-1, UX-MODELS-1, UX-SETTINGS-HONEST, TI-F2, DOC-1, TI-4, XP-3, BILL-C, ATTR-1, L1 seam-guard (corrected), v2 Redemption PUT | previous cycle §3/§6 | — | DOC-1 gets its first real lock in L3; others deferred (§12) |

## 6. Lanes (execution order is binding)

Common oracle rule: each lane's `go test -count=1 -p 2 <its test_packages>` must be red on the
named assertion before the code change and green after; the mutation list is executed by the
acceptance step (revert one named piece, re-run, paste the red line, restore). No lane runs
`./...`.

### L1 — RL-HEADROOM: headroom headers on admitted responses, scope on 429, dead helper deleted

- kind: borrow · candidates: C01-RL-HEADROOM · effort: S · migration: none
- files:
  `internal/adapter/middleware/rate-limit.go`,
  `internal/adapter/middleware/business_rate_limit.go`,
  `internal/adapter/middleware/business_rate_limit_test.go`,
  `internal/adapter/middleware/rate_limit_headroom_test.go` (new),
  `internal/adapter/middleware/business_model_rate_limit.go`,
  `internal/adapter/middleware/business_model_rate_limit_test.go`,
  `internal/adapter/middleware/concurrency_limit.go`,
  `internal/adapter/middleware/concurrency_limit_test.go`,
  `internal/app/relay/helper/perception.go`,
  `internal/app/relay/helper/perception_extra_test.go`
- test packages: `./internal/adapter/middleware`, `./internal/app/relay/helper`
- spec:
  1. `rate-limit.go`: keep `setRateLimitResponseHeaders(c, limit, remaining, retryAfterSec)`
     (:188-192) for the reject sites; add
     `setRateLimitHeadroomHeaders(c, scope, limitType string, limit int, remaining int64, resetUnix int64)`
     writing `X-RateLimit-Limit`, `X-RateLimit-Remaining` (clamped at 0), `X-RateLimit-Reset`
     (unix seconds), `X-RateLimit-Scope` (`token|tenant|model|concurrency|ip`),
     `X-RateLimit-Type` (`rpm|tpm|concurrency|requests`) and NO `Retry-After`. The two IP reject
     sites (:61, :80) add `X-RateLimit-Scope: ip` next to their existing call.
  2. `business_rate_limit.go`: `bizMemoryLimiter.allow` (:151-171) and `bizRedisAllow`
     (:181-217) return an extra `bizWindowState{used int64, oldestMs int64}`; on admit the memory
     path reports `used = len(ts)+1` and `ts[0]` (or the new stamp when the window was empty);
     the Redis path folds `ZRANGE key 0 0 WITHSCORES` into the existing ZADD+EXPIRE pipeline
     (:209-214) so no extra round-trip is added, `used = count+1`; a pipeline error keeps the
     fail-open contract (admit, log, `used` unknown → no headroom headers). `bizAllow` (:221-232)
     passes the state through. `bizTPMAdmit` (:275-296) already has `total/oldestMs`: on admit
     compute `remaining = limit - total - estimate`, `reset = (oldestMs+60000)/1000` (empty
     window → now+60). `BusinessRateLimit` (:302-358) collects one headroom per check it
     performed, picks the tightest (lowest remaining/limit ratio) and writes it ONCE immediately
     before the final `c.Next()` (:356) so streaming responses carry it (headers precede the first
     flush). `BusinessModelRateLimit` (`business_model_rate_limit.go:90-140`) writes the model
     scope only when its remaining/limit ratio is lower than what is already on the writer (read
     back from `c.Writer.Header()`). `bizReject` (:236-254) adds `X-RateLimit-Scope` and
     `X-RateLimit-Type`; `concurrency_limit.go:197-200` adds `Scope: token|tenant`,
     `Type: concurrency`.
  3. Opt-out: env `RATE_LIMIT_HEADERS_ENABLED` (default true) read once into a package var
     `bizHeadroomEnabled` so tests can flip it; when false the admit path writes nothing, 429
     headers are unconditional. `.env.example` row is written by L4 on this lane's behalf
     (L4 owns that file).
  4. Delete `helper.SetRateLimitHeaders` (`perception.go:157-166`) and `TestSetRateLimitHeaders`
     (`perception_extra_test.go:144-160`).
  5. `business_rate_limit_test.go:147-148`: relax the type lock to "type non-empty and
     code == bizRateLimitErrorCode" (L3 changes the type value after this lane).
  - Do NOT change: the fail-open rule (:224-227), `bizRetryAfterSec` (:126-133), TPM
    settled-usage semantics (:257-275 comment), gate order, `abortWithOpenAiMessage`,
    `model-rate-limit.go` (L3's), any doc file (L3 documents the headers).
- oracle: `go test -count=1 -p 2 ./internal/adapter/middleware ./internal/app/relay/helper`.
  `rate_limit_headroom_test.go` on the `eachBizBackend`/`bizTestLimits`/`bizFreezeClock`/`runBizRL`
  harness: token `rpm_limit=3` → three 200s carry `X-RateLimit-Limit 3`, `Remaining 2,1,0`,
  `Scope token`, `Type rpm`, `Reset` in `[now, now+60]`, no `Retry-After`; fourth is 429 with
  `Retry-After`, `Remaining 0`, `Scope token`, `Type rpm` — on BOTH backends (miniredis).
  Tightest-scope test: token rpm 10, tenant rpm 2 → second response `Scope tenant`,
  `Remaining 0`. TPM test: tenant TPM 1000 with 400 settled → 200 with `Remaining 600`,
  `Type tpm`. Disabled test: seam false → no `X-RateLimit-*` on 200, still present on 429.
  `concurrency_limit_test.go` / `business_model_rate_limit_test.go`: 429 carries
  `Scope concurrency|model`. Existing `TestBusinessRateLimit_TokenRPM_EnforcesAndSlides` green
  with the relaxed type lock. `go vet ./internal/app/relay/helper` compiles with the helper gone.
- mutation: comment out the pre-`c.Next()` write → `Remaining 2,1,0` red on both backends;
  return `used` without the `+1` → first response says `3` (red); invert the tightest comparison →
  tenant-scope case red; emit `retryAfter` seconds instead of unix for `Reset` → range red; drop
  the ZRANGE from the pipeline → `Reset` red on the Redis backend only (proves both paths run).
- UAT probe: bridge login on `https://test-newhub.lurus.cn`,
  `PUT /api/v2/lurus/tokens/<id> {"rate_limit_rpm":2}`; then three times within 60 s:
  `curl -sD - -o /dev/null -H "Authorization: Bearer <key>" -H "Content-Type: application/json" -d '{"model":"<live-model>","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}' https://test-newhub.lurus.cn/v1/chat/completions`
  → `X-RateLimit-Limit: 2`, `Remaining: 1` then `0`, `Scope: token`, `Reset` ≤ now+60; third
  → 429 with `Retry-After` and `X-RateLimit-Scope: token`. Restore `rate_limit_rpm 0`.
  (`<live-model>` = the single live upstream model on the UAT channel.)
- enterprise acceptance:
  - An SDK can pace itself before the first 429: every admitted relay response under a per-key,
    per-tenant or per-model limit states limit, remaining, reset instant and the tightest scope,
    proven on both limiter backends.
  - Every 429 from the five limiters names its scope and measure in standard headers on top of
    the existing `Retry-After`.
  - Opt-out is one env (default on); the second header writer is gone (compile-proved).
- consumer note: additive headers only; switch/lutu do not read `X-RateLimit-*` (root
  contracts.md `X-RateLimit` = 0 hits). `Remaining` is a snapshot under concurrency; TPM remaining
  is settled-usage based and advisory — both stated in the header table L3 writes.
- cross-repo follow-up: none directly (L3 carries the contracts.md relay-row note for headers and
  taxonomy together).

### L2 — REQUEST-IDENTITY: inbound X-Request-Id honoured, request_id/session_id/hashed end_user on the row, `/v1/generation`, `/v1/key`, `/logs` filters

- kind: borrow · candidates: C02-HEADER-CONTRACT (request-id step), C05-ROW-IDENTITY-LOOKUP,
  C16-KEY-INTROSPECTION · effort: L · migration: none
- files:
  `internal/adapter/middleware/request-id.go`,
  `internal/adapter/middleware/request_id_test.go` (new),
  `internal/pkg/common/constants.go`,
  `internal/adapter/provider/common/relay_info.go`,
  `internal/adapter/provider/common/cov_prov-common_relay_info_test.go`,
  `internal/app/governance/governance.go`,
  `internal/app/governance/enrich_test.go`,
  `internal/app/governance/classification.go`,
  `internal/app/governance/classification_test.go`,
  `internal/app/log_other_projection_lock_test.go`,
  `internal/domain/entity/log.go`,
  `internal/adapter/repo/savings.go`,
  `internal/adapter/repo/log.go`,
  `internal/adapter/repo/log_source_product_filter_test.go`,
  `internal/adapter/repo/log_request_identity_test.go` (new),
  `internal/adapter/handler/v2_log.go`,
  `internal/adapter/handler/v2_log_test.go`,
  `internal/adapter/handler/v1_generation.go` (new),
  `internal/adapter/handler/v1_generation_test.go` (new),
  `internal/adapter/handler/key_info.go` (new),
  `internal/adapter/handler/key_info_test.go` (new),
  `internal/adapter/handler/router/dashboard.go`,
  `internal/adapter/handler/router/cov_router-relay_wiring_test.go`
- test packages: `./internal/adapter/middleware`, `./internal/pkg/common`,
  `./internal/adapter/provider/common`, `./internal/app/governance`, `./internal/app`,
  `./internal/domain/entity`, `./internal/adapter/repo`, `./internal/adapter/handler`,
  `./internal/adapter/handler/router`
- spec (red→green steps, in order):
  1. Request id (`request-id.go:10-18`): accept an inbound `X-Request-Id` matching
     `^[A-Za-z0-9._-]{8,36}$` (upper bound = the `varchar(36)` width of `audit_events.request_id`; a longer inbound id would make the audit INSERT fail on PostgreSQL and drop the row); else the 32-hex trace-id of a syntactically valid `traceparent`;
     else mint as today (`GetTimeString()+GetRandomString(8)`). Add
     `common.RequestIdHeader = "X-Request-Id"` next to `RequestIdKey` (`constants.go:172-174`);
     `RequestIdKey` (`X-Oneapi-Request-Id`) stays the context key so `relay.go:93`,
     `perception.go:148`, `helper/common.go:59` and `utils.go:24` keep reading the same value.
     Write BOTH response headers before `c.Next()` (canonical `X-Request-Id`, alias
     `X-Oneapi-Request-Id` kept one release) so every 4xx/5xx rejection carries the id without
     touching any rejection helper. `trivial_cover_test.go:70` (`TestRequestId_SetsHeaderAndContext`)
     must stay green unmodified.
  2. Row fields (`relay_info.go`): `RelayInfo` gains `SessionId` and `EndUserHash`;
     `genBaseRelayInfo` (:414-475) fills `SessionId` from `X-Session-Id` (trimmed, kept only if
     `len<=200` and every byte is 0x20-0x7E, else `""`) and `EndUserHash` from the OpenAI `user`
     field (`dto/openai_request.go:57` and `:819`) or the Anthropic-wire `metadata.user_id`
     (the Anthropic-wire DTO file in `internal/pkg/dto/`, metadata struct at :14-15, raw
     metadata at :212) via a local type switch, hashed with
     `common.GenerateHMAC("end_user|"+tenantID+"|"+raw)` (`crypto.go:17`) truncated to 16 hex —
     the raw value is never persisted or logged.
  3. Writers: `repo/log.go` `RecordConsumeLog` (:445) and `RecordErrorLog` (:394) set
     `other["request_id"] = c.GetString(common.RequestIdKey)` when the key is absent — this
     covers every consume path and both error-row writers (`relay.go:730`, `utils.go:74`) without
     touching those files. `governance.go` `EnrichLogParams` (:45, body :95-113) writes
     `other.session_id` and `other.end_user` only when non-empty. `classification.go` (:46-92):
     `request_id`, `session_id` → `TierPublic`; `end_user` → `TierConfidential`.
     `log_other_projection_lock_test.go`: `request_id`, `session_id` into `wantUserVisible`
     (:51-99), `end_user` into `wantInternal` (:103) and into `repo/log.go internalOtherKeys`
     (:155) so non-admin projections strip it; `TestOtherProjectionIsFullyClassified` (:340) and
     the AST writer scan stay green. This is the ONLY lane that registers new `other.*` keys.
  4. Query/lookup: `savings.go` generalises `jsonSourceProductExpr` (:67-78) into
     `jsonOtherTextExpr(key)` with the same three dialect branches; `jsonSourceProductExpr()`
     becomes a wrapper; export `repo.OtherTextExpr(key string) string` beside `SourceProductExpr`
     (`log.go:771`) — L5 consumes it. `entity/log.go` `LogQueryParams` (:85-108) gains
     `RequestID`, `SessionID` (empty = no filter), applied next to the `SourceProduct` filters
     (`log.go:826-828`, `:881-883`). New
     `repo.GetLogByRequestID(requestID string, callerUserID int, callerTenantID string, callerTokenID int) (*Log, error)`:
     `WHERE token_id=? AND <OtherTextExpr("request_id")>=? ORDER BY id DESC LIMIT 1` (token_id is
     indexed, `entity/log.go:21`); ownership stricter than `GetLogByKey` (:254-285): the bearer
     token must be the row's token AND the caller's tenant/user must match, otherwise
     `gorm.ErrRecordNotFound` (never an empty page). `v2_log.go` (:126-147, :210-231) parses
     `request_id` and `session_id` query params into the params struct.
  5. `handler/v1_generation.go`: `GET /v1/generation?id=` in the `dashboard.go` TokenAuth group
     (:11-21): id must match the request-id regex else 404; response `{id, request_id, model,
     upstream_model, provider_name (constant.GetChannelTypeName(ChannelType)), quota, total_cost
     (quota/common.QuotaPerUnit), usage{prompt_tokens, completion_tokens, cached_tokens from
     other.cache_tokens when present}, latency_ms (TotalLatencyMs), first_token_ms (other.frt
     when > 0), streamed (IsStream), source_product, project_id, session_id, created_at}`; the
     `other` map goes through `repo.SanitizeOtherForUser` (:129) so `admin_info`/channel ids never
     leak; not-owned → 404.
  6. `handler/key_info.go`: `GET /v1/key` (same group) for the bearer key from
     `repo.GetTokenById(c.GetInt("token_id"))` (`repo/token.go:247`):
     `{label (Name), tenant_id, project_id, group, scopes, model_limits (when enabled), limit
     (null when UnlimitedQuota else (RemainQuota+UsedQuota)/QuotaPerUnit), limit_remaining
     (null|RemainQuota/QuotaPerUnit), usage (UsedQuota/QuotaPerUnit), expires_at (null when
     ExpiredTime==-1), rate_limit{rpm,tpm,tenant_rpm,tenant_tpm} (repo.GetTenantByID
     repo/tenant.go:37; entity/tenant.go:27-33), pool{balance,max_balance,health} via
     repo.GetTenantCreditPool (:60) + poolHealthForEndUser (handler/tenant_credit_pool.go:132,
     same package; ErrPoolNotFound → null), default_product: ratio_setting.DefaultSourceProduct}`.
     Read-only, no flag. `switch_user_info.go` (`GetSwitchUserInfo` :81) is NOT modified (pinned
     shape in root contracts.md switch block).
  7. `dashboard.go` registers the two GETs; `cov_router-relay_wiring_test.go` asserts both answer
     401 without a key via `routerRelayHasRoute` (:53) plus a request.
  - Do NOT change: `relay.go`, `middleware/utils.go` (L3), `quota.go` (L4), the erasure cascade
    (hash is non-reversible; stated in L3's guide section), source_product resolution
    (`governance.go:105-110`), field names of `LurusUsageExtension`, `session_affinity.go`.
- oracle: `go test -count=1 -p 2` over the test packages above. `request_id_test.go`: inbound
  `uat-abc12345` echoed on both headers and present in `c.GetString(common.RequestIdKey)`; a
  300-char value and `<script>` are replaced by a minted id; a valid `traceparent` trace-id is
  honoured; a 401 from `mountWireRejectionRouter` carries `X-Request-Id`.
  `v1_generation_test.go` on the `relay_attribution_test.go:76` fake-upstream harness plus a
  settled consume row: relay with `X-Session-Id: conv-42` and OpenAI `user: alice`; read the id
  from the `X-Request-Id` header; `GET /v1/generation?id=<it>` with the same key → 200,
  `total_cost == row.Quota/QuotaPerUnit`, `provider_name == fake channel type name`,
  `session_id == conv-42`, body has no `admin_info`/`channel_id`; the same id with another user's
  token → 404; a 201-char `X-Session-Id` → `other.session_id` absent; `other.end_user` is 16 hex
  and never contains `alice`; a middleware 401 error row carries `other.request_id`.
  `log_request_identity_test.go` (`setupSQLiteDB`): two rows with different `session_id` →
  filter returns exactly one; `GetLogByRequestID` with the wrong token id → `ErrRecordNotFound`.
  `key_info_test.go`: token rpm 5, remain 1234, expired_time T, tenant rpm 7 →
  `rate_limit.rpm 5`, `tenant_rpm 7`, `limit_remaining 1234/QuotaPerUnit`, `expires_at T`;
  unlimited → `limit`/`limit_remaining` null; no pool → `pool` null. `enrich_test.go` /
  `classification_test.go`: the three keys classified; `log_other_projection_lock_test.go` green.
- mutation: revert the inbound acceptance in `request-id.go` → echo test red while the minted-id
  test stays green; delete the `request_id` write in `RecordConsumeLog` → `/v1/generation` lookup
  404 (red); loosen `GetLogByRequestID` to user-only ownership → foreign-token case red; drop the
  ASCII/length guard → 201-char case red; persist the raw `user` instead of the HMAC →
  `end_user` assertion red; remove the `/v1/key` registration → wiring test red.
- UAT probe: `K=<uat key>`;
  `curl -sD /tmp/h -o /dev/null -H "Authorization: Bearer $K" -H "X-Request-Id: uat-abc12345" -H "X-Session-Id: uat-$(date +%s)" -H "Content-Type: application/json" -d '{"model":"<live-model>","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"user":"alice"}' https://test-newhub.lurus.cn/v1/chat/completions`
  → `X-Request-Id: uat-abc12345` and `X-Oneapi-Request-Id: uat-abc12345`;
  `curl -s -H "Authorization: Bearer $K" "https://test-newhub.lurus.cn/v1/generation?id=uat-abc12345"`
  → 200 with `total_cost > 0` and `session_id` echoed; bridge session
  `GET /api/v2/lurus/logs?session_id=uat-<ts>` → 1 row whose `other.end_user` is a 16-hex hash;
  `curl -s -H "Authorization: Bearer $K" https://test-newhub.lurus.cn/v1/key` → 200 with
  `rate_limit` and `pool` objects; a drained key → 402 whose headers still echo `X-Request-Id`.
- enterprise acceptance:
  - An integrator logs an id before the call leaves them, sees it on every response (success,
    stream, or rejection on either wire) and resolves it to cost/provider/latency with only the
    key it holds (`/v1/generation`), with no console access.
  - Per-conversation and per-end-user cost is queryable from `/logs` without the client keeping
    its own ledger, and the end-user identifier is never stored in clear (non-reversible keyed
    hash; the erasure cascade is not widened).
  - A product holding only a key can show its user limits, remaining allowance, rate limits and
    pool state from one call; the switch-specific endpoint is byte-identical.
- consumer note: additive — two new TokenAuth endpoints, two new `/logs` filters, three new
  `other.*` keys (`request_id`/`session_id` user-visible, `end_user` confidential), one new
  response header (`X-Request-Id`) with the old alias kept. switch/lutu do not read
  `X-Oneapi-Request-Id` (previous cycle cross-repo notes). OpenAI `user` and Anthropic-wire
  `metadata.user_id` are now persisted as a hash only.
- cross-repo follow-up: root contracts.md newhub row — register `GET /v1/key`,
  `GET /v1/generation`, inbound/outbound `X-Request-Id` (alias deprecated), `X-Session-Id` and
  `user`-field semantics; note the `end_user` hash is non-reversible in the erasure register;
  switch may later delegate its reconciliation to `/v1/generation` (not required).

### L3 — CONTRACT-TAXONOMY + DOC LOCK: vendor error types at the root, non-empty codes on every gateway rejection, contract documented and locked

- kind: fix · candidates: C03-ERROR-TAXONOMY-L2, C02-HEADER-CONTRACT (doc half), DOC-1 (first
  lock) · effort: M · migration: none
- runs AFTER L1 (type lock relaxed, headers exist) and L2 (endpoints and `X-Request-Id` exist to
  document; error rows already carry `request_id`).
- files:
  `internal/pkg/types/error.go`, `internal/pkg/types/error_test.go`,
  `internal/pkg/types/error_more_test.go`,
  `internal/adapter/middleware/utils.go`, `internal/adapter/middleware/distributor.go`,
  `internal/adapter/middleware/auth.go`, `internal/adapter/middleware/jimeng_adapter.go`,
  `internal/adapter/middleware/model-rate-limit.go`,
  `internal/adapter/middleware/r5e_model_rate_limit_default_test.go` (adjust only if it locks
  the 429 body; verify first),
  `internal/adapter/middleware/cost_spike.go`, `internal/adapter/middleware/entitlement.go`,
  `internal/adapter/middleware/pool_balance_check.go`,
  `internal/adapter/middleware/rejection_envelope_wire_test.go`,
  `internal/adapter/middleware/distributor_status_test.go`,
  `internal/adapter/middleware/entitlement_product_test.go`,
  `internal/adapter/middleware/abort_code_structural_test.go` (new),
  `internal/adapter/handler/relay.go` (the :776-782 stub envelope ONLY),
  `internal/adapter/handler/billing.go`,
  `internal/adapter/handler/cover_r2_billing_test.go`,
  `internal/adapter/handler/relay_inband_error_test.go`,
  `internal/adapter/handler/router/openapi_contract_lock_test.go` (new),
  `internal/app/error.go`, `internal/app/error_test.go`,
  `internal/app/r2_channel_token_quota_test.go`,
  `docs/openapi/relay.json`, `doc/product-integration-guide.md`
- test packages: `./internal/pkg/types`, `./internal/adapter/middleware`,
  `./internal/adapter/handler`, `./internal/adapter/handler/router`, `./internal/app`
- spec:
  1. Root (`types/error.go`): add `WireErrorType(status int, wire RelayFormat) string` —
     OpenAI wire: 400 `invalid_request_error`, 401 `authentication_error`, 402
     `insufficient_quota`, 403 `permission_error`, 404 `not_found_error`, 413
     `request_too_large`, 429 `rate_limit_error`, 5xx `api_error`; Anthropic wire identical
     except 402 `billing_error`, 529 `overloaded_error`. The OpenAI-wire converter default branch
     (:198-204) and the Anthropic-wire converter default branch (:238-243) use it when
     `e.errorType == ErrorTypeNewAPIError`; the `ErrorTypeOpenAIError` branch and the
     Anthropic-wire upstream-error branch keep upstream types verbatim. `ErrorCode` string values, `ErrorTypeNewAPIError`
     (:37) as the internal discriminator, `RelayErrorType` (:445-461), `IsUpstreamFailure`
     (:404), SkipRetry and channel-disable semantics are NOT changed. New constants:
     `ErrorCodeModelBlocked "model_blocked"`, `ErrorCodeTokenDisabled "token_disabled"`,
     `ErrorCodeUserBanned "user_banned"`, `ErrorCodeTenantSuspended "tenant_suspended"`,
     `ErrorCodeIPNotAllowed "ip_not_allowed"`, `ErrorCodeGroupNotAllowed "group_not_allowed"`,
     `ErrorCodeSessionRequired "session_required"`,
     `ErrorCodeChannelSpecifyForbidden "channel_specify_forbidden"`,
     `ErrorCodeScopeNotGranted "scope_not_granted"`,
     `ErrorCodeRequestRateLimitExceeded "request_rate_limit_exceeded"`,
     `ErrorCodeGatewayInternal "gateway_internal"`; `ErrorCodeInvalidRequest` (:49) is reused.
  2. Helper (`utils.go:16-38`): build the error with
     `types.NewErrorWithStatusCode(errors.New(msgWithRequestId), types.ErrorCode(codeStr), statusCode)`
     instead of `WithOpenAIError` with the literal at :30, so both converters map per wire
     (OpenAI body keeps `{message,type,param,code}`; Anthropic body gets the vendor type instead of
     the code string produced by `error.go:229-234` today). Variadic signature stays (L1's
     `business_rate_limit.go:251` / `concurrency_limit.go:198` callers compile untouched).
     `abortWithMidjourneyMessage` (:79-87) is the MJ wire and is left alone.
  3. Codes on the 29 code-less sites: `auth.go` :474 (token-state error → `token_disabled` /
     `ErrorCodeTokenQuotaExhausted` by `errors.Is`/message match, else `invalid_request`), :484/:491
     `ip_not_allowed`, :506 `scope_not_granted`, :537/:737/:817 `gateway_internal`, :542/:766
     `user_banned`, :575 `tenant_suspended`, :624/:630 `group_not_allowed`, :698
     `channel_specify_forbidden`, :721/:726 `session_required`, :746 `token_disabled`;
     `distributor.go` :36/:85/:124/:129 `invalid_request`, :50/:55 invalid channel id →
     `invalid_request`, :59 channel disabled → `channel_specify_forbidden`, :77/:97/:107 →
     `model_blocked`, :114 → `group_not_allowed`, remaining sites → `invalid_request`;
     `jimeng_adapter.go` :19/:26/:57 `invalid_request`, :40 `gateway_internal`;
     `model-rate-limit.go` :197/:224 `request_rate_limit_exceeded` plus
     `X-RateLimit-Scope: user`/`X-RateLimit-Type: requests` on the four 429 sites (:196,:223,:267,:277).
     Messages become English at the sites touched (request id is already appended by
     `common.MessageWithRequestId`, `common/utils.go:278`).
  4. Literal sweep (13 sites → 0 outside `types/error.go:37`): `cost_spike.go:104`,
     `entitlement.go:117`, `pool_balance_check.go:78,117` (keep the `extra` tenant_id field via
     `renderRejection`, `wire_format.go:58-90`), `handler/billing.go:191,208,213,242,254`
     (`types.WireErrorType(500, OpenAI)` + code `gateway_internal`), `relay.go:779`
     (`WireErrorType(501, OpenAI)` = `api_error`, code `api_not_implemented` and status 501 kept),
     `app/error.go:70` (Anthropic-wire error struct → `WireErrorType(statusCode, Anthropic)`),
     `utils.go:30` (step 2). Acceptance grep:
     `grep -rn '"new_api_error"' internal cmd --include=*.go | grep -v _test | grep -v types/error.go`
     returns only prose comment lines (`app/channel.go:98`, `app/error.go:44`, `utils.go:25` if
     kept as prose) and no code literal.
  5. Inbound compat (do_not_regress): `app/channel.go:88-120` untouched;
     `r2_channel_token_quota_test.go` keeps the legacy body case (:94) and adds the NEW 402 body
     `{type insufficient_quota, code token_quota_exhausted}` — both → `ShouldDisableChannel true`.
  6. Locks flipped/added: `rejection_envelope_wire_test.go:93-94` → `"type":"authentication_error"`
     with a non-empty code; add `/v1/messages` bad key → `error.type authentication_error`;
     `distributor_status_test.go`: token `model_limits {a}`, request `b` → 403 `code model_blocked`,
     `type permission_error` (OpenAI wire) and the Anthropic envelope
     `{"type":"error","error":{"type":"permission_error"}}`;
     `entitlement_product_test.go:132-133`, `cover_r2_billing_test.go:582-583,614-615`
     (`type api_error`), `relay_inband_error_test.go:107`, `error_more_test.go:219,461`,
     `app/error_test.go:94-130,178-179` updated to the mapped types; `error_test.go` gains the
     `WireErrorType` table incl. 529 on the Anthropic wire. New `abort_code_structural_test.go`
     parses middleware non-test files with `go/ast`, fails on any `abortWithOpenAiMessage` call
     with fewer than four args, and fails fast unless it saw > 30 call sites in > 5 files (today
     39 sites).
  7. Docs (single owner): `relay.json` — on `/v1/chat/completions` (:186), `/v1/messages` (:1236),
     `/v1/completions` (:1514), `/v1/embeddings` (:1474) fill the 200 `headers`: `X-Request-Id`,
     `X-Model-Provider`, `X-RateLimit-Limit/Remaining/Reset/Scope/Type` (present when a limit
     applies; TPM values advisory), `X-Request-Cost`/`X-Quota-Remaining` (OpenAI non-stream
     only); 4xx/5xx headers: `X-Request-Id` always, `Retry-After` on 402/429/503; one shared
     `components.schemas.GatewayError` `{type: enum per wire, code: enum of EVERY ErrorCode
     string constant in internal/pkg/types (incl. the new ones), message, metadata{topup_url?,
     upgrade_url?}}`; new paths `GET /v1/key` and `GET /v1/generation` mirroring L2's handlers;
     top-level `x-lurus-enforcement-order` = the ten names from `relay-router.go:100-121`
     (StampRelayFormat, TokenAuth, PoolBalanceCheck, CostSpikeLimit, EntitlementCheck,
     ModelRequestRateLimit, BusinessRateLimit, RelayConcurrencyLimit, Distribute,
     BusinessModelRateLimit). Guide: §B (:98-107) rewritten — drop the `new_api_error` rows
     (:106 and the Q4 body at :77), add a code column and the enforcement-order table; new §E
     header directory (inbound `X-Request-Id` rules, deprecated alias, headroom headers incl. the
     settled-usage caveat, cost headers); §F key-holder endpoints (`/v1/key`, `/v1/generation`,
     `X-Session-Id`, `user` hashing non-reversible); §A endpoint table gains the two routes.
     Language stays the file's (Chinese with English identifiers).
  8. `openapi_contract_lock_test.go` (router package; engine built exactly as
     `cov_router-relay_wiring_test.go:170` does with `SetRouter`): loads
     `../../../../docs/openapi/relay.json` and asserts (a) every documented path+method exists in
     `engine.Routes()` after normalising `{x}`→`:x` (paths documented today but not mounted are
     corrected in the doc by this lane and listed in the lane report), (b) the 200 of the four
     chat-class paths documents `X-Request-Id`, (c) every documented 429 documents `Retry-After`
     and `X-RateLimit-Scope`, (d) `/v1/key` and `/v1/generation` are documented, (e) every value in
     the error-code enum appears as an `ErrorCode = "..."` literal in `internal/pkg/types/*.go`
     non-test sources AND every such literal appears in the enum (bidirectional, prints the
     count, fails fast on zero), (f) `x-lurus-enforcement-order` equals the ten-name list.
  - Do NOT change: HTTP status codes, gate order, `SkipRetry`/`ShouldDisableChannel` semantics,
    the metadata mechanism (`error.go:209-216`, provider metadata deferred), the Google-wire
    branch, `business_rate_limit.go`/`concurrency_limit.go`/`perception.go` (L1),
    `request-id.go`/`repo/log.go` (L2), `quota.go` (L4).
- oracle: `go test -count=1 -p 2 ./internal/pkg/types ./internal/adapter/middleware ./internal/adapter/handler ./internal/adapter/handler/router ./internal/app`
  with the assertions listed in steps 6 and 8; `rejection_envelope_wire_test.go:61,84,122` and
  `relay_wire_stamp_test.go:93` ("must not contain") stay green; the structural test reports the
  count of sites scanned; the contract lock prints the number of codes compared (> 0).
- mutation: restore `string(e.errorType)` in the OpenAI-wire default branch → the 401
  `authentication_error` assertion red; restore the literal in `utils.go` → structural grep and
  wire test red; drop the code at `distributor.go:107` → structural lock red and the
  `model_blocked` assertion red; remove the new 402 body case → the round-trip test still proves
  the legacy body auto-disables (must stay green); rename `model_blocked` in the enum →
  lock (e) red; remove `X-Request-Id` from the chat 200 headers → lock (b) red; add a fictitious
  path `/v1/foo` → lock (a) red.
- UAT probe: bridge session `PUT /api/v2/lurus/tokens/<id> {"model_limits_enabled":true,"model_limits":"not-a-live-model"}`,
  then POST the live model with that key → 403
  `{"error":{"type":"permission_error","code":"model_blocked",...}}`; bad key on
  `/v1/chat/completions` → 401 `authentication_error` with a non-empty code; bad key on
  `/v1/messages` → `{"type":"error","error":{"type":"authentication_error"}}`; a drained key → 402
  `insufficient_quota` / `billing_error` per wire, still echoing `X-Request-Id`.
  `grep -rn '"new_api_error"' internal --include=*.go | grep -v _test | grep -v types/error.go`
  → no code literal. Restore `model_limits` afterwards.
- enterprise acceptance:
  - An OpenAI-SDK or Anthropic-SDK caller sees only vendor-taxonomy type values and a stable,
    non-empty machine code on every gateway rejection, on both wires, proven by wire tests through
    the real router rather than hand-built structs.
  - The relay contract is written down where integrators look (header directory, code enum,
    key-holder endpoints, ten-stage enforcement order) and a Go test fails when the OpenAPI
    document drifts from the route table or the error-code constants in either direction
    (closes DOC-1 from two previous cycles).
  - Inbound parsing of legacy downstream error bodies keeps its auto-disable behaviour
    (round-trip test with both body shapes).
- consumer note: `error.type` on every gateway-originated rejection changes from
  `new_api_error` to the vendor taxonomy, `error.code` becomes non-empty, messages at the touched
  sites become English; status codes unchanged. Zero external consumers of the literal per last
  cycle's cross-repo grep (lutu/switch/acest/platform). A downstream newhub instance relaying
  through this one will now auto-disable that channel on our 401/403 (`authentication_error` /
  `permission_error` are in its Type switch, `app/channel.go:113-120`) — intended for bad keys,
  also fires for IP/group 403s. Record in changelog.
- cross-repo follow-up: root contracts.md newhub relay row — taxonomy table per wire, non-empty
  code, header directory (`X-Request-Id`, `X-RateLimit-*`), link to guide §B/§E/§F; tell
  platform/creator/lucrum teams that relay through hub that 401/403 now carry vendor types.

### L4 — POOL-RENEWAL-AND-ALERT: scheduled credit-pool reset (observe-first) and the built threshold publisher finally called from the debit path, rehearsal endpoint, runbook made truthful

- kind: un-dead · candidates: C07-POOL-RESET-ALERT-UNDEAD · effort: M · migration: none (all
  columns exist: `entity/tenant_credit_pool.go:48-61`)
- files:
  `internal/app/credit_pool_reset.go` (new), `internal/app/credit_pool_reset_test.go` (new),
  `internal/app/credit_pool_reconcile.go`, `internal/app/credit_pool_reconcile_test.go`,
  `internal/app/quota.go`, `internal/app/pool_debit_test.go` (new),
  `internal/adapter/repo/tenant_credit_pool.go`, `internal/adapter/repo/tenant_credit_pool_test.go`,
  `internal/pkg/nats/pool_threshold.go`, `internal/pkg/nats/pool_threshold_test.go`,
  `internal/pkg/nats/pool_threshold_gorm_test.go`,
  `internal/adapter/handler/internal_maintenance.go`,
  `internal/adapter/handler/internal_maintenance_test.go`,
  `internal/adapter/handler/router/internal-api-router.go`,
  `internal/adapter/handler/router/internal_admin_reset_pools_route_test.go` (new),
  `internal/pkg/metrics/metrics.go`, `internal/pkg/metrics/metrics_extra_test.go`,
  `internal/pkg/metrics/alert_wiring_honesty_test.go` (only if it pins the :281-289 sentence),
  `internal/app/governance/audit_action.go`, `internal/app/governance/audit_test.go`,
  `doc/runbook/pool-threshold-alert.md`, `.env.example`
- test packages: `./internal/app`, `./internal/adapter/repo`, `./internal/pkg/nats`,
  `./internal/adapter/handler`, `./internal/adapter/handler/router`, `./internal/pkg/metrics`,
  `./internal/app/governance`
- spec:
  A. Reset (repo): `ResetDuePools(ctx, now time.Time, enforce bool) ([]PoolResetResult, error)` —
     `SELECT ... WHERE next_reset_at IS NOT NULL AND next_reset_at <= now AND reset_period NOT IN
     ('', 'none') AND max_balance > 0` (unlimited -1 and `none` skipped; the switch tenant pool,
     ledger 030 `reset_period='none'`, is never listed). enforce: per pool one transaction with a
     conditional `UPDATE ... WHERE id=? AND next_reset_at = <value read>` (CAS like the reconcile
     claim; no `FOR UPDATE` so the SQLite tier runs) `SET current_balance=max_balance,
     last_reset_at=now, next_reset_at=nextResetAt(period, now)` (:489-510),
     `alert_fired_at=NULL`; on `RowsAffected==1` insert a `TenantCreditPoolDraw`
     (`entity/tenant_credit_pool.go:96-107`) with `Reason=PoolDrawReasonReset` (repo :32),
     `Direction` credit, `Amount = max_balance - current_balance` (refill-to-ceiling; a negative
     overdraft balance is thereby forgiven — the runbook records this as the semantic the owner
     must confirm before enforce on production). observe: no write; results carry the would-be
     delta. Conservation law: sum of draw amounts == balance delta.
  B. `app/credit_pool_reset.go`: `ResetDuePools(ctx)` reads `CREDIT_POOL_RESET_MODE`
     (`observe` default | `enforce`), calls the repo, increments
     `metrics.CreditPoolResetTotal{tenant_id, action=observed|reset}` (declared in `metrics.go`
     next to `CreditPoolBalance`), on enforce sets `CreditPoolBalance` and writes a detached audit
     event `governance.ActionBillingPoolReset` (`"billing.pool_reset"`, added at
     `audit_action.go:105-112` and the whitelist `:195-206`) with `{pool_id, delta, next_reset_at}`.
     Wiring: inside the leader branch of the existing reconcile ticker
     (`credit_pool_reconcile.go:363-391`) call `ResetDuePools` right after
     `ReconcileStrandedTopups` through a package-level seam var so the test can assert the call —
     same 300 s interval, no `main.go` change.
  C. Rehearsal: `handler/internal_maintenance.go` `InternalResetDuePools` —
     `POST /internal/admin/reset-due-pools?mode=observe|enforce` (query overrides env; default
     env mode) mounted in the `ScopeAdmin` group (`internal-api-router.go:151-158`), response
     `{success, data:{mode, pools:[{tenant_id,pool_id,delta,action,next_reset_at}]}}`, SysLog line
     with the key name like `InternalRotateDueTokens` (:23-44).
  D. Alert: `quota.go` debit success branch (:790-799): `after := *pool;
     after.CurrentBalance = pool.CurrentBalance - int64(quota)`; if `after.ShouldAlert()` and
     (`pool.AlertFiredAt == nil || now.Sub(*pool.AlertFiredAt) >= hubnats.SchemaDedupWindow()`)
     → `fired, delivery, err := hubnats.PublishPoolThreshold(ctx, tok.TenantId, pool.ID,
     after.CurrentBalance, pool.MaxBalance, pool.AlertThresholdPct)` (import already at
     `quota.go:18`); on `fired` write a detached audit event
     `governance.ActionBillingPoolThreshold` (`"billing.pool_threshold"`) with
     `{pool_id, balance, max_balance, threshold_pct, delivery}`. Same on the overdraft branch
     (:800-810) with the returned `newBalance`. The precheck only avoids a DB read per below-
     threshold debit; the publisher's own dedup remains authoritative.
     `pool_threshold.go:134-153`: when `!Enabled()` or `Get()==nil` no longer return early — run
     `publishPoolThreshold` with a nil publisher in `delivery=recorded_only` mode: steps 1-3
     (schema dedup, Redis SETNX, `MarkAlertFired`) execute exactly as today (:180-235,
     do_not_regress), step 4 is skipped when the publisher is nil; signature becomes
     `(fired bool, delivery string, err error)`; export `SchemaDedupWindow()` returning
     `poolSchemaDedupWindow` (:29). After `MarkAlertFired` increment
     `metrics.CreditPoolAlertTotal{tenant_id, delivery=nats|recorded_only}` (declared in
     `metrics.go`; the writer lives in the nats package, so `declared_series_written_test.go`
     passes). Rewrite the `metrics.go:278-290` comment to the new truth: the threshold now
     publishes when `LLM_QUOTA_NATS_ENABLED` and is always marked/audited/counted, and still
     nothing pages — without naming any alert as firing (`TestNoAlertNamedAsFiringInGoSource`,
     `alert_wiring_honesty_test.go:287`).
  E. `.env.example`: after the `COST_SPIKE_ENFORCE` block (:259-270) document
     `CREDIT_POOL_RESET_MODE` (observe|enforce, default observe) and, on L1's behalf,
     `RATE_LIMIT_HEADERS_ENABLED` (default true) next to `RELAY_MAX_CONCURRENT_PER_TOKEN` (:251).
     Runbook `pool-threshold-alert.md`: replace the NOT WIRED note with the reset/alert behaviour,
     the two modes, the rehearsal endpoint, the refill-vs-zero decision and the fact that no pager
     consumes the metric.
  - Do NOT change: `DebitPool`/`DebitPoolInTx`/`OverdraftDebitPool` bodies, stranded-topup
    reconcile logic, `publishPoolThreshold` dedup order (:180-235), `pre_consume_quota.go`,
    `cmd/server/main.go`, the entity structs, `pool_balance_check.go` (L3).
- oracle: `go test -count=1 -p 2 ./internal/app ./internal/adapter/repo ./internal/pkg/nats ./internal/adapter/handler ./internal/adapter/handler/router ./internal/pkg/metrics ./internal/app/governance`.
  `credit_pool_reset_test.go` (SQLite harness of `tenant_credit_pool_test.go`): pool max 1000,
  balance 100, alert_fired_at set, period daily, next_reset_at = now-1h → enforce: balance 1000,
  exactly one draw `reason=reset amount 900`, next_reset_at == next UTC midnight after now,
  alert_fired_at nil, `CreditPoolResetTotal{reset}` +1, one audit row; observe: row
  byte-identical, `{observed}` +1, no draw; period `none` and max -1 pools untouched in both
  modes; two goroutines calling enforce → one draw row (CAS). `tenant_credit_pool_test.go`:
  conservation after reset+debit. Reconcile wiring test: the ticker's leader branch invokes the
  reset seam once per tick. `pool_debit_test.go`: pool max 1000 balance 850 threshold 80 → debit
  100 crosses 800 → publisher core invoked once with balance 750, one `billing.pool_threshold`
  audit row; a second debit of 10 → not invoked (alert_fired_at set by `MarkAlertFired`); a pool
  with alert_fired_at set inside the window → never invoked. nats: `Enabled()=false` →
  `MarkAlertFired` called, publish not called, returns `fired=true delivery=recorded_only`,
  `CreditPoolAlertTotal{recorded_only}` +1; the existing `publishPoolThreshold` tests stay green
  with the new return shape. Handler: `POST reset-due-pools?mode=observe` → 200 listing the due
  pool with `action observed` and no draw; `?mode=enforce` → draw written; 403 for a key without
  the admin scope; router test asserts the route sits under `RequireScope(ScopeAdmin)`.
  `audit_test.go` covers the two new constants. `declared_series_written_test.go` green.
- mutation: remove the `ResetDuePools` call from the reconcile ticker → wiring test red; change
  delta to `max_balance` (zero-then-refill) → conservation red; delete the alert hook in the
  debit success branch → invocation count 0 (red); make `PublishPoolThreshold` return early
  again when NATS is disabled → recorded_only test red; flip the default mode to enforce → the
  observe test sees a draw row (red).
- UAT probe (NATS off on UAT, `r6-uat/deployment.yaml:123`): mint a UAT admin-scope internal key
  R6-side (v2 admin internal-keys route `api-v2-router.go:409`; owner item). Ensure tenant lurus
  has a pool with `reset_period daily` and `alert_threshold_pct 99`
  (`POST /api/v2/lurus/admin/tenants/<id>/credit-pool` per `api-v2-router.go:380-384`); psql on
  newhub_uat: `UPDATE tenant_credit_pools SET next_reset_at = now() - interval '1 hour', current_balance = 50000 WHERE tenant_id='lurus';`
  `curl -s -X POST -H "X-API-Key: <key>" "https://test-newhub.lurus.cn/internal/admin/reset-due-pools?mode=observe"`
  → `pools[0].action observed`, usage list shows no reset row; `?mode=enforce` → one draw
  `reason=reset delta 50000`, balance at the ceiling, next_reset_at tomorrow 00:00Z; on the R6
  host `curl -s http://localhost:30851/metrics | grep credit_pool_reset_total` →
  `{action="reset"} 1`. Alert: set current_balance just above the 99 % line, one relay with the
  live model → `GET /api/v2/lurus/admin/audit/events?action=billing.pool_threshold` has one row,
  `credit_pool_alert_total{delivery="recorded_only"} 1`; a second relay adds no row.
- enterprise acceptance:
  - A daily/weekly/monthly tenant pool renews on schedule without an operator once enforce is on,
    each renewal is a ledger row that reconciles with the balance (conservation law kept), and
    the same pass can be rehearsed on demand through an admin-scoped endpoint in observe mode.
  - Crossing the alert threshold runs the existing deduplicated publisher exactly once per
    window, leaving a durable `alert_fired_at`, an audit row and a counter even when NATS is
    disabled, so the condition is visible on every environment.
  - Flag-off state is provably inert: default observe writes nothing to pool balances and the
    hermetic tests lock that; the debit path gains only a post-success hook that cannot fail the
    settlement.
- consumer note: platform-core starts receiving the pool-threshold NATS event on production
  (NATS enabled there) for pools crossing `alert_threshold_pct`; payload = the existing
  `SubjectPoolThreshold` shape (no change). Pool balances never change unless
  `CREDIT_POOL_RESET_MODE=enforce`; refill-to-ceiling / overdraft-forgiveness needs owner sign-off
  before enforce on production.
- cross-repo follow-up: root contracts.md event registry — register the pool-threshold event as
  emitted by newhub (today 0 hits; `llm.quota.threshold` at :312 is the neighbour row) and its
  audit action; record the refill-vs-zero decision in `doc/decisions/`; deploy manifest gets
  `CREDIT_POOL_RESET_MODE` only after the owner decision.

### L5 — TTFT-AND-CACHE-STAT: time-to-first-token histogram with product label, cache token totals in `/logs/stat`

- kind: borrow · candidates: C08-TTFT-CACHE-SERIES (TTFT + stat halves) · effort: S ·
  migration: none
- runs AFTER L2 (consumes `repo.OtherTextExpr`). Fallback if L2 did not land: add
  `OtherTextExpr` in `repo/savings.go` as the only extra edit (and list it in the lane report).
- files:
  `internal/pkg/metrics/ttft.go` (new), `internal/pkg/metrics/ttft_test.go` (new),
  `internal/adapter/handler/relay_outcome.go`, `internal/adapter/handler/relay_outcome_test.go`,
  `internal/adapter/handler/v2_log_stat.go`, `internal/adapter/handler/v2_log_stat_test.go`,
  `doc/decisions/observability.md`
- test packages: `./internal/pkg/metrics`, `./internal/adapter/handler`
- spec: new file `metrics/ttft.go` (metrics.go is L4's): `RelayTimeToFirstToken` histogram
  `lurus_gateway_relay_time_to_first_token_seconds{provider,model,product}` with buckets
  `[.05,.1,.25,.5,1,2.5,5,10,30]` and `RecordTimeToFirstToken(provider, model, product string, seconds float64)`.
  Writer: `relay_outcome.go` `observeRelayOutcome` (:46-61) — when `total==true`, `info != nil`
  and `info.HasSendResponse()` (`relay_info.go:557-559`; the seeded sentinel at :474 makes
  non-streamed/failed requests false) observe `info.FirstResponseTime.Sub(info.StartTime).Seconds()`
  with the same product fallback as the existing series. `v2_log_stat.go`: totals `Select`
  (:155) and the by_product `Select` (:245) gain `cache_read_tokens` and `cache_write_tokens` as
  `COALESCE(SUM(CAST(<repo.OtherTextExpr("cache_tokens")> AS BIGINT)),0)` and the same for
  `cache_creation_tokens` (keys written at `log_info_generate.go:40,127`; values are Go ints so
  the text cast is exact on PostgreSQL and the SQLite tier; `NULLIF(other,'')` in the PG branch
  guards empty rows). `observability.md`: one row for the new series and the stat fields.
  Deliberately NOT done: `tokens_processed_total{type=cache_read|cache_write}` (writer is
  `repo/log.go:451`, L2's file) and time-per-output-token (completion tokens unavailable at the
  outcome site) — both deferred.
  - Do NOT touch: `metrics.go`, `relay.go`, `quota.go`, `repo/log.go`, the web Log page,
    `classification.go` (no new `other.*` key is written), existing label sets.
- oracle: `go test -count=1 -p 2 ./internal/pkg/metrics ./internal/adapter/handler`.
  `relay_outcome_test.go`: RelayInfo with `StartTime = now-200ms`, `FirstResponseTime = now`,
  `SourceProduct switch` → `observeRelayOutcome(total=true)` → histogram count for
  `{provider,model,product=switch}` == 1 and sum ≥ 0.2; `total=false` → unchanged; sentinel
  (FirstResponseTime before StartTime) → unchanged; nil info → no panic. `ttft_test.go`
  registers name/labels. `v2_log_stat_test.go` (SQLite): rows with `other {"cache_tokens":120}`,
  `{"cache_tokens":30,"cache_creation_tokens":7}` and one with `other ""` →
  `totals.cache_read_tokens == 150`, `cache_write_tokens == 7`,
  `by_product[0].cache_read_tokens == 150`, no query error. `declared_series_written_test.go`
  stays green (writer in the handler package).
- mutation: delete the `HasSendResponse` branch → TTFT count 0 (red); swap `cache_tokens` for
  `cache_creation_tokens` in the read sum → totals red; declare a series in `ttft.go` with no
  writer → honesty gate red.
- UAT probe: one streaming call
  `curl -N -H "X-Lurus-Product: switch" -H "Authorization: Bearer $K" -d '{"model":"<live-model>","stream":true,...}' https://test-newhub.lurus.cn/v1/chat/completions`;
  on the R6 host `curl -s http://localhost:30851/metrics | grep relay_time_to_first_token_seconds_count`
  → `{product="switch"} 1` (was absent) and no series for a non-stream call made without the
  header. Cache: the same ≥2k-token prompt twice, then bridge session
  `GET /api/v2/lurus/logs/stat` → `cache_read_tokens` present; > 0 only if the live upstream
  reports prompt-cache hits, else 0 honestly (the hermetic tests carry the correctness proof).
- enterprise acceptance:
  - Streaming latency to first token is graphable per product from `/metrics` by the existing
    netdata scrape with zero collector changes, and every new series has a live writer enforced
    by the honesty gate.
  - Prompt-cache savings are reportable per tenant and per product from `/logs/stat` without
    scanning rows, using the same JSON projection pattern as `source_product`.
- consumer note: additive metric and two additive JSON fields in `/api/v2/:slug/logs/stat`
  totals and by_product; no consumer reads that shape today; by_product keeps its documented
  window-wide semantics.
- cross-repo follow-up: none; the netdata go.d job scrapes the series unchanged.

### L6 — CHANNEL-SAVE-VALIDATION: param/header override, model mapping and settings JSON validated at v2 and legacy save time

- kind: fix · candidates: C14-CHANNEL-SAVE-VALIDATION · effort: S · migration: none
- files:
  `internal/adapter/provider/common/channel_config_validate.go` (new),
  `internal/adapter/provider/common/channel_config_validate_test.go` (new),
  `internal/adapter/handler/v2_channel.go`,
  `internal/adapter/handler/v2_channel_config_validation_test.go` (new),
  `internal/adapter/handler/channel.go`
- test packages: `./internal/adapter/provider/common`, `./internal/adapter/handler`
- spec: new file in the same package as `override.go` (L2 edits `relay_info.go` in this package,
  a different file): `ValidateParamOverride(raw string) error` — trim, `json.Valid`, Unmarshal to
  map, if `tryParseOperations` (`override.go:51`) succeeds dry-run `applyOperations` (:311)
  against the probe body `{"model":"probe","messages":[{"role":"user","content":"x"}]}` with an
  empty condition context, else dry-run `applyOperationsLegacy` (:297); `ValidateHeaderOverride`
  — object of string values with valid header-token keys; `ValidateModelMapping` — object of
  non-empty string → non-empty string (single-hop, no cycle rule); `ValidateChannelSetting` —
  `json.Valid` + non-strict Unmarshal into `dto.ChannelSettings` (`dto/channel_settings.go:3`) so
  rows with extra keys stay editable while type mismatches fail. Code constants in the same file:
  `types.ErrorCodeChannelParamOverrideInvalid` (`error.go:64`, existing) for param override and
  new strings `channel:header_override_invalid`, `channel:model_mapping_invalid`,
  `channel:setting_invalid` (defined here, not in `error.go` which L3 owns). Handler:
  `CreateChannelV2` before `validateChannelEgress` (:340) and `UpdateChannelV2` before
  `ValidateSettings` (:487) call the four validators on the incoming non-nil fields
  (:466-483); on failure 400 `{success:false, code:<above>, message naming the field}` before any
  DB write. Legacy `channel.go:1043` and `:1054`: replace bare `json.Valid` with
  `ValidateParamOverride`/`ValidateHeaderOverride`, keeping the existing 200 + `success:false`
  shape. Existing invalid rows keep working until edited (relay-time
  `ErrorCodeChannelParamOverrideInvalid` with SkipRetry stays the backstop).
  - Do NOT touch: `override.go`, `error.go`, `entity.Channel`, relay code, `web/`.
- oracle: `go test -count=1 -p 2 ./internal/adapter/provider/common ./internal/adapter/handler`.
  `channel_config_validate_test.go`: `{` → error; a valid operations document → nil; legacy map
  `{"temperature":0.2}` → nil; an operations document with an unknown mode → error naming the
  mode; header `[1]` → error, `{"X-A":"b"}` → nil, `{"bad header":"x"}` → error; mapping
  `{"":"x"}` and `{"a":""}` → error; setting `{"proxy":1}` → error, `{"proxy":"http://p"}` → nil,
  `""` → nil. `v2_channel_config_validation_test.go` (harness of
  `TestUpdateChannelV2_PartialUpdate`, `v2_channel_test.go:172`): PUT `param_override '{'` → 400
  code `channel:param_override_invalid` and the re-read row unchanged; valid operations document
  → 200 and stored; `header_override '[1]'` → 400; `model_mapping '{"":"x"}'` → 400; `setting
  '{"proxy":1}'` → 400; POST create with `'{'` → 400 and no row; legacy tag path with `'{'` →
  200 `success:false` (shape locked).
- mutation: remove the validator calls from `UpdateChannelV2` → the `'{'` case returns 200 and
  the row changes (red); make `ValidateParamOverride` skip the dry-run → unknown-mode case
  passes (red); replace the legacy path's validator with bare `json.Valid` → a syntactically
  valid but semantically broken operations document is accepted (red).
- UAT probe: bridge admin session
  `PUT https://test-newhub.lurus.cn/api/v2/lurus/channels/<id> {"param_override":"{"}` → 400
  code `channel:param_override_invalid` (today 200, then every relay through that channel fails);
  `PUT {"param_override":"{\"operations\":[{\"path\":\"temperature\",\"mode\":\"set\",\"value\":0.1}]}"}`
  → 200; a live-model relay through the channel → 200; `PUT {"param_override":""}` to restore.
- enterprise acceptance:
  - A console typo in any of the four channel JSON documents is rejected at write time with a
    field-naming code on both the v2 and legacy save paths, so a channel's traffic can no longer
    be taken down by a saved config error that only surfaces at relay time.
  - Validation is a dry-run of the same override engine the relay uses, not a parallel schema.
- consumer note: console/admin API only; v2 channel create/update return 400 with a code for
  invalid documents (today stored and failing at relay). No relay-wire change.
- cross-repo follow-up: none.

## 7. Lane summary

| Order | Lane | Kind | Effort | Migration | Hot-path reject added? |
|---|---|---|---|---|---|
| 1 | L1 RL-HEADROOM | borrow | S | none | no (headers only) |
| 2 | L2 REQUEST-IDENTITY | borrow | L | none | no (two read-only endpoints) |
| 3 | L3 CONTRACT-TAXONOMY + DOC LOCK | fix | M | none | no (same status codes, typed bodies) |
| 4 | L4 POOL-RENEWAL-AND-ALERT | un-dead | M | none | no (observe default; alert is post-success) |
| 5 | L5 TTFT-AND-CACHE-STAT | borrow | S | none | no |
| 6 | L6 CHANNEL-SAVE-VALIDATION | fix | S | none | no (admin write path 400) |

## 8. File disjointness (every file, one owner)

- `internal/adapter/middleware/`: L1 = `rate-limit.go`, `business_rate_limit.go`,
  `business_rate_limit_test.go`, `rate_limit_headroom_test.go`, `business_model_rate_limit.go`,
  `business_model_rate_limit_test.go`, `concurrency_limit.go`, `concurrency_limit_test.go`;
  L2 = `request-id.go`, `request_id_test.go`; L3 = `utils.go`, `distributor.go`, `auth.go`,
  `jimeng_adapter.go`, `model-rate-limit.go`, `r5e_model_rate_limit_default_test.go`,
  `cost_spike.go`, `entitlement.go`, `pool_balance_check.go`, `rejection_envelope_wire_test.go`,
  `distributor_status_test.go`, `entitlement_product_test.go`, `abort_code_structural_test.go`.
- `internal/app/relay/helper/`: L1 = `perception.go`, `perception_extra_test.go`.
- `internal/pkg/common/`: L2 = `constants.go`.
- `internal/pkg/types/`: L3 = `error.go`, `error_test.go`, `error_more_test.go`.
- `internal/adapter/provider/common/`: L2 = `relay_info.go`, `cov_prov-common_relay_info_test.go`;
  L6 = `channel_config_validate.go`, `channel_config_validate_test.go`.
- `internal/app/governance/`: L2 = `governance.go`, `enrich_test.go`, `classification.go`,
  `classification_test.go`; L4 = `audit_action.go`, `audit_test.go`.
- `internal/app/`: L2 = `log_other_projection_lock_test.go`; L3 = `error.go`, `error_test.go`,
  `r2_channel_token_quota_test.go`; L4 = `credit_pool_reset.go`, `credit_pool_reset_test.go`,
  `credit_pool_reconcile.go`, `credit_pool_reconcile_test.go`, `quota.go`, `pool_debit_test.go`.
- `internal/domain/entity/`: L2 = `log.go`.
- `internal/adapter/repo/`: L2 = `savings.go`, `log.go`, `log_source_product_filter_test.go`,
  `log_request_identity_test.go`; L4 = `tenant_credit_pool.go`, `tenant_credit_pool_test.go`.
- `internal/pkg/nats/`: L4 = `pool_threshold.go`, `pool_threshold_test.go`,
  `pool_threshold_gorm_test.go`.
- `internal/adapter/handler/`: L2 = `v2_log.go`, `v2_log_test.go`, `v1_generation.go`,
  `v1_generation_test.go`, `key_info.go`, `key_info_test.go`; L3 = `relay.go`, `billing.go`,
  `cover_r2_billing_test.go`, `relay_inband_error_test.go`; L4 = `internal_maintenance.go`,
  `internal_maintenance_test.go`; L5 = `relay_outcome.go`, `relay_outcome_test.go`,
  `v2_log_stat.go`, `v2_log_stat_test.go`; L6 = `v2_channel.go`,
  `v2_channel_config_validation_test.go`, `channel.go`.
- `internal/adapter/handler/router/`: L2 = `dashboard.go`, `cov_router-relay_wiring_test.go`;
  L3 = `openapi_contract_lock_test.go`; L4 = `internal-api-router.go`,
  `internal_admin_reset_pools_route_test.go`.
- `internal/pkg/metrics/`: L4 = `metrics.go`, `metrics_extra_test.go`,
  `alert_wiring_honesty_test.go`; L5 = `ttft.go`, `ttft_test.go`.
- Docs/config: L3 = `docs/openapi/relay.json`, `doc/product-integration-guide.md`;
  L4 = `doc/runbook/pool-threshold-alert.md`, `.env.example`; L5 = `doc/decisions/observability.md`.
- `web/`: no lane. `cmd/`: no lane. `migrations/`: no lane.

No file appears under two lanes (checked by name across the lists above).

## 9. do_not_regress (every lane's acceptance re-checks the items its files touch)

- Streaming-safety failover gate: no retry once any byte was flushed, suppression counted —
  `relay.go:610,885` (`RecordFailoverSuppressed`); must stay ahead of any policy-driven retry.
- Circuit breaker fed only by upstream-attributable failures — `relay.go:462,478` via
  `types.IsUpstreamFailure` (`error.go:404`). L3 does not change `IsUpstreamFailure` or the
  `RelayErrorType` code classes (:445-461).
- Per-attempt route trace persisted with a hard cap — `internal/app/route_attempts.go` (catalog);
  untouched this cycle.
- Live EWMA-shaded weighted draw — `repo/channel_cache.go` (catalog); untouched.
- All-keys-cooling → 503 + Retry-After and `RetryAfterUnix` → Retry-After — `relay.go:178-200`;
  untouched.
- In-band wire-native error frames after a stream has started; `StreamEndReason` → status
  `client_gone` — `relay_outcome.go:46-61`; L5 adds an observation, never changes the status
  derivation.
- Cost/quota perception headers and `x_lurus` field names — `perception.go:134-151`,
  `types/lurus_extension.go`; L1 deletes only the dead rate-limit helper.
- Cache pricing keyed on wire semantics — `perception.go:58-70` (catalog); untouched.
- Product attribution resolved once and written to every row — `governance.go:105-110`; L2 adds
  keys beside it, never changes it.
- Default-deny projection gate — `log_other_projection_lock_test.go:340`; every new `other.*`
  key (L2's three) is classified; L5 writes none.
- Sensitive-word rejection semantics — `relay.go:281-308` (catalog); untouched.
- Rate limiting fails open with an explicit log, Retry-After from the oldest in-window entry, TPM
  from settled usage — `business_rate_limit.go:224-227,126-133,257-275`; L1 keeps all three.
- Relay gate order — `relay-router.go:100-121`; L3 publishes it, no lane reorders it.
- Pool threshold publisher's fail-closed dedup (schema mark before publish) —
  `nats/pool_threshold.go:180-235`; L4 calls it (with a nil publisher when NATS is off), never
  reimplements it.
- Key rotation CAS — `repo/token.go` (catalog); untouched.
- Env-gated fault simulator — `faultsim.go:61-67`; untouched.
- Metrics honesty rule (PR #170): no declared-but-never-written series —
  `declared_series_written_test.go:146`, `alert_wiring_honesty_test.go`; L4 and L5 each ship a
  live writer.
- Leader-only batched audit retention — `lifecycle/audit_cleanup.go`; untouched.
- Inbound error-body parsing keys on `Code` for `token_quota_exhausted` and on vendor `Type`
  values — `app/channel.go:88-120`; L3 proves both body shapes round-trip.
- Pinned consumer shapes: `GET /api/v2/switch/user/info` (`switch_user_info.go:81`),
  `/api/v2/lutu/search`, `/internal/v1/*` — untouched.
- Migration ledger: ID 033 remains `(next available)`; no lane writes under `migrations/`.

## 10. Cross-repo follow-ups (owner executes in the root repo; no lane writes there)

1. `doc/coord/contracts.md` newhub relay row: per-wire error type table, non-empty `code`, the
   new codes, header directory (`X-Request-Id` in/out with deprecated alias, `X-RateLimit-*` with
   the advisory TPM caveat), `GET /v1/key`, `GET /v1/generation`, `X-Session-Id` / `user`
   hashing statement, link to guide §B/§E/§F (L2, L3).
2. `doc/coord/contracts.md` event registry: register the pool-threshold NATS event as emitted by
   newhub's debit path (today 0 hits) with the audit action name; note NATS-off environments
   record `delivery=recorded_only` (L4).
3. `doc/coord/changelog.md`: one entry summarising the cycle's wire changes (types, codes,
   headers, two endpoints, pool events) and the `RATE_LIMIT_HEADERS_ENABLED` /
   `CREDIT_POOL_RESET_MODE` knobs.
4. Notify platform / creator / lucrum teams that relay through hub as a channel: 401/403 now carry
   vendor types, which their inbound parser treats as auto-disable (intended).
5. `deploy/` (separate repo): `CREDIT_POOL_RESET_MODE=enforce` on the UAT overlay for the L4
   probe; never on production before the owner decision in §11.
6. `doc/decisions/`: record the refill-to-ceiling vs zero-then-topup decision once made.

## 11. Owner items (decisions no lane may take)

1. Pool reset semantics before `CREDIT_POOL_RESET_MODE=enforce` on production: refill-to-ceiling
   (implemented; overdraft forgiven) vs zero-then-topup; interaction with stranded-topup reconcile
   (reset never touches `credit_pool_fund_events`).
2. Migration 033: this cycle leaves it unspent. Next cycle's candidates are C24 per-key renewing
   budget (needs a decision on `budget_used` vs `RemainQuota` double-counting and on retiring the
   deprecated tenant `MaxQuota` gate) and ATTR-1 (`source_product` column; JSON expression still
   suffices at 24k rows). Reserve line 134 of the ledger only when one is chosen.
3. UAT: mint an admin-scope internal key on `newhub_uat` R6-side for the L4 probe (the live
   `platform-core` key has no admin scope); set `CREDIT_POOL_RESET_MODE=enforce` on the UAT
   overlay for the enforce half of the probe and revert afterwards.
4. Deprecation window for the `X-Oneapi-Request-Id` alias (proposed: one release after L2 ships).
5. Next cycle's `BIZ_TPM_MEASURE` default flip (C06) once the divergence histogram has live data.
6. Whether the tenant `/v1/models` list should hide models a tenant allow-list excludes (C21)
   before the allow-list lane is scheduled.

## 12. Deferred (with the reason and the spec to reuse)

- C24-TOKEN-PERIODIC-BUDGET — spends the single migration on always-on money-path writes
  (`repo/token.go` decrease/increase expressions) with an out-of-repo ledger reservation as a
  merge precondition and an open owner decision; zero live tenants asking. Spec to reuse (buyer
  L-D): columns `budget_limit/budget_period/budget_used/budget_reset_at` (PG-only, idempotent,
  defaults 0/''), lazy calendar reset in `pre_consume_quota.go` after the tenant-quota block,
  `TOKEN_BUDGET_ENFORCE` default observe with `token_budget_denials_total{tenant_id,mode}`, 402
  `token_budget_exceeded` with `RetryAfterUnix = budget_reset_at`; amendment from judge 1: check
  the cached token's `BudgetPeriod/BudgetLimit` before any DB read so tokens without a budget add
  no query.
- C06-TPM-TOKENS-NOT-QUOTA — `quota.go:944-960` (L4's file) and `business_rate_limit.go` (L1's);
  no live tenant has a non-zero tpm_limit. Spec to reuse (architect L2 part B): parallel
  `rl:biz:tpm:tokens:*` windows, `BIZ_TPM_MEASURE=quota|tokens` default quota,
  `PostConsumeQuotaWithUsage` carrying `prompt+completion` tokens from the three settlement callers,
  divergence histogram `rate_limit_tpm_tokens_per_quota{scope}`, flip the default next cycle.
- C21-TENANT-MODEL-ALLOWLIST — needs L3's `model_blocked` code and `distributor.go` (L3's this
  cycle). Spec to reuse (architect L1 step 3): `tenant_configs` key `models.allowlist` via
  `repo.GetTenantConfigJSON` (`tenant_config.go:117`) with a 30 s cache, check placed after
  `original_model` is set and BEFORE the ability lookup so denial is a typed 403 never a 503,
  `TENANT_MODEL_ALLOWLIST_MODE` default observe, `tenant_model_denied_total{tenant_id,action}`,
  GET/PUT `/api/v2/:slug/admin/tenants/:id/model-allowlist`.
- C02-HEADER-CONTRACT (attempts/upstream-model half) — one `SetRelayResponseHeaders(c, info,
  attempts)` writer on every non-stream success branch and at stream start (`helper/common.go:59-64`,
  `perception.go:134-151`, `relay.go` dispatch) with `X-Lurus-Model`, `X-Lurus-Upstream-Model`,
  `X-Lurus-Attempts`; lands on the canonical id next cycle.
- C03 provider-metadata half — merge `{request_id, gateway_code, provider_name (only when
  IsUpstreamFailure), upstream_status}` into `error.metadata` in the deferred renderer
  (`relay.go:150-238`), Anthropic wire as a sibling `x_lurus` key; deferred to keep L3 at M.
- C08 cache token types on `tokens_processed_total` — writer `repo/log.go:451` is L2's file;
  `/logs/stat` totals land in L5 instead.
- C11-RETRY-POLICY, C12-PER-CHANNEL-TIMEOUTS, C13-KEY-COOLDOWN-GENERIC, C15-GUARDRAIL-OBSERVE —
  all own `handler/relay.go` (L3's single literal this cycle) and cannot show a routing difference
  on UAT beyond the fault simulator with one live channel; C11 is the first relay.go lane next
  cycle (backoff with jitter, same-tier exclusion, `X-Lurus-Retry: off`, RouteAttempt
  `FailureOrigin/WillRetry`), C12 after L5's TTFT distribution exists, C13 after C11's KeyIndex,
  C15 after the dead `StopOnSensitiveEnabled`/`StreamCacheQueueLength` options are deleted.
- C04-USAGE-EVERY-WIRE — Anthropic/Google-wire `x_lurus` injection plus an always-emit OpenAI
  usage frame behind a flag; needs a consumer tolerance confirmation from switch/lutu and a third
  column in the billing invariance matrix; next cycle's first billing lane.
- C09-VARIANT-PRICING — money-correct S but no suffixed model name observed live; carry.
- C10-TASK-POLL-BOUND — no task/MJ callers live; refund semantics need an owner decision.
- C17-MODELS-PRICING-AVAILABILITY — changes `/internal/models/catalog` `available` semantics for
  platform (consumer note) and needs one price helper shared with the charge path.
- C18-AUDIT-CHANGESETS — spans five write handlers incl. `v2_channel.go` (L6) and
  `tenant_credit_pool.go` (L4); next cycle on settled code.
- C19-TENANT-DAILY-BUDGET — depends on C06's truthful token measure and L1's headers.
- C20-LOG-RETENTION — operator L6 design kept: leader-only clone of `audit_cleanup.go`,
  `LOG_RETENTION_DAYS` default 0 = observe with a past-retention gauge, batched
  `DELETE ... WHERE id IN (SELECT id ... ORDER BY id LIMIT ?)` because `DeleteOldLog`
  (`repo/log.go:738`) relies on `Limit().Delete()` which the PG dialect drops; needs a PG-tier
  test and a UAT log-line probe for the `main.go` wiring.
- C22-LB-OBSERVABLE-MODEL-HEALTH — `channel_cache.go`/`route_attempts.go` edits ride C11.
- C23-CHANNEL-KEY-AT-REST — KEK provisioning/backup/DR runbook must precede even a flag-off
  landing (KEK loss disables every channel).
- Previous cycle carry (aggressive-consolidation-cycle-2026-09-07b.md §3/§6): OBS-R2 per-replica
  label, OBS-R4 leader liveness, BILL-FORMULA-1 cost-estimate collapse (waits on C04 in
  `perception.go`), AUTH-1 one session principal resolver (do it on L3's typed-code base),
  UX-MODELS-1 one tenant-models hook, UX-SETTINGS-HONEST fabricated tiers, TI-F2 legacy task/mj
  tenant scoping, TI-4 shared SQLite bootstrap, XP-3 switch publish round-trip, BILL-C allow-list
  (platform seed rows), ATTR-1 migration 033 column (still deferred), L1 seam-guard consolidation
  with the corrected spec (six inline internal-key tenant guards → one helper with audit+metric;
  drop the unverified `/internal/balance/topup` claim). DOC-1 gets its first real lock in L3; full
  regeneration of `relay.json` remains deferred.
- v2 Redemption page edit/enable-disable/search (no PUT route) — console gap needing a PUT route
  plus page work with i18n parity and casing gates; next web-focused cycle.

## 13. Next cycle

1. First relay.go lane: C11 retry policy with C03's provider metadata and C02's attempts header
   set (one RouteAttempt revision, one header writer).
2. C06 tokens-measure dual window (default quota) and, on its evidence, C19 tenant daily budget.
3. C21 tenant model allow-list on top of `model_blocked`, with C17 priced/available models.
4. Migration 033 decision (C24 vs ATTR-1) — owner item 2.
5. Web-focused lane bundle: Log page `request_id`/`session_id` filter inputs (six locale files),
   v2 Redemption PUT + page, UX-MODELS-1, UX-SETTINGS-HONEST.
6. Ops lane bundle: C20 log retention (observe default), OBS-R2/OBS-R4, L1 seam-guard
   consolidation, C23 KEK runbook.
7. Re-score §3 against live data (TTFT distribution, `credit_pool_reset_total`,
   `credit_pool_alert_total`, headroom headers observed by the switch client).

## 14. Operator amendments after the vet (binding; override the lane text above where they conflict)

All six lanes were approved (no lane majority-refuted). Each lane's single refuter raised a salvageable
spec defect that was verified against HEAD 1bd6a20f and is folded in here:

- **L1** — Do NOT relax `business_rate_limit_test.go:147-148`; nothing in this lane changes the JSON
  `error.type`, so that lock stays untouched and green. After deleting `helper.SetRateLimitHeaders`,
  remove the then-unused `fmt` import from `perception.go` (verify with a grep for `fmt.` first).
- **L2** — The success-path oracle cannot ride `relay_attribution_test.go:76` (that harness only proves
  the sensitive-word rejection path). Build a new hermetic success fixture in `internal/adapter/handler`
  (`relay_success_fixture_test.go`): seed a real `entity.Channel` pointing at an `httptest.NewServer`
  OpenAI-shaped upstream that returns a chat completion with usage, and drive `POST /v1/chat/completions`
  through the real middleware chain (TokenAuth → Distribute → Relay) so `RecordConsumeLog` fires with a
  non-zero `Quota` and populated `ChannelMeta`; `/v1/generation` and the session/end-user assertions run
  on that row. DTO citation is `internal/pkg/dto/openai_request.go:57,:819`. L2 does NOT edit
  `doc/product-integration-guide.md` (L3 owns it and documents L2's headers/endpoints after L2 lands);
  L2's report must state the exact header/endpoint semantics for L3 to copy.
- **L3** — `distributor.go` code assignments corrected: `:77` and `:85` → `channel_specify_forbidden`
  (same family as `:59`); `:114` → `invalid_request` (missing `model`); `:129` → `group_not_allowed`.
  Add one assertion per reassigned site pinning the corrected code (not just enum membership). The guide's
  header directory must also carry L1's `X-RateLimit-*` scope/type headers and L2's `X-Request-Id`
  (inbound accepted pattern, outbound, alias deprecation), `X-Session-Id`, user-field hashing,
  `GET /v1/generation`, `GET /v1/key`.
- **L4** — Step D (alert hook in the debit success/overdraft branches) runs only after the debit has
  succeeded, is best-effort (errors → `SysError` + counter, never alter the debit result or the HTTP
  response), and uses a detached context (`context.Background()` with a ≤2s timeout), never the request
  context, because post-response work on the request context is cancelled the moment the client
  disconnects. Default `CREDIT_POOL_RESET_MODE=observe`; the observe test must prove the pool row is
  byte-identical after a tick.
- **L6** — The legacy half targets the console's real save path: invoke the four validators inside
  `validateChannelContent` (`channel.go:692`, reached by `AddChannel:787` and `UpdateChannel:1149` via
  `validateChannel`); the oracle hits `POST /api/channel/` and `PUT /api/channel/` with a malformed
  `param_override` (new `channel_config_validation_legacy_test.go`). `EditTagChannels` (`:1043/:1054`) is
  patched as a secondary path but is not what "legacy save path" means in the acceptance criteria; the UAT
  probe's legacy leg targets `PUT /api/channel/`.
