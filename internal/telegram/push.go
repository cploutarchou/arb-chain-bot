package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/notification"
)

// PushSink is the notification channel adapter: Deliver enqueues
// without blocking (the router must never wait on network I/O) and the
// Run loop fans deliveries out to every allowlisted chat.
type PushSink struct {
	Client  *Client
	ChatIDs []int64
	Log     *slog.Logger
	// OnDrop is called when the queue is full (notification.Service's
	// CountDrop in the wiring).
	OnDrop func()
	// Targets, when set, takes precedence over ChatIDs (T-057: the
	// wiring points it at the same live allow-set as Bot.Allowed, so a
	// revoked user stops receiving pushes immediately, not just commands).
	Targets func() []int64

	once sync.Once
	ch   chan notification.Delivery

	// Push-delivery counters (BL-21: GET /api/v1/telegram/status).
	pushed     atomic.Int64
	pushErrors atomic.Int64
	lastPushAt atomic.Int64 // unix nanos; 0 = never
}

// PushStatus is the outbound-push snapshot the console reads (BL-21).
type PushStatus struct {
	Pushed     int64
	Errors     int64
	LastPushAt time.Time
}

// Status snapshots delivery counters.
func (p *PushSink) Status() PushStatus {
	return PushStatus{Pushed: p.pushed.Load(), Errors: p.pushErrors.Load(), LastPushAt: unixOrZero(p.lastPushAt.Load())}
}

const pushQueue = 64

func (p *PushSink) Name() string { return "telegram" }

// init creates the queue exactly once: Deliver (router goroutine) and
// Run (drain goroutine) start concurrently, and the unguarded version
// could make two channels — deliveries into the orphan were silently
// lost (audit CR-P1-4).
func (p *PushSink) init() {
	p.once.Do(func() {
		p.ch = make(chan notification.Delivery, pushQueue)
	})
}

// Deliver implements notification.Sink; never blocks.
func (p *PushSink) Deliver(d notification.Delivery) {
	p.init()
	select {
	case p.ch <- d:
	default:
		if p.OnDrop != nil {
			p.OnDrop()
		}
		p.Log.Warn("telegram push queue full; alert dropped", "key", d.Key)
	}
}

// Run drains the queue until ctx cancels.
func (p *PushSink) Run(ctx context.Context) error {
	p.init()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d := <-p.ch:
			text := fmt.Sprintf("[%s] %s\n%s", d.Severity.String(), d.Title, d.Body)
			if d.Suppressed > 0 {
				text += fmt.Sprintf("\n(+%d duplicates suppressed)", d.Suppressed)
			}
			chatIDs := p.ChatIDs
			if p.Targets != nil {
				chatIDs = p.Targets()
			}
			for _, chat := range chatIDs {
				sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				err := p.Client.SendMessage(sendCtx, chat, text, nil)
				cancel()
				p.lastPushAt.Store(time.Now().UnixNano())
				if err != nil {
					p.pushErrors.Add(1)
					p.Log.Warn("telegram push failed", "chat_id", chat, "error", err)
					continue
				}
				p.pushed.Add(1)
			}
		}
	}
}
