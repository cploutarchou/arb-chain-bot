package binance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"sync/atomic"
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

	// RawTap and SnapTap, when set, receive every WS frame and every REST
	// snapshot body used for splices (the recorder; SKILL.md §63). Taps
	// must be non-blocking — recording never stalls the feed.
	RawTap  func(frame []byte, recv time.Time)
	SnapTap func(symbol exchange.Symbol, body []byte, recv time.Time)

	// LatencyObserver, when set, receives event-time→receive latency per
	// decoded depth event (metrics; must be cheap and non-blocking).
	LatencyObserver func(d time.Duration)

	// Stats are cumulative transport counters, read by the metrics
	// scraper; the hot path only pays single atomic adds.
	Stats FeedStats

	// syncers is replaced per session while resync goroutines from the
	// previous session may still be running; every access goes through
	// the mutex. Per-market single-flight for resyncs lives on Syncer.
	syncMu  sync.Mutex
	syncers map[exchange.MarketID]*Syncer
}

func (f *Feed) getSyncer(id exchange.MarketID) (*Syncer, bool) {
	f.syncMu.Lock()
	defer f.syncMu.Unlock()
	s, ok := f.syncers[id]
	return s, ok
}

// FeedStats are the feed's atomic counters.
type FeedStats struct {
	Frames     atomic.Int64 // WS frames received
	Reconnects atomic.Int64 // session restarts after the first connect
	APIErrors  atomic.Int64 // REST snapshot failures
	Resyncs    atomic.Int64 // snapshot splices started
	SeqGaps    atomic.Int64 // sequence gaps detected
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
		f.Stats.Reconnects.Add(1)
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
	fresh := make(map[exchange.MarketID]*Syncer, len(f.Symbols))
	for _, sym := range f.Symbols {
		id := exchange.MarketID{Exchange: ID, Symbol: sym}
		book := orderbook.New(id, f.MaxDepth)
		f.Books.Add(book)
		fresh[id] = NewSyncer(book, 0)
	}
	f.syncMu.Lock()
	f.syncers = fresh
	f.syncMu.Unlock()

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
	recv := time.Now()
	f.Stats.Frames.Add(1)
	if f.RawTap != nil {
		f.RawTap(frame, recv)
	}
	ev, err := DecodeWSFrame(frame, recv)
	if err != nil {
		if !errors.Is(err, ErrNotDepthEvent) {
			f.Log.Warn("binance frame decode failed", "error", err)
		}
		return
	}
	if f.LatencyObserver != nil && !ev.EventTime.IsZero() {
		f.LatencyObserver(recv.Sub(ev.EventTime))
	}
	syncer, ok := f.getSyncer(ev.Market)
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
		f.Stats.SeqGaps.Add(1)
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

// CaptureSnapshots fetches one REST depth snapshot per configured symbol
// and hands it to SnapTap WITHOUT splicing it into the live books. A
// recording session that starts after the feed synced would otherwise
// hold only diff frames, and a replay of it could never initialise a
// book (its syncers buffer diffs until a snapshot arrives). Paced like
// fetchSnapshots to stay far under the REST weight budget.
func (f *Feed) CaptureSnapshots(ctx context.Context) error {
	if f.SnapTap == nil || f.REST == nil {
		return nil
	}
	for i, sym := range f.Symbols {
		if err := ctx.Err(); err != nil {
			return err
		}
		body, snap, err := f.REST.DepthRaw(ctx, sym, SnapshotDepthLimit)
		if err != nil {
			f.Stats.APIErrors.Add(1)
			return fmt.Errorf("capture snapshot %s: %w", sym, err)
		}
		f.SnapTap(sym, body, snap.ReceiveTime)
		if i < len(f.Symbols)-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	return nil
}

// resyncMarket fetches a snapshot and splices it; ErrSnapshotBehindBuffer
// retries with fresh snapshots (bounded).
func (f *Feed) resyncMarket(ctx context.Context, id exchange.MarketID) {
	syncer, ok := f.getSyncer(id)
	if !ok {
		return
	}
	// Single flight per market: a second gap during an in-flight resync
	// changes nothing — the running resync fetches a newer snapshot and
	// the buffer keeps accumulating under the syncer lock.
	if !syncer.resyncing.CompareAndSwap(false, true) {
		return
	}
	defer syncer.resyncing.Store(false)
	f.Stats.Resyncs.Add(1)
	for attempt := 0; attempt < 5; attempt++ {
		body, snap, err := f.REST.DepthRaw(ctx, id.Symbol, SnapshotDepthLimit)
		if err == nil && f.SnapTap != nil {
			f.SnapTap(id.Symbol, body, snap.ReceiveTime)
		}
		if err != nil {
			f.Stats.APIErrors.Add(1)
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
	f.syncMu.Lock()
	ids := make([]exchange.MarketID, 0, len(f.syncers))
	for id := range f.syncers {
		ids = append(ids, id)
	}
	f.syncMu.Unlock()
	for _, id := range ids {
		if b, ok := f.Books.Get(id); ok {
			fn(b)
		}
	}
}
