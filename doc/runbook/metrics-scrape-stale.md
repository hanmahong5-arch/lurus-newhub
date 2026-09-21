# Runbook — Metrics Scrape Stale

> **Source**: netdata alarm `newhub_metrics_scrape_stale`,
> `deploy/r6-host-netdata/health.d/newhub.conf` — see that file's own
> "STATUS"/"STATUS UPDATE" header for whether it is installed on R6 today
> (as of 2026-09-20, added in-repo only, NOT installed — the README's
> "Install" section has the command, and it is owner item O2).
> **Triggered by**: `$now - $last_collected_t` on
> `lurus_gateway_instance_info` exceeding 180s (warning) / 600s (critical),
> as a `template:` — one alarm per chart, and `instance_info` is labelled
> per pod, so each replica is covered separately. Those thresholds are
> per-POD gaps, not scrape intervals: the go.d job scrapes one NodePort
> that round-robins across three replicas every 10s, so a given pod's chart
> is refreshed on roughly one scrape in three (the first cut of this alarm
> used 60s, which at that hit rate is an ~8.8% chance of warning at every
> evaluation with nothing wrong — corrected 2026-09-20 in the cycle-13 L10
> repair round, D-L10-2, together with the chart-model live check in the
> alarm block's own comment, owner item O-scrape).
> `lurus_gateway_instance_info` is a constant-1 "info" gauge
> (`metrics.SetInstanceInfo`, set once at boot before `/metrics` is
> mounted — `internal/pkg/metrics/instance.go`) — its VALUE never changes,
> so this alarm cannot key off `$this`; it uses netdata's own
> last-collected-time bookkeeping instead, the same mechanism a "did my
> input plugin stop responding" check always uses.
> **Severity**: warning / critical (netdata `to: sysadmin` — see the
> README's "Ownership boundary" for what that does and does not mean
> today).
> **Last review**: 2026-09-20 (cycle-13 L10).

## Why this alarm exists and is different from every other alarm in this file

Every other alarm in `newhub.conf` reads a Prometheus series scraped off
`http://localhost:30850/metrics`. If the go.d `prometheus` job's scrape of
that endpoint starts failing — the NodePort stops answering, every replica
goes NotReady at once, the auth gate in front of `/metrics` starts
rejecting the scrape, or the job itself is misconfigured — **every other
alarm in this file goes silently stale along with it**: a chart that stops
receiving samples does not transition to WARNING, it simply stops updating,
which looks identical to "the condition being watched for has not
happened" from inside netdata's own alarm API. This alarm is the one
signal in the file that is NOT about newhub's own behavior; it is about
whether netdata can still see newhub at all, and it is deliberately bound
to a series (`lurus_gateway_instance_info`) that is written once at boot
and would exist on every scrape a healthy pod answers, so its only failure
mode is the scrape itself, not application logic.

## Symptom

The netdata alarm API (`GET /api/v1/alarms?all`) shows
`newhub_metrics_scrape_stale` in WARNING or CRITICAL, or — more likely
noticed first — every OTHER newhub alarm has stopped transitioning state
for longer than its own `every:`/`lookup:` window would explain.

## Detect

```bash
ssh root@100.122.83.20 "curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_metrics_scrape_stale"
```

Confirm the scrape target itself, from the host (this is exactly what
go.d's `prometheus` job does — the same endpoint is 404'd at the nginx edge
by design, see CLAUDE.md's `/metrics` row, so testing it through the
public domain proves nothing here):

```bash
ssh root@100.122.83.20 "curl -s -o /dev/null -w '%{http_code}\n' http://localhost:30850/metrics"
ssh root@100.122.83.20 "curl -s http://localhost:30850/metrics | grep '^lurus_gateway_instance_info'"
```

Because the alarm is per-pod, also check WHICH pod's chart is stale before
concluding the whole scrape is down — the chart id carries the pod label:

```bash
ssh root@100.122.83.20 "curl -s 'http://localhost:19999/api/v1/charts' | grep instance_info"
```

One stale chart out of three is a single replica that stopped answering.
Charts whose pod name is not in `kubectl -n lurus-newhub get pods` are
retired replicas from an earlier rollout: netdata unbinds alarms from a
chart once the collector marks it obsolete, but whether go.d does that
promptly for a vanished label-set is the second live check recorded in the
alarm block's comment (owner item O-scrape) — if you are reading a
CRITICAL for a pod that no longer exists, that check is the answer, not an
incident. All three current pods stale at once is the scrape path itself.

`200` plus a non-empty `instance_info` line means the endpoint itself is
healthy RIGHT NOW from the host's network namespace — if the alarm is
still stale, the break is between go.d and the endpoint (config, or a
timing window the NodePort's random pod selection keeps missing), not the
application. If the curl itself fails or times out:

```bash
ssh root@100.122.83.20 "kubectl -n lurus-newhub get pods -o wide"     # all 3 Running/Ready?
ssh root@100.122.83.20 "kubectl -n lurus-newhub get svc lurus-newhub" # NodePort still 30850?
ssh root@100.122.83.20 "docker logs obs-netdata --since 15m 2>&1 | grep -i prometheus"
```

## Reconcile

Three independent things can break this, in likelihood order:

1. **Every replica is NotReady at once.** The most common shared-PostgreSQL
   failure mode this repo already documents (`doc/runbook/db-pool-saturation.md`
   "A saturated pool surfaces as NotReady, not as slowness") takes all 3
   replicas out of the Service simultaneously, and a NodePort with no
   healthy endpoints stops answering entirely. Work that runbook first if
   `kubectl get pods` shows every replica NotReady.
2. **go.d's `prometheus` job itself stopped or errored.** Check the
   container log grep above; a config parse error or a job crash inside
   `obs-netdata` stops every job it runs, not just this one — cross-check
   whether OTHER hosts' netdata-scraped services on the same box also went
   stale at the same timestamp (same root cause, different symptom).
3. **The `/metrics` auth gate (`metricsAuthMiddleware`) started rejecting
   the scrape.** go.d's `prometheus` job connects directly to
   `localhost:30850` (no forwarded headers), which the gate's "no
   `X-Forwarded-For`/`X-Real-IP`" branch is supposed to always admit — see
   CLAUDE.md's `/metrics` row. A change to that middleware, or to go.d's
   job definition adding headers it should not send, is the only way this
   specific path starts 401ing; `kubectl -n lurus-newhub logs
   deploy/lurus-newhub --since=15m | grep -i metrics` would show the
   rejection.

## Recover

- **NotReady fleet**: work `doc/runbook/db-pool-saturation.md` (or
  whichever readiness dependency is actually down — check `/api/health`'s
  own `checks` object on a pod directly via `kubectl exec ... wget`, since
  the NodePort itself is what is unreachable).
- **go.d job crashed/misconfigured**: `docker restart obs-netdata` is the
  same blast-radius warning as everywhere else in this directory — it
  briefly blinds monitoring for every service on the host, not just
  newhub; see the README's "Install" section before doing this for a
  single job's sake.
- **Auth-gate regression**: revert whatever changed in
  `middleware.metricsAuthMiddleware` or the go.d job definition; this is a
  code/config rollback, not an operational recovery step.

## Verify

```bash
ssh root@100.122.83.20 "curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_metrics_scrape_stale"
```
returns to CLEAR once a scrape lands on that pod again (`delay: down 5m` —
allow up to 5 minutes after the fix for the transition; with the
round-robin NodePort, expect a WARNING to clear on one pod's chart before
the others). Confirm the OTHER alarms in
the file are moving again too (their own `last_updated` timestamps in the
same `alarms?all` response should be within one `every:` window of now).

## Prevent

Nothing newhub-side beyond keeping the fleet Ready — this alarm exists
specifically because none of the application-level alarms in this file can
distinguish "condition not currently true" from "I have stopped being
told either way," and that gap needed its own, independent signal rather
than a fix to each alarm individually.
