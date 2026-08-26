// Package scanner assembles the hot path (docs/architecture.md §6):
// dirty-market notifications → affected triangles → depth-aware sizing →
// buffered opportunity → deterministic risk gate → qualified/rejected
// events. Everything here is in-memory and bounded; consumers hang off
// the event channel, never inside the loop.
package scanner

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
)

// Config is the scanner's versioned strategy slice.
type Config struct {
	ConfigVersion int64
	Buffers       opportunity.Buffers
	TTL           time.Duration
	MinInput      decimal.Decimal // smallest cycle input worth evaluating
	Depth         int             // view depth for pricing
	Workers       int
	Search        pricing.SizeSearch
	// MaxBookAge drives both the cheap pre-gate and data quality; the
	// authoritative check is the risk engine's.
	MaxBookAge time.Duration
}

// Event is one evaluated triangle outcome.
type Event struct {
	Opportunity opportunity.Opportunity
	Decision    risk.Decision
}

// Stats are atomic scanner counters (metrics + status API).
type Stats struct {
	Evaluations  atomic.Int64
	Qualified    atomic.Int64
	Rejected     atomic.Int64
	SkippedBooks atomic.Int64 // triangles skipped for missing/unhealthy books
	DroppedEvts  atomic.Int64 // consumer too slow (bounded fan-out)
}

// CapitalView supplies the risk context's capital numbers (reservation
// manager in live wiring; static values in tests).
type CapitalView interface {
	Balance(asset exchange.Asset) (available, reserved decimal.Decimal)
	TriangleReserved(triangleID string) decimal.Decimal
}

// Strategy is the hot-swappable slice: evaluation config + risk limits.
// The config service publishes a new Strategy per version; every
// evaluation reads one consistent value.
type Strategy struct {
	Cfg      Config
	Resolver risk.Resolver
}

// Scanner evaluates triangles against live books.
type Scanner struct {
	Topo     *graph.Topology
	Books    *orderbook.Set
	Rules    map[exchange.MarketID]exchange.InstrumentRules
	Fees     *fees.Schedule
	Resolver risk.Resolver
	Breakers *risk.Registry
	Capital  CapitalView
	Clock    func() time.Time
	IDGen    func() string
	Cfg      Config

	// strategy, when set, overrides Cfg/Resolver atomically (hot swap).
	strategy atomic.Pointer[Strategy]

	Out   chan Event // bounded; non-blocking sends with drop accounting
	Stats Stats

	// ClockHealthy is flipped by the clock manager; default healthy until
	// wired (the risk engine still gates everything else).
	ClockHealthy atomic.Bool

	// Sims reports concurrent simulations (paper engine); nil = zero.
	Sims func() int
}

func (s *Scanner) Name() string { return "scanner" }

// SetStrategy swaps the evaluation config and risk limits atomically.
// Workers is start-time only; a changed value applies on restart.
func (s *Scanner) SetStrategy(st Strategy) { s.strategy.Store(&st) }

// currentStrategy returns the swapped-in strategy, falling back to the
// construction-time fields before the first SetStrategy.
func (s *Scanner) currentStrategy() Strategy {
	if p := s.strategy.Load(); p != nil {
		return *p
	}
	return Strategy{Cfg: s.Cfg, Resolver: s.Resolver}
}

// Run drains dirty markets with a worker pool until ctx cancels.
func (s *Scanner) Run(ctx context.Context) error {
	if s.Cfg.Workers <= 0 {
		s.Cfg.Workers = 2
	}
	work := make(chan exchange.MarketID, 1024)
	var wg sync.WaitGroup
	for i := 0; i < s.Cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case id := <-work:
					s.EvaluateMarket(id)
				}
			}
		}()
	}
	defer wg.Wait()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.Books.Signal():
			for _, id := range s.Books.Drain() {
				select {
				case work <- id:
				case <-ctx.Done():
					return ctx.Err()
				default:
					// Work queue saturated: re-mark so the market is not
					// lost; saturation is a breaker signal upstream.
					s.Books.MarkDirty(id)
				}
			}
		}
	}
}

// EvaluateMarket re-prices every triangle affected by one market's change.
func (s *Scanner) EvaluateMarket(id exchange.MarketID) {
	for _, idx := range s.Topo.AffectedBy(id) {
		s.EvaluateTriangle(s.Topo.Triangles[idx])
	}
}

// EvaluateTriangle runs the full pipeline for one triangle and emits an
// event when an opportunity was actually evaluated (books present).
func (s *Scanner) EvaluateTriangle(tri graph.Triangle) {
	now := s.Clock()
	st := s.currentStrategy()
	cfg := st.Cfg
	s.Stats.Evaluations.Add(1)

	var data [3]pricing.MarketData
	var states [3]orderbook.State
	var ages [3]time.Duration
	for i, leg := range tri.Legs {
		view, ok := s.Books.View(leg.Market, cfg.Depth)
		if !ok {
			s.Stats.SkippedBooks.Add(1)
			return
		}
		rules, ok := s.Rules[leg.Market]
		if !ok {
			s.Stats.SkippedBooks.Add(1)
			return
		}
		data[i] = pricing.MarketData{View: view, Rules: rules}
		states[i] = view.State
		ages[i] = view.Age(now)
	}
	// Cheap pre-gate: pricing on unhealthy books is wasted work; the risk
	// engine remains the authoritative (and audited) gate.
	for _, st := range states {
		if st != orderbook.StateHealthy {
			s.Stats.SkippedBooks.Add(1)
			return
		}
	}

	maxIn := pricing.CapacityHint(tri.Legs[0], data[0], s.Fees)
	eff, disabled := st.Resolver.Effective(tri.Exchange, tri.Start, tri.ID)
	if eff.MaxTradeSize.IsPositive() && maxIn.GreaterThan(eff.MaxTradeSize) {
		maxIn = eff.MaxTradeSize
	}
	minIn := cfg.MinInput
	if !minIn.IsPositive() {
		minIn = decimal.NewFromInt(1)
	}
	quote := func(in decimal.Decimal) (pricing.CycleQuote, error) {
		return pricing.QuoteCycle(tri, data, s.Fees, in)
	}
	res, ok := cfg.Search.Find(quote, minIn, maxIn)
	if !ok {
		return // no viable size at all (dust/min-notional floor above depth ceiling)
	}

	op := opportunity.Build(s.IDGen(), tri.Exchange, res.Best, cfg.Buffers, cfg.TTL, now, cfg.ConfigVersion)
	op.DataQuality = dataQuality(ages, cfg.MaxBookAge)
	_ = op.Transition(opportunity.StatusCalculating, "")

	avail, reserved := s.Capital.Balance(tri.Start)
	rctx := risk.Context{
		Now:                   now,
		ConfigVersion:         cfg.ConfigVersion,
		BookStates:            states,
		BookAges:              ages,
		DataQuality:           op.DataQuality,
		ClockHealthy:          s.ClockHealthy.Load(),
		CapitalAvailable:      avail,
		CapitalReserved:       reserved,
		TriangleReserved:      s.Capital.TriangleReserved(tri.ID),
		ConcurrentSimulations: s.sims(),
		BreakerOpen: s.Breakers.AnyOpen(
			"exchange:"+string(tri.Exchange), "triangle:"+tri.ID,
		),
	}
	decision := risk.Evaluate(&op, rctx, eff, disabled)
	if decision.Allowed {
		_ = op.Transition(opportunity.StatusQualified, "")
		s.Stats.Qualified.Add(1)
	} else {
		_ = op.Transition(opportunity.StatusRejected, decision.ReasonCode)
		s.Stats.Rejected.Add(1)
	}

	select {
	case s.Out <- Event{Opportunity: op, Decision: decision}:
	default:
		s.Stats.DroppedEvts.Add(1)
	}
}

func (s *Scanner) sims() int {
	if s.Sims == nil {
		return 0
	}
	return s.Sims()
}

// dataQuality derives a 0..1 score from book ages against the age budget:
// 1 when fresh, linear decay to 0 at the budget. Deterministic and
// explainable (the risk check shows the number and its threshold).
func dataQuality(ages [3]time.Duration, maxAge time.Duration) decimal.Decimal {
	if maxAge <= 0 {
		return decimal.NewFromInt(1)
	}
	worst := ages[0]
	for _, a := range ages[1:] {
		if a > worst {
			worst = a
		}
	}
	if worst <= 0 {
		return decimal.NewFromInt(1)
	}
	if worst >= maxAge {
		return decimal.Zero
	}
	frac := decimal.NewFromInt(int64(worst)).Div(decimal.NewFromInt(int64(maxAge)))
	return decimal.NewFromInt(1).Sub(frac)
}
