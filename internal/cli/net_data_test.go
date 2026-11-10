package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// netSequence runs several commands against one database, in order, and
// returns the whole conversation as one golden:
//
//	$ healthcli log weight 72.5 ...
//	<what it printed>
func netSequence(t *testing.T, path string, commands ...[]string) string {
	t.Helper()
	var b strings.Builder
	for _, args := range commands {
		out, errOut, err := run(t, append([]string{"--db", path}, args...)...)
		b.WriteString("$ healthcli " + strings.Join(args, " ") + "\n" + netText(out, errOut, err))
	}
	return b.String()
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
			lg("spo2", "40", "--at", logAt),
			lg("spo2", "101", "--at", logAt),
			lg("weight", "72", "--at", "next tuesday"),
			lg("weight", "72", "--at", "2999-01-01T00:00:00Z"),
			lg("weight"),
			lg(),
			lg("weight", "72", "73"),
			rows}},
	} {
		t.Run(c.name, func(t *testing.T) {
			checkViewGolden(t, netGoldenFile(c.name), netSequence(t, tempDB(t), c.cmds...))
		})
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

var (
	elapsedLine = regexp.MustCompile(`(?m)^Elapsed:.*$`)
	sparseDir   = filepath.Join("..", "..", "testdata", "sparse", "export.xml")
)

// netImport runs an import and hides the only moving part (the elapsed time)
// and the temporary folder.
func netImport(t *testing.T, root, path string, args ...string) string {
	t.Helper()
	out := netSequence(t, path, append([]string{"import", "apple"}, args...))
	out = elapsedLine.ReplaceAllString(out, "Elapsed:    <time>")
	out = strings.ReplaceAll(out, root, "<tmp>")
	var js map[string]any
	if i := strings.Index(out, "{"); i >= 0 && json.Unmarshal([]byte(out[i:]), &js) == nil {
		delete(js, "elapsed_seconds")
		b, _ := json.MarshalIndent(js, "", "  ")
		out = out[:i] + string(b) + "\n"
	}
	return out
}

func TestNetImport(t *testing.T) {
	root := t.TempDir()
	garbage := filepath.Join(root, "export.xml")
	if err := os.WriteFile(garbage, []byte("<HealthData><Record type="), 0o644); err != nil {
		t.Fatal(err)
	}
	notAnExport := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(notAnExport, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	small, sparse := tempDB(t), tempDB(t)
	b.WriteString("# small, text, then again (all duplicates)\n")
	b.WriteString(netImport(t, root, small, smallExport))
	b.WriteString(netImport(t, root, small, smallExport))
	b.WriteString("# small, json, again\n")
	b.WriteString(netImport(t, root, tempDB(t), smallExport, "--json"))
	b.WriteString("# sparse device\n")
	b.WriteString(netImport(t, root, sparse, sparseDir))
	b.WriteString(netImport(t, root, tempDB(t), sparseDir, "--json"))
	b.WriteString("# failures\n")
	b.WriteString(netImport(t, root, tempDB(t), filepath.Join(root, "missing.zip")))
	b.WriteString(netImport(t, root, tempDB(t), garbage))
	b.WriteString(netImport(t, root, tempDB(t), notAnExport))
	b.WriteString(netImport(t, root, tempDB(t)))
	b.WriteString(netImport(t, root, tempDB(t), smallExport, smallExport))
	checkViewGolden(t, "net_import.txt", b.String())

	// What the import left behind, seen from the other commands.
	var after strings.Builder
	after.WriteString(netSequence(t, small,
		[]string{"query", "SELECT key FROM meta ORDER BY key"},
		[]string{"query", "SELECT count(*) AS samples FROM samples"},
		[]string{"query", "SELECT count(*) AS stages FROM sleep_stages"},
		[]string{"query", "SELECT count(*) AS workouts FROM workouts"}))
	checkViewGolden(t, "net_import_state.txt", after.String())
}
