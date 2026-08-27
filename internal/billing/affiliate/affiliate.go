// Package affiliate implements the packages.md §5 commission ledger
// (T-084): decimal accrual on the net amount Paddle reports, 45-day
// maturation, reversal on refund, self-referral yields zero. Money is
// shopspring/decimal end to end; nothing here estimates.
package affiliate

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/entitlements"
)

// Terms are the §5 proposal values.
type Terms struct {
	FirstYearRate   decimal.Decimal // 20 % for the referred organisation's first 12 months
	AfterRate       decimal.Decimal // 10 % afterwards
	InstitutionRate decimal.Decimal // 8 %, first year only
	Maturation      time.Duration   // 45 days after the payment cleared
	PayoutThreshold decimal.Decimal // USD 100 accrued and matured
}

// DefaultTerms is packages.md §5 verbatim.
var DefaultTerms = Terms{
	FirstYearRate:   decimal.RequireFromString("0.20"),
	AfterRate:       decimal.RequireFromString("0.10"),
	InstitutionRate: decimal.RequireFromString("0.08"),
	Maturation:      45 * 24 * time.Hour,
	PayoutThreshold: decimal.RequireFromString("100"),
}

// Entry kinds (insert-only ledger).
const (
	EntryAccrued  = "accrued"
	EntryMatured  = "matured"
	EntryReversed = "reversed"
	EntryPaid     = "paid"
)

// Entry is one ledger row. AmountUSD is signed from the affiliate's
// point of view: accrued/matured positive, reversed negative, paid
// negative against the payable balance.
type Entry struct {
	ID            string          `json:"id"`
	AffiliateID   string          `json:"affiliate_id"`
	ReferredOrg   int64           `json:"referred_org"`
	TransactionID string          `json:"transaction_id"`
	Entry         string          `json:"entry"`
	AmountUSD     decimal.Decimal `json:"amount_usd"`
	NetUSD        decimal.Decimal `json:"net_usd"`
	Rate          decimal.Decimal `json:"rate"`
	MaturesAt     *time.Time      `json:"matures_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

// Referral is what the accrual needs to know about the referred
// organisation: who referred it, when it was created (the 12-month
// clock starts at organisation creation, §5 attribution), and its
// package (Institution has its own rate), plus the self-referral
// signals the fraud rule (1) checks.
type Referral struct {
	AffiliateID    string
	AffiliateOrgID int64
	ReferredOrgID  int64
	ReferredAt     time.Time
	PackageCode    string
	// SelfReferral is true when the fraud check found the same person,
	// card fingerprint, payout account or organisation domain.
	SelfReferral bool
}

var ErrNoReferral = errors.New("affiliate: organisation was not referred")

// Rate returns the commission rate for a payment at txnAt.
func (t Terms) Rate(ref Referral, txnAt time.Time) decimal.Decimal {
	if ref.SelfReferral || ref.AffiliateOrgID == ref.ReferredOrgID {
		return decimal.Zero
	}
	firstYear := txnAt.Before(ref.ReferredAt.AddDate(1, 0, 0))
	if ref.PackageCode == entitlements.PackageInstitution {
		if firstYear {
			return t.InstitutionRate
		}
		return decimal.Zero
	}
	if firstYear {
		return t.FirstYearRate
	}
	return t.AfterRate
}

// Accrue computes the accrued entry for one net payment (Paddle
// `details.totals.earnings`: after tax and Paddle fees). Prorated
// upgrade charges are simply smaller net amounts. Rounded half-up to
// the cent; the payouts report reconciles to the cent.
func (t Terms) Accrue(id string, ref Referral, transactionID string, netUSD decimal.Decimal, at time.Time) Entry {
	rate := t.Rate(ref, at)
	amount := netUSD.Mul(rate).RoundBank(2)
	if netUSD.Sign() <= 0 {
		amount = decimal.Zero
	}
	matures := at.Add(t.Maturation)
	return Entry{ID: id, AffiliateID: ref.AffiliateID, ReferredOrg: ref.ReferredOrgID, TransactionID: transactionID,
		Entry: EntryAccrued, AmountUSD: amount, NetUSD: netUSD, Rate: rate, MaturesAt: &matures, CreatedAt: at}
}

// Reverse produces the reversal entry for a refunded or charged-back
// transaction: the exact negative of what was accrued (and matured, if
// it had), so the affiliate's balance returns to what it was.
func Reverse(id string, accrued Entry, at time.Time) Entry {
	return Entry{ID: id, AffiliateID: accrued.AffiliateID, ReferredOrg: accrued.ReferredOrg, TransactionID: accrued.TransactionID,
		Entry: EntryReversed, AmountUSD: accrued.AmountUSD.Neg(), NetUSD: accrued.NetUSD.Neg(), Rate: accrued.Rate, CreatedAt: at}
}

// Balance is the affiliate dashboard's numbers, all read from the
// ledger (packages.md §5 "nothing estimated").
type Balance struct {
	Accrued  decimal.Decimal `json:"accrued"`  // accrued, not yet matured, not reversed
	Matured  decimal.Decimal `json:"matured"`  // matured and unpaid
	Paid     decimal.Decimal `json:"paid"`     // lifetime paid out
	Reversed decimal.Decimal `json:"reversed"` // lifetime reversals (negative)
	Payable  bool            `json:"payable"`  // matured >= threshold
}

// Ledger is the persistence surface.
type Ledger interface {
	Insert(ctx context.Context, e Entry) error
	Entries(ctx context.Context, affiliateID string) ([]Entry, error)
	ByTransaction(ctx context.Context, transactionID string) ([]Entry, error)
}

// Compute folds the entries into a Balance as of now. An accrued entry
// counts as matured once now >= matures_at and no reversal cites its
// transaction; a separate "matured" row is what the maturation job
// writes so the dashboard and the payouts report agree on the date.
func (t Terms) Compute(entries []Entry, now time.Time) Balance {
	var b Balance
	reversed := map[string]bool{}
	for _, e := range entries {
		if e.Entry == EntryReversed {
			reversed[e.TransactionID] = true
			b.Reversed = b.Reversed.Add(e.AmountUSD)
		}
	}
	for _, e := range entries {
		switch e.Entry {
		case EntryAccrued:
			if reversed[e.TransactionID] {
				continue
			}
			if e.MaturesAt != nil && !now.Before(*e.MaturesAt) {
				b.Matured = b.Matured.Add(e.AmountUSD)
			} else {
				b.Accrued = b.Accrued.Add(e.AmountUSD)
			}
		case EntryPaid:
			b.Paid = b.Paid.Add(e.AmountUSD.Neg())
			b.Matured = b.Matured.Add(e.AmountUSD)
		}
	}
	b.Payable = b.Matured.GreaterThanOrEqual(t.PayoutThreshold)
	return b
}

// Service ties the ledger to the Paddle transaction hook.
type Service struct {
	Terms  Terms
	Ledger Ledger
	// Referral resolves the referred organisation's attribution; nil or
	// ErrNoReferral means no commission.
	Referral func(ctx context.Context, orgID int64) (Referral, error)
	IDGen    func() string
}

// OnTransaction is paddle.Service.OnTransaction: accrue once per
// transaction (the ledger refuses a second accrual for the same id).
func (s *Service) OnTransaction(ctx context.Context, orgID int64, transactionID, netUSD string, at time.Time) error {
	if s.Referral == nil {
		return nil
	}
	ref, err := s.Referral(ctx, orgID)
	if errors.Is(err, ErrNoReferral) {
		return nil
	}
	if err != nil {
		return err
	}
	existing, err := s.Ledger.ByTransaction(ctx, transactionID)
	if err != nil {
		return err
	}
	for _, e := range existing {
		if e.Entry == EntryAccrued {
			return nil
		}
	}
	net, err := decimal.NewFromString(netUSD)
	if err != nil {
		return err
	}
	return s.Ledger.Insert(ctx, s.Terms.Accrue(s.IDGen(), ref, transactionID, net, at))
}

// OnRefund reverses every accrual for the transaction.
func (s *Service) OnRefund(ctx context.Context, transactionID string, at time.Time) error {
	entries, err := s.Ledger.ByTransaction(ctx, transactionID)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Entry == EntryAccrued {
			if err := s.Ledger.Insert(ctx, Reverse(s.IDGen(), e, at)); err != nil {
				return err
			}
		}
	}
	return nil
}

// MemoryLedger is the in-memory Ledger (tests).
type MemoryLedger struct {
	mu   sync.Mutex
	rows []Entry
}

func (m *MemoryLedger) Insert(_ context.Context, e Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows = append(m.rows, e)
	return nil
}

func (m *MemoryLedger) Entries(_ context.Context, affiliateID string) ([]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Entry
	for _, e := range m.rows {
		if e.AffiliateID == affiliateID {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *MemoryLedger) ByTransaction(_ context.Context, txn string) ([]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Entry
	for _, e := range m.rows {
		if e.TransactionID == txn {
			out = append(out, e)
		}
	}
	return out, nil
}
