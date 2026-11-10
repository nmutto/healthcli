package cli

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// trend never prints a line wider than the terminal (50..100 columns).
func TestTrendFitsTheTerminalWidth(t *testing.T) {
	path := seedDB(t)
	for _, width := range []string{"50", "60", "80", "100"} {
		t.Setenv("COLUMNS", width)
		for _, args := range [][]string{
			{"trend", "steps", "--date", "2026-11-08", "-v"},
			{"trend", "weight", "--date", "2026-11-08"},
			{"trend", "sleep", "--date", "2026-11-08"},
			{"trend", "steps", "--date", "2026-12-20"},                 // empty window, hint to a date
			{"trend", "steps", "--days", "90", "--date", "2026-11-08"}, // longer than the terminal
			{"trend", "rr"}, // no data at all: import hint
		} {
			out, _, err := run(t, append([]string{"--db", path}, args...)...)
			if err != nil {
				t.Fatal(err)
			}
			limit := atoiOr(width)
			for _, line := range strings.Split(out, "\n") {
				if n := utf8.RuneCountInString(line); n > limit {
					t.Errorf("COLUMNS=%s %v: %d columns: %q", width, args, n, line)
				}
			}
		}
	}
}

func atoiOr(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
