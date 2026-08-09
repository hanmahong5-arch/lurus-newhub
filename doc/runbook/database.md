# Database Runbook

> PostgreSQL 15 · Host `100.94.177.10:30543` · DB `lurusapi` · DSN `postgres://lurus:<PG_PASSWORD,见 重要信息.md>@100.94.177.10:30543/lurusapi?sslmode=disable`

## Connection

```bash
psql "$DSN"                                                   # local (via Tailscale/VPN)
kubectl exec -n lurus-system deploy/lurus-api -- env | grep SQL_DSN   # from pod
```

App-level pool: `SQL_MAX_IDLE_CONNS` (100), `SQL_MAX_OPEN_CONNS` (1000), `SQL_MAX_LIFETIME` (60s).

Optional separate log DB: `LOG_SQL_DSN=postgres://...:30543/lurusapi_logs` (unset → logs share main DB).

## Backup / Restore

```bash
# Full / table-subset / schema-only backup (custom format)
pg_dump "$DSN" --format=custom --file=lurusapi_$(date +%Y%m%d_%H%M%S).dump
pg_dump "$DSN" --format=custom --table=users --table=tokens --table=channels --file=core.dump
pg_dump "$DSN" --schema-only --file=schema.sql

# Cron (DB host): nightly + 30-day retention
0 2 * * * pg_dump "$DSN" --format=custom --file=/backups/lurusapi_$(date +\%Y\%m\%d).dump \
  && find /backups -name "lurusapi_*.dump" -mtime +30 -delete

# Restore
pg_restore --dbname="$DSN" --clean --if-exists lurusapi_20260203.dump
pg_restore --dbname="$DSN" --table=users --clean --if-exists lurusapi_20260203.dump
psql "$DSN" < schema.sql
```

PITR requires WAL archiving — see `doc/runbook/pg-restore.md` (wal-g) / HA plan (CNPG).

## Migration

GORM `AutoMigrate` runs on **master node startup only** (`NODE_TYPE=master`): `InitDB() → if IsMasterNode → migrateDB()`. It creates missing tables + adds new columns; does NOT delete columns, change types, or drop tables.

AutoMigrate-managed tables (model → table): channels (Channel), tokens (Token), users (User), passkey_credentials, options, redemptions, abilities, logs, midjourneys, top_ups, quota_data, tasks, models, vendors, prefill_groups, setups, two_fas, two_fa_backup_codes, checkins, subscriptions, internal_api_keys, invitation_codes, tenants, user_identity_mappings (Zitadel→Lurus map), tenant_configs. Manual SQL migrations in `migrations/`.

Manual changes (renames/type changes/drops AutoMigrate can't do):

```sql
ALTER TABLE users RENAME COLUMN old_name TO new_name;
CREATE INDEX CONCURRENTLY idx_logs_tenant_id ON logs(tenant_id);   -- large tables: CONCURRENTLY
ALTER TABLE tokens ALTER COLUMN quota TYPE bigint;
```

Pre-migration: backup first · test on staging · check long queries (`SELECT * FROM pg_stat_activity WHERE state='active';`) · index on large table → `CONCURRENTLY`.

## Monitoring

```sql
SELECT count(*) FROM pg_stat_activity WHERE datname='lurusapi';                          -- connections
SELECT relname, pg_size_pretty(pg_total_relation_size(relid)) FROM pg_catalog.pg_statio_user_tables ORDER BY pg_total_relation_size(relid) DESC;  -- table sizes
SELECT query, calls, mean_exec_time, total_exec_time FROM pg_stat_statements ORDER BY mean_exec_time DESC LIMIT 20;  -- slow queries (needs pg_stat_statements)
SELECT relname, n_dead_tup, last_vacuum, last_autovacuum FROM pg_stat_user_tables ORDER BY n_dead_tup DESC;  -- vacuum status
```

## Disaster Recovery

- **DB unreachable**: check `systemctl status postgresql` on DB host → `telnet 100.94.177.10 30543` → check pool exhaustion ("too many connections" in app logs) → `kubectl rollout restart deployment/lurus-api -n lurus-system` to reset pool.
- **Data corruption**: scale app to 0 → restore latest backup → verify → scale up.
- **Schema drift** (AutoMigrate fails on startup): `kubectl logs -n lurus-system deploy/lurus-api | grep -i migration`, then connect and resolve conflicts.
