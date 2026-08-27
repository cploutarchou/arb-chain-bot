package tenancy

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryStore implements Store in memory (tests; database-less
// profiles). Organisation 1 always exists.
type MemoryStore struct {
	mu      sync.RWMutex
	orgs    map[int64]Org
	members map[int64]map[string]Membership
	rules   map[string]int64
	nextID  int64
	Now     func() time.Time
}

func NewMemoryStore() *MemoryStore {
	m := &MemoryStore{orgs: map[int64]Org{}, members: map[int64]map[string]Membership{}, rules: map[string]int64{}, nextID: 2, Now: time.Now}
	m.orgs[PlatformOrgID] = Org{ID: PlatformOrgID, Name: "platform", PackageCode: "institution", CustomerType: CustomerBusiness, RiskAckVersion: "1"}
	m.members[PlatformOrgID] = map[string]Membership{}
	return m
}

// SetRuleOrg records a rule's owner for OrgOfRule (tests).
func (m *MemoryStore) SetRuleOrg(ruleID string, orgID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rules[ruleID] = orgID
}

func (m *MemoryStore) ContextForUser(_ context.Context, userID string) (Context, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var best *Membership
	for _, ms := range m.members {
		if mem, ok := ms[userID]; ok {
			if best == nil || mem.CreatedAt.Before(best.CreatedAt) || (mem.CreatedAt.Equal(best.CreatedAt) && mem.OrgID < best.OrgID) {
				c := mem
				best = &c
			}
		}
	}
	if best == nil {
		return Context{}, ErrNoMembership
	}
	return Context{Org: m.orgs[best.OrgID], Membership: *best}, nil
}

func (m *MemoryStore) Org(_ context.Context, id int64) (Org, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	o, ok := m.orgs[id]
	if !ok {
		return Org{}, ErrUnknownOrg
	}
	return o, nil
}

func (m *MemoryStore) CreateOrg(_ context.Context, o Org, ownerUserID string) (Org, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o.ID = m.nextID
	m.nextID++
	if o.CreatedAt.IsZero() {
		o.CreatedAt = m.Now()
	}
	if o.CustomerType == "" {
		o.CustomerType = CustomerBusiness
	}
	m.orgs[o.ID] = o
	m.members[o.ID] = map[string]Membership{}
	if ownerUserID != "" {
		m.members[o.ID][ownerUserID] = Membership{OrgID: o.ID, UserID: ownerUserID, Role: RoleOwner, CreatedAt: o.CreatedAt}
	}
	return o, nil
}

func (m *MemoryStore) SetRiskAck(_ context.Context, orgID int64, version string, at time.Time, ip string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orgs[orgID]
	if !ok {
		return ErrUnknownOrg
	}
	o.RiskAckVersion, o.RiskAckAt, o.RiskAckIP = version, &at, ip
	m.orgs[orgID] = o
	return nil
}

func (m *MemoryStore) SetPackage(_ context.Context, orgID int64, packageCode string, trialEndsAt *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orgs[orgID]
	if !ok {
		return ErrUnknownOrg
	}
	o.PackageCode, o.TrialEndsAt = packageCode, trialEndsAt
	m.orgs[orgID] = o
	return nil
}

func (m *MemoryStore) SetOverride(_ context.Context, orgID int64, override []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orgs[orgID]
	if !ok {
		return ErrUnknownOrg
	}
	o.EntitlementsOverride = override
	m.orgs[orgID] = o
	return nil
}

func (m *MemoryStore) ListMembers(_ context.Context, orgID int64) ([]Membership, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ms, ok := m.members[orgID]
	if !ok {
		return nil, ErrUnknownOrg
	}
	out := make([]Membership, 0, len(ms))
	for _, mem := range ms {
		out = append(out, mem)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].UserID < out[j].UserID
	})
	return out, nil
}

func (m *MemoryStore) AddMember(_ context.Context, mem Membership) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ms, ok := m.members[mem.OrgID]
	if !ok {
		return ErrUnknownOrg
	}
	if !mem.Role.Valid() {
		return ErrInvalidRole
	}
	if _, dup := ms[mem.UserID]; dup {
		return ErrDuplicate
	}
	if mem.CreatedAt.IsZero() {
		mem.CreatedAt = m.Now()
	}
	ms[mem.UserID] = mem
	return nil
}

func (m *MemoryStore) SetMemberRole(_ context.Context, orgID int64, userID string, role Role) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ms, ok := m.members[orgID]
	if !ok {
		return ErrUnknownOrg
	}
	mem, ok := ms[userID]
	if !ok {
		return ErrNoMembership
	}
	if !role.Valid() {
		return ErrInvalidRole
	}
	if mem.Role == RoleOwner && role != RoleOwner && ownersLocked(ms, userID) == 0 {
		return ErrLastOwner
	}
	mem.Role = role
	ms[userID] = mem
	return nil
}

func (m *MemoryStore) RemoveMember(_ context.Context, orgID int64, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ms, ok := m.members[orgID]
	if !ok {
		return ErrUnknownOrg
	}
	mem, ok := ms[userID]
	if !ok {
		return ErrNoMembership
	}
	if mem.Role == RoleOwner && ownersLocked(ms, userID) == 0 {
		return ErrLastOwner
	}
	delete(ms, userID)
	return nil
}

func (m *MemoryStore) OrgOfRule(_ context.Context, ruleID string) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if id, ok := m.rules[ruleID]; ok {
		return id, nil
	}
	return PlatformOrgID, nil
}

func ownersLocked(ms map[string]Membership, except string) int {
	n := 0
	for id, mem := range ms {
		if id != except && mem.Role == RoleOwner {
			n++
		}
	}
	return n
}
