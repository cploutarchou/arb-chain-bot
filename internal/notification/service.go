// Package notification is the single alert-routing service (SKILL.md
// §58, resources/telegram.md): core packages hand it events; it applies
// severity thresholds, per-key cooldowns, dedup aggregation, and routes
// to registered channel sinks (web hub, Telegram). Core packages never
// talk to a channel directly, and Notify never blocks the caller.
package notification

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Severity orders events; routing thresholds compare on it.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityCritical
)

func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "INFO"
	case SeverityWarning:
		return "WARNING"
	case SeverityCritical:
		return "CRITICAL"
	}
	return "UNKNOWN"
}

// Event is one notification. Key identifies the logical alert for
// cooldown/dedup ("breaker:exchange:binance", "paper:cycle_failed");
// events sharing a Key within the cooldown window aggregate.
type Event struct {
	Severity Severity
	Key      string
	Title    string
	Body     string
	At       time.Time
}

// Delivery is what reaches a sink: the event plus how many duplicates
// the cooldown window suppressed since the last delivery of this key.
type Delivery struct {
	Event
	Suppressed int
}

// Sink delivers to one channel. Implementations must be fast or buffer
// internally; a slow sink loses deliveries (counted), never blocks.
type Sink interface {
	Name() string
	Deliver(d Delivery)
}

// Config tunes the router. Routes maps severity name → channel names;
// a severity with no route delivers to "web" only.
type Config struct {
	Cooldown time.Duration
	Routes   map[string][]string
}

// Service routes events. Safe for concurrent use.
type Service struct {
	log *slog.Logger
	now func() time.Time

	mu       sync.Mutex
	cfg      Config
	sinks    map[string]Sink
	lastSent map[string]time.Time // key → last delivery
	pending  map[string]int       // key → suppressed count since last delivery
	recent   []Delivery           // ring of recent deliveries (console/telegram /alerts)

	dropped atomic.Int64
}

const recentCap = 64

func NewService(log *slog.Logger, cfg Config, now func() time.Time) *Service {
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = time.Minute
	}
	if now == nil {
		now = time.Now
	}
	return &Service{
		log: log, now: now, cfg: cfg,
		sinks:    map[string]Sink{},
		lastSent: map[string]time.Time{},
		pending:  map[string]int{},
	}
}

// Register attaches a channel sink ("web", "telegram").
func (s *Service) Register(sink Sink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sinks[sink.Name()] = sink
}

// Reconfigure swaps routing config (strategy config hot swap).
func (s *Service) Reconfigure(cfg Config) {
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = time.Minute
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
}

// Notify routes one event. CRITICAL events bypass the cooldown — they
// are never dropped or aggregated away; lower severities within the
// cooldown window for their key are suppressed and counted, surfacing
// as Suppressed on the next delivery.
func (s *Service) Notify(ev Event) {
	if ev.At.IsZero() {
		ev.At = s.now()
	}
	s.mu.Lock()
	if ev.Severity < SeverityCritical {
		if last, ok := s.lastSent[ev.Key]; ok && ev.At.Sub(last) < s.cfg.Cooldown {
			s.pending[ev.Key]++
			s.mu.Unlock()
			return
		}
	}
	suppressed := s.pending[ev.Key]
	delete(s.pending, ev.Key)
	s.lastSent[ev.Key] = ev.At
	d := Delivery{Event: ev, Suppressed: suppressed}
	s.recent = append(s.recent, d)
	if len(s.recent) > recentCap {
		s.recent = s.recent[len(s.recent)-recentCap:]
	}
	channels := s.routeFor(ev.Severity)
	sinks := make([]Sink, 0, len(channels))
	for _, ch := range channels {
		if sink, ok := s.sinks[ch]; ok {
			sinks = append(sinks, sink)
		}
	}
	s.mu.Unlock()

	for _, sink := range sinks {
		sink.Deliver(d)
	}
	s.log.Info("notification routed",
		"severity", ev.Severity.String(), "key", ev.Key,
		"title", ev.Title, "suppressed", suppressed, "channels", len(sinks))
}

// Recent returns the latest deliveries, newest first.
func (s *Service) Recent(limit int) []Delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > len(s.recent) {
		limit = len(s.recent)
	}
	out := make([]Delivery, 0, limit)
	for i := len(s.recent) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.recent[i])
	}
	return out
}

// Dropped counts sink-side losses reported via CountDrop.
func (s *Service) Dropped() int64 { return s.dropped.Load() }

// CountDrop is called by sinks that had to shed a delivery.
func (s *Service) CountDrop() { s.dropped.Add(1) }

// routeFor requires s.mu held.
func (s *Service) routeFor(sev Severity) []string {
	if chans, ok := s.cfg.Routes[sev.String()]; ok && len(chans) > 0 {
		return chans
	}
	return []string{"web"}
}
