package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// LatestMigrationVersion is the highest migrations/NNNNNN_*.up.sql this
// binary was built for. Migrations are applied by the migrate/migrate
// container (docker-compose.yml, Helm pre-install hook), never by arbd,
// so this constant is the only thing arbd can compare the database
// against. TestLatestMigrationVersionMatchesFiles fails when a new
// migration lands without bumping it.
const LatestMigrationVersion int64 = 21

// MigrationsPending is the boot-time db_migrations_pending input: 1 when
// golang-migrate's schema_migrations row is behind
// LatestMigrationVersion, marked dirty, or absent (table missing or
// empty — nothing applied yet); 0 when the database is at (or ahead of)
// this build's version. Errors other than "no row" are returned.
func (s *Store) MigrationsPending(ctx context.Context) (int64, error) {
	var version int64
	var dirty bool
	err := s.Pool.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&version, &dirty)
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 1, nil
	case errors.As(err, &pgErr) && pgErr.Code == "42P01":
		// No schema_migrations table: the schema was applied without
		// golang-migrate (CI pipes the SQL files through psql), so the
		// version cannot be verified. Report pending rather than guess.
		return 1, nil
	case err != nil:
		return 0, fmt.Errorf("schema_migrations: %w", err)
	case dirty || version < LatestMigrationVersion:
		return 1, nil
	}
	return 0, nil
}
