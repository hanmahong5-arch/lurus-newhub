-- 034_create_admin_permission_grants.sql
-- Idempotent PG-only creation of admin_permission_grants: per-user,
-- per-resource/action delegated permission rows that let a non-root admin
-- reach a narrow root-gated surface (this cycle: audit:read) without
-- granting full root.
--
-- WHY: before this migration, adminRoute (api-v2-router.go) gated the whole
-- /admin subtree, including the audit GETs (events/actions/export/
-- chain-verify), with middleware.RootJWTAuth — a hard root-or-nothing check
-- with no granularity in between (auth-security-17/18, console-ux-36). A
-- compliance reader who only needed the audit feed had no way to get it
-- short of full root. This table backs middleware.RootOrGranted
-- (root_or_granted.go), which the router now uses on the audit routes
-- (moved to their own auditRoute group, api-v2-router.go) for exactly
-- that: role >= root always passes; otherwise a caller needs an ACTIVE row
-- naming (their own user_id, resource, action).
--
-- Grants are GLOBAL this cycle: tenant_id is accepted only as NULL
-- (O5, cycle-8 plan §8 L4 amendment) — the column exists so cycle 9's
-- tenant-scoped grants (e.g. channel sensitive_write, plan §5) need no
-- second migration, but nothing in this cycle ever writes a non-NULL value.
--
-- AutoMigrate note: entity.AdminPermissionGrant is registered in
-- repo.migrateDB WITHOUT a GORM unique tag — GORM cannot express a partial
-- (WHERE revoked_at IS NULL) unique index, and a full-table unique tag on
-- (user_id, tenant_id, resource, action) would conflict with the partial
-- index this file creates on every boot (the 031 lesson: struct tag and SQL
-- index shape must agree, or AutoMigrate and the Runner fight each other).
-- This migration is therefore the ONLY creator of the active-grant unique
-- index; AutoMigrate only creates the bare table + the plain user_id index.
--
-- EXECUTION CONTRACT (internal/pkg/migration/runner.go): this body runs in
-- ONE transaction; the Runner appends the schema_migrations record
-- afterwards and does NOT use ON CONFLICT on that record, so the body MUST
-- be fully idempotent.
--
-- IDEMPOTENCY / SAFETY:
--   * CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS -> re-runs and
--     the AutoMigrate-first ordering are both no-ops.
--   * user_id / granted_by / created_at / revoked_at are BIGINT, not INT:
--     GORM maps a plain Go `int`/`int64` field to Postgres BIGINT, not
--     INTEGER; declaring INT here would diverge from AutoMigrate and
--     trigger a column rewrite on the next boot (migration 027 lesson).
--   * COALESCE(tenant_id,'') in the partial unique index lets two grants
--     with the same (user_id, resource, action) and both NULL tenant_id
--     collide as intended (global grants are unique per user+resource+action)
--     while still leaving room for a future non-NULL tenant_id to coexist
--     with a NULL one for the same user+resource+action (tenant-scoped and
--     global grants are independent rows, cycle 9's concern, not this one's).

CREATE TABLE IF NOT EXISTS admin_permission_grants (
    id          BIGSERIAL    PRIMARY KEY,
    user_id     BIGINT       NOT NULL,
    tenant_id   VARCHAR(36),
    resource    VARCHAR(64)  NOT NULL,
    action      VARCHAR(32)  NOT NULL,
    granted_by  BIGINT       NOT NULL,
    created_at  BIGINT       NOT NULL,
    revoked_at  BIGINT
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_admin_permission_grants_active
    ON admin_permission_grants (user_id, COALESCE(tenant_id, ''), resource, action)
    WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_admin_permission_grants_user
    ON admin_permission_grants (user_id);
