# Runbook — Upstream 5xx Burst

> **Source**: netdata alarm `newhub_upstream_5xx_burst`,
> `deploy/r6-host-netdata/health.d/newhub.conf` — see the conf file's own
> "STATUS" header for whether it is installed on R6 today; ownership and the
> "does this page anyone" limit are in `deploy/r6-host-netdata/README.md`.
> **Triggered by**: `lurus_gateway_relay_errors_total{error_type="upstream_5xx"}`
> rate averaged over 5 minutes — warn above 0.02/s, crit above 0.1/s.
> Thresholds are first-cut and **not calibrated against live data**; record
> the measured rate during the UAT probe below and revise them then.
> **Scrape topology**: the go.d job scrapes ONE NodePort that round-robins
> across 3 replicas at `update_every = 10s` — a rate reading is a
> per-replica sample, not a gateway-wide rate; a burst confined to one
> replica can be under-counted. See
> `deploy/r6-host-netdata/health.d/newhub.conf`'s "PROVOKABLE ON DEMAND"
> section header for the full caveat.
> **Severity**: warning / critical (netdata `to: sysadmin` — see the README's
> "Ownership boundary" for what that does and does not mean today).
> **Last review**: 2026-09-16.

## Symptom

An upstream provider (or several) is returning 5xx to newhub's relay layer.
`error_type=upstream_5xx` is the fail-safe bucket in `types.RelayErrorType`
(`internal/pkg/types/error.go`) — it covers explicit 5xx status codes, a
failed transport-level request (`ErrorCodeDoRequestFailed`, synthesized as a
500), and channel-capacity exhaustion with no status at all. It does **not**
include 4xx, timeouts, rate limits, or the provider-side insufficient-balance
bucket — those have their own `error_type` values and are not what this
alarm watches.

## Detect

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_upstream_5xx_burst
```

Cross-reference which provider/model is responsible — netdata's alarm state
alone does not carry the label breakdown; read it off the same `/metrics`
scrape netdata reads:

```
curl -s http://localhost:30850/metrics | grep 'lurus_gateway_relay_errors_total{' | grep 'error_type="upstream_5xx"'
```

## Reconcile

- Check `lurus_gateway_circuit_breaker_state` for the same provider — a
  sustained burst should already be driving the breaker toward Open
  (`ChannelBreakerStuckOpen` in the undeployed prometheus rule file names the
  same condition; the netdata alarm here is the live substitute for it).
- Check the provider's own status page. A burst that started at a specific
  timestamp with no newhub deploy around it is almost always upstream, not a
  newhub regression.
- If the burst correlates with a newhub deploy, check `git log` for changes
  to the affected provider's adaptor under `internal/adapter/provider/`.

## Recover

No automatic recovery action ships with this alarm — it is observability,
not enforcement. The relay's own retry/failover logic (`relay.go`,
untouched by this lane) already reacts to 5xx per-request; this alarm is for
a human deciding whether to disable a channel manually:
`PUT /api/v2/{tenant_slug}/channels/{id}` with `status=2`
(`common.ChannelStatusManuallyDisabled` — `internal/pkg/common/constants.go`)
while the provider recovers.

## Verify

Confirm the alarm returns to CLEAR once the burst subsides (netdata
re-evaluates on its `every: 30s` schedule, no manual reset needed):

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_upstream_5xx_burst
```

## Provoking this alarm on demand (proof, not incident)

UAT only (`FAULTSIM_TOKEN` is unset in production —
`internal/adapter/handler/faultsim.go`'s routes do not exist there). Seed a
UAT channel pointing at the loopback fault-sim endpoint
(`http://127.0.0.1:3000/api/v2/faultsim/v1/chat/completions`,
`X-Faultsim-Token: $FAULTSIM_TOKEN`) with a model named `http_500`
(`FaultModeHTTP500`), then drive enough requests through it in under 5
minutes to cross the warn threshold:

```
for i in $(seq 1 8); do
  curl -s -o /dev/null -w '%{http_code}\n' https://test-newhub.lurus.cn/v1/chat/completions \
    -H "Authorization: Bearer $UAT_TOKEN" -H 'Content-Type: application/json' \
    -d '{"model":"http_500","messages":[{"role":"user","content":"hi"}]}'
done
```

Then watch `curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_upstream_5xx_burst`
move CLEAR → WARNING, and CLEAR again once the burst stops (operator-run —
see the cycle plan's UAT probe for this lane). Given the scrape-topology
caveat above (3 replicas round-robinned at `update_every = 10s`), 8 requests
in a short window may not be enough to move the alarm if they land on
different replicas — the operator's probe is what establishes the real
number and should update the header's thresholds afterward, not this
runbook's prose.

## Prevent

Nothing to prevent — 5xx from a third-party provider is expected background
noise at low rate. The alarm exists to catch a *burst*, not any single
occurrence.
