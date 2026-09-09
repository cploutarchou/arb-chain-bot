// Package metrics implements the observability build-out (SKILL.md §65,
// docs/architecture.md §14): an OpenTelemetry meter provider with a
// Prometheus exporter. Hot paths never call the SDK — they bump plain
// atomics, and asynchronous instruments read those only at scrape time.
// The few synchronous histograms (evaluation duration, API latency,
// message latency) sit on paths where a ~µs record is noise, and every
// injection point is nil-guarded so the platform runs identically with
// metrics disabled.
//
// Monetary gauges (capital, PnL) convert decimal → float64 at the
// exposition boundary only: Prometheus's wire format is float. The
// canonical values remain decimals in the engine, API, and database;
// nothing computes on these floats.
package metrics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	api "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// Metrics owns the provider and the synchronous instruments.
type Metrics struct {
	provider *sdkmetric.MeterProvider
	meter    api.Meter

	// Handler serves the Prometheus exposition (mounted at /metrics).
	Handler http.Handler

	evalDuration api.Float64Histogram // triangle_evaluation_duration (ms)
	apiDuration  api.Float64Histogram // api_request_duration (s)
	msgLatency   api.Float64Histogram // market_message_latency_ms
	netEdge      api.Float64Histogram // net_edge_bps (qualified)
	slippage     api.Float64Histogram // slippage_bps (settled paper cycles)

	// Latency chain and labelled outcomes (audit O3/O5): recorded once
	// per settled cycle, per order and per rejection — off the per-frame
	// hot path, so synchronous instruments are affordable here.
	orderLatency  api.Float64Histogram // order_latency_ms{exchange,stage}
	cycleDuration api.Float64Histogram // cycle_duration_ms{exchange}
	rejections    api.Int64Counter     // risk_rejections{exchange,stage,reason}
	cycleOutcomes api.Int64Counter     // paper_cycle_outcomes{exchange,outcome}
	orderOutcomes api.Int64Counter     // order_outcomes{exchange,status}

	attrSets sync.Map // string key → api.MeasurementOption (precomputed label sets)
}

// New builds the provider + exporter. Call Shutdown on process exit.
func New() (*Metrics, error) {
	reg := prometheus.NewRegistry()
	// Instruments carry no OTel units (durations/latencies are explicit
	// in the metric names per SKILL §65), so the default translation
	// appends only the _total counter suffix — names stay verbatim.
	exp, err := otelprom.New(
		otelprom.WithRegisterer(reg),
		otelprom.WithoutScopeInfo(),  // single-binary: scope adds noise
		otelprom.WithoutTargetInfo(), // deployment labels come from scrape config
	)
	if err != nil {
		return nil, fmt.Errorf("metrics: exporter: %w", err)
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exp))
	meter := provider.Meter("arbd")

	m := &Metrics{
		provider: provider,
		meter:    meter,
		Handler:  promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
	}
	if m.evalDuration, err = meter.Float64Histogram("triangle_evaluation_duration",
		api.WithDescription("full triangle evaluation pipeline duration in ms"),
		api.WithExplicitBucketBoundaries(0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 25, 50, 100)); err != nil {
		return nil, err
	}
	if m.apiDuration, err = meter.Float64Histogram("api_request_duration",
		api.WithDescription("HTTP API request duration in seconds"),
		api.WithExplicitBucketBoundaries(0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5)); err != nil {
		return nil, err
	}
	if m.msgLatency, err = meter.Float64Histogram("market_message_latency_ms",
		api.WithDescription("exchange event time to local receive in ms"),
		api.WithExplicitBucketBoundaries(1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500)); err != nil {
		return nil, err
	}
	if m.netEdge, err = meter.Float64Histogram("net_edge_bps",
		api.WithDescription("net edge (bps) of qualified opportunities"),
		api.WithExplicitBucketBoundaries(1, 2, 5, 10, 20, 50, 100, 200, 500)); err != nil {
		return nil, err
	}
	if m.slippage, err = meter.Float64Histogram("slippage_bps",
		api.WithDescription("realized-vs-plan slippage (bps of input) for settled paper cycles"),
		api.WithExplicitBucketBoundaries(-50, -20, -10, -5, -2, -1, 0, 1, 2, 5, 10, 20, 50)); err != nil {
		return nil, err
	}
	if m.orderLatency, err = meter.Float64Histogram("order_latency_ms",
		api.WithDescription("simulated order latency in ms by stage: submit_ack, ack_fill, submit_fill"),
		api.WithExplicitBucketBoundaries(1, 2, 5, 10, 20, 35, 50, 75, 100, 150, 250, 500, 1000)); err != nil {
		return nil, err
	}
	if m.cycleDuration, err = meter.Float64Histogram("cycle_duration_ms",
		api.WithDescription("paper cycle wall time from first submission to settlement in ms"),
		api.WithExplicitBucketBoundaries(10, 25, 50, 100, 150, 250, 400, 600, 1000, 2500, 5000)); err != nil {
		return nil, err
	}
	if m.rejections, err = meter.Int64Counter("risk_rejections",
		api.WithDescription("opportunities refused by the risk gate, by stage (qualification, revalidation) and reason code")); err != nil {
		return nil, err
	}
	if m.cycleOutcomes, err = meter.Int64Counter("paper_cycle_outcomes",
		api.WithDescription("settled paper cycles by outcome")); err != nil {
		return nil, err
	}
	if m.orderOutcomes, err = meter.Int64Counter("order_outcomes",
		api.WithDescription("simulated orders by final status")); err != nil {
		return nil, err
	}
	return m, nil
}

// attrs returns a cached measurement option for a small, bounded label
// set (reason codes, outcomes, stages): the rejection path can run
// thousands of times a second and must not allocate a label set each time.
func (m *Metrics) attrs(key string, kvs ...attribute.KeyValue) api.MeasurementOption {
	if opt, ok := m.attrSets.Load(key); ok {
		return opt.(api.MeasurementOption)
	}
	opt := api.WithAttributeSet(attribute.NewSet(kvs...))
	m.attrSets.Store(key, opt)
	return opt
}

// ObserveOrderLatency records one simulated order's latency for a stage
// (submit_ack: created→acked; ack_fill: acked→filled; submit_fill: end
// to end). These calibrate the latency buffer, so the histograms are the
// numbers to compare the buffer against.
func (m *Metrics) ObserveOrderLatency(exchangeID, stage string, ms float64) {
	m.orderLatency.Record(context.Background(), ms, m.attrs("ol|"+exchangeID+"|"+stage,
		attribute.String("exchange", exchangeID), attribute.String("stage", stage)))
}

// ObserveCycleDuration records one settled cycle's wall time.
func (m *Metrics) ObserveCycleDuration(exchangeID string, ms float64) {
	m.cycleDuration.Record(context.Background(), ms, m.attrs("cd|"+exchangeID,
		attribute.String("exchange", exchangeID)))
}

// CountRejection counts one refused opportunity by stage and reason code.
func (m *Metrics) CountRejection(exchangeID, stage, reason string) {
	m.rejections.Add(context.Background(), 1, m.attrs("rj|"+exchangeID+"|"+stage+"|"+reason,
		attribute.String("exchange", exchangeID), attribute.String("stage", stage),
		attribute.String("reason", reason)))
}

// CountCycleOutcome counts one settled cycle by outcome.
func (m *Metrics) CountCycleOutcome(exchangeID, outcome string) {
	m.cycleOutcomes.Add(context.Background(), 1, m.attrs("co|"+exchangeID+"|"+outcome,
		attribute.String("exchange", exchangeID), attribute.String("outcome", outcome)))
}

// CountOrderStatus counts one simulated order by its final status.
func (m *Metrics) CountOrderStatus(exchangeID, status string) {
	m.orderOutcomes.Add(context.Background(), 1, m.attrs("os|"+exchangeID+"|"+status,
		attribute.String("exchange", exchangeID), attribute.String("status", status)))
}

// Shutdown flushes the provider.
func (m *Metrics) Shutdown(ctx context.Context) error { return m.provider.Shutdown(ctx) }

// ObserveEval records one triangle evaluation duration (injected into
// the scanner as a plain func; nil there disables it).
func (m *Metrics) ObserveEval(ms float64) {
	m.evalDuration.Record(context.Background(), ms)
}

// ObserveAPIRequest records one HTTP request.
func (m *Metrics) ObserveAPIRequest(method, route string, status int, seconds float64) {
	m.apiDuration.Record(context.Background(), seconds, api.WithAttributes(
		attribute.String("method", method),
		attribute.String("route", route),
		attribute.Int("status", status),
	))
}

// ObserveMessageLatency records exchange→local latency for one frame.
func (m *Metrics) ObserveMessageLatency(exchangeID string, ms float64) {
	m.msgLatency.Record(context.Background(), ms, api.WithAttributes(
		attribute.String("exchange", exchangeID)))
}

// MessageLatencyRecorder returns a per-exchange recording func with the
// attribute set precomputed — the per-frame feed path avoids the
// attribute-construction allocations of ObserveMessageLatency.
func (m *Metrics) MessageLatencyRecorder(exchangeID string) func(ms float64) {
	opt := api.WithAttributeSet(attribute.NewSet(attribute.String("exchange", exchangeID)))
	return func(ms float64) { m.msgLatency.Record(context.Background(), ms, opt) }
}

// ObserveQualifiedEdge records the net edge of a qualified opportunity.
func (m *Metrics) ObserveQualifiedEdge(exchangeID string, bps float64) {
	m.netEdge.Record(context.Background(), bps, api.WithAttributes(
		attribute.String("exchange", exchangeID)))
}

// ObserveSlippage records one settled cycle's realized slippage.
func (m *Metrics) ObserveSlippage(exchangeID string, bps float64) {
	m.slippage.Record(context.Background(), bps, api.WithAttributes(
		attribute.String("exchange", exchangeID)))
}

// ---- asynchronous (pull) instruments ------------------------------------

// ScannerStats is one cumulative counter snapshot.
type ScannerStats struct {
	Evaluations, Qualified, Rejected, SkippedBooks, DroppedEvents int64
	Revalidations                                                 int64
	RevalidationRejects                                           int64
}

// FeedStats are cumulative per-exchange transport counters.
type FeedStats struct {
	Exchange   string
	Frames     int64
	Reconnects int64
	APIErrors  int64
	Resyncs    int64
	SeqErrors  int64
}

// BookStat is one market's book freshness at scrape time.
type BookStat struct {
	Exchange string
	Market   string
	AgeMS    float64
	State    string
}

// CapitalStat is one asset's balances (floats for exposition only).
type CapitalStat struct {
	Asset               string
	Available, Reserved float64
}

// BreakerStat maps a breaker to 0=CLOSED, 1=HALF_OPEN, 2=OPEN.
type BreakerStat struct {
	Name, Scope string
	State       int64
}

// PaperStats are cumulative paper-engine counters plus gauges.
type PaperStats struct {
	Received, Started, Completed, Failed, Skipped int64
	Active                                        int64
}

// QueueStats are one bounded queue's backlog and loss counters (P0-3).
// Dropped counts enqueue attempts refused (queue full, or closed at
// shutdown); WriteFailures counts dequeued records the sink refused
// (outbox only); Written counts records the sink accepted.
type QueueStats struct {
	Depth, Capacity int64
	Dropped         int64
	WriteFailures   int64
	Written         int64
}

// AssetPnL is one start asset's session economics (floats for
// exposition only).
type AssetPnL struct {
	Asset          string
	Realized, Fees float64
}

// EngineSources are pull callbacks read at every scrape; nil members are
// skipped so a profile registers only what it actually runs.
type EngineSources struct {
	Scanner   func() ScannerStats
	Triangles func() int64
	Feed      func() FeedStats
	Books     func() []BookStat
	Capital   func() []CapitalStat
	Breakers  func() []BreakerStat
	Paper     func() *PaperStats
	PnL       func() []AssetPnL
	Recorder  func() (written, dropped int64)
	// Outbox and PaperQueue expose the persistence and paper inbound
	// queues; nil while the profile runs without them.
	Outbox     func() *QueueStats
	PaperQueue func() *QueueStats
}

// RegisterEngine wires the engine's atomic counters into observable
// instruments. Call once, after the engine's components exist.
func (m *Metrics) RegisterEngine(src EngineSources) error {
	meter := m.meter
	// Instrument-creation failures are collected and joined rather than
	// discarded (audit P3): a silently-nil instrument would be observed
	// into the void every scrape.
	var errs []error
	i64c := func(name, desc string) api.Int64ObservableCounter {
		c, err := meter.Int64ObservableCounter(name, api.WithDescription(desc))
		if err != nil {
			errs = append(errs, err)
		}
		return c
	}
	i64g := func(name, desc string) api.Int64ObservableGauge {
		g, err := meter.Int64ObservableGauge(name, api.WithDescription(desc))
		if err != nil {
			errs = append(errs, err)
		}
		return g
	}
	f64g := func(name, desc string) api.Float64ObservableGauge {
		g, err := meter.Float64ObservableGauge(name, api.WithDescription(desc))
		if err != nil {
			errs = append(errs, err)
		}
		return g
	}
	f64c := func(name, desc string) api.Float64ObservableCounter {
		c, err := meter.Float64ObservableCounter(name, api.WithDescription(desc))
		if err != nil {
			errs = append(errs, err)
		}
		return c
	}
	var (
		evals      = i64c("triangles_evaluated", "triangle evaluations")
		detected   = i64c("opportunities_detected", "opportunities evaluated to a decision")
		qualified  = i64c("opportunities_qualified", "opportunities passing the risk engine")
		rejected   = i64c("opportunities_rejected", "opportunities rejected by the risk engine")
		skipped    = i64c("scanner_skipped_unhealthy", "evaluations skipped on missing/unhealthy books")
		dropped    = i64c("scanner_dropped_events", "scanner events dropped by slow consumers")
		triangles  = i64g("triangles_total", "triangles in the active topology")
		frames     = i64c("market_messages", "market data frames received")
		reconnects = i64c("exchange_reconnects", "feed session reconnects")
		apiErrors  = i64c("exchange_api_errors", "exchange REST errors")
		resyncs    = i64c("orderbook_resync", "order book resyncs")
		seqErrors  = i64c("orderbook_sequence_errors", "sequence gaps detected")
		bookAge    = f64g("orderbook_age_ms", "book age at scrape")
		bookState  = i64g("orderbook_state", "0 SYNCING,1 HEALTHY,2 STALE,3 CORRUPTED,4 DISCONNECTED")
		capAvail   = f64g("capital_available", "available capital (display-only float)")
		capResv    = f64g("capital_reserved", "reserved capital (display-only float)")
		breaker    = i64g("circuit_breaker_state", "0 CLOSED,1 HALF_OPEN,2 OPEN")
		papRecv    = i64c("paper_cycles", "paper cycles started")
		papOK      = i64c("paper_cycles_success", "paper cycles completed")
		papFail    = i64c("paper_cycles_failed", "paper cycles failed")
		papActive  = i64g("paper_active_simulations", "in-flight simulations")
		papPnL     = f64g("paper_pnl", "realized session PnL per start asset (display-only float)")
		feesTotal  = f64c("fees", "cumulative simulated fees per start asset (display-only float)")
		recWritten = i64c("recorder_frames_written", "recorded frames written")
		recDropped = i64c("recorder_frames_dropped", "recorded frames dropped on overflow")
		papRecvd   = i64c("paper_cycles_received", "qualified opportunities received by the paper engine")
		papSkip    = i64c("paper_cycles_skipped", "paper opportunities skipped (paused, expired, reservation conflict, capital)")
		obDepth    = i64g("outbox_queue_depth", "persistence outbox records queued")
		obCap      = i64g("outbox_queue_capacity", "persistence outbox capacity")
		obDropped  = i64c("outbox_records_dropped", "persistence records refused at the queue (full or closed) or lost at the drain deadline")
		obFailed   = i64c("outbox_write_failures", "persistence records the database refused")
		obWritten  = i64c("outbox_records_written", "persistence records the database accepted")
		pqDepth    = i64g("paper_queue_depth", "paper engine inbound events queued")
		pqCap      = i64g("paper_queue_capacity", "paper engine inbound queue capacity")
		pqDropped  = i64c("paper_queue_dropped", "qualified opportunities refused by a full paper queue")
		revals     = i64c("opportunities_revalidated", "qualified opportunities re-checked before execution")
		revalRej   = i64c("opportunities_revalidation_rejected", "qualified opportunities refused at revalidation")
	)
	if err := errors.Join(errs...); err != nil {
		return err
	}
	insts := []api.Observable{
		evals, detected, qualified, rejected, skipped, dropped, triangles,
		frames, reconnects, apiErrors, resyncs, seqErrors, bookAge, bookState,
		capAvail, capResv, breaker, papRecv, papOK, papFail, papActive, papPnL,
		feesTotal, recWritten, recDropped, papRecvd, papSkip,
		obDepth, obCap, obDropped, obFailed, obWritten, pqDepth, pqCap, pqDropped,
		revals, revalRej,
	}
	_, err := meter.RegisterCallback(func(_ context.Context, o api.Observer) error {
		if src.Scanner != nil {
			s := src.Scanner()
			o.ObserveInt64(evals, s.Evaluations)
			o.ObserveInt64(detected, s.Qualified+s.Rejected)
			o.ObserveInt64(qualified, s.Qualified)
			o.ObserveInt64(rejected, s.Rejected)
			o.ObserveInt64(skipped, s.SkippedBooks)
			o.ObserveInt64(dropped, s.DroppedEvents)
			o.ObserveInt64(revals, s.Revalidations)
			o.ObserveInt64(revalRej, s.RevalidationRejects)
		}
		if src.Triangles != nil {
			o.ObserveInt64(triangles, src.Triangles())
		}
		if src.Feed != nil {
			f := src.Feed()
			ex := api.WithAttributes(attribute.String("exchange", f.Exchange))
			o.ObserveInt64(frames, f.Frames, ex)
			o.ObserveInt64(reconnects, f.Reconnects, ex)
			o.ObserveInt64(apiErrors, f.APIErrors, ex)
			o.ObserveInt64(resyncs, f.Resyncs, ex)
			o.ObserveInt64(seqErrors, f.SeqErrors, ex)
		}
		if src.Books != nil {
			for _, b := range src.Books() {
				attrs := api.WithAttributes(
					attribute.String("exchange", b.Exchange),
					attribute.String("market", b.Market))
				o.ObserveFloat64(bookAge, b.AgeMS, attrs)
				o.ObserveInt64(bookState, bookStateValue(b.State), attrs)
			}
		}
		if src.Capital != nil {
			for _, c := range src.Capital() {
				attrs := api.WithAttributes(attribute.String("asset", c.Asset))
				o.ObserveFloat64(capAvail, c.Available, attrs)
				o.ObserveFloat64(capResv, c.Reserved, attrs)
			}
		}
		if src.Breakers != nil {
			for _, b := range src.Breakers() {
				o.ObserveInt64(breaker, b.State, api.WithAttributes(
					attribute.String("name", b.Name),
					attribute.String("scope", b.Scope)))
			}
		}
		if src.Paper != nil {
			if p := src.Paper(); p != nil {
				o.ObserveInt64(papRecv, p.Started)
				o.ObserveInt64(papOK, p.Completed)
				o.ObserveInt64(papFail, p.Failed)
				o.ObserveInt64(papActive, p.Active)
				o.ObserveInt64(papRecvd, p.Received)
				o.ObserveInt64(papSkip, p.Skipped)
			}
		}
		if src.Outbox != nil {
			if q := src.Outbox(); q != nil {
				o.ObserveInt64(obDepth, q.Depth)
				o.ObserveInt64(obCap, q.Capacity)
				o.ObserveInt64(obDropped, q.Dropped)
				o.ObserveInt64(obFailed, q.WriteFailures)
				o.ObserveInt64(obWritten, q.Written)
			}
		}
		if src.PaperQueue != nil {
			if q := src.PaperQueue(); q != nil {
				o.ObserveInt64(pqDepth, q.Depth)
				o.ObserveInt64(pqCap, q.Capacity)
				o.ObserveInt64(pqDropped, q.Dropped)
			}
		}
		if src.PnL != nil {
			for _, a := range src.PnL() {
				attrs := api.WithAttributes(attribute.String("asset", a.Asset))
				o.ObserveFloat64(papPnL, a.Realized, attrs)
				o.ObserveFloat64(feesTotal, a.Fees, attrs)
			}
		}
		if src.Recorder != nil {
			w, d := src.Recorder()
			o.ObserveInt64(recWritten, w)
			o.ObserveInt64(recDropped, d)
		}
		return nil
	}, insts...)
	return err
}

// RegisterAI exposes ai_requests_total / ai_failures_total.
func (m *Metrics) RegisterAI(reqs, fails func() int64) error {
	requests, err := m.meter.Int64ObservableCounter("ai_requests",
		api.WithDescription("AI advisor analysis runs"))
	if err != nil {
		return err
	}
	failures, err := m.meter.Int64ObservableCounter("ai_failures",
		api.WithDescription("AI advisor failures (provider or validation)"))
	if err != nil {
		return err
	}
	_, err = m.meter.RegisterCallback(func(_ context.Context, o api.Observer) error {
		o.ObserveInt64(requests, reqs())
		o.ObserveInt64(failures, fails())
		return nil
	}, requests, failures)
	return err
}

// RegisterTelegram exposes the bot's message/error counters
// (telegram_messages_total, telegram_errors_total).
func (m *Metrics) RegisterTelegram(msgs, errs func() int64) error {
	sent, err := m.meter.Int64ObservableCounter("telegram_messages",
		api.WithDescription("telegram updates handled"))
	if err != nil {
		return err
	}
	failed, err := m.meter.Int64ObservableCounter("telegram_errors",
		api.WithDescription("telegram API errors"))
	if err != nil {
		return err
	}
	_, err = m.meter.RegisterCallback(func(_ context.Context, o api.Observer) error {
		o.ObserveInt64(sent, msgs())
		o.ObserveInt64(failed, errs())
		return nil
	}, sent, failed)
	return err
}

// RegisterHub exposes websocket_clients from the realtime hub.
func (m *Metrics) RegisterHub(clients func() int64) error {
	g, err := m.meter.Int64ObservableGauge("websocket_clients",
		api.WithDescription("connected console websocket clients"))
	if err != nil {
		return err
	}
	_, err = m.meter.RegisterCallback(func(_ context.Context, o api.Observer) error {
		o.ObserveInt64(g, clients())
		return nil
	}, g)
	return err
}

func bookStateValue(s string) int64 {
	switch s {
	case "SYNCING":
		return 0
	case "HEALTHY":
		return 1
	case "STALE":
		return 2
	case "CORRUPTED":
		return 3
	case "DISCONNECTED":
		return 4
	}
	return -1
}

// ---- Scanner Suite + platform pull instruments -------------------------

// VenueStat is one screener venue's collector status at scrape time
// (screener.VenueStatus projected; RateLimited is the collector gate's
// cumulative 429/418/403/in-band count).
type VenueStat struct {
	Venue         string
	Online        bool
	PollLatencyMS int64
	RateLimited   int64
}

// PaperExecStat is one cumulative auto-paper outcome counter.
type PaperExecStat struct {
	Strategy, Outcome string
	Count             int64
}

// ScreenerSources are the Scanner Suite pull callbacks; nil members are
// skipped.
type ScreenerSources struct {
	Venues func() []VenueStat
	// FeedRateLimited is the triangular engine's Binance REST 429/418
	// count (binance.Feed.Stats.RateLimited); it is added to the
	// screener collector's count under venue="binance".
	FeedRateLimited func() int64
	PairsTracked    func() int64
	Alerts          func() map[string]int64 // rule_kind → opened
	PaperExecutions func() []PaperExecStat
}

// RegisterScreener wires the Scanner Suite counters:
// exchange_rate_limited_total{venue}, screener_venue_online{venue},
// screener_poll_latency_ms{venue}, screener_pairs_tracked,
// screener_alerts_total{rule_kind},
// screener_paper_executions_total{strategy,outcome}.
func (m *Metrics) RegisterScreener(src ScreenerSources) error {
	meter := m.meter
	var errs []error
	i64c := func(name, desc string) api.Int64ObservableCounter {
		c, err := meter.Int64ObservableCounter(name, api.WithDescription(desc))
		if err != nil {
			errs = append(errs, err)
		}
		return c
	}
	i64g := func(name, desc string) api.Int64ObservableGauge {
		g, err := meter.Int64ObservableGauge(name, api.WithDescription(desc))
		if err != nil {
			errs = append(errs, err)
		}
		return g
	}
	var (
		rateLimited = i64c("exchange_rate_limited", "rate-limit answers (HTTP 429/418/403 or in-band) per venue, collectors + engine feed")
		online      = i64g("screener_venue_online", "1 when the venue's last poll succeeded, else 0")
		latency     = i64g("screener_poll_latency_ms", "last poll duration per venue in ms")
		pairs       = i64g("screener_pairs_tracked", "distinct base/quote pairs in the screener book")
		alerts      = i64c("screener_alerts", "alerts opened per rule kind")
		paperExecs  = i64c("screener_paper_executions", "automatic PAPER executions per strategy and outcome")
	)
	if err := errors.Join(errs...); err != nil {
		return err
	}
	_, err := meter.RegisterCallback(func(_ context.Context, o api.Observer) error {
		if src.Venues != nil {
			limited := map[string]int64{}
			for _, v := range src.Venues() {
				attrs := api.WithAttributes(attribute.String("venue", v.Venue))
				o.ObserveInt64(online, boolGauge(v.Online), attrs)
				o.ObserveInt64(latency, v.PollLatencyMS, attrs)
				limited[v.Venue] += v.RateLimited
			}
			if src.FeedRateLimited != nil {
				limited["binance"] += src.FeedRateLimited()
			}
			for venue, n := range limited {
				o.ObserveInt64(rateLimited, n, api.WithAttributes(attribute.String("venue", venue)))
			}
		} else if src.FeedRateLimited != nil {
			o.ObserveInt64(rateLimited, src.FeedRateLimited(), api.WithAttributes(attribute.String("venue", "binance")))
		}
		if src.PairsTracked != nil {
			o.ObserveInt64(pairs, src.PairsTracked())
		}
		if src.Alerts != nil {
			for kind, n := range src.Alerts() {
				o.ObserveInt64(alerts, n, api.WithAttributes(attribute.String("rule_kind", kind)))
			}
		}
		if src.PaperExecutions != nil {
			for _, p := range src.PaperExecutions() {
				o.ObserveInt64(paperExecs, p.Count, api.WithAttributes(
					attribute.String("strategy", p.Strategy),
					attribute.String("outcome", p.Outcome)))
			}
		}
		return nil
	}, rateLimited, online, latency, pairs, alerts, paperExecs)
	return err
}

// RegisterMigrations exposes db_migrations_pending (0/1: the database's
// schema_migrations version is behind, dirty, or unreadable, compared
// with the version this binary was built for at boot).
func (m *Metrics) RegisterMigrations(pending func() int64) error {
	g, err := m.meter.Int64ObservableGauge("db_migrations_pending",
		api.WithDescription("1 when schema migrations are pending for this build, else 0"))
	if err != nil {
		return err
	}
	_, err = m.meter.RegisterCallback(func(_ context.Context, o api.Observer) error {
		o.ObserveInt64(g, pending())
		return nil
	}, g)
	return err
}

// RegisterCampaign exposes campaign_runs_total{status} from the §80
// campaign runner's terminal-state counters.
func (m *Metrics) RegisterCampaign(counts func() map[string]int64) error {
	c, err := m.meter.Int64ObservableCounter("campaign_runs",
		api.WithDescription("campaign runs reaching a terminal status (done/failed)"))
	if err != nil {
		return err
	}
	_, err = m.meter.RegisterCallback(func(_ context.Context, o api.Observer) error {
		for status, n := range counts() {
			o.ObserveInt64(c, n, api.WithAttributes(attribute.String("status", status)))
		}
		return nil
	}, c)
	return err
}

func boolGauge(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
