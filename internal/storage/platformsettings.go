package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cploutarchou/arb-chain-bot/internal/platform"
)

// PlatformSettings adapts the store to platform.Store: immutable version
// rows in platform_settings with exactly one active at a time (same
// shape as StrategyConfigs, design §1.4).
type PlatformSettings struct{ s *Store }

func (s *Store) PlatformSettings() *PlatformSettings { return &PlatformSettings{s: s} }

// Insert deactivates the current active row and activates the new one in
// a single transaction. Empty createdBy / zero parent become NULL.
func (c *PlatformSettings) Insert(ctx context.Context, createdBy string, payload, diff json.RawMessage, parent int64) (int64, time.Time, error) {
	tx, err := c.s.Pool.Begin(ctx)
	if err != nil {
		return 0, time.Time{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE platform_settings SET active = FALSE WHERE active`); err != nil {
		return 0, time.Time{}, err
	}
	var (
		version   int64
		createdAt time.Time
	)
	err = tx.QueryRow(ctx, `
		INSERT INTO platform_settings (created_by, active, payload, diff, parent_version)
		VALUES (NULLIF($1,''), TRUE, $2, $3, NULLIF($4,0))
		RETURNING version, created_at`,
		createdBy, payload, diff, parent).Scan(&version, &createdAt)
	if err != nil {
		return 0, time.Time{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, time.Time{}, err
	}
	return version, createdAt, nil
}

func (c *PlatformSettings) Active(ctx context.Context) (platform.Snapshot, bool, error) {
	snap, err := c.scanOne(ctx, `WHERE active`)
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Snapshot{}, false, nil
	}
	if err != nil {
		return platform.Snapshot{}, false, err
	}
	return snap, true, nil
}

func (c *PlatformSettings) Get(ctx context.Context, version int64) (platform.Snapshot, error) {
	snap, err := c.scanOne(ctx, `WHERE version = $1`, version)
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Snapshot{}, platform.ErrNotFound
	}
	return snap, err
}

func (c *PlatformSettings) scanOne(ctx context.Context, where string, args ...any) (platform.Snapshot, error) {
	var (
		snap      platform.Snapshot
		payload   []byte
		createdBy *string
		parent    *int64
	)
	err := c.s.Pool.QueryRow(ctx, `
		SELECT version, payload, created_by, created_at, parent_version
		FROM platform_settings `+where, args...).
		Scan(&snap.Version, &payload, &createdBy, &snap.CreatedAt, &parent)
	if err != nil {
		return platform.Snapshot{}, err
	}
	if err := json.Unmarshal(payload, &snap.Settings); err != nil {
		return platform.Snapshot{}, err
	}
	if createdBy != nil {
		snap.CreatedBy = *createdBy
	}
	if parent != nil {
		snap.ParentVer = *parent
	}
	return snap, nil
}

func (c *PlatformSettings) List(ctx context.Context, limit int) ([]platform.VersionInfo, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := c.s.Pool.Query(ctx, `
		SELECT version, created_by, created_at, active, diff, parent_version
		FROM platform_settings ORDER BY version DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []platform.VersionInfo
	for rows.Next() {
		var (
			v         platform.VersionInfo
			createdBy *string
			diff      []byte
			parent    *int64
		)
		if err := rows.Scan(&v.Version, &createdBy, &v.CreatedAt, &v.Active, &diff, &parent); err != nil {
			return nil, err
		}
		if createdBy != nil {
			v.CreatedBy = *createdBy
		}
		if parent != nil {
			v.ParentVer = *parent
		}
		v.Diff = diff
		out = append(out, v)
	}
	return out, rows.Err()
}
