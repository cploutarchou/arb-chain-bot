package scanner

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func lv(p, q string) orderbook.Level { return orderbook.Level{Price: d(p), Qty: d(q)} }

var t0 = time.Unix(1_700_000_000, 0)

type staticCapital struct{ avail decimal.Decimal }

func (c staticCapital) Balance(exchange.Asset) (decimal.Decimal, decimal.Decimal) {
	return c.avail, decimal.Zero
}
func (staticCapital) TriangleReserved(string) decimal.Decimal { return decimal.Zero }

func stepRules() exchange.InstrumentRules {
	return exchange.InstrumentRules{
		QtyMode: exchange.PrecisionStep, QtyStep: d("0.001"),
		PriceMode: exchange.PrecisionStep, PriceTick: d("0.00000001"),
		// T12: usable rules carry a notional floor, as real Binance
		// symbols do.
		MinNotional: d("5"),
	}
}

func mkt(sym string, base, quote exchange.Asset) exchange.Market {
	return exchange.Market{
		ID:   exchange.MarketID{Exchange: "binance", Symbol: exchange.Symbol(sym)},
		Base: base, Quote: quote,
		Status: exchange.MarketTrading, Enabled: true,
		Rules: stepRules(),
	}
}

// harness builds the full pipeline over the profitable pricing fixture:
// BTCUSDT asks 100x10, ETHBTC asks 0.1x100, ETHUSDT bids 10.2x1000.
func harness(t testing.TB) (*Scanner, *orderbook.Set) {
	t.Helper()
	markets := []exchange.Market{
		mkt("BTCUSDT", "BTC", "USDT"),
		mkt("ETHUSDT", "ETH", "USDT"),
		mkt("ETHBTC", "ETH", "BTC"),
	}
	topo := graph.Build("binance", markets, []exchange.Asset{"USDT"})
	if len(topo.Triangles) != 2 {
		t.Fatalf("topology = %d triangles", len(topo.Triangles))
	}
	books := orderbook.NewSet()
	seed := func(sym string, bids, asks []orderbook.Level, ver int64) {
		id := exchange.MarketID{Exchange: "binance", Symbol: exchange.Symbol(sym)}
		b := orderbook.New(id, 0)
		b.ApplySnapshot(orderbook.DepthEvent{
			Market: id, IsSnapshot: true, FinalUpdateID: ver,
			Bids: bids, Asks: asks, ReceiveTime: t0,
		})
		books.Add(b)
	}
	seed("BTCUSDT", []orderbook.Level{lv("99.9", "10")}, []orderbook.Level{lv("100", "10"), lv("101", "5")}, 1)
	seed("ETHBTC", []orderbook.Level{lv("0.099", "100")}, []orderbook.Level{lv("0.1", "100"), lv("0.11", "100")}, 2)
	seed("ETHUSDT", []orderbook.Level{lv("10.2", "1000")}, []orderbook.Level{lv("10.3", "1000")}, 3)

	rules := map[exchange.MarketID]exchange.InstrumentRules{}
	for _, m := range markets {
		rules[m.ID] = m.Rules
	}
	sched, err := fees.NewSchedule("binance", exchange.FeeInReceived,
		fees.Rate{Maker: d("0.001"), Taker: d("0.001")})
	if err != nil {
		t.Fatal(err)
	}

	var seq atomic.Int64
	s := &Scanner{
		Topo:  topo,
		Books: books,
		Rules: rules,
		Fees:  sched,
		Resolver: risk.Resolver{Global: risk.Limits{
			MinNetEdgeBps:            d("5"),
			MinExpectedProfit:        d("0.5"),
			MaxTradeSize:             d("2000"),
			MaxCapitalUtilization:    d("0.9"),
			MaxConcurrentSimulations: 4,
			MaxBookAge:               10 * time.Second,
			MaxBookAgeSpread:         10 * time.Second,
			MaxPriceImpactBps:        d("500"),
			MinDataQuality:           d("0.5"),
		}},
		Breakers: risk.NewRegistry(nil),
		Capital:  staticCapital{avail: d("10000")},
		Clock:    func() time.Time { return t0.Add(50 * time.Millisecond) },
		IDGen:    func() string { return fmt.Sprintf("op-%d", seq.Add(1)) },
		Cfg: Config{
			ConfigVersion: 1,
			Buffers:       opportunity.Buffers{LatencyBps: d("2"), RiskBps: d("3")},
			TTL:           500 * time.Millisecond,
			MinInput:      d("10"),
			Depth:         50,
			Search:        pricing.DefaultSizeSearch,
			MaxBookAge:    10 * time.Second,
		},
		Out: make(chan Event, 64),
	}
	s.ClockHealthy.Store(true)
	return s, books
}

func drain(s *Scanner) []Event {
	var out []Event
	for {
		select {
		case ev := <-s.Out:
			out = append(out, ev)
		default:
			return out
		}
	}
}

// End-to-end: a book change on BTCUSDT re-prices both directions; the
// profitable direction qualifies with sized economics, the reverse is
// rejected for insufficient edge — with the full check list attached.
func TestEndToEndQualification(t *testing.T) {
	s, _ := harness(t)
	s.EvaluateMarket(exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"})

	events := drain(s)
	if len(events) != 2 {
		t.Fatalf("events = %d", len(events))
	}
	byID := map[string]Event{}
	for _, ev := range events {
		byID[ev.Opportunity.TriangleID] = ev
	}
	win, ok := byID["binance|USDT|BTCUSDT>ETHBTC>ETHUSDT"]
	if !ok {
		t.Fatalf("missing profitable triangle; have %v", keys(byID))
	}
	if win.Opportunity.Status != opportunity.StatusQualified {
		t.Fatalf("winner status = %s reason=%s", win.Opportunity.Status, win.Opportunity.Reason)
	}
	// The sizer deploys up to the 1000-capacity of leg 1 within MaxTradeSize.
	if win.Opportunity.Quote.InputConsumed.LessThan(d("500")) {
		t.Fatalf("sized input = %s", win.Opportunity.Quote.InputConsumed)
	}
	if !win.Opportunity.NetReturnBps.GreaterThan(d("100")) {
		t.Fatalf("net bps = %s", win.Opportunity.NetReturnBps)
	}
	if len(win.Decision.Checks) < 10 {
		t.Fatalf("checks recorded = %d", len(win.Decision.Checks))
	}

	lose, ok := byID["binance|USDT|ETHUSDT>ETHBTC>BTCUSDT"]
	if !ok {
		t.Fatal("missing reverse triangle")
	}
	if lose.Opportunity.Status != opportunity.StatusRejected {
		t.Fatalf("reverse status = %s", lose.Opportunity.Status)
	}
	if lose.Decision.ReasonCode != risk.ReasonMinEdge && lose.Decision.ReasonCode != risk.ReasonMinProfit {
		t.Fatalf("reverse reason = %s", lose.Decision.ReasonCode)
	}
	if s.Stats.Qualified.Load() != 1 || s.Stats.Rejected.Load() != 1 {
		t.Fatalf("stats: qualified=%d rejected=%d",
			s.Stats.Qualified.Load(), s.Stats.Rejected.Load())
	}
}

// A corrupted leg book suppresses evaluation entirely (only HEALTHY books
// create opportunities — SKILL.md §11).
func TestUnhealthyBookSuppressesTriangles(t *testing.T) {
	s, books := harness(t)
	b, _ := books.Get(exchange.MarketID{Exchange: "binance", Symbol: "ETHBTC"})
	b.MarkCorrupted("test gap")

	s.EvaluateMarket(exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"})
	if events := drain(s); len(events) != 0 {
		t.Fatalf("events on corrupted book = %d", len(events))
	}
	if s.Stats.SkippedBooks.Load() == 0 {
		t.Fatal("skip counter untouched")
	}
}

// An open exchange-scope breaker turns qualification into rejection with
// the breaker reason (safe default: do nothing).
func TestBreakerGatesQualification(t *testing.T) {
	s, _ := harness(t)
	s.Breakers.Trip("ws_unstable", "exchange:binance", "storm", t0)

	s.EvaluateMarket(exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"})
	events := drain(s)
	if len(events) != 2 {
		t.Fatalf("events = %d", len(events))
	}
	for _, ev := range events {
		if ev.Opportunity.Status != opportunity.StatusRejected || ev.Decision.ReasonCode != risk.ReasonBreakerOpen {
			t.Fatalf("breaker not gating: %s/%s", ev.Opportunity.Status, ev.Decision.ReasonCode)
		}
	}
}

// The Run loop consumes dirty signals end-to-end (live wiring path).
func TestRunLoopProcessesDirtyMarkets(t *testing.T) {
	s, books := harness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	books.MarkDirty(exchange.MarketID{Exchange: "binance", Symbol: "ETHUSDT"})

	deadline := time.After(2 * time.Second)
	var events []Event
	for len(events) < 2 {
		select {
		case ev := <-s.Out:
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("timed out; got %d events", len(events))
		}
	}
	cancel()
	<-done
}

func keys(m map[string]Event) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Hot swap: SetStrategy changes the effective limits mid-run without
// touching the construction-time fields; making the edge requirement
// unattainable flips qualification to rejection on the next evaluation.
func TestSetStrategyHotSwap(t *testing.T) {
	s, _ := harness(t)
	id := exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"}

	s.EvaluateMarket(id)
	before := drain(s)
	var qualifiedBefore int
	for _, ev := range before {
		if ev.Decision.Allowed {
			qualifiedBefore++
		}
	}
	if qualifiedBefore == 0 {
		t.Fatal("fixture must qualify before the swap")
	}

	swapped := Strategy{Cfg: s.Cfg, Resolver: s.Resolver}
	swapped.Cfg.ConfigVersion = 2
	swapped.Resolver.Global.MinNetEdgeBps = d("100000")
	s.SetStrategy(swapped)

	s.EvaluateMarket(id)
	after := drain(s)
	if len(after) == 0 {
		t.Fatal("no events after swap")
	}
	for _, ev := range after {
		if ev.Decision.Allowed {
			t.Fatalf("qualified despite 10000bps edge floor: %+v", ev.Decision)
		}
		if ev.Opportunity.ConfigVersion != 2 {
			t.Fatalf("opportunity cites config version %d, want 2", ev.Opportunity.ConfigVersion)
		}
	}
}

// T-062: a triangle whose depth cannot satisfy even MinInput rejects
// before any event is emitted — the exit is counted under NoViableSize
// instead of vanishing silently.
func TestNoViableSizeCounted(t *testing.T) {
	s, books := harness(t)
	id := exchange.MarketID{Exchange: "binance", Symbol: exchange.Symbol("BTCUSDT")}
	dust := orderbook.New(id, 0)
	dust.ApplySnapshot(orderbook.DepthEvent{
		Market: id, IsSnapshot: true, FinalUpdateID: 9,
		Bids: []orderbook.Level{lv("99.9", "0.001")}, Asks: []orderbook.Level{lv("100", "0.001")},
		ReceiveTime: t0,
	})
	books.Add(dust)
	s.EvaluateMarket(id)
	if got := s.Stats.NoViableSize.Load(); got == 0 {
		t.Fatalf("no_viable_size = 0, want >0 (dust depth cannot reach MinInput)")
	}
	if evs := drain(s); len(evs) != 0 {
		t.Fatalf("events = %d, want 0 (no event is emitted for this exit)", len(evs))
	}
}
