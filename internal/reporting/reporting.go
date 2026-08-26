// Package reporting generates the daily/weekly operational reports
// (SKILL.md §82): a detailed persisted version and a concise digest
// routed through the notification service (web + Telegram). Sections
// whose data source is absent say so instead of showing zeros as fact.
package reporting

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/notification"
)

// Kind of report.
type Kind string

const (
	KindDaily  Kind = "daily"
	KindWeekly Kind = "weekly"
)

// Report is the full §82 layout.
type Report struct {
	ID          string    `json:"id"`
	Kind        Kind      `json:"kind"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	GeneratedAt time.Time `json:"generated_at"`

	Executive string `json:"executive_summary"`

	SystemHealth   SystemSection    `json:"system_health"`
	ExchangeHealth ExchangeSection  `json:"exchange_health"`
	Scanner        ScannerSection   `json:"scanner"`
	Opportunities  OppSection       `json:"opportunities"`
	PaperCycles    CycleSection     `json:"paper_cycles"`
	PnL            []AssetSection   `json:"pnl"`
	Slippage       SlippageSection  `json:"slippage"`
	FailedCycles   []FailedCycle    `json:"failed_cycles"`
	Capital        []CapitalSection `json:"capital_utilization"`
	TopTriangles   []TriangleStat   `json:"top_triangles"`
	WorstTriangles []TriangleStat   `json:"worst_triangles"`
	RiskEvents     RiskSection      `json:"risk_events"`
	AIFindings     AISection        `json:"ai_findings"`
	Incidents      []Incident       `json:"incidents"`
	Actions        []string         `json:"recommended_actions"`

	Notes []string `json:"notes,omitempty"` // honest data-source caveats
}

type SystemSection struct {
	Mode          string `json:"mode"`
	Ready         bool   `json:"ready"`
	ConfigVersion int64  `json:"config_version"`
	ActiveAlerts  int    `json:"active_alerts"`
}

type ExchangeSection struct {
	Exchange     string `json:"exchange"`
	Frames       int64  `json:"frames"`
	Reconnects   int64  `json:"reconnects"`
	APIErrors    int64  `json:"api_errors"`
	Resyncs      int64  `json:"resyncs"`
	SeqGaps      int64  `json:"sequence_gaps"`
	BooksHealthy int    `json:"books_healthy"`
	BooksTotal   int    `json:"books_total"`
}

type ScannerSection struct {
	Evaluations int64  `json:"evaluations"`
	Qualified   int64  `json:"qualified"`
	Rejected    int64  `json:"rejected"`
	Skipped     int64  `json:"skipped"`
	Dropped     int64  `json:"dropped"`
	QualRate    string `json:"qualification_rate"`
}

type OppSection struct {
	Persisted  int    `json:"persisted"` // rows in period (DB)
	BestBps    string `json:"best_net_bps,omitempty"`
	AvgBps     string `json:"avg_net_bps,omitempty"`
	FromMemory bool   `json:"from_memory"` // true = ring only, no DB
}

type CycleSection struct {
	Total       int    `json:"total"`
	Success     int    `json:"success"`
	Failed      int    `json:"failed"`
	SuccessRate string `json:"success_rate"`
}

type AssetSection struct {
	Asset    string `json:"asset"`
	Realized string `json:"realized"`
	Fees     string `json:"fees"`
	Drawdown string `json:"drawdown"`
}

type SlippageSection struct {
	AvgBps   string `json:"avg_bps,omitempty"`
	WorstBps string `json:"worst_bps,omitempty"`
	Samples  int    `json:"samples"`
}

type FailedCycle struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
}

type CapitalSection struct {
	Asset       string `json:"asset"`
	Available   string `json:"available"`
	Reserved    string `json:"reserved"`
	Utilization string `json:"utilization"` // reserved / (available+reserved)
}

type TriangleStat struct {
	TriangleID string `json:"triangle_id"`
	Cycles     int    `json:"cycles"`
	NetPnL     string `json:"net_pnl"`
}

type RiskSection struct {
	BreakersOpen  int              `json:"breakers_open"`
	RejectReasons map[string]int64 `json:"reject_reasons,omitempty"`
}

type AISection struct {
	Available bool   `json:"available"`
	Summary   string `json:"latest_summary,omitempty"`
	Proposed  int    `json:"proposed_recommendations"`
}

type Incident struct {
	Severity string    `json:"severity"`
	Title    string    `json:"title"`
	Count    int       `json:"count"`
	LastAt   time.Time `json:"last_at"`
}

// Sources supply the sections; nil members produce honest notes.
type Sources struct {
	System    func() SystemSection
	Exchange  func() ExchangeSection
	Scanner   func() ScannerSection
	PnL       func() []AssetSection
	Capital   func() []CapitalSection
	Risk      func() RiskSection
	AI        func() AISection
	Incidents func(since time.Time) []Incident
	// History reads the persisted period aggregates (nil without DB).
	History HistorySource
}

// HistorySource is the DB-backed period view.
type HistorySource interface {
	OpportunityAggregates(ctx context.Context, from, to time.Time) (count int, bestBps, avgBps string, err error)
	CycleAggregates(ctx context.Context, from, to time.Time) (total, success, failed int, avgSlip, worstSlip string, samples int, err error)
	FailedCycles(ctx context.Context, from, to time.Time, limit int) ([]FailedCycle, error)
	TriangleLeaders(ctx context.Context, from, to time.Time, limit int) (top, worst []TriangleStat, err error)
	InsertReport(ctx context.Context, r Report) error
}

// Generator builds, persists, and announces reports.
type Generator struct {
	Sources Sources
	Notify  func(notification.Event)
	Log     *slog.Logger
	IDGen   func() string
	Now     func() time.Time
}

// Generate builds one report for the period ending now.
func (g *Generator) Generate(ctx context.Context, kind Kind) (Report, error) {
	now := g.Now().UTC()
	period := 24 * time.Hour
	if kind == KindWeekly {
		period = 7 * 24 * time.Hour
	}
	r := Report{
		ID: g.IDGen(), Kind: kind,
		PeriodStart: now.Add(-period), PeriodEnd: now, GeneratedAt: now,
	}
	if g.Sources.System != nil {
		r.SystemHealth = g.Sources.System()
	}
	if g.Sources.Exchange != nil {
		r.ExchangeHealth = g.Sources.Exchange()
	}
	if g.Sources.Scanner != nil {
		r.Scanner = g.Sources.Scanner()
		if r.Scanner.Evaluations > 0 {
			rate := decimal.NewFromInt(r.Scanner.Qualified).
				Div(decimal.NewFromInt(r.Scanner.Evaluations)).Mul(decimal.NewFromInt(100))
			r.Scanner.QualRate = rate.StringFixed(2) + "%"
		}
	}
	if g.Sources.PnL != nil {
		r.PnL = g.Sources.PnL()
	}
	if g.Sources.Capital != nil {
		r.Capital = g.Sources.Capital()
	}
	if g.Sources.Risk != nil {
		r.RiskEvents = g.Sources.Risk()
	}
	if g.Sources.AI != nil {
		r.AIFindings = g.Sources.AI()
	} else {
		r.Notes = append(r.Notes, "AI advisor not configured")
	}
	if g.Sources.Incidents != nil {
		r.Incidents = g.Sources.Incidents(r.PeriodStart)
	}

	if h := g.Sources.History; h != nil {
		if count, best, avg, err := h.OpportunityAggregates(ctx, r.PeriodStart, r.PeriodEnd); err == nil {
			r.Opportunities = OppSection{Persisted: count, BestBps: best, AvgBps: avg}
		} else {
			r.Notes = append(r.Notes, "opportunity history query failed: "+err.Error())
		}
		if total, success, failed, avgSlip, worstSlip, samples, err := h.CycleAggregates(ctx, r.PeriodStart, r.PeriodEnd); err == nil {
			r.PaperCycles = CycleSection{Total: total, Success: success, Failed: failed}
			if total > 0 {
				r.PaperCycles.SuccessRate = decimal.NewFromInt(int64(success)).
					Div(decimal.NewFromInt(int64(total))).Mul(decimal.NewFromInt(100)).StringFixed(2) + "%"
			}
			r.Slippage = SlippageSection{AvgBps: avgSlip, WorstBps: worstSlip, Samples: samples}
		} else {
			r.Notes = append(r.Notes, "cycle history query failed: "+err.Error())
		}
		if failed, err := h.FailedCycles(ctx, r.PeriodStart, r.PeriodEnd, 10); err == nil {
			r.FailedCycles = failed
		}
		if top, worst, err := h.TriangleLeaders(ctx, r.PeriodStart, r.PeriodEnd, 5); err == nil {
			r.TopTriangles, r.WorstTriangles = top, worst
		}
	} else {
		r.Opportunities.FromMemory = true
		r.Notes = append(r.Notes, "persistence disabled: period aggregates unavailable (live counters only)")
	}

	r.Actions = recommendActions(r)
	r.Executive = executive(r)

	if h := g.Sources.History; h != nil {
		if err := h.InsertReport(ctx, r); err != nil {
			g.Log.Error("report persist failed", "error", err)
			r.Notes = append(r.Notes, "report persistence failed")
		}
	}
	if g.Notify != nil {
		g.Notify(notification.Event{
			Severity: notification.SeverityInfo,
			Key:      "report:" + string(kind),
			Title:    strings.ToUpper(string(kind)[:1]) + string(kind)[1:] + " report",
			Body:     Digest(r),
		})
	}
	g.Log.Info("report generated", "report_id", r.ID, "kind", string(kind))
	return r, nil
}

// executive composes the summary line from what the report contains.
func executive(r Report) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s report %s → %s. ", r.Kind,
		r.PeriodStart.Format("Jan 02 15:04"), r.PeriodEnd.Format("Jan 02 15:04"))
	fmt.Fprintf(&sb, "Scanner: %d evaluations, %d qualified (%s). ",
		r.Scanner.Evaluations, r.Scanner.Qualified, orNA(r.Scanner.QualRate))
	if r.PaperCycles.Total > 0 {
		fmt.Fprintf(&sb, "Paper: %d cycles, %s success. ", r.PaperCycles.Total, orNA(r.PaperCycles.SuccessRate))
	}
	for _, p := range r.PnL {
		fmt.Fprintf(&sb, "%s realized %s (fees %s). ", p.Asset, p.Realized, p.Fees)
	}
	if r.RiskEvents.BreakersOpen > 0 {
		fmt.Fprintf(&sb, "%d breaker(s) OPEN. ", r.RiskEvents.BreakersOpen)
	}
	return strings.TrimSpace(sb.String())
}

// recommendActions derives rule-based next steps; no guarantees, no
// invented data.
func recommendActions(r Report) []string {
	var out []string
	if r.Scanner.Evaluations > 100 && r.Scanner.Qualified == 0 {
		out = append(out, "No opportunities qualified despite active evaluation: review min edge, fee tier, and market scope.")
	}
	if r.ExchangeHealth.Reconnects > 5 {
		out = append(out, fmt.Sprintf("%d feed reconnects in period: inspect connectivity before trusting latency-sensitive stats.", r.ExchangeHealth.Reconnects))
	}
	if r.ExchangeHealth.SeqGaps > 10 {
		out = append(out, "Frequent sequence gaps: REST budget burning on resyncs; check upstream stability.")
	}
	if r.RiskEvents.BreakersOpen > 0 {
		out = append(out, "Open circuit breakers require operator review before resuming full qualification.")
	}
	if r.PaperCycles.Failed > 0 && r.PaperCycles.Failed*2 > r.PaperCycles.Total {
		out = append(out, "More than half of paper cycles failed: inspect failed-cycle outcomes before tuning size upward.")
	}
	if len(out) == 0 {
		out = append(out, "No action required; keep observing.")
	}
	return out
}

// Digest renders the concise Telegram/web version.
func Digest(r Report) string {
	var sb strings.Builder
	sb.WriteString(r.Executive)
	if len(r.TopTriangles) > 0 {
		sb.WriteString("\nTop: ")
		parts := make([]string, 0, len(r.TopTriangles))
		for _, t := range r.TopTriangles {
			parts = append(parts, fmt.Sprintf("%s (%s)", t.TriangleID, t.NetPnL))
		}
		sb.WriteString(strings.Join(parts, ", "))
	}
	if len(r.Incidents) > 0 {
		fmt.Fprintf(&sb, "\nIncidents: %d", len(r.Incidents))
	}
	if len(r.Actions) > 0 {
		sb.WriteString("\nActions: " + r.Actions[0])
		if len(r.Actions) > 1 {
			fmt.Fprintf(&sb, " (+%d more)", len(r.Actions)-1)
		}
	}
	return sb.String()
}

func orNA(s string) string {
	if s == "" {
		return "n/a"
	}
	return s
}

// MarshalPayload renders the detailed persisted JSON.
func (r Report) MarshalPayload() (json.RawMessage, error) { return json.Marshal(r) }

// UnmarshalPayload restores a persisted report.
func (r *Report) UnmarshalPayload(raw []byte) error { return json.Unmarshal(raw, r) }

// SortTriangleStats orders by net PnL descending (helper for sources).
func SortTriangleStats(stats []TriangleStat) {
	sort.Slice(stats, func(i, j int) bool {
		a, errA := decimal.NewFromString(stats[i].NetPnL)
		b, errB := decimal.NewFromString(stats[j].NetPnL)
		if errA != nil || errB != nil {
			return stats[i].NetPnL > stats[j].NetPnL
		}
		return a.GreaterThan(b)
	})
}

// Scheduler emits daily and weekly reports.
type Scheduler struct {
	Generator *Generator
	Log       *slog.Logger
	Daily     time.Duration // defaults 24h / 7d
	Weekly    time.Duration
}

func (s *Scheduler) Name() string { return "reporting" }

func (s *Scheduler) Run(ctx context.Context) error {
	if s.Daily <= 0 {
		s.Daily = 24 * time.Hour
	}
	if s.Weekly <= 0 {
		s.Weekly = 7 * 24 * time.Hour
	}
	daily := time.NewTicker(s.Daily)
	weekly := time.NewTicker(s.Weekly)
	defer daily.Stop()
	defer weekly.Stop()
	for {
		var kind Kind
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-daily.C:
			kind = KindDaily
		case <-weekly.C:
			kind = KindWeekly
		}
		if _, err := s.Generator.Generate(ctx, kind); err != nil {
			s.Log.Error("report generation failed", "kind", string(kind), "error", err)
		}
	}
}
