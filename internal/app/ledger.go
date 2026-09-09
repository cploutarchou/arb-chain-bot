package app

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/portfolio"
	"github.com/cploutarchou/arb-chain-bot/internal/reservation"
	"github.com/cploutarchou/arb-chain-bot/internal/storage"
)

// ledgerResume is what a restart continues from: the session, the cash
// the reservation ledger starts with, and the portfolio state.
type ledgerResume struct {
	SessionID string
	At        time.Time
	Balances  map[exchange.Asset]decimal.Decimal
	Portfolio portfolio.State
	// FoldedReserved lists capital that was reserved when the previous
	// process stopped: the reservation died with it, so the cash is
	// available again (the cycle it backed settled as ABORTED or never
	// settled at all).
	FoldedReserved map[exchange.Asset]decimal.Decimal
}

// sameStartingBalances reports whether a session was registered with
// exactly the configured starting balances (same assets, equal amounts).
func sameStartingBalances(session map[string]string, configured map[exchange.Asset]decimal.Decimal) bool {
	if len(session) != len(configured) {
		return false
	}
	for asset, want := range configured {
		raw, ok := session[string(asset)]
		if !ok {
			return false
		}
		v, err := decimal.NewFromString(raw)
		if err != nil || !v.Equal(want) {
			return false
		}
	}
	return true
}

// planResume converts a persisted snapshot into the state a fresh run
// restores. Every amount is parsed exactly; one unparsable amount rejects
// the whole snapshot rather than resuming a partly-known ledger.
func planResume(snap storage.LedgerSnapshot) (ledgerResume, error) {
	parse := func(m map[string]string, what string) (map[exchange.Asset]decimal.Decimal, error) {
		out := make(map[exchange.Asset]decimal.Decimal, len(m))
		for k, raw := range m {
			v, err := decimal.NewFromString(raw)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", what, k, err)
			}
			out[exchange.Asset(k)] = v
		}
		return out, nil
	}
	out := ledgerResume{SessionID: snap.SessionID, At: snap.At,
		Balances: map[exchange.Asset]decimal.Decimal{}, FoldedReserved: map[exchange.Asset]decimal.Decimal{}}
	if len(snap.Balances) == 0 {
		return ledgerResume{}, fmt.Errorf("snapshot of %s has no balances", snap.SessionID)
	}
	for asset, b := range snap.Balances {
		avail, err := decimal.NewFromString(b.Available)
		if err != nil {
			return ledgerResume{}, fmt.Errorf("available %s: %w", asset, err)
		}
		reserved := decimal.Zero
		if b.Reserved != "" {
			if reserved, err = decimal.NewFromString(b.Reserved); err != nil {
				return ledgerResume{}, fmt.Errorf("reserved %s: %w", asset, err)
			}
		}
		if avail.IsNegative() || reserved.IsNegative() {
			return ledgerResume{}, fmt.Errorf("negative balance for %s", asset)
		}
		out.Balances[exchange.Asset(asset)] = avail.Add(reserved)
		if reserved.IsPositive() {
			out.FoldedReserved[exchange.Asset(asset)] = reserved
		}
	}
	var err error
	if out.Portfolio.Realized, err = parse(snap.Realized, "realized"); err != nil {
		return ledgerResume{}, err
	}
	if out.Portfolio.Peak, err = parse(snap.Peak, "peak"); err != nil {
		return ledgerResume{}, err
	}
	if out.Portfolio.Drawdown, err = parse(snap.Drawdown, "drawdown"); err != nil {
		return ledgerResume{}, err
	}
	if out.Portfolio.Exposure, err = parse(snap.Exposure, "exposure"); err != nil {
		return ledgerResume{}, err
	}
	if out.Portfolio.Fees, err = parse(snap.Fees, "fees"); err != nil {
		return ledgerResume{}, err
	}
	out.Portfolio.Cycles, out.Portfolio.Completed, out.Portfolio.Failed = snap.Cycles, snap.Completed, snap.Failed
	return out, nil
}

// buildLedgerSnapshot captures the session's accounting state for the
// outbox after a settlement.
func buildLedgerSnapshot(sessionID, exchangeID string, now time.Time, resv *reservation.Manager,
	port *portfolio.Portfolio, marker portfolio.Marker, starts []exchange.Asset) storage.LedgerSnapshot {
	st := port.State()
	snap := storage.LedgerSnapshot{
		SessionID: sessionID, ExchangeID: exchangeID, At: now,
		Balances:   make(map[string]storage.LedgerBalance, len(starts)),
		MarkValues: make(map[string]string, len(starts)),
		Realized:   make(map[string]string, len(starts)),
		Peak:       make(map[string]string, len(starts)),
		Drawdown:   make(map[string]string, len(starts)),
		Fees:       make(map[string]string, len(st.Fees)),
		Exposure:   make(map[string]string, len(st.Exposure)),
		Cycles:     st.Cycles, Completed: st.Completed, Failed: st.Failed,
	}
	for _, a := range starts {
		avail, reserved := resv.Balance(a)
		snap.Balances[string(a)] = storage.LedgerBalance{Available: avail.String(), Reserved: reserved.String()}
		mark, _ := port.ExposureMark(a, marker)
		snap.MarkValues[string(a)] = mark.String()
		snap.Realized[string(a)] = st.Realized[a].String()
		snap.Peak[string(a)] = st.Peak[a].String()
		snap.Drawdown[string(a)] = st.Drawdown[a].String()
	}
	for a, v := range st.Fees {
		snap.Fees[string(a)] = v.String()
	}
	for a, v := range st.Exposure {
		if !v.IsZero() {
			snap.Exposure[string(a)] = v.String()
		}
	}
	return snap
}

// resumeLedger decides whether this run continues the previous paper
// session's ledger. It does when the latest open paper session has a
// snapshot and was registered with the configured starting balances —
// the operator has not started a new experiment. Anything else starts
// a fresh session, and an open session with different starting balances
// is ended so it is never resumed later. Persistence errors are logged
// and start fresh: a restart must never fail because history is
// unreadable, but it must say so.
func (e *Engine) resumeLedger(ctx context.Context, mode config.Mode, configured map[exchange.Asset]decimal.Decimal) (ledgerResume, bool) {
	if e.Store == nil || mode != config.ModePaper {
		return ledgerResume{}, false
	}
	open, ok, err := e.Store.LatestOpenPaperSession(ctx, string(mode))
	if err != nil {
		e.log.Warn("paper session lookup failed; starting a fresh ledger", "error", err)
		return ledgerResume{}, false
	}
	if !ok {
		return ledgerResume{}, false
	}
	if !sameStartingBalances(open.StartingBalances, configured) {
		e.log.Info("configured starting balances differ from the open paper session; starting a new session",
			"previous_session", open.ID)
		if err := e.Store.EndPaperSession(ctx, open.ID, time.Now()); err != nil {
			e.log.Warn("ending superseded paper session failed", "session", open.ID, "error", err)
		}
		return ledgerResume{}, false
	}
	snap, ok, err := e.Store.LatestLedgerSnapshot(ctx, open.ID)
	if err != nil {
		e.log.Warn("ledger snapshot lookup failed; starting a fresh ledger", "session", open.ID, "error", err)
		return ledgerResume{}, false
	}
	if !ok {
		// The session never settled a cycle: nothing to restore, but the
		// session itself continues.
		return ledgerResume{SessionID: open.ID, Balances: cloneAssets(configured)}, true
	}
	resume, err := planResume(snap)
	if err != nil {
		e.log.Error("ledger snapshot unusable; starting a fresh ledger", "session", open.ID, "error", err)
		return ledgerResume{}, false
	}
	return resume, true
}
