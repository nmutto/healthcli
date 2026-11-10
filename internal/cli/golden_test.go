package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// The "net_" goldens are a safety net for refactors: every command is run
// end to end through the cobra tree with a fixed clock and a fixed
// environment, and its whole output (stdout, error, stderr) is saved. A
// change in any word of any command then shows up as a failing test.
//
// Run through run(), the output is never a terminal, so the text is ASCII.
// Unicode screens are covered by the cases that call the render functions
// with a termInfo.

// netCase is one command run.
type netCase struct {
	name  string                    // golden file: net_<name>.txt (or .json when the name ends in _json)
	seed  func(t *testing.T) string // makes the database
	now   time.Time                 // the fixed clock
	cols  string                    // $COLUMNS ("" = unset: width 80)
	force bool                      // FORCE_COLOR=1
	env   map[string]string         // any other variable
	args  []string                  // arguments after --db <path>
}

// netOutput runs c and returns everything it printed.
func netOutput(t *testing.T, c netCase) string {
	t.Helper()
	for _, k := range []string{"COLUMNS", "FORCE_COLOR", "NO_COLOR", "TERM", "LANG", "LC_ALL", "LC_CTYPE"} {
		t.Setenv(k, "")
	}
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLUMNS", c.cols)
	if c.force {
		t.Setenv("FORCE_COLOR", "1")
	}
	for k, v := range c.env {
		t.Setenv(k, v)
	}
	if !c.now.IsZero() {
		fixClock(t, c.now)
	}
	args := c.args
	if c.seed != nil {
		args = append([]string{"--db", c.seed(t)}, args...)
	}
	out, errOut, err := run(t, args...)
	return netText(out, errOut, err)
}

// netText joins what a run printed into one readable golden.
func netText(out, errOut string, err error) string {
	var b strings.Builder
	b.WriteString(out)
	if err != nil {
		fmt.Fprintf(&b, "--- error ---\n%v\n", err)
	}
	if errOut != "" {
		fmt.Fprintf(&b, "--- stderr ---\n%s", errOut)
	}
	return b.String()
}

// netGoldenFile names the golden: *_json cases are .json files.
func netGoldenFile(name string) string {
	if strings.HasSuffix(name, "_json") {
		return "net_" + strings.TrimSuffix(name, "_json") + ".json"
	}
	return "net_" + name + ".txt"
}

// runNetCases runs every case as a subtest and compares it with its golden.
func runNetCases(t *testing.T, cases []netCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := netOutput(t, c)
			checkViewGolden(t, netGoldenFile(c.name), got)
			if !c.force && strings.Contains(got, "\x1b") {
				t.Error("escape codes without colour being asked for")
			}
		})
	}
}

// Goldens for these commands live next to the code (internal/cli/testdata),
// unlike the older ones in the repo-level testdata/. Same -update flag.
func checkViewGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s (run with -update if intended)\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}
