package cli

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/nmutto/healthcli/internal/db"
)

// An iPhone alone writes only in_bed rows. Those nights have no sleep
// STAGES, so they must not show up as 0 h nights that drag the mean down.
func TestTrendSleepIgnoresInBedOnlyNights(t *testing.T) {
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		end := day(t, "2026-10-20") + int64(i)*86400 + 7*3600
		stage, src := "core", "Test Apple Watch"
		if i%2 == 1 {
			stage, src = "in_bed", "Test iPhone"
		}
		if _, err := conn.Exec(`INSERT INTO sleep_stages (stage,start_ts,end_ts,tz_offset,source) VALUES (?,?,?,0,?)`,
			stage, end-8*3600, end, src); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()

	out, _, err := run(t, "--db", path, "trend", "sleep", "--days", "10", "--date", "2026-10-29", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Summary struct {
			DaysWithData int     `json:"days_with_data"`
			Mean         float64 `json:"mean"`
		}
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if r.Summary.DaysWithData != 5 || r.Summary.Mean != 8 {
		t.Errorf("summary %+v, want 5 nights of 8 h", r.Summary)
	}
}

func TestDateAndDaysValidation(t *testing.T) {
	db := tempDB(t)
	bad := [][]string{
		{"today", "--date", "2026-13-01"},
		{"today", "--date", "2026-02-30"},
		{"today", "--date", "2026-1-5"},
		{"today", "--date", "2026-11-08T10:00:00Z"},
		{"today", "--date", " 2026-11-08"},
		{"trend", "steps", "--date", "tomorrow"},
		{"trend", "steps", "--days", "0"},
		{"trend", "steps", "--days", "-5"},
		{"trend", "steps", "--days", "3651"},
		{"trend", "steps", "--days", "99999999999999999999"},
		{"trend", "steps", "--days", "abc"},
		{"trend", "steps", "--date", "9999-12-31"}, // used to loop forever
		{"today", "--date", "0001-01-01"},
		{"today", "--date", "1899-12-31"},
		{"today", "--date", "2999-12-31", "--json"}, // the future: nothing to show
	}
	for _, args := range bad {
		if _, _, err := run(t, append([]string{"--db", db}, args...)...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
	good := [][]string{
		{"today", "--date", "2024-02-29"},
		{"trend", "steps", "--days", "1"},
		{"trend", "steps", "--days", "3650"},
		{"trend", "sleep", "--days", "3650", "--json"},
		{"trend", "steps", "--date", "1900-01-01", "--days", "3"},
		{"trend", "steps", "--date", "2999-12-31", "--days", "3650"},
	}
	for _, args := range good {
		if _, _, err := run(t, append([]string{"--db", db}, args...)...); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestJSONIsStableAndNoANSI(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	path := seedDB(t)
	for _, args := range [][]string{
		{"today", "--date", "2026-11-08", "--json"},
		{"trend", "steps", "--date", "2026-11-08", "--json"},
		{"trend", "sleep", "--date", "2026-11-08", "--json"},
		{"today", "--date", "2026-11-08"},
		{"trend", "hr", "--date", "2026-11-08", "-v"},
	} {
		a, _, err := run(t, append([]string{"--db", path}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		b, _, _ := run(t, append([]string{"--db", path}, args...)...)
		if a != b {
			t.Errorf("%v: two runs differ", args)
		}
		if strings.Contains(a, "\x1b") {
			t.Errorf("%v: escape codes in non-terminal output", args)
		}
		if strings.Contains(strings.Join(args, " "), "--json") && !json.Valid([]byte(a)) {
			t.Errorf("%v: invalid JSON", args)
		}
	}
}

// sparkline must cope with anything: empty, NaN, Inf, huge ranges.
func FuzzSparkline(f *testing.F) {
	f.Add(1.0, 2.0, 3.0)
	f.Add(math.Inf(1), math.NaN(), -math.MaxFloat64)
	f.Fuzz(func(t *testing.T, a, b, c float64) {
		vals := []float64{a, b, c, a}
		for n := 0; n <= len(vals); n++ {
			if got := []rune(sparkline(vals[:n])); len(got) != n {
				t.Fatalf("sparkline(%v) = %q, want %d runes", vals[:n], string(got), n)
			}
		}
	})
}
