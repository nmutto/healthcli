package cli

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"sort"
	"testing"
)

// Every file in testdata/golden must be written by some test case. A removed
// or renamed case would otherwise leave its golden behind, unnoticed, because
// checkViewGolden only reads the files the cases name.
//
// The test re-runs this package's own test binary with -update inside a
// scratch copy of the tree whose golden folder is empty, then lists what was
// written. Anything committed but not written has no case any more.
func TestNoOrphanGoldens(t *testing.T) {
	if os.Getenv("HEALTHCLI_ORPHAN_PROBE") != "" {
		t.Skip("running inside the probe")
	}
	if testing.Short() {
		t.Skip("re-runs the whole package; skipped with -short")
	}
	if raceEnabled {
		t.Skip("re-running the whole package under -race would double its time")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	// scratch/{docs,testdata -> repo, internal/cli/testdata/golden (empty)}
	scratch := t.TempDir()
	pkg := filepath.Join(scratch, "internal", "cli")
	golden := filepath.Join(pkg, "testdata", "golden")
	if err := os.MkdirAll(golden, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, link := range [][2]string{
		{filepath.Join(repo, "docs"), filepath.Join(scratch, "docs")},
		{filepath.Join(repo, "testdata"), filepath.Join(scratch, "testdata")},
	} {
		if err := os.Symlink(link[0], link[1]); err != nil {
			t.Fatal(err)
		}
	}

	cmd := osexec.Command(exe, "-test.run", ".", "-update")
	cmd.Dir = pkg
	cmd.Env = append(os.Environ(), "HEALTHCLI_ORPHAN_PROBE=1")
	// Tests that only read a golden (the README ones) fail in the empty copy,
	// so a failing exit status is expected. Only a binary that cannot start
	// is an error.
	if out, err := cmd.CombinedOutput(); err != nil {
		if _, failed := err.(*osexec.ExitError); !failed {
			t.Fatalf("probe could not run: %v\n%s", err, out)
		}
	}

	written := dirNames(t, golden)
	var orphans []string
	for name := range dirNames(t, filepath.Join("testdata", "golden")) {
		if !written[name] {
			orphans = append(orphans, name)
		}
	}
	sort.Strings(orphans)
	for _, name := range orphans {
		t.Errorf("testdata/golden/%s is not written by any test case (delete it, or add the case)", name)
	}
	if len(written) == 0 {
		t.Error("probe wrote no goldens: the check proves nothing")
	}
}

// dirNames returns the file names in dir as a set.
func dirNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	return names
}
