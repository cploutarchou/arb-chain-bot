package storage

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// ListFilter is the shared filter/pagination request for the global
// Orders and Fills pages (BL-20): every field empty/zero means
// unfiltered.
type ListFilter struct {
	Symbol   string
	Triangle string
	Cycle    string
	Status   string
	From, To time.Time
	Limit    int
	// Cursor, when non-empty, resumes a previous page (opaque; see
	// encodeCursor/decodeCursor). Rows are ordered newest-first by the
	// row's own timestamp column, then id, both DESC — the cursor is a
	// (timestamp, id) pair so rows sharing an identical timestamp (three
	// legs written in one transaction commonly do) are never skipped or
	// repeated across a page boundary.
	Cursor string
}

func (f ListFilter) limit() int {
	if f.Limit <= 0 || f.Limit > 500 {
		return 100
	}
	return f.Limit
}

// encodeCursor/decodeCursor pack a (RFC3339Nano timestamp, id) pair into
// one opaque base64 token. RFC3339Nano (not RFC3339) matters: Postgres
// timestamptz is microsecond-precision, and truncating to seconds would
// make the keyset silently skip or repeat rows whenever two rows share a
// second — exactly what happens with a cycle's three legs, written in
// one transaction.
func encodeCursor(ts time.Time, id string) string {
	raw := ts.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("storage: invalid cursor: %w", err)
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("storage: invalid cursor shape")
	}
	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("storage: invalid cursor timestamp: %w", err)
	}
	return ts, parts[1], nil
}

// OrderListRow is one global order row with its full cross-link chain
// (fill→)order→cycle→triangle→opportunity, per SKILL §38-39.
type OrderListRow struct {
	ID            string    `json:"id"`
	CycleID       string    `json:"cycle_id"`
	OpportunityID *string   `json:"opportunity_id,omitempty"`
	TriangleID    *string   `json:"triangle_id,omitempty"`
	Symbol        *string   `json:"symbol,omitempty"`
	LegNo         int16     `json:"leg_no"`
	Side          string    `json:"side"`
	Status        string    `json:"status"`
	QtyRequested  string    `json:"qty_requested"`
	QtyFilled     string    `json:"qty_filled"`
	AvgPrice      *string   `json:"avg_price,omitempty"`
	FeeAmount     *string   `json:"fee_amount,omitempty"`
	FeeAsset      *string   `json:"fee_asset,omitempty"`
	LatencyMs     *string   `json:"latency_ms,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// OrderPage is one page of ListOrdersGlobal; NextCursor is "" when this
// is the last page.
type OrderPage struct {
	Rows       []OrderListRow `json:"orders"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// ListOrdersGlobal serves GET /api/v1/orders: every simulated order,
// filterable and cursor-paginated by (created_at, id) DESC — its own
// index (migration 000007), not a join column, so the page stays an
// index scan regardless of which filters are set.
func (s *Store) ListOrdersGlobal(ctx context.Context, f ListFilter) (OrderPage, error) {
	limit := f.limit()
	var cursorTS any
	var cursorID string
	if f.Cursor != "" {
		ts, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return OrderPage{}, err
		}
		cursorTS, cursorID = ts, id
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT o.id, o.cycle_id, pc.opportunity_id, op.triangle_id, m.symbol,
		       o.leg_no, o.side, o.status,
		       o.qty_requested::text, o.qty_filled::text, o.price_avg::text,
		       o.fee_amount::text, o.fee_asset, o.latency_ms::text, o.created_at
		FROM orders o
		JOIN paper_cycles pc ON pc.id = o.cycle_id
		LEFT JOIN opportunities op ON op.id = pc.opportunity_id
		LEFT JOIN markets m ON m.id = o.market_id
		WHERE ($1 = '' OR m.symbol = $1)
		  AND ($2 = '' OR op.triangle_id = $2)
		  AND ($3 = '' OR o.cycle_id = $3)
		  AND ($4 = '' OR o.status = $4)
		  AND ($5::timestamptz IS NULL OR o.created_at >= $5)
		  AND ($6::timestamptz IS NULL OR o.created_at < $6)
		  AND ($7::timestamptz IS NULL OR (o.created_at, o.id) < ($7, $8))
		ORDER BY o.created_at DESC, o.id DESC
		LIMIT $9`,
		f.Symbol, f.Triangle, f.Cycle, f.Status,
		nullTime(f.From), nullTime(f.To), cursorTS, cursorID, limit+1)
	if err != nil {
		return OrderPage{}, err
	}
	defer rows.Close()
	var out OrderPage
	for rows.Next() {
		var r OrderListRow
		if err := rows.Scan(&r.ID, &r.CycleID, &r.OpportunityID, &r.TriangleID, &r.Symbol,
			&r.LegNo, &r.Side, &r.Status, &r.QtyRequested, &r.QtyFilled, &r.AvgPrice,
			&r.FeeAmount, &r.FeeAsset, &r.LatencyMs, &r.CreatedAt); err != nil {
			return OrderPage{}, err
		}
		out.Rows = append(out.Rows, r)
	}
	if err := rows.Err(); err != nil {
		return OrderPage{}, err
	}
	if len(out.Rows) > limit {
		last := out.Rows[limit-1]
		out.NextCursor = encodeCursor(last.CreatedAt, last.ID)
		out.Rows = out.Rows[:limit]
	}
	return out, nil
}

// FillListRow is one global fill row with the same cross-link chain as
// OrderListRow, plus the order's own leg/side/status for context.
type FillListRow struct {
	ID            string    `json:"id"`
	OrderID       string    `json:"order_id"`
	CycleID       string    `json:"cycle_id"`
	OpportunityID *string   `json:"opportunity_id,omitempty"`
	TriangleID    *string   `json:"triangle_id,omitempty"`
	Symbol        *string   `json:"symbol,omitempty"`
	LegNo         int16     `json:"leg_no"`
	Side          string    `json:"side"`
	OrderStatus   string    `json:"order_status"`
	Price         string    `json:"price"`
	Qty           string    `json:"qty"`
	FeeAmount     *string   `json:"fee_amount,omitempty"`
	FeeAsset      *string   `json:"fee_asset,omitempty"`
	BookVersion   *int64    `json:"book_version,omitempty"`
	TS            time.Time `json:"ts"`
}

// FillPage is one page of ListFillsGlobal.
type FillPage struct {
	Rows       []FillListRow `json:"fills"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// ListFillsGlobal serves GET /api/v1/fills: keyset-paginated by (ts, id)
// DESC on the fill's own columns (migration 000007's fills_ts_idx),
// filterable through the same joins as ListOrdersGlobal. "status" filters
// the PARENT order's status (fills have no status of their own).
func (s *Store) ListFillsGlobal(ctx context.Context, f ListFilter) (FillPage, error) {
	limit := f.limit()
	var cursorTS any
	var cursorID string
	if f.Cursor != "" {
		ts, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return FillPage{}, err
		}
		cursorTS, cursorID = ts, id
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT f.id, f.order_id, o.cycle_id, pc.opportunity_id, op.triangle_id, m.symbol,
		       o.leg_no, o.side, o.status,
		       f.price::text, f.qty::text, f.fee_amount::text, f.fee_asset, f.book_version, f.ts
		FROM fills f
		JOIN orders o ON o.id = f.order_id
		JOIN paper_cycles pc ON pc.id = o.cycle_id
		LEFT JOIN opportunities op ON op.id = pc.opportunity_id
		LEFT JOIN markets m ON m.id = o.market_id
		WHERE ($1 = '' OR m.symbol = $1)
		  AND ($2 = '' OR op.triangle_id = $2)
		  AND ($3 = '' OR o.cycle_id = $3)
		  AND ($4 = '' OR o.status = $4)
		  AND ($5::timestamptz IS NULL OR f.ts >= $5)
		  AND ($6::timestamptz IS NULL OR f.ts < $6)
		  AND ($7::timestamptz IS NULL OR (f.ts, f.id) < ($7, $8))
		ORDER BY f.ts DESC, f.id DESC
		LIMIT $9`,
		f.Symbol, f.Triangle, f.Cycle, f.Status,
		nullTime(f.From), nullTime(f.To), cursorTS, cursorID, limit+1)
	if err != nil {
		return FillPage{}, err
	}
	defer rows.Close()
	var out FillPage
	for rows.Next() {
		var r FillListRow
		if err := rows.Scan(&r.ID, &r.OrderID, &r.CycleID, &r.OpportunityID, &r.TriangleID, &r.Symbol,
			&r.LegNo, &r.Side, &r.OrderStatus, &r.Price, &r.Qty, &r.FeeAmount, &r.FeeAsset,
			&r.BookVersion, &r.TS); err != nil {
			return FillPage{}, err
		}
		out.Rows = append(out.Rows, r)
	}
	if err := rows.Err(); err != nil {
		return FillPage{}, err
	}
	if len(out.Rows) > limit {
		last := out.Rows[limit-1]
		out.NextCursor = encodeCursor(last.TS, last.ID)
		out.Rows = out.Rows[:limit]
	}
	return out, nil
}
