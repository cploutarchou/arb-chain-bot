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
	if _, _, ok := m.Get(ctx, "binance_api_key"); ok {
		t.Fatal("unknown name resolved")
	}
	if _, err := m.Put(ctx, "binance_api_key", []byte("x"), "u"); !errors.Is(err, ErrUnknownSecret) {
		t.Fatalf("unknown put = %v", err)
	}
	list, err := m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "anthropic_api_key" || list[1].Name != "telegram_bot_token" {
		t.Fatalf("list = %+v", list)
	}
	if list[0].Applies != AppliesImmediately || list[1].Applies != AppliesProcessRestart {
		t.Fatalf("applies = %q / %q", list[0].Applies, list[1].Applies)
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
	list, _ := m.List(ctx)
	if len(list) != 2 || !list[0].Present || list[0].Source != "env" || list[1].Present {
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

// TestRegistryIsClosed pins the two-entry registry: any growth is a
// reviewed code change, and nothing in it may look like an exchange key.
func TestRegistryIsClosed(t *testing.T) {
	if len(Known) != 2 {
		t.Fatalf("registry has %d entries, want exactly 2", len(Known))
	}
	for name := range Known {
		lower := strings.ToLower(name)
		for _, venue := range []string{"binance", "okx", "bybit", "bitget", "gate", "mexc", "exchange"} {
			if strings.Contains(lower, venue) {
				t.Fatalf("registry entry %q looks like an exchange credential", name)
			}
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
	list, err := m.List(ctx)
	if err != nil {
		t.Fatalf("List must not go through Get: %v", err)
	}
	if len(list) != 2 || list[0].Name != "anthropic_api_key" || list[1].Name != "telegram_bot_token" {
		t.Fatalf("list = %+v", list)
	}
	if list[0].Source != "env" || !list[0].Present || list[1].Source != "vault" || !list[1].Readable || list[1].UpdatedBy != "u_1" {
		t.Fatalf("list = %+v", list)
	}
}
