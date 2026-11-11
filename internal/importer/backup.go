package importer

import (
	"context"
	"database/sql"
	"fmt"
	"os"
)

// HasData reports whether the database holds any imported or logged rows.
func HasData(ctx context.Context, conn *sql.DB) (bool, error) {
	var has bool
	err := conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM samples)
		OR EXISTS (SELECT 1 FROM sleep_stages) OR EXISTS (SELECT 1 FROM workouts)`).Scan(&has)
	return has, err
}

// Backup copies the database to <dbPath>.bak with VACUUM INTO, replacing the
// previous copy, and returns the path. An empty database is not worth a copy:
// then it returns "" and no error. Do not call it while a transaction is open
// on conn (VACUUM cannot run inside one, and the pool has a single connection).
//
// The copy is written to a temporary file that is private from the start and
// moved into place at the end, so a failure never destroys the older backup.
func Backup(ctx context.Context, conn *sql.DB, dbPath string) (string, error) {
	has, err := HasData(ctx, conn)
	if err != nil {
		return "", fmt.Errorf("checking the database before the backup: %w", err)
	}
	if !has {
		return "", nil
	}
	final := dbPath + ".bak"
	tmp := final + ".tmp"
	os.Remove(tmp)
	// VACUUM INTO accepts a target that does not exist or is empty. Creating
	// it ourselves lets us pick the permissions (the data is personal).
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("creating the backup: %w", err)
	}
	f.Close()
	if _, err := conn.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("backing up the database: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("saving the backup: %w", err)
	}
	return final, nil
}
