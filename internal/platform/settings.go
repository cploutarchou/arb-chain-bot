// Package platform is the second versioned dynamic-configuration
// document (T-057, docs/design/platform-settings-and-restart.md and
// docs/architecture.md §13): venue enablement, symbols, starting assets,
// per-venue fees, paper starting balances, and the Telegram allowlist.
// It is deliberately separate from internal/strategy — see the design
// doc §1.1 for the RBAC-fails-open, provenance-churn and validation-shape
// reasons the split is paid for knowingly.
package platform

import (
	"fmt"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// CompiledVenues is the set of venue ids this build actually has a
// connector for. Venues is a map so a second connector (T-050) drops in
// without a schema change, but each cycle stays on one venue (design D-
// "no cross-exchange anything").
var CompiledVenues = map[string]bool{
	"binance": true,
}

// Settings is the complete platform-scope document. bps values are
// decimal (never float64); paper balances are decimal strings end to
// end so no float ever touches money.
type Settings struct {
	Venues   map[string]VenueSettings `json:"venues"` // key: exchange id, e.g. "binance"
	Paper    PaperSettings            `json:"paper"`
	Telegram TelegramSettings         `json:"telegram"`
}

// VenueSettings is one venue's configuration.
type VenueSettings struct {
	Enabled        bool        `json:"enabled"`
	PaperEnabled   bool        `json:"paper_enabled"`
	Symbols        []string    `json:"symbols"`         // sorted, upper-case
	StartingAssets []string    `json:"starting_assets"` // sorted, upper-case
	Fees           FeeSettings `json:"fees"`
}

// FeeSettings is one venue's fee configuration. TokenDiscount is a
// toggle only: the discount rate, pay asset and API eligibility come
// from the compiled-in constant table in internal/fees.
type FeeSettings struct {
	MakerBps      decimal.Decimal        `json:"maker_bps"`
	TakerBps      decimal.Decimal        `json:"taker_bps"`
	Overrides     map[string]FeeOverride `json:"overrides,omitempty"` // key: symbol
	TokenDiscount bool                   `json:"token_discount"`
}

// FeeOverride is a verified per-symbol fee rate (promo/zero-fee pairs).
type FeeOverride struct {
	MakerBps decimal.Decimal `json:"maker_bps"`
	TakerBps decimal.Decimal `json:"taker_bps"`
}

// PaperSettings holds the starting virtual balances.
type PaperSettings struct {
	Balances map[string]string `json:"balances"` // asset → decimal string
}

// TelegramSettings holds the operator allowlist. The bot token stays
// env-only and is never accepted or displayed here.
type TelegramSettings struct {
	Allowlist []int64 `json:"allowlist"`
}

// Clone returns a deep copy; maps/slices in the tree are the only
// reference types, so a caller can never mutate a stored version through
// the copy it was handed (mirrors strategy.Params.Clone, audit-P3).
func (s Settings) Clone() Settings {
	c := s
	if s.Venues != nil {
		c.Venues = make(map[string]VenueSettings, len(s.Venues))
		for id, v := range s.Venues {
			c.Venues[id] = v.clone()
		}
	}
	if s.Paper.Balances != nil {
		c.Paper.Balances = make(map[string]string, len(s.Paper.Balances))
		for k, v := range s.Paper.Balances {
			c.Paper.Balances[k] = v
		}
	}
	c.Telegram.Allowlist = append([]int64(nil), s.Telegram.Allowlist...)
	return c
}

func (v VenueSettings) clone() VenueSettings {
	c := v
	c.Symbols = append([]string(nil), v.Symbols...)
	c.StartingAssets = append([]string(nil), v.StartingAssets...)
	if v.Fees.Overrides != nil {
		c.Fees.Overrides = make(map[string]FeeOverride, len(v.Fees.Overrides))
		for k, o := range v.Fees.Overrides {
			c.Fees.Overrides[k] = o
		}
	}
	return c
}

var (
	hundred     = decimal.NewFromInt(100)
	maxBalance  = decimal.RequireFromString("1e9")
	maxAllowlst = 32
)

// Validate rejects a structurally invalid document. It is pure, bounded
// and does no I/O: symbol existence against the venue's exchangeInfo and
// the topology dry-run are impure and live in ValidateAgainstCatalog
// instead (design §1.3/§1.5).
func (s Settings) Validate() error {
	if len(s.Venues) == 0 {
		return fmt.Errorf("platform: at least one venue must be configured")
	}
	anyEnabled := false
	allStarting := map[string]bool{}
	for id, v := range s.Venues {
		if id == "" || id != strings.ToLower(id) {
			return fmt.Errorf("platform: venue id %q must be lower-case and non-empty", id)
		}
		if !CompiledVenues[id] {
			return fmt.Errorf("platform: venue %q is not a compiled-in connector", id)
		}
		if err := v.validate(id); err != nil {
			return err
		}
		if v.Enabled {
			anyEnabled = true
			for _, a := range v.StartingAssets {
				allStarting[a] = true
			}
		}
	}
	if !anyEnabled {
		return fmt.Errorf("platform: at least one venue must be enabled")
	}
	if err := s.Paper.validate(allStarting); err != nil {
		return err
	}
	if err := s.Telegram.validate(); err != nil {
		return err
	}
	return nil
}

func (v VenueSettings) validate(venueID string) error {
	if n := len(v.Symbols); n < 3 || n > 60 {
		return fmt.Errorf("platform: venues.%s.symbols must have 3..60 entries, got %d", venueID, n)
	}
	seen := map[string]bool{}
	for _, sym := range v.Symbols {
		if sym == "" || sym != strings.ToUpper(sym) {
			return fmt.Errorf("platform: venues.%s.symbols entry %q must be upper-case", venueID, sym)
		}
		if seen[sym] {
			return fmt.Errorf("platform: venues.%s.symbols has duplicate %q", venueID, sym)
		}
		seen[sym] = true
	}
	if n := len(v.StartingAssets); n < 1 || n > 8 {
		return fmt.Errorf("platform: venues.%s.starting_assets must have 1..8 entries, got %d", venueID, n)
	}
	seenA := map[string]bool{}
	for _, a := range v.StartingAssets {
		if a == "" || a != strings.ToUpper(a) {
			return fmt.Errorf("platform: venues.%s.starting_assets entry %q must be upper-case", venueID, a)
		}
		if seenA[a] {
			return fmt.Errorf("platform: venues.%s.starting_assets has duplicate %q", venueID, a)
		}
		seenA[a] = true
	}
	if err := v.Fees.validate(venueID, v.Symbols); err != nil {
		return err
	}
	return nil
}

func (f FeeSettings) validate(venueID string, symbols []string) error {
	if !f.MakerBps.IsPositive() || f.MakerBps.GreaterThan(hundred) {
		return fmt.Errorf("platform: venues.%s.fees.maker_bps out of (0,100]", venueID)
	}
	if !f.TakerBps.IsPositive() || f.TakerBps.GreaterThan(hundred) {
		return fmt.Errorf("platform: venues.%s.fees.taker_bps out of (0,100]", venueID)
	}
	symbolSet := make(map[string]bool, len(symbols))
	for _, s := range symbols {
		symbolSet[s] = true
	}
	for sym, o := range f.Overrides {
		if !symbolSet[sym] {
			return fmt.Errorf("platform: venues.%s.fees.overrides has %q which is not in symbols", venueID, sym)
		}
		if o.MakerBps.IsNegative() || o.MakerBps.GreaterThan(hundred) {
			return fmt.Errorf("platform: venues.%s.fees.overrides.%s.maker_bps out of [0,100]", venueID, sym)
		}
		if o.TakerBps.IsNegative() || o.TakerBps.GreaterThan(hundred) {
			return fmt.Errorf("platform: venues.%s.fees.overrides.%s.taker_bps out of [0,100]", venueID, sym)
		}
	}
	if f.TokenDiscount {
		// P1-2: token_discount used to apply a 25% fee cut (fees.go's
		// Schedule.Taker, gated on this flag) with no corresponding debit
		// anywhere — the pay asset (e.g. BNB) is never reserved, spent, or
		// tracked by the paper engine or portfolio, so a document with
		// this set to true made paper P&L optimistic by exactly the
		// discount amount on every fee-bearing leg. Refuse it outright
		// until a real pay-asset ledger exists (do not implement the
		// ledger here — that touches money math). This intentionally also
		// rejects a document that only carries the flag because it was
		// loaded from a version persisted before this fix landed:
		// Service.Load runs the SAME Validate on the active row at boot,
		// so an operator who had this enabled sees a clear, actionable
		// boot failure naming the field, rather than the fee schedule
		// silently reverting to a state nobody chose (the D5/P2-4
		// philosophy this codebase already applies to store failures).
		return fmt.Errorf("platform: venues.%s.fees.token_discount is not supported: "+
			"token-paid discounts are not modeled (no pay-asset ledger yet); leave it false", venueID)
	}
	return nil
}

// CompiledVenueDiscount is the read-only view of a venue's compiled-in
// token-discount profile (docs/research/fees.md). GET
// /api/v1/platform/venues serves this so the console can render the
// rate/pay-asset/eligibility (and why the toggle is refused, see P1-2)
// without hardcoding the constants client-side.
type CompiledVenueDiscount struct {
	PayAsset     string `json:"pay_asset"`
	Rate         string `json:"rate"` // fractional, e.g. "0.25" = 25% off
	AppliesToAPI bool   `json:"applies_to_api"`
	// Modeled is always false today (P1-2): FeeSettings.validate refuses
	// token_discount:true for every venue until a pay-asset debit ledger
	// exists. Exposed explicitly (not left for the client to infer from
	// AppliesToAPI) so the console can disable the control instead of
	// offering a toggle that always fails apply.
	Modeled bool   `json:"modeled"`
	Reason  string `json:"reason,omitempty"`
}

// CompiledVenue is one compiled-in connector's static profile.
type CompiledVenue struct {
	ID       string                 `json:"id"`
	Discount *CompiledVenueDiscount `json:"discount,omitempty"`
}

// CompiledVenueTable returns every venue this build has a connector for
// (CompiledVenues), sorted by id, with its compiled-in fee-discount
// profile when it has one — the single source the console reads instead
// of hardcoding venue ids or discount constants (mirrors FieldTiming's
// "computed, never hardcoded on the frontend").
func CompiledVenueTable() []CompiledVenue {
	ids := make([]string, 0, len(CompiledVenues))
	for id := range CompiledVenues {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]CompiledVenue, 0, len(ids))
	for _, id := range ids {
		v := CompiledVenue{ID: id}
		if d, ok := fees.VenueDiscount(exchange.ExchangeID(id)); ok {
			v.Discount = &CompiledVenueDiscount{
				PayAsset:     string(d.PayAsset),
				Rate:         d.Rate.String(),
				AppliesToAPI: d.AppliesToAPI,
				Modeled:      false,
				Reason:       "token-paid discounts are not modeled: no pay-asset ledger",
			}
		}
		out = append(out, v)
	}
	return out
}

func (p PaperSettings) validate(wantAssets map[string]bool) error {
	if len(p.Balances) != len(wantAssets) {
		return fmt.Errorf("platform: paper.balances key set must equal the union of enabled venues' starting assets")
	}
	for asset := range wantAssets {
		raw, ok := p.Balances[asset]
		if !ok {
			return fmt.Errorf("platform: paper.balances is missing %q", asset)
		}
		v, err := decimal.NewFromString(raw)
		if err != nil {
			return fmt.Errorf("platform: paper.balances.%s is not a decimal: %w", asset, err)
		}
		if !v.IsPositive() {
			return fmt.Errorf("platform: paper.balances.%s must be > 0", asset)
		}
		if v.GreaterThan(maxBalance) {
			return fmt.Errorf("platform: paper.balances.%s must be <= 1e9", asset)
		}
	}
	for asset := range p.Balances {
		if !wantAssets[asset] {
			return fmt.Errorf("platform: paper.balances has %q which is not a starting asset of any enabled venue", asset)
		}
	}
	return nil
}

func (t TelegramSettings) validate() error {
	if len(t.Allowlist) > maxAllowlst {
		return fmt.Errorf("platform: telegram.allowlist must have at most %d entries", maxAllowlst)
	}
	seen := map[int64]bool{}
	for _, id := range t.Allowlist {
		if id <= 0 {
			return fmt.Errorf("platform: telegram.allowlist entries must be positive, got %d", id)
		}
		if seen[id] {
			return fmt.Errorf("platform: telegram.allowlist has duplicate %d", id)
		}
		seen[id] = true
	}
	return nil
}

// ValidatePaperMode additionally requires that in PAPER mode every
// enabled venue has paper_enabled set. It is separate from Validate
// because it needs the process mode, which is not part of the document
// (design §1.3 row "venues.{ex}.paper_enabled").
func (s Settings) ValidatePaperMode(mode config.Mode) error {
	if mode != config.ModePaper {
		return nil
	}
	for id, v := range s.Venues {
		if v.Enabled && !v.PaperEnabled {
			return fmt.Errorf("platform: venues.%s is enabled but paper_enabled is false, and the process is in PAPER mode", id)
		}
	}
	return nil
}

// Seed builds the first-boot document from the bootstrap environment
// (D5): after version 1 exists, ARB_SYMBOLS/ARB_STARTING_ASSETS/
// ARB_PAPER_BALANCE/ARB_TELEGRAM_ALLOWLIST are ignored by the service.
func Seed(b config.Bootstrap) Settings {
	symbols := sortedUpper(b.Symbols)
	starting := sortedUpper(b.StartingAssets)
	balances := map[string]string{}
	for _, a := range starting {
		balances[a] = b.PaperBalance
	}
	venueID := "binance"
	return Settings{
		Venues: map[string]VenueSettings{
			venueID: {
				Enabled:        true,
				PaperEnabled:   b.Mode == config.ModePaper,
				Symbols:        symbols,
				StartingAssets: starting,
				Fees: FeeSettings{
					MakerBps: decimal.NewFromInt(10),
					TakerBps: decimal.NewFromInt(10),
				},
			},
		},
		Paper: PaperSettings{Balances: balances},
		Telegram: TelegramSettings{
			Allowlist: append([]int64(nil), b.TelegramAllowlist...),
		},
	}
}

func sortedUpper(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, v := range in {
		u := strings.ToUpper(strings.TrimSpace(v))
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}

// restartScopedPrefixes are the dotted-path prefixes that are hot, i.e.
// everything else in the document is restart-scoped (design §1.3: every
// field is restart-scoped except the Telegram allowlist).
var hotPrefixes = []string{"telegram.allowlist"}

// RestartScoped reports whether diff contains at least one change whose
// path is NOT one of the hot-only prefixes.
func (s Settings) RestartScoped(diff map[string]strategy.Change) bool {
	for path := range diff {
		hot := false
		for _, p := range hotPrefixes {
			if path == p || strings.HasPrefix(path, p+".") {
				hot = true
				break
			}
		}
		if !hot {
			return true
		}
	}
	return false
}

// FieldTiming returns the field → "hot"/"restart" map the API serves
// (design §1.3/§3): computed, never hardcoded on the frontend.
// botRunning is true when the Telegram bot process is actually
// constructed; when false, adding the FIRST allowlist entry needs a
// restart (components.go:170 - an empty boot allowlist never builds the
// bot at all).
func FieldTiming(s Settings, botRunning bool) map[string]string {
	out := map[string]string{}
	for id, v := range s.Venues {
		prefix := "venues." + id + "."
		out[prefix+"enabled"] = "restart"
		out[prefix+"paper_enabled"] = "restart"
		out[prefix+"symbols"] = "restart"
		out[prefix+"starting_assets"] = "restart"
		out[prefix+"fees.maker_bps"] = "restart"
		out[prefix+"fees.taker_bps"] = "restart"
		out[prefix+"fees.token_discount"] = "restart"
		for sym := range v.Fees.Overrides {
			out[prefix+"fees.overrides."+sym] = "restart"
		}
	}
	for asset := range s.Paper.Balances {
		out["paper.balances."+asset] = "restart"
	}
	if botRunning {
		out["telegram.allowlist"] = "hot"
	} else {
		out["telegram.allowlist"] = "restart"
	}
	return out
}
