package api

import (
	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"

	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// TestScreenerSpreadsGuardParams: the live VON row never appears by
// default; include_suspect=1&include_unknown_liquidity=1 returns it
// flagged with liquidity_quote null; ?quote= accepts a comma list and
// never merges USDC into USDT.
func TestScreenerSpreadsGuardParams(t *testing.T) {
	_, mux, svc := newScreenerServer(t)
	now := time.Now().UTC()
	q := func(venue screener.Venue, base, quote, bid, ask, qty string, unknown bool) {
		svc.Book.SetQuote(screener.Quote{Venue: venue, Base: base, Quote: quote,
			Bid: decimal.RequireFromString(bid), BidQty: decimal.RequireFromString(qty),
			Ask: decimal.RequireFromString(ask), AskQty: decimal.RequireFromString(qty), At: now, LiquidityUnknown: unknown})
	}
	q(screener.VenueGate, "VON", "USDT", "0.0000000010", "0.0000000011", "0", true)
	q(screener.VenueMEXC, "VON", "USDT", "0.19", "0.195", "1000", false)
	q(screener.VenueBinance, "BTC", "USDT", "99", "100", "20", false)
	q(screener.VenueOKX, "BTC", "USDT", "101", "102", "30", false)
	q(screener.VenueBinance, "BTC", "USDC", "99", "100", "20", false)
	q(screener.VenueOKX, "BTC", "USDC", "101", "102", "30", false)

	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	get := func(path string) (rows []map[string]any, excluded map[string]int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d: %s", path, rec.Code, rec.Body.String())
		}
		var env struct {
			Data struct {
				Rows     []map[string]any `json:"rows"`
				Excluded map[string]int   `json:"excluded"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		return env.Data.Rows, env.Data.Excluded
	}

	rows, excluded := get("/api/v1/screener/spreads?min_liquidity=0")
	for _, r := range rows {
		if r["base"] == "VON" {
			t.Fatalf("VON row served by default: %v", r)
		}
		if r["suspect"] != false || r["liquidity_unknown"] != false || r["liquidity_quote"] == nil {
			t.Fatalf("default row carries guard flags: %v", r)
		}
	}
	if len(rows) != 4 || excluded["suspect"] != 2 || excluded["liquidity_unknown"] != 0 {
		t.Fatalf("default: rows=%d excluded=%v, want 4 rows, suspect 2", len(rows), excluded)
	}

	rows, excluded = get("/api/v1/screener/spreads?min_liquidity=0&include_suspect=1&include_unknown_liquidity=1&base=VON")
	if len(rows) != 2 || excluded["suspect"] != 0 {
		t.Fatalf("include flags: rows=%d excluded=%v", len(rows), excluded)
	}
	von := rows[0]
	if von["suspect"] != true || von["suspect_reason"] != screener.SuspectSpreadImplausible || von["liquidity_unknown"] != true || von["liquidity_quote"] != nil {
		t.Fatalf("VON row = %v", von)
	}

	rows, _ = get("/api/v1/screener/spreads?min_liquidity=0&quote=USDT,usdc")
	if len(rows) != 4 {
		t.Fatalf("quote=USDT,usdc rows = %d, want 4", len(rows))
	}
	rows, _ = get("/api/v1/screener/spreads?min_liquidity=0&quote=USDC")
	if len(rows) != 2 {
		t.Fatalf("quote=USDC rows = %d, want 2", len(rows))
	}
	for _, r := range rows {
		if r["quote"] != "USDC" {
			t.Fatalf("USDC request returned quote %v", r["quote"])
		}
	}
}

// TestScreenerSettingsMaxPlausibleSpreadBpsValidated: the field is
// served with the effective default and rejected outside 100..100000.
func TestScreenerSettingsMaxPlausibleSpreadBpsValidated(t *testing.T) {
	_, mux, svc := newScreenerServer(t)
	cookie, csrf := login(t, mux, "admin@example.test", "admin-pw")
	snap := svc.Current()
	if !snap.Settings.MaxPlausibleSpreadBps.Equal(decimal.NewFromInt(2000)) {
		t.Fatalf("served default = %s, want 2000", snap.Settings.MaxPlausibleSpreadBps)
	}
	doc := snap.Settings
	doc.MaxPlausibleSpreadBps = decimal.NewFromInt(50)
	rec := postScreener(t, mux, cookie, csrf, http.MethodPost, "/api/v1/screener/settings",
		map[string]any{"parent_version": snap.Version, "settings": doc})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("max_plausible_spread_bps=50 accepted: %d %s", rec.Code, rec.Body.String())
	}
	doc.MaxPlausibleSpreadBps = decimal.NewFromInt(5000)
	rec = postScreener(t, mux, cookie, csrf, http.MethodPost, "/api/v1/screener/settings",
		map[string]any{"parent_version": snap.Version, "settings": doc})
	if rec.Code != http.StatusOK {
		t.Fatalf("max_plausible_spread_bps=5000 rejected: %d %s", rec.Code, rec.Body.String())
	}
	if !svc.Current().Settings.MaxPlausibleSpreadBps.Equal(decimal.NewFromInt(5000)) {
		t.Fatal("hot value not applied")
	}
}

// TestScreenerFundingHoursClampedToRetention (audit D9): the funding
// window never exceeds the caller's history.retention_days — an
// oversized request is trimmed instead of becoming an unbounded scan.
func TestScreenerFundingHoursClampedToRetention(t *testing.T) {
	watch := &Principal{} // no entitlements/org: falls back to Watch (1 day)
	if got := clampFundingHours(2000, watch); got != 24 {
		t.Fatalf("watch clamp = %d, want 24", got)
	}
	if got := clampFundingHours(0, watch); got != 24 {
		t.Fatalf("default = %d, want the 1-day ceiling", got)
	}
	if got := clampFundingHours(-5, nil); got != 72 {
		t.Fatalf("negative = %d, want default 72", got)
	}
	inst := &Principal{OrgID: tenancy.PlatformOrgID} // → Institution entitlements
	if got := clampFundingHours(2000, inst); got != 2000 {
		t.Fatalf("institution clamp = %d, want 2000 (under the ceiling)", got)
	}
	if got := clampFundingHours(500000, inst); got != 24*1095 {
		t.Fatalf("institution ceiling = %d", got)
	}
}
