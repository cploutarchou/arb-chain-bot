package storage

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/cploutarchou/arb-chain-bot/internal/billing/affiliate"
)

// Affiliate implements affiliate.Ledger over affiliate_ledger
// (migration 000014) and resolves referrals from
// organisations.referred_by.
type Affiliate struct{ s *Store }

func (s *Store) Affiliate() *Affiliate { return &Affiliate{s: s} }

var _ affiliate.Ledger = (*Affiliate)(nil)

func (a *Affiliate) Insert(ctx context.Context, e affiliate.Entry) error {
	_, err := a.s.Pool.Exec(ctx, `
		INSERT INTO affiliate_ledger (id, affiliate_id, referred_org, transaction_id, entry, amount_usd, net_usd, rate, matures_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		e.ID, e.AffiliateID, e.ReferredOrg, e.TransactionID, e.Entry, e.AmountUSD, e.NetUSD, e.Rate, e.MaturesAt, e.CreatedAt)
	return err
}

const ledgerCols = `id, affiliate_id, referred_org, transaction_id, entry, amount_usd, net_usd, rate, matures_at, created_at`

func (a *Affiliate) Entries(ctx context.Context, affiliateID string) ([]affiliate.Entry, error) {
	rows, err := a.s.Pool.Query(ctx, `SELECT `+ledgerCols+` FROM affiliate_ledger WHERE affiliate_id = $1 ORDER BY created_at, id`, affiliateID)
	if err != nil {
		return nil, err
	}
	return scanEntries(rows)
}

func (a *Affiliate) ByTransaction(ctx context.Context, txn string) ([]affiliate.Entry, error) {
	rows, err := a.s.Pool.Query(ctx, `SELECT `+ledgerCols+` FROM affiliate_ledger WHERE transaction_id = $1 ORDER BY created_at, id`, txn)
	if err != nil {
		return nil, err
	}
	return scanEntries(rows)
}

func scanEntries(rows pgx.Rows) ([]affiliate.Entry, error) {
	defer rows.Close()
	out := []affiliate.Entry{}
	for rows.Next() {
		var e affiliate.Entry
		if err := rows.Scan(&e.ID, &e.AffiliateID, &e.ReferredOrg, &e.TransactionID, &e.Entry, &e.AmountUSD, &e.NetUSD, &e.Rate, &e.MaturesAt, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Referral resolves the referred organisation's attribution (nil
// referred_by = ErrNoReferral). The self-referral fingerprint checks
// (packages.md §5 fraud rule 1) beyond "same organisation" are review
// flags, not automated here.
func (a *Affiliate) Referral(ctx context.Context, orgID int64) (affiliate.Referral, error) {
	var ref affiliate.Referral
	var affID *string
	var affOrg *int64
	err := a.s.Pool.QueryRow(ctx, `
		SELECT o.referred_by, af.org_id, o.id, o.created_at, o.package_code
		FROM organisations o LEFT JOIN affiliate_accounts af ON af.id = o.referred_by
		WHERE o.id = $1`, orgID).Scan(&affID, &affOrg, &ref.ReferredOrgID, &ref.ReferredAt, &ref.PackageCode)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && affID == nil) {
		return affiliate.Referral{}, affiliate.ErrNoReferral
	}
	if err != nil {
		return affiliate.Referral{}, err
	}
	ref.AffiliateID = *affID
	if affOrg != nil {
		ref.AffiliateOrgID = *affOrg
	}
	ref.SelfReferral = ref.AffiliateOrgID == ref.ReferredOrgID
	return ref, nil
}
