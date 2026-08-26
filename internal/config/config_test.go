package config

import (
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	b, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if b.Mode != ModeMarketData {
		t.Fatalf("default mode = %s, want MARKET_DATA", b.Mode)
	}
	if b.HTTPAddr == "" {
		t.Fatal("HTTPAddr empty")
	}
}

func TestLoadInvalidMode(t *testing.T) {
	t.Setenv("ARB_MODE", "LIVE") // there is no live mode, by design
	if _, err := Load(); err == nil {
		t.Fatal("want error for invalid mode LIVE")
	}
}

func TestReplayRequiresSession(t *testing.T) {
	t.Setenv("ARB_MODE", "REPLAY")
	if _, err := Load(); err == nil {
		t.Fatal("REPLAY without ARB_REPLAY_SESSION must fail")
	}
	t.Setenv("ARB_REPLAY_SESSION", "sess-1")
	if _, err := Load(); err != nil {
		t.Fatalf("REPLAY with session: %v", err)
	}
}

func TestAllowlistParsing(t *testing.T) {
	t.Setenv("ARB_TELEGRAM_ALLOWLIST", "123, 456,789")
	b, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(b.TelegramAllowlist) != 3 || b.TelegramAllowlist[1] != 456 {
		t.Fatalf("allowlist = %v", b.TelegramAllowlist)
	}
	t.Setenv("ARB_TELEGRAM_ALLOWLIST", "abc")
	if _, err := Load(); err == nil {
		t.Fatal("want error for non-numeric allowlist entry")
	}
}

// Secrets must never survive Redacted() — this test is the tripwire that
// fails when someone adds a secret field without masking it.
func TestRedactedMasksSecrets(t *testing.T) {
	t.Setenv("ARB_DATABASE_URL", "postgres://arb:hunter2@localhost:5432/arb")
	t.Setenv("ARB_TELEGRAM_TOKEN", "12345:token-secret")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret")
	b, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	r := b.Redacted()
	for _, secret := range []string{"hunter2", "token-secret", "sk-ant-secret"} {
		if strings.Contains(r.DatabaseURL+r.TelegramToken+r.AnthropicAPIKey, secret) {
			t.Fatalf("secret %q leaked through Redacted()", secret)
		}
	}
	if !strings.Contains(r.DatabaseURL, "localhost:5432") {
		t.Fatalf("redacted DSN should keep host part, got %q", r.DatabaseURL)
	}
}
