package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cploutarchou/arb-chain-bot/internal/apikey"
)

// APIKeys implements apikey.Store over migration 000015's api_keys
// table (T-086).
type APIKeys struct{ s *Store }

func (s *Store) APIKeys() *APIKeys { return &APIKeys{s: s} }

var _ apikey.Store = (*APIKeys)(nil)

const apiKeyColumns = `id, org_id, user_id, name, prefix, hash, scopes, created_at, last_used_at, revoked_at` //nolint:gosec // column list, not a credential

func scanAPIKey(row pgx.Row) (apikey.Key, error) {
	var k apikey.Key
	if err := row.Scan(&k.ID, &k.OrgID, &k.UserID, &k.Name, &k.Prefix, &k.Hash, &k.Scopes,
		&k.CreatedAt, &k.LastUsedAt, &k.RevokedAt); err != nil {
		return apikey.Key{}, err
	}
	return k, nil
}

func (a *APIKeys) CreateKey(ctx context.Context, k apikey.Key) (apikey.Key, error) {
	created, err := scanAPIKey(a.s.Pool.QueryRow(ctx, `
		INSERT INTO api_keys (id, org_id, user_id, name, prefix, hash, scopes)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+apiKeyColumns,
		k.ID, k.OrgID, k.UserID, k.Name, k.Prefix, k.Hash, k.Scopes))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return apikey.Key{}, apikey.ErrPrefixCollision
	}
	return created, err
}

func (a *APIKeys) ListKeys(ctx context.Context, orgID int64) ([]apikey.Key, error) {
	rows, err := a.s.Pool.Query(ctx, `
		SELECT `+apiKeyColumns+` FROM api_keys WHERE org_id = $1 ORDER BY created_at ASC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []apikey.Key{}
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (a *APIKeys) ByPrefix(ctx context.Context, prefix string) (apikey.Key, error) {
	k, err := scanAPIKey(a.s.Pool.QueryRow(ctx, `SELECT `+apiKeyColumns+` FROM api_keys WHERE prefix = $1`, prefix))
	if errors.Is(err, pgx.ErrNoRows) {
		return apikey.Key{}, apikey.ErrNotFound
	}
	return k, err
}

func (a *APIKeys) RevokeKey(ctx context.Context, orgID int64, id string, at time.Time) error {
	tag, err := a.s.Pool.Exec(ctx, `
		UPDATE api_keys SET revoked_at = $3 WHERE id = $1 AND org_id = $2 AND revoked_at IS NULL`, id, orgID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apikey.ErrNotFound
	}
	return nil
}

func (a *APIKeys) Touch(ctx context.Context, id string, at time.Time) error {
	tag, err := a.s.Pool.Exec(ctx, `UPDATE api_keys SET last_used_at = $2 WHERE id = $1`, id, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apikey.ErrNotFound
	}
	return nil
}

// RevokeByOwner revokes every live key owned by userID, across every
// organisation (audit S3/P1-12: cascades an account disable so a
// bearer credential does not outlive the account that minted it).
func (a *APIKeys) RevokeByOwner(ctx context.Context, userID string, at time.Time) (int, error) {
	tag, err := a.s.Pool.Exec(ctx, `
		UPDATE api_keys SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`, userID, at)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// RevokeByMembership revokes every live key owned by userID scoped to
// orgID (audit S3/P1-12: cascades a membership removal — the account
// may still be enabled and hold keys in another organisation).
func (a *APIKeys) RevokeByMembership(ctx context.Context, orgID int64, userID string, at time.Time) (int, error) {
	tag, err := a.s.Pool.Exec(ctx, `
		UPDATE api_keys SET revoked_at = $3 WHERE org_id = $1 AND user_id = $2 AND revoked_at IS NULL`, orgID, userID, at)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
