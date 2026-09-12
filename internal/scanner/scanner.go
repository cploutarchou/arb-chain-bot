// Package scanner assembles the hot path (docs/architecture.md §6):
// dirty-market notifications → affected triangles → depth-aware sizing →
// buffered opportunity → deterministic risk gate → qualified/rejected
// events. Everything here is in-memory and bounded; consumers hang off
// the event channel, never inside the loop.
package scanner

import (
	"go.opentelemetry.io/otel/attribute"

	"github.com/cploutarchou/arb-chain-bot/internal/tracing"

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
	// NoViableSize counts triangles whose every candidate size fell
	// below the dust/min-notional floor — the one pre-gate exit that
	// emits no event, so without this counter it is invisible (T-062).
	NoViableSize atomic.Int64
	DroppedEvts  atomic.Int64 // consumer too slow (bounded fan-out)
	// Revalidations counts pre-execution re-checks (Revalidate);
	// RevalidationRejects those the gate refused the second time.
	Revalidations       atomic.Int64
	RevalidationRejects atomic.Int64
}

// CapitalView supplies the risk context's capital numbers (reservation
// manager in live wiring; static values in tests).
type CapitalView interface {
	Balance(asset exchange.Asset) (available, reserved decimal.Decimal)
	TriangleReserved(triangleID string) decimal.Decimal
}

// LedgerView supplies the risk context's session loss and drawdown per
// start asset (the engine's portfolio in live wiring). A nil view leaves
// both at zero, which means the daily-loss and drawdown limits can never
// trip — acceptable only for tests and profiles without a paper ledger.
type LedgerView interface {
	DailyLoss(start exchange.Asset) decimal.Decimal
	Drawdown(start exchange.Asset) decimal.Decimal
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
	Ledger   LedgerView // optional; see LedgerView
	Clock    func() time.Time
	IDGen    func() string
	Cfg      Config

	byIDOnce sync.Once
	byID     map[string]int // triangle ID → index into Topo.Triangles

	// strategy, when set, overrides Cfg/Resolver atomically (hot swap).
	strategy atomic.Pointer[Strategy]

	Out   chan Event // bounded; non-blocking sends with drop accounting
	Stats Stats

	// ClockHealthy is flipped by the clock manager; default healthy until
	// wired (the risk engine still gates everything else).
	ClockHealthy atomic.Bool

	// Sims reports concurrent simulations (paper engine); nil = zero.
	Sims func() int

	// EvalObserver, when set, receives each evaluation's wall duration
	// (metrics). Must be cheap; nil disables with zero hot-path cost.
	EvalObserver func(d time.Duration)
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

// CurrentConfig exposes the live evaluation config so callers outside
// the scan loop (e.g. the engine's staleness sweep) honor hot-swapped
// values instead of the boot literals.
func (s *Scanner) CurrentConfig() Config { return s.currentStrategy().Cfg }

// CurrentLimits exposes the live global risk limits (the session-level
// loss and drawdown thresholds the engine's breaker policy enforces).
func (s *Scanner) CurrentLimits() risk.Limits { return s.currentStrategy().Resolver.Global }

// triangle looks a triangle up by ID (lazy index over the topology).
func (s *Scanner) triangle(id string) (graph.Triangle, bool) {
	s.byIDOnce.Do(func() {
		s.byID = make(map[string]int, len(s.Topo.Triangles))
		for i, tri := range s.Topo.Triangles {
			s.byID[tri.ID] = i
		}
	})
	idx, ok := s.byID[id]
	if !ok {
		return graph.Triangle{}, false
	}
	return s.Topo.Triangles[idx], true
}

// breakerScopes are the registry scopes whose OPEN state gates one
// triangle: its exchange, the triangle itself, and each of its markets.
func breakerScopes(tri graph.Triangle) []string {
	return []string{
		"exchange:" + string(tri.Exchange), "triangle:" + tri.ID,
		"market:" + tri.Legs[0].Market.String(),
		"market:" + tri.Legs[1].Market.String(),
		"market:" + tri.Legs[2].Market.String(),
	}
}

func (s *Scanner) dailyLoss(start exchange.Asset) decimal.Decimal {
	if s.Ledger == nil {
		return decimal.Zero
	}
	return s.Ledger.DailyLoss(start)
}

func (s *Scanner) drawdown(start exchange.Asset) decimal.Decimal {
	if s.Ledger == nil {
		return decimal.Zero
	}
	return s.Ledger.Drawdown(start)
}

// Revalidate re-prices a qualified opportunity on the books as they are
// now and re-runs the risk gate, immediately before the paper engine
// commits capital (docs/risk.md §1: books moved, capital changed,
// breakers tripped). The size is kept: a full size search is the
// scanner's job and costs milliseconds; a fixed-size re-quote costs
// microseconds. Books whose versions have not moved skip the re-quote
// but never the gate — capital, breakers, the clock and the TTL move on
// their own.
//
// The returned opportunity keeps the caller's identity, detection time
// and TTL and carries the fresh economics. ok is false when the triangle
// is unknown or a book or its rules are missing; the caller skips.
//
// The caller is itself one of the active simulations by the time it
// revalidates, so the concurrency check counts the others only.
func (s *Scanner) Revalidate(op opportunity.Opportunity) (opportunity.Opportunity, risk.Decision, bool) {
	tri, ok := s.triangle(op.TriangleID)
	if !ok {
		return op, risk.Decision{}, false
	}
	now := s.Clock()
	st := s.currentStrategy()
	cfg := st.Cfg

	var data [3]pricing.MarketData
	var states [3]orderbook.State
	var ages [3]time.Duration
	var versions [3]uint64
	for i, leg := range tri.Legs {
		view, ok := s.Books.View(leg.Market, cfg.Depth)
		if !ok {
			return op, risk.Decision{}, false
		}
		rules, ok := s.Rules[leg.Market]
		if !ok {
			return op, risk.Decision{}, false
		}
		data[i] = pricing.MarketData{View: view, Rules: rules}
		states[i], ages[i], versions[i] = view.State, view.Age(now), view.Version
	}
	s.Stats.Revalidations.Add(1)

	fresh := op
	if op.NeedsRecalc(versions) {
		quote, err := pricing.QuoteCycle(tri, data, s.Fees, op.Quote.InputConsumed)
		if err != nil {
			// The depth the plan relied on is gone, or the size no longer
			// clears the venue's minimums: nothing executable remains.
			s.Stats.RevalidationRejects.Add(1)
			return op, risk.Decision{
				ConfigVersion: cfg.ConfigVersion, ReasonCode: risk.ReasonRevalidation,
				Checks: []risk.Check{{Name: risk.ReasonRevalidation, Observed: err.Error(),
					Threshold: "executable quote at the qualified size"}},
			}, true
		}
		rebuilt := opportunity.Build(op.ID, tri.Exchange, quote, cfg.Buffers, cfg.TTL, op.DetectedAt, cfg.ConfigVersion)
		fresh.Quote = rebuilt.Quote
		fresh.Buffers, fresh.BufferAmount = rebuilt.Buffers, rebuilt.BufferAmount
		fresh.EstimatedFinal, fresh.NetProfit = rebuilt.EstimatedFinal, rebuilt.NetProfit
		fresh.NetReturnBps, fresh.GrossReturnBps = rebuilt.NetReturnBps, rebuilt.GrossReturnBps
		fresh.RecommendedSize, fresh.ConfigVersion = rebuilt.RecommendedSize, rebuilt.ConfigVersion
	}
	fresh.DataQuality = dataQuality(ages, cfg.MaxBookAge)

	eff, disabled := st.Resolver.Effective(tri.Exchange, tri.Start, tri.ID)
	avail, reserved := s.Capital.Balance(tri.Start)
	others := s.sims() - 1
	if others < 0 {
		others = 0
	}
	rctx := risk.Context{
		Now:                   now,
		ConfigVersion:         cfg.ConfigVersion,
		BookStates:            states,
		BookAges:              ages,
		DataQuality:           fresh.DataQuality,
		ClockHealthy:          s.ClockHealthy.Load(),
		CapitalAvailable:      avail,
		CapitalReserved:       reserved,
		TriangleReserved:      s.Capital.TriangleReserved(tri.ID),
		ConcurrentSimulations: others,
		DailyLoss:             s.dailyLoss(tri.Start),
		Drawdown:              s.drawdown(tri.Start),
		BreakerOpen:           s.Breakers.AnyOpen(breakerScopes(tri)...),
	}
	decision := risk.Evaluate(&fresh, rctx, eff, disabled)
	if !decision.Allowed {
		s.Stats.RevalidationRejects.Add(1)
	}
	return fresh, decision, true
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
					// O11: one span per market evaluation — the hot stage's
					// unit of work. Noop (zero cost) unless tracing.Init ran.
					_, span := tracing.Start(ctx, "scanner.evaluate_market",
						attribute.String("market", id.String()))
					s.EvaluateMarket(id)
					span.End()
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
	if s.EvalObserver != nil {
		start := time.Now() // real clock: durations stay wall-time in replay
		defer func() { s.EvalObserver(time.Since(start)) }()
	}
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
	// The exact search: candidate sizes come from the books' own depth
	// breakpoints (every level boundary on every leg mapped back to the
	// start asset), so a profitable window narrower than any sampling
	// grid cannot be skipped (audit T1). The gate's size-dependent limits
	// shape the objective (audit T2): the search returns the most
	// profitable size the gate will accept, not the maximum-profit size
	// it would reject for price impact; when nothing is feasible the
	// unconstrained optimum flows to the gate so the rejection reason is
	// the real one.
	res, ok := cfg.Search.FindCycleConstrained(tri, data, s.Fees, minIn, maxIn, gateFeasible(eff, cfg.Buffers))
	if !ok {
		s.Stats.NoViableSize.Add(1)
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
		DailyLoss:             s.dailyLoss(tri.Start),
		Drawdown:              s.drawdown(tri.Start),
		BreakerOpen:           s.Breakers.AnyOpen(breakerScopes(tri)...),
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

// gateFeasible mirrors the risk gate's size-dependent checks — worst-leg
// price impact, minimum net edge after buffers, minimum expected profit
// — as a predicate over a sized quote. Size-independent checks (books,
// clock, breakers, capital) stay with the gate.
func gateFeasible(eff risk.Limits, b opportunity.Buffers) func(pricing.CycleQuote) bool {
	return func(q pricing.CycleQuote) bool {
		if eff.MaxPriceImpactBps.IsPositive() {
			for _, l := range q.Legs {
				if l.PriceImpactBps.GreaterThan(eff.MaxPriceImpactBps) {
					return false
				}
			}
		}
		if !q.InputConsumed.IsPositive() {
			return false
		}
		bufferAmt := q.InputConsumed.Mul(b.LatencyBps.Add(b.RiskBps)).Div(decimal.NewFromInt(10_000))
		netProfit := q.FinalAmount.Sub(bufferAmt).Sub(q.InputConsumed)
		if eff.MinExpectedProfit.IsPositive() && netProfit.LessThan(eff.MinExpectedProfit) {
			return false
		}
		netBps := netProfit.Div(q.InputConsumed).Mul(decimal.NewFromInt(10_000))
		return !netBps.LessThan(eff.MinNetEdgeBps)
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
