-- 055_create_switch_teampacks.sql
-- Idempotent PG-only creation of switch_teampacks: one signed team-asset
-- bundle ("teampack") per reseller tenant, served to white-label Switch
-- end users (GET /api/v2/:tenant_slug/switch/teampack{,.sig,/meta}) and
-- written by the root admin endpoint PUT/DELETE
-- /api/v2/admin/tenants/:id/switch-teampack
-- (handler.PutSwitchTeamPack / DeleteSwitchTeamPack).
--
-- WHY: a reseller signs a teampack.tar.gz with an ed25519 key held only on
-- the reseller's own machine and publishes it through the Hub; the white-
-- label terminal verifies the signature against a public key that ships
-- with the installer. The Hub is therefore only a dumb, size-bounded blob
-- store: it computes sha256/size, checks the signature SHAPE, and never
-- verifies the signature itself. One row per tenant (tenant_id is the
-- primary key); a re-publish replaces the row.
--
-- AutoMigrate note: entity.SwitchTeamPack IS registered in repo.migrateDB's
-- model list (internal/adapter/repo/main.go), same dual-creation pattern as
-- 036/038/040 - the column types below match what GORM derives from that
-- struct (Go int64 -> BIGINT, never INTEGER: migration 027 lesson; []byte
-- with type:bytea -> BYTEA; string with type:text -> TEXT), so AutoMigrate
-- finds nothing to alter when this migration already ran, and this body is
-- a no-op when AutoMigrate created the table first.
--
-- EXECUTION CONTRACT (internal/pkg/migration/runner.go): this body runs in
-- ONE transaction; the Runner appends the schema_migrations record
-- afterwards and does NOT use ON CONFLICT on that record, so the body MUST
-- be fully idempotent.
--
-- IDEMPOTENCY / SAFETY:
--   * CREATE TABLE IF NOT EXISTS -> re-runs are no-ops. New, empty table:
--     nothing to lock or backfill.
--   * No FOREIGN KEY to tenants: 021+ migrations do not use FK constraints;
--     integrity is enforced by the handler (it resolves the tenant first).
--   * Blob size is bounded by the handler (decoded <= 8 MiB), not here.

CREATE TABLE IF NOT EXISTS switch_teampacks (
    tenant_id  VARCHAR(36) PRIMARY KEY,
    version    VARCHAR(64) NOT NULL,
    sha256     VARCHAR(64) NOT NULL,
    size       BIGINT      NOT NULL,
    blob       BYTEA,
    sig        TEXT        NOT NULL,
    updated_at BIGINT      NOT NULL
);
