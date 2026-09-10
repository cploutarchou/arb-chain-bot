// Package tenancy defines organisations, memberships and the request-
// scoped organisation context (T-081). It has no dependencies on
// storage or HTTP so both the storage layer and the API can share it.
package tenancy

import (
	"context"
	"errors"
	"time"
)

// PlatformOrgID is the operator's own organisation (migration 000013
// inserts it as id 1). Rows written without an organisation in context
// default to it, so the single-tenant engine keeps working unchanged.
const PlatformOrgID int64 = 1

// Role is a membership role inside one organisation (packages.md §2
// "Roles"). It is distinct from auth.Role, the console's platform-wide
// RBAC role, which keeps gating what an account can do at all.
type Role string

const (
	RoleOwner  Role = "OWNER"
	RoleAdmin  Role = "ADMIN"
	RoleMember Role = "MEMBER"
	RoleViewer Role = "VIEWER"
)

func (r Role) Valid() bool {
	return r == RoleOwner || r == RoleAdmin || r == RoleMember || r == RoleViewer
}

// CanManage reports whether a membership role may change the
// organisation (members, billing, risk acknowledgement).
func (r Role) CanManage() bool { return r == RoleOwner || r == RoleAdmin }

// CustomerType is the B2B/consumer status recorded at sign-up
// (compliance review #11: the EU/UK withdrawal right depends on it).
type CustomerType string

const (
	CustomerConsumer CustomerType = "consumer"
	CustomerBusiness CustomerType = "business"
)

// Org is one organisation row.
type Org struct {
	ID                   int64        `json:"id"`
	Name                 string       `json:"name"`
	PackageCode          string       `json:"package_code"`
	Country              string       `json:"country,omitempty"`
	CustomerType         CustomerType `json:"customer_type"`
	RiskAckVersion       string       `json:"risk_ack_version,omitempty"`
	RiskAckAt            *time.Time   `json:"risk_ack_at,omitempty"`
	RiskAckIP            string       `json:"-"`
	TrialEndsAt          *time.Time   `json:"trial_ends_at,omitempty"`
	EntitlementsOverride []byte       `json:"-"`
	CreatedAt            time.Time    `json:"created_at"`
}

// Membership links a user to an organisation with a role.
type Membership struct {
	OrgID     int64     `json:"org_id"`
	UserID    string    `json:"user_id"`
	Email     string    `json:"email,omitempty"`
	Role      Role      `json:"role"`
	Suspended bool      `json:"suspended"`
	CreatedAt time.Time `json:"created_at"`
}

// Context is what the API attaches to every authenticated request:
// the caller's organisation and role in it.
type Context struct {
	Org        Org
	Membership Membership
}

var (
	ErrNoMembership  = errors.New("tenancy: user belongs to no organisation")
	ErrUnknownOrg    = errors.New("tenancy: unknown organisation")
	ErrDuplicate     = errors.New("tenancy: already a member")
	ErrInvalidRole   = errors.New("tenancy: invalid role")
	ErrLastOwner     = errors.New("tenancy: cannot remove the last owner")
	ErrInvalidAck    = errors.New("tenancy: invalid risk acknowledgement")
	ErrRiskAckNeeded = errors.New("tenancy: risk acknowledgement required")
)

// Store is the persistence surface (pgx in internal/storage, memory
// below for tests and database-less profiles).
type Store interface {
	// ContextForUser resolves the organisation the user acts in by
	// default: the first entry of ContextsForUser. Because the platform
	// organisation sorts last there, a member of both a tenant
	// organisation and the platform acts in the tenant organisation
	// unless the request names the platform explicitly (the API's
	// X-Org-ID header); the platform is the default only for accounts
	// that belong to nothing else (operator staff).
	ContextForUser(ctx context.Context, userID string) (Context, error)
	// ContextsForUser returns every membership the user holds with its
	// organisation: tenant organisations first, oldest membership
	// first, the platform organisation last. Empty when the user
	// belongs to nothing.
	ContextsForUser(ctx context.Context, userID string) ([]Context, error)
	// ListOrgIDs returns every organisation id, ascending. Background
	// work that must run once per tenant (the nightly screener reports)
	// iterates it; the platform organisation is always included.
	ListOrgIDs(ctx context.Context) ([]int64, error)
	Org(ctx context.Context, id int64) (Org, error)
	CreateOrg(ctx context.Context, o Org, ownerUserID string) (Org, error)
	SetRiskAck(ctx context.Context, orgID int64, version string, at time.Time, ip string) error
	SetPackage(ctx context.Context, orgID int64, packageCode string, trialEndsAt *time.Time) error
	SetOverride(ctx context.Context, orgID int64, override []byte) error
	ListMembers(ctx context.Context, orgID int64) ([]Membership, error)
	AddMember(ctx context.Context, m Membership) error
	SetMemberRole(ctx context.Context, orgID int64, userID string, role Role) error
	RemoveMember(ctx context.Context, orgID int64, userID string) error
	// MembershipFor resolves one (org_id, user_id) membership row,
	// ErrNoMembership if the user does not currently belong to the
	// organisation. Used to verify an API key's owner still holds a
	// live seat in the key's organisation at authentication time (audit
	// S3/P1-12), independent of which organisation ContextForUser would
	// pick by default.
	MembershipFor(ctx context.Context, orgID int64, userID string) (Membership, error)
	// OrgOfRule returns the organisation owning a screener rule (the
	// engine has no request context; it resolves entitlements from the
	// rule it is about to act on).
	OrgOfRule(ctx context.Context, ruleID string) (int64, error)
}

type ctxKey int

const orgKey ctxKey = 1

// WithOrg scopes ctx to one organisation. Storage queries read it back
// with OrgFrom; API middleware sets it for every authenticated request.
func WithOrg(ctx context.Context, orgID int64) context.Context {
	return context.WithValue(ctx, orgKey, orgID)
}

// OrgFrom returns the organisation in scope. ok=false means "no scope":
// engine/background callers that must see every organisation's rows.
func OrgFrom(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(orgKey).(int64)
	return id, ok && id > 0
}

// OrgOrPlatform returns the scoped organisation or the platform one —
// the id to write on a row created without an explicit scope.
func OrgOrPlatform(ctx context.Context) int64 {
	if id, ok := OrgFrom(ctx); ok {
		return id
	}
	return PlatformOrgID
}
