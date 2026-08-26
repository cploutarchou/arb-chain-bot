-- §80 campaign runs started from the console (docs/deployment.md §3):
-- one row per run, updated as it progresses; the report is kept inline
-- so the console can show the Verdict without touching the recordings
-- volume.
CREATE TABLE campaign_runs (
    id            TEXT PRIMARY KEY,
    recording_id  TEXT NOT NULL,
    request       JSONB NOT NULL,
    status        TEXT NOT NULL,
    done          INTEGER NOT NULL DEFAULT 0,
    total         INTEGER NOT NULL DEFAULT 0,
    step          TEXT,
    created_at    TIMESTAMPTZ NOT NULL,
    started_at    TIMESTAMPTZ,
    finished_at   TIMESTAMPTZ,
    error         TEXT,
    flags         JSONB,
    report_md     TEXT,
    report_path   TEXT,
    json_path     TEXT,
    actor         TEXT
);
CREATE INDEX campaign_runs_created_idx ON campaign_runs (created_at DESC);
CREATE INDEX campaign_runs_recording_idx ON campaign_runs (recording_id);
