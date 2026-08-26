package app

import (
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/ai"
)

// AIInput builds the typed analysis summary from live engine state —
// aggregates only, per the prompt-injection boundary (docs/security.md
// §6): no raw books, no user text, no secrets.
func (e *Engine) AIInput(kind ai.AnalysisKind) ai.Input {
	st := e.Status()
	in := ai.Input{
		Kind: kind, At: time.Now().UTC(), Mode: string(e.cfg.Mode),
		Scanner: ai.ScannerSummary{
			Evaluations: st.Evaluations, Qualified: st.Qualified,
			Rejected: st.Rejected, SkippedBooks: st.Skipped, DroppedEvents: st.Dropped,
		},
		Rejects: e.RejectCounts(),
	}
	if e.Strategy != nil {
		snap := e.Strategy.Current()
		in.ConfigVersion = snap.Version
		in.Params = snap.Params
	}
	e.mu.RLock()
	feed, port := e.feed, e.port
	e.mu.RUnlock()
	if feed != nil {
		in.Feed = ai.FeedSummary{
			Frames:     feed.Stats.Frames.Load(),
			Reconnects: feed.Stats.Reconnects.Load(),
			APIErrors:  feed.Stats.APIErrors.Load(),
			Resyncs:    feed.Stats.Resyncs.Load(),
			SeqGaps:    feed.Stats.SeqGaps.Load(),
		}
	}
	if st.Paper != nil {
		in.Paper = &ai.PaperSummary{
			Received: st.Paper.Received, Completed: st.Paper.Completed,
			Failed: st.Paper.Failed, Skipped: st.Paper.Skipped,
			Active: st.Paper.Active,
		}
	}
	if port != nil {
		for _, a := range e.startAssets() {
			in.PnL = append(in.PnL, ai.AssetSummary{
				Asset:    string(a),
				Realized: port.Realized(a).String(),
				Fees:     port.FeesPaid(a).String(),
				Loss:     port.DailyLoss(a).String(),
				Drawdown: port.CurrentDrawdown(a).StringFixed(4),
			})
		}
	}
	if e.Notifier != nil {
		// Recent ring is enough for the "how noisy are we" signal.
		in.Alerts = ai.AlertSummary{Active: len(e.Notifier.Recent(0))}
	}
	return in
}
