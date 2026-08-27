package platform

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
)

// ModeProfile is one row of ModeTable: display and enforcement in one
// place (settings-expansion §2.1). The enum the console renders is
// exactly this list; Available is what Validate enforces.
type ModeProfile struct {
	ID        config.Mode `json:"id"`
	Available bool        `json:"available"`
	Reason    string      `json:"reason,omitempty"`
}

// shadowReason is D9: SHADOW is enumerated but not wired into Engine.Run.
const shadowReason = "shadow execution is not wired into Engine.Run; enabling it changes the paper→portfolio result path (separate task)"

// ModeTable lists every operating mode the platform enumerates. LIVE is
// absent by construction — there is no config.Mode constant for it to
// be absent from. REPLAY/BACKTEST are batch runs, not process modes,
// and are deliberately not in this table either (ValidateMode names
// them specifically).
func ModeTable() []ModeProfile {
	return []ModeProfile{
		{ID: config.ModeMarketData, Available: true},
		{ID: config.ModeRecord, Available: true},
		{ID: config.ModePaper, Available: true},
		{ID: config.ModeShadow, Available: false, Reason: shadowReason},
	}
}

// Settable reports whether m is a ModeTable entry with Available set —
// the single rule Validate, Seed and WithDefaults all share.
func Settable(m config.Mode) bool {
	for _, p := range ModeTable() {
		if p.ID == m {
			return p.Available
		}
	}
	return false
}

// ValidateMode rejects anything not Settable, quoting the table reason.
// Two families get a NAMED error rather than the generic one (§2.2): the
// string LIVE (any case) — live trading is permanently disabled, and the
// enum must never grow into it by accident — and REPLAY/BACKTEST, which
// are batch runs started from Replays/Campaigns, never a process mode.
func ValidateMode(m config.Mode) error {
	upper := config.Mode(strings.ToUpper(string(m)))
	switch upper {
	case "LIVE":
		return fmt.Errorf("platform: platform.mode LIVE is not an operating mode: live trading is permanently disabled (LiveExecutor returns ErrLiveTradingDisabled)")
	case config.ModeReplay, config.ModeBacktest:
		return fmt.Errorf("platform: platform.mode %s is a batch run, not a process mode: start it from Replays or Campaigns", upper)
	}
	if m == "" {
		return fmt.Errorf("platform: platform.mode is required (one of %s)", strings.Join(settableModes(), ", "))
	}
	for _, p := range ModeTable() {
		if p.ID != m {
			continue
		}
		if p.Available {
			return nil
		}
		return fmt.Errorf("platform: platform.mode %s is not available: %s", m, p.Reason)
	}
	return fmt.Errorf("platform: platform.mode %q is not an operating mode (one of %s)", m, strings.Join(settableModes(), ", "))
}

func settableModes() []string {
	var out []string
	for _, p := range ModeTable() {
		if p.Available {
			out = append(out, string(p.ID))
		}
	}
	return out
}

// LogLevels is the accepted platform.log_level enum.
var LogLevels = []string{"debug", "info", "warn", "error"}

// ValidateLogLevel accepts exactly debug|info|warn|error (lower-case).
func ValidateLogLevel(v string) error {
	for _, l := range LogLevels {
		if v == l {
			return nil
		}
	}
	return fmt.Errorf("platform: platform.log_level must be one of %s, got %q", strings.Join(LogLevels, "|"), v)
}

// ValidateOrigin accepts a strict absolute origin (§4.3):
// scheme://host[:port], scheme ∈ {http, https}, no path/query/fragment,
// no wildcard, at most 255 chars. The websocket origin check keeps
// admitting same-host origins unconditionally, so a bad value cannot
// lock a same-origin console out.
func ValidateOrigin(v string) error {
	if v == "" || len(v) > 255 {
		return fmt.Errorf("platform: platform.allowed_origin must be a non-empty origin of at most 255 chars")
	}
	if strings.Contains(v, "*") {
		return fmt.Errorf("platform: platform.allowed_origin must not contain a wildcard")
	}
	u, err := url.Parse(v)
	if err != nil {
		return fmt.Errorf("platform: platform.allowed_origin is not a URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("platform: platform.allowed_origin scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("platform: platform.allowed_origin must have a host")
	}
	if u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.Opaque != "" || strings.HasSuffix(v, "/") {
		return fmt.Errorf("platform: platform.allowed_origin must be scheme://host[:port] with no path, query, fragment or credentials")
	}
	if u.Scheme+"://"+u.Host != v {
		return fmt.Errorf("platform: platform.allowed_origin must be exactly scheme://host[:port]")
	}
	return nil
}

// AIProviderProfile is one row of AIProviderTable (design D10).
type AIProviderProfile struct {
	ID        string `json:"id"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// AIProviderTable lists every provider the console may show. Only
// entries with Available are accepted by Validate; "openai" is listed
// so the console can say honestly that it is not built rather than
// omit it — the ai.Advisor seam would accept one with no interface
// change, but there is no implementation in internal/ai.
func AIProviderTable() []AIProviderProfile {
	return []AIProviderProfile{
		{ID: "anthropic", Available: true},
		{ID: "fake", Available: true},
		{ID: "openai", Available: false, Reason: "provider not built"},
	}
}

// AIProviderSettable reports whether id is an available provider.
func AIProviderSettable(id string) bool {
	for _, p := range AIProviderTable() {
		if p.ID == id {
			return p.Available
		}
	}
	return false
}

func settableProviders() []string {
	var out []string
	for _, p := range AIProviderTable() {
		if p.Available {
			out = append(out, p.ID)
		}
	}
	return out
}
