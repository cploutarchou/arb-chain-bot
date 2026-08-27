package venue

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

func findQuote(qs []screener.Quote, base, quote string) (screener.Quote, bool) {
	for _, q := range qs {
		if q.Base == base && q.Quote == quote {
			return q, true
		}
	}
	return screener.Quote{}, false
}

func findPerp(ps []screener.Perp, base string) (screener.Perp, bool) {
	for _, p := range ps {
		if p.Base == base {
			return p, true
		}
	}
	return screener.Perp{}, false
}

func TestBinanceFixtures(t *testing.T) {
	c, fs := fixtureCollector(t, screener.VenueBinance)
	ctx := context.Background()
	inst, err := c.Instruments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var btc Instrument
	for _, in := range inst {
		if in.Kind == KindSpot && in.Symbol == "BTCUSDT" {
			btc = in
		}
	}
	if btc.Base != "BTC" || btc.Quote != "USDT" || btc.TickSize == "" || btc.StepSize == "" || btc.MinNotional == "" {
		t.Fatalf("BTCUSDT instrument = %+v", btc)
	}
	qs, err := c.Spot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findQuote(qs, "BTC", "USDT"); !ok {
		t.Fatal("no BTC/USDT quote")
	}
	ps, err := c.Perps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := findPerp(ps, "BTC")
	if !ok || p.IntervalH != 8 || p.NextFundingAt.IsZero() || !p.Index.IsPositive() {
		t.Fatalf("BTC perp = %+v", p)
	}
	// fundingInfo lists adjusted symbols only: at least one non-8 h row.
	non8 := 0
	for _, p := range ps {
		if p.IntervalH != 8 {
			non8++
		}
	}
	if non8 == 0 {
		t.Fatal("expected at least one perp with an adjusted (non-8h) funding interval from fundingInfo")
	}
	// Spot() must issue ONE bulk call per poll (instruments are cached).
	if fs.hits["/api/v3/ticker/bookTicker"] != 1 || fs.hits["/api/v3/exchangeInfo"] != 1 {
		t.Fatalf("hits = %v", fs.hits)
	}
}

func TestOKXFundingRoundRobinCarriesLastKnown(t *testing.T) {
	c, fs := fixtureCollector(t, screener.VenueOKX) // FundingCallsPerPoll: 3
	ctx := context.Background()
	ps, err := c.Perps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fs.hits["/api/v5/public/funding-rate"] != 3 {
		t.Fatalf("funding-rate calls = %d, want 3", fs.hits["/api/v5/public/funding-rate"])
	}
	// The fixture answers BTC-USDT-SWAP for any instId, so BTC has
	// funding populated with an 8h interval derived from the timestamps.
	btc, ok := findPerp(ps, "BTC")
	if !ok || btc.IntervalH != 8 || btc.FundingRate.IsZero() || btc.NextFundingAt.IsZero() {
		t.Fatalf("BTC perp = %+v", btc)
	}
	if !btc.FundingRate.Equal(decimal.RequireFromString("-0.0000143909410096")) {
		t.Fatalf("funding rate parsed as %s", btc.FundingRate)
	}
	// Second poll: 3 more calls, BTC's last-known values still carried.
	ps, err = c.Perps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fs.hits["/api/v5/public/funding-rate"] != 6 {
		t.Fatalf("funding-rate calls = %d, want 6", fs.hits["/api/v5/public/funding-rate"])
	}
	if btc2, _ := findPerp(ps, "BTC"); btc2.IntervalH != 8 {
		t.Fatalf("last-known funding not carried: %+v", btc2)
	}
	// Inverse swaps are excluded; linear ones map base from ctValCcy.
	for _, p := range ps {
		if p.Quote != "USDT" {
			t.Fatalf("non-USDT perp leaked: %+v", p)
		}
	}
}

func TestBybitFixtures(t *testing.T) {
	c, _ := fixtureCollector(t, screener.VenueBybit)
	ps, err := c.Perps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hours := map[int]int{}
	for _, p := range ps {
		hours[p.IntervalH]++
		if p.NextFundingAt.IsZero() {
			t.Fatalf("bybit perp without next funding: %+v", p)
		}
	}
	if hours[8] == 0 || len(hours) < 2 {
		t.Fatalf("expected mixed funding intervals from instruments-info fundingInterval, got %v", hours)
	}
}

func TestBitgetFixtures(t *testing.T) {
	c, fs := fixtureCollector(t, screener.VenueBitget)
	ctx := context.Background()
	inst, err := c.Instruments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range inst {
		if in.Symbol == "BTCUSDT" && in.Kind == KindSpot && in.TickSize != "0.01" {
			t.Fatalf("pricePrecision 2 must map to tick 0.01, got %q", in.TickSize)
		}
	}
	ps, err := c.Perps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fs.hits["/api/v2/mix/market/current-fund-rate"] != 3 {
		t.Fatalf("current-fund-rate calls = %d", fs.hits["/api/v2/mix/market/current-fund-rate"])
	}
	btc, ok := findPerp(ps, "BTC")
	if !ok || btc.IntervalH != 8 || btc.NextFundingAt.IsZero() || !btc.Index.IsPositive() {
		t.Fatalf("BTC perp = %+v", btc)
	}
}

func TestGateFixtures(t *testing.T) {
	c, _ := fixtureCollector(t, screener.VenueGate)
	ctx := context.Background()
	qs, err := c.Spot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range qs {
		if !q.LiquidityUnknown || !q.BidQty.IsZero() || !q.AskQty.IsZero() {
			t.Fatalf("gate spot quote must have zero sizes + LiquidityUnknown: %+v", q)
		}
	}
	ps, err := c.Perps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	btc, ok := findPerp(ps, "BTC")
	if !ok || btc.IntervalH != 8 || btc.NextFundingAt.IsZero() || !btc.Index.IsPositive() {
		t.Fatalf("BTC perp = %+v", btc)
	}
	nets, err := c.Networks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	open, closed := 0, 0
	for _, st := range nets {
		switch st.Status {
		case screener.NetworkOpen:
			open++
		case screener.NetworkClosed:
			closed++
		}
	}
	if open == 0 {
		t.Fatalf("gate networks: open=%d closed=%d of %d", open, closed, len(nets))
	}
}

func TestMEXCFixturesBareNumbersAreExact(t *testing.T) {
	c, _ := fixtureCollector(t, screener.VenueMEXC)
	ctx := context.Background()
	ps, err := c.Perps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	btc, ok := findPerp(ps, "BTC")
	if !ok {
		t.Fatal("no BTC perp")
	}
	// contract/ticker publishes bare JSON numbers; decimal must keep the
	// literal text (fixture: fairPrice 80318.2, fundingRate 0.000073).
	if btc.Mark.String() != "80318.2" || btc.FundingRate.String() != "0.000073" {
		t.Fatalf("mark=%s funding=%s", btc.Mark, btc.FundingRate)
	}
	if btc.IntervalH != 8 || btc.NextFundingAt.IsZero() {
		t.Fatalf("funding round-robin did not fill BTC: %+v", btc)
	}
}

// TestGateHonoursRetryAfter: a 429 with Retry-After blocks that venue's
// gate for the announced seconds and counts as rate-limited.
func TestGateHonoursRetryAfter(t *testing.T) {
	now := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":-1003}`))
	}))
	defer srv.Close()
	g := newGate(100, time.Second, clock)
	cl := newClient(screener.VenueBinance, g)
	_, err := cl.get(context.Background(), 1, srv.URL, "/x", nil)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 429 {
		t.Fatalf("err = %v", err)
	}
	if g.limited.Load() != 1 {
		t.Fatalf("limited = %d", g.limited.Load())
	}
	if d := g.blockedFor(); d < 7*time.Second || d > 9*time.Second {
		t.Fatalf("blockedFor = %s, want ~8s", d)
	}
	// A blocked gate refuses to issue the next call until ctx ends.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := g.wait(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait during block = %v", err)
	}
	// Bybit-style 403 without Retry-After → documented 10 min cool-down.
	he403 := &HTTPError{Venue: screener.VenueBybit, Status: 403}
	g2 := newGate(100, time.Second, clock)
	g2.observe(he403)
	if d := g2.blockedFor(); d < 10*time.Minute {
		t.Fatalf("403 block = %s", d)
	}
}

func TestGateWindowThrottles(t *testing.T) {
	now := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	g := newGate(3, time.Second, func() time.Time { return now })
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := g.wait(ctx, 1); err != nil {
			t.Fatal(err)
		}
	}
	if d := g.delay(now, 1); d <= 0 {
		t.Fatal("4th request in the window must be delayed")
	}
	now = now.Add(1100 * time.Millisecond)
	if err := g.wait(ctx, 1); err != nil {
		t.Fatal(err)
	}
}
