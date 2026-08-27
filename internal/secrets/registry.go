package secrets

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Spec describes one registry entry: the env variable it falls back to,
// the console label, the minimum accepted length, and when a write
// takes effect.
type Spec struct {
	Env     string
	Label   string
	MinLen  int
	Applies string // "immediately" | "process_restart"
}

// Applies values (backend-computed, never hardcoded on the frontend).
const (
	AppliesImmediately    = "immediately"
	AppliesProcessRestart = "process_restart"
)

// Known is the CLOSED registry. Both entries are provider credentials
// for advisory/notification channels; neither is, or can become, an
// exchange trading key. Adding a name is a reviewed code change.
var Known = map[string]Spec{
	"anthropic_api_key":  {Env: "ANTHROPIC_API_KEY", Label: "Anthropic API key", MinLen: 20, Applies: AppliesImmediately},
	"telegram_bot_token": {Env: "ARB_TELEGRAM_TOKEN", Label: "Telegram bot token", MinLen: 20, Applies: AppliesProcessRestart},
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
func ValidateValue(name, value string) (string, error) {
	spec, ok := Known[name]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownSecret, name)
	}
	v := strings.TrimSpace(value)
	if len(v) < spec.MinLen {
		return "", fmt.Errorf("%w: %s must be at least %d characters", ErrInvalidValue, name, spec.MinLen)
	}
	if len(v) > maxLen {
		return "", fmt.Errorf("%w: %s must be at most %d characters", ErrInvalidValue, name, maxLen)
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c <= 0x20 || c >= 0x7f {
			return "", fmt.Errorf("%w: %s must be printable ASCII with no whitespace", ErrInvalidValue, name)
		}
	}
	return v, nil
}
