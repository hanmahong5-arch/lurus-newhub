-- 048_tenant_payer_and_invites.sql
-- Idempotent, additive PG-only migration: tenant payer + invite revocation.
--
--   * tenants.payer_user_id     the tenant member that owns (and pays for) every
--                               admin-issued key; 0 = not set. Bulk key issuing
--                               refuses with 409 payer_not_set until it is set.
--   * tenant_invites.revoked_at Unix seconds when a tenant admin revoked the
--                               invite; 0 = never revoked.
--
-- Invites now default to a 72h expiry; that is application logic, existing
-- rows are untouched.
--
-- IDEMPOTENCY / SAFETY (same pattern as 046):
--   * ADD COLUMN IF NOT EXISTS is re-run-safe and metadata-only on PG 11+.
--   * Each block is guarded by to_regclass: on a fresh database GORM
--     AutoMigrate creates the table with the columns already present.
--   * Integers are BIGINT to match GORM's int64 mapping.

DO $mig$
BEGIN
    IF to_regclass('public.tenants') IS NULL THEN
        RAISE WARNING '048_tenant_payer_and_invites: tenants absent (AutoMigrate creates it at boot); skipping tenants.payer_user_id';
    ELSE
        ALTER TABLE tenants ADD COLUMN IF NOT EXISTS payer_user_id BIGINT NOT NULL DEFAULT 0;
    END IF;

    IF to_regclass('public.tenant_invites') IS NULL THEN
        RAISE WARNING '048_tenant_payer_and_invites: tenant_invites absent (AutoMigrate creates it at boot); skipping tenant_invites.revoked_at';
    ELSE
        ALTER TABLE tenant_invites ADD COLUMN IF NOT EXISTS revoked_at BIGINT NOT NULL DEFAULT 0;
    END IF;
END
$mig$;
