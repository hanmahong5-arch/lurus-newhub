-- 042_logs_add_priced_cny4.sql
-- Idempotent, additive PG-only migration: adds logs.priced_cny4, the CNY
-- value of the row's quota converted at the exchange rate current when the
-- row was RECORDED, in 0.0001 CNY (the same unit as charged_cny4), written
-- by governance.EnrichLogParams from params.Quota alone.
--
-- WHY: 041 froze the wallet charge, but only the wallet branch writes it.
-- Credit-pool spend, local-quota spend and every row older than 041 carry
-- charged_cny4 = 0, and the invoice still priced those at the rate current
-- when it was READ — in production that is most rows, so one edit to
-- USDExchangeRate rewrote every settled month, and a credit pool that was
-- topped up at one rate was invoiced at another. priced_cny4 is the
-- record-time price for every consume row regardless of who paid.
--
-- Unlike charged_cny4 it does NOT mean the wallet was debited: it is what
-- the quota was worth that day, not what moved. 0 means "written before this
-- column" (or a zero-quota row): readers fall back to today's derivation for
-- those rows only and say so (invoice estimated=true).
--
-- IDEMPOTENCY / SAFETY (same pattern as 041):
--   * ADD COLUMN IF NOT EXISTS is re-run-safe.
--   * ADD COLUMN ... NOT NULL DEFAULT <constant> is metadata-only on
--     PostgreSQL 11+: no rewrite of the largest table in the schema.
--   * No index: every reader aggregates it alongside quota under the
--     existing (tenant_id, created_at) indexes.
--   * Guarded by to_regclass: logs may live in a separate database
--     (LOG_SQL_DSN), where LOG_DB's AutoMigrate adds the column instead.
--
-- BIGINT matches GORM's int64 (entity.Log.PricedCNY4, gorm type:bigint), so
-- the runner-first and AutoMigrate-first paths produce the same column.

DO $mig$
BEGIN
    IF to_regclass('public.logs') IS NULL THEN
        RAISE WARNING '042_logs_add_priced_cny4: logs absent (AutoMigrate creates it at boot); skipping logs.priced_cny4';
    ELSE
        ALTER TABLE logs ADD COLUMN IF NOT EXISTS priced_cny4 BIGINT NOT NULL DEFAULT 0;
    END IF;
END
$mig$;
