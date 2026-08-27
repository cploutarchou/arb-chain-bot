// Package platform is the second versioned dynamic-configuration
// document (T-057, docs/design/platform-settings-and-restart.md and
// docs/architecture.md §13): venue enablement, symbols, starting assets,
// per-venue fees, paper starting balances, and the Telegram allowlist —
// and, since T-059 (docs/design/settings-expansion.md), the operating
// mode, log level, allowed console origin, and the AI advisor profile.
// It is deliberately separate from internal/strategy — see the design
// doc §1.1 for the RBAC-fails-open, provenance-churn and validation-shape
// reasons the split is paid for knowingly.
package platform

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
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
	Platform PlatformSettings         `json:"platform"`
	Venues   map[string]VenueSettings `json:"venues"` // key: exchange id, e.g. "binance"
	Paper    PaperSettings            `json:"paper"`
	Telegram TelegramSettings         `json:"telegram"`
	AI       AISettings               `json:"ai"`
}

// PlatformSettings is the process-level section (T-059 §2/§4.3). Mode is
// restart-scoped (applied by app.Supervisor on the next engine restart);
// LogLevel and AllowedOrigin are hot.
type PlatformSettings struct {
	Mode          config.Mode `json:"mode"`           // restart: MARKET_DATA|RECORD|PAPER (SHADOW enumerated, not settable)
	LogLevel      string      `json:"log_level"`      // hot: debug|info|warn|error
	AllowedOrigin string      `json:"allowed_origin"` // hot: scheme://host[:port]
}

// AISettings is the advisor profile (T-059 §4.1). Every field is hot:
// ai.Service/ai.Scheduler always exist behind an ai.Switch, so an enable
// at runtime takes effect without a restart.
type AISettings struct {
	Enabled  bool       `json:"enabled"`
	Provider string     `json:"provider"` // anthropic | fake
	Model    string     `json:"model"`
	Schedule AISchedule `json:"schedule"`
	Budget   AIBudget   `json:"budget"`
}

// AISchedule holds the three standing-analysis cadences; 0 disables that
// cadence.
type AISchedule struct {
	HourlyMinutes int `json:"hourly_minutes"` // 0 or 15..1440
	DailyHours    int `json:"daily_hours"`    // 0 or 1..168
	WeeklyHours   int `json:"weekly_hours"`   // 0 or 24..720
}

// AIBudget caps provider usage. MaxAnalysesPerDay is a per-process UTC-day
// counter (not persisted: a process restart resets it).
type AIBudget struct {
	MaxAnalysesPerDay int `json:"max_analyses_per_day"` // 1..96
	MaxOutputTokens   int `json:"max_output_tokens"`    // 256..8192
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

// TelegramSettings holds the operator allowlist and the mute switch. The
// bot token is never accepted or displayed here (it lives in the secrets
// vault or the environment, T-060).
//
// Disabled is NEGATIVE on purpose (design D3): a document persisted
// before this field existed unmarshals with the zero value, which must
// mean "keep delivering". An Enabled bool would silently mute commands
// and pushes on a document nobody edited — on a channel T-057 classifies
// as a security control. The same name is used on the wire, in the diff
// and in field_timing; the API never inverts it into an "enabled" alias.
type TelegramSettings struct {
	Allowlist []int64 `json:"allowlist"`
	Disabled  bool    `json:"disabled"` // hot: mutes commands and pushes together
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
	if err := s.Platform.validate(); err != nil {
		return err
	}
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
			// D12: display (VenueTable) is a superset of enforcement
			// (CompiledVenues). A known-but-unbuilt venue is refused with
			// the blocking task named; an id nobody has heard of is a
			// plain validation error.
			if vp, ok := venueProfile(id); ok {
				return fmt.Errorf("%w: venue %q cannot be configured: %s", ErrConnectorUnavailable, id, vp.Reason)
			}
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
			// Folded in from the former ValidatePaperMode (T-059 §2.2):
			// the mode is now part of the document, so Validate is total
			// again and the failure lands at apply time, never at restart.
			if s.Platform.Mode == config.ModePaper && !v.PaperEnabled {
				return fmt.Errorf("platform: venues.%s is enabled but paper_enabled is false, and platform.mode is PAPER", id)
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
	if err := s.AI.validate(); err != nil {
		return err
	}
	return nil
}

func (p PlatformSettings) validate() error {
	if err := ValidateMode(p.Mode); err != nil {
		return err
	}
	if err := ValidateLogLevel(p.LogLevel); err != nil {
		return err
	}
	if err := ValidateOrigin(p.AllowedOrigin); err != nil {
		return err
	}
	return nil
}

var modelRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func (a AISettings) validate() error {
	if !AIProviderSettable(a.Provider) {
		return fmt.Errorf("platform: ai.provider %q must be one of %s", a.Provider, strings.Join(settableProviders(), ", "))
	}
	if !modelRE.MatchString(a.Model) {
		return fmt.Errorf("platform: ai.model must be 1..64 chars of [A-Za-z0-9._-], got %q", a.Model)
	}
	if v := a.Schedule.HourlyMinutes; v != 0 && (v < 15 || v > 1440) {
		return fmt.Errorf("platform: ai.schedule.hourly_minutes must be 0 or 15..1440, got %d", v)
	}
	if v := a.Schedule.DailyHours; v != 0 && (v < 1 || v > 168) {
		return fmt.Errorf("platform: ai.schedule.daily_hours must be 0 or 1..168, got %d", v)
	}
	if v := a.Schedule.WeeklyHours; v != 0 && (v < 24 || v > 720) {
		return fmt.Errorf("platform: ai.schedule.weekly_hours must be 0 or 24..720, got %d", v)
	}
	if v := a.Budget.MaxAnalysesPerDay; v < 1 || v > 96 {
		return fmt.Errorf("platform: ai.budget.max_analyses_per_day must be 1..96, got %d", v)
	}
	if v := a.Budget.MaxOutputTokens; v < 256 || v > 8192 {
		return fmt.Errorf("platform: ai.budget.max_output_tokens must be 256..8192, got %d", v)
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

// Seed builds the first-boot document from the bootstrap environment
// (D5): after version 1 exists, ARB_SYMBOLS/ARB_STARTING_ASSETS/
// ARB_PAPER_BALANCE/ARB_TELEGRAM_ALLOWLIST are ignored by the service.
//
// T-059 extends the rule to ARB_MODE, ARB_LOG_LEVEL, ARB_ALLOWED_ORIGIN,
// ARB_AI_PROVIDER/ARB_AI_MODEL and the presence of ANTHROPIC_API_KEY (as
// the ai.enabled seed). A non-settable ARB_MODE (REPLAY, BACKTEST,
// SHADOW — all legal env values) seeds MARKET_DATA: see SeedMode.
func Seed(b config.Bootstrap) Settings {
	logLevel, _ := seedLogLevel(b.LogLevel)
	origin, _ := seedOrigin(b.AllowedOrigin)
	symbols := sortedUpper(b.Symbols)
	starting := sortedUpper(b.StartingAssets)
	balances := map[string]string{}
	for _, a := range starting {
		balances[a] = b.PaperBalance
	}
	mode, _ := SeedMode(b.Mode)
	venueID := "binance"
	return Settings{
		Platform: PlatformSettings{
			Mode:          mode,
			LogLevel:      logLevel,
			AllowedOrigin: origin,
		},
		AI: seedAI(b),
		Venues: map[string]VenueSettings{
			venueID: {
				Enabled:        true,
				PaperEnabled:   mode == config.ModePaper,
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

// SeedMode maps the bootstrap ARB_MODE onto the settable enum: a
// settable mode passes through; every non-settable one (REPLAY/BACKTEST
// are batch runs, SHADOW is enumerated-but-unwired) becomes MARKET_DATA
// and the second return names the substitution for the boot log. Written
// once, in terms of Settable, so it cannot drift from ModeTable (§2.2).
func SeedMode(m config.Mode) (config.Mode, string) {
	if m == "" {
		return config.ModeMarketData, ""
	}
	if Settable(m) {
		return m, ""
	}
	return config.ModeMarketData, fmt.Sprintf("ARB_MODE=%s is not a settable operating mode; platform.mode seeded as %s", m, config.ModeMarketData)
}

// seedLogLevel and seedOrigin mirror SeedMode: an env value the
// document's validator would refuse is substituted, and the second
// return names the substitution for the boot log (never silently).
func seedLogLevel(v string) (string, string) {
	if v == "" {
		return "info", ""
	}
	if ValidateLogLevel(v) == nil {
		return strings.ToLower(v), ""
	}
	return "info", fmt.Sprintf("ARB_LOG_LEVEL=%q is not a valid log level; platform.log_level seeded as info", v)
}

func seedOrigin(v string) (string, string) {
	if v == "" {
		return "http://localhost:3000", ""
	}
	if ValidateOrigin(v) == nil {
		return v, ""
	}
	return "http://localhost:3000", fmt.Sprintf("ARB_ALLOWED_ORIGIN=%q is not a valid origin; platform.allowed_origin seeded as http://localhost:3000", v)
}

// SeedNotes lists every named substitution Seed(b) makes (mode, log
// level, origin) so Load can log each one at WARN. Empty when the env
// seeds pass through unchanged.
func SeedNotes(b config.Bootstrap) []string {
	var out []string
	if _, n := SeedMode(b.Mode); n != "" {
		out = append(out, n)
	}
	if _, n := seedLogLevel(b.LogLevel); n != "" {
		out = append(out, n)
	}
	if _, n := seedOrigin(b.AllowedOrigin); n != "" {
		out = append(out, n)
	}
	return out
}

// seedAI reproduces the pre-T-059 env behaviour exactly: the advisor was
// built when ARB_AI_PROVIDER=fake, or when ANTHROPIC_API_KEY was set
// (provider "" or "anthropic"); an unknown provider left it disabled.
func seedAI(b config.Bootstrap) AISettings {
	ai := AISettings{
		Provider: "anthropic",
		Model:    b.AIModel,
		Schedule: AISchedule{HourlyMinutes: 60, DailyHours: 24, WeeklyHours: 168},
		Budget:   AIBudget{MaxAnalysesPerDay: 48, MaxOutputTokens: 2048},
	}
	if !modelRE.MatchString(ai.Model) {
		ai.Model = "claude-sonnet-5"
	}
	switch b.AIProvider {
	case "fake":
		ai.Provider, ai.Enabled = "fake", true
	case "", "anthropic":
		ai.Enabled = b.AnthropicAPIKey != ""
	}
	return ai
}

// WithDefaults fills the sections/fields a pre-T-059 payload lacks from
// the env seed (design D3): old JSONB rows have no "platform"/"ai" keys,
// so a naive unmarshal yields Mode=="" and Validate would refuse to boot
// after the deploy. It runs wherever a stored payload becomes a Settings
// (Service.Load, Service.Get, the rollback path), fills in memory only,
// and never writes a version — the next operator write persists the
// complete document. Fields that are already set are left untouched.
func (s Settings) WithDefaults(b config.Bootstrap) Settings {
	out, _ := s.WithDefaultsNote(b)
	return out
}

// WithDefaultsNote is WithDefaults plus the substitution it had to make,
// if any, for the caller's log. Seeding platform.mode from ARB_MODE
// cannot see the stored venues/paper sections' cross-field rules: a
// pre-expansion row written under a different mode (e.g.
// paper_enabled=false with ARB_MODE=PAPER) would fail Validate after the
// fill and refuse the boot. When the seeded mode makes the document
// invalid but MARKET_DATA does not, the mode falls back to MARKET_DATA
// and the note names that — the same named substitution SeedMode emits.
// A stored (non-empty) mode is never touched.
func (s Settings) WithDefaultsNote(b config.Bootstrap) (Settings, string) {
	seed := Seed(b)
	modeSeeded := s.Platform.Mode == ""
	if modeSeeded {
		s.Platform.Mode = seed.Platform.Mode
	}
	if s.Platform.LogLevel == "" {
		s.Platform.LogLevel = seed.Platform.LogLevel
	}
	if s.Platform.AllowedOrigin == "" {
		s.Platform.AllowedOrigin = seed.Platform.AllowedOrigin
	}
	// The ai section is filled field by field: a stored document that
	// carries only some of the keys keeps them and gets the rest. Only
	// the enabled flag, which a bool cannot mark as absent, follows the
	// seed exclusively when the whole section is missing.
	if s.AI.Provider == "" && s.AI.Model == "" {
		s.AI.Enabled = seed.AI.Enabled
	}
	if s.AI.Provider == "" {
		s.AI.Provider = seed.AI.Provider
	}
	if s.AI.Model == "" {
		s.AI.Model = seed.AI.Model
	}
	if s.AI.Schedule == (AISchedule{}) {
		s.AI.Schedule = seed.AI.Schedule
	}
	if s.AI.Budget.MaxAnalysesPerDay == 0 {
		s.AI.Budget.MaxAnalysesPerDay = seed.AI.Budget.MaxAnalysesPerDay
	}
	if s.AI.Budget.MaxOutputTokens == 0 {
		s.AI.Budget.MaxOutputTokens = seed.AI.Budget.MaxOutputTokens
	}
	note := ""
	if modeSeeded && s.Platform.Mode != config.ModeMarketData && s.Validate() != nil {
		fallback := s
		fallback.Platform.Mode = config.ModeMarketData
		if fallback.Validate() == nil {
			note = fmt.Sprintf("stored settings predate platform.mode and do not validate under ARB_MODE=%s; platform.mode seeded as %s", s.Platform.Mode, config.ModeMarketData)
			s = fallback
		}
	}
	return s, note
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

// hotPrefixes are the dotted paths (exact, or prefix when they end in
// ".") that apply without an engine restart; everything else in the
// document is restart-scoped — i.e. exactly venues.*, paper.* and
// platform.mode (settings-expansion §6).
var hotPrefixes = []string{"telegram.", "ai.", "platform.log_level", "platform.allowed_origin"}

// RestartScoped reports whether diff contains at least one change whose
// path is NOT one of the hot-only prefixes.
func (s Settings) RestartScoped(diff map[string]strategy.Change) bool {
	for path := range diff {
		if !IsHot(path) {
			return true
		}
	}
	return false
}

// IsHot reports whether one dotted settings path applies without an
// engine restart.
func IsHot(path string) bool {
	for _, p := range hotPrefixes {
		if strings.HasSuffix(p, ".") {
			if strings.HasPrefix(path, p) {
				return true
			}
		} else if path == p {
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
	out := map[string]string{
		"platform.mode":                  "restart",
		"platform.log_level":             "hot",
		"platform.allowed_origin":        "hot",
		"ai.enabled":                     "hot",
		"ai.provider":                    "hot",
		"ai.model":                       "hot",
		"ai.schedule.hourly_minutes":     "hot",
		"ai.schedule.daily_hours":        "hot",
		"ai.schedule.weekly_hours":       "hot",
		"ai.budget.max_analyses_per_day": "hot",
		"ai.budget.max_output_tokens":    "hot",
		"telegram.disabled":              "hot",
	}
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
