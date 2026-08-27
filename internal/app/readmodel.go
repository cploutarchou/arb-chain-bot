package app

import (
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// readModel adapts engine state to the API's read groups. Every method
// is nil-safe before readiness and answers honest emptiness. The
// message-rate sampler now lives on the Engine (BL-18 review P2-1):
// readModel only ever reads it, so any number of concurrent pollers
// (multiple browser tabs, web + Telegram) see the same value instead of
// each stealing part of the previous poller's delta window.
type readModel struct {
	e *Engine
	s *strategy.Service
}

// NewReadModel wires the adapter (exported for component assembly).
func NewReadModel(e *Engine, s *strategy.Service) readModel {
	return readModel{e: e, s: s}
}

func (r readModel) RecentOpportunities(limit int) any {
	return r.e.RecentOpportunities(limit)
}

func (r readModel) Portfolio() (any, bool) {
	r.e.mu.RLock()
	port, resv, marker := r.e.port, r.e.resv, r.e.marker
	r.e.mu.RUnlock()
	if port == nil || resv == nil {
		return nil, false
	}
	snap := port.TakeSnapshot(time.Now(), marker)
	balances := map[string]map[string]string{}
	for _, a := range r.e.startAssets() {
		avail, reserved := resv.Balance(a)
		balances[string(a)] = map[string]string{
			"available": avail.String(), "reserved": reserved.String(),
		}
	}
	exposure := map[string]string{}
	for asset, qty := range snap.Exposure {
		exposure[string(asset)] = qty.String()
	}
	equity := map[string]string{}
	for asset, v := range snap.Equity {
		equity[string(asset)] = v.String()
	}
	unmarked := make([]string, 0, len(snap.Unmarked))
	for _, a := range snap.Unmarked {
		unmarked = append(unmarked, string(a))
	}
	return map[string]any{
		"balances": balances,
		"exposure": exposure,
		"equity":   equity,
		"unmarked": unmarked,
		"cycles":   snap.Cycles, "completed": snap.Completed, "failed": snap.Failed,
		"at": snap.At,
	}, true
}

func (r readModel) PnL() (any, bool) {
	r.e.mu.RLock()
	port := r.e.port
	r.e.mu.RUnlock()
	if port == nil {
		return nil, false
	}
	rows := make([]map[string]string, 0, 2)
	for _, a := range r.e.startAssets() {
		rows = append(rows, map[string]string{
			"asset":      string(a),
			"realized":   port.Realized(a).String(),
			"fees":       port.FeesPaid(a).String(),
			"daily_loss": port.DailyLoss(a).String(),
			"drawdown":   port.CurrentDrawdown(a).StringFixed(4),
		})
	}
	return map[string]any{"assets": rows}, true
}

func (r readModel) Risk() any {
	out := map[string]any{}
	if r.s != nil {
		snap := r.s.Current()
		out["config_version"] = snap.Version
		out["limits"] = snap.Params.Risk
	}
	r.e.mu.RLock()
	brk := r.e.brk
	r.e.mu.RUnlock()
	if brk != nil {
		type breakerRow struct {
			Name, Scope, State, Reason string
		}
		var rows []breakerRow
		for _, tr := range brk.States() {
			rows = append(rows, breakerRow{Name: tr.Name, Scope: tr.Scope,
				State: tr.To.String(), Reason: tr.Reason})
		}
		out["breakers"] = rows
	}
	out["reject_reason_counts"] = r.e.RejectCounts()
	return out
}

func (r readModel) Health() any {
	st := r.e.Status()
	out := map[string]any{
		"ready":     st.Ready,
		"triangles": st.Triangles,
		"scanner": map[string]int64{
			"evaluations": st.Evaluations, "qualified": st.Qualified,
			"rejected": st.Rejected, "skipped": st.Skipped, "dropped": st.Dropped,
		},
	}
	r.e.mu.RLock()
	feed := r.e.feed
	r.e.mu.RUnlock()
	if feed != nil {
		frames := feed.Stats.Frames.Load()
		var msgsPerSec float64
		if rate := r.e.currentMsgRate(); rate != nil {
			msgsPerSec, _ = rate.current()
		}
		out["feed"] = map[string]any{
			"frames":       frames,
			"reconnects":   feed.Stats.Reconnects.Load(),
			"api_errors":   feed.Stats.APIErrors.Load(),
			"resyncs":      feed.Stats.Resyncs.Load(),
			"seq_gaps":     feed.Stats.SeqGaps.Load(),
			"rate_limited": feed.Stats.RateLimited.Load(),
			"msgs_per_sec": msgsPerSec,
		}
		if window := r.e.currentLatency(); window != nil {
			out["feed"].(map[string]any)["latency_ms"] = window.Snapshot()
		}
		if feed.Books != nil {
			now := time.Now()
			books := []map[string]any{}
			for _, id := range feed.Books.All() {
				if v, ok := feed.Books.View(id, 1); ok {
					books = append(books, map[string]any{
						"market": string(id.Symbol),
						"state":  v.State.String(),
						"age_ms": v.Age(now).Milliseconds(),
					})
				}
			}
			out["books"] = books
		}
	}
	if st.Paper != nil {
		out["paper"] = st.Paper
	}
	// BL-18: queue depths (outbox persistence, paper's inbound event
	// channel) — the engine-derived half; recorder queue depth and
	// process/DB stats are assembled at the API layer, which has no
	// engine dependency to reach them.
	queues := map[string]any{}
	if ob := r.e.currentOutbox(); ob != nil {
		queues["outbox"] = map[string]any{
			"depth": ob.Depth(), "capacity": ob.Capacity(),
			"dropped": ob.Dropped(), "written": ob.Written(),
		}
	}
	r.e.mu.RLock()
	pap := r.e.pap
	r.e.mu.RUnlock()
	if pap != nil {
		queues["paper"] = map[string]any{"depth": pap.QueueDepth(), "capacity": pap.QueueCapacity()}
	}
	if len(queues) > 0 {
		out["queues"] = queues
	}
	return out
}
