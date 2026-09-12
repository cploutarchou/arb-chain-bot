package backtest

import (
	"strings"
	"testing"
)

func TestFlagSeverity(t *testing.T) {
	cases := []struct {
		flag string
		want string
	}{
		{"NO BASELINE SCENARIO — run the grid with a baseline before drawing any conclusion.", "bad"},
		{"NO CYCLES EXECUTED in the baseline — no qualified opportunities.", "bad"},
		{"BASELINE UNPROFITABLE: net PnL -5 USDT over 3 cycles.", "bad"},
		{"PROFITABLE ONLY UNDER PERFECT CONDITIONS: every stress scenario turns the edge negative.", "bad"},
		{"EDGE FRAGILE under: fees+10bps (net PnL <= 0 there).", "warn"},
		{"BASELINE ONLY — §80 requires the stress grid.", "warn"},
		{"Edge survives the full stress grid in this recording. This is ONE sample window.", "ok"},
		{"", "ok"},
	}
	for _, c := range cases {
		if got := FlagSeverity(c.flag); got != c.want {
			t.Errorf("FlagSeverity(%q) = %q, want %q", c.flag, got, c.want)
		}
	}
}

// Acceptance: every line the live Flags() method can actually emit maps
// to a severity FlagSeverity recognizes (no line falls through to "ok"
// by accident because a prefix drifted from report.go's Flags()).
func TestFlagSeverityCoversLiveFlags(t *testing.T) {
	c := &Campaign{Results: nil}
	for _, f := range c.Flags("USDT") {
		if got := FlagSeverity(f); got != "bad" {
			t.Errorf("no-baseline flag %q classified %q, want bad", f, got)
		}
	}
}

// T-062: a no-cycles baseline says WHY — the top rejection reasons ride
// the verdict line, the histogram lands as a report table, and the
// extended line keeps its "bad" severity (prefix intact).
func TestNoCyclesVerdictCarriesRejectionReasons(t *testing.T) {
	c := &Campaign{Results: []Result{{
		Scenario:         Scenario{Name: "baseline"},
		RejectionReasons: map[string]int64{"RISK_MIN_EDGE": 12, ReasonSkippedBook: 5, "RISK_BREAKER_OPEN": 2},
	}}}
	flags := c.Flags("USDT")
	if len(flags) != 1 {
		t.Fatalf("flags = %v", flags)
	}
	const want = "top rejection reasons: RISK_MIN_EDGE 12, SKIPPED_UNHEALTHY_BOOK 5"
	if !strings.Contains(flags[0], want) {
		t.Errorf("flag %q missing %q", flags[0], want)
	}
	if got := FlagSeverity(flags[0]); got != "bad" {
		t.Errorf("severity = %q, want bad", got)
	}
	md := c.Markdown("USDT")
	for _, want := range []string{
		"## Rejection reasons",
		"| baseline | RISK_MIN_EDGE | 12 |",
		"| baseline | SKIPPED_UNHEALTHY_BOOK | 5 |",
		"| baseline | RISK_BREAKER_OPEN | 2 |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}

// T-062: results persisted before the histogram existed (nil map) render
// the plain verdict and no rejection section.
func TestNoCyclesVerdictWithoutReasons(t *testing.T) {
	c := &Campaign{Results: []Result{{Scenario: Scenario{Name: "baseline"}}}}
	flag := c.Flags("USDT")[0]
	if strings.Contains(flag, "top rejection reasons") {
		t.Errorf("unexpected reasons in %q", flag)
	}
	if md := c.Markdown("USDT"); strings.Contains(md, "## Rejection reasons") {
		t.Errorf("rejection section rendered from an empty histogram:\n%s", md)
	}
}

// T-062: the histogram merges across seeds inside one scenario.
func TestRejectionReasonsMergeAcrossSeeds(t *testing.T) {
	c := &Campaign{Results: []Result{
		{Scenario: Scenario{Name: "baseline"}, Seed: 1, RejectionReasons: map[string]int64{"RISK_MIN_EDGE": 3}},
		{Scenario: Scenario{Name: "baseline"}, Seed: 2, RejectionReasons: map[string]int64{"RISK_MIN_EDGE": 4, ReasonNoViableSize: 1}},
	}}
	md := c.Markdown("USDT")
	for _, want := range []string{"| baseline | RISK_MIN_EDGE | 7 |", "| baseline | NO_VIABLE_SIZE | 1 |"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}
