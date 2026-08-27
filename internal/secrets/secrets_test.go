package secrets

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func testKey(seed byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = seed + byte(i)
	}
	return k
}

func TestRoundTrip(t *testing.T) {
	c, err := NewCipher(testKey(1))
	if err != nil {
		t.Fatal(err)
	}
	ct, nonce, err := c.Seal("anthropic_api_key", []byte("sk-ant-secret-value-1234567890"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("sk-ant")) {
		t.Fatal("ciphertext leaks plaintext")
	}
	plain, err := c.Open("anthropic_api_key", ct, nonce, c.KeyID())
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "sk-ant-secret-value-1234567890" {
		t.Fatalf("plain = %q", plain)
	}
}

func TestWrongKeyIsAuthFailureNotGarbage(t *testing.T) {
	a, _ := NewCipher(testKey(1))
	b, _ := NewCipher(testKey(2))
	ct, nonce, _ := a.Seal("anthropic_api_key", []byte("sk-ant-secret-value-1234567890"))
	// Same key_id claimed (a forged row): must fail authentication, not
	// decrypt to garbage.
	plain, err := b.Open("anthropic_api_key", ct, nonce, b.KeyID())
	if err == nil || plain != nil {
		t.Fatalf("wrong key must fail: plain=%q err=%v", plain, err)
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestAADMismatchRowSwappedBetweenNames(t *testing.T) {
	c, _ := NewCipher(testKey(1))
	ct, nonce, _ := c.Seal("anthropic_api_key", []byte("sk-ant-secret-value-1234567890"))
	if _, err := c.Open("telegram_bot_token", ct, nonce, c.KeyID()); err == nil {
		t.Fatal("a row swapped between names must fail authentication")
	}
}

func TestKeyIDMismatchReportedNotReturned(t *testing.T) {
	a, _ := NewCipher(testKey(1))
	b, _ := NewCipher(testKey(2))
	ct, nonce, _ := a.Seal("anthropic_api_key", []byte("sk-ant-secret-value-1234567890"))
	_, err := b.Open("anthropic_api_key", ct, nonce, a.KeyID())
	var km *KeyMismatchError
	if !errors.As(err, &km) {
		t.Fatalf("want KeyMismatchError, got %v", err)
	}
	if km.Row != a.KeyID() || km.Process != b.KeyID() {
		t.Fatalf("mismatch = %+v", km)
	}
	if !strings.Contains(err.Error(), "encrypted under key "+a.KeyID()) || !strings.Contains(err.Error(), "this process holds "+b.KeyID()) {
		t.Fatalf("message = %q", err.Error())
	}
	if len(a.KeyID()) != 8 {
		t.Fatalf("key id = %q, want 8 hex chars", a.KeyID())
	}
}

func TestNonceUniqueOver1000Writes(t *testing.T) {
	c, _ := NewCipher(testKey(1))
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		_, nonce, err := c.Seal("anthropic_api_key", []byte("sk-ant-secret-value-1234567890"))
		if err != nil {
			t.Fatal(err)
		}
		if len(nonce) != 12 {
			t.Fatalf("nonce len = %d", len(nonce))
		}
		if seen[string(nonce)] {
			t.Fatal("nonce reused")
		}
		seen[string(nonce)] = true
	}
}

func TestKeyFromEnvErrors(t *testing.T) {
	t.Setenv(EnvKey, "")
	if _, err := KeyFromEnv(); !errors.Is(err, ErrNoKey) {
		t.Fatalf("unset: %v", err)
	}
	t.Setenv(EnvKey, "not base64!!")
	if _, err := KeyFromEnv(); !errors.Is(err, ErrBadKey) {
		t.Fatalf("bad base64: %v", err)
	}
	t.Setenv(EnvKey, base64.StdEncoding.EncodeToString([]byte("short")))
	_, err := KeyFromEnv()
	if !errors.Is(err, ErrBadKey) || !strings.Contains(err.Error(), "5 bytes") {
		t.Fatalf("short key must name the actual length: %v", err)
	}
	t.Setenv(EnvKey, base64.StdEncoding.EncodeToString(testKey(9)))
	k, err := KeyFromEnv()
	if err != nil || len(k) != 32 {
		t.Fatalf("good key: %v", err)
	}
}

func TestChainPrecedenceAndSource(t *testing.T) {
	ctx := context.Background()
	v, _ := NewVault(NewMemoryStore(), testKey(1))
	env := Env{"anthropic_api_key": "env-anthropic-key-value-1234", "telegram_bot_token": "env-telegram-token-value-1234"}
	m := NewManager(v, "", env)

	val, src, ok := m.Get(ctx, "anthropic_api_key")
	if !ok || src != "env" || val != env["anthropic_api_key"] {
		t.Fatalf("env fallback: %q %q %v", val, src, ok)
	}
	if _, err := m.Put(ctx, "anthropic_api_key", []byte("vault-anthropic-key-value-1234"), "u_1"); err != nil {
		t.Fatal(err)
	}
	val, src, ok = m.Get(ctx, "anthropic_api_key")
	if !ok || src != "vault" || val != "vault-anthropic-key-value-1234" {
		t.Fatalf("vault wins: %q %q %v", val, src, ok)
	}
	// Telegram untouched → still env.
	if _, src, _ := m.Get(ctx, "telegram_bot_token"); src != "env" {
		t.Fatalf("telegram source = %q", src)
	}
	// Delete → back to env.
	if _, err := m.Delete(ctx, "anthropic_api_key"); err != nil {
		t.Fatal(err)
	}
	if _, src, _ := m.Get(ctx, "anthropic_api_key"); src != "env" {
		t.Fatalf("after delete source = %q", src)
	}
	// Unknown name never resolves and never writes.
	if _, _, ok := m.Get(ctx, "kraken_api_key"); ok {
		t.Fatal("unknown name resolved")
	}
	if _, err := m.Put(ctx, "kraken_api_key", []byte("x"), "u"); !errors.Is(err, ErrUnknownSecret) {
		t.Fatalf("unknown put = %v", err)
	}
	if _, err := m.List(ctx); err != nil {
		t.Fatal(err)
	}
	list := providerEntries(t, m)
	if len(list) != 5 || list[0].Name != "anthropic_api_key" || list[4].Name != "telegram_bot_token" {
		t.Fatalf("list = %+v", list)
	}
	if list[0].Applies != AppliesImmediately || list[4].Applies != AppliesProcessRestart {
		t.Fatalf("applies = %q / %q", list[0].Applies, list[4].Applies)
	}
}

func TestStatusReportsKeyMismatchAsUnreadable(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	old, _ := NewVault(st, testKey(1))
	if err := old.Put(ctx, "telegram_bot_token", []byte("old-key-telegram-token-value-1"), "u_1", time.Now()); err != nil {
		t.Fatal(err)
	}
	cur, _ := NewVault(st, testKey(2))
	m := NewManager(cur, "", Env{"telegram_bot_token": "env-telegram-token-value-1234"})
	list, _ := m.List(ctx)
	var tg Info
	for _, in := range list {
		if in.Name == "telegram_bot_token" {
			tg = in
		}
	}
	if !tg.Present || tg.Readable || tg.Source != "vault" || !strings.Contains(tg.Reason, "encrypted under key "+old.KeyID()) {
		t.Fatalf("status = %+v", tg)
	}
	if tg.UpdatedBy != "u_1" || tg.UpdatedAt == nil {
		t.Fatalf("provenance lost: %+v", tg)
	}
	// Resolution falls through to env rather than failing.
	if _, src, ok := m.Get(ctx, "telegram_bot_token"); !ok || src != "env" {
		t.Fatalf("unreadable row must fall back to env: %q %v", src, ok)
	}
}

func TestClosedVaultStillServesEnv(t *testing.T) {
	ctx := context.Background()
	m := NewManager(nil, "ARB_SECRET_KEY unset", Env{"anthropic_api_key": "env-anthropic-key-value-1234"})
	if m.Configured() || m.KeyID() != "" {
		t.Fatal("closed vault must not report configured")
	}
	if _, src, ok := m.Get(ctx, "anthropic_api_key"); !ok || src != "env" {
		t.Fatalf("env must still resolve: %q %v", src, ok)
	}
	if _, err := m.Put(ctx, "anthropic_api_key", []byte("vault-anthropic-key-value-1234"), "u"); !errors.Is(err, ErrVaultUnavailable) {
		t.Fatalf("put on closed vault = %v", err)
	}
	if _, err := m.Delete(ctx, "anthropic_api_key"); !errors.Is(err, ErrVaultUnavailable) {
		t.Fatalf("delete on closed vault = %v", err)
	}
	list := providerEntries(t, m)
	if len(list) != 5 || !list[0].Present || list[0].Source != "env" || list[4].Present {
		t.Fatalf("list = %+v", list)
	}
}

func TestValidateValueTrimVsReject(t *testing.T) {
	good := "abcdefghijklmnopqrstuvwxyz"
	cases := []struct {
		name  string
		in    string
		want  string
		valid bool
	}{
		{"plain", good, good, true},
		{"trailing newline trimmed", good + "\n", good, true},
		{"surrounding spaces trimmed", "  " + good + " \t", good, true},
		{"interior space rejected", "abcdefghij klmnopqrstuvwxyz", "", false},
		{"interior newline rejected", "abcdefghij\nklmnopqrstuvwxyz", "", false},
		{"non-ascii rejected", good + "é", "", false},
		{"too short", "short", "", false},
		{"too long", strings.Repeat("a", 4097), "", false},
		{"exactly max", strings.Repeat("a", 4096), strings.Repeat("a", 4096), true},
		{"exactly min", strings.Repeat("a", 20), strings.Repeat("a", 20), true},
	}
	for _, tc := range cases {
		got, err := ValidateValue("anthropic_api_key", []byte(tc.in))
		if tc.valid && (err != nil || string(got) != tc.want) {
			t.Errorf("%s: got %q err %v", tc.name, got, err)
		}
		if !tc.valid && !errors.Is(err, ErrInvalidValue) {
			t.Errorf("%s: want ErrInvalidValue, got %v", tc.name, err)
		}
	}
	if _, err := ValidateValue("exchange_api_key", []byte(good)); !errors.Is(err, ErrUnknownSecret) {
		t.Fatalf("registry must be closed: %v", err)
	}
}

func TestManagerOnChangeFiresWithNameOnly(t *testing.T) {
	ctx := context.Background()
	v, _ := NewVault(NewMemoryStore(), testKey(1))
	m := NewManager(v, "", nil)
	var seen []string
	m.OnChange = func(name string) { seen = append(seen, name) }
	if _, err := m.Put(ctx, "anthropic_api_key", []byte("vault-anthropic-key-value-1234"), "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Put(ctx, "anthropic_api_key", []byte("short"), "u"); err == nil {
		t.Fatal("invalid value must not be stored")
	}
	if _, err := m.Delete(ctx, "anthropic_api_key"); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != "anthropic_api_key" || seen[1] != "anthropic_api_key" {
		t.Fatalf("OnChange calls = %v (a rejected write must not fire)", seen)
	}
}

// TestRegistryIsClosed pins the registry shape: exactly five provider
// entries (the advisory/notification credentials, the two Paddle
// billing secrets from T-083, and the alert e-mail SMTP URL from
// T-086), and every exchange
// entry is unreadable by construction — no env fallback, never consumed,
// refused by Manager.Get even when the vault holds a value.
func TestRegistryIsClosed(t *testing.T) {
	venues := []string{"binance", "okx", "bybit", "bitget", "gate", "mexc"}
	providers, exchanges := 0, 0
	for name, spec := range Known {
		lower := strings.ToLower(name)
		switch spec.Group {
		case GroupProvider:
			providers++
			for _, venue := range append(venues, "exchange") {
				if strings.Contains(lower, venue) {
					t.Fatalf("provider entry %q looks like an exchange credential", name)
				}
			}
			if !IsConsumable(name) {
				t.Fatalf("provider entry %q must be consumable", name)
			}
		case GroupExchange:
			exchanges++
			if spec.Env != "" || spec.Applies != AppliesNotConsumed || spec.Venue == "" || !strings.HasPrefix(name, spec.Venue+"_") {
				t.Fatalf("exchange entry %q = %+v: must have no env fallback, be not_consumed and carry its venue", name, spec)
			}
			if IsConsumable(name) {
				t.Fatalf("exchange entry %q must not be consumable", name)
			}
		default:
			t.Fatalf("entry %q has unknown group %q", name, spec.Group)
		}
	}
	if providers != 5 {
		t.Fatalf("registry has %d provider entries, want exactly 5", providers)
	}
	if exchanges != 14 {
		t.Fatalf("registry has %d exchange entries, want 14", exchanges)
	}

	// A stored exchange credential is listed as present but never resolved.
	v, err := NewVault(NewMemoryStore(), testKey(1))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(v, "", Env{})
	if _, err := m.Put(context.Background(), "binance_api_key", []byte("read-only-key-0123456789"), "admin"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := m.Get(context.Background(), "binance_api_key"); ok {
		t.Fatal("Manager.Get resolved an exchange credential")
	}
	list, err := m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range list {
		if in.Name == "binance_api_key" && (!in.Present || in.Group != GroupExchange || in.Venue != "binance") {
			t.Fatalf("listed exchange credential = %+v", in)
		}
	}
}

// failingStore is a Store whose Get/List fail: the vault must report the
// fault (P3-8) instead of silently resolving as "absent → env".
type failingStore struct{ *MemoryStore }

func (failingStore) Get(context.Context, string) (Row, bool, error) {
	return Row{}, false, errors.New("connection refused")
}

func TestVaultGetStoreErrorIsLoggedNotSilent(t *testing.T) {
	var logs bytes.Buffer
	v, err := NewVault(failingStore{NewMemoryStore()}, testKey(1))
	if err != nil {
		t.Fatal(err)
	}
	v.Log = slog.New(slog.NewTextHandler(&logs, nil))
	m := NewManager(v, "", Env{"anthropic_api_key": "env-anthropic-key-value-000001"})
	val, src, ok := m.Get(context.Background(), "anthropic_api_key")
	if !ok || src != "env" || val != "env-anthropic-key-value-000001" {
		t.Fatalf("chain must still fall through to env: %q %q %v", val, src, ok)
	}
	out := logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "connection refused") || !strings.Contains(out, "name=anthropic_api_key") {
		t.Fatalf("store error not surfaced at WARN: %s", out)
	}
	if strings.Contains(out, "env-anthropic-key-value") {
		t.Fatalf("value leaked into the log: %s", out)
	}
}

// TestManagerListUsesStoreList: the status view comes from one
// Store.List round trip; a store whose Get fails but whose List works
// still lists (and a Put'd row shows as vault-sourced).
func TestManagerListUsesStoreList(t *testing.T) {
	ctx := context.Background()
	mem := NewMemoryStore()
	v, _ := NewVault(mem, testKey(1))
	if err := v.Put(ctx, "telegram_bot_token", []byte("telegram-token-value-12345678"), "u_1", time.Now()); err != nil {
		t.Fatal(err)
	}
	fv, _ := NewVault(failingStore{mem}, testKey(1))
	fv.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	m := NewManager(fv, "", Env{"anthropic_api_key": "env-anthropic-key-value-000001"})
	if _, err := m.List(ctx); err != nil {
		t.Fatalf("List must not go through Get: %v", err)
	}
	list := providerEntries(t, m)
	// Sorted by name: anthropic, paddle_api_key, paddle_webhook_secret,
	// smtp_url, telegram (paddle/smtp entries are absent in this fixture).
	if len(list) != 5 || list[0].Name != "anthropic_api_key" || list[4].Name != "telegram_bot_token" {
		t.Fatalf("list = %+v", list)
	}
	if list[0].Source != "env" || !list[0].Present || list[4].Source != "vault" || !list[4].Readable || list[4].UpdatedBy != "u_1" {
		t.Fatalf("list = %+v", list)
	}
	if list[1].Name != "paddle_api_key" || list[1].Present || list[2].Name != "paddle_webhook_secret" || list[2].Present {
		t.Fatalf("paddle entries = %+v %+v", list[1], list[2])
	}
	if list[3].Name != "smtp_url" || list[3].Present {
		t.Fatalf("smtp entry = %+v", list[3])
	}
}

// providerEntries lists the provider-group rows (the exchange group is
// covered by TestRegistryIsClosed).
func providerEntries(t *testing.T, m *Manager) []Info {
	t.Helper()
	all, err := m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []Info
	for _, in := range all {
		if in.Group == GroupProvider {
			out = append(out, in)
		}
	}
	return out
}
