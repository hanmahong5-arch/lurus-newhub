-- 032_create_tenant_invites.sql
-- Idempotent PG-only creation of tenant_invites: root-issued, one-time
-- onboarding codes that bind a newly-auto-created zita-bridge user to a
-- specific B-end tenant instead of the "default" placeholder every bridge
-- login lands in today (N2, ledger recon 2026-09-01).
--
-- WHY: handler.autoCreateBridgedUser hard-codes TenantId "default" for
-- every first-time zita-bridge login; there was no self-service or
-- root-driven path to place a new customer's first user into their OWN
-- tenant. A tenant invite is a single-use code (same shape as a Redemption
-- key: common.GetUUID(), 32 hex chars) that repo.ConsumeTenantInvite
-- redeems atomically (SELECT ... FOR UPDATE, same lock pattern as Redeem —
-- redemption.go) inside handler.ZitaBootstrap's auto-create branch ONLY;
-- an existing user's tenant is never touched by an invite.
--
-- AutoMigrate note: entity.TenantInvite is registered in repo.migrateDB, so
-- normal boots create this table via GORM before the Runner executes. This
-- migration is the SQL counterpart so runner-only databases (tests, partial
-- DR restores) converge on the same schema. Whichever side runs first, the
-- other becomes a no-op.
--
-- EXECUTION CONTRACT (internal/pkg/migration/runner.go): this body runs in
-- ONE transaction; the Runner appends the schema_migrations record
-- afterwards and does NOT use ON CONFLICT on that record, so the body MUST
-- be fully idempotent.
--
-- IDEMPOTENCY / SAFETY:
--   * CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS -> re-runs and
--     the AutoMigrate-first ordering are both no-ops.
--   * Index names match the GORM tags (uk_tenant_invites_code /
--     idx_tenant_invites_tenant) so the two creation paths converge on
--     identical schema (migration 026 lesson).
--   * Every *_id / status column is BIGINT, not INT: GORM maps a plain Go
--     `int` field to Postgres BIGINT, not INTEGER; declaring INT here would
--     diverge from AutoMigrate and trigger a column rewrite on the next
--     boot (migration 027 lesson, same reasoning as batch_size).

CREATE TABLE IF NOT EXISTS tenant_invites (
    id                     BIGSERIAL    PRIMARY KEY,
    tenant_id              VARCHAR(36)  NOT NULL,
    code                   VARCHAR(32)  NOT NULL,
    status                 BIGINT       NOT NULL DEFAULT 1,
    expired_time           BIGINT       NOT NULL DEFAULT 0,
    created_by_user_id     BIGINT       NOT NULL,
    consumed_by_account_id BIGINT,
    consumed_at            TIMESTAMPTZ,
    created_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_tenant_invites_code
    ON tenant_invites (code);

CREATE INDEX IF NOT EXISTS idx_tenant_invites_tenant
    ON tenant_invites (tenant_id);
