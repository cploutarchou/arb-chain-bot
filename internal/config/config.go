// Package config holds the static bootstrap configuration loaded from the
// environment. Dynamic strategy configuration (thresholds, limits, filters)
// is DB-backed and versioned; it lives in the config service, not here.
package config

import (
	"fmt"
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
	AIModel         string
}

// Load reads Bootstrap from the environment. Missing optional values get
// safe defaults; invalid values return an error rather than a guess.
func Load() (Bootstrap, error) {
	b := Bootstrap{
		Mode:            Mode(getenv("ARB_MODE", string(ModeMarketData))),
		HTTPAddr:        getenv("ARB_HTTP_ADDR", ":8080"),
		MetricsAddr:     getenv("ARB_METRICS_ADDR", ""),
		DatabaseURL:     os.Getenv("ARB_DATABASE_URL"),
		LogLevel:        getenv("ARB_LOG_LEVEL", "info"),
		ShutdownGrace:   15 * time.Second,
		RecordingDir:    getenv("ARB_RECORDING_DIR", "recordings"),
		ReplaySession:   os.Getenv("ARB_REPLAY_SESSION"),
		Symbols:         splitList(getenv("ARB_SYMBOLS", "BTCUSDT,ETHUSDT,ETHBTC,BTCUSDC,ETHUSDC,USDCUSDT")),
		StartingAssets:  splitList(getenv("ARB_STARTING_ASSETS", "USDT,USDC")),
		PaperBalance:    getenv("ARB_PAPER_BALANCE", "10000"),
		TelegramToken:   os.Getenv("ARB_TELEGRAM_TOKEN"),
		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
		AIModel:         getenv("ARB_AI_MODEL", "claude-sonnet-5"),
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
	if b.Mode.UsesRecordedClock() && b.ReplaySession == "" {
		return Bootstrap{}, fmt.Errorf("config: mode %s requires ARB_REPLAY_SESSION", b.Mode)
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
	if c.AnthropicAPIKey != "" {
		c.AnthropicAPIKey = "***"
	}
	return c
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
