package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/nmutto/healthcli/internal/metrics"
	"github.com/nmutto/healthcli/internal/stats"
)

// This file FETCHES the data for "healthcli today" and shapes it into a
// todayReport. Turning the report into text lives in today_render.go, so the
// text can be tested without a database.

// Values of todayReport.Status.
const (
	statusOK       = "ok"       // the requested date has data
	statusFallback = "fallback" // it had none; Date is the latest day that does
	statusNoData   = "no_data"  // the database has nothing at all
)

// Field names below are the documented --json contract of "healthcli today".
// The first fields are the original contract; the ones marked "added" came
// with the one-day overview and are only ever additions (never null).
type todayReport struct {
	Status        string         `json:"status"`
	RequestedDate string         `json:"requested_date"`
	Date          string         `json:"date"` // the day shown
	Metrics       []todayMetric  `json:"metrics"`
	Sleep         *todaySleep    `json:"sleep"` // null when no sleep ended on Date
	Workouts      []todayWorkout `json:"workouts"`

	Categories            []stats.CategorySummary `json:"categories"`              // added: counts per category band
	TrackedWithoutReading []string                `json:"tracked_without_reading"` // added: tracked metrics with no reading on Date
	SleepSummary          *stats.SleepReport      `json:"sleep_summary,omitempty"` // added: the night's full report; left out without sleep

	day *stats.DayReport // what the text is drawn from; not part of the JSON
}

type todayMetric struct {
	Metric     string          `json:"metric"`
	Label      string          `json:"label"`
	Unit       string          `json:"unit"`
	Value      *float64        `json:"value"` // always set: metrics without a reading that day are left out
	Source     string          `json:"source,omitempty"`
	Baseline   *baselineInfo   `json:"baseline"`
	Comparison *comparisonInfo `json:"comparison"` // null unless the baseline is available

	Category string   `json:"category"`        // added: vitals, activity, body, mobility or other
	Polarity string   `json:"polarity"`        // added: higher_better, lower_better or neutral
	State    string   `json:"state"`           // added: ok or no_baseline
	Verdict  string   `json:"verdict"`         // added: better, worse, changed or typical; "" without a baseline
	Usual    *float64 `json:"usual,omitempty"` // added: the 28-day mean, same as baseline.mean
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
	Delta    float64  `json:"delta"`
	DeltaPct *float64 `json:"delta_pct"` // null when the usual is 0
	Z        float64  `json:"z"`
	Label    string   `json:"label"` // above, below or typical
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
	var noColor, ascii, compact bool
	var categoryArg []string
	cmd := &cobra.Command{
		Use:   "today",
		Short: "Show a day's summary compared with your baseline",
		Long: `Show one day (default: today, local time) as a one-day slice of "healthcli
overview": every metric that has a reading on that day next to your 28-day
usual (the 28 days before it), grouped in category bands; the night of sleep
that ended on that day; and the day's workouts.

There is no trend line and no calendar. Each row reads LATEST, USUAL and VS
USUAL, followed by a small range gauge (where the value sits in your usual
range) when the terminal is wide enough. Beyond-usual values are coloured by which direction is generally
healthy: green "better", red "worse"; metrics with no good direction say
"higher" or "lower" in plain text. A marker in the left gutter flags rows:
"!" worse, "+" better, "~" higher or lower with no good direction. A metric needs 7 days of history in the 28 before the day for a
comparison ("need 7d (have 3)" until then). That direction is a general guide,
not medical advice.

Metrics with no reading on the day are not listed; one dim line counts the
tracked ones. If the date has no data at all, the latest day that does is shown
instead, and a dim line says so. A date in the future is an error.

Sleep belongs to the date it ends on, counting sleep that ends after 18:00 as
the next day's night (as in "healthcli sleep"); its "usual" is the nights
before the one shown. --category vitals,sleep shows only those (vitals,
activity, body, mobility, other, sleep, workouts) in the text and in --json
alike, including the "no reading" count; --compact drops the blank lines and keeps a
one-character flag in the gutter. The output is 50-100 columns wide ($COLUMNS or
the terminal width). On a pipe, or with --ascii, only plain ASCII characters are
used. NO_COLOR and --no-color switch colours off; FORCE_COLOR switches them on
even for a pipe.

JSON output (--json) has these fields (the ones marked * were added later):
  status          "ok", "fallback" (date had no data; showing the latest day) or "no_data"
  requested_date  the day asked for      date  the day shown
  metrics[]       only metrics with a reading that day (--category narrows this list,
                  categories[], tracked_without_reading[], sleep and workouts[] alike): metric, label, unit, value, source,
                  baseline {available, days_with_data, days_needed, from, to, mean, stddev},
                  comparison {delta, delta_pct, z, label: above|below|typical} (null without baseline),
                  *category, *polarity, *state (ok|no_baseline),
                  *verdict (better|worse|changed|typical, "" without baseline; unlike
                  comparison.label it ignores changes too small to matter), *usual (the
                  baseline mean)
  sleep           null, or {night, source, asleep_seconds, core_seconds, deep_seconds,
                  rem_seconds, unspecified_seconds, awake_seconds, in_bed_seconds}
  workouts[]      activity, start, duration_seconds, energy_kcal, distance_m (null if not
                  recorded), source
  *categories[]   name, total, better, worse, changed, typical, stale, no_baseline, no_data
  *tracked_without_reading[]  metrics recorded on other days but not on this one
  *sleep_summary  the night's full report ("healthcli sleep --help"); left out without sleep`,
		Example: `  healthcli today
  healthcli today --date 2026-11-08
  healthcli today --category vitals,sleep --compact
  healthcli today --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cats, err := parseCategories(categoryArg)
			if err != nil {
				return err
			}
			view := overviewView{compact: compact, categories: cats, oneDay: true}
			return runToday(cmd, opts, date, noColor, ascii, view)
		},
	}
	cmd.Flags().StringVar(&date, "date", "", "local day to show, YYYY-MM-DD (default: today)")
	cmd.Flags().BoolVar(&noColor, "no-color", false, "never use colours")
	cmd.Flags().BoolVar(&ascii, "ascii", false, "use plain ASCII characters only")
	cmd.Flags().StringSliceVar(&categoryArg, "category", nil, "only these categories, comma list of\nvitals, activity, body, mobility,\nother, sleep, workouts")
	cmd.Flags().BoolVar(&compact, "compact", false, "no blank lines between sections;\ngutter is one flag (! + ~)")
	return cmd
}

func runToday(cmd *cobra.Command, opts *options, date string, noColor, ascii bool, view overviewView) error {
	now := nowFunc()
	if date == "" {
		date = now.Format(stats.DayLayout)
	} else if err := checkDateFlag(date); err != nil {
		return err
	} else if err := checkNotFuture(date, now); err != nil {
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
	if report, err = report.restrict(view); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if opts.jsonOutput {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	view.now = now
	ti := detectTerm(out, os.Getenv, noColor, ascii)
	_, err = fmt.Fprint(out, renderToday(report, ti, view))
	return err
}

// checkDateFlag validates --date: YYYY-MM-DD. Years are limited to
// 1900..2999: nothing real lies outside, and day arithmetic at the ends of the
// calendar (year 0, year 10000) is not worth supporting.
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

// checkNotFuture rejects a day after now's local day. "healthcli today" has
// nothing to say about the future; "trend" can still end there.
func checkNotFuture(date string, now time.Time) error {
	if today := now.Format(stats.DayLayout); date > today { // "YYYY-MM-DD" strings sort like dates
		return fmt.Errorf("bad --date %q: that day is in the future (today is %s)", date, today)
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

func (r *todayReport) isEmpty() bool { return r.day == nil || r.day.Empty() }

// collectDay reads one local day (stats.BuildDay) and shapes it for --json.
func collectDay(ctx context.Context, conn *sql.DB, day string) (*todayReport, error) {
	d, err := stats.BuildDay(ctx, conn, day)
	if err != nil {
		return nil, err
	}
	return shapeDay(d)
}

// shapeDay fills the --json fields of a report from the day's data.
func shapeDay(d *stats.DayReport) (*todayReport, error) {
	r := &todayReport{
		Date: d.Day, day: d,
		Metrics: []todayMetric{}, Workouts: []todayWorkout{},
		Categories: d.Categories, TrackedWithoutReading: d.NoReading,
	}
	for _, row := range d.Metrics {
		m, err := metricJSON(row, d.Day)
		if err != nil {
			return nil, err
		}
		r.Metrics = append(r.Metrics, m)
	}
	if d.Sleep != nil {
		n := d.Sleep.Last
		r.SleepSummary = d.Sleep
		r.Sleep = &todaySleep{
			Night: n.Night, Source: n.Source,
			AsleepSeconds: seconds(n.Asleep), CoreSeconds: seconds(n.Core),
			DeepSeconds: seconds(n.Deep), REMSeconds: seconds(n.REM),
			UnspecifiedSeconds: seconds(n.Unspecified), AwakeSeconds: seconds(n.Awake),
			InBedSeconds: seconds(n.InBed),
		}
	}
	for _, w := range d.Workouts {
		r.Workouts = append(r.Workouts, todayWorkout{
			Activity: w.Activity, Start: w.Start.Format(time.RFC3339),
			DurationSeconds: seconds(w.Duration), EnergyKcal: w.EnergyKcal,
			DistanceM: w.DistanceM, Source: w.Source,
		})
	}
	return r, nil
}

// restrict keeps only what --category names, in the text and in --json alike:
// the metric rows and band counts, the "no reading" footer and list, and the
// sleep and workouts parts. It runs after the fallback decision, which looks at
// the whole day.
func (r *todayReport) restrict(v overviewView) (*todayReport, error) {
	if len(v.categories) == 0 || r.day == nil {
		return r, nil
	}
	d := *r.day
	d.Metrics, d.NoReading = []stats.MetricRow{}, []string{}
	for _, row := range r.day.Metrics {
		if v.wants(row.Category) {
			d.Metrics = append(d.Metrics, row)
		}
	}
	d.Categories = stats.CategorySummaries(d.Metrics)
	for _, name := range r.day.NoReading {
		if m, err := metrics.Lookup(name); err == nil && v.wants(string(m.Category)) {
			d.NoReading = append(d.NoReading, name)
		}
	}
	if !v.wants(sleepName) {
		d.Sleep = nil
	}
	if !v.wants(workoutsName) {
		d.Workouts = []stats.Workout{}
	}
	out, err := shapeDay(&d)
	if err != nil {
		return nil, err
	}
	out.Status, out.RequestedDate = r.Status, r.RequestedDate
	return out, nil
}

func seconds(d time.Duration) int64 { return int64(d / time.Second) }

// jsonSafe makes an overflowed daily value encodable: JSON has no Infinity, so
// it becomes the largest float (the text shows it in the huge-number format).
// NaN has no sensible number and becomes null.
func jsonSafe(v *float64) *float64 {
	if v == nil {
		return nil
	}
	switch {
	case math.IsNaN(*v):
		return nil
	case math.IsInf(*v, 1):
		m := math.MaxFloat64
		return &m
	case math.IsInf(*v, -1):
		m := -math.MaxFloat64
		return &m
	}
	return v
}

// metricJSON turns a day's metric row into the --json shape. "Not enough
// data" is an expected state, not an error: baseline.available is false.
func metricJSON(row stats.MetricRow, day string) (todayMetric, error) {
	from, err := stats.AddDays(day, -stats.DefaultBaselineDays)
	if err != nil {
		return todayMetric{}, err
	}
	to, err := stats.AddDays(day, -1)
	if err != nil {
		return todayMetric{}, err
	}
	m := todayMetric{
		Metric: row.Metric, Label: row.Label, Unit: row.Unit, Value: jsonSafe(row.Value), Source: row.Source,
		Category: row.Category, Polarity: row.Polarity, State: row.State, Verdict: row.Verdict, Usual: row.Mean,
		Baseline: &baselineInfo{DaysWithData: row.BaselineDays, DaysNeeded: stats.MinBaselineDays, From: from, To: to},
	}
	if row.Mean != nil && row.Stddev != nil && row.Value != nil {
		m.Baseline.Available = true
		m.Baseline.Mean, m.Baseline.Stddev = row.Mean, row.Stddev
		// The raw comparison, before the "too small to matter" rule that the
		// verdict applies.
		c := stats.Compare(*row.Value, stats.Baseline{Mean: *row.Mean, Stddev: *row.Stddev})
		m.Comparison = &comparisonInfo{Delta: c.Delta, Z: c.Z, Label: c.Label}
		if c.HasPct {
			m.Comparison.DeltaPct = &c.DeltaPct
		}
	}
	return m, nil
}

// latestDataDay finds the newest local day with any data: the newest
// sample of any metric, workout or night of sleep. Like the overview, it
// ignores anything starting more than a day after now. found is false for an
// empty database.
func latestDataDay(ctx context.Context, conn *sql.DB) (day string, found bool, err error) {
	maxStart := nowFunc().Add(24 * time.Hour).Unix() // stats.futureSlack
	consider := func(d string) {
		// "YYYY-MM-DD" strings sort in date order, so > compares dates.
		if d > day {
			day, found = d, true
		}
	}
	for _, name := range metrics.Names() {
		s, err := stats.LatestSampleUpTo(ctx, conn, name, maxStart)
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
		`SELECT max(date(start_ts + tz_offset, 'unixepoch')) FROM workouts WHERE start_ts <= ?`,
		`SELECT max(date(end_ts + tz_offset + 21600, 'unixepoch')) FROM sleep_stages WHERE start_ts <= ?`,
	} {
		var d sql.NullString // NULL when the table is empty
		if err := conn.QueryRowContext(ctx, q, maxStart).Scan(&d); err != nil {
			return "", false, fmt.Errorf("find latest day: %w", err)
		}
		if d.Valid {
			consider(d.String)
		}
	}
	return day, found, nil
}
