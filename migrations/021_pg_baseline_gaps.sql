-- 021_pg_baseline_gaps.sql
-- Idempotent PG-only gap-fill for schema elements that GORM AutoMigrate does
-- NOT reproduce on a fresh PostgreSQL, but that baseline migrations 001-020
-- (bookkeeping-only, never executed by the Runner) would have created.
--
-- WHY: on a fresh PG (new deploy or DR restore) the Runner records 001-020 as
-- applied WITHOUT executing them; the schema then comes solely from AutoMigrate,
-- which cannot express seed data, multi-column UNIQUE constraints it has no tag
-- for, CHECK constraints, or tables that have no Go struct. This migration
-- closes that delta. See doc/newhub-vs-newapi-retirement-gap-2026-06-18.md.
--
-- SCOPE (TIER 1 — correctness / data integrity only):
--   §1 two tables absent from AutoMigrate (internal_api_key_tenants has no Go
--      struct; credit_pool_fund_events is now also added to the AutoMigrate
--      list — this is the belt-and-suspenders / DR path)
--   §2 additive composite UNIQUE indexes AutoMigrate has no struct tag for
--   §3 CHECK constraints (AutoMigrate never creates these)
--   §4 seed rows the tenant bootstrap needs (default tenant + its configs)
-- DEFERRED (not here): perf-only partial/composite indexes; updated_at triggers
--   (GORM sets updated_at in app callbacks); and the users/tokens/redemptions
--   per-tenant UNIQUE rework — that one requires coordinated struct-tag changes
--   (AutoMigrate re-adds the single-column global unique every boot otherwise)
--   plus a STAGE data-dedup audit, so it is tracked as a separate change.
--
-- EXECUTION CONTRACT (internal/pkg/migration/runner.go): this body runs in ONE
-- transaction; the Runner appends the schema_migrations record afterwards and
-- does NOT use ON CONFLICT on that record, so this body MUST be fully
-- idempotent. The existing STAGE DB already has 001-020 applied by hand, so
-- every statement below is a safe no-op there (IF NOT EXISTS / DO-block guard /
-- ON CONFLICT DO NOTHING). AutoMigrate runs BEFORE the Runner, so the tables
-- altered below already exist when this runs.

-- ============================================================
-- §1  TABLES MISSING FROM AutoMigrate
-- ============================================================

-- internal_api_key_tenants (migration 013): ScopeProvisioning tenant whitelist.
-- No Go struct exists; code accesses it via DB.Table("internal_api_key_tenants").
CREATE TABLE IF NOT EXISTS internal_api_key_tenants (
    api_key_id INTEGER     NOT NULL,
    tenant_id  VARCHAR(36) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (api_key_id, tenant_id)
);
CREATE INDEX IF NOT EXISTS idx_internal_api_key_tenants_tenant
    ON internal_api_key_tenants (tenant_id);

-- credit_pool_fund_events (migration 019): BillingOutbox supply idempotency.
-- entity.CreditPoolFundEvent now drives AutoMigrate (added in this change);
-- kept here IF NOT EXISTS for DR restores where AutoMigrate did not run.
CREATE TABLE IF NOT EXISTS credit_pool_fund_events (
    id          BIGSERIAL    PRIMARY KEY,
    event_id    VARCHAR(128) NOT NULL,
    tenant_id   VARCHAR(36)  NOT NULL,
    pool_id     BIGINT       NOT NULL,
    amount      BIGINT       NOT NULL,
    new_balance BIGINT       NOT NULL,
    source      VARCHAR(64)  NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_credit_pool_fund_events_event_id UNIQUE (event_id)
);
CREATE INDEX IF NOT EXISTS idx_credit_pool_fund_events_tenant
    ON credit_pool_fund_events (tenant_id);

-- ============================================================
-- §2  ADDITIVE COMPOSITE UNIQUE INDEXES
-- AutoMigrate has no struct tag for these column sets, so it creates no unique
-- here (there is no single-column unique to drop — unlike users/tokens/
-- redemptions, which are deferred). On STAGE the equivalent named constraints
-- already exist from 002/003/005; a distinct index name makes these a harmless
-- redundant no-op there.
-- ============================================================

CREATE UNIQUE INDEX IF NOT EXISTS uq_user_identity_mapping_zitadel_tenant
    ON user_identity_mapping (zitadel_user_id, tenant_id);

CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_configs_tenant_key
    ON tenant_configs (tenant_id, config_key);

CREATE UNIQUE INDEX IF NOT EXISTS uq_releases_product_version
    ON releases (product_id, version);

CREATE UNIQUE INDEX IF NOT EXISTS uq_release_artifacts_release_platform_arch
    ON release_artifacts (release_id, platform, arch);

-- ============================================================
-- §3  CHECK CONSTRAINTS
-- AutoMigrate never creates CHECKs. PostgreSQL has no ADD CONSTRAINT IF NOT
-- EXISTS for CHECK, so each is guarded by a column-coverage lookup. That lookup
-- also matches STAGE's inline CHECKs from 005 (whose auto-generated names
-- differ from ours), so this is a no-op there.
-- ============================================================

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
        JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
        WHERE c.conrelid = 'releases'::regclass AND c.contype = 'c' AND a.attname = 'release_type'
    ) THEN
        ALTER TABLE releases ADD CONSTRAINT chk_releases_release_type
            CHECK (release_type IN ('stable', 'beta', 'alpha'));
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
        JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
        WHERE c.conrelid = 'release_artifacts'::regclass AND c.contype = 'c' AND a.attname = 'platform'
    ) THEN
        ALTER TABLE release_artifacts ADD CONSTRAINT chk_release_artifacts_platform
            CHECK (platform IN ('windows', 'darwin', 'linux', 'android', 'ios'));
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
        JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
        WHERE c.conrelid = 'release_artifacts'::regclass AND c.contype = 'c' AND a.attname = 'arch'
    ) THEN
        ALTER TABLE release_artifacts ADD CONSTRAINT chk_release_artifacts_arch
            CHECK (arch IN ('x64', 'arm64', 'amd64', 'universal'));
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
        JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
        WHERE c.conrelid = 'download_logs'::regclass AND c.contype = 'c' AND a.attname = 'status'
    ) THEN
        ALTER TABLE download_logs ADD CONSTRAINT chk_download_logs_status
            CHECK (status IN ('initiated', 'completed', 'failed'));
    END IF;
END $$;

-- ============================================================
-- §4  SEED DATA (idempotent, existing-default-tenant aware)
-- created_at / updated_at are set explicitly because AutoMigrate-created
-- timestamp columns may be NOT NULL without a DB default. The tenant_configs
-- ON CONFLICT target is the unique index created in §2 (same transaction).
--
-- WHY NOT a plain INSERT ... ON CONFLICT (id): the default tenant may already
-- exist under a NON-canonical id. STAGE was hand-seeded as id='lurus-default',
-- slug='lurus'. INSERT id='default' does not collide on the (id) PK, so the row
-- proceeds and then trips uni_tenants_slug = UNIQUE (slug) → SQLSTATE 23505,
-- aborting the whole migration (this body runs in ONE tx) → boot FATAL loop.
-- So resolve any existing default tenant by canonical id OR the reserved
-- 'lurus' slug, create it only when truly absent, and seed configs against the
-- resolved id (a fresh PG gets 'default'; STAGE keeps 'lurus-default').
-- ============================================================

DO $$
DECLARE
    v_tenant_id text;
BEGIN
    SELECT id INTO v_tenant_id
        FROM tenants
        WHERE id = 'default' OR slug = 'lurus'
        LIMIT 1;

    IF v_tenant_id IS NULL THEN
        INSERT INTO tenants (id, zitadel_org_id, slug, name, status, plan_type, max_users, max_quota, created_at, updated_at)
        VALUES ('default', 'ZITADEL_DEFAULT_ORG_ID_PLACEHOLDER', 'lurus', 'Lurus Platform', 1, 'enterprise', 10000, 1000000000, NOW(), NOW());
        v_tenant_id := 'default';
    END IF;

    INSERT INTO tenant_configs (tenant_id, config_key, config_value, config_type, description, is_system, created_at, updated_at) VALUES
        (v_tenant_id, 'quota.new_user_quota',           '10000',   'int',    'Default quota for new users (in tokens)',  FALSE, NOW(), NOW()),
        (v_tenant_id, 'quota.max_user_quota',           '1000000', 'int',    'Maximum quota per user (in tokens)',       FALSE, NOW(), NOW()),
        (v_tenant_id, 'quota.quota_reset_enabled',      'false',   'bool',   'Enable monthly quota reset',               FALSE, NOW(), NOW()),
        (v_tenant_id, 'billing.currency',               'CNY',     'string', 'Default currency for billing',             FALSE, NOW(), NOW()),
        (v_tenant_id, 'billing.tax_rate',               '0.13',    'float',  'Tax rate (0.13 = 13%)',                    FALSE, NOW(), NOW()),
        (v_tenant_id, 'billing.min_topup_amount',       '1',       'int',    'Minimum top-up amount',                    FALSE, NOW(), NOW()),
        (v_tenant_id, 'features.enable_meilisearch',    'true',    'bool',   'Enable Meilisearch integration',           TRUE,  NOW(), NOW()),
        (v_tenant_id, 'features.enable_subscriptions',  'true',    'bool',   'Enable subscription system',               TRUE,  NOW(), NOW()),
        (v_tenant_id, 'features.enable_redemptions',    'true',    'bool',   'Enable redemption codes',                  TRUE,  NOW(), NOW()),
        (v_tenant_id, 'features.enable_oauth',          'true',    'bool',   'Enable OAuth login',                       TRUE,  NOW(), NOW()),
        (v_tenant_id, 'security.max_login_attempts',    '5',       'int',    'Maximum login attempts before lockout',    TRUE,  NOW(), NOW()),
        (v_tenant_id, 'security.session_timeout',       '86400',   'int',    'Session timeout in seconds (24 hours)',    TRUE,  NOW(), NOW()),
        (v_tenant_id, 'security.token_expiry',          '2592000', 'int',    'Access token expiry in seconds (30 days)', TRUE,  NOW(), NOW()),
        (v_tenant_id, 'rate_limit.requests_per_minute', '60',      'int',    'Maximum API requests per minute',          FALSE, NOW(), NOW()),
        (v_tenant_id, 'rate_limit.requests_per_day',    '10000',   'int',    'Maximum API requests per day',             FALSE, NOW(), NOW()),
        (v_tenant_id, 'notification.email_enabled',     'true',    'bool',   'Enable email notifications',               FALSE, NOW(), NOW())
    ON CONFLICT (tenant_id, config_key) DO NOTHING;
END $$;
