BEGIN;

DROP INDEX IF EXISTS screener_reports_org_idx;
ALTER TABLE screener_reports DROP COLUMN IF EXISTS org_id;

-- The pre-000017 index allows one active row in the whole table: keep
-- the platform organisation's and deactivate every tenant's before
-- restoring it.
DROP INDEX IF EXISTS screener_settings_org_idx;
DROP INDEX IF EXISTS screener_settings_active_org_idx;
UPDATE screener_settings SET active = FALSE WHERE active AND org_id <> 1;
ALTER TABLE screener_settings DROP COLUMN IF EXISTS org_id;
CREATE UNIQUE INDEX IF NOT EXISTS screener_settings_active_idx ON screener_settings (active) WHERE active;

COMMIT;
