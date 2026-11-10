package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestImportAppleJSONAndIdempotent(t *testing.T) {
	db := tempDB(t)
	var inserted float64
	for i, wantNew := range []bool{true, false} {
		out, errOut, err := run(t, "--db", db, "import", "apple", smallExport, "--json")
		if err != nil {
			t.Fatal(err)
		}
		if errOut != "" {
			t.Errorf("unexpected stderr output %q", errOut)
		}
		var sum map[string]any
		if err := json.Unmarshal([]byte(out), &sum); err != nil {
			t.Fatalf("bad JSON %q: %v", out, err)
		}
		inserted = sum["inserted"].(float64)
		if (inserted > 0) != wantNew {
			t.Errorf("import #%d inserted %v rows", i+1, inserted)
		}
	}
}

func TestImportAppleTextSummary(t *testing.T) {
	out, _, err := run(t, "--db", tempDB(t), "import", "apple", smallExport)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Read:", "Inserted:", "Duplicates:", "Skipped:", "unknown_type", "Elapsed:"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
}

func TestImportAppleMissingFile(t *testing.T) {
	_, _, err := run(t, "--db", tempDB(t), "import", "apple", "no-such-export.zip")
	if err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Errorf("err = %v, want a 'file not found' error", err)
	}
}

var (
	elapsedLine = regexp.MustCompile(`(?m)^Elapsed:.*$`)
	sparseDir   = filepath.Join("..", "..", "testdata", "sparse", "export.xml")
)

// netImport runs an import and hides the only moving part (the elapsed time)
// and the temporary folder.
func netImport(t *testing.T, root, path string, args ...string) string {
	t.Helper()
	out := netSequence(t, path, append([]string{"import", "apple"}, args...))
	out = elapsedLine.ReplaceAllString(out, "Elapsed:    <time>")
	out = strings.ReplaceAll(out, root, "<tmp>")
	var js map[string]any
	if i := strings.Index(out, "{"); i >= 0 && json.Unmarshal([]byte(out[i:]), &js) == nil {
		delete(js, "elapsed_seconds")
		b, _ := json.MarshalIndent(js, "", "  ")
		out = out[:i] + string(b) + "\n"
	}
	return out
}

func TestNetImport(t *testing.T) {
	root := t.TempDir()
	garbage := filepath.Join(root, "export.xml")
	if err := os.WriteFile(garbage, []byte("<HealthData><Record type="), 0o644); err != nil {
		t.Fatal(err)
	}
	notAnExport := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(notAnExport, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	small, sparse := tempDB(t), tempDB(t)
	b.WriteString("# small, text, then again (all duplicates)\n")
	b.WriteString(netImport(t, root, small, smallExport))
	b.WriteString(netImport(t, root, small, smallExport))
	b.WriteString("# small, json, again\n")
	b.WriteString(netImport(t, root, tempDB(t), smallExport, "--json"))
	b.WriteString("# sparse device\n")
	b.WriteString(netImport(t, root, sparse, sparseDir))
	b.WriteString(netImport(t, root, tempDB(t), sparseDir, "--json"))
	b.WriteString("# failures\n")
	b.WriteString(netImport(t, root, tempDB(t), filepath.Join(root, "missing.zip")))
	b.WriteString(netImport(t, root, tempDB(t), garbage))
	b.WriteString(netImport(t, root, tempDB(t), notAnExport))
	b.WriteString(netImport(t, root, tempDB(t)))
	b.WriteString(netImport(t, root, tempDB(t), smallExport, smallExport))
	checkViewGolden(t, "net_import.txt", b.String())

	// What the import left behind, seen from the other commands.
	var after strings.Builder
	after.WriteString(netSequence(t, small,
		[]string{"query", "SELECT key FROM meta ORDER BY key"},
		[]string{"query", "SELECT count(*) AS samples FROM samples"},
		[]string{"query", "SELECT count(*) AS stages FROM sleep_stages"},
		[]string{"query", "SELECT count(*) AS workouts FROM workouts"}))
	checkViewGolden(t, "net_import_state.txt", after.String())
}
