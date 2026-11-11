package stats

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// withModel sets the device model of every row of one source.
func withModel(t *testing.T, conn *sql.DB, table, source, model string) {
	t.Helper()
	if _, err := conn.Exec(`UPDATE `+table+` SET device_model = ? WHERE source = ?`, model, source); err != nil {
		t.Fatal(err)
	}
}

func TestSourceClassPrefersDeviceModel(t *testing.T) {
	cases := []struct{ name, model, want string }{
		{"Alex’s Wrist", "Watch", ClassWatch},           // renamed
		{"Alex’s Phone", "iPhone", ClassIPhone},         // renamed
		{"Withings ScanWatch", "ScanWatch", ClassOther}, // "watch" in the name, not an Apple Watch
		{"Alex’s Apple Watch", "iPad", ClassOther},      // the model wins over a misleading name
		{"Alex’s Wrist", "WATCH", ClassWatch},
		{"Alex’s Apple Watch", "", ClassWatch}, // no model: the name decides, as before
		{"Alex’s iPhone", "", ClassIPhone},
		{"manual", "", ClassManual},
		{"Whatever", "", ClassOther},
	}
	for _, c := range cases {
		if got := SourceClass(c.name, c.model); got != c.want {
			t.Errorf("SourceClass(%q, %q) = %s, want %s", c.name, c.model, got, c.want)
		}
	}
}

// Both sources renamed so that no name has a class: the Watch must still win.
// (Before the device model, the tie fell to the row count and the iPhone won.)
func TestDailySeriesRenamedSourcesUseModel(t *testing.T) {
	conn := newDB(t)
	addSample(t, conn, "step_count", "2024-12-01 08:00 +0100", 1000, "Alex’s Wrist")
	addSample(t, conn, "step_count", "2024-12-01 09:00 +0100", 800, "Alex’s Phone")
	addSample(t, conn, "step_count", "2024-12-01 10:00 +0100", 700, "Alex’s Phone")
	addSample(t, conn, "step_count", "2024-12-01 11:00 +0100", 600, "Alex’s Phone")
	withModel(t, conn, "samples", "Alex’s Wrist", "Watch")
	withModel(t, conn, "samples", "Alex’s Phone", "iPhone")

	got := series(t, conn, "step_count", "2024-12-01", "2024-12-01")
	if len(got) != 1 || got[0].Value != 1000 || got[0].Source != "Alex’s Wrist" {
		t.Errorf("got %+v, want the Watch's 1000", got)
	}
}

// A third-party device with "watch" in its name must not outrank the iPhone.
func TestDailySeriesWithingsWatchDoesNotOutrankIPhone(t *testing.T) {
	conn := newDB(t)
	addSample(t, conn, "step_count", "2024-12-01 08:00 +0100", 500, "Withings ScanWatch")
	addSample(t, conn, "step_count", "2024-12-01 09:00 +0100", 900, iphone)
	withModel(t, conn, "samples", "Withings ScanWatch", "ScanWatch")
	withModel(t, conn, "samples", iphone, "iPhone")

	got := series(t, conn, "step_count", "2024-12-01", "2024-12-01")
	if len(got) != 1 || got[0].Value != 900 || got[0].Source != iphone {
		t.Errorf("got %+v, want the iPhone's 900", got)
	}
}

// With no model (an old import) the name still decides.
func TestDailySeriesEmptyModelFallsBackToName(t *testing.T) {
	conn := newDB(t)
	addSample(t, conn, "step_count", "2024-12-01 08:00 +0100", 1000, watch)
	addSample(t, conn, "step_count", "2024-12-01 09:00 +0100", 900, iphone)
	addSample(t, conn, "step_count", "2024-12-01 10:00 +0100", 900, iphone)

	got := series(t, conn, "step_count", "2024-12-01", "2024-12-01")
	if len(got) != 1 || got[0].Source != watch {
		t.Errorf("got %+v, want the Watch", got)
	}
}

func TestLatestSampleUsesModel(t *testing.T) {
	conn := newDB(t)
	addSample(t, conn, "heart_rate", "2024-12-01 08:00 +0100", 60, "Alex’s Phone")
	addSample(t, conn, "heart_rate", "2024-12-01 08:00 +0100", 70, "Alex’s Wrist")
	withModel(t, conn, "samples", "Alex’s Wrist", "Watch")
	withModel(t, conn, "samples", "Alex’s Phone", "iPhone")
	s, err := LatestSample(context.Background(), conn, "heart_rate")
	if err != nil || s.Source != "Alex’s Wrist" {
		t.Errorf("got %+v, %v; want the Watch", s, err)
	}
}

func TestSleepSourceUsesModel(t *testing.T) {
	conn := newDB(t)
	// Same night, same length, both renamed; only the model says which is the Watch.
	for _, src := range []string{"A Phone", "B Wrist"} {
		addSleep(t, conn, "asleep_unspecified", "2024-12-01 23:00 +0100", "2024-12-02 07:00 +0100", src)
	}
	withModel(t, conn, "sleep_stages", "A Phone", "iPhone")
	withModel(t, conn, "sleep_stages", "B Wrist", "Watch")
	nights, err := SleepNights(context.Background(), conn, "2024-12-02", "2024-12-02")
	if err != nil || len(nights) != 1 {
		t.Fatalf("%v %+v", err, nights)
	}
	if nights[0].Source != "B Wrist" || nights[0].SourceClass != ClassWatch {
		t.Errorf("got %s (%s), want B Wrist as watch", nights[0].Source, nights[0].SourceClass)
	}
}

func TestWorkoutDuplicatesUseModel(t *testing.T) {
	conn := newDB(t)
	addWorkout(t, conn, "running", "2024-12-01 08:00 +0100", "2024-12-01 08:30 +0100", "A Phone")
	addWorkout(t, conn, "running", "2024-12-01 08:00 +0100", "2024-12-01 08:30 +0100", "B Wrist")
	withModel(t, conn, "workouts", "A Phone", "iPhone")
	withModel(t, conn, "workouts", "B Wrist", "Watch")
	got, err := Workouts(context.Background(), conn, "2024-12-01", "2024-12-01")
	if err != nil || len(got) != 1 || got[0].Source != "B Wrist" {
		t.Errorf("got %+v, %v; want the Watch workout", got, err)
	}
}

func TestOverviewSourceCountUsesModel(t *testing.T) {
	conn := newDB(t)
	addSample(t, conn, "step_count", "2024-12-01 08:00 +0100", 1, "Alex’s Wrist")
	addSample(t, conn, "step_count", "2024-12-01 09:00 +0100", 1, "Alex’s Wrist")
	addSample(t, conn, "step_count", "2024-12-01 10:00 +0100", 1, "Withings ScanWatch")
	if _, err := conn.Exec(`INSERT INTO meta (key, value) VALUES ('source_models', ?)`,
		`{"Alex’s Wrist":"Watch","Withings ScanWatch":"ScanWatch"}`); err != nil {
		t.Fatal(err)
	}
	d, err := buildDataHealth(context.Background(), conn, nil, time.Date(2024, 12, 2, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if d.SourceCount[ClassWatch] != 2 || d.SourceCount[ClassOther] != 1 {
		t.Errorf("SourceCount = %v, want watch 2, other 1", d.SourceCount)
	}
}
