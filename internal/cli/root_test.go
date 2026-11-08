package cli

import (
	"bytes"
	"strings"
	"testing"
)

// run executes the root command with the given args and returns what it
// wrote to stdout and stderr, plus the error (if any).
func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
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

func TestStubsPrintNotImplemented(t *testing.T) {
	cases := [][]string{
		{"today"},
		{"trend", "steps"},
		{"import", "apple", "export.zip"},
	}
	for _, args := range cases {
		out, _, err := run(t, args...)
		if err != nil {
			t.Errorf("%v: unexpected error %v", args, err)
		}
		if !strings.Contains(out, "not implemented yet") {
			t.Errorf("%v: output %q missing stub message", args, out)
		}
	}
}

func TestJSONFlagOnStub(t *testing.T) {
	out, _, err := run(t, "today", "--json", "--db", "x.db")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status"`) {
		t.Errorf("expected JSON output, got %q", out)
	}
}

func TestWrongArgCountIsAnError(t *testing.T) {
	if _, _, err := run(t, "log", "weight"); err == nil {
		t.Fatal("expected an error when value is missing")
	}
}
