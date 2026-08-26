package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// StrategyConfigs adapts the store to strategy.Store: immutable version
// rows in strategy_configs with exactly one active at a time.
type StrategyConfigs struct{ s *Store }

func (s *Store) StrategyConfigs() *StrategyConfigs { return &StrategyConfigs{s: s} }

// Insert deactivates the current active row and activates the new one in
// a single transaction. Empty createdBy / zero parent become NULL.
func (c *StrategyConfigs) Insert(ctx context.Context, createdBy string, payload, diff json.RawMessage, parent int64) (int64, time.Time, error) {
	tx, err := c.s.Pool.Begin(ctx)
	if err != nil {
		return 0, time.Time{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE strategy_configs SET active = FALSE WHERE active`); err != nil {
		return 0, time.Time{}, err
	}
	var (
		version   int64
		createdAt time.Time
	)
	err = tx.QueryRow(ctx, `
		INSERT INTO strategy_configs (created_by, active, payload, diff, parent_version)
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

func (c *StrategyConfigs) Active(ctx context.Context) (strategy.Snapshot, bool, error) {
	snap, err := c.scanOne(ctx, `WHERE active`)
	if errors.Is(err, pgx.ErrNoRows) {
		return strategy.Snapshot{}, false, nil
	}
	if err != nil {
		return strategy.Snapshot{}, false, err
	}
	return snap, true, nil
}

func (c *StrategyConfigs) Get(ctx context.Context, version int64) (strategy.Snapshot, error) {
	snap, err := c.scanOne(ctx, `WHERE version = $1`, version)
	if errors.Is(err, pgx.ErrNoRows) {
		return strategy.Snapshot{}, strategy.ErrNotFound
	}
	return snap, err
}

func (c *StrategyConfigs) scanOne(ctx context.Context, where string, args ...any) (strategy.Snapshot, error) {
	var (
		snap      strategy.Snapshot
		payload   []byte
		createdBy *string
		parent    *int64
	)
	err := c.s.Pool.QueryRow(ctx, `
		SELECT version, payload, created_by, created_at, parent_version
		FROM strategy_configs `+where, args...).
		Scan(&snap.Version, &payload, &createdBy, &snap.CreatedAt, &parent)
	if err != nil {
		return strategy.Snapshot{}, err
	}
	if err := json.Unmarshal(payload, &snap.Params); err != nil {
		return strategy.Snapshot{}, err
	}
	if createdBy != nil {
		snap.CreatedBy = *createdBy
	}
	if parent != nil {
		snap.ParentVersion = *parent
	}
	return snap, nil
}

func (c *StrategyConfigs) List(ctx context.Context, limit int) ([]strategy.VersionInfo, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := c.s.Pool.Query(ctx, `
		SELECT version, created_by, created_at, active, diff, parent_version
		FROM strategy_configs ORDER BY version DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []strategy.VersionInfo
	for rows.Next() {
		var (
			v         strategy.VersionInfo
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
			v.ParentVersion = *parent
		}
		v.Diff = diff
		out = append(out, v)
	}
	return out, rows.Err()
}

// AuditRow is one audit_events insert (insert-only table by policy).
type AuditRow struct {
	ID            string
	Actor         string // "" → NULL
	Source        string // web|telegram|system|ai
	Action        string
	Entity        string
	EntityID      string
	Before, After []byte
	IP            string // "" → NULL
	CorrelationID string
}

// InsertAuditEvent appends one audit record.
func (s *Store) InsertAuditEvent(ctx context.Context, row AuditRow) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO audit_events
			(id, actor, source, action, entity, entity_id, before, after, ip, correlation_id)
		VALUES ($1, NULLIF($2,''), $3, $4, $5, NULLIF($6,''), $7, $8,
			NULLIF($9,'')::inet, NULLIF($10,''))`,
		row.ID, row.Actor, row.Source, row.Action, row.Entity, row.EntityID,
		row.Before, row.After, row.IP, row.CorrelationID)
	return err
}
