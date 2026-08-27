-- Client API keys (T-086, docs/design/packages.md §3.1 api.*): one row
-- per issued key, org-scoped. Only a SHA-256 digest of the key is
-- stored (session-token pattern, migration 000001/internal/auth: a
-- leaked table contains no usable bearer credential); "prefix" is the
-- short, non-secret leading slice of the plaintext kept so the console
-- can show which key is which and so lookups do not need a full-table
-- hash comparison. The plaintext itself is generated, returned once,
-- and never persisted anywhere.

BEGIN;

CREATE TABLE api_keys (
    id           TEXT PRIMARY KEY,
    org_id       BIGINT NOT NULL REFERENCES organisations(id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL REFERENCES users(id),
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL UNIQUE,
    hash         TEXT NOT NULL,
    scopes       TEXT[] NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);
CREATE INDEX api_keys_org_idx ON api_keys (org_id, created_at DESC);
-- The hot lookup path (authenticate on every Bearer request) only ever
-- needs a live key, so the partial index stays small even after years
-- of revocations.
CREATE UNIQUE INDEX api_keys_prefix_live_idx ON api_keys (prefix) WHERE revoked_at IS NULL;

-- Alert-channel delivery outcomes (docs/design/packages.md §3.1
-- alerts.channels, T-086 §2): one entry per channel the rule was
-- configured to notify, written by the evaluator's dispatcher
-- (synchronously for telegram, asynchronously — patched after send —
-- for email/webhook so a slow/retrying webhook never blocks the poll
-- loop). Shape: {"telegram": {"status": "sent", "at": "..."},
-- "webhook": {"status": "failed", "error": "...", "at": "..."}}.
ALTER TABLE screener_events ADD COLUMN delivered JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMIT;
