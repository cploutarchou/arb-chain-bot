package telegram

import (
	"context"
	"fmt"
	"log/slog"
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

	ch chan notification.Delivery
}

const pushQueue = 64

func (p *PushSink) Name() string { return "telegram" }

func (p *PushSink) init() {
	if p.ch == nil {
		p.ch = make(chan notification.Delivery, pushQueue)
	}
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
			for _, chat := range p.ChatIDs {
				sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				err := p.Client.SendMessage(sendCtx, chat, text, nil)
				cancel()
				if err != nil {
					p.Log.Warn("telegram push failed", "chat_id", chat, "error", err)
				}
			}
		}
	}
}
