# Runbook — Automatic Channel Probing / Auto-Ban

> **Source**: no alert wired to this today (procedure, not an alert
> trigger) — read this before touching `ChannelDisableThreshold`,
> `AutoTestChannelEnabled`, or investigating why a channel flipped status
> with no operator action.
> **Triggered by**: n/a — see "Symptom" below for when to read this.
> **Severity**: operational (a wrongly-tuned threshold can flap the only
> channel serving a model, or silently protect a genuinely dead one).
> **Last review**: 2026-09-19 (cycle-11 L3).

## Why this needed a fix

Before cycle 11, the automatic probe pass (`AutomaticallyTestChannelsWithContext`
→ `testAllChannels`) had four compounding problems on a single-channel
production deployment:

1. **No end-to-end deadline.** `testChannel`'s `*http.Request` carried no
   context, so `c.Request.Context()` downstream resolved to
   `context.Background()`. The shared relay transport already bounds two
   narrower cases — `RelayResponseHeaderTimeout` (90s to the first response
   header, `internal/pkg/common/init.go`) and a 300s trickling-body read
   timeout (`internal/app/http_client.go`) — but nothing bounded the
   combination; the 2026-09-17 audit's field note records one probe against a
   hung upstream running about 900s.
2. **Every replica probed independently.** `taskreg.Register` registered
   the task with `leaderOnly=false`, so all three master-capable replicas
   ran their own full pass, each with independent ban/enable authority,
   every tick.
3. **A single slow pass could ban.** The disable threshold
   (`ChannelDisableThreshold`, production value 5s) was compared against
   one probe's elapsed time with no hysteresis — the only production
   channel flapped several times a day against ordinary latency jitter.
4. **Every automatic probe wrote a real consume-log row** (`repo.RecordConsumeLog`,
   token name `模型测试`, user id 1), polluting the leaderboard and
   `quota_data` with entries no customer ever generated.

## What changed

| Area | Before | After |
|---|---|---|
| Deadline | none (`context.Background()`) | `CHANNEL_TEST_TIMEOUT_SECONDS` (default 120s), read fresh per probe |
| Scheduling | every master-capable replica, every tick | leader only (`common.IsLeader()` gate in the ticker loop; `taskreg.Register(..., leaderOnly=true, ...)`) |
| Latency ban | one slow pass → immediate ban | three **consecutive** breaches required (see Hysteresis below) |
| Sole-channel protection | none | a channel that is the only enabled route for a `(group, model)` pair is never latency-banned |
| Signal | real `repo.Log` row per automatic probe | `lurus_gateway_channel_probe_total` / `lurus_gateway_channel_probe_duration_seconds` / `lurus_gateway_channel_auto_status_total` (below) |

Error-class bans (`app.ShouldDisableChannel` — invalid key, quota exhausted,
auth/permission errors, keyword match) are **unchanged**: they still ban on
the first occurrence, no streak required. Only the *latency* path gained
hysteresis. `TestEvaluateProbeOutcome_ErrorClassBansOnTheFirstPass` and
`TestAutoProbe_ErrorClassFlipsStatusToAutoDisabled` (the latter driving a
real `httptest` upstream answering 401 `invalid_api_key` through
`autoProbeChannel` and the real `app.DisableChannel` chain) are this claim's
oracle — deleting the error-class branch turns both red.

## Consumers

`GET /api/channel/test/:id` (the single-channel manual path only — not
`GET /api/channel/test` with no id) has one known external consumer:
**switch** (`2c-gui-switch/internal/hub/admin/channels.go:66`,
`frontend/src/lib/gateway-api.ts:382`). Its "test channel" button now
receives a failure after `CHANNEL_TEST_TIMEOUT_SECONDS` (default 120s)
instead of waiting on the transport's own 90s/300s bounds — the response
shape (`{"success", "message", "time"}`) is unchanged, only the maximum wait
before a `success:false` reply shrinks for a genuinely hung upstream.
`doc/coord/contracts.md` has no `channel-test` row today; this cycle's
report asks the operator to add one (see CROSS_REPO_FOLLOWUP in the L3
repair-round report). switch does **not** call the no-id `/test` or
`/update_balance` routes, so W's `RootAuth` change to those two routes (see
below) does not affect it.

## Decision rules

`internal/adapter/handler/channel_probe_policy.go`'s `evaluateProbeOutcome`
is the single place this logic lives:

1. A **timeout** (the probe's own `CHANNEL_TEST_TIMEOUT_SECONDS` deadline
   firing) is classified as **latency**, never as an error — even though
   the wrapped `context deadline exceeded` message could in principle
   collide with an `AutomaticDisableKeywords` entry, a timeout is routed
   past `app.ShouldDisableChannel` entirely. A timeout always increments the
   streak counter (point 3 below) regardless of
   `AutomaticDisableChannelEnabled` — the flag only gates whether a
   completed streak is allowed to turn into a ban (point 4).
2. A **technically successful response that exceeds `ChannelDisableThreshold`**
   also enters the latency path, but only when `AutomaticDisableChannelEnabled`
   is on — unlike a timeout, a fast-but-under-threshold response is not a
   failure at all, so there is nothing to gate when the flag is off.
3. Every latency-path breach increments a **process-local, in-memory streak
   counter** keyed by channel ID. A clean pass, or an error-class ban,
   resets the streak to zero.
4. Only on the **third consecutive** breach, and only when
   `AutomaticDisableChannelEnabled` is on, does the pass consult
   `repo.SoleEnabledModelsForChannel(channelID, tenantID)` — the
   `(group, model)` pairs this channel serves for which no other *enabled*
   channel, visible to the same tenant scope, exists. A disabled sibling
   does not count as a substitute; a sibling in a different tenant does not
   count either (tenant-scoped routing would never reach it anyway).
   - If the sole-channel set is **non-empty**, the ban is withheld and
     `latency_ban_skipped_sole_channel` is counted instead. The streak is
     NOT reset — if the operator adds a second channel for the same model
     later, the next breach (not three more) can complete the ban.
   - If **empty**, the channel is banned exactly like an error-class ban:
     `app.DisableChannel` (status → AutoDisabled, audit event, root
     notification — all unchanged).

## Streak lifetime — process-local, per-replica, NOT persisted

The streak tracker is a plain in-memory `map[int]int` held by
`channel_probe_policy.go`'s package-level `autoLatencyBreachTracker`. **Two**
callers feed this same tracker, not one:

1. The leader-gated ticker (`AutomaticallyTestChannelsWithContext`'s
   `common.IsLeader()` gate — only the current leader's ticker ever fires).
2. The operator-triggered **"test all channels" button**
   (`GET /api/channel/test`, no id → `TestAllChannels` →
   `testAllChannels` → `autoProbeChannel`) — this answers on **whichever
   replica served that HTTP request**, leader or not.

Consequences:

- Because the tracker is process-local, **streaks are per-replica, not
  per-cluster**. Three "test all channels" clicks that happen to land on
  the same follower (any HTTP-serving replica can answer that route) can
  complete a ban the leader's own ticker never contributed a single breach
  toward — the two callers do not share state across processes, only within
  one.
- A leadership change (pod restart, rolling deploy, lease expiry) hands the
  ticker to a different process with an **empty** tracker for that process.
  A channel two breaches into a streak when leadership changes starts over
  at zero on the new leader — this is a known, accepted gap for this
  cycle's S–M scope, not a bug to chase. In practice this only delays a ban
  that was about to happen anyway.
- This also means the streak is **not visible** via any API today — there
  is no `GET` endpoint for "how many consecutive breaches does channel N
  have right now, on which replica". If you need to know, read the log line
  `channel probe: SoleEnabledModelsForChannel(...) failed, withholding
  latency ban this pass` (only logged on the sole-channel-lookup failure
  path) or watch `lurus_gateway_channel_probe_total{outcome="latency_breach"}`
  rise for the channel's window.

## Metrics

| Series | Type | Labels | Meaning |
|---|---|---|---|
| `lurus_gateway_channel_probe_total` | counter | `outcome` = `ok`\|`error`\|`timeout`\|`latency_breach` | Every probe that goes through `probeChannel` — `GET /api/channel/test[/:id]` (single-channel and "test all") and the leader-gated ticker. Only the automatic pass ever reports `latency_breach` — the manual path has no threshold to compare against. **Does NOT cover** `POST /api/v2/:tenant_slug/channels/:id/test` (`TestChannelV2`) — a second, separately implemented, unmetered probe path (known gap, next-cycle item). |
| `lurus_gateway_channel_probe_duration_seconds` | histogram | none | Wall-clock probe latency, same coverage as above. |
| `lurus_gateway_channel_auto_status_total` | counter | `action` = `disable_error`\|`disable_latency`\|`enable`\|`latency_ban_skipped_sole_channel` | Automatic-pass status changes (or deliberately withheld changes) only — fed by BOTH the ticker and the "test all channels" button, per the Streak lifetime section above. |

No netdata alarm is wired to these yet — this cycle's scope was the probe
mechanics, not alerting. If a page-severity need shows up (e.g. "channel
X has been latency-flapping for an hour"), file it per `INDEX.md`'s "When
to add a runbook" rule and wire `latency_ban_skipped_sole_channel` or a
`rate()` on `channel_auto_status_total{action="disable_latency"}`.

## Environment

`CHANNEL_TEST_TIMEOUT_SECONDS` (default `120`) bounds a single probe's
upstream call, manual or automatic. Read fresh on every probe — no restart
needed to change it.

## The owner-side DB option: `ChannelDisableThreshold`

Production reads `ChannelDisableThreshold` from the `options` table (DB
option, not env), current value **5 seconds** — set by the operator, not by
this code. The Go **code default** (`internal/pkg/common/constants.go`,
`var ChannelDisableThreshold = 5.0`) is unchanged by this cycle's work; the
plan explicitly leaves raising the production value to the operator as a
separate item (O7). 5s against a real upstream's normal jitter is why the
single production channel flapped before hysteresis existed — hysteresis
reduces the blast radius of an unraised threshold, it does not replace
raising it.

## Manual vs "test all" vs the automatic ticker

**Three** distinct paths call into `probeChannel`, not two — the console's
"test all channels" button is easy to mentally lump in with the manual
single-channel test, but it shares the automatic pass's decision machinery,
not the manual path's:

| | Manual `GET /api/channel/test/:id` | "Test all channels" `GET /api/channel/test` (no id) | Leader ticker |
|---|---|---|---|
| Trigger | Operator clicks "test" on one channel in the console | Operator clicks "test all channels" in the console (`web/src/hooks/channels/useChannelsData.jsx`) | `AutomaticallyTestChannelsWithContext`, interval = `AutoTestChannelMinutes` (DB option) |
| Handler | `TestChannel` → `testChannel` → `probeChannel` | `TestAllChannels` → `testAllChannels` → **`autoProbeChannel`** (same function the ticker calls) | `testAllChannels` → `autoProbeChannel` |
| Consume-log row | **Yes** — `channelProbeOptions{RecordConsumeLog: true}`, an operator explicitly asked for this one channel, the row is expected | **No** — `RecordConsumeLog: false`, same as the ticker. It no longer writes `模型测试` rows, even though an operator clicked a button. | **No** |
| Hysteresis / sole-channel protection | N/A — never reaches `evaluateProbeOutcome`, no ban decision is made | **Yes** — shares `autoLatencyBreachTracker` with the ticker (see Streak lifetime above) | Yes |
| Ban authority | No | **Yes** — three "test all channels" clicks against a slow channel can auto-disable it, same as three ticker passes | Yes |
| Leader-gated | No — any replica answers the HTTP request | **No** — any replica answers the HTTP request; runs with full ban authority regardless of which replica served it | Yes (`common.IsLeader()`) |
| Auth (after W's routing change this cycle) | `AdminAuth` | `RootAuth` (operator-only; see HANDOFF_TO_W) | n/a (no HTTP route) |

All three report into `lurus_gateway_channel_probe_total` /
`_duration_seconds`; only the "test all channels" button and the ticker
report into `lurus_gateway_channel_auto_status_total`.

## How a follower shows up

`GET /api/v2/admin/system/tasks` lists `channel-health-test` with
`"leader_only": true`. On a follower, `last_success_at` stays `0` and
`state` reads `"standby"` / `standby_reason: "follower"` — this is
expected, not a fault. Only the current leader's row should show `"ok"`
(or `"overdue"` if the leader itself has stopped probing).
