-- ============================================================================
-- DEMO seed — point the strict tenant at a REAL locally-hosted inference
-- server, instead of the hand-written mock endpoint.
--
-- Why this file exists
-- --------------------
-- `seed-private-endpoint-type.sql` proves the *guard*: a type-57 channel
-- reaches a loopback endpoint, and a public-address channel is refused before
-- any connection opens. That proof is deterministic and needs no model weights,
-- which is exactly what makes it CI-able — but its "on-prem endpoint" is a mock
-- written by the same hand that wrote the guard. Same-source evidence can only
-- demonstrate the plumbing, never the product claim.
--
-- This file swaps in a third-party inference server that is already running on
-- the operator's own machine, holding real quantized weights on local disk. The
-- claim under test becomes the one a buyer actually cares about:
--
--     a real model answered a real prompt, and the bytes never left the host.
--
-- Parameters (psql -v, both optional)
-- -----------------------------------
--   engine_base_url  default http://127.0.0.1:11400
--   engine_model     default onprem-chat-8b
--
-- `engine_model` is the TENANT-FACING model name. Keeping it an alias of
-- whatever upstream build is installed is deliberate: the customer's model
-- catalogue should not churn when the operator swaps the underlying weights.
--
-- Apply (the base seed must have run first — this file depends on its tenant,
-- user and relay token):
--   docker exec -i newhub-pgdemo psql -U postgres -d newhub \
--     -v engine_base_url=http://127.0.0.1:11400 -v engine_model=onprem-chat-8b \
--     -f - < demo/private-inference/seed-real-engine.sql
-- ============================================================================

\if :{?engine_base_url}
\else
\set engine_base_url 'http://127.0.0.1:11400'
\endif

\if :{?engine_model}
\else
\set engine_model 'onprem-chat-8b'
\endif

-- 0) Fail loudly rather than silently seeding half a demo. This file is
--    additive to the base seed and useless without it.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM tenants WHERE slug = 'privacy-strict') THEN
    RAISE EXCEPTION 'tenant privacy-strict is missing — apply seed-private-endpoint-type.sql first';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM tokens WHERE key = 'privacystrict000000000000000000000000onprem02') THEN
    RAISE EXCEPTION 'relay token is missing — apply seed-private-endpoint-type.sql first';
  END IF;
END $$;

-- 1) The real-engine channel. Same type 57, same enforcement path, same tenant
--    as the mock-backed channel — only the address behind it differs. Nothing
--    here is code: a customer reaches this state through configuration alone.
INSERT INTO channels (type, key, name, base_url, models, "group", tenant_id, status, weight, priority, created_time, test_time, channel_info)
SELECT 57, 'sk-onprem-no-auth', 'Private Inference (strict · real local engine)',
       :'engine_base_url', :'engine_model', 'default', 'privacy-strict', 1, 0, 0,
       EXTRACT(EPOCH FROM NOW())::bigint, 0, '{}'
WHERE NOT EXISTS (SELECT 1 FROM channels WHERE name = 'Private Inference (strict · real local engine)');

-- Re-running with a different port or model name must CONVERGE, not accumulate
-- a second stale channel that the router might still pick.
UPDATE channels
   SET base_url = :'engine_base_url',
       models   = :'engine_model',
       status   = 1
 WHERE name = 'Private Inference (strict · real local engine)';

-- 2) Ability rows are what the router actually selects on. Drop the ones left
--    over from a previous model name, then add the current one.
DELETE FROM abilities a
 USING channels c
 WHERE a.channel_id = c.id
   AND c.name = 'Private Inference (strict · real local engine)'
   AND a.model <> :'engine_model';

INSERT INTO abilities ("group", model, channel_id, enabled, priority, weight)
SELECT 'default', :'engine_model', c.id, true, 0, 0
FROM channels c
WHERE c.name = 'Private Inference (strict · real local engine)'
  AND NOT EXISTS (
    SELECT 1 FROM abilities a
    WHERE a."group" = 'default' AND a.model = :'engine_model' AND a.channel_id = c.id
  );

-- 3) Show the resulting routing table for the tenant. The egress canary from
--    the base seed is expected to still be here: it is the negative control,
--    and it must remain refused now that a real model sits next to it.
SELECT c.id, c.type, c.name, c.base_url, c.models, c.status
FROM channels c
WHERE c.tenant_id = 'privacy-strict'
ORDER BY c.id;
