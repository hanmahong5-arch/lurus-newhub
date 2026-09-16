# Runbook — Relay 5xx Elevated (Health-Check Scoped)

> **Source**: netdata alarm `newhub_relay_5xx_elevated`,
> `deploy/r6-host-netdata/health.d/newhub.conf` (see the file's "STATUS"
> header for whether it is installed on R6 today).
> **Triggered by**: `lurus_gateway_requests_total{status=5*}` rate > 0.05/s
> over a 5-minute average.
> **Severity**: warning (netdata `to: sysadmin`).
> **Last review**: 2026-09-16.
>
> **LIVE STATUS (2026-09-15)**: bound and CLEAR, but ONLY to the
> `path=/api/health status=503` chart — this alarm currently watches
> health-check 503s, not general relay 5xx traffic. `lurus_gateway_requests_total`
> counts every HTTP request including the console; the relay path itself has
> apparently not produced a matching `5*`-labelled chart on this host yet.
> **Do not treat a WARNING here as "the relay is failing"** without also
> checking `newhub_upstream_5xx_burst` (`upstream-5xx-burst.md`), which is
> scoped to actual relay terminal errors.

## Symptom

The process's own `/api/health` endpoint is returning 503 at an elevated
rate — typically a readiness-gate failure (DB/Redis unreachable, or the
process is mid-startup/shutdown), not an upstream LLM provider problem.

## Detect

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_relay_5xx_elevated
curl -s http://localhost:30850/metrics | grep 'lurus_gateway_requests_total{' | grep 'path="/api/health"'
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
