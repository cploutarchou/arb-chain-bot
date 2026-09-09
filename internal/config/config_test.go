package config

import (
	"net/netip"
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

func TestTrustedProxiesParsing(t *testing.T) {
	t.Setenv("ARB_TRUSTED_PROXIES", "10.0.0.0/8, 192.168.1.5")
	b, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(b.TrustedProxies) != 2 {
		t.Fatalf("TrustedProxies = %v", b.TrustedProxies)
	}
	if !b.TrustedProxies[0].Contains(mustAddr(t, "10.1.2.3")) {
		t.Fatalf("CIDR entry did not parse as expected: %v", b.TrustedProxies[0])
	}
	// A bare address is a single-host prefix, not "match everything".
	if !b.TrustedProxies[1].Contains(mustAddr(t, "192.168.1.5")) {
		t.Fatalf("bare address entry should contain itself: %v", b.TrustedProxies[1])
	}
	if b.TrustedProxies[1].Contains(mustAddr(t, "192.168.1.6")) {
		t.Fatalf("bare address entry must not widen to a subnet: %v", b.TrustedProxies[1])
	}
	t.Setenv("ARB_TRUSTED_PROXIES", "not-an-address")
	if _, err := Load(); err == nil {
		t.Fatal("want error for an unparsable ARB_TRUSTED_PROXIES entry")
	}
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// Acceptance (audit S6/P1-14): a bootstrap password shorter than the
// platform's own minimum, or equal to the documented .env.example
// placeholder, must refuse to boot rather than mint a guessable
// administrator credential.
func TestAdminPasswordValidation(t *testing.T) {
	t.Setenv("ARB_ADMIN_EMAIL", "admin@example.test")

	t.Setenv("ARB_ADMIN_PASSWORD", "short12345") // 10 chars, below the minimum
	if _, err := Load(); err == nil {
		t.Fatal("want error for a bootstrap password shorter than 12 characters")
	}

	t.Setenv("ARB_ADMIN_PASSWORD", "change-me-local-dev-only")
	if _, err := Load(); err == nil {
		t.Fatal("want error when ARB_ADMIN_PASSWORD is the documented example value")
	}

	t.Setenv("ARB_ADMIN_PASSWORD", "a-unique-strong-password-99")
	if _, err := Load(); err != nil {
		t.Fatalf("Load with a strong password: %v", err)
	}

	// Unset stays fine — bootstrap is simply skipped elsewhere.
	t.Setenv("ARB_ADMIN_PASSWORD", "")
	if _, err := Load(); err != nil {
		t.Fatalf("Load with no admin password: %v", err)
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
