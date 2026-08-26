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
	"fmt"
	"net/http"

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
	return m, nil
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
}

// RegisterEngine wires the engine's atomic counters into observable
// instruments. Call once, after the engine's components exist.
func (m *Metrics) RegisterEngine(src EngineSources) error {
	meter := m.meter
	var (
		evals, _      = meter.Int64ObservableCounter("triangles_evaluated", api.WithDescription("triangle evaluations"))
		detected, _   = meter.Int64ObservableCounter("opportunities_detected", api.WithDescription("opportunities evaluated to a decision"))
		qualified, _  = meter.Int64ObservableCounter("opportunities_qualified", api.WithDescription("opportunities passing the risk engine"))
		rejected, _   = meter.Int64ObservableCounter("opportunities_rejected", api.WithDescription("opportunities rejected by the risk engine"))
		skipped, _    = meter.Int64ObservableCounter("scanner_skipped_unhealthy", api.WithDescription("evaluations skipped on missing/unhealthy books"))
		dropped, _    = meter.Int64ObservableCounter("scanner_dropped_events", api.WithDescription("scanner events dropped by slow consumers"))
		triangles, _  = meter.Int64ObservableGauge("triangles_total", api.WithDescription("triangles in the active topology"))
		frames, _     = meter.Int64ObservableCounter("market_messages", api.WithDescription("market data frames received"))
		reconnects, _ = meter.Int64ObservableCounter("exchange_reconnects", api.WithDescription("feed session reconnects"))
		apiErrors, _  = meter.Int64ObservableCounter("exchange_api_errors", api.WithDescription("exchange REST errors"))
		resyncs, _    = meter.Int64ObservableCounter("orderbook_resync", api.WithDescription("order book resyncs"))
		seqErrors, _  = meter.Int64ObservableCounter("orderbook_sequence_errors", api.WithDescription("sequence gaps detected"))
		bookAge, _    = meter.Float64ObservableGauge("orderbook_age_ms", api.WithDescription("book age at scrape"))
		bookState, _  = meter.Int64ObservableGauge("orderbook_state", api.WithDescription("0 SYNCING,1 HEALTHY,2 STALE,3 CORRUPTED,4 DISCONNECTED"))
		capAvail, _   = meter.Float64ObservableGauge("capital_available", api.WithDescription("available capital (display-only float)"))
		capResv, _    = meter.Float64ObservableGauge("capital_reserved", api.WithDescription("reserved capital (display-only float)"))
		breaker, _    = meter.Int64ObservableGauge("circuit_breaker_state", api.WithDescription("0 CLOSED,1 HALF_OPEN,2 OPEN"))
		papRecv, _    = meter.Int64ObservableCounter("paper_cycles", api.WithDescription("paper cycles started"))
		papOK, _      = meter.Int64ObservableCounter("paper_cycles_success", api.WithDescription("paper cycles completed"))
		papFail, _    = meter.Int64ObservableCounter("paper_cycles_failed", api.WithDescription("paper cycles failed"))
		papActive, _  = meter.Int64ObservableGauge("paper_active_simulations", api.WithDescription("in-flight simulations"))
		papPnL, _     = meter.Float64ObservableGauge("paper_pnl", api.WithDescription("realized session PnL per start asset (display-only float)"))
		feesTotal, _  = meter.Float64ObservableCounter("fees", api.WithDescription("cumulative simulated fees per start asset (display-only float)"))
		recWritten, _ = meter.Int64ObservableCounter("recorder_frames_written", api.WithDescription("recorded frames written"))
		recDropped, _ = meter.Int64ObservableCounter("recorder_frames_dropped", api.WithDescription("recorded frames dropped on overflow"))
	)
	insts := []api.Observable{
		evals, detected, qualified, rejected, skipped, dropped, triangles,
		frames, reconnects, apiErrors, resyncs, seqErrors, bookAge, bookState,
		capAvail, capResv, breaker, papRecv, papOK, papFail, papActive, papPnL,
		feesTotal, recWritten, recDropped,
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
