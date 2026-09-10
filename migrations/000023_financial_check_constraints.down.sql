-- Reverse of 000023: drop the financial CHECK constraints.

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE virtual_balances DROP CONSTRAINT IF EXISTS virtual_balances_reserved_nonnegative;
ALTER TABLE virtual_balances DROP CONSTRAINT IF EXISTS virtual_balances_available_nonnegative;
ALTER TABLE fills DROP CONSTRAINT IF EXISTS fills_fee_amount_nonnegative;
ALTER TABLE fills DROP CONSTRAINT IF EXISTS fills_qty_positive;
ALTER TABLE fills DROP CONSTRAINT IF EXISTS fills_price_positive;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_fee_amount_nonnegative;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_price_avg_nonnegative;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_price_requested_nonnegative;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_qty_filled_nonnegative;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_qty_requested_nonnegative;

COMMIT;
