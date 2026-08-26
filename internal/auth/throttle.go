package auth

import (
	"sync"
	"time"
)

// Throttle implements login throttling per key (email|ip): after
// MaxFailures within Window, the key is locked out for Lockout. Success
// resets. Bounded memory: stale entries are pruned opportunistically.
type Throttle struct {
	MaxFailures int
	Window      time.Duration
	Lockout     time.Duration

	mu      sync.Mutex
	entries map[string]*throttleEntry
}

type throttleEntry struct {
	failures    int
	windowStart time.Time
	lockedUntil time.Time
}

func NewThrottle(maxFailures int, window, lockout time.Duration) *Throttle {
	return &Throttle{
		MaxFailures: maxFailures,
		Window:      window,
		Lockout:     lockout,
		entries:     make(map[string]*throttleEntry),
	}
}

// Allow reports whether a login attempt may proceed.
func (t *Throttle) Allow(key string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(now)
	e, ok := t.entries[key]
	if !ok {
		return true
	}
	return e.lockedUntil.IsZero() || now.After(e.lockedUntil)
}

// Fail records a failed attempt.
func (t *Throttle) Fail(key string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[key]
	if !ok || now.Sub(e.windowStart) > t.Window {
		e = &throttleEntry{windowStart: now}
		t.entries[key] = e
	}
	e.failures++
	if e.failures >= t.MaxFailures {
		e.lockedUntil = now.Add(t.Lockout)
	}
}

// Reset clears a key after a successful login.
func (t *Throttle) Reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, key)
}

func (t *Throttle) pruneLocked(now time.Time) {
	if len(t.entries) < 10_000 {
		return
	}
	for k, e := range t.entries {
		if now.Sub(e.windowStart) > t.Window && (e.lockedUntil.IsZero() || now.After(e.lockedUntil)) {
			delete(t.entries, k)
		}
	}
}
