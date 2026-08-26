package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

func newConfigServer(t *testing.T) (*Server, *http.ServeMux, *strategy.Service) {
	t.Helper()
	s, mux := newTestServer(t)
	svc := strategy.NewService(strategy.NewMemoryStore(), discardLogger(), nil)
	if _, err := svc.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.Strategy = svc
	return s, mux, svc
}

func postConfig(t *testing.T, mux *http.ServeMux, cookie *http.Cookie, csrf string, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestConfigReadRequiresAuthAndReturnsSnapshot(t *testing.T) {
	_, mux, _ := newConfigServer(t)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth GET = %d", rec.Code)
	}

	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer GET = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data strategy.Snapshot `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Version != 1 {
		t.Fatalf("version = %d, want 1", env.Data.Version)
	}
}

func TestConfigWritePermissionsBySection(t *testing.T) {
	_, mux, svc := newConfigServer(t)

	// Viewer: any write forbidden.
	vCookie, vCSRF := login(t, mux, "viewer@example.test", "viewer-pw")
	p := strategy.DefaultParams()
	p.Scanner.Depth = 80
	if rec := postConfig(t, mux, vCookie, vCSRF, "/api/v1/config", p); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer write = %d: %s", rec.Code, rec.Body.String())
	}

	// Operator: scanner tuning allowed…
	oCookie, oCSRF := login(t, mux, "op@example.test", "op-pw")
	if rec := postConfig(t, mux, oCookie, oCSRF, "/api/v1/config", p); rec.Code != http.StatusOK {
		t.Fatalf("operator scanner write = %d: %s", rec.Code, rec.Body.String())
	}
	if got := svc.Current().Params.Scanner.Depth; got != 80 {
		t.Fatalf("depth after operator write = %d", got)
	}

	// …but risk limits are ADMIN-only.
	p2 := svc.Current().Params
	p2.Risk.MinNetEdgeBps = decimal.NewFromInt(11)
	if rec := postConfig(t, mux, oCookie, oCSRF, "/api/v1/config", p2); rec.Code != http.StatusForbidden {
		t.Fatalf("operator risk write = %d: %s", rec.Code, rec.Body.String())
	}

	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")
	if rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config", p2); rec.Code != http.StatusOK {
		t.Fatalf("admin risk write = %d: %s", rec.Code, rec.Body.String())
	}
	if got := svc.Current().Version; got != 3 {
		t.Fatalf("version after two writes = %d, want 3", got)
	}

	// Identical payload → 409 no_change.
	if rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config", p2); rec.Code != http.StatusConflict {
		t.Fatalf("no-change write = %d: %s", rec.Code, rec.Body.String())
	}

	// Invalid payload → 400.
	bad := svc.Current().Params
	bad.Scanner.Workers = 99
	if rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config", bad); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid write = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestConfigRollback(t *testing.T) {
	_, mux, svc := newConfigServer(t)
	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")

	p := strategy.DefaultParams()
	p.Risk.MaxDailyLoss = decimal.NewFromInt(500)
	if rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config", p); rec.Code != http.StatusOK {
		t.Fatalf("write = %d", rec.Code)
	}

	// Operator may not roll back a risk change (diff touches risk).
	oCookie, oCSRF := login(t, mux, "op@example.test", "op-pw")
	if rec := postConfig(t, mux, oCookie, oCSRF, "/api/v1/config/rollback", map[string]int64{"version": 1}); rec.Code != http.StatusForbidden {
		t.Fatalf("operator rollback = %d: %s", rec.Code, rec.Body.String())
	}

	if rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config/rollback", map[string]int64{"version": 1}); rec.Code != http.StatusOK {
		t.Fatalf("admin rollback = %d: %s", rec.Code, rec.Body.String())
	}
	cur := svc.Current()
	if cur.Version != 3 || !cur.Params.Risk.MaxDailyLoss.Equal(decimal.NewFromInt(200)) {
		t.Fatalf("after rollback: %+v", cur)
	}

	if rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config/rollback", map[string]int64{"version": 99}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing rollback = %d", rec.Code)
	}
}

func TestConfigVersionsListAndAbsentService(t *testing.T) {
	_, mux, _ := newConfigServer(t)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/versions?limit=5", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("versions = %d: %s", rec.Code, rec.Body.String())
	}

	// Without a service the routes answer 404 honestly.
	_, bareMux := newTestServer(t)
	cookie2, _ := login(t, bareMux, "viewer@example.test", "viewer-pw")
	req = httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.AddCookie(cookie2)
	rec = httptest.NewRecorder()
	bareMux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("absent service GET = %d", rec.Code)
	}
}
