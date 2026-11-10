package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmutto/healthcli/internal/db"
)

// emptyDB returns a --db flag pointing at a database that exists but has no
// rows. `query` never creates a database, so tests that look for "nothing was
// stored" need one up front.
func emptyDB(t *testing.T) string {
	t.Helper()
	path := tempDB(t)
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	return path
}

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
