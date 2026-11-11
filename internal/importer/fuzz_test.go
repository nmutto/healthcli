package importer

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/nmutto/healthcli/internal/db"
)

// seedRecords picks, from the small fixture, the first element of every
// distinct kind (each Record type, plus Workout) so the fuzzer starts from
// realistic input instead of random bytes.
func seedRecords(t testing.TB) (header string, elements []string) {
	t.Helper()
	raw, err := os.ReadFile(smallFixture)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	header = text[:strings.Index(text, "<Record")]
	kind := regexp.MustCompile(`^<(?:Record type|Workout workoutActivityType)="([^"]+)"`)
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if m := kind.FindStringSubmatch(line); m != nil && !seen[m[1]] {
			seen[m[1]] = true
			// Multi-line elements (children) are cut at the opening tag; the
			// fuzzer will sort out the rest.
			elements = append(elements, line)
		}
	}
	return header, elements
}

// FuzzParse feeds arbitrary bytes to the XML record parser. The parser may
// return an error for bad input, but it must never panic, hang, or lose count
// of the records it read.
//
// Run it for a while with:  go test ./internal/importer -fuzz FuzzParse -fuzztime 60s
func FuzzParse(f *testing.F) {
	header, elements := seedRecords(f)
	f.Add([]byte(header + strings.Join(elements, "\n") + "</HealthData>"))
	f.Add([]byte(wrap(strings.Join(elements, ""))))
	for _, e := range elements {
		f.Add([]byte(e))
	}
	f.Add([]byte(""))

	// One in-memory database for the whole run; every input gets a
	// transaction that is rolled back, so inputs cannot affect each other.
	conn, err := db.Open(":memory:")
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { conn.Close() })

	f.Fuzz(func(t *testing.T, data []byte) {
		sum := &Summary{Skipped: map[string]int64{}, SkippedTypes: map[string]int64{}}
		w, err := newWriter(conn, sum, false)
		if err != nil {
			t.Fatal(err)
		}
		defer w.rollback()

		if err := parse(context.Background(), bytes.NewReader(data), sum, w, Options{}); err != nil {
			return // rejecting bad input is fine
		}
		if got := sum.Inserted + sum.Duplicates + sum.Conflicts + sum.SkippedTotal(); got != sum.Read {
			t.Fatalf("read %d records but accounted for %d: %+v", sum.Read, got, sum)
		}
	})
}
