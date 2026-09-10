package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// Role is the RBAC role (SKILL.md §52).
type Role string

const (
	RoleAdmin    Role = "ADMIN"
	RoleOperator Role = "OPERATOR"
	RoleViewer   Role = "VIEWER"
)

func (r Role) Valid() bool { return r == RoleAdmin || r == RoleOperator || r == RoleViewer }

// User is the authentication-relevant user shape.
type User struct {
	ID           string
	Email        string
	PasswordHash string
	Role         Role
	Disabled     bool
	CreatedAt    time.Time
	// PlatformAdmin marks the operator's own staff (users.platform_admin,
	// migration 000013). It unlocks the exchange-credential vault group
	// and the system routes; no package, membership role or webhook can
	// set it (compliance review 2026-08-27 #1).
	PlatformAdmin bool
	// JoinOrgID/JoinOrgRole steer where a BRAND-NEW account lands at
	// creation (audit S4 follow-up): the users console used to join
	// every account to the platform organisation (id 1), so creating a
	// tenant's operator silently handed them a platform seat. Zero
	// JoinOrgID keeps the historical behaviour (platform organisation,
	// role mapped from the console role); a positive JoinOrgID joins
	// that organisation with JoinOrgRole instead — validated by the
	// caller (the API layer, which can see tenancy) and honoured inside
	// the same transaction that inserts the user. OWNER is refused
	// there: organisation ownership is granted by CreateOrg and guarded
	// by the last-owner protection, not by an account-creation form.
	JoinOrgID   int64
	JoinOrgRole string
}

// Session is a server-side revocable session.
type Session struct {
	Token  string // opaque, 256-bit
	UserID string
	Role   Role
	// PlatformAdmin mirrors User.PlatformAdmin at validation time (the
	// pgx store joins users on every lookup, so a revoked flag takes
	// effect on the next request).
	PlatformAdmin bool
	CreatedAt     time.Time
	ExpiresAt     time.Time
	RevokedAt     time.Time
	IP            netip.Addr
}

// Stores are small interfaces so the pgx implementation (storage layer)
// and the in-memory implementation (tests, bootstrap) are interchangeable.
type UserStore interface {
	UserByEmail(ctx context.Context, email string) (User, error)
}

type SessionStore interface {
	CreateSession(ctx context.Context, s Session) error
	SessionByToken(ctx context.Context, token string) (Session, error)
	RevokeSession(ctx context.Context, token string, at time.Time) error
	RevokeUserSessions(ctx context.Context, userID string, at time.Time) error
}

// HashStore persists upgraded password hashes. Both backing stores
// (pgx AuthStore, MemoryStore) already implement it via the admin
// service's SetUserPassword.
type HashStore interface {
	SetUserPassword(ctx context.Context, id, passwordHash string) error
}

var (
	ErrUnknownUser     = errors.New("auth: unknown user")
	ErrUserDisabled    = errors.New("auth: user disabled")
	ErrInvalidSession  = errors.New("auth: invalid session")
	ErrSessionExpired  = errors.New("auth: session expired")
	ErrSessionRevoked  = errors.New("auth: session revoked")
	ErrThrottled       = errors.New("auth: too many failed attempts")
	ErrInvalidCSRF     = errors.New("auth: invalid CSRF token")
	ErrInvalidPassword = ErrPasswordMismatch
)

// HashToken returns the hex SHA-256 digest of a session token. Stores
// index sessions by digest only (audit S-005): a leaked sessions table
// then contains no usable bearer credentials, and the lookup stays a
// constant-time key compare.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Manager owns login/logout/validation.
type Manager struct {
	Users    UserStore
	Sessions SessionStore
	// Throttle and IPThrottle are two INDEPENDENT limiters, both
	// consulted on every attempt (audit S1/P1-10): Throttle is keyed on
	// the account alone, so an attacker rotating source addresses
	// against one target account cannot get a fresh allowance per
	// address; IPThrottle is keyed on the caller's address alone (as
	// resolved by the caller — behind the documented reverse proxy that
	// is api.Server.clientAddr, trusted-proxy aware), so one address
	// spraying many accounts cannot evade the account limiter by
	// rotating emails. Either may be nil. Only Throttle resets on a
	// successful login: resetting IPThrottle on one account's success
	// would undo the protection it gives every OTHER account behind a
	// shared address (NAT, or the reverse proxy this fix exists for).
	Throttle   *Throttle
	IPThrottle *Throttle
	TTL        time.Duration // absolute session lifetime
	Now        func() time.Time
	// HashStore, when set, receives a re-hash of the presented password
	// after a successful login whose stored row still carries parameters
	// weaker than the current ones (S12). Optional: nil leaves the row as
	// it is until the next password change.
	HashStore HashStore
}

// Login verifies credentials and mints a session. Failures feed the
// throttles; throttled callers fail before password verification (no
// Argon2 work for a locked-out key).
func (m *Manager) Login(ctx context.Context, email, password string, ip netip.Addr) (Session, error) {
	if m.Throttle != nil && !m.Throttle.Allow(email, m.Now()) {
		return Session{}, ErrThrottled
	}
	if m.IPThrottle != nil && !m.IPThrottle.Allow(ip.String(), m.Now()) {
		return Session{}, ErrThrottled
	}
	fail := func() {
		if m.Throttle != nil {
			m.Throttle.Fail(email, m.Now())
		}
		if m.IPThrottle != nil {
			m.IPThrottle.Fail(ip.String(), m.Now())
		}
	}
	u, err := m.Users.UserByEmail(ctx, email)
	if err != nil {
		// Burn comparable time so unknown emails are not distinguishable
		// by response latency, then record the failure.
		_ = VerifyPassword(password, fakeHash)
		fail()
		return Session{}, ErrUnknownUser
	}
	if err := VerifyPassword(password, u.PasswordHash); err != nil {
		fail()
		return Session{}, ErrInvalidPassword
	}
	if u.Disabled {
		fail()
		return Session{}, ErrUserDisabled
	}
	// Transparent parameter upgrade (S12): the row verified, so the
	// password is known here; if it was stored with weaker parameters
	// than today's, rewrite it now. Best-effort by design — a failed
	// write must not lock a valid credential out, and the next login
	// retries the upgrade.
	if m.HashStore != nil && NeedsRehash(u.PasswordHash) {
		if hash, err := HashPassword(password); err == nil {
			_ = m.HashStore.SetUserPassword(ctx, u.ID, hash)
		}
	}
	token, err := NewToken(32)
	if err != nil {
		return Session{}, err
	}
	s := Session{
		Token:         token,
		UserID:        u.ID,
		Role:          u.Role,
		PlatformAdmin: u.PlatformAdmin,
		CreatedAt:     m.Now(),
		ExpiresAt:     m.Now().Add(m.TTL),
		IP:            ip,
	}
	// The store only ever sees the digest; the raw token exists in the
	// caller's cookie and nowhere else (audit S-005).
	stored := s
	stored.Token = HashToken(token)
	if err := m.Sessions.CreateSession(ctx, stored); err != nil {
		return Session{}, err
	}
	if m.Throttle != nil {
		m.Throttle.Reset(email)
	}
	return s, nil
}

// Validate resolves a session token to an authenticated principal.
func (m *Manager) Validate(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrInvalidSession
	}
	s, err := m.Sessions.SessionByToken(ctx, HashToken(token))
	if err != nil {
		return Session{}, ErrInvalidSession
	}
	now := m.Now()
	if !s.RevokedAt.IsZero() && !s.RevokedAt.After(now) {
		return Session{}, ErrSessionRevoked
	}
	if now.After(s.ExpiresAt) {
		return Session{}, ErrSessionExpired
	}
	return s, nil
}

// Logout revokes the session.
func (m *Manager) Logout(ctx context.Context, token string) error {
	return m.Sessions.RevokeSession(ctx, HashToken(token), m.Now())
}

// fakeHash equalizes timing for unknown users (any valid-format hash).
var fakeHash = func() string {
	h, err := HashPassword("timing-equalizer-not-a-real-credential")
	if err != nil {
		panic(err)
	}
	return h
}()

// MemoryStore implements both stores in memory (tests + first boot before
// the storage layer wires pgx implementations).
type MemoryStore struct {
	mu       sync.RWMutex
	users    map[string]User    // by email
	sessions map[string]Session // by token
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{users: make(map[string]User), sessions: make(map[string]Session)}
}

func (s *MemoryStore) AddUser(u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[u.Email] = u
}

func (s *MemoryStore) UserByEmail(_ context.Context, email string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[email]
	if !ok {
		return User{}, ErrUnknownUser
	}
	return u, nil
}

func (s *MemoryStore) CreateSession(_ context.Context, sess Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sess.Token] = sess
	return nil
}

func (s *MemoryStore) SessionByToken(_ context.Context, token string) (Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[token]
	if !ok {
		return Session{}, ErrInvalidSession
	}
	return sess, nil
}

func (s *MemoryStore) RevokeSession(_ context.Context, token string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[token]
	if !ok {
		return ErrInvalidSession
	}
	sess.RevokedAt = at
	s.sessions[token] = sess
	return nil
}

func (s *MemoryStore) RevokeUserSessions(_ context.Context, userID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tok, sess := range s.sessions {
		if sess.UserID == userID && sess.RevokedAt.IsZero() {
			sess.RevokedAt = at
			s.sessions[tok] = sess
		}
	}
	return nil
}

// The methods below make MemoryStore also satisfy AdminStore (BL-11), so
// the users/roles console API works identically with or without a
// database (docs/security.md §4). users is keyed by email; ID lookups
// scan it — the admin user list is small (operator-managed accounts),
// so this trades a map for simplicity rather than performance.

func (s *MemoryStore) ListUsers(_ context.Context) ([]User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]User, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u)
	}
	// Match AuthStore's (pgx) ORDER BY created_at ASC, email ASC — a map
	// iteration order would otherwise reshuffle the console's user
	// roster on every poll.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].Email < out[j].Email
	})
	return out, nil
}

func (s *MemoryStore) UserByID(_ context.Context, id string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.ID == id {
			return u, nil
		}
	}
	return User{}, ErrUnknownUser
}

// CreateUser inserts a brand-new user; a duplicate email fails closed
// rather than silently overwriting an existing account's credentials
// (that is what AddUser/UpsertUser-style methods are for — CreateUser is
// the console's "invite" action and must never be a privilege-escalation
// primitive).
func (s *MemoryStore) CreateUser(_ context.Context, u User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.users[u.Email]; exists {
		return ErrDuplicateEmail
	}
	s.users[u.Email] = u
	return nil
}

// enabledAdminCountLocked counts enabled ADMIN accounts other than
// excludeID. Callers must hold s.mu.
func (s *MemoryStore) enabledAdminCountLocked(excludeID string) int {
	n := 0
	for _, u := range s.users {
		if u.Role == RoleAdmin && !u.Disabled && u.ID != excludeID {
			n++
		}
	}
	return n
}

// UpdateUserRole changes a user's role, refusing (ErrLastAdmin) a
// demotion that would leave zero enabled ADMIN accounts. The read
// (current role/disabled + the other-admins count) and the write happen
// under the SAME lock acquisition (P2-5): AdminService used to do this
// as list-then-decide-then-call, two independent calls into this store
// with the lock released in between, letting two concurrent demotions of
// two different admins each observe "someone else is still an admin".
func (s *MemoryStore) UpdateUserRole(_ context.Context, id string, role Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for email, u := range s.users {
		if u.ID == id {
			if u.Role == RoleAdmin && !u.Disabled && role != RoleAdmin && s.enabledAdminCountLocked(id) == 0 {
				return ErrLastAdmin
			}
			u.Role = role
			// S8: platform_admin follows the console role on every
			// change, matching CreateUser's insert-time rule (see the
			// pgx store's UpdateUserRole for the rationale).
			u.PlatformAdmin = role == RoleAdmin
			s.users[email] = u
			return nil
		}
	}
	return ErrUnknownUser
}

// SetUserDisabled disables or re-enables a user with the same atomic
// last-admin guard as UpdateUserRole (P2-5) when disabling one.
func (s *MemoryStore) SetUserDisabled(_ context.Context, id string, disabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for email, u := range s.users {
		if u.ID == id {
			if disabled && u.Role == RoleAdmin && !u.Disabled && s.enabledAdminCountLocked(id) == 0 {
				return ErrLastAdmin
			}
			u.Disabled = disabled
			s.users[email] = u
			return nil
		}
	}
	return ErrUnknownUser
}

func (s *MemoryStore) SetUserPassword(_ context.Context, id, passwordHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for email, u := range s.users {
		if u.ID == id {
			u.PasswordHash = passwordHash
			s.users[email] = u
			return nil
		}
	}
	return ErrUnknownUser
}
