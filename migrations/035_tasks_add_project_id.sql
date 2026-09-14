-- 035_tasks_add_project_id.sql
-- Idempotent, additive PG-only migration: adds tasks.project_id and
-- tasks.request_id ahead of the generic async-task surface
-- (POST/GET /v1/tasks/:platform, cycle-8 L8).
--
-- WHY: entity.Task/repo.Task gain ProjectId (cost-attribution, same
-- convention as tokens.project_id / logs.project_id from migration 029) and
-- RequestId (the gin request id live at submit time, for support lookups —
-- distinct from TaskID, which is the upstream vendor's own id). Both are
-- populated in repo.InitTask from the relay attribution and the live
-- request, not backfilled: every pre-migration row keeps project_id=0 and
-- request_id=''.
--
-- LEDGER NOTE (operator, 2026-09-13, binding — see the cycle-8 plan's
-- "Migration renumbering" section): development order runs L8 before L7, so
-- the root ledger swapped the two reservations — this file is 035
-- (tasks_add_project_id), and create_response_registry is 036, not the
-- other way around.
--
-- IDEMPOTENCY / SAFETY (mirrors 029_create_projects.sql's tokens/logs
-- ADD COLUMN pattern):
--   * ADD COLUMN IF NOT EXISTS / CREATE INDEX IF NOT EXISTS are re-run-safe.
--   * ADD COLUMN ... NOT NULL DEFAULT <constant> is a metadata-only
--     operation on PostgreSQL 11+ — it does not rewrite the tasks table.
--   * Guarded by to_regclass so a database where AutoMigrate has not yet
--     created "tasks" (a fresh boot, or LOG_SQL_DSN split away from this
--     database) warns and continues instead of failing the whole runner.
--
-- BIGINT, NOT INT (same trap as 023 / 026 / 029): Go's plain `int`
-- (entity.Task.ProjectId / repo.Task.ProjectId) maps to postgres bigint
-- under GORM. Declaring project_id as INTEGER here would diverge from
-- AutoMigrate, and on a runner-first database the next boot's AutoMigrate
-- would rewrite the column integer->bigint under an ACCESS EXCLUSIVE lock.
-- BIGINT makes both creation paths byte-identical.

DO $mig$
BEGIN
    IF to_regclass('public.tasks') IS NULL THEN
        RAISE WARNING '035_tasks_add_project_id: tasks absent (AutoMigrate creates it at boot); skipping tasks.project_id/request_id';
    ELSE
        ALTER TABLE tasks ADD COLUMN IF NOT EXISTS project_id BIGINT NOT NULL DEFAULT 0;
        ALTER TABLE tasks ADD COLUMN IF NOT EXISTS request_id VARCHAR(64) NOT NULL DEFAULT '';
        CREATE INDEX IF NOT EXISTS idx_tasks_request_id ON tasks (request_id);
    END IF;
END
$mig$;
