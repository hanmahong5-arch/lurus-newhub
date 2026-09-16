# Runbook — Failover Suppressed Surge

> **Source**: netdata alarm `newhub_failover_suppressed_surge`,
> `deploy/r6-host-netdata/health.d/newhub.conf` — see the conf file's own
> "STATUS" header for whether it is installed on R6 today; ownership and the
> "does this page anyone" limit are in `deploy/r6-host-netdata/README.md`.
> **Triggered by**: `lurus_gateway_relay_failover_suppressed_total` rate
> averaged over 5 minutes — warn above 0.01/s, crit above 0.05/s. Thresholds
> are first-cut and **not calibrated against live data**; record the
> measured rate during the UAT probe below and revise them then.
> **Scrape topology**: the go.d job scrapes ONE NodePort that round-robins
> across 3 replicas at `update_every = 10s` — a rate reading is a
> per-replica sample, not a gateway-wide rate. See
> `deploy/r6-host-netdata/health.d/newhub.conf`'s "PROVOKABLE ON DEMAND"
> section header for the full caveat.
> **Severity**: warning / critical (netdata `to: sysadmin`).
> **Last review**: 2026-09-16.

## Symptom

A relay had already started streaming bytes to the client when the upstream
connection failed mid-stream. Retrying onto a different channel would
concatenate a second, duplicate response body onto the same writer, so the
retry is **deliberately withheld** — `FailoverSuppressedTotal`
(`internal/pkg/metrics/metrics.go`) increments instead. The client's request
ends incomplete; nothing after that point recovers it automatically.

This is a **demand signal, not a routing error**: a rising rate means
upstreams are dropping requests mid-stream at an elevated rate, something
`relay_failover_total` structurally cannot show (those requests never reach
a second channel to be counted there).

## Detect

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_failover_suppressed_surge
curl -s http://localhost:30850/metrics | grep 'lurus_gateway_relay_failover_suppressed_total{'
```

The `reason` label is currently always `stream_already_started` — see the
doc comment on `FailoverSuppressedTotal` if a second reason is ever added.

## Reconcile

- Cross-reference `lurus_gateway_relay_errors_total{error_type="upstream_5xx"}`
  and `lurus_gateway_circuit_breaker_state` for the same window — a surge
  here alongside a 5xx burst on one provider means that provider is
  dropping connections mid-response, not just failing outright. See
  `doc/runbook/upstream-5xx-burst.md`.
- Check whether the surge correlates with a specific provider/channel by
  reading recent relay logs for `stream_already_started` around the surge
  window (the counter itself carries no provider label — see the doc
  comment for why: "the counter is read as a gateway-wide rate, and the
  per-channel attribution already lives on relay_errors_total").

## Recover

No automatic recovery ships with this alarm. The affected requests already
failed from the client's perspective by the time the counter increments —
there is nothing left to retry safely. Recovery is disabling or
deprioritizing the responsible channel:
`PUT /api/v2/{tenant_slug}/channels/{id}` with `status=2`
(`common.ChannelStatusManuallyDisabled`) if one provider is clearly
responsible.

## Verify

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_failover_suppressed_surge
```
returns to CLEAR once new occurrences stop for the alarm's `delay` window.

## Provoking this alarm on demand (proof, not incident)

UAT only (`FAULTSIM_TOKEN` unset in production). Seed a UAT channel pointing
at the loopback fault-sim endpoint with a model named `mid_stream_abort`
(`FaultModeMidStreamAbort` — emits a few well-formed SSE frames, then closes
without a terminator), then drive enough streamed requests through it in
under 5 minutes to cross the warn threshold:

```
for i in $(seq 1 5); do
  curl -s -N https://test-newhub.lurus.cn/v1/chat/completions \
    -H "Authorization: Bearer $UAT_TOKEN" -H 'Content-Type: application/json' \
    -d '{"model":"mid_stream_abort","stream":true,"messages":[{"role":"user","content":"hi"}]}' \
    >/dev/null
done
```

Then watch `curl -s http://localhost:19999/api/v1/alarms?all | grep -A5 newhub_failover_suppressed_surge`
move CLEAR → WARNING, and CLEAR again once the burst stops (operator-run —
see the cycle plan's UAT probe for this lane). Given the scrape-topology
caveat above, the operator's probe is what establishes the real number
needed and should update the header's thresholds afterward, not this
runbook's prose.

## Prevent

Nothing to prevent on newhub's side — the suppression is the correct
response to a mid-stream failure, not a bug. The alarm exists so a rising
rate of it gets a human's attention rather than accumulating silently.
