-- 054_routing_policies.sql
-- Idempotent, additive PG-only migration: per-tenant decision-model routing
-- policies (_bmad-output/planning-artifacts/cycle22-tokenhub-09-parity-2026-10-10.md, ADR
-- doc/decisions/2026-05-09-cost-aware-routing.md: auto-routing is permanent
-- opt-in, so every policy is born disabled and no row means no behaviour).
--
--   routing_policies
--       one policy per (tenant_id, public_model). When enabled, a request for
--       public_model is evaluated once by evaluator_model (any decision-model
--       route the tenant can reach) and rewritten to one of candidates when
--       the evaluator's confidence reaches min_confidence; otherwise to
--       default_candidate. candidates is a JSON array of
--       {id, model, criteria} (1..32 entries, validated by the handler).
--
-- IDEMPOTENCY / SAFETY: CREATE TABLE / INDEX IF NOT EXISTS. BIGINT for every
-- integer column to match GORM int64 on PG. Defaults here, in the GORM tags
-- (entity.RoutingPolicy) and in the handler are identical.
--
-- NO foreign keys on purpose: tenants are soft-deleted and policies are
-- removed by the tenant erasure cascade, not by the database.

CREATE TABLE IF NOT EXISTS routing_policies (
    id                BIGSERIAL PRIMARY KEY,
    tenant_id         VARCHAR(36)      NOT NULL DEFAULT '',
    public_model      VARCHAR(128)     NOT NULL,
    strategy          VARCHAR(16)      NOT NULL DEFAULT 'decision',
    enabled           BOOLEAN          NOT NULL DEFAULT false,
    evaluator_model   VARCHAR(128)     NOT NULL DEFAULT '',
    instructions      TEXT             NOT NULL DEFAULT '',
    min_confidence    DOUBLE PRECISION NOT NULL DEFAULT 0.65,
    default_candidate VARCHAR(64)      NOT NULL DEFAULT '',
    candidates        JSONB            NOT NULL DEFAULT '[]',
    created_at        BIGINT           NOT NULL DEFAULT 0,
    updated_at        BIGINT           NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_routing_policies_tenant_model
    ON routing_policies (tenant_id, public_model);
