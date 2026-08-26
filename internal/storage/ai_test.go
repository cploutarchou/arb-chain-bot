package storage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/ai"
)

func TestAIStoreRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, table := range []string{"ai_recommendations", "ai_analyses"} {
		if _, err := s.Pool.Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatal(err)
		}
	}
	as := s.AI()

	res := ai.AnalysisResult{
		ID: "an-1", Kind: ai.KindHourlyHealth, At: t0,
		Model: "fake-advisor", PromptVersion: "v1", ConfigVersion: 1,
		Summary: "quiet hour", Raw: json.RawMessage(`{"summary":"quiet hour"}`),
	}
	if err := as.InsertAnalysis(ctx, res, json.RawMessage(`{"kind":"hourly_health"}`)); err != nil {
		t.Fatal(err)
	}
	rec := ai.Recommendation{
		ID: "rec-1", AnalysisID: "an-1", CreatedAt: t0,
		Scope: "global", Parameter: "scanner.ttl_ms",
		CurrentValue: "400", RecommendedValue: "500",
		Evidence: "0 qualified", Reason: "latency", Confidence: "0.6",
		ExpectedEffect: "more sims", Risks: "staler quotes",
		ExpiresAt: t0.Add(72 * time.Hour), Status: "proposed",
	}
	if err := as.InsertRecommendation(ctx, rec); err != nil {
		t.Fatal(err)
	}
	// Decision by an unknown actor persists NULL decided_by (FK-safe).
	if err := as.UpdateRecommendationStatus(ctx, "rec-1", "rejected", "telegram:100", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var (
		status    string
		decidedBy *string
	)
	if err := s.Pool.QueryRow(ctx,
		`SELECT status, decided_by FROM ai_recommendations WHERE id = 'rec-1'`).
		Scan(&status, &decidedBy); err != nil {
		t.Fatal(err)
	}
	if status != "rejected" || decidedBy != nil {
		t.Fatalf("row = %s / %v", status, decidedBy)
	}
	var kinds int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM ai_analyses WHERE kind = 'hourly_health'`).Scan(&kinds); err != nil {
		t.Fatal(err)
	}
	if kinds != 1 {
		t.Fatalf("analyses = %d", kinds)
	}
}
