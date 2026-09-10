package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/billing/affiliate"
	"github.com/cploutarchou/arb-chain-bot/internal/billing/paddle"
	"github.com/cploutarchou/arb-chain-bot/internal/entitlements"
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

func seedUser(t *testing.T, s *Store, id, email string, role auth.Role) {
	t.Helper()
	if _, err := s.Pool.Exec(context.Background(), `
		INSERT INTO users (id, email, display_name, password_hash, role, platform_admin)
		VALUES ($1, $2, $2, 'x', $3, $4)`, id, email, string(role), role == auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
}

func TestTenancyStoreAndPlatformAdmin(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ts := s.Tenancy()
	as := s.Auth()

	// Bootstrap admin: platform_admin, OWNER of organisation 1.
	hash, _ := auth.HashPassword("pw")
	if err := as.UpsertUser(ctx, auth.User{ID: "op", Email: "op@example.test", PasswordHash: hash, Role: auth.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	u, err := as.UserByEmail(ctx, "op@example.test")
	if err != nil || !u.PlatformAdmin {
		t.Fatalf("bootstrap admin = %+v err=%v", u, err)
	}
	tc, err := ts.ContextForUser(ctx, "op")
	if err != nil || tc.Org.ID != tenancy.PlatformOrgID || tc.Membership.Role != tenancy.RoleOwner || tc.Org.PackageCode != "institution" {
		t.Fatalf("platform context = %+v err=%v", tc, err)
	}
	// Session carries the flag.
	if err := as.CreateSession(ctx, auth.Session{Token: "tok", UserID: "op", CreatedAt: t0, ExpiresAt: t0.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if sess, err := as.SessionByToken(ctx, "tok"); err != nil || !sess.PlatformAdmin {
		t.Fatalf("session = %+v err=%v", sess, err)
	}
	// Console-created VIEWER: member of org 1, never platform_admin.
	if err := as.CreateUser(ctx, auth.User{ID: "v", Email: "v@example.test", PasswordHash: hash, Role: auth.RoleViewer, CreatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if u, _ := as.UserByID(ctx, "v"); u.PlatformAdmin {
		t.Fatal("viewer must not be platform admin")
	}
	if tc, _ := ts.ContextForUser(ctx, "v"); tc.Membership.Role != tenancy.RoleViewer {
		t.Fatalf("viewer membership = %+v", tc.Membership)
	}

	// Tenant organisation with an OWNER on a 14-day Operator trial.
	seedUser(t, s, "t-owner", "owner@tenant.test", auth.RoleAdmin)
	trialEnd := t0.Add(entitlements.TrialLength)
	org, err := ts.CreateOrg(ctx, tenancy.Org{Name: "Tenant A", PackageCode: "operator", Country: "CY", CustomerType: tenancy.CustomerBusiness, TrialEndsAt: &trialEnd}, "t-owner")
	if err != nil || org.ID < 2 || org.TrialEndsAt == nil {
		t.Fatalf("create org = %+v err=%v", org, err)
	}
	tc, err = ts.ContextForUser(ctx, "t-owner")
	if err != nil || tc.Org.ID != org.ID || tc.Membership.Role != tenancy.RoleOwner {
		t.Fatalf("tenant context = %+v err=%v", tc, err)
	}
	if _, err := ts.ContextForUser(ctx, "nobody"); !errors.Is(err, tenancy.ErrNoMembership) {
		t.Fatalf("no membership = %v", err)
	}

	// Risk acknowledgement.
	if err := ts.SetRiskAck(ctx, org.ID, "2026-08-27", t0, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	got, _ := ts.Org(ctx, org.ID)
	if got.RiskAckVersion != "2026-08-27" || got.RiskAckAt == nil || got.RiskAckIP != "203.0.113.7" {
		t.Fatalf("risk ack = %+v", got)
	}

	// Members: add, role change, last-owner guard, remove.
	seedUser(t, s, "t-viewer", "viewer@tenant.test", auth.RoleViewer)
	if err := ts.AddMember(ctx, tenancy.Membership{OrgID: org.ID, UserID: "t-viewer", Role: tenancy.RoleViewer}); err != nil {
		t.Fatal(err)
	}
	if err := ts.AddMember(ctx, tenancy.Membership{OrgID: org.ID, UserID: "t-viewer", Role: tenancy.RoleViewer}); !errors.Is(err, tenancy.ErrDuplicate) {
		t.Fatalf("duplicate = %v", err)
	}
	if err := ts.SetMemberRole(ctx, org.ID, "t-owner", tenancy.RoleAdmin); !errors.Is(err, tenancy.ErrLastOwner) {
		t.Fatalf("last owner demote = %v", err)
	}
	if err := ts.RemoveMember(ctx, org.ID, "t-owner"); !errors.Is(err, tenancy.ErrLastOwner) {
		t.Fatalf("last owner remove = %v", err)
	}
	if err := ts.SetMemberRole(ctx, org.ID, "t-viewer", tenancy.RoleOwner); err != nil {
		t.Fatal(err)
	}
	if err := ts.SetMemberRole(ctx, org.ID, "t-owner", tenancy.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	members, _ := ts.ListMembers(ctx, org.ID)
	if len(members) != 2 || members[0].UserID != "t-owner" || members[0].Email != "owner@tenant.test" || members[0].Role != tenancy.RoleAdmin {
		t.Fatalf("members = %+v", members)
	}

	// Override + entitlement input + resolve: a live override is
	// rejected by the resolver and the package limits apply.
	if err := ts.SetOverride(ctx, org.ID, []byte(`{"rules":{"max_active":30}}`)); err != nil {
		t.Fatal(err)
	}
	in, err := ts.EntitlementInput(ctx, org.ID)
	if err != nil || in.PackageCode != "operator" || in.SubStatus != "" || in.TrialEndsAt == nil {
		t.Fatalf("input = %+v err=%v", in, err)
	}
	in.Now = t0
	doc, err := entitlements.Resolve(in)
	if err != nil || doc.Rules.MaxActive != 30 || doc.Execution.Live {
		t.Fatalf("resolved = %+v err=%v", doc, err)
	}
	_ = ts.SetOverride(ctx, org.ID, []byte(`{"execution":{"live":true}}`))
	in, _ = ts.EntitlementInput(ctx, org.ID)
	if _, err := entitlements.Resolve(in); !errors.Is(err, entitlements.ErrLiveExecution) {
		t.Fatalf("live override = %v", err)
	}
}

// Acceptance (audit S3/P1-12): MembershipFor resolves exactly the
// (org, user) pair asked for — unlike ContextForUser, which picks one
// organisation for the caller — and reports ErrNoMembership once a
// membership is removed, which is what the API-key authentication path
// relies on to refuse a key whose owner lost their seat.
func TestTenancyMembershipFor(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ts := s.Tenancy()
	seedUser(t, s, "mf-owner", "mf-owner@example.test", auth.RoleAdmin)

	org, err := ts.CreateOrg(ctx, tenancy.Org{Name: "MF Org", PackageCode: "signal", RiskAckVersion: "1"}, "mf-owner")
	if err != nil {
		t.Fatal(err)
	}
	mem, err := ts.MembershipFor(ctx, org.ID, "mf-owner")
	if err != nil || mem.Role != tenancy.RoleOwner {
		t.Fatalf("MembershipFor = %+v err=%v", mem, err)
	}
	// Not a member of the platform organisation (only of org.ID).
	if _, err := ts.MembershipFor(ctx, tenancy.PlatformOrgID, "mf-owner"); !errors.Is(err, tenancy.ErrNoMembership) {
		t.Fatalf("platform membership = %v, want ErrNoMembership", err)
	}
	// An unknown organisation is also ErrNoMembership (no row either way).
	if _, err := ts.MembershipFor(ctx, 999_999, "mf-owner"); !errors.Is(err, tenancy.ErrUnknownOrg) && !errors.Is(err, tenancy.ErrNoMembership) {
		t.Fatalf("unknown org membership = %v", err)
	}

	// Add a second member, then remove them: MembershipFor must flip
	// from a live row to ErrNoMembership.
	seedUser(t, s, "mf-viewer", "mf-viewer@example.test", auth.RoleViewer)
	if err := ts.AddMember(ctx, tenancy.Membership{OrgID: org.ID, UserID: "mf-viewer", Role: tenancy.RoleViewer}); err != nil {
		t.Fatal(err)
	}
	if mem, err := ts.MembershipFor(ctx, org.ID, "mf-viewer"); err != nil || mem.Role != tenancy.RoleViewer {
		t.Fatalf("MembershipFor after add = %+v err=%v", mem, err)
	}
	if err := ts.RemoveMember(ctx, org.ID, "mf-viewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.MembershipFor(ctx, org.ID, "mf-viewer"); !errors.Is(err, tenancy.ErrNoMembership) {
		t.Fatalf("MembershipFor after remove = %v, want ErrNoMembership", err)
	}
}

// TestScreenerOrgScoping: a user of organisation B cannot read, update
// or delete organisation A's rules/templates/events/paper rows; an
// unscoped (engine) context sees everything and events inherit the
// rule's organisation.
func TestScreenerOrgScoping(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ts := s.Tenancy()
	seedUser(t, s, "ua", "a@x.test", auth.RoleAdmin)
	seedUser(t, s, "ub", "b@x.test", auth.RoleAdmin)
	orgA, _ := ts.CreateOrg(ctx, tenancy.Org{Name: "A", PackageCode: "desk"}, "ua")
	orgB, _ := ts.CreateOrg(ctx, tenancy.Org{Name: "B", PackageCode: "desk"}, "ub")
	ctxA, ctxB := tenancy.WithOrg(ctx, orgA.ID), tenancy.WithOrg(ctx, orgB.ID)

	rules := s.ScreenerRules()
	min := d("10")
	ruleA := screener.Rule{ID: "rule-a", Name: "A spread", Enabled: true, Kind: screener.RuleKindSpread, MinSpreadBps: &min,
		BuyVenues: []screener.Venue{screener.VenueBinance}, SellVenues: []screener.Venue{screener.VenueOKX}, Quotes: []string{"USDT"}, CooldownS: 60}
	if _, err := rules.InsertRule(ctxA, ruleA, "ua"); err != nil {
		t.Fatal(err)
	}
	ruleB := ruleA
	ruleB.ID, ruleB.Name = "rule-b", "B spread"
	if _, err := rules.InsertRule(ctxB, ruleB, "ub"); err != nil {
		t.Fatal(err)
	}
	if got, _ := rules.ListRules(ctxB); len(got) != 1 || got[0].ID != "rule-b" {
		t.Fatalf("B lists = %+v", got)
	}
	if _, err := rules.GetRule(ctxB, "rule-a"); !errors.Is(err, screener.ErrNotFound) {
		t.Fatalf("B reads A's rule: %v", err)
	}
	if _, err := rules.UpdateRule(ctxB, ruleA, "ub"); !errors.Is(err, screener.ErrNotFound) {
		t.Fatalf("B updates A's rule: %v", err)
	}
	if err := rules.DeleteRule(ctxB, "rule-a"); !errors.Is(err, screener.ErrNotFound) {
		t.Fatalf("B deletes A's rule: %v", err)
	}
	if got, _ := rules.ListRules(ctx); len(got) != 2 {
		t.Fatalf("engine (unscoped) sees %d rules, want 2", len(got))
	}
	if id, _ := ts.OrgOfRule(ctx, "rule-a"); id != orgA.ID {
		t.Fatalf("OrgOfRule = %d", id)
	}

	// Events inserted by the engine (no scope) inherit the rule's org.
	events := s.ScreenerEvents()
	if err := events.InsertEvent(ctx, screener.Event{ID: "ev-a", RuleID: "rule-a", Kind: screener.RuleKindSpread, Base: "BTC", Quote: "USDT", OpenedAt: t0, PeakNetBps: "12"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := events.ListEvents(ctxB, "", 10); len(got) != 0 {
		t.Fatalf("B sees A's events: %+v", got)
	}
	if got, _ := events.ListEvents(ctxA, "", 10); len(got) != 1 {
		t.Fatalf("A does not see its event: %+v", got)
	}
	if n, _ := events.CountEvents(ctxB, ""); n != 0 {
		t.Fatalf("B count = %d", n)
	}

	// Templates are per user AND per org.
	tpl := s.ScreenerTemplates()
	if _, err := tpl.InsertTemplate(ctxA, screener.Template{ID: "tpl-a", UserID: "ua", Name: "mine", Filters: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := tpl.DeleteTemplate(ctxB, "ua", "tpl-a"); !errors.Is(err, screener.ErrNotFound) {
		t.Fatalf("B deletes A's template: %v", err)
	}

	// Paper balances are per org.
	paper := s.ScreenerPaper()
	if err := paper.UpsertBalance(ctxA, screener.VenueBinance, "USDT", d("100"), t0); err != nil {
		t.Fatal(err)
	}
	if err := paper.UpsertBalance(ctxB, screener.VenueBinance, "USDT", d("5"), t0); err != nil {
		t.Fatal(err)
	}
	if got, _ := paper.ListBalances(ctxB); len(got) != 1 || !got[0].Balance.Equal(d("5")) {
		t.Fatalf("B balances = %+v", got)
	}
}

func TestBillingStoreIdempotency(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	b := s.Billing()
	seedUser(t, s, "u", "u@x.test", auth.RoleAdmin)
	org, _ := s.Tenancy().CreateOrg(ctx, tenancy.Org{Name: "T", PackageCode: "watch"}, "u")

	ev := paddle.Event{EventID: "evt_x", EventType: "subscription.created", OccurredAt: t0, Raw: []byte(`{"event_id":"evt_x"}`)}
	fresh, err := b.RecordEvent(ctx, ev)
	if err != nil || !fresh {
		t.Fatalf("first record fresh=%v err=%v", fresh, err)
	}
	// Not yet processed: a retry reprocesses.
	if fresh, _ := b.RecordEvent(ctx, ev); !fresh {
		t.Fatal("unprocessed retry must be fresh")
	}
	if err := b.MarkProcessed(ctx, "evt_x", t0); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := b.RecordEvent(ctx, ev); fresh {
		t.Fatal("processed replay must not be fresh")
	}

	if err := b.SetPrice(ctx, "pri_1", "signal", "month"); err != nil {
		t.Fatal(err)
	}
	if code, interval, ok, _ := b.PackageForPrice(ctx, "pri_1"); !ok || code != "signal" || interval != "month" {
		t.Fatalf("price = %s %s %v", code, interval, ok)
	}
	end := t0.Add(30 * 24 * time.Hour)
	sub := paddle.Subscription{OrgID: org.ID, CustomerID: "ctm_1", SubscriptionID: "sub_1", PriceID: "pri_1", Status: paddle.StatusActive, CurrentPeriodEnd: &end, UpdatedAt: t0}
	if err := b.UpsertSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	pd := t0.Add(time.Hour)
	sub.Status, sub.PastDueSince, sub.CustomerID = paddle.StatusPastDue, &pd, ""
	if err := b.UpsertSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	got, ok, _ := b.SubscriptionByPaddleID(ctx, "sub_1")
	if !ok || got.CustomerID != "ctm_1" || got.Status != paddle.StatusPastDue || got.PastDueSince == nil {
		t.Fatalf("mirror = %+v", got)
	}
	in, _ := s.Tenancy().EntitlementInput(ctx, org.ID)
	if in.SubStatus != paddle.StatusPastDue || in.PastDueSince == nil {
		t.Fatalf("input = %+v", in)
	}
	// Whole-service round trip over pgx: replayed second delivery.
	svc := &paddle.Service{Store: b, Packages: s.Tenancy(), Now: func() time.Time { return t0 }}
	body := []byte(`{"event_id":"evt_y","event_type":"subscription.canceled","occurred_at":"2026-08-27T12:00:00Z","data":{"id":"sub_1","status":"canceled","customer_id":"ctm_1","items":[{"status":"active","price":{"id":"pri_1"}}]}}`)
	e := paddle.Event{Raw: body}
	_ = json.Unmarshal(body, &e)
	if replayed, err := svc.Process(ctx, e); err != nil || replayed {
		t.Fatalf("first = replayed %v err %v", replayed, err)
	}
	if replayed, err := svc.Process(ctx, e); err != nil || !replayed {
		t.Fatalf("second = replayed %v err %v", replayed, err)
	}
	o, _ := s.Tenancy().Org(ctx, org.ID)
	if got, _, _ := b.Subscription(ctx, org.ID); got.Status != paddle.StatusCanceled || o.PackageCode != "watch" {
		t.Fatalf("after cancel: sub=%+v org=%+v", got, o)
	}
}

func TestAffiliateLedgerStore(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seedUser(t, s, "ua", "a@x.test", auth.RoleAdmin)
	seedUser(t, s, "ub", "b@x.test", auth.RoleAdmin)
	affOrg, _ := s.Tenancy().CreateOrg(ctx, tenancy.Org{Name: "Affiliate", PackageCode: "signal"}, "ua")
	referred, _ := s.Tenancy().CreateOrg(ctx, tenancy.Org{Name: "Referred", PackageCode: "operator"}, "ub")
	if _, err := s.Pool.Exec(ctx, `INSERT INTO affiliate_accounts (id, org_id, code) VALUES ('aff-1', $1, 'CODE1')`, affOrg.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE organisations SET referred_by = 'aff-1' WHERE id = $1`, referred.ID); err != nil {
		t.Fatal(err)
	}
	a := s.Affiliate()
	if _, err := a.Referral(ctx, affOrg.ID); !errors.Is(err, affiliate.ErrNoReferral) {
		t.Fatalf("unreferred = %v", err)
	}
	ref, err := a.Referral(ctx, referred.ID)
	if err != nil || ref.AffiliateID != "aff-1" || ref.AffiliateOrgID != affOrg.ID || ref.SelfReferral || ref.PackageCode != "operator" {
		t.Fatalf("referral = %+v err=%v", ref, err)
	}
	n := 0
	svc := &affiliate.Service{Terms: affiliate.DefaultTerms, Ledger: a, Referral: a.Referral, IDGen: func() string { n++; return "led-" + itoa(n) }}
	for i := 0; i < 2; i++ { // webhook retry accrues once
		if err := svc.OnTransaction(ctx, referred.ID, "txn_9", "69.30", t0); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := a.Entries(ctx, "aff-1")
	if len(entries) != 1 || !entries[0].AmountUSD.Equal(d("13.86")) || entries[0].MaturesAt == nil {
		t.Fatalf("entries = %+v", entries)
	}
	if err := svc.OnRefund(ctx, "txn_9", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	entries, _ = a.Entries(ctx, "aff-1")
	bal := affiliate.DefaultTerms.Compute(entries, t0.Add(60*24*time.Hour))
	if len(entries) != 2 || !bal.Matured.IsZero() || !bal.Reversed.Equal(d("-13.86")) {
		t.Fatalf("after refund: entries=%d balance=%+v", len(entries), bal)
	}
}

func itoa(n int) string { return decimalItoa(n) }

func decimalItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
