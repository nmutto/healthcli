package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The SQL files in migrations/ are compiled into the binary, so the CLI is
// a single file with no schema files to ship next to it.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// migration is one numbered SQL file, e.g. "0001_init.sql" -> version 1.
type migration struct {
	version int
	name    string
	sql     string
}

// Migrate brings the database schema up to date.
//
// How it works:
//   - The table schema_migrations remembers which versions were applied.
//   - Every migrations/NNNN_*.sql file whose number is not in that table is
//     run, in increasing order, each inside its own transaction.
//
// It is safe to call any number of times: already-applied migrations are
// skipped (idempotent). Migrations only go forward; there is no "down".
// To change the schema, add a new file with the next number. Never edit a
// migration that has already been committed, because existing databases
// have already run the old version and will not run it again.
func Migrate(conn *sql.DB) error {
	_, err := conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL -- UTC unix seconds
	)`)
	if err != nil {
		return fmt.Errorf("db: create schema_migrations: %w", err)
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	applied, err := appliedVersions(conn)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		if err := apply(conn, m); err != nil {
			return err
		}
	}
	return nil
}

// apply runs one migration and records it, all in a single transaction:
// either the schema change and the bookkeeping row both happen, or neither
// does. A half-applied migration would be very hard to recover from.
func apply(conn *sql.DB, m migration) error {
	tx, err := conn.Begin()
	if err != nil {
		return fmt.Errorf("db: begin migration %s: %w", m.name, err)
	}
	// Rollback after a successful Commit is a harmless no-op, so deferring
	// it is the simplest way to undo the transaction on every error path.
	defer tx.Rollback()

	if _, err := tx.Exec(m.sql); err != nil {
		return fmt.Errorf("db: run migration %s: %w", m.name, err)
	}
	_, err = tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		m.version, time.Now().UTC().Unix())
	if err != nil {
		return fmt.Errorf("db: record migration %s: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit migration %s: %w", m.name, err)
	}
	return nil
}

// appliedVersions returns the set of migration versions already applied.
func appliedVersions(conn *sql.DB) (map[int]bool, error) {
	rows, err := conn.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("db: read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("db: read schema_migrations: %w", err)
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// loadMigrations reads the embedded SQL files and returns them sorted by
// version. It fails loudly on a badly named file or a duplicate number,
// because silently skipping a migration would corrupt the schema history.
func loadMigrations() ([]migration, error) {
	names, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("db: list migrations: %w", err)
	}

	var migrations []migration
	seen := map[int]string{}
	for _, path := range names {
		name := strings.TrimPrefix(path, "migrations/")

		// The version is everything before the first "_": "0001_init.sql" -> 1.
		prefix, _, found := strings.Cut(name, "_")
		version, err := strconv.Atoi(prefix)
		if !found || err != nil || version <= 0 {
			return nil, fmt.Errorf("db: migration %q must be named NNNN_description.sql", name)
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("db: migrations %q and %q share version %d", other, name, version)
		}
		seen[version] = name

		body, err := migrationFiles.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("db: read migration %s: %w", name, err)
		}
		migrations = append(migrations, migration{version: version, name: name, sql: string(body)})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})
	return migrations, nil
}
