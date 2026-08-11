-- 030_seed_switch_tenant_and_credit_pool.sql
--
-- 钱路激活硬阻断修复：让 `switch` 分销渠道的注资链真正能通。
-- Apply:  psql "$NEWHUB_DSN" -v ON_ERROR_STOP=1 -f migrations/030_seed_switch_tenant_and_credit_pool.sql
-- 账本:   doc/coord/migration-ledger.md · 2b-svc-newhub · 030
--
-- ── 病灶 ──────────────────────────────────────────────────────────────────
-- platform 的 migrations/119_seed_claude_code_plans.sql 把六档 Claude Code 套餐的
-- `newhub_pool_slug` 全部写死成 "switch"，而 platform 的
-- internal/module/creditpool.go:225 直接把该值拼进
--     POST /internal/v1/provisioning/tenants/<slug>/credit-pool/fund
-- 但 newhub 的全量 migration 里**从来没有 slug=switch 这个租户** ——
-- 001_create_tenants.sql:42 只 seed 过 `lurus`。（migrations 里那些 `lurus-switch`
-- 命中是 releases.product_id，不是租户。）
--
-- 结果：用户付了钱 → platform 侧 BillingOutbox 发注资 → newhub 返 404
-- TENANT_NOT_FOUND → 表现为「钱收了，额度没有」。
-- 侧证：newhub 生产 pod 近 30 天日志里 `credit-pool/fund` 零命中 —— 这条路径
-- 从未被真正走通过。
--
-- ── 为什么在 newhub 建租户，而不是在 platform 改 slug ────────────────────────
-- 119 已经上过生产，不能改。可选的另一条路是新增 platform migration 把六档的
-- slug UPDATE 成已存在的 `lurus`，当天就能通；但那会把 switch 分销渠道的用量
-- 并进 lurus 池，之后要拆分销账极其困难。而「switch 是一条独立的分销渠道」
-- 本来就应该有自己的池 —— 所以按语义正确的那条走。
--
-- ── 为什么两张表都要插 ─────────────────────────────────────────────────────
-- fund 端点（internal/adapter/handler/internal_credit_pool_fund.go）先
-- GetTenantBySlug 再 GetTenantCreditPool，**缺任何一个都返回 404**。
-- internal_credit_pool_fund_test.go:368-382 里就有「租户存在但没有池」→ 404
-- 的用例。只建租户不建池 = 换一个 404 而已。
--
-- 幂等：两条都是 ON CONFLICT DO NOTHING，重复执行安全（本仓 initContainer /
-- migration runner 每次启动会重放）。
--
-- ── 无显式 BEGIN;/COMMIT; ────────────────────────────────────────────────────
-- runner 已经把每个 migration 包在自己的事务里（internal/pkg/migration/runner.go:324
-- BeginTx → exec body → 记 schema_migrations → Commit），021–029 也一律不写。
-- 文件里再写一个 COMMIT; 会把 runner 的事务提前提交，让「执行」与「记账」不再原子。
--
-- ── 两个 DO 块的守卫 ────────────────────────────────────────────────────────
-- `tenants` / `tenant_credit_pools` 由 boot 时的 GORM AutoMigrate 建（AutoMigrate
-- 先于本 runner 跑，见 repo/main.go runBootMigrations），SQL 血统里 001–020 是
-- 只记账不执行的 baseline。所以只按 SQL 建起来的库（migration 包自己的 PG 测试）
-- 根本没有这两张表 —— 无守卫的 INSERT 会让整个 runner 报错。按 025 的既有惯例
-- to_regclass + information_schema 守卫后跳过（RAISE WARNING，不失败）。

-- ---------------------------------------------------------------------------
-- 1. switch 租户
-- ---------------------------------------------------------------------------
-- id 用裸 `switch`，与 022_converge_default_tenant_id 确立的「租户 id 用规范裸名」
-- 一致（那次事故正是 STAGE 被手种成 `lurus-default` 而代码用 `default`，导致
-- relay pool-miss 静默漏扣费）。别再引入第二种命名风格。
--
-- 🔴 zitadel_org_id 是 UNIQUE NOT NULL，这里先落占位符。
--    它必须在 IdP 里建好对应 Organization 之后替换成真实 org id，否则该租户的
--    OIDC 登录链路不可用。注资链**不**依赖它（只按 slug 查），所以占位符不阻塞
--    钱路，但也不要当成已完成。替换方式：
--        UPDATE tenants SET zitadel_org_id = '<real-org-id>' WHERE id = 'switch';
DO $mig$
BEGIN
    IF to_regclass('public.tenants') IS NULL THEN
        RAISE WARNING '030_seed_switch_tenant_and_credit_pool: tenants absent (AutoMigrate creates it at boot); skipping';
        RETURN;
    END IF;

    INSERT INTO tenants (
        id, zitadel_org_id, slug, name, status, plan_type, max_users, max_quota
    )
    VALUES (
        'switch',
        'SWITCH_ORG_ID_PLACEHOLDER',   -- 🔴 owner: 建好 IdP Organization 后替换
        'switch',
        'Lurus Switch (分销渠道)',
        1,                             -- enabled
        'enterprise',
        10000,
        1000000000
    )
    ON CONFLICT (id) DO NOTHING;
END
$mig$;

-- ---------------------------------------------------------------------------
-- 2. switch 的额度池
-- ---------------------------------------------------------------------------
-- reset_period = 'none' 是**关键**，不能用表默认值 'monthly'：
-- 这个池装的是用户已付费买断的额度，按月 reset 会把付过钱的额度清零。
-- 012 建的部分索引 idx_tenant_credit_pools_next_reset 带
-- `WHERE reset_period <> 'none'`，即 'none' 的池天然不进 reset cron 扫描。
--
-- max_balance = -1 表示无上限：注资额度由 platform 侧按实际订单控制，
-- 在 newhub 侧再加一层天花板只会制造 POOL_CEILING_EXCEEDED 这种难查的失败。
--
-- current_balance 从 0 起 —— 余额只应由真实注资事件写入，
-- 绝不在 migration 里预置任何非零余额（那等于凭空铸币）。
DO $mig$
BEGIN
    IF to_regclass('public.tenant_credit_pools') IS NULL THEN
        RAISE WARNING '030_seed_switch_tenant_and_credit_pool: tenant_credit_pools absent (AutoMigrate creates it at boot); skipping pool seed';
        RETURN;
    END IF;
    -- created_by_user_id exists only in the GORM struct (entity/tenant_credit_pool.go);
    -- no SQL migration in 001–029 adds it, so a database built from the SQL
    -- lineage alone (the migration package's own PG tests) does not have it.
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema = 'public'
                     AND table_name = 'tenant_credit_pools'
                     AND column_name = 'created_by_user_id') THEN
        RAISE WARNING '030_seed_switch_tenant_and_credit_pool: tenant_credit_pools lacks created_by_user_id (partial schema; AutoMigrate adds it at boot); skipping pool seed';
        RETURN;
    END IF;

    INSERT INTO tenant_credit_pools (
        tenant_id, created_by_user_id, current_balance, max_balance,
        reset_period, alert_threshold_pct
    )
    VALUES (
        'switch',
        1,        -- 系统/root 用户；该列无 FK，仅作审计标注
        0,        -- 只能由真实注资事件增长
        -1,       -- 无天花板
        'none',   -- 🔴 不可改成 monthly，见上
        80
    )
    ON CONFLICT (tenant_id) DO NOTHING;
END
$mig$;

-- ── 验收（apply 后跑）──────────────────────────────────────────────────────
--   SELECT id, slug, status FROM tenants WHERE slug = 'switch';
--   SELECT tenant_id, current_balance, max_balance, reset_period
--     FROM tenant_credit_pools WHERE tenant_id = 'switch';
-- 两条都要有行。之后从 platform 侧走一次真实下单，确认 fund 返 200 且
-- current_balance 增长；并复查 newhub pod 日志出现
-- "InternalFundCreditPool: funded"（此前 30 天该字符串零命中）。
