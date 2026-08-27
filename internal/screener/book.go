package screener

import (
	"sort"
	"sync"
	"time"
)

// PairKey identifies one spot pair, independent of venue.
type PairKey struct {
	Base  string
	Quote string
}

// PerpKey identifies one venue's perpetual contract for a base asset
// (design §2: "perps store keyed by (venue, base)").
type PerpKey struct {
	Venue Venue
	Base  string
}

// Book is the concurrency-safe latest-quote store: one Quote per
// (base, quote, venue) and one Perp per (venue, base), always the most
// recently observed value (a poller overwrites in place — the book never
// keeps history; lifetime tracking is a separate, explicit concern in
// spreads.go). Safe for concurrent readers and writers.
type Book struct {
	mu     sync.RWMutex
	quotes map[PairKey]map[Venue]Quote
	perps  map[PerpKey]Perp
}

// NewBook returns an empty book.
func NewBook() *Book {
	return &Book{
		quotes: make(map[PairKey]map[Venue]Quote),
		perps:  make(map[PerpKey]Perp),
	}
}

// SetQuote records/overwrites the latest quote for its (base, quote,
// venue). Zero-value Base/Quote/Venue are refused silently is NOT done
// here — callers (collectors, tests) are expected to pass well-formed
// values; validation of venue ids lives in Settings.Validate, not the
// hot path.
func (b *Book) SetQuote(q Quote) {
	key := PairKey{Base: q.Base, Quote: q.Quote}
	b.mu.Lock()
	defer b.mu.Unlock()
	byVenue, ok := b.quotes[key]
	if !ok {
		byVenue = make(map[Venue]Quote, 1)
		b.quotes[key] = byVenue
	}
	byVenue[q.Venue] = q
}

// SetPerp records/overwrites the latest perp snapshot for its
// (venue, base).
func (b *Book) SetPerp(p Perp) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.perps[PerpKey{Venue: p.Venue, Base: p.Base}] = p
}

// QuotesFor returns a defensive copy of every venue's latest quote for
// one pair (empty map when nothing has been observed yet).
func (b *Book) QuotesFor(base, quote string) map[Venue]Quote {
	b.mu.RLock()
	defer b.mu.RUnlock()
	src := b.quotes[PairKey{Base: base, Quote: quote}]
	out := make(map[Venue]Quote, len(src))
	for v, q := range src {
		out[v] = q
	}
	return out
}

// Pairs returns every (base, quote) pair the book has at least one
// venue's quote for, sorted for deterministic iteration (golden tests,
// stable API ordering before the sort-by-net-desc pass).
func (b *Book) Pairs() []PairKey {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]PairKey, 0, len(b.quotes))
	for k, byVenue := range b.quotes {
		if len(byVenue) == 0 {
			continue
		}
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Base != out[j].Base {
			return out[i].Base < out[j].Base
		}
		return out[i].Quote < out[j].Quote
	})
	return out
}

// Perps returns a defensive copy of every perp this book holds, sorted
// by (venue, base) for deterministic iteration.
func (b *Book) Perps() []Perp {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Perp, 0, len(b.perps))
	for _, p := range b.perps {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Venue != out[j].Venue {
			return out[i].Venue < out[j].Venue
		}
		return out[i].Base < out[j].Base
	})
	return out
}

// PerpFor returns one venue's perp snapshot for base, if observed.
func (b *Book) PerpFor(venue Venue, base string) (Perp, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	p, ok := b.perps[PerpKey{Venue: venue, Base: base}]
	return p, ok
}

// Age returns how long ago t was observed, relative to now. Negative
// durations (a clock skew or a future timestamp slipping through a
// collector) are returned as-is — callers decide how to treat them
// rather than this helper silently clamping to zero.
func Age(t, now time.Time) time.Duration {
	return now.Sub(t)
}
