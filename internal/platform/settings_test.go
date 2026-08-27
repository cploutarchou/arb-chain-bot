package platform

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

func validSettings() Settings {
	return Settings{
		Venues: map[string]VenueSettings{
			"binance": {
				Enabled:        true,
				PaperEnabled:   true,
				Symbols:        []string{"BTCUSDT", "ETHUSDT", "ETHBTC"},
				StartingAssets: []string{"USDT"},
				Fees: FeeSettings{
					MakerBps: decimal.NewFromInt(10),
					TakerBps: decimal.NewFromInt(10),
				},
			},
		},
		Paper:    PaperSettings{Balances: map[string]string{"USDT": "10000"}},
		Telegram: TelegramSettings{Allowlist: []int64{111, 222}},
	}
}

func TestValidSettingsValidate(t *testing.T) {
	if err := validSettings().Validate(); err != nil {
		t.Fatalf("valid settings must validate: %v", err)
	}
}

func TestValidateTable(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Settings)
	}{
		{"no venues", func(s *Settings) { s.Venues = nil }},
		{"unknown venue id", func(s *Settings) {
			v := s.Venues["binance"]
			delete(s.Venues, "binance")
			s.Venues["okx"] = v
		}},
		{"no venue enabled", func(s *Settings) {
			v := s.Venues["binance"]
			v.Enabled = false
			s.Venues["binance"] = v
			s.Paper.Balances = map[string]string{}
		}},
		{"too few symbols", func(s *Settings) {
			v := s.Venues["binance"]
			v.Symbols = []string{"BTCUSDT"}
			s.Venues["binance"] = v
		}},
		{"lower-case symbol", func(s *Settings) {
			v := s.Venues["binance"]
			v.Symbols = []string{"btcusdt", "ETHUSDT", "ETHBTC"}
			s.Venues["binance"] = v
		}},
		{"duplicate symbol", func(s *Settings) {
			v := s.Venues["binance"]
			v.Symbols = []string{"BTCUSDT", "BTCUSDT", "ETHBTC"}
			s.Venues["binance"] = v
		}},
		{"too many starting assets", func(s *Settings) {
			v := s.Venues["binance"]
			v.StartingAssets = []string{"A", "B", "C", "D", "E", "F", "G", "H", "I"}
			s.Venues["binance"] = v
		}},
		{"duplicate starting asset", func(s *Settings) {
			v := s.Venues["binance"]
			v.StartingAssets = []string{"USDT", "USDT"}
			s.Venues["binance"] = v
		}},
		{"maker bps zero", func(s *Settings) {
			v := s.Venues["binance"]
			v.Fees.MakerBps = decimal.Zero
			s.Venues["binance"] = v
		}},
		{"taker bps over 100", func(s *Settings) {
			v := s.Venues["binance"]
			v.Fees.TakerBps = decimal.NewFromInt(101)
			s.Venues["binance"] = v
		}},
		{"override symbol not selected", func(s *Settings) {
			v := s.Venues["binance"]
			v.Fees.Overrides = map[string]FeeOverride{"DOGEUSDT": {MakerBps: decimal.Zero, TakerBps: decimal.Zero}}
			s.Venues["binance"] = v
		}},
		{"override negative", func(s *Settings) {
			v := s.Venues["binance"]
			v.Fees.Overrides = map[string]FeeOverride{"BTCUSDT": {MakerBps: decimal.NewFromInt(-1), TakerBps: decimal.Zero}}
			s.Venues["binance"] = v
		}},
		{"balance key set mismatch (missing)", func(s *Settings) {
			s.Paper.Balances = map[string]string{}
		}},
		{"balance key set mismatch (extra)", func(s *Settings) {
			s.Paper.Balances = map[string]string{"USDT": "100", "BTC": "1"}
		}},
		{"balance not decimal", func(s *Settings) {
			s.Paper.Balances = map[string]string{"USDT": "not-a-number"}
		}},
		{"balance zero", func(s *Settings) {
			s.Paper.Balances = map[string]string{"USDT": "0"}
		}},
		{"balance too large", func(s *Settings) {
			s.Paper.Balances = map[string]string{"USDT": "1e10"}
		}},
		{"allowlist too many", func(s *Settings) {
			ids := make([]int64, 33)
			for i := range ids {
				ids[i] = int64(i + 1)
			}
			s.Telegram.Allowlist = ids
		}},
		{"allowlist non-positive", func(s *Settings) {
			s.Telegram.Allowlist = []int64{0}
		}},
		{"allowlist duplicate", func(s *Settings) {
			s.Telegram.Allowlist = []int64{5, 5}
		}},
	}
	for _, tc := range cases {
		s := validSettings()
		tc.mut(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: expected validation error", tc.name)
		}
	}
}

// TestTokenDiscountRejected is the P1-2 regression test: token_discount
// must be refused for EVERY venue — even binance, whose compiled-in
// discount profile applies to API-executed trades — because no
// pay-asset debit ledger exists anywhere in the paper engine or
// portfolio (fees.go's Taker()/NetOutput comments explain why applying
// the discounted rate alone would make paper P&L optimistic). Bybit
// (API-ineligible even in principle) must be refused the same way.
func TestTokenDiscountRejected(t *testing.T) {
	s := validSettings()
	v := s.Venues["binance"]
	v.Fees.TokenDiscount = true
	s.Venues["binance"] = v
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "pay-asset ledger") {
		t.Fatalf("expected a pay-asset-ledger validation error, got %v", err)
	}

	// No Bybit connector is compiled in yet (T-050), so this path cannot
	// be reached through Settings.Validate's CompiledVenues gate; exercise
	// FeeSettings.validate directly instead of skipping the case.
	f := FeeSettings{
		MakerBps: decimal.NewFromInt(10), TakerBps: decimal.NewFromInt(10),
		TokenDiscount: true,
	}
	if err := f.validate("bybit", nil); err == nil {
		t.Fatal("expected token_discount to be refused for bybit too")
	}
}

// TestCompiledVenueTable exercises requirement (a): the console reads
// the discount table from the API instead of hardcoding it, and every
// entry must honestly report Modeled:false per P1-2.
func TestCompiledVenueTable(t *testing.T) {
	table := CompiledVenueTable()
	if len(table) == 0 {
		t.Fatal("expected at least one compiled venue")
	}
	var sawBinance bool
	for _, v := range table {
		if v.ID == "binance" {
			sawBinance = true
			if v.Discount == nil {
				t.Fatal("binance must report its compiled-in discount profile")
			}
			if v.Discount.Modeled {
				t.Fatal("Modeled must be false until a pay-asset ledger exists (P1-2)")
			}
			if v.Discount.PayAsset != "BNB" || !v.Discount.AppliesToAPI {
				t.Fatalf("unexpected discount profile: %+v", v.Discount)
			}
		}
	}
	if !sawBinance {
		t.Fatal("expected binance in the compiled venue table")
	}
	// Sorted by id: the console never has to re-sort.
	for i := 1; i < len(table); i++ {
		if table[i-1].ID >= table[i].ID {
			t.Fatalf("table not sorted: %v", table)
		}
	}
}

func TestValidatePaperMode(t *testing.T) {
	s := validSettings()
	v := s.Venues["binance"]
	v.PaperEnabled = false
	s.Venues["binance"] = v
	if err := s.ValidatePaperMode(config.ModePaper); err == nil {
		t.Fatal("expected error: enabled venue without paper_enabled in PAPER mode")
	}
	if err := s.ValidatePaperMode(config.ModeMarketData); err != nil {
		t.Fatalf("non-PAPER mode must not require paper_enabled: %v", err)
	}
}

func TestCloneMutationIndependence(t *testing.T) {
	s := validSettings()
	c := s.Clone()

	v := c.Venues["binance"]
	v.Symbols[0] = "MUTATED"
	c.Venues["binance"] = v
	c.Paper.Balances["USDT"] = "1"
	c.Telegram.Allowlist[0] = 999

	if s.Venues["binance"].Symbols[0] == "MUTATED" {
		t.Fatal("Clone: mutating the clone's symbols mutated the original")
	}
	if s.Paper.Balances["USDT"] == "1" {
		t.Fatal("Clone: mutating the clone's balances mutated the original")
	}
	if s.Telegram.Allowlist[0] == 999 {
		t.Fatal("Clone: mutating the clone's allowlist mutated the original")
	}
}

func TestSeedReproducesBootstrapDefaults(t *testing.T) {
	cfg := config.Bootstrap{
		Mode:              config.ModePaper,
		Symbols:           []string{"btcusdt", "ethusdt", "ethbtc"},
		StartingAssets:    []string{"usdt"},
		PaperBalance:      "10000",
		TelegramAllowlist: []int64{42},
	}
	s := Seed(cfg)
	if err := s.Validate(); err != nil {
		t.Fatalf("seed must validate: %v", err)
	}
	v, ok := s.Venues["binance"]
	if !ok {
		t.Fatal("seed must configure the binance venue")
	}
	if len(v.Symbols) != 3 || v.Symbols[0] != "BTCUSDT" {
		t.Fatalf("seed symbols = %v", v.Symbols)
	}
	if s.Paper.Balances["USDT"] != "10000" {
		t.Fatalf("seed paper balance = %v", s.Paper.Balances)
	}
	if len(s.Telegram.Allowlist) != 1 || s.Telegram.Allowlist[0] != 42 {
		t.Fatalf("seed allowlist = %v", s.Telegram.Allowlist)
	}
}

func TestRestartScoped(t *testing.T) {
	allowlistOnly := map[string]strategy.Change{
		"telegram.allowlist": {Old: "[1]", New: "[1,2]"},
	}
	if validSettings().RestartScoped(allowlistOnly) {
		t.Fatal("allowlist-only diff must not be restart-scoped")
	}
	mixed := map[string]strategy.Change{
		"telegram.allowlist":            {Old: "[1]", New: "[1,2]"},
		"venues.binance.fees.taker_bps": {Old: "10", New: "7.5"},
	}
	if !validSettings().RestartScoped(mixed) {
		t.Fatal("a diff touching fees must be restart-scoped")
	}
}

func TestFieldTimingAllowlistComputed(t *testing.T) {
	s := validSettings()
	if got := FieldTiming(s, true)["telegram.allowlist"]; got != "hot" {
		t.Fatalf("bot running: telegram.allowlist timing = %q, want hot", got)
	}
	if got := FieldTiming(s, false)["telegram.allowlist"]; got != "restart" {
		t.Fatalf("bot absent: telegram.allowlist timing = %q, want restart", got)
	}
	if got := FieldTiming(s, true)["venues.binance.symbols"]; got != "restart" {
		t.Fatalf("venues.binance.symbols timing = %q, want restart", got)
	}
}
