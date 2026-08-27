BEGIN;

-- T-060 secrets vault (docs/design/settings-expansion.md §3.1). One row
-- per registry name, overwritten on write; no version history (a table
-- of historical ciphertexts is a larger blast radius for zero
-- operational value — audit_events records that a write happened).
CREATE TABLE secrets (
    name       TEXT PRIMARY KEY,
    ciphertext BYTEA NOT NULL,
    nonce      BYTEA NOT NULL,
    key_id     TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by TEXT REFERENCES users(id)
);

COMMIT;
