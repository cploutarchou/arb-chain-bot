package auth

import (
	"context"
	"errors"
	"strings"
	"time"
)

// MinPasswordLength is the minimum length for any password set through
// the console (self-service change or admin reset/create).
const MinPasswordLength = 12

var (
	ErrDuplicateEmail = errors.New("auth: email already registered")
	ErrLastAdmin      = errors.New("auth: cannot remove the last enabled admin")
	ErrSelfTarget     = errors.New("auth: cannot perform this action on your own account")
	ErrWeakPassword   = errors.New("auth: password must be at least 12 characters")
	ErrInvalidRole    = errors.New("auth: invalid role")
	ErrInvalidEmail   = errors.New("auth: invalid email")
)

// AdminStore is the storage surface for user management (BL-11). Both
// the pgx-backed AuthStore (internal/storage) and MemoryStore implement
// it directly, so AdminService works identically with or without a
// database.
type AdminStore interface {
	ListUsers(ctx context.Context) ([]User, error)
	UserByID(ctx context.Context, id string) (User, error)
	CreateUser(ctx context.Context, u User) error
	UpdateUserRole(ctx context.Context, id string, role Role) error
	SetUserDisabled(ctx context.Context, id string, disabled bool) error
	SetUserPassword(ctx context.Context, id, passwordHash string) error
}

// APIKeyRevoker is the minimal capability AdminService needs to cascade
// a disable onto any API key the account minted (audit S3/P1-12): a
// disabled account must not keep authenticating through a bearer
// credential nobody remembered to revoke by hand. Declared locally
// (rather than importing internal/apikey) so this package's dependency
// graph stays a leaf; the concrete store wired in production
// (apikey.Store, over pgx or in memory) already satisfies it.
type APIKeyRevoker interface {
	RevokeByOwner(ctx context.Context, userID string, at time.Time) (int, error)
}

// AdminService implements the user & role management business rules
// shared by every caller (web today; Telegram would use the same
// service — SKILL §52, §56). The HTTP layer enforces RBAC
// (PermUserManage) and CSRF; this service enforces the invariants that
// must hold regardless of caller: an admin can never lock themselves out
// or strip the platform of its last administrator, and any credential or
// role change revokes the target's live sessions immediately (audit
// S-005-adjacent: a role held by a still-valid session must never
// outlive the row that grants it).
type AdminService struct {
	Store    AdminStore
	Sessions SessionStore
	// Now and IDGen are injectable for deterministic tests; both default
	// to real implementations when nil.
	Now   func() time.Time
	IDGen func() string
	// PasswordThrottle bounds repeated wrong-current-password guesses on
	// ChangeOwnPassword (P3-13): unlike Login, that route was reachable
	// with an already-valid session and no rate limiting at all, so a
	// stolen session cookie could brute-force the current password
	// (needed to set a new one) without ever tripping Manager's login
	// throttle. Keyed per user id (the caller is already authenticated,
	// so email|ip is not the relevant dimension here). nil disables
	// throttling (tests that do not care may omit it).
	PasswordThrottle *Throttle
	// APIKeys, set only in profiles where the client API exists, cascades
	// a disable onto the account's bearer credentials (audit S3/P1-12).
	// nil is a legitimate "no API keys in this profile", not a bug.
	APIKeys APIKeyRevoker
	// AuditCascade records the cascade revoke above as its own event —
	// the caller's audit row for "user.disable" does not know a cascade
	// even happened. nil disables it (tests that do not care may omit
	// it); actor/action/entity mirror the shape callers already use for
	// their own audit calls (e.g. internal/api's s.audit).
	AuditCascade func(actor, action, entity string)
}

func (s *AdminService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *AdminService) newID() (string, error) {
	if s.IDGen != nil {
		return s.IDGen(), nil
	}
	return NewToken(16)
}

// ListUsers returns every account (no pagination: this is an operator
// roster, not a high-cardinality table). PasswordHash is stripped here
// (P3-6, defense in depth): the pgx-backed store never selects it, but
// MemoryStore.ListUsers returns whatever was stored, so a caller of this
// service must not rely on the backing store to have scrubbed it — the
// service boundary is the one place every caller (web today; Telegram
// per SKILL §52/§56 tomorrow) goes through.
func (s *AdminService) ListUsers(ctx context.Context) ([]User, error) {
	users, err := s.Store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	for i := range users {
		users[i].PasswordHash = ""
	}
	return users, nil
}

// UserByID resolves one account, PasswordHash stripped (P3-6): used by
// the console (a future single-account view) and by the API-key
// authentication path, which re-checks the owner's status on every
// request rather than trusting it once at mint time (audit S3/P1-12).
func (s *AdminService) UserByID(ctx context.Context, id string) (User, error) {
	u, err := s.Store.UserByID(ctx, id)
	if err != nil {
		return User{}, err
	}
	u.PasswordHash = ""
	return u, nil
}

func validEmail(email string) bool {
	if email == "" || len(email) > 254 {
		return false
	}
	at := strings.IndexByte(email, '@')
	// Must contain exactly one '@', with at least one character on each
	// side and a '.' somewhere after it — enough to reject obvious junk
	// without re-implementing RFC 5322.
	if at <= 0 || at != strings.LastIndexByte(email, '@') || at == len(email)-1 {
		return false
	}
	return strings.Contains(email[at+1:], ".")
}

// CreateUser is the console's "invite" action: it never overwrites an
// existing account (see AdminStore.CreateUser / ErrDuplicateEmail). The
// new account joins the platform organisation (operator staff).
func (s *AdminService) CreateUser(ctx context.Context, email string, role Role, password string) (User, error) {
	return s.createUser(ctx, email, role, password, 0, "")
}

// CreateUserInOrg creates the account directly inside a tenant
// organisation (audit S4 follow-up) instead of the platform one — the
// operator picks the organisation explicitly at creation time rather
// than creating a platform seat and then shuffling memberships. orgRole
// is the tenancy membership role ("ADMIN"/"MEMBER"/"VIEWER"); empty
// defaults to MEMBER. OWNER is refused here: organisation ownership is
// granted by CreateOrg behind the last-owner guard, not by this form.
func (s *AdminService) CreateUserInOrg(ctx context.Context, email string, role Role, password string, orgID int64, orgRole string) (User, error) {
	switch orgRole {
	case "":
		orgRole = "MEMBER"
	case "ADMIN", "MEMBER", "VIEWER":
	default:
		return User{}, ErrInvalidRole
	}
	if orgID <= 0 {
		return User{}, ErrInvalidRole
	}
	return s.createUser(ctx, email, role, password, orgID, orgRole)
}

func (s *AdminService) createUser(ctx context.Context, email string, role Role, password string, orgID int64, orgRole string) (User, error) {
	// P3-7: normalize case before validation and storage — "Alice@x.com"
	// and "alice@x.com" must be the SAME account (ErrDuplicateEmail), and
	// Login (which does not normalize) must find whichever case a user
	// registers with reliably rather than by accident of what a caller
	// happened to send.
	email = strings.ToLower(strings.TrimSpace(email))
	if !validEmail(email) {
		return User{}, ErrInvalidEmail
	}
	if !role.Valid() {
		return User{}, ErrInvalidRole
	}
	if len(password) < MinPasswordLength {
		return User{}, ErrWeakPassword
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	id, err := s.newID()
	if err != nil {
		return User{}, err
	}
	u := User{ID: id, Email: email, PasswordHash: hash, Role: role, CreatedAt: s.now(),
		JoinOrgID: orgID, JoinOrgRole: orgRole}
	if err := s.Store.CreateUser(ctx, u); err != nil {
		return User{}, err
	}
	u.PasswordHash = ""
	return u, nil
}

// UpdateUserRole changes a user's role. Self-targeting is always
// refused — an admin who wants to change their own role must have
// another admin do it, which also protects against accidental
// self-demotion via a misclick.
//
// The last-admin guard (a demotion must never leave zero enabled ADMIN
// accounts) is enforced ATOMICALLY inside Store.UpdateUserRole itself
// (P2-5: a single transaction/conditional UPDATE for the pgx store, one
// lock for MemoryStore) rather than here as a separate list-then-check —
// two round trips through this service could otherwise race two
// concurrent demotions of two DIFFERENT admins past each other, each
// observing "at least one other admin" and leaving none.
func (s *AdminService) UpdateUserRole(ctx context.Context, actorID, targetID string, role Role) (User, error) {
	if !role.Valid() {
		return User{}, ErrInvalidRole
	}
	if actorID == targetID {
		return User{}, ErrSelfTarget
	}
	target, err := s.Store.UserByID(ctx, targetID)
	if err != nil {
		return User{}, err
	}
	if err := s.Store.UpdateUserRole(ctx, targetID, role); err != nil {
		return User{}, err
	}
	if err := s.Sessions.RevokeUserSessions(ctx, targetID, s.now()); err != nil {
		return User{}, err
	}
	target.Role = role
	target.PasswordHash = "" // P3-6: never hand a hash back across the service boundary
	return target, nil
}

// SetUserDisabled disables or re-enables a user. Disabling yourself is
// always refused (you would lock yourself out with no console path
// back in); enabling is never self-targeted in practice since a
// disabled account cannot hold a live session to call this endpoint.
// The last-admin guard is atomic in Store.SetUserDisabled — see
// UpdateUserRole's comment (P2-5).
func (s *AdminService) SetUserDisabled(ctx context.Context, actorID, targetID string, disabled bool) (User, error) {
	if disabled && actorID == targetID {
		return User{}, ErrSelfTarget
	}
	target, err := s.Store.UserByID(ctx, targetID)
	if err != nil {
		return User{}, err
	}
	if err := s.Store.SetUserDisabled(ctx, targetID, disabled); err != nil {
		return User{}, err
	}
	target.Disabled = disabled
	if disabled {
		if err := s.Sessions.RevokeUserSessions(ctx, targetID, s.now()); err != nil {
			return User{}, err
		}
		// audit S3/P1-12: a disabled account must not keep authenticating
		// through an API key it minted earlier. The authenticate-time
		// owner-status check is the durable backstop if this ever fails
		// to run; this is what makes the row itself say so.
		if s.APIKeys != nil {
			n, err := s.APIKeys.RevokeByOwner(ctx, targetID, s.now())
			if err != nil {
				return User{}, err
			}
			if n > 0 && s.AuditCascade != nil {
				s.AuditCascade(actorID, "apikey.revoke_cascade", "user:"+targetID)
			}
		}
	}
	target.PasswordHash = "" // P3-6: never hand a hash back across the service boundary
	return target, nil
}

// SetUserPassword is an admin-triggered reset (unlike ChangeOwnPassword,
// it does not require the target's current password). It always revokes
// the target's live sessions — a leaked/reset credential must not leave
// an old session usable.
func (s *AdminService) SetUserPassword(ctx context.Context, targetID, password string) (User, error) {
	if len(password) < MinPasswordLength {
		return User{}, ErrWeakPassword
	}
	target, err := s.Store.UserByID(ctx, targetID)
	if err != nil {
		return User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	if err := s.Store.SetUserPassword(ctx, targetID, hash); err != nil {
		return User{}, err
	}
	if err := s.Sessions.RevokeUserSessions(ctx, targetID, s.now()); err != nil {
		return User{}, err
	}
	target.PasswordHash = ""
	return target, nil
}

// ChangeOwnPassword is the self-service path: it requires the caller's
// current password and, on success, revokes every session for that
// user — including the one making this request, forcing a fresh login
// with the new credential. Repeated wrong-current-password guesses are
// throttled per user id (P3-13), the same lockout shape Manager.Login
// already applies to unauthenticated login attempts.
func (s *AdminService) ChangeOwnPassword(ctx context.Context, userID, currentPassword, newPassword string) error {
	if s.PasswordThrottle != nil && !s.PasswordThrottle.Allow(userID, s.now()) {
		return ErrThrottled
	}
	if len(newPassword) < MinPasswordLength {
		return ErrWeakPassword
	}
	u, err := s.Store.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := VerifyPassword(currentPassword, u.PasswordHash); err != nil {
		if s.PasswordThrottle != nil {
			s.PasswordThrottle.Fail(userID, s.now())
		}
		return err
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.Store.SetUserPassword(ctx, userID, hash); err != nil {
		return err
	}
	if s.PasswordThrottle != nil {
		s.PasswordThrottle.Reset(userID)
	}
	return s.Sessions.RevokeUserSessions(ctx, userID, s.now())
}
