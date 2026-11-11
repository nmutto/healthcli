package importer

import (
	"database/sql"
	"testing"
)

const watchDevice = `device="&lt;&lt;HKDevice: 0x600003b44a00&gt;, name:Apple Watch, manufacturer:Apple Inc., model:Watch, hardware:Watch6,1, software:9.0&gt;"`

func TestDeviceModel(t *testing.T) {
	cases := map[string]string{
		"<<HKDevice: 0x1>, name:Apple Watch, manufacturer:Apple Inc., model:Watch, hardware:Watch6,1, software:9.0>": "Watch",
		"<<HKDevice: 0x1>, name:iPhone, manufacturer:Apple Inc., model:iPhone, hardware:iPhone14,2, software:17.2>":  "iPhone",
		"<<HKDevice: 0x1>, name:Scale, model:ScanWatch>":                                                             "ScanWatch",
		"<<HKDevice: 0x1>, name:Scale, manufacturer:Acme>":                                                           "",
		"": "",
	}
	for in, want := range cases {
		if got := deviceModel(in); got != want {
			t.Errorf("deviceModel(%q) = %q, want %q", in, got, want)
		}
	}
}

// models returns the device_model of every row of a table, as a count per value.
func models(t *testing.T, conn *sql.DB, table string) map[string]int64 {
	t.Helper()
	rows, err := conn.Query(`SELECT device_model, count(*) FROM ` + table + ` GROUP BY device_model`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]int64{}
	for rows.Next() {
		var m string
		var n int64
		if err := rows.Scan(&m, &n); err != nil {
			t.Fatal(err)
		}
		got[m] = n
	}
	return got
}

// All three kinds of row keep the model of the device attribute.
func TestImportStoresDeviceModel(t *testing.T) {
	conn := newDB(t)
	xml := wrap(`
 <Record type="HKQuantityTypeIdentifierHeartRate" sourceName="Renamed" unit="count/min" ` + watchDevice + ` startDate="2024-12-01 07:00:00 +0100" endDate="2024-12-01 07:00:00 +0100" value="60"/>
 <Record type="HKCategoryTypeIdentifierSleepAnalysis" sourceName="Renamed" ` + watchDevice + ` startDate="2024-12-01 00:00:00 +0100" endDate="2024-12-01 07:00:00 +0100" value="HKCategoryValueSleepAnalysisAsleepCore"/>
 <Workout ` + wkStart + ` ` + watchDevice + `/>
 <Record type="HKQuantityTypeIdentifierHeartRate" sourceName="NoDevice" unit="count/min" startDate="2024-12-01 08:00:00 +0100" endDate="2024-12-01 08:00:00 +0100" value="61"/>`)
	if _, err := Import(conn, writeFile(t, "x.xml", xml), Options{}); err != nil {
		t.Fatal(err)
	}
	if got := models(t, conn, "samples"); got["Watch"] != 1 || got[""] != 1 {
		t.Errorf("samples: %v", got)
	}
	if got := models(t, conn, "sleep_stages"); got["Watch"] != 1 {
		t.Errorf("sleep_stages: %v", got)
	}
	if got := models(t, conn, "workouts"); got["Watch"] != 1 {
		t.Errorf("workouts: %v", got)
	}
	// The overview classes sources from this note, without scanning samples.
	var saved string
	if err := conn.QueryRow(`SELECT value FROM meta WHERE key = 'source_models'`).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	if want := `{"Renamed":"Watch","W":"Watch"}`; saved != want {
		t.Errorf("source_models = %s", saved)
	}
}

// Rows imported before migration 0003 have no model. Importing the same export
// again fills it in, and the counts are the ones a plain re-import gives.
func TestReimportBackfillsDeviceModel(t *testing.T) {
	conn := newDB(t)
	row := func(device, value string) string {
		return `<Record type="HKQuantityTypeIdentifierHeartRate" sourceName="W" unit="count/min" ` + device + ` startDate="2024-12-01 07:00:00 +0100" endDate="2024-12-01 07:00:00 +0100" value="` + value + `"/>`
	}
	sleep := func(device string) string {
		return `<Record type="HKCategoryTypeIdentifierSleepAnalysis" sourceName="W" ` + device + ` startDate="2024-12-01 00:00:00 +0100" endDate="2024-12-01 07:00:00 +0100" value="HKCategoryValueSleepAnalysisAsleepCore"/>`
	}
	workout := func(device string) string { return `<Workout ` + wkStart + ` ` + device + `/>` }

	// First import: an export without the attribute, as an old import was.
	if _, err := Import(conn, writeFile(t, "old.xml", wrap(row("", "60")+sleep("")+workout(""))), Options{}); err != nil {
		t.Fatal(err)
	}
	// Second: same keys; the heart-rate value differs, so it is a conflict.
	sum, err := Import(conn, writeFile(t, "new.xml", wrap(row(watchDevice, "99")+sleep(watchDevice)+workout(watchDevice))), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Inserted != 0 || sum.Duplicates != 2 || sum.Conflicts != 1 {
		t.Errorf("summary = %+v, want 0 inserted, 2 duplicates, 1 conflict", sum)
	}
	for _, table := range []string{"samples", "sleep_stages", "workouts"} {
		if got := models(t, conn, table); got["Watch"] != 1 || len(got) != 1 {
			t.Errorf("%s: %v, want the model filled in", table, got)
		}
	}
	// The stored value was not replaced.
	var v float64
	if err := conn.QueryRow(`SELECT value FROM samples`).Scan(&v); err != nil || v != 60 {
		t.Errorf("value = %v, %v; want 60 kept", v, err)
	}

	// A model that is already stored is never overwritten.
	other := `device="&lt;&lt;HKDevice: 0x1&gt;, model:iPhone&gt;"`
	if _, err := Import(conn, writeFile(t, "other.xml", wrap(row(other, "60"))), Options{}); err != nil {
		t.Fatal(err)
	}
	if got := models(t, conn, "samples"); got["Watch"] != 1 {
		t.Errorf("samples after a different model: %v", got)
	}
}
