package strategy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestDefaultParamsValidate(t *testing.T) {
	if err := DefaultParams().Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
}

func TestValidateRejectsOutOfBounds(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Params)
	}{
		{"ttl too small", func(p *Params) { p.Scanner.TTLMs = 10 }},
		{"depth too big", func(p *Params) { p.Scanner.Depth = 5000 }},
		{"workers zero", func(p *Params) { p.Scanner.Workers = 0 }},
		{"min input zero", func(p *Params) { p.Scanner.MinInput = decimal.Zero }},
		{"negative edge", func(p *Params) { p.Risk.MinNetEdgeBps = decimal.NewFromInt(-1) }},
		{"utilization > 1", func(p *Params) { p.Risk.MaxCapitalUtilization = decimal.NewFromInt(2) }},
		{"drawdown = 1", func(p *Params) { p.Risk.MaxDrawdown = decimal.NewFromInt(1) }},
		{"trade size zero", func(p *Params) { p.Risk.MaxTradeSize = decimal.Zero }},
		{"bad severity", func(p *Params) { p.Notifications.Routes = map[string][]string{"BAD": {"web"}} }},
		{"bad channel", func(p *Params) { p.Notifications.Routes = map[string][]string{"INFO": {"fax"}} }},
	}
	for _, tc := range cases {
		p := DefaultParams()
		tc.mut(&p)
		if err := p.Validate(); err == nil {
			t.Errorf("%s: expected validation error", tc.name)
		}
	}
}

func TestDiffAndSections(t *testing.T) {
	a := DefaultParams()
	b := DefaultParams()
	b.Risk.MinNetEdgeBps = decimal.NewFromInt(9)
	b.Scanner.Depth = 100

	diff, err := Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff) != 2 {
		t.Fatalf("diff size = %d (%v), want 2", len(diff), diff)
	}
	if _, ok := diff["risk.min_net_edge_bps"]; !ok {
		t.Fatalf("missing risk.min_net_edge_bps in %v", diff)
	}
	if _, ok := diff["scanner.depth"]; !ok {
		t.Fatalf("missing scanner.depth in %v", diff)
	}
	sections := TopLevelSections(diff)
	if len(sections) != 2 || sections[0] != "risk" || sections[1] != "scanner" {
		t.Fatalf("sections = %v", sections)
	}

	empty, err := Diff(a, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("identical params diff = %v", empty)
	}
}

func TestServiceLifecycle(t *testing.T) {
	ctx := context.Background()
	var audits []AuditEvent
	svc := NewService(NewMemoryStore(), testLogger(), func(_ context.Context, ev AuditEvent) {
		audits = append(audits, ev)
	})

	seeded, err := svc.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if seeded.Version != 1 {
		t.Fatalf("seed version = %d, want 1", seeded.Version)
	}

	var swaps []int64
	svc.Subscribe(func(s Snapshot) { swaps = append(swaps, s.Version) })
	if len(swaps) != 1 || swaps[0] != 1 {
		t.Fatalf("subscribe must deliver current snapshot; got %v", swaps)
	}

	// Apply a change.
	p := DefaultParams()
	p.Risk.MinNetEdgeBps = decimal.NewFromInt(8)
	v2, err := svc.Apply(ctx, "u-admin", "web", p)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Version != 2 || v2.ParentVersion != 1 || v2.CreatedBy != "u-admin" {
		t.Fatalf("v2 = %+v", v2)
	}
	if got := svc.Current().Params.Risk.MinNetEdgeBps; !got.Equal(decimal.NewFromInt(8)) {
		t.Fatalf("current edge = %s, want 8", got)
	}

	// No-change apply is rejected.
	if _, err := svc.Apply(ctx, "u-admin", "web", p); !errors.Is(err, ErrNoChange) {
		t.Fatalf("no-change apply err = %v", err)
	}

	// Invalid payload is rejected with ErrInvalid.
	bad := DefaultParams()
	bad.Scanner.Workers = 0
	if _, err := svc.Apply(ctx, "u-admin", "web", bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid apply err = %v", err)
	}

	// Rollback to v1 creates v3 with v1's payload.
	v3, err := svc.Rollback(ctx, "u-admin", "web", 1)
	if err != nil {
		t.Fatal(err)
	}
	if v3.Version != 3 {
		t.Fatalf("rollback version = %d, want 3", v3.Version)
	}
	if got := svc.Current().Params.Risk.MinNetEdgeBps; !got.Equal(decimal.NewFromInt(5)) {
		t.Fatalf("rolled-back edge = %s, want 5", got)
	}
	if _, err := svc.Rollback(ctx, "u-admin", "web", 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rollback missing err = %v", err)
	}

	// Swaps: initial + v2 + v3.
	if len(swaps) != 3 || swaps[2] != 3 {
		t.Fatalf("swaps = %v", swaps)
	}
	// Audits: v2 apply + v3 rollback (seed has no audit sink call — it
	// runs through Insert directly, not applyLocked).
	if len(audits) != 2 || audits[0].Action != "config.apply" || audits[1].Action != "config.rollback" {
		t.Fatalf("audits = %+v", audits)
	}

	list, err := svc.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || !list[0].Active || list[0].Version != 3 || list[1].Active {
		t.Fatalf("list = %+v", list)
	}
}

func TestConversions(t *testing.T) {
	p := DefaultParams()
	cfg := p.ScannerConfig(7)
	if cfg.ConfigVersion != 7 {
		t.Fatalf("config version = %d", cfg.ConfigVersion)
	}
	if cfg.TTL != 400*time.Millisecond || cfg.MaxBookAge != 2*time.Second {
		t.Fatalf("durations = %v / %v", cfg.TTL, cfg.MaxBookAge)
	}
	if cfg.Search.GridPoints != p.Scanner.GridPoints || cfg.Workers != 2 {
		t.Fatalf("cfg = %+v", cfg)
	}
	res := p.RiskResolver()
	if !res.Global.MinNetEdgeBps.Equal(decimal.NewFromInt(5)) {
		t.Fatalf("resolver edge = %s", res.Global.MinNetEdgeBps)
	}
	if res.Global.MaxBookAge != 1500*time.Millisecond || res.Global.MaxBookAgeSpread != 750*time.Millisecond {
		t.Fatalf("resolver ages = %v / %v", res.Global.MaxBookAge, res.Global.MaxBookAgeSpread)
	}
	if res.Global.OpportunityTTL != 400*time.Millisecond {
		t.Fatalf("resolver ttl = %v", res.Global.OpportunityTTL)
	}
}

func TestParamsJSONRoundTripUsesQuotedDecimals(t *testing.T) {
	raw, err := json.Marshal(DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	// Decimals must serialize as quoted strings, never bare floats.
	if want := `"min_net_edge_bps":"5"`; !json.Valid(raw) || !strings.Contains(string(raw), want) {
		t.Fatalf("payload missing %s: %s", want, raw)
	}
	var back Params
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	diff, err := Diff(DefaultParams(), back)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff) != 0 {
		t.Fatalf("round trip changed payload: %v", diff)
	}
}
