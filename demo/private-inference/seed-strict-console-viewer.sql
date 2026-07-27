-- ============================================================================
-- ADDITIVE seed — console-viewer account for the `privacy-strict` tenant
-- (the one using the first-class private-endpoint channel type, 57).
--
-- Sibling of seed-console-viewer.sql, which does the same for `privacy-demo`.
-- Kept as its own file for the same reason: the routing proof must stay a
-- role=1 (non-admin) tenant user driven by pure config, so the account that
-- VIEWS the console can never be the account that PROVES the routing.
--
-- Both the channel list and the private-routing status endpoint
-- (GET /api/v2/:tenant_slug/private-routing) are admin-gated
-- (middleware.AdminAuth() + requireTenantAdmin), so a viewer with role=10 in
-- the same tenant is required to render the panel.
--
-- Apply AFTER seed-private-endpoint-type.sql, against the same DB:
--   docker exec -i newhub-pgdemo psql -U postgres -d newhub \
--     -f demo/private-inference/seed-strict-console-viewer.sql
-- ============================================================================

-- Keep the proof user non-admin even if an earlier manual test promoted it —
-- the claim "a normal tenant user is routed on-prem by config alone" must be
-- true in the database, not only in a comment.
UPDATE users SET role = 1 WHERE id = 9200 AND tenant_id = 'privacy-strict' AND role <> 1;

-- The viewer account: role 10 = RoleAdminUser (internal/pkg/common/constants.go).
INSERT INTO users (id, tenant_id, username, role, status, quota, "group")
VALUES (9201, 'privacy-strict', 'privacy-strict-console-viewer', 10, 1, 0, 'default')
ON CONFLICT (id) DO UPDATE SET role = 10, tenant_id = 'privacy-strict', status = 1;

-- Verify
SELECT id, tenant_id, username, role, status FROM users WHERE tenant_id = 'privacy-strict' ORDER BY id;
