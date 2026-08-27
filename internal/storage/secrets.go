package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cploutarchou/arb-chain-bot/internal/secrets"
)

// Secrets adapts the store to secrets.Store (T-060): one row per
// registry name in the secrets table, overwritten on write. Only
// ciphertext ever reaches this layer.
type Secrets struct{ s *Store }

func (s *Store) Secrets() *Secrets { return &Secrets{s: s} }

// Put upserts the row. updated_by references users(id); an actor that
// is not a user row (e.g. "system") is stored as NULL rather than
// failing the write.
func (c *Secrets) Put(ctx context.Context, row secrets.Row) error {
	_, err := c.s.Pool.Exec(ctx, `
		INSERT INTO secrets (name, ciphertext, nonce, key_id, updated_at, updated_by)
		VALUES ($1, $2, $3, $4, $5, (SELECT id FROM users WHERE id = $6))
		ON CONFLICT (name) DO UPDATE SET
			ciphertext = EXCLUDED.ciphertext, nonce = EXCLUDED.nonce,
			key_id = EXCLUDED.key_id, updated_at = EXCLUDED.updated_at,
			updated_by = EXCLUDED.updated_by`,
		row.Name, row.Ciphertext, row.Nonce, row.KeyID, row.UpdatedAt.UTC(), row.UpdatedBy)
	return err
}

func (c *Secrets) Get(ctx context.Context, name string) (secrets.Row, bool, error) {
	var (
		row       secrets.Row
		updatedBy *string
		updatedAt time.Time
	)
	err := c.s.Pool.QueryRow(ctx, `
		SELECT name, ciphertext, nonce, key_id, updated_at, updated_by
		FROM secrets WHERE name = $1`, name).
		Scan(&row.Name, &row.Ciphertext, &row.Nonce, &row.KeyID, &updatedAt, &updatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return secrets.Row{}, false, nil
	}
	if err != nil {
		return secrets.Row{}, false, err
	}
	row.UpdatedAt = updatedAt
	if updatedBy != nil {
		row.UpdatedBy = *updatedBy
	}
	return row, true, nil
}

func (c *Secrets) Delete(ctx context.Context, name string) (bool, error) {
	tag, err := c.s.Pool.Exec(ctx, `DELETE FROM secrets WHERE name = $1`, name)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (c *Secrets) List(ctx context.Context) ([]secrets.Row, error) {
	rows, err := c.s.Pool.Query(ctx, `
		SELECT name, ciphertext, nonce, key_id, updated_at, updated_by
		FROM secrets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []secrets.Row
	for rows.Next() {
		var (
			row       secrets.Row
			updatedBy *string
		)
		if err := rows.Scan(&row.Name, &row.Ciphertext, &row.Nonce, &row.KeyID, &row.UpdatedAt, &updatedBy); err != nil {
			return nil, err
		}
		if updatedBy != nil {
			row.UpdatedBy = *updatedBy
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
