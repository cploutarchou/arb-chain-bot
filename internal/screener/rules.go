package screener

import (
	"fmt"
	"net/mail"
	"net/url"
	"strings"

	"github.com/shopspring/decimal"
)

// RuleKind is the alert-rule discriminator (design §7).
type RuleKind string

const (
	RuleKindSpread RuleKind = "spread"
	RuleKindCarry  RuleKind = "carry"
	RuleKindBasis  RuleKind = "basis"
)

// Rule is one alert rule (design §7); evaluation/cooldown/dedup and the
// Telegram push land in T-070 — this task only owns the shape and its
// validation.
type Rule struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Kind    RuleKind `json:"kind"`

	MinSpreadBps *decimal.Decimal `json:"min_spread_bps,omitempty"`
	MinCarryAPR  *decimal.Decimal `json:"min_carry_apr,omitempty"`

	MinLiquidityQuote decimal.Decimal `json:"min_liquidity_quote"`
	MinLifetimeS      int64           `json:"min_lifetime_s"`

	BuyVenues  []Venue  `json:"buy_venues"`
	SellVenues []Venue  `json:"sell_venues"`
	Quotes     []string `json:"quotes"`
	BasesAllow []string `json:"bases_allow"`
	BasesDeny  []string `json:"bases_deny"`

	CooldownS int64 `json:"cooldown_s"`

	// Telegram is kept for backward compatibility with rules saved
	// before T-086 (Channels did not exist); EffectiveChannels folds it
	// in as the ["telegram"] default when Channels is empty. New rules
	// should set Channels explicitly.
	Telegram bool `json:"telegram"`
	// Channels lists the alert-delivery channels this rule pushes to
	// (docs/design/packages.md §3.1 alerts.channels): "telegram",
	// "email", "webhook". Each entry is gated per-organisation by
	// entitlements.CheckChannel at open time; this field only says what
	// the rule ASKS for.
	Channels []string `json:"channels,omitempty"`
	// EmailTo is the destination address for the "email" channel.
	EmailTo string `json:"email_to,omitempty"`
	// WebhookURL is the destination for the "webhook" channel. Refused
	// at send time (not just here) if it resolves to a private/loopback
	// address (internal/notification's webhook sink).
	WebhookURL string `json:"webhook_url,omitempty"`
	// WebhookSecret signs every webhook delivery (X-Arb-Signature,
	// HMAC-SHA256). Write-only: every read path that returns a Rule
	// must zero it (screener.Rule.Redact) before the JSON leaves the
	// process — decodeScreenerRule below is the one place it is
	// legitimately read from a request body.
	WebhookSecret string `json:"webhook_secret,omitempty"`

	AutoPaper      bool            `json:"auto_paper"`
	PaperSizeQuote decimal.Decimal `json:"paper_size_quote"`

	// Params are the optional strategy-model inputs (rule_params.go);
	// nil keeps every documented default.
	Params *RuleParams `json:"params,omitempty"`
}

// AllowedChannels enumerates the alerts.channels vocabulary
// (docs/design/packages.md §3.1).
var AllowedChannels = map[string]bool{"telegram": true, "email": true, "webhook": true}

// EffectiveChannels returns the channels this rule actually pushes to:
// Channels verbatim when set, otherwise ["telegram"] when the legacy
// Telegram flag is set, otherwise none. Callers (the evaluator, the
// entitlement check) always go through this rather than reading
// Telegram or Channels directly, so the two fields never drift apart in
// two different places.
func (r Rule) EffectiveChannels() []string {
	if len(r.Channels) > 0 {
		return r.Channels
	}
	if r.Telegram {
		return []string{"telegram"}
	}
	return nil
}

// Redact zeroes WebhookSecret for any response leaving the process
// (list/create/update). The plaintext secret is set once, by the
// caller who configured it, and is never echoed back — the same shape
// as api_keys.hash.
func (r Rule) Redact() Rule {
	r.WebhookSecret = ""
	return r
}

const maxRuleNameLen = 100
const minWebhookSecretLen = 16

// Validate rejects a structurally invalid rule. Pure, no I/O — it does
// not check that buy/sell venues actually have live collectors (T-066).
func (r Rule) Validate() error {
	if r.Name == "" || len(r.Name) > maxRuleNameLen {
		return fmt.Errorf("%w: name must be 1..%d chars", ErrInvalid, maxRuleNameLen)
	}
	switch r.Kind {
	case RuleKindSpread:
		if r.MinSpreadBps == nil {
			return fmt.Errorf("%w: kind=spread requires min_spread_bps", ErrInvalid)
		}
		if r.MinSpreadBps.IsNegative() {
			return fmt.Errorf("%w: min_spread_bps must be >= 0", ErrInvalid)
		}
	case RuleKindCarry, RuleKindBasis:
		if r.MinCarryAPR == nil {
			return fmt.Errorf("%w: kind=%s requires min_carry_apr", ErrInvalid, r.Kind)
		}
	default:
		return fmt.Errorf("%w: kind must be one of spread|carry|basis, got %q", ErrInvalid, r.Kind)
	}
	if r.MinLiquidityQuote.IsNegative() {
		return fmt.Errorf("%w: min_liquidity_quote must be >= 0", ErrInvalid)
	}
	if r.MinLifetimeS < 0 {
		return fmt.Errorf("%w: min_lifetime_s must be >= 0", ErrInvalid)
	}
	if r.CooldownS < 0 {
		return fmt.Errorf("%w: cooldown_s must be >= 0", ErrInvalid)
	}
	for _, v := range r.BuyVenues {
		if !KnownVenues[v] {
			return fmt.Errorf("%w: buy_venues has unknown venue %q", ErrInvalid, v)
		}
	}
	for _, v := range r.SellVenues {
		if !KnownVenues[v] {
			return fmt.Errorf("%w: sell_venues has unknown venue %q", ErrInvalid, v)
		}
	}
	if r.AutoPaper && !r.PaperSizeQuote.IsPositive() {
		return fmt.Errorf("%w: auto_paper requires paper_size_quote > 0", ErrInvalid)
	}
	if err := r.validateChannels(); err != nil {
		return err
	}
	return r.Params.Validate(r.Kind)
}

// validateChannels enforces the channels vocabulary and each channel's
// required, per-channel fields (docs/design/packages.md §3.1
// alerts.channels).
func (r Rule) validateChannels() error {
	seen := map[string]bool{}
	for _, ch := range r.Channels {
		if !AllowedChannels[ch] {
			return fmt.Errorf("%w: channels has unknown channel %q", ErrInvalid, ch)
		}
		if seen[ch] {
			return fmt.Errorf("%w: channels has duplicate %q", ErrInvalid, ch)
		}
		seen[ch] = true
	}
	for _, ch := range r.EffectiveChannels() {
		switch ch {
		case "email":
			if _, err := mail.ParseAddress(r.EmailTo); err != nil {
				return fmt.Errorf("%w: channel email requires a valid email_to", ErrInvalid)
			}
		case "webhook":
			u, err := url.Parse(r.WebhookURL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("%w: channel webhook requires an http(s) webhook_url", ErrInvalid)
			}
			if len(strings.TrimSpace(r.WebhookSecret)) < minWebhookSecretLen {
				return fmt.Errorf("%w: channel webhook requires a webhook_secret of at least %d characters", ErrInvalid, minWebhookSecretLen)
			}
		}
	}
	return nil
}
