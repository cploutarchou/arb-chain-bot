package metrics

import (
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("scrape = %d", rec.Code)
	}
	return rec.Body.String()
}

// The full SKILL §65 set for implemented subsystems must appear on the
// exposition page with the promised names (counters carry the exporter's
// _total suffix). AI and Telegram metrics register with their subsystems
// (T-033/T-036) — no fake series for components that do not exist.
func TestExpositionExposesTheMetricSet(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Shutdown(t.Context()) }()

	var frames atomic.Int64
	frames.Store(1234)
	src := EngineSources{
		Scanner: func() ScannerStats {
			return ScannerStats{Evaluations: 10, Qualified: 3, Rejected: 6, SkippedBooks: 1, DroppedEvents: 0}
		},
		Triangles: func() int64 { return 2 },
		Feed: func() FeedStats {
			return FeedStats{Exchange: "binance", Frames: frames.Load(), Reconnects: 1, APIErrors: 2, Resyncs: 3, SeqErrors: 4}
		},
		Books: func() []BookStat {
			return []BookStat{{Exchange: "binance", Market: "BTCUSDT", AgeMS: 12.5, State: "HEALTHY"}}
		},
		Capital: func() []CapitalStat {
			return []CapitalStat{{Asset: "USDT", Available: 9000, Reserved: 1000}}
		},
		Breakers: func() []BreakerStat {
			return []BreakerStat{{Name: "exchange", Scope: "exchange:binance", State: 0}}
		},
		Paper: func() *PaperStats {
			return &PaperStats{Received: 5, Started: 4, Completed: 3, Failed: 1, Active: 1,
				RealizationRatio: 0.62, HasRealization: true}
		},
		PnL: func() []AssetPnL {
			return []AssetPnL{{Asset: "USDT", Realized: 12.5, Fees: 1.25}}
		},
		Recorder: func() (int64, int64) { return 100, 2 },
		Outbox: func() *QueueStats {
			return &QueueStats{Depth: 3, Capacity: 4096, Dropped: 1, WriteFailures: 2, Written: 40}
		},
		PaperQueue: func() *QueueStats { return &QueueStats{Depth: 1, Capacity: 128, Dropped: 5} },
	}
	if err := m.RegisterEngine(src); err != nil {
		t.Fatal(err)
	}
	if err := m.RegisterHub(func() int64 { return 7 }); err != nil {
		t.Fatal(err)
	}
	m.ObserveEval(0.42)
	m.ObserveAPIRequest("GET", "/api/v1/config", 200, 0.003)
	m.ObserveMessageLatency("binance", 18)
	m.ObserveQualifiedEdge("binance", 12)
	m.ObserveSlippage("binance", -1.5)
	m.ObserveOrderLatency("binance", "submit_ack", 21)
	m.ObserveOrderLatency("binance", "ack_fill", 40)
	m.ObserveCycleDuration("binance", 180)
	m.CountRejection("binance", "qualification", "RISK_MIN_EDGE")
	m.CountRejection("binance", "qualification", "RISK_MIN_EDGE")
	m.CountRejection("binance", "revalidation", "RISK_BREAKER_OPEN")
	m.CountCycleOutcome("binance", "LEG1_PARTIAL")
	m.CountOrderStatus("binance", "PARTIALLY_FILLED")

	page := scrape(t, m)
	for _, name := range []string{
		"market_messages_total",
		"market_message_latency_ms_bucket",
		"orderbook_age_ms",
		"orderbook_state",
		"orderbook_sequence_errors_total",
		"orderbook_resync_total",
		"triangles_total",
		"triangles_evaluated_total",
		"triangle_evaluation_duration_bucket",
		"opportunities_detected_total",
		"opportunities_qualified_total",
		"opportunities_rejected_total",
		"net_edge_bps_bucket",
		"paper_cycles_total",
		"paper_cycles_success_total",
		"paper_cycles_failed_total",
		"paper_pnl",
		"fees_total",
		"slippage_bps_bucket",
		"capital_available",
		"capital_reserved",
		"exchange_reconnects_total",
		"exchange_api_errors_total",
		"api_request_duration_bucket",
		"websocket_clients",
		"circuit_breaker_state",
		"recorder_frames_written_total",
		"recorder_frames_dropped_total",
		"paper_cycles_received_total",
		"paper_cycles_skipped_total",
		"paper_realization_ratio",
		"outbox_queue_depth",
		"outbox_queue_capacity",
		"outbox_records_dropped_total",
		"outbox_write_failures_total",
		"outbox_records_written_total",
		"paper_queue_depth",
		"paper_queue_capacity",
		"paper_queue_dropped_total",
		"opportunities_revalidated_total",
		"opportunities_revalidation_rejected_total",
		"order_latency_ms_bucket",
		"cycle_duration_ms_bucket",
		"risk_rejections_total",
		"paper_cycle_outcomes_total",
		"order_outcomes_total",
	} {
		if !strings.Contains(page, name) {
			t.Errorf("exposition missing %s", name)
		}
	}
	// Label sanity on a few series.
	for _, frag := range []string{
		`market_messages_total{exchange="binance"} 1234`,
		`orderbook_state{exchange="binance",market="BTCUSDT"} 1`,
		`capital_available{asset="USDT"} 9000`,
		`websocket_clients 7`,
		`outbox_write_failures_total 2`,
		`paper_queue_dropped_total 5`,
		`risk_rejections_total{exchange="binance",reason="RISK_MIN_EDGE",stage="qualification"} 2`,
		`risk_rejections_total{exchange="binance",reason="RISK_BREAKER_OPEN",stage="revalidation"} 1`,
		`paper_cycle_outcomes_total{exchange="binance",outcome="LEG1_PARTIAL"} 1`,
		`order_outcomes_total{exchange="binance",status="PARTIALLY_FILLED"} 1`,
		`order_latency_ms_count{exchange="binance",stage="submit_ack"} 1`,
		`cycle_duration_ms_count{exchange="binance"} 1`,
	} {
		if !strings.Contains(page, frag) {
			t.Errorf("exposition missing series %q", frag)
		}
	}
}

func TestNilSourcesAreSkipped(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Shutdown(t.Context()) }()
	if err := m.RegisterEngine(EngineSources{}); err != nil {
		t.Fatal(err)
	}
	page := scrape(t, m)
	if strings.Contains(page, "market_messages_total") {
		t.Fatal("nil feed source must not emit series")
	}
}

// Hot-path budget: the per-frame counter cost is one atomic add; the SDK
// is only touched at scrape time. Sync histograms bound the observer
// cost. Recorded on the dev container (Xeon 2.8GHz, -benchtime=1s):
// atomic add ~7.5 ns/op 0 allocs; unlabeled histogram record ~135 ns/op
// 0 allocs; per-exchange latency record with precomputed attribute set
// ~280 ns/op 1 alloc — noise against multi-hundred-µs evaluations and
// network I/O.
func BenchmarkHotPathAtomicAdd(b *testing.B) {
	var counter atomic.Int64
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		counter.Add(1)
	}
}

func BenchmarkEvalHistogramRecord(b *testing.B) {
	m, err := New()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = m.Shutdown(b.Context()) }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.ObserveEval(0.5)
	}
}

func BenchmarkMessageLatencyRecord(b *testing.B) {
	m, err := New()
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = m.Shutdown(b.Context()) }()
	record := m.MessageLatencyRecorder("binance")
	d := 15 * time.Millisecond
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		record(float64(d.Nanoseconds()) / 1e6)
	}
}

// The three series deploy/observability/platform-rules.yml marks
// "PENDING EXPORTER" plus the Scanner Suite set, with their labels.
func TestScreenerAndPlatformSeries(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Shutdown(t.Context()) }()
	if err := m.RegisterScreener(ScreenerSources{
		Venues: func() []VenueStat {
			return []VenueStat{
				{Venue: "binance", Online: true, PollLatencyMS: 120, RateLimited: 2},
				{Venue: "mexc", Online: false, PollLatencyMS: 9000, RateLimited: 5},
			}
		},
		FeedRateLimited: func() int64 { return 3 }, // adds to binance → 5
		PairsTracked:    func() int64 { return 412 },
		Alerts:          func() map[string]int64 { return map[string]int64{"spread": 7, "carry": 1} },
		PaperExecutions: func() []PaperExecStat {
			return []PaperExecStat{{Strategy: "cross_venue_spot", Outcome: "executed", Count: 4},
				{Strategy: "cross_venue_spot", Outcome: "skipped", Count: 2}}
		},
		Lanes: func() *LaneStat {
			return &LaneStat{Universe: 601, Lanes: 500, Truncated: 101, TruncatedTotal: 101, Holding: 3, HoldTimeoutCloses: 2}
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.RegisterMigrations(func() int64 { return 1 }); err != nil {
		t.Fatal(err)
	}
	if err := m.RegisterCampaign(func() map[string]int64 { return map[string]int64{"done": 3, "failed": 1} }); err != nil {
		t.Fatal(err)
	}
	if err := m.RegisterReplay(func() map[string]int64 { return map[string]int64{"done": 2, "failed": 4} }); err != nil {
		t.Fatal(err)
	}
	page := scrape(t, m)
	for _, frag := range []string{
		`exchange_rate_limited_total{venue="binance"} 5`,
		`exchange_rate_limited_total{venue="mexc"} 5`,
		`screener_venue_online{venue="binance"} 1`,
		`screener_venue_online{venue="mexc"} 0`,
		`screener_poll_latency_ms{venue="mexc"} 9000`,
		`screener_pairs_tracked 412`,
		`screener_alerts_total{rule_kind="spread"} 7`,
		`screener_alerts_total{rule_kind="carry"} 1`,
		`screener_paper_executions_total{outcome="executed",strategy="cross_venue_spot"} 4`,
		`screener_paper_executions_total{outcome="skipped",strategy="cross_venue_spot"} 2`,
		`screener_lane_universe 601`,
		`screener_lanes_evaluated 500`,
		`screener_lanes_truncated_total 101`,
		`screener_lanes_holding 3`,
		`screener_hold_timeout_closes_total 2`,
		`db_migrations_pending 1`,
		`campaign_runs_total{status="done"} 3`,
		`campaign_runs_total{status="failed"} 1`,
		`replay_runs_total{status="done"} 2`,
		`replay_runs_total{status="failed"} 4`,
	} {
		if !strings.Contains(page, frag) {
			t.Errorf("exposition missing series %q", frag)
		}
	}
}

func TestScreenerNilSourcesAreSkipped(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Shutdown(t.Context()) }()
	if err := m.RegisterScreener(ScreenerSources{}); err != nil {
		t.Fatal(err)
	}
	page := scrape(t, m)
	for _, name := range []string{"exchange_rate_limited_total{", "screener_venue_online{", "screener_pairs_tracked ", "screener_alerts_total{"} {
		if strings.Contains(page, name) {
			t.Errorf("nil source must not emit %s", name)
		}
	}
}
