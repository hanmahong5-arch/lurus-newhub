-- Seed: OpenAI-compatible Custom channel → in-cluster grok2api.
-- Idempotent on channels.name. Key MUST be replaced with a real grok2api
-- Client Key (g2a_…) before enabling traffic — the placeholder is disabled
-- (status=2) so a blind apply cannot bill or leak.
--
-- Usage:
--   ssh root@100.122.83.20 "kubectl exec -i -n database lurus-pg-0 -- \
--     psql -U lurus -d newhub" < deploy/k8s/r6-stage/seed-grok2api-channel.sql
-- Then: UPDATE channels SET key='g2a_…', status=1 WHERE name='Grok2API (pool)';
-- Sync channel cache (console refresh or wait SYNC_FREQUENCY).
--
-- ⚠️ base_url has NO trailing /v1 — newhub appends /v1/...
-- ⚠️ Type 8 = ChannelTypeCustom (OpenAI-compatible custom endpoint).
-- ⚠️ Pricing for each public model must exist (Admin → Pricing) or requests
--    fail with model_price_error. See doc/runbook/grok2api-upstream.md.

INSERT INTO channels (
  type, key, name, base_url, models, "group", status, weight, priority,
  created_time, test_time, channel_info, model_mapping
)
SELECT
  8,
  'g2a-REPLACE-ME',
  'Grok2API (pool)',
  'http://grok2api.lurus-grok2api.svc:8000',
  'grok-chat-fast,grok-chat-auto,grok-chat-expert,grok-4.5',
  'default',
  2,  -- disabled until key is real
  1,
  50,
  EXTRACT(EPOCH FROM NOW())::bigint,
  0,
  '{}',
  '{}'
WHERE NOT EXISTS (
  SELECT 1 FROM channels WHERE name = 'Grok2API (pool)'
);

SELECT id, type, name, base_url, models, status
FROM channels
WHERE name = 'Grok2API (pool)';
