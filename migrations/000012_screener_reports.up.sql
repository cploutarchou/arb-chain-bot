-- Scanner Suite nightly paper reports (T-078, docs/design/strategy-models.md
-- §7 statistics / §8 production gate; docs/design/scanner-suite.md §7
-- /screener/reports). One row per (period, strategy, rule) the generator
-- produced: payload is the JSON statistics + gate checklist, md the
-- rendered markdown that was also written under
-- <recordings dir>/screener-reports/<date>/. rule_id is '' for the
-- per-strategy aggregate and has no FK (a report must survive its rule
-- being edited or deleted, like screener_events).

BEGIN;

CREATE TABLE screener_reports (
    id           TEXT PRIMARY KEY,
    period_start TIMESTAMPTZ NOT NULL,
    period_end   TIMESTAMPTZ NOT NULL,
    strategy     TEXT NOT NULL CHECK (strategy IN ('cross_venue_spot', 'carry', 'funding_harvest')),
    rule_id      TEXT NOT NULL DEFAULT '',
    payload      JSONB NOT NULL,
    md           TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX screener_reports_created_idx ON screener_reports (created_at DESC);
CREATE INDEX screener_reports_scope_idx ON screener_reports (strategy, rule_id, period_start DESC);

COMMIT;
