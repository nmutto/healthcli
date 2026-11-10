package cli

import "testing"

// Like every other command, trend takes --ascii and --no-color and prints
// pure ASCII when the output is a pipe (here: a buffer).
func TestTrendIsPlainASCIIOnAPipe(t *testing.T) {
	path := seedDB(t)
	for _, extra := range [][]string{nil, {"--ascii"}, {"--no-color"}, {"--ascii", "--no-color"}} {
		args := append([]string{"--db", path, "trend", "steps", "--days", "14", "--date", "2026-11-08", "-v"}, extra...)
		out, _, err := run(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", extra, err)
		}
		for _, r := range out {
			if r > 127 {
				t.Fatalf("%v: non-ASCII %q in:\n%s", extra, r, out)
			}
		}
	}
}
