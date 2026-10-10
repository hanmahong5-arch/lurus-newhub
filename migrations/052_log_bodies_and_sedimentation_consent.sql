-- 052_log_bodies_and_sedimentation_consent.sql
-- Idempotent, additive PG-only migration: opt-in archive of request/response
-- bodies behind an explicit per-tenant consent.
--
--   tenants.sedimentation_consent  BOOLEAN NOT NULL DEFAULT false
--       the tenant's consent to have prompt/response bodies archived. Off by
--       default: no body is ever written until a tenant admin turns it on.
--   log_bodies                     one row per archived request, addressed by
--       request_id (the same id logs.other carries). expires_at is stamped at
--       write time from LOG_BODY_RETENTION_DAYS and swept by a leader-only
--       lifecycle task; reads treat an expired row as absent.
--
-- IDEMPOTENCY: ADD COLUMN IF NOT EXISTS behind a to_regclass guard (a fresh
-- database gets the column from GORM AutoMigrate), CREATE TABLE / INDEX IF NOT
-- EXISTS. Integer columns are BIGINT to match GORM int64/int on PG. The
-- default here, in the GORM tag (entity.Tenant.SedimentationConsent) and in
-- the ORM is false.
--
-- NO foreign keys on purpose: the table is swept by age, and tenants / users /
-- tokens are soft-deleted; a hard FK would make either side's cleanup fail.

DO $mig$
BEGIN
    IF to_regclass('public.tenants') IS NULL THEN
        RAISE WARNING '052_log_bodies_and_sedimentation_consent: tenants absent (AutoMigrate creates it at boot); skipping sedimentation_consent';
    ELSE
        ALTER TABLE tenants ADD COLUMN IF NOT EXISTS sedimentation_consent BOOLEAN NOT NULL DEFAULT false;
    END IF;
END
$mig$;

CREATE TABLE IF NOT EXISTS log_bodies (
    id                BIGSERIAL PRIMARY KEY,
    request_id        VARCHAR(64)  NOT NULL,
    tenant_id         VARCHAR(36)  NOT NULL DEFAULT '',
    user_id           BIGINT       NOT NULL DEFAULT 0,
    token_id          BIGINT       NOT NULL DEFAULT 0,
    model             VARCHAR(255) NOT NULL DEFAULT '',
    created_at        BIGINT       NOT NULL DEFAULT 0,
    expires_at        BIGINT       NOT NULL,
    request_body      TEXT         NOT NULL DEFAULT '',
    response_text     TEXT         NOT NULL DEFAULT '',
    response_captured BOOLEAN      NOT NULL DEFAULT false,
    truncated         BOOLEAN      NOT NULL DEFAULT false
);

CREATE INDEX IF NOT EXISTS idx_log_bodies_tenant_created
    ON log_bodies (tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_log_bodies_request_id
    ON log_bodies (request_id);
CREATE INDEX IF NOT EXISTS idx_log_bodies_expires_at
    ON log_bodies (expires_at);
