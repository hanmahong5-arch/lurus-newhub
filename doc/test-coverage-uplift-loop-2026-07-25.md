# Coverage Uplift Loop — 2026-07-25

Base: `origin/main` @ `378d7eb3`, worktree `2b-svc-newhub-cov`, branch
`test/coverage-uplift-2026-07-25`. Real PostgreSQL 16 at `127.0.0.1:5439`
(`TEST_POSTGRES_DSN`), so the repo/migration tiers actually execute instead of
skipping.

## Why a new base

The tree this loop was requested from (`fix/tenant-isolation-and-moneypath-hardening`)
is **66 commits behind `main` and missing 34 test files that already exist on
main**. Adding tests there would have duplicated existing work and targeted stale
business code. The loop therefore runs on a worktree cut from `origin/main`; the
two pre-existing worktrees are untouched.

## Measured baseline

| metric | value |
| --- | --- |
| total statements | 39 679 |
| uncovered | 19 093 |
| **coverage** | **51.9 %** |
| packages | 95 |
| suite result | green after the one fix below |

`cmd/server` and `web` did not compile until `web/dist` was generated
(`//go:embed dist`), so a frontend production build is part of the loop's
setup, not an optional extra.

### Pre-existing defect found by the baseline

`internal/app/openrouter_pool/writer_test.go` —
`TestMaybeMarkCooldown_AllGuardsPassed_RequiresDB` guarded only on
`testing.Short()`. A plain `go test ./...` is neither short nor
DB-initialised, so the test dereferenced a nil GORM handle and **panicked the
whole package**. It never surfaced because CI's coverage gate runs `-short` and
the PG gate runs `-run Integration`. Fixed by gating on `repo.DB == nil` as
well. Test-file-only change.

## Loop

One round = six steps. Steps 1, 2, 4 and 6 are run by the orchestrator and are
authoritative; steps 3 and 5 fan out to writer / verifier sub-agents.

1. **Measure** — `go test -count=1 -coverprofile -covermode=atomic ./...`,
   then rank packages by *uncovered statements*, not by percentage. A package
   at 0 % with 40 statements is worth less than one at 80 % with 5 000.
2. **Plan** — for each target package, write the acceptance checklist from a
   *business* angle (below), not a line-coverage angle.
3. **Write** — one sub-agent per package. New `*_test.go` files only.
4. **Gate** — orchestrator re-runs build / vet / the package suite and measures
   the real delta. Anything that fails is reverted, not patched around.
5. **Verify** — adversarial reviewers look for hollow tests: assertions that
   would still pass if the function under test were replaced by a stub.
6. **Converge** — stop after two consecutive rounds under +0.5 pp.

### Business dimensions every target is tested against

1. **Money path** — quota, billing, credit pool, redemption, refund: negative
   values, overflow, concurrency, idempotency, partial failure.
2. **Tenant isolation** — cross-tenant IDOR, missing / empty / drifted
   `tenant_id`, scope leakage in list endpoints.
3. **Auth** — expired, disabled, over-quota, wrong-group tokens; admin vs user;
   step-up.
4. **Relay routing** — channel failover, timeout, retry, stream interruption,
   upstream 4xx/5xx classification.
5. **Data boundaries** — nil, empty, oversized, Unicode, negative, off-by-one,
   time zones.
6. **Concurrency & idempotency** — duplicate requests, races.

## Invariants (checked every round; non-negotiable)

| id | invariant | oracle |
| --- | --- | --- |
| I1 | builds | `go build ./...` exit 0 |
| I2 | vets | `go vet ./...` exit 0 |
| I3 | suite green on real PG | `go test -count=1 ./...` exit 0 |
| I4 | zero production-code change | `git diff --stat` empty; only `??` `*_test.go` in `git status` |
| I5 | no commit, no push | `git log --oneline -1` unchanged from `378d7eb3` |
| I6 | no tool/assistant names in new files | grep |

## Round log

### Baseline correction

The first profile put the project at 51.9 % over 39 679 statements, but two
packages were missing from the denominator entirely: `cmd/server` (275
statements — did not compile without `web/dist`) and `internal/app/openrouter_pool`
(137 statements — Go emits no profile for a package that panics). Same-denominator
baseline is therefore:

**20 586 / 40 091 = 51.35 %**

Every round below is measured with the identical command
(`tmp/measure.sh`, `node_modules` excluded), so the deltas are like-for-like.

### Round 1 — 7 packages, 14 agents, 0 errors

| package | before | after | verdict |
| --- | --- | --- | --- |
| `internal/pkg/dto` | 0.0 % | **99.6 %** | SOLID |
| `pkg/ionet` | 0.0 % | **98.8 %** | SOLID |
| `internal/domain/entity` | 1.6 % | **96.7 %** | SOLID |
| `internal/adapter/provider/claude` | 26.6 % | **95.2 %** | SOLID |
| `internal/adapter/provider/gemini` | 8.8 % | **93.0 %** | SOLID |
| `internal/pkg/search` | 0.0 % | **90.5 %** | SOLID |
| `internal/adapter/provider/openai` | 4.9 % | **78.7 %** | SOLID |

**Total 51.35 % → 61.32 % (+9.97 pp).** Full suite exit 0 on real PG.
703 new test cases across 53 files. Production code diff: empty.

One verifier broke the read-only rule and left a mutation (`return params` at the
top of `cleanFunctionParameters`) in `relay-gemini.go`; it was reverted by the
orchestrator and the package re-measured green at 93.0 %. Silver lining: those
10 failures under mutation are direct evidence the gemini tests are not hollow.
The Round 2 verifier prompt bans file edits outright.

### Round 1 findings — production defects found by the tests, none fixed

Reported, not patched: the loop's contract is test-only. Five were re-verified
by the orchestrator at `file:line`; all five hold.

| # | location | defect | impact |
| --- | --- | --- | --- |
| F7 | `provider/claude/relay-claude.go:762` | `claudeResponse.Usage.ServerToolUse` dereferenced unconditionally after the mode switch — completion mode never populates `Usage` | **panic** on the mainline completion success path |
| F8 | `provider/claude/relay-claude.go:740-747` | `Usage` is `*ClaudeUsage` with `omitempty`, dereferenced with no nil guard | **panic → ungraceful 500** when upstream omits `usage` |
| F9 | `provider/claude/relay-claude.go:596-605` | same for `claudeResponse.Message` and its nested `Usage` on `message_start` | **panic** in the stream goroutine; `SafeGo` swallows it, so the stream silently stalls |
| F10 | `provider/claude/relay-claude.go:250-253` | empty-role default written to `textRequest.Messages[i]` but read from the pre-mutation range copy `message` | empty role reaches upstream and spuriously trips placeholder injection |
| F5 | `internal/pkg/search/client.go:246-262` | `for i := 0; i < RetryCount` — `RetryCount <= 0` runs `fn` **zero** times, then returns `fmt.Errorf("... %w", err)` with `err == nil` | operation silently skipped; error renders as `%!w(<nil>)` |
| F6 | `provider/gemini/relay-gemini.go:462-513` | text trailing the last markdown image is never emitted | user text silently dropped before it reaches upstream |
| F4 | `internal/pkg/search/logs_index.go:99-120` | `ConvertDocumentToLog` omits `ChannelType`/`RelayMode`/`UpstreamModel`/`TotalLatencyMs` | governance fields zeroed on every search hit |
| F1 | `internal/pkg/dto/openai_request.go:139-142` | `Name` counting nested inside `if Content != nil` | token estimate undercounts → under-billing |
| F2 | `internal/pkg/dto/openai_request.go:977-988` | `input_image` branch never reads `detail` | client's image detail tier silently discarded |
| F3 | `internal/pkg/dto/error.go:53-68` | explicit `"error": null` is 4 non-empty raw bytes, so fallback fields are masked | users/logs see the literal string `null` |
| F11 | `provider/openai/relay-openai.go:328` | `streamTTSResponse` has no caller anywhere in the module | dead code |

### Round 2 — handler split by business domain + repo + common, 15 agents

| slice | result |
| --- | --- |
| `handler` channel.go | 52.4 % → 62.4 % package-wide; `FetchUpstreamModels` 8.1 %→90.5 %, `ManageMultiKeys` 3.1 %→91.3 %, `FetchModels` 0 %→91.1 % |
| `handler` relay/playground/v2_chat | SOLID |
| `handler` deployment/setup/health/provisioning | SOLID |
| `handler` pricing/ratio/option/model-write | SOLID |
| `handler` redemption/credit-pool/invoice/backfill | SOLID |
| `handler` identity + log | **agent died — stream idle timeout**, re-queued into Round 3 |
| `internal/adapter/repo` | 75.2 % → 78.3 %, SOLID |
| `internal/pkg/common` | 79.6 % → 85.0 %, SOLID |

**Total 61.32 % → 64.64 % (+3.32 pp).** Full suite exit 0. Production diff: empty.

Two verifiers returned HOLLOW on `full_suite_green=false`. Cause was a transient
duplicate-symbol clash while a concurrent writer's file was mid-save
(`func itoa` colliding with `v2_pricing_write.go:249`). The writer renamed it;
`go vet ./internal/adapter/handler/` is clean and the authoritative gate is green,
so both verdicts were snapshots of a mid-write tree, not real breakage. Cost of
the lesson: writers in the same package now must namespace their helpers.

### Round 2 findings — 11 more, several money/tenant severity

| # | location | defect | impact |
| --- | --- | --- | --- |
| F3 | `handler/provisioning.go` `RevokeProvisionedKey` | sibling `CreateProvisionedKey` / `ListProvisionedKeys` both call `repo.InternalKeyAllowedForTenant`; revoke does **not** | **tenant isolation break** — a holder of one customer's provisioning key can enumerate small integer `key_id`s and revoke another tenant's live production tokens |
| F9 | `setting/ratio_setting/model_ratio.go:432-439` | resets the live in-memory ratio map to empty **before** parsing caller JSON | one malformed admin payload → platform-wide **billing outage**, reported as "failed, nothing changed" |
| F10 | `repo/option.go:211-224` | discards the `*gorm.DB` errors from `FirstOrCreate` and `Save` | pricing writes report success during a DB outage; in-memory vs persisted **split-brain** |
| F11 | `handler/v2_models_write.go:34-43,112-117` | request accepts `model_ratio`/`completion_ratio`/`model_price`/`enable_groups`, handler persists none of them | tenant admin believes a price is set; model bills at the global default |
| F7 | `repo/token_cache.go:31` via `common/redis.go RedisHIncrBy` | incrementing cached `RemainQuota` on a key with no TTL returns nil while writing nothing | quota decrement silently dropped → token overspends until the next DB read |
| F8 | `entity/redemption.go:8` | `TenantId` has `gorm:"default:'default'"`, so a zero-value insert is coerced to the real `default` tenant | same class as the 2026-06-25 tenant-id drift bug; `""` is not a distinguishable sentinel |
| F6 | `handler/playground.go:56-60` | access-denied built without a status option → falls back to 500 | permission denials inflate the server-error rate and defeat client 401/403 handling |
| F4 | `handler/setup.go:84` | `len()` counts bytes; the message promises 1–12 **characters** | first-run bootstrap rejects legitimate short Chinese usernames |
| F5 | `handler/health.go:45` | `RedisEnabled && RDB == nil` reports `"disabled"` | a broken Redis client is indistinguishable from intentionally off |
| F1 | `handler/internal_backfill.go:59` | account id 0 still counted in `users_matched` | backfill metrics mislead the operator |
| F2 | `common/init.go:139-175` | `TASK_PRICE_PATCH` never cleared when unset | latent; only bites if hot-reload is ever added |

### Round 3 — every zero-coverage provider / task / settings package, 19 agents

| package | before | after |
| --- | --- | --- |
| `pkg/setting/system_setting` | 0 % | **100.0 %** |
| `pkg/setting/operation_setting` | 0 % | **98.5 %** |
| `provider/baidu_v2` | 0 % | **100.0 %** |
| `pkg/setting/model_setting` | 0 % | **97.3 %** |
| `handler/router` | 56.1 % | **95.8 %** |
| `provider/tencent` | 0 % | **95.4 %** |
| `provider/common` | 36.9 % | **94.3 %** |
| `provider/zhipu_4v` | 0 % | **94.1 %** |
| `provider/coze` | 0 % | **93.9 %** |
| `app/relay/common_handler` | 0 % | **93.9 %** |
| `provider/aws` | 0 % | **92.2 %** |
| `provider/cohere` | 0 % | **92.1 %** |
| `provider/dify` | 0 % | **91.4 %** |
| `pkg/logger` | 0 % | **91.4 %** |
| `provider/ollama` | 0 % | **91.2 %** |
| `pkg/setting/console_setting` | 0 % | **91.1 %** |
| `provider/zhipu` | 0 % | **90.9 %** |
| `pkg/setting/config` | 0 % | **89.8 %** |
| `pkg/setting` | 14.7 % | **86.7 %** |
| `provider` | 13.4 % | **85.5 %** |
| `app/relay` | 85.0 % | **85.2 %** |
| `provider/replicate` | 0 % | **83.3 %** |
| `provider/volcengine` | 0 % | **79.6 %** |
| `provider/baidu` | 0 % | **71.5 %** |
| `provider/vertex` | 0 % | **68.1 %** |
| `provider/ali` | 0 % | **63.6 %** |
| `provider/xunfei` | 0 % | **52.3 %** |
| `cmd/server` | 0 % | **11.3 %** |

**Total 64.64 % → 79.32 % (+14.68 pp).** Three writers (`task-batch-a`,
`task-batch-b`, `prov-longtail`) died mid-stream on API errors; their packages
were re-queued into Round 4.

Two gate failures came out of this round, both fixed test-side:

1. **A 50 %-of-runs panic in `internal/adapter/handler`.** The verifier flagged
   it, and the orchestrator reproduced it: 2 failures in 4 runs, always
   `(*ReleaseService)(nil).HandleDownload` from `release.go:180`. The release
   fixture swapped the package-level `releaseService` and restored it to its
   pre-test **nil** in `t.Cleanup`, while `DownloadArtifact` fires
   `go releaseService.HandleDownload(...)` and returns immediately — the restore
   won the race. Fixed by waiting on the goroutines' observable effects (the
   incremented download counter and the persisted log row) before the test
   returns. **6/6 green afterwards.** No production change.
2. **`provider/task/vertex` build failure**, then a panic: the killed writer left
   a missing `io` import and a subtest calling `BuildRequestURL(&RelayInfo{})`
   with no `ChannelMeta` — a shape the relay layer never produces, so it was
   testing a nil dereference rather than the business logic. Import fixed and
   the input made realistic; package now **86.3 %** green.

`internal/lifecycle` failed once during this round's measurement and then passed
6/6 on re-run with no coverage change (48.2 % throughout, nobody added tests
there). Pre-existing load-sensitive flake, not introduced here.

### Round 4 — stalled batches retried, handler/repo deepened, 17 agents

| package | before | after |
| --- | --- | --- |
| `provider/task/sora` | 0 % | **100.0 %** |
| `provider/task/ali` | 0 % | **98.8 %** |
| `provider/task/music` | 0 % | **98.8 %** |
| `provider/minimax` | 0 % | **98.7 %** |
| `provider/task/hailuo` | 0 % | **98.6 %** |
| `provider/jimeng` | 0 % | **97.2 %** |
| `provider/task/jimeng` | 0 % | **96.8 %** |
| `provider/xai` | 0 % | **96.8 %** |
| `provider/task/kling` | 0 % | **96.7 %** |
| `provider/task/gemini` | 0 % | **96.6 %** |
| `provider/task/suno` | 0 % | **96.6 %** |
| `provider/task/doubao` | 0 % | **96.2 %** |
| `provider/task/vidu` | 0 % | **96.0 %** |
| `provider/palm` | 0 % | **95.6 %** |
| `provider/cloudflare` | 0 % | **93.3 %** |
| `provider/volcengine` | 79.6 % | **91.7 %** |
| `app/relay/helper` | 88.9 % | **91.6 %** |
| `provider/ali` | 63.6 % | **90.6 %** |
| `internal/app` | 87.8 % | **90.0 %** |
| `pkg/tracing` | 29.3 % | **88.0 %** |
| `provider/openai` | 78.7 % | **87.8 %** |
| `provider/vertex` | 68.1 % | **86.1 %** |
| `provider/task/vertex` | 0 % | **86.3 %** |
| `provider/baidu` | 71.5 % | **80.8 %** |
| `internal/lifecycle` | 48.2 % | **80.0 %** |
| `internal/adapter/repo` | 78.3 % | **80.3 %** |
| `provider/xunfei` | 52.3 % | **67.4 %** |
| `internal/adapter/handler` | 63.3 % | **65.2 %** |

**Total 79.32 % → 82.39 % (+3.07 pp).** Five writers died mid-stream
(`handler-deep-a/b/c`, `prov-lt2`, `prov-2nd-pass`); the provider ones had
already written most of their files before dying, which is why `ali`, `vertex`,
`openai`, `xunfei` and `baidu` still moved.

Debris from the killed writers, all cleaned up test-side:

- **`repo`: 8 failing tests.** `cov_repo-deep_redemption_redeem_test.go` seeded
  keys as `"code-<label>-" + GetUUID()` — 40+ chars into a `char(32)` column.
  The SQLite tier accepts that silently; real PostgreSQL rejects it with
  SQLSTATE 22001, which is exactly why this loop measures against real PG.
  Fixed by clamping seed keys to the schema width.
- **`provider/volcengine`: 2 failing tests.** One had a self-consistency guard
  asserting 36 named `EventType` constants when the source has 37. The other
  round-tripped a connection-lifecycle frame through `Marshal` →
  `NewMessageFromBytes` — impossible by construction, because `readConnectID`
  consumes a 4-byte length prefix that **no `writeConnectID` ever emits**. That
  asymmetry is real but latent (those three events only ever arrive from the
  server). Replaced with direct `readConnectID` branch tests plus a test that
  locks in the asymmetry and fails loudly if a `writeConnectID` is ever added.
  Package went 82.3 % → **91.7 %**.

A verifier again edited production code for a mutation experiment
(`handler/missing_models.go`), despite the ban. Reverted within the round so the
other four handler agents were not poisoned by it; the Round 5 prompt escalates
the wording.

### Rounds 5 & 6 — narrow handler slices, core packages, provider long tail

Round 5 ran 7 narrow writers (the wide batches were what kept stalling);
Round 6 mopped up the one batch that still timed out.

| package | before | after |
| --- | --- | --- |
| `provider/mistral` · `moonshot` · `deepseek` · `perplexity` · `submodel` · `jina` · `provider/constant` | 0 % | **100.0 %** |
| `provider/mokaai` | 0 % | **98.0 %** |
| `provider/siliconflow` | 0 % | **96.4 %** |
| `pkg/entverify` | 85.1 % | **94.5 %** |
| `app/totp` | 70.7 % | **91.9 %** |
| `provider/openai` | 87.8 % | **88.7 %** |
| `pkg/common` | 85.0 % | **85.9 %** |
| `internal/adapter/repo` | 80.3 % | **80.4 %** |
| `internal/adapter/handler` | 65.2 % | **67.3 %** |

Two things worth keeping:

- The h5a verifier found a genuinely **hollow** test —
  `TestFilterChannelIdsByTenant_EmptyAndRepoError` asserted only
  `len(got) == 0` on empty input and `got == nil` on a repo error, both of which
  a `return nil` stub satisfies. Fixed by adding
  `TestFilterChannelIdsByTenant_KeepsOwnTenantDropsForeign`, which seeds
  channels in two tenants and asserts the foreign id is dropped. That is the
  helper's actual contract, and a stub fails it.
- Round 6's writers reported that the packages briefed as "0 %" were already
  covered — Round 5's stalled agent had written its files before dying. Both
  writers said so plainly and declined to pad already-100 % packages with
  duplicate tests, which is the correct call.

## Result

| | statements | covered | coverage |
| --- | --- | --- | --- |
| baseline (`origin/main` @ `378d7eb3`) | 40 091 | 20 586 | **51.35 %** |
| after 6 rounds | 40 091 | 33 372 | **83.24 %** |
| delta | — | +12 786 | **+31.89 pp** |

232 new `cov_*_test.go` files. Identical measurement command and identical
package set at both ends, real PostgreSQL throughout.

### Invariants at close

| id | invariant | result |
| --- | --- | --- |
| I1 | `go build ./...` | exit 0 |
| I2 | `go vet` over all non-vendored packages | exit 0 |
| I3 | `go test -count=1` over all 95 packages, real PG | **exit 0** |
| I4 | zero production-code change | `git diff --stat -- . ':!*_test.go'` empty |
| I5 | no commit, no push | `HEAD == 378d7eb3`, `origin/main..HEAD` = 0 |
| I6 | no assistant / CLI tool attribution in new files | grep clean (vendor and model identifiers are business data and stay) |

The only pre-existing file modified is
`internal/app/openrouter_pool/writer_test.go` — the nil-DB guard described at
the top. It is a test file.

`internal/adapter/handler` was run 4× consecutively at close: 4/4 green, so the
50 % panic rate found in Round 3 is gone.

### What was not reached, and why

- **`cmd/server` sits at 11.3 %.** Its remaining statements are inside the boot
  sequence that binds a port and blocks. Testing that honestly needs a seam in
  production code, which this loop is not allowed to add. Padding it with a
  test that starts and immediately kills a server would be theatre.
- **`internal/adapter/handler` stops at 67.3 %.** The residue is concentrated in
  `Relay()` and the Meilisearch fast path in `SearchChannels`. `Relay()` needs a
  live upstream plus a seeded billing chain; the Meilisearch path needs a
  `meilisearch.ServiceManager`, and faking one means reimplementing a 50-method
  interface — the writers flagged both rather than mocking around them.
- **`internal/app/relay` stops at 85.2 %** for the same reason: the remainder is
  the streaming relay loop against a real upstream.
- **Three writers' worth of work was lost to API stream timeouts** across
  rounds 3–5. Each was re-queued and eventually landed except the ones noted
  above.

### Loop hygiene notes for next time

1. **Writers doing the "replace the body with a stub and check the test turns
   red" self-check took it literally** and edited production files —
   `channel.go`, `deployment.go`, `entverify.go`, `openai/adaptor.go`,
   `repo/tenant.go`, `common/email.go`, `handler/missing_models.go`,
   `gemini/relay-gemini.go`. In a shared worktree that poisons every other
   concurrent agent. The orchestrator ran a revert watchdog on
   `git diff --name-only -- . ':!*_test.go'` every ~2 minutes for the last three
   rounds. Next time: state the self-check as a reasoning exercise only, in both
   the writer and the verifier prompt, from round one.
2. **Wide batches stall.** Every timeout hit an agent assigned 7+ packages or a
   whole large package. Slices of 3–5 small packages, or one file group, went
   through cleanly.
3. **Namespace helpers per writer** when several agents share a package. One
   `func itoa` collision took the whole `handler` package red mid-round.
4. **Measure against the real database.** The `char(32)` overflow and the
   `RequiresDB` nil-panic were both invisible to the SQLite tier and to `-short`.

