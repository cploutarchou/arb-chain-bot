package screener

import (
	"fmt"
	"testing"
	"time"
)

// Three lanes on three bases, each buy binance / sell okx, same fees:
//
//	ART: mids 20 %+ apart → suspect, net 10 000 bps
//	OLD: fresh bid leg against an ask leg 6 s old → stale, net 150 bps
//	FRESH: clean and current, net 30 bps
//
// Ranked by net alone the two artefacts occupy the top; ranked by
// quality first the genuine lane leads and a cap of one keeps it.
func rankBook(now time.Time) *Book {
	book := NewBook()
	set := func(base, ask, bid string, askAt time.Time) {
		book.SetQuote(Quote{Venue: VenueBinance, Base: base, Quote: "USDT", Ask: d(ask), AskQty: d("100"), Bid: d(ask).Sub(d("0.5")), BidQty: d("100"), At: askAt})
		book.SetQuote(Quote{Venue: VenueOKX, Base: base, Quote: "USDT", Bid: d(bid), BidQty: d("100"), Ask: d(bid).Add(d("0.5")), AskQty: d("100"), At: now})
	}
	set("ART", "100", "200", now)
	set("OLD", "100", "101.5", now.Add(-6*time.Second))
	set("FRESH", "100", "100.3", now)
	return book
}

func TestComputeSpreadsRanksCleanFreshLanesFirst(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	fees := fixedFees(map[Venue]string{VenueBinance: "0", VenueOKX: "0"})
	f := SpreadFilters{BuyVenues: map[Venue]bool{VenueBinance: true}, SellVenues: map[Venue]bool{VenueOKX: true},
		IncludeSuspect: true, IncludeUnknownLiquidity: true, MaxLegAge: 5 * time.Second}
	res := ComputeSpreads(rankBook(now), fees, nil, nil, now, f)
	if len(res.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(res.Rows))
	}
	if got := []string{res.Rows[0].Base, res.Rows[1].Base, res.Rows[2].Base}; got[0] != "FRESH" || got[1] != "ART" || got[2] != "OLD" {
		t.Fatalf("order = %v, want [FRESH ART OLD] (clean first, then artefacts by net)", got)
	}
	if !res.Rows[1].Suspect || res.Rows[2].Suspect {
		t.Fatalf("suspect flags = %v/%v", res.Rows[1].Suspect, res.Rows[2].Suspect)
	}
	// A cap of one now keeps the genuine lane, not the largest artefact.
	f.Limit = 1
	res = ComputeSpreads(rankBook(now), fees, nil, nil, now, f)
	if len(res.Rows) != 1 || res.Rows[0].Base != "FRESH" || res.Total != 3 {
		t.Fatalf("capped rows = %+v total %d, want FRESH only of 3", res.Rows, res.Total)
	}
	// Without MaxLegAge the stale lane is not down-ranked (the table
	// route's behaviour is unchanged); the suspect lane still is.
	f.Limit, f.MaxLegAge = 0, 0
	res = ComputeSpreads(rankBook(now), fees, nil, nil, now, f)
	if res.Rows[0].Base != "OLD" || res.Rows[1].Base != "FRESH" || res.Rows[2].Base != "ART" {
		t.Fatalf("order without MaxLegAge = %s %s %s, want OLD FRESH ART", res.Rows[0].Base, res.Rows[1].Base, res.Rows[2].Base)
	}
}

// TestComputeSpreadsNoLimitReturnsWholeUniverse: 600 bases on two venues
// give 1 200 ordered lanes; the page cap tops out at 500 while NoLimit
// returns every one of them (Total is the same either way).
func TestComputeSpreadsNoLimitReturnsWholeUniverse(t *testing.T) {
	book := NewBook()
	now := time.Unix(1_800_000_000, 0).UTC()
	for i := 0; i < 600; i++ {
		base := fmt.Sprintf("B%03d", i)
		book.SetQuote(Quote{Venue: VenueBinance, Base: base, Quote: "USDT", Ask: d("100"), AskQty: d("1"), Bid: d("99"), BidQty: d("1"), At: now})
		book.SetQuote(Quote{Venue: VenueOKX, Base: base, Quote: "USDT", Ask: d("101"), AskQty: d("1"), Bid: d("100.5"), BidQty: d("1"), At: now})
	}
	fees := fixedFees(map[Venue]string{VenueBinance: "0", VenueOKX: "0"})
	capped := ComputeSpreads(book, fees, nil, nil, now, SpreadFilters{Limit: 9999})
	if len(capped.Rows) != 500 || capped.Total != 1200 {
		t.Fatalf("capped: rows %d total %d, want 500 of 1200", len(capped.Rows), capped.Total)
	}
	all := ComputeSpreads(book, fees, nil, nil, now, SpreadFilters{NoLimit: true})
	if len(all.Rows) != 1200 || all.Total != 1200 {
		t.Fatalf("NoLimit: rows %d total %d, want 1200", len(all.Rows), all.Total)
	}
}
