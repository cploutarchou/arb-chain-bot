package exchange

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func stepRules(qtyStep, tick string) InstrumentRules {
	return InstrumentRules{
		QtyMode: PrecisionStep, QtyStep: d(qtyStep),
		PriceMode: PrecisionStep, PriceTick: d(tick),
	}
}

func TestQuantizeQtyStep(t *testing.T) {
	cases := []struct{ step, in, want string }{
		{"0.001", "1.23456", "1.234"},   // plain truncation to step
		{"0.001", "1.234", "1.234"},     // already on grid
		{"0.05", "1.234", "1.20"},       // non-power-of-ten step
		{"0.05", "0.04999", "0"},        // below one step
		{"1", "9.999", "9"},             // integer step
		{"0.00000001", "0.123456789", "0.12345678"}, // satoshi step
	}
	for _, c := range cases {
		r := stepRules(c.step, "0.01")
		got, err := r.QuantizeQty(d(c.in))
		if err != nil {
			t.Fatalf("step %s in %s: %v", c.step, c.in, err)
		}
		if !got.Equal(d(c.want)) {
			t.Errorf("step %s: QuantizeQty(%s) = %s, want %s", c.step, c.in, got, c.want)
		}
	}
}

func TestQuantizeQtyDecimals(t *testing.T) {
	r := InstrumentRules{QtyMode: PrecisionDecimals, QtyDecimals: 2,
		PriceMode: PrecisionDecimals, PriceDecimals: 4}
	got, err := r.QuantizeQty(d("1.239"))
	if err != nil || !got.Equal(d("1.23")) {
		t.Fatalf("QuantizeQty decimals = %s, err %v; want 1.23", got, err)
	}
	// Truncation, never rounding: 1.999 → 1.99, not 2.00.
	got, _ = r.QuantizeQty(d("1.999"))
	if !got.Equal(d("1.99")) {
		t.Fatalf("QuantizeQty(1.999) = %s, want 1.99", got)
	}
}

func TestQuantizeQtyErrors(t *testing.T) {
	r := stepRules("0.001", "0.01")
	if _, err := r.QuantizeQty(d("-1")); !errors.Is(err, ErrNegativeAmount) {
		t.Fatalf("negative qty: %v", err)
	}
	var unset InstrumentRules
	if _, err := unset.QuantizeQty(d("1")); !errors.Is(err, ErrRulesUnset) {
		t.Fatalf("unset rules: %v", err)
	}
}

func TestQuantizePriceDirections(t *testing.T) {
	r := stepRules("0.001", "0.05")
	down, _ := r.QuantizePriceDown(d("1.234"))
	up, _ := r.QuantizePriceUp(d("1.234"))
	if !down.Equal(d("1.20")) || !up.Equal(d("1.25")) {
		t.Fatalf("tick 0.05: down=%s up=%s, want 1.20/1.25", down, up)
	}
	// On-grid price is unchanged in both directions.
	down, _ = r.QuantizePriceDown(d("1.25"))
	up, _ = r.QuantizePriceUp(d("1.25"))
	if !down.Equal(d("1.25")) || !up.Equal(d("1.25")) {
		t.Fatalf("on-grid: down=%s up=%s, want 1.25/1.25", down, up)
	}

	rd := InstrumentRules{PriceMode: PrecisionDecimals, PriceDecimals: 2,
		QtyMode: PrecisionDecimals, QtyDecimals: 2}
	up, _ = rd.QuantizePriceUp(d("1.231"))
	if !up.Equal(d("1.24")) {
		t.Fatalf("decimals up: %s, want 1.24", up)
	}
}

func TestValidateOrderBoundaries(t *testing.T) {
	r := stepRules("0.001", "0.01")
	r.MinQty = d("0.01")
	r.MaxQty = d("100")
	r.MinNotional = d("10")

	// Exactly at min qty and min notional: valid.
	if err := r.ValidateOrder(d("1000"), d("0.01")); err != nil {
		t.Fatalf("boundary min: %v", err)
	}
	// One step below min qty.
	if err := r.ValidateOrder(d("1000"), d("0.009")); !errors.Is(err, ErrQtyBelowMin) {
		t.Fatalf("below min qty: %v", err)
	}
	// Notional 9.99 < 10.
	if err := r.ValidateOrder(d("999"), d("0.01")); !errors.Is(err, ErrNotionalBelowMin) {
		t.Fatalf("below min notional: %v", err)
	}
	// Above max qty.
	if err := r.ValidateOrder(d("1"), d("100.001")); !errors.Is(err, ErrQtyAboveMax) {
		t.Fatalf("above max qty: %v", err)
	}
}

// The min-notional floor vs depth ceiling interaction from
// resources/triangular-math.md: quantization can push a size below min
// notional — the validation must catch it AFTER truncation.
func TestQuantizeThenValidateInteraction(t *testing.T) {
	r := stepRules("0.01", "0.01")
	r.MinNotional = d("10")
	price := d("999")
	raw := d("0.010009") // pre-truncation notional ≈ 10.0
	q, _ := r.QuantizeQty(raw)
	if !q.Equal(d("0.01")) {
		t.Fatalf("quantized = %s", q)
	}
	// 999 * 0.01 = 9.99 < 10 → must fail.
	if err := r.ValidateOrder(price, q); !errors.Is(err, ErrNotionalBelowMin) {
		t.Fatalf("post-truncation notional must fail: %v", err)
	}
}

// Decimal JSON contract: quoted strings on the wire, both directions.
func TestDecimalJSONIsString(t *testing.T) {
	b, err := d("123.4500").MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"123.45"` {
		t.Fatalf("decimal JSON = %s, want quoted string", b)
	}
	var back decimal.Decimal
	if err := back.UnmarshalJSON([]byte(`"0.00000001"`)); err != nil {
		t.Fatal(err)
	}
	if !back.Equal(d("0.00000001")) {
		t.Fatalf("round-trip = %s", back)
	}
}

func TestMarketTradeable(t *testing.T) {
	m := Market{Status: MarketTrading, Enabled: true}
	if !m.Tradeable() {
		t.Fatal("enabled+trading must be tradeable")
	}
	for _, bad := range []Market{
		{Status: MarketHalted, Enabled: true},
		{Status: MarketTrading, Enabled: false},
	} {
		if bad.Tradeable() {
			t.Fatalf("must not be tradeable: %+v", bad)
		}
	}
}
