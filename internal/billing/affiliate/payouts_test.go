package affiliate

import (
	"context"
	"errors"
	"testing"
	"time"
)

func accrual(id, txn, net, maturesIn string, at time.Time) Entry {
	m := at.Add(dur(maturesIn))
	return Entry{ID: id, AffiliateID: "aff-1", ReferredOrg: 7, TransactionID: txn, Entry: EntryAccrued,
		AmountUSD: d(net).Mul(d("0.20")).RoundBank(2), NetUSD: d(net), Rate: d("0.20"), MaturesAt: &m, CreatedAt: at}
}

func dur(days string) time.Duration {
	n, _ := time.ParseDuration(days + "h")
	return n
}

// Mature copies the accrual's money fields into the matured row: the
// ledger's balance derivation is time-based either way — the row's job
// is to fix the maturation date as a fact.
func TestMatureBuildsRow(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	e := accrual("e1", "txn-1", "500", "1080h", now.AddDate(0, 0, -46))
	m := Mature(e, "m1", now)
	if m.ID != "m1" || m.Entry != EntryMatured || m.TransactionID != "txn-1" || m.AffiliateID != "aff-1" || m.ReferredOrg != 7 {
		t.Fatalf("matured row = %+v", m)
	}
	if !m.AmountUSD.Equal(e.AmountUSD) || !m.NetUSD.Equal(e.NetUSD) || !m.Rate.Equal(e.Rate) {
		t.Fatalf("money fields drifted: %+v vs %+v", m, e)
	}
	if !m.CreatedAt.Equal(now) {
		t.Fatalf("matured at = %v, want %v", m.CreatedAt, now)
	}
}

// The report folds the ledger the way the dashboard does and adds the
// fraud figures: refund rate over 90 days trips manual review above
// 20 %, and paid-in-the-last-180-days is the clawback exposure.
func TestPayoutsReportFraudFigures(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	old := now.AddDate(0, 0, -100) // outside every window
	recent := now.AddDate(0, 0, -10)

	entries := []Entry{
		accrual("e1", "txn-1", "400", "1080h", old), // matured, net 400 in window? created 100d ago → outside 90d
		accrual("e2", "txn-2", "500", "1080h", recent),
		accrual("e3", "txn-3", "300", "1080h", recent),
		Reverse("e4", Entry{TransactionID: "txn-2", NetUSD: d("500")}, recent),
		PayoutEntry("e5", "aff-1", 7, "january", d("80"), now.AddDate(0, 0, -30)),
	}
	row := DefaultTerms.Payouts(entries, now)

	// Matured unpaid: (400 + 500 + 300) × 0.20 = 240, minus the paid 80,
	// minus the reversed txn-2's 100 → 60.
	if !row.Balance.Matured.Equal(d("60")) {
		t.Fatalf("matured unpaid = %s, want 60 (map %+v)", row.Balance.Matured, row.Balance)
	}
	if !row.PaidLast180d.Equal(d("80")) {
		t.Fatalf("paid_last_180d = %s, want 80", row.PaidLast180d)
	}
	// Refund rate: reversed net 500 over accrued net in the last 90d
	// (txn-2 500 + txn-3 300; txn-1 is 100d old) = 500/800 = 62.5 %.
	if row.RefundRate90d == nil || !row.RefundRate90d.Equal(d("0.625")) {
		t.Fatalf("refund_rate_90d = %v, want 0.625", row.RefundRate90d)
	}
	if !row.ManualReview {
		t.Fatal("manual review not tripped at 62.5 % refund rate")
	}

	// Same ledger without the reversal: the rate is present at zero
	// (accrued net exists in the window) and no review trips.
	row = DefaultTerms.Payouts(entries[:3], now)
	if row.RefundRate90d == nil || !row.RefundRate90d.IsZero() {
		t.Fatalf("refund rate = %v, want present zero", row.RefundRate90d)
	}
	if row.ManualReview {
		t.Fatal("manual review tripped with no refunds")
	}

	// A modest refund rate stays below the threshold: reverse 50 of 500.
	modest := []Entry{
		accrual("e1", "txn-1", "500", "1080h", recent),
		Reverse("e2", Entry{TransactionID: "txn-1", NetUSD: d("50")}, recent),
	}
	row = DefaultTerms.Payouts(modest, now)
	if row.ManualReview {
		t.Fatal("manual review tripped at 10 % refund rate")
	}
}

// ValidatePayout refuses non-positive money and anything beyond the
// matured unpaid balance; a below-threshold payout is the operator's
// decision (the threshold gates the monthly run, not the ledger).
func TestValidatePayout(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	entries := []Entry{accrual("e1", "txn-1", "1000", "0h", now.AddDate(0, 0, -46))} // 200 matured
	for _, bad := range []string{"0", "-5"} {
		if err := DefaultTerms.ValidatePayout(entries, d(bad), now); !errors.Is(err, ErrPayoutInvalidAmount) {
			t.Fatalf("amount %s: err = %v, want ErrPayoutInvalidAmount", bad, err)
		}
	}
	if err := DefaultTerms.ValidatePayout(entries, d("200.01"), now); !errors.Is(err, ErrPayoutExceedsMatured) {
		t.Fatalf("err = %v, want ErrPayoutExceedsMatured", err)
	}
	if err := DefaultTerms.ValidatePayout(entries, d("200"), now); err != nil {
		t.Fatalf("exact matured payout refused: %v", err)
	}
	if err := DefaultTerms.ValidatePayout(entries, d("50"), now); err != nil {
		t.Fatalf("below-threshold closing payout refused: %v", err)
	}
}

func TestPayoutEntryShape(t *testing.T) {
	at := time.Unix(1_800_000_000, 0).UTC()
	e := PayoutEntry("p1", "aff-1", 7, "2026-09", d("123.456"), at)
	if e.Entry != EntryPaid || e.AffiliateID != "aff-1" || e.ReferredOrg != 7 {
		t.Fatalf("entry = %+v", e)
	}
	if e.TransactionID != "payout:2026-09" {
		t.Fatalf("transaction = %s, want payout reference", e.TransactionID)
	}
	if !e.AmountUSD.Equal(d("-123.46")) {
		t.Fatalf("amount = %s, want -123.46 (bankers-rounded, affiliate-negative)", e.AmountUSD)
	}
}

// maturationFake records inserts and can fail them on demand.
type maturationFake struct {
	due     []Entry
	inserts []Entry
	failOn  string
}

func (f *maturationFake) DueMaturations(context.Context, time.Time) ([]Entry, error) {
	return f.due, nil
}

func (f *maturationFake) Insert(_ context.Context, e Entry) error {
	if e.TransactionID == f.failOn {
		return errors.New("insert failed")
	}
	f.inserts = append(f.inserts, e)
	return nil
}

// The job writes one matured row per due accrual at boot, tolerates an
// insert failure without dropping the rest, and idles afterwards.
func TestMaturationJobPass(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	due := []Entry{
		accrual("e1", "txn-1", "500", "1080h", now.AddDate(0, 0, -46)),
		accrual("e2", "txn-2", "300", "1080h", now.AddDate(0, 0, -50)),
	}
	fake := &maturationFake{due: due, failOn: "txn-2"}
	j := &MaturationJob{Store: fake, Now: func() time.Time { return now },
		After: func(time.Duration) <-chan time.Time { return make(chan time.Time) }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- j.Run(ctx) }()
	// The boot pass runs synchronously before the first wait; give it a
	// moment, then cancel — After never fires, so Run can only return
	// via cancellation.
	deadline := time.After(2 * time.Second)
	for {
		if passes, matured := j.Stats(); passes == 1 {
			if matured != 1 {
				t.Fatalf("matured = %d, want 1 (txn-2 fails, txn-1 lands)", matured)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("boot pass never ran")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.inserts) != 1 || fake.inserts[0].TransactionID != "txn-1" {
		t.Fatalf("inserts = %+v", fake.inserts)
	}
	if fake.inserts[0].ID == "" {
		t.Fatal("matured row has no id (IDGen default missing)")
	}
}
