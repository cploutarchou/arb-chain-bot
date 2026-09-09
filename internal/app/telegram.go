package app

import (
	"context"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/ai"
	"github.com/cploutarchou/arb-chain-bot/internal/api"
	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange/binance"
	"github.com/cploutarchou/arb-chain-bot/internal/notification"
	"github.com/cploutarchou/arb-chain-bot/internal/reporting"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
	"github.com/cploutarchou/arb-chain-bot/internal/telegram"
)

// telegramServices adapts the engine (and its shared components) to the
// bot's view interfaces. Every method is nil-safe before engine
// readiness — the bot then reports honest emptiness. Control actions go
// through the same paper controller the web console uses, so state is
// one and the same.
type telegramServices struct {
	e   *Engine
	n   *notification.Service
	c   *notification.Center
	s   *strategy.Service
	ai  *ai.Service
	rep *reporting.Generator
}

func (t telegramServices) Status() telegram.StatusView {
	st := t.e.Status()
	view := telegram.StatusView{
		Mode:        string(t.e.Mode()),
		Ready:       st.Ready,
		Triangles:   st.Triangles,
		Markets:     len(st.Markets),
		Evaluations: st.Evaluations,
		Qualified:   st.Qualified,
		Rejected:    st.Rejected,
	}
	if t.s != nil {
		view.ConfigVersion = t.s.Current().Version
	}
	if st.Paper != nil {
		view.Paper = &telegram.PaperView{
			Running: st.Paper.Running, Active: st.Paper.Active,
			Received: st.Paper.Received, Completed: st.Paper.Completed,
			Failed: st.Paper.Failed, Skipped: st.Paper.Skipped,
		}
	}
	return view
}

func (t telegramServices) Feed() telegram.FeedView {
	t.e.mu.RLock()
	feed := t.e.feed
	t.e.mu.RUnlock()
	if feed == nil {
		return telegram.FeedView{Exchange: "binance"}
	}
	view := telegram.FeedView{
		Exchange:   "binance",
		Frames:     feed.Stats.Frames.Load(),
		Reconnects: feed.Stats.Reconnects.Load(),
		APIErrors:  feed.Stats.APIErrors.Load(),
		Resyncs:    feed.Stats.Resyncs.Load(),
		Gaps:       feed.Stats.SeqGaps.Load(),
	}
	if feed.Books != nil {
		now := time.Now()
		for _, id := range feed.Books.All() {
			if v, ok := feed.Books.View(id, 1); ok {
				view.Books = append(view.Books, telegram.BookView{
					Market: string(id.Symbol), State: v.State.String(),
					AgeMS: v.Age(now).Milliseconds(),
				})
			}
		}
	}
	return view
}

func (t telegramServices) Balances() []telegram.BalanceView {
	t.e.mu.RLock()
	resv := t.e.resv
	t.e.mu.RUnlock()
	if resv == nil {
		return nil
	}
	var out []telegram.BalanceView
	for _, a := range t.e.startAssets() {
		avail, reserved := resv.Balance(a)
		out = append(out, telegram.BalanceView{
			Asset: string(a), Available: avail.String(), Reserved: reserved.String(),
		})
	}
	return out
}

func (t telegramServices) PnL() []telegram.PnLView {
	t.e.mu.RLock()
	port := t.e.port
	marker := t.e.marker
	t.e.mu.RUnlock()
	if port == nil {
		return nil
	}
	var out []telegram.PnLView
	for _, a := range t.e.startAssets() {
		out = append(out, telegram.PnLView{
			Asset:    string(a),
			Realized: port.Realized(a).String(),
			Fees:     port.FeesPaid(a).String(),
			Loss:     port.DailyLoss(a, marker).String(),
			Drawdown: port.CurrentDrawdown(a).StringFixed(4),
		})
	}
	return out
}

func (t telegramServices) Opportunities(limit int) []telegram.OppView {
	var out []telegram.OppView
	for _, o := range t.e.RecentOpportunities(limit) {
		out = append(out, telegram.OppView{
			ID: o.ID, Triangle: o.TriangleID, NetBps: o.NetBps,
			Profit: o.Profit, Input: o.Input, At: o.At,
		})
	}
	return out
}

func (t telegramServices) Config() (strategy.Snapshot, bool) {
	if t.s == nil {
		return strategy.Snapshot{}, false
	}
	return t.s.Current(), true
}

func (t telegramServices) Breakers() []telegram.BreakerView {
	t.e.mu.RLock()
	brk := t.e.brk
	t.e.mu.RUnlock()
	if brk == nil {
		return nil
	}
	var out []telegram.BreakerView
	for _, tr := range brk.States() {
		out = append(out, telegram.BreakerView{Name: tr.Name, Scope: tr.Scope, State: tr.To.String()})
	}
	return out
}

func (t telegramServices) Alerts(limit int) []notification.Alert {
	if t.c == nil {
		return nil
	}
	return t.c.List("", limit)
}

func (t telegramServices) AckAlert(id, actor string) (notification.Alert, error) {
	if t.c == nil {
		return notification.Alert{}, notification.ErrAlertNotFound
	}
	return t.c.Ack(id, actor)
}

func (t telegramServices) AIPresent() bool { return t.ai != nil }

func (t telegramServices) AIAnalyses(limit int) []ai.AnalysisResult {
	if t.ai == nil {
		return nil
	}
	return t.ai.Analyses(limit)
}

func (t telegramServices) AIRecommendations(status string) []ai.Recommendation {
	if t.ai == nil {
		return nil
	}
	return t.ai.Recommendations(status)
}

func (t telegramServices) AIApprove(id, actor string) (int64, error) {
	if t.ai == nil {
		return 0, ai.ErrRecommendationNotFound
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Allowlisted Telegram identities act as OPERATOR; the same
	// per-section RBAC as the web path applies, so risk.* approvals
	// stay ADMIN-only and are refused here.
	snap, err := t.ai.Approve(ctx, id, actor, "telegram", api.SectionAuthorizer(auth.RoleOperator))
	if err != nil {
		return 0, err
	}
	return snap.Version, nil
}

func (t telegramServices) AIReject(id, actor string) error {
	if t.ai == nil {
		return ai.ErrRecommendationNotFound
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return t.ai.Reject(ctx, id, actor)
}

func (t telegramServices) GenerateReport(kind string) (string, bool) {
	if t.rep == nil {
		return "", false
	}
	k := reporting.KindDaily
	if kind == "weekly" {
		k = reporting.KindWeekly
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rep, err := t.rep.Generate(ctx, k)
	if err != nil {
		// Generic echo only (audit S-017): raw errors can carry DSNs or
		// query text; the log keeps the detail.
		t.e.log.Error("telegram report generation failed", "kind", kind, "error", err)
		return "Report generation failed; details are in the server log.", true
	}
	return reporting.Digest(rep), true
}

func (t telegramServices) PaperPause(string) bool {
	pe := t.e.Paper()
	if pe == nil {
		return false
	}
	pe.Pause()
	return true
}

func (t telegramServices) PaperResume(string) bool {
	pe := t.e.Paper()
	if pe == nil {
		return false
	}
	pe.Resume()
	return true
}

// startAssets returns the starting assets actually in effect for the
// current (or most recently completed) run. This must track the live
// settings document, not the boot-time env (D5): after a settings-driven
// restart that changes starting assets, reports, the read model, the
// Telegram /balance-style commands, and the AI context would otherwise
// keep showing the stale env-configured set forever.
//
// e.starts is populated only while a run has completed bootstrap (E1
// nils it at the top of every Run, including every restart, and it
// never gets set at all in a profile that constructs an Engine purely
// for the read model without ever calling Run, e.g. ProfileAPI). Fall
// back to the settings document itself in that window rather than
// returning an empty list — currentSettings() already backstops to
// platform.Seed(e.cfg) when no version has been applied yet, so this
// still prefers the live document over env once one exists.
func (e *Engine) startAssets() []exchange.Asset {
	if s := e.currentStarts(); len(s) > 0 {
		return s
	}
	venue := e.currentSettings().Venues[string(binance.ID)]
	out := make([]exchange.Asset, 0, len(venue.StartingAssets))
	for _, a := range venue.StartingAssets {
		out = append(out, exchange.Asset(a))
	}
	return out
}
