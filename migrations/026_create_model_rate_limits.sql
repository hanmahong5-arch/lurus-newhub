-- 026_create_model_rate_limits.sql
-- Idempotent PG-only creation of model_rate_limits: one row per
-- (tenant_id, model) carrying rpm_limit / tpm_limit (BIGINT NOT NULL DEFAULT 0,
-- 0 = unlimited) — the model dimension of the business rate limiter.
--
-- WHY: migration 023 gave the token and tenant dimensions their limit
-- columns; the per-model dimension needs its own table because the limit is
-- keyed by a (tenant, model) pair, not by a row the relay already resolves.
-- Enforcement is middleware.BusinessModelRateLimit (after Distribute — the
-- requested model name only enters the gin context there); the TPM window is
-- fed by settled usage in PostConsumeQuota (internal/app/business_tpm.go).
-- Matching is by EXACT client-requested model name, no wildcards.
--
-- AutoMigrate note: entity.ModelRateLimit is registered in repo.migrateDB, so
-- normal boots create this table via GORM before the Runner executes. This
-- migration is the SQL counterpart so runner-only databases (tests, partial
-- DR restores) converge on the same schema. Whichever side runs first, the
-- other becomes a no-op.
--
-- EXECUTION CONTRACT (internal/pkg/migration/runner.go): this body runs in ONE
-- transaction; the Runner appends the schema_migrations record afterwards and
-- does NOT use ON CONFLICT on that record, so the body MUST be fully idempotent.
--
-- IDEMPOTENCY / SAFETY:
--   * CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS → re-runs and
--     the AutoMigrate-first ordering are both no-ops.
--   * Index names match the GORM uniqueIndex tag (uk_model_rate_limits_
--     tenant_model) so the two creation paths converge on identical schema.
--   * rpm_limit/tpm_limit are BIGINT, not INT: GORM maps a plain Go `int`
--     field (entity.ModelRateLimit) to postgres bigint, so declaring INT here
--     would DIVERGE from AutoMigrate — on a runner-first DB (DR restore) the
--     next boot's AutoMigrate would then rewrite the column (integer→bigint)
--     under an ACCESS EXCLUSIVE lock. BIGINT makes both paths identical.

CREATE TABLE IF NOT EXISTS model_rate_limits (
    id         BIGSERIAL PRIMARY KEY,
    tenant_id  VARCHAR(36)  NOT NULL DEFAULT 'default',
    model      VARCHAR(255) NOT NULL,
    rpm_limit  BIGINT       NOT NULL DEFAULT 0,
    tpm_limit  BIGINT       NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_model_rate_limits_tenant_model
    ON model_rate_limits (tenant_id, model);
