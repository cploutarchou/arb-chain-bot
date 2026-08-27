package screener

import (
	"context"
	"testing"
)

type fakeRunner struct {
	running bool
	status  []VenueStatus
}

func (f *fakeRunner) Start(context.Context) error { f.running = true; return nil }
func (f *fakeRunner) Stop()                       { f.running = false }
func (f *fakeRunner) Running() bool               { return f.running }
func (f *fakeRunner) Status() []VenueStatus       { return f.status }

func TestCollectorStatusStates(t *testing.T) {
	svc := &Service{}
	if state, st := svc.CollectorStatus(); state != "not_started" || st != nil {
		t.Fatalf("no runner: %s %v", state, st)
	}
	if err := svc.StartCollectors(context.Background()); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRunner{status: []VenueStatus{{ID: VenueGate, Enabled: true, Online: true, SpotPairs: 3}}}
	svc.Collectors = fr
	if state, _ := svc.CollectorStatus(); state != "stopped" {
		t.Fatalf("before start: %s", state)
	}
	if err := svc.StartCollectors(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, st := svc.CollectorStatus()
	if state != "running" || len(st) != 1 || st[0].SpotPairs != 3 {
		t.Fatalf("running: %s %+v", state, st)
	}
	svc.StopCollectors()
	if state, _ := svc.CollectorStatus(); state != "stopped" {
		t.Fatalf("after stop: %s", state)
	}
}
