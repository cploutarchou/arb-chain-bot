-- Organisation scope for the two screener tables migration 000013 left
-- platform-global: screener_settings (the versioned settings document)
-- and screener_reports (the nightly paper reports). Both get the org_id
-- every sibling screener table already carries, backfilled to the
-- platform organisation (1) like the 000013 backfill; the DEFAULT stays
-- for the same reason as there (a row written without a scope is the
-- platform's). The "exactly one active" rule becomes "exactly one
-- active per organisation".

BEGIN;

ALTER TABLE screener_settings
    ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 1 REFERENCES organisations(id);
DROP INDEX IF EXISTS screener_settings_active_idx;
CREATE UNIQUE INDEX IF NOT EXISTS screener_settings_active_org_idx
    ON screener_settings (org_id) WHERE active;
CREATE INDEX IF NOT EXISTS screener_settings_org_idx
    ON screener_settings (org_id, version DESC);

ALTER TABLE screener_reports
    ADD COLUMN IF NOT EXISTS org_id BIGINT NOT NULL DEFAULT 1 REFERENCES organisations(id);
CREATE INDEX IF NOT EXISTS screener_reports_org_idx
    ON screener_reports (org_id, created_at DESC, id DESC);

COMMIT;
