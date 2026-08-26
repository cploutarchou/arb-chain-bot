package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/config"
)

func newTestServer(t *testing.T) (*Server, *http.ServeMux) {
	t.Helper()
	cfg := config.Bootstrap{Mode: config.ModeMarketData, HTTPAddr: ":0"}
	s := NewServer(cfg, discardLogger(), BuildInfo{Version: "test", Components: []string{"api"}})

	store := auth.NewMemoryStore()
	for _, u := range []struct {
		id, email, pw string
		role          auth.Role
	}{
		{"u-admin", "admin@example.test", "admin-pw", auth.RoleAdmin},
		{"u-viewer", "viewer@example.test", "viewer-pw", auth.RoleViewer},
		{"u-operator", "op@example.test", "op-pw", auth.RoleOperator},
	} {
		hash, err := auth.HashPassword(u.pw)
		if err != nil {
			t.Fatal(err)
		}
		store.AddUser(auth.User{ID: u.id, Email: u.email, PasswordHash: hash, Role: u.role})
	}
	s.Auth = &auth.Manager{
		Users: store, Sessions: store,
		Throttle: auth.NewThrottle(10, time.Minute, time.Minute),
		TTL:      time.Hour,
		Now:      time.Now,
	}
	mux := http.NewServeMux()
	s.routes(mux)
	return s, mux
}

func login(t *testing.T, mux *http.ServeMux, email, pw string) (cookie *http.Cookie, csrf string) {
	t.Helper()
	body := `{"email":"` + email + `","password":"` + pw + `"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.7:5555"
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d: %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || !cookie.Secure {
		t.Fatalf("session cookie missing or weak: %+v", cookie)
	}
	var env Envelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	data := env.Data.(map[string]any)
	csrf, _ = data["csrf_token"].(string)
	if csrf == "" {
		t.Fatal("no csrf token")
	}
	return cookie, csrf
}

func TestHealthzPublic(t *testing.T) {
	_, mux := newTestServer(t)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
}

func TestSystemStatusRequiresAuth(t *testing.T) {
	_, mux := newTestServer(t)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", rec.Code)
	}
}

func TestLoginMeStatusFlow(t *testing.T) {
	_, mux := newTestServer(t)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "VIEWER") {
		t.Fatalf("me = %d %s", rec.Code, rec.Body.String())
	}

	// Viewer holds PermViewSystem → allowed.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer status = %d", rec.Code)
	}
	var env Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error != nil {
		t.Fatalf("envelope: %v %+v", err, env.Error)
	}
}

func TestWrongPasswordUniformError(t *testing.T) {
	_, mux := newTestServer(t)
	for _, body := range []string{
		`{"email":"viewer@example.test","password":"wrong"}`,
		`{"email":"ghost@example.test","password":"wrong"}`,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
		req.RemoteAddr = "203.0.113.8:1"
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("login = %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "login failed") {
			t.Fatalf("error leaks detail: %s", rec.Body.String())
		}
	}
}

// RBAC at the HTTP layer: viewer denied on an operator-gated mutation;
// operator passes RBAC but must also present CSRF.
func TestRBACAndCSRFOnPaperPause(t *testing.T) {
	_, mux := newTestServer(t)

	viewerCookie, viewerCSRF := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/paper/pause", nil)
	req.AddCookie(viewerCookie)
	req.Header.Set("X-CSRF-Token", viewerCSRF)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer pause = %d", rec.Code)
	}

	opCookie, opCSRF := login(t, mux, "op@example.test", "op-pw")
	// Without CSRF: forbidden.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/paper/pause", nil)
	req.AddCookie(opCookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("operator without csrf = %d", rec.Code)
	}
	// With CSRF: passes both gates and reaches the honest 501.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/paper/pause", nil)
	req.AddCookie(opCookie)
	req.Header.Set("X-CSRF-Token", opCSRF)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("operator pause = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	_, mux := newTestServer(t)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout = %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout = %d", rec.Code)
	}
}

func TestAuthUnconfiguredFailsClosed(t *testing.T) {
	cfg := config.Bootstrap{Mode: config.ModeMarketData}
	s := NewServer(cfg, discardLogger(), BuildInfo{})
	mux := http.NewServeMux()
	s.routes(mux)
	for _, path := range []string{"/api/v1/system/status", "/api/v1/auth/me"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s without auth manager = %d", path, rec.Code)
		}
	}
}
