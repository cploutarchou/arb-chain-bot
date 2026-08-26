package telegram

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
)

// command is one dispatchable bot command: the permission is checked by
// the caller BEFORE run executes (audit S-004 — the previous shape ran
// the handler first and only suppressed the reply, so a side-effecting
// command executed even for a role that lacked the permission). Empty
// perm means any allowlisted role.
type command struct {
	perm auth.Permission
	run  func() (string, *InlineKeyboard)
}

// dispatch maps a command name to its descriptor without executing
// anything. Every reply is plain text, and no reply ever contains
// secrets or full config payloads.
func (b *Bot) dispatch(cmd, actor string) (command, bool) {
	text := func(perm auth.Permission, f func() string) (command, bool) {
		return command{perm: perm, run: func() (string, *InlineKeyboard) { return f(), nil }}, true
	}
	withKB := func(perm auth.Permission, f func() (string, *InlineKeyboard)) (command, bool) {
		return command{perm: perm, run: f}, true
	}
	switch cmd {
	case "/start", "/help":
		return text("", func() string { return helpText })
	case "/status", "/health":
		return text(auth.PermViewSystem, b.statusText)
	case "/scanner":
		return text(auth.PermViewDashboard, b.scannerText)
	case "/triangles":
		return text(auth.PermViewDashboard, b.trianglesText)
	case "/opportunities":
		return text(auth.PermViewOpportunity, b.opportunitiesText)
	case "/paper", "/paper_status":
		return withKB(auth.PermViewDashboard, func() (string, *InlineKeyboard) { return b.paperText(actor) })
	case "/paper_pause":
		return text(auth.PermPaperControl, func() string { return b.runPaperControl("paper_pause", actor) })
	case "/paper_resume":
		return text(auth.PermPaperControl, func() string { return b.runPaperControl("paper_resume", actor) })
	case "/orders", "/fills":
		return text(auth.PermViewPortfolio, func() string {
			return "Order and fill history lives in the console (persisted per cycle); a Telegram summary lands with the reports task (T-042)."
		})
	case "/balances":
		return text(auth.PermViewPortfolio, b.balancesText)
	case "/pnl", "/stats":
		return text(auth.PermViewPortfolio, b.pnlText)
	case "/exchanges", "/latency":
		return text(auth.PermViewSystem, b.feedText)
	case "/risk":
		return text(auth.PermViewRisk, b.riskText)
	case "/alerts":
		return withKB(auth.PermViewDashboard, func() (string, *InlineKeyboard) { return b.alertsText(actor) })
	case "/ai":
		return text(auth.PermViewDashboard, b.aiText)
	case "/ai_recommendations":
		return withKB(auth.PermViewDashboard, func() (string, *InlineKeyboard) { return b.aiRecommendationsText(actor) })
	case "/report":
		return text(auth.PermReportView, func() string { return b.reportText("weekly") })
	case "/daily":
		return text(auth.PermReportView, func() string { return b.reportText("daily") })
	case "/config":
		return text(auth.PermViewSystem, b.configText)
	}
	return command{}, false
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

func (b *Bot) alertsText(actor string) (string, *InlineKeyboard) {
	alerts := b.Services.Alerts(5)
	if len(alerts) == 0 {
		return "No alerts.", nil
	}
	userID := actorID(actor)
	var sb strings.Builder
	sb.WriteString("Alerts (shared with the web console):\n")
	var rows [][]InlineButton
	for i, a := range alerts {
		line := fmt.Sprintf("%d. [%s/%s] %s — %s", i+1, a.SevName, a.State, a.Title, a.Body)
		if a.Count > 1 {
			line += fmt.Sprintf(" (×%d)", a.Count)
		}
		sb.WriteString(line + "\n")
		if a.State == "active" {
			rows = append(rows, []InlineButton{{
				Text: fmt.Sprintf("Ack %d: %s", i+1, truncate(a.Title, 24)),
				Data: b.newCallback("ack_alert:"+a.ID, userID),
			}})
		}
	}
	var kb *InlineKeyboard
	if len(rows) > 0 {
		kb = &InlineKeyboard{Rows: rows}
	}
	return sb.String(), kb
}

// truncate shortens to at most n runes without splitting a UTF-8
// sequence mid-character.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n-1]) + "…"
}

func (b *Bot) reportText(kind string) string {
	digest, ok := b.Services.GenerateReport(kind)
	if !ok {
		return "Reporting is unavailable in this profile."
	}
	return digest
}

func (b *Bot) aiText() string {
	if !b.Services.AIPresent() {
		return "AI advisor is not configured (set ANTHROPIC_API_KEY or ARB_AI_PROVIDER=fake)."
	}
	analyses := b.Services.AIAnalyses(1)
	if len(analyses) == 0 {
		return "No analyses yet; the scheduler runs hourly/daily/weekly."
	}
	a := analyses[0]
	return fmt.Sprintf("Latest analysis (%s, %s):\n%s\nFindings: %d · Recommendations: %d\nDecisions: /ai_recommendations",
		a.Kind, a.At.UTC().Format("2006-01-02 15:04"), a.Summary, len(a.Findings), len(a.Recommendations))
}

func (b *Bot) aiRecommendationsText(actor string) (string, *InlineKeyboard) {
	if !b.Services.AIPresent() {
		return "AI advisor is not configured (set ANTHROPIC_API_KEY or ARB_AI_PROVIDER=fake).", nil
	}
	recs := b.Services.AIRecommendations("proposed")
	if len(recs) == 0 {
		return "No proposed recommendations.", nil
	}
	userID := actorID(actor)
	var sb strings.Builder
	sb.WriteString("Proposed recommendations (approval changes config; audited):\n")
	var rows [][]InlineButton
	for i, r := range recs {
		fmt.Fprintf(&sb, "%d. %s: %s → %s (confidence %s)\n   %s\n",
			i+1, r.Parameter, r.CurrentValue, r.RecommendedValue, r.Confidence, r.Reason)
		rows = append(rows, []InlineButton{
			{Text: fmt.Sprintf("Approve %d", i+1), Data: b.newCallback("ai_approve:"+r.ID, userID)},
			{Text: fmt.Sprintf("Reject %d", i+1), Data: b.newCallback("ai_reject:"+r.ID, userID)},
		})
	}
	return sb.String(), &InlineKeyboard{Rows: rows}
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
