package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateTwice opens a fresh temporary database, migrates it again, then
// reopens it, and checks that nothing breaks and nothing is applied twice.
// All data here is synthetic.
func TestMigrateTwice(t *testing.T) {
	// t.TempDir is deleted automatically when the test ends.
	path := filepath.Join(t.TempDir(), "test.db")

	conn, err := Open(path) // first migration happens inside Open
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := Migrate(conn); err != nil { // second run must be a no-op
		t.Fatalf("second Migrate: %v", err)
	}
	conn.Close()

	// Reopening also runs Migrate (a third time) against the existing file.
	conn, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer conn.Close()

	// Every expected table exists.
	for _, table := range []string{"schema_migrations", "samples", "sleep_stages", "workouts", "meta"} {
		var n int
		err := conn.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n)
		if err != nil {
			t.Fatalf("look up table %s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("table %s: want 1, got %d", table, n)
		}
	}

	// Each migration file was recorded exactly once.
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	var recorded int
	if err := conn.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&recorded); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if recorded != len(migrations) {
		t.Errorf("schema_migrations rows: want %d, got %d", len(migrations), recorded)
	}
}

// TestSamplesUnique checks that the same sample cannot be inserted twice,
// which is what makes re-importing an export safe.
func TestSamplesUnique(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	const insert = `INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	if _, err := conn.Exec(insert, "heart_rate", 1700000000, 1700000000, 61.0, "count/min", 3600, "SyntheticWatch"); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	// Same metric, timestamps and source -> must be rejected, even with a
	// different value.
	_, err = conn.Exec(insert, "heart_rate", 1700000000, 1700000000, 99.0, "count/min", 3600, "SyntheticWatch")
	if err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("duplicate insert: want UNIQUE constraint error, got %v", err)
	}

	// Same reading from a different source is a different row -> allowed.
	if _, err := conn.Exec(insert, "heart_rate", 1700000000, 1700000000, 61.0, "count/min", 3600, "SyntheticPhone"); err != nil {
		t.Fatalf("insert from other source: %v", err)
	}
}

// TestMemoryDatabase checks that ":memory:" works, which relies on Open
// limiting the pool to a single connection.
func TestMemoryDatabase(t *testing.T) {
	conn, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Exec(`INSERT INTO meta (key, value) VALUES ('k', 'v')`); err != nil {
		t.Fatalf("insert into meta: %v", err)
	}
}

// A path with URI characters must be used as typed, not read as a URI.
func TestOpenPathWithURICharacters(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"we?ird#.db", "100%.db", "a%3Fb.db", "sp ace.db"} {
		path := filepath.Join(dir, name)
		conn, err := Open(path)
		if err != nil {
			t.Fatalf("Open(%q): %v", name, err)
		}
		conn.Close()
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%q: database not created at the given path: %v", name, err)
		}
		ro, err := OpenReadOnly(path)
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if err := ro.QueryRow(`SELECT count(*) FROM meta`).Scan(&n); err != nil {
			t.Errorf("%q: read-only open: %v", name, err)
		}
		ro.Close()
	}
	// Nothing else (such as a file named "we") may appear.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() == "we" || e.Name() == "100" {
			t.Errorf("stray file %q: the path was cut at a URI character", e.Name())
		}
	}
}

func TestOpenCreatesPrivateFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.db")
	conn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Exec(`INSERT INTO meta (key, value) VALUES ('k', 'v')`) // makes the -wal file
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		st, err := os.Stat(p)
		if err != nil {
			continue // not every build leaves a -wal or -shm around
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("%s has mode %v, want 0600", filepath.Base(p), st.Mode().Perm())
		}
	}
}
