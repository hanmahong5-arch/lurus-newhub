-- 028_create_billing_checkout_orders.sql
--
-- Ownership record for v2 wallet-topup checkout orders. Before this table,
-- GET /api/v2/user/billing/checkout/:order_no/status forwarded order_no
-- straight to lurus-platform's checkout-status endpoint with no proof that the
-- order belonged to the caller — any authenticated user who knew or enumerated
-- another account's order_no could read its amount_cny/status/paid_at. The
-- platform endpoint is keyed only by order_no (it takes no account parameter),
-- so the ownership check has to live here: CreateBillingCheckout records the
-- (order_no -> account_id) pair, and the status poll rejects a mismatch.
--
-- PG-only, idempotent (021+ contract). Runs with the application's PG role.

CREATE TABLE IF NOT EXISTS billing_checkout_orders (
    order_no     VARCHAR(128) PRIMARY KEY,
    account_id   BIGINT       NOT NULL,
    amount_cny   DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at   BIGINT       NOT NULL DEFAULT 0
);

-- Poll and reconciliation both fan out from account_id.
CREATE INDEX IF NOT EXISTS idx_billing_checkout_orders_account
    ON billing_checkout_orders (account_id);
