package screener

import (
	"testing"
	"time"

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
		// Venues with a clean soak are on at first boot: Tier-2 since
		// the 2026-08-27 soak (T-075), Tier-3 since the 2026-08-28 soak
		// (T-078) — see TestDefaultsEnablesEveryVenue. Tier-4 (Bithumb,
		// Phemex) is off until its first soak.
		if !ok || v.Enabled != (id != VenueCoinbase && VenueTiers[id] != Tier4) {
			t.Fatalf("Defaults() venue %s enabled=%v", id, v.Enabled)
		}
		if v.PerpsEnabled != (id != VenueCoinbase && id != VenueBithumb && VenueTiers[id] != Tier4) {
			t.Fatalf("Defaults() venue %s perps_enabled=%v", id, v.PerpsEnabled)
		}
	}
	if !def.Venues[VenueBybit].PerpTakerBps.Equal(d("5.5")) {
		t.Fatalf("bybit perp_taker_bps = %s, want 5.5", def.Venues[VenueBybit].PerpTakerBps)
	}
}

// TestDefaultsEnablesEveryVenue pins the post-soak invariant: every
// known venue whose 30-min live soak was clean starts ENABLED, and a
// venue whose soak was NOT clean (Coinbase: 20 × HTTP 429 and 20 failed
// polls on 2026-08-28) or that has not soaked yet (Tier-4: Bithumb and
// Phemex, T-075 remainder) starts DISABLED. A venue added without a
// soak, or one whose soak regresses, belongs on the disabled side here
// rather than being silently enabled.
func TestDefaultsEnablesEveryVenue(t *testing.T) {
	def := Defaults()
	if len(OrderedVenues) != len(KnownVenues) {
		t.Fatalf("OrderedVenues has %d entries, KnownVenues %d", len(OrderedVenues), len(KnownVenues))
	}
	for _, id := range OrderedVenues {
		want := id != VenueCoinbase && VenueTiers[id] != Tier4
		if def.Venues[id].Enabled != want {
			t.Fatalf("Defaults() venue %s enabled=%v, want %v", id, def.Venues[id].Enabled, want)
		}
	}
	for _, id := range []Venue{VenueCryptoCom, VenueBitfinex, VenueBingX, VenueWhiteBIT, VenueBitMart} {
		if !def.Venues[id].Enabled {
			t.Fatalf("Tier-3 venue %s must be enabled after the 2026-08-28 soak", id)
		}
	}
	for _, id := range []Venue{VenueBithumb, VenuePhemex} {
		if def.Venues[id].Enabled {
			t.Fatalf("Tier-4 venue %s must stay off until its first soak", id)
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

func TestSettingsPerpQuotePreferenceAndAlerts(t *testing.T) {
	def := Defaults()
	if got := def.PerpQuotePreference; len(got) != 1 || got[0] != "USDT" {
		t.Fatalf("Defaults() perp_quote_preference = %v, want [USDT]", got)
	}
	if def.Alerts.MaxLanesPerRule != 0 || def.Alerts.StaleHoldS != DefaultStaleHoldS {
		t.Fatalf("Defaults() alerts = %+v, want unbounded lanes and a %d s hold", def.Alerts, DefaultStaleHoldS)
	}
	// A stored document predating the fields reads as the defaults.
	var old Settings
	if pref := old.EffectivePerpQuotePreference(); len(pref) != 1 || pref[0] != "USDT" {
		t.Fatalf("empty preference resolves to %v", pref)
	}
	if old.Alerts.EffectiveStaleHold() != DefaultStaleHoldS*time.Second {
		t.Fatalf("zero stale_hold_s resolves to %s", old.Alerts.EffectiveStaleHold())
	}
	old = Defaults()
	old.PerpQuotePreference, old.Alerts.StaleHoldS = nil, 0
	n := old.Normalised()
	if len(n.PerpQuotePreference) != 1 || n.Alerts.StaleHoldS != DefaultStaleHoldS {
		t.Fatalf("Normalised() = %v / %+v", n.PerpQuotePreference, n.Alerts)
	}
	// Clone is deep for the preference list.
	c := def.Clone()
	c.PerpQuotePreference[0] = "USDC"
	if def.PerpQuotePreference[0] != "USDT" {
		t.Fatal("mutating the clone's preference leaked into the original")
	}

	bad := []func(s *Settings){
		func(s *Settings) { s.PerpQuotePreference = []string{"usdt"} },
		func(s *Settings) { s.PerpQuotePreference = []string{"USDT", "USDT"} },
		func(s *Settings) { s.PerpQuotePreference = []string{""} },
		func(s *Settings) { s.PerpQuotePreference = []string{"A", "B", "C", "D", "E", "F", "G", "H", "I"} },
		func(s *Settings) { s.Alerts.MaxLanesPerRule = -1 },
		func(s *Settings) { s.Alerts.MaxLanesPerRule = maxLanesPerRuleCeiling + 1 },
		func(s *Settings) { s.Alerts.StaleHoldS = -1 },
		func(s *Settings) { s.Alerts.StaleHoldS = maxStaleHoldS + 1 },
	}
	for i, mutate := range bad {
		s := Defaults()
		mutate(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("case %d must be rejected: %+v / %v", i, s.Alerts, s.PerpQuotePreference)
		}
	}
	good := Defaults()
	good.PerpQuotePreference = []string{"USDC", "USDT"}
	good.Alerts = AlertSettings{MaxLanesPerRule: 250, StaleHoldS: 45}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
	ft := FieldTiming(good)
	for _, k := range []string{"perp_quote_preference", "alerts.max_lanes_per_rule", "alerts.stale_hold_s"} {
		if ft[k] != "hot" {
			t.Errorf("field_timing[%s] = %q, want hot", k, ft[k])
		}
	}
	diff, err := Diff(Defaults(), good)
	if err != nil || len(diff) == 0 {
		t.Fatalf("Diff must report the new fields: %v %v", diff, err)
	}
}
