# Runbook — Rate-Limit Fail-Open Degradation

> **Source**: netdata alarm `newhub_rate_limit_degraded`,
> `deploy/r6-host-netdata/health.d/newhub.conf` — see the conf file's own
> "STATUS" header for whether it is installed on R6 today; ownership and the
> "does this page anyone" limit are in `deploy/r6-host-netdata/README.md`.
> **Triggered by**: `lurus_gateway_rate_limit_degraded_total` rate averaged
> over 5 minutes — warn above 0/s (any occurrence), crit above 0.2/s.
> Thresholds are first-cut and **not calibrated against live data**; record
> the measured rate during the UAT probe below and revise them then.
> **Scrape topology**: the go.d job scrapes ONE NodePort that round-robins
> across 3 replicas at `update_every = 10s` — a rate reading is a
> per-replica sample, not a gateway-wide rate.
> **Severity**: warning / critical (netdata `to: sysadmin`).
> **Last review**: 2026-09-16.

## Symptom

`internal/adapter/middleware/model-rate-limit.go`'s `redisRateLimitHandler`
could not complete a Redis call and fell back to a behavior that depends on
which of the three call sites failed — the `check` label tells them apart,
and they are NOT all the same kind of event:

- `model_rate_limit_success` (`model-rate-limit.go:191`) and
  `model_rate_limit_total` (`model-rate-limit.go:219`) are genuine
  **fail-open admissions**: the request-count check errored, so the request
  is admitted (`c.Next()`) without having passed the limiter.
- `model_rate_limit_record` (`model-rate-limit.go:138`, inside
  `recordRedisRequest`) is NOT a fail-open — the request had already
  succeeded and been admitted; this branch only failed to *record* that
  success (`rdb.LPush` on the MRRLS key), which under-counts the success
  dimension for later checks. No request is admitted or rejected by this
  branch.

Per operator decision D1 (2026-08-27, see
`internal/pkg/metrics/r6_rate_limit_degraded.go`), the two fail-open
branches are the accepted tradeoff for `redisRateLimitHandler`: a Redis
hiccup must not become a relay outage. Each of the three call sites listed
above increments this same counter — read the `check` label before
concluding which kind of event happened; a single occurrence is not
necessarily a fail-open admission.

**Composite risk, scoped to the two genuine fail-open branches**: while
`model_rate_limit_success`/`model_rate_limit_total` are climbing,
`BusinessRateLimit`/`BusinessModelRateLimit` (`internal/adapter/middleware/business_rate_limit.go`,
`bizRedisAllow` fails open per its own doc comment at line 308) and
`RelayConcurrencyLimit` (`internal/adapter/middleware/concurrency_limit.go`,
"every backend error fails OPEN" per its file header) degrade the same way
for the same Redis outage, and `CostSpikeLimit`'s default observe-only mode
does not stop spending either — so during that window, none of the
**relay-path** rate/cost limiters are enforcing. This is distinct from the
web/API-facing limiters (`internal/adapter/middleware/rate-limit.go`'s
`redisRateLimiterKeyed`, lines 44-49): those **fail CLOSED** — a Redis error
there returns HTTP 500 and aborts the request — so a Redis outage does not
open those up at all.

## Detect

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_rate_limit_degraded
curl -s http://localhost:30850/metrics | grep 'lurus_gateway_rate_limit_degraded_total{'
```

The `check` label tells you which of the three Redis calls is failing
(`model_rate_limit_success`, `model_rate_limit_total`, `model_rate_limit_record`
— see the doc comment on `RateLimitDegradedTotal` for what each means).

## Reconcile

- Check Redis reachability and health directly:
  `redis://redis.lurus-system.svc.cluster.local:6379/2` (per `CLAUDE.md`'s
  K8s Deployment Facts — DB 2, not 0).
- Check whether the degradation is isolated to newhub's connection (network
  policy, connection pool exhaustion) or Redis itself is down (would also
  show in session/channel-cache symptoms, since they share the same Redis
  instance at a different DB index).

## Recover

Restoring Redis reachability is the fix — there is no newhub-side toggle
that re-enables enforcement while Redis stays unreachable (fail-open is the
accepted behaviour, not a bug to patch around mid-incident). Once Redis
calls succeed again, the counter simply stops climbing; no reset action is
needed.

## Verify

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_rate_limit_degraded
```
returns to CLEAR once new occurrences stop for the alarm's `delay` window.

## Provoking this alarm on demand (proof, not incident)

Unlike the 5xx-burst and failover-suppression alarms, this counter only
increments on a genuine Redis call failure — there is no faultsim mode for
it (`internal/adapter/handler/faultsim.go` simulates upstream *provider*
behaviour, not the Redis backend a rate limiter depends on). Provoking it
live therefore means deliberately interrupting UAT's Redis reachability for
a short, bounded window (e.g. a temporary NetworkPolicy deny, or briefly
scaling the Redis deployment to zero) while sending a few relay requests
through a token subject to the model rate limit, then restoring reachability
immediately after. **This is an operator action on the cluster, not
something this lane's code or scripts perform** — see the cycle plan's UAT
probe for this lane and the "Do not connect to R6" instruction this lane
worked under.

## Prevent

Nothing newhub-side to prevent — this is downstream of Redis availability,
which is out of this service's control. The alarm's job is making an
otherwise-silent fail-open decision visible while it is happening.
