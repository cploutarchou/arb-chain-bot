// Package backtest drives a recorded market-data session through the
// COMPLETE platform pipeline — replayed books → the real scanner →
// deterministic risk engine → replay executor → portfolio — under §80
// stress scenarios (higher fees, higher latency, worse fills, lower
// liquidity). It is the profitability-validation harness (T-046): one
// recording × one scenario × one seed in, honest per-cycle evidence out.
//
// Determinism: single-threaded discrete-event execution over a virtual
// clock that advances only to recorded frame timestamps and simulated
// latency deadlines. While a cycle waits out latency, later frames whose
// receive time falls inside the wait ARE applied, so fills read books
// that moved during the wait — the same adverse drift live paper faces.
// IDs come from a counter, latency jitter from the seeded per-cycle RNG:
// the same inputs reproduce byte-identical results.
package backtest

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange/binance"
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
	"github.com/cploutarchou/arb-chain-bot/internal/marketdata"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
	"github.com/cploutarchou/arb-chain-bot/internal/portfolio"
	"github.com/cploutarchou/arb-chain-bot/internal/reservation"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
	"github.com/cploutarchou/arb-chain-bot/internal/scanner"
	"github.com/cploutarchou/arb-chain-bot/internal/simulation"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// Scenario is one §80 stress configuration. The zero value (after
// Normalize) is the unstressed baseline.
type Scenario struct {
	Name string `json:"name"`
	// FeeBumpBps raises maker AND taker rates ("higher fees").
	FeeBumpBps int64 `json:"fee_bump_bps"`
	// LatencyScale multiplies every latency-model component ("higher
	// latency"); 1 = unchanged.
	LatencyScale float64 `json:"latency_scale"`
	// FillDepthFactor scales the quantity the EXECUTOR sees at fill time
	// ("worse fills"): the planner prices the full book, fills find less.
	// (0,1]; 1 = unchanged.
	FillDepthFactor decimal.Decimal `json:"fill_depth_factor"`
	// WorldDepthFactor scales quantities in every replayed frame ("lower
	// liquidity"): detection and fills both see a thinner market. (0,1].
	WorldDepthFactor decimal.Decimal `json:"world_depth_factor"`
}

// Normalize fills zero values with the identity stress.
func (s Scenario) Normalize() Scenario {
	if s.Name == "" {
		s.Name = "baseline"
	}
	if s.LatencyScale <= 0 {
		s.LatencyScale = 1
	}
	if !s.FillDepthFactor.IsPositive() {
		s.FillDepthFactor = decimal.NewFromInt(1)
	}
	if !s.WorldDepthFactor.IsPositive() {
		s.WorldDepthFactor = decimal.NewFromInt(1)
	}
	return s
}

// Options configure one backtest run.
type Options struct {
	// Segments are recording files in replay order (depth-*.seg.zst).
	Segments []string
	// Streams is the recorder's stream table (REST frames carry the
	// symbol via stream id).
	Streams map[uint16]exchange.Symbol
	// Markets supply instrument rules and graph edges; only recorded
	// symbols matter.
	Markets []exchange.Market
	// StartingAssets seed triangle enumeration (e.g. USDT).
	StartingAssets []exchange.Asset
	// InitialBalances fund the virtual portfolio.
	InitialBalances map[exchange.Asset]decimal.Decimal
	// Params are the strategy knobs (scanner + risk); zero value means
	// strategy.DefaultParams().
	Params strategy.Params
	// BaseFees is the unstressed fee schedule (e.g. 10 bps / 10 bps).
	BaseFees fees.Rate
	// Latency is the unstressed latency model; zero value takes the
	// PAPER-mode defaults.
	Latency simulation.LatencyModel
	// Seed roots every latency draw.
	Seed     int64
	Scenario Scenario
}

// CycleRecord is one settled cycle's §80 evidence.
type CycleRecord struct {
	CycleID       string          `json:"cycle_id"`
	OpportunityID string          `json:"opportunity_id"`
	TriangleID    string          `json:"triangle_id"`
	At            time.Time       `json:"at"`
	Outcome       string          `json:"outcome"`
	GrossBps      decimal.Decimal `json:"gross_bps"` // planned, fees in
	NetBps        decimal.Decimal `json:"net_bps"`   // planned, buffers out
	Input         decimal.Decimal `json:"input"`
	RealizedPnL   decimal.Decimal `json:"realized_pnl"`
	TotalPnL      decimal.Decimal `json:"total_pnl"` // realized + exposure mark
	SlippageBps   *string         `json:"slippage_bps,omitempty"`
	AvgLegLatency time.Duration   `json:"avg_leg_latency_ns"`
}

// Result is one (recording, scenario, seed) run.
type Result struct {
	Scenario Scenario `json:"scenario"`
	Seed     int64    `json:"seed"`

	Frames      int64     `json:"frames"`
	FrameErrors int64     `json:"frame_errors"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`

	Evaluations int64 `json:"evaluations"`
	Qualified   int64 `json:"qualified"`
	Rejected    int64 `json:"rejected"`
	SkippedBook int64 `json:"skipped_unhealthy"`

	Cycles []CycleRecord `json:"cycles"`

	// Per start asset (decimal strings for exact JSON).
	NetPnL      map[string]string `json:"net_pnl"`
	FeesPaid    map[string]string `json:"fees_paid"`
	MaxDrawdown map[string]string `json:"max_drawdown"`
	// Turnover is total start-asset input deployed across cycles.
	Turnover map[string]string `json:"turnover"`
}

// Run executes one backtest. Everything is in-memory; the caller owns
// persistence of the Result.
func Run(opts Options) (Result, error) {
	sc := opts.Scenario.Normalize()
	if len(opts.Segments) == 0 {
		return Result{}, fmt.Errorf("backtest: no segments")
	}
	if len(opts.Streams) == 0 {
		return Result{}, fmt.Errorf("backtest: empty stream table")
	}
	params := opts.Params
	if err := params.Validate(); err != nil {
		params = strategy.DefaultParams()
	}
	lat := opts.Latency
	if lat.SubmitBase == 0 && lat.FillBase == 0 {
		lat = simulation.LatencyModel{
			SubmitBase: 20 * time.Millisecond, SubmitJitter: 30 * time.Millisecond,
			FillBase: 30 * time.Millisecond, FillJitter: 50 * time.Millisecond,
		}
	}
	lat = scaleLatency(lat, sc.LatencyScale)
	baseFees := opts.BaseFees
	if baseFees.Taker.IsZero() && baseFees.Maker.IsZero() {
		baseFees = fees.Rate{Maker: decimal.RequireFromString("0.001"), Taker: decimal.RequireFromString("0.001")}
	}
	bump := decimal.NewFromInt(sc.FeeBumpBps).Div(decimal.NewFromInt(10_000))
	sched, err := fees.NewSchedule(binance.ID, exchange.FeeInReceived,
		fees.Rate{Maker: baseFees.Maker.Add(bump), Taker: baseFees.Taker.Add(bump)})
	if err != nil {
		return Result{}, fmt.Errorf("backtest: fees: %w", err)
	}

	// World: replayed books, optionally thinned.
	replayer := marketdata.NewReplayer(opts.Streams)
	// Books in a recording degrade to STALE on the recorded clock with the
	// same age budget the scanner gates on (audit M3), so a quiet market
	// in replay is judged the way live would judge it.
	replayer.MaxBookAge = params.ScannerConfig(1).MaxBookAge
	if !sc.WorldDepthFactor.Equal(decimal.NewFromInt(1)) {
		f := sc.WorldDepthFactor
		replayer.Transform = func(ev *orderbook.DepthEvent) {
			for i := range ev.Bids {
				ev.Bids[i].Qty = ev.Bids[i].Qty.Mul(f)
			}
			for i := range ev.Asks {
				ev.Asks[i].Qty = ev.Asks[i].Qty.Mul(f)
			}
		}
	}

	rules := make(map[exchange.MarketID]exchange.InstrumentRules, len(opts.Markets))
	for _, m := range opts.Markets {
		rules[m.ID] = m.Rules
	}
	topo := graph.Build(binance.ID, opts.Markets, opts.StartingAssets)
	if len(topo.Triangles) == 0 {
		return Result{}, fmt.Errorf("backtest: no triangles from %d markets", len(opts.Markets))
	}

	h := &harness{
		frames:   newFrameIter(opts.Segments),
		replayer: replayer,
		dirty:    map[exchange.MarketID]bool{},
	}
	// The clock starts at the first frame; peek to anchor it.
	first, ok := h.frames.peek()
	if !ok {
		return Result{}, fmt.Errorf("backtest: recording is empty")
	}
	h.clock = simulation.NewVirtualClock(first.Recv)

	idSeq := 0
	idGen := func() string { idSeq++; return fmt.Sprintf("bt-%08d", idSeq) }

	resv := reservation.New(opts.InitialBalances, idGen, h.clock.Now)
	port := portfolio.New(resv, opts.InitialBalances)
	breakers := risk.NewRegistry(func(risk.Transition) {})
	marker := portfolio.BookMarker{Books: replayer.Books, Markets: opts.Markets}

	var execBooks simulation.BookSource = replayer.Books
	if !sc.FillDepthFactor.Equal(decimal.NewFromInt(1)) {
		execBooks = haircutBooks{src: replayer.Books, factor: sc.FillDepthFactor}
	}
	executor := simulation.NewReplay(execBooks, rulesLookup(rules), sched,
		h.clock, pumpWaiter{h: h}, marker,
		simulation.Config{
			Latency:           lat,
			LimitToleranceBps: decimal.NewFromInt(20),
			Depth:             50,
			Seed:              opts.Seed,
		}, idGen)

	scn := &scanner.Scanner{
		Topo:     topo,
		Books:    replayer.Books,
		Rules:    rules,
		Fees:     sched,
		Resolver: params.RiskResolver(),
		Breakers: breakers,
		Capital:  resv,
		Clock:    h.clock.Now,
		IDGen:    idGen,
		Cfg:      params.ScannerConfig(1),
		Out:      make(chan scanner.Event, 4096),
	}
	scn.ClockHealthy.Store(true)

	byID := make(map[string]graph.Triangle, len(topo.Triangles))
	for _, tri := range topo.Triangles {
		byID[tri.ID] = tri
	}

	res := Result{
		Scenario: sc, Seed: opts.Seed, From: first.Recv,
		NetPnL: map[string]string{}, FeesPaid: map[string]string{},
		MaxDrawdown: map[string]string{}, Turnover: map[string]string{},
	}
	maxDD := map[exchange.Asset]decimal.Decimal{}
	turnover := map[exchange.Asset]decimal.Decimal{}

	runCycle := func(ev scanner.Event) {
		op := ev.Opportunity
		tri, ok := byID[op.TriangleID]
		if !ok {
			return
		}
		now := h.clock.Now()
		if op.Expired(now) {
			return
		}
		conflicts := make([]string, 0, 3)
		for _, leg := range tri.Legs {
			conflicts = append(conflicts, fmt.Sprintf("mkt:%s:%s", leg.Market, leg.Side))
		}
		r, err := resv.Reserve(op.ID, op.Start, op.Quote.InputConsumed, op.TriangleID, conflicts)
		if err != nil || r.State != reservation.StateActive {
			return
		}
		plan := execution.CyclePlan{CycleID: idGen(), Opportunity: &op, Triangle: tri}
		result, err := executor.ExecuteCycle(context.Background(), plan)
		if err != nil {
			_ = resv.Release(r.ID)
			return
		}
		switch result.Outcome {
		case execution.OutcomeRejected, execution.OutcomeExpired:
			_ = resv.Release(r.ID)
		default:
			if err := resv.Settle(r.ID, result.InputConsumed); err == nil {
				if result.FinalAmount.IsPositive() {
					_ = resv.Credit(result.StartAsset, result.FinalAmount)
				}
			}
			_ = port.ApplyCycle(result, false)
			turnover[result.StartAsset] = turnover[result.StartAsset].Add(result.InputConsumed)
		}
		if dd := port.CurrentDrawdown(op.Start); dd.GreaterThan(maxDD[op.Start]) {
			maxDD[op.Start] = dd
		}
		res.Cycles = append(res.Cycles, record(op, result))
	}

	// Discrete-event main loop: apply a frame, evaluate what it touched,
	// then execute every qualified opportunity to settlement (the
	// executor's waits pump further frames through h).
	for {
		fr, ok := h.frames.peek()
		if !ok {
			break
		}
		h.frames.pop()
		h.applyFrame(fr)
		for {
			ids := h.takeDirty()
			if len(ids) == 0 {
				break
			}
			for _, id := range ids {
				scn.EvaluateMarket(id)
			}
			for {
				var ev scanner.Event
				select {
				case ev = <-scn.Out:
				default:
					ev = scanner.Event{}
				}
				if ev.Opportunity.ID == "" {
					break
				}
				if ev.Opportunity.Status == opportunity.StatusQualified {
					runCycle(ev)
				}
			}
		}
	}

	res.Frames = h.applied
	res.FrameErrors = h.errors
	res.To = h.clock.Now()
	res.Evaluations = scn.Stats.Evaluations.Load()
	res.Qualified = scn.Stats.Qualified.Load()
	res.Rejected = scn.Stats.Rejected.Load()
	res.SkippedBook = scn.Stats.SkippedBooks.Load()
	for _, a := range opts.StartingAssets {
		res.NetPnL[string(a)] = port.Realized(a).String()
		res.FeesPaid[string(a)] = port.FeesPaid(a).String()
		res.MaxDrawdown[string(a)] = maxDD[a].StringFixed(6)
		res.Turnover[string(a)] = turnover[a].String()
	}
	return res, nil
}

func record(op opportunity.Opportunity, r execution.CycleResult) CycleRecord {
	rec := CycleRecord{
		CycleID: r.CycleID, OpportunityID: op.ID, TriangleID: op.TriangleID,
		At: r.SettledAt, Outcome: string(r.Outcome),
		GrossBps: op.GrossReturnBps, NetBps: op.NetReturnBps,
		Input: r.InputConsumed, RealizedPnL: r.RealizedPnL, TotalPnL: r.TotalPnL,
	}
	// Slippage is meaningful only when the cycle converted back to the
	// start asset — same rule as persistence.
	switch r.Outcome {
	case execution.OutcomeAllFilled, execution.OutcomeLeg1Partial, execution.OutcomePartialCycle:
		s := r.SlippageBps.String()
		rec.SlippageBps = &s
	}
	var total time.Duration
	var n int
	for _, o := range r.Orders {
		if !o.FilledAt.IsZero() {
			total += o.FilledAt.Sub(o.CreatedAt)
			n++
		}
	}
	if n > 0 {
		rec.AvgLegLatency = total / time.Duration(n)
	}
	return rec
}

func scaleLatency(l simulation.LatencyModel, f float64) simulation.LatencyModel {
	if f == 1 {
		return l
	}
	s := func(d time.Duration) time.Duration { return time.Duration(float64(d) * f) }
	return simulation.LatencyModel{
		SubmitBase: s(l.SubmitBase), SubmitJitter: s(l.SubmitJitter),
		FillBase: s(l.FillBase), FillJitter: s(l.FillJitter),
	}
}

// harness owns the frame cursor, virtual clock, and dirty-market set.
type harness struct {
	frames   *frameIter
	replayer *marketdata.Replayer
	clock    *simulation.VirtualClock
	dirty    map[exchange.MarketID]bool
	applied  int64
	errors   int64
}

func (h *harness) advanceTo(t time.Time) {
	if d := t.Sub(h.clock.Now()); d > 0 {
		h.clock.Advance(d)
	}
}

func (h *harness) applyFrame(fr marketdata.Frame) {
	h.advanceTo(fr.Recv)
	if err := h.replayer.Apply(fr); err != nil {
		h.errors++
	}
	h.applied++
	for _, id := range h.replayer.Books.Drain() {
		h.dirty[id] = true
	}
}

func (h *harness) takeDirty() []exchange.MarketID {
	if len(h.dirty) == 0 {
		return nil
	}
	out := make([]exchange.MarketID, 0, len(h.dirty))
	for id := range h.dirty {
		out = append(out, id)
	}
	// Deterministic order.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].String() < out[i].String() {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	h.dirty = map[exchange.MarketID]bool{}
	return out
}

// pumpWaiter elapses simulated latency by applying every frame recorded
// inside the wait window before advancing the clock to the deadline —
// fills then read books that moved while the order was in flight.
// Markets touched during the pump are only marked; evaluation happens
// after the in-flight cycle settles (no reentrancy).
type pumpWaiter struct{ h *harness }

func (w pumpWaiter) Wait(_ context.Context, d time.Duration) error {
	target := w.h.clock.Now().Add(d)
	for {
		fr, ok := w.h.frames.peek()
		if !ok || fr.Recv.After(target) {
			break
		}
		w.h.frames.pop()
		w.h.applyFrame(fr)
	}
	w.h.advanceTo(target)
	return nil
}

// haircutBooks scales fill-time quantities ("worse fills"): the planner
// saw the full book, execution finds factor× of it.
type haircutBooks struct {
	src    simulation.BookSource
	factor decimal.Decimal
}

func (b haircutBooks) View(id exchange.MarketID, depth int) (orderbook.View, bool) {
	v, ok := b.src.View(id, depth)
	if !ok {
		return v, false
	}
	bids := make([]orderbook.Level, len(v.Bids))
	for i, l := range v.Bids {
		bids[i] = orderbook.Level{Price: l.Price, Qty: l.Qty.Mul(b.factor)}
	}
	asks := make([]orderbook.Level, len(v.Asks))
	for i, l := range v.Asks {
		asks[i] = orderbook.Level{Price: l.Price, Qty: l.Qty.Mul(b.factor)}
	}
	v.Bids, v.Asks = bids, asks
	return v, true
}

// rulesLookup adapts the rules map to simulation.RulesSource.
type rulesLookup map[exchange.MarketID]exchange.InstrumentRules

func (r rulesLookup) Rules(id exchange.MarketID) (exchange.InstrumentRules, bool) {
	rules, ok := r[id]
	return rules, ok
}

// frameIter streams frames across segment files with one-frame lookahead,
// loading segments lazily so long recordings never sit in memory whole.
type frameIter struct {
	paths []string
	buf   []marketdata.Frame
	idx   int
	err   error
}

func newFrameIter(paths []string) *frameIter { return &frameIter{paths: paths} }

func (it *frameIter) peek() (marketdata.Frame, bool) {
	for it.idx >= len(it.buf) {
		if len(it.paths) == 0 {
			return marketdata.Frame{}, false
		}
		path := it.paths[0]
		it.paths = it.paths[1:]
		it.buf = it.buf[:0]
		it.idx = 0
		if err := marketdata.ReadSegment(path, func(fr marketdata.Frame) error {
			it.buf = append(it.buf, fr)
			return nil
		}); err != nil {
			it.err = fmt.Errorf("backtest: %s: %w", path, err)
			return marketdata.Frame{}, false
		}
	}
	return it.buf[it.idx], true
}

func (it *frameIter) pop() { it.idx++ }
