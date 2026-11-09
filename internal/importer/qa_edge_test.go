package importer

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makeZip writes a zip with the given name -> content entries (in order).
func makeZip(t *testing.T, entries ...[2]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e[1]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "export.zip")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const oneStepRecord = `<Record type="HKQuantityTypeIdentifierStepCount" sourceName="S" unit="count" ` + qaWhen + ` value="7"/>`

// ---- file shapes ----------------------------------------------------------

func TestEmptyAndNonAppleFilesGiveClearErrors(t *testing.T) {
	cases := map[string]struct{ content, wantErr string }{
		"empty file":      {"", "no <HealthData>"},
		"whitespace only": {"  \n\t ", "no <HealthData>"},
		"other xml":       {`<?xml version="1.0"?><feed><entry/></feed>`, "no <HealthData>"},
		"html":            {`<!DOCTYPE html><html><body><br></body></html>`, "XML error"},
		"binary junk":     {"\x00\x01\x02\xff\xfe", "XML error"},
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

// ---- timestamps and time zones --------------------------------------------

func TestAppleTime(t *testing.T) {
	cases := []struct {
		in     string
		ts     int64
		offset int
		ok     bool
	}{
		{"2024-12-01 18:00:00 +0100", 1733072400, 3600, true},
		{"2024-12-01 18:00:00 +0000", 1733076000, 0, true},
		{"2024-12-01 18:00:00 -0000", 1733076000, 0, true},
		{"2024-12-01 18:00:00 +0530", 1733056200, 19800, true},  // India: half-hour offset
		{"2024-12-01 18:00:00 +0545", 1733055300, 20700, true},  // Nepal: 45-minute offset
		{"2024-12-01 18:00:00 -0930", 1733110200, -34200, true}, // Marquesas: negative half hour
		{"2024-12-01 18:00:00 +1400", 1733025600, 50400, true},  // Kiribati, the largest offset
		{"1970-01-01 00:00:00 +0000", 0, 0, true},
		{"2024-02-29 23:59:59 +0100", 1709247599, 3600, true}, // leap day
		{"2023-02-29 12:00:00 +0100", 0, 0, false},            // not a leap year
		{"2024-12-01 24:00:00 +0100", 0, 0, false},
		{"2024-12-01 18:00:00", 0, 0, false}, // no offset: refuse to guess
		{"2024-12-01T18:00:00+01:00", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, c := range cases {
		ts, off, ok := appleTime(c.in)
		if ok != c.ok || ts != c.ts || off != c.offset {
			t.Errorf("appleTime(%q) = %d, %d, %v; want %d, %d, %v", c.in, ts, off, ok, c.ts, c.offset, c.ok)
		}
	}
}

// The result must not depend on the machine's own time zone. We run the same
// import under several local zones (including one whose offset equals one in
// the file, which makes Go take a different internal path) and expect
// identical rows.
func TestImportIgnoresMachineTimeZone(t *testing.T) {
	// Spring-forward night in Europe: 01:59:59 +0100 and 03:00:00 +0200 are
	// one second apart in real time even though the wall clock jumps an hour.
	p := writeFile(t, "x.xml", wrap(`
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="a" unit="count" startDate="2024-03-31 01:59:59 +0100" endDate="2024-03-31 03:00:00 +0200" value="1"/>
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="b" unit="count" startDate="2024-12-01 18:00:00 +0530" endDate="2024-12-01 18:30:00 +0530" value="2"/>
<Record type="HKCategoryTypeIdentifierSleepAnalysis" sourceName="c" startDate="2024-12-01 23:00:00 -0500" endDate="2024-12-02 07:00:00 -0500" value="HKCategoryValueSleepAnalysisAsleepCore"/>`))

	old := time.Local
	t.Cleanup(func() { time.Local = old })

	var want string
	for _, loc := range []*time.Location{
		time.UTC,
		time.FixedZone("IST", 5*3600+1800),
		time.FixedZone("CET", 3600),
		time.FixedZone("EST", -5*3600),
	} {
		time.Local = loc
		conn := newDB(t)
		if _, err := Import(conn, p, Options{}); err != nil {
			t.Fatal(err)
		}
		got := dump(t, conn)
		if want == "" {
			want = got
			// Check the actual numbers once. 01:59:59 +0100 = 00:59:59 UTC.
			if n := count(t, conn, "SELECT COUNT(*) FROM samples WHERE start_ts = 1711846799 AND end_ts = 1711846800 AND tz_offset = 3600"); n != 1 {
				t.Errorf("DST row wrong:\n%s", got)
			}
			if n := count(t, conn, "SELECT COUNT(*) FROM samples WHERE start_ts = 1733056200 AND tz_offset = 19800"); n != 1 {
				t.Errorf("+0530 row wrong:\n%s", got)
			}
			if n := count(t, conn, "SELECT COUNT(*) FROM sleep_stages WHERE end_ts - start_ts = 8*3600 AND tz_offset = -18000"); n != 1 {
				t.Errorf("sleep across midnight wrong:\n%s", got)
			}
		}
		if got != want {
			t.Errorf("rows differ when the machine zone is %v:\n%s\nwant:\n%s", loc, got, want)
		}
	}
}

// ---- idempotence ----------------------------------------------------------

// dump renders every health row (ids included, import timestamp and summary excluded) as
// text so two databases can be compared with ==.
func dump(t *testing.T, conn *sql.DB) string {
	t.Helper()
	var sb strings.Builder
	for _, q := range []string{
		"SELECT * FROM samples ORDER BY id",
		"SELECT * FROM sleep_stages ORDER BY id",
		"SELECT * FROM workouts ORDER BY id",
		"SELECT * FROM meta WHERE key NOT LIKE 'last_import_%' ORDER BY key",
	} {
		rows, err := conn.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		fmt.Fprintf(&sb, "## %s\n", q)
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(&sb, vals...)
		}
		rows.Close()
	}
	return sb.String()
}

// Import, snapshot every row, import the same file again: nothing may change
// (same rows, same ids). A brand new database must also end up identical,
// i.e. importing is deterministic.
func TestImportRoundTripIsIdentical(t *testing.T) {
	conn := newDB(t)
	if _, err := Import(conn, smallFixture, Options{}); err != nil {
		t.Fatal(err)
	}
	first := dump(t, conn)
	if strings.Count(first, "\n") < 100 {
		t.Fatalf("dump suspiciously small:\n%.300s", first)
	}

	if _, err := Import(conn, smallFixture, Options{}); err != nil {
		t.Fatal(err)
	}
	if second := dump(t, conn); second != first {
		t.Error("second import changed the database")
	}

	fresh := newDB(t)
	if _, err := Import(fresh, smallFixture, Options{}); err != nil {
		t.Fatal(err)
	}
	if again := dump(t, fresh); again != first {
		t.Error("importing into a fresh database gave different rows")
	}
}
