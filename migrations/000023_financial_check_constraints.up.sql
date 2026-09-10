-- CHECK constraints on NUMERIC financial columns
-- (docs/audit/database-audit.md D10).
--
-- Quantities, prices and fees crossed the driver as unconstrained
-- NUMERIC: a sign error anywhere in the pipeline (a negative fill qty,
-- an available balance below zero) would persist silently and distort
-- every downstream figure. The columns where negativity is never valid
-- are now refused at the table; PnL and slippage columns stay
-- unconstrained by design — they are measurements that legitimately go
-- both ways.
--
-- Bounds, and why each is safe for every row the writers produce:
--   * orders.qty_requested / qty_filled / price_requested / price_avg /
--     fee_amount >= 0 — these cross as plain decimals, so unfilled or
--     rejected legs persist their zero value (a market order carries no
--     limit price; a dust-free leg carries no fee); zero is legitimate,
--     negative never is.
--   * fills.price / qty > 0 — a fills row exists only for an actual
--     execution; the book guards (a crossed or non-positive top of book
--     is CORRUPTED, never quoted) make non-positive fill values a
--     pipeline defect, and the constraint makes it visible instead of
--     persisted.
--   * fills.fee_amount >= 0 — rebates exist as a concept elsewhere; the
--     simulator never issues them, but the looser bound keeps the
--     constraint about corruption, not fee policy.
--   * virtual_balances.available / reserved >= 0 — the paper engine
--     reserves before executing and checks the ledger invariants after
--     every settlement; a negative balance means an invariant already
--     failed, and refusing the write surfaces it (counted by the outbox
--     refusal counter, never silently dropped).
--
-- NULL passes every CHECK below; every nullable column (price_requested,
-- price_avg, fee_amount) stays nullable for legs that carry no value.
--
-- Adding a validated CHECK constraint scans the table under ACCESS
-- EXCLUSIVE — instant on the empty/small tables every environment has
-- migrated so far (compose runs `migrate` before arbd starts); on an
-- established production table use ADD CONSTRAINT ... NOT VALID plus a
-- separate VALIDATE CONSTRAINT in a maintenance window instead.
-- The DO blocks keep the migration re-runnable under the psql path CI
-- uses (ADD CONSTRAINT has no IF NOT EXISTS form).

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'orders_qty_requested_nonnegative') THEN
        ALTER TABLE orders ADD CONSTRAINT orders_qty_requested_nonnegative CHECK (qty_requested >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'orders_qty_filled_nonnegative') THEN
        ALTER TABLE orders ADD CONSTRAINT orders_qty_filled_nonnegative CHECK (qty_filled >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'orders_price_requested_nonnegative') THEN
        ALTER TABLE orders ADD CONSTRAINT orders_price_requested_nonnegative CHECK (price_requested >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'orders_price_avg_nonnegative') THEN
        ALTER TABLE orders ADD CONSTRAINT orders_price_avg_nonnegative CHECK (price_avg >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'orders_fee_amount_nonnegative') THEN
        ALTER TABLE orders ADD CONSTRAINT orders_fee_amount_nonnegative CHECK (fee_amount >= 0);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fills_price_positive') THEN
        ALTER TABLE fills ADD CONSTRAINT fills_price_positive CHECK (price > 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fills_qty_positive') THEN
        ALTER TABLE fills ADD CONSTRAINT fills_qty_positive CHECK (qty > 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fills_fee_amount_nonnegative') THEN
        ALTER TABLE fills ADD CONSTRAINT fills_fee_amount_nonnegative CHECK (fee_amount >= 0);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'virtual_balances_available_nonnegative') THEN
        ALTER TABLE virtual_balances ADD CONSTRAINT virtual_balances_available_nonnegative CHECK (available >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'virtual_balances_reserved_nonnegative') THEN
        ALTER TABLE virtual_balances ADD CONSTRAINT virtual_balances_reserved_nonnegative CHECK (reserved >= 0);
    END IF;
END
$$;

COMMIT;
