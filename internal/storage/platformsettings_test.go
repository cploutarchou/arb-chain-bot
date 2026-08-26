package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/platform"
)

func testPlatformSettings() platform.Settings {
	return platform.Settings{
		Venues: map[string]platform.VenueSettings{
			"binance": {
				Enabled: true, PaperEnabled: true,
				Symbols:        []string{"BTCUSDT", "ETHUSDT", "ETHBTC"},
				StartingAssets: []string{"USDT"},
				Fees: platform.FeeSettings{
					MakerBps: decimal.NewFromInt(10),
					TakerBps: decimal.NewFromInt(10),
				},
			},
		},
		Paper:    platform.PaperSettings{Balances: map[string]string{"USDT": "10000"}},
		Telegram: platform.TelegramSettings{Allowlist: []int64{111}},
	}
}

func TestPlatformSettingsRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ps := s.PlatformSettings()

	if _, ok, err := ps.Active(ctx); err != nil || ok {
		t.Fatalf("empty table Active = ok=%v err=%v", ok, err)
	}

	// Seed (system actor -> NULL created_by, NULL parent).
	seed := testPlatformSettings()
	payload, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	v1, _, err := ps.Insert(ctx, "", payload, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// A real user (FK to users) applies a change.
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, role)
		VALUES ('u1','platform@example.test','Platform','x','ADMIN')`); err != nil {
		t.Fatal(err)
	}
	next := seed.Clone()
	v := next.Venues["binance"]
	v.Fees.TakerBps = decimal.NewFromInt(7)
	next.Venues["binance"] = v
	payload2, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	diff, _ := json.Marshal(map[string]any{
		"venues.binance.fees.taker_bps": map[string]string{"old": "10", "new": "7"},
	})
	v2, _, err := ps.Insert(ctx, "u1", payload2, diff, v1)
	if err != nil {
		t.Fatal(err)
	}
	if v2 <= v1 {
		t.Fatalf("versions not increasing: %d then %d", v1, v2)
	}

	active, ok, err := ps.Active(ctx)
	if err != nil || !ok {
		t.Fatalf("Active = ok=%v err=%v", ok, err)
	}
	if active.Version != v2 || active.CreatedBy != "u1" || active.ParentVer != v1 {
		t.Fatalf("active snapshot = %+v", active)
	}
	if !active.Settings.Venues["binance"].Fees.TakerBps.Equal(decimal.NewFromInt(7)) {
		t.Fatalf("active taker bps = %s", active.Settings.Venues["binance"].Fees.TakerBps)
	}

	got1, err := ps.Get(ctx, v1)
	if err != nil {
		t.Fatal(err)
	}
	if !got1.Settings.Venues["binance"].Fees.TakerBps.Equal(decimal.NewFromInt(10)) {
		t.Fatalf("v1 taker bps = %s, want 10 (immutable history)", got1.Settings.Venues["binance"].Fees.TakerBps)
	}

	if _, err := ps.Get(ctx, 9999); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("missing version error = %v, want ErrNotFound", err)
	}

	list, err := ps.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("List returned %d rows, want 2", len(list))
	}
	if !list[0].Active || list[0].Version != v2 {
		t.Fatalf("List[0] = %+v, want the active v2 row first", list[0])
	}
}

func TestEndPaperSession(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	if err := s.EnsurePaperSession(ctx, "sess-1", "PAPER", map[string]string{"USDT": "10000"}, 1, 0); err != nil {
		t.Fatal(err)
	}

	var endedAt *time.Time
	scan := func() {
		if err := s.Pool.QueryRow(ctx, `SELECT ended_at FROM paper_sessions WHERE id = $1`, "sess-1").Scan(&endedAt); err != nil {
			t.Fatal(err)
		}
	}
	scan()
	if endedAt != nil {
		t.Fatalf("freshly inserted session has ended_at = %v, want NULL", endedAt)
	}

	at := time.Now().UTC().Truncate(time.Microsecond)
	if err := s.EndPaperSession(ctx, "sess-1", at); err != nil {
		t.Fatal(err)
	}
	scan()
	if endedAt == nil || !endedAt.Equal(at) {
		t.Fatalf("ended_at = %v, want %v", endedAt, at)
	}

	// Idempotent: a second call must not overwrite the first ended_at.
	later := at.Add(time.Hour)
	if err := s.EndPaperSession(ctx, "sess-1", later); err != nil {
		t.Fatal(err)
	}
	scan()
	if !endedAt.Equal(at) {
		t.Fatalf("second EndPaperSession call overwrote ended_at: got %v, want %v", endedAt, at)
	}
}

func TestListMarketsAndCatalog(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	markets := []exchange.Market{
		{
			ID:   exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"},
			Base: "BTC", Quote: "USDT", Status: exchange.MarketTrading, Enabled: true,
			Rules: exchange.InstrumentRules{
				QtyMode: exchange.PrecisionStep, QtyStep: decimal.NewFromInt(1),
				PriceMode: exchange.PrecisionStep, PriceTick: decimal.NewFromInt(1),
			},
		},
	}
	if err := s.UpsertMarkets(ctx, markets); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListMarkets(ctx, "binance")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID.Symbol != "BTCUSDT" || got[0].Base != "BTC" {
		t.Fatalf("ListMarkets = %+v", got)
	}

	// Catalog wraps ListMarkets and reports ErrCatalogNotReady on an
	// empty result (a venue with no rows yet, not a venue with zero
	// markets), never confusing that with an unknown-symbol error.
	cat := Catalog{S: s}
	if _, err := cat.Markets(ctx, "okx"); !errors.Is(err, platform.ErrCatalogNotReady) {
		t.Fatalf("Catalog.Markets for an empty venue = %v, want ErrCatalogNotReady", err)
	}
	out, err := cat.Markets(ctx, "binance")
	if err != nil || len(out) != 1 {
		t.Fatalf("Catalog.Markets(binance) = %v, %v", out, err)
	}
}
