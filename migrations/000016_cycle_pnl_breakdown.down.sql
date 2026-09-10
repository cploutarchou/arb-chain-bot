BEGIN;

ALTER TABLE paper_cycles
    DROP COLUMN IF EXISTS realized_pnl,
    DROP COLUMN IF EXISTS exposure_mark,
    DROP COLUMN IF EXISTS input_consumed,
    DROP COLUMN IF EXISTS final_amount,
    DROP COLUMN IF EXISTS planned_return_bps,
    DROP COLUMN IF EXISTS actual_return_bps;

COMMIT;
