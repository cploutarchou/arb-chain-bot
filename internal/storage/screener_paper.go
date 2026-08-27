package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/paperexec"
)

// CloseEvent / SetEventExecution / CountEvents implement
// screener.EventCloser and screener.EventCounter (T-070) over
// screener_events.
func (c *ScreenerEvents) CloseEvent(ctx context.Context, id string, closedAt time.Time, lifetimeS int64, peakNetBps string) error {
	tag, err := c.s.Pool.Exec(ctx, `
		UPDATE screener_events SET closed_at = $2, lifetime_s = $3, peak_net_bps = $4
		WHERE id = $1`, id, closedAt, lifetimeS, peakNetBps)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return screener.ErrNotFound
	}
	return nil
}

func (c *ScreenerEvents) SetEventExecution(ctx context.Context, id, paperExecutionID string) error {
	tag, err := c.s.Pool.Exec(ctx, `UPDATE screener_events SET paper_execution_id = $2 WHERE id = $1`, id, paperExecutionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return screener.ErrNotFound
	}
	return nil
}

func (c *ScreenerEvents) CountEvents(ctx context.Context, ruleID string) (int64, error) {
	var n int64
	var err error
	if ruleID == "" {
		err = c.s.Pool.QueryRow(ctx, `SELECT count(*) FROM screener_events`).Scan(&n)
	} else {
		err = c.s.Pool.QueryRow(ctx, `SELECT count(*) FROM screener_events WHERE rule_id = $1`, ruleID).Scan(&n)
	}
	return n, err
}

// ScreenerPaper adapts the store to paperexec.Ledger (migration 000011):
// the Scanner Suite's own paper ledger. Nothing here writes to
// paper_cycles/orders/fills — see the migration header for why the
// triangular ledger is not reused.
type ScreenerPaper struct{ s *Store }

func (s *Store) ScreenerPaper() *ScreenerPaper { return &ScreenerPaper{s: s} }

var _ paperexec.Ledger = (*ScreenerPaper)(nil)

func (c *ScreenerPaper) ListBalances(ctx context.Context) ([]screener.PaperBalance, error) {
	rows, err := c.s.Pool.Query(ctx, `SELECT venue, asset, balance, updated_at FROM screener_paper_balances ORDER BY venue, asset`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []screener.PaperBalance
	for rows.Next() {
		var b screener.PaperBalance
		var venue string
		if err := rows.Scan(&venue, &b.Asset, &b.Balance, &b.UpdatedAt); err != nil {
			return nil, err
		}
		b.Venue = screener.Venue(venue)
		out = append(out, b)
	}
	return out, rows.Err()
}

func (c *ScreenerPaper) UpsertBalance(ctx context.Context, venue screener.Venue, asset string, balance decimal.Decimal, at time.Time) error {
	_, err := c.s.Pool.Exec(ctx, `
		INSERT INTO screener_paper_balances (venue, asset, balance, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (venue, asset) DO UPDATE SET balance = EXCLUDED.balance, updated_at = EXCLUDED.updated_at`,
		string(venue), asset, balance, at)
	return err
}

func (c *ScreenerPaper) InsertPosition(ctx context.Context, p paperexec.Position) error {
	payload, err := json.Marshal(p.OpenPayload)
	if err != nil {
		return err
	}
	if p.OpenPayload == nil {
		payload = []byte("{}")
	}
	_, err = c.s.Pool.Exec(ctx, `
		INSERT INTO screener_paper_positions
			(id, rule_id, event_id, strategy, base, quote, venue_a, venue_b, qty, open_payload,
			 opened_at, closed_at, pnl_quote, funding_quote, status, skipped_reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (id) DO NOTHING`,
		p.ID, p.RuleID, nullStr(p.EventID), string(p.Strategy), p.Base, p.Quote, string(p.VenueA), string(p.VenueB),
		p.Qty, payload, p.OpenedAt, p.ClosedAt, p.PnLQuote, p.FundingQuote, p.Status, nullStr(p.SkippedReason))
	return err
}

func (c *ScreenerPaper) UpdatePosition(ctx context.Context, p paperexec.Position) error {
	payload, err := json.Marshal(p.OpenPayload)
	if err != nil {
		return err
	}
	tag, err := c.s.Pool.Exec(ctx, `
		UPDATE screener_paper_positions
		SET open_payload = $2, closed_at = $3, pnl_quote = $4, funding_quote = $5, status = $6,
		    skipped_reason = $7, updated_at = now()
		WHERE id = $1`,
		p.ID, payload, p.ClosedAt, p.PnLQuote, p.FundingQuote, p.Status, nullStr(p.SkippedReason))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return screener.ErrNotFound
	}
	return nil
}

func (c *ScreenerPaper) ListPositions(ctx context.Context, ruleID, status string, limit int) ([]paperexec.Position, error) {
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	rows, err := c.s.Pool.Query(ctx, `
		SELECT id, rule_id, event_id, strategy, base, quote, venue_a, venue_b, qty, open_payload,
		       opened_at, closed_at, pnl_quote, funding_quote, status, skipped_reason
		FROM screener_paper_positions
		WHERE ($1 = '' OR rule_id = $1) AND ($2 = '' OR status = $2)
		ORDER BY opened_at DESC, id DESC LIMIT $3`, ruleID, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []paperexec.Position
	for rows.Next() {
		var (
			p                        paperexec.Position
			eventID, skipped         *string
			strategy, venueA, venueB string
			payload                  []byte
		)
		if err := rows.Scan(&p.ID, &p.RuleID, &eventID, &strategy, &p.Base, &p.Quote, &venueA, &venueB, &p.Qty, &payload,
			&p.OpenedAt, &p.ClosedAt, &p.PnLQuote, &p.FundingQuote, &p.Status, &skipped); err != nil {
			return nil, err
		}
		if eventID != nil {
			p.EventID = *eventID
		}
		if skipped != nil {
			p.SkippedReason = *skipped
		}
		p.Strategy, p.VenueA, p.VenueB = screener.Strategy(strategy), screener.Venue(venueA), screener.Venue(venueB)
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &p.OpenPayload); err != nil {
				return nil, err
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (c *ScreenerPaper) InsertExecution(ctx context.Context, e paperexec.Execution) error {
	fills, err := json.Marshal(e.Fills)
	if err != nil {
		return err
	}
	if e.Fills == nil {
		fills = []byte("[]")
	}
	var payload any
	if e.Payload != nil {
		raw, err := json.Marshal(e.Payload)
		if err != nil {
			return err
		}
		payload = raw
	}
	var realised any
	if e.RealisedSlipBps != nil {
		realised = *e.RealisedSlipBps
	}
	_, err = c.s.Pool.Exec(ctx, `
		INSERT INTO screener_paper_executions
			(id, position_id, rule_id, event_id, strategy, kind, base, quote, venue_a, venue_b,
			 fills, fees_quote, slip_allow_bps, realised_slip_bps, pnl_quote, payload, at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		ON CONFLICT (id) DO NOTHING`,
		e.ID, nullStr(e.PositionID), e.RuleID, nullStr(e.EventID), string(e.Strategy), e.Kind, e.Base, e.Quote,
		string(e.VenueA), string(e.VenueB), fills, e.FeesQuote, e.SlipAllowBps, realised, e.PnLQuote, payload, e.At)
	return err
}

func (c *ScreenerPaper) SetExecutionSlip(ctx context.Context, id string, realised decimal.Decimal) error {
	tag, err := c.s.Pool.Exec(ctx, `UPDATE screener_paper_executions SET realised_slip_bps = $2 WHERE id = $1`, id, realised)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return screener.ErrNotFound
	}
	return nil
}

func (c *ScreenerPaper) ListExecutions(ctx context.Context, ruleID string, limit int) ([]paperexec.Execution, error) {
	if limit <= 0 || limit > 20000 {
		limit = 20000
	}
	// Newest-first LIMIT, then reversed so callers get oldest first
	// (FIFO matching) over the most recent window.
	rows, err := c.s.Pool.Query(ctx, `
		SELECT id, position_id, rule_id, event_id, strategy, kind, base, quote, venue_a, venue_b,
		       fills, fees_quote, slip_allow_bps, realised_slip_bps, pnl_quote, payload, at
		FROM screener_paper_executions
		WHERE ($1 = '' OR rule_id = $1)
		ORDER BY at DESC, id DESC LIMIT $2`, ruleID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []paperexec.Execution
	for rows.Next() {
		var (
			e                        paperexec.Execution
			posID, eventID           *string
			strategy, venueA, venueB string
			fills, payload           []byte
			realised                 *decimal.Decimal
		)
		if err := rows.Scan(&e.ID, &posID, &e.RuleID, &eventID, &strategy, &e.Kind, &e.Base, &e.Quote, &venueA, &venueB,
			&fills, &e.FeesQuote, &e.SlipAllowBps, &realised, &e.PnLQuote, &payload, &e.At); err != nil {
			return nil, err
		}
		if posID != nil {
			e.PositionID = *posID
		}
		if eventID != nil {
			e.EventID = *eventID
		}
		e.Strategy, e.VenueA, e.VenueB = screener.Strategy(strategy), screener.Venue(venueA), screener.Venue(venueB)
		e.RealisedSlipBps = realised
		if len(fills) > 0 {
			if err := json.Unmarshal(fills, &e.Fills); err != nil {
				return nil, err
			}
		}
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &e.Payload); err != nil {
				return nil, err
			}
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

var _ = errors.Is
var _ = pgx.ErrNoRows
