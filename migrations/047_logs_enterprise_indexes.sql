-- lurus:no-transaction
-- 047_logs_enterprise_indexes.sql
-- Indexes for the per-department and per-employee log reads introduced with
-- migration 045 (docs/plans/enterprise-hub-2026-10-07.md section 1):
--
--   idx_logs_tenant_project_created   (tenant_id, project_id, created_at DESC)
--   idx_logs_tenant_employee_created  (tenant_id, employee_ref, created_at DESC)
--
-- WHY: a department lead's log list / stat / export and the tenant monthly
-- statement all filter `tenant_id = ? AND project_id IN (...)` (or
-- `employee_ref = ?`) and bound by created_at. Without these the only usable
-- prefix is tenant_id, so a lead of a small department scans the whole
-- tenant's rows.
--
-- LOG_SQL_DSN: the runner only touches the MAIN database. A deployment that
-- points `logs` at a separate database must create both indexes by hand with
-- the statements below (neither R6 instance sets LOG_SQL_DSN).
--
-- EXECUTION CONTRACT: see 039_logs_tenant_created_index.sql and
-- internal/pkg/migration/runner.go's NoTransactionDirective. This file may
-- contain nothing but CREATE INDEX CONCURRENTLY IF NOT EXISTS statements; a
-- failed build leaves an INVALID index that IF NOT EXISTS then skips, repaired
-- per doc/runbook/database.md (DROP INDEX CONCURRENTLY, re-run by hand).
--
-- entity.Log deliberately carries NO `index:` tag for these: GORM would emit a
-- plain, blocking CREATE INDEX during AutoMigrate. This file owns them.
-- lurus:requires-table public.logs

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_logs_tenant_project_created ON logs (tenant_id, project_id, created_at DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_logs_tenant_employee_created ON logs (tenant_id, employee_ref, created_at DESC);
