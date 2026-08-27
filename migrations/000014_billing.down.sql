BEGIN;
DROP TABLE affiliate_ledger;
ALTER TABLE organisations DROP COLUMN referred_by;
DROP TABLE affiliate_accounts;
DROP TABLE paddle_events;
DROP TABLE subscriptions;
DROP TABLE billing_prices;
COMMIT;
