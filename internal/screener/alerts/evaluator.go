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

// SkipAlertsPerDay is the skip reason recorded when the rule's
// organisation has used its alerts.per_day quota (packages.md §3.2).
const SkipAlertsPerDay = "ALERTS_PER_DAY"

// Close reasons the evaluator stores on Event.CloseReason in addition
// to a signal's own inactive reason ("below_min_spread", ...).
const (
	// CloseReasonLaneGone: the lane left the rule's universe — rule
	// disabled or deleted, venue disabled in settings, filters changed.
	CloseReasonLaneGone = "lane_gone"
	// CloseReasonHoldTimeout: the lane's legs stayed stale (DATA_AGE)
	// for longer than settings.alerts.stale_hold_s; the evaluator gave
	// up waiting for fresh quotes. Not a market event.
	CloseReasonHoldTimeout = "HOLD_TIMEOUT"
)

// LaneStats is the evaluator's lane accounting for the last tick plus
// its cumulative counters — the numbers behind GET /screener/status
// automation.alerts. Universe − Lanes is what the cap hid this tick.
type LaneStats struct {
	At time.Time
	// Rules is how many enabled rules were evaluated.
	Rules int
	// Universe is how many lanes the rules' filters admitted before
	// alerts.max_lanes_per_rule; Lanes how many were evaluated after it;
	// Truncated the difference, i.e. lanes that were NOT evaluated.
	Universe  int
	Lanes     int
	Truncated int
	// TruncatedTotal accumulates Truncated since start.
	TruncatedTotal int64
	// MaxLanesPerRule is the cap in force (0 = unbounded).
	MaxLanesPerRule int
	// Holding is how many lanes are on DATA_AGE hold right now;
	// HoldTimeoutCloses how many open events closed as HOLD_TIMEOUT
	// since start.
	Holding           int
	HoldTimeoutCloses int64
}

// Entitlement is the answer to "may this alert open now?" for one rule:
// Allow=false skips the alert with Reason (counted in Skipped());
// Telegram=false keeps the event but withholds the Telegram delivery
// (alerts.channels no longer includes it — a downgrade after the rule
// was created; creation itself is gated in the API). Channels is the
// same per-channel answer generalized to every channel the rule asks
// for (docs/design/packages.md §3.1 alerts.channels); Telegram is kept
// as a convenience alias for Channels["telegram"] so existing callers
// (and tests) that only ever cared about Telegram keep working.
type Entitlement struct {
	Allow    bool
	Reason   string
	Telegram bool
	Channels map[string]bool
}

// EntitlementCheck resolves the rule's organisation and consumes one
// unit of its daily alert quota when it answers Allow. nil = no gating
// (single-tenant profiles and tests).
type EntitlementCheck func(ctx context.Context, rule screener.Rule, now time.Time) Entitlement

// Evaluator runs every enabled rule over the book each poll.
type Evaluator struct {
	svc     *screener.Service
	notify  Notifier
	log     *slog.Logger
	idGen   func() string
	hooks   []OpenHook
	entitle EntitlementCheck

	// email/webhook are the T-086 optional channel sinks (nil = that
	// channel always records a "not configured" failure); dispatchSem
	// bounds concurrent async deliveries and dispatchWG lets tests wait
	// for them (WaitDispatch).
	email       EmailSink
	webhook     WebhookSink
	dispatchSem chan struct{}
	dispatchWG  sync.WaitGroup

	mu      sync.Mutex
	lanes   map[laneID]*laneState
	states  map[string]Signal // last signal per lane id (diagnostics)
	opened  map[screener.RuleKind]int64
	skipped map[string]int64 // by reason

	// staleHold is settings.alerts.stale_hold_s, refreshed every tick;
	// laneStats / truncatedTotal / holdTimeouts / lastTruncated are the
	// LaneStats bookkeeping (lastTruncated so the truncation warning is
	// logged when the count changes, not on every tick).
	staleHold      time.Duration
	laneStats      LaneStats
	truncatedTotal int64
	holdTimeouts   int64
	lastTruncated  int
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
	skipped   string // last entitlement skip reason logged for this lane
	// holdSince is the tick the lane's legs first failed the data-age
	// gate while it had a lifetime (or an open event) to preserve; zero
	// when not held. holdReason names the failed gate.
	holdSince  time.Time
	holdReason string
}

// New builds an evaluator over the service's book, rules, events,
// funding history and settings. notify may be nil (no Telegram).
func New(svc *screener.Service, notify Notifier, log *slog.Logger) *Evaluator {
	if log == nil {
		log = slog.Default()
	}
	e := &Evaluator{
		svc: svc, notify: notify, log: log,
		idGen:       func() string { return "evt-" + ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String() },
		lanes:       map[laneID]*laneState{},
		states:      map[string]Signal{},
		opened:      map[screener.RuleKind]int64{},
		skipped:     map[string]int64{},
		dispatchSem: make(chan struct{}, dispatchWorkers),
		staleHold:   screener.DefaultStaleHoldS * time.Second,
	}
	if svc != nil {
		svc.RegisterDiagnostics(e.Name(), e.diagnostics)
	}
	return e
}

// LaneStats returns the last tick's lane accounting (see LaneStats).
func (e *Evaluator) LaneStats() LaneStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.laneStats
}

// diagnostics is the screener.Service diagnostics projection of
// LaneStats for GET /screener/status (automation.alerts.*).
func (e *Evaluator) diagnostics() map[string]int64 {
	st := e.LaneStats()
	return map[string]int64{
		"rules":               int64(st.Rules),
		"universe":            int64(st.Universe),
		"lanes":               int64(st.Lanes),
		"truncated":           int64(st.Truncated),
		"truncated_total":     st.TruncatedTotal,
		"max_lanes_per_rule":  int64(st.MaxLanesPerRule),
		"holding":             int64(st.Holding),
		"hold_timeout_closes": st.HoldTimeoutCloses,
	}
}

// OnOpen registers an executor hook.
func (e *Evaluator) OnOpen(h OpenHook) { e.hooks = append(e.hooks, h) }

// SetEntitle installs the per-organisation entitlement check (T-082).
func (e *Evaluator) SetEntitle(f EntitlementCheck) { e.entitle = f }

// Opened returns cumulative opened alerts per rule kind
// (screener_alerts_total{rule_kind}).
func (e *Evaluator) Opened() map[screener.RuleKind]int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[screener.RuleKind]int64, len(e.opened))
	for k, v := range e.opened {
		out[k] = v
	}
	return out
}

// Skipped returns cumulative entitlement skips by reason.
func (e *Evaluator) Skipped() map[string]int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]int64, len(e.skipped))
	for k, v := range e.skipped {
		out[k] = v
	}
	return out
}

// Name implements screener.Ticker.
func (e *Evaluator) Name() string { return "alerts" }

func (e *Evaluator) inputs() Inputs {
	snap := e.svc.Current()
	poll := time.Duration(snap.Settings.PollIntervalS) * time.Second
	if poll <= 0 {
		poll = 5 * time.Second
	}
	e.mu.Lock()
	e.staleHold = snap.Settings.Alerts.EffectiveStaleHold()
	e.mu.Unlock()
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
		FundingHistory:        e.svc.Funding,
		PollInterval:          poll,
		MaxPlausibleSpreadBps: snap.Settings.EffectiveMaxPlausibleSpreadBps(),
		MaxLanesPerRule:       snap.Settings.Alerts.MaxLanesPerRule,
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
	stats := LaneStats{At: now, MaxLanesPerRule: in.MaxLanesPerRule}
	seen := map[laneID]bool{}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		sigs, acc := computeSignals(ctx, in, r, now)
		stats.Rules++
		stats.Universe += acc.universe
		stats.Lanes += len(sigs)
		stats.Truncated += acc.truncated
		for _, s := range sigs {
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
		last.Active, last.Reason, last.At = false, CloseReasonLaneGone, now
		e.observe(ctx, id, last, now)
		e.mu.Lock()
		if st.openEvent == nil {
			delete(e.lanes, id)
		}
		e.mu.Unlock()
	}
	e.mu.Lock()
	for _, st := range e.lanes {
		if !st.holdSince.IsZero() {
			stats.Holding++
		}
	}
	e.truncatedTotal += int64(stats.Truncated)
	stats.TruncatedTotal = e.truncatedTotal
	stats.HoldTimeoutCloses = e.holdTimeouts
	e.laneStats = stats
	changed := stats.Truncated != e.lastTruncated
	e.lastTruncated = stats.Truncated
	e.mu.Unlock()
	if changed && stats.Truncated > 0 {
		e.log.Warn("screener alerts: lane universe truncated by alerts.max_lanes_per_rule",
			"cap", stats.MaxLanesPerRule, "universe", stats.Universe, "evaluated", stats.Lanes, "truncated", stats.Truncated)
	}
}

// observe updates one lane's lifetime and event state for signal s.
//
// An inactive signal ends the lane's lifetime and closes its open event
// — except DATA_AGE. Stale legs say "no evidence this tick", not "the
// spread ended": venue polls and the evaluator tick run on separate
// timers, so a slow venue (Crypto.com averaged 10.2 s per poll at a
// 5 s interval in the 2026-08-28 soak) fails the gate on most ticks and
// used to reset every lifetime and close every event it touched, then
// notify and start a cooldown, with nothing having happened in the
// market. Such a lane is HELD instead: lifetime and open event are
// preserved, the event carries HoldReason, and only a hold longer than
// settings.alerts.stale_hold_s closes it — with reason HOLD_TIMEOUT.
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
		reason := s.Reason
		if s.Reason == ReasonDataAge && !st.firstSeen.IsZero() {
			if st.holdSince.IsZero() {
				st.holdSince, st.holdReason = now, s.Reason
				if st.openEvent != nil {
					// Only an open event's hold is logged: warming-up
					// lanes on a slow venue enter a hold every other
					// tick and are accounted for in LaneStats.Holding.
					st.openEvent.HoldReason = s.Reason
					e.log.Info("screener alert on hold: stale quotes, event kept open", "rule", s.Rule.ID,
						"event", st.openEvent.ID, "base", s.Lane.Base, "quote", s.Lane.Quote,
						"venue_a", s.Lane.VenueA, "venue_b", s.Lane.VenueB,
						"age_a_ms", s.AgeAMs, "age_b_ms", s.AgeBMs, "hold_for", e.staleHold.String())
				}
			}
			if now.Sub(st.holdSince) <= e.staleHold {
				e.mu.Unlock()
				return
			}
			reason = CloseReasonHoldTimeout
		}
		st.firstSeen = time.Time{}
		st.holdSince, st.holdReason = time.Time{}, ""
		open := st.openEvent
		st.openEvent = nil
		peak := st.peak
		if open != nil && reason == CloseReasonHoldTimeout {
			e.holdTimeouts++
		}
		e.mu.Unlock()
		if open != nil {
			e.closeEvent(ctx, *open, s, now, peak, reason)
		}
		return
	}
	if !st.holdSince.IsZero() {
		// Fresh quotes again: the hold ends, the lifetime that started
		// before it continues, and an open event stays open.
		st.holdSince, st.holdReason = time.Time{}, ""
		if st.openEvent != nil {
			st.openEvent.HoldReason = ""
		}
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
	// Entitlements (packages.md §3.2 alerts.*): the daily quota is
	// consumed here — after lifetime and cooldown — so a lane that is
	// merely warming up never spends it. A refused lane stays
	// "not opened": no cooldown is charged, and it opens as soon as the
	// quota resets (UTC day) or the package changes.
	var allowedChannels map[string]bool
	if e.entitle != nil {
		e.mu.Unlock()
		ent := e.entitle(ctx, s.Rule, now)
		e.mu.Lock()
		if !ent.Allow {
			e.skipped[ent.Reason]++
			if st.skipped != ent.Reason {
				st.skipped = ent.Reason
				e.log.Warn("screener alert skipped", "rule", s.Rule.ID, "reason", ent.Reason,
					"base", s.Lane.Base, "quote", s.Lane.Quote, "venue_a", s.Lane.VenueA, "venue_b", s.Lane.VenueB)
			}
			e.mu.Unlock()
			return
		}
		st.skipped = ""
		allowedChannels = ent.Channels
		if allowedChannels == nil {
			// Legacy callers that only ever set Telegram: fold it into
			// the generic map so planChannels sees the same answer.
			allowedChannels = map[string]bool{"telegram": ent.Telegram}
		}
	}
	e.opened[s.Rule.Kind]++
	eventID := e.idGen()
	ev := screener.Event{
		ID: eventID, RuleID: s.Rule.ID, Kind: s.Rule.Kind,
		Base: s.Lane.Base, Quote: s.Lane.Quote,
		BuyVenue: s.Lane.VenueA, SellVenue: s.Lane.VenueB,
		OpenedAt: now, LifetimeS: lifetime, PeakNetBps: score.String(),
	}
	st.openEvent = &ev
	st.peak = score
	st.lastOpen = now
	e.mu.Unlock()

	title, body := OpenText(s, lifetime)
	delivered, telegramSent, pending := e.planChannels(eventID, s.Rule, allowedChannels, title, body, "screener:"+s.Rule.ID+":"+laneKey(s.Lane), now)
	ev.Delivered = delivered
	ev.TelegramSent = telegramSent
	if telegramSent {
		e.mu.Lock()
		st.openEvent.TelegramSent = true
		e.mu.Unlock()
	}
	if e.svc.Events != nil {
		if err := e.svc.Events.InsertEvent(ctx, ev); err != nil {
			e.log.Error("screener alerts: event insert failed", "rule", s.Rule.ID, "error", err)
		}
	}
	// Async channel work only starts once the row above exists (or the
	// attempt at least happened): dispatchPending's later UPDATE must
	// never race the INSERT.
	e.dispatchPending(eventID, pending)
	e.log.Info("screener alert opened", "rule", s.Rule.ID, "base", s.Lane.Base, "quote", s.Lane.Quote,
		"venue_a", s.Lane.VenueA, "venue_b", s.Lane.VenueB, "score_bps", score.StringFixed(2), "lifetime_s", lifetime)
	for _, h := range e.hooks {
		h(ctx, s, ev)
	}
}

func (e *Evaluator) closeEvent(ctx context.Context, ev screener.Event, s Signal, now time.Time, peak decimal.Decimal, reason string) {
	lifetime := int64(now.Sub(ev.OpenedAt)/time.Second) + ev.LifetimeS
	ev.CloseReason, ev.HoldReason = reason, ""
	// A store that records the close reason is preferred; one that only
	// implements the older EventCloser still closes the row (the reason
	// then lives in the log line below only).
	var err error
	switch store := e.svc.Events.(type) {
	case screener.EventCloseReasonRecorder:
		err = store.CloseEventWithReason(ctx, ev.ID, now, lifetime, peak.String(), reason)
	case screener.EventCloser:
		err = store.CloseEvent(ctx, ev.ID, now, lifetime, peak.String())
	}
	if err != nil {
		e.log.Error("screener alerts: event close failed", "event", ev.ID, "reason", reason, "error", err)
	}
	// Close notifications are best-effort and fire-and-forget on every
	// channel the rule effectively asks for — the entitlement gate and
	// the alerts.per_day quota were already spent when the event opened;
	// closing does not spend them again and is not itself audited on
	// the event's Delivered map (that field is the OPEN alert's delivery
	// record).
	title, body := CloseText(s, lifetime, peak, reason)
	for _, ch := range s.Rule.EffectiveChannels() {
		switch ch {
		case "telegram":
			if e.notify != nil {
				e.notify(notification.Event{
					Severity: notification.SeverityInfo,
					Key:      "screener:" + s.Rule.ID + ":" + laneKey(s.Lane) + ":closed",
					Title:    title, Body: body, At: now,
				})
			}
		case "email":
			if e.email != nil {
				to, sink := s.Rule.EmailTo, e.email
				e.dispatchFireAndForget(ev.ID, ch, func(ctx context.Context) error { return sink.Send(ctx, to, title, body) })
			}
		case "webhook":
			if e.webhook != nil {
				url, secret, sink := s.Rule.WebhookURL, s.Rule.WebhookSecret, e.webhook
				payload := webhookPayload(ev.ID, s.Rule, title, body, now)
				e.dispatchFireAndForget(ev.ID, ch, func(ctx context.Context) error { return sink.Send(ctx, url, secret, payload) })
			}
		}
	}
	e.log.Info("screener alert closed", "rule", ev.RuleID, "event", ev.ID, "reason", reason, "lifetime_s", lifetime, "peak_bps", peak.StringFixed(2))
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
