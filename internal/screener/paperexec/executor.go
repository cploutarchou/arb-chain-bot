package paperexec

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/reservation"
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/alerts"
	"github.com/cploutarchou/arb-chain-bot/internal/simulation"
)

// PerpWalletSuffix names the per-venue futures wallet asset in the
// paper balances: carry / funding-harvest collateral is reserved from
// "<quote>:perp" (e.g. paper.balances.binance["USDT:perp"]) so the spot
// and futures wallets are separate, as §3.1 requires.
const PerpWalletSuffix = ":perp"

// Options tune the executor; zero values use the documented defaults.
type Options struct {
	Waiter  simulation.Waiter // nil = simulation.RealWaiter{}
	Latency *simulation.LatencyModel
	TolBps  *decimal.Decimal
	Seed    int64
	IDGen   func() string
	// Entitle, when set, is consulted before every automatic paper
	// execution (T-082): it resolves the rule's organisation and
	// answers whether the strategy, the concurrent-position cap and the
	// size cap allow it. nil = no gating (single-tenant profiles/tests).
	Entitle EntitlementCheck
}

// EntitlementCheck answers (reason, ok) for one prospective execution;
// reason is recorded as the skipped position's detail when !ok.
type EntitlementCheck func(ctx context.Context, ruleID, strategy string, sizeQuote decimal.Decimal) (reason string, ok bool)

// Executor is the auto-paper engine: one instance per process.
type Executor struct {
	svc    *screener.Service
	ledger Ledger
	log    *slog.Logger

	waiter  simulation.Waiter
	latency simulation.LatencyModel
	tolBps  decimal.Decimal
	seed    int64
	idGen   func() string

	entitle EntitlementCheck

	mu       sync.Mutex           // serialises executions and balance changes
	outcomes map[outcomeKey]int64 // screener_paper_executions_total{strategy,outcome}
	wallets  map[screener.Venue]*reservation.Manager
	loaded   bool
	pending  []pendingSlip
	lastTick time.Time
}

type pendingSlip struct {
	execID string
	legs   []pendingLeg
}

type pendingLeg struct {
	venue  screener.Venue
	market string
	side   string
	base   string
	quote  string
	price  decimal.Decimal
}

// New builds an executor over the service (book, settings, funding
// history, events) and the ledger.
func New(svc *screener.Service, ledger Ledger, log *slog.Logger, opts Options) *Executor {
	if log == nil {
		log = slog.Default()
	}
	x := &Executor{svc: svc, ledger: ledger, log: log, waiter: opts.Waiter, latency: DefaultLatency,
		tolBps: DefaultLimitToleranceBps, seed: opts.Seed, idGen: opts.IDGen, wallets: map[screener.Venue]*reservation.Manager{},
		entitle: opts.Entitle, outcomes: map[outcomeKey]int64{}}
	if x.waiter == nil {
		x.waiter = simulation.RealWaiter{}
	}
	if opts.Latency != nil {
		x.latency = *opts.Latency
	}
	if opts.TolBps != nil {
		x.tolBps = *opts.TolBps
	}
	if x.idGen == nil {
		x.idGen = func() string { return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String() }
	}
	return x
}

// Name implements screener.Ticker.
func (x *Executor) Name() string { return "paperexec" }

// Outcome values of Outcomes(): a position row was written either as a
// simulated execution (OPEN/CLOSED) or as a skip (SKIPPED, any reason).
const (
	OutcomeExecuted = "executed"
	OutcomeSkipped  = "skipped"
)

type outcomeKey struct {
	Strategy screener.Strategy
	Outcome  string
}

// OutcomeCount is one cumulative (strategy, outcome) counter.
type OutcomeCount struct {
	Strategy screener.Strategy
	Outcome  string
	Count    int64
}

// countOutcome tallies one inserted position (caller holds x.mu).
func (x *Executor) countOutcome(p Position) {
	outcome := OutcomeExecuted
	if p.Status == StatusSkipped {
		outcome = OutcomeSkipped
	}
	x.outcomes[outcomeKey{Strategy: p.Strategy, Outcome: outcome}]++
}

// Outcomes returns the cumulative execution outcomes since process
// start, sorted by strategy then outcome (metrics source).
func (x *Executor) Outcomes() []OutcomeCount {
	x.mu.Lock()
	defer x.mu.Unlock()
	out := make([]OutcomeCount, 0, len(x.outcomes))
	for k, n := range x.outcomes {
		out = append(out, OutcomeCount{Strategy: k.Strategy, Outcome: k.Outcome, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Strategy != out[j].Strategy {
			return out[i].Strategy < out[j].Strategy
		}
		return out[i].Outcome < out[j].Outcome
	})
	return out
}

// OnOpen is the alerts.OpenHook: called once per opened event.
func (x *Executor) OnOpen(ctx context.Context, s alerts.Signal, ev screener.Event) {
	if !s.Rule.AutoPaper {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if err := x.ensureWallets(ctx); err != nil {
		x.log.Error("paperexec: loading balances failed", "error", err)
		return
	}
	now := s.At
	// Entitlement preconditions (packages.md §3.2 auto_paper.*): the
	// rule's organisation may not have the strategy, may be over its
	// concurrent-position cap, or may cap the per-execution size. The
	// signal still fired as an alert; only the paper leg is skipped,
	// recorded as skipped: entitlement.
	if x.entitle != nil {
		if reason, ok := x.entitle(ctx, s.Rule.ID, string(s.Strategy), s.Rule.PaperSizeQuote); !ok {
			x.skip(ctx, s, ev, now, "entitlement", reason)
			return
		}
	}
	switch s.Strategy {
	case screener.StrategyCrossVenueSpot:
		x.executeSpot(ctx, s, ev, now)
	default:
		x.openPerp(ctx, s, ev, now)
	}
}

// Tick implements screener.Ticker: resolves realised-slippage
// measurements from the previous poll, then manages open positions
// (funding accrual, exits, stops). It runs BEFORE the evaluator's
// hooks in a poll because the automation runner registers the executor
// after the evaluator — the order is evaluator → executor within one
// tick, but OnOpen executes synchronously inside the evaluator's Tick.
func (x *Executor) Tick(ctx context.Context, now time.Time) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if err := x.ensureWallets(ctx); err != nil {
		x.log.Error("paperexec: loading balances failed", "error", err)
		return
	}
	x.resolvePendingSlip(ctx)
	x.managePositions(ctx, now)
	x.lastTick = now
}

func (x *Executor) pollInterval() time.Duration {
	s := x.svc.Current().Settings.PollIntervalS
	if s <= 0 {
		s = 5
	}
	return time.Duration(s) * time.Second
}

func (x *Executor) maxPlausibleSpreadBps() decimal.Decimal {
	return x.svc.Current().Settings.EffectiveMaxPlausibleSpreadBps()
}

func (x *Executor) spotFee(v screener.Venue) (decimal.Decimal, bool) {
	vs, ok := x.svc.Current().Settings.Venues[v]
	if !ok || !vs.Enabled {
		return decimal.Decimal{}, false
	}
	return vs.SpotTakerBps, true
}

func (x *Executor) perpFee(v screener.Venue) (decimal.Decimal, bool) {
	vs, ok := x.svc.Current().Settings.Venues[v]
	if !ok || !vs.Enabled || !vs.PerpsEnabled {
		return decimal.Decimal{}, false
	}
	return vs.PerpTakerBps, true
}

// ensureWallets loads per-venue balances: ledger rows first, settings
// paper.balances for any (venue, asset) the ledger has no row for
// (persisted immediately so the seed is visible in the read model).
// Requires x.mu.
func (x *Executor) ensureWallets(ctx context.Context) error {
	if x.loaded {
		return nil
	}
	rows, err := x.ledger.ListBalances(ctx)
	if err != nil {
		return err
	}
	initial := map[screener.Venue]map[exchange.Asset]decimal.Decimal{}
	have := map[[2]string]bool{}
	for _, b := range rows {
		if initial[b.Venue] == nil {
			initial[b.Venue] = map[exchange.Asset]decimal.Decimal{}
		}
		initial[b.Venue][exchange.Asset(b.Asset)] = b.Balance
		have[[2]string{string(b.Venue), b.Asset}] = true
	}
	now := time.Now().UTC()
	for venue, bal := range x.svc.Current().Settings.Paper.Balances {
		for asset, amt := range bal {
			if have[[2]string{string(venue), asset}] {
				continue
			}
			if initial[venue] == nil {
				initial[venue] = map[exchange.Asset]decimal.Decimal{}
			}
			initial[venue][exchange.Asset(asset)] = amt
			if err := x.ledger.UpsertBalance(ctx, venue, asset, amt, now); err != nil {
				return err
			}
		}
	}
	for venue, bal := range initial {
		x.wallets[venue] = reservation.New(bal, x.idGen, func() time.Time { return time.Now().UTC() })
	}
	x.loaded = true
	return nil
}

func (x *Executor) wallet(v screener.Venue) *reservation.Manager {
	m, ok := x.wallets[v]
	if !ok {
		m = reservation.New(map[exchange.Asset]decimal.Decimal{}, x.idGen, func() time.Time { return time.Now().UTC() })
		x.wallets[v] = m
	}
	return m
}

// persistBalance writes one (venue, asset) available balance.
func (x *Executor) persistBalance(ctx context.Context, v screener.Venue, asset string, at time.Time) {
	avail, _ := x.wallet(v).Balance(exchange.Asset(asset))
	if err := x.ledger.UpsertBalance(ctx, v, asset, avail, at); err != nil {
		x.log.Error("paperexec: balance persist failed", "venue", v, "asset", asset, "error", err)
	}
}

// skip records a SKIPPED position row with its reason (§2.6 step 4:
// skipped[reason]++) and logs it.
func (x *Executor) skip(ctx context.Context, s alerts.Signal, ev screener.Event, now time.Time, reason, detail string) {
	p := Position{
		ID: "pos-" + x.idGen(), RuleID: s.Rule.ID, EventID: ev.ID, Strategy: s.Strategy,
		Base: s.Lane.Base, Quote: s.Lane.Quote, VenueA: s.Lane.VenueA, VenueB: s.Lane.VenueB,
		Qty: decimal.Zero, OpenedAt: now, Status: StatusSkipped, SkippedReason: reason,
		OpenPayload: map[string]any{"detail": detail, "score_bps": s.Score().String()},
	}
	t := now
	p.ClosedAt = &t
	x.countOutcome(p)
	if err := x.ledger.InsertPosition(ctx, p); err != nil {
		x.log.Error("paperexec: skip row insert failed", "error", err)
	}
	x.log.Info("paperexec: skipped", "rule", s.Rule.ID, "reason", reason, "detail", detail,
		"base", s.Lane.Base, "venue_a", s.Lane.VenueA, "venue_b", s.Lane.VenueB)
}

func (x *Executor) insertExecution(ctx context.Context, e Execution) {
	if err := x.ledger.InsertExecution(ctx, e); err != nil {
		x.log.Error("paperexec: execution insert failed", "id", e.ID, "error", err)
	}
}

// queueSlip schedules the next-poll realised-slippage measurement.
func (x *Executor) queueSlip(execID string, fills []Fill) {
	ps := pendingSlip{execID: execID}
	for _, f := range fills {
		if f.Status != "FILLED" {
			continue
		}
		ps.legs = append(ps.legs, pendingLeg{venue: f.Venue, market: f.Market, side: f.Side, base: f.Base, quote: f.Quote, price: f.QuotePrice})
	}
	if len(ps.legs) > 0 {
		x.pending = append(x.pending, ps)
	}
}

func (x *Executor) sidePrice(venue screener.Venue, market, side, base, quote string) (decimal.Decimal, bool) {
	if market == "perp" {
		p, ok := x.svc.Book.PerpFor(venue, base)
		if !ok {
			return decimal.Decimal{}, false
		}
		if side == "BUY" {
			return p.Ask, p.Ask.IsPositive()
		}
		return p.Bid, p.Bid.IsPositive()
	}
	q, ok := x.svc.Book.QuotesFor(base, quote)[venue]
	if !ok {
		return decimal.Decimal{}, false
	}
	if side == "BUY" {
		return q.Ask, q.Ask.IsPositive()
	}
	return q.Bid, q.Bid.IsPositive()
}

func (x *Executor) resolvePendingSlip(ctx context.Context) {
	pending := x.pending
	x.pending = nil
	for _, ps := range pending {
		sum := decimal.Zero
		n := 0
		for _, l := range ps.legs {
			next, ok := x.sidePrice(l.venue, l.market, l.side, l.base, l.quote)
			if !ok {
				continue
			}
			sum = sum.Add(realisedSlipBps(l.side, l.price, next))
			n++
		}
		if n == 0 {
			continue
		}
		if err := x.ledger.SetExecutionSlip(ctx, ps.execID, sum.Div(decimal.NewFromInt(int64(n)))); err != nil {
			x.log.Error("paperexec: realised slip update failed", "id", ps.execID, "error", err)
		}
	}
}

func errf(format string, a ...any) string { return fmt.Sprintf(format, a...) }
