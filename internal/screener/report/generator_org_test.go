package report

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

type orgList []int64

func (o orgList) ListOrgIDs(context.Context) ([]int64, error) { return o, nil }

// TestGeneratorRunsPerOrganisation (database audit D2): an unscoped
// (scheduled) run covers every organisation Orgs lists, reading each
// one's own rules and filing its rows and files in its own scope; the
// result is kept per organisation; a scoped (on-demand) run covers one
// organisation only. The memory ledger is not organisation-aware, so
// both organisations see the synthetic ledger rows here; what
// separates them is the rules (tenant-only), the stored rows and the
// files.
func TestGeneratorRunsPerOrganisation(t *testing.T) {
	g, ledger, store, fn, dir := newGen(t)
	g.Orgs = orgList{tenancy.PlatformOrgID, 7}
	ctx := context.Background()
	ctx1 := tenancy.WithOrg(ctx, tenancy.PlatformOrgID)
	ctx7 := tenancy.WithOrg(ctx, 7)
	in := synthetic()
	for _, r := range in.Rules {
		if _, err := g.Svc.Rules.InsertRule(ctx7, r, "t"); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range in.Positions {
		_ = ledger.InsertPosition(ctx7, p)
	}
	for _, e := range in.Executions {
		_ = ledger.InsertExecution(ctx7, e)
	}
	for _, e := range in.Events {
		_ = g.Svc.Events.InsertEvent(ctx7, e)
	}
	now := time.Date(2026, 8, 30, 0, 5, 0, 0, time.UTC)
	res, err := g.Run(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.OrgID != 0 || len(res.Reports) != 8 || len(res.Errors) != 0 {
		t.Fatalf("merged run = %+v", res)
	}
	if l, _ := store.ListReports(ctx7, 0); len(l) != 4 {
		t.Fatalf("org 7 stored = %d", len(l))
	}
	if l, _ := store.ListReports(ctx1, 0); len(l) != 4 {
		t.Fatalf("platform stored = %d", len(l))
	}
	if l, _ := store.ListReports(ctx, 0); len(l) != 8 {
		t.Fatalf("unscoped stored = %d", len(l))
	}
	r7 := g.LastRunFor(7)
	if r7 == nil || r7.OrgID != 7 || len(r7.Reports) != 4 || !strings.HasSuffix(r7.Dir, filepath.Join("2026-08-29", "org-7")) {
		t.Fatalf("org 7 last run = %+v", r7)
	}
	r1 := g.LastRunFor(tenancy.PlatformOrgID)
	if r1 == nil || r1.OrgID != tenancy.PlatformOrgID || len(r1.Reports) != 4 || !strings.HasSuffix(r1.Dir, "2026-08-29") {
		t.Fatalf("platform last run = %+v", r1)
	}
	if g.LastRunFor(9) != nil {
		t.Fatal("unknown organisation has a last run")
	}
	// The tenant's rules are its own: its per-rule report names the
	// rule; the platform's per-rule report (from the shared memory
	// ledger rows) cannot see the rule and carries no name.
	ruleName := func(rr *RunResult, scope context.Context) string {
		for _, s := range rr.Reports {
			if s.RuleID == "r1" && s.PeriodLabel == PeriodCumulative {
				rep, err := store.GetReport(scope, s.ID)
				if err != nil {
					t.Fatal(err)
				}
				return rep.Payload.RuleName
			}
		}
		t.Fatal("no per-rule cumulative report")
		return ""
	}
	if ruleName(r7, ctx7) != "r1" || ruleName(r1, ctx1) != "" {
		t.Fatal("rule names leaked across organisations")
	}
	if _, err := store.GetReport(ctx1, r7.Reports[0].ID); err == nil {
		t.Fatal("platform scope reads a tenant report")
	}
	// Files: the platform keeps the day directory; the tenant nests
	// under org-7 inside it.
	dayDir := filepath.Join(dir, "screener-reports", "2026-08-29")
	entries, err := os.ReadDir(filepath.Join(dayDir, "org-7"))
	if err != nil || len(entries) != 8 {
		t.Fatalf("org-7 files = %d err=%v", len(entries), err)
	}
	entries, _ = os.ReadDir(dayDir)
	files := 0
	for _, e := range entries {
		if !e.IsDir() {
			files++
		}
	}
	if files != 8 {
		t.Fatalf("platform files = %d", files)
	}
	// One Telegram summary naming each organisation.
	fn.mu.Lock()
	n, body := len(fn.events), fn.events[len(fn.events)-1].Body
	fn.mu.Unlock()
	if n != 1 || !strings.Contains(body, "org 7 cross_venue_spot cumulative") || !strings.Contains(body, "org 1 cross_venue_spot cumulative") {
		t.Fatalf("notification = %d %q", n, body)
	}

	// A scoped run covers that organisation only and names none.
	res, err = g.Run(ctx7, now)
	if err != nil || res.OrgID != 7 || len(res.Reports) != 4 || !strings.HasSuffix(res.Dir, "org-7") {
		t.Fatalf("scoped run = %+v err=%v", res, err)
	}
	fn.mu.Lock()
	body = fn.events[len(fn.events)-1].Body
	fn.mu.Unlock()
	if strings.Contains(body, "org 7 ") || strings.Contains(body, "org 1 ") {
		t.Fatalf("single-organisation notification names organisations: %q", body)
	}
	if l, _ := store.ListReports(ctx1, 0); len(l) != 4 {
		t.Fatalf("scoped run touched the platform: %d", len(l))
	}
	if l, _ := store.ListReports(ctx7, 0); len(l) != 8 {
		t.Fatalf("org 7 after second run = %d", len(l))
	}
}
