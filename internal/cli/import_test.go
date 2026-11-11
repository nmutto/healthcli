package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	out = strings.ReplaceAll(out, filepath.Dir(path), "<dbdir>")
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

// ---- robustness ------------------------------------------------------------

// runWithContext is run, but the command gets ctx (the real program passes a
// context that Ctrl-C cancels).
func runWithContext(t *testing.T, ctx context.Context, args ...string) (stdout string, err error) {
	t.Helper()
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err = cmd.ExecuteContext(ctx)
	return out.String(), err
}

// writeExport writes an export.xml with the given body inside <HealthData>.
func writeExport(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "export.xml")
	if err := os.WriteFile(p, []byte(`<?xml version="1.0"?><HealthData locale="en_US">`+body+`</HealthData>`), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func stepRec(source string, value int) string {
	return fmt.Sprintf(`<Record type="HKQuantityTypeIdentifierStepCount" sourceName="%s" unit="count" startDate="2024-12-01 10:00:00 +0100" endDate="2024-12-01 11:00:00 +0100" value="%d"/>`, source, value)
}

func assertNoDBFiles(t *testing.T, path string) {
	t.Helper()
	for _, suffix := range []string{"", "-wal", "-shm", ".bak"} {
		if _, err := os.Stat(path + suffix); err == nil {
			t.Errorf("%s was created", path+suffix)
		}
	}
}

// A mistyped path must fail before the database is touched.
func TestImportMissingFileCreatesNoDatabase(t *testing.T) {
	db := tempDB(t)
	if _, _, err := run(t, "--db", db, "import", "apple", "no-such-export.zip"); err == nil {
		t.Fatal("want an error")
	}
	assertNoDBFiles(t, db)
}

// A file that fails halfway through parsing, into a database that did not
// exist, leaves nothing behind.
func TestFailedImportIntoNewDatabaseLeavesNoFile(t *testing.T) {
	garbage := filepath.Join(t.TempDir(), "export.xml")
	if err := os.WriteFile(garbage, []byte("<HealthData><Record type="), 0o600); err != nil {
		t.Fatal(err)
	}
	db := tempDB(t)
	if _, _, err := run(t, "--db", db, "import", "apple", garbage); err == nil {
		t.Fatal("want an error")
	}
	assertNoDBFiles(t, db)
}

// ...but a database that already existed is never deleted by a failed import.
func TestFailedImportKeepsExistingDatabase(t *testing.T) {
	db := tempDB(t)
	if _, _, err := run(t, "--db", db, "import", "apple", smallExport); err != nil {
		t.Fatal(err)
	}
	garbage := filepath.Join(t.TempDir(), "export.xml")
	os.WriteFile(garbage, []byte("<HealthData><Record type="), 0o600)
	if _, _, err := run(t, "--db", db, "import", "apple", garbage); err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(db); err != nil {
		t.Errorf("the existing database is gone: %v", err)
	}
}

func TestImportMakesBackupOfExistingDatabase(t *testing.T) {
	db := tempDB(t)
	// First import: the database is new and empty, nothing to protect.
	out, _, err := run(t, "--db", db, "import", "apple", smallExport)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Backup:") {
		t.Errorf("no backup is expected for an empty database:\n%s", out)
	}
	if _, err := os.Stat(db + ".bak"); err == nil {
		t.Fatal("backup made for an empty database")
	}

	out, _, err = run(t, "--db", db, "import", "apple", smallExport)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Backup:     "+db+".bak") {
		t.Errorf("summary does not name the backup:\n%s", out)
	}
	st, err := os.Stat(db + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("backup mode = %v, want 0600", st.Mode().Perm())
	}

	jsonOut, _, err := run(t, "--db", db, "import", "apple", smallExport, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var sum map[string]any
	json.Unmarshal([]byte(jsonOut), &sum)
	if sum["backup"] != db+".bak" {
		t.Errorf("json backup = %v", sum["backup"])
	}
}

func TestImportNoBackupFlag(t *testing.T) {
	db := tempDB(t)
	for i := 0; i < 2; i++ {
		if _, _, err := run(t, "--db", db, "import", "apple", smallExport, "--no-backup"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(db + ".bak"); err == nil {
		t.Error("--no-backup still made a backup")
	}
}

func TestImportDryRun(t *testing.T) {
	// Into a database that does not exist: counts, and no file.
	fresh := tempDB(t)
	out, _, err := run(t, "--db", fresh, "import", "apple", smallExport, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Dry run: nothing was written") || !strings.Contains(out, "Inserted:   1468") {
		t.Errorf("dry run output:\n%s", out)
	}
	assertNoDBFiles(t, fresh)

	// Into an existing one: unchanged, and no backup either.
	db := tempDB(t)
	if _, _, err := run(t, "--db", db, "import", "apple", sparseDir, "--no-backup"); err != nil {
		t.Fatal(err)
	}
	before, _, _ := run(t, "--db", db, "query", "SELECT count(*) AS n FROM samples")
	jsonOut, _, err := run(t, "--db", db, "import", "apple", smallExport, "--dry-run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var sum map[string]any
	json.Unmarshal([]byte(jsonOut), &sum)
	if sum["dry_run"] != true || sum["inserted"].(float64) == 0 {
		t.Errorf("json = %s", jsonOut)
	}
	after, _, _ := run(t, "--db", db, "query", "SELECT count(*) AS n FROM samples")
	if before != after {
		t.Errorf("dry run changed the database:\n%s\n%s", before, after)
	}
	if _, err := os.Stat(db + ".bak"); err == nil {
		t.Error("dry run made a backup")
	}
}

func TestImportReportsConflicts(t *testing.T) {
	p := writeExport(t, stepRec("S", 5)+stepRec("S", 5)+stepRec("S", 9))
	out, _, err := run(t, "--db", tempDB(t), "import", "apple", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Inserted:   1\n", "Duplicates: 1 ", "Conflicts:  1 (same key, different value"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	jsonOut, _, _ := run(t, "--db", tempDB(t), "import", "apple", p, "--json")
	var sum map[string]any
	json.Unmarshal([]byte(jsonOut), &sum)
	if sum["conflicts"] != float64(1) || sum["duplicates"] != float64(1) {
		t.Errorf("json = %s", jsonOut)
	}
}

func TestImportToleratesInvalidUTF8(t *testing.T) {
	p := writeExport(t, stepRec("S\xff", 5))
	out, _, err := run(t, "--db", tempDB(t), "import", "apple", p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Invalid bytes: 1") || !strings.Contains(out, "Inserted:   1") {
		t.Errorf("output:\n%s", out)
	}
}

// Ctrl-C: the command gets a cancelled context and must stop cleanly.
func TestImportInterrupted(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 12000; i++ {
		body.WriteString(stepRec(fmt.Sprintf("s%d", i), i))
	}
	p := writeExport(t, body.String())
	db := tempDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := runWithContext(t, ctx, "--db", db, "import", "apple", p, "--json", "--no-backup")
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("err = %v, want an interrupted error", err)
	}
	var sum map[string]any
	if err := json.Unmarshal([]byte(out), &sum); err != nil {
		t.Fatalf("the summary must still be printed: %q", out)
	}
	if sum["interrupted"] != true || sum["inserted"].(float64) == 0 {
		t.Errorf("json = %s", out)
	}
	conn := openTestDB(t, db)
	defer conn.Close()
	var n int
	conn.QueryRow("SELECT count(*) FROM samples").Scan(&n)
	if float64(n) != sum["inserted"] {
		t.Errorf("rows saved %d, summary %v", n, sum["inserted"])
	}
}

func TestImportHelpMentionsSafetyOptions(t *testing.T) {
	out, _, err := run(t, "import", "apple", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--dry-run", "--no-backup", ".bak"} {
		if !strings.Contains(out, want) {
			t.Errorf("help does not mention %q", want)
		}
	}
}
