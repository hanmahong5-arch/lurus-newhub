-- newhub R6 STAGE first-boot seed: default tenant
-- ===================================================
-- 问题症状：deploy 后访问 /api/v2/lurus/auth/login 返回 404，pod log
-- "tenant.go:52 record not found ... slug = 'lurus'"。
-- 原因：GORM auto-migrate 建了 tenants 表但不 seed 数据；多租户系统
-- v2 路由匹配 tenant_slug，无 tenant = 整条 v2 链路 404。
--
-- 用法：
--   ssh root@100.122.83.20 "kubectl exec -n database lurus-pg-0 -- \
--     psql -U lurus -d newhub" < seed-default-tenant.sql
--
-- 幂等：ON CONFLICT DO NOTHING，重跑无副作用。
-- 后续：等 OIDC confidential client 注册到位时，需把 zitadel_org_id 列值
--       从占位值 (lurus-default-org) 改为真实 OIDC organization ID（物理列名 zitadel_org_id 暂不改，待 migration）。

INSERT INTO tenants (
  id, zitadel_org_id, slug, name,
  status, plan_type, max_users, max_quota,
  created_at, updated_at
) VALUES (
  'lurus-default',
  'lurus-default-org',  -- TODO: replace with real OIDC org ID after client registration
  'lurus',
  'Lurus',
  1,                    -- status: 1 = active
  'enterprise',
  1000,
  100000000000,         -- 1e11 quota — STAGE 默认上限，正式收费前不限制
  NOW(), NOW()
)
ON CONFLICT (slug) DO NOTHING;

-- Verify
SELECT id, slug, name, status, plan_type FROM tenants WHERE slug = 'lurus';
