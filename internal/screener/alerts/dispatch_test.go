package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

type fakeEmailSink struct {
	mu    sync.Mutex
	calls []struct{ to, subject, body string }
	err   error
}

func (f *fakeEmailSink) Send(_ context.Context, to, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, struct{ to, subject, body string }{to, subject, body})
	return f.err
}

func (f *fakeEmailSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type fakeWebhookSink struct {
	mu    sync.Mutex
	calls []struct {
		url, secret string
		payload     []byte
	}
	err error
}

func (f *fakeWebhookSink) Send(_ context.Context, url, secret string, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, struct {
		url, secret string
		payload     []byte
	}{url, secret, payload})
	return f.err
}

func (f *fakeWebhookSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func channelsRule() screener.Rule {
	r := spreadRule()
	r.Telegram = false
	r.Channels = []string{"telegram", "email", "webhook"}
	r.EmailTo = "trader@example.test"
	r.WebhookURL = "https://hooks.example.test/screener"
	r.WebhookSecret = "webhook-secret-value-0123456789"
	return r
}

func openLane(t *testing.T, ev *Evaluator, svc *screener.Service) {
	t.Helper()
	ctx := context.Background()
	setSpread(svc.Book, "50250", t0)
	ev.Tick(ctx, t0)
	setSpread(svc.Book, "50260", t0.Add(10*time.Second))
	ev.Tick(ctx, t0.Add(10*time.Second)) // lifetime 10 >= min_lifetime_s: opens
}

func TestEvaluatorDispatchesEveryConfiguredChannel(t *testing.T) {
	svc := newSvc(t)
	r := channelsRule()
	if _, err := svc.Rules.InsertRule(context.Background(), r, "t"); err != nil {
		t.Fatal(err)
	}
	fn := &fakeNotifier{}
	ev := New(svc, fn.Notify, slog.New(slog.NewTextHandler(io.Discard, nil)))
	email, webhook := &fakeEmailSink{}, &fakeWebhookSink{}
	ev.SetChannels(email, webhook)

	openLane(t, ev, svc)
	ev.WaitDispatch()

	if fn.count() != 1 {
		t.Fatalf("telegram deliveries = %d, want 1", fn.count())
	}
	if email.count() != 1 {
		t.Fatalf("email deliveries = %d, want 1", email.count())
	}
	if webhook.count() != 1 {
		t.Fatalf("webhook deliveries = %d, want 1", webhook.count())
	}
	if email.calls[0].to != r.EmailTo {
		t.Fatalf("email to = %q, want %q", email.calls[0].to, r.EmailTo)
	}
	if webhook.calls[0].url != r.WebhookURL || webhook.calls[0].secret != r.WebhookSecret {
		t.Fatalf("webhook target = %+v", webhook.calls[0])
	}
	var payload struct {
		EventID string `json:"event_id"`
		RuleID  string `json:"rule_id"`
	}
	if err := json.Unmarshal(webhook.calls[0].payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.RuleID != "r1" || payload.EventID == "" {
		t.Fatalf("webhook payload = %+v", payload)
	}

	events, _ := svc.Events.ListEvents(context.Background(), "r1", 10)
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	d := events[0].Delivered
	if d["telegram"].Status != "sent" || d["email"].Status != "sent" || d["webhook"].Status != "sent" {
		t.Fatalf("delivered = %+v", d)
	}
}

func TestEvaluatorPerChannelEntitlementGating(t *testing.T) {
	svc := newSvc(t)
	r := channelsRule()
	if _, err := svc.Rules.InsertRule(context.Background(), r, "t"); err != nil {
		t.Fatal(err)
	}
	fn := &fakeNotifier{}
	ev := New(svc, fn.Notify, slog.New(slog.NewTextHandler(io.Discard, nil)))
	email, webhook := &fakeEmailSink{}, &fakeWebhookSink{}
	ev.SetChannels(email, webhook)
	// Only telegram is entitled; email/webhook are refused before ever
	// touching their sinks.
	ev.SetEntitle(func(context.Context, screener.Rule, time.Time) Entitlement {
		return Entitlement{Allow: true, Telegram: true, Channels: map[string]bool{"telegram": true, "email": false, "webhook": false}}
	})

	openLane(t, ev, svc)
	ev.WaitDispatch()

	if fn.count() != 1 {
		t.Fatalf("telegram deliveries = %d, want 1", fn.count())
	}
	if email.count() != 0 || webhook.count() != 0 {
		t.Fatalf("email/webhook must not be attempted: email=%d webhook=%d", email.count(), webhook.count())
	}
	events, _ := svc.Events.ListEvents(context.Background(), "r1", 10)
	d := events[0].Delivered
	if d["telegram"].Status != "sent" {
		t.Fatalf("telegram delivered = %+v", d["telegram"])
	}
	if d["email"].Status != "skipped" || d["email"].Reason != "entitlement" {
		t.Fatalf("email delivered = %+v", d["email"])
	}
	if d["webhook"].Status != "skipped" || d["webhook"].Reason != "entitlement" {
		t.Fatalf("webhook delivered = %+v", d["webhook"])
	}
}

func TestEvaluatorChannelFailureRecorded(t *testing.T) {
	svc := newSvc(t)
	r := channelsRule()
	if _, err := svc.Rules.InsertRule(context.Background(), r, "t"); err != nil {
		t.Fatal(err)
	}
	fn := &fakeNotifier{}
	ev := New(svc, fn.Notify, slog.New(slog.NewTextHandler(io.Discard, nil)))
	failWebhook := errors.New("endpoint returned 500")
	email, webhook := &fakeEmailSink{}, &fakeWebhookSink{err: failWebhook}
	ev.SetChannels(email, webhook)

	openLane(t, ev, svc)
	ev.WaitDispatch()

	events, _ := svc.Events.ListEvents(context.Background(), "r1", 10)
	d := events[0].Delivered
	if d["webhook"].Status != "failed" || d["webhook"].Reason != failWebhook.Error() {
		t.Fatalf("webhook delivered = %+v", d["webhook"])
	}
	if d["email"].Status != "sent" {
		t.Fatalf("email delivered = %+v", d["email"])
	}
}

func TestEvaluatorChannelNotConfigured(t *testing.T) {
	svc := newSvc(t)
	r := channelsRule()
	if _, err := svc.Rules.InsertRule(context.Background(), r, "t"); err != nil {
		t.Fatal(err)
	}
	fn := &fakeNotifier{}
	ev := New(svc, fn.Notify, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// SetChannels never called: both sinks are nil.

	openLane(t, ev, svc)
	ev.WaitDispatch()

	events, _ := svc.Events.ListEvents(context.Background(), "r1", 10)
	d := events[0].Delivered
	if d["email"].Status != "failed" || d["email"].Reason != errNotConfigured.Error() {
		t.Fatalf("email delivered = %+v", d["email"])
	}
	if d["webhook"].Status != "failed" || d["webhook"].Reason != errNotConfigured.Error() {
		t.Fatalf("webhook delivered = %+v", d["webhook"])
	}
	// Telegram still goes through Notifier (unaffected by the other
	// channels being unconfigured).
	if fn.count() != 1 {
		t.Fatalf("telegram deliveries = %d, want 1", fn.count())
	}
}
