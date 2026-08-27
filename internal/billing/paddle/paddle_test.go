package paddle

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/entitlements"
)

const testSecret = "pdl_ntfset_test_secret_0123456789abcdef"

var t0 = time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

func TestVerifySignature(t *testing.T) {
	body := []byte(`{"event_id":"evt_1","event_type":"subscription.created","data":{}}`)
	h := Sign(body, testSecret, t0)
	if !strings.HasPrefix(h, "ts=") || !strings.Contains(h, ";h1=") {
		t.Fatalf("header shape: %s", h)
	}
	if err := VerifySignature(h, body, testSecret, t0.Add(time.Minute)); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if err := VerifySignature(h, []byte(`{"tampered":true}`), testSecret, t0); err != ErrBadSignature {
		t.Fatalf("tampered body: %v", err)
	}
	if err := VerifySignature(h, body, "other-secret-0123456789abcdef", t0); err != ErrBadSignature {
		t.Fatalf("wrong secret: %v", err)
	}
	if err := VerifySignature(h, body, testSecret, t0.Add(6*time.Minute)); err != ErrStaleSignature {
		t.Fatalf("stale: %v", err)
	}
	if err := VerifySignature("h1=abcd", body, testSecret, t0); err == nil {
		t.Fatal("missing ts accepted")
	}
	if err := VerifySignature(h, body, "", t0); err != ErrNotConfigured {
		t.Fatalf("empty secret: %v", err)
	}
	// Rotation: the second h1 matches.
	rot := strings.Replace(h, ";h1=", ";h1=deadbeef;h1=", 1)
	if err := VerifySignature(rot, body, testSecret, t0); err != nil {
		t.Fatalf("rotation: %v", err)
	}
}

type fakePackages struct {
	mu   sync.Mutex
	pkgs map[int64]string
	log  []string
}

func (f *fakePackages) SetPackage(_ context.Context, orgID int64, code string, _ *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pkgs[orgID] = code
	f.log = append(f.log, fmt.Sprintf("%d=%s", orgID, code))
	return nil
}

func (f *fakePackages) PackageOf(_ context.Context, orgID int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.pkgs[orgID]; ok {
		return p, nil
	}
	return entitlements.PackageWatch, nil
}

func newService(t *testing.T) (*Service, *MemoryStore, *fakePackages) {
	t.Helper()
	store := NewMemoryStore()
	_ = store.SetPrice(context.Background(), "pri_signal_m", "signal", "month")
	_ = store.SetPrice(context.Background(), "pri_operator_m", "operator", "month")
	_ = store.SetPrice(context.Background(), "pri_desk_m", "desk", "month")
	pk := &fakePackages{pkgs: map[int64]string{}}
	now := t0
	svc := &Service{
		Store: store, Packages: pk, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           func() time.Time { return now },
		WebhookSecret: func(context.Context) string { return testSecret },
	}
	return svc, store, pk
}

func subEvent(id, typ, subID, status, price string, occurred time.Time, orgID int64, extra map[string]any) []byte {
	data := map[string]any{
		"id": subID, "status": status, "customer_id": "ctm_1",
		"items":                  []map[string]any{{"status": "active", "price": map[string]any{"id": price}}},
		"current_billing_period": map[string]any{"starts_at": occurred.Format(time.RFC3339), "ends_at": occurred.Add(30 * 24 * time.Hour).Format(time.RFC3339)},
		"custom_data":            map[string]any{"org_id": fmt.Sprint(orgID)},
	}
	for k, v := range extra {
		data[k] = v
	}
	raw, _ := json.Marshal(map[string]any{"event_id": id, "event_type": typ, "occurred_at": occurred.Format(time.RFC3339), "data": data})
	return raw
}

func post(t *testing.T, h http.Handler, body []byte, sig string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/billing/webhook", strings.NewReader(string(body)))
	if sig != "" {
		req.Header.Set("Paddle-Signature", sig)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestWebhookSignatureAndIdempotency: an unsigned or forged post is
// refused with a non-2xx and stores nothing; the same event delivered
// twice is applied once and acknowledged both times.
func TestWebhookSignatureAndIdempotency(t *testing.T) {
	svc, store, pk := newService(t)
	body := subEvent("evt_1", "subscription.activated", "sub_1", "active", "pri_signal_m", t0, 2, nil)

	if rec := post(t, svc, body, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned = %d", rec.Code)
	}
	if rec := post(t, svc, body, Sign(body, "wrong-secret-0123456789abcdef", t0)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("forged = %d", rec.Code)
	}
	if store.Events() != 0 {
		t.Fatal("rejected deliveries must not be stored")
	}

	calls := 0
	svc.Invalidate = func(int64) { calls++ }
	rec := post(t, svc, body, Sign(body, testSecret, t0))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"replayed":false`) {
		t.Fatalf("first = %d %s", rec.Code, rec.Body.String())
	}
	rec = post(t, svc, body, Sign(body, testSecret, t0.Add(time.Minute)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"replayed":true`) {
		t.Fatalf("replay = %d %s", rec.Code, rec.Body.String())
	}
	if calls != 1 || len(pk.log) != 1 || pk.pkgs[2] != "signal" || store.Events() != 1 {
		t.Fatalf("replay had side effects: invalidate=%d packages=%v events=%d", calls, pk.log, store.Events())
	}
	sub, ok, _ := store.Subscription(context.Background(), 2)
	if !ok || sub.SubscriptionID != "sub_1" || sub.CustomerID != "ctm_1" || sub.Status != StatusActive || sub.PriceID != "pri_signal_m" {
		t.Fatalf("mirror = %+v", sub)
	}
}

// TestLifecycle drives trial -> paid -> upgrade (prorated) -> downgrade
// (deferred) -> past due (grace, then read-only) -> cancel through the
// state machine, checking the organisation package at each step.
func TestLifecycle(t *testing.T) {
	svc, store, pk := newService(t)
	ctx := context.Background()
	now := t0
	svc.Now = func() time.Time { return now }
	pk.pkgs[5] = "operator" // 14-day trial package on sign-up

	deliver := func(id, typ, status, price string, extra map[string]any) {
		t.Helper()
		body := subEvent(id, typ, "sub_5", status, price, now, 5, extra)
		if rec := post(t, svc, body, Sign(body, testSecret, now)); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", id, rec.Code, rec.Body.String())
		}
	}
	sub := func() Subscription {
		s, _, _ := store.Subscription(ctx, 5)
		return s
	}

	// 1. trial -> paid Signal.
	deliver("evt_a", "subscription.created", "active", "pri_signal_m", nil)
	if pk.pkgs[5] != "signal" || sub().Status != StatusActive {
		t.Fatalf("paid: pkg=%s sub=%+v", pk.pkgs[5], sub())
	}
	firstEnd := *sub().CurrentPeriodEnd

	// 2. upgrade to Desk: prorated immediately, effective on the webhook.
	if ProrationFor("signal", "desk") != "prorated_immediately" || ProrationFor("desk", "signal") != "full_next_billing_period" {
		t.Fatal("proration modes")
	}
	now = now.Add(time.Hour)
	deliver("evt_b", "subscription.updated", "active", "pri_desk_m", map[string]any{
		"current_billing_period": map[string]any{"starts_at": t0.Format(time.RFC3339), "ends_at": firstEnd.Format(time.RFC3339)},
	})
	if pk.pkgs[5] != "desk" || sub().PriceID != "pri_desk_m" || !sub().CurrentPeriodEnd.Equal(firstEnd) {
		t.Fatalf("upgrade: pkg=%s sub=%+v", pk.pkgs[5], sub())
	}

	// 3. downgrade to Operator: Paddle switches the item now, we keep
	//    Desk until the period ends and record the scheduled price.
	now = now.Add(time.Hour)
	deliver("evt_c", "subscription.updated", "active", "pri_operator_m", map[string]any{
		"current_billing_period": map[string]any{"starts_at": t0.Format(time.RFC3339), "ends_at": firstEnd.Format(time.RFC3339)},
	})
	if pk.pkgs[5] != "desk" || sub().ScheduledPriceID != "pri_operator_m" || sub().PriceID != "pri_desk_m" {
		t.Fatalf("downgrade deferred: pkg=%s sub=%+v", pk.pkgs[5], sub())
	}
	// An out-of-order replay of the older upgrade event must not undo it.
	old := subEvent("evt_b2", "subscription.updated", "sub_5", "active", "pri_desk_m", t0.Add(30*time.Minute), 5, nil)
	if rec := post(t, svc, old, Sign(old, testSecret, now)); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if sub().ScheduledPriceID != "pri_operator_m" {
		t.Fatalf("older event rolled state back: %+v", sub())
	}
	// Renewal: new period, downgrade becomes effective.
	now = firstEnd.Add(time.Minute)
	deliver("evt_d", "subscription.updated", "active", "pri_operator_m", nil)
	if pk.pkgs[5] != "operator" || sub().ScheduledPriceID != "" || sub().PriceID != "pri_operator_m" {
		t.Fatalf("downgrade applied: pkg=%s sub=%+v", pk.pkgs[5], sub())
	}

	// 4. past due: full entitlements for 7 days, then read-only.
	now = now.Add(24 * time.Hour)
	pastDueAt := now
	deliver("evt_e", "subscription.past_due", "past_due", "pri_operator_m", nil)
	if sub().Status != StatusPastDue || sub().PastDueSince == nil || !sub().PastDueSince.Equal(pastDueAt) || pk.pkgs[5] != "operator" {
		t.Fatalf("past due: pkg=%s sub=%+v", pk.pkgs[5], sub())
	}
	in := entitlements.Input{OrgID: 5, PackageCode: pk.pkgs[5], SubStatus: sub().Status, PastDueSince: sub().PastDueSince}
	in.Now = pastDueAt.Add(3 * 24 * time.Hour)
	if doc, _ := entitlements.Resolve(in); doc.Status.ReadOnly || doc.Alerts.PerDay == 0 {
		t.Fatalf("inside grace should be full: %+v", doc.Status)
	}
	in.Now = pastDueAt.Add(8 * 24 * time.Hour)
	if doc, _ := entitlements.Resolve(in); !doc.Status.ReadOnly || doc.Alerts.PerDay != 0 || len(doc.AutoPaper.Strategies) != 0 {
		t.Fatalf("beyond grace should be read-only: %+v", doc.Status)
	}
	// A retry of the past_due event keeps the ORIGINAL past_due_since.
	now = now.Add(2 * 24 * time.Hour)
	deliver("evt_e2", "subscription.updated", "past_due", "pri_operator_m", nil)
	if !sub().PastDueSince.Equal(pastDueAt) {
		t.Fatalf("past_due_since moved: %+v", sub())
	}
	// Recovery clears it.
	deliver("evt_f", "subscription.updated", "active", "pri_operator_m", nil)
	if sub().PastDueSince != nil || sub().Status != StatusActive {
		t.Fatalf("recovery: %+v", sub())
	}

	// 5. cancel: scheduled first (entitlements kept), then terminal.
	now = now.Add(time.Hour)
	deliver("evt_g", "subscription.updated", "active", "pri_operator_m", map[string]any{
		"scheduled_change": map[string]any{"action": "cancel", "effective_at": now.Add(20 * 24 * time.Hour).Format(time.RFC3339)},
	})
	if !sub().CancelAtPeriodEnd || pk.pkgs[5] != "operator" {
		t.Fatalf("scheduled cancel: pkg=%s sub=%+v", pk.pkgs[5], sub())
	}
	now = now.Add(20 * 24 * time.Hour)
	deliver("evt_h", "subscription.canceled", "canceled", "pri_operator_m", map[string]any{"scheduled_change": nil})
	if sub().Status != StatusCanceled || sub().CancelAtPeriodEnd || pk.pkgs[5] != entitlements.PackageWatch {
		t.Fatalf("canceled: pkg=%s sub=%+v", pk.pkgs[5], sub())
	}
	if len(pk.log) != 5 {
		t.Fatalf("package writes = %v", pk.log)
	}
}

func TestTransactionCompletedAndUnmappedPrice(t *testing.T) {
	svc, store, pk := newService(t)
	var got []string
	svc.OnTransaction = func(_ context.Context, orgID int64, txn, net string, _ time.Time) {
		got = append(got, fmt.Sprintf("%d:%s:%s", orgID, txn, net))
	}
	txn, _ := json.Marshal(map[string]any{
		"event_id": "evt_t1", "event_type": "transaction.completed", "occurred_at": t0.Format(time.RFC3339),
		"data": map[string]any{
			"id": "txn_1", "subscription_id": "sub_9", "customer_id": "ctm_9",
			"custom_data": map[string]any{"org_id": "9"},
			"details":     map[string]any{"totals": map[string]any{"total": "39.00", "tax": "6.24", "fee": "2.15", "earnings": "30.61", "currency_code": "USD"}},
		},
	})
	for i := 0; i < 2; i++ {
		if rec := post(t, svc, txn, Sign(txn, testSecret, t0)); rec.Code != http.StatusOK {
			t.Fatal(rec.Code)
		}
	}
	if len(got) != 1 || got[0] != "9:txn_1:30.61" {
		t.Fatalf("affiliate hook = %v", got)
	}
	sub, ok, _ := store.Subscription(context.Background(), 9)
	if !ok || sub.CustomerID != "ctm_9" || sub.SubscriptionID != "sub_9" {
		t.Fatalf("mirror after transaction = %+v", sub)
	}
	// Unmapped price: mirrored, package untouched.
	body := subEvent("evt_u", "subscription.activated", "sub_9", "active", "pri_unknown", t0.Add(time.Minute), 9, nil)
	if rec := post(t, svc, body, Sign(body, testSecret, t0)); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if _, set := pk.pkgs[9]; set {
		t.Fatalf("unmapped price changed the package: %v", pk.pkgs)
	}
	sub, _, _ = store.Subscription(context.Background(), 9)
	if sub.PriceID != "pri_unknown" || sub.Status != StatusActive {
		t.Fatalf("unmapped mirror = %+v", sub)
	}
}

// TestCheckoutAndPortalAgainstFakePaddle exercises the client against a
// fake Paddle API: new organisation -> transaction; existing -> update
// with the right proration; portal URL from the mirrored customer id.
func TestCheckoutAndPortalAgainstFakePaddle(t *testing.T) {
	var calls []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test_api_key_0123456789" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"authentication_malformed","detail":"bad key"}}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		calls = append(calls, r.Method+" "+r.URL.Path+" "+string(raw))
		switch {
		case r.URL.Path == "/transactions":
			_, _ = w.Write([]byte(`{"data":{"id":"txn_new","status":"ready"}}`))
		case strings.HasPrefix(r.URL.Path, "/subscriptions/") && r.Method == http.MethodPatch:
			_, _ = w.Write([]byte(`{"data":{"id":"sub_1","status":"active"}}`))
		case strings.HasSuffix(r.URL.Path, "/portal-sessions"):
			_, _ = w.Write([]byte(`{"data":{"id":"cpls_1","urls":{"general":{"overview":"https://customer-portal.paddle.com/cpl_x?token=y"}}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer fake.Close()
	svc, store, _ := newService(t)
	key := strings.Repeat("k", 24) // fixture, not a credential
	svc.Client = NewClient(fake.URL, func(context.Context) string { return key })
	svc.ClientToken, svc.Environment = "test_client_token", "sandbox"
	ctx := context.Background()

	if _, err := svc.Checkout(ctx, 2, "watch", "pri_nope", "a@b.test"); err != ErrUnmappedPrice {
		t.Fatalf("unmapped = %v", err)
	}
	res, err := svc.Checkout(ctx, 2, "watch", "pri_signal_m", "a@b.test")
	if err != nil || res.TransactionID != "txn_new" || res.ClientToken != "test_client_token" || res.Pending {
		t.Fatalf("new checkout = %+v err=%v", res, err)
	}
	if !strings.Contains(calls[0], `"custom_data":{"org_id":"2"}`) || !strings.Contains(calls[0], `"price_id":"pri_signal_m"`) {
		t.Fatalf("transaction body: %s", calls[0])
	}
	if _, err := svc.PortalURL(ctx, 2); err != ErrNoCustomer {
		t.Fatalf("portal without customer = %v", err)
	}

	_ = store.UpsertSubscription(ctx, Subscription{OrgID: 2, CustomerID: "ctm_1", SubscriptionID: "sub_1", PriceID: "pri_signal_m", Status: StatusActive, UpdatedAt: t0})
	res, err = svc.Checkout(ctx, 2, "signal", "pri_desk_m", "a@b.test")
	if err != nil || !res.Pending || res.Proration != "prorated_immediately" {
		t.Fatalf("upgrade = %+v err=%v", res, err)
	}
	if !strings.Contains(calls[1], "PATCH /subscriptions/sub_1") || !strings.Contains(calls[1], `"proration_billing_mode":"prorated_immediately"`) || !strings.Contains(calls[1], `"on_payment_failure":"prevent_change"`) {
		t.Fatalf("update body: %s", calls[1])
	}
	res, _ = svc.Checkout(ctx, 2, "desk", "pri_operator_m", "a@b.test")
	if res.Proration != "full_next_billing_period" {
		t.Fatalf("downgrade proration = %+v", res)
	}
	url, err := svc.PortalURL(ctx, 2)
	if err != nil || !strings.HasPrefix(url, "https://customer-portal.paddle.com/") {
		t.Fatalf("portal = %q err=%v", url, err)
	}
	if !strings.Contains(calls[len(calls)-1], "/customers/ctm_1/portal-sessions") || !strings.Contains(calls[len(calls)-1], `"subscription_ids":["sub_1"]`) {
		t.Fatalf("portal call: %s", calls[len(calls)-1])
	}
	key = ""
	if _, err := svc.Checkout(ctx, 3, "watch", "pri_signal_m", ""); !IsNotConfigured(err) {
		t.Fatalf("no key = %v", err)
	}
	for _, c := range calls {
		if strings.Contains(c, "test_api_key") {
			t.Fatal("api key in request body")
		}
	}
}

func TestApplyScheduledJob(t *testing.T) {
	end := t0
	sub := Subscription{OrgID: 4, PriceID: "pri_desk_m", ScheduledPriceID: "pri_signal_m", Status: StatusActive, CurrentPeriodEnd: &end}
	prices := func(p string) (string, bool) {
		return map[string]string{"pri_signal_m": "signal", "pri_desk_m": "desk"}[p], true
	}
	if _, _, changed := ApplyScheduled(sub, prices, t0.Add(-time.Hour)); changed {
		t.Fatal("applied before period end")
	}
	next, pkg, changed := ApplyScheduled(sub, prices, t0.Add(time.Hour))
	if !changed || pkg != "signal" || next.PriceID != "pri_signal_m" || next.ScheduledPriceID != "" {
		t.Fatalf("scheduled = %+v %s %v", next, pkg, changed)
	}
}
