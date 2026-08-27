package app

import (
	"sync/atomic"

	"github.com/cploutarchou/arb-chain-bot/internal/metrics"
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/alerts"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/paperexec"
)

// migrationsPending is the boot-time db_migrations_pending value (0/1),
// set once in buildComponents from storage.Store.MigrationsPending.
var migrationsPending atomic.Int64

// screenerMetricSources projects the Scanner Suite's live status into
// the exporter's pull callbacks (deploy/observability/platform-rules.yml
// CollectorRateLimited* read exchange_rate_limited_total). Every source
// is an accessor closure so a collector restart keeps reporting.
//
// feedRateLimited is late-bound: the triangular engine (whose Binance
// feed counts its own REST 429/418s) is built after the screener block;
// nil until then, and nil for profiles without an engine.
func screenerMetricSources(svc *screener.Service, ev *alerts.Evaluator, ex *paperexec.Executor, feedRateLimited *func() int64) metrics.ScreenerSources {
	return metrics.ScreenerSources{
		FeedRateLimited: func() int64 {
			if feedRateLimited == nil || *feedRateLimited == nil {
				return 0
			}
			return (*feedRateLimited)()
		},
		Venues: func() []metrics.VenueStat {
			_, status := svc.CollectorStatus()
			out := make([]metrics.VenueStat, 0, len(status))
			for _, st := range status {
				out = append(out, metrics.VenueStat{Venue: string(st.ID), Online: st.Online,
					PollLatencyMS: st.PollMS, RateLimited: int64(st.RateLimited)})
			}
			return out
		},
		PairsTracked: func() int64 { return int64(len(svc.Book.Pairs())) },
		Alerts: func() map[string]int64 {
			opened := ev.Opened()
			out := make(map[string]int64, len(opened))
			for kind, n := range opened {
				out[string(kind)] = n
			}
			return out
		},
		PaperExecutions: func() []metrics.PaperExecStat {
			oc := ex.Outcomes()
			out := make([]metrics.PaperExecStat, 0, len(oc))
			for _, o := range oc {
				out = append(out, metrics.PaperExecStat{Strategy: string(o.Strategy), Outcome: o.Outcome, Count: o.Count})
			}
			return out
		},
	}
}
