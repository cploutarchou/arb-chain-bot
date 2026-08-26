// Package app wires platform components into one process and owns their
// lifecycle. Components are started together, stopped in reverse order on
// context cancellation, and given a bounded grace period to drain.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
)

// Component is a long-running platform subsystem. Run must block until ctx
// is cancelled (or a fatal error occurs) and must release all resources
// before returning. Components own their goroutines: nothing they spawn
// may outlive Run.
type Component interface {
	Name() string
	Run(ctx context.Context) error
}

// ComponentFunc adapts a function to the Component interface.
type ComponentFunc struct {
	ComponentName string
	Fn            func(ctx context.Context) error
}

func (c ComponentFunc) Name() string                  { return c.ComponentName }
func (c ComponentFunc) Run(ctx context.Context) error { return c.Fn(ctx) }

// App runs a set of components under one lifecycle.
type App struct {
	cfg        config.Bootstrap
	log        *slog.Logger
	components []Component
}

func New(cfg config.Bootstrap, log *slog.Logger, components ...Component) *App {
	return &App{cfg: cfg, log: log, components: components}
}

// Run starts every component and blocks until the first fatal component
// error or an interrupt signal, then cancels the shared context and waits
// up to ShutdownGrace for components to return. A component returning
// context.Canceled during shutdown is a clean exit, not an error.
func (a *App) Run(parent context.Context) error {
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(a.components))
	var wg sync.WaitGroup

	for _, c := range a.components {
		wg.Add(1)
		go func(c Component) {
			defer wg.Done()
			a.log.Info("component starting", "component", c.Name(), "mode", string(a.cfg.Mode))
			err := c.Run(runCtx)
			if err != nil && !errors.Is(err, context.Canceled) {
				a.log.Error("component failed", "component", c.Name(), "error", err)
				errCh <- fmt.Errorf("%s: %w", c.Name(), err)
				return
			}
			a.log.Info("component stopped", "component", c.Name())
		}(c)
	}

	var runErr error
	select {
	case <-ctx.Done():
		a.log.Info("shutdown requested")
	case runErr = <-errCh:
		a.log.Error("fatal component error, shutting down", "error", runErr)
	}
	cancel()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(a.cfg.ShutdownGrace):
		a.log.Error("shutdown grace exceeded; components still running", "grace", a.cfg.ShutdownGrace.String())
		if runErr == nil {
			runErr = errors.New("app: shutdown grace exceeded")
		}
	}
	return runErr
}
