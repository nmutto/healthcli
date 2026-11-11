package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogWeightPounds(t *testing.T) {
	db := tempDB(t)
	out, _, err := run(t, "--db", db, "log", "weight", "160", "--unit", "lb", "--at", "2025-03-01T08:30:00Z", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad JSON %q: %v", out, err)
	}
	if got["metric"] != "body_mass" || got["unit"] != "kg" || got["source"] != "manual" || got["inserted"] != true {
		t.Errorf("unexpected result: %v", got)
	}
	if v := got["value"].(float64); v < 72.57 || v > 72.58 {
		t.Errorf("160 lb should be ~72.575 kg, got %v", v)
	}
	if got["start_ts"] != got["end_ts"] {
		t.Errorf("instant should have end_ts == start_ts: %v", got)
	}
}

func TestLogIsIdempotent(t *testing.T) {
	db := tempDB(t)
	args := []string{"--db", db, "log", "hr", "61", "--at", "2025-03-01 07:45"}
	first, _, err := run(t, args...)
	if err != nil || !strings.HasPrefix(first, "Logged heart_rate") {
		t.Fatalf("first: %q, %v", first, err)
	}
	second, _, err := run(t, args...)
	if err != nil || !strings.Contains(second, "Already logged") {
		t.Fatalf("second: %q, %v", second, err)
	}
	out, _, err := run(t, "--db", db, "query", "SELECT count(*) AS n FROM samples", "--json")
	if err != nil || !strings.Contains(out, `"n": 1`) {
		t.Errorf("want exactly one row, got %q, %v", out, err)
	}
}

func TestLogLocalTimeAndOffset(t *testing.T) {
	db := tempDB(t)
	if _, _, err := run(t, "--db", db, "log", "steps", "5000", "--at", "2025-07-01 12:00"); err != nil {
		t.Fatal(err)
	}
	local := time.Date(2025, 7, 1, 12, 0, 0, 0, time.Local)
	_, off := local.Zone()
	out, _, err := run(t, "--db", db, "query", "SELECT start_ts, tz_offset, metric FROM samples", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("bad output %q: %v", out, err)
	}
	if int64(rows[0]["start_ts"].(float64)) != local.Unix() || int(rows[0]["tz_offset"].(float64)) != off || rows[0]["metric"] != "step_count" {
		t.Errorf("unexpected row %v", rows[0])
	}
}

func TestLogErrors(t *testing.T) {
	db := tempDB(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"log", "mood", "4"}, "known metrics"},
		{[]string{"log", "weight", "abc"}, "not a number"},
		{[]string{"log", "weight", "70", "--unit", "stone"}, "not supported"},
		{[]string{"log", "weight", "70", "--at", "yesterday"}, "cannot read time"},
	}
	for _, c := range cases {
		_, _, err := run(t, append([]string{"--db", db}, c.args...)...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: want error containing %q, got %v", c.args, c.want, err)
		}
	}
}

func TestLogRoundsConvertedValue(t *testing.T) {
	db := tempDB(t)
	args := []string{"--db", db, "log", "weight", "160", "--unit", "lb", "--at", "2025-03-01T08:30:00Z", "--json"}
	out, _, err := run(t, args...)
	if err != nil || !strings.Contains(out, `"value":72.5748}`) {
		t.Fatalf("want rounded 72.5748, got %q, %v", out, err)
	}
	// Re-logging the same row must still be recognised as a duplicate.
	out, _, err = run(t, args...)
	if err != nil || !strings.Contains(out, `"inserted":false`) {
		t.Errorf("want inserted:false, got %q, %v", out, err)
	}
}

// BUG: `healthcli log steps NaN` passed ParseFloat, and `inf` was stored. NaN hit
// a confusing NOT NULL database error; Inf later broke `query --json`.
func TestLogRejectsNonFiniteValues(t *testing.T) {
	db := emptyDB(t)
	for _, v := range []string{"NaN", "inf", "1e305"} {
		_, _, err := run(t, "--db", db, "log", "steps", v)
		if err == nil || (!strings.Contains(err.Error(), "not a number") && !strings.Contains(err.Error(), "too large")) {
			t.Errorf("log steps %s: err = %v, want 'not a number' or 'too large'", v, err)
		}
	}
	out, _, err := run(t, "--db", db, "query", "SELECT count(*) AS n FROM samples", "--json")
	if err != nil || !strings.Contains(out, `"n": 0`) {
		t.Errorf("nothing should have been stored, got %q, %v", out, err)
	}
}

// `healthcli log --at "YYYY-MM-DD HH:MM"` reads local time. Across a daylight
// saving change the same wall-clock time must give different UTC instants and
// different stored offsets, and odd offsets such as +0530 must be exact.
func TestLogAtRespectsDSTAndOddOffsets(t *testing.T) {
	old := time.Local
	t.Cleanup(func() { time.Local = old })
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tz database: %v", err)
	}
	cases := []struct {
		zone    *time.Location
		at      string
		wantTS  int64
		wantOff int
	}{
		{newYork, "2025-03-08 12:00", 1741453200, -18000}, // EST, day before the change
		{newYork, "2025-03-10 12:00", 1741622400, -14400}, // EDT, day after
		{time.FixedZone("IST", 19800), "2025-03-01 08:30", 1740798000, 19800},
	}
	for _, c := range cases {
		time.Local = c.zone
		db := tempDB(t)
		if _, _, err := run(t, "--db", db, "log", "steps", "1", "--at", c.at); err != nil {
			t.Fatal(err)
		}
		out, _, err := run(t, "--db", db, "query", "SELECT start_ts, tz_offset FROM samples", "--json")
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf(`{"start_ts": %d, "tz_offset": %d}`, c.wantTS, c.wantOff)
		if !strings.Contains(out, want) {
			t.Errorf("%s in %v: got %s, want %s", c.at, c.zone, out, want)
		}
	}
}

// An explicit offset in --at is what the person meant, so that is the offset
// we store, whatever zone this machine happens to be in. "Z" means UTC (0).
func TestLogAtExplicitOffsetIsStored(t *testing.T) {
	old := time.Local
	t.Cleanup(func() { time.Local = old })
	time.Local = time.FixedZone("CET", 3600) // deliberately different from every case

	cases := []struct {
		at      string
		wantTS  int64
		wantOff int
	}{
		{"2025-03-01T08:30:00+05:30", 1740798000, 19800},
		{"2025-03-01T08:30:00-09:30", 1740852000, -34200},
		{"2025-03-01T08:30:00Z", 1740817800, 0},
		{"2025-03-01 08:30", 1740814200, 3600}, // no offset typed: machine zone
	}
	for _, c := range cases {
		db := tempDB(t)
		if _, _, err := run(t, "--db", db, "log", "steps", "1", "--at", c.at, "--json"); err != nil {
			t.Fatal(err)
		}
		out, _, err := run(t, "--db", db, "query", "SELECT start_ts, tz_offset FROM samples", "--json")
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf(`{"start_ts": %d, "tz_offset": %d}`, c.wantTS, c.wantOff)
		if !strings.Contains(out, want) {
			t.Errorf("--at %s: got %s, want %s", c.at, out, want)
		}
	}
}

// BUG: hex floats and underscores were accepted, and impossible values were
// stored (or reported as "already logged") because the duplicate check ran
// before any plausibility check.
func TestLogRejectsImplausibleInput(t *testing.T) {
	db := emptyDB(t)
	future := time.Now().Add(48 * time.Hour).Format(time.RFC3339)
	for _, args := range [][]string{
		{"weight", "0x1p6"}, {"weight", "1_0"},
		{"steps", "1e300"}, {"weight", "1e12"},
		{"spo2", "0.97"}, {"spo2", "101"}, {"body_fat_percentage", "150"},
		{"weight", "70", "--at", future},
	} {
		if _, _, err := run(t, append([]string{"--db", db, "log"}, args...)...); err == nil {
			t.Errorf("log %v should fail", args)
		}
	}
	out, _, err := run(t, "--db", db, "query", "SELECT count(*) AS n FROM samples", "--json")
	if err != nil || !strings.Contains(out, `"n": 0`) {
		t.Errorf("nothing should have been stored, got %q, %v", out, err)
	}
}

// The plausibility check runs before the duplicate check.
func TestLogImplausibleValueIsNotCalledADuplicate(t *testing.T) {
	db := tempDB(t)
	at := []string{"--at", "2025-03-01T08:30:00Z"}
	run(t, append([]string{"--db", db, "log", "weight", "70"}, at...)...)
	_, _, err := run(t, append([]string{"--db", db, "log", "weight", "1e12"}, at...)...)
	if err == nil || strings.Contains(err.Error(), "already logged") {
		t.Errorf("want a size error, got %v", err)
	}
	_, _, err = run(t, "--db", db, "log", "spo2", "0.97")
	if err == nil || !strings.Contains(err.Error(), "type 97 for 97%") {
		t.Errorf("want the 97 hint, got %v", err)
	}
}

func TestLogAcceptsPlausibleEdges(t *testing.T) {
	db := tempDB(t)
	soon := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	for _, args := range [][]string{
		{"spo2", "97", "--at", "2025-03-01 08:00"}, {"spo2", "50", "--at", "2025-03-01 09:00"},
		{"body_fat_percentage", "100"},
		{"steps", "1000000"}, {"weight", "70", "--at", soon},
	} {
		if _, _, err := run(t, append([]string{"--db", db, "log"}, args...)...); err != nil {
			t.Errorf("log %v: %v", args, err)
		}
	}
}

func TestLogHelpListsAliases(t *testing.T) {
	out, _, err := run(t, "log", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"hr", "hrv", "rhr", "rr", "spo2", "steps", "temp", "vo2", "weight", "bmi"} {
		if !strings.Contains(out, a+" (") {
			t.Errorf("log --help does not list the alias %q", a)
		}
	}
}

// Logging a different value for a reading that already exists must not claim
// success: the old value stays, so the user has to be told.
func TestLogDifferentValueSameTimeIsAnError(t *testing.T) {
	db := tempDB(t)
	if _, _, err := run(t, "--db", db, "log", "weight", "70", "--at", "2025-04-01 08:00"); err != nil {
		t.Fatal(err)
	}
	_, _, err := run(t, "--db", db, "log", "weight", "71", "--at", "2025-04-01 08:00")
	if err == nil || !strings.Contains(err.Error(), "already") || !strings.Contains(err.Error(), "70") {
		t.Fatalf("want an error naming the stored value 70, got %v", err)
	}
	// the identical value is still a harmless repeat
	if out, _, err := run(t, "--db", db, "log", "weight", "70", "--at", "2025-04-01 08:00"); err != nil || !strings.Contains(out, "Already logged") {
		t.Fatalf("repeat: %q, %v", out, err)
	}
}

func TestLogRejectsImpossibleValues(t *testing.T) {
	for _, args := range [][]string{
		{"weight", "0"}, {"hr", "0"}, {"hrv_sdnn", "0"}, {"resting_heart_rate", "0"},
		{"steps", "--", "-5"}, {"weight", "--", "-5"},
	} {
		db := emptyDB(t)
		if _, _, err := run(t, append([]string{"--db", db, "log"}, args...)...); err == nil {
			t.Errorf("log %v should fail", args)
		}
		out, _, _ := run(t, "--db", db, "query", "SELECT count(*) AS n FROM samples", "--json")
		if !strings.Contains(out, `"n": 0`) {
			t.Errorf("log %v left a row: %s", args, out)
		}
	}
	// zero steps is a legitimate reading
	if _, _, err := run(t, "--db", tempDB(t), "log", "steps", "0"); err != nil {
		t.Errorf("steps 0 should be accepted: %v", err)
	}
}

const logAt = "2025-03-01T08:30:00Z"

func TestNetLog(t *testing.T) {
	// Every case ends with a query so the stored row is part of the golden.
	rows := []string{"query", "SELECT metric, round(value, 3) AS value, unit, source, start_ts, end_ts, tz_offset FROM samples ORDER BY metric, start_ts"}
	lg := func(args ...string) []string { return append([]string{"log"}, args...) }
	for _, c := range []struct {
		name string
		cmds [][]string
	}{
		{"log_weight_kg", [][]string{lg("weight", "72.5", "--at", logAt), rows}},
		{"log_weight_lb", [][]string{lg("weight", "160", "--unit", "lb", "--at", logAt), rows}},
		{"log_weight_json", [][]string{lg("weight", "160", "--unit", "lb", "--at", logAt, "--json"), rows}},
		{"log_heart_rate_alias", [][]string{lg("hr", "61", "--at", "2025-03-01T07:45:00+01:00"), rows}},
		{"log_idempotent", [][]string{lg("hr", "61", "--at", logAt), lg("hr", "61", "--at", logAt), lg("hr", "61", "--at", logAt, "--json"), rows}},
		{"log_every_alias", [][]string{
			lg("steps", "8000", "--at", logAt), lg("rhr", "52", "--at", logAt), lg("hrv", "48.5", "--at", logAt),
			lg("rr", "15.2", "--at", logAt), lg("spo2", "97", "--at", logAt), lg("temp", "36.4", "--at", logAt),
			lg("vo2", "42", "--at", logAt), lg("bmi", "23.1", "--at", logAt), rows}},
		{"log_percent_metric", [][]string{lg("body_fat_percentage", "18.5", "--at", logAt), lg("walking_double_support", "28", "--at", logAt), rows}},
		{"log_rejections", [][]string{
			lg("bogus", "1", "--at", logAt),
			lg("weight", "abc", "--at", logAt),
			lg("weight", "-5", "--at", logAt),
			lg("weight", "0", "--at", logAt),
			lg("weight", "1e9", "--at", logAt),
			lg("weight", "NaN", "--at", logAt),
			lg("weight", "72", "--unit", "stone", "--at", logAt),
			lg("hr", "61", "--unit", "kg", "--at", logAt),
			lg("hr", "500", "--at", logAt),
			lg("hr", "300", "--at", logAt),
			lg("spo2", "40", "--at", logAt),
			lg("spo2", "101", "--at", logAt),
			lg("weight", "72", "--at", "next tuesday"),
			lg("weight", "72", "--at", "2999-01-01T00:00:00Z"),
			lg("weight"),
			lg(),
			lg("weight", "72", "73"),
			lg("weight", "72", "--at", "1980-01-01T00:00:00Z"),
			lg("--delete", "hr", "--at", "2025-03-01T08:31:00Z"),
			lg("--delete", "hr", "--at", logAt),
			rows}},
	} {
		t.Run(c.name, func(t *testing.T) {
			checkViewGolden(t, netGoldenFile(c.name), netSequence(t, tempDB(t), c.cmds...))
		})
	}
}

// One registry rule for log and the importer. BUG: log accepted `hr 9999`
// (only a generic 1e7 cap existed), refused a wrist temperature below zero
// (a legitimate delta) and refused 0 flights climbed.
func TestLogUsesTheRegistryRule(t *testing.T) {
	db := emptyDB(t)
	for _, args := range [][]string{
		{"hr", "9999"}, {"hr", "301"}, {"rhr", "251"}, {"weight", "501"}, {"blood_pressure_systolic", "301"},
		{"steps", "1000001"}, {"temp", "46"},
	} {
		if _, _, err := run(t, append([]string{"--db", db, "log"}, args...)...); err == nil {
			t.Errorf("log %v should fail", args)
		}
	}
	for _, args := range [][]string{
		{"hr", "300", "--at", "2025-03-01 08:00"}, {"temp", "--at", "2025-03-01 09:00", "--", "-0.4"},
		{"flights_climbed", "0", "--at", "2025-03-01 10:00"},
	} {
		if _, _, err := run(t, append([]string{"--db", db, "log"}, args...)...); err != nil {
			t.Errorf("log %v: %v", args, err)
		}
	}
}

func TestLogAtBounds(t *testing.T) {
	db := emptyDB(t)
	for _, at := range []string{"1989-12-31 23:59", "1925-03-01T08:30:00Z"} {
		_, _, err := run(t, "--db", db, "log", "weight", "70", "--at", at)
		if err == nil || !strings.Contains(err.Error(), "before 1990-01-01") {
			t.Errorf("--at %s: err = %v, want 'before 1990-01-01'", at, err)
		}
	}
	if _, _, err := run(t, "--db", db, "log", "weight", "70", "--at", "1990-01-01T00:00:00Z"); err != nil {
		t.Errorf("1990-01-01 should be accepted: %v", err)
	}
}

// A local time that does not exist (spring gap) or exists twice (autumn
// repeat) is refused instead of being filed an hour off.
func TestLogAtRefusesDSTGapAndRepeat(t *testing.T) {
	old := time.Local
	t.Cleanup(func() { time.Local = old })
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tz database: %v", err)
	}
	time.Local = newYork
	db := emptyDB(t)
	for at, want := range map[string]string{
		"2025-03-09 02:30": "does not exist",
		"2025-11-02 01:30": "happens twice",
	} {
		_, _, err := run(t, "--db", db, "log", "weight", "70", "--at", at)
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "offset") {
			t.Errorf("--at %s: err = %v, want %q and a hint about an offset", at, err, want)
		}
	}
	// the hours either side, and an explicit offset, are fine
	for _, at := range []string{"2025-03-09 01:30", "2025-03-09 03:30", "2025-11-02 00:30", "2025-11-02 02:30", "2025-11-02T01:30:00-04:00"} {
		if _, _, err := run(t, "--db", db, "log", "weight", "70", "--at", at); err != nil {
			t.Errorf("--at %s: %v", at, err)
		}
	}
}

func TestLogDelete(t *testing.T) {
	db := tempDB(t)
	if _, _, err := run(t, "--db", db, "log", "hr", "61", "--at", "2025-03-01 07:45"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "--db", db, "log", "hr", "70", "--at", "2025-03-01 08:45"); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, "--db", db, "log", "--delete", "hr", "--at", "2025-03-01 07:45")
	if err != nil || out != "Deleted heart_rate = 61 bpm at 2025-03-01 07:45\n" {
		t.Fatalf("delete: %q, %v", out, err)
	}
	q, _, _ := run(t, "--db", db, "query", "SELECT value FROM samples WHERE source = 'manual'", "--json")
	if !strings.Contains(q, "70") || strings.Contains(q, "61") {
		t.Errorf("only the other reading should stay, got %s", q)
	}
	// second time: nothing left to delete
	_, _, err = run(t, "--db", db, "log", "--delete", "hr", "--at", "2025-03-01 07:45")
	if err == nil || !strings.Contains(err.Error(), "no manual heart_rate reading") {
		t.Errorf("want 'no manual heart_rate reading', got %v", err)
	}
}

// BUG-class check: --delete must never remove an imported row, even when it
// sits at exactly the requested instant.
func TestLogDeleteRefusesImportedRows(t *testing.T) {
	db := seedDB(t)
	conn := openTestDB(t, db)
	ts := int64(1740817800)
	if _, err := conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
		VALUES ('heart_rate', ?, ?, 58, 'bpm', 0, 'Apple Watch')`, ts, ts); err != nil {
		t.Fatal(err)
	}
	_, _, err := run(t, "--db", db, "log", "--delete", "hr", "--at", "2025-03-01T08:30:00Z")
	if err == nil || !strings.Contains(err.Error(), "only readings typed") {
		t.Fatalf("want a refusal, got %v", err)
	}
	var n int
	conn.QueryRow(`SELECT COUNT(*) FROM samples WHERE source = 'Apple Watch' AND start_ts = ?`, ts).Scan(&n)
	if n != 1 {
		t.Errorf("the imported row was deleted (%d left)", n)
	}
}

func TestLogDeleteInputs(t *testing.T) {
	db := tempDB(t)
	run(t, "--db", db, "log", "hr", "61", "--at", logAt)
	for want, args := range map[string][]string{
		"needs --at":         {"log", "--delete", "hr"},
		"cannot be used":     {"log", "--delete", "hr", "--at", logAt, "--unit", "bpm"},
		"known metrics":      {"log", "--delete", "mood", "--at", logAt},
		"accepts 1 arg(s)":   {"log", "--delete", "hr", "61", "--at", logAt},
		"database not found": {"--db", filepath.Join(t.TempDir(), "none.db"), "log", "--delete", "hr", "--at", logAt},
		"cannot read time":   {"log", "--delete", "hr", "--at", "soon"},
	} {
		if !strings.HasPrefix(args[0], "--db") {
			args = append([]string{"--db", db}, args...)
		}
		if _, _, err := run(t, args...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: err = %v, want %q", args, err, want)
		}
	}
	out, _, err := run(t, "--db", db, "log", "--delete", "hr", "--at", logAt, "--json")
	if err != nil || !strings.Contains(out, `"deleted":true`) || !strings.Contains(out, `"value":61`) {
		t.Errorf("json delete: %q, %v", out, err)
	}
}
