package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/quality"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

func discardTestLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestStrategyConfigsRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.Pool.Exec(ctx, "DELETE FROM strategy_configs"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, "DELETE FROM audit_events"); err != nil {
		t.Fatal(err)
	}
	cs := s.StrategyConfigs()

	if _, ok, err := cs.Active(ctx); err != nil || ok {
		t.Fatalf("empty table Active = ok=%v err=%v", ok, err)
	}

	// Seed (system actor → NULL created_by, NULL parent).
	seed, _ := json.Marshal(strategy.DefaultParams())
	v1, _, err := cs.Insert(ctx, "", seed, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Change by a real user (FK to users).
	hash := "x"
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, role)
		VALUES ('u1','cfg@example.test','Cfg',$1,'ADMIN')`, hash); err != nil {
		t.Fatal(err)
	}
	p := strategy.DefaultParams()
	p.Risk.MinNetEdgeBps = decimal.NewFromInt(9)
	payload, _ := json.Marshal(p)
	diff, _ := json.Marshal(map[string]any{"risk.min_net_edge_bps": map[string]string{"old": "5", "new": "9"}})
	v2, _, err := cs.Insert(ctx, "u1", payload, diff, v1)
	if err != nil {
		t.Fatal(err)
	}
	if v2 <= v1 {
		t.Fatalf("versions not increasing: %d then %d", v1, v2)
	}

	active, ok, err := cs.Active(ctx)
	if err != nil || !ok {
		t.Fatalf("Active err=%v ok=%v", err, ok)
	}
	if active.Version != v2 || active.CreatedBy != "u1" || active.ParentVersion != v1 {
		t.Fatalf("active = %+v", active)
	}
	if !active.Params.Risk.MinNetEdgeBps.Equal(decimal.NewFromInt(9)) {
		t.Fatalf("active edge = %s", active.Params.Risk.MinNetEdgeBps)
	}

	old, err := cs.Get(ctx, v1)
	if err != nil || old.CreatedBy != "" || old.ParentVersion != 0 {
		t.Fatalf("v1 = %+v err=%v", old, err)
	}
	if _, err := cs.Get(ctx, 10_000); !errors.Is(err, strategy.ErrNotFound) {
		t.Fatalf("missing version err = %v", err)
	}

	list, err := cs.List(ctx, 10)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %+v err=%v", list, err)
	}
	if !list[0].Active || list[0].Version != v2 || list[1].Active {
		t.Fatalf("list order/active = %+v", list)
	}
	if len(list[0].Diff) == 0 {
		t.Fatal("v2 diff missing in list")
	}

	// Service on the real store: apply + rollback end to end with audit.
	svc := strategy.NewService(cs, discardTestLogger(), func(ctx context.Context, ev strategy.AuditEvent) {
		if err := s.InsertAuditEvent(ctx, AuditRow{
			ID: "audit-" + ev.EntityID, Actor: ev.Actor, Source: ev.Source,
			Action: ev.Action, Entity: ev.Entity, EntityID: ev.EntityID,
			Before: ev.Before, After: ev.After,
		}); err != nil {
			t.Errorf("audit insert: %v", err)
		}
	})
	if _, err := svc.Load(ctx); err != nil {
		t.Fatal(err)
	}
	rolled, err := svc.Rollback(ctx, "u1", "web", v1)
	if err != nil {
		t.Fatal(err)
	}
	if rolled.Version <= v2 || rolled.ParentVersion != v2 {
		t.Fatalf("rollback = %+v", rolled)
	}
	if !rolled.Params.Risk.MinNetEdgeBps.Equal(decimal.NewFromInt(5)) {
		t.Fatalf("rolled edge = %s", rolled.Params.Risk.MinNetEdgeBps)
	}
	var audits int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events WHERE entity = 'strategy_config'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("audit rows = %d, want 1", audits)
	}
}

func TestQualitySamples(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	// Reuse the persisted fixture from the opportunity/cycle test by
	// inserting a minimal chain here (independent test data).
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO exchanges (id, name) VALUES ('binance','binance') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO triangles (id, exchange_id, starting_asset, legs, canonical_key)
		VALUES ('tri-q','binance','USDT','[]'::jsonb,'tri-q') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO opportunities (id, exchange_id, triangle_id, status, starting_asset, starting_amount, legs, detected_at)
		VALUES ('op-q','binance','tri-q','QUALIFIED','USDT',1000,'[]'::jsonb, now() - interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO paper_sessions (id, mode, started_at, starting_balances, config_version, seed)
		VALUES ('sess-q','PAPER', now(), '{}'::jsonb, 1, 1) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO paper_cycles (id, session_id, opportunity_id, outcome, pnl_amount, pnl_asset, slippage_bps, started_at, settled_at)
		VALUES
		('cyc-q1','sess-q','op-q','ALL_FILLED', 2.5,'USDT',-1.2, now() - interval '50 minutes', now() - interval '49 minutes'),
		('cyc-q2','sess-q','op-q','TIMEOUT',   -0.8,'USDT',NULL, now() - interval '40 minutes', now() - interval '39 minutes')`); err != nil {
		t.Fatal(err)
	}

	samples, err := s.QualitySamples(ctx, time.Now().Add(-24*time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var found *quality.Sample
	for i := range samples {
		if samples[i].TriangleID == "tri-q" {
			found = &samples[i]
		}
	}
	if found == nil {
		t.Fatalf("tri-q missing: %+v", samples)
	}
	if found.Cycles != 2 || found.Successes != 1 {
		t.Fatalf("sample counts = %+v", found)
	}
	if !found.NetPnL.Equal(decimal.RequireFromString("1.7")) {
		t.Fatalf("net = %s", found.NetPnL)
	}
	if !found.WorstLoss.Equal(decimal.RequireFromString("-0.8")) {
		t.Fatalf("worst = %s", found.WorstLoss)
	}
	if found.EdgeWindows != 1 || found.WindowHours != 24 {
		t.Fatalf("edge/windows = %d/%d", found.EdgeWindows, found.WindowHours)
	}
	scored := quality.Rank(samples, quality.Config{})
	if len(scored) == 0 || scored[0].Total <= 0 {
		t.Fatalf("rank = %+v", scored)
	}
}
