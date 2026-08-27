package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/notification"
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// EmailSink delivers one rendered alert by e-mail
// (internal/notification.EmailSink is the production implementation; a
// fake in tests).
type EmailSink interface {
	Send(ctx context.Context, to, subject, body string) error
}

// WebhookSink posts one rendered alert to a rule's webhook_url,
// signed, with its own retry/backoff/timeout — Send may block for
// several seconds, so it is always called off the poll loop (see
// dispatchPending below).
type WebhookSink interface {
	Send(ctx context.Context, url, secret string, payload []byte) error
}

// errNotConfigured is the delivery-failure reason recorded when a rule
// asks for a channel this process has no sink for (e.g. no smtp_url in
// the vault).
var errNotConfigured = errors.New("channel not configured on this process")

// SetChannels installs the email/webhook sinks (T-086); either may be
// nil (that channel then always fails with errNotConfigured rather
// than being attempted).
func (e *Evaluator) SetChannels(email EmailSink, webhook WebhookSink) {
	e.email, e.webhook = email, webhook
}

// dispatchWorkers bounds how many email/webhook deliveries run at
// once, so a burst of opens (or a slow/backing-off endpoint) cannot
// spawn unbounded goroutines.
const dispatchWorkers = 8

// dispatchTimeout bounds one channel delivery, independent of whatever
// internal timeout/retry budget the sink itself uses — a defensive
// ceiling so a misbehaving sink cannot hold a worker slot forever.
const dispatchTimeout = 30 * time.Second

// webhookEvent is the JSON body posted to a rule's webhook_url —
// deliberately small and stable (not the internal Signal type), since
// it is the one alert shape a third party integrates against.
type webhookEvent struct {
	EventID string    `json:"event_id"`
	RuleID  string    `json:"rule_id"`
	Kind    string    `json:"kind"`
	Title   string    `json:"title"`
	Body    string    `json:"body"`
	At      time.Time `json:"at"`
}

func webhookPayload(eventID string, rule screener.Rule, title, body string, at time.Time) []byte {
	// Marshal cannot fail for this fixed, string-only shape.
	out, _ := json.Marshal(webhookEvent{EventID: eventID, RuleID: rule.ID, Kind: string(rule.Kind), Title: title, Body: body, At: at})
	return out
}

// pendingDispatch is one channel whose outcome is not yet known: send
// runs off the poll loop, after the event row exists (planChannels'
// doc comment explains why the ordering matters).
type pendingDispatch struct {
	channel string
	send    func(ctx context.Context) error
}

// planChannels computes every requested channel's outcome synchronously
// (before the event is inserted) except email/webhook, which start
// "pending" and are executed only by dispatchPending, called AFTER the
// event row exists. This ordering is deliberate and race-sensitive: if
// an async goroutine's UPDATE (SetEventDelivered) could run before the
// event's own INSERT commits, it would silently no-op against a row
// that does not exist yet. planChannels only ever touches the event's
// in-memory Delivered map; nothing here does I/O against the store.
//
// telegram is delivered synchronously here too (Notifier never blocks,
// by its own contract) so telegramSent can be folded into the same
// insert. notifyKey is the Notifier cooldown/dedup key (the caller
// builds it from the signal's lane, same as before T-086).
func (e *Evaluator) planChannels(eventID string, rule screener.Rule, allowed map[string]bool, title, body, notifyKey string, now time.Time) (delivered map[string]screener.DeliveryOutcome, telegramSent bool, pending []pendingDispatch) {
	delivered = map[string]screener.DeliveryOutcome{}
	for _, ch := range rule.EffectiveChannels() {
		if allowed != nil && !allowed[ch] {
			delivered[ch] = screener.DeliveryOutcome{Status: "skipped", Reason: "entitlement", At: now}
			continue
		}
		switch ch {
		case "telegram":
			if e.notify == nil {
				delivered[ch] = screener.DeliveryOutcome{Status: "failed", Reason: errNotConfigured.Error(), At: now}
				continue
			}
			e.notify(notification.Event{
				Severity: notification.SeverityInfo,
				Key:      notifyKey,
				Title:    title, Body: body, At: now,
			})
			telegramSent = true
			delivered[ch] = screener.DeliveryOutcome{Status: "sent", At: now}
		case "email":
			if e.email == nil {
				delivered[ch] = screener.DeliveryOutcome{Status: "failed", Reason: errNotConfigured.Error(), At: now}
				continue
			}
			delivered[ch] = screener.DeliveryOutcome{Status: "pending", At: now}
			to, sink := rule.EmailTo, e.email
			pending = append(pending, pendingDispatch{channel: ch, send: func(ctx context.Context) error {
				return sink.Send(ctx, to, title, body)
			}})
		case "webhook":
			if e.webhook == nil {
				delivered[ch] = screener.DeliveryOutcome{Status: "failed", Reason: errNotConfigured.Error(), At: now}
				continue
			}
			delivered[ch] = screener.DeliveryOutcome{Status: "pending", At: now}
			url, secret, sink := rule.WebhookURL, rule.WebhookSecret, e.webhook
			payload := webhookPayload(eventID, rule, title, body, now)
			pending = append(pending, pendingDispatch{channel: ch, send: func(ctx context.Context) error {
				return sink.Send(ctx, url, secret, payload)
			}})
		}
	}
	return delivered, telegramSent, pending
}

// recordDelivery persists one channel's outcome on an event that is
// already known to exist in the store (best effort: a store failure is
// logged, never surfaced — delivery already happened or was refused;
// only the audit-trail write failed).
func (e *Evaluator) recordDelivery(ctx context.Context, eventID, channel, status, reason string, at time.Time) {
	recorder, ok := e.svc.Events.(screener.EventDeliveryRecorder)
	if !ok || recorder == nil {
		return
	}
	outcome := screener.DeliveryOutcome{Status: status, Reason: reason, At: at}
	if err := recorder.SetEventDelivered(ctx, eventID, channel, outcome); err != nil {
		e.log.Error("screener alerts: recording delivery outcome failed", "event", eventID, "channel", channel, "error", err)
	}
}

// dispatchPending launches one bounded-worker goroutine per pending
// channel and patches its outcome via recordDelivery once it finishes.
// Callers must only call this AFTER the event row has been inserted
// (see planChannels).
func (e *Evaluator) dispatchPending(eventID string, pending []pendingDispatch) {
	for _, p := range pending {
		e.dispatchWG.Add(1)
		go func(p pendingDispatch) {
			defer e.dispatchWG.Done()
			e.dispatchSem <- struct{}{}
			defer func() { <-e.dispatchSem }()
			ctx, cancel := context.WithTimeout(context.Background(), dispatchTimeout)
			defer cancel()
			err := p.send(ctx)
			now := time.Now()
			if err != nil {
				e.log.Warn("screener alerts: channel delivery failed", "event", eventID, "channel", p.channel, "error", err)
				e.recordDelivery(ctx, eventID, p.channel, "failed", err.Error(), now)
				return
			}
			e.recordDelivery(ctx, eventID, p.channel, "sent", "", now)
		}(p)
	}
}

// dispatchFireAndForget runs fn on the same bounded worker pool as
// dispatchPending but never records an outcome — used for the CLOSE
// notification, which is best-effort and not itself part of the open
// event's audited Delivered map.
func (e *Evaluator) dispatchFireAndForget(eventID, channel string, fn func(ctx context.Context) error) {
	e.dispatchWG.Add(1)
	go func() {
		defer e.dispatchWG.Done()
		e.dispatchSem <- struct{}{}
		defer func() { <-e.dispatchSem }()
		ctx, cancel := context.WithTimeout(context.Background(), dispatchTimeout)
		defer cancel()
		if err := fn(ctx); err != nil {
			e.log.Warn("screener alerts: close-notification delivery failed", "event", eventID, "channel", channel, "error", err)
		}
	}()
}

// WaitDispatch blocks until every in-flight async channel dispatch has
// finished (tests only — production never needs to wait for these).
func (e *Evaluator) WaitDispatch() { e.dispatchWG.Wait() }
