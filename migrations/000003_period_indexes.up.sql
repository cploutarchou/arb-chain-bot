-- Audit P2-5: reports and quality-score period queries filter
-- paper_cycles on started_at; without this index every request scans the
-- table (user-triggerable via /api/v1/triangles/quality?hours=720).
CREATE INDEX paper_cycles_started_idx ON paper_cycles (started_at DESC);
