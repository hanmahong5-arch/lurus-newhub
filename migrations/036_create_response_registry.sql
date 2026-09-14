-- 036_create_response_registry.sql
-- Idempotent PG-only creation of response_registry: the row that pins a
-- POST /v1/responses id (OpenAI Responses API, store!=false) to the exact
-- channel + model that produced it, so a LATER GET/DELETE
-- /v1/responses/:response_id can be routed straight back to that channel
-- instead of going through weighted channel selection — an id minted by
-- vendor A is meaningless to vendor B.
--
-- WHY (tasks-plugins-12): before this, newhub had no state at all for the
-- Responses API's stateful surface — a client that created a response with
-- store:true (the OpenAI default) had no way to retrieve or delete it
-- through the gateway; only the vendor's own dashboard could. GET/DELETE
-- /v1/responses/:response_id (relay_responses_registry.go) look this row up,
-- then middleware.SetupContextForSelectedChannel pins the request to
-- ChannelId/UpstreamModel exactly as recorded here.
--
-- Row lifetime: written by relay.ResponsesHelper's post-postConsumeQuota
-- insert hook (best-effort — a registry failure must never fail the billed
-- response, see relay/responses_handler.go); read/deleted by the two new
-- handler routes; swept by lifecycle.StartResponseRegistrySweepWithContext
-- once expires_at has passed (RESPONSE_REGISTRY_TTL_DAYS, default 30).
--
-- AutoMigrate note: entity.ResponseRegistry is registered in repo.migrateDB
-- (mirrors 032/033's dual-creation pattern), so a normal boot creates this
-- table via GORM before the Runner ever runs. This migration is the SQL
-- counterpart for runner-only databases (tests, partial DR restores).
-- Whichever side runs first, the other is a no-op.
--
-- EXECUTION CONTRACT (internal/pkg/migration/runner.go): this body runs in
-- ONE transaction; the Runner appends the schema_migrations record
-- afterwards and does NOT use ON CONFLICT on that record, so the body MUST
-- be fully idempotent.
--
-- IDEMPOTENCY / SAFETY:
--   * CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS -> re-runs and
--     the AutoMigrate-first ordering are both no-ops.
--   * response_id is the vendor-minted id (OpenAI's "resp_..." strings) and
--     is the PRIMARY KEY directly — there is no separate surrogate id column,
--     because insert/get/delete key off it directly and a vendor id is
--     already globally unique per channel's namespace. The retention sweep
--     is the exception: it scans by expires_at (its own index) and only
--     reaches response_id to build the batched DELETE.
--   * user_id / token_id / channel_id / created_at / expires_at are BIGINT,
--     not INT: GORM maps a plain Go `int`/`int64` field to Postgres BIGINT,
--     not INTEGER; declaring INT here would diverge from AutoMigrate and
--     trigger a column rewrite on the next boot (migration 027 lesson).

CREATE TABLE IF NOT EXISTS response_registry (
    response_id     VARCHAR(128) PRIMARY KEY,
    tenant_id       VARCHAR(36)  NOT NULL,
    user_id         BIGINT       NOT NULL,
    token_id        BIGINT       NOT NULL,
    channel_id      BIGINT       NOT NULL,
    upstream_model  VARCHAR(128) NOT NULL,
    created_at      BIGINT       NOT NULL,
    expires_at      BIGINT       NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_response_registry_expires_at
    ON response_registry (expires_at);

CREATE INDEX IF NOT EXISTS idx_response_registry_tenant_user
    ON response_registry (tenant_id, user_id);
