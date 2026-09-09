-- Cycle close reason (audit ui-ux F6, roadmap T-pending): the settled
-- CycleResult has always carried Reason (why an adverse outcome
-- happened — which book went stale, which rule refused the leg, what
-- interrupted the cycle), but only the outcome code was persisted, so
-- the console's cycles table could show WHAT happened and never WHY.
-- Nullable: rows written before this migration have no reason and the
-- API omits the field rather than fabricating one.
ALTER TABLE paper_cycles ADD COLUMN IF NOT EXISTS reason TEXT;
