BEGIN;

DROP TABLE IF EXISTS market_recording_metadata;
DROP TABLE IF EXISTS system_events;
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS reports;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS alerts;
DROP TABLE IF EXISTS risk_events;
DROP TABLE IF EXISTS ai_recommendations;
DROP TABLE IF EXISTS strategy_configs;
DROP TABLE IF EXISTS pnl_snapshots;
DROP TABLE IF EXISTS balance_snapshots;
DROP TABLE IF EXISTS virtual_balances;
DROP TABLE IF EXISTS fills;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS paper_cycles;
DROP TABLE IF EXISTS paper_sessions;
DROP TABLE IF EXISTS opportunities;
DROP TABLE IF EXISTS triangles;
DROP TABLE IF EXISTS markets;
DROP TABLE IF EXISTS exchange_health;
DROP TABLE IF EXISTS exchanges;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;

COMMIT;
