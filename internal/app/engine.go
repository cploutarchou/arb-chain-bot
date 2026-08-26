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
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
	"github.com/cploutarchou/arb-chain-bot/internal/realtime"
	"github.com/cploutarchou/arb-chain-bot/internal/reservation"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
	"github.com/cploutarchou/arb-chain-bot/internal/scanner"
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

	mu    sync.RWMutex
	scn   *scanner.Scanner
	topo  *graph.Topology
	ready bool
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
	return st
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

	breakers := risk.NewRegistry(func(tr risk.Transition) {
		e.log.Warn("circuit breaker transition",
			"breaker", tr.Name, "scope", tr.Scope,
			"from", tr.From.String(), "to", tr.To.String(), "reason", tr.Reason)
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

	e.mu.Lock()
	e.scn, e.topo, e.ready = scn, topo, true
	e.mu.Unlock()

	if e.Hub != nil {
		e.Hub.RegisterTopic("scanner", func() (json.RawMessage, error) {
			return json.Marshal(e.Status())
		})
		e.Hub.RegisterTopic("health", func() (json.RawMessage, error) {
			return json.Marshal(map[string]any{"engine": e.Status(), "mode": string(e.cfg.Mode)})
		})
	}

	errCh := make(chan error, 3)
	go func() { errCh <- feed.Run(ctx) }()
	go func() { errCh <- scn.Run(ctx) }()
	go func() { errCh <- e.consumeEvents(ctx, scn) }()

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
			for _, id := range books.All() {
				if b, ok := books.Get(id); ok {
					b.EvaluateStaleness(now, scn.Cfg.MaxBookAge)
				}
			}
		}
	}
}

// consumeEvents is the interim event sink: qualified opportunities are
// logged (paper engine + persistence + realtime hub attach here as
// T-018-wiring/T-022/T-024 land in the app layer).
func (e *Engine) consumeEvents(ctx context.Context, scn *scanner.Scanner) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-scn.Out:
			if ev.Opportunity.Status == opportunity.StatusQualified {
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

// defaultRiskLimits are the conservative bootstrap limits; the versioned
// config service (T-034) replaces them with DB-backed values.
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
