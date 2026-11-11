package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nmutto/healthcli/internal/stats"
)

func newSleepCmd(opts *options) *cobra.Command {
	var noColor, ascii bool
	var nights, weeks int
	var by, target, date string
	cmd := &cobra.Command{
		Use:   "sleep",
		Short: "Last night's sleep: hypnogram, stats against your usual, calendar",
		Long: `Show last night's sleep as a hypnogram (awake, REM, core and deep sleep over
the night), then a table of last night, your last 7 nights and how that
compares with your usual, the --nights nights before it (default 28): time
asleep, in bed, efficiency, awake time and awakenings, falling-asleep time,
deep/REM/core sleep, bedtime, wake time, midpoint, bedtime regularity and
sleep debt against --sleep-target (default 8h). Below the table is a calendar
heatmap of the nights (--weeks, default 12; 0 hides it). With fewer than 14
nights a bar per night is shown instead.

A night is the sleep that ENDS on a local date: 18:00 to 18:00. A nap ending
before 18:00 counts toward the night that just ended.

Only what your data holds is shown. A device that records only "asleep" gets
a single bar and no stage rows; in-bed time alone shows no sleep numbers.
Rows with nothing to show are left out, and one dim line names them.

Colour: green better, red worse, always with a word. Rows with no good
direction (bedtime, in bed) have no colour and say later/earlier,
longer/shorter or higher/lower. "vs usual" compares your last 7 nights with the usual; a
change under 5% (10% for awake time and falling-asleep time, 2 points of
efficiency, 15 minutes of clock time) is typical.

--by picks the calendar value: asleep (time asleep), bedtime (later is
darker) or efficiency.

--date YYYY-MM-DD treats that local day as today (not a future day): the night
shown is the one ending on or before it.

JSON output (--json): today, empty, age_days, kind (stages|unspecified|in_bed),
night (seconds, local clock times, stage rows), seven_nights, fourteen_nights,
usual (averages; missing numbers are left out, durations in seconds, clock
times as minutes since 18:00), regularity, debt (positive seconds = short of
the target), heatmap.`,
		Example: `  healthcli sleep
  healthcli sleep --sleep-target 7h30m --nights 14
  healthcli sleep --by bedtime --weeks 26
  healthcli sleep --date 2026-11-01
  healthcli sleep --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := parseSleepTarget(target)
			if err != nil {
				return err
			}
			if weeks < 0 || weeks > stats.MaxHeatmapWeeks {
				return usageErrorf("--weeks must be between 0 and %d, got %d", stats.MaxHeatmapWeeks, weeks)
			}
			return runSleep(cmd, opts, date, noColor, ascii, stats.SleepOptions{Nights: nights, Target: d, By: by, Weeks: weeks})
		},
	}
	cmd.Flags().IntVar(&nights, "nights", stats.DefaultSleepNights, "nights before the last one in the usual window (7-90)")
	cmd.Flags().StringVar(&target, "sleep-target", "8h", "nightly sleep goal, e.g. 7h30m")
	cmd.Flags().StringVar(&by, "by", stats.SleepByAsleep, "calendar value: asleep, bedtime, efficiency")
	cmd.Flags().IntVar(&weeks, "weeks", defaultHeatWeeks, "weeks of calendar (0 hides it)")
	cmd.Flags().StringVar(&date, "date", "", dateFlagHelp)
	cmd.Flags().BoolVar(&noColor, "no-color", false, "never use colours")
	cmd.Flags().BoolVar(&ascii, "ascii", false, "use plain ASCII characters only")
	return cmd
}

// parseSleepTarget reads "8h", "7h30m" or "450m"; between 4 and 12 hours.
func parseSleepTarget(s string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil || d < 4*time.Hour || d > 12*time.Hour {
		return 0, usageErrorf("--sleep-target must be a duration between 4h and 12h such as 8h or 7h30m, got %q", s)
	}
	return d, nil
}

func runSleep(cmd *cobra.Command, opts *options, date string, noColor, ascii bool, so stats.SleepOptions) error {
	now, err := clockFor(date)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	ti := detectTerm(out, os.Getenv, noColor, ascii)
	if !opts.jsonOutput {
		so.Weeks = min(so.Weeks, heatWeeksFit(ti.width-len(sleepIndent)))
	}
	conn, err := openDB(opts)
	if err != nil {
		return err
	}
	defer conn.Close()
	report, err := stats.BuildSleepReport(cmdContext(cmd), conn, now, so)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	if report.Empty {
		_, err = fmt.Fprintf(out, "No sleep data in the last %s yet.\n", plural(int64(max(so.Nights, 28)), "day"))
		return err
	}
	lines := renderSleepSection(report, ti, ti.width, sleepOpts{Full: true, Target: so.Target})
	_, err = fmt.Fprintln(out, strings.Join(lines, "\n"))
	return err
}
