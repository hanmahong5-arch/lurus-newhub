-- 025_tier2_per_tenant_uniques.sql
-- Idempotent PG-only TIER2 unique rework (explicitly deferred by 021):
-- users.username converges from a GLOBAL single-column UNIQUE to a per-tenant
-- composite UNIQUE (tenant_id, username), named uk_users_tenant_username.
--
-- SCOPE — deliberately users.username ONLY. The per-column audit (2026-07-09)
-- keeps every other unique as-is:
--   * users.access_token / users.lurus_account_id — credential / cross-tenant
--     binding keys resolved WITHOUT tenant context (repo.ValidateAccessToken;
--     repo.GetUserByLurusAccountID under WithoutTenantIsolation) → stay global.
--   * tokens.key / redemptions.key — relay bearer auth (GetTokenByKey) and
--     anonymous redeem (findRedemptionByKey / repo.Redeem) resolve by key
--     alone, no tenant in hand → stay global.
--   * tokens.name / redemptions.name — never unique by design: every user's
--     auto-created token is named 'default' (AutoCreateDefaultToken) and
--     redemption batches share one name across up to 100 codes, so NO new
--     unique is added there.
--
-- WHY: the OIDC signup path guarantees username uniqueness only within a
-- tenant (repo.ensureUniqueUsername counts on a tenant-scoped DB), so under
-- the legacy global unique a cross-tenant name collision surfaces as a
-- spurious SQLSTATE 23505 during login auto-provisioning. Auth itself is
-- OIDC-sub based (user_identity_mapping) — no login resolves a bare global
-- username — and the remaining bare-username lookups were tenant-scoped in
-- the same change (handler.findOrCreateSwitchEndUser, repo User.Insert
-- refetch). Per-tenant uniqueness is strictly WEAKER than the legacy global
-- unique, so existing data always satisfies the new index; no dedup needed.
--
-- AutoMigrate note: entity.User and the repo-local User struct now carry
-- uniqueIndex:uk_users_tenant_username (tenant_id,username) and NO single-
-- column unique tag. AutoMigrate (which runs BEFORE the Runner on the boot
-- lease winner) creates the same-named composite index on fresh DBs — the
-- IF NOT EXISTS below then no-ops — and never re-adds the global unique this
-- migration drops. Struct tags and this file must move together.
--
-- EXECUTION CONTRACT (internal/pkg/migration/runner.go): this body runs in ONE
-- transaction; the Runner appends the schema_migrations record afterwards and
-- does NOT use ON CONFLICT on that record, so the body MUST be fully idempotent.
--
-- IDEMPOTENCY / SAFETY:
--   * to_regclass guard (022/023/024 convention) + column-coverage guard: on
--     runner-only databases (tests, partial DR restores) where users is absent
--     or lacks username/tenant_id columns, skip with a WARNING instead of
--     failing (the converge-test fixture builds users with tenant_id only).
--   * the legacy-unique DROP scans pg_index for single-column UNIQUE
--     indexes/constraints on users(username) by SHAPE, not by name — GORM's
--     historical name differs across versions (users_username_key vs
--     uni_users_username), and hand-applied DBs may carry a standalone unique
--     index instead of a constraint. Re-runs find nothing and no-op.
--   * CREATE UNIQUE INDEX IF NOT EXISTS → re-runs are no-ops.

DO $mig$
DECLARE
    legacy record;
BEGIN
    IF to_regclass('public.users') IS NULL THEN
        RAISE WARNING '025_tier2_per_tenant_uniques: users absent (AutoMigrate creates it at boot); skipping';
        RETURN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema = 'public' AND table_name = 'users' AND column_name = 'username')
       OR NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema = 'public' AND table_name = 'users' AND column_name = 'tenant_id') THEN
        RAISE WARNING '025_tier2_per_tenant_uniques: users lacks username/tenant_id columns (partial schema); skipping';
        RETURN;
    END IF;

    -- §1 Drop every single-column UNIQUE on users(username), whether it is
    -- backed by a table constraint (ALTER ... DROP CONSTRAINT) or a standalone
    -- unique index (DROP INDEX). Shape-matched, name-agnostic; the composite
    -- index created in §2 has 2 key columns and never matches this filter.
    FOR legacy IN
        SELECT i.indexrelid::regclass::text AS index_name,
               c.conname                    AS constraint_name
        FROM pg_index i
        LEFT JOIN pg_constraint c ON c.conindid = i.indexrelid
        WHERE i.indrelid = 'public.users'::regclass
          AND i.indisunique
          AND i.indnkeyatts = 1
          AND (SELECT a.attname FROM pg_attribute a
               WHERE a.attrelid = i.indrelid AND a.attnum = i.indkey[0]) = 'username'
    LOOP
        IF legacy.constraint_name IS NOT NULL THEN
            EXECUTE format('ALTER TABLE users DROP CONSTRAINT %I', legacy.constraint_name);
        ELSE
            EXECUTE format('DROP INDEX %s', legacy.index_name);
        END IF;
    END LOOP;

    -- §2 Per-tenant composite unique (same name the struct tags use, so
    -- AutoMigrate-first and Runner-first orderings converge on one index).
    EXECUTE 'CREATE UNIQUE INDEX IF NOT EXISTS uk_users_tenant_username ON users (tenant_id, username)';
END
$mig$;
