package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tempDB returns a --db flag pointing at a fresh database file.
func tempDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.db")
}

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

func TestQueryTable(t *testing.T) {
	db := tempDB(t)
	run(t, "--db", db, "log", "weight", "70", "--at", "2025-03-01T08:30:00Z")
	out, _, err := run(t, "--db", db, "query", "SELECT metric, value, unit FROM samples")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "metric") || !strings.Contains(lines[1], "body_mass") {
		t.Errorf("unexpected table:\n%s", out)
	}
}

func TestQueryEmptyJSONIsArray(t *testing.T) {
	out, _, err := run(t, "--db", tempDB(t), "query", "SELECT * FROM samples", "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Errorf("want [], got %q, %v", out, err)
	}
}

func TestQueryRejectsWrites(t *testing.T) {
	db := tempDB(t)
	for _, q := range []string{"DELETE FROM samples", "  drop table samples", "INSERT INTO meta VALUES ('a','b')"} {
		if _, _, err := run(t, "--db", db, "query", q); err == nil {
			t.Errorf("%q should be rejected", q)
		}
	}
	// A write hidden inside a WITH statement must be stopped by query_only.
	q := "WITH x AS (SELECT 1) INSERT INTO meta VALUES ('a','b')"
	if _, _, err := run(t, "--db", db, "query", q); err == nil {
		t.Errorf("%q should fail", q)
	}
}
