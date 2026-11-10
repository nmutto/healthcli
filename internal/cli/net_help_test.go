package cli

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

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

// Other ways to reach the same texts, and the other root-level outputs.
func TestNetRootAndVersion(t *testing.T) {
	var b strings.Builder
	for _, args := range [][]string{
		{},
		{"-h"},
		{"help"},
		{"help", "trend"},
		{"help", "import", "apple"},
		{"help", "bogus"},
		{"bogus"},
		{"--bogus"},
		{"trend", "--bogus"},
		{"log", "-x"},
		{"version"},
		{"version", "--json"},
		{"version", "extra"},
		{"completion"},
		{"completion", "bogus"},
		{"completion", "bash", "extra"},
		{"import"},
		{"import", "bogus"},
		{"__complete", "trend", ""},
		{"__complete", "heatmap", ""},
		{"__complete", ""},
		{"__complete", "sleep", "--"},
		{"__complete", "completion", ""},
	} {
		out, errOut, err := run(t, args...)
		b.WriteString("$ healthcli " + strings.Join(args, " ") + "\n" + netText(out, errOut, err))
	}
	checkViewGolden(t, "net_root.txt", b.String())
}

// The completion scripts are long and written by cobra from the command tree,
// so the golden keeps their size and checksum: any new flag or command
// changes them.
func TestNetCompletionScripts(t *testing.T) {
	var b strings.Builder
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		out, errOut, err := run(t, "completion", shell)
		if err != nil || errOut != "" {
			t.Fatalf("%s: %v %q", shell, err, errOut)
		}
		fmt.Fprintf(&b, "%-10s %5d lines %7d bytes sha256 %x\n", shell, strings.Count(out, "\n"), len(out), sha256.Sum256([]byte(out)))
		out, _, _ = run(t, "completion", shell, "--no-descriptions")
		fmt.Fprintf(&b, "%-10s %5d lines %7d bytes sha256 %x (no descriptions)\n", shell, strings.Count(out, "\n"), len(out), sha256.Sum256([]byte(out)))
	}
	checkViewGolden(t, "net_completion_scripts.txt", b.String())
}
