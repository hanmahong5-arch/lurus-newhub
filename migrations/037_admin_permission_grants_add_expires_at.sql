-- 037_admin_permission_grants_add_expires_at.sql
-- Additive-only, idempotent PG migration: adds admin_permission_grants.expires_at
-- (cycle-9 L1, auth-security-17/18 follow-up).
--
-- WHY: 034 shipped exactly one liveness predicate, revoked_at IS NULL — a
-- delegated grant was permanent until an admin explicitly revoked it. L2 of
-- this cycle hands out channel:sensitive_write through the same table, which
-- makes "permanent by default" a standing credential-substitution capability.
-- expires_at lets CreatePermissionGrant (repo/admin_permission_grant.go) mint
-- a time-bounded grant via an optional ttl_seconds on the write handler
-- (v2_admin_authz.go); a grant created without it is unaffected (expires_at
-- stays NULL, exactly today's forever-active shape).
--
-- THE TRAP THIS FILE DOES NOT WALK INTO: 034's partial unique index,
-- uk_admin_permission_grants_active ... WHERE revoked_at IS NULL, is NOT
-- reshaped here. An expired-but-unrevoked row still occupies that index's
-- slot — that is deliberate (re-shaping a partial unique index on a live
-- table is a second migration's worth of risk for a two-line schema change).
-- The re-grant-after-expiry path is a repo-layer behaviour change instead:
-- CreatePermissionGrant stamps revoked_at on an expired-but-unrevoked row and
-- inserts the new one inside the same transaction. This file only adds the
-- column the predicate and that repo logic both read.
--
-- BIGINT, NOT INT (same trap as 027 / 029 / 035): a nullable Go `*int64`
-- field (entity.AdminPermissionGrant.ExpiresAt), like RevokedAt on the same
-- struct, maps to postgres BIGINT under GORM, not INTEGER — declaring
-- INTEGER here would diverge from AutoMigrate and trigger a column rewrite
-- on the next boot.
--
-- EXECUTION CONTRACT (internal/pkg/migration/runner.go): this body runs in
-- ONE transaction; the Runner appends the schema_migrations record
-- afterwards without ON CONFLICT, so the body MUST be fully idempotent.
--
-- IDEMPOTENCY / SAFETY:
--   * ADD COLUMN IF NOT EXISTS is re-run-safe.
--   * ADD COLUMN ... (no NOT NULL, no DEFAULT) is a metadata-only operation
--     on PostgreSQL — it does not rewrite admin_permission_grants.
--   * Guarded by to_regclass so a database where AutoMigrate has not yet
--     created admin_permission_grants (a fresh boot ordering, or a
--     runner-only test database) warns and continues instead of failing
--     the whole runner.

DO $mig$
BEGIN
    IF to_regclass('public.admin_permission_grants') IS NULL THEN
        RAISE WARNING '037_admin_permission_grants_add_expires_at: admin_permission_grants absent (AutoMigrate creates it at boot); skipping expires_at';
    ELSE
        ALTER TABLE admin_permission_grants ADD COLUMN IF NOT EXISTS expires_at BIGINT;
    END IF;
END $mig$;
