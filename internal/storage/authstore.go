package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/tenancy"
)

// AuthStore implements auth.UserStore and auth.SessionStore over
// PostgreSQL, replacing the in-memory bootstrap stores so sessions
// survive restarts (docs/security.md §4).
type AuthStore struct{ s *Store }

func (s *Store) Auth() *AuthStore { return &AuthStore{s: s} }

var (
	_ auth.UserStore    = (*AuthStore)(nil)
	_ auth.SessionStore = (*AuthStore)(nil)
	_ auth.AdminStore   = (*AuthStore)(nil)
)

func (a *AuthStore) UserByEmail(ctx context.Context, email string) (auth.User, error) {
	row := a.s.Pool.QueryRow(ctx, `
		SELECT id, email, password_hash, role, status = 'disabled', platform_admin
		FROM users WHERE email = $1`, email)
	var u auth.User
	var role string
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &role, &u.Disabled, &u.PlatformAdmin); err != nil {
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
	tx, err := a.s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The bootstrap admin is the operator: platform_admin is set on
	// insert and left alone on update (the console may have changed
	// it). A brand-new row joins the platform organisation as OWNER.
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, role, status, platform_admin)
		VALUES ($1, $2, $2, $3, $4, $5, $6)
		ON CONFLICT (email) DO UPDATE
		SET password_hash = EXCLUDED.password_hash,
		    role = EXCLUDED.role,
		    status = EXCLUDED.status
		RETURNING id`,
		u.ID, u.Email, u.PasswordHash, string(u.Role), status, u.PlatformAdmin || u.Role == auth.RoleAdmin).Scan(&id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO memberships (org_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (org_id, user_id) DO NOTHING`, tenancy.PlatformOrgID, id, string(platformRoleFor(u.Role))); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// platformRoleFor maps the console RBAC role onto the membership role
// a platform-staff account gets in organisation 1 (migration 000013
// uses the same mapping for the backfill).
func platformRoleFor(r auth.Role) tenancy.Role {
	switch r {
	case auth.RoleAdmin:
		return tenancy.RoleOwner
	case auth.RoleOperator:
		return tenancy.RoleAdmin
	default:
		return tenancy.RoleViewer
	}
}

// ListUsers returns every account, oldest first, for the console user
// roster (BL-11). Password hashes are never selected — callers of this
// method never need them.
func (a *AuthStore) ListUsers(ctx context.Context) ([]auth.User, error) {
	rows, err := a.s.Pool.Query(ctx, `
		SELECT id, email, role, status = 'disabled', created_at, platform_admin
		FROM users ORDER BY created_at ASC, email ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auth.User
	for rows.Next() {
		var u auth.User
		var role string
		if err := rows.Scan(&u.ID, &u.Email, &role, &u.Disabled, &u.CreatedAt, &u.PlatformAdmin); err != nil {
			return nil, err
		}
		u.Role = auth.Role(role)
		out = append(out, u)
	}
	return out, rows.Err()
}

// UserByID resolves one account by primary key (existence checks, and
// the self-service password-change path, both need this — UserByEmail
// alone cannot serve them).
func (a *AuthStore) UserByID(ctx context.Context, id string) (auth.User, error) {
	row := a.s.Pool.QueryRow(ctx, `
		SELECT id, email, password_hash, role, status = 'disabled', created_at, platform_admin
		FROM users WHERE id = $1`, id)
	var u auth.User
	var role string
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &role, &u.Disabled, &u.CreatedAt, &u.PlatformAdmin); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.User{}, auth.ErrUnknownUser
		}
		return auth.User{}, err
	}
	u.Role = auth.Role(role)
	return u, nil
}

// CreateUser inserts a brand-new account and fails closed on a
// duplicate email — unlike UpsertUser (bootstrap admin convenience),
// this must never silently overwrite an existing user's credentials or
// role (that would be a privilege-escalation primitive, not an "invite"
// action).
//
// Accounts created here are the operator's console accounts (the
// users & roles console is platform-admin only), so they join the
// platform organisation; platform_admin is granted only to ADMINs,
// never inferred later. Tenant users are created through the
// organisation membership routes instead.
func (a *AuthStore) CreateUser(ctx context.Context, u auth.User) error {
	tx, err := a.s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, role, status, created_at, platform_admin)
		VALUES ($1, $2, $2, $3, $4, 'active', $5, $6)
		ON CONFLICT (email) DO NOTHING`,
		u.ID, u.Email, u.PasswordHash, string(u.Role), u.CreatedAt, u.PlatformAdmin || u.Role == auth.RoleAdmin)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return auth.ErrDuplicateEmail
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, $3)
		ON CONFLICT (org_id, user_id) DO NOTHING`, tenancy.PlatformOrgID, u.ID, string(platformRoleFor(u.Role))); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// lastAdminGuardRows locks, in one deterministic order (ORDER BY id),
// every row the last-admin check needs: the target row itself plus every
// currently-enabled admin row. Locking the target and the admin set in a
// SINGLE statement — rather than two separate `SELECT ... FOR UPDATE`
// queries — is what makes this deadlock-safe: two concurrent
// transactions targeting DIFFERENT admins (T1: demote A, T2: demote B)
// would otherwise be free to lock A-then-B and B-then-A respectively.
// Ordering by id gives every transaction the same lock-acquisition
// order regardless of which admin it targets, so they queue instead of
// deadlocking (P2-5).
func lastAdminGuardRows(ctx context.Context, tx pgxIface, id string) (targetRole string, targetDisabled bool, targetFound bool, otherEnabledAdmins int, err error) {
	rows, err := tx.Query(ctx, `
		SELECT id, role, status = 'disabled' FROM users
		WHERE id = $1 OR (role = 'ADMIN' AND status <> 'disabled')
		ORDER BY id
		FOR UPDATE`, id)
	if err != nil {
		return "", false, false, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var rowID, role string
		var disabled bool
		if err := rows.Scan(&rowID, &role, &disabled); err != nil {
			return "", false, false, 0, err
		}
		if rowID == id {
			targetRole, targetDisabled, targetFound = role, disabled, true
		}
		if role == "ADMIN" && !disabled && rowID != id {
			otherEnabledAdmins++
		}
	}
	return targetRole, targetDisabled, targetFound, otherEnabledAdmins, rows.Err()
}

// pgxIface is the subset of pgx.Tx (and *pgxpool.Pool, for tests that
// bypass a transaction) this package's transactional helpers need.
type pgxIface interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// UpdateUserRole changes a user's role, refusing (auth.ErrLastAdmin) a
// demotion that would leave zero enabled ADMIN accounts. The guard is
// enforced INSIDE the same transaction as the write (P2-5): the earlier
// shape — AdminService listing users, deciding, then calling this method
// — was check-then-act across two independent round trips, so two
// concurrent demotions of two DIFFERENT admins could each see "at least
// one other admin" and both succeed, leaving none.
func (a *AuthStore) UpdateUserRole(ctx context.Context, id string, role auth.Role) error {
	tx, err := a.s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	curRole, curDisabled, found, otherAdmins, err := lastAdminGuardRows(ctx, tx, id)
	if err != nil {
		return err
	}
	if !found {
		return auth.ErrUnknownUser
	}
	if curRole == string(auth.RoleAdmin) && !curDisabled && role != auth.RoleAdmin && otherAdmins == 0 {
		return auth.ErrLastAdmin
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET role = $2 WHERE id = $1`, id, string(role)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetUserDisabled disables or re-enables a user, with the same atomic
// last-admin guard as UpdateUserRole (P2-5) when disabling one.
func (a *AuthStore) SetUserDisabled(ctx context.Context, id string, disabled bool) error {
	status := "active"
	if disabled {
		status = "disabled"
	}
	if !disabled {
		// Enabling never removes an admin from the enabled set, so the
		// guard is unnecessary — a plain conditional UPDATE is enough.
		tag, err := a.s.Pool.Exec(ctx, `UPDATE users SET status = $2 WHERE id = $1`, id, status)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return auth.ErrUnknownUser
		}
		return nil
	}

	tx, err := a.s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	curRole, curDisabled, found, otherAdmins, err := lastAdminGuardRows(ctx, tx, id)
	if err != nil {
		return err
	}
	if !found {
		return auth.ErrUnknownUser
	}
	if curRole == string(auth.RoleAdmin) && !curDisabled && otherAdmins == 0 {
		return auth.ErrLastAdmin
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET status = $2 WHERE id = $1`, id, status); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (a *AuthStore) SetUserPassword(ctx context.Context, id, passwordHash string) error {
	tag, err := a.s.Pool.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, id, passwordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return auth.ErrUnknownUser
	}
	return nil
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

// SessionByToken resolves a session by its digest key. Disabling a user
// invalidates their live sessions immediately (audit S-005) — the join
// filters them out here rather than trusting a later revocation sweep.
func (a *AuthStore) SessionByToken(ctx context.Context, token string) (auth.Session, error) {
	row := a.s.Pool.QueryRow(ctx, `
		SELECT s.id, s.user_id, u.role, s.created_at, s.expires_at,
		       COALESCE(s.revoked_at, 'epoch'::timestamptz), u.platform_admin
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id = $1 AND u.status <> 'disabled'`, token)
	var sess auth.Session
	var role string
	var revoked time.Time
	if err := row.Scan(&sess.Token, &sess.UserID, &role, &sess.CreatedAt, &sess.ExpiresAt, &revoked, &sess.PlatformAdmin); err != nil {
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
