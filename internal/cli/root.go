// Package cli defines the health command-line interface (cobra commands).
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version is the program version. It can be overridden at build time with:
//
//	go build -ldflags "-X github.com/muttoni/health/internal/cli.Version=1.2.3"
var Version = "0.1.0-dev"

// options holds the global flags shared by every subcommand.
// Subcommands receive a pointer to it, so they see the final flag values.
type options struct {
	jsonOutput bool   // --json: print machine-readable JSON instead of text
	dbPath     string // --db: path to the SQLite file
}

// Execute runs the CLI and returns the process exit code.
// We print errors ourselves (to stderr) so main has nothing to do but exit.
func Execute() int {
	root := NewRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	return 0
}

// NewRootCmd builds a fresh command tree. Building it in a function (rather
// than a global variable) lets each test start from a clean state.
func NewRootCmd() *cobra.Command {
	opts := &options{}

	root := &cobra.Command{
		Use:   "health",
		Short: "Local-first Apple Health data in SQLite",
		Long: `health imports your Apple Health export into a local SQLite database
and lets you log, review and query your data. Nothing leaves your machine.`,
		Example: `  health import apple export.zip
  health log weight 72.5
  health today
  health trend steps --days 30
  health query "SELECT * FROM records LIMIT 5" --json`,
		// We print errors in Execute, and usage on every error is just noise.
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	// Persistent flags are inherited by all subcommands.
	root.PersistentFlags().BoolVar(&opts.jsonOutput, "json", false, "output JSON instead of human-readable text")
	root.PersistentFlags().StringVar(&opts.dbPath, "db", "", "path to the SQLite database (default: per-user data directory)")

	root.AddCommand(
		newLogCmd(opts),
		newTodayCmd(opts),
		newTrendCmd(opts),
		newQueryCmd(opts),
		newImportCmd(opts),
		newVersionCmd(),
	)
	// The "completion" command is added automatically by cobra.
	return root
}

// notImplemented is the shared body of the Phase 1 stub commands.
//
// TODO(architect): once internal/db exists, open the database here using
// opts.dbPath (falling back to the default location when empty) and pass it
// to the real command implementation. Commands must close it when done.
func notImplemented(cmd *cobra.Command, opts *options) {
	if opts.jsonOutput {
		fmt.Fprintln(cmd.OutOrStdout(), `{"status":"not implemented yet"}`)
		return
	}
	fmt.Fprintln(cmd.OutOrStdout(), "not implemented yet")
}
