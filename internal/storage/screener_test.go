package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

func TestScreenerSettingsRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ss := s.ScreenerSettings()

	if _, ok, err := ss.Active(ctx); err != nil || ok {
		t.Fatalf("empty table Active = ok=%v err=%v", ok, err)
	}

	seed := screener.Defaults()
	payload, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	v1, _, err := ss.Insert(ctx, "", payload, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, role)
		VALUES ('u1','screener@example.test','Screener','x','ADMIN')`); err != nil {
		t.Fatal(err)
	}
	next := seed.Clone()
	v := next.Venues[screener.VenueBinance]
	v.SpotTakerBps = d("7")
	next.Venues[screener.VenueBinance] = v
	payload2, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	v2, _, err := ss.Insert(ctx, "u1", payload2, nil, v1)
	if err != nil {
		t.Fatal(err)
	}
	if v2 <= v1 {
		t.Fatalf("versions not increasing: %d then %d", v1, v2)
	}

	active, ok, err := ss.Active(ctx)
	if err != nil || !ok {
		t.Fatalf("Active = ok=%v err=%v", ok, err)
	}
	if active.Version != v2 || active.CreatedBy != "u1" || active.ParentVer != v1 {
		t.Fatalf("active snapshot = %+v", active)
	}
	if !active.Settings.Venues[screener.VenueBinance].SpotTakerBps.Equal(d("7")) {
		t.Fatalf("active spot_taker_bps = %s", active.Settings.Venues[screener.VenueBinance].SpotTakerBps)
	}

	got1, err := ss.Get(ctx, v1)
	if err != nil {
		t.Fatal(err)
	}
	if !got1.Settings.Venues[screener.VenueBinance].SpotTakerBps.Equal(d("10")) {
		t.Fatalf("v1 spot_taker_bps = %s, want 10 (immutable history)", got1.Settings.Venues[screener.VenueBinance].SpotTakerBps)
	}

	if _, err := ss.Get(ctx, 9999); !errors.Is(err, screener.ErrNotFound) {
		t.Fatalf("missing version error = %v, want ErrNotFound", err)
	}

	list, err := ss.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !list[0].Active || list[0].Version != v2 {
		t.Fatalf("List = %+v, want [v2(active), v1]", list)
	}
}

func TestScreenerRulesCRUD(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	rs := s.ScreenerRules()

	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, role)
		VALUES ('u-creator','creator@example.test','Creator','x','ADMIN'),
		       ('u-editor','editor@example.test','Editor','x','ADMIN')`); err != nil {
		t.Fatal(err)
	}

	bps := d("50")
	r := screener.Rule{
		ID: "rule-1", Name: "BTC cross", Enabled: true, Kind: screener.RuleKindSpread,
		MinSpreadBps: &bps, MinLiquidityQuote: d("1000"), MinLifetimeS: 10,
		BuyVenues: []screener.Venue{screener.VenueBinance}, SellVenues: []screener.Venue{screener.VenueOKX},
		CooldownS: 60,
	}
	if _, err := rs.InsertRule(ctx, r, "u-creator"); err != nil {
		t.Fatal(err)
	}
	got, err := rs.GetRule(ctx, "rule-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "BTC cross" || got.MinSpreadBps == nil || !got.MinSpreadBps.Equal(d("50")) {
		t.Fatalf("GetRule = %+v", got)
	}
	assertUpdatedBy(t, s, "rule-1", "u-creator")

	list, err := rs.ListRules(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListRules = %+v err=%v", list, err)
	}

	got.Enabled = false
	got.MinLifetimeS = 20
	if _, err := rs.UpdateRule(ctx, got, "u-editor"); err != nil {
		t.Fatal(err)
	}
	got2, err := rs.GetRule(ctx, "rule-1")
	if err != nil || got2.Enabled || got2.MinLifetimeS != 20 {
		t.Fatalf("after update = %+v err=%v", got2, err)
	}
	assertUpdatedBy(t, s, "rule-1", "u-editor")

	if err := rs.DeleteRule(ctx, "rule-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.GetRule(ctx, "rule-1"); !errors.Is(err, screener.ErrNotFound) {
		t.Fatalf("GetRule after delete = %v, want ErrNotFound", err)
	}
	if err := rs.DeleteRule(ctx, "rule-1"); !errors.Is(err, screener.ErrNotFound) {
		t.Fatalf("DeleteRule twice = %v, want ErrNotFound", err)
	}
}

// assertUpdatedBy is the T-067 review-fix regression: screener_rules.
// updated_by must actually be populated by InsertRule/UpdateRule, not
// left permanently NULL.
func assertUpdatedBy(t *testing.T, s *Store, ruleID, want string) {
	t.Helper()
	var got *string
	if err := s.Pool.QueryRow(context.Background(), `SELECT updated_by FROM screener_rules WHERE id = $1`, ruleID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != want {
		t.Fatalf("screener_rules.updated_by = %v, want %q", got, want)
	}
}

func TestScreenerEventsListAndInsert(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	es := s.ScreenerEvents()

	t0 := time.Unix(1_800_000_000, 0).UTC()
	ev := screener.Event{
		ID: "ev-1", RuleID: "rule-1", Kind: screener.RuleKindSpread,
		Base: "BTC", Quote: "USDT", BuyVenue: screener.VenueBinance, SellVenue: screener.VenueOKX,
		OpenedAt: t0, LifetimeS: 30, PeakNetBps: "79.9",
	}
	if err := es.InsertEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	// Idempotent re-insert.
	if err := es.InsertEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	other := screener.Event{ID: "ev-2", RuleID: "rule-2", Kind: screener.RuleKindCarry, Base: "ETH", Quote: "USDT", OpenedAt: t0.Add(time.Minute), PeakNetBps: "10"}
	if err := es.InsertEvent(ctx, other); err != nil {
		t.Fatal(err)
	}

	all, err := es.ListEvents(ctx, "", 10)
	if err != nil || len(all) != 2 {
		t.Fatalf("ListEvents(all) = %+v err=%v", all, err)
	}
	if all[0].ID != "ev-2" { // newest first
		t.Fatalf("ListEvents order = %+v, want ev-2 first", all)
	}
	filtered, err := es.ListEvents(ctx, "rule-1", 10)
	if err != nil || len(filtered) != 1 || filtered[0].ID != "ev-1" {
		t.Fatalf("ListEvents(rule-1) = %+v err=%v", filtered, err)
	}
	if filtered[0].BuyVenue != screener.VenueBinance || filtered[0].PeakNetBps != "79.9" {
		t.Fatalf("event row = %+v", filtered[0])
	}
}

// TestScreenerEventsDeliveredRoundTrip covers T-086's per-channel
// delivery outcomes: seeded at insert (telegram sent synchronously,
// email/webhook pending) and patched afterwards by SetEventDelivered
// (the async dispatch worker's write), without disturbing the other
// channels' entries.
func TestScreenerEventsDeliveredRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	es := s.ScreenerEvents()

	t0 := time.Unix(1_800_000_100, 0).UTC()
	ev := screener.Event{
		ID: "ev-delivered", RuleID: "rule-delivered", Kind: screener.RuleKindSpread,
		Base: "BTC", Quote: "USDT", OpenedAt: t0, PeakNetBps: "10",
		Delivered: map[string]screener.DeliveryOutcome{
			"telegram": {Status: "sent", At: t0},
			"email":    {Status: "pending", At: t0},
			"webhook":  {Status: "pending", At: t0},
		},
	}
	if err := es.InsertEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	got, err := es.ListEvents(ctx, "rule-delivered", 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("ListEvents = %+v err=%v", got, err)
	}
	if got[0].Delivered["telegram"].Status != "sent" || got[0].Delivered["email"].Status != "pending" {
		t.Fatalf("seeded delivered = %+v", got[0].Delivered)
	}

	// The async worker patches email to "sent" without touching webhook.
	if err := es.SetEventDelivered(ctx, "ev-delivered", "email", screener.DeliveryOutcome{Status: "sent", At: t0.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := es.SetEventDelivered(ctx, "ev-delivered", "webhook", screener.DeliveryOutcome{Status: "failed", Reason: "endpoint returned 500", At: t0.Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	got, err = es.ListEvents(ctx, "rule-delivered", 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("ListEvents = %+v err=%v", got, err)
	}
	d := got[0].Delivered
	if d["telegram"].Status != "sent" || d["email"].Status != "sent" || d["webhook"].Status != "failed" || d["webhook"].Reason != "endpoint returned 500" {
		t.Fatalf("patched delivered = %+v", d)
	}

	// Unknown event id fails closed.
	if err := es.SetEventDelivered(ctx, "no-such-event", "email", screener.DeliveryOutcome{Status: "sent", At: t0}); err != screener.ErrNotFound {
		t.Fatalf("SetEventDelivered on unknown id = %v", err)
	}
}

func TestScreenerTemplatesPerUser(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ts := s.ScreenerTemplates()

	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, role)
		VALUES ('u1','templ@example.test','Templ','x','VIEWER')`); err != nil {
		t.Fatal(err)
	}

	tpl := screener.Template{ID: "tpl-1", UserID: "u1", Name: "My filter", Filters: json.RawMessage(`{"min_spread_bps":50}`)}
	created, err := ts.InsertTemplate(ctx, tpl)
	if err != nil {
		t.Fatal(err)
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("InsertTemplate did not set created_at")
	}

	list, err := ts.ListTemplates(ctx, "u1")
	if err != nil || len(list) != 1 || list[0].Name != "My filter" {
		t.Fatalf("ListTemplates = %+v err=%v", list, err)
	}

	// Another user's list stays empty (per-user scoping).
	other, err := ts.ListTemplates(ctx, "nobody")
	if err != nil || len(other) != 0 {
		t.Fatalf("ListTemplates(nobody) = %+v err=%v", other, err)
	}

	// Delete by a different user_id is refused (ownership check).
	if err := ts.DeleteTemplate(ctx, "nobody", "tpl-1"); !errors.Is(err, screener.ErrNotFound) {
		t.Fatalf("DeleteTemplate(wrong user) = %v, want ErrNotFound", err)
	}
	if err := ts.DeleteTemplate(ctx, "u1", "tpl-1"); err != nil {
		t.Fatal(err)
	}
}

func TestScreenerFundingUpsertAndList(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	fs := s.ScreenerFunding()

	t0 := time.Unix(1_800_000_000, 0).UTC()
	if err := fs.UpsertFunding(ctx, screener.VenueBinance, "BTC", t0, "0.0001"); err != nil {
		t.Fatal(err)
	}
	// Idempotent re-poll of the same interval.
	if err := fs.UpsertFunding(ctx, screener.VenueBinance, "BTC", t0, "0.0002"); err != nil {
		t.Fatal(err)
	}
	if err := fs.UpsertFunding(ctx, screener.VenueBinance, "BTC", t0.Add(8*time.Hour), "0.0003"); err != nil {
		t.Fatal(err)
	}
	if err := fs.UpsertFunding(ctx, screener.VenueOKX, "BTC", t0, "0.00005"); err != nil {
		t.Fatal(err)
	}

	series, err := fs.ListFunding(ctx, "BTC", nil, t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 2 {
		t.Fatalf("series = %+v, want 2 (binance, okx)", series)
	}
	var binanceSeries *screener.FundingSeries
	for i := range series {
		if series[i].Venue == screener.VenueBinance {
			binanceSeries = &series[i]
		}
	}
	if binanceSeries == nil || len(binanceSeries.Points) != 2 {
		t.Fatalf("binance series = %+v", binanceSeries)
	}
	if binanceSeries.Points[0].Rate != "0.0001" {
		t.Fatalf("first funding point rate = %s, want 0.0001 (ON CONFLICT DO NOTHING kept the original)", binanceSeries.Points[0].Rate)
	}

	venueFiltered, err := fs.ListFunding(ctx, "BTC", []screener.Venue{screener.VenueOKX}, t0.Add(-time.Hour))
	if err != nil || len(venueFiltered) != 1 || venueFiltered[0].Venue != screener.VenueOKX {
		t.Fatalf("venue-filtered series = %+v err=%v", venueFiltered, err)
	}
}
