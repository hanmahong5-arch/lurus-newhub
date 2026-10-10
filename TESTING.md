# Testing

Three layers. Each one answers a different question, and a green result in
one says nothing about the others.

| Layer | Question it answers | Where | Runs |
|-------|---------------------|-------|------|
| 1. Unit / package | Does this function do what its author meant? | `*_test.go`, `web/src/**/*.test.jsx` | every PR (go-ci, web-ci) |
| 2. Real-chain acceptance | Does a customer get the right answer **and pay the right amount** through the real binary, real migrations and production's settings? | `web/tests/acceptance/`, `scripts/acceptance-stack.sh` | every PR (acceptance.yml) |
| 3. Live | Does the deployed instance still behave? | `web/tests/e2e/` against UAT, post-deploy verify | nightly + after each rollout (web-ci e2e) |

Why layer 2 exists: every defect that reached production in 2026-09 passed
layer 1, because layer 1 tests a *model* of the system — SQLite instead of
PostgreSQL migrations, seed data where slug equals id, the price table in
code instead of the one stored in production, money asserted as "went down"
instead of an amount (#210 charged 7.3x too little, #212 billed output at
the input price). Layer 2 runs the real thing and asserts exact amounts.

## Layer 1 — unit and package tests

```bash
go test -short ./...                       # unit (skips integration via testing.Short())
go test -race -short ./...                 # race detector — run before merging
go test -run Integration ./...             # integration (needs PostgreSQL)
go test ./internal/adapter/handler/        # one package (never a single file: it lacks the package's other files)
cd web && bun run test                     # vitest; also lint (prettier), eslint, check:casing
```

Names: `Test<Subject>_<Method>_<Behavior>`, table-driven where there are
several cases.

### A fake upstream: `internal/testkit/fakeupstream`

A test that needs an LLM vendor uses this package, not its own
`httptest.NewServer` with a hand-written SSE body:

```go
srv, url := fakeupstream.NewTest(t, fakeupstream.Config{Key: "k"})
// point the channel at url; srv.Requests() shows what the relay sent
srv.QueueFault(fakeupstream.FaultMidStreamAbort, 1)
```

It speaks OpenAI chat, OpenAI Responses, Anthropic messages, Gemini and
System One, streamed and not, reports deterministic usage (1000 in / 500 out
by default) and serves the fault modes (401, 429 with Retry-After, 500,
insufficient balance, slow headers, disconnect before the first byte,
mid-stream abort). For an adaptor test that feeds a handler directly, build
bodies with `fakeupstream.SSEData` / `SSEEvents` / `SSEResponse`.
`internal/pkg/gates/sse_helper_ratchet_test.go` counts the private SSE
builders that remain; the number only goes down.

`internal/testkit/fakeplatform` is the same idea for the platform wallet
(pre-authorize, settle, release, debit, balance), booked at the platform's
own precision (0.0001 yuan).

### Coverage: frozen

The coverage floors are frozen at their 2026-10-05 values (go-ci.yml
`check_pkg` lines and the whole-module floor; `web/vitest.config.js`
thresholds). They still fail a PR that lowers coverage. They are no longer
raised: tests written to move a percentage (the `cov_*` / `*_extra` /
`*_gap` files) found little, and new testing effort goes to layer 2.

## Layer 2 — real-chain acceptance

```bash
bash scripts/acceptance-stack.sh                    # build, start, run, tear down
ACCEPT_KEEP=1 bash scripts/acceptance-stack.sh      # leave the stack up afterwards
ACCEPT_UP_ONLY=1 bash scripts/acceptance-stack.sh   # only start it; then:
  . web/acceptance-report/stack.env && cd web && bunx playwright test -c playwright.acceptance.config.ts
```

Linux (WSL works). Needs go, bun and docker. The script starts PostgreSQL 16
and Redis on random ports, builds the web app and the binary, starts
`cmd/fakeupstream` (fake vendor + fake platform on one port) and the binary
with r6-stage's environment — unified billing on, credit pool enforced,
channel cache syncing every 60s — and runs the scenarios. The deviations
from r6-stage (no IdP, plain http, no NATS) are listed in the script.

What a scenario looks like (`web/tests/acceptance/*.spec.ts`):

- its title names its case in `doc/uat/business-acceptance-tests.md`
  (`TC-M1 …`);
- money is asserted **exactly**, on every ledger at once
  (`fixtures/ledger.ts`: user quota and used, token, credit pool, log rows,
  platform wallet), against prices written out by hand in
  `fixtures/pricebook.ts` — never against what the instance says a thing
  should cost;
- it runs at least one `reverseControl(...)` step: an input that must fail,
  checked to fail and to move no money.

The meta-gate (`web/scripts/acceptance-meta-gate.mjs`, self-test next to it)
fails the run when fewer tests or cases ran than the floor, anything was
skipped, flaky or failed, a test lacks a reverse control, the fake vendor
answered no call, or the fake platform saw a call it does not implement. It
prints the instance under test (`acceptance-report/target.json`); the stack
script refuses to run when the version answering is not the one it built.

Adding a scenario: write it against the fixtures, run it, then break the
code it guards and watch it fail — a scenario that has never been red has
not been shown to test anything.

## Layer 3 — live

`web/tests/e2e/` runs against the UAT instance (`test-newhub.lurus.cn`):
nightly at 03:00 Beijing and after each rollout (docker-image-main.yml
`verify_rollout` dispatches it and the result is posted on the PR). See
`deploy/k8s/r6-uat/README.md` for the instance and the bridge login.

## Common issues

| Issue | Cause | Fix |
|-------|-------|-----|
| `undefined: repo` | ran a single test file | test the package |
| `database connection refused` | no PostgreSQL | `go test -short ./...`, or start one and `export SQL_DSN=...` |
| acceptance: `model_not_found` in setup | the channel cache has not synced yet | setup waits up to 100s; a longer wait means the channel is wrong |
| acceptance: bridge login 429 | more than 5 logins per minute | setup logs in twice per run; wait a minute between quick reruns |
