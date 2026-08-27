package screener

import (
	"context"
	"time"
)

// EventCloser is the optional EventStore extension the T-070 evaluator
// uses to close an alert (lifetime, peak) and to attach the paper
// execution id. Kept as a separate interface so the T-067 EventStore
// contract and its implementations are untouched; both the storage
// adapter and MemoryEventStore implement it.
type EventCloser interface {
	CloseEvent(ctx context.Context, id string, closedAt time.Time, lifetimeS int64, peakNetBps string) error
	SetEventExecution(ctx context.Context, id, paperExecutionID string) error
}

// EventCounter is the optional EventStore extension the auto-paper
// summary uses for the per-rule "alerts" figure without paging through
// ListEvents' capped result.
type EventCounter interface {
	CountEvents(ctx context.Context, ruleID string) (int64, error)
}

func (m *MemoryEventStore) CloseEvent(_ context.Context, id string, closedAt time.Time, lifetimeS int64, peakNetBps string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.rows {
		if m.rows[i].ID == id {
			t := closedAt
			m.rows[i].ClosedAt = &t
			m.rows[i].LifetimeS = lifetimeS
			m.rows[i].PeakNetBps = peakNetBps
			return nil
		}
	}
	return ErrNotFound
}

func (m *MemoryEventStore) SetEventExecution(_ context.Context, id, paperExecutionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.rows {
		if m.rows[i].ID == id {
			v := paperExecutionID
			m.rows[i].PaperExecutionID = &v
			return nil
		}
	}
	return ErrNotFound
}

func (m *MemoryEventStore) CountEvents(_ context.Context, ruleID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, e := range m.rows {
		if ruleID == "" || e.RuleID == ruleID {
			n++
		}
	}
	return n, nil
}
