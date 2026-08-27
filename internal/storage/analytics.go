package storage

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

// BL-19 PnL & analytics. Every aggregate reports n (the sample count),
// per the design brief: a breakdown with an empty/thin window must say
// so rather than silently showing zeros as fact.

// PnLBreakdownRow is one group of a breakdown dimension.
type PnLBreakdownRow struct {
	Key    string  `json:"key"`
	N      int     `json:"n"`
	NetPnL *string `json:"net_pnl,omitempty"`
	// AvgLatencyMs is populated only for by=market (see PnLBreakdown's
	// doc comment for why market never carries net_pnl).
	AvgLatencyMs *string `json:"avg_latency_ms,omitempty"`
}

// PnLBreakdownResult is GET /api/v1/pnl/breakdown's payload.
type PnLBreakdownResult struct {
	By          string            `json:"by"`
	WindowHours int               `json:"window_hours"`
	Rows        []PnLBreakdownRow `json:"rows"`
	N           int               `json:"n"` // total cycles considered
	// Unattributed has no omitempty (review P3): 0 unattributed cycles is
	// a real, meaningful zero for exchange/triangle/config_version
	// breakdowns (N + Unattributed always equals the window's cycles),
	// not an absence the client should have to infer from a missing key.
	Unattributed int      `json:"unattributed"`
	Notes        []string `json:"notes,omitempty"`
}

var pnlBreakdownDims = map[string]bool{
	"exchange": true, "triangle": true, "asset": true,
	"market": true, "hour": true, "config_version": true,
}

// ErrBadBreakdown reports an unsupported `by` dimension.
var ErrBadBreakdown = fmt.Errorf("storage: by must be one of exchange|triangle|asset|market|hour|config_version")

// PnLBreakdown aggregates settled paper cycles over [from, to) by one
// dimension (BL-19). exchange/triangle/config_version require the
// cycle's opportunity (nullable FK: paper_cycles.opportunity_id) — an
// INNER join, so N excludes cycles that never resolved to a persisted
// opportunity; Unattributed reports how many were excluded, so N +
// Unattributed always equals the cycles in the window (the "every
// aggregate returns n" guarantee extends to accounting for what n left
// out). asset/hour live directly on paper_cycles, no join, no
// unattributed cycles possible. market is different in kind: a cycle's
// P&L cannot be split across its three legs' markets without inventing
// numbers, so by=market reports LEG activity (order count, average
// latency) instead of net_pnl — never a fabricated per-market P&L.
func (s *Store) PnLBreakdown(ctx context.Context, by string, from, to time.Time) (PnLBreakdownResult, error) {
	if !pnlBreakdownDims[by] {
		return PnLBreakdownResult{}, ErrBadBreakdown
	}
	res := PnLBreakdownResult{By: by}

	if by == "market" {
		rows, err := s.Pool.Query(ctx, `
			SELECT coalesce(m.symbol, o.market_id), count(*), avg(o.latency_ms)::text
			FROM orders o
			JOIN paper_cycles c ON c.id = o.cycle_id
			LEFT JOIN markets m ON m.id = o.market_id
			WHERE c.started_at >= $1 AND c.started_at < $2
			GROUP BY coalesce(m.symbol, o.market_id)
			ORDER BY count(*) DESC`, from, to)
		if err != nil {
			return PnLBreakdownResult{}, err
		}
		defer rows.Close()
		for rows.Next() {
			var r PnLBreakdownRow
			if err := rows.Scan(&r.Key, &r.N, &r.AvgLatencyMs); err != nil {
				return PnLBreakdownResult{}, err
			}
			res.N += r.N
			res.Rows = append(res.Rows, r)
		}
		if err := rows.Err(); err != nil {
			return PnLBreakdownResult{}, err
		}
		res.Notes = append(res.Notes, "market groups report leg activity (order count, average latency), not P&L: a cycle's P&L spans all three legs and is not attributable to one market")
		// Review P3(e): N is the total ORDER count here (rows.N summed),
		// not cycles considered like every other `by` dimension's N —
		// call that out explicitly rather than silently overloading one
		// field's meaning across dimensions.
		res.Notes = append(res.Notes, "n (and each row's n) counts orders for by=market, not cycles as it does for every other `by` dimension")
		return res, nil
	}

	var groupExpr, joinClause string
	switch by {
	case "exchange":
		groupExpr, joinClause = "o.exchange_id", "JOIN opportunities o ON o.id = c.opportunity_id"
	case "triangle":
		groupExpr, joinClause = "o.triangle_id", "JOIN opportunities o ON o.id = c.opportunity_id"
	case "config_version":
		// coalesce: a cycle's opportunity can carry a NULL config_version
		// (rows persisted before strategy versioning, or a config applied
		// with no version stamped). Scanning that straight into r.Key
		// (a non-nullable string) turned every such row into a 500 —
		// review P3(a) — so NULL now groups under the honest "unknown"
		// key instead of erroring the whole breakdown.
		groupExpr, joinClause = "coalesce(o.config_version::text,'unknown')", "JOIN opportunities o ON o.id = c.opportunity_id"
	case "asset":
		// Same NULL → 500 hazard as config_version above (review P3(a)):
		// pnl_asset is nullable on cycles that never reached a settled
		// outcome with a resolvable asset.
		groupExpr, joinClause = "coalesce(c.pnl_asset,'unknown')", ""
	case "hour":
		// AT TIME ZONE 'UTC' pins the truncation boundary regardless of
		// the connection's session TimeZone; without it two clients (or
		// the same client after a `SET TIME ZONE`) could bucket the same
		// row under different hour keys, which would silently corrupt
		// the by=hour breakdown's grouping. to_char with an explicit
		// format (review P3(c)) keeps the key an unambiguous ISO-8601
		// UTC instant ("...THH24:MI:SSZ") instead of ::text's
		// locale/driver-dependent timestamp rendering.
		groupExpr, joinClause = `to_char(date_trunc('hour', c.started_at AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`, ""
	}
	query := fmt.Sprintf(`
		SELECT %s AS k, count(*), coalesce(sum(c.pnl_amount),0)::text
		FROM paper_cycles c
		%s
		WHERE c.started_at >= $1 AND c.started_at < $2
		GROUP BY k
		ORDER BY sum(c.pnl_amount) DESC NULLS LAST`, groupExpr, joinClause)
	rows, err := s.Pool.Query(ctx, query, from, to)
	if err != nil {
		return PnLBreakdownResult{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var r PnLBreakdownRow
		if err := rows.Scan(&r.Key, &r.N, &r.NetPnL); err != nil {
			return PnLBreakdownResult{}, err
		}
		res.N += r.N
		res.Rows = append(res.Rows, r)
	}
	if err := rows.Err(); err != nil {
		return PnLBreakdownResult{}, err
	}
	if joinClause != "" {
		var unattributed int
		if err := s.Pool.QueryRow(ctx, `
			SELECT count(*) FROM paper_cycles
			WHERE opportunity_id IS NULL AND started_at >= $1 AND started_at < $2`,
			from, to).Scan(&unattributed); err != nil {
			return PnLBreakdownResult{}, err
		}
		res.Unattributed = unattributed
	}
	return res, nil
}

// PnLPoint is one cumulative-P&L / drawdown sample.
type PnLPoint struct {
	At         time.Time `json:"at"`
	Cumulative string    `json:"cumulative_pnl"`
	Drawdown   string    `json:"drawdown"`
}

// PnLSeriesResult is GET /api/v1/pnl/series's payload.
type PnLSeriesResult struct {
	WindowHours int        `json:"window_hours"`
	Points      []PnLPoint `json:"points"`
	N           int        `json:"n"`
}

// PnLSeries returns settled cycles over [from, to) ordered oldest-first
// with a running cumulative P&L and drawdown (peak-to-current, always
// <= 0) — computed in decimal, never float64 (architecture brief: "All
// money math: shopspring/decimal. float64 on financial paths = P0").
func (s *Store) PnLSeries(ctx context.Context, from, to time.Time) (PnLSeriesResult, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT started_at, coalesce(pnl_amount, 0)::text
		FROM paper_cycles
		WHERE started_at >= $1 AND started_at < $2
		ORDER BY started_at ASC, id ASC`, from, to)
	if err != nil {
		return PnLSeriesResult{}, err
	}
	defer rows.Close()

	var res PnLSeriesResult
	cumulative := decimal.Zero
	peak := decimal.Zero
	for rows.Next() {
		var at time.Time
		var pnlStr string
		if err := rows.Scan(&at, &pnlStr); err != nil {
			return PnLSeriesResult{}, err
		}
		pnl, err := decimal.NewFromString(pnlStr)
		if err != nil {
			return PnLSeriesResult{}, fmt.Errorf("storage: pnl series: %w", err)
		}
		cumulative = cumulative.Add(pnl)
		if cumulative.GreaterThan(peak) {
			peak = cumulative
		}
		res.Points = append(res.Points, PnLPoint{
			At: at, Cumulative: cumulative.String(),
			Drawdown: cumulative.Sub(peak).String(), // <= 0
		})
	}
	if err := rows.Err(); err != nil {
		return PnLSeriesResult{}, err
	}
	res.N = len(res.Points)
	return res, nil
}

// Distribution is a small histogram over a decimal sample set: min/max/
// avg/percentiles plus fixed-width buckets. N is always populated, even
// when 0 (an honest empty distribution, not an absent field).
//
// Truncated/WindowComplete (review P2-4) report whether N is every
// sample in [from, to) or only the first analyticsSampleCap of them:
// the query below now orders deterministically, so a truncated
// Distribution is always the SAME (oldest analyticsSampleCap) subset
// across repeated calls, but it is still a subset — callers must not
// read N as "the total population" without checking WindowComplete
// first.
type Distribution struct {
	N              int               `json:"n"`
	Truncated      bool              `json:"truncated"`
	WindowComplete bool              `json:"window_complete"`
	Min            string            `json:"min,omitempty"`
	Max            string            `json:"max,omitempty"`
	Avg            string            `json:"avg,omitempty"`
	P50            string            `json:"p50,omitempty"`
	P95            string            `json:"p95,omitempty"`
	P99            string            `json:"p99,omitempty"`
	Buckets        []HistogramBucket `json:"buckets,omitempty"`
}

// HistogramBucket is one [From, To) bucket with its sample count.
type HistogramBucket struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int    `json:"count"`
}

const histogramBuckets = 10

// distributionOf builds a Distribution from a decimal sample set;
// samples is sorted in place. truncated reports whether the caller's
// query hit analyticsSampleCap (review P2-4): N is always the number of
// samples actually bucketed, but a truncated Distribution's N is a
// subset of the window's true population, not the population itself.
func distributionOf(samples []decimal.Decimal, truncated bool) Distribution {
	d := Distribution{N: len(samples), Truncated: truncated, WindowComplete: !truncated}
	if d.N == 0 {
		return d
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].LessThan(samples[j]) })
	min, max := samples[0], samples[len(samples)-1]
	d.Min, d.Max = min.String(), max.String()
	sum := decimal.Zero
	for _, v := range samples {
		sum = sum.Add(v)
	}
	d.Avg = sum.Div(decimal.NewFromInt(int64(d.N))).String()
	pick := func(p float64) string {
		idx := int(p * float64(d.N-1))
		return samples[idx].String()
	}
	d.P50, d.P95, d.P99 = pick(0.50), pick(0.95), pick(0.99)

	width := max.Sub(min)
	if width.IsZero() {
		d.Buckets = []HistogramBucket{{From: min.String(), To: max.String(), Count: d.N}}
		return d
	}
	step := width.Div(decimal.NewFromInt(histogramBuckets))
	buckets := make([]HistogramBucket, histogramBuckets)
	edge := min
	for i := 0; i < histogramBuckets; i++ {
		next := min.Add(step.Mul(decimal.NewFromInt(int64(i + 1))))
		if i == histogramBuckets-1 {
			next = max // last bucket closes exactly on max regardless of rounding
		}
		buckets[i] = HistogramBucket{From: edge.String(), To: next.String()}
		edge = next
	}
	for _, v := range samples {
		ratio, _ := v.Sub(min).Div(width).Float64()
		buckets[bucketIndex(ratio)].Count++
	}
	d.Buckets = buckets
	return d
}

// bucketIndex clamps a [0,1] position into [0, histogramBuckets-1]; the
// explicit min/max clamp (rather than a conditional) keeps the bound
// provably safe regardless of how a ratio derived from decimal division
// rounds at the edges.
func bucketIndex(ratio float64) int {
	idx := int(ratio * float64(histogramBuckets))
	if idx < 0 {
		return 0
	}
	if idx > histogramBuckets-1 {
		return histogramBuckets - 1
	}
	return idx
}

// DistributionsResult is GET /api/v1/analytics/distributions's payload.
type DistributionsResult struct {
	WindowHours int          `json:"window_hours"`
	Edge        Distribution `json:"edge_bps"`
	Slippage    Distribution `json:"slippage_bps"`
	Latency     Distribution `json:"latency_ms"`
}

// analyticsSampleCap bounds how many raw values a distribution query
// pulls into the API process for bucketing — generous for the paper-
// trading data volumes this platform runs at, but never unbounded. A
// package var, not a const, so analytics_test.go can shrink it to
// exercise the truncation path without seeding 20,000+ rows.
var analyticsSampleCap = 20_000

// Distributions computes edge/slippage/latency histograms over [from,
// to) (BL-19): edge from qualified opportunities' net_return_bps,
// slippage from settled cycles' slippage_bps (NULL-filtered — only
// cycles that reached leg 3 carry a meaningful value, same filter
// reports.go's CycleAggregates already uses), latency from orders'
// latency_ms.
func (s *Store) Distributions(ctx context.Context, from, to time.Time) (DistributionsResult, error) {
	edge, edgeTrunc, err := s.decimalColumn(ctx, `
		SELECT net_return_bps::text FROM opportunities
		WHERE status = 'QUALIFIED' AND net_return_bps IS NOT NULL
		  AND detected_at >= $1 AND detected_at < $2
		ORDER BY detected_at ASC, id ASC
		LIMIT $3`, from, to)
	if err != nil {
		return DistributionsResult{}, fmt.Errorf("storage: edge distribution: %w", err)
	}
	slippage, slipTrunc, err := s.decimalColumn(ctx, `
		SELECT slippage_bps::text FROM paper_cycles
		WHERE slippage_bps IS NOT NULL AND started_at >= $1 AND started_at < $2
		ORDER BY started_at ASC, id ASC
		LIMIT $3`, from, to)
	if err != nil {
		return DistributionsResult{}, fmt.Errorf("storage: slippage distribution: %w", err)
	}
	latency, latTrunc, err := s.decimalColumn(ctx, `
		SELECT o.latency_ms::text FROM orders o
		JOIN paper_cycles c ON c.id = o.cycle_id
		WHERE o.latency_ms IS NOT NULL AND c.started_at >= $1 AND c.started_at < $2
		ORDER BY o.created_at ASC, o.id ASC
		LIMIT $3`, from, to)
	if err != nil {
		return DistributionsResult{}, fmt.Errorf("storage: latency distribution: %w", err)
	}
	return DistributionsResult{
		Edge:     distributionOf(edge, edgeTrunc),
		Slippage: distributionOf(slippage, slipTrunc),
		Latency:  distributionOf(latency, latTrunc),
	}, nil
}

// decimalColumn runs a deterministically-ordered, LIMIT-capped query and
// reports whether the cap was hit (review P2-4). It queries
// analyticsSampleCap+1 rows and trims the extra one off rather than
// comparing len(out) == analyticsSampleCap, so a population that lands
// EXACTLY on the cap is correctly reported complete instead of a false
// truncated.
func (s *Store) decimalColumn(ctx context.Context, query string, from, to time.Time) (samples []decimal.Decimal, truncated bool, err error) {
	rows, err := s.Pool.Query(ctx, query, from, to, analyticsSampleCap+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []decimal.Decimal
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, false, err
		}
		v, err := decimal.NewFromString(raw)
		if err != nil {
			return nil, false, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(out) > analyticsSampleCap {
		out = out[:analyticsSampleCap]
		truncated = true
	}
	return out, truncated, nil
}
