-- T-058 (console package D): keyset-pagination indexes for the global
-- Orders/Fills pages (BL-20), structured decision/book-version evidence
-- on opportunities (BL-27), and the console-driven replay-run job table
-- (BL-17). risk_events already exists (migration 000001) — nothing to
-- add there.
--
-- review P3(i): CREATE INDEX below takes an ACCESS EXCLUSIVE lock on its
-- table for the duration of the build (no CONCURRENTLY — illegal inside
-- a transaction, and this migration deliberately keeps its
-- BEGIN/COMMIT wrapper for atomicity). On an EMPTY/small table (true for
-- every environment this migration set has ever run against — compose's
-- `migrate` service runs it BEFORE arbd starts, so orders/fills are
-- still empty) the build is effectively instant and the lock is
-- invisible. On an established production table with meaningful data
-- already in orders/fills, the same statement would hold that lock for
-- the whole build and block concurrent readers/writers for however long
-- that takes — run it in a maintenance window, or CREATE INDEX
-- CONCURRENTLY outside a transaction, in that case.

BEGIN;

-- === BL-20: orders/fills keyset pagination ================================
-- GET /api/v1/orders and /fills page by (own timestamp column, id) DESC;
-- without this index every page beyond the first is a sort over the full
-- table (audit P2-5 already fixed the equivalent paper_cycles query in
-- migration 000003 — same class of problem here).
-- Both columns DESC: the handler orders "created_at DESC, id DESC" as
-- its tiebreak for keyset pagination (rows sharing one transaction's
-- timestamp must still come back in a stable, index-satisfiable order).
-- An (id ASC) index would force a Sort node on top of the scan for that
-- ORDER BY; matching the direction lets Postgres walk the index in order
-- with no extra sort, which is the whole point of a keyset index.
CREATE INDEX orders_created_idx ON orders (created_at DESC, id DESC);
CREATE INDEX fills_ts_idx ON fills (ts DESC, id DESC);

-- === BL-27: structured decision + book versions on opportunities ==========
-- InsertOpportunity has always folded {legs, risk_checks} into the
-- existing `legs` JSONB column (risk.Decision.Checks was already
-- persisted, just not addressably). These are additive columns for new
-- rows; historical rows keep reading the risk_checks sub-object out of
-- `legs` as a fallback (internal/storage reads both shapes).
ALTER TABLE opportunities ADD COLUMN decision JSONB;
ALTER TABLE opportunities ADD COLUMN book_versions BIGINT[];

-- === BL-17: console-driven replay runs =====================================
-- One row per replay run, updated as it progresses — the same shape
-- campaign_runs (migration 000004) uses for the same reason (the
-- console shows progress without a WS connection surviving a page
-- reload).
CREATE TABLE replay_runs (
    id             TEXT PRIMARY KEY,
    recording_id   TEXT NOT NULL,
    config_version BIGINT,
    speed          NUMERIC,
    status         TEXT NOT NULL,
    done           INTEGER NOT NULL DEFAULT 0,
    total          INTEGER NOT NULL DEFAULT 0,
    step           TEXT,
    created_at     TIMESTAMPTZ NOT NULL,
    started_at     TIMESTAMPTZ,
    finished_at    TIMESTAMPTZ,
    error          TEXT,
    opportunities  BIGINT,
    qualified      BIGINT,
    cycles         BIGINT,
    top            JSONB,
    actor          TEXT
);
CREATE INDEX replay_runs_created_idx ON replay_runs (created_at DESC);
CREATE INDEX replay_runs_recording_idx ON replay_runs (recording_id);

COMMIT;
