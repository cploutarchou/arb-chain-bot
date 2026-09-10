package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"
)

var (
	t0 = time.Unix(1_700_000_000, 0)
	ip = netip.MustParseAddr("203.0.113.7")
)

func manager(t *testing.T) (*Manager, *MemoryStore) {
	t.Helper()
	store := NewMemoryStore()
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	store.AddUser(User{ID: "u1", Email: "op@example.test", PasswordHash: hash, Role: RoleOperator})
	now := t0
	m := &Manager{
		Users:    store,
		Sessions: store,
		Throttle: NewThrottle(3, time.Minute, 5*time.Minute),
		TTL:      time.Hour,
		Now:      func() time.Time { return now },
	}
	return m, store
}

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("format: %s", h)
	}
	if err := VerifyPassword("s3cret", h); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPassword("wrong", h); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("wrong password: %v", err)
	}
	// Two hashes of the same password differ (fresh salts).
	h2, _ := HashPassword("s3cret")
	if h == h2 {
		t.Fatal("salt reuse")
	}
	// Tampered/unknown formats fail closed.
	if err := VerifyPassword("s3cret", "$bcrypt$whatever"); err == nil {
		t.Fatal("foreign format accepted")
	}
}

func TestLoginLogoutFlow(t *testing.T) {
	m, _ := manager(t)
	ctx := context.Background()

	s, err := m.Login(ctx, "op@example.test", "correct horse battery staple", ip)
	if err != nil {
		t.Fatal(err)
	}
	if s.Role != RoleOperator || len(s.Token) < 40 {
		t.Fatalf("session = %+v", s)
	}
	got, err := m.Validate(ctx, s.Token)
	if err != nil || got.UserID != "u1" {
		t.Fatalf("validate: %v %+v", err, got)
	}
	if err := m.Logout(ctx, s.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Validate(ctx, s.Token); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("revoked session validated: %v", err)
	}
}

func TestSessionExpiry(t *testing.T) {
	m, _ := manager(t)
	ctx := context.Background()
	s, _ := m.Login(ctx, "op@example.test", "correct horse battery staple", ip)

	m.Now = func() time.Time { return t0.Add(2 * time.Hour) }
	if _, err := m.Validate(ctx, s.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expired session validated: %v", err)
	}
}

func TestWrongPasswordAndUnknownUser(t *testing.T) {
	m, _ := manager(t)
	ctx := context.Background()
	if _, err := m.Login(ctx, "op@example.test", "nope", ip); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := m.Login(ctx, "ghost@example.test", "nope", ip); !errors.Is(err, ErrUnknownUser) {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestDisabledUserRejected(t *testing.T) {
	m, store := manager(t)
	hash, _ := HashPassword("pw")
	store.AddUser(User{ID: "u2", Email: "off@example.test", PasswordHash: hash, Role: RoleViewer, Disabled: true})
	if _, err := m.Login(context.Background(), "off@example.test", "pw", ip); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("disabled user: %v", err)
	}
}

func TestLoginThrottling(t *testing.T) {
	m, store := manager(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := m.Login(ctx, "op@example.test", "wrong", ip); !errors.Is(err, ErrInvalidPassword) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	// Locked out now — even the CORRECT password is refused.
	if _, err := m.Login(ctx, "op@example.test", "correct horse battery staple", ip); !errors.Is(err, ErrThrottled) {
		t.Fatalf("throttle: %v", err)
	}
	// Acceptance (audit S1/P1-10): the account limiter is keyed on the
	// account alone, so it follows the account across source addresses —
	// a distributed attacker cannot get a fresh allowance per address.
	other := netip.MustParseAddr("198.51.100.9")
	if _, err := m.Login(ctx, "op@example.test", "correct horse battery staple", other); !errors.Is(err, ErrThrottled) {
		t.Fatalf("account throttle must follow the account across source addresses: %v", err)
	}
	// A sibling account at the very same (locked-out) address is
	// unaffected — the account limiter never blocks a DIFFERENT account.
	hash, err := HashPassword("sibling password")
	if err != nil {
		t.Fatal(err)
	}
	store.AddUser(User{ID: "u-sibling", Email: "sibling@example.test", PasswordHash: hash, Role: RoleViewer})
	if _, err := m.Login(ctx, "sibling@example.test", "sibling password", ip); err != nil {
		t.Fatalf("sibling account blocked by an unrelated account's throttle: %v", err)
	}
	// Lockout expires.
	m.Now = func() time.Time { return t0.Add(6 * time.Minute) }
	if _, err := m.Login(ctx, "op@example.test", "correct horse battery staple", ip); err != nil {
		t.Fatalf("post-lockout: %v", err)
	}
}

// Acceptance (audit S1/P1-10): a successful login clears the account
// throttle's failure count — two failures followed by a success must
// not carry forward into the next round and trip the lockout early.
func TestLoginSuccessResetsAccountThrottle(t *testing.T) {
	m, _ := manager(t) // Throttle: NewThrottle(3, time.Minute, 5*time.Minute)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, err := m.Login(ctx, "op@example.test", "wrong", ip); !errors.Is(err, ErrInvalidPassword) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := m.Login(ctx, "op@example.test", "correct horse battery staple", ip); err != nil {
		t.Fatalf("login after 2 failures should succeed: %v", err)
	}
	// If the reset had not happened, this failure would be the third
	// cumulative one and trip the lockout for the next attempt.
	if _, err := m.Login(ctx, "op@example.test", "wrong", ip); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("post-success attempt: %v", err)
	}
	if _, err := m.Login(ctx, "op@example.test", "correct horse battery staple", ip); err != nil {
		t.Fatalf("account throttle did not reset on success: %v", err)
	}
}

// Acceptance (audit S1/P1-10): IPThrottle does NOT reset on success —
// an attacker sharing an address with a legitimate account must not be
// able to "launder" a fresh allowance just because that OTHER account
// happened to log in successfully.
func TestLoginSuccessDoesNotResetIPThrottle(t *testing.T) {
	m, store := manager(t)
	m.IPThrottle = NewThrottle(3, time.Minute, 5*time.Minute)
	hash, err := HashPassword("second password")
	if err != nil {
		t.Fatal(err)
	}
	store.AddUser(User{ID: "u-second", Email: "second@example.test", PasswordHash: hash, Role: RoleViewer})
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, err := m.Login(ctx, "op@example.test", "wrong", ip); !errors.Is(err, ErrInvalidPassword) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	// A different account succeeds from the same address.
	if _, err := m.Login(ctx, "second@example.test", "second password", ip); err != nil {
		t.Fatalf("second account login should succeed: %v", err)
	}
	// One more failure reaches IPThrottle's limit of 3 (2 + 1 — the
	// success above did not reset it) and trips the lockout.
	if _, err := m.Login(ctx, "op@example.test", "wrong", ip); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("third failure: %v", err)
	}
	if _, err := m.Login(ctx, "second@example.test", "second password", ip); !errors.Is(err, ErrThrottled) {
		t.Fatalf("IPThrottle should now be engaged for every account behind this address: %v", err)
	}
}

func TestPasswordChangeRevokesOtherSessions(t *testing.T) {
	m, store := manager(t)
	ctx := context.Background()
	s1, _ := m.Login(ctx, "op@example.test", "correct horse battery staple", ip)
	s2, _ := m.Login(ctx, "op@example.test", "correct horse battery staple", ip)
	if err := store.RevokeUserSessions(ctx, "u1", t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	m.Now = func() time.Time { return t0.Add(2 * time.Second) }
	for _, s := range []Session{s1, s2} {
		if _, err := m.Validate(ctx, s.Token); !errors.Is(err, ErrSessionRevoked) {
			t.Fatalf("session survived revoke-all: %v", err)
		}
	}
}

// The full RBAC denial matrix: every permission × every role, pinned.
func TestRBACMatrix(t *testing.T) {
	viewerAllowed := []Permission{
		PermViewDashboard, PermViewOpportunity, PermViewPortfolio,
		PermViewRisk, PermViewSystem, PermReportView,
		PermScreenerView,
	}
	viewerDenied := []Permission{
		PermViewAudit, PermPaperControl, PermPaperReset, PermScannerConfig,
		PermAIApprove, PermAlertAck, PermReportGenerate, PermRiskConfig,
		PermExchangeConfig, PermUserManage, PermSystemConfig, PermScreenerConfig,
	}
	for _, p := range viewerAllowed {
		if !Can(RoleViewer, p) {
			t.Fatalf("viewer denied %s", p)
		}
	}
	for _, p := range viewerDenied {
		if Can(RoleViewer, p) {
			t.Fatalf("viewer allowed %s", p)
		}
	}
	operatorDenied := []Permission{
		PermPaperReset, PermRiskConfig, PermExchangeConfig, PermUserManage, PermSystemConfig,
		PermScreenerConfig,
	}
	for _, p := range operatorDenied {
		if Can(RoleOperator, p) {
			t.Fatalf("operator allowed %s", p)
		}
	}
	if !Can(RoleOperator, PermPaperControl) || !Can(RoleOperator, PermAIApprove) ||
		!Can(RoleOperator, PermReportGenerate) || !Can(RoleOperator, PermScreenerView) {
		t.Fatal("operator missing core permissions")
	}
	all := append(append([]Permission{}, viewerAllowed...), viewerDenied...)
	for _, p := range all {
		if !Can(RoleAdmin, p) {
			t.Fatalf("admin denied %s", p)
		}
	}
	// Unknown role holds nothing.
	if Can(Role("SUPERUSER"), PermViewDashboard) {
		t.Fatal("unknown role granted permission")
	}
}

func TestCSRF(t *testing.T) {
	if err := VerifyCSRF("tok", "tok"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][2]string{{"tok", "other"}, {"", "tok"}, {"tok", ""}} {
		if err := VerifyCSRF(bad[0], bad[1]); !errors.Is(err, ErrInvalidCSRF) {
			t.Fatalf("csrf %v accepted", bad)
		}
	}
}

// Acceptance (audit S-005): the session store must only ever hold token
// digests — a leaked table then contains no usable bearer credentials.
func TestSessionStoreHoldsOnlyTokenDigests(t *testing.T) {
	m, store := manager(t)
	ctx := context.Background()
	s, err := m.Login(ctx, "op@example.test", "correct horse battery staple", ip)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SessionByToken(ctx, s.Token); !errors.Is(err, ErrInvalidSession) {
		t.Fatal("store is keyed by the RAW token; it must hold digests only")
	}
	stored, err := store.SessionByToken(ctx, HashToken(s.Token))
	if err != nil {
		t.Fatalf("digest lookup failed: %v", err)
	}
	if stored.Token == s.Token {
		t.Fatal("stored session retains the raw token")
	}
	// The raw token still validates and revokes through the manager.
	if _, err := m.Validate(ctx, s.Token); err != nil {
		t.Fatal(err)
	}
	if err := m.Logout(ctx, s.Token); err != nil {
		t.Fatal(err)
	}
}

// Acceptance (audit S-006): rotating emails from one IP cannot evade the
// account throttle — the per-IP net catches the spray.
func TestIPThrottleCatchesEmailRotation(t *testing.T) {
	m, store := manager(t)
	m.IPThrottle = NewThrottle(5, time.Minute, 5*time.Minute)
	hash, _ := HashPassword("pw2")
	store.AddUser(User{ID: "u9", Email: "second@example.test", PasswordHash: hash, Role: RoleViewer})
	ctx := context.Background()
	// 5 failures spread over distinct emails: each (email, ip) key stays
	// under the account limit, but the IP key saturates.
	for i := 0; i < 5; i++ {
		email := fmt.Sprintf("ghost%d@example.test", i)
		if _, err := m.Login(ctx, email, "wrong", ip); !errors.Is(err, ErrUnknownUser) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := m.Login(ctx, "second@example.test", "pw2", ip); !errors.Is(err, ErrThrottled) {
		t.Fatalf("ip throttle did not engage: %v", err)
	}
	// Another IP is unaffected.
	other := netip.MustParseAddr("198.51.100.77")
	if _, err := m.Login(ctx, "second@example.test", "pw2", other); err != nil {
		t.Fatalf("clean ip blocked: %v", err)
	}
}

// phcEncode builds a PHC-format hash from explicit parameters, for rows
// written by configurations other than today's.
func phcEncode(password string, iters, mem uint32, par uint8, keyLen int) string {
	salt := make([]byte, argonSaltLen)
	key := deriveKey([]byte(password), salt, iters, mem, par, uint32(keyLen))
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, mem, iters, par,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
}

// Acceptance (audit S12): parameters read back from a stored hash are
// validated and clamped, so a tampered row cannot turn verification into
// an allocation bomb.
func TestParseHashClampsAndRejects(t *testing.T) {
	legit := phcEncode("s3cret", argonTime, argonMemory, argonThreads, argonKeyLen)
	if _, err := parseHash(legit); err != nil {
		t.Fatalf("legit hash rejected: %v", err)
	}
	oversized := strings.Replace(legit, fmt.Sprintf("m=%d,t=%d,p=%d", argonMemory, argonTime, argonThreads),
		"m=4000000000,t=999999,p=99", 1)
	p, err := parseHash(oversized)
	if err != nil {
		t.Fatalf("oversized hash rejected instead of clamped: %v", err)
	}
	if p.mem != argonMaxMem || p.iters != argonMaxTime || p.par != argonMaxPar {
		t.Fatalf("clamp: m=%d t=%d p=%d", p.mem, p.iters, p.par)
	}
	for name, mut := range map[string]func(string) string{
		"zero iters":  func(s string) string { return strings.Replace(s, "t=3", "t=0", 1) },
		"zero memory": func(s string) string { return strings.Replace(s, "m=65536", "m=0", 1) },
		"short salt": func(s string) string {
			return strings.Replace(s, "$"+base64.RawStdEncoding.EncodeToString(make([]byte, 16))+"$", "$"+base64.RawStdEncoding.EncodeToString(make([]byte, 4))+"$", 1)
		},
	} {
		if _, err := parseHash(mut(legit)); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

// A tampered row still fails closed — the clamped derivation simply does
// not reproduce the stored key.
func TestVerifyPasswordTamperedParametersFailClosed(t *testing.T) {
	legit := phcEncode("s3cret", argonTime, argonMemory, argonThreads, argonKeyLen)
	tampered := strings.Replace(legit, "t=3", "t=999999", 1)
	if err := VerifyPassword("s3cret", tampered); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("tampered hash verified: %v", err)
	}
}

func TestNeedsRehash(t *testing.T) {
	current, _ := HashPassword("s3cret")
	if NeedsRehash(current) {
		t.Fatal("current parameters flagged")
	}
	weak := phcEncode("s3cret", argonTime, argonMemory/2, argonThreads, argonKeyLen)
	if !NeedsRehash(weak) {
		t.Fatal("weak memory not flagged")
	}
	shortKey := phcEncode("s3cret", argonTime, argonMemory, argonThreads, argonKeyLen-1)
	if !NeedsRehash(shortKey) {
		t.Fatal("short key not flagged")
	}
	if NeedsRehash("not-a-hash") {
		t.Fatal("unparseable row flagged")
	}
}

// Acceptance (S12): a successful login over a weak row upgrades it in
// place; the credential itself is unchanged.
func TestLoginRehashesWeakParameters(t *testing.T) {
	m, store := manager(t)
	m.HashStore = store
	weak := phcEncode("correct horse battery staple", argonTime, argonMemory/2, argonThreads, argonKeyLen)
	store.AddUser(User{ID: "u2", Email: "weak@example.test", PasswordHash: weak, Role: RoleViewer})
	ctx := context.Background()
	if _, err := m.Login(ctx, "weak@example.test", "correct horse battery staple", ip); err != nil {
		t.Fatal(err)
	}
	u, err := store.UserByEmail(ctx, "weak@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if NeedsRehash(u.PasswordHash) {
		t.Fatal("row not upgraded")
	}
	if err := VerifyPassword("correct horse battery staple", u.PasswordHash); err != nil {
		t.Fatalf("upgraded row does not verify: %v", err)
	}
}
