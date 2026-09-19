# Runbook — Database Slow Queries / Connection-Pool Saturation

> **Source**: netdata alarms `newhub_db_slow_queries` and
> `newhub_channel_cache_stale`, both in
> `deploy/r6-host-netdata/health.d/newhub.conf` — see that file's own
> "STATUS"/"STATUS UPDATE" header for whether they are installed on R6 today
> (as of 2026-09-19 both are added in-repo only, NOT installed; the README's
> "Install" section has the command, and it is owner item O2).
> **Triggered by**: `lurus_gateway_db_slow_query_total{db}` (counter) averaged
> over 5 minutes crossing 0.05/s. The counter is incremented by the GORM
> logger (`internal/adapter/repo/gorm_logger.go`, `gormLogger.Trace`) once per
> statement whose wall time exceeds `DB_SLOW_QUERY_MS` (default 200ms). `db`
> is `newhub` (the `SQL_DSN` pool) or `newhub_log` (the `LOG_SQL_DSN` pool,
> which no deployment in this repo configures today — `grep -c LOG_SQL_DSN
> deploy/k8s/r6-stage/deployment.yaml deploy/k8s/r6-uat/deployment.yaml`
> returns 0 for both).
> **Also triggered by**:
> `lurus_gateway_channel_cache_sync_failed_total{query}` (counter), any
> sustained nonzero rate — the `newhub_channel_cache_stale` alarm. That one is
> not about latency; it says the relay routing table on a replica is stale.
> Jump to "Stale channel cache" at the bottom of this page.
> **Severity**: warning for both (netdata `to: sysadmin` — see
> `deploy/r6-host-netdata/README.md` "Ownership boundary" for what that does
> and does not mean today).
> **Last review**: 2026-09-19.
>
> **Threshold calibration status — read this before treating a WARNING as an
> incident.** The 0.05/s slow-query threshold has NEVER been calibrated
> against live data: production has had near-zero chargeable traffic (30 days,
> 12,772 calls, 14 of them human), so there is no measured baseline to set it
> from. It is a first cut meaning "about three slow statements a minute on the
> replica that happened to be scraped". Two consequences:
>
> - A known structural source can put the rate in the same order as the
>   threshold with no customer traffic at all — see "Is this just the channel
>   cache?" below. Check that first.
> - The scrape is one NodePort round-robining across 3 replicas, so the
>   rate-converted dimension netdata charts is a PER-REPLICA sample, not a
>   gateway-wide rate (the conf file's "SCRAPE TOPOLOGY" note has the full
>   story). Do not multiply it by the replica count when comparing it to this
>   threshold.
>
> Whoever first sees this alarm on real traffic should record the measured
> rate and revise the threshold in the conf file. An alarm parked at WARNING
> forever is worth as much as one that never changes state.

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

## A saturated pool surfaces as NotReady, not as slowness

This is the one thing to know before raising or lowering `SQL_MAX_OPEN_CONNS`.

Readiness on both deployments is DEEP: `readinessProbe` -> `GET /api/health`
(`deploy/k8s/r6-stage/deployment.yaml` `readinessProbe:`, `timeoutSeconds: 4`,
`periodSeconds: 5`, `failureThreshold: 3`), and that handler pings the
database — `sqlDB.PingContext(ctx)` in
`internal/adapter/handler/health.go`, bounded by `common.HealthDBPingTimeout`
(1500ms, `HEALTH_DB_PING_TIMEOUT_MS`). `database/sql`'s `PingContext`
**acquires a connection from the pool**; it does not bypass it. So when the
pool is at `SQL_MAX_OPEN_CONNS` and every connection is busy, the probe queues
with everyone else, gives up at 1.5s, and `/api/health` answers `503` with
`checks.database = "unreachable"` — the only check that moves the HTTP status.

Three consequences an operator has to hold together:

- **The symptom of a saturated pool is a pod leaving the Service, not a slow
  pod.** ~15s of sustained saturation (`failureThreshold: 3` x
  `periodSeconds: 5`) flips a replica NotReady.
- **It flips all replicas at once**, because saturation of a shared PostgreSQL
  instance is correlated across them. This is the same correlation risk the
  manifest already spells out in prose next to `livenessProbe:` for the
  "PG blip" case; pool exhaustion is a second path into it, and this one can
  be caused from inside the service by lowering `SQL_MAX_OPEN_CONNS` too far.
- **Therefore `SQL_MAX_OPEN_CONNS` is not just a latency knob.** Set too low,
  a traffic burst does not degrade into slower responses, it degrades into a
  full outage. Lower it only with the budget arithmetic below in hand, and
  treat a NotReady fleet with a healthy-looking PostgreSQL as this, not as a
  database failure.

A saturated pool and a genuinely unreachable database look identical from
`/api/health`. The pool gauges below are what tells them apart; check them
before concluding PostgreSQL is down.

Cycle 12 did not change the probe. Keeping the readiness ping off the shared
pool (a dedicated `sql.Conn`, or a one-connection health pool) is the real
fix and is recorded as next-cycle work.

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
  `kubectl set env`, selfHeal reverts it). Expect the symptom you are fixing
  to be **503 / NotReady replicas**, not merely slow ones — see "A saturated
  pool surfaces as NotReady" above — so this is an availability change, not a
  latency tweak, and the rollout deserves the same care.
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
  manifests, plus a `deploy/k8s/deploy_consistency_test.go` case requiring
  every manifest that declares a container memory limit to set
  `SQL_MAX_OPEN_CONNS` and `GOMEMLIMIT` rather than inherit the 1000 default
  (the replica count only enters the budget arithmetic, so the single-replica
  UAT manifest is covered too), is cycle-12 work handed to the wiring lane.
  Until that lands, both deployments run on the defaults above — check the
  live env before doing the budget arithmetic:
  `ssh root@100.122.83.20 "kubectl -n lurus-newhub get deploy lurus-newhub -o jsonpath='{.spec.template.spec.containers[0].env}' | tr ',' '\n' | grep -i SQL_MAX"`
  (empty output = defaults).

## Is this just the channel cache?

Check this before escalating a `newhub_db_slow_queries` WARNING, because the
service generates slow-query candidates on its own schedule with no customer
involved. `repo.rebuildChannelCache`
(`internal/adapter/repo/channel_cache.go`) issues two unfiltered full-table
reads — `SELECT * FROM channels`, then `SELECT * FROM abilities` — on every
replica every `SYNC_FREQUENCY` (60s on r6-stage). `abilities` holds one row
per channel x group x model, so it grows much faster than the channel count
suggests.

Arithmetic: 2 statements / 60s = 0.033/s on one replica, 0.1/s across three.
The chart shows the per-replica number (see the scrape-topology note above),
so the figure to compare against the 0.05/s threshold is the first one — the
same order as the threshold, before a single customer request is counted.

```bash
ssh root@100.122.83.20 "kubectl -n lurus-newhub logs deploy/lurus-newhub --since=15m | grep 'SLOW SQL' | grep -c channel_cache.go"
ssh root@100.122.83.20 "kubectl -n lurus-newhub logs deploy/lurus-newhub --since=15m | grep 'SLOW SQL' | grep -vc channel_cache.go"
```

If nearly every `SLOW SQL` line names `channel_cache.go` and
`go_sql_wait_count_total` is flat, this is the periodic cache refresh, not an
incident. Raise `DB_SLOW_QUERY_MS`, or raise the alarm threshold in the conf
file, and write down the rate you measured. Reducing the cost properly (an
index, a narrower projection, or a longer `SYNC_FREQUENCY`) is a code change,
not an incident action.

## Stale channel cache (`newhub_channel_cache_stale`)

**Series**: `lurus_gateway_channel_cache_sync_failed_total{query}`, where
`query` is `channels` or `abilities` — which of the rebuild's two reads
failed. Both label values are pre-registered at zero
(`internal/pkg/metrics/db_observability.go` `init()`), so the series is on
`/metrics` from boot: an absent chart means a scrape problem, not "no
failures".

**What it means.** A channel-cache rebuild whose database read failed is
abandoned, and the replica keeps the previous routing table. Before cycle 12
the failed query's empty result was swapped in instead, so one failed read
emptied that replica's routing table and relay answered "no available
channel" until the next successful sync — loud, and customer-visible within
minutes. Keeping the old table is the better failure, but it is a **silent**
one, and this counter (plus its alarm) is the whole of what makes it visible.

**What is actually wrong while it is nonzero**, on the affected replica only:

- a channel disabled, deleted or key-rotated since the last good sync **keeps
  serving**;
- a channel added since then **gets no traffic**;
- priority/weight/group edits since then are not in effect.

Everything else looks normal, including `/api/health`, which is exactly why it
needs its own alarm.

**Detect**:

```bash
ssh root@100.122.83.20 "curl -s http://localhost:30850/metrics | grep '^lurus_gateway_channel_cache_sync_failed_total'"
ssh root@100.122.83.20 "kubectl -n lurus-newhub logs deploy/lurus-newhub --since=30m | grep 'channel cache sync aborted'"
```

The log line carries the failing read and the driver error
(`channel cache sync aborted, previous routing table kept: load <channels|abilities> for cache rebuild: ...`).
Because the NodePort round-robins, prefer the per-pod form when it matters:

```bash
ssh root@100.122.83.20 "kubectl -n lurus-newhub exec <pod> -- wget -qO- http://127.0.0.1:3000/metrics | grep '^lurus_gateway_channel_cache_sync_failed_total'"
```

**Reconcile**: the cause is always the database read — the same two causes as
the rest of this page (slow/unavailable PostgreSQL, or a saturated pool). The
`query=abilities` label is the more likely one on a large dataset, since that
table is the bigger of the two. Work the pool/database sections above.

**Recover**: nothing to do by hand once the database is healthy — the counter
stops climbing as soon as one sync succeeds, and the table is rebuilt in full
on the next `SYNC_FREQUENCY` tick (60s on r6-stage). If you need the refresh
immediately rather than within the tick, any channel write through the admin
API triggers one on the replica that serves it, and a rollout restart
rebuilds every replica at boot. If the counter keeps climbing while
PostgreSQL looks healthy, the routing table on that replica is old by however
long the alarm has been up: treat channel changes made during that window as
not yet in effect.
