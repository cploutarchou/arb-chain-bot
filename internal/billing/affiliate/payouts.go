// Payouts (T-084, packages.md §5): the maturation job's rows, the
// operator's payouts report, and the paid-entry recording that settles
// a payout. All decimal; the report reconciles to the cent because it
// folds the same insert-only ledger the dashboard reads.
package affiliate

import (
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// Mature builds the `matured` row for one due accrual (the maturation
// job's unit). "Due" — matures_at has passed and no matured row cites
// the transaction yet — is decided by the store's DueMaturations
// (SQL NOT EXISTS), so the rule has one authority and concurrent runs
// cannot double-propose. Compute keeps deriving balances from time;
// these rows make the maturation date a ledger fact, so a payouts
// report for a past day can distinguish "mature then" from "mature
// now".
func Mature(e Entry, id string, now time.Time) Entry {
	return Entry{ID: id, AffiliateID: e.AffiliateID, ReferredOrg: e.ReferredOrg,
		TransactionID: e.TransactionID, Entry: EntryMatured, AmountUSD: e.AmountUSD, NetUSD: e.NetUSD,
		Rate: e.Rate, MaturesAt: e.MaturesAt, CreatedAt: now}
}

// Account is the affiliate_accounts row as the payouts report sees
// it. Status gates paying (§5 fraud rules put accounts in 'review';
// 'closed' accounts are never paid).
type Account struct {
	ID     string `json:"id"`
	OrgID  int64  `json:"org_id"`
	Code   string `json:"code"`
	Status string `json:"status"` // active | review | closed
}

// PayoutRow is one affiliate's line on the operator's payouts report.
type PayoutRow struct {
	AffiliateID string  `json:"affiliate_id"`
	Balance     Balance `json:"balance"`
	// RefundRate90d is refunded net ÷ accrued net over the last 90
	// days (fraud rule 3: above 20 % the account goes to manual
	// review). nil when nothing accrued in the window.
	RefundRate90d *decimal.Decimal `json:"refund_rate_90d,omitempty"`
	// ManualReview is fraud rule 3 tripped: the refund rate exceeds
	// 20 % over 90 days. The operator pays by hand or withholds.
	ManualReview bool `json:"manual_review"`
	// PaidLast180d is commission paid in the clawback window (fraud
	// rule 6: proven fraud can claw back paid commissions for up to
	// 180 days) — the exposure a review is deciding about.
	PaidLast180d decimal.Decimal `json:"paid_last_180d"`
}

// fraudRefundRate is the §5 fraud-rule-3 threshold.
const fraudRefundRateWindow = 90 * 24 * time.Hour

// Payouts folds one affiliate's entries into the report row.
func (t Terms) Payouts(entries []Entry, now time.Time) PayoutRow {
	row := PayoutRow{AffiliateID: affiliateIDOf(entries), Balance: t.Compute(entries, now)}
	var accruedNet, refundedNet decimal.Decimal
	for _, e := range entries {
		switch e.Entry {
		case EntryAccrued:
			if !e.CreatedAt.Before(now.Add(-fraudRefundRateWindow)) {
				accruedNet = accruedNet.Add(e.NetUSD)
			}
		case EntryReversed:
			if !e.CreatedAt.Before(now.Add(-fraudRefundRateWindow)) {
				refundedNet = refundedNet.Add(e.NetUSD.Abs())
			}
		case EntryPaid:
			if !e.CreatedAt.Before(now.Add(-180 * 24 * time.Hour)) {
				row.PaidLast180d = row.PaidLast180d.Add(e.AmountUSD.Abs())
			}
		}
	}
	if accruedNet.IsPositive() {
		rate := refundedNet.Div(accruedNet)
		row.RefundRate90d = &rate
		over := decimal.RequireFromString("0.20")
		row.ManualReview = rate.GreaterThan(over)
	}
	return row
}

func affiliateIDOf(entries []Entry) string {
	for _, e := range entries {
		if e.AffiliateID != "" {
			return e.AffiliateID
		}
	}
	return ""
}

// ErrPayoutInvalidAmount and ErrPayoutExceedsMatured are the recording
// gate's refusals; the API maps them to 400/409 respectively.
var (
	ErrPayoutInvalidAmount  = errors.New("affiliate: payout amount must be positive USD")
	ErrPayoutExceedsMatured = errors.New("affiliate: payout exceeds the matured unpaid balance")
)

// ValidatePayout refuses a payout the ledger cannot stand behind:
// non-positive money, or more than the matured unpaid balance. A
// below-threshold payout is NOT refused (the threshold gates the
// monthly run; a closing payout of a smaller matured balance is an
// operator decision the ledger records either way).
func (t Terms) ValidatePayout(entries []Entry, amount decimal.Decimal, now time.Time) error {
	if !amount.IsPositive() {
		return ErrPayoutInvalidAmount
	}
	b := t.Compute(entries, now)
	if amount.GreaterThan(b.Matured) {
		return fmt.Errorf("%w: %s > %s", ErrPayoutExceedsMatured, amount.StringFixed(2), b.Matured.StringFixed(2))
	}
	return nil
}

// PayoutEntry builds the `paid` row that settles a payout. The row is
// negative from the affiliate's point of view and cites the operator's
// payout reference as its transaction (payout:<ref>): money movement
// happens outside the ledger — bank transfer or PayPal via Paddle —
// and the ledger's job is to record exactly what left. orgID is the
// AFFILIATE's own organisation (the ledger requires a referred_org;
// a payout aggregates many referred orgs, so it cites its payer).
func PayoutEntry(id, affiliateID string, orgID int64, reference string, amount decimal.Decimal, at time.Time) Entry {
	return Entry{ID: id, AffiliateID: affiliateID, ReferredOrg: orgID, TransactionID: "payout:" + reference,
		Entry: EntryPaid, AmountUSD: amount.RoundBank(2).Neg(), CreatedAt: at}
}
