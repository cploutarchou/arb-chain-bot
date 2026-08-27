package storage

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The constant must track the repository's migrations directory.
func TestLatestMigrationVersionMatchesFiles(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("migrations glob: %v (%d files)", err, len(files))
	}
	var max int64
	for _, f := range files {
		n, err := strconv.ParseInt(strings.SplitN(filepath.Base(f), "_", 2)[0], 10, 64)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if n > max {
			max = n
		}
	}
	if max != LatestMigrationVersion {
		t.Fatalf("LatestMigrationVersion = %d, migrations/ has %d: bump the constant", LatestMigrationVersion, max)
	}
}

// Against the test database (migrations applied by the operator) the
// gauge is 0; a dirty flag or an older version flips it to 1.
func TestMigrationsPending(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	var hasTable bool
	if err := s.Pool.QueryRow(ctx, `SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&hasTable); err != nil {
		t.Fatal(err)
	}
	pending, err := s.MigrationsPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !hasTable {
		// Schema applied without golang-migrate (CI): the version cannot
		// be verified, so the gauge must read pending.
		if pending != 1 {
			t.Fatalf("pending = %d without schema_migrations, want 1", pending)
		}
		return
	}
	if pending != 0 {
		t.Fatalf("pending = %d on an up-to-date test database (built for %d)", pending, LatestMigrationVersion)
	}
	var version int64
	if err := s.Pool.QueryRow(ctx, `SELECT version FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.Pool.Exec(ctx, `UPDATE schema_migrations SET version = $1, dirty = false`, version)
	})
	if _, err := s.Pool.Exec(ctx, `UPDATE schema_migrations SET dirty = true`); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.MigrationsPending(ctx); p != 1 {
		t.Fatalf("dirty: pending = %d, want 1", p)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE schema_migrations SET dirty = false, version = $1`, LatestMigrationVersion-1); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.MigrationsPending(ctx); p != 1 {
		t.Fatalf("behind: pending = %d, want 1", p)
	}
}
