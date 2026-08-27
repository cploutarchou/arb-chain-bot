package campaign

import "testing"

func TestRunCountsStartAtZero(t *testing.T) {
	r := &Runner{}
	c := r.RunCounts()
	if c[StatusDone] != 0 || c[StatusFailed] != 0 || len(c) != 2 {
		t.Fatalf("RunCounts() = %v", c)
	}
	r.done.Add(2)
	r.failed.Add(1)
	if c := r.RunCounts(); c["done"] != 2 || c["failed"] != 1 {
		t.Fatalf("RunCounts() = %v", c)
	}
}
