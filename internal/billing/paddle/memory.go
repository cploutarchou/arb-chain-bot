package paddle

import (
	"context"
	"sync"
	"time"
)

// MemoryStore implements Store in memory (tests; database-less profiles
// where billing is simply unconfigured).
type MemoryStore struct {
	mu     sync.Mutex
	events map[string]*time.Time // event id -> processed_at
	subs   map[int64]Subscription
	prices map[string]Price
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{events: map[string]*time.Time{}, subs: map[int64]Subscription{}, prices: map[string]Price{}}
}

func (m *MemoryStore) RecordEvent(_ context.Context, ev Event) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if processed, seen := m.events[ev.EventID]; seen {
		return processed == nil, nil
	}
	m.events[ev.EventID] = nil
	return true, nil
}

func (m *MemoryStore) MarkProcessed(_ context.Context, eventID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events[eventID] = &at
	return nil
}

// Events returns the number of stored event ids (tests).
func (m *MemoryStore) Events() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

func (m *MemoryStore) Subscription(_ context.Context, orgID int64) (Subscription, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[orgID]
	return s, ok, nil
}

func (m *MemoryStore) SubscriptionByPaddleID(_ context.Context, id string) (Subscription, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.subs {
		if s.SubscriptionID == id && id != "" {
			return s, true, nil
		}
	}
	return Subscription{}, false, nil
}

func (m *MemoryStore) UpsertSubscription(_ context.Context, s Subscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.subs[s.OrgID]; ok {
		if s.CustomerID == "" {
			s.CustomerID = cur.CustomerID
		}
		if s.SubscriptionID == "" {
			s.SubscriptionID = cur.SubscriptionID
		}
	}
	m.subs[s.OrgID] = s
	return nil
}

func (m *MemoryStore) PackageForPrice(_ context.Context, priceID string) (string, string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.prices[priceID]
	return p.PackageCode, p.Interval, ok, nil
}

func (m *MemoryStore) SetPrice(_ context.Context, priceID, packageCode, interval string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prices[priceID] = Price{PriceID: priceID, PackageCode: packageCode, Interval: interval}
	return nil
}

func (m *MemoryStore) ListPrices(context.Context) ([]Price, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Price, 0, len(m.prices))
	for _, p := range m.prices {
		out = append(out, p)
	}
	return out, nil
}
