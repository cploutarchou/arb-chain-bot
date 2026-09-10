package venue

import (
	"context"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

func perp(venue screener.Venue, base, quote string) screener.Perp {
	return screener.Perp{Venue: venue, Base: base, Quote: quote, Mark: dec("1"), IntervalH: 8}
}

// TestSelectPerpContractsDeterministic: the preference, not the venue's
// listing order, decides which contract survives; a base with one
// contract is kept whatever its quote; an asset outside the preference
// ranks last and ties fall back to the quote string.
func TestSelectPerpContractsDeterministic(t *testing.T) {
	usdt, usdc := perp(screener.VenueBinance, "AAVE", "USDT"), perp(screener.VenueBinance, "AAVE", "USDC")
	btc := perp(screener.VenueBinance, "BTC", "USDT")
	usd := perp(screener.VenueKraken, "ETH", "USD")
	for _, order := range [][]screener.Perp{{usdt, usdc, btc, usd}, {usdc, usdt, btc, usd}} {
		kept, dropped := selectPerpContracts(order, []string{"USDT"})
		if len(kept) != 3 || len(dropped) != 1 || dropped[0].Quote != "USDC" {
			t.Fatalf("order %v: kept %d dropped %+v", order, len(kept), dropped)
		}
		for _, k := range kept {
			if k.Base == "AAVE" && k.Quote != "USDT" {
				t.Fatalf("AAVE kept %s, want USDT", k.Quote)
			}
		}
	}
	kept, dropped := selectPerpContracts([]screener.Perp{usdt, usdc, btc, usd}, []string{"USDC", "USDT"})
	if len(dropped) != 1 || dropped[0].Quote != "USDT" || dropped[0].Base != "AAVE" || len(kept) != 3 {
		t.Fatalf("USDC preferred: dropped %+v", dropped)
	}
	// Neither quote listed: the quote string breaks the tie the same way
	// regardless of order (USDC < USDT).
	for _, order := range [][]screener.Perp{{usdt, usdc}, {usdc, usdt}} {
		kept, dropped := selectPerpContracts(order, []string{"BUSD"})
		if len(kept) != 1 || kept[0].Quote != "USDC" || len(dropped) != 1 {
			t.Fatalf("no listed quote: kept %+v", kept)
		}
	}
	// The common case (no base listed twice) returns the input untouched.
	kept, dropped = selectPerpContracts([]screener.Perp{btc, usd}, []string{"USDT"})
	if len(kept) != 2 || dropped != nil {
		t.Fatalf("no collision: kept %d dropped %v", len(kept), dropped)
	}
	if kept, dropped := selectPerpContracts(nil, []string{"USDT"}); kept != nil || dropped != nil {
		t.Fatalf("nil input: %v %v", kept, dropped)
	}
}

// TestPollerKeepsOneBinanceContractPerBase drives the recorded Binance
// premium index (55 contracts, 5 of them USDC-margined on bases that
// also have a USDT contract: 1000PEPE, ADA, AAVE, 1000SHIB, 1000BONK)
// through the Poller. The collector reports both AAVE contracts; the
// book ends up with the USDT one under (binance, AAVE, USDT), the USDC
// one is absent, the status row counts the 5 drops, and reversing the
// preference keeps the USDC contracts instead.
func TestPollerKeepsOneBinanceContractPerBase(t *testing.T) {
	fs := newFixtureServer(t, screener.VenueBinance)
	c, err := New(screener.VenueBinance, Options{SpotBase: fs.URL, PerpBase: fs.URL, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.Perps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	quotes := map[string]int{}
	for _, p := range raw {
		if p.Base == "AAVE" {
			quotes[p.Quote]++
		}
	}
	if quotes["USDT"] != 1 || quotes["USDC"] != 1 {
		t.Fatalf("collector AAVE contracts by quote = %v, want one USDT and one USDC (the collision input)", quotes)
	}

	run := func(prefer []string) (*screener.Book, screener.VenueStatus) {
		settings := screener.Defaults()
		settings.PollIntervalS = 2
		settings.PerpQuotePreference = prefer
		for id := range settings.Venues {
			vs := settings.Venues[id]
			vs.Enabled = id == screener.VenueBinance
			settings.Venues[id] = vs
		}
		book := screener.NewBook()
		p := &Poller{Book: book, Current: func() screener.Settings { return settings },
			NewCollector: func(id screener.Venue, opts Options) (Collector, error) {
				opts.SpotBase, opts.PerpBase = fs.URL, fs.URL
				return New(id, opts)
			}}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := p.Start(ctx); err != nil {
			t.Fatal(err)
		}
		defer p.Stop()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			for _, st := range p.Status() {
				if st.ID == screener.VenueBinance && st.Polls > 0 {
					return book, st
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("binance never completed a poll")
		return nil, screener.VenueStatus{}
	}

	book, st := run([]string{"USDT"})
	if st.PerpContracts != len(raw)-5 || st.PerpsDropped != 5 {
		t.Fatalf("status = contracts %d dropped %d, want %d and 5", st.PerpContracts, st.PerpsDropped, len(raw)-5)
	}
	p, ok := book.PerpFor(screener.VenueBinance, "AAVE", "USDT")
	if !ok || p.Quote != "USDT" {
		t.Fatalf("(binance, AAVE, USDT) = %+v ok=%v", p, ok)
	}
	if _, ok := book.PerpFor(screener.VenueBinance, "AAVE", "USDC"); ok {
		t.Fatal("the USDC contract reached the book despite the USDT preference")
	}
	for _, p := range book.Perps() {
		if p.Quote != "USDT" {
			t.Fatalf("non-USDT contract in the book: %+v", p)
		}
	}

	book, st = run([]string{"USDC", "USDT"})
	if st.PerpsDropped != 5 {
		t.Fatalf("USDC preferred: dropped %d, want the 5 USDT duplicates", st.PerpsDropped)
	}
	if p, ok := book.PerpFor(screener.VenueBinance, "AAVE", "USDC"); !ok || p.Quote != "USDC" {
		t.Fatalf("USDC preferred: (binance, AAVE, USDC) = %+v ok=%v", p, ok)
	}
	if _, ok := book.PerpFor(screener.VenueBinance, "AAVE", "USDT"); ok {
		t.Fatal("USDC preferred: the USDT AAVE contract must be the one dropped")
	}
	if _, ok := book.PerpFor(screener.VenueBinance, "BTC", "USDT"); !ok {
		t.Fatal("a base with a single (USDT) contract must be kept under any preference")
	}
}

// TestRecordFundingKeyedPerContract: with the last-seen state keyed
// (venue, base) an intervening USDC observation replaced the USDT
// contract's rate, so the USDT settlement was booked with the USDC rate.
// Keyed per contract each advance records its own contract's rate.
func TestRecordFundingKeyedPerContract(t *testing.T) {
	ctx := context.Background()
	funding := screener.NewMemoryFundingStore()
	p := &Poller{Book: screener.NewBook(), Funding: funding, Current: screener.Defaults}
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	t1, t2 := time.Unix(1_800_000_000, 0).UTC(), time.Unix(1_800_028_800, 0).UTC()
	usdt := screener.Perp{Venue: screener.VenueBinance, Base: "AAVE", Quote: "USDT", FundingRate: dec("0.0001"), NextFundingAt: t1}
	usdc := screener.Perp{Venue: screener.VenueBinance, Base: "AAVE", Quote: "USDC", FundingRate: dec("0.0009"), NextFundingAt: t1}
	p.recordFunding(ctx, nil, usdt)
	p.recordFunding(ctx, nil, usdc)
	usdt.FundingRate, usdt.NextFundingAt = dec("0.0002"), t2 // USDT settled at t1
	p.recordFunding(ctx, nil, usdt)
	series, _ := funding.ListFunding(ctx, "AAVE", []screener.Venue{screener.VenueBinance}, time.Time{})
	if len(series) != 1 || len(series[0].Points) != 1 {
		t.Fatalf("series = %+v, want one settled point", series)
	}
	if pt := series[0].Points[0]; pt.Rate != "0.0001" || !pt.At.Equal(t1) {
		t.Fatalf("settled point = %+v, want the USDT contract's own 0.0001 at t1 (not the USDC 0.0009)", pt)
	}
}

// prevPeriodCollector stands in for the BitMart/Crypto.com/Bitfinex
// shape (audit X4): a collector whose bulk funding field reports the
// previous period's settled rate.
type prevPeriodCollector struct{ wedgedCollector }

func (*prevPeriodCollector) FundingIsPreviousPeriod() bool { return true }

// TestRecordFundingAttributesByRateSemantics (audit X4): when
// NextFundingAt advances, an accruing-rate venue books the previously
// seen rate at the advancing timestamp; a previous-period venue books
// the newly observed one — its bulk field only started reporting the
// just-settled rate after the boundary. Before X4 both stored the old
// rate, shifting every settlement on those venues one interval.
func TestRecordFundingAttributesByRateSemantics(t *testing.T) {
	ctx := context.Background()
	t1, t2 := time.Unix(1_800_000_000, 0).UTC(), time.Unix(1_800_028_800, 0).UTC()

	accruing := screener.NewMemoryFundingStore()
	pa := &Poller{Book: screener.NewBook(), Funding: accruing, Current: screener.Defaults}
	if err := pa.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer pa.Stop()
	first := screener.Perp{Venue: screener.VenueBinance, Base: "BTC", Quote: "USDT", FundingRate: dec("0.0001"), NextFundingAt: t1}
	pa.recordFunding(ctx, nil, first)
	first.FundingRate, first.NextFundingAt = dec("0.0003"), t2
	pa.recordFunding(ctx, nil, first)
	pts, _ := accruing.ListFunding(ctx, "BTC", []screener.Venue{screener.VenueBinance}, time.Time{})
	if len(pts) != 1 || len(pts[0].Points) != 1 || pts[0].Points[0].Rate != "0.0001" || !pts[0].Points[0].At.Equal(t1) {
		t.Fatalf("accruing venue: %+v, want the OLD rate 0.0001 at t1", pts)
	}

	previous := screener.NewMemoryFundingStore()
	pp := &Poller{Book: screener.NewBook(), Funding: previous, Current: screener.Defaults}
	if err := pp.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer pp.Stop()
	second := screener.Perp{Venue: screener.VenueBitMart, Base: "BTC", Quote: "USDT", FundingRate: dec("0.0001"), NextFundingAt: t1}
	pp.recordFunding(ctx, &prevPeriodCollector{}, second)
	second.FundingRate, second.NextFundingAt = dec("0.0003"), t2
	pp.recordFunding(ctx, &prevPeriodCollector{}, second)
	pts, _ = previous.ListFunding(ctx, "BTC", []screener.Venue{screener.VenueBitMart}, time.Time{})
	if len(pts) != 1 || len(pts[0].Points) != 1 || pts[0].Points[0].Rate != "0.0003" || !pts[0].Points[0].At.Equal(t1) {
		t.Fatalf("previous-period venue: %+v, want the NEW rate 0.0003 at t1", pts)
	}
}
