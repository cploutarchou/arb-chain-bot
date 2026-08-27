package alerts

import (
	"context"
	"crypto/rand"
	"log/slog"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/notification"
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Notifier is the notification.Service seam (Notify never blocks).
type Notifier func(notification.Event)

// OpenHook is called once per opened event, synchronously inside Tick,
// so the paper executor sees the same book the signal was computed
// from (strategy-models §2.6 step 3 → 4).
type OpenHook func(ctx context.Context, s Signal, ev screener.Event)

// Evaluator runs every enabled rule over the book each poll.
type Evaluator struct {
	svc    *screener.Service
	notify Notifier
	log    *slog.Logger
	idGen  func() string
	hooks  []OpenHook

	mu     sync.Mutex
	lanes  map[laneID]*laneState
	states map[string]Signal // last signal per lane id (diagnostics)
}

type laneID struct {
	RuleID string
	Lane   Lane
}

type laneState struct {
	firstSeen time.Time
	openEvent *screener.Event
	peak      decimal.Decimal
	lastOpen  time.Time
	lastSig   Signal
}

// New builds an evaluator over the service's book, rules, events,
// funding history and settings. notify may be nil (no Telegram).
func New(svc *screener.Service, notify Notifier, log *slog.Logger) *Evaluator {
	if log == nil {
		log = slog.Default()
	}
	return &Evaluator{
		svc: svc, notify: notify, log: log,
		idGen:  func() string { return "evt-" + ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String() },
		lanes:  map[laneID]*laneState{},
		states: map[string]Signal{},
	}
}

// OnOpen registers an executor hook.
func (e *Evaluator) OnOpen(h OpenHook) { e.hooks = append(e.hooks, h) }

// Name implements screener.Ticker.
func (e *Evaluator) Name() string { return "alerts" }

func (e *Evaluator) inputs() Inputs {
	snap := e.svc.Current()
	poll := time.Duration(snap.Settings.PollIntervalS) * time.Second
	if poll <= 0 {
		poll = 5 * time.Second
	}
	return Inputs{
		Book: e.svc.Book,
		SpotFees: func(v screener.Venue) (decimal.Decimal, bool) {
			vs, ok := snap.Settings.Venues[v]
			if !ok || !vs.Enabled {
				return decimal.Decimal{}, false
			}
			return vs.SpotTakerBps, true
		},
		PerpFees: func(v screener.Venue) (decimal.Decimal, bool) {
			vs, ok := snap.Settings.Venues[v]
			if !ok || !vs.Enabled || !vs.PerpsEnabled {
				return decimal.Decimal{}, false
			}
			return vs.PerpTakerBps, true
		},
		FundingHistory: e.svc.Funding,
		PollInterval:   poll,
	}
}

// Tick implements screener.Ticker: one evaluation pass.
func (e *Evaluator) Tick(ctx context.Context, now time.Time) {
	if e.svc.Rules == nil {
		return
	}
	rules, err := e.svc.Rules.ListRules(ctx)
	if err != nil {
		e.log.Error("screener alerts: listing rules failed", "error", err)
		return
	}
	in := e.inputs()
	seen := map[laneID]bool{}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		for _, s := range ComputeSignals(ctx, in, r, now) {
			id := laneID{RuleID: r.ID, Lane: s.Lane}
			seen[id] = true
			e.observe(ctx, id, s, now)
		}
	}
	// Lanes that vanished from the book (venue offline, rule disabled or
	// deleted) close their open events with the last signal seen.
	e.mu.Lock()
	var gone []laneID
	for id := range e.lanes {
		if !seen[id] {
			gone = append(gone, id)
		}
	}
	e.mu.Unlock()
	for _, id := range gone {
		e.mu.Lock()
		st := e.lanes[id]
		e.mu.Unlock()
		if st == nil {
			continue
		}
		last := st.lastSig
		last.Active, last.Reason, last.At = false, "lane_gone", now
		e.observe(ctx, id, last, now)
		e.mu.Lock()
		if st.openEvent == nil {
			delete(e.lanes, id)
		}
		e.mu.Unlock()
	}
}

// observe updates one lane's lifetime and event state for signal s.
func (e *Evaluator) observe(ctx context.Context, id laneID, s Signal, now time.Time) {
	e.mu.Lock()
	st := e.lanes[id]
	if st == nil {
		st = &laneState{}
		e.lanes[id] = st
	}
	st.lastSig = s
	e.states[id.RuleID+"|"+id.Lane.Base+"/"+id.Lane.Quote+"|"+string(id.Lane.VenueA)+">"+string(id.Lane.VenueB)] = s

	if !s.Active {
		st.firstSeen = time.Time{}
		open := st.openEvent
		st.openEvent = nil
		peak := st.peak
		e.mu.Unlock()
		if open != nil {
			e.closeEvent(ctx, *open, s, now, peak)
		}
		return
	}
	if st.firstSeen.IsZero() || now.Before(st.firstSeen) {
		st.firstSeen = now
	}
	lifetime := int64(now.Sub(st.firstSeen) / time.Second)
	score := s.Score()
	if st.openEvent != nil {
		if score.GreaterThan(st.peak) {
			st.peak = score
		}
		e.mu.Unlock()
		return
	}
	if lifetime < s.Rule.MinLifetimeS {
		e.mu.Unlock()
		return
	}
	if !st.lastOpen.IsZero() && now.Sub(st.lastOpen) < time.Duration(s.Rule.CooldownS)*time.Second {
		e.mu.Unlock()
		return
	}
	ev := screener.Event{
		ID: e.idGen(), RuleID: s.Rule.ID, Kind: s.Rule.Kind,
		Base: s.Lane.Base, Quote: s.Lane.Quote,
		BuyVenue: s.Lane.VenueA, SellVenue: s.Lane.VenueB,
		OpenedAt: now, LifetimeS: lifetime, PeakNetBps: score.String(),
	}
	st.openEvent = &ev
	st.peak = score
	st.lastOpen = now
	e.mu.Unlock()

	if s.Rule.Telegram && e.notify != nil {
		title, body := OpenText(s, lifetime)
		e.notify(notification.Event{
			Severity: notification.SeverityInfo,
			Key:      "screener:" + s.Rule.ID + ":" + laneKey(s.Lane),
			Title:    title, Body: body, At: now,
		})
		ev.TelegramSent = true
		e.mu.Lock()
		st.openEvent.TelegramSent = true
		e.mu.Unlock()
	}
	if e.svc.Events != nil {
		if err := e.svc.Events.InsertEvent(ctx, ev); err != nil {
			e.log.Error("screener alerts: event insert failed", "rule", s.Rule.ID, "error", err)
		}
	}
	e.log.Info("screener alert opened", "rule", s.Rule.ID, "base", s.Lane.Base, "quote", s.Lane.Quote,
		"venue_a", s.Lane.VenueA, "venue_b", s.Lane.VenueB, "score_bps", score.StringFixed(2), "lifetime_s", lifetime)
	for _, h := range e.hooks {
		h(ctx, s, ev)
	}
}

func (e *Evaluator) closeEvent(ctx context.Context, ev screener.Event, s Signal, now time.Time, peak decimal.Decimal) {
	lifetime := int64(now.Sub(ev.OpenedAt)/time.Second) + ev.LifetimeS
	if closer, ok := e.svc.Events.(screener.EventCloser); ok && e.svc.Events != nil {
		if err := closer.CloseEvent(ctx, ev.ID, now, lifetime, peak.String()); err != nil {
			e.log.Error("screener alerts: event close failed", "event", ev.ID, "error", err)
		}
	}
	if s.Rule.Telegram && e.notify != nil {
		title, body := CloseText(s, lifetime, peak)
		e.notify(notification.Event{
			Severity: notification.SeverityInfo,
			Key:      "screener:" + s.Rule.ID + ":" + laneKey(s.Lane) + ":closed",
			Title:    title, Body: body, At: now,
		})
	}
	e.log.Info("screener alert closed", "rule", ev.RuleID, "event", ev.ID, "lifetime_s", lifetime, "peak_bps", peak.StringFixed(2))
}

func laneKey(l Lane) string {
	return l.Base + "/" + l.Quote + ":" + string(l.VenueA) + ">" + string(l.VenueB)
}

// Snapshot returns the last signal per lane (diagnostics/tests).
func (e *Evaluator) Snapshot() map[string]Signal {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]Signal, len(e.states))
	for k, v := range e.states {
		out[k] = v
	}
	return out
}

// OpenEvents returns the currently open events (tests and executor).
func (e *Evaluator) OpenEvents() []screener.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []screener.Event
	for _, st := range e.lanes {
		if st.openEvent != nil {
			out = append(out, *st.openEvent)
		}
	}
	return out
}
