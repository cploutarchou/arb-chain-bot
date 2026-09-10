package screener

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestCalculateGoldenMath(t *testing.T) {
	book := NewBook()
	now := time.Now().UTC()
	book.SetQuote(Quote{Venue: VenueBinance, Base: "BTC", Quote: "USDT", Ask: d("100"), AskQty: d("1000"), Bid: d("99"), BidQty: d("1000"), At: now})
	book.SetQuote(Quote{Venue: VenueOKX, Base: "BTC", Quote: "USDT", Ask: d("102"), AskQty: d("1000"), Bid: d("101"), BidQty: d("1000"), At: now})
	fees := fixedFees(map[Venue]string{VenueBinance: "10", VenueOKX: "10"})

	res, err := Calculate(book, fees, CalculatorRequest{
		Base: "BTC", Quote: "USDT", BuyVenue: VenueBinance, SellVenue: VenueOKX,
		SizeQuote: d("1000"),
	}, now, decimal.Zero)
	if err != nil {
		t.Fatal(err)
	}
	// size_base = 1000/100 = 10; proceeds = 10*101 = 1010; gross = 1010-1000 = 10.
	if !res.SizeBase.Equal(d("10")) {
		t.Fatalf("size_base = %s, want 10", res.SizeBase)
	}
	if !res.Gross.Equal(d("10")) {
		t.Fatalf("gross = %s, want 10", res.Gross)
	}
	// fees_buy = 1000 * 0.001 = 1; fees_sell = 1010 * 0.001 = 1.01.
	if !res.FeesBuy.Equal(d("1")) {
		t.Fatalf("fees_buy = %s, want 1", res.FeesBuy)
	}
	if !res.FeesSell.Equal(d("1.01")) {
		t.Fatalf("fees_sell = %s, want 1.01", res.FeesSell)
	}
	// net = 10 - 1 - 1.01 - 0(transfer) = 7.99; net_bps = 7.99/1000*10000 = 79.9.
	if !res.Net.Equal(d("7.99")) {
		t.Fatalf("net = %s, want 7.99", res.Net)
	}
	if !res.NetBps.Equal(d("79.9")) {
		t.Fatalf("net_bps = %s, want 79.9", res.NetBps)
	}
	if !res.LiquidityOK {
		t.Fatal("liquidity_ok = false, want true (top-of-book plenty deep)")
	}
}

func TestCalculateWithTransferFeeAndOverrides(t *testing.T) {
	book := NewBook()
	now := time.Now().UTC()
	book.SetQuote(Quote{Venue: VenueBinance, Base: "BTC", Quote: "USDT", Ask: d("100"), AskQty: d("1000"), Bid: d("99"), BidQty: d("1000"), At: now})
	book.SetQuote(Quote{Venue: VenueOKX, Base: "BTC", Quote: "USDT", Ask: d("102"), AskQty: d("1000"), Bid: d("101"), BidQty: d("1000"), At: now})
	fees := fixedFees(map[Venue]string{VenueBinance: "10", VenueOKX: "10"})

	transferFee := d("2")
	overrideBuy := d("0")
	overrideSell := d("0")
	res, err := Calculate(book, fees, CalculatorRequest{
		Base: "BTC", Quote: "USDT", BuyVenue: VenueBinance, SellVenue: VenueOKX,
		SizeQuote: d("1000"), TransferFeeQuote: &transferFee,
		OverrideBuyFeeBps: &overrideBuy, OverrideSellFeeBps: &overrideSell,
	}, now, decimal.Zero)
	if err != nil {
		t.Fatal(err)
	}
	if !res.FeesBuy.IsZero() || !res.FeesSell.IsZero() {
		t.Fatalf("fee overrides not applied: buy=%s sell=%s", res.FeesBuy, res.FeesSell)
	}
	// net = gross(10) - 0 - 0 - transfer(2) = 8.
	if !res.Net.Equal(d("8")) {
		t.Fatalf("net = %s, want 8", res.Net)
	}
	if !res.TransferFee.Equal(d("2")) {
		t.Fatalf("transfer_fee = %s, want 2", res.TransferFee)
	}
}

func TestCalculateLiquidityNotOK(t *testing.T) {
	book := NewBook()
	now := time.Now().UTC()
	book.SetQuote(Quote{Venue: VenueBinance, Base: "BTC", Quote: "USDT", Ask: d("100"), AskQty: d("1"), Bid: d("99"), BidQty: d("1"), At: now})
	book.SetQuote(Quote{Venue: VenueOKX, Base: "BTC", Quote: "USDT", Ask: d("102"), AskQty: d("1"), Bid: d("101"), BidQty: d("1"), At: now})
	fees := fixedFees(map[Venue]string{VenueBinance: "10", VenueOKX: "10"})

	res, err := Calculate(book, fees, CalculatorRequest{
		Base: "BTC", Quote: "USDT", BuyVenue: VenueBinance, SellVenue: VenueOKX,
		SizeQuote: d("1000"), // far exceeds top-of-book depth (100*1=100 quote)
	}, now, decimal.Zero)
	if err != nil {
		t.Fatal(err)
	}
	if res.LiquidityOK {
		t.Fatal("liquidity_ok = true, want false (size far exceeds top-of-book)")
	}
}

func TestCalculateNoQuoteError(t *testing.T) {
	book := NewBook()
	fees := fixedFees(map[Venue]string{VenueBinance: "10", VenueOKX: "10"})
	_, err := Calculate(book, fees, CalculatorRequest{
		Base: "BTC", Quote: "USDT", BuyVenue: VenueBinance, SellVenue: VenueOKX, SizeQuote: d("100"),
	}, time.Now().UTC(), decimal.Zero)
	if !errors.Is(err, ErrNoQuote) {
		t.Fatalf("err = %v, want ErrNoQuote", err)
	}
}

func TestCalculateInvalidSize(t *testing.T) {
	book := NewBook()
	fees := fixedFees(map[Venue]string{})
	_, err := Calculate(book, fees, CalculatorRequest{SizeQuote: decimal.Zero}, time.Now().UTC(), decimal.Zero)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// TestCalculateRefusesSuspectLaneAndReportsAges (audit X11): the
// calculator applies the shared identity guard — a lane whose sides
// cannot be the same asset is refused, never priced — and every answer
// carries how old each side's quote was.
func TestCalculateRefusesSuspectLaneAndReportsAges(t *testing.T) {
	book := NewBook()
	now := time.Now().UTC()
	// The live VON shape: one venue's "VON" is another asset entirely.
	book.SetQuote(Quote{Venue: VenueBinance, Base: "VON", Quote: "USDT", Ask: d("0.02"), AskQty: d("1000"), Bid: d("0.019"), BidQty: d("1000"), At: now.Add(-2 * time.Second)})
	book.SetQuote(Quote{Venue: VenueMEXC, Base: "VON", Quote: "USDT", Ask: d("40"), AskQty: d("1000"), Bid: d("39"), BidQty: d("1000"), At: now.Add(-5 * time.Second)})
	fees := fixedFees(map[Venue]string{VenueBinance: "10", VenueMEXC: "10"})

	_, err := Calculate(book, fees, CalculatorRequest{
		Base: "VON", Quote: "USDT", BuyVenue: VenueBinance, SellVenue: VenueMEXC,
		SizeQuote: d("10"),
	}, now, decimal.Zero)
	if !errors.Is(err, ErrSuspectLane) {
		t.Fatalf("err = %v, want ErrSuspectLane", err)
	}

	// A plausible lane answers with the ages of the quotes it used.
	book.SetQuote(Quote{Venue: VenueMEXC, Base: "VON", Quote: "USDT", Ask: d("0.021"), AskQty: d("1000"), Bid: d("0.0205"), BidQty: d("1000"), At: now.Add(-5 * time.Second)})
	res, err := Calculate(book, fees, CalculatorRequest{
		Base: "VON", Quote: "USDT", BuyVenue: VenueBinance, SellVenue: VenueMEXC,
		SizeQuote: d("10"),
	}, now, decimal.Zero)
	if err != nil {
		t.Fatal(err)
	}
	if res.BuyAgeMs < 1900 || res.SellAgeMs < 4900 {
		t.Fatalf("ages = %d/%d ms, want ~2000/5000", res.BuyAgeMs, res.SellAgeMs)
	}
	if res.Suspect || res.LiquidityUnknown {
		t.Fatalf("guard verdict leaked into a clean lane: %+v", res)
	}
}
