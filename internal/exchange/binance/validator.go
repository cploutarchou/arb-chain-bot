package binance

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
)

// Validator implements Binance's steady-state continuity rule
// (official spot docs, "How to manage a local order book correctly"):
//   - drop events with u <= local lastUpdateId (stale duplicates)
//   - after init, each event must satisfy U <= lastUpdateId+1
//     (normally U == previous u + 1); U > lastUpdateId+1 is a GAP
//   - deltas arriving before initialization are dropped (the Syncer
//     buffers them separately)
type Validator struct{}

var _ orderbook.SequenceValidator = Validator{}

func (Validator) Validate(m orderbook.Meta, ev orderbook.DepthEvent) orderbook.Action {
	if !m.Initialized {
		return orderbook.ActionDrop
	}
	if ev.FinalUpdateID <= m.LastUpdateID {
		return orderbook.ActionDrop
	}
	if ev.FirstUpdateID > m.LastUpdateID+1 {
		return orderbook.ActionGap
	}
	return orderbook.ActionApply
}

// ErrSnapshotBehindBuffer: the REST snapshot predates the earliest
// buffered delta — deltas covering (snapshot, firstBuffered) were never
// received, so the splice is impossible. The caller must fetch a NEWER
// snapshot (the buffer keeps growing meanwhile).
var ErrSnapshotBehindBuffer = errors.New("binance: snapshot older than buffered stream start; refetch")

// ErrBufferOverflow: the init buffer exceeded its bound before a usable
// snapshot arrived — restart the sync from scratch (safety valve against
// pathological REST latency).
var ErrBufferOverflow = errors.New("binance: init buffer overflow; restart sync")

// Syncer orchestrates the mandatory REST+buffer initialization
// (docs/research/exchanges.md §Binance): buffer deltas, splice the
// snapshot, replay the tail, then hand steady-state to Validator. The
// mutex makes OnDelta/OnSnapshot/Synced safe against each other: the
// feed applies deltas on its session goroutine while snapshot splices
// arrive from resync goroutines (audit P0: an unlocked interleaving
// could tear the buffer and mark a wrong book HEALTHY).
type Syncer struct {
	// resyncing is the feed's per-market single-flight flag.
	resyncing atomic.Bool

	mu        sync.Mutex
	book      *orderbook.Book
	validator Validator
	buffer    []orderbook.DepthEvent
	maxBuffer int
	synced    bool
}

// NewSyncer wraps a book (typically fresh or post-corruption).
func NewSyncer(book *orderbook.Book, maxBuffer int) *Syncer {
	if maxBuffer <= 0 {
		maxBuffer = 10_000
	}
	return &Syncer{book: book, maxBuffer: maxBuffer}
}

// Synced reports steady-state.
func (s *Syncer) Synced() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.synced
}

// OnDelta routes one decoded delta. Before sync: buffered (bounded).
// After sync: validated and applied; a GAP verdict corrupts the book and
// flips the syncer back to buffering for the next snapshot.
func (s *Syncer) OnDelta(ev orderbook.DepthEvent) (orderbook.Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.synced {
		action := s.book.Apply(ev, s.validator)
		if action == orderbook.ActionGap {
			s.synced = false
			s.buffer = s.buffer[:0]
			s.book.MarkSyncing()
			return action, nil
		}
		return action, nil
	}
	if len(s.buffer) >= s.maxBuffer {
		s.buffer = s.buffer[:0]
		return orderbook.ActionDrop, ErrBufferOverflow
	}
	s.buffer = append(s.buffer, ev)
	return orderbook.ActionDrop, nil
}

// OnSnapshot splices a REST snapshot against the buffer per the official
// procedure: discard buffered events with u <= lastUpdateId; the first
// remaining event must satisfy U <= lastUpdateId+1 <= u, else the
// snapshot is behind the buffered stream and a newer one is required.
func (s *Syncer) OnSnapshot(snap orderbook.DepthEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !snap.IsSnapshot {
		return fmt.Errorf("binance: OnSnapshot called with a delta")
	}
	l := snap.FinalUpdateID

	// Trim stale prefix.
	idx := 0
	for idx < len(s.buffer) && s.buffer[idx].FinalUpdateID <= l {
		idx++
	}
	tail := s.buffer[idx:]

	if len(tail) > 0 && tail[0].FirstUpdateID > l+1 {
		return ErrSnapshotBehindBuffer
	}

	s.book.ApplySnapshot(snap)
	for _, ev := range tail {
		if action := s.book.Apply(ev, s.validator); action == orderbook.ActionGap {
			// A gap inside the buffered tail: corrupted mid-splice; caller
			// restarts the whole sync.
			s.synced = false
			s.buffer = s.buffer[:0]
			s.book.MarkSyncing()
			return fmt.Errorf("binance: gap while replaying buffered tail")
		}
	}
	s.buffer = s.buffer[:0]
	s.synced = true
	return nil
}
