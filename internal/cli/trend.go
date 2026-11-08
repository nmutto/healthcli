package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nmutto/health/internal/metrics"
	"github.com/nmutto/health/internal/stats"
)

// This file FETCHES the data for "health trend" and shapes it into a
// trendReport. Text output lives in trend_render.go.

// sleepMetric is the pseudo-metric name for "asleep hours per night". Sleep
// is not a row in the metrics registry (it has its own table), so the
// command handles it as a special case.
const sleepMetric = "sleep"

// Field names below are the documented --json contract of "health trend".
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

// trendBase is the baseline of the 28 days ending on the last day shown.
type trendBase struct {
	Available    bool     `json:"available"`
	DaysWithData int      `json:"days_with_data"`
	DaysNeeded   int      `json:"days_needed"`
	Mean         *float64 `json:"mean"`
}

func newTrendCmd(opts *options) *cobra.Command {
	var days int
	var date string
	var verbose bool

	cmd := &cobra.Command{
		Use:   "trend <metric>",
		Short: "Show how a metric changes over the last days",
		Long: `Show one metric over the last N days as a sparkline (· marks a day without
data), with min, max and mean, the last 7 days against the 7 before, and the
current 28-day baseline.

Metrics: ` + strings.Join(metrics.Names(), ", ") + `, sleep (hours asleep per night).
Aliases: steps, weight, hr.

The window ends today unless --date says otherwise. With --verbose, a table
lists every day with its value and source.

JSON output (--json) has these fields:
  status      "ok", or "no_data" if the window is empty (then latest_day says where data is)
  metric, unit, from, to, days
  daily[]     day, value (null on a gap), source        one entry per day
  summary     days_with_data, min, min_day, max, max_day, mean   (null if no data)
  change_7d   available, last7_mean, last7_days_with_data, previous7_mean,
              previous7_days_with_data, delta, delta_pct
  baseline    available, days_with_data, days_needed, mean   (28 days up to "to")
Sleep values are hours asleep.`,
		Example: `  health trend steps
  health trend weight --days 90
  health trend sleep --verbose
  health trend hr --days 14 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTrend(cmd, opts, args[0], days, date, verbose)
		},
	}
	cmd.Flags().IntVar(&days, "days", 30, "number of days to show")
	cmd.Flags().StringVar(&date, "date", "", "last day of the window, YYYY-MM-DD (default: today)")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "also print a per-day table")
	return cmd
}

func runTrend(cmd *cobra.Command, opts *options, name string, days int, date string, verbose bool) error {
	if days < 1 || days > 3650 {
		return fmt.Errorf("--days must be between 1 and 3650, got %d", days)
	}
	if date == "" {
		date = nowFunc().Format(stats.DayLayout)
	} else if _, err := time.Parse(stats.DayLayout, date); err != nil {
		return fmt.Errorf("bad --date %q: want YYYY-MM-DD, e.g. 2026-11-08", date)
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
	_, err = fmt.Fprint(out, renderTrend(report, style{color: colorFor(out)}))
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
	// Read at least 28 days back: the 7-vs-7 comparison needs 14 and the
	// baseline 28, even when the window shown is shorter.
	readDays := max(days, stats.DefaultBaselineDays)
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
		day, _ := stats.AddDays(from, i)
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
		if latest, ok, err := latestDayFor(ctx, conn, metric); err != nil {
			return nil, err
		} else if ok {
			r.LatestDay = latest
		}
	} else {
		r.Summary = summarize(shown, shownDays)
	}

	r.Change = weekChange(byDay, end)
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

func summarize(values []float64, days []string) *trendStats {
	s := &trendStats{DaysWithData: len(values), Min: values[0], MinDay: days[0], Max: values[0], MaxDay: days[0]}
	var sum float64
	for i, v := range values {
		sum += v
		if v < s.Min {
			s.Min, s.MinDay = v, days[i]
		}
		if v > s.Max {
			s.Max, s.MaxDay = v, days[i]
		}
	}
	s.Mean = sum / float64(len(values))
	return s
}

// meanOver averages the days from..to (inclusive) that have data.
func meanOver(byDay map[string]stats.DayValue, from, to string) (mean float64, n int) {
	var sum float64
	for d := from; d <= to; {
		if dv, ok := byDay[d]; ok {
			sum += dv.Value
			n++
		}
		next, _ := stats.AddDays(d, 1)
		d = next
	}
	if n == 0 {
		return 0, 0
	}
	return sum / float64(n), n
}

// weekChange compares the 7 days ending on end with the 7 before them.
func weekChange(byDay map[string]stats.DayValue, end string) trendChange {
	lastFrom, _ := stats.AddDays(end, -6)
	prevTo, _ := stats.AddDays(end, -7)
	prevFrom, _ := stats.AddDays(end, -13)
	last, ln := meanOver(byDay, lastFrom, end)
	prev, pn := meanOver(byDay, prevFrom, prevTo)
	c := trendChange{Last7Days: ln, PrevDays: pn}
	if ln > 0 {
		c.Last7Mean = &last
	}
	if pn > 0 {
		c.PrevMean = &prev
	}
	if ln > 0 && pn > 0 {
		c.Available = true
		d := last - prev
		if math.Abs(d) <= 1e-9*math.Max(1, math.Abs(prev)) {
			d = 0 // float noise, not a real change
		}
		c.Delta = &d
		pct := 0.0
		if prev != 0 {
			pct = d / math.Abs(prev) * 100
		}
		c.DeltaPct = &pct
	}
	return c
}

// trendBaseline is the baseline of the 28 days ending on end (end included:
// this is "what is normal now", unlike `today`, which leaves the day out).
func trendBaseline(ctx context.Context, conn *sql.DB, metric, end string, byDay map[string]stats.DayValue) (trendBase, error) {
	tb := trendBase{DaysNeeded: stats.MinBaselineDays}
	if metric == sleepMetric {
		// Sleep is not in the registry, so stats.BaselineFor cannot be used;
		// apply the same rule (mean of the last 28 days, at least 7 with data).
		from, _ := stats.AddDays(end, -(stats.DefaultBaselineDays - 1))
		mean, n := meanOver(byDay, from, end)
		tb.DaysWithData = n
		if n >= stats.MinBaselineDays {
			tb.Available, tb.Mean = true, &mean
		}
		return tb, nil
	}
	// BaselineFor looks at the days BEFORE its day argument, so pass the
	// day after end to include end itself.
	next, err := stats.AddDays(end, 1)
	if err != nil {
		return tb, err
	}
	b, err := stats.BaselineFor(ctx, conn, metric, next, stats.DefaultBaselineDays)
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

// latestDayFor finds the newest day that has data for metric, to point the
// user at a window that is not empty.
func latestDayFor(ctx context.Context, conn *sql.DB, metric string) (string, bool, error) {
	if metric == sleepMetric {
		var d sql.NullString
		err := conn.QueryRowContext(ctx,
			`SELECT max(date(end_ts + tz_offset + 21600, 'unixepoch')) FROM sleep_stages`).Scan(&d)
		return d.String, d.Valid, err
	}
	s, err := stats.LatestSample(ctx, conn, metric)
	if errors.Is(err, stats.ErrNoData) {
		return "", false, nil
	}
	return s.Day, err == nil, err
}
