package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nmutto/healthcli/internal/stats"
)

func newWorkoutsCmd(opts *options) *cobra.Command {
	var days, weeks int
	var by string
	var noColor, ascii bool
	cmd := &cobra.Command{
		Use:   "workouts",
		Short: "Workouts: type table, weekly volume, bests, recent sessions and a calendar",
		Long: `Show your workouts over the last --days days (default 28): totals per activity
type with a share bar, weekly training time (--weeks, default 8) with a change
against the previous four weeks, personal bests over 90 days (activities with
at least 3 sessions), every session of the last 7 days and a calendar heatmap of workout
minutes per day. Days without a workout in the calendar are rest days (shown
from your first workout on). --by type draws each day's main activity as a
letter.

Columns with no data (no calories or distance recorded) are left out, and parts
that need more sessions simply do not appear. The weekly deltas are not
coloured green or red: more training is not automatically better.

JSON output (--json):
  today, days, count, duration_seconds, energy_kcal, distance_km (both left out
  when never recorded), active_days, by_type[], weeks[] (start,
  duration_seconds, count, partial, days, delta_pct), bests[] (kind, activity, day, ..., new),
  recent[] (day, activity, start, end, duration_seconds, energy_kcal,
  distance_km)
All durations are seconds and absent values are left out, never null.`,
		Example: `  healthcli workouts
  healthcli workouts --days 90 --weeks 12
  healthcli workouts --by type`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if days < 1 || days > 365 {
				return fmt.Errorf("--days must be between 1 and 365, got %d", days)
			}
			if weeks < 1 || weeks > stats.MaxHeatmapWeeks {
				return fmt.Errorf("--weeks must be between 1 and %d, got %d", stats.MaxHeatmapWeeks, weeks)
			}
			if by != "" && strings.ToLower(by) != "type" {
				return fmt.Errorf("--by must be \"type\", got %q", by)
			}
			conn, err := openDB(opts)
			if err != nil {
				return err
			}
			defer conn.Close()
			today := nowFunc().Format(stats.DayLayout)
			report, err := stats.BuildWorkoutsReport(cmdContext(cmd), conn, today, days, weeks)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if opts.jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}
			ti := detectTerm(out, os.Getenv, noColor, ascii)
			lines := renderWorkoutsSection(report, ti, ti.width, workoutsOpts{Full: true, Weeks: weeks, ByType: by != ""})
			_, err = fmt.Fprintln(out, strings.Join(lines, "\n"))
			return err
		},
	}
	cmd.Flags().IntVar(&days, "days", 28, "window for totals and types (1-365); the recent list is always the last 7 days")
	cmd.Flags().IntVar(&weeks, "weeks", 8, "weeks of volume bars and calendar (1-53)")
	cmd.Flags().StringVar(&by, "by", "", `"type": draw the calendar with one letter per activity`)
	cmd.Flags().BoolVar(&noColor, "no-color", false, "never use colours")
	cmd.Flags().BoolVar(&ascii, "ascii", false, "use plain ASCII characters only")
	return cmd
}
