package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/report"
	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

// TestTenantMembershipOutranksPlatformMembership (security audit S4):
// a platform membership written first (as CreateUser does for every
// console account) must not shadow the tenant organisation the user is
// added to later. ContextsForUser lists the tenant first and the
// platform last; ListOrgIDs sees both organisations.
func TestTenantMembershipOutranksPlatformMembership(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ts := s.Tenancy()
	seedUser(t, s, "dual", "dual@example.test", auth.RoleOperator)
	if err := ts.AddMember(ctx, tenancy.Membership{OrgID: tenancy.PlatformOrgID, UserID: "dual", Role: tenancy.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	org, err := ts.CreateOrg(ctx, tenancy.Org{Name: "Later Tenant", PackageCode: "signal"}, "dual")
	if err != nil {
		t.Fatal(err)
	}
	cs, err := ts.ContextsForUser(ctx, "dual")
	if err != nil || len(cs) != 2 || cs[0].Org.ID != org.ID || cs[0].Membership.Role != tenancy.RoleOwner || cs[1].Org.ID != tenancy.PlatformOrgID {
		t.Fatalf("contexts = %+v err=%v", cs, err)
	}
	if tc, err := ts.ContextForUser(ctx, "dual"); err != nil || tc.Org.ID != org.ID {
		t.Fatalf("default context = %+v err=%v", tc, err)
	}
	if _, err := ts.ContextForUser(ctx, "nobody"); !errors.Is(err, tenancy.ErrNoMembership) {
		t.Fatalf("no membership = %v", err)
	}
	ids, err := ts.ListOrgIDs(ctx)
	if err != nil || len(ids) < 2 || ids[0] != tenancy.PlatformOrgID {
		t.Fatalf("org ids = %v err=%v", ids, err)
	}
	found := false
	for _, id := range ids {
		found = found || id == org.ID
	}
	if !found {
		t.Fatalf("org ids %v lack %d", ids, org.ID)
	}
}

// TestScreenerSettingsAndReportsOrgScoping (security S7, database
// D1/D2, migration 000017): each organisation has its own active
// settings version and its own report rows; an unscoped (engine)
// caller reads the platform's document; one organisation's versions
// and report ids are invisible to another; the schema itself allows
// one active row per organisation.
func TestScreenerSettingsAndReportsOrgScoping(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ts := s.Tenancy()
	seedUser(t, s, "ua", "a@x.test", auth.RoleAdmin)
	seedUser(t, s, "ub", "b@x.test", auth.RoleAdmin)
	orgA, err := ts.CreateOrg(ctx, tenancy.Org{Name: "A", PackageCode: "desk"}, "ua")
	if err != nil {
		t.Fatal(err)
	}
	orgB, err := ts.CreateOrg(ctx, tenancy.Org{Name: "B", PackageCode: "desk"}, "ub")
	if err != nil {
		t.Fatal(err)
	}
	ctxA, ctxB := tenancy.WithOrg(ctx, orgA.ID), tenancy.WithOrg(ctx, orgB.ID)

	ss := s.ScreenerSettings()
	seed := screener.Defaults()
	payload, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	platformV, _, err := ss.Insert(ctx, "", payload, nil, 0) // unscoped: the platform organisation
	if err != nil {
		t.Fatal(err)
	}
	aV1, _, err := ss.Insert(ctxA, "ua", payload, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	changed := seed.Clone()
	changed.MinLiquidityQuote = d("4242")
	payload2, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	aV2, _, err := ss.Insert(ctxA, "ua", payload2, nil, aV1)
	if err != nil {
		t.Fatal(err)
	}
	// A's second version deactivated A's first only.
	if got, ok, err := ss.Active(ctx); err != nil || !ok || got.Version != platformV {
		t.Fatalf("platform active = %+v ok=%v err=%v", got, ok, err)
	}
	if got, ok, err := ss.Active(ctxA); err != nil || !ok || got.Version != aV2 || !got.Settings.MinLiquidityQuote.Equal(d("4242")) {
		t.Fatalf("A active = %+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, err := ss.Active(ctxB); err != nil || ok {
		t.Fatalf("B active before any write: ok=%v err=%v", ok, err)
	}
	if _, err := ss.Get(ctxB, aV2); !errors.Is(err, screener.ErrNotFound) {
		t.Fatalf("B reads A's version: %v", err)
	}
	if got, err := ss.Get(ctxA, aV2); err != nil || got.ParentVer != aV1 {
		t.Fatalf("A reads own version = %+v err=%v", got, err)
	}
	if list, err := ss.List(ctxA, 10); err != nil || len(list) != 2 || !list[0].Active || list[0].Version != aV2 {
		t.Fatalf("A list = %+v err=%v", list, err)
	}
	if list, err := ss.List(ctxB, 10); err != nil || len(list) != 0 {
		t.Fatalf("B list = %+v err=%v", list, err)
	}
	if list, err := ss.List(ctx, 10); err != nil || len(list) != 3 {
		t.Fatalf("unscoped list = %+v err=%v", list, err)
	}
	var active int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM screener_settings WHERE active`).Scan(&active); err != nil || active != 2 {
		t.Fatalf("active rows = %d err=%v (want one per organisation)", active, err)
	}
	// The per-organisation unique index refuses a second active row in
	// one organisation.
	if _, err := s.Pool.Exec(ctx, `INSERT INTO screener_settings (active, payload, org_id) VALUES (TRUE, $1, $2)`, payload, orgA.ID); err == nil {
		t.Fatal("two active rows in one organisation were accepted")
	}

	st := s.ScreenerReports()
	p := report.Payload{Window: report.Window{Label: report.PeriodDay, Start: t0, End: t0.Add(24 * time.Hour)},
		Strategy: screener.StrategyCrossVenueSpot, GeneratedAt: t0.Add(24 * time.Hour), Model: report.Model}
	p.Gate = report.Checklist(screener.StrategyCrossVenueSpot, p.Stats)
	p.GateTotal = len(p.Gate)
	rep := report.Report{ID: "rep-a", PeriodStart: p.Window.Start, PeriodEnd: p.Window.End, Strategy: screener.StrategyCrossVenueSpot,
		Payload: p, Markdown: report.Markdown(p), CreatedAt: t0.Add(25 * time.Hour)}
	if err := st.InsertReport(ctxA, rep); err != nil {
		t.Fatal(err)
	}
	rep.ID, rep.CreatedAt = "rep-b", t0.Add(26*time.Hour)
	if err := st.InsertReport(ctxB, rep); err != nil {
		t.Fatal(err)
	}
	if list, err := st.ListReports(ctxA, 10); err != nil || len(list) != 1 || list[0].ID != "rep-a" {
		t.Fatalf("A reports = %+v err=%v", list, err)
	}
	if list, err := st.ListReports(ctxB, 10); err != nil || len(list) != 1 || list[0].ID != "rep-b" {
		t.Fatalf("B reports = %+v err=%v", list, err)
	}
	if _, err := st.GetReport(ctxB, "rep-a"); !errors.Is(err, screener.ErrNotFound) {
		t.Fatalf("B reads A's report: %v", err)
	}
	if got, err := st.GetReport(ctxA, "rep-a"); err != nil || got.Markdown == "" {
		t.Fatalf("A reads own report = %+v err=%v", got, err)
	}
	if list, err := st.ListReports(ctx, 10); err != nil || len(list) != 2 {
		t.Fatalf("unscoped reports = %+v err=%v", list, err)
	}
}
