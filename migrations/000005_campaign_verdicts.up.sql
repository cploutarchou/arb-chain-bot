-- Backend-computed §80 verdict severity (BL-05b): a parallel column to
-- `flags`, one Verdict{text,severity} array per starting asset, so the
-- console renders tone from data instead of grepping report prose.
ALTER TABLE campaign_runs ADD COLUMN verdicts JSONB;
