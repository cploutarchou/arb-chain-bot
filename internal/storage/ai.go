package storage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/ai"
)

// AIStore adapts the store to ai.Store. decided_by carries FK'd
// platform user IDs; actors without a users row persist as NULL (the
// audit trail records the concrete actor).
type AIStore struct{ s *Store }

func (s *Store) AI() *AIStore { return &AIStore{s: s} }

func (a *AIStore) InsertAnalysis(ctx context.Context, res ai.AnalysisResult, input json.RawMessage) error {
	_, err := a.s.Pool.Exec(ctx, `
		INSERT INTO ai_analyses (id, created_at, kind, model, prompt_version, config_version, input, output, summary)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		res.ID, res.At, string(res.Kind), res.Model, res.PromptVersion,
		res.ConfigVersion, input, []byte(res.Raw), res.Summary)
	return err
}

func (a *AIStore) InsertRecommendation(ctx context.Context, rec ai.Recommendation) error {
	conf, err := decimal.NewFromString(rec.Confidence)
	if err != nil {
		return err
	}
	evidence, _ := json.Marshal(map[string]string{"evidence": rec.Evidence})
	_, err = a.s.Pool.Exec(ctx, `
		INSERT INTO ai_recommendations
			(id, created_at, scope, parameter, current_value, recommended_value,
			 evidence, reason, confidence, expected_effect, risks, expires_at, status, analysis_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		rec.ID, rec.CreatedAt, rec.Scope, rec.Parameter, rec.CurrentValue,
		rec.RecommendedValue, evidence, rec.Reason, conf,
		rec.ExpectedEffect, rec.Risks, rec.ExpiresAt, rec.Status, rec.AnalysisID)
	return err
}

func (a *AIStore) UpdateRecommendationStatus(ctx context.Context, id, status, decidedBy string, decidedAt time.Time) error {
	_, err := a.s.Pool.Exec(ctx, `
		UPDATE ai_recommendations SET status = $2,
			decided_by = (SELECT id FROM users WHERE id = $3),
			decided_at = $4
		WHERE id = $1`,
		id, status, decidedBy, decidedAt)
	return err
}
