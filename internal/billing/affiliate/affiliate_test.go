package affiliate

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

var t0 = time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestAccrualMathIsDecimal(t *testing.T) {
	ref := Referral{AffiliateID: "aff", AffiliateOrgID: 2, ReferredOrgID: 3, ReferredAt: t0, PackageCode: "signal"}
	// Paddle net for a $39 Signal month after tax and fee: 30.61 → 20 % = 6.122 → 6.12.
	e := DefaultTerms.Accrue("e1", ref, "txn_1", d("30.61"), t0.Add(time.Hour))
	if !e.AmountUSD.Equal(d("6.12")) || !e.Rate.Equal(d("0.20")) || e.MaturesAt == nil || !e.MaturesAt.Equal(t0.Add(time.Hour+45*24*time.Hour)) {
		t.Fatalf("accrued = %+v", e)
	}
	// Partial-period proration on an upgrade: net 0.07 → 0.014 → 0.01 (bankers' rounding on the cent).
	if p := DefaultTerms.Accrue("e2", ref, "txn_2", d("0.07"), t0); !p.AmountUSD.Equal(d("0.01")) {
		t.Fatalf("prorated = %s", p.AmountUSD)
	}
	// 0.1 + 0.2 style inputs stay exact.
	if x := DefaultTerms.Accrue("e3", ref, "txn_3", d("0.30"), t0); x.AmountUSD.String() != "0.06" {
		t.Fatalf("decimal drift: %s", x.AmountUSD)
	}
	// Month 13: 10 %.
	if y := DefaultTerms.Accrue("e4", ref, "txn_4", d("100"), t0.AddDate(1, 0, 1)); !y.AmountUSD.Equal(d("10")) {
		t.Fatalf("after first year = %s", y.AmountUSD)
	}
	// Institution: 8 % first year, nothing after.
	inst := ref
	inst.PackageCode = "institution"
	if z := DefaultTerms.Accrue("e5", inst, "txn_5", d("1000"), t0); !z.AmountUSD.Equal(d("80")) {
		t.Fatalf("institution = %s", z.AmountUSD)
	}
	if z := DefaultTerms.Accrue("e6", inst, "txn_6", d("1000"), t0.AddDate(1, 0, 1)); !z.AmountUSD.IsZero() {
		t.Fatalf("institution year 2 = %s", z.AmountUSD)
	}
	// Negative or zero net never accrues.
	if n := DefaultTerms.Accrue("e7", ref, "txn_7", d("-5"), t0); !n.AmountUSD.IsZero() {
		t.Fatalf("negative net = %s", n.AmountUSD)
	}
}

func TestSelfReferralAccruesZero(t *testing.T) {
	for _, ref := range []Referral{
		{AffiliateID: "aff", AffiliateOrgID: 2, ReferredOrgID: 2, ReferredAt: t0, PackageCode: "desk"},
		{AffiliateID: "aff", AffiliateOrgID: 2, ReferredOrgID: 3, ReferredAt: t0, PackageCode: "desk", SelfReferral: true},
	} {
		if e := DefaultTerms.Accrue("x", ref, "txn", d("500"), t0); !e.AmountUSD.IsZero() || !e.Rate.IsZero() {
			t.Fatalf("self referral accrued %s", e.AmountUSD)
		}
	}
}

func TestRefundReversalAndMaturation(t *testing.T) {
	ledger := &MemoryLedger{}
	n := 0
	svc := &Service{Terms: DefaultTerms, Ledger: ledger, IDGen: func() string { n++; return fmt.Sprint("e", n) },
		Referral: func(_ context.Context, orgID int64) (Referral, error) {
			if orgID != 3 {
				return Referral{}, ErrNoReferral
			}
			return Referral{AffiliateID: "aff", AffiliateOrgID: 2, ReferredOrgID: 3, ReferredAt: t0, PackageCode: "operator"}, nil
		}}
	ctx := context.Background()
	// Two payments, the second delivered twice (webhook retry) — accrues once.
	if err := svc.OnTransaction(ctx, 3, "txn_a", "70.00", t0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := svc.OnTransaction(ctx, 3, "txn_b", "700.00", t0.Add(24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	// Unreferred organisation: nothing.
	if err := svc.OnTransaction(ctx, 4, "txn_c", "70.00", t0); err != nil {
		t.Fatal(err)
	}
	entries, _ := ledger.Entries(ctx, "aff")
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	bal := DefaultTerms.Compute(entries, t0.Add(2*24*time.Hour))
	if !bal.Accrued.Equal(d("154")) || !bal.Matured.IsZero() || bal.Payable {
		t.Fatalf("balance day 2 = %+v", bal)
	}
	// Day 45 + 1 h: the first payment matured (14), the second (paid a
	// day later) not yet (140).
	bal = DefaultTerms.Compute(entries, t0.Add(45*24*time.Hour+time.Hour))
	if !bal.Matured.Equal(d("14")) || !bal.Accrued.Equal(d("140")) || bal.Payable {
		t.Fatalf("balance day 45 = %+v", bal)
	}
	// Refund of the second payment reverses exactly 140.
	if err := svc.OnRefund(ctx, "txn_b", t0.Add(10*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	entries, _ = ledger.Entries(ctx, "aff")
	bal = DefaultTerms.Compute(entries, t0.Add(60*24*time.Hour))
	if !bal.Matured.Equal(d("14")) || !bal.Accrued.IsZero() || !bal.Reversed.Equal(d("-140")) || bal.Payable {
		t.Fatalf("balance after refund = %+v", bal)
	}
	// A large matured balance crosses the payout threshold; a payout
	// entry brings it back down and reconciles to the cent.
	_ = ledger.Insert(ctx, DefaultTerms.Accrue("big", Referral{AffiliateID: "aff", AffiliateOrgID: 2, ReferredOrgID: 3, ReferredAt: t0, PackageCode: "desk"}, "txn_d", d("1000.05"), t0))
	entries, _ = ledger.Entries(ctx, "aff")
	bal = DefaultTerms.Compute(entries, t0.Add(60*24*time.Hour))
	if !bal.Matured.Equal(d("214.01")) || !bal.Payable {
		t.Fatalf("balance with big = %+v", bal)
	}
	_ = ledger.Insert(ctx, Entry{ID: "p1", AffiliateID: "aff", TransactionID: "payout_1", Entry: EntryPaid, AmountUSD: d("-214.01"), CreatedAt: t0.Add(61 * 24 * time.Hour)})
	entries, _ = ledger.Entries(ctx, "aff")
	bal = DefaultTerms.Compute(entries, t0.Add(62*24*time.Hour))
	if !bal.Matured.IsZero() || !bal.Paid.Equal(d("214.01")) || bal.Payable {
		t.Fatalf("balance after payout = %+v", bal)
	}
}
