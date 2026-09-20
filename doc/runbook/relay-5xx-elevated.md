# Runbook — Relay 5xx Elevated

> **Source**: netdata alarm `newhub_relay_5xx_elevated`,
> `deploy/r6-host-netdata/health.d/newhub.conf` (see the file's "STATUS"
> header for whether it is installed on R6 today).
> **Triggered by**: `lurus_gateway_non_probe_5xx_total` rate > 0.05/s over a
> 5-minute average.
> **Severity**: warning (netdata `to: sysadmin`).
> **Last review**: 2026-09-20 (cycle-13 L10 repair).
>
> **REBOUND 2026-09-20**: this alarm used to watch
> `lurus_gateway_requests_total` with `chart labels: status=5*`, which the
> operator's 2026-09-15 live check found matched only the
> `path=/api/health status=503` chart — it was a health-probe alarm. Since
> cycle-13 L11, `/api/health` and `/api/status` answer 503 for the whole
> graceful-shutdown window by design, so it now reads
> `lurus_gateway_non_probe_5xx_total`
> (`internal/pkg/metrics/middleware.go`), which counts 5xx on every route
> EXCEPT those two probe paths.
> **A WARNING inside a deploy window is now the drain gate**: probe 503s no
> longer reach this alarm, so what is left is customer-facing 5xx during a
> rollout — read it together with `graceful shutdown:` log lines and
> `graceful-drain.md`. Still cross-check `newhub_upstream_5xx_burst`
> (`upstream-5xx-burst.md`) before calling it an upstream-provider problem.

## Symptom

newhub is returning 5xx to callers on non-probe routes (relay and console
alike) at a sustained rate — a readiness-gate failure that also breaks real
traffic, an upstream/channel-selection failure surfacing as 5xx, or a panic
loop behind a recovery boundary (`newhub_panics_recovered`).

## Detect

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_relay_5xx_elevated
curl -s http://localhost:30850/metrics | grep '^lurus_gateway_non_probe_5xx_total'
# which path/status is actually failing (this series is deliberately unlabelled):
curl -s http://localhost:30850/metrics | grep 'lurus_gateway_requests_total{' | grep 'status="5'
```

## Reconcile

- Check the pod's own health endpoint directly and its logs for the specific
  readiness check that is failing (DB, Redis, schema migration state — see
  `doc/runbook/database.md` and `/api/health`'s `checks` object).
- If this correlates with a deploy, check whether the new revision is
  crash-looping (`kubectl get pods -n lurus-newhub`, operator-run only).

## Recover

Restore whatever readiness dependency is failing (DB/Redis reachability,
etc.). If a bad deploy is the cause, revert per `doc/runbook/staging-deploy.md`.

## Verify

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_relay_5xx_elevated
```

## Prevent

Nothing to prevent beyond normal readiness-dependency hygiene; this is not
an upstream-provider signal.
