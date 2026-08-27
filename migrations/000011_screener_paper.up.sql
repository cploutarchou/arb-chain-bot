-- Scanner Suite automatic PAPER execution ledger (T-071,
-- docs/design/scanner-suite.md §4, docs/design/strategy-models.md §1.6,
-- §2.6, §3.6). This is the screener's OWN paper ledger: paper_cycles
-- (migration 000001) is shaped around a three-leg triangular cycle with
-- a paper_sessions FK and no strategy column, and execution.CycleResult
-- carries no strategy tag, so booking screener executions through
-- storage.InsertCycle would either mislabel them as triangular cycles
-- or require changing internal/execution types — neither is done here.
-- Nothing in these tables can place a real order: they only record
-- simulated fills against public top-of-book quotes.

BEGIN;

-- Per-venue paper balances (§1.6). Seeded from the screener settings
-- document's paper.balances on first run; every simulated fill moves
-- them. One row per (venue, asset); balance is a decimal string on the
-- wire and NUMERIC here — never a float.
CREATE TABLE screener_paper_balances (
    venue      TEXT NOT NULL,
    asset      TEXT NOT NULL,
    balance    NUMERIC NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (venue, asset)
);

-- One row per paper position. Cross-venue spot executions are
-- instantaneous (opened and closed in the same execution) and are
-- recorded with status CLOSED straight away; carry / funding-harvest
-- positions stay OPEN until an exit or stop closes them. rule_id has no
-- FK on purpose (same reasoning as screener_events: the ledger must
-- survive its rule being edited or deleted).
CREATE TABLE screener_paper_positions (
    id             TEXT PRIMARY KEY,
    rule_id        TEXT NOT NULL,
    event_id       TEXT,
    strategy       TEXT NOT NULL CHECK (strategy IN ('cross_venue_spot', 'carry', 'funding_harvest')),
    base           TEXT NOT NULL,
    quote          TEXT NOT NULL,
    venue_a        TEXT NOT NULL,
    venue_b        TEXT NOT NULL,
    qty            NUMERIC NOT NULL,
    open_payload   JSONB NOT NULL,
    opened_at      TIMESTAMPTZ NOT NULL,
    closed_at      TIMESTAMPTZ,
    pnl_quote      NUMERIC NOT NULL DEFAULT 0,
    funding_quote  NUMERIC NOT NULL DEFAULT 0,
    status         TEXT NOT NULL CHECK (status IN ('OPEN', 'CLOSED', 'SKIPPED')),
    skipped_reason TEXT,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX screener_paper_positions_rule_idx ON screener_paper_positions (rule_id, opened_at DESC);
CREATE INDEX screener_paper_positions_open_idx ON screener_paper_positions (status) WHERE status = 'OPEN';

-- One row per execution event: a spot execution (kind 'spot'), a carry /
-- funding open ('open'), a close ('close'), a booked funding settlement
-- ('funding', §1.4: venue, symbol, T_i, F_i, mark, amount in payload),
-- an unwind after a rejected second leg ('unwind'), or a one-legged
-- spot execution ('partial_leg'). fills is the leg list (side, venue,
-- quote price, fill price, qty, fee) as decimal strings.
CREATE TABLE screener_paper_executions (
    id                TEXT PRIMARY KEY,
    position_id       TEXT REFERENCES screener_paper_positions(id) ON DELETE CASCADE,
    rule_id           TEXT NOT NULL,
    event_id          TEXT,
    strategy          TEXT NOT NULL,
    kind              TEXT NOT NULL CHECK (kind IN ('spot', 'open', 'close', 'funding', 'unwind', 'partial_leg')),
    base              TEXT NOT NULL,
    quote             TEXT NOT NULL,
    venue_a           TEXT NOT NULL,
    venue_b           TEXT NOT NULL,
    fills             JSONB NOT NULL,
    fees_quote        NUMERIC NOT NULL DEFAULT 0,
    slip_allow_bps    NUMERIC NOT NULL DEFAULT 0,
    realised_slip_bps NUMERIC,
    pnl_quote         NUMERIC NOT NULL DEFAULT 0,
    payload           JSONB,
    at                TIMESTAMPTZ NOT NULL
);
CREATE INDEX screener_paper_executions_rule_idx ON screener_paper_executions (rule_id, at DESC);
CREATE INDEX screener_paper_executions_position_idx ON screener_paper_executions (position_id, at);

COMMIT;
