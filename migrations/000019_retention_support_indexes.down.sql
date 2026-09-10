BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP INDEX IF EXISTS sessions_expiry_idx;
DROP INDEX IF EXISTS funding_history_at_idx;
DROP INDEX IF EXISTS system_events_ts_only_idx;
DROP INDEX IF EXISTS exchange_health_ts_only_idx;

COMMIT;
