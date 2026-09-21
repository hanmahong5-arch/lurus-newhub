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
   probe) that the pod was shutting down, so the pod stayed in the Service's
   endpoint list and new traffic kept arriving for the whole window between
   SIGTERM and the pod actually dying — right when it is least able to
   finish new work.
2. **A budget timeout crashed the process.** `httpServer.Shutdown` returns
   `context.DeadlineExceeded` when the budget runs out with connections
   still open. That error propagated through the `errgroup` to `main()`,
   which called `common.FatalLog` + `os.Exit(1)` — the busiest pod (the one
   still finishing real work when the clock ran out) exited with a fault
   code instead of a clean shutdown, and nothing recorded how many requests
   were cut.
3. **No accounting.** Even on the timeout path, there was no count of how
   many connections got cut, only a generic error log line.

`internal/lifecycle/drain.go`'s `Drainer` answers the three: `MarkDraining`
flips a process-wide flag that both `GetHealthDetailed`
(`internal/adapter/handler/health.go`, readiness) and `GetStatus`
(`internal/adapter/handler/misc.go`, startup + liveness) check first and
unconditionally; `Shutdown` returns a nil error on each of its three
outcomes (a budget timeout is an expected outcome, not a fault) and reports
how many in-flight **requests** it had to give up on. That count comes from
`metrics.ActiveConnections`, which `internal/pkg/metrics/middleware.go:50-51`
Inc/Decs around `c.Next()` for every request the engine serves, so it is a
request count rather than a connection count. A `srv.Shutdown` error that is
*not* `context.DeadlineExceeded` (net/http hands back the first
listener-close failure, and only once every connection has gone idle) gets
its own log line — `graceful shutdown: shutdown error (not a budget
timeout)` — and a cut of 0, because on that path nothing was cut.

## Timeline

```
T+0s    SIGTERM delivered to the pod (kubelet begins termination)
        ├─ preStop hook: sleep 5s — lets in-flight relay requests that are
        │  about to complete drain naturally before the app even sees the
        │  signal, and gives the Service's endpoint controller time to stop
        │  sending new connections here.
T+5s    signal.NotifyContext's ctx fires → main.go's shutdown goroutine
        runs → lifecycle.Default().Shutdown(shutdownCtx, httpServer, nil):
        ├─ MarkDraining() — from this instant, GET /api/health and
        │  GET /api/status answer 503 draining immediately, without
        │  touching the DB/Redis/billing checks.
        ├─ httpServer.Shutdown(shutdownCtx budget) — stops accepting new
        │  connections, waits for in-flight ones to finish on their own.
        │  The nil third argument means "count in-flight requests from
        │  metrics.ActiveConnections"; a caller may pass its own probe.
T+5s..  Three outcomes:
        (a) every connection finishes before the budget elapses:
            Shutdown returns nil, cut=0, process exits 0.
        (b) the budget (GRACEFUL_SHUTDOWN_TIMEOUT — see the table below
            for the manifest value) elapses with connections still open:
            Shutdown logs "graceful shutdown: budget exceeded, cut=N
            in-flight request(s)" and returns (N, nil) — NOT an error. The
            process still exits 0.
        (c) srv.Shutdown fails for some other reason (a listener-close
            error): logged as "graceful shutdown: shutdown error (not a
            budget timeout)" with cut=0, also (0, nil).
T+tgps  terminationGracePeriodSeconds — kubelet SIGKILLs the pod if it has
        not exited on its own by now. The target this cycle sets is
        preStop=5s + GRACEFUL_SHUTDOWN_TIMEOUT=75s + a 10s margin for the
        rest of run()'s cleanup (DB close, tracing shutdown, the quota_data
        flush below) = tgps 90. The matching `deploy_consistency_test`
        assertion (tgps ≥ preStop + graceful + 10) lands with the manifest
        change in the wiring step of this same PR — until then, read the
        live values with the Verify commands below rather than trusting
        this paragraph.
```

## What gets cut

A request is "cut" only if it is still in flight when the graceful-shutdown
budget (`GRACEFUL_SHUTDOWN_TIMEOUT`) elapses — `http.Server.Shutdown` itself
does not interrupt a connection before that; it just stops accepting new ones
and waits. In practice this means:

- A relay stream (SSE, or any handler whose response takes longer than the
  budget to finish) started shortly before SIGTERM and still running when
  the budget runs out.
- **`ReadTimeout`/`WriteTimeout` stay at 0** (cycle 12 do-not-regress,
  `cmd/server/main.go`'s `buildHTTPServer` doc comment) — no per-request
  timeout cuts a request; the shutdown budget is what does.
- A stream still running when `GRACEFUL_SHUTDOWN_TIMEOUT` elapses is cut by
  a routine rolling deploy, not just by incidents — `STREAMING_TIMEOUT`
  (300s) is the documented upper bound for how long a relay stream may run,
  and it is larger than the shutdown budget. Whether to raise
  `GRACEFUL_SHUTDOWN_TIMEOUT`/`terminationGracePeriodSeconds` to cover the
  full streaming timeout is **O-tgps** (owner decision,
  `_bmad-output/planning-artifacts/cycle13-industrial-grade-2026-09-20.md`
  §8) — this cycle targets 75s/90s, enough for the large majority of relay
  calls without stretching each rollout's wall-clock time by 5 minutes.

## Readiness vs liveness during drain

Both `GET /api/health` (readinessProbe, `internal/adapter/handler/health.go`)
and `GET /api/status` (startupProbe + livenessProbe,
`deploy/k8s/r6-stage/deployment.yaml:271,283`, handler in
`internal/adapter/handler/misc.go`) check `lifecycle.IsDraining()` first and
answer 503 before touching any dependency — `{"status":"draining"}` from
health, `{"success":false,"status":"draining"}` from status (that endpoint's
clients read `success`). This is deliberate for both probes, not just
readiness:

- **Readiness** failing pulls the pod out of the Service's endpoint list —
  this is the whole point, it must happen the instant draining starts.
- **Liveness** failing during the drain window does **not** cause an *extra*
  restart: SIGTERM has already been sent, the pod is already being torn
  down, and kubelet does not layer a second termination action on top of the
  one already in progress. A liveness 503 here is honest (the pod truly is
  not meant to keep serving) and harmless.

### The 5xx alarm no longer sees these 503s (since 2026-09-20)

`newhub_relay_5xx_elevated`
(`deploy/r6-host-netdata/health.d/newhub.conf`) used to watch
`lurus_gateway_requests_total` with `chart labels: status=5*`, whose only
live chart was the `/api/health` one — so every probe 503 answered during a
drain landed in it. The observability lane rebound it to
`lurus_gateway_non_probe_5xx_total`
(`internal/pkg/metrics/middleware.go`), which excludes `/api/health` and
`/api/status` in code, so drain 503s do not reach it at all.

The consequence is the opposite of the old advice: a
`newhub_relay_5xx_elevated` WARNING whose window overlaps a rollout now
means real, customer-facing 5xx during the drain — i.e. the drain did NOT
protect in-flight traffic. Treat it as a signal, not as noise, and read it
with the `graceful shutdown:` log lines from the pod that was terminating.

## Environment

Values in the "target" column are what this cycle's manifest change sets;
the wiring step of this PR is what edits `deploy/k8s/r6-stage/deployment.yaml`
and `deploy/k8s/r6-uat/deployment.yaml`. Both files still carried the older
`40` / `"30s"` pair when this page was written, so read the live values
(Verify, below) rather than quoting this table at anyone.

| Var | Where | Target | Note |
|---|---|---|---|
| `GRACEFUL_SHUTDOWN_TIMEOUT` | manifest env, read by `internal/pkg/config` | `75s` | Code default (no env set) is `30s`; the manifest sets it explicitly. |
| `terminationGracePeriodSeconds` | pod spec | `90` | Stays ≥ preStop + `GRACEFUL_SHUTDOWN_TIMEOUT` + 10s margin; the gate for that assertion lands with the manifest change. |
| preStop | pod spec | `sleep 5` | Runs before the app process observes SIGTERM. Unchanged this cycle. |

## Verify

```bash
# Watch a real rollout for the log lines this runbook describes:
ssh root@100.122.83.20 "kubectl -n lurus-newhub logs -l app=lurus-newhub -f" | grep -i "graceful shutdown\|draining"

# Confirm the outgoing pod exited 0, not crash-looped:
ssh root@100.122.83.20 "kubectl -n lurus-newhub get pod <old-pod-name> -o jsonpath='{.status.containerStatuses[0].lastState.terminated.exitCode}'"

# Readiness flips within the drain window (curl from inside the cluster or
# via the NodePort while a rollout is in progress):
curl -s -o /dev/null -w '%{http_code}\n' https://hub.lurus.cn/api/health
curl -s -o /dev/null -w '%{http_code}\n' https://hub.lurus.cn/api/status

# The two numbers this page depends on, read from the live Deployment
# (do this before quoting the table above):
ssh root@100.122.83.20 "kubectl -n lurus-newhub get deploy lurus-newhub \
  -o jsonpath='{.spec.template.spec.terminationGracePeriodSeconds}{\"\\n\"}'"
ssh root@100.122.83.20 "kubectl -n lurus-newhub get deploy lurus-newhub \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name==\"GRACEFUL_SHUTDOWN_TIMEOUT\")].value}{\"\\n\"}'"
```

## Related

- `internal/lifecycle/drain.go` — `Drainer` implementation.
- `internal/adapter/repo/usedata.go` — `UpdateQuotaDataWithContext` flushes
  the in-memory quota_data cache on the same shutdown ctx.Done(), so a
  bucket that has not yet hit its periodic flush is not lost on a rolling
  deploy. The flush snapshots the cache under its lock and writes with the
  lock released, under a 10s deadline of its own
  (`quotaDataShutdownFlushTimeout`), so neither a relay request finishing
  during the drain nor a slow PG can stretch the shutdown.
- `internal/app/notify-limit.go` — `InitNotifyLimitCleanup` is the single
  entry point (both main.go's boot-time call and `checkMemoryLimit`'s lazy
  fallback route through the same `sync.Once`) for the notify-limit cleanup
  goroutine's lifecycle, so it can actually be stopped via
  `StopNotifyLimitCleanup` regardless of which caller started it first.
- `doc/runbook/staging-deploy.md` — deploy procedure; a 503 `draining` from
  `/api/health` or `/api/status` right after a rollout trigger is expected,
  not a failed deploy.
