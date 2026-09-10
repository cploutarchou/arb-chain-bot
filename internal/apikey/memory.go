package apikey

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryStore implements Store in memory (tests, database-less
// profiles).
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]Key
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{rows: map[string]Key{}} }

func (m *MemoryStore) CreateKey(_ context.Context, k Key) (Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.rows {
		if existing.Prefix == k.Prefix {
			return Key{}, ErrPrefixCollision
		}
	}
	m.rows[k.ID] = k
	return k, nil
}

func (m *MemoryStore) ListKeys(_ context.Context, orgID int64) ([]Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Key, 0, len(m.rows))
	for _, k := range m.rows {
		if k.OrgID == orgID {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *MemoryStore) ByPrefix(_ context.Context, prefix string) (Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range m.rows {
		if k.Prefix == prefix {
			return k, nil
		}
	}
	return Key{}, ErrNotFound
}

func (m *MemoryStore) RevokeKey(_ context.Context, orgID int64, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.rows[id]
	if !ok || k.OrgID != orgID {
		return ErrNotFound
	}
	t := at
	k.RevokedAt = &t
	m.rows[id] = k
	return nil
}

func (m *MemoryStore) Touch(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.rows[id]
	if !ok {
		return ErrNotFound
	}
	t := at
	k.LastUsedAt = &t
	m.rows[id] = k
	return nil
}

// RevokeByOwner revokes every live key owned by userID, across every
// organisation (audit S3/P1-12: cascades an account disable).
func (m *MemoryStore) RevokeByOwner(_ context.Context, userID string, at time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, k := range m.rows {
		if k.UserID == userID && k.RevokedAt == nil {
			t := at
			k.RevokedAt = &t
			m.rows[id] = k
			n++
		}
	}
	return n, nil
}

// RevokeByMembership revokes every live key owned by userID scoped to
// orgID (audit S3/P1-12: cascades a membership removal).
func (m *MemoryStore) RevokeByMembership(_ context.Context, orgID int64, userID string, at time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, k := range m.rows {
		if k.OrgID == orgID && k.UserID == userID && k.RevokedAt == nil {
			t := at
			k.RevokedAt = &t
			m.rows[id] = k
			n++
		}
	}
	return n, nil
}
