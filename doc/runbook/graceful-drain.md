# Runbook — Graceful shutdown / drain

> **Source**: no alert wired to this (procedure, not an alert trigger) —
> read this before tuning `GRACEFUL_SHUTDOWN_TIMEOUT`/
> `terminationGracePeriodSeconds`, or investigating a pod that logged
> `graceful shutdown: budget exceeded` during a rollout.
> **Triggered by**: n/a — see "Symptom" below for when to read this.
> **Severity**: operational (an under-sized budget cuts real in-flight relay
> streams on every rolling deploy; an over-sized `terminationGracePeriodSeconds`
> only delays a rollout).
> **Last review**: 2026-09-20 (cycle-13 L11).

## Symptom

A pod's log shows `graceful shutdown: budget exceeded, cut=N in-flight
request(s)` during a rollout or node drain, or a customer reports a relay
stream cut off mid-response around a deploy window. Read this before
assuming the cut request is a bug — for a stream longer than the configured
budget, it is the documented, deliberate outcome (see "What gets cut"
below), not a crash.

## Why this needed a fix

Before cycle 13, `cmd/server/main.go`'s shutdown goroutine called
`httpServer.Shutdown(shutdownCtx)` directly. Three problems:

1. **No readiness flip.** Nothing told `GET /api/health` (the readiness
   probe) that the pod was shutting down, so kubelet kept routing new
   traffic to it for the entire window between SIGTERM and the pod actually
   dying — right when the pod is least able to finish new work.
2. **A budget timeout crashed the process.** `httpServer.Shutdown` returns
   `context.DeadlineExceeded` when the budget runs out with connections
   still open. That error propagated through the `errgroup` to `main()`,
   which called `common.FatalLog` + `os.Exit(1)` — the busiest pod (the one
   still finishing real work when the clock ran out) exited with a fault
   code instead of a clean shutdown, and nothing recorded how many requests
   were cut.
3. **No accounting.** Even on the timeout path, there was no count of how
   many connections got cut, only a generic error log line.

`internal/lifecycle/drain.go`'s `Drainer` fixes all three: `MarkDraining`
flips a process-wide flag `GetHealthDetailed` (and, once wired, `GetStatus`)
check first and unconditionally; `Shutdown` always returns a nil error
(a budget timeout is an expected outcome, not a fault) and returns the
number of connections it had to give up on.

## Timeline

```
T+0s    SIGTERM delivered to the pod (kubelet begins termination)
        ├─ preStop hook: sleep 5s — lets in-flight relay requests that are
        │  about to complete drain naturally before the app even sees the
        │  signal, and gives the Service's endpoint controller time to stop
        │  sending new connections here.
T+5s    signal.NotifyContext's ctx fires → main.go's shutdown goroutine
        runs → lifecycle.Default().Shutdown(shutdownCtx, httpServer, inflight):
        ├─ MarkDraining() — from this instant, GET /api/health and
        │  GET /api/status answer 503 {"status":"draining"} immediately,
        │  without touching the DB/Redis/billing checks.
        ├─ httpServer.Shutdown(shutdownCtx budget) — stops accepting new
        │  connections, waits for in-flight ones to finish on their own.
T+5s..  Two outcomes:
T+80s     (a) every connection finishes before the budget elapses:
              Shutdown returns nil, cut=0, process exits 0.
          (b) the budget (GRACEFUL_SHUTDOWN_TIMEOUT, prod/UAT: 75s) elapses
              with connections still open: Shutdown logs
              "graceful shutdown: budget exceeded, cut=N in-flight
              request(s)" and returns (N, nil) — NOT an error. The process
              still exits 0.
T+90s   terminationGracePeriodSeconds — kubelet SIGKILLs the pod if it has
        not exited on its own by now. With preStop=5s + GRACEFUL_SHUTDOWN_
        TIMEOUT=75s that leaves a 10s margin for the rest of run()'s cleanup
        (DB close, tracing shutdown) — deploy_consistency_test asserts
        tgps ≥ preStop + graceful + 10 (W-owned gate).
```

## What gets cut

A request is "cut" only if it is still in flight when the graceful-shutdown
budget (`GRACEFUL_SHUTDOWN_TIMEOUT`) elapses — `http.Server.Shutdown` itself
never interrupts a connection before that; it just stops accepting new ones
and waits. In practice this means:

- A relay stream (SSE, or any handler whose response takes longer than the
  budget to finish) started shortly before SIGTERM and still running when
  the budget runs out.
- **`ReadTimeout`/`WriteTimeout` stay at 0** (cycle 12 do-not-regress,
  `cmd/server/main.go`'s `buildHTTPServer` doc comment) — a request is never
  cut by a per-request timeout, only by the shutdown budget itself.
- A stream longer than `GRACEFUL_SHUTDOWN_TIMEOUT` (75s) is cut on **every**
  rolling deploy, not just incidents — `STREAMING_TIMEOUT` (300s) is the
  documented upper bound for how long a relay stream may run, and it is
  larger than the shutdown budget. Whether to raise
  `GRACEFUL_SHUTDOWN_TIMEOUT`/`terminationGracePeriodSeconds` to cover the
  full streaming timeout is **O-tgps** (owner decision, `_bmad-output/planning-artifacts/cycle13-industrial-grade-2026-09-20.md`
  §8) — this cycle ships 75s/90s, enough to cover the large majority of
  relay calls without stretching every rollout's wall-clock time by 5
  minutes.

## Readiness vs liveness during drain

Both `GET /api/health` (readinessProbe) and `GET /api/status` (startupProbe
+ livenessProbe, `deploy/k8s/r6-stage/deployment.yaml:271,283`) check
`lifecycle.IsDraining()` first and answer 503 `{"status":"draining"}` before
touching any dependency. This is deliberate for both probes, not just
readiness:

- **Readiness** failing pulls the pod out of the Service's endpoint list —
  this is the whole point, it must happen the instant draining starts.
- **Liveness** failing during the drain window does **not** cause an *extra*
  restart: SIGTERM has already been sent, the pod is already being torn
  down, and kubelet does not layer a second termination action on top of the
  one already in progress. A liveness 503 here is honest (the pod truly is
  not meant to keep serving) and harmless.

## Environment

| Var | Where | Value (prod/UAT) | Note |
|---|---|---|---|
| `GRACEFUL_SHUTDOWN_TIMEOUT` | manifest env, read by `internal/pkg/config` | `75s` | Code default (no env set) is `30s` — always set explicitly in the manifest. |
| `terminationGracePeriodSeconds` | pod spec | `90` | Must stay ≥ preStop + `GRACEFUL_SHUTDOWN_TIMEOUT` + 10s margin (enforced by `deploy_consistency_test`, W-owned). |
| preStop | pod spec | `sleep 5` | Runs before the app process ever observes SIGTERM. |

## Verify

```bash
# Watch a real rollout for the log lines this runbook describes:
ssh root@100.122.83.20 "kubectl -n lurus-newhub logs -l app=lurus-newhub -f" | grep -i "graceful shutdown\|draining"

# Confirm the outgoing pod exited 0, not crash-looped:
ssh root@100.122.83.20 "kubectl -n lurus-newhub get pod <old-pod-name> -o jsonpath='{.status.containerStatuses[0].lastState.terminated.exitCode}'"

# Readiness flips within the drain window (curl from inside the cluster or
# via the NodePort while a rollout is in progress):
curl -s -o /dev/null -w '%{http_code}\n' https://hub.lurus.cn/api/health
```

## Related

- `internal/lifecycle/drain.go` — `Drainer` implementation.
- `internal/adapter/repo/usedata.go` — `UpdateQuotaDataWithContext` flushes
  the in-memory quota_data cache on the same shutdown ctx.Done(), so a
  bucket that has not yet hit its periodic flush is not lost on a rolling
  deploy.
- `internal/app/notify-limit.go` — `InitNotifyLimitCleanup` is the single
  entry point (both main.go's boot-time call and `checkMemoryLimit`'s lazy
  fallback route through the same `sync.Once`) for the notify-limit cleanup
  goroutine's lifecycle, so it can actually be stopped via
  `StopNotifyLimitCleanup` regardless of which caller started it first.
- `doc/runbook/staging-deploy.md` — deploy procedure; a 503 `draining` from
  `/api/health` or `/api/status` right after a rollout trigger is expected,
  not a failed deploy.
