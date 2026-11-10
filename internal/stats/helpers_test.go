package stats

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/db"
)

const (
	watch  = "Alex’s Apple Watch"
	iphone = "Alex’s iPhone"
)

// newDB returns an empty, migrated in-memory database.
func newDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// at parses a local time like "2024-12-01 08:00 +0100" into UTC seconds and
// the offset, the way the importer stores them.
func at(t *testing.T, s string) (int64, int) {
	t.Helper()
	tm, err := time.Parse("2006-01-02 15:04 -0700", s)
	if err != nil {
		t.Fatal(err)
	}
	_, off := tm.Zone()
	return tm.Unix(), off
}

func addSample(t *testing.T, conn *sql.DB, metric, when string, value float64, source string) {
	t.Helper()
	ts, off := at(t, when)
	_, err := conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
		VALUES (?, ?, ?, ?, '', ?, ?)`, metric, ts, ts+60, value, off, source)
	if err != nil {
		t.Fatal(err)
	}
}

func addSleep(t *testing.T, conn *sql.DB, stage, from, to, source string) {
	t.Helper()
	start, off := at(t, from)
	end, _ := at(t, to)
	_, err := conn.Exec(`INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source)
		VALUES (?, ?, ?, ?, ?)`, stage, start, end, off, source)
	if err != nil {
		t.Fatal(err)
	}
}

func series(t *testing.T, conn *sql.DB, metric, from, to string) []DayValue {
	t.Helper()
	got, err := DailySeries(context.Background(), conn, metric, from, to)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// fillDays writes one Watch step sample per day for n days ending the day
// before 2024-12-29, with the values given (cycled).
func fillDays(t *testing.T, conn *sql.DB, n int, values ...float64) {
	t.Helper()
	day, _ := time.Parse(DayLayout, "2024-12-29")
	for i := 1; i <= n; i++ {
		d := day.AddDate(0, 0, -i).Format(DayLayout)
		addSample(t, conn, "step_count", d+" 12:00 +0100", values[i%len(values)], watch)
	}
}

func addWorkout(t *testing.T, conn *sql.DB, activity, from, to, source string) {
	t.Helper()
	start, off := at(t, from)
	end, _ := at(t, to)
	_, err := conn.Exec(`INSERT INTO workouts (activity, start_ts, end_ts, duration_s, energy_kcal, distance_m, tz_offset, source)
		VALUES (?, ?, ?, ?, NULL, NULL, ?, ?)`, activity, start, end, end-start, off, source)
	if err != nil {
		t.Fatal(err)
	}
}
