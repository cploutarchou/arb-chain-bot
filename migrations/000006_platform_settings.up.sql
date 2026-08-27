-- Platform settings (T-057, docs/design/platform-settings-and-restart.md
-- §1.4): venues/symbols/starting assets/fees, paper starting balances,
-- and the Telegram allowlist as one versioned document, same
-- append-only/exactly-one-active shape as strategy_configs. 000005 is
-- campaign verdicts, landing concurrently; the number is ordering, not
-- coupling.
CREATE TABLE platform_settings (
    version        BIGSERIAL PRIMARY KEY,
    created_by     TEXT REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    active         BOOLEAN NOT NULL DEFAULT FALSE,
    payload        JSONB NOT NULL,
    diff           JSONB,
    parent_version BIGINT REFERENCES platform_settings(version)
);
CREATE UNIQUE INDEX platform_settings_active_idx ON platform_settings(active) WHERE active;
