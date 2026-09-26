-- 041_logs_add_charged_cny4.sql
-- Idempotent, additive PG-only migration: adds logs.charged_cny4, the amount
-- a request actually took from the customer's platform wallet, in 0.0001 CNY
-- (the wallet's numeric(14,4) unit), written at settlement by
-- governance.EnrichLogParams from RelayInfo.WalletChargeCNY4.
--
-- WHY: the charge was never stored. Every surface that showed money
-- re-derived it from logs.quota at the exchange rate current when it was
-- READ, so changing USDExchangeRate re-priced past invoices and the per-
-- request lookup (GET /v1/generation) reported a USD figure the customer was
-- never charged in. 0 means "not wallet-charged" (credit pool, local quota,
-- released pre-auth) or "written before this column": readers fall back to
-- the old derivation for those rows only.
--
-- IDEMPOTENCY / SAFETY (same pattern as 029's logs.project_id and 035):
--   * ADD COLUMN IF NOT EXISTS is re-run-safe.
--   * ADD COLUMN ... NOT NULL DEFAULT <constant> is metadata-only on
--     PostgreSQL 11+: no rewrite of the largest table in the schema.
--   * No index: every reader aggregates it alongside quota under the
--     existing (tenant_id, created_at) indexes.
--   * Guarded by to_regclass: logs may live in a separate database
--     (LOG_SQL_DSN), where LOG_DB's AutoMigrate adds the column instead.
--
-- BIGINT matches GORM's int64 (entity.Log.ChargedCNY4, gorm type:bigint), so
-- the runner-first and AutoMigrate-first paths produce the same column.

DO $mig$
BEGIN
    IF to_regclass('public.logs') IS NULL THEN
        RAISE WARNING '041_logs_add_charged_cny4: logs absent (AutoMigrate creates it at boot); skipping logs.charged_cny4';
    ELSE
        ALTER TABLE logs ADD COLUMN IF NOT EXISTS charged_cny4 BIGINT NOT NULL DEFAULT 0;
    END IF;
END
$mig$;
