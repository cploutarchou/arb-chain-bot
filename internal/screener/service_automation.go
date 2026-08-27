package screener

import (
	"context"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

// Ticker is one automation stage run on every poll interval: the T-070
// alert evaluator and the T-071 paper executor both implement it. The
// concrete types live in sub-packages (internal/screener/alerts,
// internal/screener/paperexec) that import this package, so the seam
// here is an interface and the Service never names them.
type Ticker interface {
	Name() string
	Tick(ctx context.Context, now time.Time)
}

// AutoPaperSource is what GET /screener/auto-paper reads (design §7);
// the paperexec executor implements it and app wiring sets
// Service.AutoPaper. Nil means "executor not running in this profile"
// and the route answers an empty, honestly-labelled document.
type AutoPaperSource interface {
	AutoPaperView(ctx context.Context, now time.Time) (AutoPaperView, error)
}

// AutoPaperView is the auto-paper read model. Every number is decimal
// on the wire and net of fees; every mark carries its data age.
type AutoPaperView struct {
	Positions   []PaperPosition  `json:"positions"`
	Summary     AutoPaperSummary `json:"summary"`
	Balances    []PaperBalance   `json:"balances"`
	GeneratedAt time.Time        `json:"generated_at"`
	// Model states what the numbers are: simulated fills against public
	// top-of-book quotes, never real orders.
	Model string `json:"model"`
}

// AutoPaperSummary groups per-rule statistics.
type AutoPaperSummary struct {
	PerRule []RuleSummary `json:"per_rule"`
}

// PaperBalance is one (venue, asset) paper balance.
type PaperBalance struct {
	Venue     Venue           `json:"venue"`
	Asset     string          `json:"asset"`
	Balance   decimal.Decimal `json:"balance"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// PaperPosition is one open or closed paper position (strategy-models
// §2.2 / §3.2 / §5.2).
type PaperPosition struct {
	ID            string          `json:"id"`
	RuleID        string          `json:"rule_id"`
	EventID       string          `json:"event_id,omitempty"`
	Strategy      Strategy        `json:"strategy"`
	Base          string          `json:"base"`
	Quote         string          `json:"quote"`
	VenueA        Venue           `json:"venue_a"`
	VenueB        Venue           `json:"venue_b"`
	Qty           decimal.Decimal `json:"qty"`
	OpenPayload   map[string]any  `json:"open"`
	OpenedAt      time.Time       `json:"opened_at"`
	ClosedAt      *time.Time      `json:"closed_at,omitempty"`
	PnLQuote      decimal.Decimal `json:"pnl_quote"`
	FundingQuote  decimal.Decimal `json:"funding_quote"`
	Status        string          `json:"status"`
	SkippedReason string          `json:"skipped_reason,omitempty"`
	// Unrealised mark for OPEN positions (spot bid / perp ask at the
	// latest poll), with the age of the quotes it was computed from.
	MarkPnLQuote *decimal.Decimal `json:"mark_pnl_quote,omitempty"`
	MarkAgeMs    *int64           `json:"mark_age_ms,omitempty"`
}

// DriftRow is one base's inventory drift (§2.3) for a spot rule.
type DriftRow struct {
	Base          string          `json:"base"`
	VenueA        Venue           `json:"venue_a"`
	VenueB        Venue           `json:"venue_b"`
	DriftBase     decimal.Decimal `json:"drift_base"`
	DriftNotional decimal.Decimal `json:"drift_notional"`
	MarkAgeMs     *int64          `json:"mark_age_ms,omitempty"`
	Unmatched     int             `json:"unmatched"`
}

// RuleSummary is the per-rule statistic block of strategy-models §7 —
// the output; no adjective replaces it.
type RuleSummary struct {
	RuleID             string           `json:"rule_id"`
	Strategy           Strategy         `json:"strategy"`
	Alerts             int64            `json:"alerts"`
	Executed           int64            `json:"executed"`
	Skipped            map[string]int64 `json:"skipped"`
	NetPnLQuote        decimal.Decimal  `json:"net_pnl_quote"`
	FeesQuote          decimal.Decimal  `json:"fees_quote"`
	FundingQuote       decimal.Decimal  `json:"funding_quote"`
	FundingRows        int64            `json:"funding_rows"`
	HitRate            *decimal.Decimal `json:"hit_rate,omitempty"`
	Samples            int64            `json:"samples"`
	MeanLifetimeS      *decimal.Decimal `json:"mean_lifetime_s,omitempty"`
	OpenPositions      int64            `json:"open_positions"`
	InventoryDrift     []DriftRow       `json:"inventory_drift"`
	PnLAfterRebalance  decimal.Decimal  `json:"pnl_after_rebalance"`
	MatchedPairs       int64            `json:"matched_pairs"`
	MatchedPairNet     decimal.Decimal  `json:"matched_pair_net"`
	RealisedSlipMean   *decimal.Decimal `json:"realised_slip_bps_mean,omitempty"`
	RealisedSlipP95    *decimal.Decimal `json:"realised_slip_bps_p95,omitempty"`
	SlipAllowanceBps   decimal.Decimal  `json:"slip_allowance_bps"`
	LastExecutionAt    *time.Time       `json:"last_execution_at,omitempty"`
	OldestOpenAgeS     *int64           `json:"oldest_open_age_s,omitempty"`
	UnwindCostQuote    decimal.Decimal  `json:"unwind_cost_quote"`
	PartialLegPnLQuote decimal.Decimal  `json:"partial_leg_pnl_quote"`
}

// Automation runs the registered tickers on the settings document's
// poll interval (re-read every tick so a hot poll_interval_s change is
// honoured without restart). It is a single goroutine: the evaluator
// and executor run in registration order within one tick, so the
// executor always sees the events the evaluator opened in the same
// poll (strategy-models §2.6 step 3 → 4).
type Automation struct {
	svc     *Service
	tickers []Ticker

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	now    func() time.Time
}

// NewAutomation builds the runner; nothing runs until Start.
func NewAutomation(svc *Service, tickers ...Ticker) *Automation {
	return &Automation{svc: svc, tickers: tickers, now: func() time.Time { return time.Now().UTC() }}
}

// Name implements app.Component.
func (a *Automation) Name() string { return "screener-automation" }

// Run implements app.Component: Start, block until ctx is done, Stop.
func (a *Automation) Run(ctx context.Context) error {
	a.Start(ctx)
	<-ctx.Done()
	a.Stop()
	return nil
}

// Start launches the tick loop. Calling Start twice is a no-op.
func (a *Automation) Start(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.done != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.done = make(chan struct{})
	go a.loop(ctx, a.done)
}

// Stop cancels the loop and waits for the in-flight tick to finish.
func (a *Automation) Stop() {
	a.mu.Lock()
	cancel, done := a.cancel, a.done
	a.cancel, a.done = nil, nil
	a.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (a *Automation) interval() time.Duration {
	s := a.svc.Current().Settings.PollIntervalS
	if s <= 0 {
		s = Defaults().PollIntervalS
	}
	return time.Duration(s) * time.Second
}

func (a *Automation) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	timer := time.NewTimer(a.interval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		a.TickOnce(ctx, a.now())
		timer.Reset(a.interval())
	}
}

// TickOnce runs every ticker once, in order. Exposed for tests and for
// the loop; safe to call while the loop is stopped.
func (a *Automation) TickOnce(ctx context.Context, now time.Time) {
	for _, t := range a.tickers {
		if ctx.Err() != nil {
			return
		}
		func() {
			defer func() {
				if r := recover(); r != nil && a.svc.log != nil {
					a.svc.log.Error("screener automation ticker panicked", "ticker", t.Name(), "panic", r)
				}
			}()
			t.Tick(ctx, now)
		}()
	}
}

// AutoPaper is set by app wiring when a paper executor runs.
func (s *Service) SetAutoPaper(src AutoPaperSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoPaper = src
}

// AutoPaper returns the executor's read model source, nil when none.
func (s *Service) AutoPaper() AutoPaperSource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.autoPaper
}
