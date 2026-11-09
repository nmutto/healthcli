package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/nmutto/healthcli/internal/db"
	"github.com/nmutto/healthcli/internal/stats"
)

// This file is the command: it fetches the report with stats.BuildOverview
// and prints it. The text is built in overview_render.go; --json prints the
// very same stats.Overview struct, so the two cannot drift apart.

func newOverviewCmd(opts *options) *cobra.Command {
	var noColor, ascii bool
	var heatWeeks int
	var heatmapArg, categoryArg []string
	var compact, full bool
	cmd := &cobra.Command{
		Use:   "overview",
		Short: "One-screen summary of every metric, sleep, workouts and data health",
		Long: `Show the latest value of each metric next to your 28-day usual, a band that
shows where it sits, a 28-day trend line and how old the reading is; then last
night's sleep, recent workouts, a calendar heatmap and a data-health summary.

Each metric is anchored on its own newest day, so a metric that was not
recorded today still shows its last reading, with its age. A metric is "stale"
when that reading is more than 3 days old.

Only metrics that have data are listed; the rest are counted in a footer line.
A value is "beyond your usual" when it is 1+ standard deviation from your
28-day usual and the change is big enough to matter (2%, or a per-metric
minimum such as 0.2 degC for wrist temperature); anything smaller is typical.

Beyond-usual values are coloured by which direction is generally healthy for
that metric: green with the word "better", red with "worse". Metrics with no
good direction (weight, respiratory rate, ...) are cyan with the word
"changed". That direction is a general guide, not medical advice. Stale data
is yellow. Colour is never the only signal: every row has an arrow, and
beyond-usual rows also carry the word.

Metrics are grouped in category bands (vitals, activity, body, mobility) with a
one-line summary each. A marker in the left gutter flags rows that need a look:
"!" worse, "+" better, "~" changed. An index line under the headline lists the
categories and counts the worse rows ("!1"); categories your data has nothing
for are named once as "not tracked". The AGE column shows only readings 2 or
more days old. --category vitals,sleep shows only those (vitals, activity,
body, mobility, other, sleep, workouts); --compact drops the blank lines and
the gutter for small terminal panes; --full shows every section.

SLEEP shows last night's hypnogram and the core rows of the sleep table (last
night, 7 nights, vs your 28-night usual); WORKOUTS shows the totals, a table per
activity type and the recent sessions. Both leave out silently when your data has
none. Then comes one calendar heatmap of daily activity: steps if you have them,
else exercise time, else active energy. --weeks sets its length (default 12, 0
hides all heatmaps). --heatmap steps,sleep,workouts,<metric> draws exactly those,
in that order. --full shows the full sleep and workouts sections (all stat rows,
weekly bars, bests, their heatmaps) and the activity heatmap. A single
--category sleep or --category workouts also shows the full section, the same as
"healthcli sleep" and "healthcli workouts".

The output is 60-100 columns wide ($COLUMNS or the terminal width). On a pipe,
or with --ascii, only plain ASCII characters are used. NO_COLOR and --no-color
switch colours off; FORCE_COLOR switches them on even for a pipe. Samples dated
more than a day in the future are ignored.

JSON output (--json) is the same report:
  today, empty (true when the database has no data)
  categories[]  name, total, better, worse, changed, typical, stale, no_baseline, no_data
  metrics[]  metric, label, category, polarity, unit, state (ok|no_baseline|stale|no_data), latest_day,
             age_days, value, source, baseline_days_with_data, baseline_mean,
             baseline_stddev, delta_pct, z, comparison (above|below|typical),
             verdict (better|worse|changed|typical)
             spark (28 daily values ending today, null = no data)
  sleep      state, night, age_days, source, *_seconds per stage, in_bed_seconds
  workouts   days, count, duration_seconds, energy_kcal, latest
  sleep_summary    the full sleep report (see "healthcli sleep --help"); left out without sleep
  workouts_report  the full workouts report (see "healthcli workouts --help"); left out without workouts
  heatmaps   calendars for the metrics named in --heatmap, by metric name
  not_tracked[]  metrics with no data at all
  heatmap    the activity grid (see "healthcli heatmap --help"), null if none
  data       samples, first_day, last_day, source_samples, last_import, gaps[]`,
		Example: `  healthcli overview
  healthcli overview --category vitals,activity
  healthcli overview --compact
  healthcli overview --full
  healthcli overview --heatmap steps,sleep,workouts --weeks 26
  healthcli overview --category sleep
  healthcli overview --ascii --no-color
  healthcli overview --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if heatWeeks < 0 || heatWeeks > stats.MaxHeatmapWeeks {
				return fmt.Errorf("--weeks must be between 0 and %d, got %d", stats.MaxHeatmapWeeks, heatWeeks)
			}
			cats, err := parseCategories(categoryArg)
			if err != nil {
				return err
			}
			// --heatmap is a list of steps|sleep|workouts|<metric>. Each name is
			// checked now (friendly list on a typo). Metric names go to the
			// report (the first also becomes its main heatmap); the renderer
			// draws every name in the order given.
			var heatmaps, heatMetrics []string
			heatMetric := ""
			for _, arg := range heatmapArg {
				name, err := heatmapName(arg)
				if err != nil {
					return err
				}
				if !slices.Contains(heatmaps, name) {
					heatmaps = append(heatmaps, name)
				}
				if name == sleepName || name == workoutsName {
					continue
				}
				if heatMetric == "" {
					heatMetric = name
				}
				heatMetrics = append(heatMetrics, name)
			}
			view := overviewView{compact: compact, categories: cats, heatmaps: heatmaps, full: full, heatWeeks: heatWeeks, noHeat: heatWeeks == 0}
			return runOverview(cmd, opts, noColor, ascii, view, stats.OverviewOptions{HeatWeeks: heatWeeks, HeatMetric: heatMetric, HeatMetrics: heatMetrics})
		},
	}
	cmd.Flags().BoolVar(&noColor, "no-color", false, "never use colours")
	cmd.Flags().BoolVar(&ascii, "ascii", false, "use plain ASCII characters only")
	cmd.Flags().IntVar(&heatWeeks, "weeks", defaultHeatWeeks, "weeks of activity heatmap (0 hides it)")
	cmd.Flags().StringSliceVar(&heatmapArg, "heatmap", nil, "heatmaps to show, comma list of steps|sleep|workouts|<metric> (default: steps, else exercise time, else active energy)")
	cmd.Flags().StringSliceVar(&categoryArg, "category", nil, "only these categories, comma list (vitals, activity, body, mobility, other, sleep, workouts)")
	cmd.Flags().BoolVar(&compact, "compact", false, "dense table: no blank lines between categories, no gutter marker")
	cmd.Flags().BoolVar(&full, "full", false, "show every section and heatmap")
	return cmd
}

func runOverview(cmd *cobra.Command, opts *options, noColor, ascii bool, view overviewView, bo stats.OverviewOptions) error {
	conn, err := openDB(opts)
	if err != nil {
		return err
	}
	defer conn.Close()

	out := cmd.OutOrStdout()
	ti := detectTerm(out, os.Getenv, noColor, ascii)
	if !opts.jsonOutput {
		// Build only as many weeks as fit, so the legend matches the cells.
		bo.HeatWeeks = min(bo.HeatWeeks, heatWeeksFit(ti.width-len(indent)))
	}
	now := nowFunc()
	report, err := stats.BuildOverview(cmdContext(cmd), conn, now, bo)
	if err != nil {
		return err
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	name, err := dbDisplayName(opts)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(out, renderOverview(report, ti, withDB(view, name, now)))
	return err
}

// withDB adds the database name and the clock to the view the flags built.
func withDB(v overviewView, name string, now time.Time) overviewView {
	v.dbName, v.now = name, now
	return v
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
