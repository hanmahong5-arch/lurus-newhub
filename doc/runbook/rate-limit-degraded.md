# Runbook — Rate-Limit Fail-Open Degradation

> **Source**: netdata alarm `newhub_rate_limit_degraded`,
> `deploy/r6-host-netdata/health.d/newhub.conf` (installed via
> `scripts/install-netdata-alarms.sh`; ownership and the "does this page
> anyone" limit are in `deploy/r6-host-netdata/README.md`).
> **Triggered by**: `lurus_gateway_rate_limit_degraded_total` rising — any
> occurrence in 5 minutes warns (`$this > 0`), 50 in 5 minutes is critical.
> **Severity**: warning / critical (netdata `to: sysadmin`).
> **Last review**: 2026-09-15.

## Symptom

`internal/adapter/middleware/model-rate-limit.go`'s `redisRateLimitHandler`
could not complete a Redis call (`checkRedisRateLimit`'s `LLen`, the token
bucket's `Allow`, or `recordRedisRequest`'s post-response `LPush`) and, per
operator decision D1 (2026-08-27, see `internal/pkg/metrics/r6_rate_limit_degraded.go`),
fell back to **admitting** the request rather than rejecting it. This counter
is the visibility half of that tradeoff — it increments unconditionally,
every time, regardless of the `check` label.

**Composite risk**: while this counter is climbing, `BusinessRateLimit` /
`BusinessModelRateLimit` and `RelayConcurrencyLimit` are degrading for the
same Redis outage, and `CostSpikeLimit`'s default observe-only mode does not
stop spending either — so a sustained Redis outage means **no** rate or cost
ceiling is being enforced on relay traffic for its duration.

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
