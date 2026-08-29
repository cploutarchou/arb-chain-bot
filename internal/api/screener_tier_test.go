package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/entitlements"
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// enforceRuleFor runs the rule-level entitlement gate the way the real
// handlers do and reports the status plus the entitlement key the 403
// carries.
func enforceRuleFor(t *testing.T, s *Server, ent entitlements.Entitlements, rule screener.Rule) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/screener/rules", nil)
	if s.enforceRule(rec, req, ent, rule) {
		return http.StatusOK, ""
	}
	// The entitlement 403 carries the breached key and its limit in
	// "data", alongside the "error" envelope the console reads.
	var env struct {
		Data struct {
			Key string `json:"key"`
		} `json:"data"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "" && env.Error.Code != "entitlement_exceeded" {
		return rec.Code, "unexpected error code: " + env.Error.Code
	}
	if env.Data.Key != "" {
		return rec.Code, env.Data.Key
	}
	// Fall back to the raw body so a shape change shows up as a readable
	// failure rather than an empty string.
	return rec.Code, rec.Body.String()
}

// TestRuleVenueTierEnforced is T-104's acceptance criterion at the layer
// that serves it: an Operator principal naming a Tier-3 venue is refused
// with 403 entitlement_exceeded on venues.screener_tiers, while the same
// rule under Desk is allowed. Before T-104 both were allowed — nothing
// read venues.screener_tiers, so the row that separates the packages
// gated nothing.
func TestRuleVenueTierEnforced(t *testing.T) {
	s, _, _ := newScreenerServer(t)

	// BitMart is Tier-3: coded, conformance-tested and soaked (T-078),
	// and enabled by default — which is exactly why an unenforced tier
	// row let every package reach it.
	tier3Rule := screener.Rule{
		Kind:       screener.RuleKindSpread,
		BuyVenues:  []screener.Venue{screener.VenueBinance},
		SellVenues: []screener.Venue{screener.VenueBitMart},
		CooldownS:  3600,
	}
	tier2Rule := screener.Rule{
		Kind:       screener.RuleKindSpread,
		BuyVenues:  []screener.Venue{screener.VenueBinance},
		SellVenues: []screener.Venue{screener.VenueKuCoin},
		CooldownS:  3600,
	}

	op, ok := entitlements.Package(entitlements.PackageOperator)
	if !ok {
		t.Fatal("operator package missing")
	}
	desk, ok := entitlements.Package(entitlements.PackageDesk)
	if !ok {
		t.Fatal("desk package missing")
	}

	if code, key := enforceRuleFor(t, s, op, tier3Rule); code != http.StatusForbidden || key != "venues.screener_tiers" {
		t.Errorf("operator + Tier-3 venue = %d %q, want 403 venues.screener_tiers", code, key)
	}
	if code, key := enforceRuleFor(t, s, op, tier2Rule); code != http.StatusOK {
		t.Errorf("operator + Tier-2 venue = %d %q, want 200 (Operator includes tier2)", code, key)
	}
	if code, key := enforceRuleFor(t, s, desk, tier3Rule); code != http.StatusOK {
		t.Errorf("desk + Tier-3 venue = %d %q, want 200 (Desk includes tier3)", code, key)
	}
}

// TestVenueTierLookupMatchesTheScreener guards the injected lookup
// itself: entitlements takes a func rather than importing the screener,
// so nothing else proves the two agree.
func TestVenueTierLookupMatchesTheScreener(t *testing.T) {
	for _, id := range screener.OrderedVenues {
		want := screener.VenueTiers[id]
		if want == "" {
			t.Errorf("venue %q has no tier; CheckVenueTiers would refuse it for every package", id)
			continue
		}
		if got := venueTier(string(id)); got != want {
			t.Errorf("venueTier(%q) = %q, want %q", id, got, want)
		}
	}
	if got := venueTier("not-a-venue"); got != "" {
		t.Errorf("venueTier(unknown) = %q, want \"\" so the gate refuses it", got)
	}
}
