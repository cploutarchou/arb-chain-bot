package report

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/notification"
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/alerts"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/paperexec"
)

// Notifier is the notification.Service seam (Notify never blocks).
type Notifier func(notification.Event)

// Generator computes and files the reports. One instance per process;
// Run is serialised (a scheduled and an on-demand run never overlap).
type Generator struct {
	Svc    *screener.Service
	Ledger paperexec.Ledger
	Store  Store // nil → reports are written to disk / returned only
	Notify Notifier
	// Dir is the recordings directory; reports go to
	// <Dir>/screener-reports/<YYYY-MM-DD>/. "" → no files.
	Dir   string
	Log   *slog.Logger
	IDGen func() string
	Now   func() time.Time
	// Seed for the bootstrap (reproducible; default 1).
	Seed int64

	mu      sync.Mutex
	lastRun *RunResult
	seq     int64 // fallback id counter when IDGen is nil
}

// RunResult is what a run produced.
type RunResult struct {
	Day       string    `json:"day"` // the UTC day reported (previous day)
	StartedAt time.Time `json:"started_at"`
	Duration  int64     `json:"duration_ms"`
	Reports   []Summary `json:"reports"`
	Errors    []string  `json:"errors,omitempty"`
	Dir       string    `json:"dir,omitempty"`
}

func (g *Generator) log() *slog.Logger {
	if g.Log == nil {
		return slog.Default()
	}
	return g.Log
}

func (g *Generator) now() time.Time {
	if g.Now != nil {
		return g.Now().UTC()
	}
	return time.Now().UTC()
}

// LastRun returns the most recent run's result (nil before any run).
func (g *Generator) LastRun() *RunResult {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.lastRun == nil {
		return nil
	}
	cp := *g.lastRun
	return &cp
}

// Run generates every report for the UTC day BEFORE now (the nightly
// contract: at 00:05 the previous day is complete) plus the cumulative
// window, for each strategy and each rule, files them and sends one
// Telegram summary. Errors on one report are collected, not fatal.
func (g *Generator) Run(ctx context.Context, now time.Time) (RunResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now = now.UTC()
	started := g.now()
	dayEnd := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	dayStart := dayEnd.Add(-24 * time.Hour)
	res := RunResult{Day: dayStart.Format("2006-01-02"), StartedAt: started}

	rules, err := g.rules(ctx)
	if err != nil {
		return res, err
	}
	positions, err := g.Ledger.ListPositions(ctx, "", "", 0)
	if err != nil {
		return res, err
	}
	executions, err := g.Ledger.ListExecutions(ctx, "", 0)
	if err != nil {
		return res, err
	}
	var events []screener.Event
	if g.Svc.Events != nil {
		// ListEvents is capped at 500 by the store: lifetime figures are
		// over that window; the notes say so.
		events, _ = g.Svc.Events.ListEvents(ctx, "", 500)
	}

	// Scope set: every strategy with a rule or a ledger row, and every
	// rule with rows or auto_paper enabled.
	type scope struct {
		strategy screener.Strategy
		ruleID   string
	}
	scopes := map[scope]bool{}
	ruleByID := map[string]screener.Rule{}
	for _, r := range rules {
		ruleByID[r.ID] = r
		if r.AutoPaper {
			scopes[scope{r.EffectiveStrategy(), ""}] = true
			scopes[scope{r.EffectiveStrategy(), r.ID}] = true
		}
	}
	for _, p := range positions {
		scopes[scope{p.Strategy, ""}] = true
		scopes[scope{p.Strategy, p.RuleID}] = true
	}
	for _, e := range executions {
		scopes[scope{e.Strategy, ""}] = true
		scopes[scope{e.Strategy, e.RuleID}] = true
	}
	ordered := make([]scope, 0, len(scopes))
	for s := range scopes {
		ordered = append(ordered, s)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].strategy != ordered[j].strategy {
			return ordered[i].strategy < ordered[j].strategy
		}
		return ordered[i].ruleID < ordered[j].ruleID
	})

	var earliest time.Time
	for _, p := range positions {
		if earliest.IsZero() || p.OpenedAt.Before(earliest) {
			earliest = p.OpenedAt
		}
	}
	for _, e := range executions {
		if earliest.IsZero() || e.At.Before(earliest) {
			earliest = e.At
		}
	}
	if earliest.IsZero() {
		earliest = dayStart
	}
	windows := []Window{
		{Label: PeriodDay, Start: dayStart, End: dayEnd},
		{Label: PeriodCumulative, Start: earliest.UTC().Truncate(24 * time.Hour), End: now},
	}

	outDir := ""
	if g.Dir != "" {
		outDir = filepath.Join(g.Dir, "screener-reports", res.Day)
		if err := os.MkdirAll(outDir, 0o700); err != nil {
			res.Errors = append(res.Errors, "mkdir: "+err.Error())
			outDir = ""
		}
		res.Dir = outDir
	}
	capital := g.capital()
	snap := g.Svc.Current()
	spotFee := func(v screener.Venue) (decimal.Decimal, bool) {
		vs, ok := snap.Settings.Venues[v]
		if !ok {
			return decimal.Decimal{}, false
		}
		return vs.SpotTakerBps, true
	}

	var summaries []Summary
	for _, sc := range ordered {
		var inScope []screener.Rule
		for _, r := range rules {
			if r.EffectiveStrategy() == sc.strategy && (sc.ruleID == "" || r.ID == sc.ruleID) {
				inScope = append(inScope, r)
			}
		}
		for _, w := range windows {
			in := Inputs{Strategy: sc.strategy, Rules: inScope, Window: w, Capital: capital, Mid: g.mid, SpotFee: spotFee, Now: now, Seed: g.seed()}
			for _, p := range positions {
				if p.Strategy == sc.strategy && (sc.ruleID == "" || p.RuleID == sc.ruleID) {
					in.Positions = append(in.Positions, p)
				}
			}
			for _, e := range executions {
				if e.Strategy == sc.strategy && (sc.ruleID == "" || e.RuleID == sc.ruleID) {
					in.Executions = append(in.Executions, e)
				}
			}
			for _, e := range events {
				if sc.ruleID == "" || e.RuleID == sc.ruleID {
					if r, ok := ruleByID[e.RuleID]; ok && r.EffectiveStrategy() == sc.strategy {
						in.Events = append(in.Events, e)
					}
				}
			}
			rep := g.build(in, sc.ruleID, ruleByID[sc.ruleID].Name, now, len(events) >= 500)
			if outDir != "" {
				name := string(sc.strategy)
				if sc.ruleID != "" {
					name += "__" + safeName(sc.ruleID)
				}
				name += "__" + w.Label
				mdPath := filepath.Join(outDir, name+".md")
				jsonPath := filepath.Join(outDir, name+".json")
				rep.Payload.Files.Markdown, rep.Payload.Files.JSON = mdPath, jsonPath
				rep.Markdown = Markdown(rep.Payload)
				doc, _ := json.MarshalIndent(rep.Payload, "", "  ")
				if err := os.WriteFile(jsonPath, doc, 0o600); err != nil {
					res.Errors = append(res.Errors, jsonPath+": "+err.Error())
				}
				if err := os.WriteFile(mdPath, []byte(rep.Markdown), 0o600); err != nil {
					res.Errors = append(res.Errors, mdPath+": "+err.Error())
				}
			} else {
				rep.Markdown = Markdown(rep.Payload)
			}
			if g.Store != nil {
				if err := g.Store.InsertReport(ctx, rep); err != nil {
					res.Errors = append(res.Errors, rep.ID+": "+err.Error())
				}
			}
			summaries = append(summaries, rep.Summary())
		}
	}
	res.Reports = summaries
	res.Duration = g.now().Sub(started).Milliseconds()
	g.notifySummary(res, summaries, now)
	cp := res
	g.lastRun = &cp
	g.log().Info("screener reports generated", "day", res.Day, "reports", len(summaries), "errors", len(res.Errors), "dir", outDir)
	return res, nil
}

func (g *Generator) build(in Inputs, ruleID, ruleName string, now time.Time, eventsCapped bool) Report {
	st := Compute(in)
	gate := Checklist(in.Strategy, st)
	p := Payload{
		Window: in.Window, Strategy: in.Strategy, RuleID: ruleID, RuleName: ruleName,
		Stats: st, Gate: gate, GatePassed: Passed(gate), GateTotal: len(gate),
		GeneratedAt: now, Model: Model,
	}
	if st.LastSampleAt != nil {
		age := now.Sub(*st.LastSampleAt).Milliseconds()
		p.DataAgeMs = &age
	}
	p.Notes = []string{
		"Regimes (calm / volatile per strategy-models §7) are not computed: the screener stores no BTC/USDT 1-minute mids, so rv24 cannot be derived; weekend days are counted from the calendar only.",
		"max_drawdown is on realised samples in time order; open positions and unmatched inventory are not marked into the curve.",
		"Wilcoxon uses the normal approximation without tie correction; the bootstrap resamples whole UTC days (10 000 draws, fixed seed).",
		"The §8 stress grid (item 5), fee verification (item 7) and the non-statistical items (8) are not evidenced by this report and FAIL until filed manually.",
	}
	if in.Window.Label == PeriodDay {
		p.Notes = append(p.Notes, "Daily window: the §8 gate is over ≥ 30 consecutive days; every duration/sample item fails here by construction — see the cumulative report.")
	}
	if eventsCapped {
		p.Notes = append(p.Notes, "Spot lifetime figures use the newest 500 alert events only (store cap).")
	}
	if in.SpotFee == nil {
		p.Notes = append(p.Notes, "No fee lookup wired: the §2.3 rebalancing charge is 0.")
	}
	return Report{
		ID: g.id(), PeriodStart: in.Window.Start, PeriodEnd: in.Window.End, Strategy: in.Strategy, RuleID: ruleID,
		Payload: p, CreatedAt: now,
	}
}

// notifySummary sends ONE Telegram message per run: one line per
// (strategy, cumulative) with the measurements and the gate count.
func (g *Generator) notifySummary(res RunResult, summaries []Summary, now time.Time) {
	if g.Notify == nil {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Paper report for %s UTC (previous day) and cumulative.\n", res.Day)
	for _, s := range summaries {
		if s.RuleID != "" {
			continue
		}
		fmt.Fprintf(&b, "%s %s: n=%d, net %s quote, gate %d/%d pass\n", s.Strategy, s.PeriodLabel, s.N, s.NetPnLQuote.StringFixed(4), s.GatePassed, s.GateTotal)
	}
	if len(summaries) == 0 {
		b.WriteString("No strategy has rules or ledger rows yet: nothing measured.\n")
	}
	if len(res.Errors) > 0 {
		fmt.Fprintf(&b, "%d error(s) while filing; see logs.\n", len(res.Errors))
	}
	b.WriteString("\n" + alerts.Footer)
	g.Notify(notification.Event{
		Severity: notification.SeverityInfo,
		Key:      "screener:report:" + res.Day,
		Title:    "Screener paper report " + res.Day,
		Body:     b.String(),
		At:       now,
	})
}

func (g *Generator) rules(ctx context.Context) ([]screener.Rule, error) {
	if g.Svc.Rules == nil {
		return nil, nil
	}
	return g.Svc.Rules.ListRules(ctx)
}

// capital sums the settings' paper balances in quote assets (USDT,
// USDC, FDUSD, USD and their ":perp" wallets) across venues.
func (g *Generator) capital() decimal.Decimal {
	total := decimal.Zero
	for _, bal := range g.Svc.Current().Settings.Paper.Balances {
		for asset, amt := range bal {
			base := strings.TrimSuffix(asset, paperexec.PerpWalletSuffix)
			switch base {
			case "USDT", "USDC", "FDUSD", "USD":
				total = total.Add(amt)
			}
		}
	}
	return total
}

func (g *Generator) mid(v screener.Venue, base, quote string) (decimal.Decimal, int64, bool) {
	q, ok := g.Svc.Book.QuotesFor(base, quote)[v]
	if !ok || !q.Bid.IsPositive() || !q.Ask.IsPositive() {
		return decimal.Decimal{}, 0, false
	}
	return q.Bid.Add(q.Ask).Div(decTwo), g.now().Sub(q.At).Milliseconds(), true
}

// id returns a report id: the wired ULID generator, or a
// timestamp+sequence fallback that stays unique under a fixed clock.
func (g *Generator) id() string {
	if g.IDGen != nil {
		return "rep-" + g.IDGen()
	}
	g.seq++
	return fmt.Sprintf("rep-%s-%d", g.now().Format("20060102T150405"), g.seq)
}

func (g *Generator) seed() int64 {
	if g.Seed == 0 {
		return 1
	}
	return g.Seed
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
