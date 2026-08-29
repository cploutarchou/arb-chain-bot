// Package entitlements_test holds the checks that cross the package
// boundary: they compare what the package documents advertise against
// what the shipped tree can actually deliver. They live in the external
// test package so entitlements itself keeps no dependency on the screener
// — the policy layer must not import the domain it describes.
package entitlements_test

import (
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/entitlements"
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/venue"
)

// registeredTiers is the set of tiers with at least one collector this
// build ships — the tree's own answer to "what can we actually screen".
func registeredTiers(t *testing.T) map[string]int {
	t.Helper()
	reg := venue.Registry()
	if len(reg) == 0 {
		t.Fatal("venue registry is empty; every package would fail below")
	}
	counts := map[string]int{}
	for _, e := range reg {
		tier, ok := screener.VenueTiers[e.ID]
		if !ok {
			t.Errorf("registered venue %q has no tier in screener.VenueTiers", e.ID)
			continue
		}
		counts[tier]++
	}
	return counts
}

// TestAdvertisedTiersResolveToRegisteredVenues is T-102's acceptance
// criterion. Every screener tier a package advertises must resolve to at
// least one venue this build actually ships a collector for. This is the
// test that would have caught the Desk and Institution packages selling a
// "dex" tier with zero DEX code behind it.
func TestAdvertisedTiersResolveToRegisteredVenues(t *testing.T) {
	counts := registeredTiers(t)
	for _, code := range entitlements.Codes {
		p, ok := entitlements.Package(code)
		if !ok {
			t.Fatalf("package %q missing", code)
		}
		for _, tier := range p.Venues.ScreenerTiers {
			if counts[tier] == 0 {
				t.Errorf("package %q advertises screener tier %q, which no registered venue belongs to "+
					"(nothing in the tree delivers it — see MASTER_PLAN T-102)", code, tier)
			}
		}
	}
}

// TestDexCapabilityMatchesTree keeps the DexImplemented constant honest
// from the other direction: flipping it to true without registering a DEX
// venue would re-open T-102 by turning the guard off rather than by
// building the thing.
func TestDexCapabilityMatchesTree(t *testing.T) {
	inTree := len(screener.VenuesInTier(screener.TierDex)) > 0
	if entitlements.DexImplemented && !inTree {
		t.Error("entitlements.DexImplemented is true but no venue belongs to the dex tier; " +
			"flip it in the change that registers DEX venues (T-116), not before")
	}
	if !entitlements.DexImplemented && inTree {
		t.Error("DEX venues are registered but entitlements.DexImplemented is still false; " +
			"the packages are now under-selling what the build delivers (T-116)")
	}
}

// TestVenueTiersCoverKnownVenues stops the vocabulary drifting from the
// venue set: a venue added to KnownVenues without a tier would be
// invisible to the guard above.
func TestVenueTiersCoverKnownVenues(t *testing.T) {
	for id := range screener.KnownVenues {
		tier, ok := screener.VenueTiers[id]
		if !ok {
			t.Errorf("known venue %q has no entry in screener.VenueTiers", id)
			continue
		}
		if tier == screener.TierDex {
			t.Errorf("venue %q is tagged as a DEX tier venue; DEX is not built (T-076)", id)
		}
	}
	for id := range screener.VenueTiers {
		if !screener.KnownVenues[id] {
			t.Errorf("screener.VenueTiers has a tier for %q, which is not a known venue", id)
		}
	}
}
