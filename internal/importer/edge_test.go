package importer

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestBadFiles(t *testing.T) {
	conn := newDB(t)
	cases := map[string]string{
		"missing":    filepath.Join(t.TempDir(), "nope.xml"),
		"not xml":    writeFile(t, "a.xml", "just some text"),
		"wrong root": writeFile(t, "b.xml", "<Other/>"),
		"truncated":  writeFile(t, "c.xml", "<HealthData><Record "),
		"bad zip":    writeFile(t, "d.zip", "PK but not really a zip"),
	}
	for name, p := range cases {
		if _, err := Import(conn, p, Options{}); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// ---- file shapes ----------------------------------------------------------

func TestEmptyAndNonAppleFilesGiveClearErrors(t *testing.T) {
	cases := map[string]struct{ content, wantErr string }{
		"empty file":      {"", "no <HealthData>"},
		"whitespace only": {"  \n\t ", "no <HealthData>"},
		"other xml":       {`<?xml version="1.0"?><feed><entry/></feed>`, "no <HealthData>"},
		"html":            {`<!DOCTYPE html><html><body><br></body></html>`, "XML error"},
		"binary junk":     {"\x00\x01\x02\xff\xfe", "no <HealthData>"}, // the bad bytes are replaced, leaving no XML
		"truncated":       {`<HealthData>` + oneStepRecord + `<Record type="x`, "XML error"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			conn := newDB(t)
			_, err := Import(conn, writeFile(t, "x.xml", c.content), Options{})
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v, want it to mention %q", err, c.wantErr)
			}
			// A failed import must leave no health rows behind.
			if n := count(t, conn, "SELECT COUNT(*) FROM samples"); n != 0 {
				t.Errorf("%d samples left behind", n)
			}
		})
	}
}

func TestEmptyHealthDataIsFine(t *testing.T) {
	sum, err := Import(newDB(t), writeFile(t, "x.xml", wrap("")), Options{})
	if err != nil || sum.Read != 0 {
		t.Errorf("sum = %+v, err = %v; an export with no records is valid", sum, err)
	}
}

// A 5 MB attribute must not blow up or truncate: it is just text. Here it is
// the source name, so it also checks long text survives the round trip.
func TestHugeAttribute(t *testing.T) {
	huge := strings.Repeat("x", 5<<20)
	p := writeFile(t, "x.xml", wrap(
		`<Record type="HKQuantityTypeIdentifierStepCount" sourceName="`+huge+`" unit="count" `+qaWhen+` value="1"/>`+
			`<Record type="HKQuantityTypeIdentifierStepCount" sourceName="S" unit="count" `+qaWhen+` value="`+huge+`"/>`))
	conn := newDB(t)
	sum, err := Import(conn, p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Inserted != 1 || sum.Skipped[reasonBadValue] != 1 {
		t.Errorf("sum = %+v, want 1 inserted and 1 bad_value", sum)
	}
	var n int
	conn.QueryRow("SELECT length(source) FROM samples").Scan(&n)
	if n != len(huge) {
		t.Errorf("stored source length = %d, want %d", n, len(huge))
	}
}

func TestZipLayouts(t *testing.T) {
	xml := wrap(oneStepRecord)
	cases := map[string]struct {
		entries [][2]string
		wantErr string
	}{
		"export.xml at the top":   {[][2]string{{"export.xml", xml}}, ""},
		"real layout (subfolder)": {[][2]string{{"apple_health_export/export_cda.xml", "<ClinicalDocument/>"}, {"apple_health_export/export.xml", xml}}, ""},
		"deeply nested":           {[][2]string{{"a/b/c/export.xml", xml}}, ""},
		"no export.xml":           {[][2]string{{"apple_health_export/export_cda.xml", "<ClinicalDocument/>"}, {"notes.txt", "hi"}}, "contains no export.xml"},
		"empty zip":               {nil, "contains no export.xml"},
		"export.xml is junk":      {[][2]string{{"export.xml", "not xml at all"}}, "not an Apple Health export"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			sum, err := Import(newDB(t), makeZip(t, c.entries...), Options{})
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("err = %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil || sum.Inserted != 1 {
				t.Errorf("sum = %+v, err = %v, want 1 inserted", sum, err)
			}
		})
	}
}

// ---- batch boundaries -----------------------------------------------------

// The batch commits after every 5000th write. These sizes sit right on, just
// before and just after that edge. Every record has a child element, and one
// in four repeats an earlier row, so the "record straddling a commit" and
// "duplicate straddling a commit" paths are both walked.
func TestRecordsAroundBatchBoundary(t *testing.T) {
	for _, n := range []int{batchSize - 1, batchSize, batchSize + 1, 2 * batchSize, 2*batchSize + 1} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			var body strings.Builder
			wantUnique := 0
			for i := 0; i < n; i++ {
				src := i
				if i%4 == 3 {
					src = i - 3 // exact repeat of an earlier record
				} else {
					wantUnique++
				}
				fmt.Fprintf(&body, `<Record type="HKQuantityTypeIdentifierStepCount" sourceName="S%d" unit="count" %s value="1">`+
					`<MetadataEntry key="k" value="v"/></Record>`, src, qaWhen)
			}
			conn := newDB(t)
			sum, err := Import(conn, writeFile(t, "x.xml", wrap(body.String())), Options{})
			if err != nil {
				t.Fatal(err)
			}
			if int(sum.Read) != n || int(sum.Inserted) != wantUnique || int(sum.Duplicates) != n-wantUnique {
				t.Errorf("sum = %+v, want read %d inserted %d dup %d", sum, n, wantUnique, n-wantUnique)
			}
			if got := count(t, conn, "SELECT COUNT(*) FROM samples"); int(got) != wantUnique {
				t.Errorf("rows = %d, want %d", got, wantUnique)
			}
			if count(t, conn, "SELECT COUNT(*) FROM meta WHERE key='last_import_ts'") != 1 {
				t.Error("last_import_ts missing")
			}
		})
	}
}

// Documented behaviour: a broken file stops the import, batches that were
// already committed stay, the half-finished one is rolled back, and running
// the import again on a fixed file completes the job with no duplicates.
func TestPartialFailureKeepsCommittedBatchesOnly(t *testing.T) {
	var body strings.Builder
	total := batchSize + 100
	for i := 0; i < total; i++ {
		fmt.Fprintf(&body, `<Record type="HKQuantityTypeIdentifierStepCount" sourceName="S%d" unit="count" %s value="1"/>`, i, qaWhen)
	}
	good := wrap(body.String())
	broken := strings.TrimSuffix(good, "</HealthData>") + "<Record <<<"

	conn := newDB(t)
	if _, err := Import(conn, writeFile(t, "broken.xml", broken), Options{}); err == nil {
		t.Fatal("want an error for the broken file")
	}
	if n := count(t, conn, "SELECT COUNT(*) FROM samples"); n != batchSize {
		t.Errorf("rows after failure = %d, want exactly the one committed batch (%d)", n, batchSize)
	}
	if n := count(t, conn, "SELECT COUNT(*) FROM meta WHERE key='last_import_ts'"); n != 0 {
		t.Error("last_import_ts must only be set by a completed import")
	}

	sum, err := Import(conn, writeFile(t, "good.xml", good), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Inserted != 100 || sum.Duplicates != batchSize {
		t.Errorf("re-import: inserted %d dup %d, want 100 and %d", sum.Inserted, sum.Duplicates, batchSize)
	}
	if n := count(t, conn, "SELECT COUNT(*) FROM samples"); n != int64(total) {
		t.Errorf("rows after re-import = %d, want %d", n, total)
	}
}

// BUG: <Record> elements were imported even before (or without) a
// <HealthData> root. A non-Apple XML file full of <Record> tags was only
// rejected at the very end, after earlier batches had already been committed.
func TestRecordsWithoutHealthDataRootAreRejectedEarly(t *testing.T) {
	var body strings.Builder
	body.WriteString("<NotApple>")
	for i := 0; i < batchSize+10; i++ {
		fmt.Fprintf(&body, `<Record type="HKQuantityTypeIdentifierStepCount" sourceName="S%d" unit="count" %s value="1"/>`, i, qaWhen)
	}
	body.WriteString("</NotApple>")

	conn := newDB(t)
	if _, err := Import(conn, writeFile(t, "x.xml", body.String()), Options{}); err == nil {
		t.Fatal("want an error for a file with no <HealthData>")
	}
	if n := count(t, conn, "SELECT COUNT(*) FROM samples"); n != 0 {
		t.Errorf("%d rows were committed from a non-Apple file, want 0", n)
	}
}

// BUG: a file cut off after rows were already committed (every 5000 rows)
// failed with a bare XML error, and nothing said that most of it was saved.
func TestTruncatedImportSaysHowManyRowsWereSaved(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\"?>\n<HealthData locale=\"en_US\">\n")
	for i := 0; i < 12000; i++ {
		fmt.Fprintf(&sb, `<Record type="HKQuantityTypeIdentifierStepCount" sourceName="a" unit="count" startDate="2024-12-01 10:00:00 +0100" endDate="2024-12-01 10:00:%02d +0100" value="%d"/>`+"\n", i%60, i+1)
	}
	// no closing </HealthData>
	p := writeFile(t, "export.xml", sb.String())
	conn := newDB(t)
	_, err := Import(conn, p, Options{})
	if err == nil {
		t.Fatal("a truncated file should fail")
	}
	// The file is damaged, so "run the import again" would be a lie.
	want := "10000 rows were saved before the error and are kept"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
	if strings.Contains(err.Error(), "run the import again") {
		t.Errorf("error = %q, must not suggest a re-import that cannot help", err)
	}
	if n := count(t, conn, "SELECT COUNT(*) FROM samples"); n == 0 || n > 10000 {
		t.Errorf("samples = %d, want the committed rows only", n)
	}
}

// 12_000 rows crosses the 5000-row batch boundary twice. Each row differs
// only in its source, so none of them are duplicates.
func TestBatchBoundaries(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 12000; i++ {
		fmt.Fprintf(&body, `<Record type="HKQuantityTypeIdentifierStepCount" sourceName="S%d" unit="count" `+
			`startDate="2024-12-01 10:00:00 +0100" endDate="2024-12-01 11:00:00 +0100" value="1"/>`, i)
	}
	conn := newDB(t)
	sum, err := Import(conn, writeFile(t, "export.xml", wrap(body.String())), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Inserted != 12000 || count(t, conn, "SELECT COUNT(*) FROM samples") != 12000 {
		t.Errorf("inserted %d, want 12000", sum.Inserted)
	}
}
