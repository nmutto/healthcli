package importer

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmutto/healthcli/internal/db"
)

const smallFixture = "../../testdata/small/export.xml"

func newDB(t testing.TB) *sql.DB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// newDBAt is newDB with a path the test chooses.
func newDBAt(t testing.TB, path string) *sql.DB {
	t.Helper()
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func count(t *testing.T, conn *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := conn.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func wrap(body string) string {
	return `<?xml version="1.0"?><HealthData locale="en_US">` + body + `</HealthData>`
}

// record builds one Apple <Record> line for the unit-conversion tests.
func record(typ, unit, value string) string {
	return fmt.Sprintf(`<Record type="HKQuantityTypeIdentifier%s" sourceName="Watch" unit="%s" startDate="2024-12-01 07:00:00 +0100" endDate="2024-12-01 07:00:00 +0100" value="%s"/>`, typ, unit, value)
}

// makeZip writes a zip with the given name -> content entries (in order).
func makeZip(t *testing.T, entries ...[2]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e[1]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "export.zip")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const oneStepRecord = `<Record type="HKQuantityTypeIdentifierStepCount" sourceName="S" unit="count" ` + qaWhen + ` value="7"/>`

const qaWhen = `startDate="2024-12-01 10:00:00 +0100" endDate="2024-12-01 11:00:00 +0100"`

// dump renders every health row (ids included, import timestamp and summary excluded) as
// text so two databases can be compared with ==.
func dump(t *testing.T, conn *sql.DB) string {
	t.Helper()
	var sb strings.Builder
	for _, q := range []string{
		"SELECT * FROM samples ORDER BY id",
		"SELECT * FROM sleep_stages ORDER BY id",
		"SELECT * FROM workouts ORDER BY id",
		"SELECT * FROM meta WHERE key NOT LIKE 'last_import_%' ORDER BY key",
	} {
		rows, err := conn.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		fmt.Fprintf(&sb, "## %s\n", q)
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(&sb, vals...)
		}
		rows.Close()
	}
	return sb.String()
}

const wkStart = `workoutActivityType="HKWorkoutActivityTypeRunning" duration="30" durationUnit="min" sourceName="W" ` + qaWhen

// stat builds one <WorkoutStatistics> child.
func stat(typ, sum, unit string) string {
	return `<WorkoutStatistics type="HKQuantityTypeIdentifier` + typ + `" ` + qaWhen + ` sum="` + sum + `" unit="` + unit + `"/>`
}

type wkRow struct {
	dur      float64
	energy   sqlFloat
	distance sqlFloat
}

type sqlFloat struct {
	v     float64
	valid bool
}

func importOneWorkout(t *testing.T, workout string) (wkRow, *Summary) {
	t.Helper()
	conn := newDB(t)
	sum, err := Import(conn, writeFile(t, "x.xml", wrap(workout)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	var r wkRow
	var e, d *float64
	rows, err := conn.Query("SELECT duration_s, energy_kcal, distance_m FROM workouts")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		if err := rows.Scan(&r.dur, &e, &d); err != nil {
			t.Fatal(err)
		}
	}
	if e != nil {
		r.energy = sqlFloat{*e, true}
	}
	if d != nil {
		r.distance = sqlFloat{*d, true}
	}
	return r, sum
}

func near(a sqlFloat, want float64) bool { return a.valid && math.Abs(a.v-want) < 1e-3 }
