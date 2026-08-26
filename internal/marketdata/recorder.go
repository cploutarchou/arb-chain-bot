package marketdata

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

// Recorder consumes feed taps through a bounded queue and writes rotated
// segment files. Overflow drops frames (counted) rather than stalling the
// feed; closed segments surface through OnSegment for metadata
// registration (market_recording_metadata).
type Recorder struct {
	Dir            string // recordings/<session>/
	SessionID      string
	Log            *slog.Logger
	RotateFrames   int64 // rotate after N frames (default 200k)
	QueueSize      int
	OnSegment      func(SegmentMeta) // called after each successful close
	StreamOfSymbol map[exchange.Symbol]uint16

	ch      chan Frame
	dropped atomic.Int64
	written atomic.Int64
}

func (r *Recorder) init() {
	if r.RotateFrames <= 0 {
		r.RotateFrames = 200_000
	}
	if r.QueueSize <= 0 {
		r.QueueSize = 8192
	}
	if r.ch == nil {
		r.ch = make(chan Frame, r.QueueSize)
	}
}

func (r *Recorder) Name() string { return "recorder" }

// Streams returns the stream table for replay metadata.
func (r *Recorder) Streams() map[uint16]exchange.Symbol {
	out := make(map[uint16]exchange.Symbol, len(r.StreamOfSymbol))
	for sym, id := range r.StreamOfSymbol {
		out[id] = sym
	}
	return out
}

// TapWS is the Feed.RawTap adapter (WS frames carry stream 0: the symbol
// is inside the payload envelope).
func (r *Recorder) TapWS(frame []byte, recv time.Time) {
	r.init()
	// Copy: the feed reuses/releases its buffer after the tap returns.
	p := make([]byte, len(frame))
	copy(p, frame)
	r.enqueue(Frame{Recv: recv, Dir: DirWS, StreamID: 0, Payload: p})
}

// TapSnapshot is the Feed.SnapTap adapter; the stream id encodes the
// symbol for replay.
func (r *Recorder) TapSnapshot(symbol exchange.Symbol, body []byte, recv time.Time) {
	r.init()
	id, ok := r.StreamOfSymbol[symbol]
	if !ok {
		return
	}
	p := make([]byte, len(body))
	copy(p, body)
	r.enqueue(Frame{Recv: recv, Dir: DirREST, StreamID: id, Payload: p})
}

func (r *Recorder) enqueue(fr Frame) {
	select {
	case r.ch <- fr:
	default:
		r.dropped.Add(1)
	}
}

// Dropped / Written counters (metrics; a growing drop count is an alert).
func (r *Recorder) Dropped() int64 { return r.dropped.Load() }
func (r *Recorder) Written() int64 { return r.written.Load() }

// Run drains the queue into rotated segments until ctx cancels, closing
// the active segment on the way out.
func (r *Recorder) Run(ctx context.Context) error {
	r.init()
	var (
		w   *SegmentWriter
		seq int
	)
	openNext := func() error {
		seq++
		path := filepath.Join(r.Dir, r.SessionID, fmt.Sprintf("depth-%06d.seg.zst", seq))
		nw, err := NewSegmentWriter(path)
		if err != nil {
			return err
		}
		w = nw
		return nil
	}
	closeCurrent := func() {
		if w == nil {
			return
		}
		meta, err := w.Close()
		w = nil
		if err != nil {
			r.Log.Error("recorder segment close failed", "error", err)
			return
		}
		r.Log.Info("recording segment closed",
			"path", meta.Path, "frames", meta.Frames, "bytes", meta.Bytes)
		if r.OnSegment != nil {
			r.OnSegment(meta)
		}
	}
	defer closeCurrent()

	for {
		select {
		case <-ctx.Done():
			// Drain what is already queued before closing.
			for {
				select {
				case fr := <-r.ch:
					if w == nil {
						if err := openNext(); err != nil {
							return err
						}
					}
					if err := w.Append(fr); err != nil {
						r.Log.Error("recorder append failed during drain", "error", err)
						return ctx.Err()
					}
					r.written.Add(1)
				default:
					return ctx.Err()
				}
			}
		case fr := <-r.ch:
			if w == nil {
				if err := openNext(); err != nil {
					return err
				}
			}
			if err := w.Append(fr); err != nil {
				r.Log.Error("recorder append failed; rotating", "error", err)
				closeCurrent()
				continue
			}
			r.written.Add(1)
			if w.Frames() >= r.RotateFrames {
				closeCurrent()
			}
		}
	}
}
