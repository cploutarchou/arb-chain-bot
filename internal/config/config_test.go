package config

import (
	"reflect"
	"regexp"
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
	t.Setenv("ARB_ADMIN_PASSWORD", "admin-pw-secret")
	b, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	r := b.Redacted()
	for _, secret := range []string{"hunter2", "token-secret", "sk-ant-secret", "admin-pw-secret"} {
		if strings.Contains(r.DatabaseURL+r.TelegramToken+r.AnthropicAPIKey+r.AdminPassword, secret) {
			t.Fatalf("secret %q leaked through Redacted()", secret)
		}
	}
	if !strings.Contains(r.DatabaseURL, "localhost:5432") {
		t.Fatalf("redacted DSN should keep host part, got %q", r.DatabaseURL)
	}
}

// Reflective tripwire (audit S-009): every string field whose name looks
// like a credential must come back changed from Redacted() when set to a
// sentinel. Catches NEW secret fields that the explicit test above does
// not yet know about.
func TestRedactedMasksSecretLookingFieldsReflectively(t *testing.T) {
	secretName := regexp.MustCompile(`(?i)(password|token|secret|apikey|api_key|credential)`)
	var b Bootstrap
	v := reflect.ValueOf(&b).Elem()
	tp := v.Type()
	var checked []string
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		if f.Type.Kind() != reflect.String || !secretName.MatchString(f.Name) {
			continue
		}
		v.Field(i).SetString("sentinel-" + f.Name)
		checked = append(checked, f.Name)
	}
	if len(checked) == 0 {
		t.Fatal("no secret-looking fields found; the reflective tripwire is miswired")
	}
	r := reflect.ValueOf(b.Redacted())
	for _, name := range checked {
		got := r.FieldByName(name).String()
		if strings.Contains(got, "sentinel-") {
			t.Errorf("field %s survived Redacted() unmasked: %q", name, got)
		}
	}
}
