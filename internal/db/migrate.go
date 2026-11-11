package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
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
	// Rebuild marks a migration that rebuilds a table (copy the rows into a
	// new table, drop the old one). It can take long on a big database and
	// cannot be undone, so Migrate saves a copy of the file first. A SQL
	// file asks for this with the line "-- healthcli:rebuild".
	Rebuild bool
}

// NoticeWriter is where Migrate prints its one-line progress notices. It is
// a variable so tests can capture the text.
var NoticeWriter io.Writer = os.Stderr

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
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	return migrateWith(conn, migrations)
}

// migrateWith is Migrate with the list of migrations given, so a test can
// pass its own.
func migrateWith(conn *sql.DB, migrations []migration) error {
	_, err := conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL -- UTC unix seconds
	)`)
	if err != nil {
		return fmt.Errorf("db: create schema_migrations: %w", err)
	}

	applied, err := appliedVersions(conn)
	if err != nil {
		return err
	}
	if err := checkNotNewer(applied, migrations); err != nil {
		return err
	}

	pending := pendingMigrations(applied, migrations)
	if len(pending) == 0 {
		return nil
	}

	// A brand-new database has nothing to lose and nobody to tell. An
	// existing one gets a notice, so a slow upgrade is not a silent pause.
	if len(applied) > 0 {
		fmt.Fprintf(NoticeWriter, "upgrading database to schema %d\n", pending[len(pending)-1].version)
	}

	for _, m := range pending {
		if m.Rebuild && len(applied) > 0 {
			if err := backupBefore(conn, m); err != nil {
				return err
			}
		}
		if err := apply(conn, m); err != nil {
			return err
		}
	}
	return nil
}

// checkNotNewer refuses a database whose newest applied migration is one this
// binary does not know: it was made by a newer healthcli, and running on it
// could misread or damage data.
func checkNotNewer(applied map[int]bool, migrations []migration) error {
	known := 0
	for _, m := range migrations {
		if m.version > known {
			known = m.version
		}
	}
	newest := 0
	for v := range applied {
		if v > newest {
			newest = v
		}
	}
	if newest > known {
		return fmt.Errorf("db: this database was created by a newer healthcli (schema %d, this one knows %d); upgrade healthcli", newest, known)
	}
	return nil
}

// pendingMigrations returns the migrations not yet applied, in order.
func pendingMigrations(applied map[int]bool, migrations []migration) []migration {
	var pending []migration
	for _, m := range migrations {
		if !applied[m.version] {
			pending = append(pending, m)
		}
	}
	return pending
}

// backupBefore saves a copy of the database next to it, as
// "<db>.pre-migration-N.bak" with mode 0600, using VACUUM INTO (a
// consistent copy even in WAL mode). An in-memory database has no file, so
// there is nothing to save.
func backupBefore(conn *sql.DB, m migration) error {
	var file string
	row := conn.QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`)
	if err := row.Scan(&file); err != nil {
		return fmt.Errorf("db: find database file for backup: %w", err)
	}
	if file == "" {
		return nil
	}

	backup := fmt.Sprintf("%s.pre-migration-%d.bak", file, m.version)
	// VACUUM INTO needs a missing or empty target. Create it ourselves
	// first so it is private from the start; drop a leftover from an
	// earlier failed attempt, which no longer matches the database.
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("db: replace old backup: %w", err)
	}
	f, err := os.OpenFile(backup, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("db: create backup: %w", err)
	}
	f.Close()

	if _, err := conn.Exec(`VACUUM INTO ?`, backup); err != nil {
		os.Remove(backup)
		return fmt.Errorf("db: back up before migration %s: %w", m.name, err)
	}
	fmt.Fprintf(NoticeWriter, "saved a copy of the database to %s\n", backup)
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
		migrations = append(migrations, migration{
			version: version,
			name:    name,
			sql:     string(body),
			Rebuild: strings.Contains(string(body), "-- healthcli:rebuild"),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})
	return migrations, nil
}
