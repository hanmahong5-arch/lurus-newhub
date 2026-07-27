-- ============================================================================
-- DEMO seed — the FIRST-CLASS private-endpoint channel type (type = 57).
--
-- Additive: does not touch `seed-tenant-private-endpoint.sql` (which proves the
-- same routing using the generic ChannelTypeCustom=8 escape hatch and is frozen
-- as an artifact). This file seeds a second, independent tenant that uses the
-- dedicated `ChannelTypePrivateEndpoint` type — the one whose contract the code
-- ENFORCES rather than merely documents.
--
-- Two channels are seeded on purpose, because the claim has two halves:
--
--   A) `Private Inference (strict · intranet)` — base_url on the loopback.
--      A relay call for model `qwen2.5-7b-instruct-strict` must reach the mock
--      on-prem endpoint. Positive proof: the request lands inside the network.
--
--   B) `EGRESS CANARY (must never dispatch)` — base_url pointing at
--      https://api.openai.com, i.e. a channel that would leak prompts to a SaaS
--      provider. It is inserted DIRECTLY INTO THE DATABASE precisely because
--      that path bypasses the HTTP config validation in
--      handler.validateChannel — exactly how a bad row arrives in reality (a
--      seed script, a DBA, a restored backup, a migration). A relay call for
--      model `egress-canary-model` must be REFUSED at dispatch time by
--      openai.Adaptor.GetRequestURL, emitting no outbound request at all.
--      Negative proof: config validation is not the only line of defence.
--
-- If (B) ever succeeds in returning a completion, the "data never leaves your
-- network" claim is false and this demo must fail loudly.
--
-- Apply against the demo Postgres AFTER the backend has auto-migrated once:
--   psql "postgres://postgres:postgres@localhost:5435/newhub" \
--     -f demo/private-inference/seed-private-endpoint-type.sql
-- ============================================================================

-- 0) Demo convenience: self-use mode so the relay does not demand per-model
--    pricing (a real paid deployment would configure model prices instead).
INSERT INTO options (key, value) VALUES ('SelfUseModeEnabled', 'true')
ON CONFLICT (key) DO UPDATE SET value = 'true';

-- 1) A second customer tenant, independent of `privacy-demo`.
INSERT INTO tenants (id, zitadel_org_id, slug, name, status, plan_type, max_users, max_quota, created_at, updated_at)
VALUES ('privacy-strict', 'privacy-strict-org', 'privacy-strict', 'Privacy Strict Corp', 1, 'enterprise', 100, 100000000000, NOW(), NOW())
ON CONFLICT (slug) DO NOTHING;

-- 2) A NORMAL (non-admin, role=1) tenant user — this is tenant configuration,
--    not an admin channel override.
INSERT INTO users (id, tenant_id, username, role, status, quota, "group")
VALUES (9200, 'privacy-strict', 'privacy-strict-user', 1, 1, 100000000, 'default')
ON CONFLICT (id) DO NOTHING;

-- 3) A relay token for that user. 45 chars, NO dashes (a dash is parsed as an
--    admin "specify channel id" suffix). group='' inherits the user's group.
INSERT INTO tokens (user_id, tenant_id, key, status, name, "group", unlimited_quota, remain_quota, expired_time, created_time, accessed_time)
SELECT 9200, 'privacy-strict', 'privacystrict000000000000000000000000onprem02', 1, 'privacy-strict relay token', '', true, 0, -1,
       EXTRACT(EPOCH FROM NOW())::bigint, EXTRACT(EPOCH FROM NOW())::bigint
WHERE NOT EXISTS (SELECT 1 FROM tokens WHERE key = 'privacystrict000000000000000000000000onprem02');

-- 4A) The intranet channel. type 57 = ChannelTypePrivateEndpoint
--     (internal/pkg/constant/channel.go). Pure config — no code change.
INSERT INTO channels (type, key, name, base_url, models, "group", tenant_id, status, weight, priority, created_time, test_time, channel_info)
SELECT 57, 'sk-onprem-no-auth', 'Private Inference (strict · intranet)',
       'http://127.0.0.1:11434', 'qwen2.5-7b-instruct-strict', 'default', 'privacy-strict', 1, 0, 0,
       EXTRACT(EPOCH FROM NOW())::bigint, 0, '{}'
WHERE NOT EXISTS (SELECT 1 FROM channels WHERE name = 'Private Inference (strict · intranet)');

-- 4B) The egress canary. Same type 57, but a PUBLIC base_url. Inserted straight
--     into the DB to bypass config validation — the dispatch-time guard is what
--     must stop it.
INSERT INTO channels (type, key, name, base_url, models, "group", tenant_id, status, weight, priority, created_time, test_time, channel_info)
SELECT 57, 'sk-should-never-be-used', 'EGRESS CANARY (must never dispatch)',
       'https://api.openai.com', 'egress-canary-model', 'default', 'privacy-strict', 1, 0, 0,
       EXTRACT(EPOCH FROM NOW())::bigint, 0, '{}'
WHERE NOT EXISTS (SELECT 1 FROM channels WHERE name = 'EGRESS CANARY (must never dispatch)');

-- 5) Ability rows the relay selects on: (group, model, channel) enabled.
INSERT INTO abilities ("group", model, channel_id, enabled, priority, weight)
SELECT 'default', 'qwen2.5-7b-instruct-strict', c.id, true, 0, 0
FROM channels c
WHERE c.name = 'Private Inference (strict · intranet)'
  AND NOT EXISTS (
    SELECT 1 FROM abilities a
    WHERE a."group" = 'default' AND a.model = 'qwen2.5-7b-instruct-strict' AND a.channel_id = c.id
  );

INSERT INTO abilities ("group", model, channel_id, enabled, priority, weight)
SELECT 'default', 'egress-canary-model', c.id, true, 0, 0
FROM channels c
WHERE c.name = 'EGRESS CANARY (must never dispatch)'
  AND NOT EXISTS (
    SELECT 1 FROM abilities a
    WHERE a."group" = 'default' AND a.model = 'egress-canary-model' AND a.channel_id = c.id
  );

-- Verify: both channels present, typed 57, one intranet + one public canary.
SELECT 'channel' AS kind, c.id::text AS id, c.type::text AS type, c.name, c.base_url, c.tenant_id
FROM channels c
WHERE c.tenant_id = 'privacy-strict'
UNION ALL
SELECT 'ability', a.channel_id::text, a.enabled::text, a."group", a.model, ''
FROM abilities a
WHERE a.model IN ('qwen2.5-7b-instruct-strict', 'egress-canary-model')
UNION ALL
SELECT 'token', t.id::text, t.status::text, t.name, t."group", t.tenant_id
FROM tokens t WHERE t.tenant_id = 'privacy-strict'
ORDER BY kind, id;
