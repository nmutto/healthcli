package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The read commands must fail on a missing database and leave nothing behind.
func TestReadCommandsDoNotCreateDatabase(t *testing.T) {
	for _, args := range [][]string{
		{"query", "select 1"},
		{"today"},
		{"overview"},
		{"sleep"},
		{"workouts"},
		{"heatmap"},
		{"trend", "steps"},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "nope.db")
		_, _, err := run(t, append([]string{"--db", path}, args...)...)
		want := `database not found: ` + path + `; run "healthcli import apple <export.zip>" first`
		if err == nil || err.Error() != want {
			t.Errorf("%v: err = %v, want %q", args, err, want)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%v left files behind: %v", args, entries)
		}
	}
}

func TestLogStillCreatesDatabase(t *testing.T) {
	path := tempDB(t)
	if _, _, err := run(t, "--db", path, "log", "weight", "70"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("log should create the database: %v", err)
	}
}

func TestImportStillCreatesDatabase(t *testing.T) {
	path := tempDB(t)
	if _, _, err := run(t, "--db", path, "import", "apple", "../../testdata/small/export.xml"); err != nil && !strings.Contains(err.Error(), "file not found") {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("import should create the database: %v", err)
	}
}

// A read-only copy (file 0400 in a 0500 folder, like a Time Machine copy)
// must work for every read command, and reading must leave nothing behind.
func TestReadCommandsWorkOnReadOnlyCopy(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	src := seedDB(t)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "copy.db")
	if err := os.WriteFile(path, data, 0o400); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0o500)
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	fixClock(t, at(2026, 11, 9, 12))

	for _, args := range [][]string{
		{"today"},
		{"overview"},
		{"sleep"},
		{"workouts"},
		{"heatmap"},
		{"trend", "steps"},
		{"query", "select count(*) from samples"},
	} {
		if _, _, err := run(t, append([]string{"--db", path}, args...)...); err != nil {
			t.Errorf("%v on a read-only copy: %v", args, err)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("reading left files behind: %v", entries)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o400 {
		t.Errorf("mode changed to %v", st.Mode().Perm())
	}
}
