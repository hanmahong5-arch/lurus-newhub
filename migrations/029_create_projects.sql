-- 029_create_projects.sql
-- Idempotent PG-only creation of the cost-attribution Project layer:
--   * new table `projects` (one row per tenant-scoped cost label)
--   * `tokens.project_id`  — which project a key's spend belongs to
--   * `logs.project_id`    — the attribution stamped on every relay log row
-- Both columns are BIGINT NOT NULL DEFAULT 0, where 0 = unassigned
-- (entity.ProjectUnassigned). NULL is deliberately not used: 0 is the
-- repo-wide "missing int reference" convention (token_id, creator_user_id,
-- identity_account_id, rpm_limit), so per-project aggregates need no COALESCE
-- and always sum to the tenant total.
--
-- WHY: the schema can already group spend by tenant, user, token, model,
-- channel, group, source_product and time — but there is nothing BETWEEN
-- tenant and token, so "which department spent what this month" is
-- unanswerable except by guessing from token names. Attribution cannot be
-- backfilled: a log row written without a project_id is permanently
-- unattributable, which is why this tagging migration ships ahead of the
-- console that displays it.
--
-- SCOPE — showback only. There is deliberately NO budget/limit column here.
-- Budget ENFORCEMENT (chargeback) sits on the relay hot path and is a separate
-- change; an unused column that reads as a spending guard but enforces nothing
-- is exactly the dead configuration this repo's structural tests exist to
-- police. Likewise `projects` is a LABEL, not a permission boundary — this
-- codebase has no tenant-level role table and no per-project subject to gate
-- (see internal/domain/entity/project.go).
--
-- AutoMigrate note: entity.Project is registered in repo.migrateDB and both
-- Token structs / entity.Log carry the project_id field, so a NORMAL boot
-- creates all of this via GORM BEFORE the Runner executes — 029 is then a
-- pure no-op. It exists for runner-first databases (DR restores, hermetic test
-- databases) so both orderings converge on identical schema.
--
-- EXECUTION CONTRACT (internal/pkg/migration/runner.go, applyOne): this whole
-- file body is executed as ONE statement inside ONE transaction, and the
-- schema_migrations row is INSERTed in that same transaction right after the
-- body, WITHOUT ON CONFLICT. The body must therefore be fully idempotent —
-- there is no "already recorded" escape hatch that would keep a second
-- execution from touching the schema.
--
-- BIGINT, NOT INT (same trap as 023 / 026): Go's plain `int` maps to postgres
-- bigint under GORM. Declaring project_id as INT here would DIVERGE from
-- AutoMigrate, and on a runner-first database (DR restore) the next boot's
-- AutoMigrate would rewrite the column integer->bigint under an ACCESS
-- EXCLUSIVE lock — on `logs`, the largest table in the schema. BIGINT makes
-- both creation paths byte-identical.
--
-- LOG_DB SPLIT DATABASE — read this before touching the logs branch. When
-- LOG_SQL_DSN is set, `logs` lives in a SEPARATE database that this Runner
-- NEVER connects to: runBootMigrations builds the Runner from the main DSN's
-- *sql.DB only (internal/adapter/repo/main.go:257,275), while the log
-- database gets its schema exclusively from migrateLOGDB()'s AutoMigrate
-- (main.go:519-525). In that deployment the ALTER below finds no `logs` table
-- in the main database at all, and `logs.project_id` over there comes from
-- AutoMigrate alone. That is why the logs branch is guarded by to_regclass and
-- merely WARNs when the table is absent: failing would break every
-- split-log-database boot and every runner-only test database. The same guard
-- covers `tokens` for partial DR restores. (No prior migration in this repo
-- documents the LOG_DB split — this is the first one that has to care.)
--
-- NO INDEX ON logs.project_id, deliberately — and none is added later by a
-- struct tag either. GORM's `index:` tag emits a plain CREATE INDEX (never
-- CONCURRENTLY) during AutoMigrate; on the largest table in the schema that
-- holds a ShareLock for the duration and blocks every INSERT — including the
-- relay's own consume-log writes — and it runs inside
-- withPGAdvisoryLock(bootAutoMigrateLockID, migrateDB) (main.go:277) on EVERY
-- master-capable replica of EVERY rolling update (cf. the boot-migration
-- incident recorded at internal/pkg/migration/runner.go:238-247). Nor can this
-- file do it safely: CREATE INDEX CONCURRENTLY is structurally impossible in
-- this Runner because applyOne wraps each body in a transaction
-- (runner.go:324-336) and PostgreSQL rejects CIC inside one. If per-project
-- log queries ever need an index, it is an operational step run against a live
-- database with MIGRATIONS_AUTO_RUN=false — not a tag and not this file.
--
-- IDEMPOTENCY / SAFETY:
--   * CREATE TABLE / CREATE INDEX / ADD COLUMN all use IF NOT EXISTS → re-runs
--     and the AutoMigrate-first ordering are both clean no-ops.
--   * The partial unique index is created ONLY here, never by a GORM
--     uniqueIndex tag: GORM cannot express `WHERE deleted_at IS NULL`, so a
--     tag would create a DIFFERENT index under the SAME name depending on
--     which path ran first (the divergence documented in 026's header). The
--     partial predicate is what lets a soft-deleted "Marketing" coexist with a
--     freshly recreated one.
--   * The plain lookup indexes match the names GORM derives from the `index`
--     tags on Project.TenantId / Project.DeletedAt (idx_projects_tenant_id,
--     idx_projects_deleted_at) so the two paths converge on one index each.
--   * ADD COLUMN ... NOT NULL DEFAULT <constant> is a metadata-only operation
--     on PostgreSQL 11+ — it does not rewrite the logs table.

CREATE TABLE IF NOT EXISTS projects (
    id          BIGSERIAL    PRIMARY KEY,
    tenant_id   VARCHAR(36)  NOT NULL DEFAULT 'default',
    name        VARCHAR(128) NOT NULL,
    description VARCHAR(512) NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ,
    updated_at  TIMESTAMPTZ,
    deleted_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_projects_tenant_id  ON projects (tenant_id);
CREATE INDEX IF NOT EXISTS idx_projects_deleted_at ON projects (deleted_at);

-- Per-tenant name uniqueness among LIVE rows only: soft-deleted rows keep
-- their name (historical logs must still resolve it via Unscoped()) and must
-- not block recreating a project with the same name.
CREATE UNIQUE INDEX IF NOT EXISTS uk_projects_tenant_name
    ON projects (tenant_id, name)
    WHERE deleted_at IS NULL;

DO $mig$
BEGIN
    IF to_regclass('public.tokens') IS NULL THEN
        RAISE WARNING '029_create_projects: tokens absent (AutoMigrate creates it at boot); skipping tokens.project_id';
    ELSE
        ALTER TABLE tokens ADD COLUMN IF NOT EXISTS project_id BIGINT NOT NULL DEFAULT 0;
    END IF;

    -- See the LOG_DB SPLIT DATABASE note above: when LOG_SQL_DSN is set this
    -- database has no logs table and never will — warn and carry on rather
    -- than failing the boot.
    IF to_regclass('public.logs') IS NULL THEN
        RAISE WARNING '029_create_projects: logs absent in this database (separate LOG_SQL_DSN, or AutoMigrate creates it at boot); skipping logs.project_id';
    ELSE
        ALTER TABLE logs ADD COLUMN IF NOT EXISTS project_id BIGINT NOT NULL DEFAULT 0;
    END IF;
END
$mig$;
