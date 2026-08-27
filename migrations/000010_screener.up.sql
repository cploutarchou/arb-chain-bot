-- Scanner Suite backend core (T-067/T-068,
-- docs/design/scanner-suite.md §2/§7): versioned settings document,
-- alert rules, alert history, funding history, and per-user saved
-- filter templates. Collectors (T-066) land separately; nothing here
-- depends on them existing.

BEGIN;

-- screener_settings: same append-only, exactly-one-active shape as
-- platform_settings (migration 000006) — version/parent_version/diff
-- for the audit trail, "active" plus a partial unique index so exactly
-- one row is ever active at a time.
CREATE TABLE screener_settings (
    version        BIGSERIAL PRIMARY KEY,
    created_by     TEXT REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    active         BOOLEAN NOT NULL DEFAULT FALSE,
    payload        JSONB NOT NULL,
    diff           JSONB,
    parent_version BIGINT REFERENCES screener_settings(version)
);
CREATE UNIQUE INDEX screener_settings_active_idx ON screener_settings(active) WHERE active;

-- screener_rules: alert-rule definitions (design §7). Mutable in place
-- (unlike the settings document) — a rule is one row, edited by ADMIN,
-- evaluation/cooldown state lives in the running process, not here.
CREATE TABLE screener_rules (
    id         TEXT PRIMARY KEY,
    payload    JSONB NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by TEXT REFERENCES users(id)
);

-- screener_events: alert open/close history (design §7). rule_id is
-- deliberately a plain column with NO foreign key: an alert history
-- that gets deleted the moment its rule is deleted would not be an
-- audit trail — a row must survive its rule being edited or removed.
CREATE TABLE screener_events (
    id                  TEXT PRIMARY KEY,
    rule_id             TEXT NOT NULL,
    kind                TEXT NOT NULL CHECK (kind IN ('spread', 'carry', 'basis')),
    base                TEXT NOT NULL,
    quote               TEXT NOT NULL,
    buy_venue           TEXT,
    sell_venue          TEXT,
    opened_at           TIMESTAMPTZ NOT NULL,
    closed_at           TIMESTAMPTZ,
    lifetime_s          BIGINT NOT NULL DEFAULT 0,
    peak_net_bps        NUMERIC NOT NULL,
    telegram_sent       BOOLEAN NOT NULL DEFAULT FALSE,
    paper_execution_id  TEXT
);
CREATE INDEX screener_events_rule_idx ON screener_events (rule_id, opened_at DESC);

-- funding_history: settled funding observations, one row per
-- (venue, base, at) so a re-poll of the same interval is idempotent via
-- ON CONFLICT DO NOTHING (design §7 GET /screener/funding series).
CREATE TABLE funding_history (
    venue TEXT NOT NULL,
    base  TEXT NOT NULL,
    at    TIMESTAMPTZ NOT NULL,
    rate  NUMERIC NOT NULL,
    PRIMARY KEY (venue, base, at)
);

-- screener_templates: per-user saved filter templates (design §7).
CREATE TABLE screener_templates (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    filters    JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX screener_templates_user_idx ON screener_templates (user_id, created_at DESC);

COMMIT;
