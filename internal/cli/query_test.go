package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// countRows runs a SELECT through the CLI and returns its single number.
func countRows(t *testing.T, db, table string) string {
	t.Helper()
	out, _, err := run(t, "--db", db, "query", "SELECT count(*) AS n FROM "+table, "--json")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestQueryGuardCannotBeBypassed uses the exact attacks that used to empty
// tables: a second statement that switches the read-only pragma off.
func TestQueryGuardCannotBeBypassed(t *testing.T) {
	db := tempDB(t)
	run(t, "--db", db, "log", "weight", "70", "--at", "2025-03-01T08:30:00Z")
	metaBefore := countRows(t, db, "meta")
	samplesBefore := countRows(t, db, "samples")

	elsewhere := filepath.Join(t.TempDir(), "created.db")
	for _, q := range []string{
		"select 1; pragma query_only=0; delete from meta",
		"select 1; pragma query_only=off; delete from samples",
		"select 1; attach '" + elsewhere + "' as x",
		"pragma query_only=0",
		"attach '" + elsewhere + "' as x",
		"explain attach '" + elsewhere + "' as x",
	} {
		if _, _, err := run(t, "--db", db, "query", q); err == nil {
			t.Errorf("%q should be rejected", q)
		}
		if got := countRows(t, db, "meta"); got != metaBefore {
			t.Errorf("after %q meta changed: %s -> %s", q, metaBefore, got)
		}
		if got := countRows(t, db, "samples"); got != samplesBefore {
			t.Errorf("after %q samples changed: %s -> %s", q, samplesBefore, got)
		}
	}
	if _, err := os.Stat(elsewhere); err == nil {
		t.Errorf("ATTACH created %s", elsewhere)
	}
}

func TestQueryAcceptsReadsAndComments(t *testing.T) {
	db := tempDB(t)
	run(t, "--db", db, "log", "weight", "70", "--at", "2025-03-01T08:30:00Z")
	for _, q := range []string{
		"select 1",
		"select 1;",
		"  SELECT 1 ; -- done",
		"/* x */ select 1",
		"/* a */ -- b\nselect 1",
		"with x as (select 1 as a) select a from x",
		"explain select 1",
		"explain query plan select * from samples",
		"select ';' as semi, 'it''s' as q",
		`select 1 as "a;b"`,
		"select 1 /* ; */",
	} {
		if _, _, err := run(t, "--db", db, "query", q); err != nil {
			t.Errorf("%q should work: %v", q, err)
		}
	}
}

func TestQueryRejectsSecondStatement(t *testing.T) {
	_, _, err := run(t, "--db", tempDB(t), "query", "select 1; select 2")
	if err == nil || !strings.Contains(err.Error(), "one statement") {
		t.Errorf("want a one-statement error, got %v", err)
	}
}

func TestQueryJSONEdgeCases(t *testing.T) {
	db := emptyDB(t)
	cases := []struct{ sql, want string }{
		{"select 1e999 as big, -1e999 as small", `[` + "\n" + `  {"big": null, "small": null}` + "\n" + `]` + "\n"},
		{"select x'00ff10' as b, 'text' as t", `[` + "\n" + `  {"b": "00ff10", "t": "text"}` + "\n" + `]` + "\n"},
		{"select 1 as a, 2 as a, 3 as a, 4 as a_2", `[` + "\n" + `  {"a": 1, "a_2": 2, "a_3": 3, "a_2_2": 4}` + "\n" + `]` + "\n"},
	}
	for _, c := range cases {
		out, _, err := run(t, "--db", db, "query", c.sql, "--json")
		if err != nil || out != c.want {
			t.Errorf("%s:\n got %q, %v\nwant %q", c.sql, out, err, c.want)
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
	out, _, err := run(t, "--db", emptyDB(t), "query", "SELECT * FROM samples", "--json")
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

func TestQueryJSONKeepsColumnOrder(t *testing.T) {
	db := tempDB(t)
	run(t, "--db", db, "log", "hr", "60", "--at", "2025-03-01T08:30:00Z")
	out, _, err := run(t, "--db", db, "query", "SELECT value, metric, unit AS a FROM samples", "--json")
	if err != nil {
		t.Fatal(err)
	}
	want := "[\n  {\"value\": 60, \"metric\": \"heart_rate\", \"a\": \"bpm\"}\n]\n"
	if out != want {
		t.Errorf("got %q, want %q", out, want)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Errorf("output is not valid JSON: %v", err)
	}
}

func TestNetQuery(t *testing.T) {
	apple := seedOverview(t, "import:small")
	q := func(args ...string) []string { return append([]string{"query"}, args...) }
	var b strings.Builder
	for _, args := range [][]string{
		q("SELECT metric, unit, count(*) AS n, count(DISTINCT source) AS sources, round(avg(value), 2) AS avg FROM samples GROUP BY metric ORDER BY metric"),
		q("SELECT metric, unit, count(*) AS n, round(avg(value), 2) AS avg FROM samples GROUP BY metric ORDER BY metric", "--json"),
		q("SELECT DISTINCT source FROM samples ORDER BY source"),
		q("SELECT DISTINCT source FROM samples ORDER BY source", "--json"),
		q("SELECT 1 AS one, 'two' AS two, NULL AS empty_cell, 2.5 AS three, x'ff' AS bytes"),
		q("SELECT 1 AS one, 'two' AS two, NULL AS empty_cell, 2.5 AS three, x'ff' AS bytes", "--json"),
		q("SELECT * FROM samples WHERE 0"),
		q("SELECT * FROM samples WHERE 0", "--json"),
		q("SELECT stage, count(*) AS n FROM sleep_stages GROUP BY stage ORDER BY stage"),
		q("SELECT activity, count(*) AS n, round(sum(distance_m)) AS metres FROM workouts GROUP BY activity ORDER BY activity", "--json"),
		q("WITH d AS (SELECT date(start_ts, 'unixepoch') AS day FROM samples WHERE metric = 'step_count') SELECT count(DISTINCT day) AS days FROM d"),
		q("EXPLAIN SELECT 1"),
		q("SELECT key FROM meta ORDER BY key"),
		q("SELECT 'a very long text value that goes on and on to see how a wide cell is drawn in the table output' AS wide, 1 AS n"),
		q("SELECT 'héllo ’ wörld' AS accent, 1 AS n"),
		// Refused or failing:
		q("DELETE FROM samples"),
		q("SELECT 1; DELETE FROM samples"),
		q("PRAGMA writable_schema = 1"),
		q("ATTACH DATABASE ':memory:' AS x"),
		q("SELEC 1"),
		q("SELECT * FROM nonexistent"),
		q("SELECT nope FROM samples"),
		q(""),
		q("  "),
		q(),
		q("SELECT 1", "SELECT 2"),
		q("DROP TABLE samples", "--json"),
		q("SELEC 1", "--json"),
	} {
		b.WriteString(netSequence(t, apple, args))
	}
	b.WriteString(netSequence(t, "/nonexistent-dir/none.db", q("SELECT 1")))
	checkViewGolden(t, "net_query.txt", b.String())
	// Nothing above may have changed the database.
	count := q("SELECT (SELECT count(*) FROM samples) AS samples, (SELECT count(*) FROM sleep_stages) AS stages, (SELECT count(*) FROM workouts) AS workouts")
	if before, after := netSequence(t, seedOverview(t, "import:small"), count), netSequence(t, apple, count); before != after {
		t.Errorf("the queries changed the database:\n%s\n%s", before, after)
	}
}
