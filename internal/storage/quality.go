package storage

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/quality"
)

// QualitySamples aggregates per-triangle history for the quality score
// (T-043). MaxDrawdown stays zero: drawdown is tracked at portfolio
// level; the API response says so rather than inventing a per-triangle
// number.
func (s *Store) QualitySamples(ctx context.Context, from, to time.Time) ([]quality.Sample, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT o.triangle_id,
		       count(c.id),
		       count(*) FILTER (WHERE c.outcome = 'ALL_FILLED'),
		       coalesce(sum(c.pnl_amount), 0)::text,
		       coalesce(min(c.pnl_amount), 0)::text,
		       coalesce(round(avg(c.slippage_bps) FILTER (WHERE c.outcome = 'ALL_FILLED'), 4), 0)::text,
		       coalesce(round(stddev_samp(c.slippage_bps) FILTER (WHERE c.outcome = 'ALL_FILLED'), 4), 0)::text
		FROM paper_cycles c
		JOIN opportunities o ON o.id = c.opportunity_id
		WHERE c.started_at >= $1 AND c.started_at < $2
		GROUP BY o.triangle_id`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]*quality.Sample{}
	for rows.Next() {
		var (
			sm                           quality.Sample
			net, worst, avgSlip, stdSlip string
		)
		if err := rows.Scan(&sm.TriangleID, &sm.Cycles, &sm.Successes,
			&net, &worst, &avgSlip, &stdSlip); err != nil {
			return nil, err
		}
		sm.NetPnL = decimal.RequireFromString(net)
		if w := decimal.RequireFromString(worst); w.IsNegative() {
			sm.WorstLoss = w
		}
		sm.AvgSlippageBps = decimal.RequireFromString(avgSlip)
		sm.SlippageStdBps = decimal.RequireFromString(stdSlip)
		s := sm
		byID[sm.TriangleID] = &s
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	windowHours := int(to.Sub(from) / time.Hour)
	if windowHours < 1 {
		windowHours = 1
	}
	edgeRows, err := s.Pool.Query(ctx, `
		SELECT triangle_id, count(DISTINCT date_trunc('hour', detected_at))
		FROM opportunities
		WHERE status = 'QUALIFIED' AND detected_at >= $1 AND detected_at < $2
		GROUP BY triangle_id`, from, to)
	if err != nil {
		return nil, err
	}
	defer edgeRows.Close()
	for edgeRows.Next() {
		var (
			id      string
			windows int
		)
		if err := edgeRows.Scan(&id, &windows); err != nil {
			return nil, err
		}
		sm, ok := byID[id]
		if !ok {
			// Qualified but never simulated in the window: still scoreable
			// (edge persistence + sample-size honesty).
			sm = &quality.Sample{TriangleID: id}
			byID[id] = sm
		}
		sm.EdgeWindows = windows
	}
	if err := edgeRows.Err(); err != nil {
		return nil, err
	}

	out := make([]quality.Sample, 0, len(byID))
	for _, sm := range byID {
		sm.WindowHours = windowHours
		out = append(out, *sm)
	}
	return out, nil
}
