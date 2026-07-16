-- 027_create_provisioned_redemption_batches.sql
-- Idempotent PG-only creation of provisioned_redemption_batches: one row per
-- distributor batch-issuance event received on
-- POST /internal/v1/provisioning/tenants/:slug/redemptions.
--
-- WHY: platform's distributor flow issues redemption codes in bulk over the
-- internal provisioning API. UNIQUE(event_id) is the idempotency guard — a
-- replayed POST returns the original batch (HTTP 200 + data.replayed=true)
-- without minting a second set of codes. The generated code list is stored
-- verbatim in `codes` (JSON array, TEXT) so replay readback never depends on
-- reverse-querying the redemptions table. Same ledger pattern as
-- credit_pool_fund_events (migration 019 / repo FundPoolIdempotent); see
-- internal/adapter/repo/provisioned_redemption.go.
--
-- AutoMigrate note: entity.ProvisionedRedemptionBatch is registered in
-- repo.migrateDB, so normal boots create this table via GORM before the Runner
-- executes. This migration is the SQL counterpart so runner-only databases
-- (tests, partial DR restores) converge on the same schema. Whichever side
-- runs first, the other becomes a no-op.
--
-- EXECUTION CONTRACT (internal/pkg/migration/runner.go): this body runs in ONE
-- transaction; the Runner appends the schema_migrations record afterwards and
-- does NOT use ON CONFLICT on that record, so the body MUST be fully idempotent.
--
-- IDEMPOTENCY / SAFETY:
--   * CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS → re-runs and
--     the AutoMigrate-first ordering are both no-ops.
--   * Index names match the GORM tags (uk_provisioned_redemption_batches_
--     event_id / idx_provisioned_redemption_batches_tenant) so the two
--     creation paths converge on identical schema (migration 026 lesson).
--   * batch_size is BIGINT, not INT: GORM maps a plain Go `int` field to
--     postgres bigint; declaring INT here would diverge from AutoMigrate and
--     trigger a column rewrite on the next boot (migration 026 lesson).

CREATE TABLE IF NOT EXISTS provisioned_redemption_batches (
    id             BIGSERIAL    PRIMARY KEY,
    event_id       VARCHAR(128) NOT NULL,
    tenant_id      VARCHAR(36)  NOT NULL,
    batch_size     BIGINT       NOT NULL,
    quota_per_code BIGINT       NOT NULL,
    source         VARCHAR(64)  NOT NULL,
    codes          TEXT         NOT NULL,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_provisioned_redemption_batches_event_id
    ON provisioned_redemption_batches (event_id);

CREATE INDEX IF NOT EXISTS idx_provisioned_redemption_batches_tenant
    ON provisioned_redemption_batches (tenant_id);
