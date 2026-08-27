package venue

import "github.com/shopspring/decimal"

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }
