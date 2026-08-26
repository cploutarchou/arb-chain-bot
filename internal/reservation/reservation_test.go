package reservation

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func newMgr(usdt string) *Manager {
	var seq atomic.Int64
	return New(
		map[exchange.Asset]decimal.Decimal{"USDT": d(usdt)},
		func() string { return fmt.Sprintf("res-%d", seq.Add(1)) },
		func() time.Time { return time.Unix(1_700_000_000, 0) },
	)
}

func TestReserveSettleFlow(t *testing.T) {
	m := newMgr("1000")
	r, err := m.Reserve("op-1", "USDT", d("400"), "tri-A", []string{"mkt:BTCUSDT:buy"})
	if err != nil {
		t.Fatal(err)
	}
	if avail, res := m.Balance("USDT"); !avail.Equal(d("600")) || !res.Equal(d("400")) {
		t.Fatalf("after reserve: %s/%s", avail, res)
	}
	if !m.TriangleReserved("tri-A").Equal(d("400")) {
		t.Fatalf("triangle reserved = %s", m.TriangleReserved("tri-A"))
	}
	// Deploy 380, return 20; proceeds 385 credited back later.
	if err := m.Settle(r.ID, d("380")); err != nil {
		t.Fatal(err)
	}
	if avail, res := m.Balance("USDT"); !avail.Equal(d("620")) || !res.IsZero() {
		t.Fatalf("after settle: %s/%s", avail, res)
	}
	if err := m.Credit("USDT", d("385")); err != nil {
		t.Fatal(err)
	}
	if avail, _ := m.Balance("USDT"); !avail.Equal(d("1005")) {
		t.Fatalf("after credit: %s", avail)
	}
	if err := m.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestIdempotentReserve(t *testing.T) {
	m := newMgr("1000")
	r1, _ := m.Reserve("op-1", "USDT", d("400"), "tri-A", nil)
	r2, err := m.Reserve("op-1", "USDT", d("999"), "tri-B", nil) // different args, same key
	if err != nil {
		t.Fatal(err)
	}
	if r1.ID != r2.ID || !r2.Amount.Equal(d("400")) {
		t.Fatalf("idempotency broken: %+v vs %+v", r1, r2)
	}
	// No double spend happened.
	if avail, _ := m.Balance("USDT"); !avail.Equal(d("600")) {
		t.Fatalf("available = %s", avail)
	}
}

func TestConflictKeysSerializeTriangles(t *testing.T) {
	m := newMgr("1000")
	_, err := m.Reserve("op-1", "USDT", d("100"), "tri-A", []string{"mkt:ETHBTC:buy"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Reserve("op-2", "USDT", d("100"), "tri-B", []string{"mkt:ETHBTC:buy"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict not detected: %v", err)
	}
	// Different conflict key proceeds.
	if _, err := m.Reserve("op-3", "USDT", d("100"), "tri-C", []string{"mkt:ETHBTC:sell"}); err != nil {
		t.Fatal(err)
	}
}

func TestInsufficientFunds(t *testing.T) {
	m := newMgr("100")
	if _, err := m.Reserve("op-1", "USDT", d("100.00000001"), "t", nil); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("err = %v", err)
	}
	// Exactly-all is allowed.
	if _, err := m.Reserve("op-2", "USDT", d("100"), "t", nil); err != nil {
		t.Fatal(err)
	}
}

func TestExactlyOnceSettleRelease(t *testing.T) {
	m := newMgr("1000")
	r, _ := m.Reserve("op-1", "USDT", d("100"), "t", nil)
	if err := m.Settle(r.ID, d("50")); err != nil {
		t.Fatal(err)
	}
	if err := m.Settle(r.ID, d("50")); !errors.Is(err, ErrNotActive) {
		t.Fatalf("double settle: %v", err)
	}
	if err := m.Release(r.ID); !errors.Is(err, ErrNotActive) {
		t.Fatalf("settle-then-release: %v", err)
	}

	r2, _ := m.Reserve("op-2", "USDT", d("100"), "t", nil)
	if err := m.Release(r2.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Release(r2.ID); !errors.Is(err, ErrNotActive) {
		t.Fatalf("double release: %v", err)
	}
	if err := m.Settle("nope", d("1")); !errors.Is(err, ErrUnknownReservation) {
		t.Fatalf("unknown id: %v", err)
	}
	if err := m.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestOverConsumeRejected(t *testing.T) {
	m := newMgr("1000")
	r, _ := m.Reserve("op-1", "USDT", d("100"), "t", nil)
	if err := m.Settle(r.ID, d("100.01")); !errors.Is(err, ErrOverConsume) {
		t.Fatalf("over-consume: %v", err)
	}
}

// Storm: concurrent reserve/settle/release with random amounts. Run with
// -race. The ledger must conserve and never go negative.
func TestConcurrentStormInvariants(t *testing.T) {
	m := newMgr("100000")
	var wg sync.WaitGroup
	workers := 8
	perWorker := 200
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			for i := 0; i < perWorker; i++ {
				key := fmt.Sprintf("op-%d-%d", w, i)
				amount := decimal.NewFromInt(int64(rng.Intn(50) + 1))
				r, err := m.Reserve(key, "USDT", amount, fmt.Sprintf("tri-%d", rng.Intn(5)), nil)
				if err != nil {
					continue // insufficient funds under contention is fine
				}
				switch rng.Intn(3) {
				case 0:
					consumed := amount.Div(decimal.NewFromInt(2))
					_ = m.Settle(r.ID, consumed)
					_ = m.Credit("USDT", consumed) // cycle returns what it consumed
				case 1:
					_ = m.Release(r.ID)
				default:
					// leave active
				}
			}
		}(w)
	}
	wg.Wait()
	if err := m.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
	avail, res := m.Balance("USDT")
	if avail.IsNegative() || res.IsNegative() {
		t.Fatalf("negative ledger: %s/%s", avail, res)
	}
}

// Duplicate-key race: N goroutines race the same idempotency key; exactly
// one reservation must exist.
func TestConcurrentDuplicateKey(t *testing.T) {
	m := newMgr("1000")
	var wg sync.WaitGroup
	ids := make([]string, 16)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := m.Reserve("same-key", "USDT", d("10"), "t", nil)
			if err == nil {
				ids[i] = r.ID
			}
		}(i)
	}
	wg.Wait()
	first := ids[0]
	for _, id := range ids {
		if id != first {
			t.Fatalf("distinct reservations under one key: %v", ids)
		}
	}
	if avail, _ := m.Balance("USDT"); !avail.Equal(d("990")) {
		t.Fatalf("double spend: available = %s", avail)
	}
}
