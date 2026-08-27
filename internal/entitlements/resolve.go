package entitlements

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// PastDueGrace is how long a past_due organisation keeps full
// entitlements before turning read-only (packages.md §4).
const PastDueGrace = 7 * 24 * time.Hour

// TrialLength is the Operator trial on sign-up (packages.md §2).
const TrialLength = 14 * 24 * time.Hour

// Input is everything Resolve needs about one organisation. It is a
// plain value so the resolver has no storage dependency.
type Input struct {
	OrgID       int64
	PackageCode string // organisations.package_code
	Override    []byte // organisations.entitlements_override (nil = none)
	TrialEndsAt *time.Time

	// Subscription mirror (nil = no subscription row).
	SubStatus    string // trialing|active|past_due|paused|canceled|""
	PastDueSince *time.Time
	Now          time.Time
}

// Resolve builds the effective document: the package for the
// organisation's effective package code, deep-merged with the override,
// re-validated, then shaped by the subscription state. It never
// returns a document with execution.live = true: an override that
// tries is rejected with ErrLiveExecution and the caller decides
// whether to fall back (Resolver does: bare package, override ignored,
// error surfaced in the log).
func Resolve(in Input) (Entitlements, error) {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	effective, status := effectivePackage(in, now)
	base, ok := Package(effective)
	if !ok {
		return Entitlements{}, fmt.Errorf("%w: unknown package %q", ErrInvalid, effective)
	}
	merged := base
	if len(in.Override) > 0 {
		var err error
		merged, err = Merge(base, in.Override)
		if err != nil {
			return Entitlements{}, err
		}
	}
	if status.ReadOnly {
		merged = readOnly(merged)
	}
	merged.Status = status
	// Belt and braces: Validate already rejected live=true; this is the
	// last line before the document leaves the package.
	if merged.Execution.Live {
		return Entitlements{}, ErrLiveExecution
	}
	return merged, nil
}

// effectivePackage applies packages.md §4 transitions that do not need
// a webhook: trial expiry (day 15 -> Watch), a canceled/paused mirror
// (-> Watch), and the past-due grace clock.
func effectivePackage(in Input, now time.Time) (string, Status) {
	st := Status{Subscription: in.SubStatus, Effective: in.PackageCode}
	if st.Subscription == "" {
		st.Subscription = "none"
	}
	if in.TrialEndsAt != nil {
		st.TrialEndsAt = in.TrialEndsAt.UTC().Format(time.RFC3339)
	}
	switch in.SubStatus {
	case "active", "trialing":
		return in.PackageCode, st
	case "past_due":
		if in.PastDueSince != nil && now.Sub(*in.PastDueSince) > PastDueGrace {
			st.ReadOnly = true
		}
		return in.PackageCode, st
	case "canceled", "paused":
		if in.PackageCode != PackageWatch && in.OrgID != 1 {
			st.Effective = PackageWatch
			return PackageWatch, st
		}
		return in.PackageCode, st
	}
	// No subscription: a running trial keeps the trial package; an
	// expired one drops to Watch. No trial at all (platform org, or
	// an override-provisioned pilot) keeps package_code as stored.
	if in.TrialEndsAt != nil && !now.Before(*in.TrialEndsAt) && in.PackageCode != PackageWatch {
		st.Effective = PackageWatch
		return PackageWatch, st
	}
	return in.PackageCode, st
}

// readOnly is the past-due-beyond-grace shape (packages.md §4): alerts
// off, auto-paper off, API read only. Data stays visible.
func readOnly(e Entitlements) Entitlements {
	e.Alerts.PerDay = 0
	e.AutoPaper.Strategies = []string{}
	e.AutoPaper.MaxOpenPositions = 0
	e.API.Scopes = []string{"read"}
	return e
}

// Merge deep-merges a partial override document over base and
// re-validates the result. Objects merge key by key; arrays and
// scalars in the override replace the base value wholesale. Unknown
// keys anywhere fail validation (additionalProperties: false), and so
// does any attempt to set execution.live.
func Merge(base Entitlements, override []byte) (Entitlements, error) {
	var ov map[string]any
	if err := json.Unmarshal(override, &ov); err != nil {
		return Entitlements{}, fmt.Errorf("%w: override is not a JSON object: %v", ErrInvalid, err)
	}
	if _, has := ov["status"]; has {
		return Entitlements{}, fmt.Errorf("%w: override may not set status", ErrInvalid)
	}
	baseRaw, err := json.Marshal(base)
	if err != nil {
		return Entitlements{}, err
	}
	var bm map[string]any
	if err := json.Unmarshal(baseRaw, &bm); err != nil {
		return Entitlements{}, err
	}
	delete(bm, "status")
	merged := deepMerge(bm, ov)
	out, err := json.Marshal(merged)
	if err != nil {
		return Entitlements{}, err
	}
	return ValidateJSON(out)
}

func deepMerge(dst, src map[string]any) map[string]any {
	for k, v := range src {
		if sv, ok := v.(map[string]any); ok {
			if dv, ok := dst[k].(map[string]any); ok {
				dst[k] = deepMerge(dv, sv)
				continue
			}
		}
		dst[k] = v
	}
	return dst
}

// Source supplies Resolve inputs for an organisation (storage-backed
// in production, a map in tests).
type Source interface {
	EntitlementInput(ctx context.Context, orgID int64) (Input, error)
}

// Resolver caches resolved documents per organisation for TTL
// (packages.md §3: 60 s) and is invalidated by every subscription
// webhook. A failed override falls back to the bare package so a bad
// pilot document degrades service, never widens it.
type Resolver struct {
	Source Source
	TTL    time.Duration
	Now    func() time.Time
	// OnError is called when an override is rejected (log hook).
	OnError func(orgID int64, err error)

	mu    sync.Mutex
	cache map[int64]cached
}

type cached struct {
	doc     Entitlements
	expires time.Time
}

func NewResolver(src Source) *Resolver {
	return &Resolver{Source: src, TTL: 60 * time.Second, Now: time.Now, cache: map[int64]cached{}}
}

// For returns the organisation's effective entitlements.
func (r *Resolver) For(ctx context.Context, orgID int64) (Entitlements, error) {
	now := r.Now()
	r.mu.Lock()
	if c, ok := r.cache[orgID]; ok && now.Before(c.expires) {
		r.mu.Unlock()
		return c.doc, nil
	}
	r.mu.Unlock()

	in, err := r.Source.EntitlementInput(ctx, orgID)
	if err != nil {
		return Entitlements{}, err
	}
	in.Now = now
	doc, err := Resolve(in)
	if err != nil {
		if r.OnError != nil {
			r.OnError(orgID, err)
		}
		in.Override = nil
		doc, err = Resolve(in)
		if err != nil {
			return Entitlements{}, err
		}
	}
	r.mu.Lock()
	r.cache[orgID] = cached{doc: doc, expires: now.Add(r.TTL)}
	r.mu.Unlock()
	return doc, nil
}

// Invalidate drops the cached document (subscription webhook, override
// change, package change).
func (r *Resolver) Invalidate(orgID int64) {
	r.mu.Lock()
	delete(r.cache, orgID)
	r.mu.Unlock()
}

// MapSource is a Source over a map (tests, database-less profiles).
type MapSource struct {
	mu   sync.RWMutex
	rows map[int64]Input
}

func NewMapSource() *MapSource { return &MapSource{rows: map[int64]Input{}} }

func (m *MapSource) Set(in Input) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[in.OrgID] = in
}

func (m *MapSource) EntitlementInput(_ context.Context, orgID int64) (Input, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	in, ok := m.rows[orgID]
	if !ok {
		return Input{}, fmt.Errorf("%w: organisation %d has no entitlement input", ErrInvalid, orgID)
	}
	return in, nil
}
