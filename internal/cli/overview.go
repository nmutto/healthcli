package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/nmutto/healthcli/internal/db"
	"github.com/nmutto/healthcli/internal/stats"
)

// This file is the command: it fetches the report with stats.BuildOverview
// and prints it. The text is built in overview_render.go; --json prints the
// very same stats.Overview struct, so the two cannot drift apart.

func newOverviewCmd(opts *options) *cobra.Command {
	var noColor, ascii bool
	cmd := &cobra.Command{
		Use:   "overview",
		Short: "One-screen summary of every metric, sleep, workouts and data health",
		Long: `Show the latest value of each metric next to your 28-day usual, a band that
shows where it sits, a 28-day trend line and how old the reading is; then last
night's sleep, recent workouts and a data-health summary.

Each metric is anchored on its own newest day, so a metric that was not
recorded today still shows its last reading, with its age. A metric is "stale"
when that reading is more than 3 days old.

Colour means "unusual", never "good" or "bad": cyan marks a value outside your
usual range, yellow a far-out value (2+ standard deviations) or stale data.
Every colour is backed by an arrow or a word. The output is 60-100 columns wide
($COLUMNS or the terminal width). On a pipe, or with --ascii, only plain ASCII
characters are used. NO_COLOR and --no-color switch colours off; FORCE_COLOR
switches them on even for a pipe.

JSON output (--json) is the same report:
  today, empty (true when the database has no data)
  metrics[]  metric, label, unit, state (ok|no_baseline|stale|no_data), latest_day,
             age_days, value, source, baseline_days_with_data, baseline_mean,
             baseline_stddev, delta_pct, z, comparison (above|below|typical),
             spark (28 daily values ending today, null = no data)
  sleep      state, night, age_days, source, *_seconds per stage, in_bed_seconds
  workouts   days, count, duration_seconds, energy_kcal, latest
  data       samples, first_day, last_day, source_samples, last_import, gaps[]`,
		Example: `  healthcli overview
  healthcli overview --ascii --no-color
  healthcli overview --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOverview(cmd, opts, noColor, ascii)
		},
	}
	cmd.Flags().BoolVar(&noColor, "no-color", false, "never use colours")
	cmd.Flags().BoolVar(&ascii, "ascii", false, "use plain ASCII characters only")
	return cmd
}

func runOverview(cmd *cobra.Command, opts *options, noColor, ascii bool) error {
	conn, err := openDB(opts)
	if err != nil {
		return err
	}
	defer conn.Close()

	now := nowFunc()
	report, err := stats.BuildOverview(cmdContext(cmd), conn, now)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if opts.jsonOutput {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	name, err := dbDisplayName(opts)
	if err != nil {
		return err
	}
	ti := detectTerm(out, os.Getenv, noColor, ascii)
	_, err = fmt.Fprint(out, renderOverview(report, ti, overviewView{dbName: name, now: now}))
	return err
}

// dbDisplayName is the file name of the database in use ("health.db").
func dbDisplayName(opts *options) (string, error) {
	path := opts.dbPath
	if path == "" {
		var err error
		if path, err = db.DefaultPath(); err != nil {
			return "", err
		}
	}
	return filepath.Base(path), nil
}
