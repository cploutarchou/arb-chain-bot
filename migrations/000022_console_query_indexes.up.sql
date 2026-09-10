-- Console query indexes (docs/audit/database-audit.md D7).
--
-- Three queries filtered or joined on columns no index covered:
--
--   1. ListCycles (internal/storage/lists.go) filters
--      "WHERE session_id = $1 ORDER BY started_at DESC LIMIT $2".
--      paper_cycles had (session_id, settled_at DESC) (migration 000003)
--      and (opportunity_id, started_at DESC) (migration 000009) — the
--      session's own newest-first page fell back to the global
--      (started_at DESC) index (migration 000003) with the session
--      filter as a residual, i.e. a scan that touches other sessions'
--      rows until it has filled the limit. (session_id, started_at DESC)
--      answers it directly.
--
--   2. ListOrdersGlobal / ListFillsGlobal (internal/storage/
--      orders_fills.go) filter on the joined m.symbol with only the
--      keyset indexes (migration 000007) on the own-table columns. A
--      symbol filter is selective (one row per market), so the planner
--      wants to start from markets — but markets had no symbol index,
--      and orders had no market_id index to join back through. Both are
--      added; the symbol-driven plan becomes index-only into orders.
--      Materialising symbol/triangle_id onto orders itself (D7's
--      "consider") is deliberately NOT taken: it denormalises the hot
--      outbox write path (every order row, every cycle) to accelerate a
--      paginated console filter that already drives off the keyset
--      index — revisit if the global orders page needs sub-second
--      filtering over a large table.
--
-- Lock policy follows migrations 000009/000019: CREATE INDEX (no
-- CONCURRENTLY — illegal inside the transaction this set keeps for
-- atomicity) holds ACCESS EXCLUSIVE for the build, which is invisible
-- on the empty/small tables every environment has migrated so far
-- (compose runs `migrate` before arbd starts). On an established
-- production table, run CREATE INDEX CONCURRENTLY outside a
-- transaction in a maintenance window instead.

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '5min';

CREATE INDEX IF NOT EXISTS paper_cycles_session_started_idx
    ON paper_cycles (session_id, started_at DESC);

CREATE INDEX IF NOT EXISTS markets_symbol_idx
    ON markets (symbol);

CREATE INDEX IF NOT EXISTS orders_market_idx
    ON orders (market_id);

COMMIT;
