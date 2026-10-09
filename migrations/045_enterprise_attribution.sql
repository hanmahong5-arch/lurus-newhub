-- 045_enterprise_attribution.sql
-- Idempotent, additive PG-only migration: per-employee / per-department
-- attribution for the enterprise gateway (docs/plans/enterprise-hub-2026-10-07.md
-- section 1).
--
--   tokens.trusted_identity_headers  BOOLEAN  - only a key with this flag set
--                                               may name the employee / department
--                                               through X-Lurus-Employee / X-Lurus-Dept
--   tokens.employee_ref              VARCHAR(64) - the employee a key was issued to
--   logs.employee_ref                VARCHAR(64) - copied onto every consume/error row
--   projects.external_code           VARCHAR(64) - the department code a customer's
--                                               gateway sends in X-Lurus-Dept
--
-- Two partial unique indexes keep the references unambiguous inside a tenant:
-- (tenant_id, employee_ref) on live tokens and (tenant_id, external_code) on
-- live projects, both only for non-empty values. The predicate is exactly why
-- they live here and not in a GORM tag (see the header of migration 026).
--
-- The logs index for the new filters is migration 047 (CREATE INDEX
-- CONCURRENTLY cannot run inside this file's transaction).
--
-- IDEMPOTENCY / SAFETY (same pattern as 041/043):
--   * ADD COLUMN IF NOT EXISTS / CREATE ... INDEX IF NOT EXISTS are re-run-safe.
--   * ADD COLUMN ... NOT NULL DEFAULT <constant> is metadata-only on
--     PostgreSQL 11+, so the logs ALTER does not rewrite the table.
--   * Each table is guarded by to_regclass: on a fresh database GORM
--     AutoMigrate creates the table with the columns already present.
--   * The unique indexes are created on columns that are all '' on first run,
--     so they cannot fail on pre-existing data.

DO $mig$
BEGIN
    IF to_regclass('public.tokens') IS NULL THEN
        RAISE WARNING '045_enterprise_attribution: tokens absent (AutoMigrate creates it at boot); skipping token columns';
    ELSE
        ALTER TABLE tokens ADD COLUMN IF NOT EXISTS trusted_identity_headers BOOLEAN NOT NULL DEFAULT false;
        ALTER TABLE tokens ADD COLUMN IF NOT EXISTS employee_ref VARCHAR(64) NOT NULL DEFAULT '';
        -- The predicate needs tokens.deleted_at; a bare legacy fixture without
        -- it skips the index (AutoMigrate'd real schemas always have it).
        IF EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema = 'public' AND table_name = 'tokens' AND column_name = 'deleted_at') THEN
            CREATE UNIQUE INDEX IF NOT EXISTS uk_tokens_tenant_employee_ref
                ON tokens (tenant_id, employee_ref)
                WHERE employee_ref <> '' AND deleted_at IS NULL;
        ELSE
            RAISE WARNING '045_enterprise_attribution: tokens.deleted_at absent; skipping uk_tokens_tenant_employee_ref';
        END IF;
    END IF;

    IF to_regclass('public.logs') IS NULL THEN
        RAISE WARNING '045_enterprise_attribution: logs absent (AutoMigrate creates it at boot); skipping logs.employee_ref';
    ELSE
        ALTER TABLE logs ADD COLUMN IF NOT EXISTS employee_ref VARCHAR(64) NOT NULL DEFAULT '';
    END IF;

    IF to_regclass('public.projects') IS NULL THEN
        RAISE WARNING '045_enterprise_attribution: projects absent (AutoMigrate creates it at boot); skipping projects.external_code';
    ELSE
        ALTER TABLE projects ADD COLUMN IF NOT EXISTS external_code VARCHAR(64) NOT NULL DEFAULT '';
        IF EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema = 'public' AND table_name = 'projects' AND column_name = 'deleted_at') THEN
            CREATE UNIQUE INDEX IF NOT EXISTS uk_projects_tenant_external_code
                ON projects (tenant_id, external_code)
                WHERE external_code <> '' AND deleted_at IS NULL;
        ELSE
            RAISE WARNING '045_enterprise_attribution: projects.deleted_at absent; skipping uk_projects_tenant_external_code';
        END IF;
    END IF;
END
$mig$;
