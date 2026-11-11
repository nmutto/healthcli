package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// stepRecords builds n step records that differ in their start time, so none
// is a duplicate. sourceFor(i) lets a test put odd text in one of them.
func stepRecords(n int, sourceFor func(i int) string) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		src := "a"
		if sourceFor != nil {
			src = sourceFor(i)
		}
		fmt.Fprintf(&sb, `<Record type="HKQuantityTypeIdentifierStepCount" sourceName="%s" unit="count" startDate="2024-12-01 10:00:00 +0100" endDate="2024-12-01 10:00:00 +0100" value="%d"/>`+"\n", src, i+1)
	}
	return sb.String()
}

// One bad byte in 12,000 records used to abort the import with "invalid UTF-8"
// after the first batches were saved. Now the byte is replaced and counted.
func TestInvalidUTF8ByteDoesNotAbortImport(t *testing.T) {
	body := stepRecords(12000, func(i int) string {
		return fmt.Sprintf("a%d", i) // a distinct source makes each row new
	})
	bad := strings.Replace(body, `sourceName="a7000"`, "sourceName=\"a7\xff000\"", 1)
	if bad == body {
		t.Fatal("test setup: bad byte not placed")
	}
	conn := newDB(t)
	sum, err := Import(conn, writeFile(t, "export.xml", wrap(bad)), Options{})
	if err != nil {
		t.Fatalf("one bad byte must not abort the import: %v", err)
	}
	if sum.Inserted != 12000 || sum.InvalidBytes != 1 {
		t.Errorf("inserted %d, invalid bytes %d; want 12000 and 1", sum.Inserted, sum.InvalidBytes)
	}
	var src string
	if err := conn.QueryRow(`SELECT source FROM samples WHERE source LIKE 'a7%000' AND source != 'a7000'`).Scan(&src); err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(src) || src != "a7�000" {
		t.Errorf("stored source = %q, want the bad byte replaced by U+FFFD", src)
	}
}

// A control character that XML forbids is handled the same way.
func TestForbiddenControlCharacterIsReplaced(t *testing.T) {
	body := stepRecords(3, func(i int) string { return fmt.Sprintf("a%d\x01", i) })
	sum, err := Import(newDB(t), writeFile(t, "x.xml", wrap(body)), Options{})
	if err != nil || sum.Inserted != 3 || sum.InvalidBytes != 3 {
		t.Errorf("sum = %+v, err = %v; want 3 inserted, 3 invalid bytes", sum, err)
	}
}

// Valid multi-byte characters must survive wherever the 64 KB read buffer
// happens to cut them in half.
func TestMultiByteCharactersSurviveBufferBoundaries(t *testing.T) {
	const name = "Café’s 手表"
	body := stepRecords(2000, func(i int) string { return fmt.Sprintf("%s %d", name, i) })
	conn := newDB(t)
	sum, err := Import(conn, writeFile(t, "x.xml", wrap(body)), Options{})
	if err != nil || sum.Inserted != 2000 || sum.InvalidBytes != 0 {
		t.Fatalf("sum = %+v, err = %v", sum, err)
	}
	if n := count(t, conn, `SELECT count(*) FROM samples WHERE source LIKE ?`, name+" %"); n != 2000 {
		t.Errorf("%d sources kept their text, want 2000", n)
	}
}

// The decoder keeps a whole tag in memory, so one enormous attribute is
// refused with a clear error.
func TestOversizedTagIsRejected(t *testing.T) {
	huge := strings.Repeat("x", maxTokenBytes+1)
	p := writeFile(t, "x.xml", wrap(`<Record type="a" sourceName="`+huge+`"/>`))
	_, err := Import(newDB(t), p, Options{})
	if err == nil || !strings.Contains(err.Error(), "longer than 16 MiB") {
		t.Errorf("err = %v, want a size error", err)
	}
}

// ---- advice after a partial import ----------------------------------------

func TestSavedRowsNoteOnlySuggestsRerunWhenItCanHelp(t *testing.T) {
	w := &writer{committed: 5000}
	db := savedRowsNote(errors.New("writing to database: disk full"), w)
	if !strings.Contains(db.Error(), "run the import again") {
		t.Errorf("a database error should suggest a re-run: %v", db)
	}
	file := savedRowsNote(&fileError{errors.New("XML error")}, w)
	if strings.Contains(file.Error(), "run the import again") || !strings.Contains(file.Error(), "5000 rows were saved") {
		t.Errorf("a damaged file must not suggest a re-run: %v", file)
	}
	if got := savedRowsNote(errors.New("x"), &writer{}); got.Error() != "x" {
		t.Errorf("nothing saved, nothing to add: %v", got)
	}
}

// ---- checking the input ---------------------------------------------------

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]struct{ path, want string }{
		"missing":      {filepath.Join(dir, "nope.zip"), "file not found"},
		"directory":    {dir, "is not a file"},
		"zip no entry": {makeZip(t, [2]string{"notes.txt", "x"}), "contains no export.xml"},
	}
	for name, c := range cases {
		if err := Check(c.path); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
	if err := Check(smallFixture); err != nil {
		t.Errorf("small fixture: %v", err)
	}
	if err := Check(makeZip(t, [2]string{"apple_health_export/export.xml", wrap("")})); err != nil {
		t.Errorf("zip with export.xml: %v", err)
	}
}

// ---- zip bomb guards ------------------------------------------------------

func TestZipEntryOverLimitIsRefused(t *testing.T) {
	old := maxEntryBytes
	maxEntryBytes = 1 << 10
	defer func() { maxEntryBytes = old }()

	big := wrap(stepRecords(100, nil)) // far more than 1 KiB
	err := Check(makeZip(t, [2]string{"export.xml", big}))
	if err == nil || !strings.Contains(err.Error(), "safety limit") {
		t.Errorf("err = %v, want the safety limit error", err)
	}
}

func TestZipArchiveTotalOverLimitIsRefused(t *testing.T) {
	old := maxArchiveBytes
	maxArchiveBytes = 1 << 10
	defer func() { maxArchiveBytes = old }()

	p := makeZip(t, [2]string{"export.xml", wrap("")}, [2]string{"other.bin", strings.Repeat("0", 4096)})
	if err := Check(p); err == nil || !strings.Contains(err.Error(), "safety limit") {
		t.Errorf("err = %v, want the safety limit error", err)
	}
}

// The sizes written in a zip can lie, so the bytes actually unpacked are
// counted as well.
func TestLimitedReaderStopsWithAnError(t *testing.T) {
	l := &limitedReader{r: strings.NewReader(strings.Repeat("x", 100)), left: 10}
	buf := make([]byte, 64)
	var total int
	var err error
	for err == nil {
		var n int
		n, err = l.Read(buf)
		total += n
	}
	if !errors.Is(err, errTooBig) || total > 10 {
		t.Errorf("read %d bytes, err = %v; want at most 10 bytes then errTooBig", total, err)
	}
	ok := &limitedReader{r: strings.NewReader("12345"), left: 5}
	b := make([]byte, 16)
	n, _ := ok.Read(b)
	if _, err := ok.Read(b); n != 5 || err == nil || errors.Is(err, errTooBig) {
		t.Errorf("exactly the limit must be fine: n = %d, err = %v", n, err)
	}
}

// ---- Ctrl-C ---------------------------------------------------------------

func TestCancelledImportSavesWhatItReadAndSaysSo(t *testing.T) {
	p := writeFile(t, "x.xml", wrap(stepRecords(12000, func(i int) string { return fmt.Sprintf("a%d", i) })))
	conn := newDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the first look at the context stops the import

	sum, err := ImportContext(ctx, conn, p, Options{})
	if err != nil {
		t.Fatalf("an interrupted import is not an error: %v", err)
	}
	if !sum.Interrupted {
		t.Error("summary should say it was interrupted")
	}
	stored := count(t, conn, "SELECT count(*) FROM samples")
	if stored == 0 || stored >= 12000 || stored != sum.Inserted {
		t.Errorf("stored %d rows, summary inserted %d, want a partial import that matches", stored, sum.Inserted)
	}
	if n := count(t, conn, "SELECT count(*) FROM meta WHERE key = 'last_import_summary'"); n != 0 {
		t.Error("an interrupted import must not be recorded as the last complete import")
	}
	// Running again finishes the job.
	full, err := Import(conn, p, Options{})
	if err != nil || full.Interrupted || count(t, conn, "SELECT count(*) FROM samples") != 12000 {
		t.Errorf("second run: %+v, %v", full, err)
	}
}

// ---- dry run --------------------------------------------------------------

func TestDryRunCountsLikeARealRunAndWritesNothing(t *testing.T) {
	// 6 rows, one of them repeated inside the file, spread over two batches
	// worth of data so a per-batch rollback would miss the repeat.
	body := stepRecords(12000, func(i int) string { return fmt.Sprintf("a%d", i) }) +
		stepRecords(1, func(int) string { return "a0" }) // same key as the first row, same value
	p := writeFile(t, "x.xml", wrap(body))

	dry := newDB(t)
	dsum, err := Import(dry, p, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"samples", "sleep_stages", "workouts", "meta"} {
		if n := count(t, dry, "SELECT count(*) FROM "+table); n != 0 {
			t.Errorf("dry run left %d rows in %s", n, table)
		}
	}
	real, err := Import(newDB(t), p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !dsum.DryRun || dsum.Read != real.Read || dsum.Inserted != real.Inserted || dsum.Duplicates != real.Duplicates {
		t.Errorf("dry %+v differs from real %+v", dsum, real)
	}
	if dsum.Duplicates != 1 {
		t.Errorf("duplicates = %d, want the repeat inside the file to be seen", dsum.Duplicates)
	}
}

// ---- conflicts ------------------------------------------------------------

func TestConflictsAreCountedApartFromDuplicates(t *testing.T) {
	rec := func(value string) string { return record("StepCount", "count", value) }
	conn := newDB(t)

	// Inside one file: same key twice with the same value, then with another.
	sum, err := Import(conn, writeFile(t, "x.xml", wrap(rec("5")+rec("5")+rec("6"))), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Inserted != 1 || sum.Duplicates != 1 || sum.Conflicts != 1 {
		t.Errorf("sum = %+v, want 1 inserted, 1 duplicate, 1 conflict", sum)
	}
	// The stored value is untouched (dedupe behaviour is unchanged).
	if n := count(t, conn, "SELECT count(*) FROM samples WHERE value = 5"); n != 1 {
		t.Errorf("the first value must stay")
	}

	// Across imports.
	sum, err = Import(conn, writeFile(t, "y.xml", wrap(rec("5")+rec("7"))), Options{})
	if err != nil || sum.Duplicates != 1 || sum.Conflicts != 1 || sum.Inserted != 0 {
		t.Errorf("second import: %+v, %v", sum, err)
	}
	// The saved summary carries the number too.
	var saved string
	conn.QueryRow(`SELECT value FROM meta WHERE key = 'last_import_summary'`).Scan(&saved)
	if !strings.Contains(saved, `"conflicts":1`) {
		t.Errorf("saved summary = %s", saved)
	}
}

func TestWorkoutConflictsNeedADifferentValue(t *testing.T) {
	w := func(extra string) string { return `<Workout ` + wkStart + extra + `/>` }
	same := w(` totalEnergyBurned="100" totalEnergyBurnedUnit="kcal"`)
	other := w(` totalEnergyBurned="200" totalEnergyBurnedUnit="kcal"`)
	noEnergy := w("")

	sum, err := Import(newDB(t), writeFile(t, "x.xml", wrap(same+same+other+noEnergy)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Inserted != 1 || sum.Duplicates != 1 || sum.Conflicts != 2 {
		t.Errorf("sum = %+v, want 1 inserted, 1 duplicate, 2 conflicts (a missing value is a value)", sum)
	}
	// Two workouts with no energy at all are plain duplicates (NULL = NULL).
	sum, err = Import(newDB(t), writeFile(t, "y.xml", wrap(noEnergy+noEnergy)), Options{})
	if err != nil || sum.Duplicates != 1 || sum.Conflicts != 0 {
		t.Errorf("sum = %+v, %v", sum, err)
	}
}

// ---- backup ---------------------------------------------------------------

func TestBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "h.db")
	conn := newDBAt(t, dbPath)

	if got, err := Backup(context.Background(), conn, dbPath); err != nil || got != "" {
		t.Fatalf("empty database: got %q, %v; want no backup", got, err)
	}
	if _, err := Import(conn, smallFixture, Options{}); err != nil {
		t.Fatal(err)
	}
	// Twice: the second must replace the first.
	for i := 0; i < 2; i++ {
		got, err := Backup(context.Background(), conn, dbPath)
		if err != nil || got != dbPath+".bak" {
			t.Fatalf("backup #%d: %q, %v", i+1, got, err)
		}
	}
	st, err := os.Stat(dbPath + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("backup mode = %v, want 0600", st.Mode().Perm())
	}
	if _, err := os.Stat(dbPath + ".bak.tmp"); err == nil {
		t.Error("temporary backup file left behind")
	}
	bak := newDBAt(t, dbPath+".bak")
	if a, b := count(t, conn, "SELECT count(*) FROM samples"), count(t, bak, "SELECT count(*) FROM samples"); a == 0 || a != b {
		t.Errorf("samples: database %d, backup %d", a, b)
	}
}
