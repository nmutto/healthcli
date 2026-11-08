package importer

import (
	"archive/zip"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmutto/healthcli/internal/db"
)

const smallFixture = "../../testdata/small/export.xml"

func newDB(t testing.TB) *sql.DB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func count(t *testing.T, conn *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := conn.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func wrap(body string) string {
	return `<?xml version="1.0"?><HealthData locale="en_US">` + body + `</HealthData>`
}

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

func TestUnitsAndWorkoutNormalised(t *testing.T) {
	const when = `startDate="2024-12-01 10:00:00 +0100" endDate="2024-12-01 11:00:00 +0100"`
	p := writeFile(t, "export.xml", wrap(`
<Record type="HKQuantityTypeIdentifierBodyMass" sourceName="S" unit="lb" `+when+` value="220.4623"/>
<Record type="HKQuantityTypeIdentifierActiveEnergyBurned" sourceName="S" unit="kJ" `+when+` value="418.4"/>
<Record type="HKQuantityTypeIdentifierHeartRate" sourceName="S" unit="count/min" `+when+` value="70"/>
<Workout workoutActivityType="HKWorkoutActivityTypeHighIntensityIntervalTraining" duration="30" durationUnit="min" totalDistance="5" totalDistanceUnit="km" totalEnergyBurned="300" totalEnergyBurnedUnit="kcal" sourceName="S" `+when+`/>
<Workout workoutActivityType="HKWorkoutActivityTypeYoga" duration="60" durationUnit="min" sourceName="S" `+when+`/>`))
	conn := newDB(t)
	if _, err := Import(conn, p, Options{}); err != nil {
		t.Fatal(err)
	}
	check := func(metric string, wantVal float64, wantUnit string) {
		t.Helper()
		var v float64
		var u string
		if err := conn.QueryRow("SELECT value, unit FROM samples WHERE metric = ?", metric).Scan(&v, &u); err != nil {
			t.Fatal(err)
		}
		if v != wantVal || u != wantUnit {
			t.Errorf("%s = %v %s, want %v %s", metric, v, u, wantVal, wantUnit)
		}
	}
	check("body_mass", 100, "kg")
	check("active_energy", 100, "kcal")
	check("heart_rate", 70, "bpm")

	var act string
	var dur, dist, kcal float64
	err := conn.QueryRow("SELECT activity, duration_s, distance_m, energy_kcal FROM workouts WHERE distance_m IS NOT NULL").Scan(&act, &dur, &dist, &kcal)
	if err != nil {
		t.Fatal(err)
	}
	if act != "high_intensity_interval_training" || dur != 1800 || dist != 5000 || kcal != 300 {
		t.Errorf("workout = %s %v s %v m %v kcal", act, dur, dist, kcal)
	}
	if n := count(t, conn, "SELECT COUNT(*) FROM workouts WHERE distance_m IS NULL AND energy_kcal IS NULL"); n != 1 {
		t.Errorf("workout without totals should store NULLs, got %d such rows", n)
	}
}

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
