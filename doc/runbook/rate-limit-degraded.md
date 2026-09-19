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
> **Last review**: 2026-09-19 (cycle-12 L4: credential buckets no longer
> fail open — see the table below).

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
**relay-path** rate/cost limiters are enforcing.

**Cycle-11 L2 update**: the web/API-facing limiters
(`internal/adapter/middleware/rate-limit.go`'s `redisRateLimiterKeyed`) now
fail OPEN too, under two new labels on the same counter. `redisRateLimiterKeyed`
is reached by every `rateLimitFactory`/`keyedRateLimitFactory` caller in the
package — grepped: `GlobalAPIRateLimit`, `GlobalWebRateLimit`,
`InternalApiRateLimit`, `CriticalRateLimit` (channel-key reveal, TOTP
enroll/confirm/disable), `TotpBackupCodesRateLimit`, `DownloadRateLimit`,
`UploadRateLimit`, `RedemptionRateLimit`, `TopupRateLimit` (unmounted this
cycle), `BootstrapRateLimit`, and `GlobalV2RateLimit`
(`internal/adapter/middleware/rate-limit-v2.go`, `/api/v2` route group).
While Redis is unreachable, the **traffic** buckets do not enforce — a page
load, an `/api` call or a download is admitted unchecked. That is the
accepted tradeoff (the readiness outage D1 fixed), and it is what
`web_rate_limit_backend` counts.

**Cycle-12 L4 update — the credential buckets no longer fail open.** The
statement this section used to carry ("none of those buckets enforce") was
true when it was written and is no longer. `redisRateLimiterKeyed` now
splits its LLen-error path by bucket
(`internal/adapter/middleware/rate-limit.go`,
`rateLimitMemoryFallbackMarks`):

| mark | limiter | Redis-error behaviour |
|---|---|---|
| `RD` | `RedemptionRateLimit` | process-local limiter |
| `BS` | `BootstrapRateLimit` | process-local limiter |
| `TB` | `TotpBackupCodesRateLimit` | process-local limiter |
| `CT` | `CriticalRateLimit` (channel-key reveal, TOTP disable) | process-local limiter |
| `TU` | `TopupRateLimit` (unmounted) | process-local limiter |
| `IKP-IP` | `/internal` pre-auth IP tier | process-local limiter |
| `GW` `GA` `GV` `DW` `UP` `IKR` `IKW` `IKP` `IKF` | traffic / already-authenticated-key tiers | fail open (unchanged) |

The process-local limiter is a **per-replica** ceiling: with 3 replicas
behind one NodePort, the effective budget during an outage is up to
`3 x budget` rather than `budget`. That is weaker than Redis and is not the
same thing as unthrottled. `middleware.TestRateLimitMarks_EveryMarkIsClassified`
fails if a new limiter is added without a side.

- `web_rate_limit_backend` — the LLen check on the bucket key errored
  (backend unreachable) on a traffic bucket. The request is admitted; no
  self-heal needed, the next request retries the same call.
- `web_rate_limit_backend_memory` — the same LLen error on a credential
  bucket from the table above. **Not** a fail-open: the request was measured
  against the process-local limiter, so callers can still get a 429 while
  this series climbs. A 429 whose `X-RateLimit-*` headers look right during
  a Redis incident is this path, not a bug.
- `web_rate_limit_corrupt` — the backend answered but the list's stored
  timestamp doesn't parse. The request is admitted AND the key is deleted
  (`rdb.Del`) so a fresh window starts on the next request for that ident,
  instead of every future request failing the same parse forever.

Before this change these two returned HTTP 500 and aborted the request —
including the k8s probe routes (`/api/status`, `/api/health`) mounted behind
`GlobalAPIRateLimit`, so a Redis blip took every replica out of Service
readiness at once, and liveness restarted them. That readiness/restart loop
is what this change fixes. It does NOT change what happens if a pod is
*booting* while Redis is unreachable: `internal/pkg/common/redis.go:62`
still `FatalLog`s on a failed boot-time Redis ping, unchanged this cycle —
a pod that starts up during a Redis outage still exits.

Direct in-cluster requests to the two probe routes now also bypass the
limiter entirely (`middleware.IsDirectInClusterRequest` — RemoteAddr
loopback/private and none of X-Forwarded-For/X-Real-IP/Forwarded present);
in today's topology that condition is met by the kubelet, and also by
anything else on the node/cluster network calling the pod with no
forwarding header. A request relayed through the host nginx carries a
forwarding header (`deploy/r6-host-nginx/*.conf:37-38,42-43`) and stays
rate limited exactly as before.

## Detect

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_rate_limit_degraded
curl -s http://localhost:30850/metrics | grep 'lurus_gateway_rate_limit_degraded_total{'
```

The `check` label tells you which Redis call is failing
(`model_rate_limit_success`, `model_rate_limit_total`, `model_rate_limit_record`,
`web_rate_limit_backend`, `web_rate_limit_backend_memory`,
`web_rate_limit_corrupt` — see the doc comment on `RateLimitDegradedTotal`
for what each means). `web_rate_limit_backend_memory` climbing alongside
user reports of 429s on redeem/login-bootstrap/2FA screens is the expected
combination during a Redis outage, not a second incident.

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
