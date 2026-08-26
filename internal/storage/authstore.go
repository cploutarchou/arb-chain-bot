package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
)

// AuthStore implements auth.UserStore and auth.SessionStore over
// PostgreSQL, replacing the in-memory bootstrap stores so sessions
// survive restarts (docs/security.md §4).
type AuthStore struct{ s *Store }

func (s *Store) Auth() *AuthStore { return &AuthStore{s: s} }

var (
	_ auth.UserStore    = (*AuthStore)(nil)
	_ auth.SessionStore = (*AuthStore)(nil)
)

func (a *AuthStore) UserByEmail(ctx context.Context, email string) (auth.User, error) {
	row := a.s.Pool.QueryRow(ctx, `
		SELECT id, email, password_hash, role, status = 'disabled'
		FROM users WHERE email = $1`, email)
	var u auth.User
	var role string
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &role, &u.Disabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.User{}, auth.ErrUnknownUser
		}
		return auth.User{}, err
	}
	u.Role = auth.Role(role)
	return u, nil
}

// UpsertUser creates or updates a user (bootstrap admin, user management).
func (a *AuthStore) UpsertUser(ctx context.Context, u auth.User) error {
	status := "active"
	if u.Disabled {
		status = "disabled"
	}
	_, err := a.s.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, role, status)
		VALUES ($1, $2, $2, $3, $4, $5)
		ON CONFLICT (email) DO UPDATE
		SET password_hash = EXCLUDED.password_hash,
		    role = EXCLUDED.role,
		    status = EXCLUDED.status`,
		u.ID, u.Email, u.PasswordHash, string(u.Role), status)
	return err
}

func (a *AuthStore) CreateSession(ctx context.Context, sess auth.Session) error {
	var ip any
	if sess.IP.IsValid() {
		ip = sess.IP.String()
	}
	_, err := a.s.Pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, created_at, expires_at, ip)
		VALUES ($1, $2, $3, $4, $5)`,
		sess.Token, sess.UserID, sess.CreatedAt, sess.ExpiresAt, ip)
	return err
}

func (a *AuthStore) SessionByToken(ctx context.Context, token string) (auth.Session, error) {
	row := a.s.Pool.QueryRow(ctx, `
		SELECT s.id, s.user_id, u.role, s.created_at, s.expires_at,
		       COALESCE(s.revoked_at, 'epoch'::timestamptz)
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id = $1`, token)
	var sess auth.Session
	var role string
	var revoked time.Time
	if err := row.Scan(&sess.Token, &sess.UserID, &role, &sess.CreatedAt, &sess.ExpiresAt, &revoked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.Session{}, auth.ErrInvalidSession
		}
		return auth.Session{}, err
	}
	sess.Role = auth.Role(role)
	if revoked.Unix() > 0 {
		sess.RevokedAt = revoked
	}
	return sess, nil
}

func (a *AuthStore) RevokeSession(ctx context.Context, token string, at time.Time) error {
	tag, err := a.s.Pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`, token, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return auth.ErrInvalidSession
	}
	return nil
}

func (a *AuthStore) RevokeUserSessions(ctx context.Context, userID string, at time.Time) error {
	_, err := a.s.Pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`, userID, at)
	return err
}
