// Affiliate payouts surface (T-084, packages.md §5): the operator's
// monthly report — who is payable, at what matured balance, with the
// fraud-rule-3 refund rate and the clawback exposure — and the
// audited recording of a payout as a `paid` ledger row. Money never
// moves here: the report is the instruction sheet and the ledger row
// is the record of what left via Paddle-supported transfer.
package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/billing/affiliate"
)

func newAffiliateID(prefix string) string {
	return prefix + "-" + ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// AffiliatePayoutStore is the persistence this surface needs: the
// report folds each affiliate's entries in decimal (no SQL SUM), and
// recording validates against the same entries before writing.
type AffiliatePayoutStore interface {
	Accounts(ctx context.Context) ([]affiliate.Account, error)
	Entries(ctx context.Context, affiliateID string) ([]affiliate.Entry, error)
	Insert(ctx context.Context, e affiliate.Entry) error
}

func (s *Server) affiliateRoutes(mux *http.ServeMux) {
	gate := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.Affiliate == nil {
				WriteError(w, http.StatusServiceUnavailable, "affiliate_unavailable", "the affiliate ledger is not wired in this profile", correlationID(r))
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /api/v1/billing/affiliate/payouts",
		s.requireAuth(s.requirePlatformAdmin(gate(s.handleAffiliatePayouts))))
	mux.HandleFunc("POST /api/v1/billing/affiliate/payouts",
		s.requireAuth(s.requirePlatformAdmin(s.requireCSRF(gate(s.handleAffiliatePayoutRecord)))))
}

// affiliatePayoutLine is one report row: the account, the folded
// balance and the fraud-rule review figures.
type affiliatePayoutLine struct {
	affiliate.Account
	Row affiliate.PayoutRow `json:"payout"`
}

func (s *Server) handleAffiliatePayouts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accounts, err := s.Affiliate.Accounts(ctx)
	if err != nil {
		s.writeAffiliateError(w, r, err)
		return
	}
	now := time.Now().UTC()
	lines := make([]affiliatePayoutLine, 0, len(accounts))
	for _, acc := range accounts {
		line := affiliatePayoutLine{Account: acc}
		entries, err := s.Affiliate.Entries(ctx, acc.ID)
		if err != nil {
			s.writeAffiliateError(w, r, err)
			return
		}
		if len(entries) > 0 {
			line.Row = affiliate.DefaultTerms.Payouts(entries, now)
		}
		lines = append(lines, line)
	}
	WriteData(w, http.StatusOK, map[string]any{"generated_at": now, "accounts": lines})
}

func (s *Server) handleAffiliatePayoutRecord(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AffiliateID string `json:"affiliate_id"`
		AmountUSD   string `json:"amount_usd"`
		Reference   string `json:"reference"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", "body must be JSON", correlationID(r))
		return
	}
	if body.AffiliateID == "" || body.Reference == "" {
		WriteError(w, http.StatusBadRequest, "bad_request", "affiliate_id and reference are required", correlationID(r))
		return
	}
	amount, err := decimal.NewFromString(body.AmountUSD)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_request", "amount_usd must be a decimal amount", correlationID(r))
		return
	}
	ctx := r.Context()
	accounts, err := s.Affiliate.Accounts(ctx)
	if err != nil {
		s.writeAffiliateError(w, r, err)
		return
	}
	var acc *affiliate.Account
	for i := range accounts {
		if accounts[i].ID == body.AffiliateID {
			acc = &accounts[i]
			break
		}
	}
	if acc == nil {
		WriteError(w, http.StatusNotFound, "affiliate_not_found", "no such affiliate account", correlationID(r))
		return
	}
	if acc.Status == "closed" {
		WriteError(w, http.StatusConflict, "affiliate_closed", "a closed affiliate account cannot be paid", correlationID(r))
		return
	}
	entries, err := s.Affiliate.Entries(ctx, body.AffiliateID)
	if err != nil {
		s.writeAffiliateError(w, r, err)
		return
	}
	now := time.Now().UTC()
	if err := affiliate.DefaultTerms.ValidatePayout(entries, amount, now); err != nil {
		switch {
		case errors.Is(err, affiliate.ErrPayoutInvalidAmount):
			WriteError(w, http.StatusBadRequest, "invalid_amount", err.Error(), correlationID(r))
		case errors.Is(err, affiliate.ErrPayoutExceedsMatured):
			WriteError(w, http.StatusConflict, "exceeds_matured", err.Error(), correlationID(r))
		default:
			s.writeAffiliateError(w, r, err)
		}
		return
	}
	e := affiliate.PayoutEntry(newAffiliateID("affpay"), acc.ID, acc.OrgID, body.Reference, amount, now)
	if err := s.Affiliate.Insert(ctx, e); err != nil {
		s.writeAffiliateError(w, r, err)
		return
	}
	p, _ := PrincipalFrom(ctx)
	s.audit(r, p.UserID, "affiliate.payout.record", "affiliate:"+acc.ID)
	balance := affiliate.DefaultTerms.Compute(entries, now)
	WriteData(w, http.StatusCreated, map[string]any{
		"entry": e, "matured_after": balance.Matured.Sub(amount.RoundBank(2)),
	})
}

// writeAffiliateError maps a ledger failure: every error reaching
// here is a storage fault (the request's own refusals are written by
// the handler), so it is logged server-side and answered with a bare
// 500 — never with the raw driver error.
func (s *Server) writeAffiliateError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("affiliate ledger error", "error", err)
	WriteError(w, http.StatusInternalServerError, "affiliate_store", "the affiliate ledger could not be read or written", correlationID(r))
}
