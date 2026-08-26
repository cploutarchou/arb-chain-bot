package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/shopspring/decimal"

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
