package screener

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// loadGuardFixture fills a Book from testdata/guard/mismatch_book.json
// (the VON / TROLL / XTER live cases plus a BTC control lane).
func loadGuardFixture(t *testing.T, now time.Time) *Book {
	t.Helper()
	raw, err := os.ReadFile("testdata/guard/mismatch_book.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Quotes []struct {
			Venue            Venue           `json:"venue"`
			Base             string          `json:"base"`
			Quote            string          `json:"quote"`
			Bid              decimal.Decimal `json:"bid"`
			BidQty           decimal.Decimal `json:"bid_qty"`
			Ask              decimal.Decimal `json:"ask"`
			AskQty           decimal.Decimal `json:"ask_qty"`
			LiquidityUnknown bool            `json:"liquidity_unknown"`
		} `json:"quotes"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	book := NewBook()
	for _, q := range doc.Quotes {
		book.SetQuote(Quote{Venue: q.Venue, Base: q.Base, Quote: q.Quote, Bid: q.Bid, BidQty: q.BidQty,
			Ask: q.Ask, AskQty: q.AskQty, At: now, LiquidityUnknown: q.LiquidityUnknown})
	}
	return book
}

func allFees() VenueFeeLookup {
	return fixedFees(map[Venue]string{VenueBinance: "10", VenueOKX: "10", VenueBybit: "10", VenueBitget: "10", VenueGate: "20", VenueMEXC: "5"})
}

func findLane(rows []SpreadRow, base, quote string, buy, sell Venue) *SpreadRow {
	for i := range rows {
		r := &rows[i]
		if r.Base == base && r.Quote == quote && r.BuyVenue == buy && r.SellVenue == sell {
			return r
		}
	}
	return nil
}

// TestGuardLaneVerdicts pins the shared function's answers on the live
// cases: VON (gap ~1.9e12 bps AND gate has no sizes) → suspect
// spread_exceeds_max_plausible, skip reason SUSPECT_MISMATCH; TROLL
// gate→mexc (plausible price, gate has no sizes) → LIQUIDITY_UNKNOWN;
// TROLL bitget vs the 3-venue median → price_deviates_from_median;
// XTER okx↔bybit (0.05 vs 0.30 = 5000 %) → suspect; BTC → clean.
func TestGuardLaneVerdicts(t *testing.T) {
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	book := loadGuardFixture(t, now)
	max := DefaultMaxPlausibleSpreadBps

	von := book.QuotesFor("VON", "USDT")
	g := GuardLane(von[VenueGate], von[VenueMEXC], von, max)
	if !g.Suspect || g.SuspectReason != SuspectSpreadImplausible || !g.LiquidityUnknown || g.SkipReason() != SkipSuspectMismatch {
		t.Fatalf("VON gate->mexc guard = %+v", g)
	}

	troll := book.QuotesFor("TROLL", "USDT")
	g = GuardLane(troll[VenueGate], troll[VenueMEXC], troll, max)
	if g.Suspect || !g.LiquidityUnknown || g.SkipReason() != SkipLiquidityUnknown {
		t.Fatalf("TROLL gate->mexc guard = %+v (want liquidity unknown only)", g)
	}
	g = GuardLane(troll[VenueMEXC], troll[VenueBitget], troll, max)
	if !g.Suspect || g.SuspectReason != SuspectSpreadImplausible {
		// 0.00000176 vs 0.1305: the pairwise test fires first (it is the
		// cheaper, always-available check); the median test is exercised
		// below with a gap inside the plausible band.
		t.Fatalf("TROLL mexc->bitget guard = %+v", g)
	}

	xter := book.QuotesFor("XTER", "USDT")
	g = GuardLane(xter[VenueOKX], xter[VenueBybit], xter, max)
	if !g.Suspect || g.SuspectReason != SuspectSpreadImplausible || g.LiquidityUnknown {
		t.Fatalf("XTER okx->bybit guard = %+v", g)
	}

	btc := book.QuotesFor("BTC", "USDT")
	g = GuardLane(btc[VenueBinance], btc[VenueOKX], btc, max)
	if g.Suspect || g.LiquidityUnknown || g.SkipReason() != "" {
		t.Fatalf("BTC guard = %+v, want clean", g)
	}
}

// TestGuardLaneMedianOutlier: three venues at 100/100/165 mids. Every
// pairwise gap (65 %) is inside a 100 % max_plausible_spread_bps, so
// only the median test (165 vs median 100 = +65 % > 50 %) catches it —
// and only when ≥ 3 venues quote the pair.
func TestGuardLaneMedianOutlier(t *testing.T) {
	now := time.Now().UTC()
	book := NewBook()
	for _, v := range []struct {
		venue    Venue
		bid, ask string
	}{{VenueBinance, "99.5", "100.5"}, {VenueOKX, "99.5", "100.5"}, {VenueBybit, "164.5", "165.5"}} {
		book.SetQuote(Quote{Venue: v.venue, Base: "ABC", Quote: "USDT", Bid: d(v.bid), BidQty: d("10"), Ask: d(v.ask), AskQty: d("10"), At: now})
	}
	peers := book.QuotesFor("ABC", "USDT")
	max := decimal.NewFromInt(10000) // 100 %
	g := GuardLane(peers[VenueBinance], peers[VenueBybit], peers, max)
	if !g.Suspect || g.SuspectReason != SuspectMedianOutlier {
		t.Fatalf("guard = %+v, want median outlier", g)
	}
	if g := GuardLane(peers[VenueBinance], peers[VenueOKX], peers, max); g.Suspect {
		t.Fatalf("binance/okx flagged: %+v", g)
	}
	// Two venues only: no median test → the 65 % gap passes a 100 % max.
	two := map[Venue]Quote{VenueBinance: peers[VenueBinance], VenueBybit: peers[VenueBybit]}
	if g := GuardLane(two[VenueBinance], two[VenueBybit], two, max); g.Suspect {
		t.Fatalf("2-venue lane flagged by median test: %+v", g)
	}
}

// TestComputeSpreadsExcludesSuspectAndUnknownByDefault is the task 1a/1b
// acceptance: on the live fixture the default view shows ONLY the BTC
// lanes; the counts say how many were hidden; the include flags bring
// the rows back flagged, with liquidity_quote null where unknown.
func TestComputeSpreadsExcludesSuspectAndUnknownByDefault(t *testing.T) {
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	book := loadGuardFixture(t, now)
	fees := allFees()

	res := ComputeSpreads(book, fees, nil, nil, now, SpreadFilters{})
	for _, r := range res.Rows {
		if r.Base != "BTC" {
			t.Fatalf("default view leaked %s/%s %s->%s (net %s bps, suspect=%v unknown=%v)", r.Base, r.Quote, r.BuyVenue, r.SellVenue, r.SpreadBpsNet, r.Suspect, r.LiquidityUnknown)
		}
		if r.Suspect || r.LiquidityUnknown || r.LiquidityQuote == nil {
			t.Fatalf("default row carries a guard flag: %+v", r)
		}
	}
	// BTC/USDT, BTC/USDC: 2 lanes each = 4 (FDUSD and USD have one venue
	// each → no lane; they are NOT merged into USDT).
	if res.Total != 4 {
		t.Fatalf("total = %d, want 4 BTC lanes; rows=%+v", res.Total, res.Rows)
	}
	// Hidden: VON 2 lanes (suspect), TROLL: gate<->mexc 2 (unknown liq),
	// gate<->bitget 2 (suspect; gate is also unknown, suspect wins),
	// mexc<->bitget 2 (suspect), XTER 2 (suspect) → suspect 8, unknown 2.
	if res.ExcludedSuspect != 8 || res.ExcludedLiquidityUnknown != 2 {
		t.Fatalf("excluded = suspect %d / unknown %d, want 8 / 2", res.ExcludedSuspect, res.ExcludedLiquidityUnknown)
	}

	// include_suspect=1 alone: XTER (suspect, sizes known) is back,
	// flagged; VON stays hidden because its gate side also has no size
	// and include_unknown_liquidity was not asked for.
	res = ComputeSpreads(book, fees, nil, nil, now, SpreadFilters{IncludeSuspect: true})
	xter := findLane(res.Rows, "XTER", "USDT", VenueOKX, VenueBybit)
	if xter == nil || !xter.Suspect || xter.SuspectReason != SuspectSpreadImplausible || xter.LiquidityUnknown {
		t.Fatalf("XTER with include_suspect = %+v", xter)
	}
	if findLane(res.Rows, "VON", "USDT", VenueGate, VenueMEXC) != nil {
		t.Fatal("VON returned by include_suspect alone (its liquidity is unknown)")
	}
	// unknown: VON 2 + TROLL gate<->mexc 2 + TROLL gate<->bitget 2 = 6.
	if res.ExcludedSuspect != 0 || res.ExcludedLiquidityUnknown != 6 {
		t.Fatalf("excluded with include_suspect = %d / %d, want 0 / 6", res.ExcludedSuspect, res.ExcludedLiquidityUnknown)
	}

	// Both flags: the VON lane is back, labelled with both verdicts, its
	// absurd figure preserved on the flagged row rather than "fixed".
	res = ComputeSpreads(book, fees, nil, nil, now, SpreadFilters{IncludeSuspect: true, IncludeUnknownLiquidity: true})
	von := findLane(res.Rows, "VON", "USDT", VenueGate, VenueMEXC)
	if von == nil {
		t.Fatal("both include flags did not return the VON lane")
	}
	if !von.Suspect || von.SuspectReason != SuspectSpreadImplausible {
		t.Fatalf("VON row flags = %+v", von)
	}
	if !von.LiquidityUnknown || von.LiquidityQuote != nil {
		t.Fatalf("VON row liquidity = %+v, want unknown/null", von)
	}
	if von.SpreadBpsNet.LessThan(decimal.NewFromInt(1_000_000_000_000)) {
		t.Fatalf("VON net bps = %s, expected the live order of magnitude on the flagged row", von.SpreadBpsNet)
	}
	if res.ExcludedSuspect != 0 || res.ExcludedLiquidityUnknown != 0 {
		t.Fatalf("excluded with both flags = %d / %d, want 0 / 0", res.ExcludedSuspect, res.ExcludedLiquidityUnknown)
	}

	// include_unknown_liquidity=1: TROLL gate->mexc appears with null
	// liquidity even though min_liquidity is set — an unknown is never
	// compared with a minimum.
	minLiq := decimal.NewFromInt(500)
	res = ComputeSpreads(book, fees, nil, nil, now, SpreadFilters{IncludeUnknownLiquidity: true, MinLiquidityQuote: &minLiq})
	troll := findLane(res.Rows, "TROLL", "USDT", VenueGate, VenueMEXC)
	if troll == nil || !troll.LiquidityUnknown || troll.LiquidityQuote != nil || troll.Suspect {
		t.Fatalf("TROLL gate->mexc with include_unknown_liquidity = %+v", troll)
	}
	if findLane(res.Rows, "VON", "USDT", VenueGate, VenueMEXC) != nil {
		t.Fatal("VON leaked through include_unknown_liquidity (it is suspect)")
	}
	if res.ExcludedSuspect != 8 || res.ExcludedLiquidityUnknown != 0 {
		t.Fatalf("excluded = %d / %d, want 8 / 0", res.ExcludedSuspect, res.ExcludedLiquidityUnknown)
	}
}

// TestComputeSpreadsQuoteSetNeverMerges: ?quote=USDT,USDC returns both
// quotes' lanes and nothing else; FDUSD/USD single-venue quotes are
// neither merged into USDT nor into each other.
func TestComputeSpreadsQuoteSetNeverMerges(t *testing.T) {
	now := time.Now().UTC()
	book := loadGuardFixture(t, now)
	res := ComputeSpreads(book, allFees(), nil, nil, now, SpreadFilters{Quotes: map[string]bool{"USDT": true, "USDC": true}, BasesAllow: map[string]bool{"BTC": true}})
	if res.Total != 4 {
		t.Fatalf("total = %d, want 4", res.Total)
	}
	seen := map[string]int{}
	for _, r := range res.Rows {
		seen[r.Quote]++
	}
	if seen["USDT"] != 2 || seen["USDC"] != 2 || len(seen) != 2 {
		t.Fatalf("quotes = %v", seen)
	}
	res = ComputeSpreads(book, allFees(), nil, nil, now, SpreadFilters{Quotes: map[string]bool{"FDUSD": true}})
	if res.Total != 0 {
		t.Fatalf("FDUSD alone has one venue; total = %d, want 0 (never merged with USDT)", res.Total)
	}
	res = ComputeSpreads(book, allFees(), nil, nil, now, SpreadFilters{Quotes: map[string]bool{"USDC": true}})
	if res.Total != 2 {
		t.Fatalf("USDC total = %d, want 2", res.Total)
	}
}

// TestComputeSpreadsMaxPlausibleFromSettings: the threshold is the
// settings field, not a constant — at 100 000 bps (1 000 %) the XTER
// lane (500 %) is no longer suspect, while VON still is.
func TestComputeSpreadsMaxPlausibleFromSettings(t *testing.T) {
	now := time.Now().UTC()
	book := loadGuardFixture(t, now)
	res := ComputeSpreads(book, allFees(), nil, nil, now, SpreadFilters{MaxPlausibleSpreadBps: decimal.NewFromInt(100000)})
	if findLane(res.Rows, "XTER", "USDT", VenueOKX, VenueBybit) == nil {
		t.Fatal("XTER excluded at max_plausible_spread_bps=100000")
	}
	if findLane(res.Rows, "VON", "USDT", VenueGate, VenueMEXC) != nil {
		t.Fatal("VON included at max_plausible_spread_bps=100000")
	}
}

func TestSettingsMaxPlausibleSpreadBps(t *testing.T) {
	s := Defaults()
	if !s.MaxPlausibleSpreadBps.Equal(decimal.NewFromInt(2000)) {
		t.Fatalf("default = %s, want 2000", s.MaxPlausibleSpreadBps)
	}
	if FieldTiming(s)["max_plausible_spread_bps"] != "hot" {
		t.Fatal("max_plausible_spread_bps must be hot")
	}
	for _, v := range []string{"99", "100001", "-1"} {
		s := Defaults()
		s.MaxPlausibleSpreadBps = d(v)
		if err := s.Validate(); err == nil {
			t.Fatalf("max_plausible_spread_bps=%s validated", v)
		}
	}
	for _, v := range []string{"100", "2000", "100000"} {
		s := Defaults()
		s.MaxPlausibleSpreadBps = d(v)
		if err := s.Validate(); err != nil {
			t.Fatalf("max_plausible_spread_bps=%s rejected: %v", v, err)
		}
	}
	// A stored document from before the field: zero validates and the
	// effective value is the default.
	s = Defaults()
	s.MaxPlausibleSpreadBps = decimal.Zero
	if err := s.Validate(); err != nil {
		t.Fatalf("zero (absent) rejected: %v", err)
	}
	if !s.EffectiveMaxPlausibleSpreadBps().Equal(DefaultMaxPlausibleSpreadBps) || !s.Normalised().MaxPlausibleSpreadBps.Equal(DefaultMaxPlausibleSpreadBps) {
		t.Fatal("effective/normalised default not applied")
	}
}
