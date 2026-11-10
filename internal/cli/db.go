package cli

import (
	"database/sql"
	"errors"
	"fmt"
	"os"

	"github.com/nmutto/healthcli/internal/db"
)

// dbPath is the database named by --db, or the default location when the
// flag was not given.
func dbPath(opts *options) (string, error) {
	if opts.dbPath != "" {
		return opts.dbPath, nil
	}
	return db.DefaultPath()
}

// requireDB fails with a helpful message when there is no database file yet.
// Commands that only read must never create one as a side effect.
func requireDB(path string) error {
	if path == ":memory:" {
		return nil
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf(`database not found: %s; run "healthcli import apple <export.zip>" first`, path)
	}
	return nil
}

// openDB opens the database, creating it when missing. The caller must Close
// the result.
func openDB(opts *options) (*sql.DB, error) {
	path, err := dbPath(opts)
	if err != nil {
		return nil, err
	}
	return db.Open(path)
}

// openReadOnlyDB is openDB for `query`: the connection cannot write at all.
func openReadOnlyDB(opts *options) (*sql.DB, error) {
	path, err := dbPath(opts)
	if err != nil {
		return nil, err
	}
	if err := requireDB(path); err != nil {
		return nil, err
	}
	return db.OpenReadOnly(path)
}
