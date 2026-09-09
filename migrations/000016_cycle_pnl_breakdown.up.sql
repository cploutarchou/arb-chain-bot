-- Paper-cycle PnL breakdown. pnl_amount holds the marked total
-- (realized cash PnL plus stranded exposure valued at settlement) and was
-- the only figure persisted, so the quality score summed a mark-to-market
-- estimate as if it were realized. Each cycle now also records the
-- cash-basis realized PnL and the exposure mark separately, the input
-- actually deployed, the amount returned, and the planned versus actual
-- return in bps so expected-vs-actual can be answered per row. Rows
-- written before this migration keep NULLs; readers coalesce to
-- pnl_amount where a realized figure is needed.

BEGIN;

ALTER TABLE paper_cycles
    ADD COLUMN realized_pnl       NUMERIC,
    ADD COLUMN exposure_mark      NUMERIC,
    ADD COLUMN input_consumed     NUMERIC,
    ADD COLUMN final_amount       NUMERIC,
    ADD COLUMN planned_return_bps NUMERIC,
    ADD COLUMN actual_return_bps  NUMERIC;

COMMIT;
