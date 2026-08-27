package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
)

// Spec describes one registry entry: the env variable it falls back to,
// the console label, the minimum accepted length, and when a write
// takes effect.
type Spec struct {
	Env     string
	Label   string
	MinLen  int
	Applies string // "immediately" | "process_restart" | "not_consumed"
	Group   string // "provider" | "exchange"
	Venue   string // exchange group only: the venue id the credential belongs to
}

// Applies values (backend-computed, never hardcoded on the frontend).
const (
	AppliesImmediately    = "immediately"
	AppliesProcessRestart = "process_restart"
	// AppliesNotConsumed marks a credential that is stored encrypted but
	// read by NO component: exchange API credentials. Manager.Get refuses
	// them, so no code path — trading or otherwise — can obtain the value
	// without a reviewed change to this package.
	AppliesNotConsumed = "not_consumed"
)

// Registry groups.
const (
	GroupProvider = "provider"
	GroupExchange = "exchange"
)

// Known is the CLOSED registry. Adding a name is a reviewed code change.
//
// Provider entries are credentials for the advisory/notification channels
// and are resolved by their consumers through Manager.Get.
//
// Exchange entries hold the operator's exchange API credentials so they
// can be managed from the console and stored encrypted instead of in
// .env. They are write-only in the strongest sense: Manager.Get refuses
// the exchange group, nothing in the codebase reads them, and live
// trading stays disabled by design. They exist so that a future,
// separately reviewed read-only consumer (fee-tier lookup, account
// snapshot) has a vetted place to find them; operators should create
// them with read-only permissions and no trading/withdrawal scopes.
var Known = map[string]Spec{
	"anthropic_api_key":  {Env: "ANTHROPIC_API_KEY", Label: "Anthropic API key", MinLen: 20, Applies: AppliesImmediately, Group: GroupProvider},
	"telegram_bot_token": {Env: "ARB_TELEGRAM_TOKEN", Label: "Telegram bot token", MinLen: 20, Applies: AppliesProcessRestart, Group: GroupProvider},
	// Paddle Billing (T-083, docs/design/billing.md): the server-side API
	// key (checkout transactions, subscription updates, portal sessions)
	// and the notification-destination secret that signs every webhook.
	// Both are read on each use, so a rotation applies immediately. The
	// client-side token Paddle.js needs is public and lives in config,
	// not here. No card data ever reaches this process.
	"paddle_api_key":        {Env: "PADDLE_API_KEY", Label: "Paddle API key", MinLen: 20, Applies: AppliesImmediately, Group: GroupProvider},
	"paddle_webhook_secret": {Env: "PADDLE_WEBHOOK_SECRET", Label: "Paddle webhook secret", MinLen: 20, Applies: AppliesImmediately, Group: GroupProvider},
	// Alert-channel e-mail sink (T-086, docs/design/billing.md §4): an
	// SMTP URL of the form smtp://user:pass@host:port (STARTTLS), read
	// on each send so a rotation applies immediately. It carries a
	// password, so it is never logged and Manager.Get is its only path
	// out of the vault.
	"smtp_url": {Env: "SMTP_URL", Label: "SMTP URL for alert e-mails", MinLen: 10, Applies: AppliesImmediately, Group: GroupProvider},

	"binance_api_key":       exchangeCred("binance", "Binance API key"),
	"binance_api_secret":    exchangeCred("binance", "Binance API secret"),
	"okx_api_key":           exchangeCred("okx", "OKX API key"),
	"okx_api_secret":        exchangeCred("okx", "OKX API secret"),
	"okx_api_passphrase":    exchangeCred("okx", "OKX API passphrase"),
	"bybit_api_key":         exchangeCred("bybit", "Bybit API key"),
	"bybit_api_secret":      exchangeCred("bybit", "Bybit API secret"),
	"bitget_api_key":        exchangeCred("bitget", "Bitget API key"),
	"bitget_api_secret":     exchangeCred("bitget", "Bitget API secret"),
	"bitget_api_passphrase": exchangeCred("bitget", "Bitget API passphrase"),
	"gate_api_key":          exchangeCred("gate", "Gate API key"),
	"gate_api_secret":       exchangeCred("gate", "Gate API secret"),
	"mexc_api_key":          exchangeCred("mexc", "MEXC API key"),
	"mexc_api_secret":       exchangeCred("mexc", "MEXC API secret"),
}

// exchangeCred builds an exchange-group entry: no env fallback (exchange
// credentials are never read from the environment), a short minimum
// (passphrases are operator-chosen), and never consumed.
func exchangeCred(venue, label string) Spec {
	return Spec{Label: label, MinLen: 8, Applies: AppliesNotConsumed, Group: GroupExchange, Venue: venue}
}

// IsConsumable reports whether Manager.Get may resolve name.
func IsConsumable(name string) bool {
	spec, ok := Known[name]
	return ok && spec.Group != GroupExchange
}

// Names returns the registry names, sorted.
func Names() []string {
	out := make([]string, 0, len(Known))
	for n := range Known {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ErrUnknownSecret rejects a name outside the registry (404).
var ErrUnknownSecret = errors.New("secrets: unknown secret name")

// ErrInvalidValue rejects a value that fails the length/charset rules (400).
var ErrInvalidValue = errors.New("secrets: invalid value")

const maxLen = 4096

// ValidateValue trims surrounding whitespace (pasting a token with a
// trailing newline is the single most common operator error) and then
// enforces MinLen ≤ len ≤ 4096 and printable ASCII with no interior
// whitespace: a token containing "\n" is an HTTP header-injection
// vector (the Telegram client builds its base URL from it; the
// Anthropic client puts it in x-api-key).
//
// The value travels as []byte so the caller can zero it after use: a
// Go string copy cannot be scrubbed. The returned slice aliases the
// trimmed window of the input (no extra copy is made).
func ValidateValue(name string, value []byte) ([]byte, error) {
	spec, ok := Known[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownSecret, name)
	}
	v := bytes.TrimSpace(value)
	if len(v) < spec.MinLen {
		return nil, fmt.Errorf("%w: %s must be at least %d characters", ErrInvalidValue, name, spec.MinLen)
	}
	if len(v) > maxLen {
		return nil, fmt.Errorf("%w: %s must be at most %d characters", ErrInvalidValue, name, maxLen)
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c <= 0x20 || c >= 0x7f {
			return nil, fmt.Errorf("%w: %s must be printable ASCII with no whitespace", ErrInvalidValue, name)
		}
	}
	return v, nil
}
