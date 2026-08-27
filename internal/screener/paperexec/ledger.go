// Package paperexec is the T-071 automatic PAPER executor for the
// Scanner Suite (docs/design/scanner-suite.md §4,
// docs/design/strategy-models.md §1–§3, §5). It subscribes to opened
// alert events, simulates fills against public top-of-book quotes with
// the shared fill/latency model, books every execution in the screener
// paper ledger (migration 000011) and serves the per-rule statistics.
//
// It never places an order: there is no exchange client in this
// package, and execution.LiveExecutor stays ErrLiveTradingDisabled.
// Money math is decimal end to end.
package paperexec

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Execution kinds (screener_paper_executions.kind).
const (
	KindSpot       = "spot"
	KindOpen       = "open"
	KindClose      = "close"
	KindFunding    = "funding"
	KindUnwind     = "unwind"
	KindPartialLeg = "partial_leg"
)

// Position statuses.
const (
	StatusOpen    = "OPEN"
	StatusClosed  = "CLOSED"
	StatusSkipped = "SKIPPED"
)

// Skip reasons (strategy-models §7 skipped{reason}).
const (
	SkipDataAge        = "DATA_AGE"
	SkipDepth          = "DEPTH"
	SkipBalance        = "BALANCE"
	SkipDriftCap       = "DRIFT_CAP"
	SkipMMRUnknown     = "MMR_UNKNOWN"
	SkipUnwind         = "UNWIND"
	SkipRejected       = "REJECTED"
	SkipMinNotional    = "MIN_NOTIONAL"
	SkipOpenPosition   = "OPEN_POSITION"
	SkipSettlementNear = "SETTLEMENT_NEAR"
	SkipPartialLeg     = "partial_leg"
	// Shared with the alert evaluator (screener/guard.go): a lane the
	// asset-identity guard refuses, or one with no top-of-book size.
	SkipSuspectMismatch  = screener.SkipSuspectMismatch
	SkipLiquidityUnknown = screener.SkipLiquidityUnknown
)

// Fill is one simulated leg.
type Fill struct {
	Leg         int             `json:"leg"`
	Venue       screener.Venue  `json:"venue"`
	Market      string          `json:"market"` // spot|perp
	Side        string          `json:"side"`   // BUY|SELL
	Base        string          `json:"base"`
	Quote       string          `json:"quote"`
	QuotePrice  decimal.Decimal `json:"quote_price"`  // top of book at decision
	RereadPrice decimal.Decimal `json:"reread_price"` // top of book after submit latency
	LimitPrice  decimal.Decimal `json:"limit_price"`  // quote ± LimitToleranceBps
	FillPrice   decimal.Decimal `json:"fill_price"`   // reread × (1 ± slip)
	Qty         decimal.Decimal `json:"qty"`
	FeeQuote    decimal.Decimal `json:"fee_quote"`
	Status      string          `json:"status"` // FILLED|REJECTED
	Reason      string          `json:"reason,omitempty"`
	SubmitMs    int64           `json:"submit_ms"`
	FillMs      int64           `json:"fill_ms"`
}

// Execution is one ledger row (screener_paper_executions).
type Execution struct {
	ID              string            `json:"id"`
	PositionID      string            `json:"position_id,omitempty"`
	RuleID          string            `json:"rule_id"`
	EventID         string            `json:"event_id,omitempty"`
	Strategy        screener.Strategy `json:"strategy"`
	Kind            string            `json:"kind"`
	Base            string            `json:"base"`
	Quote           string            `json:"quote"`
	VenueA          screener.Venue    `json:"venue_a"`
	VenueB          screener.Venue    `json:"venue_b"`
	Fills           []Fill            `json:"fills"`
	FeesQuote       decimal.Decimal   `json:"fees_quote"`
	SlipAllowBps    decimal.Decimal   `json:"slip_allow_bps"`
	RealisedSlipBps *decimal.Decimal  `json:"realised_slip_bps,omitempty"`
	PnLQuote        decimal.Decimal   `json:"pnl_quote"`
	Payload         map[string]any    `json:"payload,omitempty"`
	At              time.Time         `json:"at"`
}

// Position aliases the read-model type; the ledger stores it verbatim.
type Position = screener.PaperPosition

// Ledger persists balances, positions and executions. The storage
// package implements it over migration 000011; MemoryLedger serves
// tests and DB-less profiles.
type Ledger interface {
	ListBalances(ctx context.Context) ([]screener.PaperBalance, error)
	UpsertBalance(ctx context.Context, venue screener.Venue, asset string, balance decimal.Decimal, at time.Time) error

	InsertPosition(ctx context.Context, p Position) error
	UpdatePosition(ctx context.Context, p Position) error
	// ListPositions filters by rule ("" = all) and status ("" = all);
	// newest first; limit ≤ 0 = implementation default.
	ListPositions(ctx context.Context, ruleID, status string, limit int) ([]Position, error)

	InsertExecution(ctx context.Context, e Execution) error
	SetExecutionSlip(ctx context.Context, id string, realisedBps decimal.Decimal) error
	// ListExecutions returns oldest first for FIFO matching; limit ≤ 0 =
	// implementation default.
	ListExecutions(ctx context.Context, ruleID string, limit int) ([]Execution, error)
}

// MemoryLedger is an in-process Ledger.
type MemoryLedger struct {
	mu        sync.Mutex
	balances  map[[2]string]screener.PaperBalance
	positions map[string]Position
	execs     []Execution
}

// NewMemoryLedger returns an empty ledger.
func NewMemoryLedger() *MemoryLedger {
	return &MemoryLedger{balances: map[[2]string]screener.PaperBalance{}, positions: map[string]Position{}}
}

func (m *MemoryLedger) ListBalances(context.Context) ([]screener.PaperBalance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]screener.PaperBalance, 0, len(m.balances))
	for _, b := range m.balances {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Venue != out[j].Venue {
			return out[i].Venue < out[j].Venue
		}
		return out[i].Asset < out[j].Asset
	})
	return out, nil
}

func (m *MemoryLedger) UpsertBalance(_ context.Context, venue screener.Venue, asset string, balance decimal.Decimal, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.balances[[2]string{string(venue), asset}] = screener.PaperBalance{Venue: venue, Asset: asset, Balance: balance, UpdatedAt: at}
	return nil
}

func (m *MemoryLedger) InsertPosition(_ context.Context, p Position) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.positions[p.ID] = clonePosition(p)
	return nil
}

func (m *MemoryLedger) UpdatePosition(_ context.Context, p Position) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.positions[p.ID]; !ok {
		return screener.ErrNotFound
	}
	m.positions[p.ID] = clonePosition(p)
	return nil
}

func (m *MemoryLedger) ListPositions(_ context.Context, ruleID, status string, limit int) ([]Position, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Position
	for _, p := range m.positions {
		if ruleID != "" && p.RuleID != ruleID {
			continue
		}
		if status != "" && p.Status != status {
			continue
		}
		out = append(out, clonePosition(p))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].OpenedAt.Equal(out[j].OpenedAt) {
			return out[i].OpenedAt.After(out[j].OpenedAt)
		}
		return out[i].ID > out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryLedger) InsertExecution(_ context.Context, e Execution) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.execs {
		if x.ID == e.ID {
			return nil
		}
	}
	m.execs = append(m.execs, e)
	return nil
}

func (m *MemoryLedger) SetExecutionSlip(_ context.Context, id string, realised decimal.Decimal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.execs {
		if m.execs[i].ID == id {
			v := realised
			m.execs[i].RealisedSlipBps = &v
			return nil
		}
	}
	return screener.ErrNotFound
}

func (m *MemoryLedger) ListExecutions(_ context.Context, ruleID string, limit int) ([]Execution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Execution
	for _, e := range m.execs {
		if ruleID != "" && e.RuleID != ruleID {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func clonePosition(p Position) Position {
	if p.OpenPayload != nil {
		raw, _ := json.Marshal(p.OpenPayload)
		var cp map[string]any
		_ = json.Unmarshal(raw, &cp)
		p.OpenPayload = cp
	}
	return p
}

// toMap / fromMap move typed open payloads through the JSONB column.
func toMap(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func fromMap(m map[string]any, v any) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}
