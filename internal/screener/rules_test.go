package screener

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

func validSpreadRule() Rule {
	bps := decimal.NewFromInt(50)
	return Rule{
		ID: "r1", Name: "BTC cross-venue", Enabled: true, Kind: RuleKindSpread,
		MinSpreadBps: &bps, MinLiquidityQuote: d("1000"), MinLifetimeS: 10,
		BuyVenues: []Venue{VenueBinance}, SellVenues: []Venue{VenueOKX},
		Quotes: []string{"USDT"}, CooldownS: 60,
	}
}

func TestRuleValidateSpreadRequiresMinSpreadBps(t *testing.T) {
	r := validSpreadRule()
	r.MinSpreadBps = nil
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestRuleValidateCarryRequiresMinCarryAPR(t *testing.T) {
	r := validSpreadRule()
	r.Kind = RuleKindCarry
	r.MinSpreadBps = nil
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid (missing min_carry_apr)", err)
	}
	apr := d("0.1")
	r.MinCarryAPR = &apr
	if err := r.Validate(); err != nil {
		t.Fatalf("valid carry rule rejected: %v", err)
	}
}

func TestRuleValidateUnknownKind(t *testing.T) {
	r := validSpreadRule()
	r.Kind = RuleKind("nonsense")
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestRuleValidateUnknownVenue(t *testing.T) {
	r := validSpreadRule()
	r.BuyVenues = []Venue{"dydx"}
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestRuleValidateAutoPaperRequiresSize(t *testing.T) {
	r := validSpreadRule()
	r.AutoPaper = true
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid (auto_paper without paper_size_quote)", err)
	}
	r.PaperSizeQuote = d("100")
	if err := r.Validate(); err != nil {
		t.Fatalf("valid auto_paper rule rejected: %v", err)
	}
}

func TestRuleValidateEmptyName(t *testing.T) {
	r := validSpreadRule()
	r.Name = ""
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}
