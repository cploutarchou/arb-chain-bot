// Package config holds the static bootstrap configuration loaded from the
// environment. Dynamic strategy configuration (thresholds, limits, filters)
// is DB-backed and versioned; it lives in the config service, not here.
package config

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

// Mode is the platform operating mode. Exactly one mode is active per run;
// it selects the time source, the executor implementation, and which
// components start.
type Mode string

const (
	ModeMarketData Mode = "MARKET_DATA"
	ModeRecord     Mode = "RECORD"
	ModeReplay     Mode = "REPLAY"
	ModeBacktest   Mode = "BACKTEST"
	ModePaper      Mode = "PAPER"
	ModeShadow     Mode = "SHADOW"
)

func (m Mode) Valid() bool {
	switch m {
	case ModeMarketData, ModeRecord, ModeReplay, ModeBacktest, ModePaper, ModeShadow:
		return true
	}
	return false
}

// UsesRecordedClock reports whether the mode is driven by recorded time
// rather than the wall clock (determinism requirement, SKILL.md §64).
func (m Mode) UsesRecordedClock() bool { return m == ModeReplay || m == ModeBacktest }

// Bootstrap is the process-level configuration. Values that are secrets
// (DB password inside the DSN, API keys, bot tokens) come only from the
// environment or a secret manager; they are never persisted or logged.
type Bootstrap struct {
	Mode Mode

	HTTPAddr      string // API listen address
	MetricsAddr   string // Prometheus exporter address ("" = same mux)
	DatabaseURL   string // pgx DSN; empty disables persistence (dev scanner-only)
	LogLevel      string // debug|info|warn|error
	ShutdownGrace time.Duration

	// MetadataCheckInterval re-fetches the venue's exchangeInfo and
	// diffs it against the running topology/instrument rules (audit T8);
	// a material change opens the operator-closed metadata_changed
	// breaker. Zero or negative disables the monitor.
	MetadataCheckInterval time.Duration

	// Recording / replay
	RecordingDir  string
	ReplaySession string // recording session id for REPLAY/BACKTEST
	Seed          int64  // RNG seed for stochastic simulation (0 = derive and log)

	// Market scope (first exchange: Binance per research)
	Symbols        []string // spot symbols to subscribe (e.g. BTCUSDT,ETHUSDT,ETHBTC)
	StartingAssets []string // triangle starting assets
	PaperBalance   string   // starting virtual balance per starting asset (decimal string)

	// Telegram (optional; empty token disables the bot)
	TelegramToken     string
	TelegramAllowlist []int64 // Telegram user IDs

	// AI advisor (optional; empty key disables the advisor)
	AnthropicAPIKey string
	// Paddle Billing (T-083). The API key and webhook secret are env
	// fallbacks for the secrets vault (registry: paddle_api_key,
	// paddle_webhook_secret); the client token is public (Paddle.js) and
	// PaddleEnv is "sandbox" (default) or "production".
	PaddleAPIKey        string
	PaddleWebhookSecret string
	PaddleClientToken   string
	PaddleEnv           string
	AIModel             string
	// AIProvider selects the implementation: "anthropic" (default when
	// the key is set), "fake" (deterministic; dev/tests), "" = disabled.
	AIProvider string

	// Console origin allowed for cross-origin WS in dev; auth bootstrap
	// credentials (dev-only convenience — production users live in the DB).
	AllowedOrigin string
	AdminEmail    string
	AdminPassword string

	// TrustedProxies gates when the API server may honour
	// X-Forwarded-For/X-Real-IP for login throttling and audit forensics
	// (audit S1/P1-10): behind the documented reverse proxy, every
	// request's TCP peer is the proxy itself, so those headers are
	// consulted ONLY when the immediate peer address falls inside one of
	// these CIDRs. Empty (the default) means no proxy is trusted — the
	// resolved address is always the raw TCP peer, which is safe (if
	// wrong behind an unconfigured proxy) rather than spoofable.
	TrustedProxies []netip.Prefix
}

// DefaultMetadataCheckInterval is the venue-metadata monitor's cadence
// (audit T8); see Bootstrap.MetadataCheckInterval.
const DefaultMetadataCheckInterval = time.Hour

// Load reads Bootstrap from the environment. Missing optional values get
// safe defaults; invalid values return an error rather than a guess.
func Load() (Bootstrap, error) {
	b := Bootstrap{
		Mode:                Mode(getenv("ARB_MODE", string(ModeMarketData))),
		HTTPAddr:            getenv("ARB_HTTP_ADDR", ":8080"),
		MetricsAddr:         getenv("ARB_METRICS_ADDR", ""),
		DatabaseURL:         os.Getenv("ARB_DATABASE_URL"),
		LogLevel:            getenv("ARB_LOG_LEVEL", "info"),
		ShutdownGrace:       15 * time.Second,
		RecordingDir:        getenv("ARB_RECORDING_DIR", "recordings"),
		ReplaySession:       os.Getenv("ARB_REPLAY_SESSION"),
		Symbols:             splitList(getenv("ARB_SYMBOLS", "BTCUSDT,ETHUSDT,ETHBTC,BTCUSDC,ETHUSDC,USDCUSDT")),
		StartingAssets:      splitList(getenv("ARB_STARTING_ASSETS", "USDT,USDC")),
		PaperBalance:        getenv("ARB_PAPER_BALANCE", "10000"),
		TelegramToken:       os.Getenv("ARB_TELEGRAM_TOKEN"),
		AnthropicAPIKey:     os.Getenv("ANTHROPIC_API_KEY"),
		PaddleAPIKey:        os.Getenv("PADDLE_API_KEY"),
		PaddleWebhookSecret: os.Getenv("PADDLE_WEBHOOK_SECRET"),
		PaddleClientToken:   os.Getenv("PADDLE_CLIENT_TOKEN"),
		PaddleEnv:           os.Getenv("PADDLE_ENV"),
		AIModel:             getenv("ARB_AI_MODEL", "claude-sonnet-5"),
		AIProvider:          os.Getenv("ARB_AI_PROVIDER"),
		AllowedOrigin:       getenv("ARB_ALLOWED_ORIGIN", "http://localhost:3000"),
		AdminEmail:          os.Getenv("ARB_ADMIN_EMAIL"),
		AdminPassword:       os.Getenv("ARB_ADMIN_PASSWORD"),
	}
	if !b.Mode.Valid() {
		return Bootstrap{}, fmt.Errorf("config: invalid ARB_MODE %q", b.Mode)
	}
	if v := os.Getenv("ARB_SHUTDOWN_GRACE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Bootstrap{}, fmt.Errorf("config: invalid ARB_SHUTDOWN_GRACE: %w", err)
		}
		b.ShutdownGrace = d
	}
	// T8: default on (hourly); "0" disables the venue-metadata monitor.
	b.MetadataCheckInterval = DefaultMetadataCheckInterval
	if v := os.Getenv("ARB_METADATA_CHECK_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Bootstrap{}, fmt.Errorf("config: invalid ARB_METADATA_CHECK_INTERVAL: %w", err)
		}
		b.MetadataCheckInterval = d
	}
	if v := os.Getenv("ARB_SEED"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return Bootstrap{}, fmt.Errorf("config: invalid ARB_SEED: %w", err)
		}
		b.Seed = n
	}
	if v := os.Getenv("ARB_TELEGRAM_ALLOWLIST"); v != "" {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := strconv.ParseInt(part, 10, 64)
			if err != nil {
				return Bootstrap{}, fmt.Errorf("config: invalid ARB_TELEGRAM_ALLOWLIST entry %q: %w", part, err)
			}
			b.TelegramAllowlist = append(b.TelegramAllowlist, id)
		}
	}
	if v := os.Getenv("ARB_TRUSTED_PROXIES"); v != "" {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			prefix, err := parseTrustedProxy(part)
			if err != nil {
				return Bootstrap{}, fmt.Errorf("config: invalid ARB_TRUSTED_PROXIES entry %q: %w", part, err)
			}
			b.TrustedProxies = append(b.TrustedProxies, prefix)
		}
	}
	if b.Mode.UsesRecordedClock() && b.ReplaySession == "" {
		return Bootstrap{}, fmt.Errorf("config: mode %s requires ARB_REPLAY_SESSION", b.Mode)
	}
	if err := validateAdminPassword(b.AdminPassword); err != nil {
		return Bootstrap{}, err
	}
	return b, nil
}

// Redacted returns a copy safe for logging: secrets are masked, never
// printed. Adding a secret field to Bootstrap requires masking it here;
// the config test enforces the invariant.
func (b Bootstrap) Redacted() Bootstrap {
	c := b
	c.DatabaseURL = maskDSN(c.DatabaseURL)
	if c.TelegramToken != "" {
		c.TelegramToken = "***"
	}
	if c.PaddleAPIKey != "" {
		c.PaddleAPIKey = "***"
	}
	if c.PaddleWebhookSecret != "" {
		c.PaddleWebhookSecret = "***"
	}
	// Public by design (Paddle.js), but masked anyway: the redaction
	// test treats every *Token field as secret-looking and nothing
	// needs it in a log line.
	if c.PaddleClientToken != "" {
		c.PaddleClientToken = "***"
	}
	if c.AnthropicAPIKey != "" {
		c.AnthropicAPIKey = "***"
	}
	if c.AdminPassword != "" {
		c.AdminPassword = "***"
	}
	return c
}

// parseTrustedProxy accepts either a CIDR ("10.0.0.0/8") or a bare
// address ("10.0.0.5", treated as a single-host /32 or /128) — an
// operator listing one load balancer address should not have to spell
// out a host prefix.
func parseTrustedProxy(s string) (netip.Prefix, error) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p, nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// minAdminPasswordLength mirrors auth.MinPasswordLength. Kept as a
// local constant (rather than importing internal/auth) so this
// foundational package — read before anything else at process start —
// stays free of dependencies on the layers built on top of it.
const minAdminPasswordLength = 12

// defaultAdminPassword is the placeholder shipped in .env.example and
// scripts/create-secret.sh. Booting with it configured almost always
// means an operator copied the example file verbatim rather than
// choosing a credential (audit S6/P1-14); a publicly documented
// password is not a secret, so this refuses rather than mints a real
// administrator account with it.
const defaultAdminPassword = "change-me-local-dev-only"

// validateAdminPassword refuses to boot with a bootstrap admin password
// that is too short or is the documented example value — both are
// "the operator never actually set a password" in practice, and the
// consequence (a standing, widely-guessable administrator credential)
// is worse than refusing to start. An unset password is fine: bootstrap
// is then simply skipped (buildAuth logs and leaves login unavailable
// until an account exists some other way).
func validateAdminPassword(password string) error {
	if password == "" {
		return nil
	}
	if len(password) < minAdminPasswordLength {
		return fmt.Errorf("config: ARB_ADMIN_PASSWORD must be at least %d characters", minAdminPasswordLength)
	}
	if password == defaultAdminPassword {
		return fmt.Errorf("config: ARB_ADMIN_PASSWORD must not be the documented example value; set a unique credential")
	}
	return nil
}

func maskDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	// postgres://user:password@host/db → mask the credential section.
	if at := strings.Index(dsn, "@"); at > 0 {
		if scheme := strings.Index(dsn, "://"); scheme > 0 && scheme+3 < at {
			return dsn[:scheme+3] + "***" + dsn[at:]
		}
	}
	return "***"
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func splitList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, strings.ToUpper(p))
		}
	}
	return out
}
