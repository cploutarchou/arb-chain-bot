package alerts

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Footer is appended to every alert (docs/compliance/review-2026-08-27.md
// #7: outputs are measurements of filters the user configured, never
// advice). Fixed text; no caller may change it.
const Footer = "Measurement of public quotes net of configured taker fees; " +
	"model-based, hypothetical, not a recommendation to trade. " +
	"Nothing here is guaranteed; simulated or past figures are not indicative of future results."

func bps(d decimal.Decimal) string { return d.StringFixed(2) + " bps" }

func ageS(ms int64) string {
	return decimal.NewFromInt(ms).Div(decimal.NewFromInt(1000)).StringFixed(1) + "s"
}

// OpenText renders the alert-opened message. No buy/sell verbs: legs
// are described as "ask@venue" and "bid@venue", figures as measured.
func OpenText(s Signal, lifetimeS int64) (title, body string) {
	r := s.Rule
	pair := s.Lane.Base + "/" + s.Lane.Quote
	var b strings.Builder
	switch s.Strategy {
	case screener.StrategyCrossVenueSpot:
		title = fmt.Sprintf("Screener rule %q: %s spread %s", r.Name, pair, bps(s.ExecBps))
		fmt.Fprintf(&b, "%s ask@%s %s / bid@%s %s\n", pair, s.Lane.VenueA, s.SpotA.Ask.String(), s.Lane.VenueB, s.SpotB.Bid.String())
		fmt.Fprintf(&b, "gross %s, net of fees %s, after slip+buffer %s\n", bps(s.GrossBps), bps(s.NetBps), bps(s.ExecBps))
		fmt.Fprintf(&b, "top-of-book liquidity %s %s (ask %s %s, bid %s %s)\n",
			s.LiquidityQuote.StringFixed(2), s.Lane.Quote, s.SpotA.AskQty.String(), s.Lane.Base, s.SpotB.BidQty.String(), s.Lane.Base)
		fmt.Fprintf(&b, "fees %s / %s taker; data age %s / %s; lifetime %ds\n", bps(s.FeeA), bps(s.FeeB), ageS(s.AgeAMs), ageS(s.AgeBMs), lifetimeS)
	case screener.StrategyFundingHarvest:
		title = fmt.Sprintf("Screener rule %q: %s funding %s/interval on %s", r.Name, pair, bps(s.FHatBps), s.Lane.VenueA)
		fmt.Fprintf(&b, "%s %s: predicted funding %s, settled mean (last %d) %s, basis %s\n",
			s.Lane.VenueA, pair, bps(s.PredictedBps), harvestLookback, bps(s.MeanSettled.Mul(decTenK)), bps(s.BasisEntryBps))
		fmt.Fprintf(&b, "round-trip costs %s + slip/buffer; breakeven %d intervals of %dh\n", bps(s.FeesRTBps), s.BreakevenN, s.Perp.IntervalH)
		fmt.Fprintf(&b, "liquidity %s %s; data age spot %s / perp %s; lifetime %ds\n", s.LiquidityQuote.StringFixed(2), s.Lane.Quote, ageS(s.AgeAMs), ageS(s.AgeBMs), lifetimeS)
	default:
		title = fmt.Sprintf("Screener rule %q: %s carry edge %s on %s", r.Name, pair, bps(s.EdgeBps), s.Lane.VenueA)
		fmt.Fprintf(&b, "%s %s: perp bid %s vs spot ask %s → basis %s\n", s.Lane.VenueA, pair, s.Perp.Bid.String(), s.SpotA.Ask.String(), bps(s.BasisEntryBps))
		fmt.Fprintf(&b, "predicted funding %s/interval (%dh), expected over hold %s, fees round-trip %s, edge %s\n",
			bps(s.PredictedBps), s.Perp.IntervalH, bps(s.FundingExpBps), bps(s.FeesRTBps), bps(s.EdgeBps))
		fmt.Fprintf(&b, "liquidity %s %s; data age spot %s / perp %s; lifetime %ds\n", s.LiquidityQuote.StringFixed(2), s.Lane.Quote, ageS(s.AgeAMs), ageS(s.AgeBMs), lifetimeS)
	}
	b.WriteString("\n")
	b.WriteString(Footer)
	return title, b.String()
}

// CloseText renders the alert-closed message (lifetime, peak and the
// reason the event closed — a market reason, "lane_gone", or
// HOLD_TIMEOUT when the quotes stayed stale — so a reader can tell an
// ended spread from lost data).
func CloseText(s Signal, lifetimeS int64, peak decimal.Decimal, reason string) (title, body string) {
	pair := s.Lane.Base + "/" + s.Lane.Quote
	title = fmt.Sprintf("Screener rule %q: %s signal ended after %ds", s.Rule.Name, pair, lifetimeS)
	body = fmt.Sprintf("%s %s → %s: peak %s, now %s; data age %s / %s; reason %s\n\n%s",
		pair, s.Lane.VenueA, s.Lane.VenueB, bps(peak), bps(s.Score()), ageS(s.AgeAMs), ageS(s.AgeBMs), reason, Footer)
	return title, body
}
