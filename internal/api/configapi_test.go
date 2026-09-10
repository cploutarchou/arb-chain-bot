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

	// S5: reads need scanner:config (OPERATOR+) — a VIEWER is refused,
	// the operator who can edit reads the snapshot.
	viewerCookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.AddCookie(viewerCookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer GET = %d: %s", rec.Code, rec.Body.String())
	}
	cookie, _ := login(t, mux, "op@example.test", "op-pw")
	req = httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("operator GET = %d: %s", rec.Code, rec.Body.String())
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

	// Viewer: any write forbidden. parent_version=1 (the seeded version)
	// so the request clears the review P3(g) parent_version gate and
	// actually reaches the RBAC check this case is testing.
	vCookie, vCSRF := login(t, mux, "viewer@example.test", "viewer-pw")
	p := strategy.DefaultParams()
	p.Scanner.Depth = 80
	if rec := postConfig(t, mux, vCookie, vCSRF, "/api/v1/config", withParentVersion(t, p, 1)); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer write = %d: %s", rec.Code, rec.Body.String())
	}

	// Operator: scanner tuning allowed…
	oCookie, oCSRF := login(t, mux, "op@example.test", "op-pw")
	if rec := postConfig(t, mux, oCookie, oCSRF, "/api/v1/config", withParentVersion(t, p, 1)); rec.Code != http.StatusOK {
		t.Fatalf("operator scanner write = %d: %s", rec.Code, rec.Body.String())
	}
	if got := svc.Current().Params.Scanner.Depth; got != 80 {
		t.Fatalf("depth after operator write = %d", got)
	}

	// …but risk limits are ADMIN-only.
	p2 := svc.Current().Params
	p2.Risk.MinNetEdgeBps = decimal.NewFromInt(11)
	if rec := postConfig(t, mux, oCookie, oCSRF, "/api/v1/config", withParentVersion(t, p2, 2)); rec.Code != http.StatusForbidden {
		t.Fatalf("operator risk write = %d: %s", rec.Code, rec.Body.String())
	}

	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")
	if rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config", withParentVersion(t, p2, 2)); rec.Code != http.StatusOK {
		t.Fatalf("admin risk write = %d: %s", rec.Code, rec.Body.String())
	}
	if got := svc.Current().Version; got != 3 {
		t.Fatalf("version after two writes = %d, want 3", got)
	}

	// Identical payload → 409 no_change.
	if rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config", withParentVersion(t, p2, 3)); rec.Code != http.StatusConflict {
		t.Fatalf("no-change write = %d: %s", rec.Code, rec.Body.String())
	}

	// Invalid payload → 400.
	bad := svc.Current().Params
	bad.Scanner.Workers = 99
	if rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config", withParentVersion(t, bad, 3)); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid write = %d: %s", rec.Code, rec.Body.String())
	}
}

// TestConfigApplyRollbackRequireParentVersionForWeb is the review P3(g)
// regression: a web-sourced apply/rollback that omits parent_version
// entirely must fail with 400 parent_version_required, not silently
// skip the optimistic-concurrency check.
func TestConfigApplyRollbackRequireParentVersionForWeb(t *testing.T) {
	_, mux, _ := newConfigServer(t)
	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")

	p := strategy.DefaultParams()
	p.Scanner.Depth = 91
	rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config", p) // no parent_version key at all
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "parent_version_required") {
		t.Fatalf("apply without parent_version = %d: %s, want 400 parent_version_required", rec.Code, rec.Body.String())
	}

	rec = postConfig(t, mux, aCookie, aCSRF, "/api/v1/config/rollback", map[string]int64{"version": 1})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "parent_version_required") {
		t.Fatalf("rollback without parent_version = %d: %s, want 400 parent_version_required", rec.Code, rec.Body.String())
	}
}

func TestConfigRollback(t *testing.T) {
	_, mux, svc := newConfigServer(t)
	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")

	p := strategy.DefaultParams()
	p.Risk.MaxDailyLoss = decimal.NewFromInt(500)
	if rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config", withParentVersion(t, p, 1)); rec.Code != http.StatusOK {
		t.Fatalf("write = %d", rec.Code)
	}

	// Operator may not roll back a risk change (diff touches risk).
	oCookie, oCSRF := login(t, mux, "op@example.test", "op-pw")
	if rec := postConfig(t, mux, oCookie, oCSRF, "/api/v1/config/rollback", map[string]any{"version": 1, "parent_version": 2}); rec.Code != http.StatusForbidden {
		t.Fatalf("operator rollback = %d: %s", rec.Code, rec.Body.String())
	}

	if rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config/rollback", map[string]any{"version": 1, "parent_version": 2}); rec.Code != http.StatusOK {
		t.Fatalf("admin rollback = %d: %s", rec.Code, rec.Body.String())
	}
	cur := svc.Current()
	if cur.Version != 3 || !cur.Params.Risk.MaxDailyLoss.Equal(decimal.NewFromInt(200)) {
		t.Fatalf("after rollback: %+v", cur)
	}

	if rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config/rollback", map[string]any{"version": 99, "parent_version": 3}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing rollback = %d", rec.Code)
	}
}

func TestConfigVersionsListAndAbsentService(t *testing.T) {
	_, mux, _ := newConfigServer(t)
	cookie, _ := login(t, mux, "op@example.test", "op-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/versions?limit=5", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("versions = %d: %s", rec.Code, rec.Body.String())
	}

	// Without a service the routes answer 404 honestly.
	_, bareMux := newTestServer(t)
	cookie2, _ := login(t, bareMux, "op@example.test", "op-pw")
	req = httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.AddCookie(cookie2)
	rec = httptest.NewRecorder()
	bareMux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("absent service GET = %d", rec.Code)
	}
}

// withParentVersion merges a "parent_version" key into a JSON-marshalable
// body — the config-apply route pulls it out before strict-decoding the
// rest into strategy.Params.
func withParentVersion(t *testing.T, body any, parent int64) map[string]any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m["parent_version"] = parent
	return m
}

func TestConfigApplyOptimisticConcurrency(t *testing.T) {
	_, mux, svc := newConfigServer(t)
	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")

	// Correct parent_version (matches the loaded version 1) succeeds and
	// advances the version.
	p := strategy.DefaultParams()
	p.Scanner.Depth = 77
	body := withParentVersion(t, p, 1)
	if rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config", body); rec.Code != http.StatusOK {
		t.Fatalf("apply with correct parent_version = %d: %s", rec.Code, rec.Body.String())
	}
	if got := svc.Current().Version; got != 2 {
		t.Fatalf("version after apply = %d, want 2", got)
	}

	// Stale parent_version (still says 1, but current is now 2) is
	// refused with 409 stale_version and reports the current version —
	// even when it is the SAME payload that would otherwise be a
	// no-change 409 conflict; the concurrency error must win.
	stale := withParentVersion(t, p, 1)
	rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config", stale)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale apply = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data  map[string]int64 `json:"data"`
		Error *APIError        `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error == nil || env.Error.Code != "stale_version" {
		t.Fatalf("error = %+v, want code stale_version", env.Error)
	}
	if env.Data["current_version"] != 2 {
		t.Fatalf("current_version = %d, want 2", env.Data["current_version"])
	}
	if got := svc.Current().Version; got != 2 {
		t.Fatalf("version must not advance on a stale write, got %d", got)
	}

	// review P3(g): omitting parent_version entirely is now a hard 400
	// (TestConfigApplyRollbackRequireParentVersionForWeb covers this in
	// isolation) rather than the old unchecked-apply fallthrough.
	p2 := svc.Current().Params
	p2.Scanner.Depth = 88
	if rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config", p2); rec.Code != http.StatusBadRequest {
		t.Fatalf("apply without parent_version = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if got := svc.Current().Version; got != 2 {
		t.Fatalf("version must not advance on a rejected apply, got %d", got)
	}
}

func TestConfigRollbackOptimisticConcurrency(t *testing.T) {
	_, mux, svc := newConfigServer(t)
	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")

	p := strategy.DefaultParams()
	p.Risk.MaxDailyLoss = decimal.NewFromInt(500)
	if rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config", withParentVersion(t, p, 1)); rec.Code != http.StatusOK {
		t.Fatalf("seed write = %d", rec.Code)
	}
	if got := svc.Current().Version; got != 2 {
		t.Fatalf("version = %d, want 2", got)
	}

	// Stale rollback: caller thinks current is 1, it is actually 2.
	rec := postConfig(t, mux, aCookie, aCSRF, "/api/v1/config/rollback",
		map[string]any{"version": 1, "parent_version": 1})
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale rollback = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Error *APIError `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error == nil || env.Error.Code != "stale_version" {
		t.Fatalf("error = %+v, want stale_version", env.Error)
	}

	// Correct parent_version rolls back fine.
	rec = postConfig(t, mux, aCookie, aCSRF, "/api/v1/config/rollback",
		map[string]any{"version": 1, "parent_version": 2})
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback with correct parent_version = %d: %s", rec.Code, rec.Body.String())
	}
}
