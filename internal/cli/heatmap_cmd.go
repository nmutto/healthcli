package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nmutto/healthcli/internal/metrics"
	"github.com/nmutto/healthcli/internal/stats"
)

func newHeatmapCmd(opts *options) *cobra.Command {
	var weeks int
	var noColor, ascii bool
	cmd := &cobra.Command{
		Use:   "heatmap [metric]",
		Short: "Calendar heatmap of one metric, one cell per day",
		Long: `Draw a GitHub-style calendar: one row per weekday (Mon to Sun), one column
per week, newest week on the right. Shades are the metric's own quintiles over
the window (the legend lists the cut points), so the darkest fifth of days is
always the highest fifth for that metric. A day without data shows a dot, not
the lowest shade; for workouts a dot means a rest day. Today is marked.

The metric defaults to step_count; "sleep" (time asleep per day) and
"workouts" (workout minutes per day, with rest days) work too. --weeks sets the
maximum number of weeks (default 12); fewer are shown if the terminal is too
narrow (oldest dropped). Pages fit 50 to 100 columns ($COLUMNS or the terminal
width); a narrower terminal is treated as 50, so lines may wrap there.

JSON output (--json):
  metric, unit, today, from, thresholds (4 upper bounds of levels 1-4),
  days_with_data, weeks[7 days each]: day, value (left out = no data), level
  (1-5; 0 = no data, or a rest day with value 0 for workouts), future (true after today)`,
		Example: `  healthcli heatmap
  healthcli heatmap active_energy --weeks 26
  healthcli heatmap sleep
  healthcli heatmap workouts --weeks 26
  healthcli heatmap steps --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "step_count"
			if len(args) == 1 {
				name = args[0]
			}
			return runHeatmap(cmd, opts, name, weeks, noColor, ascii)
		},
	}
	cmd.Flags().IntVar(&weeks, "weeks", defaultHeatWeeks, "maximum number of weeks to show (1-53)")
	cmd.Flags().BoolVar(&noColor, "no-color", false, "never use colours")
	cmd.Flags().BoolVar(&ascii, "ascii", false, "use plain ASCII characters only")
	return cmd
}

// heatmapName resolves the argument: "sleep", "workouts" or a registry metric
// (aliases allowed). The error for a typo also lists sleep and workouts.
func heatmapName(arg string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case stats.SleepMetric:
		return stats.SleepMetric, nil
	case stats.WorkoutsMetric, "workout":
		return stats.WorkoutsMetric, nil
	}
	m, err := metrics.Lookup(arg)
	if err != nil {
		return "", fmt.Errorf("%w\nalso: sleep, workouts", err)
	}
	return m.Name, nil
}

// buildNamedHeatmap builds the heatmap for sleep, workouts or a metric. Sleep
// and workouts feed the same engine as steps, just with a different source
// of "one number per day".
func buildNamedHeatmap(ctx context.Context, conn *sql.DB, name string, weeks int, now time.Time) (*stats.Heatmap, error) {
	if name != stats.SleepMetric && name != stats.WorkoutsMetric {
		return stats.BuildHeatmap(ctx, conn, name, weeks, now)
	}
	if weeks < 1 || weeks > stats.MaxHeatmapWeeks {
		return nil, fmt.Errorf("--weeks must be between 1 and %d, got %d", stats.MaxHeatmapWeeks, weeks)
	}
	// The window starts on the Monday `weeks` calendar weeks back; reading
	// one day more than needed is harmless, so just go back far enough.
	today := now.Format(stats.DayLayout)
	from, err := stats.AddDays(today, -(7*weeks + 6))
	if err != nil {
		return nil, err
	}
	var byDay map[string]float64
	spec := stats.HeatSpec{Title: name, Format: minutesText}
	if name == stats.SleepMetric {
		spec.Hue = stats.HueBlue
		byDay, err = stats.SleepAsleepByDay(ctx, conn, from, today)
	} else {
		spec.ZeroIsRest = true
		spec.Hue = stats.HueGreen
		// Read all history: a workout before the window makes the blank days
		// at its start rest days.
		byDay, err = stats.WorkoutMinutesByDay(ctx, conn, "0001-01-01", today)
	}
	if err != nil {
		return nil, err
	}
	return stats.HeatmapFromDays(spec, now, weeks, byDay)
}

func runHeatmap(cmd *cobra.Command, opts *options, name string, weeks int, noColor, ascii bool) error {
	if weeks < 1 || weeks > stats.MaxHeatmapWeeks {
		return fmt.Errorf("--weeks must be between 1 and %d, got %d", stats.MaxHeatmapWeeks, weeks)
	}
	// Check the name before opening the database (friendly list on a typo).
	name, err := heatmapName(name)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	ti := detectTerm(out, os.Getenv, noColor, ascii)
	if !opts.jsonOutput {
		weeks = min(weeks, heatWeeksFit(ti.width))
	}

	conn, err := openDB(opts)
	if err != nil {
		return err
	}
	defer conn.Close()
	h, err := buildNamedHeatmap(cmdContext(cmd), conn, name, weeks, nowFunc())
	if err != nil {
		return err
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(h)
	}
	if h.Empty() {
		what := strings.ReplaceAll(name, "_", " ") + " data"
		if name == stats.WorkoutsMetric {
			what = "workouts" // "workouts data" reads wrong
		}
		_, err = fmt.Fprintf(out, "No %s in the last %s yet.\n", what, plural(int64(weeks), "week"))
		return err
	}
	_, err = fmt.Fprintln(out, strings.Join(renderHeatmap(h, ti), "\n"))
	return err
}
