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

// PerpKey identifies one venue's perpetual contract: (venue, base,
// quote). A (venue, base) pair is NOT a contract identity — Binance
// USDⓈ-M lists a USDT-margined and a USDC-margined perpetual on the
// same base (AAVEUSDT / AAVEUSDC in the recorded 2026-08-27
// exchangeInfo), and keying on (venue, base) alone let whichever
// contract the venue listed last overwrite the other's mark, book and
// funding fields. Every contract this suite collects is a linear
// perpetual, so the quote (= margin) asset completes the key; a
// delivery/quarterly contract type would need a further component and
// is filtered out by every collector.
type PerpKey struct {
	Venue Venue
	Base  string
	Quote string
}

// Book is the concurrency-safe latest-quote store: one Quote per
// (base, quote, venue) and one Perp per (venue, base, quote), always the
// most recently observed value (a poller overwrites in place — the book
// never keeps history; lifetime tracking is a separate, explicit concern
// in spreads.go). Safe for concurrent readers and writers.
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
// (venue, base, quote). Two contracts on the same base with different
// quote/margin assets are two entries; the poller decides which of them
// the suite tracks (venue.Poller: settings.perp_quote_preference).
func (b *Book) SetPerp(p Perp) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.perps[PerpKey{Venue: p.Venue, Base: p.Base, Quote: p.Quote}] = p
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
// by (venue, base, quote) for deterministic iteration.
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
		if out[i].Base != out[j].Base {
			return out[i].Base < out[j].Base
		}
		return out[i].Quote < out[j].Quote
	})
	return out
}

// PerpFor returns one venue's perp snapshot for the (base, quote)
// contract, if observed. Callers always know the quote: a lane, a paper
// position and a signal all carry the contract's quote asset, so there
// is no "the perp for this base" lookup that could pick the wrong
// contract.
func (b *Book) PerpFor(venue Venue, base, quote string) (Perp, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	p, ok := b.perps[PerpKey{Venue: venue, Base: base, Quote: quote}]
	return p, ok
}

// EvictOlderThan removes quotes and perps last observed strictly before
// cutoff (audit X7). The book is otherwise overwrite-only, so a delisted
// pair or a venue that went offline kept its last quote forever —
// feeding the guard's median, pairs_tracked and the lane cap with data
// no poll will ever refresh. The poller sweeps with cutoff = now −
// 3 × poll interval: nothing a live venue republishes every interval
// can age that far, and an offline venue's artefacts are gone after
// three missed polls. Empty per-pair venue maps are dropped with their
// last quote so Pairs() and pairs_tracked tell the truth too.
func (b *Book) EvictOlderThan(cutoff time.Time) (quotes, perps int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for key, byVenue := range b.quotes {
		for v, q := range byVenue {
			if q.At.Before(cutoff) {
				delete(byVenue, v)
				quotes++
			}
		}
		if len(byVenue) == 0 {
			delete(b.quotes, key)
		}
	}
	for k, p := range b.perps {
		if p.At.Before(cutoff) {
			delete(b.perps, k)
			perps++
		}
	}
	return quotes, perps
}

// Age returns how long ago t was observed, relative to now. Negative
// durations (a clock skew or a future timestamp slipping through a
// collector) are returned as-is — callers decide how to treat them
// rather than this helper silently clamping to zero.
func Age(t, now time.Time) time.Duration {
	return now.Sub(t)
}

// DataAgeOK is the shared freshness gate (strategy-models §1.1) for a
// two-leg lane: each leg observed no longer than maxAge ago, and the two
// observations within maxAge/2 of each other, so a fresh leg is never
// compared against a stale one. Negative ages (a leg stamped in the
// future) fail. One definition serves the spreads ranking, the alert
// evaluator and any caller that must agree with them on what "stale"
// means.
func DataAgeOK(ageA, ageB, maxAge time.Duration) bool {
	if ageA < 0 || ageB < 0 {
		return false
	}
	if ageA > maxAge || ageB > maxAge {
		return false
	}
	diff := ageA - ageB
	if diff < 0 {
		diff = -diff
	}
	return diff <= maxAge/2
}
