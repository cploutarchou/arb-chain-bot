-- Review T-058 follow-up (package D):
--
-- P2(3): GET /api/v1/triangles/{id} looks up a triangle's most recent
-- paper_cycles row by opportunity_id (internal/storage/opportunity_detail.go,
-- "ORDER BY started_at DESC LIMIT 1"); without an index on
-- (opportunity_id, started_at DESC) that is a full sequential scan of
-- paper_cycles per detail-page view, by any viewer.
--
-- P3(h): campaign_runs/replay_runs gain an owner_id + heartbeat_at pair
-- so orphan reconciliation (internal/campaign.Runner.reconcileOrphans,
-- internal/replay.Runner.reconcileOrphans) can tell "a row a PREVIOUS
-- instance of THIS process abandoned" apart from "a row a DIFFERENT,
-- still-live process is actively updating" — see internal/jobrun's doc
-- comment for the full reasoning. Nullable: existing rows (persisted
-- before this migration) have neither, and jobrun.Reclaimable treats a
-- NULL/zero heartbeat_at as unconditionally reclaimable, matching the
-- pre-migration behavior exactly (nothing here needs a backfill).
--
-- review P3(i) (see migration 000007's note): CREATE INDEX below takes
-- an ACCESS EXCLUSIVE lock on its table for the duration of the build.
-- No CONCURRENTLY (illegal inside a transaction; this migration keeps
-- its BEGIN/COMMIT wrapper for atomicity, matching every other
-- migration in this set) — safe here because compose's `migrate`
-- service always runs this BEFORE arbd starts, so paper_cycles is still
-- empty in every environment this has ever run against. On an
-- established production table with real data already in paper_cycles,
-- run this in a maintenance window instead, or use CREATE INDEX
-- CONCURRENTLY outside a transaction.

BEGIN;

CREATE INDEX paper_cycles_opportunity_idx ON paper_cycles (opportunity_id, started_at DESC);

ALTER TABLE campaign_runs ADD COLUMN owner_id TEXT;
ALTER TABLE campaign_runs ADD COLUMN heartbeat_at TIMESTAMPTZ;
-- Reconciliation only ever looks this up for "queued"/"running" rows
-- (a handful at a time, never the whole table): a partial index keeps
-- it small and cheap to maintain on every heartbeat write.
CREATE INDEX campaign_runs_active_heartbeat_idx ON campaign_runs (heartbeat_at)
    WHERE status IN ('queued', 'running');

ALTER TABLE replay_runs ADD COLUMN owner_id TEXT;
ALTER TABLE replay_runs ADD COLUMN heartbeat_at TIMESTAMPTZ;
CREATE INDEX replay_runs_active_heartbeat_idx ON replay_runs (heartbeat_at)
    WHERE status IN ('queued', 'running');

COMMIT;
