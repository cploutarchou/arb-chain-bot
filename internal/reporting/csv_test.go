package reporting

import (
	"context"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/notification"
)

func TestReportCSVRoundTripsThroughAStandardParser(t *testing.T) {
	var events []notification.Event
	g := testGenerator(&fakeHistory{}, &events)
	rep, err := g.Generate(context.Background(), KindDaily)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := rep.CSV()
	if err != nil {
		t.Fatal(err)
	}
	rd := csv.NewReader(strings.NewReader(string(raw)))
	records, err := rd.ReadAll()
	if err != nil {
		t.Fatalf("standard csv reader rejected the output: %v", err)
	}
	if len(records) < 2 {
		t.Fatalf("expected a header row plus data rows, got %d rows", len(records))
	}
	if got := records[0]; len(got) != 3 || got[0] != "section" || got[1] != "field" || got[2] != "value" {
		t.Fatalf("header = %v", got)
	}

	// Spot-check a scalar leaf (system_health.mode) and an indexed list
	// leaf (top_triangles[0].triangle_id) are both present.
	found := map[string]string{}
	for _, row := range records[1:] {
		found[row[0]+"."+row[1]] = row[2]
	}
	if got := found["system_health.mode"]; got != "PAPER" {
		t.Fatalf("system_health.mode = %q", got)
	}
	if got := found["top_triangles[0].triangle_id"]; got != "tri-a" {
		t.Fatalf("top_triangles[0].triangle_id = %q; rows=%v", got, found)
	}
	if got := found["risk_events.breakers_open"]; got != "1" {
		t.Fatalf("risk_events.breakers_open = %q", got)
	}
}

func TestReportCSVNeutralizesFormulaPrefixedValues(t *testing.T) {
	rep := Report{
		Executive: "=cmd|'/bin/calc'!A0",
		Incidents: []Incident{{Severity: "CRITICAL", Title: "@import evil"}},
	}
	raw, err := rep.CSV()
	if err != nil {
		t.Fatal(err)
	}
	rd := csv.NewReader(strings.NewReader(string(raw)))
	records, err := rd.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range records {
		for _, cell := range row {
			if len(cell) > 0 && strings.ContainsRune("=+-@", rune(cell[0])) {
				t.Fatalf("cell %q still starts with a formula-triggering character", cell)
			}
		}
	}
}

func TestReportCSVRowsSectionColumnIsAlwaysTheTopLevelField(t *testing.T) {
	rep := Report{
		SystemHealth: SystemSection{Mode: "PAPER", Ready: true},
		Notes:        []string{"caveat one"},
	}
	rows := rep.CSVRows()
	for _, row := range rows[1:] { // skip header
		section := row[0]
		if strings.HasPrefix(section, "system_health") {
			continue
		}
		if strings.HasPrefix(section, "notes") {
			continue
		}
		// every other populated top-level field in this fixture is its
		// zero value and still produces a row (e.g. executive_summary);
		// just assert the section never contains a dot (BL-32: leading
		// section column, not a dotted path).
		if strings.Contains(section, ".") {
			t.Fatalf("section %q contains a dot; section must be the top-level field only", section)
		}
	}
}
