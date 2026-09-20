# R6 host netdata — newhub-owned alarm definitions

This directory is the source of truth for newhub's netdata **health** config
on the R6 host, mirroring the pattern `deploy/r6-host-nginx/` already uses for
the host's edge nginx vhosts: the file that actually runs on the host is a
copy of the file in this repo, not something hand-edited in place.

## Provenance

`health.d/newhub.conf` was adopted verbatim from the live R6 host copy
(mtime 2026-08-20; itself ported at some earlier point from
`deploy/grafana/newhub-alerts.yaml`, which commit `e0425425` deleted along
with the rest of `deploy/grafana/` — that file is no longer this repo's
tuning source of truth for anything). It was brought into this repo
2026-09-15; three new alarms (`newhub_upstream_5xx_burst`,
`newhub_rate_limit_degraded`, `newhub_failover_suppressed_surge`) were
merged into it 2026-09-16. **This repo directory is now the source of
truth** — edit `health.d/newhub.conf` here, then re-run
`scripts/install-netdata-alarms.sh`; do not hand-edit the host copy, the
next install silently overwrites it.

## What's here

| File | Bind-mount source | Installs to (in the `obs-netdata` container) |
|---|---|---|
| `health.d/newhub.conf` | `/data/obs-pack/runtime/netdata/config/health.d/newhub.conf` on the R6 host | `/etc/netdata/health.d/newhub.conf` |

The mount is **per file**, not per directory: dropping a *new* file into the
host's `health.d/` directory does nothing until that file is itself named as
a bind source. `scripts/install-netdata-alarms.sh` only manages
`newhub.conf` — a second alarm file would need its own bind mount added to
the container definition first (out of scope for this directory).

`health.d/newhub.conf` currently defines 29 alarms:

- 8 ported from the host's original 2026-08-20 copy
  (`newhub_platform_breaker_open`, `newhub_billing_outbox_failures`,
  `newhub_credit_pool`, `newhub_channel_breaker_open`,
  `newhub_billing_outbox_backlog`, `newhub_relay_5xx_elevated`,
  `newhub_cost_spike_429`, `newhub_quota_cap_402`).
- 3 added 2026-09-16, chosen because they can be **provoked on demand** — see
  each one's linked runbook page for the exact trigger, so it can be proved
  live rather than trusted on faith: `newhub_upstream_5xx_burst`,
  `newhub_rate_limit_degraded`, `newhub_failover_suppressed_surge`.
- 1 added 2026-09-19 (cycle-11 L7): `newhub_settlement_failed` — fires on
  `lurus_billing_settlement_failed_total`, the counter
  `app.SettleConsume` (internal/app/settlement_outcome.go) increments when a
  consume-quota settlement call fails. Added **in-repo only** in the same
  change; not yet installed onto R6 — see the conf file's own "STATUS
  UPDATE 2026-09-19" header note.
- 1 added 2026-09-19 (cycle-12 L8): `newhub_db_slow_queries` — watches
  `lurus_gateway_db_slow_query_total`, incremented by the GORM logger
  (`internal/adapter/repo/gorm_logger.go`) for every statement over
  `DB_SLOW_QUERY_MS` (default 200ms). It is deliberately NOT bound to the
  `go_sql_wait_count_total` pool gauge, which is the more direct saturation
  signal: those `go_sql_*` series come from prometheus/client_golang's
  `NewDBStatsCollector`, and the repo oracle below can only resolve an `on:`
  line to a series declared with `promauto` in `internal/pkg/metrics`, so an
  alarm bound to one of them would sit outside the gate that keeps alarms
  from going silently dead. GORM's measured statement time includes the wait
  for a pooled connection, so pool saturation does surface on this counter;
  `doc/runbook/db-pool-saturation.md` covers reading it together with the
  `go_sql_*` gauges to tell "pool saturated" from "database slow". Added
  **in-repo only**; not yet installed onto R6 — see the conf file's "STATUS
  UPDATE 2026-09-19 (cycle-12 L8)" header note.
- 1 added 2026-09-19 (cycle-12 L8): `newhub_channel_cache_stale` — watches
  `lurus_gateway_channel_cache_sync_failed_total`, incremented by
  `repo.rebuildChannelCache` (`internal/adapter/repo/channel_cache.go`) once
  per rebuild abandoned because one of its two database reads failed. The
  same cycle-12 change made that failure keep the previous routing table
  instead of emptying it, which is the better failure but a **silent** one —
  a channel disabled or key-rotated during the incident keeps serving on that
  replica. This alarm is what keeps the trade from being "loud bug for silent
  bug". Both label values are pre-registered at zero
  (`internal/pkg/metrics/db_observability.go` `init()`), so the chart exists
  from boot and an absent chart cannot be misread as "no failures". Runbook
  is the same page as `newhub_db_slow_queries`
  (`doc/runbook/db-pool-saturation.md`). Added **in-repo only**; not yet
  installed onto R6 — same header note.
- 1 that was already live in this file but this count had never mentioned by
  name: `newhub_rate_limit_memory_fallback` (cycle-12 L4) — a lower-severity
  sibling of `newhub_rate_limit_degraded` that watches the same
  `lurus_gateway_rate_limit_degraded_total` series filtered to
  `check=web_rate_limit_backend_memory` (a credential/abuse bucket that
  fell back to the process-local limiter, still enforcing, just per replica
  instead of cluster-wide). See `doc/runbook/rate-limit-degraded.md`, which
  the two alarms share.
- **14 added 2026-09-20 (cycle-13 L10)**, alarm completion pass — all
  **in-repo only**, not yet installed onto R6 (same `scripts/install-netdata-alarms.sh`
  run, same owner item O2; see the conf file's "STATUS UPDATE 2026-09-20"
  header note for the full list and the two cross-lane notes on
  `newhub_log_retention_backlog`/`newhub_task_stalled`):
  `newhub_metrics_scrape_stale` (the one alarm bound to scrape health
  itself, not application behavior — every other alarm in the file goes
  silently stale if this one is red), `newhub_channel_auto_disabled_error` /
  `newhub_channel_auto_disabled_latency` / `newhub_channel_sole_latency_ban_skipped`
  (automatic channel status changes made with no operator action —
  `doc/runbook/channel-auto-ban.md`), `newhub_credit_pool_debit_lost` /
  `newhub_credit_pool_lookup_miss` (post-consume credit-pool debits lost to
  hard DB errors — money-conservation violations, `crit`/`warn`
  respectively), `newhub_billing_advisory_meter_lost` /
  `newhub_billing_zero_amount_charge` (shadow-ledger write loss / wallet
  rounding-to-zero under `LOCAL_LEDGER_ADVISORY`), `newhub_credit_pool_stranded_open`
  (this file had no alarm bound to the gauge `doc/runbook/wallet-revert-stranded.md`
  already tells operators to read), `newhub_upstream_insufficient_balance`
  (an upstream provider's own account balance ran out — distinct from
  newhub's local tenant quota/credit-pool 402s), `newhub_task_stalled` /
  `newhub_schema_migrations_pending` / `newhub_panics_recovered` (three Go
  doc comments in `internal/pkg/metrics` claimed "alert on any
  increase"/"the condition to page on"/"should page" with nothing bound to
  them — `TestNetdataSelfClaimedAlertableSeriesAreBound`, added this same
  cycle, is the reverse gate that keeps a fourth one from going unnoticed
  the same way `newhub_credit_pool_lookup_miss` above did), and
  `newhub_log_retention_backlog` (cycle-13 L6's log-retention task
  backlog gauge).

Every alarm reads the same `/metrics` endpoint netdata's go.d `prometheus`
collector already scrapes on R6 (job name `newhub`,
`http://localhost:30850/metrics`, per `CLAUDE.md`'s "`/metrics`" row — this
directory does not add or change that scrape config). See `doc/runbook/INDEX.md`
"Repo-owned netdata alarms" for the full list with links to each runbook.

## LIVE STATUS is dated, not evergreen

The conf file's own header carries a `# STATUS:` line with the date this
repo copy was last synced from / installed onto the host — read that line,
not this README, for the current state. As of 2026-09-16: repo copy synced
from host 2026-08-20, three new alarms merged 2026-09-16, and the merged file
**installed** onto R6 (bind source md5 `85abc751e883f732777e75529e2549bb`,
matching this repo). It is **installed but not loaded**: `netdatacli
reload-health` inside the `obs-netdata` container does not return (30s timeout,
netdata v2.10.3, container healthy), so netdata is still evaluating the
8-template file it read at startup and `newhub_upstream_5xx_burst`,
`newhub_rate_limit_degraded` and `newhub_failover_suppressed_surge` are **not
live**. `GET /api/v1/alarms?all` currently lists three newhub alarms, all from
the original set. Loading the new ones needs a working reload path or an
`obs-netdata` restart — the latter briefly blinds monitoring for every service
on R6, not just newhub, so it is an operator call and was deliberately not
taken here. The previous host file is backed up at
`/root/c9-netdata/newhub.conf.backup-20260916`.

The three alarms added on 2026-09-19 (`newhub_settlement_failed`, cycle-11
L7, and `newhub_db_slow_queries` + `newhub_channel_cache_stale`, cycle-12 L8)
are a step behind even that: they were added to this repo copy only, so the
bind source on R6 no longer matches this file and the md5 above is stale for
it. All three need the install script run before netdata sees them at all, on
top of the reload/restart the 2026-09-16 three are still waiting on. Same
operator item (O2).

Several of the 8 originally-ported alarms carry a dated "LIVE STATUS"
comment recording what the operator observed directly on the R6 host on
2026-09-15 via the netdata alarm API — specifically, which alarms are
currently **bound** (a netdata chart exists matching the `on:` line) versus
**unbound** (declared and evaluated, but no matching chart exists, so the
alarm can never fire). This repo's own oracle (below) cannot observe chart
binding from a checkout — it can only prove the underlying Prometheus series
is real and written, not that a netdata chart currently exists for it. The
unbound ones observed 2026-09-15 (`newhub_credit_pool`,
`newhub_channel_breaker_open`, `newhub_cost_spike_429`,
`newhub_quota_cap_402`) are each written by production code but have simply
had no matching data since the scrape job started — see each one's comment
in `health.d/newhub.conf` and its runbook page for detail. `newhub_relay_5xx_elevated`
is bound but only to the `path=/api/health status=503` chart, i.e. it
watches health-check failures, not general relay traffic — see
`doc/runbook/relay-5xx-elevated.md`.

## Ownership boundary — read this before assuming an alarm pages anyone

**This directory proves an alarm can transition CLEAR → WARNING/CRITICAL and
that the transition is visible in netdata's own alarm API
(`GET /api/v1/alarms` / `/api/v1/alarm_log`). It does not prove, and cannot
prove from a repo checkout, that a human is notified.** Notification is
controlled by `health_alarm_notify.conf` on the R6 host — the recipients
configured under each `to:` role (`newhub.conf`'s alarms all target
`sysadmin`). That file is host-local configuration this repo does not own
and does not ship, but it is not empty: `SEND_EMAIL="NO"`,
`SEND_CUSTOM="YES"`, `role_recipients_custom[sysadmin]="default"` — a custom
webhook sender is configured. This repo cannot confirm from a checkout where
that webhook actually delivers. The remaining owner action (tracked as O2 in
the current cycle's plan) is for the operator to verify the custom sender's
destination during the live probe and record it here; until that is done,
"alerting exists" means "queryable from the netdata API and forwarded to
whatever `SEND_CUSTOM` is configured to hit," not confirmed "someone is
paged." Say this plainly to anyone reading this directory as if it were a
finished on-call story — it isn't yet.

**O2 verification (fill in after the operator's probe):** _pending —
`SEND_CUSTOM` destination not yet confirmed as of 2026-09-16._

## Install

```
scripts/install-netdata-alarms.sh
```

Run **on the R6 host itself**, from wherever the file has been copied to —
the script does not assume a git checkout is present on the host; it only
needs `health.d/newhub.conf` to sit next to it (see the script header for
the exact layout it expects). It writes to
`/data/obs-pack/runtime/netdata/config/health.d/newhub.conf` (the bind
source for the `obs-netdata` container — see "What's here" above) and
reloads via `docker exec obs-netdata netdatacli reload-health`; there is no
host-level netdata process or stock-config fallback directory to write to
instead. Root is only required if that destination is not writable by the
running user. It is idempotent: re-running it simply re-copies the current
source file over the installed one (so a stale host copy never lingers) and
reloads, it does not duplicate or accumulate stale alarms. If the
`obs-netdata` container is not running or `docker` is unreachable, the
script fails loudly (nonzero exit) rather than silently leaving the file
installed-but-not-reloaded — set `INSTALL_ONLY=1` to acknowledge skipping
the reload deliberately.

After installing, confirm the alarms appear:

```
curl -s http://localhost:19999/api/v1/alarms?all | grep -o '"newhub_[a-z_]*"' | sort -u
```

## Chart naming (verified against the live host, 2026-09-15)

- context = `prometheus.newhub.<metric>` (job = `newhub`)
- chart id = `prometheus_newhub.<metric>-<label>=<value>...` — one chart per
  label-set, single dimension named after the metric, counters
  rate-converted by go.d, `update_every = 10s`.
- Because the scrape job hits one NodePort that round-robins across 3
  replicas (`deploy/k8s/r6-stage/deployment.yaml` `replicas: 3`), a rate
  reading on any of these alarms is a per-replica sample, not a
  gateway-wide rate — see each new alarm's runbook "Scrape topology" note.
  `lookup: average -Nm` on the rate-converted dimension is used throughout;
  never `sum`, which only equals an event count if the collection interval
  were 1s (it is 10s here).

Before relying on a warn/crit threshold, confirm the chart an alarm is
actually attached to:

```
curl -s http://localhost:19999/api/v1/charts | grep -o '"prometheus_newhub[^"]*"' | sort -u
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
- Whether a netdata chart currently exists (is "bound") for a given alarm's
  `on:` line — that is live, data-dependent host state this repo cannot see
  from a checkout. The "LIVE STATUS" section above and the dated comments in
  `health.d/newhub.conf` record what the operator observed directly.

Adding an alarm on a new series means adding the `alarm:`/`template:` block
(with its `# series:` annotation and a `# runbook:` pointer) and a runbook
page linked from `doc/runbook/INDEX.md` in the same change.
`internal/pkg/metrics/netdata_alarm_series_test.go` enforces the series
annotation and the runbook/INDEX pairing as CI gates (see "The repo oracle"
below) — this is a real test failure, not a convention.

## The repo oracle

`internal/pkg/metrics/netdata_alarm_series_test.go` runs three checks
against every non-`# DEAD`-marked `template:`/`alarm:` block in
`health.d/*.conf`:

1. **`TestNetdataAlarmsNameOnlyLiveSeries`** — resolves the block's `on:`
   line (the line netdata itself reads; the `# series:` comment above it is
   a required cross-check, not the thing evaluated) to a Prometheus series
   and fails if that series is not declared **and** actually written by
   production code in `internal/pkg/metrics` — the same "declared but never
   written" bar `declared_series_written_test.go` already holds the package
   itself to, applied here to whatever `Namespace`/`Subsystem`/`Name`
   literals each `promauto` block actually declares (not a fabricated
   `lurus_gateway_` prefix — this package has metrics that intentionally
   don't carry it, e.g. `newhub_rate_limited_total` in `ratelimit.go`).
2. **`TestNetdataAlarmsRequireSeriesAnnotation`** — fails if a non-dead
   block has no `# series:` annotation at all (the converse of #1: a block
   with zero annotation used to be invisible to the oracle instead of
   failing it).
3. **`TestNetdataAlarmsChartLabelKeysAreDeclared`** — for any `chart
   labels: key=value` filter, fails if `key` is not one of the metric's
   declared label names. It cannot check whether the *value* is ever
   emitted (a data-dependent live fact, see "LIVE STATUS" above) — only
   that the key itself is real.
4. **`TestNetdataAlarmsHaveARunbookLinkedFromIndex`** — fails if a non-dead
   block names no runbook, the named runbook file doesn't exist, or
   `doc/runbook/INDEX.md` doesn't link to it.

An alarm on a series nobody emits, an unannotated block, a stale label
filter, or a runbook-less alarm are all silent-forever failure modes from
inside netdata's own view — none of them ever produce an error, they just
never fire or never get read. These four tests are what turns each into a
build failure instead.
