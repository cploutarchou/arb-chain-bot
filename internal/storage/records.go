package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
)

// Record is one outbox item. Kind selects the writer; payloads are the
// domain structs (already immutable evidence).
type Record struct {
	Kind        string // "opportunity" | "cycle"
	Opportunity *opportunity.Opportunity
	Decision    *risk.Decision
	Cycle       *execution.CycleResult
	SessionID   string
}

// ensureRefs upserts the exchange/triangle rows an opportunity references
// (idempotent; the metadata sync owns richer fields later).
func (s *Store) ensureRefs(ctx context.Context, op *opportunity.Opportunity) error {
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO exchanges (id, name) VALUES ($1, $1)
		ON CONFLICT (id) DO NOTHING`, string(op.Exchange)); err != nil {
		return err
	}
	legs, err := json.Marshal(op.Quote.Legs)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO triangles (id, exchange_id, starting_asset, legs, canonical_key)
		VALUES ($1, $2, $3, $4, $1)
		ON CONFLICT (id) DO NOTHING`,
		op.TriangleID, string(op.Exchange), string(op.Start), legs)
	return err
}

// InsertOpportunity persists one evaluated opportunity with its decision
// evidence (SKILL.md §19, §36).
func (s *Store) InsertOpportunity(ctx context.Context, op *opportunity.Opportunity, dec *risk.Decision) error {
	if err := s.ensureRefs(ctx, op); err != nil {
		return fmt.Errorf("storage: refs: %w", err)
	}
	legs, err := json.Marshal(struct {
		Legs   any `json:"legs"`
		Checks any `json:"risk_checks,omitempty"`
	}{op.Quote.Legs, checksOf(dec)})
	if err != nil {
		return err
	}
	var cfgVersion *int64
	if op.ConfigVersion != 0 {
		cfgVersion = &op.ConfigVersion
	}
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO opportunities (
			id, exchange_id, triangle_id, status, reason_code,
			starting_asset, starting_amount, legs,
			gross_final_amount, estimated_final_amount,
			gross_profit, net_profit, gross_return_bps, net_return_bps,
			latency_buffer, risk_buffer, recommended_size,
			confidence, data_quality, config_version,
			detected_at, expires_at, decided_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
		ON CONFLICT (id) DO NOTHING`,
		op.ID, string(op.Exchange), op.TriangleID, string(op.Status), nullStr(op.Reason),
		string(op.Start), op.Quote.InputConsumed, legs,
		op.Quote.FinalAmount, op.EstimatedFinal,
		op.Quote.GrossProfit, op.NetProfit, op.GrossReturnBps, op.NetReturnBps,
		op.Buffers.LatencyBps, op.Buffers.RiskBps, op.RecommendedSize,
		op.DataQuality, op.DataQuality, cfgVersion,
		op.DetectedAt, op.ExpiresAt, time.Now(),
	)
	return err
}

// InsertCycle persists a settled paper cycle with its orders and fills in
// one transaction (correlation chain fill → order → cycle → opportunity).
// slippageMeasurable reports whether an outcome carries a meaningful
// realized-vs-plan slippage (the cycle converted back to the start
// asset). Keep in sync with the aggregate filters in reports/quality.
func slippageMeasurable(o execution.Outcome) bool {
	switch o {
	case execution.OutcomeAllFilled, execution.OutcomeLeg1Partial, execution.OutcomePartialCycle:
		return true
	}
	return false
}

func (s *Store) InsertCycle(ctx context.Context, sessionID string, res *execution.CycleResult) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	fees, err := json.Marshal(stringMap(res.Fees))
	if err != nil {
		return err
	}
	exposure, err := json.Marshal(stringMap(res.Exposure))
	if err != nil {
		return err
	}
	// SlippageBps is realized-vs-plan and only meaningful for cycles
	// that reached leg 3 (a mid-cycle failure would persist a ~+10000
	// artifact); other outcomes store NULL.
	var slippage any
	if slippageMeasurable(res.Outcome) {
		slippage = res.SlippageBps
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO paper_cycles (
			id, session_id, opportunity_id, outcome, pnl_amount, pnl_asset,
			fees, slippage_bps, exposure, started_at, settled_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (id) DO NOTHING`,
		res.CycleID, sessionID, nullStr(res.OpportunityID), string(res.Outcome),
		res.TotalPnL, string(res.StartAsset), fees, slippage, exposure,
		res.StartedAt, res.SettledAt); err != nil {
		return fmt.Errorf("storage: cycle: %w", err)
	}
	for _, o := range res.Orders {
		// Defensive market/exchange refs: real rows come from UpsertMarkets
		// at metadata sync; these keep FK integrity for orders regardless.
		if _, err := tx.Exec(ctx, `
			INSERT INTO exchanges (id, name) VALUES ($1, $1)
			ON CONFLICT DO NOTHING`, string(o.Market.Exchange)); err != nil {
			return fmt.Errorf("storage: exchange ref: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO markets (id, exchange_id, symbol, base_asset, quote_asset)
			VALUES ($1, $2, $3, '', '') ON CONFLICT DO NOTHING`,
			o.Market.String(), string(o.Market.Exchange), string(o.Market.Symbol)); err != nil {
			return fmt.Errorf("storage: market ref: %w", err)
		}
		var latencyMS any
		if !o.FilledAt.IsZero() {
			latencyMS = o.FilledAt.Sub(o.CreatedAt).Milliseconds()
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO orders (
				id, cycle_id, leg_no, market_id, side, order_type, time_in_force,
				qty_requested, qty_filled, price_requested, price_avg,
				fee_amount, fee_asset, latency_ms, status, created_at, acked_at, filled_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
			ON CONFLICT (id) DO NOTHING`,
			o.ID, res.CycleID, o.LegNo, o.Market.String(), o.Side.String(), o.Type, "IOC",
			o.QtyRequested, o.QtyFilled, o.LimitPrice, o.AvgPrice,
			o.FeeAmount, nullStr(string(o.FeeAsset)),
			latencyMS, string(o.Status),
			o.CreatedAt, nullTime(o.AckedAt), nullTime(o.FilledAt)); err != nil {
			return fmt.Errorf("storage: order %s: %w", o.ID, err)
		}
		for _, f := range o.Fills {
			if _, err := tx.Exec(ctx, `
				INSERT INTO fills (id, order_id, price, qty, fee_amount, fee_asset, book_version, ts)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
				ON CONFLICT (id) DO NOTHING`,
				f.ID, o.ID, f.Price, f.Qty, f.FeeAmount, nullStr(string(f.FeeAsset)),
				int64(f.BookVersion), f.At); err != nil { //nolint:gosec // book versions are far below int64 range
				return fmt.Errorf("storage: fill %s: %w", f.ID, err)
			}
		}
	}
	return tx.Commit(ctx)
}

// EnsurePaperSession registers the session row (idempotent).
func (s *Store) EnsurePaperSession(ctx context.Context, id, mode string, startingBalances map[string]string, configVersion int64, seed int64) error {
	balances, err := json.Marshal(startingBalances)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO paper_sessions (id, mode, starting_balances, config_version, seed)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT (id) DO NOTHING`,
		id, mode, balances, configVersion, seed)
	return err
}

func nullStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func stringMap[K ~string, V fmt.Stringer](m map[K]V) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[string(k)] = v.String()
	}
	return out
}

func checksOf(d *risk.Decision) any {
	if d == nil {
		return nil
	}
	return d.Checks
}
