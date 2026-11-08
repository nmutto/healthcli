package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// The commands below are stubs for Phase 1: they only define the shape of the
// CLI (names, arguments, help text). Real behaviour comes in later phases.

func newLogCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "log <metric> <value>",
		Short: "Record a manual measurement",
		Long:  "Log a single measurement (for example weight or mood) with the current time.",
		Example: `  health log weight 72.5
  health log mood 4`,
		Args: cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			notImplemented(cmd, opts)
		},
	}
}

func newTodayCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "today",
		Short: "Show today's summary",
		Long:  "Show a summary of today's health data: steps, sleep, workouts and manual logs.",
		Example: `  health today
  health today --json`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			notImplemented(cmd, opts)
		},
	}
}

func newTrendCmd(opts *options) *cobra.Command {
	var days int

	cmd := &cobra.Command{
		Use:   "trend <metric>",
		Short: "Show how a metric changes over time",
		Long:  "Show the trend of one metric over the last N days.",
		Example: `  health trend steps
  health trend weight --days 90`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			notImplemented(cmd, opts)
		},
	}
	cmd.Flags().IntVar(&days, "days", 30, "number of days to look back")
	return cmd
}

func newQueryCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "query <sql>",
		Short: "Run a read-only SQL query",
		Long:  "Run a read-only SQL query against the local database and print the rows.",
		Example: `  health query "SELECT count(*) FROM records"
  health query "SELECT * FROM records LIMIT 5" --json`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			notImplemented(cmd, opts)
		},
	}
}

// newImportCmd is a parent command: "import" itself does nothing, it only
// groups importers by source (apple now, maybe others later).
func newImportCmd(opts *options) *cobra.Command {
	importCmd := &cobra.Command{
		Use:   "import",
		Short: "Import data from another source",
		Long:  "Import health data from an external source into the local database.",
	}

	importCmd.AddCommand(&cobra.Command{
		Use:   "apple <export.zip|export.xml>",
		Short: "Import an Apple Health export",
		Long: `Import an Apple Health export file. In the Health app on your iPhone choose
your profile picture, then "Export All Health Data", and pass the resulting file here.`,
		Example: `  health import apple export.zip`,
		Args:    cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			notImplemented(cmd, opts)
		},
	})
	return importCmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the health version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "health", Version)
		},
	}
}
