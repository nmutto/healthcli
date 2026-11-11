package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

// Exit codes. They are part of the command-line interface, see the root help.
const (
	exitOK       = 0
	exitRuntime  = 1 // anything that went wrong while running
	exitUsage    = 2 // bad arguments, bad flags, unknown command
	exitNotFound = 3 // the database file does not exist
)

// usageError marks a mistake in how the command was typed. Execute turns it
// into exit code 2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usageErrorf(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

// exitCodeFor picks the exit code and the short code name used in JSON errors.
func exitCodeFor(err error) (int, string) {
	var usage *usageError
	switch {
	case errors.As(err, &usage), strings.HasPrefix(err.Error(), "unknown command"):
		return exitUsage, "usage"
	case errors.Is(err, errDatabaseNotFound):
		return exitNotFound, "database_not_found"
	}
	return exitRuntime, "error"
}

// Execute runs the CLI and returns the process exit code. Ctrl-C and SIGTERM
// cancel the context that every command receives, so a long query or import
// stops cleanly. We print errors ourselves, so main has nothing to do but exit.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return execute(ctx, os.Args[1:], os.Stdout, os.Stderr)
}

// execute is Execute with its inputs spelled out, so tests can call it.
func execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := NewRootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return exitOK
	}
	code, name := exitCodeFor(err)
	if wantsJSON(root, args) {
		// With --json the error is the only thing on stdout.
		body := map[string]any{"error": map[string]string{"code": name, "message": err.Error()}}
		enc := json.NewEncoder(stdout)
		enc.SetEscapeHTML(false) // keep <export.zip> readable
		enc.Encode(body)
	} else {
		fmt.Fprintln(stderr, "Error:", err)
	}
	return code
}

// wantsJSON is true when --json was given. When the arguments failed to parse
// the flag may not have been read yet, so we also look at the raw arguments.
func wantsJSON(root *cobra.Command, args []string) bool {
	if f := root.PersistentFlags().Lookup("json"); f != nil && f.Value.String() == "true" {
		return true
	}
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "--json" || a == "--json=true" {
			return true
		}
	}
	return false
}
