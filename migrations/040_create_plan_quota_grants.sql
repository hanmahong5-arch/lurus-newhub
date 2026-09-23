-- 040_create_plan_quota_grants.sql
-- Idempotent PG-only creation of plan_quota_grants: the idempotency ledger
-- for POST /api/v2/:tenant_slug/plan-grant (handler.PlanGrantV2,
-- internal/adapter/handler/v2_plan_grant.go).
--
-- WHY: a Claude-Code-class (cc_*) plan purchase funds the tenant credit
-- pool, but relay pre-consume (internal/app/pre_consume_quota.go) also gates
-- on the buyer's OWN users.quota, which provisioning leaves at the welcome
-- grant — so a paying buyer 402'd after a few requests. PlanGrantV2 credits
-- the plan amount onto users.quota once per subscription period; one row
-- here = one applied grant. grant_key = "<subscription_id>:<subscription_
-- expires_at>" from the platform entitlement, so a renewal (new expires_at)
-- grants again while a replay of the same period hits the unique index.
--
-- AutoMigrate note: entity.PlanQuotaGrant IS registered in repo.migrateDB's
-- model list (internal/adapter/repo/main.go), same dual-creation pattern as
-- 036/038 — the column types below match what GORM derives from that struct
-- (Go int/int64 -> BIGINT, never INTEGER: migration 027 lesson), and the
-- unique index name matches its uniqueIndex tag, so AutoMigrate finds
-- nothing to alter when this migration already ran, and this body is a
-- no-op when AutoMigrate created the table first.
--
-- EXECUTION CONTRACT (internal/pkg/migration/runner.go): this body runs in
-- ONE transaction; the Runner appends the schema_migrations record
-- afterwards and does NOT use ON CONFLICT on that record, so the body MUST
-- be fully idempotent.
--
-- IDEMPOTENCY / SAFETY:
--   * CREATE TABLE IF NOT EXISTS / CREATE UNIQUE INDEX IF NOT EXISTS ->
--     re-runs are no-ops. New, empty table: the unique index builds
--     instantly, no CONCURRENTLY needed.
--   * No FOREIGN KEY to users/tenants: 021+ migrations do not use FK
--     constraints; integrity is enforced by the handler.

CREATE TABLE IF NOT EXISTS plan_quota_grants (
    id         BIGSERIAL    PRIMARY KEY,
    tenant_id  VARCHAR(36)  NOT NULL,
    account_id BIGINT       NOT NULL,
    user_id    BIGINT       NOT NULL,
    grant_key  VARCHAR(128) NOT NULL,
    plan_code  VARCHAR(64)  NOT NULL,
    amount     BIGINT       NOT NULL,
    created_at BIGINT       NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_plan_quota_grants_tenant_key
    ON plan_quota_grants (tenant_id, grant_key);
