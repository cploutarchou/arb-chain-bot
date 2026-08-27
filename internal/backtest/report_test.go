package backtest

import "testing"

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
