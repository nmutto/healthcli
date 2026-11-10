package cli

import (
	"strings"
	"testing"
)

// The commands that talk about widths say what happens below the 50 floor.
func TestHelpSaysTheWidthFloor(t *testing.T) {
	for _, cmd := range []string{"overview", "today", "heatmap", "trend"} {
		out, _, err := run(t, cmd, "--help")
		if err != nil {
			t.Fatal(err)
		}
		flat := strings.Join(strings.Fields(out), " ")
		if !strings.Contains(flat, "fit 50 to 100 columns") || !strings.Contains(flat, "narrower terminal is treated as 50") {
			t.Errorf("%s --help lacks the width floor:\n%s", cmd, out)
		}
	}
}
