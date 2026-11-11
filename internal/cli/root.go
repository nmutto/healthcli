// Package cli defines the healthcli command-line interface (cobra commands).
package cli

import (
	"github.com/spf13/cobra"
)

// Version, Commit and Date describe the build. GoReleaser sets them at build
// time; you can too:
//
//	go build -ldflags "-X github.com/nmutto/healthcli/internal/cli.Version=1.2.3"
var (
	Version = "0.1.0-dev"
	Commit  = "none"
	Date    = "unknown"
)

// options holds the global flags shared by every subcommand.
// Subcommands receive a pointer to it, so they see the final flag values.
type options struct {
	jsonOutput bool   // --json: print machine-readable JSON instead of text
	dbPath     string // --db: path to the SQLite file
}

// NewRootCmd builds a fresh command tree. Building it in a function (rather
// than a global variable) lets each test start from a clean state.
func NewRootCmd() *cobra.Command {
	opts := &options{}

	root := &cobra.Command{
		Use:   "healthcli",
		Short: "Local-first Apple Health data in SQLite",
		Long: `healthcli imports your Apple Health export into a local SQLite database
and lets you log, review and query your data. Nothing leaves your machine.

Exit codes:
  0  ok
  1  runtime error
  2  usage error (bad arguments, bad flags, unknown command)
  3  database not found

With --json an error is printed on stdout as
{"error": {"code": "...", "message": "..."}} and nothing else.`,
		Example: `  healthcli import apple export.zip
  healthcli log weight 72.5
  healthcli today
  healthcli overview
  healthcli trend steps --days 30
  healthcli query "SELECT * FROM samples LIMIT 5" --json`,
		// We print errors in Execute, and usage on every error is just noise.
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	// Persistent flags are inherited by all subcommands.
	root.PersistentFlags().BoolVar(&opts.jsonOutput, "json", false, "output JSON instead of human-readable text")
	root.PersistentFlags().StringVar(&opts.dbPath, "db", "", "SQLite database path (default: per-user data dir)")

	root.AddCommand(
		newLogCmd(opts),
		newTodayCmd(opts),
		newTrendCmd(opts),
		newOverviewCmd(opts),
		newHeatmapCmd(opts),
		newSleepCmd(opts),
		newWorkoutsCmd(opts),
		newQueryCmd(opts),
		newImportCmd(opts),
		newVersionCmd(opts),
	)
	// The "completion" command is added automatically by cobra.
	wireUsage(root)
	wireCompletion(root)
	return root
}
