package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange/binance"
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
	"github.com/cploutarchou/arb-chain-bot/internal/marketdata"
	"github.com/cploutarchou/arb-chain-bot/internal/metrics"
	"github.com/cploutarchou/arb-chain-bot/internal/notification"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
	"github.com/cploutarchou/arb-chain-bot/internal/paper"
	"github.com/cploutarchou/arb-chain-bot/internal/portfolio"
	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
	"github.com/cploutarchou/arb-chain-bot/internal/realtime"
	"github.com/cploutarchou/arb-chain-bot/internal/reservation"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
	"github.com/cploutarchou/arb-chain-bot/internal/scanner"
	"github.com/cploutarchou/arb-chain-bot/internal/simulation"
	"github.com/cploutarchou/arb-chain-bot/internal/storage"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// Engine assembles the trading core for the first exchange: metadata →
// topology → feed → books → scanner. It runs until ctx cancels; a failed
// metadata bootstrap keeps retrying rather than pretending to scan.
//
// Current wiring status (docs/MASTER_PLAN.md is authoritative): live feed
// + scanner + risk + reservation are wired; the paper engine loop,
// persistence, and realtime fan-out attach behind the scanner's event
// stream as their tasks complete.
type Engine struct {
	cfg config.Bootstrap
	log *slog.Logger

	// Hub, when set by the component wiring, receives scanner/health
	// events for the console.
	Hub *realtime.Hub
	// Store, when set, enables persistence through the outbox.
	Store *storage.Store
	// Strategy, when set, supplies the versioned dynamic config and
	// hot-swaps the scanner on every applied version (T-034).
	Strategy *strategy.Service
	// Metrics, when set, receives the engine's instrument sources; hot
	// paths pay only atomic adds (T-035).
	Metrics *metrics.Metrics
	// Notifier, when set, receives platform alerts (never called on the
	// hot path; keys per SKILL §58 with cooldown/dedup in the service).
	Notifier *notification.Service
	// Center, when set, supplies the live active-alert count (AI input).
	Center *notification.Center

	mu     sync.RWMutex
	scn    *scanner.Scanner
	topo   *graph.Topology
	pap    *paper.Engine
	rctl   *marketdata.RecorderControl
	port   *portfolio.Portfolio
	feed   *binance.Feed
	resv   *reservation.Manager
	brk    *risk.Registry
	marker portfolio.BookMarker
	ready  bool

	oppMu        sync.Mutex
	recentOpps   []RecentOpportunity
	rejectCounts map[string]int64 // risk reason code → count (AI input)
}

// RecentOpportunity is a compact ring entry for /opportunities and the
// console's recent list.
type RecentOpportunity struct {
	ID         string    `json:"id"`
	TriangleID string    `json:"triangle_id"`
	NetBps     string    `json:"net_bps"`
	Profit     string    `json:"profit"`
	Input      string    `json:"input"`
	At         time.Time `json:"at"`
}

const recentOppCap = 32

// RecentOpportunities returns the latest qualified opportunities,
// newest first.
func (e *Engine) RecentOpportunities(limit int) []RecentOpportunity {
	e.oppMu.Lock()
	defer e.oppMu.Unlock()
	if limit <= 0 || limit > len(e.recentOpps) {
		limit = len(e.recentOpps)
	}
	out := make([]RecentOpportunity, 0, limit)
	for i := len(e.recentOpps) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, e.recentOpps[i])
	}
	return out
}

func (e *Engine) countReject(code string) {
	if code == "" {
		return
	}
	e.oppMu.Lock()
	defer e.oppMu.Unlock()
	if e.rejectCounts == nil {
		e.rejectCounts = map[string]int64{}
	}
	if len(e.rejectCounts) < 64 || e.rejectCounts[code] > 0 {
		e.rejectCounts[code]++
	}
}

// RejectCounts snapshots the rejection-reason histogram.
func (e *Engine) RejectCounts() map[string]int64 {
	e.oppMu.Lock()
	defer e.oppMu.Unlock()
	out := make(map[string]int64, len(e.rejectCounts))
	for k, v := range e.rejectCounts {
		out[k] = v
	}
	return out
}

func (e *Engine) rememberOpportunity(op RecentOpportunity) {
	e.oppMu.Lock()
	defer e.oppMu.Unlock()
	e.recentOpps = append(e.recentOpps, op)
	if len(e.recentOpps) > recentOppCap {
		e.recentOpps = e.recentOpps[len(e.recentOpps)-recentOppCap:]
	}
}

// notify is a nil-safe alert emit.
func (e *Engine) notify(sev notification.Severity, key, title, body string) {
	if e.Notifier != nil {
		e.Notifier.Notify(notification.Event{Severity: sev, Key: key, Title: title, Body: body})
	}
}

func NewEngine(cfg config.Bootstrap, log *slog.Logger) *Engine {
	return &Engine{cfg: cfg, log: log}
}

func (e *Engine) Name() string { return "engine" }

// Status is the scanner-facing snapshot for the API layer.
type EngineStatus struct {
	Ready       bool     `json:"ready"`
	Triangles   int      `json:"triangles"`
	Markets     []string `json:"markets"`
	Evaluations int64    `json:"evaluations"`
	Qualified   int64    `json:"qualified"`
	Rejected    int64    `json:"rejected"`
	Skipped     int64    `json:"skipped_unhealthy"`
	Dropped     int64    `json:"dropped_events"`

	Paper *PaperStatus `json:"paper,omitempty"`
}

// PaperStatus reports the paper engine when PAPER mode is active.
type PaperStatus struct {
	Running   bool  `json:"running"`
	Active    int   `json:"active_simulations"`
	Received  int64 `json:"received"`
	Completed int64 `json:"completed"`
	Failed    int64 `json:"failed"`
	Skipped   int64 `json:"skipped"`
}

func (e *Engine) Status() EngineStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()
	st := EngineStatus{Ready: e.ready}
	if e.topo != nil {
		st.Triangles = len(e.topo.Triangles)
		for id := range e.topo.ByMarket {
			st.Markets = append(st.Markets, id.String())
		}
	}
	if e.scn != nil {
		st.Evaluations = e.scn.Stats.Evaluations.Load()
		st.Qualified = e.scn.Stats.Qualified.Load()
		st.Rejected = e.scn.Stats.Rejected.Load()
		st.Skipped = e.scn.Stats.SkippedBooks.Load()
		st.Dropped = e.scn.Stats.DroppedEvts.Load()
	}
	if e.pap != nil {
		ps := e.pap.Snapshot()
		st.Paper = &PaperStatus{
			Running: e.pap.Running(), Active: e.pap.Active(),
			Received: ps.Received, Completed: ps.Completed,
			Failed: ps.Failed, Skipped: ps.Skipped,
		}
	}
	return st
}

// Recorder exposes the in-process recording control (nil before the
// engine has bootstrapped).
func (e *Engine) Recorder() *marketdata.RecorderControl {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.rctl
}

// Paper exposes the paper engine control surface (nil outside PAPER mode).
func (e *Engine) Paper() *paper.Engine {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.pap
}

func (e *Engine) Run(ctx context.Context) error {
	rest := binance.NewRESTClient(binance.MarketDataRESTHost)

	markets, err := e.bootstrapMetadata(ctx, rest)
	if err != nil {
		return err
	}

	symbols := make(map[string]bool, len(e.cfg.Symbols))
	for _, s := range e.cfg.Symbols {
		symbols[s] = true
	}
	var scoped []exchange.Market
	rules := make(map[exchange.MarketID]exchange.InstrumentRules)
	var feedSymbols []exchange.Symbol
	for _, m := range markets {
		if !symbols[string(m.ID.Symbol)] {
			continue
		}
		scoped = append(scoped, m)
		rules[m.ID] = m.Rules
		feedSymbols = append(feedSymbols, m.ID.Symbol)
	}
	if len(scoped) == 0 {
		return fmt.Errorf("engine: none of the configured symbols exist on %s", binance.ID)
	}

	starts := make([]exchange.Asset, 0, len(e.cfg.StartingAssets))
	for _, a := range e.cfg.StartingAssets {
		starts = append(starts, exchange.Asset(a))
	}
	topo := graph.Build(binance.ID, scoped, starts)
	e.log.Info("triangle topology built",
		"markets", len(scoped), "triangles", len(topo.Triangles),
		"rejected_untradeable", topo.Rejected.Untradeable)

	var outbox *storage.Outbox
	sessionID := newULID()
	if e.Store != nil {
		if err := e.Store.UpsertMarkets(ctx, scoped); err != nil {
			e.log.Warn("market metadata sync failed", "error", err)
		}
		outbox = &storage.Outbox{Store: e.Store, Log: e.log, SessionID: sessionID}
	}

	// Recording control: raw frames + splice snapshots for replay. The
	// control is always wired so the console can start/stop sessions
	// in-process; RECORD mode auto-starts one at boot.
	streamTable := make(map[exchange.Symbol]uint16, len(feedSymbols))
	for i, sym := range feedSymbols {
		streamTable[sym] = uint16(i + 1) //nolint:gosec // bounded by symbol count
	}
	rctl := &marketdata.RecorderControl{
		Dir:            e.cfg.RecordingDir,
		Log:            e.log,
		StreamOfSymbol: streamTable,
		NewSessionID:   newULID,
	}
	rctl.Bind(ctx)
	if e.Store != nil {
		streams := (&marketdata.Recorder{StreamOfSymbol: streamTable}).Streams()
		rctl.OnSegment = func(recID string, startedAt time.Time, meta marketdata.SegmentMeta) {
			regCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := e.Store.UpsertRecordingSegment(regCtx, recID, binance.ID, streams, startedAt, meta); err != nil {
				e.log.Warn("recording metadata registration failed", "error", err)
			}
		}
	}
	if e.Hub != nil {
		rctl.OnChange = func(st marketdata.RecorderStatus) {
			_ = e.Hub.Publish("recordings", map[string]any{"kind": "recorder", "recorder": st})
		}
	}
	e.mu.Lock()
	e.rctl = rctl
	e.mu.Unlock()

	// Base-tier taker fees; per-account refresh is a follow-up task
	// (fees are re-pulled at runtime, never hardcoded for real accounts —
	// docs/research/fees.md). 10 bps default per current verified schedule.
	sched, err := fees.NewSchedule(binance.ID, binance.Capabilities.FeeConvention,
		fees.Rate{Maker: decimal.RequireFromString("0.001"), Taker: decimal.RequireFromString("0.001")})
	if err != nil {
		return err
	}

	balance, err := decimal.NewFromString(e.cfg.PaperBalance)
	if err != nil {
		return fmt.Errorf("engine: invalid ARB_PAPER_BALANCE: %w", err)
	}
	initial := make(map[exchange.Asset]decimal.Decimal, len(starts))
	for _, a := range starts {
		initial[a] = balance
	}
	resv := reservation.New(initial, newULID, time.Now)

	books := orderbook.NewSet()
	feed := &binance.Feed{
		WSHost:  binance.MarketDataWSHost,
		REST:    rest,
		Books:   books,
		Symbols: feedSymbols,
		Log:     e.log,
	}
	feed.RawTap = rctl.TapWS
	feed.SnapTap = rctl.TapSnapshot

	breakers := risk.NewRegistry(func(tr risk.Transition) {
		e.log.Warn("circuit breaker transition",
			"breaker", tr.Name, "scope", tr.Scope,
			"from", tr.From.String(), "to", tr.To.String(), "reason", tr.Reason)
		sev := notification.SeverityInfo
		if tr.To.String() == "OPEN" {
			sev = notification.SeverityCritical
		}
		e.notify(sev, "breaker:"+tr.Scope, "Circuit breaker "+tr.To.String(),
			fmt.Sprintf("%s (%s): %s → %s (%s)", tr.Name, tr.Scope, tr.From.String(), tr.To.String(), tr.Reason))
	})

	scn := &scanner.Scanner{
		Topo:     topo,
		Books:    books,
		Rules:    rules,
		Fees:     sched,
		Resolver: defaultRiskLimits(),
		Breakers: breakers,
		Capital:  resv,
		Clock:    time.Now,
		IDGen:    newULID,
		Cfg: scanner.Config{
			ConfigVersion: 1,
			Buffers:       opportunity.Buffers{LatencyBps: decimal.NewFromInt(5), RiskBps: decimal.NewFromInt(5)},
			TTL:           400 * time.Millisecond,
			MinInput:      decimal.NewFromInt(50),
			Depth:         50,
			Workers:       2,
			Search:        pricing.DefaultSizeSearch,
			MaxBookAge:    2 * time.Second,
		},
		Out: make(chan scanner.Event, 256),
	}
	scn.ClockHealthy.Store(true)
	if e.Strategy != nil {
		// Versioned config replaces the fallback literals above: the
		// subscription delivers the active snapshot immediately and every
		// later Apply/Rollback hot-swaps the running scanner.
		e.Strategy.Subscribe(func(snap strategy.Snapshot) {
			scn.SetStrategy(scanner.Strategy{
				Cfg:      snap.Params.ScannerConfig(snap.Version),
				Resolver: snap.Params.RiskResolver(),
			})
		})
		if snap := e.Strategy.Current(); snap.Version != 0 {
			// Workers is start-time-only, so copy it into the base config
			// the Run loop reads.
			scn.Cfg.Workers = snap.Params.Scanner.Workers
		}
	}

	// PAPER mode: assemble the full simulation loop behind the scanner.
	var paperEng *paper.Engine
	var paperIn chan scanner.Event
	port := portfolio.New(resv, initial)
	if e.cfg.Mode == config.ModePaper {
		if e.Store != nil {
			balances := map[string]string{}
			for a, v := range initial {
				balances[string(a)] = v.String()
			}
			if err := e.Store.EnsurePaperSession(ctx, sessionID, string(e.cfg.Mode), balances, 1, e.cfg.Seed); err != nil {
				e.log.Warn("paper session registration failed", "error", err)
			}
		}
		marker := portfolio.BookMarker{Books: books, Markets: scoped}
		executor := simulation.NewPaper(
			books, rulesLookup(rules), sched,
			simulation.WallClock{}, simulation.RealWaiter{}, marker,
			simulation.Config{
				Latency: simulation.LatencyModel{
					SubmitBase: 20 * time.Millisecond, SubmitJitter: 30 * time.Millisecond,
					FillBase: 30 * time.Millisecond, FillJitter: 50 * time.Millisecond,
				},
				LimitToleranceBps: decimal.NewFromInt(20),
				Depth:             50,
				Seed:              e.cfg.Seed,
			},
			newULID,
		)
		byID := make(map[string]graph.Triangle, len(topo.Triangles))
		for _, tri := range topo.Triangles {
			byID[tri.ID] = tri
		}
		paperIn = make(chan scanner.Event, 128)
		paperEng = &paper.Engine{
			Executor:  executor,
			Resv:      resv,
			Portfolio: port,
			Triangles: byID,
			In:        paperIn,
			Clock:     time.Now,
			IDGen:     newULID,
			OnResult: func(res execution.CycleResult) {
				e.log.Info("paper cycle settled",
					"cycle_id", res.CycleID, "outcome", string(res.Outcome),
					"pnl", res.TotalPnL.String(), "consumed", res.InputConsumed.String())
				if e.Metrics != nil && res.Outcome == execution.OutcomeAllFilled {
					e.Metrics.ObserveSlippage(string(binance.ID), res.SlippageBps.InexactFloat64())
				}
				if res.Outcome != execution.OutcomeAllFilled {
					e.notify(notification.SeverityWarning, "paper:cycle_failed",
						"Paper cycle "+string(res.Outcome),
						fmt.Sprintf("cycle %s: %s (pnl %s)", res.CycleID, res.Reason, res.TotalPnL.String()))
				}
				if e.Strategy != nil {
					limit := e.Strategy.Current().Params.Risk.MaxDrawdown
					dd := port.CurrentDrawdown(res.StartAsset)
					if limit.IsPositive() && dd.GreaterThan(limit.Mul(decimal.RequireFromString("0.8"))) {
						e.notify(notification.SeverityWarning, "portfolio:drawdown",
							"Drawdown threshold approached",
							fmt.Sprintf("%s drawdown %s (limit %s)", res.StartAsset, dd.StringFixed(4), limit.String()))
					}
				}
				if outbox != nil {
					r := res
					outbox.Enqueue(storage.Record{Kind: "cycle", Cycle: &r, SessionID: sessionID})
				}
				if e.Hub != nil {
					_ = e.Hub.Publish("cycles", map[string]any{
						"cycle_id": res.CycleID, "outcome": string(res.Outcome),
						"realized_pnl": res.RealizedPnL.String(),
						"total_pnl":    res.TotalPnL.String(),
						"settled_at":   res.SettledAt,
					})
				}
			},
		}
		paperEng.Resume()
		scn.Sims = paperEng.Active
	}

	e.mu.Lock()
	e.scn, e.topo, e.pap, e.port, e.ready = scn, topo, paperEng, port, true
	e.feed, e.resv, e.brk = feed, resv, breakers
	e.marker = portfolio.BookMarker{Books: books, Markets: scoped}
	e.mu.Unlock()
	e.notify(notification.SeverityInfo, "engine:ready", "Engine ready",
		fmt.Sprintf("%d triangles over %d markets in %s mode", len(topo.Triangles), len(scoped), e.cfg.Mode))
	defer e.notify(notification.SeverityInfo, "engine:stopped", "Engine stopped", "shutdown or fatal component exit")

	if e.Metrics != nil {
		e.wireMetrics(scn, topo, feed, books, resv, breakers, paperEng, port, rctl, starts)
	}

	if e.Hub != nil {
		e.Hub.RegisterTopic("scanner", func() (json.RawMessage, error) {
			return json.Marshal(e.Status())
		})
		e.Hub.RegisterTopic("health", func() (json.RawMessage, error) {
			return json.Marshal(map[string]any{"engine": e.Status(), "mode": string(e.cfg.Mode)})
		})
		e.Hub.RegisterTopic("recordings", func() (json.RawMessage, error) {
			return json.Marshal(map[string]any{"recorder": rctl.Status()})
		})
	}
	if e.cfg.Mode == config.ModeRecord {
		if _, err := rctl.Start(ctx); err != nil {
			return fmt.Errorf("engine: start recording: %w", err)
		}
	}

	errCh := make(chan error, 6)
	go func() { errCh <- feed.Run(ctx) }()
	go func() { errCh <- scn.Run(ctx) }()
	go func() { errCh <- e.consumeEvents(ctx, scn, paperIn, outbox) }()
	if paperEng != nil {
		go func() { errCh <- paperEng.Run(ctx) }()
	}
	if outbox != nil {
		go func() { errCh <- outbox.Run(ctx) }()
	}

	// Staleness sweep: books that stop ticking degrade to STALE.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			if err != nil && ctx.Err() == nil {
				return err
			}
		case now := <-ticker.C:
			// Live config, not the boot literal: a hot-swapped
			// max_book_age_ms applies to the sweep too (audit CR-P2-7).
			maxAge := scn.CurrentConfig().MaxBookAge
			for _, id := range books.All() {
				if b, ok := books.Get(id); ok {
					b.EvaluateStaleness(now, maxAge)
				}
			}
		}
	}
}

// consumeEvents fans scanner events out: qualified opportunities go to
// the paper engine (PAPER mode) and the hub; persistence attaches here
// when the storage layer (T-022) lands.
func (e *Engine) consumeEvents(ctx context.Context, scn *scanner.Scanner, paperIn chan<- scanner.Event, outbox *storage.Outbox) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-scn.Out:
			if outbox != nil && ev.Opportunity.Status == opportunity.StatusQualified {
				op, dec := ev.Opportunity, ev.Decision
				outbox.Enqueue(storage.Record{Kind: "opportunity", Opportunity: &op, Decision: &dec})
			}
			if ev.Opportunity.Status == opportunity.StatusRejected {
				e.countReject(ev.Decision.ReasonCode)
			}
			if ev.Opportunity.Status == opportunity.StatusQualified {
				if e.Metrics != nil {
					e.Metrics.ObserveQualifiedEdge(string(ev.Opportunity.Exchange),
						ev.Opportunity.NetReturnBps.InexactFloat64())
				}
				e.rememberOpportunity(RecentOpportunity{
					ID: ev.Opportunity.ID, TriangleID: ev.Opportunity.TriangleID,
					NetBps: ev.Opportunity.NetReturnBps.StringFixed(2),
					Profit: ev.Opportunity.NetProfit.String(),
					Input:  ev.Opportunity.Quote.InputConsumed.String(),
					At:     ev.Opportunity.DetectedAt,
				})
				if ev.Opportunity.NetReturnBps.GreaterThanOrEqual(decimal.NewFromInt(50)) {
					e.notify(notification.SeverityInfo, "opportunity:large",
						"Large qualified opportunity",
						fmt.Sprintf("%s: %s bps, profit %s", ev.Opportunity.TriangleID,
							ev.Opportunity.NetReturnBps.StringFixed(2), ev.Opportunity.NetProfit.String()))
				}
				if paperIn != nil {
					select {
					case paperIn <- ev:
					default:
						e.log.Warn("paper queue full; opportunity dropped",
							"opportunity_id", ev.Opportunity.ID)
					}
				}
				e.log.Info("opportunity qualified",
					"opportunity_id", ev.Opportunity.ID,
					"triangle_id", ev.Opportunity.TriangleID,
					"input", ev.Opportunity.Quote.InputConsumed.String(),
					"net_bps", ev.Opportunity.NetReturnBps.StringFixed(2),
					"net_profit", ev.Opportunity.NetProfit.String(),
				)
				if e.Hub != nil {
					_ = e.Hub.Publish("scanner", map[string]any{
						"opportunity_id": ev.Opportunity.ID,
						"triangle_id":    ev.Opportunity.TriangleID,
						"status":         string(ev.Opportunity.Status),
						"input":          ev.Opportunity.Quote.InputConsumed.String(),
						"net_bps":        ev.Opportunity.NetReturnBps.StringFixed(4),
						"net_profit":     ev.Opportunity.NetProfit.String(),
						"detected_at":    ev.Opportunity.DetectedAt,
					})
				}
			} else {
				e.log.Debug("opportunity rejected",
					"triangle_id", ev.Opportunity.TriangleID,
					"reason", ev.Decision.ReasonCode,
					"net_bps", ev.Opportunity.NetReturnBps.StringFixed(2),
				)
			}
		}
	}
}

func (e *Engine) bootstrapMetadata(ctx context.Context, rest *binance.RESTClient) ([]exchange.Market, error) {
	backoff := 2 * time.Second
	for {
		markets, err := rest.ExchangeInfo(ctx)
		if err == nil {
			e.log.Info("exchange metadata loaded", "markets", len(markets))
			return markets, nil
		}
		e.log.Warn("metadata bootstrap failed; retrying", "error", err, "backoff", backoff.String())
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

// wireMetrics attaches the engine's instrument sources: sync observers
// on the scanner/feed and pull callbacks over the existing atomics.
func (e *Engine) wireMetrics(
	scn *scanner.Scanner, topo *graph.Topology, feed *binance.Feed,
	books *orderbook.Set, resv *reservation.Manager, breakers *risk.Registry,
	paperEng *paper.Engine, port *portfolio.Portfolio,
	rctl *marketdata.RecorderControl, starts []exchange.Asset,
) {
	m := e.Metrics
	scn.EvalObserver = func(d time.Duration) {
		m.ObserveEval(float64(d.Nanoseconds()) / 1e6)
	}
	recordLatency := m.MessageLatencyRecorder(string(binance.ID))
	feed.LatencyObserver = func(d time.Duration) {
		recordLatency(float64(d.Nanoseconds()) / 1e6)
	}
	src := metrics.EngineSources{
		Scanner: func() metrics.ScannerStats {
			return metrics.ScannerStats{
				Evaluations:   scn.Stats.Evaluations.Load(),
				Qualified:     scn.Stats.Qualified.Load(),
				Rejected:      scn.Stats.Rejected.Load(),
				SkippedBooks:  scn.Stats.SkippedBooks.Load(),
				DroppedEvents: scn.Stats.DroppedEvts.Load(),
			}
		},
		Triangles: func() int64 { return int64(len(topo.Triangles)) },
		Feed: func() metrics.FeedStats {
			return metrics.FeedStats{
				Exchange:   string(binance.ID),
				Frames:     feed.Stats.Frames.Load(),
				Reconnects: feed.Stats.Reconnects.Load(),
				APIErrors:  feed.Stats.APIErrors.Load(),
				Resyncs:    feed.Stats.Resyncs.Load(),
				SeqErrors:  feed.Stats.SeqGaps.Load(),
			}
		},
		Books: func() []metrics.BookStat {
			now := time.Now()
			ids := books.All()
			out := make([]metrics.BookStat, 0, len(ids))
			for _, id := range ids {
				view, ok := books.View(id, 1)
				if !ok {
					continue
				}
				out = append(out, metrics.BookStat{
					Exchange: string(id.Exchange), Market: string(id.Symbol),
					AgeMS: float64(view.Age(now).Nanoseconds()) / 1e6,
					State: view.State.String(),
				})
			}
			return out
		},
		Capital: func() []metrics.CapitalStat {
			out := make([]metrics.CapitalStat, 0, len(starts))
			for _, a := range starts {
				avail, reserved := resv.Balance(a)
				out = append(out, metrics.CapitalStat{
					Asset:     string(a),
					Available: avail.InexactFloat64(), Reserved: reserved.InexactFloat64(),
				})
			}
			return out
		},
		Breakers: func() []metrics.BreakerStat {
			states := breakers.States()
			out := make([]metrics.BreakerStat, 0, len(states))
			for _, tr := range states {
				var v int64
				switch tr.To.String() {
				case "HALF_OPEN":
					v = 1
				case "OPEN":
					v = 2
				}
				out = append(out, metrics.BreakerStat{Name: tr.Name, Scope: tr.Scope, State: v})
			}
			return out
		},
	}
	if paperEng != nil {
		src.Paper = func() *metrics.PaperStats {
			st := paperEng.Snapshot()
			return &metrics.PaperStats{
				Received: st.Received, Started: st.Started,
				Completed: st.Completed, Failed: st.Failed, Skipped: st.Skipped,
				Active: int64(paperEng.Active()),
			}
		}
		src.PnL = func() []metrics.AssetPnL {
			out := make([]metrics.AssetPnL, 0, len(starts))
			for _, a := range starts {
				out = append(out, metrics.AssetPnL{
					Asset:    string(a),
					Realized: port.Realized(a).InexactFloat64(),
					Fees:     port.FeesPaid(a).InexactFloat64(),
				})
			}
			return out
		}
	}
	if rctl != nil {
		src.Recorder = func() (int64, int64) {
			st := rctl.Status()
			return st.Written, st.Dropped
		}
	}
	if err := m.RegisterEngine(src); err != nil {
		e.log.Error("metrics registration failed", "error", err)
	}
}

// defaultRiskLimits are the conservative fallback limits used only when
// no strategy service is attached (direct-constructed engines in tests);
// normal wiring hot-swaps versioned values over these at startup.
func defaultRiskLimits() risk.Resolver {
	return risk.Resolver{Global: risk.Limits{
		MinNetEdgeBps:            decimal.NewFromInt(5),
		MinExpectedProfit:        decimal.NewFromInt(1),
		MaxTradeSize:             decimal.NewFromInt(1000),
		MaxCapitalPerTriangle:    decimal.NewFromInt(2000),
		MaxCapitalUtilization:    decimal.RequireFromString("0.5"),
		MaxConcurrentSimulations: 3,
		MaxBookAge:               1500 * time.Millisecond,
		MaxBookAgeSpread:         750 * time.Millisecond,
		MaxPriceImpactBps:        decimal.NewFromInt(30),
		MaxDailyLoss:             decimal.NewFromInt(200),
		MaxDrawdown:              decimal.RequireFromString("0.05"),
		MinDataQuality:           decimal.RequireFromString("0.5"),
	}}
}

func newULID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// rulesLookup adapts the instrument-rules map to simulation.RulesSource.
type rulesLookup map[exchange.MarketID]exchange.InstrumentRules

func (r rulesLookup) Rules(id exchange.MarketID) (exchange.InstrumentRules, bool) {
	v, ok := r[id]
	return v, ok
}
