# Runbooks — Index

Operational playbooks for newhub. One file per failure mode or
recurring procedure. Each runbook starts with **Source** (where the
alert / signal comes from), **Triggered by** (the literal condition),
**Severity**, and **Last review** date.

## Alerts

| Runbook | Trigger | Severity |
|---|---|---|
| [pool-threshold-alert](pool-threshold-alert.md) | `CreditPoolBalanceLow` / `CreditPoolExhausted` Prometheus rules | warning / page |
| [wallet-revert-stranded](wallet-revert-stranded.md) | log line `STRANDED wallet debit` from `tenant_credit_pool.go` | page |

| [release-download-gate](release-download-gate.md) | `RELEASE_GATED_PRODUCTS` entitlement gate (mechanism shipped, default OFF) | activation |

## Procedures (no specific trigger)

| Runbook | When to read |
|---|---|
| [tenant-onboarding](tenant-onboarding.md) | New Reseller signs up — provisioning a tenant + first key |
| [deployment](deployment.md) | Cutting a new image to R6 stage / R1 prod |
| [staging-deploy](staging-deploy.md) | Deploying newhub to R6 STAGE via the working SSH path (`scripts/deploy-stage.sh`; GHA deploy is dead) |
| [ha-deployment](ha-deployment.md) | Multi-replica considerations (session secret, batch updates) |
| [staging-environment](staging-environment.md) | Bringing up STAGE on R6 from scratch |
| [database](database.md) | DB shape, common queries, GORM auto-migrate gotchas |
| [pg-restore](pg-restore.md) | Restoring PostgreSQL from backup |
| [incident-response](incident-response.md) | General incident response framework |
| [oidc-enable-activation](oidc-enable-activation.md) | Turning `OIDC_ENABLED` on (Lutu search is dark without it) — blast radius across four auth paths, and the order that keeps `/api/v2/admin/**` reachable |

## When to add a runbook

- Page-severity alert without a runbook → file blocks the alert until written.
- Failure mode needed manual recovery twice → runbook on second occurrence.
- Procedure needed >30 min of "look up old Slack threads" → write it down.

## Style

- Order: **Symptom → Detect → Reconcile → Recover → Verify → Prevent**.
- Exact grep/SQL/curl commands, no "check the logs" hand-waving.
- Mark irreversible actions with a **4-eyes** requirement.
- Note source of truth (ADR/audit/commit) so readers can verify no drift.
