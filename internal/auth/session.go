package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"
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
}

// Session is a server-side revocable session.
type Session struct {
	Token     string // opaque, 256-bit
	UserID    string
	Role      Role
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt time.Time
	IP        netip.Addr
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
	// Throttle locks out one (email, ip) pair after repeated failures;
	// IPThrottle caps total failures per source IP so rotating emails
	// does not evade the lockout (audit S-006). Either may be nil.
	Throttle   *Throttle
	IPThrottle *Throttle
	TTL        time.Duration // absolute session lifetime
	Now        func() time.Time
}

// Login verifies credentials and mints a session. Failures feed the
// throttles; throttled callers fail before password verification (no
// Argon2 work for a locked-out key).
func (m *Manager) Login(ctx context.Context, email, password string, ip netip.Addr) (Session, error) {
	key := email + "|" + ip.String()
	if m.Throttle != nil && !m.Throttle.Allow(key, m.Now()) {
		return Session{}, ErrThrottled
	}
	if m.IPThrottle != nil && !m.IPThrottle.Allow(ip.String(), m.Now()) {
		return Session{}, ErrThrottled
	}
	fail := func() {
		if m.Throttle != nil {
			m.Throttle.Fail(key, m.Now())
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
	token, err := NewToken(32)
	if err != nil {
		return Session{}, err
	}
	s := Session{
		Token:     token,
		UserID:    u.ID,
		Role:      u.Role,
		CreatedAt: m.Now(),
		ExpiresAt: m.Now().Add(m.TTL),
		IP:        ip,
	}
	// The store only ever sees the digest; the raw token exists in the
	// caller's cookie and nowhere else (audit S-005).
	stored := s
	stored.Token = HashToken(token)
	if err := m.Sessions.CreateSession(ctx, stored); err != nil {
		return Session{}, err
	}
	if m.Throttle != nil {
		m.Throttle.Reset(key)
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
