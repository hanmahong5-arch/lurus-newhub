# Runbook — Shipped Defaults vs the Live `options` Table

> **Source**: no alarm. This is a procedure runbook.
> **Triggered by**: read it before answering "why didn't the gateway do X?"
> when X is governed by a tunable — failover, an automatic channel ban, a
> quota reminder, the cost-spike fuse — and before changing any `options`
> row in production.
> **Severity**: n/a.
> **Last review**: 2026-09-21 (cycle-14 L2).

## The one thing to understand

newhub resolves a setting in exactly two steps:

1. The binary starts with the value compiled into
   `internal/pkg/common/constants.go`.
2. `repo.InitOptionMap()` then overlays **only the rows that exist in the
   `options` table**, and `repo.SyncOptions` re-applies that overlay on every
   replica every `SYNC_FREQUENCY` seconds (60).

There is no third step. **A setting with no row in `options` runs on the
compiled default, forever, silently.** The production table is small — the
operator's 2026-09 check found six rows — so for almost every key in the
table below, the shipped value *is* the production value.

That is how relay failover shipped switched off: `RetryTimes` had no row, the
compiled default was `0`, and `0` means "one upstream attempt, no failover"
while `internal/app/channel_select.go`'s doc comment and
`internal/adapter/handler/relay.go` were both written as though cross-group,
cross-priority retry was running.

Environment variables are a **separate** mechanism that covers a different set
of knobs (`RELAY_*`, `DB_CONNECT_*`, `COST_SPIKE_*`, the rate-limit budgets).
A key in the table below marked "DB option" has **no** env var; setting one
does nothing.

## Check the live values — one query

```bash
# PROD (ns lurus-newhub → DB newhub). UAT is the same query against newhub_uat.
kubectl exec -n database lurus-pg-0 -- psql -U postgres -d newhub -c \
  "select key, value from options order by key;"
```

That prints the **whole** table — it is small enough to read in full, and
reading it in full is the point: what matters is which keys are *absent*.
If you want only the keys this runbook covers:

```bash
kubectl exec -n database lurus-pg-0 -- psql -U postgres -d newhub -c \
  "select key, value from options where key in (
     'RetryTimes','ChannelDisableThreshold',
     'AutomaticDisableChannelEnabled','AutomaticEnableChannelEnabled',
     'PreConsumedQuota','QuotaRemindThreshold','QuotaPerUnit',
     'QuotaForNewUser','QuotaForInviter','QuotaForInvitee',
     'SessionTimeoutMinutes','RegisterEnabled','LogConsumeEnabled'
   ) order by key;"
```

**Zero rows back is the normal result, and it is not an error** — it means
every one of those keys is running the shipped value in the next table. Do
not read an empty result as "the query failed": run the first query, confirm
it returns the handful of rows that do exist, and only then conclude absence.

## What the binary ships

Pinned by `internal/pkg/gates/shipped_defaults_test.go`, which fails if any
of these is edited without the reason being edited with it. Read that file's
blind-spot header before trusting it: **it reads source, not this database**,
so it can be green while production runs something else entirely. This query
is the only check for that half.

| Key | Shipped value | Governs | What a surprising value looks like |
|---|---|---|---|
| `RetryTimes` | **2** (was `0` until cycle 14) | Relay failover budget. Total upstream attempts = this + 1. | `0` = no failover: one upstream 5xx reaches the customer even with a healthy channel for the same model. A large value multiplies both the customer's worst-case wait and the load on already-failing upstreams. |
| `ChannelDisableThreshold` | `5.0` | Seconds a probe may take before the automatic pass counts a latency breach. Production sets this row explicitly. | 5s is tight against ordinary upstream jitter — see `channel-auto-ban.md`, which explains why hysteresis exists and why raising this is still an open operator item. |
| `AutomaticDisableChannelEnabled` | `false` | Master switch for **every** automatic channel ban, error-class and latency alike. | `false` means no channel is ever auto-banned no matter what `channel-auto-ban.md`'s decision table says — that whole table is conditional on this flag. |
| `AutomaticEnableChannelEnabled` | `false` | Automatic re-enable of an auto-disabled channel. | `false` means an auto-disabled channel stays down until an operator re-enables it. |
| `PreConsumedQuota` | `500` | Fallback pre-authorisation amount, quota units. | Taken once per request, not once per retry — see the `RetryTimes` note below. |
| `QuotaRemindThreshold` | `1000` | Balance under which the low-quota reminder fires. | Too low and nobody is warned before a 402; too high and every customer gets mail constantly. |
| `QuotaPerUnit` | `500000` | Quota-to-currency divisor used by every money-facing surface. | The admin write path refuses `<= 0` and anything `>= 1e9` for this key, so a bad value here can only arrive by direct SQL. |
| `QuotaForNewUser` | `0` | Free quota granted on registration. | `0` is deliberate on this deployment — money and account lifecycle belong to platform — but it is the number an operator coming from upstream New API documentation least expects. |
| `QuotaForInviter` / `QuotaForInvitee` | `0` / `0` | Invite rewards. | The invite flow exists in code and pays nothing by default. |
| `SessionTimeoutMinutes` | `10080` (7 days) | Console session lifetime. | A `<= 0` write is clamped back to `10080` by `repo/option.go`, so "I set it to 0 to disable sessions" silently does nothing. |
| `RegisterEnabled` | `true` | Self-service registration. | Shipped **on**. Both deployed instances turn it off out of band (UAT sets the row to `false`). A freshly stood-up instance is open until someone writes the row. |
| `LogConsumeEnabled` | `true` | Whether a consume-log row is written per billable relay. | `false` silently empties the leaderboard, `quota_data`, and every log-derived dashboard the other runbooks point at. |

## Changing one

Preferred, because it validates before it persists:

```
PUT /api/option/   {"key": "RetryTimes", "value": "3"}
```

`repo.UpdateOption` parses the value **before** writing the row
(`ValidateOptionValue`), so a typo is refused with a 400 instead of landing a
value that every replica then rejects on every sync tick forever.

Direct SQL skips that validation. If you must:

```bash
kubectl exec -n database lurus-pg-0 -- psql -U postgres -d newhub -c \
  "insert into options (key, value) values ('RetryTimes','3')
     on conflict (key) do update set value = excluded.value;"
```

Then wait up to `SYNC_FREQUENCY` (60s) and confirm on a pod, not in the
database — the row existing is not the same as the value being applied. A
value the engine cannot parse is logged (`option <key> rejected: value is not
a valid integer; the previous value is kept`) and counted by
`lurus_gateway_option_parse_rejected_total`; the row stays wrong and every
replica keeps running the old number.

## Why `RetryTimes` does not multiply anyone's bill

Worth stating because it is the first objection to raising it. The
pre-authorisation is taken **once per request, before** the retry loop:
`relay.go` calls `app.PreConsumeQuota` outside the loop, and
`PreConsumeQuota` additionally short-circuits when
`relayInfo.PlatformPreAuthID` is already set. Retries reuse that single hold,
the one deferred `releasePreConsumedOnFailure` releases exactly one, and only
the attempt that actually succeeds settles.

Two further bounds on what the number can cost:

- **An open circuit breaker consumes an iteration of the same loop.** The
  `continue` that skips an Open-breaker channel runs the loop's post
  statement, so a request whose candidates are all breaker-open burns the
  budget without calling any upstream and answers 503 with `Retry-After: 30`.
  `RetryTimes=2` leaves room to skip two dead channels and still make one real
  call; `RetryTimes=1` would not.
- **Failover is suppressed once any byte has reached the client.**
  `shouldRetry`'s streaming gate fires before every other rule, so this number
  can never produce a duplicated or interleaved stream — it only ever applies
  before the first byte. The suppression is counted; see
  `failover-suppressed-surge.md`.

The per-attempt worst case is `RelayDialTimeout` (10s) plus
`RelayResponseHeaderTimeout` (90s) before any response header arrives, and
there is no total request deadline (`RELAY_TIMEOUT=0`, deliberately, so long
SSE streams are never cut). Three attempts is therefore about five minutes of
worst-case wait — the reason the budget is 2 and not larger.

## Blind spots of this runbook

- It lists the keys that had a claim attached to them as of cycle 14. Every
  other key in `repo.InitOptionMap` resolves the same way and is not covered
  here.
- It does not cover the hierarchical `<config>.<field>` keys
  (`gemini.safety_settings`, `fetch_setting.domain_list`, …), which go through
  a different dispatch (`applyHierarchicalOption`) with its own defaults.
- The values in the table are what the **repo at this commit** ships. A
  deployed pod is running whatever image it is running; check `/api/status`
  or the deployment's pinned digest before assuming this file describes it.
