package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/nmutto/healthcli/internal/metrics"
	"github.com/nmutto/healthcli/internal/stats"
)

// This file FETCHES the data for "healthcli today" and shapes it into a
// todayReport. Turning the report into text lives in today_render.go, so the
// text can be tested without a database.

// todayMetrics is the order and wording of the rows in the summary.
var todayMetrics = []struct{ name, label string }{
	{"step_count", "Steps"},
	{"active_energy", "Active energy"},
	{"heart_rate", "Avg heart rate"},
	{"resting_heart_rate", "Resting heart rate"},
	{"hrv_sdnn", "HRV"},
	{"body_mass", "Weight"},
}

// Values of todayReport.Status.
const (
	statusOK       = "ok"       // the requested date has data
	statusFallback = "fallback" // it had none; Date is the latest day that does
	statusNoData   = "no_data"  // the database has nothing at all
)

// Field names below are the documented --json contract of "healthcli today".
type todayReport struct {
	Status        string         `json:"status"`
	RequestedDate string         `json:"requested_date"`
	Date          string         `json:"date"` // the day shown
	Metrics       []todayMetric  `json:"metrics"`
	Sleep         *todaySleep    `json:"sleep"` // null when no sleep ended on Date
	Workouts      []todayWorkout `json:"workouts"`
}

type todayMetric struct {
	Metric     string          `json:"metric"`
	Label      string          `json:"label"`
	Unit       string          `json:"unit"`
	Value      *float64        `json:"value"` // null when nothing was recorded that day
	Source     string          `json:"source,omitempty"`
	Baseline   *baselineInfo   `json:"baseline"`   // null when Value is null
	Comparison *comparisonInfo `json:"comparison"` // null unless the baseline is available
}

type baselineInfo struct {
	Available    bool     `json:"available"`
	DaysWithData int      `json:"days_with_data"`
	DaysNeeded   int      `json:"days_needed"`
	From         string   `json:"from"`
	To           string   `json:"to"`
	Mean         *float64 `json:"mean,omitempty"`
	Stddev       *float64 `json:"stddev,omitempty"`
}

type comparisonInfo struct {
	Delta    float64 `json:"delta"`
	DeltaPct float64 `json:"delta_pct"`
	Z        float64 `json:"z"`
	Label    string  `json:"label"` // above, below or typical
}

type todaySleep struct {
	Night              string `json:"night"` // the date the sleep ended on
	Source             string `json:"source"`
	AsleepSeconds      int64  `json:"asleep_seconds"`
	CoreSeconds        int64  `json:"core_seconds"`
	DeepSeconds        int64  `json:"deep_seconds"`
	REMSeconds         int64  `json:"rem_seconds"`
	UnspecifiedSeconds int64  `json:"unspecified_seconds"`
	AwakeSeconds       int64  `json:"awake_seconds"`
	InBedSeconds       int64  `json:"in_bed_seconds"`
}

type todayWorkout struct {
	Activity        string   `json:"activity"`
	Start           string   `json:"start"` // local wall clock, RFC 3339
	DurationSeconds int64    `json:"duration_seconds"`
	EnergyKcal      *float64 `json:"energy_kcal"`
	DistanceM       *float64 `json:"distance_m"`
	Source          string   `json:"source"`
}

// nowFunc is the clock. Tests replace it so "today" is a fixed date.
var nowFunc = time.Now

func newTodayCmd(opts *options) *cobra.Command {
	var date string
	cmd := &cobra.Command{
		Use:   "today",
		Short: "Show a day's summary compared with your baseline",
		Long: `Show one day (default: today, local time): steps, active energy, average and
resting heart rate, HRV and weight, each next to its 28-day baseline; last
night's sleep; and the day's workouts.

If the date has no data at all, the latest day that does is shown instead.
Colors are used only on a terminal, and never when NO_COLOR is set.

JSON output (--json) has these fields:
  status          "ok", "fallback" (date had no data; showing the latest day) or "no_data"
  requested_date  the day asked for      date  the day shown
  metrics[]       metric, label, unit, value (null if none), source,
                  baseline {available, days_with_data, days_needed, from, to, mean, stddev},
                  comparison {delta, delta_pct, z, label: above|below|typical} (null without baseline)
  sleep           null, or {night, source, asleep_seconds, core_seconds, deep_seconds,
                  rem_seconds, unspecified_seconds, awake_seconds, in_bed_seconds}
  workouts[]      activity, start, duration_seconds, energy_kcal, distance_m (null if not
                  recorded), source`,
		Example: `  healthcli today
  healthcli today --date 2026-11-08
  healthcli today --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToday(cmd, opts, date)
		},
	}
	cmd.Flags().StringVar(&date, "date", "", "local day to show, YYYY-MM-DD (default: today)")
	return cmd
}

func runToday(cmd *cobra.Command, opts *options, date string) error {
	if date == "" {
		date = nowFunc().Format(stats.DayLayout)
	} else if err := checkDateFlag(date); err != nil {
		return err
	}

	conn, err := openDB(opts)
	if err != nil {
		return err
	}
	defer conn.Close()

	report, err := buildToday(cmdContext(cmd), conn, date)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if opts.jsonOutput {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	_, err = fmt.Fprint(out, renderToday(report, style{color: colorFor(out)}))
	return err
}

// checkDateFlag validates --date. Years are limited to 1900..2999: nothing
// real lies outside, and day arithmetic at the ends of the calendar
// (year 0, year 10000) is not worth supporting.
func checkDateFlag(date string) error {
	t, err := time.Parse(stats.DayLayout, date)
	if err != nil {
		return fmt.Errorf("bad --date %q: want YYYY-MM-DD, e.g. 2026-11-08", date)
	}
	if y := t.Year(); y < 1900 || y > 2999 {
		return fmt.Errorf("bad --date %q: year must be between 1900 and 2999", date)
	}
	return nil
}

// cmdContext returns the command's context, or a background one if cobra
// was started without any (older cobra versions leave it nil).
func cmdContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

// buildToday gathers everything for date. When date has nothing at all it
// falls back to the latest day with data, so the command is never just empty.
func buildToday(ctx context.Context, conn *sql.DB, date string) (*todayReport, error) {
	report, err := collectDay(ctx, conn, date)
	if err != nil {
		return nil, err
	}
	report.RequestedDate = date
	if !report.isEmpty() {
		report.Status = statusOK
		return report, nil
	}

	latest, found, err := latestDataDay(ctx, conn)
	if err != nil {
		return nil, err
	}
	if !found {
		report.Status = statusNoData
		return report, nil
	}
	report, err = collectDay(ctx, conn, latest)
	if err != nil {
		return nil, err
	}
	report.RequestedDate = date
	report.Status = statusFallback
	return report, nil
}

func (r *todayReport) isEmpty() bool {
	if r.Sleep != nil || len(r.Workouts) > 0 {
		return false
	}
	for _, m := range r.Metrics {
		if m.Value != nil {
			return false
		}
	}
	return true
}

// collectDay reads all the data of one local day.
func collectDay(ctx context.Context, conn *sql.DB, day string) (*todayReport, error) {
	r := &todayReport{Date: day, Metrics: []todayMetric{}, Workouts: []todayWorkout{}}

	for _, tm := range todayMetrics {
		m, err := metrics.Lookup(tm.name)
		if err != nil {
			return nil, err
		}
		row := todayMetric{Metric: m.Name, Label: tm.label, Unit: m.Unit}
		series, err := stats.DailySeries(ctx, conn, m.Name, day, day)
		if err != nil {
			return nil, err
		}
		if len(series) > 0 {
			v := series[0].Value
			row.Value, row.Source = &v, series[0].Source
			if err := addBaseline(ctx, conn, &row, day); err != nil {
				return nil, err
			}
		}
		r.Metrics = append(r.Metrics, row)
	}

	nights, err := stats.SleepNights(ctx, conn, day, day)
	if err != nil {
		return nil, err
	}
	for _, n := range nights {
		if n.Night == day {
			r.Sleep = &todaySleep{
				Night: n.Night, Source: n.Source,
				AsleepSeconds: seconds(n.Asleep), CoreSeconds: seconds(n.Core),
				DeepSeconds: seconds(n.Deep), REMSeconds: seconds(n.REM),
				UnspecifiedSeconds: seconds(n.Unspecified), AwakeSeconds: seconds(n.Awake),
				InBedSeconds: seconds(n.InBed),
			}
		}
	}

	ws, err := stats.Workouts(ctx, conn, day, day)
	if err != nil {
		return nil, err
	}
	for _, w := range ws {
		r.Workouts = append(r.Workouts, todayWorkout{
			Activity: w.Activity, Start: w.Start.Format(time.RFC3339),
			DurationSeconds: seconds(w.Duration), EnergyKcal: w.EnergyKcal,
			DistanceM: w.DistanceM, Source: w.Source,
		})
	}
	return r, nil
}

func seconds(d time.Duration) int64 { return int64(d / time.Second) }

// addBaseline attaches the 28-day baseline and the comparison to row.
// "Not enough data" is an expected state, not an error: it just means the
// text says "baseline n/a".
func addBaseline(ctx context.Context, conn *sql.DB, row *todayMetric, day string) error {
	b, err := stats.BaselineFor(ctx, conn, row.Metric, day, stats.DefaultBaselineDays)
	info := &baselineInfo{
		DaysWithData: b.N, DaysNeeded: stats.MinBaselineDays, From: b.From, To: b.To,
	}
	row.Baseline = info
	switch {
	case errors.Is(err, stats.ErrNotEnoughData):
		return nil
	case err != nil:
		return err
	}
	info.Available = true
	info.Mean, info.Stddev = &b.Mean, &b.Stddev
	c := stats.Compare(*row.Value, b)
	row.Comparison = &comparisonInfo{Delta: c.Delta, DeltaPct: c.DeltaPct, Z: c.Z, Label: c.Label}
	return nil
}

// latestDataDay finds the newest local day with any data: the newest
// sample of any metric, workout or night of sleep. found is false for an
// empty database.
func latestDataDay(ctx context.Context, conn *sql.DB) (day string, found bool, err error) {
	consider := func(d string) {
		// "YYYY-MM-DD" strings sort in date order, so > compares dates.
		if d > day {
			day, found = d, true
		}
	}
	for _, name := range metrics.Names() {
		s, err := stats.LatestSample(ctx, conn, name)
		if errors.Is(err, stats.ErrNoData) {
			continue
		}
		if err != nil {
			return "", false, err
		}
		consider(s.Day)
	}

	// Workouts and sleep have no "latest" helper in stats, so ask directly.
	// Local day = UTC time + tz_offset; a night belongs to the date it ends on,
	// with the 6-hour shift stats uses (see stats.SleepNight).
	for _, q := range []string{
		`SELECT max(date(start_ts + tz_offset, 'unixepoch')) FROM workouts`,
		`SELECT max(date(end_ts + tz_offset + 21600, 'unixepoch')) FROM sleep_stages`,
	} {
		var d sql.NullString // NULL when the table is empty
		if err := conn.QueryRowContext(ctx, q).Scan(&d); err != nil {
			return "", false, fmt.Errorf("find latest day: %w", err)
		}
		if d.Valid {
			consider(d.String)
		}
	}
	return day, found, nil
}
