package simulation

import (
	"context"
	"sync"
	"time"
)

// WallClock is the live paper clock.
type WallClock struct{}

func (WallClock) Now() time.Time { return time.Now() }

// RealWaiter elapses real time, bounded by the context.
type RealWaiter struct{}

func (RealWaiter) Wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// VirtualClock is a manually advanced clock shared by replay components.
type VirtualClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewVirtualClock(start time.Time) *VirtualClock { return &VirtualClock{now: start} }

func (c *VirtualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *VirtualClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// VirtualWaiter advances the virtual clock instead of sleeping; context
// cancellation still interrupts (timeout scenarios in replay).
type VirtualWaiter struct{ Clock *VirtualClock }

func (w VirtualWaiter) Wait(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.Clock.Advance(d)
	return nil
}
