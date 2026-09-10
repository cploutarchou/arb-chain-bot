package app

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/metrics"
)

// A settled cycle exports its outcome, each order's status and the
// latency stages the buffer is calibrated against.
func TestObserveCycleExportsOutcomeAndLatencyChain(t *testing.T) {
	m, err := metrics.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Shutdown(t.Context()) }()
	e := NewEngine(config.Bootstrap{}, testLogger())
	e.Metrics = m

	t0 := time.Unix(1_700_000_000, 0)
	res := execution.CycleResult{
		Outcome: execution.OutcomeLeg1FilledLeg2Failed, StartedAt: t0, SettledAt: t0.Add(180 * time.Millisecond),
		Orders: []execution.SimOrder{
			{Status: execution.OrderFilled, CreatedAt: t0, AckedAt: t0.Add(20 * time.Millisecond), FilledAt: t0.Add(60 * time.Millisecond)},
			{Status: execution.OrderRejected, CreatedAt: t0.Add(70 * time.Millisecond), AckedAt: t0.Add(95 * time.Millisecond)},
		},
	}
	e.observeCycle("binance", res)
	// Nil metrics is a no-op.
	NewEngine(config.Bootstrap{}, testLogger()).observeCycle("binance", res)

	rec := httptest.NewRecorder()
	m.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	page := rec.Body.String()
	for _, frag := range []string{
		`paper_cycle_outcomes_total{exchange="binance",outcome="LEG1_FILLED_LEG2_FAILED"} 1`,
		`order_outcomes_total{exchange="binance",status="FILLED"} 1`,
		`order_outcomes_total{exchange="binance",status="REJECTED"} 1`,
		`order_latency_ms_count{exchange="binance",stage="submit_ack"} 2`,
		`order_latency_ms_count{exchange="binance",stage="ack_fill"} 1`,
		`order_latency_ms_count{exchange="binance",stage="submit_fill"} 1`,
		`order_latency_ms_sum{exchange="binance",stage="submit_ack"} 45`,
		`cycle_duration_ms_sum{exchange="binance"} 180`,
	} {
		if !strings.Contains(page, frag) {
			t.Errorf("exposition missing %s", frag)
		}
	}
}
