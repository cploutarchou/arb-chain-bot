package risk

import (
	"sync"
	"time"
)

// BreakerState per docs/risk.md §3. The safe default on any uncertainty is
// OPEN: qualification paused, nothing happens.
type BreakerState uint8

const (
	BreakerClosed BreakerState = iota + 1
	BreakerOpen
	BreakerHalfOpen
)

func (s BreakerState) String() string {
	switch s {
	case BreakerClosed:
		return "CLOSED"
	case BreakerOpen:
		return "OPEN"
	case BreakerHalfOpen:
		return "HALF_OPEN"
	default:
		return "UNKNOWN"
	}
}

// Transition is emitted to the observer on every state change.
type Transition struct {
	Name   string
	Scope  string
	From   BreakerState
	To     BreakerState
	Reason string
	At     time.Time
}

// Breaker is one named circuit breaker with an optional scope
// (""=global, or "exchange:binance", "market:...", "triangle:...").
type Breaker struct {
	name       string
	scope      string
	probeAfter time.Duration // OPEN → HALF_OPEN eligibility delay

	mu       sync.Mutex
	state    BreakerState
	openedAt time.Time
	reason   string
}

// Registry holds all breakers and answers the gate question: is anything
// relevant open?
type Registry struct {
	mu       sync.RWMutex
	breakers map[string]*Breaker // key: name|scope
	observer func(Transition)
}

func NewRegistry(observer func(Transition)) *Registry {
	if observer == nil {
		observer = func(Transition) {}
	}
	return &Registry{breakers: make(map[string]*Breaker), observer: observer}
}

func key(name, scope string) string { return name + "|" + scope }

// Register creates (or returns) a breaker. New breakers start CLOSED.
func (r *Registry) Register(name, scope string, probeAfter time.Duration) *Breaker {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := key(name, scope)
	if b, ok := r.breakers[k]; ok {
		return b
	}
	b := &Breaker{name: name, scope: scope, probeAfter: probeAfter, state: BreakerClosed}
	r.breakers[k] = b
	return b
}

// Trip opens the breaker (idempotent while open; refreshes reason).
func (r *Registry) Trip(name, scope, reason string, now time.Time) {
	b := r.Register(name, scope, 0)
	b.mu.Lock()
	from := b.state
	b.state = BreakerOpen
	b.openedAt = now
	b.reason = reason
	b.mu.Unlock()
	if from != BreakerOpen {
		r.observer(Transition{Name: name, Scope: scope, From: from, To: BreakerOpen, Reason: reason, At: now})
	}
}

// Probe moves an eligible OPEN breaker to HALF_OPEN (a single trial is
// allowed through by the caller's policy).
func (r *Registry) Probe(name, scope string, now time.Time) bool {
	r.mu.RLock()
	b, ok := r.breakers[key(name, scope)]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	b.mu.Lock()
	eligible := b.state == BreakerOpen && b.probeAfter > 0 && now.Sub(b.openedAt) >= b.probeAfter
	if eligible {
		b.state = BreakerHalfOpen
	}
	b.mu.Unlock()
	if eligible {
		r.observer(Transition{Name: name, Scope: scope, From: BreakerOpen, To: BreakerHalfOpen, At: now})
	}
	return eligible
}

// Close closes a breaker after a successful probe or explicit operator
// action.
func (r *Registry) Close(name, scope string, now time.Time) {
	r.mu.RLock()
	b, ok := r.breakers[key(name, scope)]
	r.mu.RUnlock()
	if !ok {
		return
	}
	b.mu.Lock()
	from := b.state
	b.state = BreakerClosed
	b.reason = ""
	b.mu.Unlock()
	if from != BreakerClosed {
		r.observer(Transition{Name: name, Scope: scope, From: from, To: BreakerClosed, At: now})
	}
}

// AnyOpen reports whether any breaker matching one of the scopes (or the
// global scope "") is OPEN. HALF_OPEN does not gate: the probe policy
// decides what flows during probing.
func (r *Registry) AnyOpen(scopes ...string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	match := make(map[string]struct{}, len(scopes)+1)
	match[""] = struct{}{}
	for _, s := range scopes {
		match[s] = struct{}{}
	}
	for _, b := range r.breakers {
		b.mu.Lock()
		open := b.state == BreakerOpen
		scope := b.scope
		b.mu.Unlock()
		if !open {
			continue
		}
		if _, ok := match[scope]; ok {
			return true
		}
	}
	return false
}

// State resolves one breaker's current state. ok=false when no breaker
// is registered under (name, scope) — the honest answer for an operator
// action against a name that does not exist (typo, or a policy this
// build does not register).
func (r *Registry) State(name, scope string) (BreakerState, bool) {
	r.mu.RLock()
	b, ok := r.breakers[key(name, scope)]
	r.mu.RUnlock()
	if !ok {
		return BreakerClosed, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state, true
}

// States snapshots every breaker (console Risk Center payload).
func (r *Registry) States() []Transition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Transition, 0, len(r.breakers))
	for _, b := range r.breakers {
		b.mu.Lock()
		out = append(out, Transition{Name: b.name, Scope: b.scope, To: b.state, Reason: b.reason, At: b.openedAt})
		b.mu.Unlock()
	}
	return out
}
