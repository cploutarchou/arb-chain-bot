BEGIN;

DROP INDEX IF EXISTS replay_runs_active_heartbeat_idx;
ALTER TABLE replay_runs DROP COLUMN IF EXISTS heartbeat_at;
ALTER TABLE replay_runs DROP COLUMN IF EXISTS owner_id;

DROP INDEX IF EXISTS campaign_runs_active_heartbeat_idx;
ALTER TABLE campaign_runs DROP COLUMN IF EXISTS heartbeat_at;
ALTER TABLE campaign_runs DROP COLUMN IF EXISTS owner_id;

DROP INDEX IF EXISTS paper_cycles_opportunity_idx;

COMMIT;
