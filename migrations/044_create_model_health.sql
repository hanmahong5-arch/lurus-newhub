-- 044_create_model_health.sql
-- Idempotent, additive PG-only migration: per-(channel, model) active probe
-- results (entity.ModelHealth, internal/app/modelprobe).
--
-- WHY: the channel-level test only exercises a channel's first/TestModel
-- model, so the other models of a multi-model channel (an OpenRouter free
-- pool carries ~20, and OpenRouter changes which are free week to week) were
-- never tested. The prober writes one row per (channel_id, model); after
-- consecutive failures it sets auto_disabled and the routing cache skips that
-- pair until a later probe succeeds.
--
-- IDEMPOTENCY / SAFETY:
--   * CREATE TABLE / INDEX IF NOT EXISTS are re-run-safe; GORM AutoMigrate
--     creates the same table and index name on a fresh database.
--   * BIGINT for every integer column matches GORM's int / int64 with
--     type:bigint (the 029/030 INTEGER-vs-bigint lesson).

CREATE TABLE IF NOT EXISTS model_health (
    id                   BIGSERIAL PRIMARY KEY,
    channel_id           BIGINT       NOT NULL,
    model                VARCHAR(255) NOT NULL,
    last_probe_at        BIGINT       NOT NULL DEFAULT 0,
    ok                   BOOLEAN      NOT NULL DEFAULT FALSE,
    latency_ms           BIGINT       NOT NULL DEFAULT 0,
    last_error           TEXT,
    consecutive_failures BIGINT       NOT NULL DEFAULT 0,
    auto_disabled        BOOLEAN      NOT NULL DEFAULT FALSE,
    auto_disabled_at     BIGINT       NOT NULL DEFAULT 0,
    updated_at           BIGINT       NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_model_health_channel_model
    ON model_health (channel_id, model);
