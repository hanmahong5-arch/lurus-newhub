# Runbooks — Index

Operational playbooks for newhub. One file per failure mode or
recurring procedure. Each runbook starts with **Source** (where the
alert / signal comes from), **Triggered by** (the literal condition),
**Severity**, and **Last review** date.

## Alerts

| Runbook | Trigger | Severity |
|---|---|---|
| [pool-threshold-alert](pool-threshold-alert.md) | `CreditPoolBalanceLow` / `CreditPoolExhausted` rules in `deploy/k8s/r6-stage/newhub-prometheus-rule.yaml` — **NOT DEPLOYED**, nothing evaluates them; today the trigger is a human reading `credit_pool_balance` on the host netdata | warning / page (intended) |
| [wallet-revert-stranded](wallet-revert-stranded.md) | log line `STRANDED wallet debit` from `tenant_credit_pool.go` | page |

### Repo-owned netdata alarms

The 14 rows below all come from `deploy/r6-host-netdata/health.d/newhub.conf`.
**Install state changes as the operator runs `scripts/install-netdata-alarms.sh`
on R6; read the conf file's own "STATUS" header for the current dated state
rather than trusting this table's prose, which cannot update itself.** As of
2026-09-16: repo copy adopted from the host 2026-08-20, three new alarms
merged 2026-09-16, re-installation of the merged file onto R6 is
operator-run and PENDING. A 12th alarm (`newhub_settlement_failed`) was
added 2026-09-19 (cycle-11 L7), in-repo only, same PENDING install state. A
13th (`newhub_db_slow_queries`) and a 14th (`newhub_channel_cache_stale`) were
added 2026-09-19 (cycle-12 L8), also in-repo only, same PENDING install state.
A 15th, `newhub_rate_limit_memory_fallback` (cycle-12 L4), shares the
[rate-limit-degraded](rate-limit-degraded.md) row below rather than adding one
— the two alarms split one runbook by severity.
`internal/pkg/metrics/netdata_alarm_series_test.go`
proves every metric named below is a real, written series and that every row
here is reachable from a `# runbook:` pointer in the conf file — it does not
prove, and cannot prove from a repo checkout, that a given alarm's netdata
CHART currently has any bound data (see each runbook's own "LIVE STATUS"
line, dated 2026-09-15 from the operator's direct host check, for which ones
do).

| Runbook | Trigger | Severity |
|---|---|---|
| [platform-billing-breaker-open](platform-billing-breaker-open.md) | netdata `newhub_platform_breaker_open` — `lurus_billing_circuit_breaker_state` | critical |
| [billing-outbox-failures](billing-outbox-failures.md) | netdata `newhub_billing_outbox_failures` — `lurus_billing_outbox_failed_total` | critical |
| [credit-pool-low](credit-pool-low.md) | netdata `newhub_credit_pool` — `lurus_gateway_credit_pool_balance` | warning / critical |
| [channel-breaker-open](channel-breaker-open.md) | netdata `newhub_channel_breaker_open` — `lurus_gateway_circuit_breaker_state` | warning |
| [billing-outbox-backlog](billing-outbox-backlog.md) | netdata `newhub_billing_outbox_backlog` — `lurus_billing_outbox_pending` | warning |
| [relay-5xx-elevated](relay-5xx-elevated.md) | netdata `newhub_relay_5xx_elevated` — `requests_total{status=5*}` (bound to `path=/api/health` only, see the runbook) | warning |
| [cost-spike-429](cost-spike-429.md) | netdata `newhub_cost_spike_429` — `requests_total{status=429}` | warning |
| [quota-cap-402](quota-cap-402.md) | netdata `newhub_quota_cap_402` — `requests_total{status=402}` | warning |
| [upstream-5xx-burst](upstream-5xx-burst.md) | netdata `newhub_upstream_5xx_burst` — `relay_errors_total{error_type="upstream_5xx"}` | warning / critical |
| [rate-limit-degraded](rate-limit-degraded.md) | netdata `newhub_rate_limit_degraded` — `rate_limit_degraded_total` | warning / critical |
| [failover-suppressed-surge](failover-suppressed-surge.md) | netdata `newhub_failover_suppressed_surge` — `relay_failover_suppressed_total` | warning / critical |
| [settlement-failed](settlement-failed.md) | netdata `newhub_settlement_failed` — `lurus_billing_settlement_failed_total{path}` | warning |
| [db-pool-saturation](db-pool-saturation.md) | netdata `newhub_db_slow_queries` — `lurus_gateway_db_slow_query_total{db}` | warning |
| [db-pool-saturation](db-pool-saturation.md) | netdata `newhub_channel_cache_stale` — `lurus_gateway_channel_cache_sync_failed_total{query}` | warning |

| [release-download-gate](release-download-gate.md) | `RELEASE_GATED_PRODUCTS` entitlement gate (mechanism shipped, default OFF) | activation |

## Procedures (no specific trigger)

| Runbook | When to read |
|---|---|
| [tenant-onboarding](tenant-onboarding.md) | New Reseller signs up — provisioning a tenant + first key |
| [deployment](deployment.md) | Cutting a new image to R6 stage / R1 prod |
| [staging-deploy](staging-deploy.md) | Deploying newhub to R6 STAGE via the working SSH path (`scripts/deploy-stage.sh`; GHA deploy is dead) |
| [ha-deployment](ha-deployment.md) | Multi-replica considerations (session secret, batch updates) |
| [database](database.md) | DB shape, common queries, GORM auto-migrate gotchas |
| [pg-restore](pg-restore.md) | Restoring PostgreSQL from backup |
| [incident-response](incident-response.md) | General incident response framework |
| [oidc-enable-activation](oidc-enable-activation.md) | Turning `OIDC_ENABLED` on (Lutu search is dark without it) — blast radius across four auth paths, and the order that keeps `/api/v2/admin/**` reachable |
| [channel-auto-ban](channel-auto-ban.md) | Investigating why a channel flipped status with no operator action, or tuning `ChannelDisableThreshold`/`AutoTestChannelEnabled` |
| [platform-dependency-degraded](platform-dependency-degraded.md) | platform-core is down/slow, or any outbound dependency (Redis, NATS, webhook, bark/gotify, SMTP, provider admin calls) is hanging — what each caller does and the time bound it now has |

## When to add a runbook

- Page-severity alert without a runbook → file blocks the alert until written.
- Failure mode needed manual recovery twice → runbook on second occurrence.
- Procedure needed >30 min of "look up old Slack threads" → write it down.

## Style

- Order: **Symptom → Detect → Reconcile → Recover → Verify → Prevent**.
- Exact grep/SQL/curl commands, no "check the logs" hand-waving.
- Mark irreversible actions with a **4-eyes** requirement.
- Note source of truth (ADR/audit/commit) so readers can verify no drift.
