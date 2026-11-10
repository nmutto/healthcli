package cli

import (
	"encoding/json"
	"flag"
	"strings"
	"testing"
)

// Run `go test ./internal/cli -update` to rewrite the golden files after an
// intentional change, then read the diff in git before committing it.
var update = flag.Bool("update", false, "rewrite golden files in testdata/golden")

// import small fixture -> query a summary. The fixture is generated with a
// fixed seed, so the numbers are stable. Pinned here: row counts and totals
// per metric (unit conversion), sleep per stage (mapping), workouts, the
// number of distinct sources (iPhone + Watch kept), and the skip counts.
func TestGoldenImportSmallThenQuerySummary(t *testing.T) {
	db := tempDB(t)
	var out strings.Builder

	imp, _, err := run(t, "--db", db, "import", "apple", smallExport, "--json")
	if err != nil {
		t.Fatal(err)
	}
	// elapsed_seconds changes on every run, so drop it before comparing.
	var sum map[string]any
	if err := json.Unmarshal([]byte(imp), &sum); err != nil {
		t.Fatal(err)
	}
	delete(sum, "elapsed_seconds")
	b, _ := json.MarshalIndent(sum, "", "  ") // map keys are sorted: stable
	out.WriteString("# import summary\n" + string(b) + "\n")

	for _, q := range []struct{ title, sql string }{
		{"samples per metric", `SELECT metric, unit, count(*) AS n, count(DISTINCT source) AS sources,
			round(min(value), 2) AS min, round(avg(value), 2) AS avg, round(max(value), 2) AS max
			FROM samples GROUP BY metric ORDER BY metric`},
		{"sleep per stage", `SELECT stage, count(*) AS n, round(sum(end_ts - start_ts) / 3600.0, 2) AS hours
			FROM sleep_stages GROUP BY stage ORDER BY stage`},
		{"workouts", `SELECT activity, count(*) AS n, round(sum(duration_s) / 60, 1) AS minutes,
			round(sum(distance_m) / 1000, 2) AS km, round(sum(energy_kcal), 1) AS kcal
			FROM workouts GROUP BY activity ORDER BY activity`},
		{"time zones seen", `SELECT tz_offset, count(*) AS n FROM samples GROUP BY tz_offset ORDER BY tz_offset`},
	} {
		table, _, err := run(t, "--db", db, "query", q.sql)
		if err != nil {
			t.Fatalf("%s: %v", q.title, err)
		}
		out.WriteString("\n# " + q.title + "\n" + table)
	}
	checkViewGolden(t, "import_small_summary.txt", out.String())
}
