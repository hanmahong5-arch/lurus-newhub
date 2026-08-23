-- 031_fund_event_idempotency_per_tenant.sql
-- Idempotent PG-only convergence (same shape as 025_tier2_per_tenant_uniques):
-- credit_pool_fund_events.event_id moves from a GLOBAL single-column UNIQUE to
-- a per-tenant composite UNIQUE (tenant_id, event_id), named
-- uk_credit_pool_fund_events_tenant_event_id.
--
-- WHY: FundPoolIdempotent's idempotency key is the platform BillingOutbox
-- event_id, which is globally unique for real BillingOutbox traffic — but the
-- SAME event_id space is also reused for hand-typed / smoke-test fund calls
-- (runbook keys like "smoke-1"). Under the legacy global UNIQUE, tenant B
-- funding with an event_id tenant A already used gets judged a replay: the
-- endpoint returns 200 success=true replayed=true, but tenant B's pool is
-- never credited and the reported new_balance is tenant A's — money paid,
-- zero quota landed, no error anywhere in the chain. Idempotency scope MUST
-- be per-tenant: replay protection within a tenant, independence across
-- tenants. See internal/adapter/repo/tenant_credit_pool.go FundPoolIdempotent
-- and internal/adapter/handler/credit_pool_fund_money_path_test.go
-- TestFundCreditPool_EventIDScopedPerTenant.
--
-- Per-tenant uniqueness is strictly WEAKER than the legacy global unique (any
-- data satisfying "no duplicate event_id anywhere" trivially satisfies "no
-- duplicate event_id within the same tenant"), so existing rows always
-- satisfy the new composite index — no dedup pass needed, same reasoning 025
-- used for users.username.
--
-- AutoMigrate note: entity.CreditPoolFundEvent now carries
-- uniqueIndex:uk_credit_pool_fund_events_tenant_event_id on both TenantID
-- (priority 1) and EventID (priority 2), and NO single-column unique tag on
-- EventID. AutoMigrate (which runs BEFORE the Runner on the boot lease
-- winner) creates the same-named composite index on fresh DBs — the IF NOT
-- EXISTS below then no-ops — and never re-adds the global unique this
-- migration drops. Struct tag and this file must move together (021 §2
-- convention: index names match across both creation paths).
--
-- EXECUTION CONTRACT (internal/pkg/migration/runner.go): this body runs in ONE
-- transaction; the Runner appends the schema_migrations record afterwards and
-- does NOT use ON CONFLICT on that record, so the body MUST be fully idempotent.
--
-- IDEMPOTENCY / SAFETY:
--   * to_regclass guard: on a runner-only database where credit_pool_fund_events
--     is absent (AutoMigrate did not run and 019/021 have not executed yet —
--     e.g. a Runner invoked with BaselineThrough past 021), skip with a
--     WARNING instead of failing; 021 (or AutoMigrate) is what creates the
--     table, not this migration.
--   * the legacy-unique DROP scans pg_index for single-column UNIQUE
--     indexes/constraints on credit_pool_fund_events(event_id) by SHAPE, not
--     by name — 019/021's SQL path names it uq_credit_pool_fund_events_event_id
--     but the pre-change GORM tag was a bare `uniqueIndex` (no explicit name),
--     so a database whose schema came solely from AutoMigrate may carry a
--     differently-named standalone unique index instead of that constraint.
--     Re-runs (or a DB that already converged) find nothing and no-op.
--   * CREATE UNIQUE INDEX IF NOT EXISTS → re-runs are no-ops.
--   * idx_credit_pool_fund_events_tenant (019/021, plain non-unique index on
--     tenant_id alone) is untouched — this migration only removes the
--     single-column UNIQUE, not the plain lookup index.
--
-- 2026-08-22 hardened: the drop-loop join now filters c.contype IN ('u','p')
-- (nulls from the LEFT JOIN — a standalone unique index with no owning
-- constraint — still pass through). pg_constraint.conindid is populated for
-- FOREIGN KEY constraints too (pointing at the REFERENCED table's supporting
-- index), so without this filter a future external FK referencing this
-- table's legacy unique index would join in under the SAME index, and the
-- loop would try `ALTER TABLE credit_pool_fund_events DROP CONSTRAINT
-- <fk_name>` — a constraint that belongs to the OTHER table, which fails.
-- No such FK exists today; this is fresh-install-only preventive hardening
-- (stage already applied the unfixed 031 — schema_migrations records only
-- version + applied_at, no checksum, so this in-place edit only changes what
-- a NEW install executes).
-- Residue semantics of that filter, stated honestly: when an external FK
-- pins the legacy unique, this migration now COMPLETES but leaves the
-- global UNIQUE(event_id) in place — per-tenant idempotency is then NOT in
-- effect. The post-loop RAISE WARNING below makes that residue visible in
-- the install log instead of masquerading as success; removing the pinning
-- dependency and re-running (idempotent) finishes the convergence.

DO $mig$
DECLARE
    legacy record;
BEGIN
    IF to_regclass('public.credit_pool_fund_events') IS NULL THEN
        RAISE WARNING '031_fund_event_idempotency_per_tenant: credit_pool_fund_events absent (021/AutoMigrate creates it); skipping';
        RETURN;
    END IF;

    -- Drop every single-column UNIQUE on credit_pool_fund_events(event_id),
    -- whether backed by a table constraint or a standalone unique index. The
    -- composite index created below has 2 key columns and never matches this
    -- filter, so re-runs after convergence are no-ops.
    FOR legacy IN
        SELECT i.indexrelid::regclass::text AS index_name,
               c.conname                    AS constraint_name
        FROM pg_index i
        LEFT JOIN pg_constraint c ON c.conindid = i.indexrelid
        WHERE i.indrelid = 'public.credit_pool_fund_events'::regclass
          AND i.indisunique
          AND i.indnkeyatts = 1
          AND (SELECT a.attname FROM pg_attribute a
               WHERE a.attrelid = i.indrelid AND a.attnum = i.indkey[0]) = 'event_id'
          AND (c.contype IS NULL OR c.contype IN ('u', 'p'))
    LOOP
        IF legacy.constraint_name IS NOT NULL THEN
            EXECUTE format('ALTER TABLE credit_pool_fund_events DROP CONSTRAINT %I', legacy.constraint_name);
        ELSE
            EXECUTE format('DROP INDEX %s', legacy.index_name);
        END IF;
    END LOOP;

    -- Fail LOUDLY on the residue the contype filter can leave behind: if an
    -- external dependency (e.g. a future FK from another table) pinned a
    -- legacy single-column unique so the loop had to leave it, the per-tenant
    -- idempotency this migration exists for is NOT in effect — cross-tenant
    -- event_id reuse still collides. A silent skip here would look exactly
    -- like success in the install log.
    FOR legacy IN
        SELECT i.indexrelid::regclass::text AS index_name
        FROM pg_index i
        WHERE i.indrelid = 'public.credit_pool_fund_events'::regclass
          AND i.indisunique
          AND i.indnkeyatts = 1
          AND (SELECT a.attname FROM pg_attribute a
               WHERE a.attrelid = i.indrelid AND a.attnum = i.indkey[0]) = 'event_id'
    LOOP
        RAISE WARNING '031: legacy single-column unique % survived (an external dependency pins it) — per-tenant fund idempotency is NOT in effect until it is removed manually', legacy.index_name;
    END LOOP;

    -- Per-tenant composite unique (same name the struct tags use, so
    -- AutoMigrate-first and Runner-first orderings converge on one index).
    EXECUTE 'CREATE UNIQUE INDEX IF NOT EXISTS uk_credit_pool_fund_events_tenant_event_id ON credit_pool_fund_events (tenant_id, event_id)';
END
$mig$;
