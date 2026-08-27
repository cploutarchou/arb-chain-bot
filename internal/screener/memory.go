package screener

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryRuleStore is a RuleStore for tests and DB-less dev profiles
// (mirrors MemoryStore's role for the versioned settings document: a
// profile with no database configured still gets a working, in-process
// screener rather than a nil-pointer panic on the first mutation).
type MemoryRuleStore struct {
	mu   sync.Mutex
	rows map[string]Rule
}

func NewMemoryRuleStore() *MemoryRuleStore { return &MemoryRuleStore{rows: map[string]Rule{}} }

func (m *MemoryRuleStore) ListRules(context.Context) ([]Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Rule, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *MemoryRuleStore) GetRule(_ context.Context, id string) (Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return Rule{}, ErrNotFound
	}
	return r, nil
}

// InsertRule/UpdateRule accept actor to satisfy RuleStore (the
// storage-backed screener_rules.updated_by column); this in-memory
// store has nowhere to persist it beyond the row itself, which Rule
// does not carry — dev/test profiles lose "who touched this rule" the
// same way they lose everything else on restart.
func (m *MemoryRuleStore) InsertRule(_ context.Context, r Rule, _ string) (Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[r.ID] = r
	return r, nil
}

func (m *MemoryRuleStore) UpdateRule(_ context.Context, r Rule, _ string) (Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[r.ID]; !ok {
		return Rule{}, ErrNotFound
	}
	m.rows[r.ID] = r
	return r, nil
}

func (m *MemoryRuleStore) DeleteRule(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[id]; !ok {
		return ErrNotFound
	}
	delete(m.rows, id)
	return nil
}

// MemoryEventStore is an EventStore for tests and DB-less dev profiles.
type MemoryEventStore struct {
	mu   sync.Mutex
	rows []Event
}

func NewMemoryEventStore() *MemoryEventStore { return &MemoryEventStore{} }

func (m *MemoryEventStore) ListEvents(_ context.Context, ruleID string, limit int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Event
	for i := len(m.rows) - 1; i >= 0; i-- { // newest first, matching the storage-backed ORDER BY opened_at DESC
		e := m.rows[i]
		if ruleID != "" && e.RuleID != ruleID {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *MemoryEventStore) InsertEvent(_ context.Context, e Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.rows {
		if existing.ID == e.ID {
			return nil // idempotent, matching the storage-backed ON CONFLICT DO NOTHING
		}
	}
	m.rows = append(m.rows, e)
	return nil
}

// MemoryTemplateStore is a TemplateStore for tests and DB-less dev
// profiles.
type MemoryTemplateStore struct {
	mu   sync.Mutex
	rows map[string]Template
}

func NewMemoryTemplateStore() *MemoryTemplateStore {
	return &MemoryTemplateStore{rows: map[string]Template{}}
}

func (m *MemoryTemplateStore) ListTemplates(_ context.Context, userID string) ([]Template, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Template
	for _, t := range m.rows {
		if t.UserID == userID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *MemoryTemplateStore) InsertTemplate(_ context.Context, t Template) (Template, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t.CreatedAt = time.Now().UTC()
	m.rows[t.ID] = t
	return t, nil
}

func (m *MemoryTemplateStore) DeleteTemplate(_ context.Context, userID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.rows[id]
	if !ok || t.UserID != userID {
		return ErrNotFound
	}
	delete(m.rows, id)
	return nil
}

// MemoryFundingStore is a FundingStore for tests and DB-less dev
// profiles.
type MemoryFundingStore struct {
	mu   sync.Mutex
	rows map[[3]string]FundingPoint // key: venue, base, at.Format(RFC3339Nano)
}

func NewMemoryFundingStore() *MemoryFundingStore {
	return &MemoryFundingStore{rows: map[[3]string]FundingPoint{}}
}

func (m *MemoryFundingStore) UpsertFunding(_ context.Context, venue Venue, base string, at time.Time, rate string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := [3]string{string(venue), base, at.UTC().Format(time.RFC3339Nano)}
	if _, ok := m.rows[key]; ok {
		return nil // ON CONFLICT DO NOTHING semantics
	}
	m.rows[key] = FundingPoint{At: at.UTC(), Rate: rate}
	return nil
}

func (m *MemoryFundingStore) ListFunding(_ context.Context, base string, venues []Venue, since time.Time) ([]FundingSeries, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	venueSet := map[Venue]bool{}
	for _, v := range venues {
		venueSet[v] = true
	}
	bySeries := map[[2]string]*FundingSeries{}
	var order [][2]string
	for key, pt := range m.rows {
		venue, b := Venue(key[0]), key[1]
		if base != "" && b != base {
			continue
		}
		if len(venueSet) > 0 && !venueSet[venue] {
			continue
		}
		if pt.At.Before(since) {
			continue
		}
		k := [2]string{key[0], b}
		series, ok := bySeries[k]
		if !ok {
			series = &FundingSeries{Venue: venue, Base: b}
			bySeries[k] = series
			order = append(order, k)
		}
		series.Points = append(series.Points, pt)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i][0] != order[j][0] {
			return order[i][0] < order[j][0]
		}
		return order[i][1] < order[j][1]
	})
	out := make([]FundingSeries, 0, len(order))
	for _, k := range order {
		series := *bySeries[k]
		sort.Slice(series.Points, func(i, j int) bool { return series.Points[i].At.Before(series.Points[j].At) })
		out = append(out, series)
	}
	return out, nil
}
