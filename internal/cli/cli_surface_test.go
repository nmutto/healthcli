package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// exec runs the real Execute path in-process and returns code, stdout, stderr.
func exec(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := execute(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestExitCodes(t *testing.T) {
	good := seedOverview(t, "import:small")
	missing := filepath.Join(t.TempDir(), "none.db")
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"ok", []string{"--db", good, "query", "select 1"}, 0},
		{"version", []string{"version"}, 0},
		{"runtime: bad sql", []string{"--db", good, "query", "select * from nope"}, 1},
		{"runtime: unknown metric", []string{"--db", good, "trend", "nope"}, 1},
		{"usage: unknown command", []string{"frobnicate"}, 2},
		{"usage: unknown flag", []string{"--db", good, "today", "--nope"}, 2},
		{"usage: missing arg", []string{"--db", good, "trend"}, 2},
		{"usage: extra arg", []string{"overview", "x"}, 2},
		{"usage: bad flag value", []string{"--db", good, "trend", "steps", "--days", "abc"}, 2},
		{"usage: negative limit", []string{"--db", good, "query", "select 1", "--limit", "-1"}, 2},
		{"usage: days out of range", []string{"--db", good, "trend", "steps", "--days", "0"}, 2},
		{"usage: bad date", []string{"--db", good, "today", "--date", "yesterday"}, 2},
		{"not found", []string{"--db", missing, "today"}, 3},
		{"not found: query", []string{"--db", missing, "query", "select 1"}, 3},
	}
	for _, c := range cases {
		code, _, _ := exec(t, c.args...)
		if code != c.want {
			t.Errorf("%s: exit %d, want %d", c.name, code, c.want)
		}
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("a read command created the database")
	}
}

func TestJSONErrorIsOnlyThingOnStdout(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.db")
	for _, c := range []struct {
		args []string
		code string
		exit int
	}{
		{[]string{"--db", missing, "today", "--json"}, "database_not_found", 3},
		{[]string{"trend", "--json"}, "usage", 2},
		{[]string{"frobnicate", "--json"}, "usage", 2},
		{[]string{"--db", missing, "log", "nope", "1", "--json"}, "error", 1},
	} {
		code, out, errOut := exec(t, c.args...)
		if code != c.exit || errOut != "" {
			t.Errorf("%v: exit %d, stderr %q", c.args, code, errOut)
		}
		var body struct {
			Error struct{ Code, Message string }
		}
		if err := json.Unmarshal([]byte(out), &body); err != nil {
			t.Errorf("%v: stdout is not JSON: %q", c.args, out)
			continue
		}
		if body.Error.Code != c.code || body.Error.Message == "" {
			t.Errorf("%v: got %+v, want code %s", c.args, body.Error, c.code)
		}
	}
}

func TestPlainErrorGoesToStderr(t *testing.T) {
	code, out, errOut := exec(t, "frobnicate")
	if code != 2 || out != "" || !strings.HasPrefix(errOut, "Error: unknown command") {
		t.Errorf("got %d, %q, %q", code, out, errOut)
	}
}

func TestUsageMessagesSayWhatToType(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"trend"}, "trend needs a metric, e.g. healthcli trend steps"},
		{[]string{"log", "weight"}, "log needs a metric and a value"},
		{[]string{"query"}, "query needs one SQL statement"},
		{[]string{"import", "apple"}, "import apple needs the export file"},
		{[]string{"overview", "x"}, `overview takes no arguments, got "x"`},
		{[]string{"today", "--nope"}, `run "healthcli today --help" to see the flags`},
	} {
		_, _, errOut := exec(t, c.args...)
		if !strings.Contains(errOut, c.want) || strings.Contains(errOut, "accepts") {
			t.Errorf("%v: stderr %q lacks %q", c.args, errOut, c.want)
		}
	}
}

func TestRootHelpDocumentsExitCodes(t *testing.T) {
	out, _, _ := run(t, "--help")
	for _, s := range []string{"Exit codes:", "usage error", "database not found", `{"error"`} {
		if !strings.Contains(out, s) {
			t.Errorf("root help lacks %q", s)
		}
	}
}

func TestExecuteCancelledContextStopsQuery(t *testing.T) {
	db := seedOverview(t, "import:small")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	code := execute(ctx, []string{"--db", db, "query", "select 1"}, &out, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "canceled") {
		t.Errorf("got %d, %q", code, errOut.String())
	}
}

// --- query: limit, streaming, timeout ---

const countTo = "with recursive c(x) as (select 1 union all select x+1 from c limit %d) select x from c"

func TestQueryLimit(t *testing.T) {
	db := emptyDB(t)
	sql := fmt.Sprintf(countTo, 1500)
	// Default limit is 1000, with a note on stderr.
	out, errOut, err := run(t, "--db", db, "query", sql)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "\n"); n != 1001 { // header + 1000 rows
		t.Errorf("default: %d lines, want 1001", n)
	}
	if !strings.Contains(errOut, "1000 rows shown, more exist; use --limit") {
		t.Errorf("stderr: %q", errOut)
	}
	// Exactly at the limit: no note.
	_, errOut, _ = run(t, "--db", db, "query", fmt.Sprintf(countTo, 5), "--limit", "5")
	if errOut != "" {
		t.Errorf("no more rows, but stderr %q", errOut)
	}
	// JSON stays valid and has the limited number of rows.
	out, errOut, _ = run(t, "--db", db, "query", sql, "--json", "--limit", "3")
	var rows []map[string]int
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 3 {
		t.Errorf("json: %v, %d rows", err, len(rows))
	}
	if !strings.Contains(errOut, "3 rows shown, more exist") {
		t.Errorf("json stderr: %q", errOut)
	}
	// 0 means no limit.
	out, errOut, _ = run(t, "--db", db, "query", sql, "--limit", "0")
	if strings.Count(out, "\n") != 1501 || errOut != "" {
		t.Errorf("limit 0: %d lines, stderr %q", strings.Count(out, "\n"), errOut)
	}
}

func TestQueryTimeout(t *testing.T) {
	forever := "with recursive c(x) as (select 1 union all select x+1 from c) select count(*) from c"
	start := time.Now()
	code, _, errOut := exec(t, "--db", emptyDB(t), "query", forever, "--timeout", "200ms")
	if code != 1 || !strings.Contains(errOut, "query stopped after 200ms") {
		t.Errorf("got %d, %q", code, errOut)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("the timeout did not stop the query")
	}
}

// A row-by-row writer must not hold the result: 1 million rows with --limit 0
// should not grow the heap by anything near their size (they are about 30 MB
// as JSON).
func TestQueryStreamsRows(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	db := emptyDB(t)
	var out countingWriter
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--db", db, "query", fmt.Sprintf(countTo, 1000000), "--limit", "0", "--timeout", "0", "--json"})
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	peak := uint64(0)
	out.onWrite = func() {
		if out.writes%100000 == 0 {
			runtime.ReadMemStats(&after)
			if after.HeapAlloc > peak {
				peak = after.HeapAlloc
			}
		}
	}
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.writes < 1000000 {
		t.Fatalf("only %d writes", out.writes)
	}
	if growth := int64(peak) - int64(before.HeapAlloc); growth > 20<<20 {
		t.Errorf("heap grew by %d MB while streaming", growth>>20)
	}
}

type countingWriter struct {
	writes  int
	onWrite func()
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.onWrite != nil {
		w.onWrite()
	}
	return len(p), nil
}

func TestQueryRejectionMessageNamesAllKinds(t *testing.T) {
	_, _, err := run(t, "--db", emptyDB(t), "query", "delete from samples")
	if err == nil || !strings.Contains(err.Error(), "SELECT, WITH or EXPLAIN") {
		t.Errorf("got %v", err)
	}
}

// --- version ---

func TestApplyBuildInfo(t *testing.T) {
	saved := [3]string{Version, Commit, Date}
	defer func() { Version, Commit, Date = saved[0], saved[1], saved[2] }()
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v1.2.3"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef"},
			{Key: "vcs.time", Value: "2026-11-11T10:00:00Z"},
		},
	}
	Version, Commit, Date = defaultVersion, "none", "unknown"
	applyBuildInfo(info)
	if Version != "1.2.3" || Commit != "0123456" || Date != "2026-11-11T10:00:00Z" {
		t.Errorf("got %s %s %s", Version, Commit, Date)
	}
	// "(devel)" keeps the default version.
	Version, Commit, Date = defaultVersion, "none", "unknown"
	applyBuildInfo(&debug.BuildInfo{Main: debug.Module{Version: "(devel)"}})
	if Version != defaultVersion {
		t.Errorf("devel build changed Version to %s", Version)
	}
	// ldflags win.
	Version, Commit, Date = "9.9.9", "abc", "today"
	applyBuildInfo(info)
	if Version != "9.9.9" || Commit != "abc" || Date != "today" {
		t.Errorf("ldflags values were overwritten: %s %s %s", Version, Commit, Date)
	}
}

// --- completion ---

func complete(t *testing.T, args ...string) string {
	t.Helper()
	out, _, err := run(t, append([]string{cobra.ShellCompRequestCmd}, append(args, "")...)...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCompletion(t *testing.T) {
	for _, c := range []struct {
		args    []string
		want    []string
		notWant []string
	}{
		{[]string{"trend"}, []string{"step_count", "heart_rate"}, nil},
		{[]string{"heatmap"}, []string{"step_count"}, nil},
		{[]string{"log"}, []string{"body_mass"}, nil},
		{[]string{"log", "weight"}, nil, []string{"step_count"}}, // the value is a number
		{[]string{"trend", "steps"}, nil, []string{"step_count"}},
		{[]string{"overview", "--category"}, []string{"vitals", "sleep", "workouts"}, nil},
		{[]string{"today", "--category"}, []string{"vitals", "mobility"}, nil},
		{[]string{"overview", "--heatmap"}, []string{"steps", "sleep", "workouts", "step_count"}, nil},
		{[]string{"sleep", "--by"}, []string{"asleep", "bedtime", "efficiency"}, nil},
		{[]string{"workouts", "--by"}, []string{"type"}, nil},
	} {
		out := complete(t, c.args...)
		for _, w := range c.want {
			if !strings.Contains(out, w+"\n") {
				t.Errorf("%v: completion lacks %s:\n%s", c.args, w, out)
			}
		}
		for _, w := range c.notWant {
			if strings.Contains(out, w) {
				t.Errorf("%v: completion has %s:\n%s", c.args, w, out)
			}
		}
	}
	// A comma list keeps what was typed before the comma.
	out, _, _ := run(t, cobra.ShellCompRequestCmd, "overview", "--category", "vitals,sl")
	if !strings.Contains(out, "vitals,sleep\n") {
		t.Errorf("comma list: %s", out)
	}
	// A prefix narrows the metric list.
	out, _, _ = run(t, cobra.ShellCompRequestCmd, "trend", "step")
	if !strings.Contains(out, "step_count\n") || strings.Contains(out, "heart_rate") {
		t.Errorf("prefix: %s", out)
	}
}
