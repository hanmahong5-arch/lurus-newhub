# Key-metric uplift — 2026-08-17

Baseline taken on `main ff19c348`. Every number below was measured with the tool
CI uses; none is projected. Numbers are labelled local or CI, because this round
produced a case where the two disagreed by 207 findings (§2) — where they
disagree, CI wins for anything CI enforces. Where a claim could not be verified
at all it says so rather than being asserted.

Local harness for this round: a real PostgreSQL 16 (installed in WSL, reachable
from the Windows side at `127.0.0.1:5432`), golangci-lint v2.12.2 and gosec off
`~/go/bin`, bun 1.3.10. `-race` remains the one gate that cannot run here (no
gcc). Docker Desktop's engine was returning HTTP 500 on every API route for the
whole session, so the usual disposable-container route was unavailable — hence
WSL.

---

## 1. The largest finding: the coverage gate was measuring a fiction

`coverage-gate` ran `go test -short -coverprofile` **without**
`TEST_POSTGRES_DSN`. Every PG-gated test reaches the database through
`SetupTestDB`, which `t.Skip()`s when that variable is unset — so the job
silently excluded all of them and the number the gate watched was the coverage
of whatever happened to run hermetically.

Same tree, same tests, the only difference being the environment variable:

| package | no DSN | real PG | Δ |
|---|---|---|---|
| `internal/pkg/migration` | 13.2% | **90.6%** | +77.4 |
| `internal/app/openrouter_pool` | 48.2% | **96.4%** | +48.2 |
| `internal/adapter/repo` | 64.8% | **80.2%** | +15.4 |
| `internal/app` (gated aggregate) | 88.4% | **89.4%** | +1.0 |
| `internal/adapter/handler` | 69.1% | 69.1% | 0 |

Not one line of test code was written to produce those numbers. The 88.4%
reproduces this job's own 2026-08-09 CI measurement exactly, which is what makes
the paired figures trustworthy.

`internal/pkg/migration` is the sharpest case. It has ~2 400 lines of
`*_pg_test.go` covering the migration runner, advisory locking, the 022
convergence and the 030 seed — and the repo has been recording it as a 13%
package.

Attaching PG can only move a percentage **up** (the denominator is the
statement count either way; a DSN only un-skips tests), so this cannot turn the
gate red on its own.

The gate's own rule is that a ratchet cites CI-measured actuals, so the
thresholds were held until this branch's first CI run produced them
(run 32052999168):

| package | CI with PG | old gate | new gate | dead zone closed |
|---|---|---|---|---|
| `internal/app` | 89.4% | 84 | **86** | 5.4pt |
| `internal/adapter/repo` | 80.5% | 62 | **77** | **18.5pt** |
| `internal/adapter/handler` | 69.1% | 64 | 64 | — |

The repo one is why this mattered: 62 was derived from a 64.3% "hermetic
ceiling" that was really just the PG tests skipping, so that package could have
shed 18 points of real coverage without the gate saying a word. The repo has
been burned by exactly this before — `internal/app` sat at a gate of 25 against
an actual 88.4% for months, a 61pt dead zone.

`internal/app/coverage_honesty_test.go` reads `go-ci.yml` and fails if its
`want` map diverges from the gate lines, so it moved in the same change.

Also fixed while attaching PG: `check_pkg` hard-coded `-timeout=120s`, and
`internal/adapter/repo` takes ~220s once its PG tests actually run. Left alone,
attaching PG would have turned the gate red on the timeout, not the coverage.
Raised to 900s.

## 2. Lint debt was unwatched, and is now capped

CI ran only `golangci-lint --new-from-rev=origin/main`, which blocks *new*
issues. The standing debt was never counted, so it could grow silently.

Whole-repo count over `./internal/... ./cmd/...`, golangci-lint v2.12.2 with
this repo's `.golangci.yml`:

| | main, local | branch, local | **branch, CI** |
|---|---|---|---|
| total | 950 | 873 | **666** |
| production files | 639 | 573 | **573** |
| `_test.go` | 311 | 300 | **93** |
| staticcheck | 538 | 526 | **320** |
| errcheck | 208 | 208 | 208 |
| errorlint | 84 | 83 | 83 |
| contextcheck | 36 | 36 | 36 |
| bodyclose | 36 | 4 | **3** |
| ineffassign | 20 | 13 | 13 |
| unused | 16 | 3 | 3 |
| govet | 7 | 0 | 0 |
| nilerr | 5 | 0 | 0 |

A new blocking `lint-debt-ceiling` job enforces `CEILING=666`. It is a ratchet:
only ever lowered, each drop with a measured before/after.

### The local count was wrong, and the per-linter table is what caught it

The ceiling was first set to 873 — the local figure for the same tree. CI's own
first run printed **666**. Production findings agree exactly at 573; the entire
207-point spread is test-file staticcheck, 526 local against 320 in CI.

Cause: this machine's golangci-lint binary was built with a newer Go toolchain
than `setup-go` installs, and staticcheck's analysis moves with the toolchain it
was compiled against. Same version string, different verdict.

Two things made this recoverable. The step prints the count it observed on every
run, and the constant shipped with an instruction to correct it to CI's number
rather than widen it. And the job prints a **per-linter breakdown** — with only
a total, 873 vs 666 reads as ordinary drift; with the breakdown, "production
identical, one linter accounts for all of it" is unmistakable.

The standing rule is now written into the file: a local run is useful for
direction, never for the value.

The job carries an anti-hollow sentinel — a count of 0 fails loudly, because 0
means the run analysed nothing (bad scope, crashed linter, empty checkout)
rather than a clean repo. A gate that reports success without measuring
anything is worse than no gate.

### The ceiling job shipped broken and was caught before merge

The first version opened with `set -uo pipefail` and then called
`golangci-lint`, taking its exit code on the next line. GitHub Actions runs
`run:` steps as `bash -e {0}`, and `set -uo pipefail` does **not** clear that
errexit — so the step would have died the instant golangci-lint exited 1 (its
normal "found something" path), before any of the logic below it ran, with no
diagnostic. It would have been red on 100% of runs, and the anti-hollow
sentinel would have been dead code.

It was caught because the verification pass re-ran the shipped step bytes under
`bash -e` instead of the plain `bash` the author had used: measurement and
measured were not the same shell. Fixed with `|| LINT_RC=$?`, then re-verified
under `bash -e`:

(run while `CEILING` was still at the pre-final 877; it later became 873 as four
more findings were fixed, then 666 once CI reported its own count)

- normal path (877 issues, linter exits 1) → step prints the breakdown, exit 0
- count over ceiling → `::error::lint debt ceiling exceeded: 878 > 877`, exit 1
- hollow run (well-formed report, 0 issues, linter exits 7) → sentinel fires, exit 1
- no report written at all → fails loudly, exit 1

## 3. Frontend coverage was not low — it was unmeasurable

`web/vitest.config.js` had a complete `coverage:` block and `package.json` had a
`test:coverage` script, but `@vitest/coverage-v8` was never in
`devDependencies`. The command failed with `MISSING DEPENDENCY`, and Web CI ran
`bun run test` without coverage. The configuration had never produced a number.

Provider installed, and the first real measurement over 35 test files / 254
tests: **11.46% statements**, 12.86% branches, 11.52% functions, 11.12% lines,
across 389 source files.

Floors set at 10/11/10/10 — below the run-to-run jitter observed across three
consecutive runs, so test ordering alone can never turn CI red — and Web CI's
`test` job now runs `bun run test:coverage`, which exits non-zero on a breach.
These floors only go up.

11% is a real number and a low one. It is now visible and cannot regress, which
is the prerequisite for raising it.

## 4. `bun audit` had gone stale by three months

The comment in `web-ci.yml` said `0 crit / 0 high / 7 mod / 0 low` — a
2026-05-26 snapshot. Nothing watched it, because the gate blocks only on
`critical`. Re-measured against the npmjs registry:

    83 vulnerabilities (0 critical, 20 HIGH, 56 moderate, 7 low)

Ten packages had drifted back in-range, several of them pinned by the
`overrides` block itself (`immutable` was pinned at 5.1.5, which the new
advisory covers).

Overrides refreshed — all within-major, verified against a production build:
`immutable` 5.1.5→5.1.9, `@isaacs/brace-expansion` 5.0.1→5.0.9, `axios`
1.16.1→1.19.0 (direct dep), plus new pins for `undici` 7.29.0, `form-data`
4.0.6, `flatted` 3.4.4, `js-yaml` 4.3.1, `nanoid` 3.3.18, `postcss` 8.5.26.

    45 vulnerabilities (0 critical, 2 high, 38 moderate, 5 low)      20 high -> 2

Both remaining highs are the same advisory, GHSA-fx2h-pf6j-xcff (vite
`server.fs.deny` bypass — dev server only, not in the shipped bundle). Fixing it
needs vite > 6.4.2, i.e. the vite 5 → 8 major bump that is deliberately
deferred. So the gate stays at `--audit-level critical`; ratchet to `high` in
the PR that lands the vite major.

A plain `brace-expansion` override was tried and reverted: unnecessary once
`@isaacs/brace-expansion` is current, and forcing 5.x onto minimatch-1.x
consumers is risk without payoff.

## 5. Defects found by triaging the lint findings

Every finding was judged before being acted on: real defect → minimal fix plus a
regression test proven to go red when the production change is reverted;
intentional → `//nolint` with a written reason, code untouched.

**`getOpusDuration` reported undecodable audio as zero seconds** —
`internal/pkg/common/audio.go`. Read/seek failures inside the page loop `break`,
and the function then returned `(0, nil)`. Both callers treat that as success:
`app/token_counter.go:230` bills `ceil(0)/60*1000` = **0 tokens**, and
`provider/openai/audio.go:94` only uses its body-size fallback when
`durationErr != nil`, so a zero duration skips the `duration > 0` branch and
bills nothing. Now returns an error when no granule position was found, matching
`getAACDuration`'s existing `no valid aac frames found` convention. Revert-proof
run in this session: removing the guard turns 2 of the 3 new tests red while the
valid-stream anchor stays green.

**Two async-task fetch builders swallowed a marshal error** —
`internal/app/relay/relay_task.go`. `sunoFetchRespBodyBuilder` and
`sunoFetchByIDRespBodyBuilder` dropped `json.Marshal`'s error and returned
`(nil, nil)`, which `RelayTaskFetch` serves as HTTP 200 with a literal
`{"code":"success","data":null}` — a query failure disguised as an empty result.
The sibling video and music builders already reported it. Known consequence,
now written next to the fix: a 500 is retryable in `shouldRetryTaskRelay`
(it tests `StatusCode/100 == 5` before `LocalError`), so a corrupt row retries a
deterministic local failure. That is wasted work, not a correctness bug, and it
matches the siblings' existing behaviour; narrowing it means reordering the
retry policy, which is a separate change.

**One real response-body leak out of 37 reported** — `provider/coze/adaptor.go`.
The response from the create-chat call is consumed by `io.ReadAll` and then
dropped on all three exits while the function returns a *different* response
from the polling call. Non-stream bodies are wrapped in
`WrapNonStreamReadDeadline`, whose `time.AfterFunc` is only stopped by `Close`,
so the leak holds a timer for the default 300s. Closed after the read rather
than by `defer`, because the polling loop below sleeps 1s per iteration.

The other 32 bodyclose reports are false positives with one shared cause: the
vendor adaptors return `*http.Response` **upward** and the relay layer closes it
(`openai/relay-openai.go`, `stream_scanner.go`, `app/error.go`). Adding `defer
Body.Close()` there would truncate SSE streams. They carry `//nolint:bodyclose`
with the reason; the code is unchanged. 4 further reports sit outside this
change's scope and remain.

**5 `ineffassign` reports on `err` in `internal/adapter/repo` were all benign** —
`var err error = nil` zero-value initialisation in `token.go` (×2),
`ability.go`, `channel.go`, `redemption.go`. Given `token.go` and
`redemption.go` sit on the money and token-lifecycle paths this needed checking
rather than assuming; each was read in full and the error is checked or returned
in every case. Fixed by deleting the redundant ` = nil` — not by suppressing the
finding, which was the first attempt and which would have left the dead
assignment in place while quietly lowering the debt count.

**11 unused symbols removed**, including `JWKSManager.autoRefresh`. That one was
checked before deletion, because a JWKS refresher that is never started means
the process verifies signatures against stale keys after an IdP re-key. It is a
deprecated context-less wrapper; the constructor starts
`autoRefreshWithContext` via `SafeGoWithContext`, so background refresh is
wired. A regression test now locks that wiring — the pre-existing constructor
test asserts only the initial fetch and stays green if the background line is
removed.

**7 `govet` `reflect.Ptr` → `reflect.Pointer`** in `pkg/common/redis.go` and
`pkg/setting/config/config.go`. Alias since Go 1.18, zero behaviour change.

## 6. Verification actually run

    go build ./...                                        exit 0
    go vet ./...                                          exit 0
    go test -count=1 -short ./internal/... ./cmd/...       86 packages ok, exit 0
      (with TEST_POSTGRES_DSN — the PG tier really ran, not skipped)
    golangci-lint run --new-from-rev=origin/main ./...     0 issues
    gosec -severity high -confidence medium ./...          0 findings
    lint-debt count, CI's exact command                    873 local / 666 in CI
    lint-debt-ceiling step under `bash -e`                 pass + 3 negative tests
    cd web && bun run lint                                 clean
    cd web && bun run test:coverage                        35 files / 254 tests, 11.46%
    cd web && bun run build                                production build succeeded
    cd web && bun audit                                    45 vuln (0 crit, 2 high)

Not verified locally, deliberately stated as such:

- **`-race`** — no gcc on this machine. CI is the oracle. The new tests touch
  `internal/pkg/common` globals and start a background JWKS refresher, so this
  is the gate most likely to have something to say.
- **`bun run eslint`** — fails on this machine with *No files matching the
  pattern* even on an unmodified `HEAD` checkout, so it is a pre-existing local
  limitation, not a regression from this change. Verified by restoring HEAD's
  `package.json` + `bun.lock`, reinstalling, and reproducing. CI runs it on
  ubuntu and has been green.
- **The coverage numbers CI will print** with PG attached. The thresholds are
  therefore unchanged; ratchet after CI reports.

## 7. What was deliberately not done

- **No bulk lint remediation.** 666 findings remain and most sit in
  upstream-derived New API files. This fork syncs from upstream periodically;
  rewriting those files for lint cosmetics makes every future cherry-pick
  conflict. The ceiling caps the debt without paying that cost. Pay it down when
  a file is being edited for a real reason.
- **No further coverage-threshold ratchet** beyond the CI-measured one in §1.
  `internal/adapter/handler` stays at 64 against 69.1%: it carries the widest
  buffer of the three because it has the most cross-test global state, and
  nothing this round changed that.
- **No `bun audit --audit-level high`.** See §4 — it still exits 1.
- **`CEILING` was not set to the pristine-main 950.** That would have handed
  back 284 points of silent room. It is set at 666 — the number CI itself
  printed.
