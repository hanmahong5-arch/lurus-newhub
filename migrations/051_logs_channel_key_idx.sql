-- 051_logs_channel_key_idx.sql
-- Idempotent, additive PG-only migration: per-account (per-key) cost and
-- utilization (docs/plans/account-pool-ops-2026-10-09.md section 3).
--
--   logs.channel_key_idx  BIGINT NOT NULL DEFAULT -1
--       index of the upstream key a multi-key channel used for this request;
--       -1 = single-key channel, or a row written before this column existed.
--
-- NO index on purpose. The per-key and per-channel usage reads filter
-- `channel_id = ? AND created_at >= ?` and group by channel_key_idx, which the
-- existing single-column logs.channel_id index already serves. Any index on
-- this table must be a CREATE INDEX CONCURRENTLY migration (see 039/047).
--
-- IDEMPOTENCY / SAFETY:
--   * ADD COLUMN IF NOT EXISTS is re-run-safe.
--   * NOT NULL DEFAULT <constant> is metadata-only on PostgreSQL 11+, so the
--     ALTER does not rewrite the largest table in the schema.
--   * to_regclass guard: on a fresh database GORM AutoMigrate creates logs with
--     the column already present.
--   * BIGINT matches the GORM tag (type:bigint) on entity.Log.ChannelKeyIdx.

DO $mig$
BEGIN
    IF to_regclass('public.logs') IS NULL THEN
        RAISE WARNING '051_logs_channel_key_idx: logs absent (AutoMigrate creates it at boot); skipping';
    ELSE
        ALTER TABLE logs ADD COLUMN IF NOT EXISTS channel_key_idx BIGINT NOT NULL DEFAULT -1;
    END IF;
END
$mig$;
