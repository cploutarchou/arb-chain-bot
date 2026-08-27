package platform

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testCfg() config.Bootstrap {
	return config.Bootstrap{
		Mode:           config.ModePaper,
		Symbols:        []string{"BTCUSDT", "ETHUSDT", "ETHBTC"},
		StartingAssets: []string{"USDT"},
		PaperBalance:   "10000",
	}
}

func TestServiceLoadSeeds(t *testing.T) {
	svc := NewService(NewMemoryStore(), testLogger(), nil)
	snap, err := svc.Load(context.Background(), testCfg())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Version != 1 {
		t.Fatalf("first load version = %d, want 1", snap.Version)
	}
	if got := svc.Current().Version; got != 1 {
		t.Fatalf("Current().Version = %d, want 1", got)
	}
}

func TestServiceApplyAndSubscribe(t *testing.T) {
	svc := NewService(NewMemoryStore(), testLogger(), nil)
	if _, err := svc.Load(context.Background(), testCfg()); err != nil {
		t.Fatal(err)
	}

	var seen []Snapshot
	svc.Subscribe(func(s Snapshot) { seen = append(seen, s) })
	if len(seen) != 1 {
		t.Fatalf("Subscribe must deliver the current snapshot immediately, got %d", len(seen))
	}

	next := validSettings()
	snap, err := svc.Apply(context.Background(), "alice", "web", next)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Version != 2 {
		t.Fatalf("applied version = %d, want 2", snap.Version)
	}
	if len(seen) != 2 {
		t.Fatalf("Subscribe must fire on Apply, got %d deliveries", len(seen))
	}

	// Applying the identical document again is a no-op error.
	if _, err := svc.Apply(context.Background(), "alice", "web", next); !errors.Is(err, ErrNoChange) {
		t.Fatalf("expected ErrNoChange, got %v", err)
	}
}

func TestServiceApplyAuthorizeInLock(t *testing.T) {
	svc := NewService(NewMemoryStore(), testLogger(), nil)
	if _, err := svc.Load(context.Background(), testCfg()); err != nil {
		t.Fatal(err)
	}
	refused := errors.New("nope")
	_, err := svc.ApplyAuthorized(context.Background(), "bob", "web", validSettings(), func(diff map[string]strategy.Change) error {
		if len(diff) == 0 {
			t.Fatal("authorize must see a non-empty diff")
		}
		return refused
	})
	if !errors.Is(err, refused) {
		t.Fatalf("expected the authorize error, got %v", err)
	}
	// Refusal must not have written a new version.
	if got := svc.Current().Version; got != 1 {
		t.Fatalf("refused apply must not advance the version, got %d", got)
	}
}

func TestServiceRollback(t *testing.T) {
	svc := NewService(NewMemoryStore(), testLogger(), nil)
	seedSnap, err := svc.Load(context.Background(), testCfg())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Apply(context.Background(), "alice", "web", validSettings()); err != nil {
		t.Fatal(err)
	}
	rolled, err := svc.Rollback(context.Background(), "alice", "web", seedSnap.Version)
	if err != nil {
		t.Fatal(err)
	}
	if rolled.Version != 3 {
		t.Fatalf("rollback version = %d, want 3 (append-only)", rolled.Version)
	}
	if rolled.ParentVer != 2 {
		t.Fatalf("rollback parent = %d, want 2 (the version rolled FROM)", rolled.ParentVer)
	}
}

func TestServicePlanDiffAndList(t *testing.T) {
	svc := NewService(NewMemoryStore(), testLogger(), nil)
	if _, err := svc.Load(context.Background(), testCfg()); err != nil {
		t.Fatal(err)
	}
	diff, err := svc.PlanDiff(validSettings())
	if err != nil {
		t.Fatal(err)
	}
	if len(diff) == 0 {
		t.Fatal("expected a non-empty diff")
	}
	if _, err := svc.Apply(context.Background(), "alice", "web", validSettings()); err != nil {
		t.Fatal(err)
	}
	list, err := svc.List(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("List returned %d rows, want 2", len(list))
	}
}

// TestServiceApplyRejectsUnbuildableTopology locks in D8: a settings
// version that cannot build a topology is rejected AT APPLY TIME, not
// discovered for the first time when the engine restarts.
func TestServiceApplyRejectsUnbuildableTopology(t *testing.T) {
	svc := NewService(NewMemoryStore(), testLogger(), nil)
	svc.Catalog = triangleCatalog()
	if _, err := svc.Load(context.Background(), testCfg()); err != nil {
		t.Fatal(err)
	}
	bad := validSettings()
	v := bad.Venues["binance"]
	v.Symbols = []string{"BTCUSDT", "ETHUSDT", "DOGEUSDT"}
	bad.Venues["binance"] = v
	_, err := svc.Apply(context.Background(), "alice", "web", bad)
	if !errors.Is(err, ErrUnknownSymbol) {
		t.Fatalf("expected ErrUnknownSymbol from the apply-time dry-run, got %v", err)
	}
	if got := svc.Current().Version; got != 1 {
		t.Fatalf("a rejected apply must not advance the version, got %d", got)
	}
}

// TestServiceRollbackRejectsUnbuildableTopology: a rollback to a version
// whose symbols were since delisted must fail the same way as Apply.
func TestServiceRollbackRejectsUnbuildableTopology(t *testing.T) {
	cat := triangleCatalog()
	svc := NewService(NewMemoryStore(), testLogger(), nil)
	svc.Catalog = cat
	seed, err := svc.Load(context.Background(), testCfg())
	if err != nil {
		t.Fatal(err)
	}
	// Version 1 (the seed) is valid against cat today. Simulate the
	// catalog shrinking (e.g. ETHBTC delisted) before the rollback.
	svc.Catalog = fakeCatalog{markets: map[exchange.ExchangeID][]exchange.Market{
		"binance": {
			market("binance", "BTCUSDT", "BTC", "USDT", exchange.MarketTrading),
			market("binance", "ETHUSDT", "ETH", "USDT", exchange.MarketTrading),
		},
	}}
	if _, err := svc.Rollback(context.Background(), "alice", "web", seed.Version); err == nil {
		t.Fatal("expected the rollback to fail its apply-time dry-run against the shrunk catalog")
	}
}

// TestServicePaperModeRequiresPaperEnabled: the paper_enabled rule now
// reads platform.mode from the document itself (T-059 §2.2), so the
// apply path refuses the combination without any injected process mode.
func TestServicePaperModeRequiresPaperEnabled(t *testing.T) {
	svc := NewService(NewMemoryStore(), testLogger(), nil)
	if _, err := svc.Load(context.Background(), testCfg()); err != nil {
		t.Fatal(err)
	}
	bad := validSettings()
	bad.Platform.Mode = config.ModePaper
	v := bad.Venues["binance"]
	v.PaperEnabled = false
	bad.Venues["binance"] = v
	if _, err := svc.Apply(context.Background(), "alice", "web", bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid from Validate, got %v", err)
	}
}
