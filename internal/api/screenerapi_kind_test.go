package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Creating a spread rule and then a carry rule must persist BOTH exactly
// as submitted. Observed live on 2026-08-28: a carry rule created with
// min_carry_apr came back from the API as carry, but the stored row held
// kind "spread" and the FIRST rule's min_spread_bps, with min_carry_apr
// gone — the discriminating fields of one rule leaking into another.
func TestScreenerRuleKindDoesNotLeakBetweenRules(t *testing.T) {
	_, mux, _ := newScreenerServer(t)
	cookie, csrf := login(t, mux, "admin@example.test", "admin-pw")

	spread := map[string]any{
		"name": "spot", "enabled": true, "kind": "spread",
		"min_spread_bps": "5", "min_liquidity_quote": "500", "min_lifetime_s": 3,
		"buy_venues": []string{"binance"}, "sell_venues": []string{"okx"},
		"cooldown_s": 60, "paper_size_quote": "0",
	}
	if rec := postScreener(t, mux, cookie, csrf, http.MethodPost, "/api/v1/screener/rules", spread); rec.Code != http.StatusCreated {
		t.Fatalf("create spread rule = %d: %s", rec.Code, rec.Body.String())
	}

	carry := map[string]any{
		"name": "carry", "enabled": true, "kind": "carry",
		"min_carry_apr": "0.05", "min_liquidity_quote": "500", "min_lifetime_s": 3,
		"buy_venues": []string{"binance"}, "sell_venues": []string{"binance"},
		"cooldown_s": 300, "paper_size_quote": "0",
		"params": map[string]any{"mmr": "0.004"},
	}
	if rec := postScreener(t, mux, cookie, csrf, http.MethodPost, "/api/v1/screener/rules", carry); rec.Code != http.StatusCreated {
		t.Fatalf("create carry rule = %d: %s", rec.Code, rec.Body.String())
	}

	// Read the rules back the way the console and the evaluator do.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/screener/rules", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list rules = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			Rules []struct {
				Name         string  `json:"name"`
				Kind         string  `json:"kind"`
				MinSpreadBps *string `json:"min_spread_bps"`
				MinCarryAPR  *string `json:"min_carry_apr"`
			} `json:"rules"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data.Rules) != 2 {
		t.Fatalf("listed %d rules, want 2", len(out.Data.Rules))
	}
	for _, r := range out.Data.Rules {
		switch r.Name {
		case "spot":
			if r.Kind != "spread" || r.MinSpreadBps == nil || *r.MinSpreadBps != "5" || r.MinCarryAPR != nil {
				t.Fatalf("spread rule round-tripped as %+v", r)
			}
		case "carry":
			if r.Kind != "carry" {
				t.Fatalf("carry rule stored with kind %q (the other rule's kind leaked in)", r.Kind)
			}
			if r.MinCarryAPR == nil || *r.MinCarryAPR != "0.05" {
				t.Fatalf("carry rule lost min_carry_apr: %+v", r)
			}
			if r.MinSpreadBps != nil {
				t.Fatalf("carry rule gained min_spread_bps=%q from the spread rule", *r.MinSpreadBps)
			}
		default:
			t.Fatalf("unexpected rule %q", r.Name)
		}
	}
}
