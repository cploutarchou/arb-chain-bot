package alerts

import (
	"context"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// T-097's remaining half, decided 2026-08-29: a carry is refused unless
// its funding is confirmed by SETTLED points, and unless those points
// agree in sign with the venue's forecast.
//
// The hole was structural rather than statistical. meanSettled returns
// (0, 0) when the look-back finds nothing, and its caller then
// substitutes the predicted rate for the settled mean — so
// FHatBps = min(predicted, predicted) = predicted, and the "conservative"
// estimator quietly compares the forecast against itself. Every number
// downstream of it, breakeven included, then rests on one unverified
// figure published by the venue whose perp we are about to trade.
func TestCarryRequiresConfirmedFunding(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)
	apr := d("0")
	r := screener.Rule{ID: "c1", Name: "carry", Enabled: true, Kind: screener.RuleKindCarry, MinCarryAPR: &apr,
		BuyVenues: []screener.Venue{screener.VenueBinance}}

	// A book whose predicted funding is healthy enough to clear every
	// other gate; only the settled history varies below.
	setBook := func() {
		setCarry(svc.Book, t0)
		p, _ := svc.Book.PerpFor(screener.VenueBinance, "BTC", "USDT")
		p.PredictedFundingRate = d("0.0005")
		p.FundingRate = d("0.0005")
		p.Bid = d("50100")
		svc.Book.SetPerp(p)
	}
	seed := func(rate string, n int) screener.FundingStore {
		fs := screener.NewMemoryFundingStore()
		for i := 1; i <= n; i++ {
			_ = fs.UpsertFunding(ctx, screener.VenueBinance, "BTC", t0.Add(-time.Duration(i*8)*time.Hour), rate)
		}
		return fs
	}
	inputs := func(fs screener.FundingStore) Inputs {
		return Inputs{Book: svc.Book, SpotFees: feeLookup(svc, false), PerpFees: feeLookup(svc, true),
			FundingHistory: fs, PollInterval: 5 * time.Second}
	}

	cases := []struct {
		name       string
		history    screener.FundingStore
		wantActive bool
		wantReason string
	}{
		{
			// The universe T-097 left open: a perp nobody has settled
			// funding for, where the forecast is the only evidence.
			name: "no settled history is refused", history: seed("0.0005", 0),
			wantActive: false, wantReason: "funding_unconfirmed",
		},
		{
			// Settled funding contradicts the forecast. harvestActive has
			// refused this since it was written; carry did not.
			name: "settled funding of the opposite sign is refused", history: seed("-0.0005", 30),
			wantActive: false, wantReason: "funding_not_consistent",
		},
		{
			name: "confirmed positive funding is admitted", history: seed("0.0005", 30),
			wantActive: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setBook()
			sigs := ComputeSignals(ctx, inputs(tc.history), r, t0)
			if len(sigs) == 0 {
				t.Fatal("no signal computed")
			}
			s := sigs[0]
			if s.Active != tc.wantActive {
				t.Fatalf("active = %v (%s), want %v", s.Active, s.Reason, tc.wantActive)
			}
			if tc.wantReason != "" && s.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", s.Reason, tc.wantReason)
			}
			if tc.wantActive && s.BreakevenN < 1 {
				// The exit rule (T-100) waits for BreakevenN settlements,
				// so an admitted carry must carry a real number: zero
				// would mean "closeable on the first settlement" again.
				t.Errorf("admitted carry has BreakevenN = %d, want >= 1", s.BreakevenN)
			}
		})
	}
}

// TestSettledNIsRecorded pins the field the gates read, since a signal
// that silently reported zero settled points would refuse everything.
func TestSettledNIsRecorded(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)
	apr := d("0")
	r := screener.Rule{ID: "c2", Name: "carry", Enabled: true, Kind: screener.RuleKindCarry, MinCarryAPR: &apr,
		BuyVenues: []screener.Venue{screener.VenueBinance}}
	setCarry(svc.Book, t0)
	p, _ := svc.Book.PerpFor(screener.VenueBinance, "BTC", "USDT")
	p.PredictedFundingRate = d("0.0005")
	p.FundingRate = d("0.0005")
	p.Bid = d("50100")
	svc.Book.SetPerp(p)

	fs := screener.NewMemoryFundingStore()
	const points = 12
	for i := 1; i <= points; i++ {
		_ = fs.UpsertFunding(ctx, screener.VenueBinance, "BTC", t0.Add(-time.Duration(i*8)*time.Hour), "0.0005")
	}
	in := Inputs{Book: svc.Book, SpotFees: feeLookup(svc, false), PerpFees: feeLookup(svc, true),
		FundingHistory: fs, PollInterval: 5 * time.Second}
	s := ComputeSignals(ctx, in, r, t0)[0]
	if s.SettledN != points {
		t.Errorf("SettledN = %d, want %d", s.SettledN, points)
	}
}
