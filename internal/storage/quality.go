package storage

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/quality"
)

// QualitySamples aggregates per-triangle history for the quality score
// (T-043). Slippage statistics cover every slippage-measurable cycle
// (reached leg 3: ALL_FILLED and both partial outcomes), and NULL
// aggregates stay unmeasured — the scorer awards nothing for absent
// evidence. DrawdownKnown stays false: per-triangle drawdown is not
// recorded yet.
func (s *Store) QualitySamples(ctx context.Context, from, to time.Time) ([]quality.Sample, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT o.triangle_id,
		       count(c.id),
		       count(*) FILTER (WHERE c.outcome = 'ALL_FILLED'),
		       coalesce(sum(c.pnl_amount), 0)::text,
		       coalesce(min(c.pnl_amount), 0)::text,
		       round(avg(c.slippage_bps), 4)::text,
		       round(stddev_samp(c.slippage_bps), 4)::text,
		       count(c.slippage_bps)
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
			sm               quality.Sample
			net, worst       string
			avgSlip, stdSlip *string // NULL when unmeasured
		)
		if err := rows.Scan(&sm.TriangleID, &sm.Cycles, &sm.Successes,
			&net, &worst, &avgSlip, &stdSlip, &sm.SlippageSamples); err != nil {
			return nil, err
		}
		sm.NetPnL = decimal.RequireFromString(net)
		if w := decimal.RequireFromString(worst); w.IsNegative() {
			sm.WorstLoss = w
		}
		if avgSlip != nil {
			sm.AvgSlippageBps = decimal.RequireFromString(*avgSlip)
		}
		if stdSlip != nil {
			sm.SlippageStdBps = decimal.RequireFromString(*stdSlip)
		} else if sm.SlippageSamples > 1 {
			// Shouldn't happen (stddev_samp is non-NULL for n>=2), but
			// never let a missing dispersion masquerade as measured.
			sm.SlippageSamples = 1
		}
		row := sm
		byID[sm.TriangleID] = &row
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// WindowHours counts the hour buckets the window touches, matching
	// how EdgeWindows buckets by date_trunc('hour', ...): a rolling 24h
	// window spans up to 25 buckets.
	windowHours := int(to.Truncate(time.Hour).Sub(from.Truncate(time.Hour))/time.Hour) + 1
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

// QualitySamplesForTriangle is QualitySamples scoped to a single
// triangle (review P2-3): GET /api/v1/triangles/{id} only ever needs
// ONE triangle's sample, but reused QualitySamples' ALL-triangles,
// 30-day query and then discarded every row except the one requested —
// on every page view, by any viewer with view:dashboard. ok is false
// when the triangle has no evidence at all in the window (no settled
// cycles AND no qualified-opportunity edge windows) — the caller must
// not fabricate a zero-filled Sample for a triangle with no history,
// mirroring QualitySamples' own "absent, not zero" treatment of a
// triangle it never sees in either query.
func (s *Store) QualitySamplesForTriangle(ctx context.Context, triangleID string, from, to time.Time) (quality.Sample, bool, error) {
	sm := quality.Sample{TriangleID: triangleID}
	var (
		net, worst       string
		avgSlip, stdSlip *string // NULL when unmeasured
	)
	if err := s.Pool.QueryRow(ctx, `
		SELECT count(c.id),
		       count(*) FILTER (WHERE c.outcome = 'ALL_FILLED'),
		       coalesce(sum(c.pnl_amount), 0)::text,
		       coalesce(min(c.pnl_amount), 0)::text,
		       round(avg(c.slippage_bps), 4)::text,
		       round(stddev_samp(c.slippage_bps), 4)::text,
		       count(c.slippage_bps)
		FROM paper_cycles c
		JOIN opportunities o ON o.id = c.opportunity_id
		WHERE o.triangle_id = $1 AND c.started_at >= $2 AND c.started_at < $3`,
		triangleID, from, to,
	).Scan(&sm.Cycles, &sm.Successes, &net, &worst, &avgSlip, &stdSlip, &sm.SlippageSamples); err != nil {
		return quality.Sample{}, false, err
	}
	sm.NetPnL = decimal.RequireFromString(net)
	if w := decimal.RequireFromString(worst); w.IsNegative() {
		sm.WorstLoss = w
	}
	if avgSlip != nil {
		sm.AvgSlippageBps = decimal.RequireFromString(*avgSlip)
	}
	if stdSlip != nil {
		sm.SlippageStdBps = decimal.RequireFromString(*stdSlip)
	} else if sm.SlippageSamples > 1 {
		// Shouldn't happen (stddev_samp is non-NULL for n>=2), but never
		// let a missing dispersion masquerade as measured.
		sm.SlippageSamples = 1
	}
	found := sm.Cycles > 0

	windowHours := int(to.Truncate(time.Hour).Sub(from.Truncate(time.Hour))/time.Hour) + 1
	if windowHours < 1 {
		windowHours = 1
	}
	sm.WindowHours = windowHours

	if err := s.Pool.QueryRow(ctx, `
		SELECT count(DISTINCT date_trunc('hour', detected_at))
		FROM opportunities
		WHERE status = 'QUALIFIED' AND triangle_id = $1 AND detected_at >= $2 AND detected_at < $3`,
		triangleID, from, to,
	).Scan(&sm.EdgeWindows); err != nil {
		return quality.Sample{}, false, err
	}
	if sm.EdgeWindows > 0 {
		found = true
	}

	if !found {
		return quality.Sample{}, false, nil
	}
	return sm, true, nil
}
