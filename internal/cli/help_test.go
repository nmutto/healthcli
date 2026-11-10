package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
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

// helpPaths lists every command in the tree ("" is the root), the built-in
// help and completion commands included.
func helpPaths() [][]string {
	root := NewRootCmd()
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	var paths [][]string
	var walk func(c *cobra.Command, path []string)
	walk = func(c *cobra.Command, path []string) {
		paths = append(paths, path)
		for _, sub := range c.Commands() {
			walk(sub, append(append([]string{}, path...), sub.Name()))
		}
	}
	walk(root, nil)
	return paths
}

// Every command's --help text, one golden each.
func TestNetHelpOfEveryCommand(t *testing.T) {
	for _, path := range helpPaths() {
		name := "help_root"
		if len(path) > 0 {
			name = "help_" + strings.Join(path, "_")
		}
		t.Run(name, func(t *testing.T) {
			out, errOut, err := run(t, append(append([]string{}, path...), "--help")...)
			checkViewGolden(t, "net_"+name+".txt", netText(out, errOut, err))
		})
	}
}

func TestHelpListsCommandsAndFlags(t *testing.T) {
	out, _, err := run(t, "--help")
	if err != nil {
		t.Fatalf("--help returned error: %v", err)
	}
	for _, want := range []string{"log", "today", "trend", "query", "import", "version", "completion", "--json", "--db", "Examples:"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output missing %q:\n%s", want, out)
		}
	}
}
