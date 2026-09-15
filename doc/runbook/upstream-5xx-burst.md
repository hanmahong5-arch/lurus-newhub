# Runbook — Upstream 5xx Burst

> **Source**: netdata alarm `newhub_upstream_5xx_burst`,
> `deploy/r6-host-netdata/health.d/newhub.conf` (installed via
> `scripts/install-netdata-alarms.sh`; ownership and the "does this page
> anyone" limit are in `deploy/r6-host-netdata/README.md`).
> **Triggered by**: `lurus_gateway_relay_errors_total{error_type="upstream_5xx"}`
> rising — 5 in 5 minutes warns, 20 in 5 minutes is critical (thresholds are
> first-cut, no production traffic to calibrate against; see
> `deploy/k8s/r6-stage/newhub-prometheus-rule.yaml`'s equivalent, undeployed,
> `RelayProviderErrorSpike` rule for the same caveat).
> **Severity**: warning / critical (netdata `to: sysadmin` — see the README's
> "Ownership boundary" for what that does and does not mean today).
> **Last review**: 2026-09-15.

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
a human deciding whether to disable a channel manually
(`PUT /api/v2/admin/channels/:id` with `status=disabled`) while the provider
recovers.

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
see the cycle plan's UAT probe for this lane).

## Prevent

Nothing to prevent — 5xx from a third-party provider is expected background
noise at low rate. The alarm exists to catch a *burst*, not any single
occurrence.
