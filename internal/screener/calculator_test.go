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
	})
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
	})
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
	})
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
	})
	if !errors.Is(err, ErrNoQuote) {
		t.Fatalf("err = %v, want ErrNoQuote", err)
	}
}

func TestCalculateInvalidSize(t *testing.T) {
	book := NewBook()
	fees := fixedFees(map[Venue]string{})
	_, err := Calculate(book, fees, CalculatorRequest{SizeQuote: decimal.Zero})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}
