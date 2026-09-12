package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/billing/affiliate"
)

// affiliateFake backs the payouts surface without PostgreSQL: accounts
// in memory, entries appended in order, inserts recorded.
type affiliateFake struct {
	accounts []affiliate.Account
	entries  map[string][]affiliate.Entry
	inserts  []affiliate.Entry
}

func (f *affiliateFake) Accounts(context.Context) ([]affiliate.Account, error) {
	return f.accounts, nil
}

func (f *affiliateFake) Entries(_ context.Context, id string) ([]affiliate.Entry, error) {
	return f.entries[id], nil
}

func (f *affiliateFake) Insert(_ context.Context, e affiliate.Entry) error {
	f.inserts = append(f.inserts, e)
	f.entries[e.AffiliateID] = append(f.entries[e.AffiliateID], e)
	return nil
}

func maturedAffiliateFixture() *affiliateFake {
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -60)
	m := old.AddDate(0, 0, 46)
	acc := affiliate.Entry{ID: "e1", AffiliateID: "aff-1", ReferredOrg: 7, TransactionID: "txn-1",
		Entry: affiliate.EntryAccrued, AmountUSD: decimal.RequireFromString("200"),
		NetUSD: decimal.RequireFromString("1000"), Rate: decimal.RequireFromString("0.2"),
		MaturesAt: &m, CreatedAt: old}
	return &affiliateFake{
		accounts: []affiliate.Account{
			{ID: "aff-1", OrgID: 7, Code: "PARTNER1", Status: "active"},
			{ID: "aff-2", OrgID: 8, Code: "CLOSED1", Status: "closed"},
		},
		entries: map[string][]affiliate.Entry{"aff-1": {acc}},
	}
}

func affiliateGet(t *testing.T, mux *http.ServeMux, cookie *http.Cookie) (body map[string]any, rec *httptest.ResponseRecorder) {
	t.Helper()
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/billing/affiliate/payouts", nil)
	req.AddCookie(cookie)
	mux.ServeHTTP(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body, rec
}

// The report is platform-admin only, lists every account with its
// folded balance, and marks the payable line.
func TestAffiliatePayoutsReport(t *testing.T) {
	s, mux := newTestServer(t)
	s.Affiliate = maturedAffiliateFixture()
	cookie, _ := login(t, mux, "admin@example.test", "admin-pw")
	body, rec := affiliateGet(t, mux, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("report = %d: %s", rec.Code, rec.Body.String())
	}
	data, _ := body["data"].(map[string]any)
	accounts, _ := data["accounts"].([]any)
	if len(accounts) != 2 {
		t.Fatalf("accounts = %d, want 2 (body %s)", len(accounts), rec.Body.String())
	}
	first, _ := accounts[0].(map[string]any)
	if first["id"] != "aff-1" || first["code"] != "PARTNER1" || first["status"] != "active" {
		t.Fatalf("account row = %v", first)
	}
	pay, _ := first["payout"].(map[string]any)
	bal, _ := pay["balance"].(map[string]any)
	if bal["matured"] != "200" {
		t.Fatalf("matured = %v, want 200", bal["matured"])
	}
	if bal["payable"] != true {
		t.Fatalf("payable = %v (200 ≥ 100 threshold)", bal["payable"])
	}

	// A non-platform-admin sees nothing of it.
	cookie2, _ := login(t, mux, "viewer@example.test", "viewer-pw")
	_, rec = affiliateGet(t, mux, cookie2)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer report = %d, want 403", rec.Code)
	}
}

func affiliatePost(t *testing.T, mux *http.ServeMux, cookie *http.Cookie, csrf, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/billing/affiliate/payouts", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	mux.ServeHTTP(rec, req)
	return rec
}

// Recording a payout validates against the matured balance, refuses a
// closed account, and lands exactly one paid row citing the reference.
func TestAffiliatePayoutRecord(t *testing.T) {
	s, mux := newTestServer(t)
	fake := maturedAffiliateFixture()
	s.Affiliate = fake
	cookie, csrf := login(t, mux, "admin@example.test", "admin-pw")

	rec := affiliatePost(t, mux, cookie, csrf, `{"affiliate_id":"aff-1","amount_usd":"150","reference":"2026-09"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("record = %d: %s", rec.Code, rec.Body.String())
	}
	if len(fake.inserts) != 1 {
		t.Fatalf("inserts = %d, want 1", len(fake.inserts))
	}
	e := fake.inserts[0]
	if e.Entry != affiliate.EntryPaid || e.TransactionID != "payout:2026-09" || e.ReferredOrg != 7 {
		t.Fatalf("paid row = %+v", e)
	}
	if !e.AmountUSD.Equal(decimal.RequireFromString("-150")) {
		t.Fatalf("amount = %s, want -150", e.AmountUSD)
	}

	// The ledger's remaining matured balance is now 50: paying 100 more
	// must be refused as exceeding it.
	rec = affiliatePost(t, mux, cookie, csrf, `{"affiliate_id":"aff-1","amount_usd":"100","reference":"2026-10"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "exceeds_matured") {
		t.Fatalf("overpay = %d: %s", rec.Code, rec.Body.String())
	}

	// A closed account is never paid, whatever its balance.
	rec = affiliatePost(t, mux, cookie, csrf, `{"affiliate_id":"aff-2","amount_usd":"10","reference":"x"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "affiliate_closed") {
		t.Fatalf("closed = %d: %s", rec.Code, rec.Body.String())
	}

	// Garbage amounts and unknown accounts are client errors.
	rec = affiliatePost(t, mux, cookie, csrf, `{"affiliate_id":"aff-1","amount_usd":"ten","reference":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad amount = %d", rec.Code)
	}
	rec = affiliatePost(t, mux, cookie, csrf, `{"affiliate_id":"nope","amount_usd":"10","reference":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown affiliate = %d", rec.Code)
	}

	// Unwired profile: 503, not a panic.
	s.Affiliate = nil
	if _, rec := affiliateGet(t, mux, cookie); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired report = %d", rec.Code)
	}
}
