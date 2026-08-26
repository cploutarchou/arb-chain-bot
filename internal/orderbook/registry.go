package orderbook

import (
	"sync"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

// Set is the registry of books plus the coalescing dirty tracker that
// feeds the evaluator pool (docs/architecture.md §5). MarkDirty is called
// on the feed goroutine after every applied update; Drain is called by the
// evaluator when signalled. The dirty set coalesces bursts by construction:
// a market appears at most once per drain regardless of update rate.
type Set struct {
	mu    sync.RWMutex
	books map[exchange.MarketID]*Book

	dirtyMu sync.Mutex
	dirty   map[exchange.MarketID]struct{}
	signal  chan struct{} // capacity 1: presence means "dirty set non-empty"
}

func NewSet() *Set {
	return &Set{
		books:  make(map[exchange.MarketID]*Book),
		dirty:  make(map[exchange.MarketID]struct{}),
		signal: make(chan struct{}, 1),
	}
}

// Add registers a book. Adding the same market twice replaces the entry
// (resubscription after resync creates a fresh book).
func (s *Set) Add(b *Book) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.books[b.ID()] = b
}

// Get returns the book for a market.
func (s *Set) Get(id exchange.MarketID) (*Book, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.books[id]
	return b, ok
}

// View returns a top-depth copy of a market's book.
func (s *Set) View(id exchange.MarketID, depth int) (View, bool) {
	b, ok := s.Get(id)
	if !ok {
		return View{}, false
	}
	return b.View(depth), true
}

// All returns the registered market IDs (order unspecified).
func (s *Set) All() []exchange.MarketID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]exchange.MarketID, 0, len(s.books))
	for id := range s.books {
		out = append(out, id)
	}
	return out
}

// MarkDirty records that a market's book changed. Non-blocking.
func (s *Set) MarkDirty(id exchange.MarketID) {
	s.dirtyMu.Lock()
	s.dirty[id] = struct{}{}
	s.dirtyMu.Unlock()
	select {
	case s.signal <- struct{}{}:
	default: // already signalled
	}
}

// Signal returns the channel that fires when the dirty set becomes
// non-empty. Receivers must Drain after a signal.
func (s *Set) Signal() <-chan struct{} { return s.signal }

// Drain atomically takes the current dirty set.
func (s *Set) Drain() []exchange.MarketID {
	s.dirtyMu.Lock()
	defer s.dirtyMu.Unlock()
	if len(s.dirty) == 0 {
		return nil
	}
	out := make([]exchange.MarketID, 0, len(s.dirty))
	for id := range s.dirty {
		out = append(out, id)
	}
	s.dirty = make(map[exchange.MarketID]struct{}, len(out))
	return out
}
