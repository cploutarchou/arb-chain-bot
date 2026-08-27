package app

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/ai"
	"github.com/cploutarchou/arb-chain-bot/internal/notification"
	"github.com/cploutarchou/arb-chain-bot/internal/reporting"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// reportSources builds the §82 section sources over live engine state
// plus the shared services. Every closure is nil-safe pre-readiness.
func reportSources(e *Engine, stratSvc *strategy.Service, center *notification.Center, aiSvc *ai.Service, history reporting.HistorySource) reporting.Sources {
	src := reporting.Sources{
		System: func() reporting.SystemSection {
			st := e.Status()
			out := reporting.SystemSection{Mode: string(e.Mode()), Ready: st.Ready}
			if stratSvc != nil {
				out.ConfigVersion = stratSvc.Current().Version
			}
			if center != nil {
				out.ActiveAlerts = center.ActiveCount()
			}
			return out
		},
		Exchange: func() reporting.ExchangeSection {
			e.mu.RLock()
			feed := e.feed
			e.mu.RUnlock()
			out := reporting.ExchangeSection{Exchange: "binance"}
			if feed == nil {
				return out
			}
			out.Frames = feed.Stats.Frames.Load()
			out.Reconnects = feed.Stats.Reconnects.Load()
			out.APIErrors = feed.Stats.APIErrors.Load()
			out.Resyncs = feed.Stats.Resyncs.Load()
			out.SeqGaps = feed.Stats.SeqGaps.Load()
			if feed.Books != nil {
				for _, id := range feed.Books.All() {
					if v, ok := feed.Books.View(id, 1); ok {
						out.BooksTotal++
						if v.State.String() == "HEALTHY" {
							out.BooksHealthy++
						}
					}
				}
			}
			return out
		},
		Scanner: func() reporting.ScannerSection {
			st := e.Status()
			return reporting.ScannerSection{
				Evaluations: st.Evaluations, Qualified: st.Qualified,
				Rejected: st.Rejected, Skipped: st.Skipped, Dropped: st.Dropped,
			}
		},
		PnL: func() []reporting.AssetSection {
			e.mu.RLock()
			port := e.port
			e.mu.RUnlock()
			if port == nil {
				return nil
			}
			var out []reporting.AssetSection
			for _, a := range e.startAssets() {
				out = append(out, reporting.AssetSection{
					Asset:    string(a),
					Realized: port.Realized(a).String(),
					Fees:     port.FeesPaid(a).String(),
					Drawdown: port.CurrentDrawdown(a).StringFixed(4),
				})
			}
			return out
		},
		Capital: func() []reporting.CapitalSection {
			e.mu.RLock()
			resv := e.resv
			e.mu.RUnlock()
			if resv == nil {
				return nil
			}
			var out []reporting.CapitalSection
			for _, a := range e.startAssets() {
				avail, reserved := resv.Balance(a)
				row := reporting.CapitalSection{
					Asset: string(a), Available: avail.String(), Reserved: reserved.String(),
				}
				if total := avail.Add(reserved); total.IsPositive() {
					row.Utilization = reserved.Div(total).Mul(decimal.NewFromInt(100)).StringFixed(2) + "%"
				}
				out = append(out, row)
			}
			return out
		},
		Risk: func() reporting.RiskSection {
			e.mu.RLock()
			brk := e.brk
			e.mu.RUnlock()
			out := reporting.RiskSection{RejectReasons: e.RejectCounts()}
			if brk != nil {
				for _, tr := range brk.States() {
					if tr.To.String() == "OPEN" {
						out.BreakersOpen++
					}
				}
			}
			return out
		},
		History: history,
	}
	if center != nil {
		src.Incidents = func(since time.Time) []reporting.Incident {
			var out []reporting.Incident
			for _, a := range center.List("", 0) {
				if a.SevName != "CRITICAL" || a.LastAt.Before(since) {
					continue
				}
				out = append(out, reporting.Incident{
					Severity: a.SevName, Title: a.Title, Count: a.Count, LastAt: a.LastAt,
				})
			}
			return out
		}
	}
	if aiSvc != nil {
		src.AI = func() reporting.AISection {
			out := reporting.AISection{Available: true, Proposed: len(aiSvc.Recommendations("proposed"))}
			if latest := aiSvc.Analyses(1); len(latest) > 0 {
				out.Summary = latest[0].Summary
			}
			return out
		}
	}
	return src
}
