package exchange

import "github.com/shopspring/decimal"

// Money-path divisions (budget/price walks, fee input scaling, bps ratios)
// must not lose precision at shopspring's default of 16 digits. Every
// package that touches money imports this package, so the raise happens
// exactly once, before any division runs. A test pins the invariant.
func init() {
	if decimal.DivisionPrecision < 28 {
		decimal.DivisionPrecision = 28
	}
}
