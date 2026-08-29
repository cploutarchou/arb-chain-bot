package entitlements

// Package documents from docs/design/packages.md §2 (PROPOSAL
// 2026-08-27). Numbers that the table does not state explicitly are
// marked "derived" in docs/design/billing.md §2 and are as conservative
// as the table allows. Every document passes Validate (TestPackagesValid).
var packages = map[string]Entitlements{
	PackageWatch: {
		SchemaVersion: SchemaVersion, PackageCode: PackageWatch,
		Venues:    Venues{ScreenerMax: 3, ScreenerTiers: []string{"tier1"}, ScreenerFixed: []string{"binance", "okx", "bybit"}, TriangularMax: 1, DexEnabled: false, PerpsEnabled: true},
		Rules:     Rules{MaxActive: 2, TemplatesMax: 3, MinRefreshS: 30, Kinds: []string{"spread", "basis"}},
		Alerts:    Alerts{Channels: []string{"web"}, PerDay: 20, TelegramDestinationsMax: 0, MinCooldownS: 300},
		AutoPaper: AutoPaper{Strategies: []string{}, MaxOpenPositions: 0, LedgersMax: 1, MaxSizeQuote: "0"},
		API:       API{Enabled: false, Scopes: []string{}, RatePerMin: 0, Burst: 0, KeysMax: 0, Streaming: false},
		History:   History{RetentionDays: 1, ExportFormats: []string{}, ExportScheduled: false, Reports: "samples"},
		Seats:     Seats{Max: 1, Roles: []string{"owner"}},
		Support:   Support{Tier: "community", ResponseHours: 0},
		Execution: Execution{Paper: true, Live: false},
	},
	PackageSignal: {
		SchemaVersion: SchemaVersion, PackageCode: PackageSignal,
		Venues:    Venues{ScreenerMax: 6, ScreenerTiers: []string{"tier1"}, ScreenerFixed: []string{}, TriangularMax: 2, DexEnabled: false, PerpsEnabled: true},
		Rules:     Rules{MaxActive: 8, TemplatesMax: 10, MinRefreshS: 10, Kinds: []string{"spread", "carry", "basis"}},
		Alerts:    Alerts{Channels: []string{"web", "telegram"}, PerDay: 200, TelegramDestinationsMax: 1, MinCooldownS: 60},
		AutoPaper: AutoPaper{Strategies: []string{"cross_venue_spot"}, MaxOpenPositions: 5, LedgersMax: 1, MaxSizeQuote: "5000"},
		API:       API{Enabled: false, Scopes: []string{}, RatePerMin: 0, Burst: 0, KeysMax: 0, Streaming: false},
		History:   History{RetentionDays: 14, ExportFormats: []string{"csv"}, ExportScheduled: false, Reports: "weekly"},
		Seats:     Seats{Max: 1, Roles: []string{"owner"}},
		Support:   Support{Tier: "email", ResponseHours: 16},
		Execution: Execution{Paper: true, Live: false},
	},
	PackageOperator: {
		SchemaVersion: SchemaVersion, PackageCode: PackageOperator,
		Venues:    Venues{ScreenerMax: Unlimited, ScreenerTiers: []string{"tier1", "tier2"}, ScreenerFixed: []string{}, TriangularMax: 4, DexEnabled: false, PerpsEnabled: true},
		Rules:     Rules{MaxActive: 25, TemplatesMax: 40, MinRefreshS: 5, Kinds: []string{"spread", "carry", "basis", "funding", "triangular"}},
		Alerts:    Alerts{Channels: []string{"web", "telegram", "email"}, PerDay: 1500, TelegramDestinationsMax: 1, MinCooldownS: 30},
		AutoPaper: AutoPaper{Strategies: []string{"cross_venue_spot", "carry", "triangular"}, MaxOpenPositions: 30, LedgersMax: 3, MaxSizeQuote: "25000"},
		API:       API{Enabled: true, Scopes: []string{"read"}, RatePerMin: 60, Burst: 20, KeysMax: 2, Streaming: false},
		History:   History{RetentionDays: 90, ExportFormats: []string{"csv"}, ExportScheduled: false, Reports: "nightly"},
		Seats:     Seats{Max: 3, Roles: []string{"owner", "admin", "viewer"}},
		Support:   Support{Tier: "priority", ResponseHours: 8},
		Execution: Execution{Paper: true, Live: false},
	},
	PackageDesk: {
		SchemaVersion: SchemaVersion, PackageCode: PackageDesk,
		// DEX coverage is NOT advertised here: T-102 (operator decision
		// 2026-08-29) switched it off until T-116 builds it. Restore
		// "dex" and DexEnabled: true in the same change that flips
		// DexImplemented — Validate rejects them until then.
		Venues:    Venues{ScreenerMax: Unlimited, ScreenerTiers: []string{"tier1", "tier2"}, ScreenerFixed: []string{}, TriangularMax: Unlimited, DexEnabled: false, PerpsEnabled: true},
		Rules:     Rules{MaxActive: 80, TemplatesMax: Unlimited, MinRefreshS: 3, Kinds: []string{"spread", "carry", "basis", "funding", "triangular"}},
		Alerts:    Alerts{Channels: []string{"web", "telegram", "email", "webhook"}, PerDay: 8000, TelegramDestinationsMax: 1, MinCooldownS: 10},
		AutoPaper: AutoPaper{Strategies: []string{"cross_venue_spot", "carry", "futures_futures", "funding_harvest", "triangular"}, MaxOpenPositions: 150, LedgersMax: 10, MaxSizeQuote: "100000"},
		API:       API{Enabled: true, Scopes: []string{"read", "rules:write", "templates:write"}, RatePerMin: 300, Burst: 60, KeysMax: 10, Streaming: false},
		History:   History{RetentionDays: 400, ExportFormats: []string{"csv", "parquet"}, ExportScheduled: false, Reports: "nightly_compare"},
		Seats:     Seats{Max: 12, Roles: []string{"owner", "admin", "operator", "viewer"}},
		Support:   Support{Tier: "desk", ResponseHours: 8},
		Execution: Execution{Paper: true, Live: false},
	},
	PackageInstitution: {
		SchemaVersion: SchemaVersion, PackageCode: PackageInstitution,
		// See the Desk note: no DEX tier until T-116 (T-102).
		Venues:    Venues{ScreenerMax: Unlimited, ScreenerTiers: []string{"tier1", "tier2"}, ScreenerFixed: []string{}, TriangularMax: Unlimited, DexEnabled: false, PerpsEnabled: true},
		Rules:     Rules{MaxActive: 250, TemplatesMax: Unlimited, MinRefreshS: 2, Kinds: []string{"spread", "carry", "basis", "funding", "triangular"}},
		Alerts:    Alerts{Channels: []string{"web", "telegram", "email", "webhook"}, PerDay: 40000, TelegramDestinationsMax: 5, MinCooldownS: 5},
		AutoPaper: AutoPaper{Strategies: []string{"cross_venue_spot", "carry", "futures_futures", "funding_harvest", "triangular"}, MaxOpenPositions: 600, LedgersMax: 25, MaxSizeQuote: "1000000"},
		API:       API{Enabled: true, Scopes: []string{"read", "rules:write", "templates:write", "paper:write"}, RatePerMin: 1200, Burst: 200, KeysMax: 50, Streaming: true},
		History:   History{RetentionDays: 1095, ExportFormats: []string{"csv", "parquet"}, ExportScheduled: true, Reports: "custom"},
		Seats:     Seats{Max: 40, Roles: []string{"owner", "admin", "operator", "viewer", "custom"}},
		Support:   Support{Tier: "named", ResponseHours: 4},
		Execution: Execution{Paper: true, Live: false},
	},
}

// Package returns a copy of the package document for code.
func Package(code string) (Entitlements, bool) {
	p, ok := packages[code]
	if !ok {
		return Entitlements{}, false
	}
	return p.Clone(), true
}
