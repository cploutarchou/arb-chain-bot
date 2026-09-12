package venue

import (
	"context"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// T-075 remainder per-venue fixture tests for the Tier-4 collectors:
// lane composition, scaled-integer decoding, filters (delisted spot,
// COIN-M perp), funding intervals, liquidity-unknown flags, the public
// network-status mapping and the per-poll call budgets.

func TestBithumbFixtures(t *testing.T) {
	c, fs := fixtureCollector(t, screener.VenueBithumb)
	ctx := context.Background()
	inst, err := c.Instruments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lanes := map[string]int{}
	for _, in := range inst {
		if in.Kind != KindSpot {
			t.Fatalf("bithumb lists a non-spot instrument: %+v", in)
		}
		lanes[in.Quote]++
		if in.Symbol != in.Quote+"-"+in.Base {
			t.Fatalf("bithumb symbol is not <quote>-<base>: %+v", in)
		}
	}
	if lanes["KRW"] == 0 || lanes["BTC"] == 0 {
		t.Fatalf("bithumb lanes: %+v", lanes)
	}

	quotes, err := c.Spot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range quotes {
		if q.LiquidityUnknown {
			t.Fatalf("bithumb books carry sizes — quote must not be liquidity-unknown: %+v", q)
		}
		if !q.BidQty.IsPositive() || !q.AskQty.IsPositive() {
			t.Fatalf("bithumb top-of-book size missing: %+v", q)
		}
		if q.Quote != "KRW" && q.Quote != "BTC" {
			t.Fatalf("bithumb quote lane: %+v", q)
		}
	}
	if fs.hits["/public/orderbook/ALL"] != 1 || fs.hits["/public/orderbook/ALL_BTC"] != 1 {
		t.Fatalf("bithumb book call budget: %+v", fs.hits)
	}

	nets, err := c.Networks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for asset, st := range nets {
		// The recorded window has every status 1: open, and the
		// conservative mapping means anything else would be closed with
		// the raw numbers as the reason.
		if st.Status == screener.NetworkUnknown {
			t.Fatalf("bithumb networks must come from assetsstatus: %s %+v", asset, st)
		}
	}

	if perps, _ := c.Perps(ctx); len(perps) != 0 {
		t.Fatalf("bithumb has no perps, got %d", len(perps))
	}
}

func TestPhemexFixtures(t *testing.T) {
	c, fs := fixtureCollector(t, screener.VenuePhemex)
	ctx := context.Background()
	inst, err := c.Instruments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var btcSpot, btcPerp, trySpot, coinm int
	intervals := map[int]int{}
	for _, in := range inst {
		switch {
		case in.Kind == KindSpot && in.Symbol == "BTCUSDT":
			btcSpot++
			// sBTCUSDT: quoteTickSizeEv 1e6 at priceScale 8 = 0.01;
			// minOrderValueEv 1e8 at USDT valueScale 8 = 1.
			if in.TickSize != "0.01" || in.MinNotional != "1" {
				t.Fatalf("phemex BTCUSDT spot limits misdecoded: %+v", in)
			}
		case in.Kind == KindPerp && in.Symbol == "BTCUSDT":
			btcPerp++
			if in.Quote != "USDT" {
				t.Fatalf("phemex perp shape: %+v", in)
			}
		case in.Kind == KindSpot && in.Symbol == "USDTTRY":
			trySpot++
			if in.Tradable {
				t.Fatalf("delisted TRY row must not be tradable: %+v", in)
			}
		case in.Symbol == "BTCUSD":
			coinm++ // the COIN-M row must not exist as either kind
		}
		if in.Kind == KindPerp {
			if in.Quote != "USDT" {
				t.Fatalf("non-USDT-settled perp leaked: %+v", in)
			}
			if in.IntervalH != 4 && in.IntervalH != 8 {
				t.Fatalf("phemex funding interval not 4h/8h: %+v", in)
			}
			intervals[in.IntervalH]++
		}
	}
	if btcSpot != 1 || btcPerp != 1 || trySpot != 1 || coinm != 0 {
		t.Fatalf("phemex instruments: btcSpot=%d btcPerp=%d trySpot=%d coinm=%d", btcSpot, btcPerp, trySpot, coinm)
	}
	if len(intervals) < 2 {
		t.Fatalf("fixture should exercise both funding intervals, got %+v", intervals)
	}

	quotes, err := c.Spot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range quotes {
		if !q.LiquidityUnknown {
			t.Fatalf("phemex bulk spot ticker has no sizes: %+v", q)
		}
		if q.Base == "BTC" && q.Quote == "USDT" {
			// Recorded: bidEp 7722347000000 at priceScale 8 → 77223.47.
			if q.Bid.String() != "77223.47" {
				t.Fatalf("phemex Ep decoding drifted: %s", q.Bid)
			}
		}
	}

	perps, err := c.Perps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range perps {
		if !p.NextFundingAt.IsZero() {
			t.Fatalf("phemex publishes no next-funding time on the bulk ticker — it must stay zero, not be synthesised: %+v", p)
		}
		if p.BidQty.IsPositive() || p.AskQty.IsPositive() {
			t.Fatalf("phemex bulk perp ticker has no sizes: %+v", p)
		}
	}

	nets, err := c.Networks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for asset, st := range nets {
		if st.Status != screener.NetworkUnknown || st.Reason != KeyGatedReason {
			t.Fatalf("phemex is key-gated for networks: %s %+v", asset, st)
		}
	}

	if fs.hits["/public/products"] == 0 || fs.hits["/md/spot/ticker/24hr/all"] != 1 || fs.hits["/md/v3/ticker/24hr/all"] != 1 {
		t.Fatalf("phemex call budget: %+v", fs.hits)
	}
}

// TestPhemexFundingIntervalEvidence pins the 8 h / 4 h intervals the
// collector reads from perpProductsV2.fundingInterval to the recorded
// funding-rate-history rows (intervalSeconds) — the two independent
// sources must agree for BTCUSDT.
func TestPhemexFundingIntervalEvidence(t *testing.T) {
	c, _ := fixtureCollector(t, screener.VenuePhemex)
	inst, err := c.Instruments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	btc := -1
	for _, in := range inst {
		if in.Kind == KindPerp && in.Symbol == "BTCUSDT" {
			btc = in.IntervalH
		}
	}
	if btc != 8 {
		t.Fatalf("BTCUSDT funding interval = %dh, want 8 (recorded history intervalSeconds 28800)", btc)
	}
}
