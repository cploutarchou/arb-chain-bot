package screener

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// TestComputePerpsGoldenMath is the hand-computed golden test (design
// §3): funding 0.0001 at interval_h 8 -> gross APR 0.1095; spot taker
// 10 bps + perp taker 5 bps -> net APR 0.073 at hold_days_assumed 30.
func TestComputePerpsGoldenMath(t *testing.T) {
	book := NewBook()
	now := time.Unix(1_800_000_000, 0).UTC()
	book.SetQuote(Quote{Venue: VenueBinance, Base: "BTC", Quote: "USDT", Bid: d("99.9"), Ask: d("100.1"), At: now})
	book.SetPerp(Perp{
		Venue: VenueBinance, Base: "BTC", Quote: "USDT",
		Mark: d("100.5"), Index: d("100.4"),
		FundingRate: d("0.0001"), IntervalH: 8,
		At: now,
	})
	spotMid := func(v Venue, base, quote string) (decimal.Decimal, bool) {
		q, ok := book.QuotesFor(base, quote)[v]
		if !ok {
			return decimal.Decimal{}, false
		}
		return q.Bid.Add(q.Ask).Div(decimal.NewFromInt(2)), true
	}
	fees := fixedFees(map[Venue]string{VenueBinance: "10"})
	perpFees := fixedFees(map[Venue]string{VenueBinance: "5"})

	rows := ComputePerps(book, spotMid, fees, perpFees, now, PerpFilters{})
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if !row.CarryAPRGross.Equal(d("0.1095")) {
		t.Fatalf("carry_apr_gross = %s, want 0.1095", row.CarryAPRGross)
	}
	if !row.CarryAPRNet.Equal(d("0.073")) {
		t.Fatalf("carry_apr_net = %s, want 0.073", row.CarryAPRNet)
	}
	if row.HoldDaysAssumed != 30 {
		t.Fatalf("hold_days_assumed = %d, want 30", row.HoldDaysAssumed)
	}
	wantMid := d("100")
	if !row.SpotMid.Equal(wantMid) {
		t.Fatalf("spot_mid = %s, want %s", row.SpotMid, wantMid)
	}
	wantBasis := d("50") // (100.5-100)/100*10000
	if !row.BasisBps.Equal(wantBasis) {
		t.Fatalf("basis_bps = %s, want %s", row.BasisBps, wantBasis)
	}
}

func TestComputePerpsSkipsMissingSpotMid(t *testing.T) {
	book := NewBook()
	now := time.Now().UTC()
	book.SetPerp(Perp{Venue: VenueBinance, Base: "BTC", Quote: "USDT", Mark: d("100"), FundingRate: d("0.0001"), IntervalH: 8, At: now})
	noSpot := func(Venue, string, string) (decimal.Decimal, bool) { return decimal.Decimal{}, false }
	fees := fixedFees(map[Venue]string{VenueBinance: "10"})
	rows := ComputePerps(book, noSpot, fees, fees, now, PerpFilters{})
	if len(rows) != 0 {
		t.Fatalf("rows = %d, want 0 (no spot mid for this venue's own book)", len(rows))
	}
}

func TestComputePerpsGuardsZeroInterval(t *testing.T) {
	book := NewBook()
	now := time.Now().UTC()
	book.SetPerp(Perp{Venue: VenueBinance, Base: "BTC", Quote: "USDT", Mark: d("100"), FundingRate: d("0.0001"), IntervalH: 0, At: now})
	spotMid := func(Venue, string, string) (decimal.Decimal, bool) { return d("100"), true }
	fees := fixedFees(map[Venue]string{VenueBinance: "10"})
	rows := ComputePerps(book, spotMid, fees, fees, now, PerpFilters{})
	if len(rows) != 0 {
		t.Fatalf("rows = %d, want 0 (zero interval_h must not divide-by-zero)", len(rows))
	}
}

func TestComputePerpsSortedAndFiltered(t *testing.T) {
	book := NewBook()
	now := time.Now().UTC()
	book.SetQuote(Quote{Venue: VenueBinance, Base: "BTC", Quote: "USDT", Bid: d("100"), Ask: d("100"), At: now})
	book.SetQuote(Quote{Venue: VenueBinance, Base: "ETH", Quote: "USDT", Bid: d("50"), Ask: d("50"), At: now})
	book.SetPerp(Perp{Venue: VenueBinance, Base: "BTC", Quote: "USDT", Mark: d("100"), FundingRate: d("0.0001"), IntervalH: 8, At: now})
	book.SetPerp(Perp{Venue: VenueBinance, Base: "ETH", Quote: "USDT", Mark: d("50"), FundingRate: d("0.001"), IntervalH: 8, At: now})
	spotMid := func(v Venue, base, quote string) (decimal.Decimal, bool) {
		q, ok := book.QuotesFor(base, quote)[v]
		if !ok {
			return decimal.Decimal{}, false
		}
		return q.Bid.Add(q.Ask).Div(decimal.NewFromInt(2)), true
	}
	fees := fixedFees(map[Venue]string{VenueBinance: "0"})
	rows := ComputePerps(book, spotMid, fees, fees, now, PerpFilters{})
	if len(rows) != 2 || rows[0].Base != "ETH" {
		t.Fatalf("rows = %+v, want ETH (higher funding) sorted first", rows)
	}
	minAPR := d("1.0")
	filtered := ComputePerps(book, spotMid, fees, fees, now, PerpFilters{MinCarryAPR: &minAPR})
	if len(filtered) != 1 || filtered[0].Base != "ETH" {
		t.Fatalf("min_carry_apr filter = %+v, want only ETH (carry_apr_net 1.095 >= 1.0; BTC's 0.1095 excluded)", filtered)
	}
}
