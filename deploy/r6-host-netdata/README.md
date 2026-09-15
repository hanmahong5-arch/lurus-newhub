# R6 host netdata — newhub-owned alarm definitions

This directory is the source of truth for newhub's netdata **health** config
on the R6 host, mirroring the pattern `deploy/r6-host-nginx/` already uses for
the host's edge nginx vhosts: the file that actually runs on the host is a
copy of the file in this repo, not something hand-edited in place.

## What's here

| File | Installs to |
|---|---|
| `health.d/newhub.conf` | `/etc/netdata/health.d/newhub.conf` (or the netdata stock-config directory if `/etc/netdata` is unpopulated — see the install script) |

`health.d/newhub.conf` defines three netdata-native alarms, all reading the
same `/metrics` endpoint netdata's go.d `prometheus` collector already
scrapes on R6 (job name `newhub`, `http://localhost:30850/metrics`, per
`CLAUDE.md`'s "`/metrics`" row — this directory does not add or change that
scrape config):

- `newhub_upstream_5xx_burst` — `lurus_gateway_relay_errors_total{error_type="upstream_5xx"}` rising.
- `newhub_rate_limit_degraded` — `lurus_gateway_rate_limit_degraded_total` rising (a rate limiter's Redis check failed and fell back to admitting traffic).
- `newhub_failover_suppressed_surge` — `lurus_gateway_relay_failover_suppressed_total` rising (upstream dropped a request mid-stream and a retry was withheld).

Each is chosen because it can be **provoked on demand** — see the linked
runbook page for the exact trigger — so it can be proved live rather than
trusted on faith. That is deliberately a small first batch, not the full set
of series this service exports; extending it to alarms that only fire under
real incident conditions is future work, once these three have a track
record.

## Ownership boundary — read this before assuming an alarm pages anyone

**This directory proves an alarm can transition CLEAR → WARNING/CRITICAL and
that the transition is visible in netdata's own alarm API
(`GET /api/v1/alarms` / `/api/v1/alarm_log`). It does not prove, and cannot
prove from a repo checkout, that a human is notified.** Notification is
controlled by `health_alarm_notify.conf` on the R6 host — the recipients
configured under each `to:` role (`newhub.conf`'s alarms all target
`sysadmin`) — which is host-local configuration this repo does not own and
does not ship. Populating it is an owner action (tracked as O2 in the
current cycle's plan). Until it is done, "alerting exists" means "queryable
from the netdata API," not "someone is paged." Say this plainly to anyone
reading this directory as if it were a finished on-call story — it isn't yet.

## Install

```
scripts/install-netdata-alarms.sh
```

Run as root (or with sudo) **on the R6 host itself**, from wherever the file
has been copied to — the script does not assume a git checkout is present on
the host; it only needs `health.d/newhub.conf` to sit next to it (see the
script header for the exact layout it expects). It is idempotent: re-running
it after a repo update simply re-copies the current file and reloads
netdata's health config, it does not duplicate or accumulate stale alarms.

After installing, reload netdata's health engine (the install script does
this) and confirm the three alarms appear:

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -o '"newhub_[a-z_]*"' | sort -u
```

## Verifying chart names before trusting a threshold

Netdata's go.d `prometheus` collector derives its chart contexts and
dimension names from the scraped metric and label set **at run time** — that
mapping cannot be confirmed from a repo checkout with no netdata process to
ask, and `health.d/newhub.conf`'s `on:` lines are written to the collector's
documented naming convention (`prometheus_local_<job>.<metric_name>`), not
to a live capture. Before relying on a warn/crit threshold, confirm the chart
this alarm is actually attached to:

```
curl -s http://localhost:19999/api/v1/charts | grep -o '"prometheus_local_newhub[^"]*"' | sort -u
```

If a context name has drifted (a netdata upgrade changed the collector's
naming, or the go.d job name is not literally `newhub`), fix the `on:` line
in `health.d/newhub.conf` in this repo, re-run the install script, and record
the correction in the runbook page for that alarm — do not patch the host
copy directly, or the next install will silently revert the fix.

## What this repo does NOT own here

- The netdata `go.d` scrape job itself (job `newhub` → `/metrics`) — that is
  existing host config, not part of this lane or this directory.
- `health_alarm_notify.conf` recipients — see "Ownership boundary" above.
- Any alarm on a series outside the three listed here. Adding one means
  adding both the `alarm:` block (with its `# series:` annotation) and a
  runbook page in the same change — `internal/pkg/metrics/netdata_alarm_series_test.go`
  and `doc/runbook/INDEX.md`'s hard gate both enforce that pairing.

## The repo oracle

`internal/pkg/metrics/netdata_alarm_series_test.go` parses every
`# series: <name>` annotation in `health.d/*.conf` and asserts the named
series is declared in `internal/pkg/metrics` **and** actually written by
production code (the same "declared but never written" check
`declared_series_written_test.go` already runs for the metrics package
itself). An alarm on a series nobody emits would otherwise be silent
forever — this is the only thing in the repo that would catch a rename or a
typo before it reaches the host.
