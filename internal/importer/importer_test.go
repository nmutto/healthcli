package importer

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportSmallFixture(t *testing.T) {
	conn := newDB(t)
	sum, err := Import(conn, smallFixture, Options{})
	if err != nil {
		t.Fatal(err)
	}

	total := count(t, conn, "SELECT COUNT(*) FROM samples") +
		count(t, conn, "SELECT COUNT(*) FROM sleep_stages") +
		count(t, conn, "SELECT COUNT(*) FROM workouts")
	if total != sum.Inserted {
		t.Errorf("rows in db = %d, summary says inserted %d", total, sum.Inserted)
	}
	if sum.Read != sum.Inserted+sum.Duplicates+sum.SkippedTotal() {
		t.Errorf("read %d != inserted %d + duplicates %d + skipped %d",
			sum.Read, sum.Inserted, sum.Duplicates, sum.SkippedTotal())
	}
	// The fixture documents three malformed records and nine exact repeats.
	if got := sum.SkippedTotal(); got != 3 {
		t.Errorf("skipped = %d (%v), want 3", got, sum.Skipped)
	}
	if sum.Duplicates != 9 {
		t.Errorf("duplicates = %d, want 9", sum.Duplicates)
	}
	for _, table := range []string{"samples", "sleep_stages", "workouts"} {
		if count(t, conn, "SELECT COUNT(*) FROM "+table) == 0 {
			t.Errorf("no rows in %s", table)
		}
	}
	if count(t, conn, "SELECT COUNT(*) FROM meta WHERE key = 'last_import_ts'") != 1 {
		t.Error("last_import_ts not recorded")
	}
}

func TestLosslessKeepsBothSources(t *testing.T) {
	conn := newDB(t)
	if _, err := Import(conn, smallFixture, Options{}); err != nil {
		t.Fatal(err)
	}
	n := count(t, conn, `SELECT COUNT(DISTINCT source) FROM samples WHERE metric = 'step_count'`)
	if n < 2 {
		t.Errorf("step_count has %d source(s), want iPhone and Watch rows both kept", n)
	}
}

func TestImportTwiceInsertsNothing(t *testing.T) {
	conn := newDB(t)
	first, err := Import(conn, smallFixture, Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Import(conn, smallFixture, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Inserted != 0 {
		t.Errorf("second import inserted %d rows, want 0", second.Inserted)
	}
	if second.Duplicates != first.Inserted+first.Duplicates {
		t.Errorf("second import duplicates = %d, want %d", second.Duplicates, first.Inserted+first.Duplicates)
	}
}

func TestImportZip(t *testing.T) {
	xmlBytes, err := os.ReadFile(smallFixture)
	if err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(t.TempDir(), "export.zip")
	f, _ := os.Create(zipPath)
	zw := zip.NewWriter(f)
	// A decoy entry first: only export.xml must be read.
	d, _ := zw.Create("apple_health_export/export_cda.xml")
	d.Write([]byte("<ClinicalDocument/>"))
	w, _ := zw.Create("apple_health_export/export.xml")
	w.Write(xmlBytes)
	zw.Close()
	f.Close()

	fromXML, err := Import(newDB(t), smallFixture, Options{})
	if err != nil {
		t.Fatal(err)
	}
	fromZip, err := Import(newDB(t), zipPath, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if fromZip.Inserted != fromXML.Inserted || fromZip.Read != fromXML.Read {
		t.Errorf("zip: %+v, xml: %+v", fromZip, fromXML)
	}
}

func TestMalformedRecordsAreSkippedAndCounted(t *testing.T) {
	const start = `startDate="2024-12-01 10:00:00 +0100" endDate="2024-12-01 10:01:00 +0100"`
	p := writeFile(t, "export.xml", wrap(`
<Record type="HKQuantityTypeIdentifierHeartRate" sourceName="W" unit="count/min" `+start+` value="60"/>
<Record type="HKQuantityTypeIdentifierHeartRate" sourceName="W" unit="count/min" `+start+`/>
<Record type="HKQuantityTypeIdentifierHeartRate" sourceName="W" unit="count/min" `+start+` value="fast"/>
<Record type="HKQuantityTypeIdentifierHeartRate" sourceName="W" unit="count/min" startDate="2024-13-45 99:99:99 +0100" endDate="2024-12-01 10:01:00 +0100" value="61"/>
<Record type="HKQuantityTypeIdentifierHeartRate" sourceName="W" unit="furlongs" `+start+` value="62"/>
<Record type="HKQuantityTypeIdentifierDietaryWater" sourceName="W" `+start+` value="1"/>
<Record type="HKCategoryTypeIdentifierSleepAnalysis" sourceName="W" `+start+` value="HKCategoryValueSleepAnalysisBogus"/>
<Workout workoutActivityType="HKWorkoutActivityTypeRunning" duration="x" durationUnit="min" sourceName="W" `+start+`/>
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="W" unit="count" `+start+` value="10"/>`))

	conn := newDB(t)
	sum, err := Import(conn, p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{
		reasonMissingValue: 1, reasonBadValue: 1, reasonBadDate: 1,
		reasonBadUnit: 1, reasonUnknownType: 1, reasonBadSleep: 1, reasonBadWorkout: 1,
	}
	for reason, n := range want {
		if sum.Skipped[reason] != n {
			t.Errorf("skipped[%s] = %d, want %d", reason, sum.Skipped[reason], n)
		}
	}
	if sum.Inserted != 2 {
		t.Errorf("inserted = %d, want the 2 good records", sum.Inserted)
	}
}

func TestTimezoneOffsetPreserved(t *testing.T) {
	p := writeFile(t, "export.xml", wrap(`
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="W" unit="count" startDate="2024-07-01 08:00:00 -0500" endDate="2024-07-01 09:00:00 -0500" value="100"/>
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="W" unit="count" startDate="2024-12-01 08:00:00 +0100" endDate="2024-12-01 09:00:00 +0100" value="100"/>`))
	conn := newDB(t)
	if _, err := Import(conn, p, Options{}); err != nil {
		t.Fatal(err)
	}
	// 08:00 at -0500 is 13:00 UTC; 08:00 at +0100 is 07:00 UTC.
	cases := []struct {
		start int64
		tz    int
	}{{1719838800, -18000}, {1733036400, 3600}}
	for _, c := range cases {
		if n := count(t, conn, "SELECT COUNT(*) FROM samples WHERE start_ts = ? AND tz_offset = ?", c.start, c.tz); n != 1 {
			t.Errorf("no row with start_ts=%d tz_offset=%d", c.start, c.tz)
		}
	}
}

func TestImportNewTypesIdempotent(t *testing.T) {
	conn := newDB(t)
	path := writeFile(t, "export.xml", wrap(record("OxygenSaturation", "%", "0.97")))
	if _, err := Import(conn, path, Options{}); err != nil {
		t.Fatal(err)
	}
	sum, err := Import(conn, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Inserted != 0 || sum.Duplicates != 1 {
		t.Errorf("re-import: inserted %d, duplicates %d; want 0 and 1", sum.Inserted, sum.Duplicates)
	}
}

func TestImportRecordsSkippedTypes(t *testing.T) {
	conn := newDB(t)
	path := writeFile(t, "export.xml", wrap(
		record("AudioExposure", "dBASPL", "70")+
			record("AudioExposure", "dBASPL", "71")+
			record("WalkingSpeed", "furlongs", "5")+ // known type, bad unit: not an unknown_type
			record("UVExposure", "count", "1")))
	sum, err := Import(conn, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Skipped[reasonUnknownType] != 3 || sum.Skipped[reasonBadUnit] != 1 {
		t.Fatalf("skipped = %v", sum.Skipped)
	}
	top := sum.TopSkippedTypes(5)
	if len(top) != 2 || top[0].Type != "HKQuantityTypeIdentifierAudioExposure" || top[0].Count != 2 {
		t.Errorf("top skipped types = %+v", top)
	}
}

// BUG: the progress check ran after EVERY start tag, not just after records.
// Once Read hit a multiple of 100000, each child element (MetadataEntry...)
// that followed fired the callback again with the same number.
func TestProgressFiresOncePerMilestone(t *testing.T) {
	var body strings.Builder
	for i := 0; i < progressEvery; i++ {
		fmt.Fprintf(&body, `<Record type="HKQuantityTypeIdentifierStepCount" sourceName="S%d" unit="count" %s value="1"`, i, qaWhen)
		if i == progressEvery-1 {
			// The last record has children: they used to re-trigger progress.
			body.WriteString(`><MetadataEntry key="a" value="1"/><MetadataEntry key="b" value="2"/></Record>`)
		} else {
			body.WriteString(`/>`)
		}
	}
	var calls []int64
	_, err := Import(newDB(t), writeFile(t, "export.xml", wrap(body.String())),
		Options{Progress: func(n int64) { calls = append(calls, n) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != progressEvery {
		t.Errorf("progress calls = %v, want exactly [%d]", calls, progressEvery)
	}
}

// ---- idempotence ----------------------------------------------------------

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
