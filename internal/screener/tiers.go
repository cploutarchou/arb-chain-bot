package screener

// Venue tiers are the order in which venues were researched, built and
// soaked (MASTER_PLAN T-074 / T-075 / T-078), and they are the vocabulary
// the package documents use to describe venue coverage
// (`internal/entitlements`, docs/design/packages.md §2). The grouping
// lived only in the comment blocks above the Venue constants and in plan
// prose until T-102, which needed it machine-readable: a package may not
// advertise a venue tier that no shipped collector belongs to.
const (
	Tier1 = "tier1"
	Tier2 = "tier2"
	Tier3 = "tier3"
	// TierDex is the on-chain tier. No venue belongs to it: DEX quote
	// sources are designed (docs/design/dex-arbitrage.md) but not built
	// (T-076 → T-110..T-116). The constant exists so the entitlement
	// guard and its tests can name the tier without a magic string, and
	// so VenuesInTier(TierDex) returning empty is what proves the
	// capability is unbuilt rather than an accident of spelling.
	TierDex = "dex"
)

// VenueTiers maps every KnownVenue to its tier. A venue added to
// KnownVenues without a tier here fails TestVenueTiersCoverKnownVenues —
// the entitlement vocabulary and the shipped venue set are not allowed to
// drift apart.
var VenueTiers = map[Venue]string{
	VenueBinance:   Tier1,
	VenueOKX:       Tier1,
	VenueBybit:     Tier1,
	VenueBitget:    Tier1,
	VenueGate:      Tier1,
	VenueMEXC:      Tier1,
	VenueKuCoin:    Tier2,
	VenueHTX:       Tier2,
	VenueKraken:    Tier2,
	VenueCoinbase:  Tier2,
	VenueCryptoCom: Tier3,
	VenueBitfinex:  Tier3,
	VenueBingX:     Tier3,
	VenueWhiteBIT:  Tier3,
	VenueBitMart:   Tier3,
}

// VenuesInTier returns the venues in tier, in OrderedVenues order. An
// unknown tier returns an empty slice — that is the signal T-102's
// entitlement guard reads as "nothing in the tree delivers this".
func VenuesInTier(tier string) []Venue {
	out := make([]Venue, 0, len(OrderedVenues))
	for _, id := range OrderedVenues {
		if VenueTiers[id] == tier {
			out = append(out, id)
		}
	}
	return out
}
