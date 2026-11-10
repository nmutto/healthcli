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
