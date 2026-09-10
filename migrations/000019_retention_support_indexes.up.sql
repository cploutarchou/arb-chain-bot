-- Supporting indexes for the retention job (docs/audit/database-audit.md
-- D5, docs/audit/infra-delivery-audit.md I9, internal/storage/retention.go).
-- Each telemetry table the job prunes already has an index whose LEADING
-- column is something other than the cutoff timestamp
-- (exchange_health/system_events are keyed first by exchange/component
-- for the console's own queries; sessions' only index is a partial one
-- for live sessions; funding_history's primary key leads with
-- venue/base). None of those help "rows older than a cutoff, across
-- every partition key", which is exactly what a nightly sweep runs
-- against a table this job is specifically meant to keep from growing
-- forever — without its own index that query degrades into a full scan
-- that gets slower every night the table is not pruned.
--
-- opportunities and market_recording_metadata are left alone:
-- opportunities' existing (status, detected_at DESC) index already
-- serves the job's WHERE status = '…' AND detected_at < $1 predicate
-- (retentionRules in internal/storage/retention.go matches literal
-- status values for exactly this reason), and market_recording_metadata
-- defaults to retention disabled and is expected to stay small (one row
-- per recording session, not per frame — see docs/data-flow.md §5).

BEGIN;

SET LOCAL lock_timeout = '5s';
-- CREATE INDEX (no CONCURRENTLY) holds ACCESS EXCLUSIVE for the build,
-- which blocks writers to the table for its duration. That is fine on an
-- empty or lightly populated database (every environment this has run
-- against so far, same reasoning as migration 000009's note) but not
-- guaranteed once one of these tables is large in a live deployment;
-- statement_timeout is deliberately generous (not the 5s used for plain
-- DDL elsewhere in this migration set) so a real build is not aborted
-- partway through. On an established production table, run
-- CREATE INDEX CONCURRENTLY outside a transaction in a maintenance
-- window instead of applying this migration as-is.
SET LOCAL statement_timeout = '5min';

CREATE INDEX IF NOT EXISTS exchange_health_ts_only_idx ON exchange_health (ts);
CREATE INDEX IF NOT EXISTS system_events_ts_only_idx ON system_events (ts);
CREATE INDEX IF NOT EXISTS funding_history_at_idx ON funding_history (at);
-- Expression index matching internal/storage/retention.go's sessions
-- rule verbatim (WHERE COALESCE(revoked_at, expires_at) < $1): a
-- revoked session is prunable from its revocation time, a session that
-- merely expired from its expiry time.
CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions (COALESCE(revoked_at, expires_at));

COMMIT;
