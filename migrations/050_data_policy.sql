-- 050_data_policy.sql
-- Idempotent, additive PG-only migration: relay data-processing control.
--
--   tenants.content_retention / tokens.content_retention
--       '' = inherit; full | metadata_only | none. Layers only tighten
--       (platform default -> tenant -> token, the strictest wins).
--   content_rules              ordered mask/reject rules applied to request
--                              bodies before they reach an upstream
--                              (scope platform|tenant, observe|enforce).
--   channel_override_templates reusable param/header override documents that
--                              platform staff apply in bulk to channels.
--   channel_template_applications
--                              which template (id + version) was last
--                              written into which channel.
--
-- IDEMPOTENCY: ADD COLUMN IF NOT EXISTS behind to_regclass guards (a fresh
-- database gets the columns from GORM AutoMigrate), CREATE TABLE / INDEX IF
-- NOT EXISTS. Integer columns are BIGINT to match GORM int64/int on PG.
-- Defaults here, in the GORM tags and in the ORM are all '' / 0 / false.

DO $mig$
BEGIN
    IF to_regclass('public.tenants') IS NULL THEN
        RAISE WARNING '050_data_policy: tenants absent (AutoMigrate creates it at boot); skipping content_retention';
    ELSE
        ALTER TABLE tenants ADD COLUMN IF NOT EXISTS content_retention VARCHAR(16) NOT NULL DEFAULT '';
    END IF;
    IF to_regclass('public.tokens') IS NULL THEN
        RAISE WARNING '050_data_policy: tokens absent (AutoMigrate creates it at boot); skipping content_retention';
    ELSE
        ALTER TABLE tokens ADD COLUMN IF NOT EXISTS content_retention VARCHAR(16) NOT NULL DEFAULT '';
    END IF;
END
$mig$;

CREATE TABLE IF NOT EXISTS content_rules (
    id           BIGSERIAL PRIMARY KEY,
    scope        VARCHAR(16)  NOT NULL DEFAULT 'tenant',
    tenant_id    VARCHAR(36)  NOT NULL DEFAULT '',
    ordinal      BIGINT       NOT NULL DEFAULT 0,
    name         VARCHAR(64)  NOT NULL DEFAULT '',
    role_scope   VARCHAR(16)  NOT NULL DEFAULT 'any',
    kind         VARCHAR(16)  NOT NULL DEFAULT 'mask',
    pattern_type VARCHAR(16)  NOT NULL DEFAULT 'builtin',
    builtin      VARCHAR(32)  NOT NULL DEFAULT '',
    pattern      VARCHAR(512) NOT NULL DEFAULT '',
    replacement  VARCHAR(64)  NOT NULL DEFAULT '',
    mode         VARCHAR(16)  NOT NULL DEFAULT 'observe',
    enabled      BOOLEAN      NOT NULL DEFAULT TRUE,
    created_by   BIGINT       NOT NULL DEFAULT 0,
    created_at   BIGINT       NOT NULL DEFAULT 0,
    updated_at   BIGINT       NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_content_rules_scope_tenant
    ON content_rules (scope, tenant_id, ordinal);

CREATE TABLE IF NOT EXISTS channel_override_templates (
    id              BIGSERIAL PRIMARY KEY,
    name            VARCHAR(64) NOT NULL DEFAULT '',
    description     VARCHAR(255) NOT NULL DEFAULT '',
    param_override  TEXT        NOT NULL DEFAULT '',
    header_override TEXT        NOT NULL DEFAULT '',
    version         BIGINT      NOT NULL DEFAULT 1,
    created_by      BIGINT      NOT NULL DEFAULT 0,
    created_at      BIGINT      NOT NULL DEFAULT 0,
    updated_at      BIGINT      NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_channel_override_templates_name
    ON channel_override_templates (name);

CREATE TABLE IF NOT EXISTS channel_template_applications (
    id               BIGSERIAL PRIMARY KEY,
    channel_id       BIGINT NOT NULL DEFAULT 0,
    template_id      BIGINT NOT NULL DEFAULT 0,
    template_version BIGINT NOT NULL DEFAULT 0,
    applied_by       BIGINT NOT NULL DEFAULT 0,
    applied_at       BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_channel_template_applications_channel
    ON channel_template_applications (channel_id);
