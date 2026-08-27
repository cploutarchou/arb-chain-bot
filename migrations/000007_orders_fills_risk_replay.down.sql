BEGIN;

DROP TABLE IF EXISTS replay_runs;
ALTER TABLE opportunities DROP COLUMN IF EXISTS book_versions;
ALTER TABLE opportunities DROP COLUMN IF EXISTS decision;
DROP INDEX IF EXISTS fills_ts_idx;
DROP INDEX IF EXISTS orders_created_idx;

COMMIT;
