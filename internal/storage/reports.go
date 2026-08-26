package storage

import (
	"context"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/reporting"
)

// Reports adapts the store to reporting.HistorySource.
type Reports struct{ s *Store }

func (s *Store) Reports() *Reports { return &Reports{s: s} }

func (r *Reports) OpportunityAggregates(ctx context.Context, from, to time.Time) (int, string, string, error) {
	var (
		count     int
		best, avg *string
	)
	err := r.s.Pool.QueryRow(ctx, `
		SELECT count(*),
		       max(net_return_bps)::text,
		       round(avg(net_return_bps), 4)::text
		FROM opportunities
		WHERE status = 'QUALIFIED' AND detected_at >= $1 AND detected_at < $2`,
		from, to).Scan(&count, &best, &avg)
	if err != nil {
		return 0, "", "", err
	}
	return count, deref(best), deref(avg), nil
}

func (r *Reports) CycleAggregates(ctx context.Context, from, to time.Time) (int, int, int, string, string, int, error) {
	var (
		total, success, failed, samples int
		avgSlip, worstSlip              *string
	)
	err := r.s.Pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE outcome = 'ALL_FILLED'),
		       count(*) FILTER (WHERE outcome <> 'ALL_FILLED'),
		       round(avg(slippage_bps) FILTER (WHERE outcome = 'ALL_FILLED'), 4)::text,
		       min(slippage_bps) FILTER (WHERE outcome = 'ALL_FILLED')::text,
		       count(slippage_bps) FILTER (WHERE outcome = 'ALL_FILLED')
		FROM paper_cycles
		WHERE started_at >= $1 AND started_at < $2`,
		from, to).Scan(&total, &success, &failed, &avgSlip, &worstSlip, &samples)
	if err != nil {
		return 0, 0, 0, "", "", 0, err
	}
	return total, success, failed, deref(avgSlip), deref(worstSlip), samples, nil
}

func (r *Reports) FailedCycles(ctx context.Context, from, to time.Time, limit int) ([]reporting.FailedCycle, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := r.s.Pool.Query(ctx, `
		SELECT id, outcome FROM paper_cycles
		WHERE outcome <> 'ALL_FILLED' AND started_at >= $1 AND started_at < $2
		ORDER BY started_at DESC LIMIT $3`, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []reporting.FailedCycle
	for rows.Next() {
		var f reporting.FailedCycle
		if err := rows.Scan(&f.ID, &f.Outcome); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *Reports) TriangleLeaders(ctx context.Context, from, to time.Time, limit int) ([]reporting.TriangleStat, []reporting.TriangleStat, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := r.s.Pool.Query(ctx, `
		SELECT o.triangle_id, count(*), coalesce(sum(c.pnl_amount), 0)::text
		FROM paper_cycles c
		JOIN opportunities o ON o.id = c.opportunity_id
		WHERE c.started_at >= $1 AND c.started_at < $2
		GROUP BY o.triangle_id
		ORDER BY sum(c.pnl_amount) DESC NULLS LAST`, from, to)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var all []reporting.TriangleStat
	for rows.Next() {
		var t reporting.TriangleStat
		if err := rows.Scan(&t.TriangleID, &t.Cycles, &t.NetPnL); err != nil {
			return nil, nil, err
		}
		all = append(all, t)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	top := all
	if len(top) > limit {
		top = top[:limit]
	}
	var worst []reporting.TriangleStat
	for i := len(all) - 1; i >= 0 && len(worst) < limit; i-- {
		worst = append(worst, all[i])
	}
	return top, worst, nil
}

func (r *Reports) InsertReport(ctx context.Context, rep reporting.Report) error {
	payload, err := rep.MarshalPayload()
	if err != nil {
		return err
	}
	_, err = r.s.Pool.Exec(ctx, `
		INSERT INTO reports (id, kind, period_start, period_end, generated_at, payload)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		rep.ID, string(rep.Kind), rep.PeriodStart, rep.PeriodEnd, rep.GeneratedAt, payload)
	return err
}

// ListReports returns persisted reports newest-first.
func (r *Reports) ListReports(ctx context.Context, kind string, limit int) ([]reporting.Report, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.s.Pool.Query(ctx, `
		SELECT payload FROM reports
		WHERE ($1 = '' OR kind = $1)
		ORDER BY generated_at DESC LIMIT $2`, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []reporting.Report
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var rep reporting.Report
		if err := rep.UnmarshalPayload(payload); err != nil {
			return nil, err
		}
		out = append(out, rep)
	}
	return out, rows.Err()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
