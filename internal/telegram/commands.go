package telegram

import (
	"fmt"
	"strings"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
)

// dispatch maps a command to (reply text, optional keyboard, required
// permission). Empty permission means any allowlisted role; empty text
// means unknown command. Every reply is plain text, and no reply ever
// contains secrets or full config payloads.
func (b *Bot) dispatch(cmd, actor string) (string, *InlineKeyboard, string) {
	switch cmd {
	case "/start", "/help":
		return helpText, nil, ""
	case "/status", "/health":
		return b.statusText(), nil, string(auth.PermViewSystem)
	case "/scanner":
		return b.scannerText(), nil, string(auth.PermViewDashboard)
	case "/triangles":
		return b.trianglesText(), nil, string(auth.PermViewDashboard)
	case "/opportunities":
		return b.opportunitiesText(), nil, string(auth.PermViewOpportunity)
	case "/paper", "/paper_status":
		text, kb := b.paperText(actor)
		return text, kb, string(auth.PermViewDashboard)
	case "/paper_pause":
		return b.runPaperControl("paper_pause", actor), nil, string(auth.PermPaperControl)
	case "/paper_resume":
		return b.runPaperControl("paper_resume", actor), nil, string(auth.PermPaperControl)
	case "/orders", "/fills":
		return "Order and fill history lives in the console (persisted per cycle); a Telegram summary lands with the reports task (T-042).", nil, string(auth.PermViewPortfolio)
	case "/balances":
		return b.balancesText(), nil, string(auth.PermViewPortfolio)
	case "/pnl", "/stats":
		return b.pnlText(), nil, string(auth.PermViewPortfolio)
	case "/exchanges", "/latency":
		return b.feedText(), nil, string(auth.PermViewSystem)
	case "/risk":
		return b.riskText(), nil, string(auth.PermViewRisk)
	case "/alerts":
		return b.alertsText(), nil, string(auth.PermViewDashboard)
	case "/ai", "/ai_recommendations":
		return "The AI advisor is not built yet (MASTER_PLAN T-036); nothing to show.", nil, string(auth.PermViewDashboard)
	case "/report", "/daily":
		return "Reports are not built yet (MASTER_PLAN T-042); nothing to show.", nil, string(auth.PermReportView)
	case "/config":
		return b.configText(), nil, string(auth.PermViewSystem)
	}
	return "", nil, ""
}

const helpText = `arbd — triangular arbitrage platform (paper trading only; live execution is permanently disabled by design).

/status /health — engine state
/scanner — evaluation counters
/triangles — active topology
/opportunities — recent qualified
/paper — paper engine + controls
/paper_pause /paper_resume
/balances /pnl /stats
/exchanges /latency — feed health
/risk — limits + breakers
/alerts — recent notifications
/config — active config version
/ai /report — future subsystems`

func (b *Bot) statusText() string {
	st := b.Services.Status()
	var sb strings.Builder
	fmt.Fprintf(&sb, "Mode: %s\nReady: %v\nTriangles: %d over %d markets\nConfig version: %d\n",
		st.Mode, st.Ready, st.Triangles, st.Markets, st.ConfigVersion)
	fmt.Fprintf(&sb, "Evaluations: %d (qualified %d, rejected %d)\n", st.Evaluations, st.Qualified, st.Rejected)
	if st.Paper != nil {
		state := "PAUSED"
		if st.Paper.Running {
			state = "RUNNING"
		}
		fmt.Fprintf(&sb, "Paper engine: %s (%d active)\n", state, st.Paper.Active)
	}
	return sb.String()
}

func (b *Bot) scannerText() string {
	st := b.Services.Status()
	return fmt.Sprintf("Scanner\nEvaluations: %d\nQualified: %d\nRejected: %d",
		st.Evaluations, st.Qualified, st.Rejected)
}

func (b *Bot) trianglesText() string {
	st := b.Services.Status()
	return fmt.Sprintf("Topology: %d triangles across %d markets. Full explorer: console Triangles page.",
		st.Triangles, st.Markets)
}

func (b *Bot) opportunitiesText() string {
	opps := b.Services.Opportunities(5)
	if len(opps) == 0 {
		return "No qualified opportunities in the recent window."
	}
	var sb strings.Builder
	sb.WriteString("Recent qualified opportunities:\n")
	for _, o := range opps {
		fmt.Fprintf(&sb, "• %s  %s bps  profit %s (input %s)  %s\n",
			o.Triangle, o.NetBps, o.Profit, o.Input, o.At.UTC().Format("15:04:05"))
	}
	return sb.String()
}

func (b *Bot) paperText(actor string) (string, *InlineKeyboard) {
	st := b.Services.Status()
	if st.Paper == nil {
		return "Paper engine is not running (mode is not PAPER).", nil
	}
	p := st.Paper
	state := "PAUSED"
	if p.Running {
		state = "RUNNING"
	}
	text := fmt.Sprintf("Paper engine: %s\nActive simulations: %d\nReceived: %d\nCompleted: %d\nFailed: %d\nSkipped: %d",
		state, p.Active, p.Received, p.Completed, p.Failed, p.Skipped)
	userID := actorID(actor)
	kb := &InlineKeyboard{Rows: [][]InlineButton{{
		{Text: "Pause", Data: b.newCallback("paper_pause", userID)},
		{Text: "Resume", Data: b.newCallback("paper_resume", userID)},
	}}}
	return text, kb
}

func (b *Bot) balancesText() string {
	bals := b.Services.Balances()
	if len(bals) == 0 {
		return "No balances (engine not ready)."
	}
	var sb strings.Builder
	sb.WriteString("Balances (virtual):\n")
	for _, v := range bals {
		fmt.Fprintf(&sb, "• %s  available %s, reserved %s\n", v.Asset, v.Available, v.Reserved)
	}
	return sb.String()
}

func (b *Bot) pnlText() string {
	rows := b.Services.PnL()
	if len(rows) == 0 {
		return "No PnL yet (paper engine idle or absent)."
	}
	var sb strings.Builder
	sb.WriteString("Session PnL (paper):\n")
	for _, r := range rows {
		fmt.Fprintf(&sb, "• %s  realized %s, fees %s, daily loss %s, drawdown %s\n",
			r.Asset, r.Realized, r.Fees, r.Loss, r.Drawdown)
	}
	return sb.String()
}

func (b *Bot) feedText() string {
	f := b.Services.Feed()
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s feed\nFrames: %d\nReconnects: %d\nREST errors: %d\nResyncs: %d\nSequence gaps: %d\n",
		f.Exchange, f.Frames, f.Reconnects, f.APIErrors, f.Resyncs, f.Gaps)
	if len(f.Books) > 0 {
		sb.WriteString("Books:\n")
		for _, bk := range f.Books {
			fmt.Fprintf(&sb, "• %s  %s (%d ms old)\n", bk.Market, bk.State, bk.AgeMS)
		}
	}
	return sb.String()
}

func (b *Bot) riskText() string {
	snap, ok := b.Services.Config()
	if !ok {
		return "Config service unavailable."
	}
	r := snap.Params.Risk
	var sb strings.Builder
	fmt.Fprintf(&sb, "Risk limits (config v%d):\n", snap.Version)
	fmt.Fprintf(&sb, "• min net edge: %s bps\n• max trade size: %s\n• max capital/triangle: %s\n• max utilization: %s\n• max daily loss: %s\n• max drawdown: %s\n",
		r.MinNetEdgeBps, r.MaxTradeSize, r.MaxCapitalPerTriangle,
		r.MaxCapitalUtilization, r.MaxDailyLoss, r.MaxDrawdown)
	brs := b.Services.Breakers()
	if len(brs) == 0 {
		sb.WriteString("Breakers: none registered.")
	} else {
		sb.WriteString("Breakers:\n")
		for _, br := range brs {
			scope := br.Scope
			if scope == "" {
				scope = "global"
			}
			fmt.Fprintf(&sb, "• %s (%s): %s\n", br.Name, scope, br.State)
		}
	}
	return sb.String()
}

func (b *Bot) alertsText() string {
	alerts := b.Services.Alerts(5)
	if len(alerts) == 0 {
		return "No recent notifications."
	}
	var sb strings.Builder
	sb.WriteString("Recent notifications:\n")
	for _, a := range alerts {
		line := fmt.Sprintf("• [%s] %s — %s", a.Severity.String(), a.Title, a.Body)
		if a.Suppressed > 0 {
			line += fmt.Sprintf(" (+%d suppressed)", a.Suppressed)
		}
		sb.WriteString(line + "\n")
	}
	return sb.String()
}

func (b *Bot) configText() string {
	snap, ok := b.Services.Config()
	if !ok {
		return "Config service unavailable."
	}
	s := snap.Params.Scanner
	return fmt.Sprintf(
		"Active config: version %d (created %s)\nScanner: min input %s, TTL %dms, depth %d, buffers %s+%s bps\nChanges go through the console (audited, versioned).",
		snap.Version, snap.CreatedAt.UTC().Format("2006-01-02 15:04:05"),
		s.MinInput, s.TTLMs, s.Depth, s.LatencyBufferBps, s.RiskBufferBps)
}

// actorID extracts the numeric ID from "telegram:<id>".
func actorID(actor string) int64 {
	var id int64
	_, _ = fmt.Sscanf(actor, "telegram:%d", &id)
	return id
}
