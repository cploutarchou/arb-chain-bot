package graph

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

const ex = exchange.ExchangeID("binance")

func mkt(sym string, base, quote exchange.Asset) exchange.Market {
	step := decimal.RequireFromString("0.001")
	return exchange.Market{
		ID:      exchange.MarketID{Exchange: ex, Symbol: exchange.Symbol(sym)},
		Base:    base,
		Quote:   quote,
		Status:  exchange.MarketTrading,
		Enabled: true,
		Rules: exchange.InstrumentRules{
			QtyMode: exchange.PrecisionStep, QtyStep: step,
			PriceMode: exchange.PrecisionStep, PriceTick: step,
		},
	}
}

func standardMarkets() []exchange.Market {
	return []exchange.Market{
		mkt("BTCUSDT", "BTC", "USDT"),
		mkt("ETHUSDT", "ETH", "USDT"),
		mkt("ETHBTC", "ETH", "BTC"),
	}
}

func TestEnumerateBothDirectionsFromOneStart(t *testing.T) {
	topo := Build(ex, standardMarkets(), []exchange.Asset{"USDT"})
	if len(topo.Triangles) != 2 {
		t.Fatalf("triangles = %d, want 2 (both directions)", len(topo.Triangles))
	}
	for _, tri := range topo.Triangles {
		if err := tri.Validate(); err != nil {
			t.Fatalf("invalid triangle %s: %v", tri.ID, err)
		}
		if tri.Start != "USDT" {
			t.Fatalf("start = %s", tri.Start)
		}
	}

	byID := map[string]Triangle{}
	for _, tri := range topo.Triangles {
		byID[tri.ID] = tri
	}
	// Direction 1: USDT→BTC (buy BTCUSDT) → ETH (buy ETHBTC) → USDT (sell ETHUSDT).
	t1, ok := byID["binance|USDT|BTCUSDT>ETHBTC>ETHUSDT"]
	if !ok {
		t.Fatalf("missing direction 1; have %v", keys(byID))
	}
	wantSides := []exchange.Side{exchange.SideBuy, exchange.SideBuy, exchange.SideSell}
	for i, s := range wantSides {
		if t1.Legs[i].Side != s {
			t.Fatalf("dir1 leg%d side = %s, want %s", i+1, t1.Legs[i].Side, s)
		}
	}
	// Direction 2: USDT→ETH (buy ETHUSDT) → BTC (sell ETHBTC) → USDT (sell BTCUSDT).
	t2, ok := byID["binance|USDT|ETHUSDT>ETHBTC>BTCUSDT"]
	if !ok {
		t.Fatalf("missing direction 2; have %v", keys(byID))
	}
	wantSides = []exchange.Side{exchange.SideBuy, exchange.SideSell, exchange.SideSell}
	for i, s := range wantSides {
		if t2.Legs[i].Side != s {
			t.Fatalf("dir2 leg%d side = %s, want %s", i+1, t2.Legs[i].Side, s)
		}
	}
}

func TestMultipleStartsEnumerateSeparately(t *testing.T) {
	topo := Build(ex, standardMarkets(), []exchange.Asset{"USDT", "BTC", "ETH"})
	// Each of the 3 assets is a configured start of the same underlying
	// 3-market cycle, two directions each: 6 triangles total.
	if len(topo.Triangles) != 6 {
		t.Fatalf("triangles = %d, want 6", len(topo.Triangles))
	}
}

func TestUntradeableAndSelfLoopRejected(t *testing.T) {
	ms := standardMarkets()
	halted := mkt("ETHBTC", "ETH", "BTC")
	halted.Status = exchange.MarketHalted
	ms[2] = halted
	ms = append(ms, mkt("WEIRD", "USDT", "USDT")) // self loop

	topo := Build(ex, ms, []exchange.Asset{"USDT"})
	if len(topo.Triangles) != 0 {
		t.Fatalf("triangles = %d, want 0 (cycle broken by halted market)", len(topo.Triangles))
	}
	if topo.Rejected.Untradeable != 1 || topo.Rejected.SelfLoop != 1 {
		t.Fatalf("rejections = %+v", topo.Rejected)
	}
}

func TestBadRulesRejected(t *testing.T) {
	ms := standardMarkets()
	ms[0].Rules = exchange.InstrumentRules{} // unusable
	topo := Build(ex, ms, []exchange.Asset{"USDT"})
	if len(topo.Triangles) != 0 || topo.Rejected.BadRules != 1 {
		t.Fatalf("triangles=%d rejected=%+v", len(topo.Triangles), topo.Rejected)
	}
}

func TestByMarketIndexCoversEveryLeg(t *testing.T) {
	topo := Build(ex, standardMarkets(), []exchange.Asset{"USDT"})
	for _, m := range standardMarkets() {
		idxs := topo.AffectedBy(m.ID)
		if len(idxs) != 2 { // every market participates in both directions
			t.Fatalf("market %s affects %d triangles, want 2", m.ID, len(idxs))
		}
		for _, i := range idxs {
			found := false
			for _, l := range topo.Triangles[i].Legs {
				if l.Market == m.ID {
					found = true
				}
			}
			if !found {
				t.Fatalf("index lists triangle %s not containing %s", topo.Triangles[i].ID, m.ID)
			}
		}
	}
	if topo.AffectedBy(exchange.MarketID{Exchange: ex, Symbol: "NOPE"}) != nil {
		t.Fatal("unknown market must affect nothing")
	}
}

func TestForeignExchangeMarketsIgnored(t *testing.T) {
	ms := standardMarkets()
	foreign := mkt("ETHBTC", "ETH", "BTC")
	foreign.ID.Exchange = "okx"
	ms[2] = foreign // cycle now spans two venues — forbidden by construction
	topo := Build(ex, ms, []exchange.Asset{"USDT"})
	if len(topo.Triangles) != 0 {
		t.Fatalf("cross-exchange cycle enumerated: %d", len(topo.Triangles))
	}
}

func TestLargerUniverseEveryTriangleValid(t *testing.T) {
	ms := []exchange.Market{
		mkt("BTCUSDT", "BTC", "USDT"), mkt("ETHUSDT", "ETH", "USDT"),
		mkt("ETHBTC", "ETH", "BTC"), mkt("SOLUSDT", "SOL", "USDT"),
		mkt("SOLBTC", "SOL", "BTC"), mkt("SOLETH", "SOL", "ETH"),
		mkt("BTCUSDC", "BTC", "USDC"), mkt("ETHUSDC", "ETH", "USDC"),
		mkt("USDCUSDT", "USDC", "USDT"),
	}
	topo := Build(ex, ms, []exchange.Asset{"USDT", "USDC"})
	if len(topo.Triangles) == 0 {
		t.Fatal("expected triangles")
	}
	ids := map[string]struct{}{}
	for _, tri := range topo.Triangles {
		if err := tri.Validate(); err != nil {
			t.Fatalf("%s: %v", tri.ID, err)
		}
		if _, dup := ids[tri.ID]; dup {
			t.Fatalf("duplicate id %s", tri.ID)
		}
		ids[tri.ID] = struct{}{}
	}
	// USDT-start triangles through USDC leg exist (stablecoin cycles).
	if _, ok := ids["binance|USDT|USDCUSDT>BTCUSDC>BTCUSDT"]; !ok {
		t.Fatalf("expected stablecoin triangle; ids: %v", keys(ids))
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func BenchmarkBuildTopology(b *testing.B) {
	// Synthetic universe: 40 assets fully crossed against 3 quotes.
	quotes := []exchange.Asset{"USDT", "USDC", "BTC"}
	// Quote-to-quote crosses close the cycles (base/X + base/Y + Y/X).
	ms := []exchange.Market{
		mkt("BTCUSDT", "BTC", "USDT"),
		mkt("BTCUSDC", "BTC", "USDC"),
		mkt("USDCUSDT", "USDC", "USDT"),
	}
	for i := 0; i < 40; i++ {
		base := exchange.Asset(string(rune('A'+i%26)) + string(rune('A'+(i/26)%26)) + "X")
		for _, q := range quotes {
			if base == q {
				continue
			}
			ms = append(ms, mkt(string(base)+string(q), base, q))
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		topo := Build(ex, ms, []exchange.Asset{"USDT", "USDC"})
		if len(topo.Triangles) == 0 {
			b.Fatal("no triangles")
		}
	}
}
