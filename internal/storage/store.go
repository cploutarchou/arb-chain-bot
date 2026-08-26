// Package storage is the PostgreSQL persistence layer: pgx pool, typed
// repositories over the migration schema, pgx-backed auth stores, and the
// async outbox that keeps the hot path free of database waits
// (docs/architecture.md §12). All money columns are NUMERIC and cross the
// driver as strings via decimal.
package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store owns the connection pool.
type Store struct {
	Pool *pgxpool.Pool
}

// Open connects and verifies the schema is reachable.
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: parse dsn: %w", err)
	}
	cfg.MaxConns = 8
	cfg.MaxConnLifetime = time.Hour
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("storage: connect: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("storage: ping: %w", err)
	}
	return &Store{Pool: pool}, nil
}

func (s *Store) Close() { s.Pool.Close() }

// Healthy reports pool reachability (readiness probe input).
func (s *Store) Healthy(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return s.Pool.Ping(ctx) == nil
}

// PoolStat is a plain snapshot of the connection pool (BL-18 system
// health); it exists so callers (internal/api) never need to import
// pgxpool just to report pool depth.
type PoolStat struct {
	AcquiredConns    int32 `json:"acquired_conns"`
	IdleConns        int32 `json:"idle_conns"`
	ConstructingConn int32 `json:"constructing_conns"`
	TotalConns       int32 `json:"total_conns"`
	MaxConns         int32 `json:"max_conns"`
	AcquireCount     int64 `json:"acquire_count"`
	EmptyAcquireCnt  int64 `json:"empty_acquire_count"`
	CanceledAcquires int64 `json:"canceled_acquire_count"`
}

// PoolStats snapshots the pgx pool for the system-health endpoint.
func (s *Store) PoolStats() PoolStat {
	st := s.Pool.Stat()
	return PoolStat{
		AcquiredConns: st.AcquiredConns(), IdleConns: st.IdleConns(),
		ConstructingConn: st.ConstructingConns(), TotalConns: st.TotalConns(),
		MaxConns: st.MaxConns(), AcquireCount: st.AcquireCount(),
		EmptyAcquireCnt: st.EmptyAcquireCount(), CanceledAcquires: st.CanceledAcquireCount(),
	}
}
