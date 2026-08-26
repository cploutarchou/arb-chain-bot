package marketdata

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

func TestRecorderControlStartStopRegistersSegments(t *testing.T) {
	dir := t.TempDir()
	var (
		mu   sync.Mutex
		segs []string
	)
	n := 0
	c := &RecorderControl{
		Dir:            dir,
		StreamOfSymbol: map[exchange.Symbol]uint16{"BTCUSDT": 1},
		NewSessionID:   func() string { n++; return "S" + string(rune('0'+n)) },
		OnSegment: func(id string, _ time.Time, meta SegmentMeta) {
			mu.Lock()
			segs = append(segs, id+":"+filepath.Base(meta.Path))
			mu.Unlock()
		},
	}
	if st := c.Status(); st.Running {
		t.Fatal("idle control reports running")
	}
	// Frames while idle are dropped, not queued for a future session.
	c.TapWS([]byte(`{"e":"depthUpdate"}`), time.Now())

	ctx := context.Background()
	id, err := c.Start(ctx)
	if err != nil || id != "S1" {
		t.Fatalf("start: %v %q", err, id)
	}
	if _, err := c.Start(ctx); !errors.Is(err, ErrRecorderRunning) {
		t.Fatalf("second start: %v", err)
	}
	for i := 0; i < 10; i++ {
		c.TapWS([]byte(`{"e":"depthUpdate"}`), time.Now())
	}
	c.TapSnapshot("BTCUSDT", []byte(`{"lastUpdateId":1}`), time.Now())
	c.TapSnapshot("UNKNOWN", []byte(`{}`), time.Now()) // unmapped: ignored

	deadline := time.Now().Add(2 * time.Second)
	for c.Status().Written < 11 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	st := c.Status()
	if !st.Running || st.SessionID != "S1" || st.Written != 11 || st.Dropped != 0 {
		t.Fatalf("status = %+v", st)
	}

	stopped, err := c.Stop()
	if err != nil || stopped != "S1" {
		t.Fatalf("stop: %v %q", err, stopped)
	}
	if _, err := c.Stop(); !errors.Is(err, ErrRecorderIdle) {
		t.Fatalf("second stop: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(segs) != 1 || segs[0] != "S1:depth-000001.seg.zst" {
		t.Fatalf("segments registered = %v", segs)
	}
	if st := c.Status(); st.Running || st.SessionID != "" {
		t.Fatalf("post-stop status = %+v", st)
	}

	// A new session gets a fresh id and a fresh recorder.
	id2, err := c.Start(ctx)
	if err != nil || id2 != "S2" {
		t.Fatalf("restart: %v %q", err, id2)
	}
	if _, err := c.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestRecorderControlStopsWithParentContext(t *testing.T) {
	c := &RecorderControl{
		Dir:          t.TempDir(),
		NewSessionID: func() string { return "S" },
	}
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for c.Status().Running && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if c.Status().Running {
		t.Fatal("session survived parent cancellation")
	}
	if err := c.Err(); err != nil {
		t.Fatalf("cancellation reported as fatal: %v", err)
	}
}
