package storage

import (
	"context"
	"time"
)

// List queries backing the read-side API groups (T-024). Money columns
// cross the driver as strings (NUMERIC ↔ decimal string), never floats.

// OpportunityRow is the list view of one persisted opportunity.
type OpportunityRow struct {
	ID            string     `json:"id"`
	TriangleID    string     `json:"triangle_id"`
	Status        string     `json:"status"`
	ReasonCode    *string    `json:"reason_code,omitempty"`
	StartAsset    string     `json:"starting_asset"`
	StartAmount   string     `json:"starting_amount"`
	NetProfit     *string    `json:"net_profit,omitempty"`
	NetReturnBps  *string    `json:"net_return_bps,omitempty"`
	DataQuality   *string    `json:"data_quality,omitempty"`
	ConfigVersion *int64     `json:"config_version,omitempty"`
	DetectedAt    time.Time  `json:"detected_at"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
}

// ListOpportunities returns persisted opportunities newest-first,
// optionally filtered by status.
func (s *Store) ListOpportunities(ctx context.Context, status string, limit int) ([]OpportunityRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, triangle_id, status, reason_code, starting_asset,
		       starting_amount::text, net_profit::text, net_return_bps::text,
		       data_quality::text, config_version, detected_at, expires_at
		FROM opportunities
		WHERE ($1 = '' OR status = $1)
		ORDER BY detected_at DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OpportunityRow
	for rows.Next() {
		var r OpportunityRow
		if err := rows.Scan(&r.ID, &r.TriangleID, &r.Status, &r.ReasonCode,
			&r.StartAsset, &r.StartAmount, &r.NetProfit, &r.NetReturnBps,
			&r.DataQuality, &r.ConfigVersion, &r.DetectedAt, &r.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CycleRow is the list view of one settled paper cycle.
type CycleRow struct {
	ID            string     `json:"id"`
	SessionID     string     `json:"session_id"`
	OpportunityID *string    `json:"opportunity_id,omitempty"`
	Outcome       string     `json:"outcome"`
	PnLAmount     *string    `json:"pnl_amount,omitempty"`
	PnLAsset      *string    `json:"pnl_asset,omitempty"`
	SlippageBps   *string    `json:"slippage_bps,omitempty"`
	StartedAt     time.Time  `json:"started_at"`
	SettledAt     *time.Time `json:"settled_at,omitempty"`
}

// ListCycles returns paper cycles newest-first ("" session = all).
func (s *Store) ListCycles(ctx context.Context, sessionID string, limit int) ([]CycleRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, session_id, opportunity_id, outcome,
		       pnl_amount::text, pnl_asset, slippage_bps::text,
		       started_at, settled_at
		FROM paper_cycles
		WHERE ($1 = '' OR session_id = $1)
		ORDER BY started_at DESC LIMIT $2`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CycleRow
	for rows.Next() {
		var r CycleRow
		if err := rows.Scan(&r.ID, &r.SessionID, &r.OpportunityID, &r.Outcome,
			&r.PnLAmount, &r.PnLAsset, &r.SlippageBps, &r.StartedAt, &r.SettledAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListCyclesByTriangle returns a triangle's settled cycles newest-first
// (BL-26's "recent cycles" panel).
func (s *Store) ListCyclesByTriangle(ctx context.Context, triangleID string, limit int) ([]CycleRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 20
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT c.id, c.session_id, c.opportunity_id, c.outcome,
		       c.pnl_amount::text, c.pnl_asset, c.slippage_bps::text,
		       c.started_at, c.settled_at
		FROM paper_cycles c
		JOIN opportunities o ON o.id = c.opportunity_id
		WHERE o.triangle_id = $1
		ORDER BY c.started_at DESC LIMIT $2`, triangleID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CycleRow
	for rows.Next() {
		var r CycleRow
		if err := rows.Scan(&r.ID, &r.SessionID, &r.OpportunityID, &r.Outcome,
			&r.PnLAmount, &r.PnLAsset, &r.SlippageBps, &r.StartedAt, &r.SettledAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// OrderRow is one simulated order (with its fill columns inline).
type OrderRow struct {
	ID       string  `json:"id"`
	CycleID  string  `json:"cycle_id"`
	LegNo    int16   `json:"leg_no"`
	MarketID string  `json:"market_id"`
	Side     string  `json:"side"`
	Status   string  `json:"status"`
	Qty      *string `json:"qty,omitempty"`
	Filled   *string `json:"filled_qty,omitempty"`
	AvgPrice *string `json:"avg_price,omitempty"`
	Fee      *string `json:"fee,omitempty"`
	FeeAsset *string `json:"fee_asset,omitempty"`
}

// ListOrders returns a cycle's orders in leg order.
func (s *Store) ListOrders(ctx context.Context, cycleID string) ([]OrderRow, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, cycle_id, leg_no, market_id, side, status,
		       qty_requested::text, qty_filled::text, price_avg::text,
		       fee_amount::text, fee_asset
		FROM orders WHERE cycle_id = $1 ORDER BY leg_no`, cycleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OrderRow
	for rows.Next() {
		var r OrderRow
		if err := rows.Scan(&r.ID, &r.CycleID, &r.LegNo, &r.MarketID, &r.Side,
			&r.Status, &r.Qty, &r.Filled, &r.AvgPrice, &r.Fee, &r.FeeAsset); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RiskEventRow is one persisted risk event (BL-31): a circuit-breaker
// transition or a risk-rejected opportunity.
type RiskEventRow struct {
	ID            string    `json:"id"`
	TS            time.Time `json:"ts"`
	Kind          string    `json:"kind"`
	Subject       *string   `json:"subject,omitempty"`
	LimitName     *string   `json:"limit_name,omitempty"`
	Observed      *string   `json:"observed,omitempty"`
	Threshold     *string   `json:"threshold,omitempty"`
	Action        *string   `json:"action,omitempty"`
	BreakerState  *string   `json:"breaker_state,omitempty"`
	CorrelationID *string   `json:"correlation_id,omitempty"`
}

// ListRiskEvents returns risk_events rows newest-first within [from, to).
func (s *Store) ListRiskEvents(ctx context.Context, from, to time.Time, limit int) ([]RiskEventRow, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, ts, kind, subject, limit_name, observed, threshold, action, breaker_state, correlation_id
		FROM risk_events
		WHERE ts >= $1 AND ts < $2
		ORDER BY ts DESC LIMIT $3`, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RiskEventRow
	for rows.Next() {
		var r RiskEventRow
		if err := rows.Scan(&r.ID, &r.TS, &r.Kind, &r.Subject, &r.LimitName, &r.Observed,
			&r.Threshold, &r.Action, &r.BreakerState, &r.CorrelationID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AuditEventRow is one audit record (insert-only table).
type AuditEventRow struct {
	ID            string    `json:"id"`
	TS            time.Time `json:"ts"`
	Actor         *string   `json:"actor,omitempty"`
	Source        string    `json:"source"`
	Action        string    `json:"action"`
	Entity        string    `json:"entity"`
	EntityID      *string   `json:"entity_id,omitempty"`
	CorrelationID *string   `json:"correlation_id,omitempty"`
}

// ListAuditEvents returns audit records newest-first, optionally
// filtered by entity.
func (s *Store) ListAuditEvents(ctx context.Context, entity string, limit int) ([]AuditEventRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, ts, actor, source, action, entity, entity_id, correlation_id
		FROM audit_events
		WHERE ($1 = '' OR entity = $1)
		ORDER BY ts DESC LIMIT $2`, entity, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEventRow
	for rows.Next() {
		var r AuditEventRow
		if err := rows.Scan(&r.ID, &r.TS, &r.Actor, &r.Source, &r.Action,
			&r.Entity, &r.EntityID, &r.CorrelationID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
