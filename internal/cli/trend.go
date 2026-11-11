package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nmutto/healthcli/internal/metrics"
	"github.com/nmutto/healthcli/internal/stats"
)

// This file FETCHES the data for "healthcli trend" and shapes it into a
// trendReport. Text output lives in trend_render.go.

// sleepMetric is the pseudo-metric name for "asleep hours per night". Sleep
// is not a row in the metrics registry (it has its own table), so the
// command handles it as a special case.
const sleepMetric = stats.SleepMetric

// Field names below are the documented --json contract of "healthcli trend".
type trendReport struct {
	Status    string      `json:"status"` // "ok" or "no_data" (nothing in the window)
	Metric    string      `json:"metric"` // canonical name, or "sleep"
	Unit      string      `json:"unit"`   // for sleep: "h" (hours asleep)
	From      string      `json:"from"`
	To        string      `json:"to"`
	Days      int         `json:"days"`
	LatestDay string      `json:"latest_day,omitempty"` // when no_data: newest day that has data
	Daily     []trendDay  `json:"daily"`                // one entry per day, gaps included
	Summary   *trendStats `json:"summary"`              // null when no_data
	Change    trendChange `json:"change_7d"`
	Baseline  trendBase   `json:"baseline"`
	verbose   bool        `json:"-"` // text only: print the per-day table
}

type trendDay struct {
	Day    string   `json:"day"`
	Value  *float64 `json:"value"` // null on a day without data
	Source string   `json:"source,omitempty"`
}

type trendStats struct {
	DaysWithData int     `json:"days_with_data"`
	Min          float64 `json:"min"`
	MinDay       string  `json:"min_day"`
	Max          float64 `json:"max"`
	MaxDay       string  `json:"max_day"`
	Mean         float64 `json:"mean"`
}

// trendChange compares the mean of the last 7 days with the 7 days before.
type trendChange struct {
	Available bool     `json:"available"` // false if either week has no data
	Last7Mean *float64 `json:"last7_mean"`
	Last7Days int      `json:"last7_days_with_data"`
	PrevMean  *float64 `json:"previous7_mean"`
	PrevDays  int      `json:"previous7_days_with_data"`
	Delta     *float64 `json:"delta"`
	DeltaPct  *float64 `json:"delta_pct"`
}

// trendBase is the baseline of the 28 days before the last day shown.
type trendBase struct {
	Available    bool     `json:"available"`
	DaysWithData int      `json:"days_with_data"`
	DaysNeeded   int      `json:"days_needed"`
	Mean         *float64 `json:"mean"`
}

func newTrendCmd(opts *options) *cobra.Command {
	var days int
	var date string
	var verbose, noColor, ascii bool

	cmd := &cobra.Command{
		Use:   "trend <metric>",
		Short: "Show how a metric changes over the last days",
		Long: `Show one metric over the last N days as a sparkline (a dot marks a day without
data), with min, max and mean, the last 7 days against the 7 before, and the
baseline (the 28 days before the last day, as in overview and today). On a
pipe, or with --ascii, the sparkline uses plain ASCII characters instead of
block bars (_ marks a day without data). With colour on, the bars use the same
palette as the heatmaps: the hue of the metric (heart red, respiratory cyan,
sleep blue, body purple and violet, orange for activity), dim for low days and
bright for high ones.

Metrics: ` + strings.Join(metrics.Names(), ", ") + `, sleep (hours asleep per night).
Aliases: hr, hrv, rhr, rr, spo2, steps, temp, vo2, weight, bmi.

Pages fit 50 to 100 columns ($COLUMNS or the terminal width); a narrower
terminal is treated as 50, so lines may wrap there.

The window ends today unless --date says otherwise. With --verbose, a table
lists every day with its value and source.

JSON output (--json) has these fields:
  status      "ok", or "no_data" if the window is empty (then latest_day says where data is)
  metric, unit, from, to, days
  daily[]     day, value (null on a gap), source        one entry per day
  summary     days_with_data, min, min_day, max, max_day, mean   (null if no data)
  change_7d   available, last7_mean, last7_days_with_data, previous7_mean,
              previous7_days_with_data, delta, delta_pct
  baseline    available, days_with_data, days_needed, mean   (the 28 days before "to")
Sleep values are hours asleep.`,
		Example: `  healthcli trend steps
  healthcli trend weight --days 90
  healthcli trend sleep --verbose
  healthcli trend hr --days 14 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTrend(cmd, opts, args[0], days, date, verbose, noColor, ascii)
		},
	}
	cmd.Flags().IntVar(&days, "days", 30, "number of days to show")
	cmd.Flags().StringVar(&date, "date", "", "last day of the window, YYYY-MM-DD (default: today)")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "also print a per-day table")
	cmd.Flags().BoolVar(&noColor, "no-color", false, "never use colours")
	cmd.Flags().BoolVar(&ascii, "ascii", false, "use plain ASCII characters only")
	return cmd
}

func runTrend(cmd *cobra.Command, opts *options, name string, days int, date string, verbose, noColor, ascii bool) error {
	if days < 1 || days > 3650 {
		return fmt.Errorf("--days must be between 1 and 3650, got %d", days)
	}
	if date == "" {
		date = nowFunc().Format(stats.DayLayout)
	} else if err := checkDateFlag(date); err != nil {
		return err
	}

	// Check the name before opening the database, so a typo gives the
	// metric list even when no database exists yet.
	metric, unit, err := resolveTrendMetric(name)
	if err != nil {
		return err
	}

	conn, err := openDB(opts)
	if err != nil {
		return err
	}
	defer conn.Close()

	report, err := buildTrend(cmdContext(cmd), conn, metric, unit, date, days)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if opts.jsonOutput {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	report.verbose = verbose
	_, err = fmt.Fprint(out, renderTrend(report, detectTerm(out, os.Getenv, noColor, ascii)))
	return err
}

// resolveTrendMetric maps what the user typed to a canonical metric name and unit.
func resolveTrendMetric(name string) (metric, unit string, err error) {
	if strings.EqualFold(strings.TrimSpace(name), sleepMetric) {
		return sleepMetric, "h", nil
	}
	m, err := metrics.Lookup(name)
	if err != nil {
		// Lookup's message already lists metrics and aliases; add sleep.
		return "", "", fmt.Errorf("%w\nalso: %s (hours asleep per night)", err, sleepMetric)
	}
	return m.Name, m.Unit, nil
}

// buildTrend reads the window of `days` days ending on `end`.
func buildTrend(ctx context.Context, conn *sql.DB, metric, unit, end string, days int) (*trendReport, error) {
	from, err := stats.AddDays(end, -(days - 1))
	if err != nil {
		return nil, err
	}
	// Read at least 29 days back: the 7-vs-7 comparison needs 14 and the
	// sleep baseline the 28 days before the last day, even when the window
	// shown is shorter.
	readDays := max(days, stats.DefaultBaselineDays+1)
	readFrom, err := stats.AddDays(end, -(readDays - 1))
	if err != nil {
		return nil, err
	}
	byDay, err := readValues(ctx, conn, metric, readFrom, end)
	if err != nil {
		return nil, err
	}

	r := &trendReport{Status: statusOK, Metric: metric, Unit: unit, From: from, To: end, Days: days, Daily: []trendDay{}}
	var shown []float64 // values with data, for the summary
	var shownDays []string
	for i := 0; i < days; i++ {
		day, err := stats.AddDays(from, i)
		if err != nil {
			return nil, err
		}
		td := trendDay{Day: day}
		if dv, ok := byDay[day]; ok {
			v := dv.Value
			td.Value, td.Source = &v, dv.Source
			shown = append(shown, v)
			shownDays = append(shownDays, day)
		}
		r.Daily = append(r.Daily, td)
	}

	if len(shown) == 0 {
		r.Status = statusNoData
		if latest, ok, err := stats.LatestDayFor(ctx, conn, metric); err != nil {
			return nil, err
		} else if ok {
			r.LatestDay = latest
		}
	} else {
		sum := stats.Summarize(shown, shownDays)
		r.Summary = &trendStats{DaysWithData: sum.DaysWithData, Min: sum.Min, MinDay: sum.MinDay,
			Max: sum.Max, MaxDay: sum.MaxDay, Mean: sum.Mean}
	}

	wc, err := stats.WeekChangeFor(byDay, end)
	if err != nil {
		return nil, err
	}
	r.Change = trendChange{Available: wc.Available, Last7Mean: wc.Last7Mean, Last7Days: wc.Last7Days,
		PrevMean: wc.PrevMean, PrevDays: wc.PrevDays, Delta: wc.Delta, DeltaPct: wc.DeltaPct}
	r.Baseline, err = trendBaseline(ctx, conn, metric, end, byDay)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// readValues returns the daily values of metric by day. For sleep the value
// is hours asleep that night.
func readValues(ctx context.Context, conn *sql.DB, metric, from, to string) (map[string]stats.DayValue, error) {
	out := map[string]stats.DayValue{}
	if metric == sleepMetric {
		nights, err := stats.SleepNights(ctx, conn, from, to)
		if err != nil {
			return nil, err
		}
		for _, n := range nights {
			if n.Source == "" {
				continue // only in_bed rows (an iPhone alone): not hours asleep, not 0 h
			}
			out[n.Night] = stats.DayValue{Day: n.Night, Value: n.Asleep.Hours(), Source: n.Source}
		}
		return out, nil
	}
	series, err := stats.DailySeries(ctx, conn, metric, from, to)
	if err != nil {
		return nil, err
	}
	for _, dv := range series {
		out[dv.Day] = dv
	}
	return out, nil
}

// trendBaseline is the baseline of the 28 days BEFORE end, the same one
// overview and today compare their latest day against (end itself is left out).
func trendBaseline(ctx context.Context, conn *sql.DB, metric, end string, byDay map[string]stats.DayValue) (trendBase, error) {
	tb := trendBase{DaysNeeded: stats.MinBaselineDays}
	if metric == sleepMetric {
		mean, n, ok, err := stats.SleepBaseline(byDay, end)
		if err != nil {
			return tb, err
		}
		tb.DaysWithData = n
		if ok {
			tb.Available, tb.Mean = true, &mean
		}
		return tb, nil
	}
	b, err := stats.BaselineFor(ctx, conn, metric, end, stats.DefaultBaselineDays)
	tb.DaysWithData = b.N
	switch {
	case errors.Is(err, stats.ErrNotEnoughData):
	case err != nil:
		return tb, err
	default:
		tb.Available, tb.Mean = true, &b.Mean
	}
	return tb, nil
}
