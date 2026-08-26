package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeReads is a static ReadModel.
type fakeReads struct{ portfolio bool }

func (f fakeReads) RecentOpportunities(int) any {
	return []map[string]string{{"id": "op-1", "net_bps": "17.20"}}
}

func (f fakeReads) Portfolio() (any, bool) {
	if !f.portfolio {
		return nil, false
	}
	return map[string]any{"equity": map[string]string{"USDT": "10012.5"}}, true
}

func (f fakeReads) PnL() (any, bool) {
	if !f.portfolio {
		return nil, false
	}
	return map[string]any{"assets": []map[string]string{{"asset": "USDT", "realized": "12.5"}}}, true
}

func (f fakeReads) Risk() any {
	return map[string]any{"config_version": 3, "reject_reason_counts": map[string]int64{"MIN_EDGE": 4}}
}

func (f fakeReads) Health() any {
	return map[string]any{"ready": true}
}

func getWith(t *testing.T, mux *http.ServeMux, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestReadRoutes(t *testing.T) {
	s, mux := newTestServer(t)
	s.Reads = fakeReads{portfolio: true}
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")

	for path, want := range map[string]string{
		"/api/v1/opportunities": `"net_bps":"17.20"`,
		"/api/v1/portfolio":     `"USDT":"10012.5"`,
		"/api/v1/pnl":           `"realized":"12.5"`,
		"/api/v1/risk":          `"MIN_EDGE":4`,
		"/api/v1/system/health": `"ready":true`,
	} {
		rec := getWith(t, mux, cookie, path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s = %d: %s", path, rec.Code, rec.Body.String())
			continue
		}
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s missing %s: %s", path, want, rec.Body.String())
		}
	}

	// History groups without a store answer honest 404s.
	for _, path := range []string{
		"/api/v1/opportunities/history",
		"/api/v1/paper/cycles",
		"/api/v1/paper/cycles/c1/orders",
	} {
		rec := getWith(t, mux, cookie, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s without store = %d", path, rec.Code)
		}
	}

	// /api/v1/audit: permission gate is outermost — the viewer lacks
	// PermViewAudit and gets 403 before the storage gate can 404; an
	// operator passes the permission and hits the honest 404.
	if rec := getWith(t, mux, cookie, "/api/v1/audit"); rec.Code != http.StatusForbidden {
		t.Errorf("viewer audit = %d", rec.Code)
	}
	opCookie, _ := login(t, mux, "op@example.test", "op-pw")
	if rec := getWith(t, mux, opCookie, "/api/v1/audit"); rec.Code != http.StatusNotFound {
		t.Errorf("operator audit without store = %d", rec.Code)
	}
}

func TestReadRoutesEngineAbsent(t *testing.T) {
	_, mux := newTestServer(t)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	rec := getWith(t, mux, cookie, "/api/v1/opportunities")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("engine absent = %d", rec.Code)
	}
}

func TestPortfolioNotReadyIsHonest(t *testing.T) {
	s, mux := newTestServer(t)
	s.Reads = fakeReads{portfolio: false}
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	rec := getWith(t, mux, cookie, "/api/v1/portfolio")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "portfolio_absent") {
		t.Fatalf("not-ready portfolio = %d: %s", rec.Code, rec.Body.String())
	}
}
