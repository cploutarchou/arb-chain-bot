package screener

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func fixedFees(bps map[Venue]string) VenueFeeLookup {
	return func(v Venue) (decimal.Decimal, bool) {
		s, ok := bps[v]
		if !ok {
			return decimal.Decimal{}, false
		}
		return d(s), true
	}
}

// TestComputeSpreadsGoldenMath is the hand-computed golden test (design
// §3/§7): buy_ask 100, sell_bid 101, both fees 10 bps ->
// gross 100 bps, net 79.9 bps; qtys 2 and 3 -> liquidity_quote = 200.
func TestComputeSpreadsGoldenMath(t *testing.T) {
	book := NewBook()
	now := time.Unix(1_800_000_000, 0).UTC()
	book.SetQuote(Quote{Venue: VenueBinance, Base: "BTC", Quote: "USDT", Ask: d("100"), AskQty: d("2"), Bid: d("99"), BidQty: d("2"), At: now})
	book.SetQuote(Quote{Venue: VenueOKX, Base: "BTC", Quote: "USDT", Ask: d("102"), AskQty: d("3"), Bid: d("101"), BidQty: d("3"), At: now})

	fees := fixedFees(map[Venue]string{VenueBinance: "10", VenueOKX: "10"})
	res := ComputeSpreads(book, fees, nil, nil, now, SpreadFilters{})
	if res.Total != 2 {
		t.Fatalf("total = %d, want 2 (binance->okx and okx->binance)", res.Total)
	}
	var row *SpreadRow
	for i := range res.Rows {
		if res.Rows[i].BuyVenue == VenueBinance && res.Rows[i].SellVenue == VenueOKX {
			row = &res.Rows[i]
		}
	}
	if row == nil {
		t.Fatal("binance->okx row missing")
	}
	if !row.SpreadBpsGross.Equal(d("100")) {
		t.Fatalf("gross bps = %s, want 100", row.SpreadBpsGross)
	}
	if !row.SpreadBpsNet.Equal(d("79.9")) {
		t.Fatalf("net bps = %s, want 79.9", row.SpreadBpsNet)
	}
	if !row.LiquidityQuote.Equal(d("200")) {
		t.Fatalf("liquidity_quote = %s, want 200 (min(100*2=200, 101*3=303))", row.LiquidityQuote)
	}
	if row.BuyAgeMs != 0 || row.SellAgeMs != 0 {
		t.Fatalf("ages = %d, %d, want 0 (quotes observed exactly at now)", row.BuyAgeMs, row.SellAgeMs)
	}
}

func TestComputeSpreadsSortedNetDesc(t *testing.T) {
	book := NewBook()
	now := time.Now().UTC()
	book.SetQuote(Quote{Venue: VenueBinance, Base: "ETH", Quote: "USDT", Ask: d("100"), AskQty: d("10"), Bid: d("99.5"), BidQty: d("10"), At: now})
	book.SetQuote(Quote{Venue: VenueOKX, Base: "ETH", Quote: "USDT", Ask: d("101"), AskQty: d("10"), Bid: d("100.5"), BidQty: d("10"), At: now})
	book.SetQuote(Quote{Venue: VenueBybit, Base: "ETH", Quote: "USDT", Ask: d("103"), AskQty: d("10"), Bid: d("102.5"), BidQty: d("10"), At: now})

	fees := fixedFees(map[Venue]string{VenueBinance: "0", VenueOKX: "0", VenueBybit: "0"})
	res := ComputeSpreads(book, fees, nil, nil, now, SpreadFilters{})
	for i := 1; i < len(res.Rows); i++ {
		if res.Rows[i-1].SpreadBpsNet.LessThan(res.Rows[i].SpreadBpsNet) {
			t.Fatalf("rows not sorted net desc at %d: %s < %s", i, res.Rows[i-1].SpreadBpsNet, res.Rows[i].SpreadBpsNet)
		}
	}
}

func TestComputeSpreadsFilters(t *testing.T) {
	book := NewBook()
	now := time.Now().UTC()
	book.SetQuote(Quote{Venue: VenueBinance, Base: "BTC", Quote: "USDT", Ask: d("100"), AskQty: d("1"), Bid: d("99"), BidQty: d("1"), At: now})
	book.SetQuote(Quote{Venue: VenueOKX, Base: "BTC", Quote: "USDT", Ask: d("110"), AskQty: d("1"), Bid: d("109"), BidQty: d("1"), At: now})
	book.SetQuote(Quote{Venue: VenueBybit, Base: "ETH", Quote: "USDT", Ask: d("50"), AskQty: d("100"), Bid: d("49"), BidQty: d("100"), At: now})

	fees := fixedFees(map[Venue]string{VenueBinance: "0", VenueOKX: "0", VenueBybit: "0"})

	// base allow-list excludes ETH.
	res := ComputeSpreads(book, fees, nil, nil, now, SpreadFilters{BasesAllow: map[string]bool{"BTC": true}})
	for _, r := range res.Rows {
		if r.Base != "BTC" {
			t.Fatalf("bases_allow leaked %s", r.Base)
		}
	}

	// min liquidity excludes the thin BTC book (liquidity_quote=100).
	minLiq := d("1000")
	res = ComputeSpreads(book, fees, nil, nil, now, SpreadFilters{MinLiquidityQuote: &minLiq})
	for _, r := range res.Rows {
		if r.Base == "BTC" {
			t.Fatalf("min_liquidity_quote leaked a thin BTC row: %+v", r)
		}
	}

	// buy venue restriction.
	res = ComputeSpreads(book, fees, nil, nil, now, SpreadFilters{BuyVenues: map[Venue]bool{VenueBinance: true}})
	for _, r := range res.Rows {
		if r.BuyVenue != VenueBinance {
			t.Fatalf("buy_venues leaked %s", r.BuyVenue)
		}
	}
}

// TestLifetimeTrackerTracksAndResets is the lifetime-tracking golden
// test (design §3): first_seen is set the first time net >= threshold,
// held while it stays >=, and reset the moment it drops below.
func TestLifetimeTrackerTracksAndResets(t *testing.T) {
	tracker := NewLifetimeTracker(decimal.Zero)
	key := SpreadKey{Base: "BTC", Quote: "USDT", BuyVenue: VenueBinance, SellVenue: VenueOKX}
	t0 := time.Unix(1_800_000_000, 0).UTC()

	fs, life := tracker.Observe(key, d("50"), t0)
	if !fs.Equal(t0) || life != 0 {
		t.Fatalf("first observation = %v, %d; want t0, 0", fs, life)
	}

	t1 := t0.Add(30 * time.Second)
	fs, life = tracker.Observe(key, d("10"), t1)
	if !fs.Equal(t0) || life != 30 {
		t.Fatalf("second observation = %v, %d; want t0, 30", fs, life)
	}

	t2 := t1.Add(5 * time.Second)
	fs, life = tracker.Observe(key, d("-1"), t2)
	if !fs.IsZero() || life != 0 {
		t.Fatalf("below-threshold observation = %v, %d; want zero, 0 (reset)", fs, life)
	}

	t3 := t2.Add(10 * time.Second)
	fs, life = tracker.Observe(key, d("0"), t3)
	if !fs.Equal(t3) || life != 0 {
		t.Fatalf("re-armed observation = %v, %d; want t3, 0 (fresh first_seen)", fs, life)
	}
}

func TestComputeSpreadsUsesLifetimeTracker(t *testing.T) {
	book := NewBook()
	now := time.Unix(1_800_000_000, 0).UTC()
	book.SetQuote(Quote{Venue: VenueBinance, Base: "BTC", Quote: "USDT", Ask: d("100"), AskQty: d("2"), Bid: d("99"), BidQty: d("2"), At: now})
	book.SetQuote(Quote{Venue: VenueOKX, Base: "BTC", Quote: "USDT", Ask: d("102"), AskQty: d("3"), Bid: d("101"), BidQty: d("3"), At: now})
	fees := fixedFees(map[Venue]string{VenueBinance: "0", VenueOKX: "0"})
	tracker := NewLifetimeTracker(decimal.Zero)

	res1 := ComputeSpreads(book, fees, tracker, nil, now, SpreadFilters{})
	row1 := findRow(t, res1.Rows, VenueBinance, VenueOKX)
	if row1.LifetimeS != 0 || row1.FirstSeenAt == nil {
		t.Fatalf("first tick lifetime = %d, first_seen_at = %v", row1.LifetimeS, row1.FirstSeenAt)
	}

	later := now.Add(90 * time.Second)
	res2 := ComputeSpreads(book, fees, tracker, nil, later, SpreadFilters{})
	row2 := findRow(t, res2.Rows, VenueBinance, VenueOKX)
	if row2.LifetimeS != 90 {
		t.Fatalf("second tick lifetime = %d, want 90", row2.LifetimeS)
	}
}

func findRow(t *testing.T, rows []SpreadRow, buy, sell Venue) SpreadRow {
	t.Helper()
	for _, r := range rows {
		if r.BuyVenue == buy && r.SellVenue == sell {
			return r
		}
	}
	t.Fatalf("row %s->%s not found", buy, sell)
	return SpreadRow{}
}

// TestComputeSpreadsSkipsZeroPrices guards the divide-by-zero panic path
// (shopspring panics on Div by zero): a quote with a zero ask/bid must
// be skipped, not crash the handler.
func TestComputeSpreadsSkipsZeroPrices(t *testing.T) {
	book := NewBook()
	now := time.Now().UTC()
	book.SetQuote(Quote{Venue: VenueBinance, Base: "BTC", Quote: "USDT", Ask: decimal.Zero, AskQty: d("1"), Bid: d("99"), BidQty: d("1"), At: now})
	book.SetQuote(Quote{Venue: VenueOKX, Base: "BTC", Quote: "USDT", Ask: d("100"), AskQty: d("1"), Bid: d("99"), BidQty: d("1"), At: now})
	fees := fixedFees(map[Venue]string{VenueBinance: "0", VenueOKX: "0"})
	res := ComputeSpreads(book, fees, nil, nil, now, SpreadFilters{})
	for _, r := range res.Rows {
		if r.BuyVenue == VenueBinance {
			t.Fatalf("row bought at a zero ask should have been skipped: %+v", r)
		}
	}
}
