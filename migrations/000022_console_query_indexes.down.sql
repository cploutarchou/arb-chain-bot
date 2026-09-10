-- Reverse of 000022: drop the console query indexes.

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP INDEX IF EXISTS orders_market_idx;
DROP INDEX IF EXISTS markets_symbol_idx;
DROP INDEX IF EXISTS paper_cycles_session_started_idx;

COMMIT;
