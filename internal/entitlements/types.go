// Package entitlements is the server-side source of feature gating
// (docs/design/packages.md §3, saas-billing skill): package documents
// as Go values, the JSON schema they must satisfy, the deep-merge of a
// per-organisation override, and the resolver the API middleware uses.
//
// Two invariants are enforced by code, not by data:
//
//   - execution.live is false in every package, every override and
//     every merge result (packages.md §7; compliance review #23). The
//     validator rejects any document that says otherwise, and Resolve
//     never returns a document with Live == true.
//   - Every limit is a plain value the API enforces; the console only
//     reads GET /api/v1/me.entitlements.
package entitlements

import (
	_ "embed"
	"encoding/json"
)

// SchemaVersion is the only schema_version this package understands.
const SchemaVersion = 1

// SchemaJSON is the draft 2020-12 schema from packages.md §3.1, verbatim.
// The Go validator in validate.go mirrors it; TestSchemaAgreesWithGo
// pins the enums and required keys against this file.
//
//go:embed schema.v1.json
var SchemaJSON []byte

// Package codes, ordered from smallest to largest (Rank).
const (
	PackageWatch       = "watch"
	PackageSignal      = "signal"
	PackageOperator    = "operator"
	PackageDesk        = "desk"
	PackageInstitution = "institution"
)

// Unlimited is the sentinel for "-1 = unlimited" integer limits.
const Unlimited = -1

// Entitlements is the resolved document for one organisation.
type Entitlements struct {
	SchemaVersion int       `json:"schema_version"`
	PackageCode   string    `json:"package_code"`
	Venues        Venues    `json:"venues"`
	Rules         Rules     `json:"rules"`
	Alerts        Alerts    `json:"alerts"`
	AutoPaper     AutoPaper `json:"auto_paper"`
	API           API       `json:"api"`
	History       History   `json:"history"`
	Seats         Seats     `json:"seats"`
	Support       Support   `json:"support"`
	Execution     Execution `json:"execution"`
	WhiteLabel    bool      `json:"white_label"`

	// Status is the subscription state that shaped the document
	// (resolve.go); informational for the console, not part of the
	// schema and never merged from an override.
	Status Status `json:"status,omitempty"`
}

type Venues struct {
	ScreenerMax   int      `json:"screener_max"`
	ScreenerTiers []string `json:"screener_tiers"`
	ScreenerFixed []string `json:"screener_fixed"`
	TriangularMax int      `json:"triangular_max"`
	DexEnabled    bool     `json:"dex_enabled"`
	PerpsEnabled  bool     `json:"perps_enabled"`
}

type Rules struct {
	MaxActive    int      `json:"max_active"`
	TemplatesMax int      `json:"templates_max"`
	MinRefreshS  int      `json:"min_refresh_s"`
	Kinds        []string `json:"kinds"`
}

type Alerts struct {
	Channels                []string `json:"channels"`
	PerDay                  int      `json:"per_day"`
	TelegramDestinationsMax int      `json:"telegram_destinations_max"`
	MinCooldownS            int      `json:"min_cooldown_s"`
}

type AutoPaper struct {
	Strategies       []string `json:"strategies"`
	MaxOpenPositions int      `json:"max_open_positions"`
	LedgersMax       int      `json:"ledgers_max"`
	MaxSizeQuote     string   `json:"max_size_quote"`
}

type API struct {
	Enabled    bool     `json:"enabled"`
	Scopes     []string `json:"scopes"`
	RatePerMin int      `json:"rate_per_min"`
	Burst      int      `json:"burst"`
	KeysMax    int      `json:"keys_max"`
	Streaming  bool     `json:"streaming"`
}

type History struct {
	RetentionDays   int      `json:"retention_days"`
	ExportFormats   []string `json:"export_formats"`
	ExportScheduled bool     `json:"export_scheduled"`
	Reports         string   `json:"reports"`
}

type Seats struct {
	Max   int      `json:"max"`
	Roles []string `json:"roles"`
}

type Support struct {
	Tier          string `json:"tier"`
	ResponseHours int    `json:"response_hours"`
}

// Execution is not an entitlement (packages.md §7): Paper is always
// true and Live is always false. The fields exist so the document says
// so explicitly to every reader.
type Execution struct {
	Paper bool `json:"paper"`
	Live  bool `json:"live"`
}

// Status describes the subscription state applied by Resolve.
type Status struct {
	Subscription string `json:"subscription"`            // none|trialing|active|past_due|paused|canceled
	ReadOnly     bool   `json:"read_only"`               // past_due beyond the 7-day grace (packages.md §4)
	TrialEndsAt  string `json:"trial_ends_at,omitempty"` // RFC 3339
	Effective    string `json:"effective_package"`       // package the limits come from (may be "watch" after a demotion)
}

// Has reports whether list contains v.
func Has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Clone returns a deep copy (slices are copied).
func (e Entitlements) Clone() Entitlements {
	b, _ := json.Marshal(e)
	var out Entitlements
	_ = json.Unmarshal(b, &out)
	return out
}

// Rank orders package codes for upgrade/downgrade decisions
// (packages.md §4): -1 for unknown codes.
func Rank(code string) int {
	for i, c := range Codes {
		if c == code {
			return i
		}
	}
	return -1
}

// Codes lists the package codes smallest first.
var Codes = []string{PackageWatch, PackageSignal, PackageOperator, PackageDesk, PackageInstitution}
