package orderbook

import (
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

// State is the book health state. Only HEALTHY books feed the scanner.
type State uint8

const (
	StateSyncing State = iota + 1
	StateHealthy
	StateStale
	StateCorrupted
	StateDisconnected
)

func (s State) String() string {
	switch s {
	case StateSyncing:
		return "SYNCING"
	case StateHealthy:
		return "HEALTHY"
	case StateStale:
		return "STALE"
	case StateCorrupted:
		return "CORRUPTED"
	case StateDisconnected:
		return "DISCONNECTED"
	default:
		return "UNKNOWN"
	}
}

// Book is one market's local L2 book. Writes come from a single feed
// goroutine; reads copy under a short RWMutex (docs/architecture.md §5).
type Book struct {
	mu sync.RWMutex

	id       exchange.MarketID
	maxDepth int // >0: truncate ladders after every update (Kraken semantics)

	bids ladder // descending by price
	asks ladder // ascending by price

	state        State
	version      uint64 // local monotonic version; increments on every applied change
	lastUpdateID int64
	initialized  bool

	lastEventTime   time.Time
	lastReceiveTime time.Time

	transitions func(from, to State, reason string) // optional observer, set once before use
}

// New creates a book in SYNCING state. maxDepth 0 means unlimited.
func New(id exchange.MarketID, maxDepth int) *Book {
	return &Book{id: id, maxDepth: maxDepth, state: StateSyncing}
}

// OnTransition registers a state-transition observer (metrics, breakers).
// Must be called before the book is shared between goroutines.
func (b *Book) OnTransition(fn func(from, to State, reason string)) { b.transitions = fn }

func (b *Book) ID() exchange.MarketID { return b.id }

// Meta returns the validator-visible state.
func (b *Book) Meta() Meta {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return Meta{State: b.state, LastUpdateID: b.lastUpdateID, Initialized: b.initialized}
}

// ApplySnapshot replaces the book contents and marks it HEALTHY — unless
// the snapshot itself fails the integrity checks (a non-positive price,
// or a crossed top of book), in which case the book is CORRUPTED and
// needs a fresh snapshot: a venue anomaly must surface as a state, never
// as a price the pricing engine trusts.
func (b *Book) ApplySnapshot(ev DepthEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if reason := invalidLevels(ev); reason != "" {
		b.initialized = false
		b.setStateLocked(StateCorrupted, reason)
		return
	}
	b.bids = b.bids.reset(ev.Bids, true)
	b.asks = b.asks.reset(ev.Asks, false)
	b.truncateLocked()
	b.lastUpdateID = ev.FinalUpdateID
	b.version++
	b.lastEventTime = ev.EventTime
	b.lastReceiveTime = ev.ReceiveTime
	if b.crossedLocked() {
		b.initialized = false
		b.setStateLocked(StateCorrupted, "crossed book")
		return
	}
	b.initialized = true
	b.setStateLocked(StateHealthy, "snapshot")
}

// invalidLevels reports a malformed level in the event ("" when clean).
// Zero and negative quantities are deletions by the venue's contract;
// a non-positive price is never meaningful.
func invalidLevels(ev DepthEvent) string {
	for _, l := range ev.Bids {
		if l.Price.Sign() <= 0 {
			return "non-positive bid price"
		}
	}
	for _, l := range ev.Asks {
		if l.Price.Sign() <= 0 {
			return "non-positive ask price"
		}
	}
	return ""
}

// crossedLocked reports a best bid at or above the best ask. Two
// decimal comparisons per applied event; the check is the whole point
// of holding a local book rather than trusting whatever arrives.
func (b *Book) crossedLocked() bool {
	return len(b.bids) > 0 && len(b.asks) > 0 && b.bids[0].Price.GreaterThanOrEqual(b.asks[0].Price)
}

// Apply routes one event through the venue validator and merges or reacts
// accordingly, returning the action taken.
func (b *Book) Apply(ev DepthEvent, v SequenceValidator) Action {
	if ev.IsSnapshot {
		b.ApplySnapshot(ev)
		return ActionReset
	}
	action := v.Validate(b.Meta(), ev)
	switch action {
	case ActionApply:
		b.applyDelta(ev)
	case ActionReset:
		b.ApplySnapshot(ev)
	case ActionGap:
		b.MarkCorrupted("sequence gap")
	case ActionDrop:
		// stale duplicate: nothing
	}
	return action
}

func (b *Book) applyDelta(ev DepthEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if reason := invalidLevels(ev); reason != "" {
		b.initialized = false
		b.setStateLocked(StateCorrupted, reason)
		return
	}
	for _, l := range ev.Bids {
		b.bids = b.bids.apply(l, true)
	}
	for _, l := range ev.Asks {
		b.asks = b.asks.apply(l, false)
	}
	b.truncateLocked()
	b.lastUpdateID = ev.FinalUpdateID
	b.version++
	b.lastEventTime = ev.EventTime
	b.lastReceiveTime = ev.ReceiveTime
	if b.crossedLocked() {
		b.initialized = false
		b.setStateLocked(StateCorrupted, "crossed book")
		return
	}
	// A fresh applied update restores STALE → HEALTHY; SYNCING stays until
	// a snapshot arrives (delta-before-init is a validator bug caught in tests).
	if b.state == StateStale || b.state == StateHealthy {
		b.setStateLocked(StateHealthy, "update")
	}
}

// MarkCorrupted flags integrity loss; the feed must resync.
func (b *Book) MarkCorrupted(reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.initialized = false
	b.setStateLocked(StateCorrupted, reason)
}

// MarkDisconnected flags transport loss.
func (b *Book) MarkDisconnected() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.initialized = false
	b.setStateLocked(StateDisconnected, "transport lost")
}

// MarkSyncing is called when a resync begins (reconnect or post-gap).
func (b *Book) MarkSyncing() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setStateLocked(StateSyncing, "resync")
}

// EvaluateStaleness transitions HEALTHY→STALE when the book has not
// received an update within maxAge. Returns the state after evaluation.
func (b *Book) EvaluateStaleness(now time.Time, maxAge time.Duration) State {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == StateHealthy && !b.lastReceiveTime.IsZero() && now.Sub(b.lastReceiveTime) > maxAge {
		b.setStateLocked(StateStale, "age exceeded")
	}
	return b.state
}

func (b *Book) setStateLocked(to State, reason string) {
	from := b.state
	if from == to {
		return
	}
	b.state = to
	if b.transitions != nil {
		b.transitions(from, to, reason)
	}
}

// View is an immutable copy of the top-K levels plus metadata. Slices are
// freshly allocated: callers may hold them across evaluations.
type View struct {
	Market      exchange.MarketID
	State       State
	Version     uint64
	Bids        []Level
	Asks        []Level
	EventTime   time.Time
	ReceiveTime time.Time
}

// Age of the view relative to now (receive-time based).
func (v View) Age(now time.Time) time.Duration {
	if v.ReceiveTime.IsZero() {
		return time.Duration(1<<62 - 1) // effectively infinite: never trusted
	}
	return now.Sub(v.ReceiveTime)
}

// BestBid / BestAsk return the top level and false when that side is empty.
func (v View) BestBid() (Level, bool) {
	if len(v.Bids) == 0 {
		return Level{}, false
	}
	return v.Bids[0], true
}

func (v View) BestAsk() (Level, bool) {
	if len(v.Asks) == 0 {
		return Level{}, false
	}
	return v.Asks[0], true
}

// View copies the top depth levels (0 = all).
func (b *Book) View(depth int) View {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return View{
		Market:      b.id,
		State:       b.state,
		Version:     b.version,
		Bids:        b.bids.copyTop(depth),
		Asks:        b.asks.copyTop(depth),
		EventTime:   b.lastEventTime,
		ReceiveTime: b.lastReceiveTime,
	}
}

func (b *Book) truncateLocked() {
	if b.maxDepth <= 0 {
		return
	}
	if len(b.bids) > b.maxDepth {
		b.bids = b.bids[:b.maxDepth]
	}
	if len(b.asks) > b.maxDepth {
		b.asks = b.asks[:b.maxDepth]
	}
}

// ladder is a price-sorted level slice (bids descending, asks ascending).
type ladder []Level

func (l ladder) reset(levels []Level, desc bool) ladder {
	out := l[:0]
	for _, lv := range levels {
		if lv.Qty.IsPositive() {
			out = append(out, lv)
		}
	}
	// One sort beats per-level insertion for snapshot sizes (≤5000 levels).
	sort.Slice(out, func(i, j int) bool {
		c := out[i].Price.Cmp(out[j].Price)
		if desc {
			return c > 0
		}
		return c < 0
	})
	return out
}

// apply merges one absolute-quantity level; zero qty deletes.
func (l ladder) apply(lv Level, desc bool) ladder {
	idx, found := l.search(lv.Price, desc)
	switch {
	case found && (lv.Qty.IsZero() || lv.Qty.IsNegative()):
		return append(l[:idx], l[idx+1:]...)
	case found:
		l[idx].Qty = lv.Qty
		return l
	case lv.Qty.IsZero() || lv.Qty.IsNegative():
		return l // deleting an absent level is a no-op (venues send these)
	default:
		l = append(l, Level{})
		copy(l[idx+1:], l[idx:])
		l[idx] = lv
		return l
	}
}

// search finds the index of price (found=true) or its insertion point.
func (l ladder) search(price decimal.Decimal, desc bool) (int, bool) {
	lo, hi := 0, len(l)
	for lo < hi {
		mid := (lo + hi) / 2
		cmp := l[mid].Price.Cmp(price)
		if desc {
			cmp = -cmp
		}
		switch {
		case cmp == 0:
			return mid, true
		case cmp < 0:
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return lo, false
}

func (l ladder) copyTop(depth int) []Level {
	n := len(l)
	if depth > 0 && depth < n {
		n = depth
	}
	out := make([]Level, n)
	copy(out, l[:n])
	return out
}
