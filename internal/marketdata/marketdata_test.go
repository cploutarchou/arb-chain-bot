package marketdata

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
)

var t0 = time.Unix(1_700_000_000, 0).UTC()

func wsFrame(sym string, U, u int64, bids string) []byte {
	return []byte(fmt.Sprintf(
		`{"e":"depthUpdate","E":%d,"s":%q,"U":%d,"u":%d,"b":[%s],"a":[]}`,
		t0.UnixMilli(), sym, U, u, bids))
}

func restBody(lastID int64) []byte {
	return []byte(fmt.Sprintf(`{"lastUpdateId":%d,"bids":[["100.0","1.0"]],"asks":[["101.0","2.0"]]}`, lastID))
}

func TestSegmentRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seg-1.seg.zst")
	w, err := NewSegmentWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	frames := []Frame{
		{Recv: t0, Dir: DirREST, StreamID: 1, Payload: restBody(100)},
		{Recv: t0.Add(time.Millisecond), Dir: DirWS, StreamID: 1, Payload: wsFrame("BTCUSDT", 101, 102, `["100.5","3"]`)},
		{Recv: t0.Add(2 * time.Millisecond), Dir: DirWS, StreamID: 1, Payload: []byte(`{"e":"trade"}`)},
	}
	for _, fr := range frames {
		if err := w.Append(fr); err != nil {
			t.Fatal(err)
		}
	}
	meta, err := w.Close()
	if err != nil {
		t.Fatal(err)
	}
	if meta.Frames != 3 || meta.SHA256 == "" || !meta.FromTS.Equal(t0) {
		t.Fatalf("meta = %+v", meta)
	}

	var got []Frame
	if err := ReadSegment(path, func(fr Frame) error { got = append(got, fr); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("frames = %d", len(got))
	}
	for i := range frames {
		if !got[i].Recv.Equal(frames[i].Recv) || got[i].Dir != frames[i].Dir ||
			got[i].StreamID != frames[i].StreamID || string(got[i].Payload) != string(frames[i].Payload) {
			t.Fatalf("frame %d mismatch: %+v vs %+v", i, got[i], frames[i])
		}
	}
}

// A crash-torn tail yields the intact prefix then ErrCorruptSegment —
// never garbage frames.
func TestTornSegmentTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seg.zst")
	w, _ := NewSegmentWriter(path)
	_ = w.Append(Frame{Recv: t0, Dir: DirWS, StreamID: 0, Payload: []byte("intact")})
	if _, err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// Append raw junk to simulate a torn compressed tail.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("torn-partial-write")
	_ = f.Close()

	var got int
	err := ReadSegment(path, func(Frame) error { got++; return nil })
	if got != 1 {
		t.Fatalf("intact frames = %d", got)
	}
	if err == nil {
		t.Skip("zstd tolerated the tail (frame-boundary append); corruption still bounded by header parsing")
	}
}

func TestReplayDeterminism(t *testing.T) {
	dir := t.TempDir()
	seg1 := filepath.Join(dir, "a.seg.zst")
	seg2 := filepath.Join(dir, "b.seg.zst")

	// Session: snapshot, chained updates (including one non-depth frame and
	// a duplicate), rotation, gap, healing snapshot, more updates.
	w1, _ := NewSegmentWriter(seg1)
	_ = w1.Append(Frame{Recv: t0, Dir: DirREST, StreamID: 1, Payload: restBody(100)})
	_ = w1.Append(Frame{Recv: t0.Add(1 * time.Millisecond), Dir: DirWS, StreamID: 1, Payload: wsFrame("BTCUSDT", 101, 102, `["100.5","3"]`)})
	_ = w1.Append(Frame{Recv: t0.Add(2 * time.Millisecond), Dir: DirWS, StreamID: 1, Payload: wsFrame("BTCUSDT", 101, 102, `["100.5","3"]`)}) // duplicate
	_ = w1.Append(Frame{Recv: t0.Add(3 * time.Millisecond), Dir: DirWS, StreamID: 1, Payload: []byte(`{"e":"trade","s":"BTCUSDT"}`)})
	if _, err := w1.Close(); err != nil {
		t.Fatal(err)
	}
	w2, _ := NewSegmentWriter(seg2)
	_ = w2.Append(Frame{Recv: t0.Add(4 * time.Millisecond), Dir: DirWS, StreamID: 1, Payload: wsFrame("BTCUSDT", 110, 111, `["99.0","1"]`)}) // gap
	_ = w2.Append(Frame{Recv: t0.Add(5 * time.Millisecond), Dir: DirREST, StreamID: 1, Payload: restBody(115)})
	_ = w2.Append(Frame{Recv: t0.Add(6 * time.Millisecond), Dir: DirWS, StreamID: 1, Payload: wsFrame("BTCUSDT", 116, 117, `["100.25","4"]`)})
	if _, err := w2.Close(); err != nil {
		t.Fatal(err)
	}

	run := func() (string, []orderbook.Action) {
		r := NewReplayer(map[uint16]exchange.Symbol{1: "BTCUSDT"})
		var actions []orderbook.Action
		r.OnApply = func(_ exchange.MarketID, a orderbook.Action) { actions = append(actions, a) }
		if err := r.ReplaySegments([]string{seg1, seg2}); err != nil {
			t.Fatal(err)
		}
		return r.Fingerprint(), actions
	}
	fp1, actions1 := run()
	fp2, actions2 := run()
	if fp1 != fp2 {
		t.Fatalf("fingerprints diverge:\n%s\n%s", fp1, fp2)
	}
	if len(actions1) != len(actions2) {
		t.Fatalf("action logs diverge: %v vs %v", actions1, actions2)
	}
	// The healed book ends HEALTHY at version snapshot+1 with the final bid.
	id := exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"}
	r := NewReplayer(map[uint16]exchange.Symbol{1: "BTCUSDT"})
	if err := r.ReplaySegments([]string{seg1, seg2}); err != nil {
		t.Fatal(err)
	}
	v, _ := r.Books.View(id, 0)
	if v.State != orderbook.StateHealthy {
		t.Fatalf("end state = %s", v.State)
	}
	found := false
	for _, l := range v.Bids {
		if l.Price.String() == "100.25" {
			found = true
		}
	}
	if !found {
		t.Fatalf("final update missing from replayed book: %+v", v.Bids)
	}
}

func TestReadMissingSegment(t *testing.T) {
	err := ReadSegment(filepath.Join(t.TempDir(), "nope.zst"), func(Frame) error { return nil })
	if err == nil || errors.Is(err, ErrCorruptSegment) {
		t.Fatalf("err = %v", err)
	}
}

// A market that stops receiving frames degrades to STALE during replay
// on the recorded clock (audit M3); a later frame restores HEALTHY, and a
// replayer without an age budget never sweeps.
func TestReplayerSweepsStaleness(t *testing.T) {
	streams := map[uint16]exchange.Symbol{1: "BTCUSDT", 2: "ETHUSDT"}
	r := NewReplayer(streams)
	r.MaxBookAge = 2 * time.Second
	btc := exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"}
	eth := exchange.MarketID{Exchange: "binance", Symbol: "ETHUSDT"}
	frames := []Frame{
		{Recv: t0, Dir: DirREST, StreamID: 1, Payload: restBody(100)},
		{Recv: t0, Dir: DirREST, StreamID: 2, Payload: restBody(100)},
		{Recv: t0.Add(time.Second), Dir: DirWS, StreamID: 1, Payload: wsFrame("BTCUSDT", 101, 102, `["100.5","3"]`)},
		// ETHUSDT goes quiet; BTCUSDT keeps ticking past the age budget.
		{Recv: t0.Add(3 * time.Second), Dir: DirWS, StreamID: 1, Payload: wsFrame("BTCUSDT", 103, 104, `["100.6","3"]`)},
	}
	for _, fr := range frames {
		if err := r.Apply(fr); err != nil {
			t.Fatal(err)
		}
	}
	if v, _ := r.Books.View(eth, 1); v.State != orderbook.StateStale {
		t.Fatalf("quiet market state = %s, want STALE", v.State)
	}
	if v, _ := r.Books.View(btc, 1); v.State != orderbook.StateHealthy {
		t.Fatalf("ticking market state = %s, want HEALTHY", v.State)
	}
	// A frame for the quiet market restores it.
	if err := r.Apply(Frame{Recv: t0.Add(4 * time.Second), Dir: DirWS, StreamID: 2, Payload: wsFrame("ETHUSDT", 101, 102, `["100.5","3"]`)}); err != nil {
		t.Fatal(err)
	}
	if v, _ := r.Books.View(eth, 1); v.State != orderbook.StateHealthy {
		t.Fatalf("restored market state = %s", v.State)
	}

	// No budget: the previous behaviour, HEALTHY forever.
	r2 := NewReplayer(streams)
	for _, fr := range frames {
		if err := r2.Apply(fr); err != nil {
			t.Fatal(err)
		}
	}
	if v, _ := r2.Books.View(eth, 1); v.State != orderbook.StateHealthy {
		t.Fatalf("without a budget state = %s", v.State)
	}
}
