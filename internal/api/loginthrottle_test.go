package api

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/config"
)

// newLoginThrottleServer builds a server behind a trusted reverse proxy
// (10.0.0.0/8) with the account throttle effectively disabled and the
// per-IP throttle capped at ipMax, so the HTTP-level tests below
// exercise ONLY the per-IP behaviour end to end (audit S1/P1-10).
func newLoginThrottleServer(t *testing.T, ipMax int) *http.ServeMux {
	t.Helper()
	cfg := config.Bootstrap{
		Mode: config.ModeMarketData, HTTPAddr: ":0",
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
	}
	s := NewServer(cfg, discardLogger(), BuildInfo{Version: "test"})
	store := auth.NewMemoryStore()
	for _, u := range []struct{ id, email, pw string }{
		{"u-a", "a@example.test", "a-password"},
		{"u-b", "b@example.test", "b-password"},
	} {
		hash, err := auth.HashPassword(u.pw)
		if err != nil {
			t.Fatal(err)
		}
		store.AddUser(auth.User{ID: u.id, Email: u.email, PasswordHash: hash, Role: auth.RoleViewer})
	}
	s.Auth = &auth.Manager{
		Users: store, Sessions: store,
		Throttle:   auth.NewThrottle(100, time.Minute, 10*time.Minute),
		IPThrottle: auth.NewThrottle(ipMax, time.Minute, 10*time.Minute),
		TTL:        time.Hour,
		Now:        time.Now,
	}
	mux := http.NewServeMux()
	s.routes(mux)
	return mux
}

func attemptLogin(mux *http.ServeMux, email, password, remoteAddr, forwardedFor string) *httptest.ResponseRecorder {
	body := `{"email":"` + email + `","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.RemoteAddr = remoteAddr
	if forwardedFor != "" {
		req.Header.Set("X-Forwarded-For", forwardedFor)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// Acceptance (audit S1/P1-10): behind the trusted reverse proxy, two
// different real clients (distinguished by X-Forwarded-For) that share
// the same TCP peer (the proxy) get INDEPENDENT per-IP throttle
// buckets — one attacker sharing the proxy cannot lock out every user.
func TestLoginIPThrottleDoesNotCrossRealClients(t *testing.T) {
	mux := newLoginThrottleServer(t, 3)
	const proxyPeer = "10.0.0.5:443"

	// The attacker, real address 198.51.100.1, fails enough to trip its
	// own bucket.
	for i := 0; i < 3; i++ {
		rec := attemptLogin(mux, "a@example.test", "wrong", proxyPeer, "198.51.100.1")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d: %s", i, rec.Code, rec.Body.String())
		}
	}
	if rec := attemptLogin(mux, "a@example.test", "a-password", proxyPeer, "198.51.100.1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("attacker should now be throttled: %d %s", rec.Code, rec.Body.String())
	}

	// A DIFFERENT real client behind the SAME proxy, logging into a
	// different account, is unaffected.
	if rec := attemptLogin(mux, "b@example.test", "b-password", proxyPeer, "198.51.100.2"); rec.Code != http.StatusOK {
		t.Fatalf("a different real client was locked out by the attacker's bucket: %d %s", rec.Code, rec.Body.String())
	}
}

// Without a trusted proxy configured, every request behind a shared
// ingress resolves to the SAME address (the proxy's), so the per-IP
// throttle still applies to the proxy's own peer address regardless of
// X-Forwarded-For — locking out real clients is the pre-existing,
// documented failure mode this fix requires operators to configure
// ARB_TRUSTED_PROXIES to avoid. This test pins that an UNconfigured
// deployment ignores the header rather than trusting it by accident.
func TestLoginIPThrottleIgnoresForwardedForWithoutTrustedProxy(t *testing.T) {
	cfg := config.Bootstrap{Mode: config.ModeMarketData, HTTPAddr: ":0"}
	s := NewServer(cfg, discardLogger(), BuildInfo{Version: "test"})
	store := auth.NewMemoryStore()
	hash, err := auth.HashPassword("a-password")
	if err != nil {
		t.Fatal(err)
	}
	store.AddUser(auth.User{ID: "u-a", Email: "a@example.test", PasswordHash: hash, Role: auth.RoleViewer})
	s.Auth = &auth.Manager{
		Users: store, Sessions: store,
		Throttle:   auth.NewThrottle(100, time.Minute, 10*time.Minute),
		IPThrottle: auth.NewThrottle(3, time.Minute, 10*time.Minute),
		TTL:        time.Hour,
		Now:        time.Now,
	}
	mux := http.NewServeMux()
	s.routes(mux)

	// Each attempt claims a different X-Forwarded-For; with no trusted
	// proxy configured it must be ignored, so all three still count
	// against the SAME (peer) bucket.
	forwardedFor := []string{"203.0.113.1", "203.0.113.2", "203.0.113.3"}
	for i, xff := range forwardedFor {
		rec := attemptLogin(mux, "a@example.test", "wrong", "10.0.0.5:443", xff)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d: %s", i, rec.Code, rec.Body.String())
		}
	}
	if rec := attemptLogin(mux, "a@example.test", "a-password", "10.0.0.5:443", "203.0.113.99"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("peer-address bucket should be throttled regardless of the forged header: %d %s", rec.Code, rec.Body.String())
	}
}
