package screener

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestDefaultsValidates(t *testing.T) {
	def := Defaults()
	if err := def.Validate(); err != nil {
		t.Fatalf("Defaults() must validate: %v", err)
	}
	if len(def.Venues) != len(KnownVenues) {
		t.Fatalf("Defaults() venues = %d, want %d", len(def.Venues), len(KnownVenues))
	}
	for id := range KnownVenues {
		v, ok := def.Venues[id]
		// Tier-1 and Tier-2 on since the 2026-08-27 soak (T-075); Tier-3
		// (T-078) opt-in until its own soak — see TestDefaultsTier3OptIn.
		if !ok || v.Enabled == tier3Venues[id] {
			t.Fatalf("Defaults() venue %s enabled=%v", id, v.Enabled)
		}
		if v.PerpsEnabled == (id == VenueCoinbase) { // Coinbase: no retail perps
			t.Fatalf("Defaults() venue %s perps_enabled=%v", id, v.PerpsEnabled)
		}
	}
	if !def.Venues[VenueBybit].PerpTakerBps.Equal(d("5.5")) {
		t.Fatalf("bybit perp_taker_bps = %s, want 5.5", def.Venues[VenueBybit].PerpTakerBps)
	}
}

// TestDefaultsTier3OptIn pins the T-078 opt-in set: the five Tier-3
// venues start DISABLED (they enable per venue after their own 30-min
// soak, SKILL.md step 5) and every other known venue starts enabled.
func TestDefaultsTier3OptIn(t *testing.T) {
	wantOff := map[Venue]bool{
		VenueCryptoCom: true, VenueBitfinex: true, VenueBingX: true,
		VenueWhiteBIT: true, VenueBitMart: true,
	}
	if len(wantOff) != len(tier3Venues) {
		t.Fatalf("tier3Venues has %d entries, want %d", len(tier3Venues), len(wantOff))
	}
	for id := range wantOff {
		if !tier3Venues[id] {
			t.Fatalf("tier3Venues missing %s", id)
		}
	}
	def := Defaults()
	for _, id := range OrderedVenues {
		if def.Venues[id].Enabled != !wantOff[id] {
			t.Fatalf("Defaults() venue %s enabled=%v, want %v", id, def.Venues[id].Enabled, !wantOff[id])
		}
	}
}

func TestSettingsValidateRejectsBadPollInterval(t *testing.T) {
	s := Defaults()
	s.PollIntervalS = 1
	if err := s.Validate(); err == nil {
		t.Fatal("poll_interval_s=1 must be rejected (2..60)")
	}
	s.PollIntervalS = 61
	if err := s.Validate(); err == nil {
		t.Fatal("poll_interval_s=61 must be rejected (2..60)")
	}
}

func TestSettingsValidateRejectsUnknownVenue(t *testing.T) {
	s := Defaults()
	s.Venues["dydx"] = VenueSettings{Enabled: true}
	if err := s.Validate(); err == nil {
		t.Fatal("unknown venue must be rejected")
	}
}

func TestSettingsValidateFeesInclusiveZeroAndHundred(t *testing.T) {
	s := Defaults()
	v := s.Venues[VenueBinance]
	v.SpotTakerBps = decimal.Zero // zero-fee pairs exist; 0 must be legal
	v.PerpTakerBps = decimal.NewFromInt(100)
	s.Venues[VenueBinance] = v
	if err := s.Validate(); err != nil {
		t.Fatalf("fees 0 and 100 (inclusive bounds) must validate: %v", err)
	}
	v.SpotTakerBps = decimal.NewFromInt(-1)
	s.Venues[VenueBinance] = v
	if err := s.Validate(); err == nil {
		t.Fatal("negative fee must be rejected")
	}
}

func TestSettingsValidateRejectsNegativeBalance(t *testing.T) {
	s := Defaults()
	s.Paper.Balances[VenueBinance] = map[string]decimal.Decimal{"USDT": decimal.NewFromInt(-1)}
	if err := s.Validate(); err == nil {
		t.Fatal("negative paper balance must be rejected")
	}
	s.Paper.Balances[VenueBinance] = map[string]decimal.Decimal{"USDT": decimal.Zero}
	if err := s.Validate(); err != nil {
		t.Fatalf("zero paper balance must be legal: %v", err)
	}
}

func TestSettingsCloneIsDeep(t *testing.T) {
	s := Defaults()
	s.Paper.Balances[VenueBinance] = map[string]decimal.Decimal{"USDT": d("100")}
	c := s.Clone()
	v := c.Venues[VenueBinance]
	v.SpotTakerBps = d("999")
	c.Venues[VenueBinance] = v
	c.Paper.Balances[VenueBinance]["USDT"] = d("999")
	if s.Venues[VenueBinance].SpotTakerBps.Equal(d("999")) {
		t.Fatal("mutating the clone's venue map leaked into the original")
	}
	if s.Paper.Balances[VenueBinance]["USDT"].Equal(d("999")) {
		t.Fatal("mutating the clone's paper balances leaked into the original")
	}
}

func TestFieldTimingVenueEnabledIsRestartScoped(t *testing.T) {
	s := Defaults()
	ft := FieldTiming(s)
	if ft["venues.binance.enabled"] != "restart" {
		t.Fatalf("venues.binance.enabled timing = %q, want restart", ft["venues.binance.enabled"])
	}
	if ft["venues.binance.spot_taker_bps"] != "hot" {
		t.Fatalf("venues.binance.spot_taker_bps timing = %q, want hot", ft["venues.binance.spot_taker_bps"])
	}
	if ft["poll_interval_s"] != "hot" {
		t.Fatalf("poll_interval_s timing = %q, want hot", ft["poll_interval_s"])
	}
}

func TestDiffDetectsChange(t *testing.T) {
	old := Defaults()
	next := old.Clone()
	v := next.Venues[VenueBinance]
	v.SpotTakerBps = d("7")
	next.Venues[VenueBinance] = v
	diff, err := Diff(old, next)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff) == 0 {
		t.Fatal("Diff must report the changed fee")
	}
	if _, err := Diff(old, old.Clone()); err != nil {
		t.Fatal(err)
	}
	if same, _ := Diff(old, old.Clone()); len(same) != 0 {
		t.Fatalf("Diff of identical documents = %+v, want empty", same)
	}
}
