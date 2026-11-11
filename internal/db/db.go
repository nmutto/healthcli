// Package db owns the SQLite database: where it lives, how it is opened,
// and how its schema is created and upgraded (migrations).
//
// Other packages should only need two functions:
//
//	path, err := db.DefaultPath() // where the database lives by default
//	conn, err := db.Open(path)    // open it, ready to use (schema up to date)
package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	// The blank import registers the "sqlite" driver with database/sql.
	// modernc.org/sqlite is SQLite translated to pure Go, so building this
	// project never needs a C compiler (no cgo).
	_ "modernc.org/sqlite"
)

// EnvPath is the environment variable that overrides the default database
// location. Handy for tests and for keeping several databases around.
const EnvPath = "HEALTHCLI_DB"

// Open opens (creating if needed) the SQLite database at path, applies any
// pending migrations, and returns a ready-to-use handle.
//
// The caller owns the returned *sql.DB and should Close it when done.
func Open(path string) (*sql.DB, error) {
	if path == "" {
		return nil, errors.New("db: empty database path")
	}

	// Make sure the folder exists. 0700 = only the current user can read it,
	// because this database will hold personal health data.
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("db: create directory: %w", err)
		}
	}

	// Create the file ourselves, private from the start. SQLite gives the
	// -wal and -shm files the same permissions as the database file.
	if path != ":memory:" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("db: open %s for writing: %w", path, err)
		}
		f.Close()
	}

	// _pragma options in the connection string are applied by the driver to
	// every new connection, so they can't be forgotten:
	//   foreign_keys(1)    enforce FOREIGN KEY constraints (off by default!)
	//   busy_timeout(5000) wait up to 5s for a lock instead of failing at once
	//   journal_mode(WAL)  safer and faster writes than the default journal
	dsn := "file:" + escapePath(path) +
		"?_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)"

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}

	// database/sql keeps a pool of connections. SQLite allows only one
	// writer at a time, and a CLI does one thing at a time anyway, so a
	// single connection avoids "database is locked" surprises. It also makes
	// ":memory:" work: every new connection to ":memory:" would otherwise
	// get its own, separate, empty database.
	conn.SetMaxOpenConns(1)

	if err := Migrate(conn); err != nil {
		conn.Close()
		return nil, err
	}
	if path != ":memory:" {
		// Best effort: a file that already existed may have looser
		// permissions, and the -wal and -shm files exist only now. Failing to
		// tighten them must never stop the user from using the database.
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Chmod(path+suffix, 0o600)
		}
	}
	return conn, nil
}

// escapePath makes a file path safe to put after "file:" in the connection
// string. There "?" starts the options, "#" starts a fragment and "%" starts
// an escape, so a path containing them would be cut short or misread.
func escapePath(path string) string {
	return strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(path)
}

// OpenReadOnly opens an existing database for reading only. SQLite itself
// refuses every write on this handle (mode=ro), so no SQL text, not even a
// PRAGMA, can switch that off. Migrations are not applied: they write.
//
// If SQLite cannot open the file this way (a read-only copy of a WAL
// database in a read-only folder cannot get its -shm file), it retries with
// immutable=1, which promises SQLite that nothing changes the file. That is
// right for a copy, and it ignores a -wal file that was not checkpointed.
func OpenReadOnly(path string) (*sql.DB, error) {
	if path == "" {
		return nil, errors.New("db: empty database path")
	}
	conn, err := openRO(path, false)
	if err == nil {
		return conn, nil
	}
	if conn, err2 := openRO(path, true); err2 == nil {
		return conn, nil
	}
	return nil, err
}

// openRO opens the file read-only and proves it can be read.
func openRO(path string, immutable bool) (*sql.DB, error) {
	dsn := "file:" + escapePath(path) + "?mode=ro"
	if immutable {
		dsn += "&immutable=1"
	}
	dsn += "&_pragma=busy_timeout(5000)&_pragma=query_only(1)"

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}
	conn.SetMaxOpenConns(1)
	// sql.Open does not touch the file; this does.
	var n int
	if err := conn.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n); err != nil {
		conn.Close()
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}
	return conn, nil
}

// OpenForRead opens an existing database for the commands that only read.
// The returned handle cannot write.
//
// When nothing needs migrating it writes nothing at all (no WAL switch, no
// chmod, no schema_migrations table), so a read-only copy works. When the
// schema is behind, it upgrades the database first, exactly as Open does; if
// the file cannot be written it fails and says so rather than reading an old
// schema. A database from a newer healthcli is refused.
func OpenForRead(path string) (*sql.DB, error) {
	conn, err := OpenReadOnly(path)
	if err != nil {
		return nil, err
	}
	migrations, err := loadMigrations()
	if err != nil {
		conn.Close()
		return nil, err
	}
	applied, err := readApplied(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := checkNotNewer(applied, migrations); err != nil {
		conn.Close()
		return nil, err
	}
	pending := pendingMigrations(applied, migrations)
	if len(pending) == 0 {
		return conn, nil
	}
	conn.Close()

	writable, err := Open(path)
	if err != nil {
		return nil, fmt.Errorf("db: the database needs upgrading to schema %d but cannot be written (read-only copy?): %w",
			pending[len(pending)-1].version, err)
	}
	writable.Close()
	return OpenReadOnly(path)
}

// readApplied is appliedVersions for a database that may not have the
// schema_migrations table yet (it is then "nothing applied").
func readApplied(conn *sql.DB) (map[int]bool, error) {
	var n int
	err := conn.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'schema_migrations'`).Scan(&n)
	if err != nil {
		return nil, fmt.Errorf("db: read schema_migrations: %w", err)
	}
	if n == 0 {
		return map[int]bool{}, nil
	}
	return appliedVersions(conn)
}

// DefaultPath returns where the database lives when the user does not say.
//
// Rules, first match wins:
//  1. $HEALTHCLI_DB, if set.
//  2. $XDG_DATA_HOME/healthcli/healthcli.db, if XDG_DATA_HOME is set.
//  3. macOS: ~/Library/Application Support/healthcli/healthcli.db
//  4. elsewhere: ~/.local/share/healthcli/healthcli.db
//
// A --db command-line flag, if the CLI has one, should be checked by the
// caller before calling DefaultPath.
func DefaultPath() (string, error) {
	if p := os.Getenv(EnvPath); p != "" {
		return p, nil
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "healthcli", "healthcli.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("db: find home directory: %w", err)
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "healthcli", "healthcli.db"), nil
	}
	return filepath.Join(home, ".local", "share", "healthcli", "healthcli.db"), nil
}
