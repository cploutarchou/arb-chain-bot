-- Initial schema. Conventions (docs/data-flow.md §4): NUMERIC for all
-- money/quantities, timestamptz UTC, ULID text PKs for domain entities.
-- audit_events is insert-only by policy: the application role must not be
-- granted UPDATE/DELETE on it (grants are applied per environment).

BEGIN;

-- === identity =============================================================

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    display_name  TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('ADMIN','OPERATOR','VIEWER')),
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    mfa_enrolled  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    ip         INET,
    user_agent TEXT
);
CREATE INDEX sessions_user_idx ON sessions(user_id) WHERE revoked_at IS NULL;

-- === market structure =====================================================

CREATE TABLE exchanges (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    enabled       BOOLEAN NOT NULL DEFAULT FALSE,
    paper_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    capabilities  JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE exchange_health (
    id              BIGSERIAL PRIMARY KEY,
    exchange_id     TEXT NOT NULL REFERENCES exchanges(id),
    ts              TIMESTAMPTZ NOT NULL,
    feed_state      TEXT NOT NULL,
    latency_ms      NUMERIC,
    clock_offset_ms NUMERIC,
    reconnects      BIGINT NOT NULL DEFAULT 0,
    seq_errors      BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX exchange_health_ts_idx ON exchange_health(exchange_id, ts DESC);

CREATE TABLE markets (
    id           TEXT PRIMARY KEY,
    exchange_id  TEXT NOT NULL REFERENCES exchanges(id),
    symbol       TEXT NOT NULL,
    base_asset   TEXT NOT NULL,
    quote_asset  TEXT NOT NULL,
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    status       TEXT NOT NULL DEFAULT 'unknown',
    rules        JSONB NOT NULL DEFAULT '{}'::jsonb,
    fee_override JSONB,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (exchange_id, symbol)
);
CREATE INDEX markets_assets_idx ON markets(exchange_id, base_asset, quote_asset);

CREATE TABLE triangles (
    id             TEXT PRIMARY KEY,
    exchange_id    TEXT NOT NULL REFERENCES exchanges(id),
    starting_asset TEXT NOT NULL,
    legs           JSONB NOT NULL,
    canonical_key  TEXT NOT NULL UNIQUE,
    enabled        BOOLEAN NOT NULL DEFAULT TRUE,
    liquidity_score NUMERIC,
    discovered_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX triangles_exchange_idx ON triangles(exchange_id, enabled);

-- === opportunities ========================================================

CREATE TABLE opportunities (
    id               TEXT PRIMARY KEY,
    exchange_id      TEXT NOT NULL REFERENCES exchanges(id),
    triangle_id      TEXT NOT NULL REFERENCES triangles(id),
    status           TEXT NOT NULL,
    reason_code      TEXT,
    starting_asset   TEXT NOT NULL,
    starting_amount  NUMERIC NOT NULL,
    legs             JSONB NOT NULL,
    gross_final_amount NUMERIC,
    fee_cost         NUMERIC,
    slippage_cost    NUMERIC,
    latency_buffer   NUMERIC,
    risk_buffer      NUMERIC,
    estimated_final_amount NUMERIC,
    gross_profit     NUMERIC,
    net_profit       NUMERIC,
    gross_return_bps NUMERIC,
    net_return_bps   NUMERIC,
    max_profitable_size NUMERIC,
    recommended_size NUMERIC,
    confidence       NUMERIC,
    data_quality     NUMERIC,
    config_version   BIGINT,
    detected_at      TIMESTAMPTZ NOT NULL,
    decided_at       TIMESTAMPTZ,
    expires_at       TIMESTAMPTZ
);
CREATE INDEX opportunities_triangle_idx ON opportunities(triangle_id, detected_at DESC);
CREATE INDEX opportunities_status_idx ON opportunities(status, detected_at DESC);
CREATE INDEX opportunities_exchange_time_idx ON opportunities(exchange_id, detected_at DESC);

-- === paper trading ========================================================

CREATE TABLE paper_sessions (
    id                TEXT PRIMARY KEY,
    mode              TEXT NOT NULL,
    started_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at          TIMESTAMPTZ,
    starting_balances JSONB NOT NULL,
    config_version    BIGINT,
    seed              BIGINT
);

CREATE TABLE paper_cycles (
    id             TEXT PRIMARY KEY,
    session_id     TEXT NOT NULL REFERENCES paper_sessions(id),
    opportunity_id TEXT REFERENCES opportunities(id),
    outcome        TEXT NOT NULL,
    pnl_amount     NUMERIC,
    pnl_asset      TEXT,
    fees           JSONB,
    slippage_bps   NUMERIC,
    exposure       JSONB,
    started_at     TIMESTAMPTZ NOT NULL,
    settled_at     TIMESTAMPTZ
);
CREATE INDEX paper_cycles_session_idx ON paper_cycles(session_id, settled_at DESC);

CREATE TABLE orders (
    id          TEXT PRIMARY KEY,
    cycle_id    TEXT NOT NULL REFERENCES paper_cycles(id),
    leg_no      SMALLINT NOT NULL CHECK (leg_no IN (1,2,3)),
    market_id   TEXT NOT NULL REFERENCES markets(id),
    side        TEXT NOT NULL CHECK (side IN ('BUY','SELL')),
    order_type  TEXT NOT NULL,
    time_in_force TEXT,
    qty_requested NUMERIC NOT NULL,
    qty_filled  NUMERIC NOT NULL DEFAULT 0,
    price_requested NUMERIC,
    price_avg   NUMERIC,
    fee_amount  NUMERIC,
    fee_asset   TEXT,
    latency_ms  NUMERIC,
    status      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL,
    acked_at    TIMESTAMPTZ,
    filled_at   TIMESTAMPTZ
);
CREATE INDEX orders_cycle_idx ON orders(cycle_id, leg_no);

CREATE TABLE fills (
    id           TEXT PRIMARY KEY,
    order_id     TEXT NOT NULL REFERENCES orders(id),
    price        NUMERIC NOT NULL,
    qty          NUMERIC NOT NULL,
    fee_amount   NUMERIC,
    fee_asset    TEXT,
    book_version BIGINT,
    ts           TIMESTAMPTZ NOT NULL
);
CREATE INDEX fills_order_idx ON fills(order_id);

CREATE TABLE virtual_balances (
    session_id  TEXT NOT NULL REFERENCES paper_sessions(id),
    exchange_id TEXT NOT NULL REFERENCES exchanges(id),
    asset       TEXT NOT NULL,
    available   NUMERIC NOT NULL DEFAULT 0,
    reserved    NUMERIC NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, exchange_id, asset)
);

CREATE TABLE balance_snapshots (
    id          BIGSERIAL PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES paper_sessions(id),
    ts          TIMESTAMPTZ NOT NULL,
    balances    JSONB NOT NULL,
    mark_values JSONB
);
CREATE INDEX balance_snapshots_idx ON balance_snapshots(session_id, ts DESC);

CREATE TABLE pnl_snapshots (
    id           BIGSERIAL PRIMARY KEY,
    session_id   TEXT NOT NULL REFERENCES paper_sessions(id),
    ts           TIMESTAMPTZ NOT NULL,
    realized     NUMERIC NOT NULL DEFAULT 0,
    unrealized   NUMERIC NOT NULL DEFAULT 0,
    fees_paid    NUMERIC NOT NULL DEFAULT 0,
    slippage_cost NUMERIC NOT NULL DEFAULT 0,
    drawdown     NUMERIC,
    by_dimension JSONB
);
CREATE INDEX pnl_snapshots_idx ON pnl_snapshots(session_id, ts DESC);

-- === configuration ========================================================

CREATE TABLE strategy_configs (
    version        BIGSERIAL PRIMARY KEY,
    created_by     TEXT REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    active         BOOLEAN NOT NULL DEFAULT FALSE,
    payload        JSONB NOT NULL,
    diff           JSONB,
    parent_version BIGINT REFERENCES strategy_configs(version)
);
-- exactly one active version at a time
CREATE UNIQUE INDEX strategy_configs_active_idx ON strategy_configs(active) WHERE active;

-- === ai ===================================================================

CREATE TABLE ai_recommendations (
    id                TEXT PRIMARY KEY,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    scope             TEXT NOT NULL,
    parameter         TEXT NOT NULL,
    current_value     TEXT,
    recommended_value TEXT,
    evidence          JSONB,
    reason            TEXT,
    confidence        NUMERIC,
    expected_effect   TEXT,
    risks             TEXT,
    expires_at        TIMESTAMPTZ,
    status            TEXT NOT NULL DEFAULT 'proposed'
                      CHECK (status IN ('proposed','approved','rejected','deferred','expired')),
    decided_by        TEXT REFERENCES users(id),
    decided_at        TIMESTAMPTZ
);
CREATE INDEX ai_recommendations_status_idx ON ai_recommendations(status, created_at DESC);

-- === risk / alerts / notifications =======================================

CREATE TABLE risk_events (
    id             TEXT PRIMARY KEY,
    ts             TIMESTAMPTZ NOT NULL,
    kind           TEXT NOT NULL,
    subject        TEXT,
    limit_name     TEXT,
    observed       TEXT,
    threshold      TEXT,
    action         TEXT,
    breaker_state  TEXT,
    correlation_id TEXT
);
CREATE INDEX risk_events_ts_idx ON risk_events(ts DESC);

CREATE TABLE alerts (
    id          TEXT PRIMARY KEY,
    ts          TIMESTAMPTZ NOT NULL,
    severity    TEXT NOT NULL CHECK (severity IN ('INFO','WARNING','HIGH','CRITICAL')),
    source      TEXT NOT NULL,
    title       TEXT NOT NULL,
    body        TEXT,
    dedup_key   TEXT,
    state       TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active','acked','resolved')),
    acked_by    TEXT REFERENCES users(id),
    acked_at    TIMESTAMPTZ,
    resolved_by TEXT REFERENCES users(id),
    resolved_at TIMESTAMPTZ
);
CREATE INDEX alerts_state_idx ON alerts(state, severity, ts DESC);
CREATE INDEX alerts_dedup_idx ON alerts(dedup_key, ts DESC);

CREATE TABLE notifications (
    id       TEXT PRIMARY KEY,
    alert_id TEXT REFERENCES alerts(id),
    channel  TEXT NOT NULL,
    status   TEXT NOT NULL,
    sent_at  TIMESTAMPTZ
);

-- === reports / audit / system ============================================

CREATE TABLE reports (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    period_end   TIMESTAMPTZ NOT NULL,
    generated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload      JSONB NOT NULL,
    storage_ref  TEXT
);
CREATE INDEX reports_kind_idx ON reports(kind, period_start DESC);

CREATE TABLE audit_events (
    id             TEXT PRIMARY KEY,
    ts             TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor          TEXT,
    source         TEXT NOT NULL CHECK (source IN ('web','telegram','system','ai')),
    action         TEXT NOT NULL,
    entity         TEXT NOT NULL,
    entity_id      TEXT,
    before         JSONB,
    after          JSONB,
    ip             INET,
    correlation_id TEXT
);
CREATE INDEX audit_events_entity_idx ON audit_events(entity, ts DESC);
CREATE INDEX audit_events_actor_idx ON audit_events(actor, ts DESC);

CREATE TABLE system_events (
    id        TEXT PRIMARY KEY,
    ts        TIMESTAMPTZ NOT NULL DEFAULT now(),
    component TEXT NOT NULL,
    kind      TEXT NOT NULL,
    detail    JSONB
);
CREATE INDEX system_events_idx ON system_events(component, ts DESC);

CREATE TABLE market_recording_metadata (
    id             TEXT PRIMARY KEY,
    exchange_id    TEXT NOT NULL REFERENCES exchanges(id),
    started_at     TIMESTAMPTZ NOT NULL,
    ended_at       TIMESTAMPTZ,
    streams        JSONB NOT NULL,
    segment_files  JSONB NOT NULL DEFAULT '[]'::jsonb,
    config_version BIGINT,
    notes          TEXT
);
CREATE INDEX recording_metadata_idx ON market_recording_metadata(exchange_id, started_at DESC);

COMMIT;
