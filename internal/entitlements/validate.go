package entitlements

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sync"
)

// ErrInvalid marks a document that fails schema validation.
var ErrInvalid = errors.New("entitlements: invalid document")

// ErrLiveExecution is the one validation failure that gets its own
// sentinel: a document (package, override or merge result) claiming
// execution.live = true. It is never accepted from any source
// (packages.md §7; compliance review #23).
var ErrLiveExecution = fmt.Errorf("%w: execution.live must be false", ErrInvalid)

// Enumerations, verbatim from schema.v1.json (pinned by test).
var (
	enumTiers      = []string{"tier1", "tier2", "dex"}
	enumKinds      = []string{"spread", "carry", "basis", "funding", "triangular"}
	enumChannels   = []string{"web", "telegram", "email", "webhook"}
	enumStrategies = []string{"cross_venue_spot", "carry", "futures_futures", "funding_harvest", "triangular"}
	enumScopes     = []string{"read", "rules:write", "templates:write", "paper:write"}
	enumFormats    = []string{"csv", "parquet"}
	enumReports    = []string{"samples", "weekly", "nightly", "nightly_compare", "custom"}
	enumRoles      = []string{"owner", "admin", "operator", "viewer", "custom"}
	enumSupport    = []string{"community", "email", "priority", "desk", "named"}
)

var decimalPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// ValidateJSON decodes raw with unknown properties rejected
// (additionalProperties: false at every level) and then applies
// Validate. It is the entry point for merge results and stored
// documents.
func ValidateJSON(raw []byte) (Entitlements, error) {
	// encoding/json matches keys case-insensitively, so
	// DisallowUnknownFields alone would let {"Execution":{"Live":true}}
	// through as execution.live. Walk the raw keys against the schema
	// first, case-sensitively.
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return Entitlements{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := checkKeys("", generic, schemaRoot()); err != nil {
		return Entitlements{}, err
	}
	var e Entitlements
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return Entitlements{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := Validate(e); err != nil {
		return Entitlements{}, err
	}
	return e, nil
}

// Validate mirrors schema.v1.json: required keys are present by
// construction (struct fields), so this checks constants, enums,
// minimums and the decimal pattern. The live-execution check runs
// first so its sentinel wins over any other failure.
func Validate(e Entitlements) error {
	if e.Execution.Live {
		return ErrLiveExecution
	}
	if !e.Execution.Paper {
		return fmt.Errorf("%w: execution.paper must be true", ErrInvalid)
	}
	if e.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: schema_version must be %d", ErrInvalid, SchemaVersion)
	}
	if Rank(e.PackageCode) < 0 {
		return fmt.Errorf("%w: package_code %q not in %v", ErrInvalid, e.PackageCode, Codes)
	}
	v := e.Venues
	if err := minOrUnlimited("venues.screener_max", v.ScreenerMax); err != nil {
		return err
	}
	if err := enumList("venues.screener_tiers", v.ScreenerTiers, enumTiers); err != nil {
		return err
	}
	if v.ScreenerFixed == nil {
		return fmt.Errorf("%w: venues.screener_fixed is required", ErrInvalid)
	}
	if err := minOrUnlimited("venues.triangular_max", v.TriangularMax); err != nil {
		return err
	}
	r := e.Rules
	if err := min("rules.max_active", r.MaxActive, 0); err != nil {
		return err
	}
	if err := minOrUnlimited("rules.templates_max", r.TemplatesMax); err != nil {
		return err
	}
	if err := min("rules.min_refresh_s", r.MinRefreshS, 2); err != nil {
		return err
	}
	if err := enumList("rules.kinds", r.Kinds, enumKinds); err != nil {
		return err
	}
	a := e.Alerts
	if err := enumList("alerts.channels", a.Channels, enumChannels); err != nil {
		return err
	}
	if err := min("alerts.per_day", a.PerDay, 0); err != nil {
		return err
	}
	if err := min("alerts.telegram_destinations_max", a.TelegramDestinationsMax, 0); err != nil {
		return err
	}
	if err := min("alerts.min_cooldown_s", a.MinCooldownS, 1); err != nil {
		return err
	}
	p := e.AutoPaper
	if err := enumList("auto_paper.strategies", p.Strategies, enumStrategies); err != nil {
		return err
	}
	if err := min("auto_paper.max_open_positions", p.MaxOpenPositions, 0); err != nil {
		return err
	}
	if err := min("auto_paper.ledgers_max", p.LedgersMax, 1); err != nil {
		return err
	}
	if !decimalPattern.MatchString(p.MaxSizeQuote) {
		return fmt.Errorf("%w: auto_paper.max_size_quote must be a decimal string", ErrInvalid)
	}
	api := e.API
	if err := enumList("api.scopes", api.Scopes, enumScopes); err != nil {
		return err
	}
	for name, n := range map[string]int{"api.rate_per_min": api.RatePerMin, "api.burst": api.Burst, "api.keys_max": api.KeysMax} {
		if err := min(name, n, 0); err != nil {
			return err
		}
	}
	h := e.History
	if err := min("history.retention_days", h.RetentionDays, 1); err != nil {
		return err
	}
	if err := enumList("history.export_formats", h.ExportFormats, enumFormats); err != nil {
		return err
	}
	if !Has(enumReports, h.Reports) {
		return fmt.Errorf("%w: history.reports %q not in %v", ErrInvalid, h.Reports, enumReports)
	}
	if err := min("seats.max", e.Seats.Max, 1); err != nil {
		return err
	}
	if err := enumList("seats.roles", e.Seats.Roles, enumRoles); err != nil {
		return err
	}
	if !Has(enumSupport, e.Support.Tier) {
		return fmt.Errorf("%w: support.tier %q not in %v", ErrInvalid, e.Support.Tier, enumSupport)
	}
	if err := min("support.response_hours", e.Support.ResponseHours, 0); err != nil {
		return err
	}
	return nil
}

func min(name string, v, floor int) error {
	if v < floor {
		return fmt.Errorf("%w: %s must be >= %d, got %d", ErrInvalid, name, floor, v)
	}
	return nil
}

// minOrUnlimited accepts >= 0 or the -1 sentinel. The schema states
// minimum 0 and documents -1 as "unlimited" in the description; the
// sentinel is accepted here so the schema text and the Go values agree
// on what packages.md's "unlimited" cells mean.
func minOrUnlimited(name string, v int) error {
	if v == Unlimited {
		return nil
	}
	return min(name, v, 0)
}

func enumList(name string, list, allowed []string) error {
	if list == nil {
		return fmt.Errorf("%w: %s is required", ErrInvalid, name)
	}
	for _, v := range list {
		if !Has(allowed, v) {
			return fmt.Errorf("%w: %s value %q not in %v", ErrInvalid, name, v, allowed)
		}
	}
	return nil
}

// schemaNode is the slice of schema.v1.json the key walk needs.
type schemaNode struct {
	Properties           map[string]*schemaNode `json:"properties"`
	AdditionalProperties *bool                  `json:"additionalProperties"`
}

var (
	schemaOnce sync.Once
	schemaTree *schemaNode
)

func schemaRoot() *schemaNode {
	schemaOnce.Do(func() {
		var n schemaNode
		if err := json.Unmarshal(SchemaJSON, &n); err != nil {
			panic("entitlements: embedded schema: " + err.Error())
		}
		schemaTree = &n
	})
	return schemaTree
}

// checkKeys rejects, case-sensitively, any key the schema does not list
// at a level that declares additionalProperties: false. "status" is
// tolerated at the root because Resolve writes it into the marshalled
// document (never merged from an override; Merge strips it first).
func checkKeys(path string, doc map[string]any, node *schemaNode) error {
	strict := node.AdditionalProperties != nil && !*node.AdditionalProperties
	for k, v := range doc {
		child, known := node.Properties[k]
		if !known {
			if path == "" && k == "status" {
				continue
			}
			if strict {
				return fmt.Errorf("%w: unknown key %q at %q", ErrInvalid, k, path)
			}
			continue
		}
		if sub, ok := v.(map[string]any); ok && child != nil {
			if err := checkKeys(path+"/"+k, sub, child); err != nil {
				return err
			}
		}
	}
	return nil
}
