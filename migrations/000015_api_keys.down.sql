BEGIN;
ALTER TABLE screener_events DROP COLUMN delivered;
DROP TABLE api_keys;
COMMIT;
