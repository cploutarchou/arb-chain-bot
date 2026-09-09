package screener

import (
	"testing"
	"time"
)

// TestBookKeepsOneEntryPerPerpContract reproduces the Binance USDⓈ-M
// collision: the venue lists AAVEUSDT and AAVEUSDC, and a store keyed
// (venue, base) let whichever was written last overwrite the other, so
// a USDT position could be marked against the USDC contract. Keyed
// (venue, base, quote) both survive and each lookup names its contract.
func TestBookKeepsOneEntryPerPerpContract(t *testing.T) {
	book := NewBook()
	now := time.Unix(1_800_000_000, 0).UTC()
	usdt := Perp{Venue: VenueBinance, Base: "AAVE", Quote: "USDT", Mark: d("300"), Bid: d("299.9"), Ask: d("300.1"),
		FundingRate: d("0.0001"), IntervalH: 8, NextFundingAt: now.Add(time.Hour), At: now}
	usdc := usdt
	usdc.Quote, usdc.Mark, usdc.FundingRate = "USDC", d("301"), d("-0.0003")
	book.SetPerp(usdt) // the fixture order: AAVEUSDT precedes AAVEUSDC
	book.SetPerp(usdc)

	got, ok := book.PerpFor(VenueBinance, "AAVE", "USDT")
	if !ok || got.Quote != "USDT" || !got.Mark.Equal(d("300")) || !got.FundingRate.Equal(d("0.0001")) {
		t.Fatalf("USDT contract = %+v ok=%v; want the USDT mark 300 and rate 0.0001, not the USDC contract's", got, ok)
	}
	got, ok = book.PerpFor(VenueBinance, "AAVE", "USDC")
	if !ok || got.Quote != "USDC" || !got.Mark.Equal(d("301")) {
		t.Fatalf("USDC contract = %+v ok=%v", got, ok)
	}
	if _, ok := book.PerpFor(VenueBinance, "AAVE", ""); ok {
		t.Fatal("a lookup without a quote must not resolve to either contract")
	}
	all := book.Perps()
	if len(all) != 2 || all[0].Quote != "USDC" || all[1].Quote != "USDT" {
		t.Fatalf("Perps() = %+v, want both contracts sorted by (venue, base, quote)", all)
	}
	// Overwrite is per contract: a fresher USDT snapshot replaces only
	// the USDT entry.
	usdt.Mark = d("305")
	book.SetPerp(usdt)
	if got, _ := book.PerpFor(VenueBinance, "AAVE", "USDT"); !got.Mark.Equal(d("305")) {
		t.Fatalf("USDT mark after overwrite = %s, want 305", got.Mark)
	}
	if got, _ := book.PerpFor(VenueBinance, "AAVE", "USDC"); !got.Mark.Equal(d("301")) {
		t.Fatalf("USDC mark changed by a USDT write: %s", got.Mark)
	}
}

// TestDataAgeOKGolden pins the shared §1.1 gate at a 5 s window: each
// leg ≤ 5 s old and the legs within 2.5 s of each other.
func TestDataAgeOKGolden(t *testing.T) {
	const poll = 5 * time.Second
	cases := []struct {
		name   string
		a, b   time.Duration
		wantOK bool
	}{
		{"both fresh", 0, 0, true},
		{"at the limit", 5 * time.Second, 5 * time.Second, true},
		{"one leg 6 s", 6 * time.Second, 0, false},
		{"skew 3 s", 3 * time.Second, 0, false},
		{"skew 2.5 s", 2500 * time.Millisecond, 0, true},
		{"future stamp", -time.Second, 0, false},
	}
	for _, c := range cases {
		if got := DataAgeOK(c.a, c.b, poll); got != c.wantOK {
			t.Errorf("%s: DataAgeOK(%s, %s) = %v, want %v", c.name, c.a, c.b, got, c.wantOK)
		}
	}
}
