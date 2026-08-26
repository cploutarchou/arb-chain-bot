package binance

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"time"

	"github.com/gorilla/websocket"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
)

// Feed maintains the subscribed markets' books: WS connect/read, syncer
// splices with REST snapshots, gap-triggered resyncs, keep-alive, and the
// pre-emptive reconnect ahead of Binance's 24h forced disconnect. The
// decode→validate→apply pipeline is the unit-tested code in this package;
// Feed is the I/O shell around it.
type Feed struct {
	WSHost   string
	REST     *RESTClient
	Books    *orderbook.Set
	Symbols  []exchange.Symbol
	Log      *slog.Logger
	MaxDepth int // book truncation (0 = unlimited)

	// Reconnect policy (SKILL.md §11): bounded backoff with jitter,
	// respecting the 300-attempts/5min budget by construction.
	BackoffMin time.Duration
	BackoffMax time.Duration
	// PreemptAfter forces a clean reconnect before the venue's 24h cut.
	PreemptAfter time.Duration

	syncers map[exchange.MarketID]*Syncer
}

func (f *Feed) defaults() {
	if f.BackoffMin <= 0 {
		f.BackoffMin = time.Second
	}
	if f.BackoffMax <= 0 {
		f.BackoffMax = 30 * time.Second
	}
	if f.PreemptAfter <= 0 {
		f.PreemptAfter = 23 * time.Hour
	}
}

func (f *Feed) Name() string { return "binance-feed" }

// Run connects and reads until ctx cancels, reconnecting with backoff on
// any failure. Books transition DISCONNECTED→SYNCING→HEALTHY per session.
func (f *Feed) Run(ctx context.Context) error {
	f.defaults()
	backoff := f.BackoffMin
	rng := rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec // jitter only
	for {
		err := f.session(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		f.Log.Warn("binance feed session ended; reconnecting", "error", err, "backoff", backoff.String())
		f.markAll(func(b *orderbook.Book) { b.MarkDisconnected() })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff + time.Duration(rng.Int63n(int64(backoff/2)+1))):
		}
		backoff = min(backoff*2, f.BackoffMax)
	}
}

// session runs one WS connection lifecycle.
func (f *Feed) session(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, StreamURL(f.WSHost, f.Symbols), nil)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	conn.SetReadLimit(16 << 20)
	// Gorilla answers server pings with pongs by default; the deadline
	// enforces the server-ping-every-20s liveness contract.
	resetDeadline := func() { _ = conn.SetReadDeadline(time.Now().Add(75 * time.Second)) }
	conn.SetPingHandler(func(appData string) error {
		resetDeadline()
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(5*time.Second))
	})
	resetDeadline()

	// Fresh books + syncers each session; the registry swap makes stale
	// views visibly DISCONNECTED-era (versions restart).
	f.syncers = make(map[exchange.MarketID]*Syncer, len(f.Symbols))
	for _, sym := range f.Symbols {
		id := exchange.MarketID{Exchange: ID, Symbol: sym}
		book := orderbook.New(id, f.MaxDepth)
		f.Books.Add(book)
		f.syncers[id] = NewSyncer(book, 0)
	}

	// Snapshot fetches run beside the read loop, paced for REST weight
	// (250 per 5000-level call against the 6000/min budget).
	snapErr := make(chan error, 1)
	go f.fetchSnapshots(ctx, snapErr)

	preempt := time.NewTimer(f.PreemptAfter)
	defer preempt.Stop()

	frames := make(chan []byte, 1024)
	readErr := make(chan error, 1)
	go func() {
		for {
			_, frame, err := conn.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			resetDeadline()
			select {
			case frames <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-preempt.C:
			return errors.New("pre-emptive reconnect before 24h cut")
		case err := <-readErr:
			return err
		case err := <-snapErr:
			if err != nil {
				return err
			}
		case frame := <-frames:
			f.handleFrame(ctx, frame)
		}
	}
}

func (f *Feed) handleFrame(ctx context.Context, frame []byte) {
	ev, err := DecodeWSFrame(frame, time.Now())
	if err != nil {
		if !errors.Is(err, ErrNotDepthEvent) {
			f.Log.Warn("binance frame decode failed", "error", err)
		}
		return
	}
	syncer, ok := f.syncers[ev.Market]
	if !ok {
		return
	}
	wasSynced := syncer.Synced()
	action, serr := syncer.OnDelta(ev)
	if serr != nil { // buffer overflow: restart this market's sync
		f.Log.Warn("binance sync restart", "market", ev.Market.String(), "error", serr)
		go f.resyncMarket(ctx, ev.Market)
		return
	}
	switch {
	case action == orderbook.ActionApply:
		f.Books.MarkDirty(ev.Market)
	case action == orderbook.ActionGap, wasSynced && !syncer.Synced():
		f.Log.Warn("binance sequence gap; resyncing", "market", ev.Market.String())
		go f.resyncMarket(ctx, ev.Market)
	}
}

// fetchSnapshots primes every subscribed market once, paced.
func (f *Feed) fetchSnapshots(ctx context.Context, done chan<- error) {
	for _, sym := range f.Symbols {
		if ctx.Err() != nil {
			done <- ctx.Err()
			return
		}
		id := exchange.MarketID{Exchange: ID, Symbol: sym}
		f.resyncMarket(ctx, id)
		// ~4 snapshots/second keeps far under the weight budget.
		select {
		case <-ctx.Done():
			done <- ctx.Err()
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
	done <- nil
}

// resyncMarket fetches a snapshot and splices it; ErrSnapshotBehindBuffer
// retries with fresh snapshots (bounded).
func (f *Feed) resyncMarket(ctx context.Context, id exchange.MarketID) {
	syncer, ok := f.syncers[id]
	if !ok {
		return
	}
	for attempt := 0; attempt < 5; attempt++ {
		snap, err := f.REST.Depth(ctx, id.Symbol, SnapshotDepthLimit)
		if err != nil {
			f.Log.Warn("binance snapshot fetch failed", "market", id.String(), "attempt", attempt, "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second << attempt):
			}
			continue
		}
		err = syncer.OnSnapshot(snap)
		switch {
		case err == nil:
			f.Books.MarkDirty(id)
			return
		case errors.Is(err, ErrSnapshotBehindBuffer):
			continue // newer snapshot needed; buffer keeps growing
		default:
			f.Log.Warn("binance splice failed; restarting sync", "market", id.String(), "error", err)
		}
	}
	f.Log.Error("binance resync exhausted attempts", "market", id.String())
}

func (f *Feed) markAll(fn func(*orderbook.Book)) {
	for id := range f.syncers {
		if b, ok := f.Books.Get(id); ok {
			fn(b)
		}
	}
}
