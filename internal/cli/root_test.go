package cli

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestUnknownCommandIsAnError(t *testing.T) {
	_, _, err := run(t, "bogus")
	if err == nil {
		t.Fatal("expected an error for unknown command")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("unexpected error text: %v", err)
	}
}

func TestVersion(t *testing.T) {
	out, _, err := run(t, "version")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, Version) {
		t.Errorf("version output %q does not contain %q", out, Version)
	}
}

func TestWrongArgCountIsAnError(t *testing.T) {
	if _, _, err := run(t, "log", "weight"); err == nil {
		t.Fatal("expected an error when value is missing")
	}
}

func TestVersionShowsBuildInfo(t *testing.T) {
	out, _, err := run(t, "version")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"commit " + Commit, "built " + Date} {
		if !strings.Contains(out, want) {
			t.Errorf("version output %q missing %q", out, want)
		}
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

func TestVersionJSON(t *testing.T) {
	out, _, err := run(t, "version", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"version"`, `"commit"`, `"date"`} {
		if !strings.Contains(out, k) {
			t.Errorf("version --json missing %s: %q", k, out)
		}
	}
}
