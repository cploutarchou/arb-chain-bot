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
	Kind        string // "opportunity" | "cycle" | "risk_event" | "ledger_snapshot"
	Opportunity *opportunity.Opportunity
	Decision    *risk.Decision
	Cycle       *execution.CycleResult
	SessionID   string
	RiskEvent   *RiskEvent
	Ledger      *LedgerSnapshot
}

// RiskEvent is one persisted risk-engine event (BL-31): a circuit-breaker
// transition or a risk-rejected opportunity. It maps directly onto the
// risk_events table (migration 000001); Kind distinguishes the two
// sources so the console can filter/label them.
type RiskEvent struct {
	ID            string
	TS            time.Time
	Kind          string // "breaker_transition" | "risk_reject"
	Subject       string // breaker scope, or "triangle:<id>" for a rejection
	LimitName     string // breaker name, or the failing risk check's name
	Observed      string
	Threshold     string
	Action        string // free-text reason
	BreakerState  string // breaker's new state; empty for rejections
	CorrelationID string
}

// InsertRiskEvent persists one risk_events row (BL-31); idempotent on id.
func (s *Store) InsertRiskEvent(ctx context.Context, e RiskEvent) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO risk_events (
			id, ts, kind, subject, limit_name, observed, threshold, action, breaker_state, correlation_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (id) DO NOTHING`,
		e.ID, e.TS, e.Kind, nullStr(e.Subject), nullStr(e.LimitName), nullStr(e.Observed),
		nullStr(e.Threshold), nullStr(e.Action), nullStr(e.BreakerState), nullStr(e.CorrelationID))
	return err
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

// opportunityDecision is the structured decision evidence persisted in
// the `decision` column (BL-27): the full risk verdict, not just the
// checks sub-object InsertOpportunity has always folded into `legs`
// (kept there too, for the historical rows that predate this column).
type opportunityDecision struct {
	Allowed       bool         `json:"allowed"`
	ReasonCode    string       `json:"reason_code,omitempty"`
	Checks        []risk.Check `json:"checks,omitempty"`
	ConfigVersion int64        `json:"config_version,omitempty"`
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
	var decisionJSON []byte
	if dec != nil {
		decisionJSON, err = json.Marshal(opportunityDecision{
			Allowed: dec.Allowed, ReasonCode: dec.ReasonCode,
			Checks: dec.Checks, ConfigVersion: dec.ConfigVersion,
		})
		if err != nil {
			return err
		}
	}
	// BookVersions() is the revalidation contract's own per-leg evidence
	// (internal/opportunity, already exported) — not re-derived here.
	versions := op.BookVersions()
	bookVersions := make([]int64, len(versions))
	for i, v := range versions {
		bookVersions[i] = int64(v) //nolint:gosec // book versions are far below int64 range
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
			detected_at, expires_at, decided_at,
			decision, book_versions
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25)
		ON CONFLICT (id) DO NOTHING`,
		op.ID, string(op.Exchange), op.TriangleID, string(op.Status), nullStr(op.Reason),
		string(op.Start), op.Quote.InputConsumed, legs,
		op.Quote.FinalAmount, op.EstimatedFinal,
		op.Quote.GrossProfit, op.NetProfit, op.GrossReturnBps, op.NetReturnBps,
		op.Buffers.LatencyBps, op.Buffers.RiskBps, op.RecommendedSize,
		op.DataQuality, op.DataQuality, cfgVersion,
		op.DetectedAt, op.ExpiresAt, time.Now(),
		decisionJSON, bookVersions,
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
	// SlippageBps (planned return − realized return, both per unit of
	// input actually deployed) is only meaningful for cycles that
	// converted back to the start asset; other outcomes store NULL.
	var slippage, plannedBps, actualBps any
	if slippageMeasurable(res.Outcome) {
		slippage = res.SlippageBps
		plannedBps = res.PlannedReturnBps
		actualBps = res.ActualReturnBps
	}
	// The opportunity row may never have landed (dropped by a full
	// outbox, refused by the database, lost at shutdown). Letting the FK
	// fail the cycle insert would lose the cycle's own orders and fills
	// too — the record of money moving — so the cycle is written
	// unlinked (NULL opportunity_id), counted, and logged.
	oppRef := nullStr(res.OpportunityID)
	if res.OpportunityID != "" {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM opportunities WHERE id = $1)`,
			res.OpportunityID).Scan(&exists); err != nil {
			return fmt.Errorf("storage: opportunity lookup: %w", err)
		}
		if !exists {
			oppRef = nil
			s.unlinkedCycles.Add(1)
			s.logger().Warn("storage: cycle persisted without its opportunity row",
				"cycle_id", res.CycleID, "opportunity_id", res.OpportunityID,
				"unlinked_total", s.unlinkedCycles.Load())
		}
	}
	// pnl_amount stays the marked total for readers that predate the
	// breakdown; realized_pnl (cash basis) and exposure_mark are the two
	// components, persisted separately so a mark-to-market estimate can
	// never be summed as if it were realized.
	if _, err := tx.Exec(ctx, `
		INSERT INTO paper_cycles (
			id, session_id, opportunity_id, outcome, pnl_amount, pnl_asset,
			fees, slippage_bps, exposure, started_at, settled_at,
			realized_pnl, exposure_mark, input_consumed, final_amount,
			planned_return_bps, actual_return_bps, reason
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		ON CONFLICT (id) DO NOTHING`,
		res.CycleID, sessionID, oppRef, string(res.Outcome),
		res.TotalPnL, string(res.StartAsset), fees, slippage, exposure,
		res.StartedAt, res.SettledAt,
		res.RealizedPnL, res.ExposureMark, res.InputConsumed, res.FinalAmount,
		plannedBps, actualBps, nullStr(res.Reason)); err != nil {
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

// EndPaperSession stamps ended_at on a paper session row (T-057 E10):
// restart makes many sessions per process normal, and an unbounded set
// of NULL-ended sessions would make "the current session" ambiguous for
// the Paper and PnL views. Idempotent: only the first call sets it.
func (s *Store) EndPaperSession(ctx context.Context, id string, at time.Time) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE paper_sessions SET ended_at = $2
		WHERE id = $1 AND ended_at IS NULL`, id, at)
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
