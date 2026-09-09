-- Enforce the append-only contract on audit_events and risk_events
-- (docs/audit/database-audit.md D6, roadmap P1-19). Both tables have
-- been insert-only "by policy" since migration 000001 (see its header
-- comment), but every environment runs migrations and the app under the
-- same database role (D6's own evidence), so nothing actually stopped an
-- UPDATE or DELETE from reaching either table. A trigger enforces the
-- rule at the row level regardless of which role or code path issues the
-- statement — cheaper to operate than a second migration-only role and a
-- grant matrix, and it keeps working unchanged if that role separation
-- is added later.
--
-- Nothing in internal/storage legitimately mutates these rows today:
-- InsertAuditEvent and InsertRiskEvent only INSERT, and every read path
-- (ListAuditEvents, ListRiskEvents) is SELECT-only. The two test
-- fixtures that used to reset these tables between runs with DELETE
-- (internal/storage/storage_test.go's testStore, and
-- internal/storage/configs_test.go) now TRUNCATE them instead: TRUNCATE
-- does not fire row-level triggers, so it stays available for the
-- disposable test databases these fixtures require, while a real
-- application role still cannot single-row UPDATE/DELETE its way around
-- the guarantee.
--
-- Both blocked operations raise SQLSTATE P0001 ("raise_exception", the
-- default plpgsql RAISE EXCEPTION code) with a message naming the table
-- and the attempted operation.

BEGIN;

-- The DDL below takes a brief ACCESS EXCLUSIVE lock on each table to
-- attach the trigger (metadata only, no table rewrite — this is not an
-- index build). lock_timeout keeps this migration from queuing
-- indefinitely behind a long-running transaction that already holds a
-- conflicting lock on either table; statement_timeout is a generous
-- backstop for the same reason migration 000009 keeps one — see that
-- migration's note on CREATE INDEX for the general policy this follows.
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

CREATE OR REPLACE FUNCTION enforce_append_only() RETURNS trigger
    LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'append-only table %.% does not allow %: evidence rows are insert-only',
        TG_TABLE_SCHEMA, TG_TABLE_NAME, TG_OP;
END;
$$;

COMMENT ON FUNCTION enforce_append_only() IS
    'Raises SQLSTATE P0001 for any UPDATE or DELETE on a table this is attached to (docs/audit/database-audit.md D6, roadmap P1-19). Insert-only evidence tables use this instead of relying on convention.';

CREATE OR REPLACE TRIGGER audit_events_immutable
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION enforce_append_only();

CREATE OR REPLACE TRIGGER risk_events_immutable
    BEFORE UPDATE OR DELETE ON risk_events
    FOR EACH ROW EXECUTE FUNCTION enforce_append_only();

COMMENT ON TRIGGER audit_events_immutable ON audit_events IS
    'Blocks UPDATE/DELETE: audit_events is append-only audit evidence (docs/audit/database-audit.md D6). Bulk resets in tests must TRUNCATE instead.';
COMMENT ON TRIGGER risk_events_immutable ON risk_events IS
    'Blocks UPDATE/DELETE: risk_events is append-only risk evidence (docs/audit/database-audit.md D6) and is kept indefinitely — excluded from the retention job in internal/storage/retention.go for the same reason. Bulk resets in tests must TRUNCATE instead.';

COMMIT;
