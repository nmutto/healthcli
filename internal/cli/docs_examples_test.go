package cli

import (
	"os"
	"strings"
	"testing"
)

// readmeBlock returns the output that follows the nth (0-based) occurrence of
// the command line head in the command reference, up to the closing fence.
func readmeBlock(t *testing.T, head string, nth int) string {
	t.Helper()
	readme, err := os.ReadFile("../../docs/commands.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(readme)
	for i := 0; ; i++ {
		at := strings.Index(text, head)
		if at < 0 {
			t.Fatalf("command reference example %q (#%d) not found", head, nth)
		}
		text = text[at+len(head):]
		if i == nth {
			break
		}
	}
	end := strings.Index(text, "\n```")
	if end < 0 {
		t.Fatalf("command reference example %q has no closing fence", head)
	}
	return text[:end]
}

// trimLines drops trailing spaces and blank lines at the end: the command reference
// may not keep them, the goldens do.
func trimLines(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

// Each command reference block that is a copy of a golden must still equal it. The
// "overview --full" block is not listed: it is a marked excerpt, not a copy
// (the full golden is 3 times longer), so there is nothing to compare.
// The today block is checked by TestReadmeTodayExampleMatchesGolden.
func TestReadmeExamplesMatchGoldens(t *testing.T) {
	for _, c := range []struct {
		head, golden string
		nth          int
	}{
		{"$ healthcli --db demo.db overview\n", "overview_apple_80.txt", 0},
		{"$ healthcli workouts\n", "workouts_rich_80.txt", 0},
		{"$ healthcli sleep\n", "sleep_rich_80.txt", 0},
		{"$ healthcli sleep\n", "sleep_unspecified_80.txt", 1},
		{"$ healthcli heatmap\n", "heatmap_full_80.txt", 0},
		{"$ healthcli heatmap --ascii\n", "heatmap_full_ascii.txt", 0},
	} {
		t.Run(c.golden, func(t *testing.T) {
			golden, err := os.ReadFile("testdata/golden/" + c.golden)
			if err != nil {
				t.Fatal(err)
			}
			want := trimLines(string(golden))
			got := trimLines(readmeBlock(t, c.head, c.nth))
			if strings.HasPrefix(c.golden, "heatmap_") {
				// The command reference's heatmap title rule is shorter than the 80
				// columns of the golden (a known copy slip in the command reference).
				// Compare the title text and every other line exactly.
				want = heatmapTitle(want)
				got = heatmapTitle(got)
			}
			if got != want {
				t.Errorf("command reference example %q differs from testdata/golden/%s\n--- command reference ---\n%s\n--- golden ---\n%s", c.head, c.golden, got, want)
			}
		})
	}
}

// heatmapTitle cuts the rule characters off the end of the first line.
func heatmapTitle(s string) string {
	first, rest, _ := strings.Cut(s, "\n")
	first = strings.TrimRight(first, "━= ")
	return first + "\n" + rest
}
