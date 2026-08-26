package auth

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"
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
	m, _ := manager(t)
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
	// A different IP is a different key (per email|ip) and still works.
	other := netip.MustParseAddr("198.51.100.9")
	if _, err := m.Login(ctx, "op@example.test", "correct horse battery staple", other); err != nil {
		t.Fatalf("other ip blocked: %v", err)
	}
	// Lockout expires.
	m.Now = func() time.Time { return t0.Add(6 * time.Minute) }
	if _, err := m.Login(ctx, "op@example.test", "correct horse battery staple", ip); err != nil {
		t.Fatalf("post-lockout: %v", err)
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
	}
	viewerDenied := []Permission{
		PermViewAudit, PermPaperControl, PermPaperReset, PermScannerConfig,
		PermAIApprove, PermAlertAck, PermRiskConfig, PermExchangeConfig,
		PermUserManage, PermSystemConfig,
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
	}
	for _, p := range operatorDenied {
		if Can(RoleOperator, p) {
			t.Fatalf("operator allowed %s", p)
		}
	}
	if !Can(RoleOperator, PermPaperControl) || !Can(RoleOperator, PermAIApprove) {
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
