# Runbook: PostgreSQL Restore from the pg_dump backups

> **Audience**: on-call operator restoring the `newhub` database after loss or corruption.
> **Live topology (2026-09-19)**: in-cluster StatefulSet `lurus-pg` (pod `lurus-pg-0`) in ns
> `database`, Service `lurus-pg-rw.database.svc.cluster.local:5432`, DB `newhub` (tables in
> `public`). Backups = CronJob `daily-pg-dump` (02:00 Asia/Shanghai) writing every database of the
> instance plus globals to PVC `lurus-pg-backup` under `/backups/`, retained 30 days; off-site copy =
> host cron rsync (source `2l-svc-platform/deploy/r6-host/`); authoritative restore drill = weekly
> host cron `dr-drill.sh` (same repo). Details and the freshness alarms: `doc/runbook/database.md`.
> **RPO**: up to 24 h — the live topology has **no WAL archiving and no PITR**. **RTO** target 30 min,
> measured by the weekly drill (last-success stamp in `database.md`).
>
> 2026-09-19: rewritten. The previous version of this file described the docker-compose + wal-g
> topology retired in 2026-04 (compose service names, a wal-g base-backup fetch, a named docker
> volume); none of those objects exist on R6. `deploy/k8s/docs_claims_test.go` now fails if those
> commands come back into any runbook.

---

## Decision tree

```
Loss type?
├── Whole DB / corruption / disk failure
│   → § A: full restore of the latest dump into `newhub`
│
├── Logical mistake (DROP TABLE, bad UPDATE, …)
│   → § B: restore the latest dump into a scratch DB, copy the table or rows back
│         (state = last dump; there is no point-in-time recovery)
│
└── Single table / rows
    → § B
```

**Snapshot the broken database first** if it is still readable (Pre-flight step 2). Never
overwrite the only copy of the current state.

---

## Pre-flight (always)

```bash
# 1. Stop writes. Permanent k8s changes go through git + ArgoCD (selfHeal reverts a
#    `kubectl scale`): set spec.replicas: 0 in deploy/k8s/r6-stage/deployment.yaml
#    (and r6-uat if the UAT database is affected), push, wait for convergence.
#    Emergency path when ArgoCD itself is down: doc/runbook/staging-deploy.md.
kubectl get deploy -n lurus-newhub            # READY must read 0/0 before you continue

# 2. Open a shell that sees both the backup PVC and the database service.
kubectl run -n database --rm -it restore-shell --image=postgres:16-alpine --restart=Never \
  --overrides='{"spec":{"volumes":[{"name":"b","persistentVolumeClaim":{"claimName":"lurus-pg-backup"}}],"containers":[{"name":"restore-shell","image":"postgres:16-alpine","stdin":true,"tty":true,"volumeMounts":[{"name":"b","mountPath":"/backups"}]}]}}' -- sh

# Inside the shell. Credentials: the superuser password is the one used by the
# daily-pg-dump CronJob (kubectl get cronjob -n database daily-pg-dump -o yaml);
# export PGPASSWORD before the commands below.
export PGHOST=lurus-pg-rw.database.svc PGUSER=postgres

# 3. Snapshot the broken database if it still answers.
pg_dump -Fc -d newhub -f /backups/newhub.broken.$(date +%F-%H%M).dump

# 4. List the dumps you can restore from.
ls -lh /backups | grep -E 'newhub|globals'
```

If `/backups` is empty or the newest `newhub` dump is older than 24 h, the CronJob did not run:
check `kubectl get cronjob -n database daily-pg-dump` and the last job's logs, then fall back to
the off-site copy (host layer, see `database.md` "异地副本"). If both are missing, data after the
last existing dump is lost — say so in the incident record before restoring.

---

## § A — Full restore of the latest dump

```bash
# In the restore-shell from Pre-flight. Roles and passwords live in the globals dump of the
# same batch; restore them first when the target instance was rebuilt from scratch.
ls /backups | grep globals | tail -1                              # e.g. lurus-globals-<TS>.sql
psql -d postgres -f /backups/lurus-globals-<TS>.sql               # only on a rebuilt instance

# Drop and recreate the objects from the dump (the recipe in database.md "Restore").
pg_restore -d newhub --clean --if-exists /backups/lurus-newhub-<TS>.dump
```

Verify before opening writes:

```bash
psql -d newhub -c "SELECT count(*) FROM users;"
psql -d newhub -c "SELECT max(created_at) FROM logs;"    # must match the dump time you chose
psql -d newhub -c "SELECT count(*) FROM schema_migrations;"
```

Then restore `spec.replicas` in git, let ArgoCD converge, and check:

- `GET /api/health` → `checks.schema_migrations` healthy and `/metrics`
  `lurus_gateway_schema_migrations_pending` = 0. A dump older than the newest migration is fine:
  every master-capable replica runs the embedded migration runner at boot (advisory-locked).
- `kubectl logs -n lurus-newhub deploy/lurus-newhub --tail=100` has no `FatalLog`.
- One real relay through the gateway (UAT: faultsim channel; prod: read-only checks only).

---

## § B — One table or selected rows

```bash
# In the restore-shell. Load the dump into a scratch database, copy out what you need.
createdb newhub_restore
pg_restore -d newhub_restore /backups/lurus-newhub-<TS>.dump

pg_dump -d newhub_restore -t public.tokens --data-only -f /tmp/tokens_at_dump.sql

# Look at the SQL before loading it: unique keys already present in the live table make
# the load fail half-way, so delete or narrow the bad rows first.
psql -d newhub -f /tmp/tokens_at_dump.sql

dropdb newhub_restore
```

Stop writes (Pre-flight step 1) unless the affected table is append-only for the whole window.

---

## Drills

- **Authoritative**: host cron `32 5 * * 0` → `2l-svc-platform/scripts/dr-drill.sh` restores the
  latest batch into a throwaway namespace, brings up platform-core against it, runs the smoke
  reconciliation and records the measured RTO. Its last-success stamp is the only evidence that
  the live backups restore; read it before promising an RTO.
- **Removed 2026-09-19**: this repo's `scripts/pg-restore-drill.sh`. It exercised wal-g/S3 (not
  installed on R6) and skipped itself silently when `WALG_S3_PREFIX` was unset, so a green run
  proved nothing about the live pg_dump path.
- **Ten-second non-destructive check**: `database.md` "单库快速验证" (pg_restore `--list` on the
  newest dump).

---

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `/backups` is empty in the restore-shell | wrong PVC name, or the CronJob never ran | `kubectl get pvc -n database lurus-pg-backup`; `kubectl get cronjob -n database`; read the last job's logs, not its status |
| `pg_restore: error: role "…" does not exist` | globals not restored on a rebuilt instance | load the globals dump of the same batch first (§ A step 1) |
| `pg_restore` reports errors on `DROP` statements | objects absent in the target | expected with `--clean --if-exists` on a partial target; errors on `CREATE`/`COPY` are the ones that matter |
| Deployment stays at 3/3 after you scaled it | you used `kubectl scale`; ArgoCD selfHeal reverted it | change `spec.replicas` in git |
| App boots, `lurus_gateway_schema_migrations_pending` > 0 for minutes | migration runner waiting on the advisory lock or failing | `kubectl logs … \| grep -i migration`; `doc/runbook/database.md` "Schema 漂移" |
| Newest dump is older than the incident window | daily cadence; no PITR | restore what exists, record the data loss window, raise owner item O-pitr |

---

## Owner items

- **O-pitr**: if the product needs an RPO measured in minutes, WAL archiving on the in-cluster
  PostgreSQL is a decision (storage target, retention, and a restore path this runbook does not
  have). Until then every promise must say "RPO up to 24 h".

## References

- `doc/runbook/database.md` — backup CronJobs, off-site rsync, freshness alarms, quick verification.
- `2l-svc-platform/deploy/r6-host/` — off-site copy and the weekly `dr-drill.sh`.
- `doc/runbook/staging-deploy.md` — ArgoCD emergency and rollback paths.
- `lurus/doc/decisions/2026-05-07-newhub-pg-ha.md` — the wal-g design; superseded for the live topology.
