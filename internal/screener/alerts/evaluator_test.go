package alerts

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/notification"
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var t0 = time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

type fakeNotifier struct {
	mu     sync.Mutex
	events []notification.Event
}

func (f *fakeNotifier) Notify(ev notification.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

func newSvc(t *testing.T) *screener.Service {
	t.Helper()
	svc := screener.NewService(screener.NewBook(), screener.NewMemoryStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if _, err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc.Rules = screener.NewMemoryRuleStore()
	svc.Events = screener.NewMemoryEventStore()
	svc.Funding = screener.NewMemoryFundingStore()
	return svc
}

// setSpread populates the §2.5 lane: Binance ask 50 000 / 0.35, venue B
// (OKX, fee 10 bps UNVERIFIED placeholder from Defaults) bid bidB / 0.20.
func setSpread(book *screener.Book, bidB string, at time.Time) {
	book.SetQuote(screener.Quote{Venue: screener.VenueBinance, Base: "BTC", Quote: "USDT",
		Bid: d("49999"), BidQty: d("1"), Ask: d("50000"), AskQty: d("0.35"), At: at})
	book.SetQuote(screener.Quote{Venue: screener.VenueOKX, Base: "BTC", Quote: "USDT",
		Bid: d(bidB), BidQty: d("0.20"), Ask: d(bidB).Add(d("5")), AskQty: d("1"), At: at})
}

func spreadRule() screener.Rule {
	min := d("5")
	return screener.Rule{ID: "r1", Name: "btc spread", Enabled: true, Kind: screener.RuleKindSpread,
		MinSpreadBps: &min, MinLiquidityQuote: d("500"), MinLifetimeS: 10, CooldownS: 60,
		BuyVenues: []screener.Venue{screener.VenueBinance}, SellVenues: []screener.Venue{screener.VenueOKX},
		Quotes: []string{"USDT"}, Telegram: true}
}

func TestSpotSignalMatchesWorkedExample(t *testing.T) {
	svc := newSvc(t)
	r := spreadRule()
	setSpread(svc.Book, "50150", t0)
	in := Inputs{Book: svc.Book, SpotFees: feeLookup(svc, false), PerpFees: feeLookup(svc, true), PollInterval: 5 * time.Second}
	sigs := ComputeSignals(context.Background(), in, r, t0)
	if len(sigs) != 1 {
		t.Fatalf("signals = %d, want 1", len(sigs))
	}
	s := sigs[0]
	if got := s.GrossBps.StringFixed(2); got != "30.00" {
		t.Errorf("gross = %s, want 30.00", got)
	}
	if got := s.NetBps.StringFixed(2); got != "9.97" {
		t.Errorf("net = %s, want 9.97", got)
	}
	if got := s.ExecBps.StringFixed(2); got != "0.97" {
		t.Errorf("exec = %s, want 0.97", got)
	}
	if s.Active {
		t.Errorf("0.97 bps must not activate a 5 bps rule")
	}
	setSpread(svc.Book, "50250", t0)
	s = ComputeSignals(context.Background(), in, r, t0)[0]
	if got := s.NetBps.StringFixed(2); got != "29.95" {
		t.Errorf("net = %s, want 29.95", got)
	}
	if got := s.ExecBps.StringFixed(2); got != "20.95" {
		t.Errorf("exec = %s, want 20.95", got)
	}
	if !s.Active {
		t.Errorf("20.95 bps must activate: %s", s.Reason)
	}
	if got := s.LiquidityQuote.String(); got != "10050" {
		t.Errorf("liquidity = %s, want 10050 (0.20 × 50 250)", got)
	}
}

func TestDataAgeGate(t *testing.T) {
	svc := newSvc(t)
	r := spreadRule()
	in := Inputs{Book: svc.Book, SpotFees: feeLookup(svc, false), PerpFees: feeLookup(svc, true), PollInterval: 5 * time.Second}
	// One leg 6 s old against a 5 s poll: DATA_AGE.
	svc.Book.SetQuote(screener.Quote{Venue: screener.VenueBinance, Base: "BTC", Quote: "USDT", Bid: d("49999"), BidQty: d("1"), Ask: d("50000"), AskQty: d("1"), At: t0.Add(-6 * time.Second)})
	svc.Book.SetQuote(screener.Quote{Venue: screener.VenueOKX, Base: "BTC", Quote: "USDT", Bid: d("50250"), BidQty: d("1"), Ask: d("50255"), AskQty: d("1"), At: t0})
	s := ComputeSignals(context.Background(), in, r, t0)[0]
	if s.Active || s.Reason != "DATA_AGE" {
		t.Fatalf("stale leg: active=%v reason=%q", s.Active, s.Reason)
	}
	// Both fresh but 3 s apart (> poll/2): a fresh leg against a stale leg.
	svc.Book.SetQuote(screener.Quote{Venue: screener.VenueBinance, Base: "BTC", Quote: "USDT", Bid: d("49999"), BidQty: d("1"), Ask: d("50000"), AskQty: d("1"), At: t0.Add(-3 * time.Second)})
	s = ComputeSignals(context.Background(), in, r, t0)[0]
	if s.Active || s.Reason != "DATA_AGE" {
		t.Fatalf("skewed legs: active=%v reason=%q", s.Active, s.Reason)
	}
}

func feeLookup(svc *screener.Service, perp bool) FeeLookup {
	return func(v screener.Venue) (decimal.Decimal, bool) {
		vs, ok := svc.Current().Settings.Venues[v]
		if !ok {
			return decimal.Decimal{}, false
		}
		if perp {
			return vs.PerpTakerBps, true
		}
		return vs.SpotTakerBps, true
	}
}

func TestEvaluatorLifecycleCooldownAndText(t *testing.T) {
	svc := newSvc(t)
	r := spreadRule()
	if _, err := svc.Rules.InsertRule(context.Background(), r, "t"); err != nil {
		t.Fatal(err)
	}
	fn := &fakeNotifier{}
	ev := New(svc, fn.Notify, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var opened []screener.Event
	ev.OnOpen(func(_ context.Context, _ Signal, e screener.Event) { opened = append(opened, e) })
	ctx := context.Background()

	// t0: signal active, lifetime 0 < 10 s: no event.
	setSpread(svc.Book, "50250", t0)
	ev.Tick(ctx, t0)
	if len(ev.OpenEvents()) != 0 || fn.count() != 0 {
		t.Fatalf("event opened before min_lifetime")
	}
	// t0+5 s: lifetime 5, still no event.
	setSpread(svc.Book, "50250", t0.Add(5*time.Second))
	ev.Tick(ctx, t0.Add(5*time.Second))
	if len(ev.OpenEvents()) != 0 {
		t.Fatalf("event opened at 5 s")
	}
	// t0+10 s: lifetime 10 ≥ min: open, hook called, Telegram text sent.
	setSpread(svc.Book, "50260", t0.Add(10*time.Second)) // peak higher
	ev.Tick(ctx, t0.Add(10*time.Second))
	if len(ev.OpenEvents()) != 1 || len(opened) != 1 || fn.count() != 1 {
		t.Fatalf("open: events=%d hooks=%d notified=%d", len(ev.OpenEvents()), len(opened), fn.count())
	}
	body := fn.events[0].Body
	lower := strings.ToLower(fn.events[0].Title + " " + body)
	for _, verb := range []string{"buy", "sell", "long", "short", "enter", "go "} {
		if strings.Contains(lower, verb) {
			t.Errorf("alert text contains trading verb %q: %s", verb, body)
		}
	}
	for _, want := range []string{"binance", "okx", "BTC/USDT", "bps", "liquidity", "data age", Footer} {
		if !strings.Contains(body, want) {
			t.Errorf("alert text missing %q:\n%s", want, body)
		}
	}
	// Same poll again: no second notification (event already open).
	ev.Tick(ctx, t0.Add(15*time.Second))
	if fn.count() != 1 {
		t.Fatalf("dedup: notified %d, want 1", fn.count())
	}
	// Signal drops: event closed with lifetime and peak.
	setSpread(svc.Book, "50150", t0.Add(20*time.Second))
	ev.Tick(ctx, t0.Add(20*time.Second))
	if len(ev.OpenEvents()) != 0 {
		t.Fatalf("event not closed")
	}
	events, _ := svc.Events.ListEvents(ctx, "r1", 10)
	if len(events) != 1 || events[0].ClosedAt == nil {
		t.Fatalf("stored event not closed: %+v", events)
	}
	if events[0].LifetimeS != 20 {
		t.Errorf("lifetime_s = %d, want 20 (first seen t0, closed t0+20)", events[0].LifetimeS)
	}
	if !events[0].TelegramSent {
		t.Errorf("telegram_sent not recorded")
	}
	wantPeak := d("50260").Mul(d("0.999")).Sub(d("50000").Mul(d("1.001"))).Div(d("50000")).Mul(decTenK).Sub(d("9"))
	if got := d(events[0].PeakNetBps); !got.Equal(wantPeak) {
		t.Errorf("peak = %s, want %s", got, wantPeak)
	}
	if fn.count() != 2 {
		t.Fatalf("close notification: %d, want 2", fn.count())
	}
	// Re-activate immediately with ≥ min_lifetime: cooldown 60 s blocks.
	for i := 0; i <= 8; i++ { // up to t0+65 s: 55 s since the open at t0+10
		at := t0.Add(time.Duration(25+5*i) * time.Second)
		setSpread(svc.Book, "50250", at)
		ev.Tick(ctx, at)
	}
	if len(ev.OpenEvents()) != 0 {
		t.Fatalf("cooldown not honoured: event reopened within 60 s")
	}
	// After the cooldown elapses the lane opens again.
	at := t0.Add(80 * time.Second)
	setSpread(svc.Book, "50250", at)
	ev.Tick(ctx, at)
	if len(ev.OpenEvents()) != 1 || len(opened) != 2 {
		t.Fatalf("reopen after cooldown: open=%d hooks=%d", len(ev.OpenEvents()), len(opened))
	}
}

func setCarry(book *screener.Book, at time.Time) {
	book.SetQuote(screener.Quote{Venue: screener.VenueBinance, Base: "BTC", Quote: "USDT",
		Bid: d("49998"), BidQty: d("2"), Ask: d("50000"), AskQty: d("2"), At: at})
	book.SetPerp(screener.Perp{Venue: screener.VenueBinance, Base: "BTC", Quote: "USDT",
		Mark: d("50050"), Index: d("49999"), Bid: d("50100"), Ask: d("50102"),
		FundingRate: d("0.0001"), PredictedFundingRate: d("0.0001"), IntervalH: 8,
		NextFundingAt: at.Add(4 * time.Hour), At: at})
}

func TestCarrySignalMatchesWorkedExample(t *testing.T) {
	svc := newSvc(t)
	setCarry(svc.Book, t0)
	// last-30 mean +0.0120 %/8 h
	for i := 1; i <= 30; i++ {
		_ = svc.Funding.UpsertFunding(context.Background(), screener.VenueBinance, "BTC", t0.Add(-time.Duration(i*8)*time.Hour), "0.00012")
	}
	apr := d("0")
	r := screener.Rule{ID: "c1", Name: "carry", Enabled: true, Kind: screener.RuleKindCarry, MinCarryAPR: &apr,
		BuyVenues: []screener.Venue{screener.VenueBinance}, Params: &screener.RuleParams{MaxHoldH: 720}}
	in := Inputs{Book: svc.Book, SpotFees: feeLookup(svc, false), PerpFees: feeLookup(svc, true), FundingHistory: svc.Funding, PollInterval: 5 * time.Second}
	sigs := ComputeSignals(context.Background(), in, r, t0)
	if len(sigs) != 1 {
		t.Fatalf("signals = %d", len(sigs))
	}
	s := sigs[0]
	if got := s.BasisEntryBps.StringFixed(2); got != "20.00" {
		t.Errorf("basis = %s, want 20.00", got)
	}
	if got := s.FundingExpBps.StringFixed(2); got != "107.80" {
		t.Errorf("funding_exp = %s, want 107.80", got)
	}
	if got := s.EdgeBps.StringFixed(2); got != "84.80" {
		t.Errorf("edge = %s, want 84.80", got)
	}
	if !s.Active {
		t.Errorf("carry signal inactive: %s", s.Reason)
	}
	if got := s.CarryAPR.StringFixed(4); got != "0.1095" {
		t.Errorf("carry apr = %s, want 0.1095", got)
	}
}

func TestHarvestBreakevenMatchesSpec(t *testing.T) {
	svc := newSvc(t)
	apr := d("0")
	r := screener.Rule{ID: "h1", Name: "harvest", Enabled: true, Kind: screener.RuleKindBasis, MinCarryAPR: &apr,
		BuyVenues: []screener.Venue{screener.VenueBinance}}
	in := Inputs{Book: svc.Book, SpotFees: feeLookup(svc, false), PerpFees: feeLookup(svc, true), FundingHistory: svc.Funding, PollInterval: 5 * time.Second}
	set := func(rate string) {
		setCarry(svc.Book, t0)
		p, _ := svc.Book.PerpFor(screener.VenueBinance, "BTC")
		p.PredictedFundingRate = d(rate)
		p.FundingRate = d(rate)
		p.Bid = d("49990") // basis −2 bps
		svc.Book.SetPerp(p)
	}
	set("0.0001") // 1 bps/8 h: costs 43 → breakeven 43 → skip
	s := ComputeSignals(context.Background(), in, r, t0)[0]
	if s.BreakevenN != 43 || s.Active {
		t.Errorf("1 bps: breakeven=%d active=%v (%s), want 43/false", s.BreakevenN, s.Active, s.Reason)
	}
	set("0.0005") // 5 bps/8 h: breakeven 9 → enter
	s = ComputeSignals(context.Background(), in, r, t0)[0]
	if s.BreakevenN != 9 || !s.Active {
		t.Errorf("5 bps: breakeven=%d active=%v (%s), want 9/true", s.BreakevenN, s.Active, s.Reason)
	}
	if got := s.BasisEntryBps.StringFixed(2); got != "-2.00" {
		t.Errorf("basis = %s, want -2.00", got)
	}
}
