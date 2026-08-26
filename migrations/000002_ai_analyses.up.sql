-- AI analysis results (T-036). Inputs are the typed summaries the
-- prompt was built from, so every analysis is reproducible from this
-- row + the versioned prompt template. Output is the validated JSON.
CREATE TABLE ai_analyses (
    id             TEXT PRIMARY KEY,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    kind           TEXT NOT NULL CHECK (kind IN ('hourly_health','daily_performance','weekly_parameters')),
    model          TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    config_version BIGINT,
    input          JSONB NOT NULL,
    output         JSONB NOT NULL,
    summary        TEXT
);
CREATE INDEX ai_analyses_kind_idx ON ai_analyses(kind, created_at DESC);

-- Link recommendations to their producing analysis.
ALTER TABLE ai_recommendations ADD COLUMN analysis_id TEXT REFERENCES ai_analyses(id);
