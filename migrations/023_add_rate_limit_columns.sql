-- 023_add_rate_limit_columns.sql
-- Idempotent PG-only addition of business rate-limit columns to tokens and
-- tenants: rpm_limit (requests per minute) and tpm_limit (LLM tokens per
-- minute), both BIGINT NOT NULL DEFAULT 0 where 0 = unlimited.
--
-- BIGINT, not INT: GORM maps the plain Go `int` fields (repo Token /
-- entity.Tenant) to postgres bigint, so declaring INT here would DIVERGE from
-- AutoMigrate. On the normal boot path AutoMigrate runs first (bigint) and this
-- ADD COLUMN is a no-op; but on a runner-first DB (DR restore) INT columns
-- would later be rewritten integer→bigint by AutoMigrate under an ACCESS
-- EXCLUSIVE lock — heavy on the large tokens table. BIGINT keeps both identical.
--
-- WHY: middleware.BusinessRateLimit adds per-token / per-tenant RPM sliding
-- windows on the relay chain (rate-limit.go only covers the ClientIP
-- dimension). The limits are data, not config — a Reseller sets them per
-- token/tenant — so they live on the rows the relay already resolves.
-- DEFAULT 0 keeps every existing row unthrottled: behavior is unchanged
-- until an operator sets a positive limit. TPM windows are fed by settled
-- usage recorded in PostConsumeQuota (see internal/app/business_tpm.go).
--
-- AutoMigrate note: both tables also gain these columns via GORM AutoMigrate
-- (repo.Tenant aliases entity.Tenant; the repo-local Token struct carries the
-- same fields/columns). This migration is the SQL counterpart so pre-existing
-- databases and DR restores that skip AutoMigrate converge on the same schema.
--
-- EXECUTION CONTRACT (internal/pkg/migration/runner.go): this body runs in ONE
-- transaction; the Runner appends the schema_migrations record afterwards and
-- does NOT use ON CONFLICT on that record, so the body MUST be fully idempotent.
--
-- IDEMPOTENCY / SAFETY:
--   * ADD COLUMN IF NOT EXISTS → re-runs are no-ops.
--   * to_regclass guards (022/024 convention): tokens/tenants are created by
--     GORM AutoMigrate, which runs before the Runner on the boot lease winner;
--     on runner-only databases (tests, partial DR restores) where a table is
--     absent this migration skips it with a WARNING instead of failing —
--     ADD COLUMN IF NOT EXISTS does not protect against a missing TABLE.
--   * Whichever side creates a column first, the other becomes a no-op
--     (identical type + default).

DO $mig$
BEGIN
    IF to_regclass('public.tokens') IS NULL THEN
        RAISE WARNING '023_add_rate_limit_columns: tokens absent (AutoMigrate creates it at boot); skipping';
    ELSE
        EXECUTE 'ALTER TABLE tokens ADD COLUMN IF NOT EXISTS rpm_limit BIGINT NOT NULL DEFAULT 0';
        EXECUTE 'ALTER TABLE tokens ADD COLUMN IF NOT EXISTS tpm_limit BIGINT NOT NULL DEFAULT 0';
    END IF;

    IF to_regclass('public.tenants') IS NULL THEN
        RAISE WARNING '023_add_rate_limit_columns: tenants absent (AutoMigrate creates it at boot); skipping';
    ELSE
        EXECUTE 'ALTER TABLE tenants ADD COLUMN IF NOT EXISTS rpm_limit BIGINT NOT NULL DEFAULT 0';
        EXECUTE 'ALTER TABLE tenants ADD COLUMN IF NOT EXISTS tpm_limit BIGINT NOT NULL DEFAULT 0';
    END IF;
END
$mig$;
