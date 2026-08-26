package orderbook

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func lv(price, qty string) Level { return Level{Price: d(price), Qty: d(qty)} }

var testMkt = exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"}

func snapshot(finalID int64, bids, asks []Level) DepthEvent {
	return DepthEvent{
		Market: testMkt, IsSnapshot: true, FinalUpdateID: finalID,
		Bids: bids, Asks: asks,
		ReceiveTime: time.Unix(1000, 0),
	}
}

// chainValidator implements the generic update-ID chain: first delta after
// snapshot must satisfy First <= last+1 <= Final; then First == last+1.
type chainValidator struct{}

func (chainValidator) Validate(m Meta, ev DepthEvent) Action {
	if !m.Initialized {
		return ActionDrop
	}
	if ev.FinalUpdateID <= m.LastUpdateID {
		return ActionDrop
	}
	if ev.FirstUpdateID > m.LastUpdateID+1 {
		return ActionGap
	}
	return ActionApply
}

func TestSnapshotSortsAndFilters(t *testing.T) {
	b := New(testMkt, 0)
	b.ApplySnapshot(snapshot(100,
		[]Level{lv("99", "1"), lv("101", "2"), lv("100", "0"), lv("100.5", "3")},
		[]Level{lv("103", "1"), lv("102", "2")},
	))
	v := b.View(0)
	if v.State != StateHealthy {
		t.Fatalf("state = %s", v.State)
	}
	// Bids descending, zero-qty filtered out.
	wantBids := []string{"101", "100.5", "99"}
	for i, w := range wantBids {
		if !v.Bids[i].Price.Equal(d(w)) {
			t.Fatalf("bids[%d] = %s, want %s", i, v.Bids[i].Price, w)
		}
	}
	// Asks ascending.
	if !v.Asks[0].Price.Equal(d("102")) || !v.Asks[1].Price.Equal(d("103")) {
		t.Fatalf("asks = %v", v.Asks)
	}
}

func TestDeltaMergeReplaceInsertDelete(t *testing.T) {
	b := New(testMkt, 0)
	b.ApplySnapshot(snapshot(100,
		[]Level{lv("100", "1"), lv("99", "1")},
		[]Level{lv("101", "1"), lv("102", "1")}))

	action := b.Apply(DepthEvent{
		Market: testMkt, FirstUpdateID: 101, FinalUpdateID: 101,
		Bids: []Level{lv("100", "5"), lv("99.5", "2"), lv("99", "0")},
		Asks: []Level{lv("101", "0"), lv("103", "4")},
		ReceiveTime: time.Unix(1001, 0),
	}, chainValidator{})
	if action != ActionApply {
		t.Fatalf("action = %s", action)
	}
	v := b.View(0)
	// Bids: 100 replaced qty 5; 99.5 inserted; 99 deleted.
	if len(v.Bids) != 2 || !v.Bids[0].Qty.Equal(d("5")) || !v.Bids[1].Price.Equal(d("99.5")) {
		t.Fatalf("bids = %v", v.Bids)
	}
	// Asks: 101 deleted; 102 kept; 103 inserted.
	if len(v.Asks) != 2 || !v.Asks[0].Price.Equal(d("102")) || !v.Asks[1].Price.Equal(d("103")) {
		t.Fatalf("asks = %v", v.Asks)
	}
	if v.Version != 2 {
		t.Fatalf("version = %d", v.Version)
	}
}

func TestDeleteAbsentLevelIsNoop(t *testing.T) {
	b := New(testMkt, 0)
	b.ApplySnapshot(snapshot(100, []Level{lv("100", "1")}, []Level{lv("101", "1")}))
	b.Apply(DepthEvent{FirstUpdateID: 101, FinalUpdateID: 101,
		Bids: []Level{lv("98", "0")}}, chainValidator{})
	if v := b.View(0); len(v.Bids) != 1 {
		t.Fatalf("bids = %v", v.Bids)
	}
}

func TestSequenceGapCorruptsBook(t *testing.T) {
	b := New(testMkt, 0)
	b.ApplySnapshot(snapshot(100, []Level{lv("100", "1")}, []Level{lv("101", "1")}))
	action := b.Apply(DepthEvent{FirstUpdateID: 105, FinalUpdateID: 106}, chainValidator{})
	if action != ActionGap {
		t.Fatalf("action = %s", action)
	}
	if got := b.View(0).State; got != StateCorrupted {
		t.Fatalf("state = %s, want CORRUPTED", got)
	}
	// A corrupted book requires a fresh snapshot; meta shows uninitialized.
	if b.Meta().Initialized {
		t.Fatal("corrupted book must not be initialized")
	}
}

func TestDuplicateDropped(t *testing.T) {
	b := New(testMkt, 0)
	b.ApplySnapshot(snapshot(100, []Level{lv("100", "1")}, nil))
	action := b.Apply(DepthEvent{FirstUpdateID: 99, FinalUpdateID: 100,
		Bids: []Level{lv("100", "9")}}, chainValidator{})
	if action != ActionDrop {
		t.Fatalf("action = %s", action)
	}
	if v := b.View(0); !v.Bids[0].Qty.Equal(d("1")) {
		t.Fatalf("duplicate mutated book: %v", v.Bids)
	}
}

func TestStateMachineTransitions(t *testing.T) {
	b := New(testMkt, 0)
	var log []string
	b.OnTransition(func(from, to State, reason string) {
		log = append(log, fmt.Sprintf("%s->%s(%s)", from, to, reason))
	})

	if got := b.Meta().State; got != StateSyncing {
		t.Fatalf("initial = %s", got)
	}
	b.ApplySnapshot(snapshot(1, []Level{lv("1", "1")}, nil)) // SYNCING->HEALTHY
	b.EvaluateStaleness(time.Unix(2000, 0), time.Second)     // HEALTHY->STALE (age 1000s)
	if got := b.Meta().State; got != StateStale {
		t.Fatalf("after staleness = %s", got)
	}
	// Fresh delta restores HEALTHY.
	b.Apply(DepthEvent{FirstUpdateID: 2, FinalUpdateID: 2,
		Bids: []Level{lv("1", "2")}, ReceiveTime: time.Unix(2000, 0)}, chainValidator{})
	if got := b.Meta().State; got != StateHealthy {
		t.Fatalf("after update = %s", got)
	}
	b.MarkDisconnected()
	b.MarkSyncing()
	b.ApplySnapshot(snapshot(10, []Level{lv("1", "1")}, nil))
	want := []string{
		"SYNCING->HEALTHY(snapshot)",
		"HEALTHY->STALE(age exceeded)",
		"STALE->HEALTHY(update)",
		"HEALTHY->DISCONNECTED(transport lost)",
		"DISCONNECTED->SYNCING(resync)",
		"SYNCING->HEALTHY(snapshot)",
	}
	if len(log) != len(want) {
		t.Fatalf("transitions = %v", log)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("transition[%d] = %s, want %s", i, log[i], want[i])
		}
	}
}

func TestMaxDepthTruncation(t *testing.T) {
	b := New(testMkt, 2) // Kraken semantics: truncate to subscribed depth
	b.ApplySnapshot(snapshot(1,
		[]Level{lv("100", "1"), lv("99", "1"), lv("98", "1")},
		[]Level{lv("101", "1"), lv("102", "1"), lv("103", "1")}))
	v := b.View(0)
	if len(v.Bids) != 2 || len(v.Asks) != 2 {
		t.Fatalf("truncation failed: %d/%d", len(v.Bids), len(v.Asks))
	}
	// An insert at the top pushes the bottom level out.
	b.Apply(DepthEvent{FirstUpdateID: 2, FinalUpdateID: 2,
		Bids: []Level{lv("100.5", "1")}}, chainValidator{})
	v = b.View(0)
	if !v.Bids[0].Price.Equal(d("100.5")) || !v.Bids[1].Price.Equal(d("100")) || len(v.Bids) != 2 {
		t.Fatalf("bids after truncating insert = %v", v.Bids)
	}
}

// Views must be immune to later book mutations (evaluators hold them).
func TestViewIsolation(t *testing.T) {
	b := New(testMkt, 0)
	b.ApplySnapshot(snapshot(1, []Level{lv("100", "1")}, []Level{lv("101", "1")}))
	v := b.View(0)
	b.Apply(DepthEvent{FirstUpdateID: 2, FinalUpdateID: 2,
		Bids: []Level{lv("100", "999")}}, chainValidator{})
	if !v.Bids[0].Qty.Equal(d("1")) {
		t.Fatalf("view mutated by later update: %v", v.Bids)
	}
}

func TestViewAgeWithoutReceiveTimeIsInfinite(t *testing.T) {
	v := View{}
	if v.Age(time.Now()) < time.Hour*24*365 {
		t.Fatal("zero receive time must read as effectively infinite age")
	}
}

func TestSetDirtyCoalescing(t *testing.T) {
	s := NewSet()
	b := New(testMkt, 0)
	s.Add(b)
	for i := 0; i < 100; i++ {
		s.MarkDirty(testMkt)
	}
	<-s.Signal()
	got := s.Drain()
	if len(got) != 1 || got[0] != testMkt {
		t.Fatalf("drain = %v", got)
	}
	if again := s.Drain(); again != nil {
		t.Fatalf("second drain = %v", again)
	}
}

// Race: one writer applying deltas, many readers taking views. Run with -race.
func TestConcurrentApplyAndView(t *testing.T) {
	b := New(testMkt, 0)
	b.ApplySnapshot(snapshot(0, []Level{lv("100", "1")}, []Level{lv("101", "1")}))
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					v := b.View(10)
					_ = v.Age(time.Now())
				}
			}
		}()
	}
	for i := int64(1); i <= 5000; i++ {
		b.Apply(DepthEvent{FirstUpdateID: i, FinalUpdateID: i,
			Bids: []Level{{Price: d("100").Add(decimal.New(i%50, -2)), Qty: d("1")}},
		}, chainValidator{})
	}
	close(stop)
	wg.Wait()
	if got := b.Meta().LastUpdateID; got != 5000 {
		t.Fatalf("lastUpdateID = %d", got)
	}
}

func BenchmarkApplyDelta(b *testing.B) {
	book := New(testMkt, 0)
	levels := make([]Level, 0, 500)
	for i := 0; i < 500; i++ {
		levels = append(levels, Level{Price: decimal.New(int64(10000+i), -2), Qty: d("1")})
	}
	book.ApplySnapshot(DepthEvent{IsSnapshot: true, FinalUpdateID: 0, Bids: levels})
	v := chainValidator{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		book.Apply(DepthEvent{
			FirstUpdateID: int64(i + 1), FinalUpdateID: int64(i + 1),
			Bids: []Level{{Price: decimal.New(int64(10000+i%500), -2), Qty: decimal.New(int64(i%10+1), 0)}},
		}, v)
	}
}

func BenchmarkView50(b *testing.B) {
	book := New(testMkt, 0)
	levels := make([]Level, 0, 1000)
	for i := 0; i < 1000; i++ {
		levels = append(levels, Level{Price: decimal.New(int64(10000+i), -2), Qty: d("1")})
	}
	book.ApplySnapshot(DepthEvent{IsSnapshot: true, Bids: levels, Asks: levels})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = book.View(50)
	}
}
