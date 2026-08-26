package notification

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// AlertState is the lifecycle (matches the alerts table CHECK).
type AlertState string

const (
	AlertActive   AlertState = "active"
	AlertAcked    AlertState = "acked"
	AlertResolved AlertState = "resolved"
)

// Alert is one lifecycle-tracked alert. Deliveries sharing a Key while
// the alert is unresolved fold into Count; a delivery after resolution
// opens a fresh alert.
type Alert struct {
	ID         string     `json:"id"`
	Severity   Severity   `json:"-"`
	SevName    string     `json:"severity"`
	Source     string     `json:"source"`
	Key        string     `json:"key"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	FirstAt    time.Time  `json:"first_at"`
	LastAt     time.Time  `json:"last_at"`
	Count      int        `json:"count"`
	State      AlertState `json:"state"`
	AckedBy    string     `json:"acked_by,omitempty"`
	AckedAt    time.Time  `json:"acked_at,omitempty"`
	ResolvedBy string     `json:"resolved_by,omitempty"`
	ResolvedAt time.Time  `json:"resolved_at,omitempty"`
}

// CenterStore persists lifecycle changes (nil disables persistence).
// Implementations must tolerate repeated upserts.
type CenterStore interface {
	UpsertAlert(ctx context.Context, a Alert) error
	UpdateAlertState(ctx context.Context, id string, state AlertState, actor string, at time.Time) error
}

// ErrAlertNotFound reports an unknown alert ID.
var ErrAlertNotFound = errors.New("notification: alert not found")

// ErrBadTransition reports an invalid lifecycle move.
var ErrBadTransition = errors.New("notification: invalid alert state transition")

// Center is the alert lifecycle service shared by web and Telegram
// (single backend state). It consumes every routed delivery via
// RegisterAlways, so channel routing never hides an alert from the
// center. In-memory state is authoritative for the session; the store
// write is asynchronous best-effort (logged on failure).
type Center struct {
	Log   *slog.Logger
	Store CenterStore
	IDGen func() string
	Now   func() time.Time
	// OnChange fires after every lifecycle change (hub publish).
	OnChange func(Alert)

	mu    sync.Mutex
	byID  map[string]*Alert
	byKey map[string]*Alert // unresolved alert per key
	order []string          // insertion order for eviction
}

// centerCap bounds memory. Eviction prefers resolved, then the oldest
// non-critical; active CRITICAL alerts are never evicted — the map
// grows past the cap rather than dropping one.
const centerCap = 1024

func (c *Center) init() {
	if c.byID == nil {
		c.byID = map[string]*Alert{}
		c.byKey = map[string]*Alert{}
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

func (c *Center) Name() string { return "center" }

// Deliver implements Sink: upsert by key.
func (c *Center) Deliver(d Delivery) {
	c.init()
	c.mu.Lock()
	if a, ok := c.byKey[d.Key]; ok && a.State != AlertResolved {
		a.Count += 1 + d.Suppressed
		a.LastAt = d.At
		// Escalation raises severity in place; it never lowers.
		if d.Severity > a.Severity {
			a.Severity = d.Severity
			a.SevName = d.Severity.String()
		}
		out := *a
		c.mu.Unlock()
		c.notifyChange(out, false)
		return
	}
	a := &Alert{
		ID: c.IDGen(), Severity: d.Severity, SevName: d.Severity.String(),
		Source: sourceOf(d.Key), Key: d.Key, Title: d.Title, Body: d.Body,
		FirstAt: d.At, LastAt: d.At, Count: 1 + d.Suppressed, State: AlertActive,
	}
	c.byID[a.ID] = a
	c.byKey[d.Key] = a
	c.order = append(c.order, a.ID)
	c.evictLocked()
	out := *a
	c.mu.Unlock()
	c.notifyChange(out, true)
}

// Ack moves an active alert to acked. actor is the platform user ID
// when known ("" for interfaces without a user mapping — the audit
// trail still carries the concrete actor).
func (c *Center) Ack(id, actor string) (Alert, error) {
	return c.transition(id, actor, AlertAcked, map[AlertState]bool{AlertActive: true})
}

// Resolve closes an active or acked alert.
func (c *Center) Resolve(id, actor string) (Alert, error) {
	return c.transition(id, actor, AlertResolved, map[AlertState]bool{AlertActive: true, AlertAcked: true})
}

func (c *Center) transition(id, actor string, to AlertState, from map[AlertState]bool) (Alert, error) {
	c.init()
	c.mu.Lock()
	a, ok := c.byID[id]
	if !ok {
		c.mu.Unlock()
		return Alert{}, ErrAlertNotFound
	}
	if !from[a.State] {
		c.mu.Unlock()
		return Alert{}, ErrBadTransition
	}
	now := c.Now().UTC()
	a.State = to
	switch to {
	case AlertAcked:
		a.AckedBy, a.AckedAt = actor, now
	case AlertResolved:
		a.ResolvedBy, a.ResolvedAt = actor, now
		if c.byKey[a.Key] == a {
			delete(c.byKey, a.Key)
		}
	}
	out := *a
	c.mu.Unlock()

	if c.Store != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.Store.UpdateAlertState(ctx, out.ID, out.State, actor, now); err != nil {
				c.Log.Error("alert state persist failed", "alert_id", out.ID, "error", err)
			}
		}()
	}
	if c.OnChange != nil {
		c.OnChange(out)
	}
	return out, nil
}

// List returns alerts newest-first; state "" lists all.
func (c *Center) List(state AlertState, limit int) []Alert {
	c.init()
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Alert, 0, 32)
	for _, a := range c.byID {
		if state != "" && a.State != state {
			continue
		}
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Get returns one alert by ID.
func (c *Center) Get(id string) (Alert, bool) {
	c.init()
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.byID[id]
	if !ok {
		return Alert{}, false
	}
	return *a, true
}

// ActiveCount reports unresolved alerts (status payloads).
func (c *Center) ActiveCount() int {
	c.init()
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, a := range c.byID {
		if a.State != AlertResolved {
			n++
		}
	}
	return n
}

func (c *Center) notifyChange(a Alert, isNew bool) {
	if c.Store != nil && isNew {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.Store.UpsertAlert(ctx, a); err != nil {
				c.Log.Error("alert persist failed", "alert_id", a.ID, "error", err)
			}
		}()
	}
	if c.OnChange != nil {
		c.OnChange(a)
	}
}

// evictLocked requires c.mu held.
func (c *Center) evictLocked() {
	if len(c.byID) <= centerCap {
		return
	}
	// Pass 1: oldest resolved. Pass 2: oldest non-critical.
	for pass := 0; pass < 2 && len(c.byID) > centerCap; pass++ {
		kept := c.order[:0]
		for _, id := range c.order {
			a, ok := c.byID[id]
			if !ok {
				continue
			}
			evictable := (pass == 0 && a.State == AlertResolved) ||
				(pass == 1 && a.Severity < SeverityCritical)
			if len(c.byID) > centerCap && evictable {
				delete(c.byID, id)
				if c.byKey[a.Key] == a {
					delete(c.byKey, a.Key)
				}
				continue
			}
			kept = append(kept, id)
		}
		c.order = kept
	}
	// Anything still over cap is active CRITICAL: retained by design.
}

func sourceOf(key string) string {
	if i := strings.IndexByte(key, ':'); i > 0 {
		return key[:i]
	}
	return "system"
}
