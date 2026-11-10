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

	// _pragma options in the connection string are applied by the driver to
	// every new connection, so they can't be forgotten:
	//   foreign_keys(1)    enforce FOREIGN KEY constraints (off by default!)
	//   busy_timeout(5000) wait up to 5s for a lock instead of failing at once
	//   journal_mode(WAL)  safer and faster writes than the default journal
	dsn := "file:" + path +
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
	return conn, nil
}

// OpenReadOnly opens an existing database for reading only. SQLite itself
// refuses every write on this handle (mode=ro), so no SQL text, not even a
// PRAGMA, can switch that off. Migrations are not applied: they write.
func OpenReadOnly(path string) (*sql.DB, error) {
	if path == "" {
		return nil, errors.New("db: empty database path")
	}
	dsn := "file:" + path +
		"?mode=ro" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=query_only(1)"

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}
	conn.SetMaxOpenConns(1)
	return conn, nil
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
