package app

import (
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
)

// observeCycle exports one settled cycle's outcome and latency chain
// (audit O3/O5): outcome and per-order status counts, the order latency
// stages that calibrate the latency buffer, and the cycle wall time.
// Called once per settlement, off the per-frame hot path; nil metrics
// disables it with no other effect.
func (e *Engine) observeCycle(exchangeID string, res execution.CycleResult) {
	m := e.Metrics
	if m == nil {
		return
	}
	m.CountCycleOutcome(exchangeID, string(res.Outcome))
	if !res.StartedAt.IsZero() && !res.SettledAt.IsZero() && res.SettledAt.After(res.StartedAt) {
		m.ObserveCycleDuration(exchangeID, float64(res.SettledAt.Sub(res.StartedAt).Microseconds())/1000)
	}
	for _, o := range res.Orders {
		m.CountOrderStatus(exchangeID, string(o.Status))
		if o.CreatedAt.IsZero() {
			continue
		}
		if !o.AckedAt.IsZero() && !o.AckedAt.Before(o.CreatedAt) {
			m.ObserveOrderLatency(exchangeID, "submit_ack", float64(o.AckedAt.Sub(o.CreatedAt).Microseconds())/1000)
			if !o.FilledAt.IsZero() && !o.FilledAt.Before(o.AckedAt) {
				m.ObserveOrderLatency(exchangeID, "ack_fill", float64(o.FilledAt.Sub(o.AckedAt).Microseconds())/1000)
			}
		}
		if !o.FilledAt.IsZero() && !o.FilledAt.Before(o.CreatedAt) {
			m.ObserveOrderLatency(exchangeID, "submit_fill", float64(o.FilledAt.Sub(o.CreatedAt).Microseconds())/1000)
		}
	}
}
