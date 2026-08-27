package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/report"
)

// ScreenerReports adapts the store to report.Store (migration 000012).
type ScreenerReports struct{ s *Store }

func (s *Store) ScreenerReports() *ScreenerReports { return &ScreenerReports{s: s} }

var _ report.Store = (*ScreenerReports)(nil)

func (c *ScreenerReports) InsertReport(ctx context.Context, r report.Report) error {
	payload, err := json.Marshal(r.Payload)
	if err != nil {
		return err
	}
	_, err = c.s.Pool.Exec(ctx, `
		INSERT INTO screener_reports (id, period_start, period_end, strategy, rule_id, payload, md, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		r.ID, r.PeriodStart, r.PeriodEnd, string(r.Strategy), r.RuleID, payload, r.Markdown, r.CreatedAt)
	return err
}

func (c *ScreenerReports) ListReports(ctx context.Context, limit int) ([]report.Summary, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := c.s.Pool.Query(ctx, `
		SELECT id, period_start, period_end, strategy, rule_id, payload, created_at
		FROM screener_reports ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []report.Summary{}
	for rows.Next() {
		var r report.Report
		var strategy string
		var payload []byte
		if err := rows.Scan(&r.ID, &r.PeriodStart, &r.PeriodEnd, &strategy, &r.RuleID, &payload, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Strategy = screener.Strategy(strategy)
		if err := json.Unmarshal(payload, &r.Payload); err != nil {
			return nil, err
		}
		out = append(out, r.Summary())
	}
	return out, rows.Err()
}

func (c *ScreenerReports) GetReport(ctx context.Context, id string) (report.Report, error) {
	var r report.Report
	var strategy string
	var payload []byte
	var created time.Time
	err := c.s.Pool.QueryRow(ctx, `
		SELECT id, period_start, period_end, strategy, rule_id, payload, md, created_at
		FROM screener_reports WHERE id = $1`, id).
		Scan(&r.ID, &r.PeriodStart, &r.PeriodEnd, &strategy, &r.RuleID, &payload, &r.Markdown, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return report.Report{}, screener.ErrNotFound
	}
	if err != nil {
		return report.Report{}, err
	}
	r.Strategy = screener.Strategy(strategy)
	r.CreatedAt = created
	if err := json.Unmarshal(payload, &r.Payload); err != nil {
		return report.Report{}, err
	}
	return r, nil
}
