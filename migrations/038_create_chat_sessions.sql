-- 038_create_chat_sessions.sql
-- Idempotent PG-only creation of chat_sessions + chat_messages: a
-- client-driven, best-effort SAVE of a v2 console Chat conversation.
--
-- WHY: POST /api/v2/:tenant_slug/chat/send (handler.ChatSend,
-- internal/adapter/handler/v2_chat.go) already runs a real multi-turn
-- completion through in-process loopback to /v1/chat/completions — the v2
-- Chat page's banner claiming the whole page is "design-mock only" was
-- false and removed this cycle. What WAS true is narrower: there was no
-- server-side conversation store, so refreshing the page or opening it on
-- another device lost every conversation. These two tables are that store.
-- ChatSend itself is UNCHANGED by this migration and does not read or write
-- either table — a session row is the client saving a conversation ChatSend
-- already returned, not a server-side store ChatSend consults on the hot
-- path. See entity.ChatSession's doc comment for the full relationship.
--
-- SSE streaming is explicitly OUT OF SCOPE this cycle (loopback SSE would
-- need a second HTTP hop that re-multiplexes upstream chunks — see
-- ChatSend's doc comment) and these tables carry nothing streaming-related.
--
-- AutoMigrate note: entity.ChatSession/entity.ChatMessage ARE registered in
-- repo.migrateDB's model list (internal/adapter/repo/main.go), same
-- dual-creation pattern as 029/032/033/034/036 — this file's column types
-- match what GORM derives from those structs (see below), so AutoMigrate
-- finds nothing to alter when this migration has already run, and this
-- migration's IF NOT EXISTS body is a no-op when AutoMigrate created the
-- tables first (fresh Postgres, e.g. a DR restore that skips the Runner).
-- NOTE: the two entities are not (yet) added to
-- repo/tenant_plugin_registry_test.go's registeredModels/tenantColumnExempt
-- forcing function — that file is owned elsewhere; both tables carry a
-- tenant_id column and should be classified there (see that test's own
-- doc comment for what "classify" means).
--
-- EXECUTION CONTRACT (internal/pkg/migration/runner.go): this body runs in
-- ONE transaction; the Runner appends the schema_migrations record
-- afterwards and does NOT use ON CONFLICT on that record, so the body MUST
-- be fully idempotent.
--
-- IDEMPOTENCY / SAFETY:
--   * CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS -> re-runs are
--     no-ops.
--   * id BIGSERIAL PRIMARY KEY matches GORM's mapping for a Go `int` field
--     tagged primaryKey;autoIncrement (tenant_invites, migration 032, same
--     shape).
--   * tenant_id / user_id / session_id / seq are BIGINT/VARCHAR(36) NOT INT:
--     GORM maps a plain Go `int`/`int64` field to Postgres BIGINT, not
--     INTEGER; declaring INTEGER here would diverge from AutoMigrate and
--     trigger a column rewrite the day main.go registers these entities
--     (migration 027 lesson, restated in 032/034/036/037 — a chat table is
--     exactly the kind of thing that looks disposable enough to skip this
--     on, and that reasoning is how the 027 incident happened).
--   * No FOREIGN KEY from chat_messages.session_id to chat_sessions.id: this
--     schema's later migrations (021+) do not use FK constraints (002-005
--     did and were the exception, not the convention) — cross-row integrity
--     is enforced in the repo layer instead (repo.DeleteChatSessionOwned
--     deletes a session's messages in the same transaction as the session
--     row), matching response_registry / admin_permission_grants / projects.

CREATE TABLE IF NOT EXISTS chat_sessions (
    id         BIGSERIAL    PRIMARY KEY,
    tenant_id  VARCHAR(36)  NOT NULL,
    user_id    BIGINT       NOT NULL,
    title      VARCHAR(255) NOT NULL DEFAULT '',
    model      VARCHAR(128) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_chat_sessions_tenant_user
    ON chat_sessions (tenant_id, user_id);

CREATE TABLE IF NOT EXISTS chat_messages (
    id         BIGSERIAL    PRIMARY KEY,
    session_id BIGINT       NOT NULL,
    tenant_id  VARCHAR(36)  NOT NULL,
    user_id    BIGINT       NOT NULL,
    seq        BIGINT       NOT NULL,
    role       VARCHAR(16)  NOT NULL,
    content    TEXT         NOT NULL,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_chat_messages_session
    ON chat_messages (session_id, seq);
