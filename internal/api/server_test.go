package api

import (
	"context"
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
		store.AddUser(auth.User{ID: u.id, Email: u.email, PasswordHash: hash, Role: u.role, PlatformAdmin: u.role == auth.RoleAdmin})
	}
	s.Auth = &auth.Manager{
		Users: store, Sessions: store,
		Throttle: auth.NewThrottle(10, time.Minute, time.Minute),
		TTL:      time.Hour,
		Now:      time.Now,
	}
	s.Users = &auth.AdminService{Store: store, Sessions: store, Now: time.Now}
	s.MetricsHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("# metrics"))
	})
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
	// With CSRF but no paper engine wired: honest 404.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/paper/pause", nil)
	req.AddCookie(opCookie)
	req.Header.Set("X-CSRF-Token", opCSRF)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("operator pause without engine = %d: %s", rec.Code, rec.Body.String())
	}
}

type fakePaper struct {
	running bool
	// resetErr, when set, is returned by Reset instead of succeeding —
	// tests use ErrPaperNotIdle to exercise the 409 path.
	resetErr error
	resets   int
}

func (f *fakePaper) Pause()        { f.running = false }
func (f *fakePaper) Resume()       { f.running = true }
func (f *fakePaper) Running() bool { return f.running }

func (f *fakePaper) Reset(context.Context) error {
	if f.resetErr != nil {
		return f.resetErr
	}
	f.resets++
	return nil
}

func TestPaperPauseResumeWiring(t *testing.T) {
	s, mux := newTestServer(t)
	fp := &fakePaper{running: true}
	s.Paper = fp
	cookie, csrf := login(t, mux, "op@example.test", "op-pw")

	call := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := call("/api/v1/paper/pause"); rec.Code != http.StatusOK || fp.running {
		t.Fatalf("pause = %d running=%v", rec.Code, fp.running)
	}
	if rec := call("/api/v1/paper/resume"); rec.Code != http.StatusOK || !fp.running {
		t.Fatalf("resume = %d running=%v", rec.Code, fp.running)
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	_, mux := newTestServer(t)
	cookie, csrf := login(t, mux, "viewer@example.test", "viewer-pw")

	// Logout is CSRF-protected like every mutating route (S-013).
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("logout without csrf = %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	rec = httptest.NewRecorder()
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

// Acceptance (audit S-003): the same-mux /metrics dev convenience stays
// behind RBAC — metric names map the platform's internals.
func TestMetricsEndpointRequiresPermission(t *testing.T) {
	_, mux := newTestServer(t)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /metrics = %d", rec.Code)
	}

	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "# metrics") {
		t.Fatalf("viewer /metrics = %d: %s", rec.Code, rec.Body.String())
	}
}

// Acceptance (audit S-017): a malformed correlation ID is never echoed
// back verbatim.
func TestCorrelationIDSanitized(t *testing.T) {
	_, mux := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	req.Header.Set("X-Correlation-ID", "evil\nheader\"injection")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req) // unauthenticated → error envelope carries the ID
	if strings.Contains(rec.Body.String(), "evil") {
		t.Fatalf("raw correlation id echoed: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid-correlation-id") {
		t.Fatalf("sanitized marker missing: %s", rec.Body.String())
	}
}

// Denial matrix for the AI and reports mutation routes (audit CR-P2-14):
// authentication, RBAC, and CSRF are enforced in that order, before any
// service-absence check leaks route topology.
func TestAIAndReportsDenialMatrix(t *testing.T) {
	_, mux := newTestServer(t)

	post := func(path string, cookie *http.Cookie, csrf string) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"kind":"daily"}`))
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	viewerCookie, viewerCSRF := login(t, mux, "viewer@example.test", "viewer-pw")
	opCookie, opCSRF := login(t, mux, "op@example.test", "op-pw")

	approve := "/api/v1/ai/recommendations/rec-1/approve"
	reject := "/api/v1/ai/recommendations/rec-1/reject"
	generate := "/api/v1/reports/generate"

	for path, cases := range map[string][]struct {
		name   string
		cookie *http.Cookie
		csrf   string
		want   int
	}{
		approve: {
			{"unauthenticated", nil, "", http.StatusUnauthorized},
			{"viewer lacks ai:approve", viewerCookie, viewerCSRF, http.StatusForbidden},
			{"operator without csrf", opCookie, "", http.StatusForbidden},
			{"operator full — service absent", opCookie, opCSRF, http.StatusNotFound},
		},
		reject: {
			{"unauthenticated", nil, "", http.StatusUnauthorized},
			{"viewer lacks ai:approve", viewerCookie, viewerCSRF, http.StatusForbidden},
			{"operator without csrf", opCookie, "", http.StatusForbidden},
			{"operator full — service absent", opCookie, opCSRF, http.StatusNotFound},
		},
		generate: {
			{"unauthenticated", nil, "", http.StatusUnauthorized},
			{"viewer lacks reports:generate", viewerCookie, viewerCSRF, http.StatusForbidden},
			{"operator without csrf", opCookie, "", http.StatusForbidden},
			{"operator full — generator absent", opCookie, opCSRF, http.StatusNotFound},
		},
	} {
		for _, tc := range cases {
			if got := post(path, tc.cookie, tc.csrf); got != tc.want {
				t.Errorf("%s / %s = %d, want %d", path, tc.name, got, tc.want)
			}
		}
	}

	// Reads: viewer may list analyses/reports (view perms), unauth may not.
	for _, path := range []string{"/api/v1/ai/analyses", "/api/v1/reports"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("unauth GET %s = %d", path, rec.Code)
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(viewerCookie)
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		// Authorized; the backing service/store is absent in this harness.
		if rec.Code != http.StatusNotFound {
			t.Errorf("viewer GET %s = %d, want 404 (absent backend)", path, rec.Code)
		}
	}
}
