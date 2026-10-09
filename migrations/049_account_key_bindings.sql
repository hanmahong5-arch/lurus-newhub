-- 049_account_key_bindings.sql
-- Idempotent, additive PG-only migration: platform-driven per-account,
-- per-product key issuance (internal API POST
-- /internal/v1/provisioning/accounts/:account_id/keys).
--
--   account_key_bindings   one live key per (identity_account_id, product);
--                          the partial unique index is the concurrency
--                          backstop for "two simultaneous creates -> one key".
--   tokens.source_product  product attribution default carried by the key, so
--                          relay traffic is attributed to the bound product
--                          even when the caller sends no X-Lurus-Product.
--
-- IDEMPOTENCY: CREATE TABLE / INDEX IF NOT EXISTS, ADD COLUMN IF NOT EXISTS,
-- to_regclass guard for tokens (a fresh database gets the column from GORM
-- AutoMigrate). Integer columns are BIGINT to match GORM int64/int on PG.

CREATE TABLE IF NOT EXISTS account_key_bindings (
    id                  BIGSERIAL PRIMARY KEY,
    identity_account_id BIGINT       NOT NULL,
    product             VARCHAR(32)  NOT NULL,
    token_id            BIGINT       NOT NULL,
    tenant_id           VARCHAR(36)  NOT NULL DEFAULT 'default',
    idempotency_key     VARCHAR(128) NOT NULL DEFAULT '',
    created_at          BIGINT       NOT NULL DEFAULT 0,
    deleted_at          TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_account_key_bindings_live
    ON account_key_bindings (identity_account_id, product)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_account_key_bindings_token
    ON account_key_bindings (token_id);

DO $mig$
BEGIN
    IF to_regclass('public.tokens') IS NULL THEN
        RAISE WARNING '049_account_key_bindings: tokens absent (AutoMigrate creates it at boot); skipping source_product';
    ELSE
        ALTER TABLE tokens ADD COLUMN IF NOT EXISTS source_product VARCHAR(32) NOT NULL DEFAULT '';
    END IF;
END
$mig$;
