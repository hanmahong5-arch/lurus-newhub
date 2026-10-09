-- 046_tenant_roles.sql
-- Idempotent, additive PG-only migration: tenant-scoped roles, the
-- "wallet is authoritative" tenant switch, project membership, and the
-- role/project fields of tenant invites.
--
--   * users.tenant_role           '' | 'admin' | 'dept_lead'. Deliberately NOT
--                                 the integer users.role: a global role >= 10
--                                 opens the v1 AdminAuth routes (channels,
--                                 users, all logs) that a customer's admin
--                                 must never reach.
--   * tenants.wallet_authoritative  when true, a platform-pre-authorized
--                                 request is no longer refused by the local
--                                 user-balance check (logged only).
--   * project_members             (tenant, project, user) membership: the
--                                 subject a dept_lead's data scope hangs on.
--   * tenant_invites.member_role / project_id  granted on redemption.
--
-- IDEMPOTENCY / SAFETY (same pattern as 041/043):
--   * ADD COLUMN IF NOT EXISTS / CREATE ... IF NOT EXISTS are re-run-safe.
--   * ADD COLUMN ... NOT NULL DEFAULT <const> is metadata-only on PG 11+.
--   * Each block is guarded by to_regclass: on a fresh database GORM
--     AutoMigrate creates the table with the columns already present.
--   * Integers are BIGINT to match GORM's int64 / int mapping.

DO $mig$
BEGIN
    IF to_regclass('public.users') IS NULL THEN
        RAISE WARNING '046_tenant_roles: users absent (AutoMigrate creates it at boot); skipping users.tenant_role';
    ELSE
        ALTER TABLE users ADD COLUMN IF NOT EXISTS tenant_role VARCHAR(16) NOT NULL DEFAULT '';
    END IF;

    IF to_regclass('public.tenants') IS NULL THEN
        RAISE WARNING '046_tenant_roles: tenants absent (AutoMigrate creates it at boot); skipping tenants.wallet_authoritative';
    ELSE
        ALTER TABLE tenants ADD COLUMN IF NOT EXISTS wallet_authoritative BOOLEAN NOT NULL DEFAULT false;
    END IF;

    IF to_regclass('public.tenant_invites') IS NULL THEN
        RAISE WARNING '046_tenant_roles: tenant_invites absent (AutoMigrate creates it at boot); skipping invite columns';
    ELSE
        ALTER TABLE tenant_invites ADD COLUMN IF NOT EXISTS member_role VARCHAR(16) NOT NULL DEFAULT '';
        ALTER TABLE tenant_invites ADD COLUMN IF NOT EXISTS project_id BIGINT NOT NULL DEFAULT 0;
    END IF;
END
$mig$;

CREATE TABLE IF NOT EXISTS project_members (
    id         BIGSERIAL PRIMARY KEY,
    tenant_id  VARCHAR(36) NOT NULL,
    project_id BIGINT NOT NULL,
    user_id    BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    CONSTRAINT uk_project_members_tenant_project_user UNIQUE (tenant_id, project_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_project_members_tenant_user ON project_members (tenant_id, user_id);
