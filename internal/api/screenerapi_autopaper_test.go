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
)

type fakeAutoPaper struct{ view screener.AutoPaperView }

func (f fakeAutoPaper) AutoPaperView(context.Context, time.Time) (screener.AutoPaperView, error) {
	return f.view, nil
}

func TestScreenerAutoPaperHonestWithoutExecutor(t *testing.T) {
	_, mux, _ := newScreenerServer(t)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/screener/auto-paper", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Executor  string `json:"executor"`
			Positions []any  `json:"positions"`
			Summary   struct {
				PerRule []any `json:"per_rule"`
			} `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Executor != "not_running" || len(env.Data.Positions) != 0 || len(env.Data.Summary.PerRule) != 0 {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestScreenerAutoPaperServesExecutorViewAndRequiresAuth(t *testing.T) {
	_, mux, svc := newScreenerServer(t)
	pnl := decimal.RequireFromString("12.970005")
	hit := decimal.NewFromInt(1)
	svc.SetAutoPaper(fakeAutoPaper{view: screener.AutoPaperView{
		GeneratedAt: time.Now().UTC(), Model: "paper",
		Positions: []screener.PaperPosition{{ID: "pos-1", RuleID: "r1", Strategy: screener.StrategyCrossVenueSpot, Base: "BTC", Quote: "USDT",
			VenueA: screener.VenueBinance, VenueB: screener.VenueOKX, Qty: decimal.RequireFromString("0.1"), PnLQuote: pnl, Status: "CLOSED"}},
		Summary: screener.AutoPaperSummary{PerRule: []screener.RuleSummary{{RuleID: "r1", Strategy: screener.StrategyCrossVenueSpot,
			Alerts: 1, Executed: 1, Skipped: map[string]int64{"DEPTH": 2}, NetPnLQuote: pnl, HitRate: &hit, Samples: 1}}},
	}})

	// No session: 401.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/screener/auto-paper", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", rec.Code)
	}

	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/screener/auto-paper", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Executor  string `json:"executor"`
			Positions []struct {
				ID       string `json:"id"`
				PnLQuote string `json:"pnl_quote"`
			} `json:"positions"`
			Summary struct {
				PerRule []struct {
					RuleID   string           `json:"rule_id"`
					Skipped  map[string]int64 `json:"skipped"`
					NetPnL   string           `json:"net_pnl_quote"`
					HitRate  string           `json:"hit_rate"`
					Executed int64            `json:"executed"`
				} `json:"per_rule"`
			} `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Executor != "running" || len(env.Data.Positions) != 1 || env.Data.Positions[0].PnLQuote != "12.970005" {
		t.Fatalf("body = %s", rec.Body.String())
	}
	pr := env.Data.Summary.PerRule
	if len(pr) != 1 || pr[0].RuleID != "r1" || pr[0].Skipped["DEPTH"] != 2 || pr[0].NetPnL != "12.970005" || pr[0].HitRate != "1" || pr[0].Executed != 1 {
		t.Fatalf("per_rule = %+v", pr)
	}
}

func TestScreenerRuleParamsValidatedOnCreate(t *testing.T) {
	_, mux, _ := newScreenerServer(t)
	cookie, csrf := login(t, mux, "admin@example.test", "admin-pw")
	body := map[string]any{
		"name": "carry", "enabled": true, "kind": "carry", "min_carry_apr": "0.05",
		"min_liquidity_quote": "500", "min_lifetime_s": 0, "buy_venues": []string{"binance"}, "sell_venues": []string{},
		"quotes": []string{"USDT"}, "bases_allow": []string{}, "bases_deny": []string{}, "cooldown_s": 60,
		"telegram": false, "auto_paper": true, "paper_size_quote": "10000",
		"params": map[string]any{"mmr": "1.5"},
	}
	rec := postScreener(t, mux, cookie, csrf, http.MethodPost, "/api/v1/screener/rules", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mmr 1.5 accepted: %d %s", rec.Code, rec.Body.String())
	}
	body["params"] = map[string]any{"mmr": "0.004", "slip_bps": "2", "max_hold_h": 720}
	rec = postScreener(t, mux, cookie, csrf, http.MethodPost, "/api/v1/screener/rules", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("valid params rejected: %d %s", rec.Code, rec.Body.String())
	}
}
