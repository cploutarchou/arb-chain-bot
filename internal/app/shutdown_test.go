package app

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
)

// The persistence context must outlive every producer (P0-3): a paper
// cycle settling during shutdown enqueues its result, and the outbox
// has to still be accepting when it does.
func TestShutdownStagedCancelsWritersOnlyAfterProducersReturn(t *testing.T) {
	e := NewEngine(config.Bootstrap{ShutdownGrace: 2 * time.Second}, testLogger())
	runCtx, cancelRun := context.WithCancel(context.Background())
	persistCtx, cancelPersist := context.WithCancel(context.Background())

	var producers, writers sync.WaitGroup
	var writerLiveAtSettle atomic.Bool
	producers.Add(1)
	go func() {
		defer producers.Done()
		<-runCtx.Done()
		time.Sleep(50 * time.Millisecond) // an in-flight cycle settling after cancel
		writerLiveAtSettle.Store(persistCtx.Err() == nil)
	}()
	writers.Add(1)
	go func() {
		defer writers.Done()
		<-persistCtx.Done()
	}()

	cancelRun()
	start := time.Now()
	e.shutdownStaged(&producers, &writers, cancelPersist, 100*time.Millisecond)
	if !writerLiveAtSettle.Load() {
		t.Fatal("persistence context was cancelled before the producers had returned")
	}
	if persistCtx.Err() == nil {
		t.Fatal("persistence context still live after shutdown")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("shutdown took %s with cooperative goroutines", elapsed)
	}
}

// A producer that never returns must not hold the restart forever, and
// the writer stage must still get its drain window after the grace is
// spent on the producers.
func TestShutdownStagedIsBoundedAndStillDrainsWriters(t *testing.T) {
	e := NewEngine(config.Bootstrap{ShutdownGrace: 100 * time.Millisecond}, testLogger())
	_, cancelPersist := context.WithCancel(context.Background())

	var producers, writers sync.WaitGroup
	producers.Add(1) // never Done: a stuck goroutine
	writers.Add(1)
	var writerFinished atomic.Bool
	go func() {
		defer writers.Done()
		time.Sleep(150 * time.Millisecond) // a drain longer than the (spent) grace
		writerFinished.Store(true)
	}()

	start := time.Now()
	e.shutdownStaged(&producers, &writers, cancelPersist, 200*time.Millisecond)
	elapsed := time.Since(start)
	if !writerFinished.Load() {
		t.Fatal("writer stage was cut short although its drain window had not elapsed")
	}
	// grace (100ms, spent on the stuck producer) + writer floor (drain 200ms + 1s)
	// is the upper bound; the writer itself finished after ~150ms.
	if elapsed > 2*time.Second {
		t.Fatalf("shutdown took %s, want bounded", elapsed)
	}
}
