// Package report is the T-078 nightly report generator for the Scanner
// Suite's automatic PAPER execution (docs/design/strategy-models.md §7
// statistics, §8 production gate; docs/design/scanner-suite.md §7
// /screener/reports). Every UTC day at 00:05 — and on demand — it
// computes, per strategy and per rule, the §7 statistics table over the
// previous UTC day and cumulatively since the first ledger row, runs
// the §8 gate checklist with pass/fail and a reason per item, writes
// markdown + JSON under <recordings dir>/screener-reports/<date>/,
// stores a screener_reports row (migration 000012) and sends one
// measurement-worded Telegram summary.
//
// The numbers are the output; no adjective replaces them. Items the
// ledger cannot evidence say "no evidence yet" and FAIL — they are
// never softened into a pass. Money math is decimal end to end; the
// test statistics (Wilson, Wilcoxon, bootstrap) are decimal too, with
// the one square root done by Newton iteration on decimals.
package report

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

// Period labels.
const (
	PeriodDay        = "day"
	PeriodCumulative = "cumulative"
)

// Window is the reporting period in UTC.
type Window struct {
	Label string    `json:"label"` // day | cumulative
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Contains reports whether t is in [Start, End).
func (w Window) Contains(t time.Time) bool {
	return !t.Before(w.Start) && t.Before(w.End)
}

// Report is one stored row (screener_reports) and the API view.
type Report struct {
	ID          string            `json:"id"`
	PeriodStart time.Time         `json:"period_start"`
	PeriodEnd   time.Time         `json:"period_end"`
	Strategy    screener.Strategy `json:"strategy"`
	// RuleID is "" for the per-strategy aggregate over every rule.
	RuleID    string    `json:"rule_id"`
	Payload   Payload   `json:"payload"`
	Markdown  string    `json:"md,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Payload is the JSON document of one report.
type Payload struct {
	Window      Window            `json:"window"`
	Strategy    screener.Strategy `json:"strategy"`
	RuleID      string            `json:"rule_id,omitempty"`
	RuleName    string            `json:"rule_name,omitempty"`
	Stats       Stats             `json:"stats"`
	Gate        []GateItem        `json:"gate"`
	GatePassed  int               `json:"gate_passed"`
	GateTotal   int               `json:"gate_total"`
	GeneratedAt time.Time         `json:"generated_at"`
	// DataAgeMs is the age of the newest ledger row the report used
	// (every read model carries a data age); nil when the window holds
	// no rows.
	DataAgeMs *int64 `json:"data_age_ms"`
	// Model states what the figures are; fixed wording.
	Model string `json:"model"`
	// Notes lists what the report could NOT compute and why (regimes,
	// stress grid, fee verification) — printed in every report.
	Notes []string `json:"notes"`
	// Files are the markdown/json paths written for this report ("" when
	// no directory is configured).
	Files struct {
		Markdown string `json:"markdown,omitempty"`
		JSON     string `json:"json,omitempty"`
	} `json:"files"`
}

// Summary is the list-view row (payload and markdown omitted).
type Summary struct {
	ID          string            `json:"id"`
	PeriodStart time.Time         `json:"period_start"`
	PeriodEnd   time.Time         `json:"period_end"`
	PeriodLabel string            `json:"period_label"`
	Strategy    screener.Strategy `json:"strategy"`
	RuleID      string            `json:"rule_id"`
	N           int64             `json:"n"`
	NetPnLQuote decimal.Decimal   `json:"realised_net_pnl_quote"`
	GatePassed  int               `json:"gate_passed"`
	GateTotal   int               `json:"gate_total"`
	CreatedAt   time.Time         `json:"created_at"`
}

// Store persists screener_reports rows. The storage package implements
// it over migrations 000012/000017; MemoryStore serves tests and DB-less
// profiles. Rows belong to the organisation in ctx at insert time
// (tenancy.OrgOrPlatform); a scoped read sees only its organisation's
// rows, an unscoped one every organisation's.
type Store interface {
	InsertReport(ctx context.Context, r Report) error
	ListReports(ctx context.Context, limit int) ([]Summary, error)
	GetReport(ctx context.Context, id string) (Report, error)
}

// OrgSource lists the organisations a scheduled (unscoped) run
// iterates; tenancy.Store implements it. nil: the platform organisation
// only (tests, database-less profiles).
type OrgSource interface {
	ListOrgIDs(ctx context.Context) ([]int64, error)
}

// orgVisible mirrors the screener package's memory-store rule: an
// unscoped read sees every organisation's rows, a scoped read its own.
func orgVisible(ctx context.Context, rowOrg int64) bool {
	id, ok := tenancy.OrgFrom(ctx)
	return !ok || id == rowOrg
}

// MemoryStore is an in-process Store.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]Report
	orgs map[string]int64
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: map[string]Report{}, orgs: map[string]int64{}}
}

func (m *MemoryStore) InsertReport(ctx context.Context, r Report) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[r.ID] = cloneReport(r)
	m.orgs[r.ID] = tenancy.OrgOrPlatform(ctx)
	return nil
}

func (m *MemoryStore) ListReports(ctx context.Context, limit int) ([]Summary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Summary, 0, len(m.rows))
	for id, r := range m.rows {
		if !orgVisible(ctx, m.orgs[id]) {
			continue
		}
		out = append(out, r.Summary())
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) GetReport(ctx context.Context, id string) (Report, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok || !orgVisible(ctx, m.orgs[id]) {
		return Report{}, screener.ErrNotFound
	}
	return cloneReport(r), nil
}

// Summary projects the list row.
func (r Report) Summary() Summary {
	return Summary{
		ID: r.ID, PeriodStart: r.PeriodStart, PeriodEnd: r.PeriodEnd, PeriodLabel: r.Payload.Window.Label,
		Strategy: r.Strategy, RuleID: r.RuleID, N: r.Payload.Stats.N, NetPnLQuote: r.Payload.Stats.NetPnLQuote,
		GatePassed: r.Payload.GatePassed, GateTotal: r.Payload.GateTotal, CreatedAt: r.CreatedAt,
	}
}

func cloneReport(r Report) Report {
	raw, err := json.Marshal(r.Payload)
	if err != nil {
		return r
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return r
	}
	r.Payload = p
	return r
}
