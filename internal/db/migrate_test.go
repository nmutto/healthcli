package db

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureNotices redirects the migration notices into a buffer.
func captureNotices(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := NoticeWriter
	NoticeWriter = &buf
	t.Cleanup(func() { NoticeWriter = old })
	return &buf
}

// populated makes a migrated database with one synthetic row.
func populated(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "health.db")
	conn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO meta (key, value) VALUES ('k', 'v')`); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	return path
}

func TestNewerSchemaIsRefused(t *testing.T) {
	path := populated(t, t.TempDir())
	conn, _ := sql.Open("sqlite", "file:"+path)
	if _, err := conn.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (99, 0)`); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	want := "created by a newer healthcli"
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Open: err = %v, want it to contain %q", err, want)
	}
	if _, err := OpenForRead(path); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("OpenForRead: err = %v, want it to contain %q", err, want)
	}
}

// fakeRebuild is a test-only migration list: the real ones plus a rebuild.
func withExtra(t *testing.T, rebuild bool) []migration {
	t.Helper()
	real, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	return append(real, migration{
		version: 100,
		name:    "0100_test.sql",
		sql:     `CREATE TABLE extra (x INTEGER)`,
		Rebuild: rebuild,
	})
}

func TestUpgradeNoticeAndBackup(t *testing.T) {
	path := populated(t, t.TempDir())
	notices := captureNotices(t)
	conn, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := migrateWith(conn, withExtra(t, true)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notices.String(), "upgrading database to schema 100\n") {
		t.Errorf("notice = %q", notices.String())
	}
	bak := path + ".pre-migration-100.bak"
	st, err := os.Stat(bak)
	if err != nil {
		t.Fatalf("no backup: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("backup mode = %v, want 0600", st.Mode().Perm())
	}
	// The copy is the database as it was before the migration.
	b, err := OpenReadOnly(bak)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var v string
	if err := b.QueryRow(`SELECT value FROM meta WHERE key = 'k'`).Scan(&v); err != nil || v != "v" {
		t.Errorf("backup lost the data: %q, %v", v, err)
	}
	var n int
	b.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'extra'`).Scan(&n)
	if n != 0 {
		t.Error("backup already has the migrated table")
	}
}

func TestNoBackupWithoutRebuild(t *testing.T) {
	path := populated(t, t.TempDir())
	notices := captureNotices(t)
	conn, _ := sql.Open("sqlite", "file:"+path)
	defer conn.Close()

	if err := migrateWith(conn, withExtra(t, false)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notices.String(), "upgrading database to schema 100") {
		t.Errorf("notice missing: %q", notices.String())
	}
	if m, _ := filepath.Glob(path + "*.bak"); len(m) != 0 {
		t.Errorf("unexpected backups: %v", m)
	}
}

func TestFreshDatabaseHasNoNoticeOrBackup(t *testing.T) {
	notices := captureNotices(t)
	dir := t.TempDir()
	conn, err := Open(filepath.Join(dir, "new.db"))
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if notices.Len() != 0 {
		t.Errorf("fresh database printed %q", notices.String())
	}
}

func TestCurrentDatabasePrintsNothing(t *testing.T) {
	path := populated(t, t.TempDir())
	notices := captureNotices(t)
	conn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if notices.Len() != 0 {
		t.Errorf("printed %q", notices.String())
	}
}

// A copy that is read-only, in a read-only folder, must still be readable.
func TestOpenForReadOnReadOnlyCopy(t *testing.T) {
	src := populated(t, t.TempDir())
	dir := t.TempDir()
	dst := filepath.Join(dir, "copy.db")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	// Copy through VACUUM INTO's logic is not needed: the source was closed,
	// so the main file holds everything (WAL is checkpointed on close).
	if err := os.WriteFile(dst, b, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}

	conn, err := OpenForRead(dst)
	if err != nil {
		t.Fatalf("OpenForRead: %v", err)
	}
	defer conn.Close()
	var v string
	if err := conn.QueryRow(`SELECT value FROM meta WHERE key = 'k'`).Scan(&v); err != nil || v != "v" {
		t.Errorf("read = %q, %v", v, err)
	}
	if _, err := conn.Exec(`INSERT INTO meta (key, value) VALUES ('a', 'b')`); err == nil {
		t.Error("the handle can write")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("reading left files behind: %v", entries)
	}
}

// Reading must not touch the file when nothing needs migrating.
func TestOpenForReadWritesNothing(t *testing.T) {
	path := populated(t, t.TempDir())
	os.Chmod(path, 0o640) // Open would tighten this to 0600
	before, _ := os.Stat(path)

	conn, err := OpenForRead(path)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	after, _ := os.Stat(path)
	if after.Mode().Perm() != 0o640 || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("file touched: %v %v", after.Mode().Perm(), after.ModTime())
	}
}

// A database without schema_migrations is migrated, not skipped.
func TestOpenForReadMigratesWritableDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, _ := sql.Open("sqlite", "file:"+path)
	raw.Exec(`CREATE TABLE junk (x)`)
	raw.Close()

	conn, err := OpenForRead(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var n int
	if err := conn.QueryRow(`SELECT count(*) FROM samples`).Scan(&n); err != nil {
		t.Errorf("not migrated: %v", err)
	}
}

// A read-only database that needs migrating must fail loudly.
func TestOpenForReadPendingOnReadOnlyFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "old.db")
	raw, _ := sql.Open("sqlite", "file:"+path)
	raw.Exec(`CREATE TABLE junk (x)`)
	raw.Close()
	os.Chmod(path, 0o400)
	os.Chmod(dir, 0o500)
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	_, err := OpenForRead(path)
	if err == nil || !strings.Contains(err.Error(), "needs upgrading") {
		t.Errorf("err = %v", err)
	}
}

func TestOpenReportsUnwritableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	path := populated(t, t.TempDir())
	os.Chmod(path, 0o400)
	if _, err := Open(path); err == nil {
		t.Error("Open of a read-only file should fail clearly")
	}
}
