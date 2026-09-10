// Package reservation guards virtual capital with atomic, idempotent
// reserve/settle/release semantics and conservation invariants
// (docs/risk.md §4). One mutex serializes transitions — reservations are
// low-frequency relative to book updates, and correctness beats sharding
// until a profile says otherwise.
package reservation

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

var (
	ErrInsufficientFunds  = errors.New("reservation: insufficient available funds")
	ErrConflict           = errors.New("reservation: conflicting reservation active")
	ErrNotActive          = errors.New("reservation: not active")
	ErrUnknownReservation = errors.New("reservation: unknown id")
	ErrInvalidAmount      = errors.New("reservation: non-positive amount")
	ErrOverConsume        = errors.New("reservation: consumed exceeds reserved amount")
	// ErrDuplicateActive: a second Reserve with the key of a reservation
	// that is still ACTIVE. The original is returned with it so the caller
	// can inspect it, but it must not act on it — the first caller is
	// already executing against that hold (audit F10).
	ErrDuplicateActive = errors.New("reservation: duplicate key while the original is still active")
)

// State of one reservation.
type State string

const (
	StateActive   State = "ACTIVE"
	StateSettled  State = "SETTLED"
	StateReleased State = "RELEASED"
)

// Reservation is one capital hold. Immutable after terminal state.
type Reservation struct {
	ID           string
	Key          string // idempotency key (opportunity id)
	Asset        exchange.Asset
	Amount       decimal.Decimal
	TriangleID   string
	ConflictKeys []string
	State        State
	CreatedAt    time.Time
	Consumed     decimal.Decimal // set on settle
}

// Manager owns the session's start-asset ledger.
type Manager struct {
	mu sync.Mutex

	available map[exchange.Asset]decimal.Decimal
	reserved  map[exchange.Asset]decimal.Decimal

	initial  map[exchange.Asset]decimal.Decimal
	credited map[exchange.Asset]decimal.Decimal
	consumed map[exchange.Asset]decimal.Decimal

	byKey            map[string]*Reservation
	byID             map[string]*Reservation
	activeConflicts  map[string]string          // conflict key -> reservation id
	triangleReserved map[string]decimal.Decimal // triangle id -> active amount

	nextID func() string
	now    func() time.Time
}

// New creates a manager with initial balances. nextID and now are
// injectable for deterministic replay.
func New(initial map[exchange.Asset]decimal.Decimal, nextID func() string, now func() time.Time) *Manager {
	m := &Manager{
		available:        make(map[exchange.Asset]decimal.Decimal, len(initial)),
		reserved:         make(map[exchange.Asset]decimal.Decimal),
		initial:          make(map[exchange.Asset]decimal.Decimal, len(initial)),
		credited:         make(map[exchange.Asset]decimal.Decimal),
		consumed:         make(map[exchange.Asset]decimal.Decimal),
		byKey:            make(map[string]*Reservation),
		byID:             make(map[string]*Reservation),
		activeConflicts:  make(map[string]string),
		triangleReserved: make(map[string]decimal.Decimal),
		nextID:           nextID,
		now:              now,
	}
	for a, v := range initial {
		m.available[a] = v
		m.initial[a] = v
	}
	return m
}

// Reserve atomically holds amount of asset. A repeated key returns the
// original reservation unchanged (idempotency): with ErrDuplicateActive
// while that reservation is still ACTIVE — two callers holding the same
// key concurrently is the duplicate-execution shape a trading system must
// never allow — and with a nil error once it has settled or been
// released, when callers inspect State and skip the replay.
func (m *Manager) Reserve(key string, asset exchange.Asset, amount decimal.Decimal, triangleID string, conflictKeys []string) (Reservation, error) {
	if !amount.IsPositive() {
		return Reservation{}, ErrInvalidAmount
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.byKey[key]; ok {
		if existing.State == StateActive {
			return *existing, fmt.Errorf("%w: %s", ErrDuplicateActive, key)
		}
		return *existing, nil
	}
	for _, ck := range conflictKeys {
		if holder, ok := m.activeConflicts[ck]; ok {
			return Reservation{}, fmt.Errorf("%w: key %q held by %s", ErrConflict, ck, holder)
		}
	}
	avail := m.available[asset]
	if avail.LessThan(amount) {
		return Reservation{}, fmt.Errorf("%w: %s %s < %s", ErrInsufficientFunds, asset, avail, amount)
	}

	r := &Reservation{
		ID:           m.nextID(),
		Key:          key,
		Asset:        asset,
		Amount:       amount,
		TriangleID:   triangleID,
		ConflictKeys: append([]string(nil), conflictKeys...),
		State:        StateActive,
		CreatedAt:    m.now(),
	}
	m.available[asset] = avail.Sub(amount)
	m.reserved[asset] = m.reserved[asset].Add(amount)
	m.byKey[key] = r
	m.byID[r.ID] = r
	for _, ck := range conflictKeys {
		m.activeConflicts[ck] = r.ID
	}
	m.triangleReserved[triangleID] = m.triangleReserved[triangleID].Add(amount)
	return *r, nil
}

// Settle finalizes a reservation: consumed leaves the ledger (deployed
// into the cycle; proceeds come back via Credit), the remainder returns
// to available. Exactly-once: a settled/released reservation errors.
func (m *Manager) Settle(id string, consumed decimal.Decimal) error {
	if consumed.IsNegative() {
		return ErrInvalidAmount
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.byID[id]
	if !ok {
		return ErrUnknownReservation
	}
	if r.State != StateActive {
		return fmt.Errorf("%w: %s is %s", ErrNotActive, id, r.State)
	}
	if consumed.GreaterThan(r.Amount) {
		return fmt.Errorf("%w: %s > %s", ErrOverConsume, consumed, r.Amount)
	}
	m.reserved[r.Asset] = m.reserved[r.Asset].Sub(r.Amount)
	m.available[r.Asset] = m.available[r.Asset].Add(r.Amount.Sub(consumed))
	m.consumed[r.Asset] = m.consumed[r.Asset].Add(consumed)
	r.State = StateSettled
	r.Consumed = consumed
	m.clearHoldsLocked(r)
	return nil
}

// Release returns the full amount to available (revalidation failed,
// opportunity expired). Exactly-once.
func (m *Manager) Release(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.byID[id]
	if !ok {
		return ErrUnknownReservation
	}
	if r.State != StateActive {
		return fmt.Errorf("%w: %s is %s", ErrNotActive, id, r.State)
	}
	m.reserved[r.Asset] = m.reserved[r.Asset].Sub(r.Amount)
	m.available[r.Asset] = m.available[r.Asset].Add(r.Amount)
	r.State = StateReleased
	m.clearHoldsLocked(r)
	return nil
}

// Credit adds cycle proceeds (or exposure unwind results) to available.
func (m *Manager) Credit(asset exchange.Asset, amount decimal.Decimal) error {
	if amount.IsNegative() {
		return ErrInvalidAmount
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.available[asset] = m.available[asset].Add(amount)
	m.credited[asset] = m.credited[asset].Add(amount)
	return nil
}

func (m *Manager) clearHoldsLocked(r *Reservation) {
	for _, ck := range r.ConflictKeys {
		if m.activeConflicts[ck] == r.ID {
			delete(m.activeConflicts, ck)
		}
	}
	m.triangleReserved[r.TriangleID] = m.triangleReserved[r.TriangleID].Sub(r.Amount)
	if m.triangleReserved[r.TriangleID].IsZero() {
		delete(m.triangleReserved, r.TriangleID)
	}
}

// Reset rebuilds the ledger to fresh initial balances, discarding every
// reservation and conflict hold (BL-10, paper reset). It does not alter
// any of the arithmetic above: Reserve/Settle/Release/Credit/
// CheckInvariants are untouched, and the maps this method replaces are
// the same ones those methods already read and write under m.mu. Callers
// must ensure no reservation is in flight when calling Reset (the paper
// engine must be paused with zero active simulations) — Reset does not
// itself detect or wait for in-flight activity.
func (m *Manager) Reset(initial map[exchange.Asset]decimal.Decimal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.available = make(map[exchange.Asset]decimal.Decimal, len(initial))
	m.reserved = make(map[exchange.Asset]decimal.Decimal)
	m.initial = make(map[exchange.Asset]decimal.Decimal, len(initial))
	m.credited = make(map[exchange.Asset]decimal.Decimal)
	m.consumed = make(map[exchange.Asset]decimal.Decimal)
	m.byKey = make(map[string]*Reservation)
	m.byID = make(map[string]*Reservation)
	m.activeConflicts = make(map[string]string)
	m.triangleReserved = make(map[string]decimal.Decimal)
	for a, v := range initial {
		m.available[a] = v
		m.initial[a] = v
	}
}

// Balance reports (available, reserved) for an asset.
func (m *Manager) Balance(asset exchange.Asset) (avail, reserved decimal.Decimal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.available[asset], m.reserved[asset]
}

// TriangleReserved reports active holds for a triangle (risk input).
func (m *Manager) TriangleReserved(id string) decimal.Decimal {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.triangleReserved[id]
}

// CheckInvariants verifies the ledger's conservation laws. A violation is
// the simulation-inconsistency breaker's trigger — the caller halts paper
// trading on error (docs/risk.md §4).
func (m *Manager) CheckInvariants() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sumActive := make(map[exchange.Asset]decimal.Decimal)
	for _, r := range m.byID {
		if r.State == StateActive {
			sumActive[r.Asset] = sumActive[r.Asset].Add(r.Amount)
		}
	}
	assets := make(map[exchange.Asset]struct{})
	for a := range m.available {
		assets[a] = struct{}{}
	}
	for a := range m.reserved {
		assets[a] = struct{}{}
	}
	for a := range assets {
		if m.available[a].IsNegative() {
			return fmt.Errorf("invariant: negative available %s %s", a, m.available[a])
		}
		if m.reserved[a].IsNegative() {
			return fmt.Errorf("invariant: negative reserved %s %s", a, m.reserved[a])
		}
		if !m.reserved[a].Equal(sumActive[a]) {
			return fmt.Errorf("invariant: reserved %s != active sum %s for %s", m.reserved[a], sumActive[a], a)
		}
		// available + reserved == initial + credited - consumed
		want := m.initial[a].Add(m.credited[a]).Sub(m.consumed[a])
		got := m.available[a].Add(m.reserved[a])
		if !got.Equal(want) {
			return fmt.Errorf("invariant: conservation broken for %s: %s != %s", a, got, want)
		}
	}
	return nil
}
