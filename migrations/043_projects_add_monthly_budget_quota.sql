-- 043_projects_add_monthly_budget_quota.sql
-- Idempotent, additive PG-only migration: adds projects.monthly_budget_quota,
-- the per-project monthly spending cap in quota units (QuotaPerUnit quota =
-- $1, the same unit as tenants.max_quota and logs.quota). 0 = no cap.
--
-- WHY: projects have carried cost attribution since 029 (tenant → project →
-- token, spend report per project) but nothing enforced anything: a tenant
-- admin who wanted "Marketing may not spend more than $500 this month" had
-- only the tenant-wide max_quota. The entity header used to say a budget
-- column would be "dead configuration" until something enforced it; the
-- enforcement (app.enforceProjectBudget on the pre-consume path, 402
-- project_budget_exceeded with Retry-After = next month) lands in the same
-- change as this column.
--
-- IDEMPOTENCY / SAFETY (same pattern as 041/042):
--   * ADD COLUMN IF NOT EXISTS is re-run-safe.
--   * ADD COLUMN ... NOT NULL DEFAULT 0 is metadata-only on PostgreSQL 11+.
--   * No index: the column is read by primary key (GetProjectByID).
--   * Guarded by to_regclass: on a fresh database GORM AutoMigrate creates
--     the table with the column already present.
--
-- BIGINT matches GORM's int64 (entity.Project.MonthlyBudgetQuota, gorm
-- type:bigint), so the runner-first and AutoMigrate-first paths produce the
-- same column.

DO $mig$
BEGIN
    IF to_regclass('public.projects') IS NULL THEN
        RAISE WARNING '043_projects_add_monthly_budget_quota: projects absent (AutoMigrate creates it at boot); skipping projects.monthly_budget_quota';
    ELSE
        ALTER TABLE projects ADD COLUMN IF NOT EXISTS monthly_budget_quota BIGINT NOT NULL DEFAULT 0;
    END IF;
END
$mig$;
