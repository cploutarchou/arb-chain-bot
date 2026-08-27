package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/paperexec"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/report"
)

func decimalFrom(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestScreenerReportsUnavailableWithoutGenerator(t *testing.T) {
	_, mux, _ := newScreenerServer(t)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/screener/reports", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("reports without generator = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestScreenerReportsRunIsAdminCSRFAndListsResult(t *testing.T) {
	s, mux, svc := newScreenerServer(t)
	ledger := paperexec.NewMemoryLedger()
	fixed := time.Date(2026, 8, 30, 0, 5, 0, 0, time.UTC)
	s.ScreenerReports = &report.Generator{Svc: svc, Ledger: ledger, Store: report.NewMemoryStore(), Log: discardLogger(),
		Dir: t.TempDir(), Now: func() time.Time { return fixed }}
	e := paperexec.Execution{ID: "e1", RuleID: "r1", Strategy: screener.StrategyCrossVenueSpot, Kind: paperexec.KindSpot,
		Base: "BTC", Quote: "USDT", VenueA: screener.VenueBinance, VenueB: screener.VenueOKX, At: time.Now().UTC().Add(-2 * time.Hour),
		PnLQuote: decimalFrom("1.5"), Fills: []paperexec.Fill{{Leg: 1, Venue: screener.VenueBinance, Side: "BUY", Qty: decimalFrom("0.1"), FillPrice: decimalFrom("50000"), Status: "FILLED"}}}
	if err := ledger.InsertExecution(context.Background(), e); err != nil {
		t.Fatal(err)
	}

	// Operator: cannot run (ADMIN only).
	opCookie, opCSRF := login(t, mux, "op@example.test", "op-pw")
	rec := postScreener(t, mux, opCookie, opCSRF, http.MethodPost, "/api/v1/screener/reports/run", map[string]any{})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("operator run = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	// Admin without CSRF: refused.
	adCookie, adCSRF := login(t, mux, "admin@example.test", "admin-pw")
	rec = postScreener(t, mux, adCookie, "", http.MethodPost, "/api/v1/screener/reports/run", map[string]any{})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("admin run without CSRF = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	rec = postScreener(t, mux, adCookie, adCSRF, http.MethodPost, "/api/v1/screener/reports/run", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin run = %d: %s", rec.Code, rec.Body.String())
	}
	var runEnv struct {
		Data struct {
			Run report.RunResult `json:"run"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &runEnv); err != nil {
		t.Fatal(err)
	}
	if len(runEnv.Data.Run.Reports) != 4 { // (spot,"") and (spot,r1) × day/cumulative
		t.Fatalf("run reports = %+v", runEnv.Data.Run)
	}

	// Viewer lists and views.
	vCookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/screener/reports", nil)
	req.AddCookie(vCookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}
	var listEnv struct {
		Data struct {
			Reports []report.Summary `json:"reports"`
			NextRun time.Time        `json:"next_run_utc"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listEnv); err != nil {
		t.Fatal(err)
	}
	if len(listEnv.Data.Reports) != 4 || listEnv.Data.NextRun.IsZero() {
		t.Fatalf("list = %+v", listEnv.Data)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/screener/reports/"+listEnv.Data.Reports[0].ID, nil)
	req.AddCookie(vCookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get = %d: %s", rec.Code, rec.Body.String())
	}
	var getEnv struct {
		Data struct {
			Report report.Report `json:"report"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &getEnv); err != nil {
		t.Fatal(err)
	}
	if getEnv.Data.Report.Markdown == "" || len(getEnv.Data.Report.Payload.Gate) != 8 || getEnv.Data.Report.Payload.Model != report.Model {
		t.Fatalf("report = %+v", getEnv.Data.Report)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/screener/reports/nope", nil)
	req.AddCookie(vCookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing report = %d", rec.Code)
	}
}
