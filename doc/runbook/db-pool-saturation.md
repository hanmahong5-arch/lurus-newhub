# Runbook — Database Slow Queries / Connection-Pool Saturation

> **Source**: netdata alarm `newhub_db_slow_queries`,
> `deploy/r6-host-netdata/health.d/newhub.conf` — see that file's own
> "STATUS"/"STATUS UPDATE" header for whether it is installed on R6 today (as
> of 2026-09-19 it is added in-repo only, NOT installed; the README's
> "Install" section has the command, and it is owner item O2).
> **Triggered by**: `lurus_gateway_db_slow_query_total{db}` (counter) averaged
> over 5 minutes crossing 0.05/s. The counter is incremented by the GORM
> logger (`internal/adapter/repo/gorm_logger.go`, `gormLogger.Trace`) once per
> statement whose wall time exceeds `DB_SLOW_QUERY_MS` (default 200ms). `db`
> is `newhub` (the `SQL_DSN` pool) or `newhub_log` (the `LOG_SQL_DSN` pool,
> which no deployment in this repo configures today — `grep -c LOG_SQL_DSN
> deploy/k8s/r6-stage/deployment.yaml deploy/k8s/r6-uat/deployment.yaml`
> returns 0 for both).
> **Severity**: warning (netdata `to: sysadmin` — see
> `deploy/r6-host-netdata/README.md` "Ownership boundary" for what that does
> and does not mean today).
> **Last review**: 2026-09-19.

## Symptom

Statements against PostgreSQL are taking longer than the slow threshold often
enough to show up as a sustained rate. Users see it as latency on console
pages and, on the relay path, as added time before the upstream call even
starts.

## The two causes this alarm cannot tell apart

The time GORM measures starts in `gorm.io/gorm/callbacks.go` (`curTime` at the
top of `processor.Execute`) and ends after the statement returns — which means
it **includes the wait for a free connection from the `database/sql` pool**.
So a rising slow-query rate has two quite different causes:

1. **The database is slow.** Query plans, lock waits, a vacuum storm, an
   undersized instance.
2. **The pool is saturated.** The statements themselves are fine; callers are
   queueing for a connection because `SQL_MAX_OPEN_CONNS` is reached (or
   because PostgreSQL's own `max_connections` is, and new connections fail or
   stall).

Telling them apart takes one look at the pool gauges, which are on the same
`/metrics` page (`metrics.RegisterDBStats`, `internal/adapter/repo/main.go`).

## Detect

`/metrics` is not reachable through nginx (`location = /metrics { return
404; }`); scrape it on the node, which is what netdata's go.d job does:

```bash
ssh root@100.122.83.20 "curl -s http://localhost:30850/metrics | grep -E '^lurus_gateway_db_slow_query_total|^go_sql_'"
```

Read it like this:

| Series | Meaning |
|---|---|
| `go_sql_wait_count_total{db_name="newhub"}` | number of times a caller had to wait for a connection. **Rising = pool saturation.** |
| `go_sql_wait_duration_seconds_total{db_name="newhub"}` | total time spent waiting. Divide the delta by the `wait_count_total` delta for the average wait. |
| `go_sql_in_use_connections` vs `go_sql_max_open_connections` | in-use pinned at max is the same story from the other side. |
| `go_sql_open_connections` | total established (in use + idle). |
| `go_sql_max_lifetime_closed_total` / `go_sql_max_idle_time_closed_total` | recycling rate; a fast-climbing lifetime counter means connections (and, with `PrepareStmt:true`, their prepared-statement caches) are being thrown away often. |

**`wait_count_total` flat while the slow-query rate climbs → cause 1, the
database.** `wait_count_total` climbing with it → cause 2, the pool.

Note that the NodePort round-robins across replicas
(`deploy/k8s/r6-stage/deployment.yaml`, `replicas: 3`), so consecutive scrapes
can land on different pods and these counters are per-replica. Compare two
samples from the same pod when the numbers matter:

```bash
ssh root@100.122.83.20 "kubectl -n lurus-newhub get pods -o name"
ssh root@100.122.83.20 "kubectl -n lurus-newhub exec <pod> -- wget -qO- http://127.0.0.1:3000/metrics | grep '^go_sql_'"
```

The slow statements themselves are in the pod log, one line per statement,
carrying the caller's `file:line` and the parameterized SQL (values are NOT
interpolated — `ParameterizedQueries: true`):

```bash
ssh root@100.122.83.20 "kubectl -n lurus-newhub logs deploy/lurus-newhub --since=15m | grep 'SLOW SQL'"
```

## Reconcile

For cause 1 (database slow), on the PostgreSQL side:

```bash
ssh root@100.122.83.20 "kubectl -n database exec lurus-pg-0 -- psql -U postgres -d newhub -c \
  \"SELECT pid, now()-query_start AS age, wait_event_type, wait_event, left(query,120) \
    FROM pg_stat_activity WHERE state <> 'idle' AND datname='newhub' ORDER BY age DESC LIMIT 20;\""
```

A statement older than `SQL_STATEMENT_TIMEOUT_MS` (default 8000, injected as a
DSN runtime parameter by `withStatementTimeout`) should not exist — if one
does, the DSN override is in play, check it.

For cause 2 (pool saturated), the ceiling is per replica per pool:

```bash
ssh root@100.122.83.20 "kubectl -n lurus-newhub get deploy lurus-newhub -o jsonpath='{.spec.template.spec.containers[0].env}' | tr ',' '\n' | grep -i SQL_MAX"
ssh root@100.122.83.20 "kubectl -n database exec lurus-pg-0 -- psql -U postgres -c 'SHOW max_connections'"
ssh root@100.122.83.20 "kubectl -n database exec lurus-pg-0 -- psql -U postgres -c \
  \"SELECT datname, count(*) FROM pg_stat_activity GROUP BY 1 ORDER BY 2 DESC;\""
```

Budget arithmetic: `replicas x 2 pools x SQL_MAX_OPEN_CONNS` is this service's
worst case, and it shares `max_connections` with platform and the IdP. Raising
`SQL_MAX_OPEN_CONNS` without checking the other two is how the whole instance
runs out of connection slots at once.

## Recover

- **Database slow, one bad statement**: cancel it
  (`SELECT pg_cancel_backend(<pid>)`), then fix the query or add the index.
  `pg_terminate_backend` only if cancel does not take — it drops the whole
  session.
- **Pool saturated, and PostgreSQL has headroom**: raise
  `SQL_MAX_OPEN_CONNS`/`SQL_MAX_IDLE_CONNS` in
  `deploy/k8s/r6-stage/deployment.yaml` (git, then ArgoCD — do not
  `kubectl set env`, selfHeal reverts it).
- **Pool saturated and PostgreSQL has no headroom**: the fix is upstream of
  this service — fewer replicas, a pooler, or a bigger `max_connections`.
  Owner decision (cycle-12 owner item O-pool).
- **Noise only** (the threshold is simply too tight for this workload): raise
  `DB_SLOW_QUERY_MS`. This changes what is logged and counted, not what is
  happening; write down the measured rate you tuned against.

## Verify

After the change, the same two reads:

```bash
ssh root@100.122.83.20 "curl -s http://localhost:30850/metrics | grep -E '^lurus_gateway_db_slow_query_total|^go_sql_wait_count_total'"
ssh root@100.122.83.20 "kubectl -n lurus-newhub logs deploy/lurus-newhub --since=10m | grep -c 'SLOW SQL'"
```

The alarm clears on its own once the 5-minute average drops back under the
threshold (`delay: down 10m`).

## Prevent

- The pool defaults were retuned in cycle 12 (`currentPoolSettings`,
  `internal/adapter/repo/main.go`): `SQL_MAX_LIFETIME` 60s → 1800s, and
  `SQL_MAX_IDLE_TIME` added at 300s. The old 60-second lifetime discarded
  every connection's prepared-statement cache once a minute, which showed up
  as latency that looked like a slow database.
- Setting `SQL_MAX_OPEN_CONNS`/`SQL_MAX_IDLE_CONNS` explicitly in both
  manifests, plus a `deploy/k8s/deploy_consistency_test.go` case that makes a
  multi-replica deployment declare them rather than inherit the 1000 default,
  is cycle-12 work handed to the wiring lane. Until that lands, both
  deployments run on the defaults above — check the live env before doing the
  budget arithmetic:
  `ssh root@100.122.83.20 "kubectl -n lurus-newhub get deploy lurus-newhub -o jsonpath='{.spec.template.spec.containers[0].env}' | tr ',' '\n' | grep -i SQL_MAX"`
  (empty output = defaults).

## Related signal on the same page

`lurus_gateway_channel_cache_sync_failed_total{query}` (cycle 12) counts
channel-cache rebuilds abandoned because their database read failed
(`repo.InitChannelCache`). It has no alarm of its own — grepping
`deploy/r6-host-netdata/health.d/*.conf` for `channel_cache_sync_failed`
returns zero hits as of 2026-09-19 — but a nonzero value during a database
incident tells you the relay routing table on that replica is **stale**: it
kept the last table it built from a complete read rather than emptying
itself, so a channel disabled during the incident may still be serving.
Check it alongside the pool gauges:

```bash
ssh root@100.122.83.20 "curl -s http://localhost:30850/metrics | grep '^lurus_gateway_channel_cache_sync_failed_total'"
```

It clears (stops climbing) as soon as one sync succeeds; the table is rebuilt
in full on the next `SYNC_FREQUENCY` tick (60s on r6-stage).
