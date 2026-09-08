# Runbook: PostgreSQL Restore from wal-g

> **Audience**: on-call operator restoring `lurus-postgres` data after loss/corruption.
> **Prerequisites**: `WALG_*` envs populated in `.env` (see `deploy/single-node/.env.example`),
> base backups available in the S3 prefix.
> **SLO**: RTO ≤ 30 min, RPO ≤ 5 min (set by `archive_timeout=60`).

---

## Decision Tree

```
Loss type?
├── Whole DB / corruption / hardware failure
│   → Full restore: latest base + WAL replay to LATEST  (§ A)
│
├── Logical mistake (DROP TABLE, bad UPDATE, …)
│   → PITR: latest base + WAL replay to specific timestamp  (§ B)
│
└── Single table / row
    → Spin up a parallel restored PG (§ B), then dump+restore the table  (§ C)
```

**Always** snapshot the broken `pg_data` volume first if disk is intact —
never overwrite it during recovery (§ Pre-flight).

---

## Pre-flight (always)

```bash
# 1. Stop the application — prevents new writes during recovery.
docker compose stop lurus-api

# 2. Snapshot the broken volume (if disk is healthy enough to read).
docker run --rm -v lurus-hub_pg_data:/data:ro -v $(pwd):/backup \
  alpine tar czf /backup/pg_data.broken.$(date +%F).tgz -C /data .

# 3. List available base backups.
docker exec lurus-postgres wal-g backup-list

# Expected output:
#   name                      modified             wal_segment_backup_start
#   base_000000010000000000000003  2026-05-08T03:00:01Z  000000010000000000000003
#   base_000000010000000000000007  2026-05-09T03:00:01Z  000000010000000000000007
```

If `backup-list` is empty: **no recovery possible from wal-g**. Fall back to
the volume snapshot tarball (§ Pre-flight step 2). If that's also missing,
you've lost data — escalate.

---

## § A — Full restore to LATEST (RPO ≤ 1 min)

```bash
# 1. Stop PG so we can replace the data dir.
docker compose stop postgres

# 2. Wipe the broken data volume.
docker volume rm lurus-hub_pg_data
docker volume create lurus-hub_pg_data

# 3. Fetch the latest base backup into the volume.
docker run --rm \
  --env-file deploy/single-node/.env \
  -v lurus-hub_pg_data:/var/lib/postgresql/data \
  lurus-postgres:walg-15 \
  wal-g backup-fetch /var/lib/postgresql/data LATEST

# 4. Tell PG to replay all archived WAL after the backup, then exit recovery.
docker run --rm \
  -v lurus-hub_pg_data:/var/lib/postgresql/data \
  lurus-postgres:walg-15 \
  bash -c "echo \"restore_command = '/usr/local/bin/wal-g wal-fetch %f %p'\" \
           >> /var/lib/postgresql/data/postgresql.auto.conf && \
           touch /var/lib/postgresql/data/recovery.signal"

# 5. Start PG — it will replay WAL until the latest archived segment, then
#    promote itself to writable.
docker compose up -d postgres

# 6. Watch logs until you see "archive recovery complete" + "database system is ready".
docker compose logs -f postgres

# 7. Bring the app back up.
docker compose up -d lurus-api
```

Verify:
```bash
docker exec lurus-postgres psql -U lurus -d newhub -c "SELECT pg_is_in_recovery();"  # should be f
docker exec lurus-postgres psql -U lurus -d newhub -c "SELECT count(*) FROM users;"
```

---

## § B — Point-in-Time Recovery (PITR)

Same as § A, except step 4 sets a `recovery_target_time`:

```bash
# Replace TARGET with the last good moment, e.g. just before "DROP TABLE ..."
TARGET="2026-05-08 14:30:00 UTC"

docker run --rm \
  -v lurus-hub_pg_data:/var/lib/postgresql/data \
  lurus-postgres:walg-15 \
  bash -c "cat >> /var/lib/postgresql/data/postgresql.auto.conf <<EOF
restore_command = '/usr/local/bin/wal-g wal-fetch %f %p'
recovery_target_time = '$TARGET'
recovery_target_action = 'pause'
EOF
           touch /var/lib/postgresql/data/recovery.signal"
```

Start PG (step 5), then once recovery pauses at the target:

```bash
# Verify state at target time
docker exec lurus-postgres psql -U lurus -d newhub -c "SELECT count(*) FROM users;"

# If correct, promote
docker exec lurus-postgres psql -U lurus -d newhub -c "SELECT pg_wal_replay_resume();"
docker exec lurus-postgres pg_ctl promote -D /var/lib/postgresql/data

# If wrong, target was too late/early — stop PG, edit recovery_target_time, restart
```

---

## § C — Restore one table (or selective rows)

Use § B to spin up a **parallel** restored DB on a different port, then dump
the desired table out.

```bash
# 1. Run a throwaway restored PG on port 5433.
docker run -d --name lurus-postgres-restore \
  -p 127.0.0.1:5433:5432 \
  --env-file deploy/single-node/.env \
  -e POSTGRES_USER=lurus \
  -e POSTGRES_PASSWORD=$POSTGRES_PASSWORD \
  -v restore_data:/var/lib/postgresql/data \
  lurus-postgres:walg-15

# 2. Inside it, do § B PITR to the target timestamp.
# 3. Once promoted, dump the wanted table:
docker exec lurus-postgres-restore pg_dump \
  -U lurus -d newhub -t public.tokens --data-only \
  > tokens_at_target.sql

# 4. Reload into the live PG.
docker exec -i lurus-postgres psql -U lurus -d newhub < tokens_at_target.sql

# 5. Tear down the restore container.
docker rm -f lurus-postgres-restore
docker volume rm restore_data
```

---

## Monthly drill (recommended)

```bash
bash scripts/pg-restore-drill.sh
```

That script spins up a clean PG container, does a full LATEST restore, runs a
sanity SQL query, and reports PASS/FAIL. Schedule via host cron:

```cron
0 4 1 * * cd /opt/lurus-hub && bash scripts/pg-restore-drill.sh \
          >> /var/log/lurus-restore-drill.log 2>&1
```

If a drill fails, page on-call immediately — backups silently rotting is the
classic ops disaster.

---

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `wal-g backup-list` returns "S3 access denied" | bad `WALG_AWS_*` creds | re-issue MinIO/OSS access key, update `.env`, `docker compose up -d --force-recreate postgres` |
| PG won't start: "could not open file ... in archive" | missing WAL segment | check `WALG_S3_PREFIX` matches what produced the segment; if irrecoverable, restore to the last consistent point and accept data loss |
| `recovery.signal` ignored | PG ≥ 12 changed file location | confirm file is in `/var/lib/postgresql/data/`, not the OS-level data dir — wal-g fetches into PGDATA so this should be correct |
| `archive_command failed`, WAL piling up in `pg_wal/` | wal-g target unreachable | check S3 endpoint reachability from PG container: `docker exec lurus-postgres wal-g wal-push /tmp/dummy` returns errors with details |
| Container won't even start | corrupted PGDATA from interrupted restore | wipe volume and redo § A from scratch — the volume snapshot from Pre-flight is your fallback |

---

## References

- wal-g docs: <https://wal-g.readthedocs.io/PostgreSQL/>
- HA strategy ADR: `lurus/doc/decisions/2026-05-07-newhub-pg-ha.md`
- Drill script: `scripts/pg-restore-drill.sh`
- Compose config: `deploy/single-node/docker-compose.yml` (postgres service)
