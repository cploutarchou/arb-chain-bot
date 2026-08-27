package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

func newScreenerServer(t *testing.T) (*Server, *http.ServeMux, *screener.Service) {
	t.Helper()
	s, _ := newTestServer(t)
	svc := screener.NewService(screener.NewBook(), screener.NewMemoryStore(), discardLogger(), nil)
	if _, err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc.Rules = screener.NewMemoryRuleStore()
	svc.Events = screener.NewMemoryEventStore()
	svc.Templates = screener.NewMemoryTemplateStore()
	svc.Funding = screener.NewMemoryFundingStore()
	s.Screener = svc
	mux := http.NewServeMux()
	s.routes(mux)
	return s, mux, svc
}

func postScreener(t *testing.T, mux *http.ServeMux, cookie *http.Cookie, csrf, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
	req.AddCookie(cookie)
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestScreenerAbsentServiceIs503(t *testing.T) {
	_, mux := newTestServer(t)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/screener/status", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
}

func TestScreenerStatusHonestAboutCollectors(t *testing.T) {
	_, mux, _ := newScreenerServer(t)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/screener/status", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Collectors string `json:"collectors"`
			Venues     []struct {
				Online bool `json:"online"`
			} `json:"venues"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Collectors != "not_started" {
		t.Fatalf("collectors = %q, want not_started", env.Data.Collectors)
	}
	if len(env.Data.Venues) != len(screener.OrderedVenues) {
		t.Fatalf("venues = %d, want %d (every known venue, enabled or not)", len(env.Data.Venues), len(screener.OrderedVenues))
	}
	for _, v := range env.Data.Venues {
		if v.Online {
			t.Fatal("a venue reported online with no collector running")
		}
	}
}

// TestScreenerSpreadsReadOverPopulatedBook is the required "spreads read
// over a book you populate directly in the test" acceptance check.
func TestScreenerSpreadsReadOverPopulatedBook(t *testing.T) {
	_, mux, svc := newScreenerServer(t)
	now := time.Now().UTC()
	svc.Book.SetQuote(screener.Quote{
		Venue: screener.VenueBinance, Base: "BTC", Quote: "USDT",
		Ask: decimal.RequireFromString("100"), AskQty: decimal.RequireFromString("2"),
		Bid: decimal.RequireFromString("99"), BidQty: decimal.RequireFromString("2"), At: now,
	})
	svc.Book.SetQuote(screener.Quote{
		Venue: screener.VenueOKX, Base: "BTC", Quote: "USDT",
		Ask: decimal.RequireFromString("102"), AskQty: decimal.RequireFromString("3"),
		Bid: decimal.RequireFromString("101"), BidQty: decimal.RequireFromString("3"), At: now,
	})

	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	// The book's liquidity is 200 quote; the default min_liquidity is
	// settings.min_liquidity_quote (500), so the same request WITHOUT
	// the parameter hides both lanes — asserted first.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/screener/spreads", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("spreads = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Total   int                  `json:"total"`
			Rows    []screener.SpreadRow `json:"rows"`
			Filters struct {
				MinLiquidity string `json:"min_liquidity"`
			} `json:"filters"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Total != 0 || env.Data.Filters.MinLiquidity != "500" {
		t.Fatalf("default request: total = %d (want 0), min_liquidity = %q (want 500)", env.Data.Total, env.Data.Filters.MinLiquidity)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/screener/spreads?min_liquidity=0", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("spreads = %d: %s", rec.Code, rec.Body.String())
	}
	env.Data.Rows = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Total != 2 {
		t.Fatalf("total = %d, want 2", env.Data.Total)
	}
	var buyBinance *screener.SpreadRow
	for i := range env.Data.Rows {
		if env.Data.Rows[i].BuyVenue == screener.VenueBinance && env.Data.Rows[i].SellVenue == screener.VenueOKX {
			buyBinance = &env.Data.Rows[i]
		}
	}
	if buyBinance == nil {
		t.Fatal("binance->okx row missing from the API response")
	}
	if !buyBinance.SpreadBpsGross.Equal(decimal.RequireFromString("100")) {
		t.Fatalf("gross bps = %s, want 100", buyBinance.SpreadBpsGross)
	}
}

func TestScreenerReadRequiresAuthViewerCanRead(t *testing.T) {
	_, mux, _ := newScreenerServer(t)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/screener/settings", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth GET = %d", rec.Code)
	}

	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/screener/settings", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer GET = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Version     int64             `json:"version"`
			FieldTiming map[string]string `json:"field_timing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Version != 1 {
		t.Fatalf("version = %d, want 1", env.Data.Version)
	}
	if env.Data.FieldTiming["venues.binance.enabled"] != "restart" {
		t.Fatalf("venues.binance.enabled timing = %q, want restart", env.Data.FieldTiming["venues.binance.enabled"])
	}
}

func TestScreenerWriteOperatorDeniedAdminAllowed(t *testing.T) {
	_, mux, svc := newScreenerServer(t)
	next := svc.Current().Settings.Clone()
	v := next.Venues[screener.VenueBinance]
	v.SpotTakerBps = decimal.NewFromInt(7)
	next.Venues[screener.VenueBinance] = v

	vCookie, vCSRF := login(t, mux, "viewer@example.test", "viewer-pw")
	if rec := postScreener(t, mux, vCookie, vCSRF, http.MethodPost, "/api/v1/screener/settings", map[string]any{"settings": next, "parent_version": 1}); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer write = %d: %s", rec.Code, rec.Body.String())
	}

	oCookie, oCSRF := login(t, mux, "op@example.test", "op-pw")
	if rec := postScreener(t, mux, oCookie, oCSRF, http.MethodPost, "/api/v1/screener/settings", map[string]any{"settings": next, "parent_version": 1}); rec.Code != http.StatusForbidden {
		t.Fatalf("operator write = %d: %s", rec.Code, rec.Body.String())
	}

	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")
	rec := postScreener(t, mux, aCookie, aCSRF, http.MethodPost, "/api/v1/screener/settings", map[string]any{"settings": next, "parent_version": 1})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin write = %d: %s", rec.Code, rec.Body.String())
	}
	if got := svc.Current().Version; got != 2 {
		t.Fatalf("version after admin write = %d, want 2", got)
	}
}

func TestScreenerSettingsWriteCSRFRequired(t *testing.T) {
	_, mux, svc := newScreenerServer(t)
	aCookie, _ := login(t, mux, "admin@example.test", "admin-pw")
	next := svc.Current().Settings.Clone()
	rec := postScreener(t, mux, aCookie, "", http.MethodPost, "/api/v1/screener/settings", map[string]any{"settings": next, "parent_version": 1})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestScreenerSettingsParentVersionConflict(t *testing.T) {
	_, mux, svc := newScreenerServer(t)
	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")

	next := svc.Current().Settings.Clone()
	v := next.Venues[screener.VenueBinance]
	v.SpotTakerBps = decimal.NewFromInt(7)
	next.Venues[screener.VenueBinance] = v

	rec := postScreener(t, mux, aCookie, aCSRF, http.MethodPost, "/api/v1/screener/settings", map[string]any{"settings": next, "parent_version": 1})
	if rec.Code != http.StatusOK {
		t.Fatalf("first apply = %d: %s", rec.Code, rec.Body.String())
	}
	if got := svc.Current().Version; got != 2 {
		t.Fatalf("version = %d, want 2", got)
	}

	// Stale parent_version (still 1) refused with 409.
	rec = postScreener(t, mux, aCookie, aCSRF, http.MethodPost, "/api/v1/screener/settings", map[string]any{"settings": next, "parent_version": 1})
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
		t.Fatalf("error = %+v, want stale_version", env.Error)
	}
	if env.Data["current_version"] != 2 {
		t.Fatalf("current_version = %d, want 2", env.Data["current_version"])
	}

	// Missing parent_version entirely is a 400, matching platform's
	// convention (P3(g) regression coverage mirrored here).
	rec = postScreener(t, mux, aCookie, aCSRF, http.MethodPost, "/api/v1/screener/settings", map[string]any{"settings": next})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "parent_version_required") {
		t.Fatalf("missing parent_version = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestScreenerRulesCRUDViaAPI(t *testing.T) {
	_, mux, _ := newScreenerServer(t)
	aCookie, aCSRF := login(t, mux, "admin@example.test", "admin-pw")

	bps := "50"
	body := map[string]any{
		"name": "BTC cross", "enabled": true, "kind": "spread",
		"min_spread_bps": bps, "min_liquidity_quote": "1000", "min_lifetime_s": 10,
		"buy_venues": []string{"binance"}, "sell_venues": []string{"okx"},
		"cooldown_s": 60, "paper_size_quote": "0",
	}
	rec := postScreener(t, mux, aCookie, aCSRF, http.MethodPost, "/api/v1/screener/rules", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rule = %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			Rule screener.Rule `json:"rule"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Data.Rule.ID == "" {
		t.Fatal("created rule has no server-assigned id")
	}

	// Viewer cannot create.
	vCookie, vCSRF := login(t, mux, "viewer@example.test", "viewer-pw")
	if rec := postScreener(t, mux, vCookie, vCSRF, http.MethodPost, "/api/v1/screener/rules", body); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer create rule = %d", rec.Code)
	}

	// List includes it.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/screener/rules", nil)
	req.AddCookie(vCookie)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req)
	var list struct {
		Data struct {
			Rules []screener.Rule `json:"rules"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data.Rules) != 1 {
		t.Fatalf("rules list = %+v, want 1", list.Data.Rules)
	}

	// Delete.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/screener/rules/"+created.Data.Rule.ID, nil)
	delReq.AddCookie(aCookie)
	delReq.Header.Set("X-CSRF-Token", aCSRF)
	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete rule = %d: %s", delRec.Code, delRec.Body.String())
	}
}

func TestScreenerCalculator(t *testing.T) {
	_, mux, svc := newScreenerServer(t)
	now := time.Now().UTC()
	svc.Book.SetQuote(screener.Quote{Venue: screener.VenueBinance, Base: "BTC", Quote: "USDT", Ask: decimal.RequireFromString("100"), AskQty: decimal.RequireFromString("1000"), Bid: decimal.RequireFromString("99"), BidQty: decimal.RequireFromString("1000"), At: now})
	svc.Book.SetQuote(screener.Quote{Venue: screener.VenueOKX, Base: "BTC", Quote: "USDT", Ask: decimal.RequireFromString("102"), AskQty: decimal.RequireFromString("1000"), Bid: decimal.RequireFromString("101"), BidQty: decimal.RequireFromString("1000"), At: now})

	cookie, csrf := login(t, mux, "viewer@example.test", "viewer-pw")
	body := map[string]any{
		"base": "BTC", "quote": "USDT", "buy_venue": "binance", "sell_venue": "okx", "size_quote": "1000",
	}
	rec := postScreener(t, mux, cookie, csrf, http.MethodPost, "/api/v1/screener/calculator", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("calculator = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data screener.CalculatorResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.Data.SizeBase.Equal(decimal.RequireFromString("10")) {
		t.Fatalf("size_base = %s, want 10", env.Data.SizeBase)
	}
}

// TestScreenerTemplatesPerUserAPI covers design §7's route table
// tagging templates "(per user)", not "(ADMIN)" like every other
// mutation in this group: a VIEWER (who holds only screener:view, never
// screener:config) must be able to save and see their own filter
// preset, and it must not leak to a different user.
func TestScreenerTemplatesPerUserAPI(t *testing.T) {
	_, mux, _ := newScreenerServer(t)
	vCookie, vCSRF := login(t, mux, "viewer@example.test", "viewer-pw")

	body := map[string]any{"name": "My filter", "filters": map[string]any{"min_spread_bps": 50}}
	rec := postScreener(t, mux, vCookie, vCSRF, http.MethodPost, "/api/v1/screener/templates", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("viewer create template = %d, want 201 (templates are per-user, not screener:config): %s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/screener/templates", nil)
	req.AddCookie(vCookie)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req)
	var list struct {
		Data struct {
			Templates []screener.Template `json:"templates"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data.Templates) != 1 {
		t.Fatalf("viewer's templates = %+v, want 1", list.Data.Templates)
	}

	// A different user sees none.
	oCookie, _ := login(t, mux, "op@example.test", "op-pw")
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/screener/templates", nil)
	req2.AddCookie(oCookie)
	rec3 := httptest.NewRecorder()
	mux.ServeHTTP(rec3, req2)
	var list2 struct {
		Data struct {
			Templates []screener.Template `json:"templates"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec3.Body.Bytes(), &list2); err != nil {
		t.Fatal(err)
	}
	if len(list2.Data.Templates) != 0 {
		t.Fatalf("operator's templates leaked viewer's: %+v", list2.Data.Templates)
	}

	// The viewer can delete their own template.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/screener/templates/"+list.Data.Templates[0].ID, nil)
	delReq.AddCookie(vCookie)
	delReq.Header.Set("X-CSRF-Token", vCSRF)
	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("viewer delete own template = %d: %s", delRec.Code, delRec.Body.String())
	}
}

func TestScreenerAutoPaperEmptySummary(t *testing.T) {
	_, mux, _ := newScreenerServer(t)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/screener/auto-paper", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("auto-paper = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Positions []any `json:"positions"`
			Summary   struct {
				PerRule []any `json:"per_rule"`
			} `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Positions) != 0 || len(env.Data.Summary.PerRule) != 0 {
		t.Fatalf("auto-paper must be empty until T-071: %+v", env.Data)
	}
}
