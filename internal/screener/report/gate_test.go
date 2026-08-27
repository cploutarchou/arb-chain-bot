package report

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/paperexec"
)

func byItem(items []GateItem) map[int]GateItem {
	out := map[int]GateItem{}
	for _, it := range items {
		out[it.Item] = it
	}
	return out
}

// TestChecklistNoEvidenceOnSmallSample: with 6 samples every item
// fails; the sample-dependent ones say "no evidence yet" with the floor;
// nothing is softened into a pass.
func TestChecklistNoEvidenceOnSmallSample(t *testing.T) {
	st := Compute(synthetic())
	items := Checklist(screener.StrategyCrossVenueSpot, st)
	if len(items) != 8 || Passed(items) != 0 {
		t.Fatalf("items = %d, passed = %d: %+v", len(items), Passed(items), items)
	}
	m := byItem(items)
	for _, n := range []int{1, 2, 3, 4, 6, 7, 8} {
		if !strings.Contains(m[n].Reason, NoEvidence) {
			t.Fatalf("item %d reason %q lacks %q", n, m[n].Reason, NoEvidence)
		}
	}
	if !strings.Contains(m[2].Reason, "n = 6 (floor 200)") || !strings.Contains(m[2].Reason, "matched pairs = 2 (floor 60)") {
		t.Fatalf("item 2 reason = %q", m[2].Reason)
	}
	if !strings.Contains(m[5].Reason, "stress grid") || m[5].Status != GateFail {
		t.Fatalf("item 5 = %+v", m[5])
	}
	if !strings.Contains(m[8].Reason, "LIVE stays disabled") {
		t.Fatalf("item 8 = %+v", m[8])
	}
}

// TestChecklistLargeSampleComputesItems4And6: a synthetic 240-execution
// ledger over 40 days (all wins, small losses on a few) passes the
// computable items (4, 6) and still fails the regime / stress / manual
// items — the gate cannot be fully passed by the ledger alone.
func TestChecklistLargeSampleComputesItems4And6(t *testing.T) {
	var execs []paperexec.Execution
	day := t0
	id := 0
	for dIdx := 0; dIdx < 40; dIdx++ {
		for k := 0; k < 6; k++ {
			id++
			pnl := "4"
			if k == 5 {
				pnl = "-1"
			}
			a, b := screener.VenueBinance, screener.VenueOKX
			if k%2 == 1 {
				a, b = b, a
			}
			execs = append(execs, spotExec("x"+itoa(id), day.Add(time.Duration(k)*time.Hour), a, b, "0.1", "50000", pnl, strp("1")))
		}
		day = day.Add(24 * time.Hour)
	}
	in := Inputs{Strategy: screener.StrategyCrossVenueSpot, Executions: execs,
		Rules:  []screener.Rule{{ID: "r1", Kind: screener.RuleKindSpread}},
		Window: Window{Label: PeriodCumulative, Start: t0, End: day}, Capital: decimal.NewFromInt(100000), Now: day, Seed: 1,
		SpotFee: func(screener.Venue) (decimal.Decimal, bool) { return decimal.NewFromInt(10), true },
		Mid: func(screener.Venue, string, string) (decimal.Decimal, int64, bool) {
			return decimal.NewFromInt(50000), 0, true
		}}
	st := Compute(in)
	if st.N != 240 || st.MatchedPairs != 120 || st.Days != 40 {
		t.Fatalf("n=%d matched=%d days=%d", st.N, st.MatchedPairs, st.Days)
	}
	items := Checklist(screener.StrategyCrossVenueSpot, st)
	m := byItem(items)
	if m[4].Status != GatePass {
		t.Fatalf("item 4 = %+v", m[4])
	}
	if m[6].Status != GatePass {
		t.Fatalf("item 6 = %+v", m[6])
	}
	for _, n := range []int{1, 2, 3, 5, 7, 8} {
		if m[n].Status != GateFail {
			t.Fatalf("item %d passed without evidence: %+v", n, m[n])
		}
	}
	if !strings.Contains(m[7].Reason, "slippage sub-check: true") {
		t.Fatalf("item 7 reason = %q", m[7].Reason)
	}
	if Passed(items) != 2 {
		t.Fatalf("passed = %d, want 2", Passed(items))
	}
}

func itoa(i int) string { return decimal.NewFromInt(int64(i)).String() }
