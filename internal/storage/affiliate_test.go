package storage

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/billing/affiliate"
)

// affiliateFixture seeds one organisation, one affiliate account and
// three accruals: two past their 45-day window, one still maturing.
func affiliateFixture(t *testing.T, s *Store) (acc affiliate.Account, old, recent, immature affiliate.Entry) {
	t.Helper()
	ctx := context.Background()
	var orgID int64
	if err := s.Pool.QueryRow(ctx,
		`INSERT INTO organisations (name) VALUES ('affiliate payouts test') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO affiliate_accounts (id, org_id, code) VALUES ('aff-test-1', $1, 'TESTCODE')`, orgID); err != nil {
		t.Fatal(err)
	}
	acc = affiliate.Account{ID: "aff-test-1", OrgID: orgID, Code: "TESTCODE", Status: "active"}
	now := time.Now().UTC()
	mk := func(id, txn string, net string, cleared time.Time, maturesIn time.Duration) affiliate.Entry {
		m := cleared.Add(maturesIn)
		return affiliate.Entry{ID: id, AffiliateID: acc.ID, ReferredOrg: orgID, TransactionID: txn,
			Entry: affiliate.EntryAccrued, AmountUSD: decimal.RequireFromString(net).Mul(decimal.RequireFromString("0.2")).RoundBank(2),
			NetUSD: decimal.RequireFromString(net), Rate: decimal.RequireFromString("0.2"),
			MaturesAt: &m, CreatedAt: cleared}
	}
	old = mk("e-old", "txn-old", "600", now.AddDate(0, 0, -60), 45*24*time.Hour)          // matured
	recent = mk("e-recent", "txn-recent", "300", now.AddDate(0, 0, -10), 45*24*time.Hour) // matured (55d total)? no: matures at +35d
	immature = mk("e-immature", "txn-imm", "200", now.AddDate(0, 0, -1), 45*24*time.Hour)
	for _, e := range []affiliate.Entry{old, recent, immature} {
		if err := s.Affiliate().Insert(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	return acc, old, recent, immature
}

// DueMaturations proposes exactly the accruals past matures_at, and
// writing the matured row removes the transaction from the next pass —
// the SQL NOT EXISTS is the idempotence the maturation job relies on.
func TestAffiliateDueMaturationsIdempotent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, old, _, _ := affiliateFixture(t, s)
	now := time.Now().UTC()

	// recent (cleared 10d ago, matures in 35d) and immature are not due;
	// old (cleared 60d ago, matured 15d ago) is.
	due, err := s.Affiliate().DueMaturations(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].TransactionID != old.TransactionID {
		t.Fatalf("due = %+v, want only txn-old", due)
	}
	if err := s.Affiliate().Insert(ctx, affiliate.Mature(due[0], "m-old", now)); err != nil {
		t.Fatal(err)
	}
	due, err = s.Affiliate().DueMaturations(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("due after maturing = %+v, want none (idempotence broken)", due)
	}
}

// AllEntries and Accounts give the payouts report its inputs: whole
// ledger ordered by affiliate then time, accounts with their status.
func TestAffiliateAllEntriesAndAccounts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	acc, _, _, _ := affiliateFixture(t, s)

	entries, err := s.Affiliate().AllEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("all entries = %d, want 3", len(entries))
	}
	for _, e := range entries {
		if e.AffiliateID != acc.ID {
			t.Fatalf("entry belongs to %s", e.AffiliateID)
		}
	}
	// Ordered by created_at: old (-60d) before recent (-10d) before immature (-1d).
	if !entries[0].CreatedAt.Before(entries[1].CreatedAt) || !entries[1].CreatedAt.Before(entries[2].CreatedAt) {
		t.Fatalf("entries out of order: %v, %v, %v", entries[0].CreatedAt, entries[1].CreatedAt, entries[2].CreatedAt)
	}

	accounts, err := s.Affiliate().Accounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range accounts {
		if a.ID == acc.ID {
			found = true
			if a.OrgID != acc.OrgID || a.Code != acc.Code || a.Status != "active" {
				t.Fatalf("account = %+v, want %+v", a, acc)
			}
		}
	}
	if !found {
		t.Fatalf("account %s not listed in %+v", acc.ID, accounts)
	}
}
