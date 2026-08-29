package entitlements

import (
	"errors"
	"strings"
	"testing"
)

// tierOfFixture is a stand-in for screener.VenueTiers. The real lookup is
// injected by the API layer (T-104); the policy layer never imports the
// screener, so its tests never do either.
func tierOfFixture(id string) string {
	return map[string]string{
		"binance":  "tier1",
		"okx":      "tier1",
		"kucoin":   "tier2",
		"kraken":   "tier2",
		"bitmart":  "tier3",
		"bitfinex": "tier3",
	}[id]
}

func TestCheckVenueTiersEnforcesThePackageRow(t *testing.T) {
	cases := []struct {
		name    string
		pkg     string
		venues  []string
		wantErr bool
		wantMsg string
	}{
		{name: "operator may use tier1", pkg: PackageOperator, venues: []string{"binance", "okx"}},
		{name: "operator may use tier2", pkg: PackageOperator, venues: []string{"kucoin", "kraken"}},
		{
			name: "operator may NOT use tier3", pkg: PackageOperator,
			venues: []string{"binance", "bitmart"},
			// The whole point of T-104: this used to be allowed because
			// nothing read screener_tiers.
			wantErr: true, wantMsg: `venue "bitmart" is in tier "tier3"`,
		},
		{name: "desk may use tier3", pkg: PackageDesk, venues: []string{"binance", "kucoin", "bitmart"}},
		{name: "institution may use tier3", pkg: PackageInstitution, venues: []string{"bitfinex"}},
		{
			name: "signal is tier1 only", pkg: PackageSignal,
			venues: []string{"kucoin"},
			// Verified as reachable today: nothing stopped a Signal tenant
			// enabling a Tier-2 venue before this gate existed.
			wantErr: true, wantMsg: `venue "kucoin" is in tier "tier2"`,
		},
		{
			name: "an unclassifiable venue is refused, not waved through", pkg: PackageInstitution,
			venues:  []string{"binance", "definitely-not-a-venue"},
			wantErr: true, wantMsg: "belongs to no known tier",
		},
		{name: "no venues is not a refusal", pkg: PackageWatch, venues: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := Package(tc.pkg)
			if !ok {
				t.Fatalf("package %q missing", tc.pkg)
			}
			err := p.CheckVenueTiers(tc.venues, tierOfFixture)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("CheckVenueTiers(%v) = %v, want nil", tc.venues, err)
				}
				return
			}
			if !errors.Is(err, ErrExceeded) {
				t.Fatalf("got %v, want an ErrExceeded", err)
			}
			var ex *Exceeded
			if !errors.As(err, &ex) {
				t.Fatalf("got %v, want *Exceeded", err)
			}
			// The key is what the 403 payload reports and what the console
			// keys its upgrade prompt on.
			if ex.Key != "venues.screener_tiers" {
				t.Errorf("Key = %q, want venues.screener_tiers", ex.Key)
			}
			if !strings.Contains(ex.Message, tc.wantMsg) {
				t.Errorf("Message = %q, want it to contain %q", ex.Message, tc.wantMsg)
			}
		})
	}
}

// TestCheckVenueTiersFailsClosed pins the posture: a gate that cannot
// classify a venue refuses. A missing lookup is a programming error, and
// the safe answer to "I cannot tell whether you may use this" is no.
func TestCheckVenueTiersFailsClosed(t *testing.T) {
	p, _ := Package(PackageInstitution)
	err := p.CheckVenueTiers([]string{"binance"}, nil)
	if !errors.Is(err, ErrExceeded) {
		t.Fatalf("nil lookup = %v, want a refusal", err)
	}
	// ...but only when there is something to classify: an empty request
	// asks nothing of the lookup.
	if err := p.CheckVenueTiers(nil, nil); err != nil {
		t.Errorf("nil lookup with no venues = %v, want nil", err)
	}
}

// TestTier3IsAdvertisedByTheTopTwoPackages is the packaging half of
// T-104: Tier-3 venues ship, are soaked and are enabled by default, and
// the row that describes them has to say so or Desk and Operator remain
// indistinguishable on venues.
func TestTier3IsAdvertisedByTheTopTwoPackages(t *testing.T) {
	for _, code := range []string{PackageDesk, PackageInstitution} {
		p, _ := Package(code)
		if !Has(p.Venues.ScreenerTiers, "tier3") {
			t.Errorf("package %q does not advertise tier3: %v", code, p.Venues.ScreenerTiers)
		}
	}
	for _, code := range []string{PackageWatch, PackageSignal, PackageOperator} {
		p, _ := Package(code)
		if Has(p.Venues.ScreenerTiers, "tier3") {
			t.Errorf("package %q advertises tier3; it is the Desk differentiator: %v", code, p.Venues.ScreenerTiers)
		}
	}
	desk, _ := Package(PackageDesk)
	op, _ := Package(PackageOperator)
	if strings.Join(desk.Venues.ScreenerTiers, ",") == strings.Join(op.Venues.ScreenerTiers, ",") {
		t.Error("Desk and Operator advertise identical venue tiers; the row differentiates nothing")
	}
}
