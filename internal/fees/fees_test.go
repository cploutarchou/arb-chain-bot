package fees

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var (
	btcusdt = exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"}
	promo   = exchange.MarketID{Exchange: "binance", Symbol: "BTCFDUSD"}
)

func schedule(t *testing.T) *Schedule {
	t.Helper()
	s, err := NewSchedule("binance", exchange.FeeInReceived,
		Rate{Maker: d("0.001"), Taker: d("0.001")})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDefaultMustBePositive(t *testing.T) {
	_, err := NewSchedule("x", exchange.FeeInReceived, Rate{Maker: d("0"), Taker: d("0.001")})
	if !errors.Is(err, ErrNonPositiveDefault) {
		t.Fatalf("zero default accepted: %v", err)
	}
	if _, err := NewSchedule("x", "", Rate{Maker: d("0.001"), Taker: d("0.001")}); !errors.Is(err, ErrNoConvention) {
		t.Fatalf("missing convention accepted: %v", err)
	}
}

func TestResolutionPrecedence(t *testing.T) {
	s := schedule(t)
	if err := s.SetOverride(promo, Rate{Maker: d("0"), Taker: d("0")}); err != nil {
		t.Fatal(err)
	}
	if got := s.Taker(btcusdt); !got.Rate.Equal(d("0.001")) || got.Source != "default" {
		t.Fatalf("default resolution: %+v", got)
	}
	// Verified promo override may be zero.
	if got := s.Taker(promo); !got.Rate.IsZero() || got.Source != "override" {
		t.Fatalf("promo resolution: %+v", got)
	}
	if err := s.SetOverride(promo, Rate{Taker: d("-0.001")}); err == nil {
		t.Fatal("negative override accepted")
	}
}

func TestDiscountMath(t *testing.T) {
	s := schedule(t)
	s.Discount = Discount{Enabled: true, Rate: d("0.25"), PayAsset: "BNB", AppliesToAPI: true}
	got := s.Taker(btcusdt)
	// 10 bps * (1-0.25) = 7.5 bps.
	if !got.Rate.Equal(d("0.00075")) || !got.TokenPaid || got.PayAsset != "BNB" {
		t.Fatalf("discounted: %+v", got)
	}
	if !Bps(got.Rate).Equal(d("7.5")) {
		t.Fatalf("bps = %s", Bps(got.Rate))
	}
	// A zero-rate promo pair stays zero — no discount source suffix.
	_ = s.SetOverride(promo, Rate{Maker: d("0"), Taker: d("0")})
	if got := s.Taker(promo); !got.Rate.IsZero() || got.TokenPaid {
		t.Fatalf("promo with discount: %+v", got)
	}
}

// Bybit MNT case: discount excluded for API flow must never be applied.
func TestDiscountExcludedForAPI(t *testing.T) {
	s := schedule(t)
	s.Discount = Discount{Enabled: true, Rate: d("0.25"), PayAsset: "MNT", AppliesToAPI: false}
	got := s.Taker(btcusdt)
	if !got.Rate.Equal(d("0.001")) || got.TokenPaid {
		t.Fatalf("API-excluded discount applied: %+v", got)
	}
}

func TestPlacementTable(t *testing.T) {
	cases := []struct {
		conv exchange.FeeConvention
		side exchange.Side
		want Placement
	}{
		{exchange.FeeInReceived, exchange.SideBuy, FeeOnOutput},
		{exchange.FeeInReceived, exchange.SideSell, FeeOnOutput},
		{exchange.FeeInQuote, exchange.SideBuy, FeeOnInput},
		{exchange.FeeInQuote, exchange.SideSell, FeeOnOutput},
		{exchange.FeeInSpent, exchange.SideBuy, FeeOnInput},
		{exchange.FeeInSpent, exchange.SideSell, FeeOnInput},
	}
	for _, c := range cases {
		got, err := PlacementFor(c.conv, c.side)
		if err != nil || got != c.want {
			t.Fatalf("PlacementFor(%s,%s) = %v,%v want %v", c.conv, c.side, got, err, c.want)
		}
	}
	if _, err := PlacementFor("", exchange.SideBuy); err == nil {
		t.Fatal("unset convention accepted")
	}
}

func TestUsableInput(t *testing.T) {
	// 1000 quote at 10 bps input-side: usable = 1000/1.001.
	usable, fee := UsableInput(FeeOnInput, d("1000"), d("0.001"))
	if !usable.Add(fee).Equal(d("1000")) {
		t.Fatalf("conservation: usable %s + fee %s != 1000", usable, fee)
	}
	// usable * (1+rate) ≈ 1000 within division precision.
	back := usable.Mul(d("1.001"))
	if back.Sub(d("1000")).Abs().GreaterThan(d("0.000000000000000001")) {
		t.Fatalf("usable*1.001 = %s", back)
	}
	// Output placement: input untouched.
	usable, fee = UsableInput(FeeOnOutput, d("1000"), d("0.001"))
	if !usable.Equal(d("1000")) || !fee.IsZero() {
		t.Fatalf("output placement altered input: %s/%s", usable, fee)
	}
}

func TestNetOutput(t *testing.T) {
	// 2 BTC received at 10 bps: fee 0.002 BTC, net 1.998.
	net, fee := NetOutput(FeeOnOutput, d("2"), d("0.001"))
	if !net.Equal(d("1.998")) || !fee.Equal(d("0.002")) {
		t.Fatalf("net=%s fee=%s", net, fee)
	}
	// Zero rate (promo): untouched.
	net, fee = NetOutput(FeeOnOutput, d("2"), d("0"))
	if !net.Equal(d("2")) || !fee.IsZero() {
		t.Fatalf("zero-rate: net=%s fee=%s", net, fee)
	}
}
