-- 033_create_user_sessions.sql
-- Idempotent PG-only creation of user_sessions: the per-device session
-- registry behind SESSION_REGISTRY_ENABLED (default false; true in
-- deploy/k8s/r6-uat this cycle).
--
-- WHY: ListSessionsV2 (v2_sessions.go) returned exactly one synthetic row —
-- "there is no multi-device session store" — and RevokeCurrentSessionV2
-- could only clear the caller's OWN cookie. There was no way for a user to
-- see a stolen session on a second device, or kill it, without an engineer
-- deleting a Redis key by hand. This table is the substrate: one row per
-- gin-contrib/sessions session (keyed by session_key = the store's
-- session.ID(), same id the Redis session store keys as
-- "session_"+session.ID() — cmd/server/session_store.go), so a revoke can
-- both flag the row (defence in depth, checked in authHelper) and delete the
-- session's own Redis key (authoritative logout).
--
-- AutoMigrate note: entity.UserSession is registered in repo.migrateDB
-- (mirrors migration 032 + TenantInvite's pattern), so a normal boot creates
-- this table via GORM before the Runner ever runs. This migration is the SQL
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
--   * Index names match the GORM tags so the two creation paths converge on
--     identical schema (migration 026 lesson).
--   * user_id / id are BIGINT, not INT: GORM maps a plain Go `int` field to
--     Postgres BIGINT, not INTEGER; declaring INT here would diverge from
--     AutoMigrate and trigger a column rewrite on the next boot (migration
--     027 lesson, same reasoning as batch_size).

CREATE TABLE IF NOT EXISTS user_sessions (
    id             BIGSERIAL    PRIMARY KEY,
    session_key    VARCHAR(128) NOT NULL,
    user_id        BIGINT       NOT NULL,
    tenant_id      VARCHAR(36)  NOT NULL DEFAULT 'default',
    ip             VARCHAR(45)  NOT NULL DEFAULT '',
    user_agent     VARCHAR(255) NOT NULL DEFAULT '',
    auth_method    VARCHAR(32)  NOT NULL DEFAULT '',
    created_at     BIGINT       NOT NULL,
    last_seen_at   BIGINT       NOT NULL,
    revoked_at     BIGINT       NOT NULL DEFAULT 0,
    revoke_reason  VARCHAR(32)  NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_user_sessions_session_key
    ON user_sessions (session_key);

CREATE INDEX IF NOT EXISTS idx_user_sessions_user_revoked
    ON user_sessions (user_id, revoked_at);

CREATE INDEX IF NOT EXISTS idx_user_sessions_last_seen
    ON user_sessions (last_seen_at);
