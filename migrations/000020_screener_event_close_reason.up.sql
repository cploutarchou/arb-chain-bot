-- Store why a screener event closed (docs/audit/scanner-suite-audit.md
-- X3, roadmap P1-22). The evaluator has always known the reason — the
-- signal that failed, the stale-hold timeout, the lane disappearing —
-- and until now only the notification text carried it. NULL for events
-- closed before this column existed and for events still open.
ALTER TABLE screener_events ADD COLUMN IF NOT EXISTS close_reason TEXT;
