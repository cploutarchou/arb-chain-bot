package venue

import (
	"bytes"

	"github.com/shopspring/decimal"
)

// num is a decimal.Decimal that also accepts "" and null (venues emit an
// empty string for a side with no order, e.g. Gate's batch spot ticker
// lowest_ask/highest_bid) as zero. Bare JSON numbers and quoted numbers
// are parsed from their literal text — never via float64.
type num struct{ decimal.Decimal }

func (n *num) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte(`""`)) || bytes.Equal(b, []byte("null")) {
		n.Decimal = decimal.Zero
		return nil
	}
	return n.Decimal.UnmarshalJSON(b)
}
