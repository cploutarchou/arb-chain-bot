package app

import (
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
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
	marker := r.e.marker
	r.e.mu.RUnlock()
	if port == nil {
		return nil, false
	}
	rows := make([]map[string]any, 0, 2)
	for _, a := range r.e.startAssets() {
		// realized is cash basis (what came back minus what was deployed);
		// exposure_mark values the intermediate assets still held; net_pnl
		// is their sum and the figure daily_loss is measured on. unmarked
		// lists exposure assets no book can value (counted at zero).
		mark, unmarked := port.ExposureMark(a, marker)
		net, _ := port.NetPnL(a, marker)
		if unmarked == nil {
			unmarked = []exchange.Asset{}
		}
		// fees is the slice charged in the start asset itself (kept for
		// readers that predate the valuation); fees_marked values every
		// fee asset in the start asset, fees_by_asset is the raw map, and
		// fees_unmarked lists fee assets no book can value.
		feesMarked, feesByAsset, feesUnmarked := port.FeesMark(a, marker)
		byAsset := make(map[string]string, len(feesByAsset))
		for fa, v := range feesByAsset {
			byAsset[string(fa)] = v.String()
		}
		if feesUnmarked == nil {
			feesUnmarked = []exchange.Asset{}
		}
		rows = append(rows, map[string]any{
			"asset":         string(a),
			"realized":      port.Realized(a).String(),
			"exposure_mark": mark.String(),
			"net_pnl":       net.String(),
			"unmarked":      unmarked,
			"fees":          port.FeesPaid(a).String(),
			"fees_marked":   feesMarked.String(),
			"fees_by_asset": byAsset,
			"fees_unmarked": feesUnmarked,
			"daily_loss":    port.DailyLoss(a, marker).String(),
			"drawdown":      port.CurrentDrawdown(a).StringFixed(4),
		})
	}
	return map[string]any{"assets": rows}, true
}

// PaperActive snapshots the paper engine's in-flight cycles (audit F6:
// the console's live-cycle monitor). ok=false when no paper engine
// exists in this profile.
func (r readModel) PaperActive() (any, bool) {
	r.e.mu.RLock()
	pap := r.e.pap
	r.e.mu.RUnlock()
	if pap == nil {
		return nil, false
	}
	return map[string]any{
		"running": pap.Running(),
		"cycles":  pap.ActiveCycles(),
	}, true
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
			"revalidations": st.Revalidations, "revalidation_rejects": st.RevalidationRejects,
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
	// P1-8: the venue clock offset the RISK_CLOCK_UNSAFE gate reads.
	// healthy is false until the first successful probe.
	if clock := r.e.currentClock(); clock != nil {
		clockView := map[string]any{
			"healthy":   clock.Healthy(),
			"offset_ms": float64(clock.Offset().Microseconds()) / 1000,
		}
		if err := clock.LastError(); err != nil {
			clockView["last_error"] = err.Error()
		}
		out["clock"] = clockView
	}
	// BL-18: queue depths (outbox persistence, paper's inbound event
	// channel) — the engine-derived half; recorder queue depth and
	// process/DB stats are assembled at the API layer, which has no
	// engine dependency to reach them.
	// P0-3: every way a financial record can fail to land is a number
	// here — refused at the queue (dropped), refused by the database
	// (write_failures), persisted without its opportunity row
	// (unlinked_cycles) — plus whether the writer is currently failing.
	queues := map[string]any{}
	if ob := r.e.currentOutbox(); ob != nil {
		outbox := map[string]any{
			"depth": ob.Depth(), "capacity": ob.Capacity(),
			"dropped": ob.Dropped(), "written": ob.Written(),
			"write_failures": ob.WriteFailures(), "failing": ob.Failing(),
		}
		if r.e.Store != nil {
			outbox["unlinked_cycles"] = r.e.Store.UnlinkedCycles()
		}
		queues["outbox"] = outbox
	}
	r.e.mu.RLock()
	pap := r.e.pap
	r.e.mu.RUnlock()
	if pap != nil {
		queues["paper"] = map[string]any{
			"depth": pap.QueueDepth(), "capacity": pap.QueueCapacity(),
			"dropped": r.e.paperDropped.Load(),
		}
	}
	if len(queues) > 0 {
		out["queues"] = queues
	}
	return out
}
