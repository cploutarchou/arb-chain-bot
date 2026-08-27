package entitlements

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestPackagesValid(t *testing.T) {
	for _, code := range Codes {
		p, ok := Package(code)
		if !ok {
			t.Fatalf("package %q missing", code)
		}
		if err := Validate(p); err != nil {
			t.Fatalf("package %q invalid: %v", code, err)
		}
		if p.Execution.Live || !p.Execution.Paper {
			t.Fatalf("package %q execution = %+v", code, p.Execution)
		}
		raw, _ := json.Marshal(p)
		if _, err := ValidateJSON(raw); err != nil {
			t.Fatalf("package %q round trip: %v", code, err)
		}
	}
	// The tiers strictly widen on the levers packages.md calls out.
	prev, _ := Package(Codes[0])
	for _, code := range Codes[1:] {
		p, _ := Package(code)
		if p.Rules.MaxActive <= prev.Rules.MaxActive || p.Alerts.PerDay <= prev.Alerts.PerDay ||
			p.History.RetentionDays <= prev.History.RetentionDays || p.Rules.MinRefreshS > prev.Rules.MinRefreshS {
			t.Fatalf("%q does not widen over %q", code, prev.PackageCode)
		}
		prev = p
	}
}

// TestSchemaAgreesWithGo pins the Go validator's enums and required
// keys to schema.v1.json (packages.md §3.1) so the two cannot drift.
func TestSchemaAgreesWithGo(t *testing.T) {
	var schema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Enum       []string `json:"enum"`
			Required   []string `json:"required"`
			Properties map[string]struct {
				Enum  []string `json:"enum"`
				Items struct {
					Enum []string `json:"enum"`
				} `json:"items"`
				Const any `json:"const"`
			} `json:"properties"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(SchemaJSON, &schema); err != nil {
		t.Fatal(err)
	}
	same := func(name string, got, want []string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s: schema %v, go %v", name, got, want)
		}
	}
	same("package_code", schema.Properties["package_code"].Enum, Codes)
	same("venues.screener_tiers", schema.Properties["venues"].Properties["screener_tiers"].Items.Enum, enumTiers)
	same("rules.kinds", schema.Properties["rules"].Properties["kinds"].Items.Enum, enumKinds)
	same("alerts.channels", schema.Properties["alerts"].Properties["channels"].Items.Enum, enumChannels)
	same("auto_paper.strategies", schema.Properties["auto_paper"].Properties["strategies"].Items.Enum, enumStrategies)
	same("api.scopes", schema.Properties["api"].Properties["scopes"].Items.Enum, enumScopes)
	same("history.export_formats", schema.Properties["history"].Properties["export_formats"].Items.Enum, enumFormats)
	same("history.reports", schema.Properties["history"].Properties["reports"].Enum, enumReports)
	same("seats.roles", schema.Properties["seats"].Properties["roles"].Items.Enum, enumRoles)
	same("support.tier", schema.Properties["support"].Properties["tier"].Enum, enumSupport)
	if live := schema.Properties["execution"].Properties["live"].Const; live != false {
		t.Fatalf("schema execution.live const = %v, want false", live)
	}
	// Every required top-level key is a field of Entitlements (a
	// missing one would silently default to zero and pass the Go
	// validator for the wrong reason).
	var doc map[string]any
	p, _ := Package(PackageWatch)
	raw, _ := json.Marshal(p)
	_ = json.Unmarshal(raw, &doc)
	for _, k := range schema.Required {
		if _, ok := doc[k]; !ok {
			t.Fatalf("required key %q not produced by Entitlements", k)
		}
	}
}

// TestOverrideCannotEnableLive is compliance review #23: the merge
// result is schema-validated, so execution.live can never become true
// through an override, at any nesting or spelling.
func TestOverrideCannotEnableLive(t *testing.T) {
	base, _ := Package(PackageInstitution)
	for _, ov := range []string{
		`{"execution":{"live":true}}`,
		`{"execution":{"paper":true,"live":true}}`,
		`{"execution":{"live":1}}`,
		`{"Execution":{"live":true}}`,
		`{"live":true}`,
	} {
		_, err := Merge(base, []byte(ov))
		if err == nil {
			t.Fatalf("override %s accepted", ov)
		}
		if ov == `{"execution":{"live":true}}` && !errors.Is(err, ErrLiveExecution) {
			t.Fatalf("override %s: err = %v, want ErrLiveExecution", ov, err)
		}
	}
	// Resolve with a live override never yields a live document.
	_, err := Resolve(Input{OrgID: 7, PackageCode: PackageDesk, Override: []byte(`{"execution":{"live":true}}`), SubStatus: "active"})
	if !errors.Is(err, ErrLiveExecution) {
		t.Fatalf("Resolve live override err = %v", err)
	}
	// The resolver falls back to the bare package, still live=false.
	src := NewMapSource()
	src.Set(Input{OrgID: 7, PackageCode: PackageDesk, Override: []byte(`{"execution":{"live":true}}`), SubStatus: "active"})
	r := NewResolver(src)
	var logged error
	r.OnError = func(_ int64, err error) { logged = err }
	doc, err := r.For(context.Background(), 7)
	if err != nil || doc.Execution.Live || doc.PackageCode != PackageDesk {
		t.Fatalf("resolver fallback doc = %+v err = %v", doc, err)
	}
	if !errors.Is(logged, ErrLiveExecution) {
		t.Fatalf("resolver did not report the rejected override: %v", logged)
	}
}

func TestMergeOverrideDeepMergesAndValidates(t *testing.T) {
	base, _ := Package(PackageSignal)
	got, err := Merge(base, []byte(`{"rules":{"max_active":12},"venues":{"screener_max":8},"seats":{"max":2,"roles":["owner","viewer"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Rules.MaxActive != 12 || got.Rules.MinRefreshS != base.Rules.MinRefreshS || got.Venues.ScreenerMax != 8 ||
		got.Venues.TriangularMax != base.Venues.TriangularMax || got.Seats.Max != 2 || len(got.Seats.Roles) != 2 {
		t.Fatalf("merged = %+v", got)
	}
	for _, bad := range []string{
		`{"rules":{"bogus":1}}`,
		`{"rules":{"min_refresh_s":1}}`,
		`{"alerts":{"channels":["sms"]}}`,
		`{"auto_paper":{"max_size_quote":"1e5"}}`,
		`{"package_code":"platinum"}`,
		`{"schema_version":2}`,
		`{"status":{"read_only":false}}`,
		`[]`,
	} {
		if _, err := Merge(base, []byte(bad)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("override %s: err = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestResolveSubscriptionStates(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	trialEnd := now.Add(3 * 24 * time.Hour)
	expired := now.Add(-time.Hour)
	pastDueOld := now.Add(-8 * 24 * time.Hour)
	pastDueNew := now.Add(-2 * 24 * time.Hour)
	cases := []struct {
		name     string
		in       Input
		wantPkg  string
		readOnly bool
	}{
		{"trial running", Input{OrgID: 2, PackageCode: PackageOperator, TrialEndsAt: &trialEnd}, PackageOperator, false},
		{"trial expired -> watch", Input{OrgID: 2, PackageCode: PackageOperator, TrialEndsAt: &expired}, PackageWatch, false},
		{"active", Input{OrgID: 2, PackageCode: PackageDesk, SubStatus: "active"}, PackageDesk, false},
		{"past due inside grace", Input{OrgID: 2, PackageCode: PackageDesk, SubStatus: "past_due", PastDueSince: &pastDueNew}, PackageDesk, false},
		{"past due beyond grace", Input{OrgID: 2, PackageCode: PackageDesk, SubStatus: "past_due", PastDueSince: &pastDueOld}, PackageDesk, true},
		{"canceled -> watch", Input{OrgID: 2, PackageCode: PackageDesk, SubStatus: "canceled"}, PackageWatch, false},
		{"platform org never demoted", Input{OrgID: 1, PackageCode: PackageInstitution, SubStatus: "canceled"}, PackageInstitution, false},
	}
	for _, tc := range cases {
		tc.in.Now = now
		doc, err := Resolve(tc.in)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if doc.PackageCode != tc.wantPkg || doc.Status.Effective != tc.wantPkg || doc.Status.ReadOnly != tc.readOnly {
			t.Fatalf("%s: package %q effective %q read_only %v", tc.name, doc.PackageCode, doc.Status.Effective, doc.Status.ReadOnly)
		}
		if tc.readOnly && (doc.Alerts.PerDay != 0 || len(doc.AutoPaper.Strategies) != 0 || strings.Join(doc.API.Scopes, ",") != "read") {
			t.Fatalf("%s: read-only shape not applied: %+v", tc.name, doc)
		}
		if doc.Execution.Live {
			t.Fatalf("%s: live", tc.name)
		}
	}
}

func TestResolverCacheAndInvalidate(t *testing.T) {
	src := NewMapSource()
	src.Set(Input{OrgID: 3, PackageCode: PackageSignal, SubStatus: "active"})
	now := time.Unix(1_700_000_000, 0)
	r := NewResolver(src)
	r.Now = func() time.Time { return now }
	if doc, _ := r.For(context.Background(), 3); doc.PackageCode != PackageSignal {
		t.Fatal("first resolve")
	}
	src.Set(Input{OrgID: 3, PackageCode: PackageDesk, SubStatus: "active"})
	if doc, _ := r.For(context.Background(), 3); doc.PackageCode != PackageSignal {
		t.Fatal("cache should still serve signal")
	}
	r.Invalidate(3)
	if doc, _ := r.For(context.Background(), 3); doc.PackageCode != PackageDesk {
		t.Fatal("invalidate should refetch")
	}
	now = now.Add(61 * time.Second)
	src.Set(Input{OrgID: 3, PackageCode: PackageWatch})
	if doc, _ := r.For(context.Background(), 3); doc.PackageCode != PackageWatch {
		t.Fatal("ttl expiry should refetch")
	}
}

func TestLimitChecks(t *testing.T) {
	w, _ := Package(PackageWatch)
	s, _ := Package(PackageSignal)
	d, _ := Package(PackageDesk)
	var ex *Exceeded
	must := func(err error, key string) {
		t.Helper()
		if !errors.As(err, &ex) || ex.Key != key || !errors.Is(err, ErrExceeded) {
			t.Fatalf("err = %v, want Exceeded %s", err, key)
		}
	}
	must(w.CheckRuleCount(2), "rules.max_active")
	if err := w.CheckRuleCount(1); err != nil {
		t.Fatal(err)
	}
	must(w.CheckTemplateCount(3), "rules.templates_max")
	if err := d.CheckTemplateCount(100000); err != nil {
		t.Fatal("unlimited templates")
	}
	must(w.CheckRuleKind("carry"), "rules.kinds")
	must(w.CheckRefresh(10), "rules.min_refresh_s")
	must(w.CheckCooldown(10), "alerts.min_cooldown_s")
	must(w.CheckChannel("telegram"), "alerts.channels")
	must(w.CheckVenues([]string{"binance", "gate"}), "venues.screener_fixed")
	must(s.CheckVenues([]string{"a", "b", "c", "d", "e", "f", "g"}), "venues.screener_max")
	if err := d.CheckVenues([]string{"a", "b", "c", "d", "e", "f", "g"}); err != nil {
		t.Fatal("unlimited venues")
	}
	must(w.CheckAutoPaper("cross_venue_spot", decimal.NewFromInt(1)), "auto_paper.strategies")
	must(s.CheckAutoPaper("carry", decimal.NewFromInt(1)), "auto_paper.strategies")
	must(s.CheckAutoPaper("cross_venue_spot", decimal.RequireFromString("5000.01")), "auto_paper.max_size_quote")
	if err := s.CheckAutoPaper("cross_venue_spot", decimal.RequireFromString("5000.00")); err != nil {
		t.Fatal(err)
	}
	must(s.CheckOpenPositions(5), "auto_paper.max_open_positions")
	must(s.CheckSeat(1, "owner"), "seats.max")
	must(d.CheckSeat(1, "custom"), "seats.roles")
	must(s.CheckAPIScope("read"), "api.enabled")
	must(d.CheckAPIScope("paper:write"), "api.scopes")
	if cut := w.RetentionCutoff(time.Unix(86400*2, 0)); cut.Unix() != 86400 {
		t.Fatalf("retention cutoff = %v", cut)
	}
}

func TestDailyCounterAndRateLimiter(t *testing.T) {
	c := NewDailyCounter()
	day := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		if !c.Allow(2, 20, day) {
			t.Fatalf("alert %d refused", i)
		}
	}
	if c.Allow(2, 20, day) {
		t.Fatal("21st alert allowed")
	}
	if !c.Allow(3, 20, day) {
		t.Fatal("other org blocked")
	}
	if !c.Allow(2, 20, day.Add(24*time.Hour)) {
		t.Fatal("next day not reset")
	}

	l := NewRateLimiter()
	now := day
	for i := 0; i < 20; i++ {
		if ok, _ := l.Allow("k", 60, 20, now); !ok {
			t.Fatalf("burst request %d refused", i)
		}
	}
	ok, wait := l.Allow("k", 60, 20, now)
	if ok || wait < time.Second {
		t.Fatalf("over burst: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("k", 60, 20, now.Add(2*time.Second)); !ok {
		t.Fatal("refill after 2 s should allow one")
	}
	if ok, _ := l.Allow("k", 0, 0, now); ok {
		t.Fatal("rate 0 must refuse")
	}
}

func TestDailyCounterUnlimitedAndReset(t *testing.T) {
	c := NewDailyCounter()
	now := time.Date(2026, 8, 27, 23, 59, 0, 0, time.UTC)
	for i := 0; i < 1000; i++ {
		if !c.Allow(7, Unlimited, now) {
			t.Fatalf("Unlimited refused at %d", i)
		}
	}
	if c.Used(7, now) != 0 {
		t.Fatalf("Unlimited must not consume: used=%d", c.Used(7, now))
	}
	for i := 0; i < 3; i++ {
		if got, want := c.Allow(7, 2, now), i < 2; got != want {
			t.Fatalf("per_day=2 call %d: allow=%v, want %v", i, got, want)
		}
	}
	if c.Used(7, now) != 2 || c.Used(8, now) != 0 {
		t.Fatalf("used = %d/%d", c.Used(7, now), c.Used(8, now))
	}
	if !c.Allow(8, 1, now) {
		t.Fatal("another organisation has its own quota")
	}
	if c.Allow(7, 0, now) {
		t.Fatal("per_day=0 (read-only) must refuse")
	}
	// UTC midnight resets every organisation.
	next := now.Add(2 * time.Minute)
	if !c.Allow(7, 2, next) || c.Used(7, next) != 1 {
		t.Fatal("quota did not reset at UTC midnight")
	}
}
